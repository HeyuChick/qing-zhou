package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// execer / txLike are satisfied by both *sql.DB and *sql.Tx, so bucket ops work
// standalone (migration) or inside a purchase transaction (which must read its
// own uncommitted writes — the pool-bucket lookup — on the same connection).
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}
type txLike interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// insertPlanBucket creates a fresh, independently-metered plan bucket with its
// own sing-box identity (qz_<user>_p<order>). Called inside the purchase tx.
func insertPlanBucket(ex execer, userID int64, username string, pkg *Package, orderID, now int64) error {
	uu, ss := genBucketCreds()
	_, err := insertBucket(ex, &Bucket{
		UserID: userID, Kind: "plan", PackageID: pkg.ID, Name: pkg.Name,
		ClientName:   fmt.Sprintf("qz_%s_p%d", username, orderID),
		ClientUUID:   uu, ClientSecret: ss,
		TrafficLimit: pkg.TrafficBytes, ExpiryAt: now + pkg.DurationDays*86400,
		OrderID: orderID, CreatedAt: now,
	})
	return err
}

// upsertPlanBucket renews the user's existing bucket for this package if one
// exists, otherwise creates a fresh one. Renewal is the correct model for
// repurchasing the SAME plan: it keeps a single metered identity and stacks —
// traffic quota is added and expiry extended from whichever is later (now or the
// current expiry, so an expired plan renews from now, not the past). Buying a
// DIFFERENT plan still mints its own independent bucket. This is what makes a
// repeat purchase show as one renewed row, not many duplicates.
func upsertPlanBucket(tx txLike, userID int64, username string, pkg *Package, orderID, now int64) error {
	var id, limit, expiry int64
	err := tx.QueryRow(`SELECT id, traffic_limit, expiry_at FROM user_plans
		WHERE user_id=? AND kind='plan' AND package_id=? ORDER BY id LIMIT 1`, userID, pkg.ID).Scan(&id, &limit, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return insertPlanBucket(tx, userID, username, pkg, orderID, now)
	}
	if err != nil {
		return err
	}
	base := now
	if expiry > now {
		base = expiry // still active: extend from current expiry
	}
	newExpiry := base + pkg.DurationDays*86400
	newLimit := limit + pkg.TrafficBytes
	_, err = tx.Exec(`UPDATE user_plans SET traffic_limit=?, expiry_at=?, order_id=?, updated_at=? WHERE id=?`,
		newLimit, newExpiry, orderID, now, id)
	return err
}

