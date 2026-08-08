package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
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
	Query(query string, args ...any) (*sql.Rows, error)
}

// enqueuePlanBucket mints a fresh, independently-metered plan bucket in the
// 'queued' state for a plan purchase. Unlike the old stacking model, buying the
// SAME package again does NOT merge into one bucket — each purchase is its own
// bucket with its own identity and quota. A queued bucket does not count down
// (expiry_at=0) and is invisible to the config until advanceUserQueues promotes
// it. The caller runs advanceUserQueues right after, so a first purchase (no
// active head yet) activates immediately while a repeat purchase waits in line.
func enqueuePlanBucket(ex execer, userID int64, username string, pkg *Package, orderID, now int64) error {
	uu, ss := genBucketCreds()
	_, err := insertBucket(ex, &Bucket{
		UserID: userID, Kind: "plan", PackageID: pkg.ID, Name: pkg.Name,
		ClientName:   fmt.Sprintf("qz_%s_p%d", username, orderID),
		ClientUUID:   uu, ClientSecret: ss,
		TrafficLimit: pkg.TrafficBytes,
		Status:       "queued", ExpiryAt: 0, DurationDays: pkg.DurationDays,
		OrderID: orderID, CreatedAt: now,
	})
	return err
}

// advanceUserQueues promotes queued plan buckets whose slot is now free. For each
// package the user has a queued bucket for, if there is no currently-USABLE active
// head (same package, status='active', not expired, has quota), the oldest queued
// bucket is promoted: status='active' and its expiry_at starts counting now
// (now + duration_days). Idempotent — a usable head means no change. Returns
// whether anything was promoted so callers can trigger a config rebuild.
func advanceUserQueues(tx txLike, userID, now int64) (bool, error) {
	rows, err := tx.Query(`SELECT DISTINCT package_id FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='queued' AND package_id>0`, userID)
	if err != nil {
		return false, err
	}
	var pkgIDs []int64
	for rows.Next() {
		var p int64
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return false, err
		}
		pkgIDs = append(pkgIDs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	changed := false
	for _, pkgID := range pkgIDs {
		// A usable active head blocks promotion: not expired AND has quota.
		var usable int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM user_plans
			WHERE user_id=? AND kind='plan' AND package_id=? AND status='active'
			  AND (expiry_at=0 OR expiry_at>?)
			  AND (traffic_limit=0 OR used_up+used_down<traffic_limit)`,
			userID, pkgID, now).Scan(&usable); err != nil {
			return changed, err
		}
		if usable > 0 {
			continue
		}
		// Promote the oldest queued bucket for this package.
		var id, dur int64
		err := tx.QueryRow(`SELECT id, duration_days FROM user_plans
			WHERE user_id=? AND kind='plan' AND package_id=? AND status='queued'
			ORDER BY id LIMIT 1`, userID, pkgID).Scan(&id, &dur)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return changed, err
		}
		newExpiry := int64(0) // duration 0 = unlimited-duration plan → never expires
		if dur > 0 {
			newExpiry = now + dur*86400
		}
		if _, err := tx.Exec(`UPDATE user_plans SET status='active', expiry_at=?, updated_at=? WHERE id=?`,
			newExpiry, now, id); err != nil {
			return changed, err
		}
		changed = true
	}
	// A promotion changes the user's effective expiry (the newly-active份 starts its
	// countdown now). Recompute the legacy users.* aggregate so the dashboard's
	// top-line expiry/"已过期" alert reflects the fresh plan instead of the retired
	// head's now-past date. Enforcement reads buckets and is already correct; this
	// only keeps the display mirror in sync when promotion happens outside a
	// purchase/refund (i.e. the periodic ticker).
	if changed {
		if _, _, _, _, err := recomputeUserAggregate(tx, userID, now); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// AdvanceAllQueues promotes due queued buckets across every user (exhausted or
// expired heads free their slot). Each user is advanced in its own transaction so
// one failure doesn't abort the rest. Returns the users whose queue changed, so
// the caller can push fresh config only where an identity actually activated.
func (s *Store) AdvanceAllQueues() ([]int64, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT DISTINCT user_id FROM user_plans
		WHERE kind='plan' AND status='queued' AND package_id>0`)
	if err != nil {
		return nil, err
	}
	var userIDs []int64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return nil, err
		}
		userIDs = append(userIDs, uid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var changed []int64
	for _, uid := range userIDs {
		tx, err := s.db.Begin()
		if err != nil {
			return changed, err
		}
		ch, aerr := advanceUserQueues(tx, uid, now)
		if aerr != nil {
			_ = tx.Rollback()
			return changed, aerr
		}
		if err := tx.Commit(); err != nil {
			return changed, err
		}
		if ch {
			changed = append(changed, uid)
		}
	}
	return changed, nil
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

// reverseOrderBucket undoes ONE order's plan entitlement on refund. In the queue
// model each purchase is its own bucket, so a refund removes exactly that bucket
// (found by order_id) and then advances the queue — if the removed bucket was the
// active head, the next queued same-package bucket takes over. Falls back to the
// legacy package-based clamped reversal (reversePlanBucket) when no bucket carries
// this order_id (a pre-queue account) or when the matched bucket still holds more
// than this one order's quota (a legacy merged bucket whose order_id points here),
// so refunding an old stacked order never wipes several orders' entitlement.
func reverseOrderBucket(tx txLike, orderID, userID, packageID, trafficBytes, durationDays, now int64) error {
	var id, limit int64
	var status string
	err := tx.QueryRow(`SELECT id, traffic_limit, status FROM user_plans
		WHERE kind='plan' AND user_id=? AND order_id=? ORDER BY id LIMIT 1`, userID, orderID).Scan(&id, &limit, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return reversePlanBucket(tx, userID, packageID, trafficBytes, durationDays, now)
	}
	if err != nil {
		return err
	}
	// A single-order (queue-model) bucket holds exactly this order's quota; a legacy
	// merged bucket holds the sum of several stacked orders (limit > this order's
	// bytes) — reverse that the old, clamped way instead of deleting it wholesale.
	if status == "active" && trafficBytes > 0 && limit > trafficBytes {
		return reversePlanBucket(tx, userID, packageID, trafficBytes, durationDays, now)
	}
	if _, err := tx.Exec(`DELETE FROM user_plans WHERE id=?`, id); err != nil {
		return err
	}
	_, err = advanceUserQueues(tx, userID, now)
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
//
// Free buckets are excluded from all three sums: they carry no limit, so letting
// their usage into used_* would count unmetered free-group traffic against the
// user's paid quota in handleSub's serviceable check — the very coupling the
// free bucket exists to break.
func recomputeUserAggregate(tx txLike, userID, now int64) (limit, up, down, expiry int64, err error) {
	err = tx.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN kind=? THEN 0 ELSE traffic_limit END),0),
		COALESCE(SUM(CASE WHEN kind=? THEN 0 ELSE used_up END),0),
		COALESCE(SUM(CASE WHEN kind=? THEN 0 ELSE used_down END),0),
		COALESCE(MAX(CASE WHEN kind='plan' THEN expiry_at ELSE 0 END),0)
		FROM user_plans WHERE user_id=?`,
		KindFree, KindFree, KindFree, userID).Scan(&limit, &up, &down, &expiry)
	if err != nil {
		return
	}
	_, err = tx.Exec(`UPDATE users SET traffic_limit=?, used_up=?, used_down=?, expiry_at=?, updated_at=? WHERE id=?`,
		limit, up, down, expiry, now, userID)
	return
}

// KindFree is the bucket holding a user's free-group (unmetered) allowance.
//
// It exists so free traffic gets its own sing-box stats identity. Metering is
// identity-based, so when the pool covered the free group the free bytes were
// debited from the user's PAID balance — and since a top-up only raises
// traffic_limit and never clears used_*, traffic burned on free nodes before a
// purchase silently ate into that purchase. A free bucket has no limit and never
// expires; its usage is recorded for display but excluded from the user's quota
// aggregate (see recomputeUserAggregate).
const KindFree = "free"

// WelcomePackageID marks the signup-grant bucket. It is a plan bucket (so the
// trial actually expires) but belongs to no package, like the admin grant's
// package_id=0 — orderBuckets scopes both the same way. Distinct from 0 so an
// admin grant and a signup grant can coexist instead of overwriting each other.
const WelcomePackageID = -1

// EnsureFreeBucket creates the user's unmetered free-group bucket if it has none
// yet. Idempotent, so it doubles as the backfill for accounts provisioned before
// the free bucket existed.
func (s *Store) EnsureFreeBucket(userID int64, username string) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind=? ORDER BY id LIMIT 1`,
		userID, KindFree).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	uu, ss := genBucketCreds()
	_, err = insertBucket(s.db, &Bucket{
		UserID: userID, Kind: KindFree, Name: "免费流量",
		ClientName: fmt.Sprintf("qz_%s_free", username), ClientUUID: uu, ClientSecret: ss,
	})
	return err
}

