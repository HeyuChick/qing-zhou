package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrPackageDisabled = errors.New("商品已下架")
	ErrOutOfStock      = errors.New("商品库存不足")
	ErrUnknownPkgType  = errors.New("未知商品类型")
	ErrOrderNotFound   = errors.New("订单不存在")
	ErrAlreadyRefunded = errors.New("该订单已退款")
)

type Order struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"user_id"`
	Username    string `json:"username,omitempty"` // filled by admin queries only
	PackageID   int64  `json:"package_id"`
	Name        string `json:"name"` // from snapshot (survives package deletion)
	Type        string `json:"type"`
	PricePoints int64  `json:"price_points"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
}

type PurchaseResult struct {
	Order *Order
	User  *User
}

// Purchase runs the full buy transaction atomically: validate funds/stock,
// deduct points, write the ledger row and order, apply the entitlement change,
// then (for node-affecting packages) call sync to push to sing-box INSIDE the
// transaction. If sync fails, the whole transaction rolls back — no points are
// lost and the user's quota is unchanged.
//
// sync receives the updated user snapshot and whether traffic counters should
// be reset (expired-plan renewal).
func (s *Store) Purchase(userID int64, pkg *Package, sync func(updated *User, resetUsed bool) error) (*PurchaseResult, error) {
	if !pkg.Enabled {
		return nil, ErrPackageDisabled
	}
	if pkg.Stock == 0 {
		return nil, ErrOutOfStock
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	u, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, userID))
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}
	if u.Points < pkg.PricePoints {
		return nil, ErrInsufficientFunds
	}

	now := time.Now().Unix()
	resetUsed := false
	setPlan := false
	newTraffic := u.TrafficLimit
	newExpiry := u.ExpiryAt

	switch pkg.Type {
	case "traffic":
		newTraffic += pkg.TrafficBytes
	case "plan":
		if u.ExpiryAt > now { // active: stack remaining traffic, extend expiry
			newExpiry = u.ExpiryAt + pkg.DurationDays*86400
			newTraffic = u.TrafficLimit + pkg.TrafficBytes
		} else { // expired: forfeit old traffic, start fresh
			newExpiry = now + pkg.DurationDays*86400
			newTraffic = pkg.TrafficBytes
			resetUsed = true
		}
		setPlan = true
	default:
		return nil, ErrUnknownPkgType
	}

	// Deduct points.
	newPoints := u.Points - pkg.PricePoints
	if _, err = tx.Exec(`UPDATE users SET points=?, updated_at=? WHERE id=?`, newPoints, now, userID); err != nil {
		return nil, err
	}

	// Order (with package snapshot).
	snap, _ := json.Marshal(pkg)
	res, err := tx.Exec(`INSERT INTO orders (user_id, package_id, package_snapshot, price_points, status, created_at)
		VALUES (?,?,?,?, 'success', ?)`, userID, pkg.ID, string(snap), pkg.PricePoints, now)
	if err != nil {
		return nil, err
	}
	orderID, _ := res.LastInsertId()

	// Bucket model (per-plan independent metering): a plan purchase mints a fresh
	// independently-metered bucket with its own identity; a traffic purchase tops
	// up the shared pool. The legacy users.* fields above are kept as a rough
	// aggregate for back-compat; buckets are authoritative for enforcement.
	if pkg.Type == "plan" {
		if err = insertPlanBucket(tx, userID, u.Username, pkg, orderID, now); err != nil {
			return nil, err
		}
	} else if pkg.Type == "traffic" {
		if err = addToPool(tx, userID, u.Username, pkg.TrafficBytes, now); err != nil {
			return nil, err
		}
	}

	// Ledger.
	if _, err = tx.Exec(`INSERT INTO point_transactions
		(user_id, amount, type, balance_after, ref_id, note, operator_id, created_at)
		VALUES (?,?, 'purchase', ?, ?, ?, 0, ?)`,
		userID, -pkg.PricePoints, newPoints, orderID, "购买: "+pkg.Name, now); err != nil {
		return nil, err
	}

	// Apply entitlement (traffic + expiry; plan also sets current plan).
	if resetUsed {
		_, err = tx.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, used_up=0, used_down=0, updated_at=? WHERE id=?`,
			newTraffic, newExpiry, now, userID)
	} else {
		_, err = tx.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, updated_at=? WHERE id=?`,
			newTraffic, newExpiry, now, userID)
	}
	if err != nil {
		return nil, err
	}
	if setPlan {
		if _, err = tx.Exec(`UPDATE users SET current_plan_id=? WHERE id=?`, pkg.ID, userID); err != nil {
			return nil, err
		}
	}

	// Decrement stock if limited.
	if pkg.Stock > 0 {
		if _, err = tx.Exec(`UPDATE packages SET stock=stock-1 WHERE id=? AND stock>0`, pkg.ID); err != nil {
			return nil, err
		}
	}

	// Build the updated snapshot for the sync callback.
	updated := *u
	updated.Points = newPoints
	updated.TrafficLimit = newTraffic
	updated.ExpiryAt = newExpiry
	if resetUsed {
		updated.UsedUp, updated.UsedDown = 0, 0
	}
	if setPlan {
		updated.CurrentPlanID = sql.NullInt64{Int64: pkg.ID, Valid: true}
	}

	// External sync inside the tx.
	if sync != nil {
		if err = sync(&updated, resetUsed); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true

	return &PurchaseResult{
		Order: &Order{ID: orderID, UserID: userID, PackageID: pkg.ID, PricePoints: pkg.PricePoints, Status: "success", CreatedAt: now},
		User:  &updated,
	}, nil
}

