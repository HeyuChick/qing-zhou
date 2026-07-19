package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID            int64
	Username      string
	Email         sql.NullString
	PasswordHash  string
	Role          string
	Status        string
	EmailVerified bool
	Points        int64
	ClientID     sql.NullInt64
	ClientName   sql.NullString
	ClientUUID   sql.NullString
	ClientSecret sql.NullString
	SubToken        sql.NullString
	CurrentPlanID sql.NullInt64
	TrafficLimit  int64
	DeviceLimit   int64
	UsedUp        int64
	UsedDown      int64
	ExpiryAt      int64
	CreatedAt     int64
	UpdatedAt     int64
}

const userCols = `id, username, email, password_hash, role, status, email_verified, points,
	client_id, client_name, client_uuid, client_secret, sub_token, current_plan_id,
	traffic_limit, device_limit, used_up, used_down, expiry_at, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanUser(sc scanner) (*User, error) {
	var u User
	err := sc.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.Status,
		&u.EmailVerified, &u.Points, &u.ClientID, &u.ClientName, &u.ClientUUID,
		&u.ClientSecret, &u.SubToken, &u.CurrentPlanID, &u.TrafficLimit, &u.DeviceLimit,
		&u.UsedUp, &u.UsedDown, &u.ExpiryAt, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) UserByUsername(username string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username=?`, username))
}

func (s *Store) UserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (s *Store) UserByEmail(email string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE email=?`, email))
}

func (s *Store) UserBySubToken(token string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE sub_token=?`, token))
}

// NewUser holds the fields needed to create a panel user.
type NewUser struct {
	Username     string
	Email        string
	PasswordHash string
	Role         string // defaults to "user"
	Points       int64
	SubToken     string
	TrafficLimit int64
	DeviceLimit  int64
	ExpiryAt     int64
}

func (s *Store) CreateUser(nu NewUser) (int64, error) {
	role := nu.Role
	if role == "" {
		role = "user"
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO users
		(username, email, password_hash, role, status, email_verified, points,
		 sub_token, traffic_limit, device_limit, expiry_at, created_at, updated_at)
		VALUES (?,?,?,?,'active',0,?,?,?,?,?,?,?)`,
		nu.Username, nullStr(nu.Email), nu.PasswordHash, role, nu.Points,
		nullStr(nu.SubToken), nu.TrafficLimit, nu.DeviceLimit, nu.ExpiryAt, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetUserClient records the provisioned sing-box client identity (including the
// credential secret, needed to rebuild config for edits without rotating it).
func (s *Store) SetUserClient(userID, clientID int64, name, uuid, secret string) error {
	_, err := s.db.Exec(
		`UPDATE users SET client_id=?, client_name=?, client_uuid=?, client_secret=?, updated_at=? WHERE id=?`,
		clientID, name, uuid, secret, time.Now().Unix(), userID)
	return err
}

// SetSubToken rotates a user's subscription token (revokes the old link).
func (s *Store) SetSubToken(userID int64, token string) error {
	_, err := s.db.Exec(`UPDATE users SET sub_token=?, updated_at=? WHERE id=?`,
		token, time.Now().Unix(), userID)
	return err
}

// SetUserEmail changes a user's email and marks it unverified (pending re-verify).
// SetUserEmail rebinds a user's address and drops any verification token still
// outstanding for them.
//
// Invalidating the old tokens is the security-relevant half. A verify token
// carries only user_id — no address — and SetEmailVerified marks whatever
// address the row currently holds. So without this, a user could request a token
// for an address they own, not click it, rebind to someone else's address, then
// redeem the old token and end up email_verified on an address they never
// controlled — squatting it permanently, since both registration and rebinding
// reject an address already held by another account.
//
// Both statements share one transaction: leaving the address changed while the
// old tokens survived would be exactly the state this defends against.
func (s *Store) SetUserEmail(userID int64, email string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE users SET email=?, email_verified=0, updated_at=? WHERE id=?`,
		nullStr(email), time.Now().Unix(), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM email_tokens WHERE user_id=? AND purpose='verify' AND used=0`,
		userID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateEntitlement persists traffic/expiry/plan/used changes (used by purchase
// and admin edits) and returns nothing; callers sync to sing-box separately.
func (s *Store) UpdateEntitlement(userID, trafficLimit, expiryAt int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET traffic_limit=?, expiry_at=?, updated_at=? WHERE id=?`,
		trafficLimit, expiryAt, time.Now().Unix(), userID)
	return err
}

