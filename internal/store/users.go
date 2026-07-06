package store

import (
	"database/sql"
	"errors"
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
func (s *Store) SetUserEmail(userID int64, email string) error {
	_, err := s.db.Exec(`UPDATE users SET email=?, email_verified=0, updated_at=? WHERE id=?`,
		nullStr(email), time.Now().Unix(), userID)
	return err
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
	// snapshotted reg_code_uses are kept intentionally.
	for _, q := range []string{
		`DELETE FROM sessions WHERE user_id=?`,
		`DELETE FROM email_tokens WHERE user_id=?`,
		`DELETE FROM user_disabled_nodes WHERE user_id=?`,
		`DELETE FROM device_addons WHERE user_id=?`,
		`DELETE FROM announcement_reads WHERE user_id=?`,
		`DELETE FROM users WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AdminUpdateUser sets a user's quota/expiry/status, optionally resetting used.
func (s *Store) AdminUpdateUser(id, trafficLimit, expiryAt int64, status string, resetUsed bool) error {
	now := time.Now().Unix()
	if resetUsed {
		_, err := s.db.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, status=?, used_up=0, used_down=0, updated_at=? WHERE id=?`,
			trafficLimit, expiryAt, status, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE users SET traffic_limit=?, expiry_at=?, status=?, updated_at=? WHERE id=?`,
		trafficLimit, expiryAt, status, now, id)
	return err
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