// EnsureWelcomeBucket lands the configured signup grant (default_traffic /
// default_expiry_days) in a real bucket.
//
// Writing it to users.traffic_limit / users.expiry_at — which is what
// registration used to do — did nothing: those columns are a display mirror
// recomputed from the buckets, and enforcement reads buckets only. So the grant
// was invisible to sing-box (a user with no free group configured landed in no
// inbound at all), and the moment the user bought anything the recompute
// overwrote expiry_at with the max *plan* expiry — zero for a pool-only buyer —
// which handleSub reads as "never expires". Same class of bug as the admin grant
// fixed in 5bea5ad; this is the registration path it missed.
//
// No-ops when the grant is disabled (traffic and expiry both zero) or the user
// already has one. Recomputes the aggregate so the dashboard mirrors the bucket.
func (s *Store) EnsureWelcomeBucket(userID int64, username string, traffic, expiry int64) error {
	if traffic <= 0 && expiry <= 0 {
		return nil
	}
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

	var id int64
	err = tx.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind='plan' AND package_id=? ORDER BY id LIMIT 1`,
		userID, WelcomePackageID).Scan(&id)
	switch {
	case err == nil:
		return nil // already granted
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	uu, ss := genBucketCreds()
	if _, err = insertBucket(tx, &Bucket{
		UserID: userID, Kind: "plan", PackageID: WelcomePackageID, Name: "注册赠送",
		ClientName: fmt.Sprintf("qz_%s_welcome", username), ClientUUID: uu, ClientSecret: ss,
		TrafficLimit: traffic, ExpiryAt: expiry,
	}); err != nil {
		return err
	}
	if _, _, _, _, err = recomputeUserAggregate(tx, userID, time.Now().Unix()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
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
	// Status is the queue state of a plan bucket: 'active' (the current head that
	// meters/renders) or 'queued' (a same-package purchase waiting its turn; not
	// yet counting down and not in any config). pool/free are always 'active'.
	// DurationDays is the package duration a queued bucket applies to its expiry
	// when it is promoted to active.
	Status       string `json:"status"`
	DurationDays int64  `json:"duration_days"`
	// Mixed (HTTP/SOCKS5) proxy credential — a proxy-only account, unrelated to the
	// login account. Empty ProxyUsername → fall back to ClientName/ClientSecret.
	// ProxyExpiresAt 0 = permanent. See migrate.go for the schema.
	ProxyUsername  string `json:"-"`
	ProxyPassword  string `json:"-"`
	ProxyExpiresAt int64  `json:"-"`
}

// ProxyName is the mixed-proxy stats identity for this bucket: the custom
// username if set, else the system ClientName. This is the name sing-box tracks
// (and AddBucketUsage meters) for the bucket's mixed inbounds.
func (b *Bucket) ProxyName() string {
	if b.ProxyUsername != "" {
		return b.ProxyUsername
	}
	return b.ClientName
}

// ProxySecret is the mixed-proxy password: the custom one if set, else the
// system ClientSecret.
func (b *Bucket) ProxySecret() string {
	if b.ProxyPassword != "" {
		return b.ProxyPassword
	}
	return b.ClientSecret
}

// ProxyActive reports whether the mixed-proxy credential is usable now: the
// bucket itself must be able to carry traffic AND the proxy credential must not
// have hit its own (separate, optional) expiry.
func (b *Bucket) ProxyActive(now int64) bool {
	if b.ProxyExpiresAt != 0 && b.ProxyExpiresAt <= now {
		return false
	}
	return b.Active(now)
}

// Used is the bucket's total consumed bytes.
func (b *Bucket) Used() int64 { return b.UsedUp + b.UsedDown }

// HasQuota reports whether the bucket has traffic left (limit 0 = unlimited).
func (b *Bucket) HasQuota() bool { return b.TrafficLimit == 0 || b.Used() < b.TrafficLimit }

// NotExpired reports whether the bucket is still within its time window.
func (b *Bucket) NotExpired(now int64) bool { return b.ExpiryAt == 0 || b.ExpiryAt > now }

// Active reports whether the bucket can currently carry traffic. A pool is only
// active when it has a positive, non-exhausted balance (an empty pool is inert);
// a plan is active while not expired and not over quota; a free bucket is always
// active — it is the unmetered free-group allowance and has no limit to exhaust.
func (b *Bucket) Active(now int64) bool {
	switch b.Kind {
	case "pool":
		return b.TrafficLimit > 0 && b.Used() < b.TrafficLimit && b.NotExpired(now)
	case KindFree:
		return true
	}
	return b.NotExpired(now) && b.HasQuota()
}

const bucketCols = `id, user_id, kind, package_id, name, client_name, client_uuid, client_secret,
	traffic_limit, used_up, used_down, expiry_at, last_online_at, order_id, created_at, updated_at,
	proxy_username, proxy_password, proxy_expires_at, status, duration_days`

func scanBucket(sc scanner) (*Bucket, error) {
	var b Bucket
	err := sc.Scan(&b.ID, &b.UserID, &b.Kind, &b.PackageID, &b.Name, &b.ClientName, &b.ClientUUID,
		&b.ClientSecret, &b.TrafficLimit, &b.UsedUp, &b.UsedDown, &b.ExpiryAt, &b.LastOnlineAt,
		&b.OrderID, &b.CreatedAt, &b.UpdatedAt,
		&b.ProxyUsername, &b.ProxyPassword, &b.ProxyExpiresAt, &b.Status, &b.DurationDays)
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

// ListBucketsBulk returns the buckets of several users in one query, keyed by
// user id. The admin user list needs every user's buckets to roll up traffic
// correctly (the users.* columns are a naive sum — see AdminUserTraffic), and
// one ListBuckets per row would be a query per user.
func (s *Store) ListBucketsBulk(userIDs []int64) (map[int64][]*Bucket, error) {
	out := map[int64][]*Bucket{}
	if len(userIDs) == 0 {
		return out, nil
	}
	q := `SELECT ` + bucketCols + ` FROM user_plans WHERE user_id IN (?` +
		strings.Repeat(`,?`, len(userIDs)-1) + `) ORDER BY user_id, id`
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out[b.UserID] = append(out[b.UserID], b)
	}
	return out, rows.Err()
}

var (
	// ErrBucketNotFound — no such bucket for that user (wrong id, or already gone).
	ErrBucketNotFound = errors.New("该套餐不存在")
	// ErrBucketProtected — the free bucket is the account's unmetered metering
	// identity, not something the user was granted; removing it would silently
	// re-route free-group traffic onto the paid pool (the very coupling it exists
	// to break), so it is not removable.
	ErrBucketProtected = errors.New("该额度是系统内部计量身份，不能移除")
)

// DeleteBucket removes one of a user's buckets — an admin pulling back a plan份
// or the traffic pool. Returns the removed bucket so the caller can report what
// went.
//
// This is a revocation, not a refund: no points are returned and the order row
// (if any) stays as it was. Refunding is ListOrders → RefundOrder, which reverses
// exactly one order's entitlement and gives the points back.
//
// Removing the active head of a package queue frees the slot, so advanceUserQueues
// promotes the next queued份 in the same transaction — otherwise a user whose
// current份 was pulled would sit with a paid-but-invisible份 until the periodic
// ticker noticed.
func (s *Store) DeleteBucket(userID, bucketID int64) (*Bucket, error) {
	now := time.Now().Unix()
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

	b, err := scanBucket(tx.QueryRow(`SELECT `+bucketCols+` FROM user_plans WHERE id=? AND user_id=?`, bucketID, userID))
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrBucketNotFound
	}
	if b.Kind == KindFree {
		return nil, ErrBucketProtected
	}
	if _, err := tx.Exec(`DELETE FROM user_plans WHERE id=? AND user_id=?`, bucketID, userID); err != nil {
		return nil, err
	}
	if _, err := advanceUserQueues(tx, userID, now); err != nil {
		return nil, err
	}
	// advanceUserQueues only recomputes when it promoted something; the deletion
	// itself always changes the aggregate, so recompute unconditionally.
	if _, _, _, _, err := recomputeUserAggregate(tx, userID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return b, nil
}

// BucketByClientName resolves a sing-box stats identity to its bucket.
func (s *Store) BucketByClientName(name string) (*Bucket, error) {
	return scanBucket(s.db.QueryRow(`SELECT `+bucketCols+` FROM user_plans WHERE client_name=?`, name))
}

// proxyUsernameRe restricts a custom mixed-proxy username to safe characters: it
// lands verbatim in the sing-box config and becomes a stats identity, so no
// whitespace/quotes/control chars. Length 2-64, must start alphanumeric.
var proxyUsernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{1,63}$`)