// UpdateUserUsage stores the latest used up/down bytes from sing-box.
func (s *Store) UpdateUserUsage(userID, up, down int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET used_up=?, used_down=?, updated_at=? WHERE id=?`,
		up, down, time.Now().Unix(), userID)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Clean up the user's operational rows so they don't linger as orphans.
	// Financial/audit history (orders, point_transactions) and the deliberately
	// snapshotted reg_code_uses are kept intentionally. user_plans (buckets) and
	// traffic_samples MUST go — there is no FK cascade, and a leftover bucket still
	// resolves via BucketByClientName (stale identity) while its samples accumulate.
	for _, q := range []string{
		`DELETE FROM sessions WHERE user_id=?`,
		`DELETE FROM email_tokens WHERE user_id=?`,
		`DELETE FROM user_disabled_nodes WHERE user_id=?`,
		`DELETE FROM device_addons WHERE user_id=?`,
		`DELETE FROM announcement_reads WHERE user_id=?`,
		`DELETE FROM user_group_members WHERE user_id=?`,
		`DELETE FROM user_plans WHERE user_id=?`,
		`DELETE FROM traffic_samples WHERE user_id=?`,
		`DELETE FROM users WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ManualGrant describes an admin's manual "general allowance" for a user. Enabled
// false removes any existing grant; Traffic 0 = unlimited (plan-bucket semantics);
// Expiry 0 = never. A nil *ManualGrant passed to AdminUpdateUser leaves the grant
// untouched (for edits that only change status/reset).
type ManualGrant struct {
	Enabled bool
	Traffic int64
	Expiry  int64
}

// AdminUpdateUser applies an admin's edits to a user: status (ban/unban), an
// optional usage reset, and the manual allowance grant.
//
// The manual grant lives in a dedicated, real, metered bucket (kind='plan',
// package_id=0, "管理员额度") rather than the legacy users.traffic_limit column.
// That column is only a display mirror recomputed from the buckets, so writing it
// directly did nothing to enforcement and was silently overwritten on the user's
// next purchase/refund. Routing the grant into a bucket makes it actually usable
// (scoped like the pool: free group + the user's plan groups — see orderBuckets)
// and durable.
func (s *Store) AdminUpdateUser(id int64, status string, resetUsed bool, manual *ManualGrant) error {
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

	if _, err := tx.Exec(`UPDATE users SET status=?, updated_at=? WHERE id=?`, status, now, id); err != nil {
		return err
	}
	if resetUsed {
		// Zero the authoritative bucket counters (not just the users.* mirror, which a
		// recompute would immediately overwrite back from the buckets).
		if _, err := tx.Exec(`UPDATE user_plans SET used_up=0, used_down=0, updated_at=? WHERE user_id=?`, now, id); err != nil {
			return err
		}
	}

	if manual != nil {
		if err := applyManualGrant(tx, id, manual, now); err != nil {
			return err
		}
	}

	// Recompute the legacy users.* aggregate so the dashboard mirrors the buckets.
	if _, _, _, _, err := recomputeUserAggregate(tx, id, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// applyManualGrant upserts (Enabled) or removes (!Enabled) the user's package_id=0
// admin-grant bucket within the caller's transaction.
func applyManualGrant(tx txLike, userID int64, g *ManualGrant, now int64) error {
	var bid int64
	qerr := tx.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind='plan' AND package_id=0 ORDER BY id LIMIT 1`, userID).Scan(&bid)
	switch {
	case errors.Is(qerr, sql.ErrNoRows):
		if g.Enabled {
			var uname string
			if err := tx.QueryRow(`SELECT username FROM users WHERE id=?`, userID).Scan(&uname); err != nil {
				return err
			}
			uu, ss := genBucketCreds()
			if _, err := insertBucket(tx, &Bucket{
				UserID: userID, Kind: "plan", PackageID: 0, Name: "管理员额度",
				ClientName: fmt.Sprintf("qz_%s_admin", uname), ClientUUID: uu, ClientSecret: ss,
				TrafficLimit: g.Traffic, ExpiryAt: g.Expiry, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	case qerr != nil:
		return qerr
	default:
		if g.Enabled {
			_, err := tx.Exec(`UPDATE user_plans SET traffic_limit=?, expiry_at=?, updated_at=? WHERE id=?`,
				g.Traffic, g.Expiry, now, bid)
			return err
		}
		_, err := tx.Exec(`DELETE FROM user_plans WHERE id=?`, bid)
		return err
	}
}

// ListUsers returns users for the admin list, newest first, optional search.
func (s *Store) ListUsers(search string, limit int) ([]*User, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT ` + userCols + ` FROM users`
	args := []any{}
	if search != "" {
		q += ` WHERE username LIKE ? OR email LIKE ?`
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsersWithClient returns users that have a provisioned sing-box client.
func (s *Store) UsersWithClient() ([]*User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users
		WHERE client_name IS NOT NULL AND client_name <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