// AssignPackage grants a package to a user WITHOUT charging points — an admin
// comp/manual activation. It applies the same entitlement change as Purchase
// (traffic + expiry, and current plan for "plan" packages), records a 0-price
// order for audit, and runs sync inside the transaction so a sync failure rolls
// the whole thing back. Package enabled/stock are ignored (admin override).
func (s *Store) AssignPackage(userID int64, pkg *Package, operatorID int64, sync func(updated *User, resetUsed bool) error) (*PurchaseResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	u, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, userID))
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}

	now := time.Now().Unix()
	resetUsed := false
	setPlan := false
	newTraffic := u.TrafficLimit
	newExpiry := u.ExpiryAt

	switch pkg.Type {
	case "traffic":
		newTraffic += pkg.TrafficBytes
	case "plan":
		if u.ExpiryAt > now { // active: stack remaining traffic, extend expiry
			newExpiry = u.ExpiryAt + pkg.DurationDays*86400
			newTraffic = u.TrafficLimit + pkg.TrafficBytes
		} else { // expired: forfeit old traffic, start fresh
			newExpiry = now + pkg.DurationDays*86400
			newTraffic = pkg.TrafficBytes
			resetUsed = true
		}
		setPlan = true
	default:
		return nil, ErrUnknownPkgType
	}

	// Order (with package snapshot), price 0 = admin grant.
	snap, _ := json.Marshal(pkg)
	res, err := tx.Exec(`INSERT INTO orders (user_id, package_id, package_snapshot, price_points, status, created_at)
		VALUES (?,?,?,0, 'success', ?)`, userID, pkg.ID, string(snap), now)
	if err != nil {
		return nil, err
	}
	orderID, _ := res.LastInsertId()

	// Bucket model (same as Purchase): plan → new metered bucket, traffic → pool.
	if pkg.Type == "plan" {
		if err = insertPlanBucket(tx, userID, u.Username, pkg, orderID, now); err != nil {
			return nil, err
		}
	} else if pkg.Type == "traffic" {
		if err = addToPool(tx, userID, u.Username, pkg.TrafficBytes, now); err != nil {
			return nil, err
		}
	}

	// Ledger note (0 points) for audit trail of the manual activation.
	if _, err = tx.Exec(`INSERT INTO point_transactions
		(user_id, amount, type, balance_after, ref_id, note, operator_id, created_at)
		VALUES (?,0, 'admin_grant', ?, ?, ?, ?, ?)`,
		userID, u.Points, orderID, "管理员开通: "+pkg.Name, operatorID, now); err != nil {
		return nil, err
	}

	if resetUsed {
		_, err = tx.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, used_up=0, used_down=0, updated_at=? WHERE id=?`,
			newTraffic, newExpiry, now, userID)
	} else {
		_, err = tx.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, updated_at=? WHERE id=?`,
			newTraffic, newExpiry, now, userID)
	}
	if err != nil {
		return nil, err
	}
	if setPlan {
		if _, err = tx.Exec(`UPDATE users SET current_plan_id=? WHERE id=?`, pkg.ID, userID); err != nil {
			return nil, err
		}
	}

	updated := *u
	updated.TrafficLimit = newTraffic
	updated.ExpiryAt = newExpiry
	if resetUsed {
		updated.UsedUp, updated.UsedDown = 0, 0
	}
	if setPlan {
		updated.CurrentPlanID = sql.NullInt64{Int64: pkg.ID, Valid: true}
	}

	if sync != nil {
		if err = sync(&updated, resetUsed); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true

	return &PurchaseResult{
		Order: &Order{ID: orderID, UserID: userID, PackageID: pkg.ID, PricePoints: 0, Status: "success", CreatedAt: now},
		User:  &updated,
	}, nil
}

