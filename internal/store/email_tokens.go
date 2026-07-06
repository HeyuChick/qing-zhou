package store

import (
	"time"
)

func (s *Store) CreateEmailToken(userID int64, token, purpose string, ttl time.Duration) error {
	now := time.Now()
	_, err := s.db.Exec(
		`INSERT INTO email_tokens (user_id, token, purpose, expires_at, used, created_at)
		 VALUES (?,?,?,?,0,?)`,
		userID, token, purpose, now.Add(ttl).Unix(), now.Unix())
	return err
}

// UseEmailToken atomically validates and consumes a token. Returns the user id
// and ok=true only if the token exists, matches purpose, is unused, and unexpired.
func (s *Store) UseEmailToken(token, purpose string) (int64, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var id, userID int64
	err = tx.QueryRow(
		`SELECT id, user_id FROM email_tokens
		 WHERE token=? AND purpose=? AND used=0 AND expires_at>?`,
		token, purpose, time.Now().Unix()).Scan(&id, &userID)
	if err != nil {
		return 0, false, nil // not found / expired / used → ok=false, no error
	}
	if _, err = tx.Exec(`UPDATE email_tokens SET used=1 WHERE id=?`, id); err != nil {
		return 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	committed = true
	return userID, true, nil
}

func (s *Store) SetEmailVerified(userID int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET email_verified=1, updated_at=? WHERE id=?`,
		time.Now().Unix(), userID)
	return err
}

func (s *Store) UpdatePassword(userID int64, passwordHash string) error {
	_, err := s.db.Exec(
		`UPDATE users SET password_hash=?, updated_at=? WHERE id=?`,
		passwordHash, time.Now().Unix(), userID)
	return err
}

// TokenUserVerified looks up the user a (possibly already-used) verify token
// belongs to, and whether that user is already email-verified. Used to render a
// friendly "already verified" page instead of "invalid link" on a re-click or a
// mail-scanner pre-fetch that consumed the one-time token.
func (s *Store) TokenUserVerified(token string) (found bool, verified bool) {
	var uid int64
	if err := s.db.QueryRow(
		`SELECT user_id FROM email_tokens WHERE token=? AND purpose='verify'`, token).Scan(&uid); err != nil {
		return false, false
	}
	var v bool
	_ = s.db.QueryRow(`SELECT email_verified FROM users WHERE id=?`, uid).Scan(&v)
	return true, v
}

// CleanupEmailTokens removes expired tokens. Used-but-unexpired tokens are kept
// so a re-click / mail-scanner pre-fetch can still resolve to an "already
// verified" page within the token's lifetime instead of "invalid link".
func (s *Store) CleanupEmailTokens() {
	_, _ = s.db.Exec(`DELETE FROM email_tokens WHERE expires_at < ?`, time.Now().Unix())
}
