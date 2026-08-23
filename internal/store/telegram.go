package store

import (
	"database/sql"
	"errors"
	"time"
)

// ErrTelegramTaken is returned when a Telegram account is already bound to a
// different panel user. Letting two accounts share a chat would leak the other
// user's subscription URL through /sub.
var ErrTelegramTaken = errors.New("该 Telegram 已绑定其他账号")

// TelegramBind is one panel user ↔ Telegram account pairing.
type TelegramBind struct {
	UserID        int64
	TelegramID    int64
	ChatID        int64
	Username      string
	FirstName     string
	NotifyExpiry  bool
	NotifyTraffic bool
	BoundAt       int64
	LastChatAt    int64
}

const telegramBindCols = `user_id, telegram_id, chat_id, username, first_name,
	notify_expiry, notify_traffic, bound_at, last_chat_at`

func scanTelegramBind(sc scanner) (*TelegramBind, error) {
	var b TelegramBind
	var expiry, traffic int
	err := sc.Scan(&b.UserID, &b.TelegramID, &b.ChatID, &b.Username, &b.FirstName,
		&expiry, &traffic, &b.BoundAt, &b.LastChatAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.NotifyExpiry = expiry != 0
	b.NotifyTraffic = traffic != 0
	return &b, nil
}

func (s *Store) TelegramBindByUser(userID int64) (*TelegramBind, error) {
	return scanTelegramBind(s.db.QueryRow(`SELECT `+telegramBindCols+` FROM telegram_binds WHERE user_id=?`, userID))
}

func (s *Store) TelegramBindByTelegramID(telegramID int64) (*TelegramBind, error) {
	return scanTelegramBind(s.db.QueryRow(`SELECT `+telegramBindCols+` FROM telegram_binds WHERE telegram_id=?`, telegramID))
}

// ListTelegramBinds returns every bound account. The notify sweep walks this;
// a small panel's bound-user count is the whole table.
func (s *Store) ListTelegramBinds() ([]*TelegramBind, error) {
	rows, err := s.db.Query(`SELECT ` + telegramBindCols + ` FROM telegram_binds`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TelegramBind
	for rows.Next() {
		b, err := scanTelegramBind(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BindTelegram attaches telegramID to userID. If this user already had a
// different Telegram, it is replaced (they generated a new bind link from the
// panel). If telegramID is already someone else's, the call fails.
func (s *Store) BindTelegram(userID, telegramID, chatID int64, username, firstName string) error {
	if userID <= 0 || telegramID == 0 {
		return errors.New("invalid telegram bind")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := bindTelegramTx(tx, userID, telegramID, chatID, username, firstName); err != nil {
		return err
	}
	return tx.Commit()
}

func bindTelegramTx(tx *sql.Tx, userID, telegramID, chatID int64, username, firstName string) error {
	var owner int64
	err := tx.QueryRow(`SELECT user_id FROM telegram_binds WHERE telegram_id=?`, telegramID).Scan(&owner)
	if err == nil && owner != userID {
		return ErrTelegramTaken
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().Unix()
	_, err = tx.Exec(`INSERT INTO telegram_binds
		(user_id, telegram_id, chat_id, username, first_name, notify_expiry, notify_traffic, bound_at, last_chat_at)
		VALUES (?,?,?,?,?,1,1,?,?)
		ON CONFLICT(user_id) DO UPDATE SET
			telegram_id=excluded.telegram_id,
			chat_id=excluded.chat_id,
			username=excluded.username,
			first_name=excluded.first_name,
			bound_at=excluded.bound_at,
			last_chat_at=excluded.last_chat_at`,
		userID, telegramID, chatID, username, firstName, now, now)
	return err
}

func (s *Store) UnbindTelegram(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM telegram_binds WHERE user_id=?`, userID)
	return err
}

// UnbindTelegramByTelegramID drops the bind for a chat-initiated /unbind.
// DELETE ... RETURNING makes lookup and deletion one atomic statement, so a
// concurrent rebind cannot make this command report or remove the wrong owner.
func (s *Store) UnbindTelegramByTelegramID(telegramID int64) (userID int64, ok bool, err error) {
	err = s.db.QueryRow(`DELETE FROM telegram_binds WHERE telegram_id=? RETURNING user_id`, telegramID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return userID, true, nil
}

func (s *Store) SetTelegramNotify(userID int64, expiry, traffic bool) error {
	toInt := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	res, err := s.db.Exec(`UPDATE telegram_binds SET notify_expiry=?, notify_traffic=? WHERE user_id=?`,
		toInt(expiry), toInt(traffic), userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) TouchTelegramChat(telegramID int64) {
	_, _ = s.db.Exec(`UPDATE telegram_binds SET last_chat_at=? WHERE telegram_id=?`,
		time.Now().Unix(), telegramID)
}

// CreateTelegramBindToken mints a one-time start payload for userID and
// invalidates any unused token they already had, so the link currently on
// screen is the only live one.
func (s *Store) CreateTelegramBindToken(userID int64, token string, ttl time.Duration) error {
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE telegram_bind_tokens SET used=1 WHERE user_id=? AND used=0`, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(
		`INSERT INTO telegram_bind_tokens (token, user_id, expires_at, used, created_at)
		 VALUES (?,?,?,0,?)`,
		token, userID, now.Add(ttl).Unix(), now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// BindTelegramWithToken validates and consumes a live token in the same
// transaction that installs the bind. A failed/taken bind therefore leaves the
// one-time token usable for a corrected attempt.
func (s *Store) BindTelegramWithToken(token string, telegramID, chatID int64, username, firstName string) (userID int64, ok bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(
		`SELECT user_id FROM telegram_bind_tokens
		 WHERE token=? AND used=0 AND expires_at>?`,
		token, time.Now().Unix()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if err = bindTelegramTx(tx, userID, telegramID, chatID, username, firstName); err != nil {
		return userID, false, err
	}
	res, err := tx.Exec(`UPDATE telegram_bind_tokens SET used=1 WHERE token=? AND used=0`, token)
	if err != nil {
		return userID, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, false, nil
	}
	if err = tx.Commit(); err != nil {
		return userID, false, err
	}
	return userID, true, nil
}

// UseTelegramBindToken remains for callers that only need token consumption.
func (s *Store) UseTelegramBindToken(token string) (userID int64, ok bool, err error) {
	err = s.db.QueryRow(
		`UPDATE telegram_bind_tokens SET used=1
		 WHERE token=? AND used=0 AND expires_at>?
		 RETURNING user_id`, token, time.Now().Unix()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return userID, err == nil, err
}

func (s *Store) CleanupTelegramBindTokens() {
	_, _ = s.db.Exec(`DELETE FROM telegram_bind_tokens WHERE expires_at < ? OR used=1`, time.Now().Unix())
}

// ClaimNotify records that we are about to send (user, kind, subject).
// Returns true only on the first claim — a second sweep of the same condition
// is a no-op, which is what keeps expiry/traffic notices from repeating every
// few minutes.
func (s *Store) ClaimNotify(userID int64, kind, subject string) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO user_notify_log (user_id, kind, subject, sent_at)
		 VALUES (?,?,?,?)`,
		userID, kind, subject, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClearNotify forgets a previous claim so the condition can fire again after
// it recovers (user buys more traffic, remaining climbs back above the
// threshold). No-op if nothing was claimed.
func (s *Store) ClearNotify(userID int64, kind, subject string) error {
	_, err := s.db.Exec(`DELETE FROM user_notify_log WHERE user_id=? AND kind=? AND subject=?`,
		userID, kind, subject)
	return err
}