// reversePlanBucket undoes one plan order's contribution to its package bucket
// (used on refund): it subtracts the package's traffic quota (clamped so the
// limit never drops below what's already used) and one duration period from the
// expiry. If nothing is left (no remaining quota and expired), the bucket is
// removed; otherwise it survives with the reduced allowances — correct whether
// the bucket held a single purchase or several stacked renewals.
func reversePlanBucket(tx txLike, userID, packageID, trafficBytes, durationDays, now int64) error {
	var id, limit, used, expiry int64
	err := tx.QueryRow(`SELECT id, traffic_limit, used_up+used_down, expiry_at FROM user_plans
		WHERE user_id=? AND kind='plan' AND package_id=? ORDER BY id LIMIT 1`, userID, packageID).Scan(&id, &limit, &used, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	newLimit := limit - trafficBytes
	if newLimit < used {
		newLimit = used
	}
	if newLimit < 0 {
		newLimit = 0
	}
	newExpiry := expiry - durationDays*86400
	if newLimit <= used && newExpiry <= now {
		_, err = tx.Exec(`DELETE FROM user_plans WHERE id=?`, id)
		return err
	}
	_, err = tx.Exec(`UPDATE user_plans SET traffic_limit=?, expiry_at=?, updated_at=? WHERE id=?`, newLimit, newExpiry, now, id)
	return err
}

// addToPool tops up the user's traffic-package pool, creating it if absent.
func addToPool(tx txLike, userID int64, username string, addBytes, now int64) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind='pool' ORDER BY id LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		uu, ss := genBucketCreds()
		_, err = insertBucket(tx, &Bucket{
			UserID: userID, Kind: "pool", Name: "通用流量",
			ClientName: fmt.Sprintf("qz_%s_pool", username), ClientUUID: uu, ClientSecret: ss,
			TrafficLimit: addBytes, CreatedAt: now,
		})
		return err
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE user_plans SET traffic_limit=traffic_limit+?, updated_at=? WHERE id=?`, addBytes, now, id)
	return err
}

// subFromPool reverses a traffic-package top-up (refund), clamped so the pool
// limit never drops below what's already been used, nor below zero.
func subFromPool(tx txLike, userID int64, subBytes, now int64) error {
	var id, limit, used int64
	err := tx.QueryRow(`SELECT id, traffic_limit, used_up+used_down FROM user_plans WHERE user_id=? AND kind='pool' ORDER BY id LIMIT 1`, userID).Scan(&id, &limit, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	nl := limit - subBytes
	if nl < used {
		nl = used
	}
	if nl < 0 {
		nl = 0
	}
	_, err = tx.Exec(`UPDATE user_plans SET traffic_limit=?, updated_at=? WHERE id=?`, nl, now, id)
	return err
}

// recomputeUserAggregate rebuilds the legacy users.* aggregate columns from the
// authoritative buckets after a bucket change (e.g. a refund), so the dashboard
// totals stay consistent with what enforcement actually sees. traffic_limit /
// used_up / used_down are summed across all buckets; expiry_at is the latest
// plan-bucket expiry (0 when the user holds only the never-expiring pool).
func recomputeUserAggregate(tx txLike, userID, now int64) (limit, up, down, expiry int64, err error) {
	err = tx.QueryRow(`SELECT
		COALESCE(SUM(traffic_limit),0),
		COALESCE(SUM(used_up),0),
		COALESCE(SUM(used_down),0),
		COALESCE(MAX(CASE WHEN kind='plan' THEN expiry_at ELSE 0 END),0)
		FROM user_plans WHERE user_id=?`, userID).Scan(&limit, &up, &down, &expiry)
	if err != nil {
		return
	}
	_, err = tx.Exec(`UPDATE users SET traffic_limit=?, used_up=?, used_down=?, expiry_at=?, updated_at=? WHERE id=?`,
		limit, up, down, expiry, now, userID)
	return
}

// EnsurePoolBucket creates the user's pool bucket with the given identity if it
// has none yet (called when a new user is provisioned).
func (s *Store) EnsurePoolBucket(userID int64, name, clientUUID, clientSecret string) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind='pool' ORDER BY id LIMIT 1`, userID).Scan(&id)
	if err == nil {
		return nil // already has one
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = insertBucket(s.db, &Bucket{
		UserID: userID, Kind: "pool", Name: "通用流量",
		ClientName: name, ClientUUID: clientUUID, ClientSecret: clientSecret,
	})
	return err
}

// Bucket is an independently-metered unit a user holds: a purchased plan
// (Kind="plan") or the shared traffic-package pool (Kind="pool"). Each carries
// its own sing-box identity so per-identity stats give per-bucket usage.
type Bucket struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	Kind         string `json:"kind"` // plan | pool
	PackageID    int64  `json:"package_id"`
	Name         string `json:"name"`
	ClientName   string `json:"-"`
	ClientUUID   string `json:"-"`
	ClientSecret string `json:"-"`
	TrafficLimit int64  `json:"traffic_limit"`
	UsedUp       int64  `json:"used_up"`
	UsedDown     int64  `json:"used_down"`
	ExpiryAt     int64  `json:"expiry_at"`
	LastOnlineAt int64  `json:"last_online_at"`
	OrderID      int64  `json:"order_id"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Used is the bucket's total consumed bytes.
func (b *Bucket) Used() int64 { return b.UsedUp + b.UsedDown }

// HasQuota reports whether the bucket has traffic left (limit 0 = unlimited).
func (b *Bucket) HasQuota() bool { return b.TrafficLimit == 0 || b.Used() < b.TrafficLimit }

// NotExpired reports whether the bucket is still within its time window.
func (b *Bucket) NotExpired(now int64) bool { return b.ExpiryAt == 0 || b.ExpiryAt > now }

// Active reports whether the bucket can currently carry traffic. A pool is only
// active when it has a positive, non-exhausted balance (an empty pool is inert);
// a plan is active while not expired and not over quota.
func (b *Bucket) Active(now int64) bool {
	if b.Kind == "pool" {
		return b.TrafficLimit > 0 && b.Used() < b.TrafficLimit && b.NotExpired(now)
	}
	return b.NotExpired(now) && b.HasQuota()
}

const bucketCols = `id, user_id, kind, package_id, name, client_name, client_uuid, client_secret,
	traffic_limit, used_up, used_down, expiry_at, last_online_at, order_id, created_at, updated_at`

func scanBucket(sc scanner) (*Bucket, error) {
	var b Bucket
	err := sc.Scan(&b.ID, &b.UserID, &b.Kind, &b.PackageID, &b.Name, &b.ClientName, &b.ClientUUID,
		&b.ClientSecret, &b.TrafficLimit, &b.UsedUp, &b.UsedDown, &b.ExpiryAt, &b.LastOnlineAt,
		&b.OrderID, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBuckets returns all of a user's buckets (plans + pool), oldest first.
func (s *Store) ListBuckets(userID int64) ([]*Bucket, error) {
	rows, err := s.db.Query(`SELECT `+bucketCols+` FROM user_plans WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Bucket
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BucketByClientName resolves a sing-box stats identity to its bucket.
func (s *Store) BucketByClientName(name string) (*Bucket, error) {
	return scanBucket(s.db.QueryRow(`SELECT `+bucketCols+` FROM user_plans WHERE client_name=?`, name))
}