// ValidateProxyUsername checks a user-chosen mixed-proxy username. The "qz_"
// prefix is reserved for system-generated client_names, so banning it guarantees
// a custom username can never collide with any bucket's client_name identity.
func ValidateProxyUsername(name string) error {
	if !proxyUsernameRe.MatchString(name) {
		return errors.New("用户名需 2-64 位，仅限字母/数字/ _.@- ，且以字母或数字开头")
	}
	if strings.HasPrefix(name, "qz_") {
		return errors.New("用户名不能以 qz_ 开头（系统保留前缀）")
	}
	return nil
}

// SetBucketProxyCred sets a bucket's mixed-proxy credential (a proxy-only
// account, unrelated to the login account). expiresAt 0 = permanent. Ownership is
// enforced via userID. The username must not collide with another bucket's
// proxy_username or any client_name.
func (s *Store) SetBucketProxyCred(bucketID, userID int64, username, password string, expiresAt int64) error {
	if err := ValidateProxyUsername(username); err != nil {
		return err
	}
	if len(password) < 6 || len(password) > 128 {
		return errors.New("密码需 6-128 位")
	}
	if expiresAt < 0 {
		return errors.New("有效期非法")
	}
	// Reject a username already taken by another bucket's proxy_username or by any
	// client_name (belt-and-suspenders; the qz_ ban already excludes client_names).
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE (proxy_username=? AND id<>?) OR client_name=?`,
		username, bucketID, username).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("该用户名已被占用，请换一个")
	}
	res, err := s.db.Exec(`UPDATE user_plans SET proxy_username=?, proxy_password=?, proxy_expires_at=?, updated_at=? WHERE id=? AND user_id=?`,
		username, password, expiresAt, time.Now().Unix(), bucketID, userID)
	if err != nil {
		return errors.New("保存失败，用户名可能已被占用") // unique-index guard against a concurrent duplicate
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		return errors.New("套餐不存在或无权修改")
	}
	return nil
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
	status := b.Status
	if status == "" {
		status = "active" // pool/free/grant buckets are active on creation
	}
	res, err := ex.Exec(`INSERT INTO user_plans
		(user_id, kind, package_id, name, client_name, client_uuid, client_secret,
		 traffic_limit, used_up, used_down, expiry_at, last_online_at, order_id, status, duration_days, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.UserID, b.Kind, b.PackageID, b.Name, b.ClientName, b.ClientUUID, b.ClientSecret,
		b.TrafficLimit, b.UsedUp, b.UsedDown, b.ExpiryAt, b.LastOnlineAt, b.OrderID, status, b.DurationDays, b.CreatedAt, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddBucketUsage applies a sing-box stats delta to the matching bucket, and
// mirrors it onto the owning user (aggregate counters + last_online + the
// per-user time-series) so the dashboard totals/charts and online detection
// keep working. Called once per stats poll per active identity.
func (s *Store) AddBucketUsage(statName string, up, down int64) error {
	if statName == "" || (up == 0 && down == 0) {
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

	// Resolve the stats identity to its bucket. A bucket has up to two identities:
	// client_name (used by every protocol) and, for mixed inbounds, an optional
	// custom proxy_username. Both meter the same bucket. proxy_username can never
	// equal a client_name (client_names are qz_-prefixed, proxy_usernames may not
	// be) and is globally unique, so at most one row matches.
	bucketID, userID, err := resolveStatsIdentity(tx, statName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // unknown identity (e.g. a just-removed bucket) — ignore, rolls back
	}
	if err != nil {
		return err
	}
	if err = applyBucketUsage(tx, bucketID, userID, up, down, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// UsageDelta is one identity's per-poll traffic delta, keyed by its stats name in
// AddUsageBatch.
type UsageDelta struct {
	Up   int64
	Down int64
}

// AddUsageBatch applies a whole stats poll's per-identity deltas in ONE
// transaction. Previously each identity opened its own write transaction, so a
// 100-user poll grabbed the single SQLite/WAL write lock ~100-200 times a minute,
// contending with live subscription/purchase writes; this collapses that to one
// lock acquisition. Each identity's three writes (bucket + mirrored user aggregate
// + time-series sample) are wrapped in a SAVEPOINT so one failure rolls back just
// that identity — the poll used reset=true, so the rest of the deltas must still
// land rather than being discarded with it. Returns how many identities applied,
// and (if any failed) an error naming the first.
func (s *Store) AddUsageBatch(deltas map[string]UsageDelta) (int, error) {
	if len(deltas) == 0 {
		return 0, nil
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	applied, failed := 0, 0
	var firstErr error
	for name, d := range deltas {
		if name == "" || (d.Up == 0 && d.Down == 0) {
			continue
		}
		bucketID, userID, rerr := resolveStatsIdentity(tx, name)
		if errors.Is(rerr, sql.ErrNoRows) {
			continue // unknown / just-removed identity — skip, not a failure
		}
		if rerr != nil {
			if firstErr == nil {
				firstErr = rerr
			}
			failed++
			continue
		}
		if _, err := tx.Exec(`SAVEPOINT usg`); err != nil {
			return applied, err // savepoint itself failing means the tx is unusable
		}
		if werr := applyBucketUsage(tx, bucketID, userID, d.Up, d.Down, now); werr != nil {
			_, _ = tx.Exec(`ROLLBACK TO usg`)
			_, _ = tx.Exec(`RELEASE usg`)
			if firstErr == nil {
				firstErr = werr
			}
			failed++
			continue
		}
		_, _ = tx.Exec(`RELEASE usg`)
		applied++
	}
	if err := tx.Commit(); err != nil {
		return applied, err
	}
	committed = true
	if firstErr != nil {
		return applied, fmt.Errorf("stats: %d identity updates failed, first: %w", failed, firstErr)
	}
	return applied, nil
}

// resolveStatsIdentity maps a sing-box stats name to its bucket. A bucket has up
// to two identities: client_name (every protocol) and, for mixed inbounds, an
// optional custom proxy_username. Both meter the same bucket; proxy_username can
// never equal a client_name (client_names are qz_-prefixed, proxy_usernames may
// not be) and is globally unique, so at most one row matches.
func resolveStatsIdentity(tx txLike, statName string) (bucketID, userID int64, err error) {
	err = tx.QueryRow(`SELECT id, user_id FROM user_plans WHERE client_name=? OR (proxy_username<>'' AND proxy_username=?)`,
		statName, statName).Scan(&bucketID, &userID)
	return
}

// applyBucketUsage writes one identity's delta: the bucket counter, the mirrored
// user aggregate + last_online, and the per-user time-series sample. Caller owns
// the transaction (or savepoint) so these three land together or not at all.
func applyBucketUsage(tx txLike, bucketID, userID, up, down, now int64) error {
	if _, err := tx.Exec(`UPDATE user_plans SET used_up=used_up+?, used_down=used_down+?, last_online_at=?, updated_at=? WHERE id=?`,
		up, down, now, now, bucketID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET used_up=used_up+?, used_down=used_down+?, last_online_at=?, updated_at=? WHERE id=?`,
		up, down, now, now, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO traffic_samples (user_id, ts, up, down) VALUES (?, ?, ?, ?)`,
		userID, now, up, down); err != nil {
		return err
	}
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
	// Only collapse legacy 'active' duplicates. Never touch 'queued' buckets —
	// merging them would re-create the very stacking the queue model removes.
	rows, err := s.db.Query(`SELECT user_id, package_id, MIN(id),
		SUM(traffic_limit), SUM(used_up), SUM(used_down), MAX(expiry_at)
		FROM user_plans WHERE kind='plan' AND package_id>0 AND status='active'
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
		if _, err := s.db.Exec(`DELETE FROM user_plans WHERE kind='plan' AND user_id=? AND package_id=? AND id<>? AND status='active'`,
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
