package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrAutoRenewNotPlan = errors.New("只有订阅套餐可以设置自动续订")

// SetPlanAutoRenew changes the setting for the whole renewal line, not merely
// the clicked bucket. Queued purchases inherit the same choice and therefore
// cannot silently re-enable a line the user has already turned off.
func (s *Store) SetPlanAutoRenew(userID, bucketID int64, enabled bool) error {
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var kind, queueKey string
	var packageID int64
	if err := tx.QueryRow(`SELECT kind, package_id, queue_key FROM user_plans WHERE id=? AND user_id=?`, bucketID, userID).
		Scan(&kind, &packageID, &queueKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBucketNotFound
		}
		return err
	}
	if kind != "plan" || packageID <= 0 {
		return ErrAutoRenewNotPlan
	}
	if queueKey == "" {
		queueKey = effectiveQueueKey(packageID, "")
	}
	if _, err := tx.Exec(`UPDATE user_plans SET auto_renew=?, updated_at=?
		WHERE user_id=? AND kind='plan' AND queue_key=? AND status<>?`, enabled, now, userID, queueKey, StatusRetired); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// AutoRenewDuePlans renews plan lines that reached their configured expiry with
// no already-purchased queued segment. It charges the package's current price
// for the same duration, then uses the normal bucket/queue transaction so the
// replacement starts immediately and the prior segment becomes retired.
//
// Expected business conditions (insufficient points, package off sale, no
// matching duration or stock) leave the toggle enabled and are retried later;
// they are not database failures and do not prevent other users from renewing.
func (s *Store) AutoRenewDuePlans() ([]int64, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT p.user_id, p.queue_key FROM user_plans p
		WHERE p.kind='plan' AND p.status='active' AND p.auto_renew=1
		  AND p.expiry_at>0 AND p.expiry_at<=?
		  AND NOT EXISTS (SELECT 1 FROM user_plans q
		      WHERE q.user_id=p.user_id AND q.kind='plan' AND q.queue_key=p.queue_key AND q.status='queued')
		GROUP BY p.user_id, p.queue_key ORDER BY p.user_id, p.queue_key`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type renewalLine struct {
		userID   int64
		queueKey string
	}
	var lines []renewalLine
	for rows.Next() {
		var line renewalLine
		if err := rows.Scan(&line.userID, &line.queueKey); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	changed := make([]int64, 0, len(lines))
	seen := map[int64]bool{}
	var firstErr error
	for _, line := range lines {
		renewed, err := s.autoRenewLine(line.userID, line.queueKey, now)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("user %d line %q: %w", line.userID, line.queueKey, err)
			}
			continue
		}
		if renewed && !seen[line.userID] {
			seen[line.userID] = true
			changed = append(changed, line.userID)
		}
	}
	return changed, firstErr
}

func (s *Store) autoRenewLine(userID int64, queueKey string, now int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var bucketID, packageID, durationDays int64
	err = tx.QueryRow(`SELECT id, package_id, duration_days FROM user_plans
		WHERE user_id=? AND kind='plan' AND queue_key=? AND status='active' AND auto_renew=1
		  AND expiry_at>0 AND expiry_at<=?
		ORDER BY id DESC LIMIT 1`, userID, queueKey, now).
		Scan(&bucketID, &packageID, &durationDays)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var queued int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_plans
		WHERE user_id=? AND kind='plan' AND queue_key=? AND status='queued'`, userID, queueKey).Scan(&queued); err != nil {
		return false, err
	}
	if queued > 0 {
		return false, nil
	}

	u, err := scanUser(tx.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, userID))
	if err != nil {
		return false, err
	}
	if u == nil {
		return false, nil
	}
	fresh, err := scanPackage(tx.QueryRow(`SELECT `+pkgCols+` FROM packages WHERE id=?`, packageID))
	if err != nil {
		return false, err
	}
	if fresh == nil || !fresh.Enabled || fresh.Stock == 0 || fresh.Type != "plan" {
		return false, nil
	}
	pkg, err := fresh.forDuration(durationDays)
	if errors.Is(err, ErrOptionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pkg.TrafficBytes <= 0 || u.Points < pkg.PricePoints {
		return false, nil
	}
	allowed, err := canBuyPackageTx(tx, userID, pkg.ID)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, nil
	}

	newPoints := u.Points - pkg.PricePoints
	if _, err = tx.Exec(`UPDATE users SET points=?, updated_at=? WHERE id=?`, newPoints, now, userID); err != nil {
		return false, err
	}
	snap, _ := json.Marshal(pkg)
	res, err := tx.Exec(`INSERT INTO orders (user_id, package_id, package_snapshot, price_points, status, created_at)
		VALUES (?,?,?,?, 'success', ?)`, userID, pkg.ID, string(snap), pkg.PricePoints, now)
	if err != nil {
		return false, err
	}
	orderID, err := res.LastInsertId()
	if err != nil {
		return false, err
	}
	if err = enqueuePlanBucket(tx, userID, u.Username, pkg, orderID, now); err != nil {
		return false, err
	}
	if _, err = advanceUserQueues(tx, userID, now); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`INSERT INTO point_transactions
		(user_id, amount, type, balance_after, ref_id, note, operator_id, created_at)
		VALUES (?,?, 'auto_renew', ?, ?, ?, 0, ?)`,
		userID, -pkg.PricePoints, newPoints, orderID, "自动续订: "+pkg.Name, now); err != nil {
		return false, err
	}
	newTraffic, newUp, newDown, newExpiry, err := recomputeUserAggregate(tx, userID, now)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE users SET current_plan_id=?, traffic_limit=?, used_up=?, used_down=?, expiry_at=?, updated_at=? WHERE id=?`,
		pkg.ID, newTraffic, newUp, newDown, newExpiry, now, userID); err != nil {
		return false, err
	}
	if pkg.Stock > 0 {
		res, err = tx.Exec(`UPDATE packages SET stock=stock-1 WHERE id=? AND stock>0`, pkg.ID)
		if err != nil {
			return false, err
		}
		if affected, _ := res.RowsAffected(); affected != 1 {
			return false, ErrOutOfStock
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	_ = bucketID
	return true, nil
}