// GetOrder loads a single order with its snapshot fields decoded.
func (s *Store) GetOrder(id int64) (*Order, error) {
	var o Order
	var snap string
	err := s.db.QueryRow(`SELECT id, user_id, package_id, package_snapshot, price_points, status, created_at
		FROM orders WHERE id=?`, id).Scan(&o.ID, &o.UserID, &o.PackageID, &snap, &o.PricePoints, &o.Status, &o.CreatedAt)
	if err != nil {
		return nil, nil // not found
	}
	var sp struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(snap), &sp) == nil {
		o.Name, o.Type = sp.Name, sp.Type
	}
	return &o, nil
}

// UserExists reports whether a user id still exists (used to detect orphaned
// orders left behind by a deleted account).
func (s *Store) UserExists(id int64) bool {
	var n int
	_ = s.db.QueryRow(`SELECT 1 FROM users WHERE id=?`, id).Scan(&n)
	return n == 1
}

// DeleteOrder permanently removes a single order record. Intended for cleaning
// up orphaned orders whose user has been deleted; the entitlement was already
// consumed, so this only drops the historical row.
func (s *Store) DeleteOrder(id int64) error {
	_, err := s.db.Exec(`DELETE FROM orders WHERE id=?`, id)
	return err
}

// RefundOrder reverses a successful purchase: refunds the points spent, undoes
// the entitlement this order granted (traffic + plan duration, clamped so they
// never go negative), marks the order 'refunded' (data is kept, not deleted),
// and pushes the new entitlement to sing-box inside the transaction. Idempotent
// guard: an already-refunded order returns ErrAlreadyRefunded.
func (s *Store) RefundOrder(orderID, operatorID int64, sync func(updated *User, resetUsed bool) error) (*User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var (
		userID, price, createdAt int64
		snap, status             string
	)
	err = tx.QueryRow(`SELECT user_id, package_snapshot, price_points, status, created_at
		FROM orders WHERE id=?`, orderID).Scan(&userID, &snap, &price, &status, &createdAt)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if status == "refunded" {
		return nil, ErrAlreadyRefunded
	}

	var sp struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		TrafficBytes int64  `json:"traffic_bytes"`
		DurationDays int64  `json:"duration_days"`
	}
	_ = json.Unmarshal([]byte(snap), &sp)

	u, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, userID))
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}

	now := time.Now().Unix()
	// Reverse the entitlement this order granted, clamped to safe minimums.
	newTraffic := u.TrafficLimit - sp.TrafficBytes
	if newTraffic < 0 {
		newTraffic = 0
	}
	newExpiry := u.ExpiryAt
	if sp.Type == "plan" && sp.DurationDays > 0 {
		newExpiry = u.ExpiryAt - sp.DurationDays*86400
		if newExpiry < now {
			newExpiry = now // becomes expired, not negative
		}
	}
	if _, err = tx.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, updated_at=? WHERE id=?`,
		newTraffic, newExpiry, now, userID); err != nil {
		return nil, err
	}

	// Refund points (if any were charged) and write the ledger row.
	newPoints := u.Points + price
	if price != 0 {
		if _, err = tx.Exec(`UPDATE users SET points=? WHERE id=?`, newPoints, userID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO point_transactions
			(user_id, amount, type, balance_after, ref_id, note, operator_id, created_at)
			VALUES (?,?, 'refund', ?, ?, ?, ?, ?)`,
			userID, price, newPoints, orderID, "退款: "+sp.Name, operatorID, now); err != nil {
			return nil, err
		}
	}

	// Bucket model: drop the plan bucket this order created, or claw the traffic
	// top-up back out of the pool (clamped to what's already used).
	if sp.Type == "plan" {
		if err = removeBucketByOrder(tx, orderID); err != nil {
			return nil, err
		}
	} else if sp.Type == "traffic" {
		if err = subFromPool(tx, userID, sp.TrafficBytes, now); err != nil {
			return nil, err
		}
	}

	if _, err = tx.Exec(`UPDATE orders SET status='refunded' WHERE id=?`, orderID); err != nil {
		return nil, err
	}

	updated := *u
	updated.Points = newPoints
	updated.TrafficLimit = newTraffic
	updated.ExpiryAt = newExpiry

	if sync != nil {
		if err = sync(&updated, false); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &updated, nil
}