// PoolBucket returns the user's traffic-package pool bucket (or nil).
func (s *Store) PoolBucket(userID int64) (*Bucket, error) {
	return scanBucket(s.db.QueryRow(`SELECT `+bucketCols+` FROM user_plans WHERE user_id=? AND kind='pool' ORDER BY id LIMIT 1`, userID))
}

// genBucketCreds mints a fresh sing-box identity (mirrors idgen.NewCredentials).
func genBucketCreds() (uuidStr, secret string) {
	id, _ := uuid.NewRandom()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return id.String(), hex.EncodeToString(b)
}

// insertBucket writes a bucket row via the given execer and returns its id.
func insertBucket(ex execer, b *Bucket) (int64, error) {
	now := time.Now().Unix()
	if b.CreatedAt == 0 {
		b.CreatedAt = now
	}
	res, err := ex.Exec(`INSERT INTO user_plans
		(user_id, kind, package_id, name, client_name, client_uuid, client_secret,
		 traffic_limit, used_up, used_down, expiry_at, last_online_at, order_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.UserID, b.Kind, b.PackageID, b.Name, b.ClientName, b.ClientUUID, b.ClientSecret,
		b.TrafficLimit, b.UsedUp, b.UsedDown, b.ExpiryAt, b.LastOnlineAt, b.OrderID, b.CreatedAt, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddBucketUsage applies a sing-box stats delta to the matching bucket, and
// mirrors it onto the owning user (aggregate counters + last_online + the
// per-user time-series) so the dashboard totals/charts and online detection
// keep working. Called once per stats poll per active identity.
func (s *Store) AddBucketUsage(clientName string, up, down int64) error {
	if clientName == "" || (up == 0 && down == 0) {
		return nil
	}
	now := time.Now().Unix()
	// One transaction: the bucket counter, the mirrored user aggregate, and the
	// time-series sample must all land together (they may otherwise run on
	// different pooled connections and diverge if one fails).
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

	res, err := tx.Exec(`UPDATE user_plans SET used_up=used_up+?, used_down=used_down+?, last_online_at=?, updated_at=? WHERE client_name=?`,
		up, down, now, now, clientName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // unknown identity (e.g. a just-removed bucket) — ignore, rolls back
	}
	if _, err = tx.Exec(`UPDATE users SET used_up=used_up+?, used_down=used_down+?, last_online_at=?, updated_at=?
		WHERE id=(SELECT user_id FROM user_plans WHERE client_name=?)`, up, down, now, now, clientName); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO traffic_samples (user_id, ts, up, down)
		SELECT user_id, ?, ?, ? FROM user_plans WHERE client_name=?`, now, up, down, clientName); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// mergeDuplicatePlanBuckets collapses pre-existing duplicate plan buckets (same
// user + package) into one, summing traffic quota and usage and taking the
// latest expiry. This repairs accounts that repurchased a plan before renewal
// stacking existed (which minted a new bucket each time). The survivor is the
// oldest bucket, so it keeps a stable identity; the rest are deleted and their
// sing-box identities drop out on the next rebuild. Idempotent: once merged,
// each (user, package) has a single row and the query matches nothing. The
// users.* aggregate is unchanged because the survivor holds the summed totals.
func (s *Store) mergeDuplicatePlanBuckets() error {
	rows, err := s.db.Query(`SELECT user_id, package_id, MIN(id),
		SUM(traffic_limit), SUM(used_up), SUM(used_down), MAX(expiry_at)
		FROM user_plans WHERE kind='plan' AND package_id>0
		GROUP BY user_id, package_id HAVING COUNT(*)>1`)
	if err != nil {
		return err
	}
	type dup struct {
		userID, packageID, keepID, limit, up, down, expiry int64
	}
	var dups []dup
	for rows.Next() {
		var d dup
		if err := rows.Scan(&d.userID, &d.packageID, &d.keepID, &d.limit, &d.up, &d.down, &d.expiry); err != nil {
			rows.Close()
			return err
		}
		dups = append(dups, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, d := range dups {
		if _, err := s.db.Exec(`UPDATE user_plans SET traffic_limit=?, used_up=?, used_down=?, expiry_at=?, updated_at=? WHERE id=?`,
			d.limit, d.up, d.down, d.expiry, now, d.keepID); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DELETE FROM user_plans WHERE kind='plan' AND user_id=? AND package_id=? AND id<>?`,
			d.userID, d.packageID, d.keepID); err != nil {
			return err
		}
	}
	return nil
}

// backfillUserPlans seeds the bucket model from the legacy single-plan columns
// on first run (idempotent: skipped once any bucket exists). Existing clients
// keep working because a plan bucket reuses the user's current identity; the
// pool gets a fresh identity and starts empty.
func (s *Store) backfillUserPlans() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_plans`).Scan(&n); err != nil || n > 0 {
		return err
	}
	rows, err := s.db.Query(`SELECT id, username, client_name, client_uuid, client_secret,
		current_plan_id, traffic_limit, used_up, used_down, expiry_at FROM users`)
	if err != nil {
		return err
	}
	type urec struct {
		id                     int64
		username               string
		name, cuuid, csecret   sql.NullString
		planID                 sql.NullInt64
		limit, up, down, expiry int64
	}
	var us []urec
	for rows.Next() {
		var u urec
		if err := rows.Scan(&u.id, &u.username, &u.name, &u.cuuid, &u.csecret,
			&u.planID, &u.limit, &u.up, &u.down, &u.expiry); err != nil {
			rows.Close()
			return err
		}
		us = append(us, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, u := range us {
		primaryName := u.name.String
		if primaryName == "" {
			primaryName = fmt.Sprintf("qz_%s", u.username)
		}
		if u.planID.Valid && u.planID.Int64 > 0 {
			// Existing identity becomes the migrated plan; pool is new & empty.
			name := "套餐"
			if p, _ := s.GetPackage(u.planID.Int64); p != nil {
				name = p.Name
			}
			if _, err := insertBucket(s.db, &Bucket{
				UserID: u.id, Kind: "plan", PackageID: u.planID.Int64, Name: name,
				ClientName: primaryName, ClientUUID: u.cuuid.String, ClientSecret: u.csecret.String,
				TrafficLimit: u.limit, UsedUp: u.up, UsedDown: u.down, ExpiryAt: u.expiry,
			}); err != nil {
				return err
			}
			pu, ps := genBucketCreds()
			if _, err := insertBucket(s.db, &Bucket{
				UserID: u.id, Kind: "pool", Name: "通用流量",
				ClientName: primaryName + "_pool", ClientUUID: pu, ClientSecret: ps,
			}); err != nil {
				return err
			}
		} else {
			// No plan: existing identity becomes the pool, carrying any balance.
			if _, err := insertBucket(s.db, &Bucket{
				UserID: u.id, Kind: "pool", Name: "通用流量",
				ClientName: primaryName, ClientUUID: u.cuuid.String, ClientSecret: u.csecret.String,
				TrafficLimit: u.limit, UsedUp: u.up, UsedDown: u.down,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