// ListOrdersAdmin returns recent orders across all users, joined with the
// username, optionally filtered by a username/order-id search term.
func (s *Store) ListOrdersAdmin(q string, limit int) ([]*Order, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows *sql.Rows
	var err error
	base := `SELECT o.id, o.user_id, COALESCE(u.username,''), o.package_id, o.package_snapshot, o.price_points, o.status, o.created_at
		FROM orders o LEFT JOIN users u ON u.id=o.user_id`
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + q + "%"
		rows, err = s.db.Query(base+` WHERE u.username LIKE ? ORDER BY o.id DESC LIMIT ?`, like, limit)
	} else {
		rows, err = s.db.Query(base+` ORDER BY o.id DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Order{}
	for rows.Next() {
		var o Order
		var snap string
		if err := rows.Scan(&o.ID, &o.UserID, &o.Username, &o.PackageID, &snap, &o.PricePoints, &o.Status, &o.CreatedAt); err != nil {
			return nil, err
		}
		var sp struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(snap), &sp) == nil {
			o.Name, o.Type = sp.Name, sp.Type
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

// ListOrders returns a user's orders, or all orders when userID == 0 (admin).
func (s *Store) ListOrders(userID int64, limit int) ([]*Order, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if userID == 0 {
		rows, err = s.db.Query(`SELECT id, user_id, package_id, package_snapshot, price_points, status, created_at
			FROM orders ORDER BY id DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.Query(`SELECT id, user_id, package_id, package_snapshot, price_points, status, created_at
			FROM orders WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		var o Order
		var snap string
		if err := rows.Scan(&o.ID, &o.UserID, &o.PackageID, &snap, &o.PricePoints, &o.Status, &o.CreatedAt); err != nil {
			return nil, err
		}
		var sp struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(snap), &sp) == nil {
			o.Name, o.Type = sp.Name, sp.Type
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}
