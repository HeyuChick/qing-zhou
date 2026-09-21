package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ManualNotifyTelegram = "telegram"
	ManualNotifyEmail    = "email"
	ManualNotifyBoth     = "both"
)

type ManualNotification struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	TargetType string `json:"target_type"`
	Channel    string `json:"channel"`
	CreatedBy  int64  `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
	Total      int64  `json:"total"`
	Pending    int64  `json:"pending"`
	Sent       int64  `json:"sent"`
	Failed     int64  `json:"failed"`
	Skipped    int64  `json:"skipped"`
}

type ManualNotificationRecipient struct {
	NotificationID int64  `json:"notification_id"`
	UserID         int64  `json:"user_id"`
	Username       string `json:"username"`
	Channel        string `json:"channel"`
	ChatID         int64  `json:"-"`
	Email          string `json:"email"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	SentAt         int64  `json:"sent_at"`
}

func NormalizeManualNotifyChannel(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case "", ManualNotifyTelegram:
		return ManualNotifyTelegram, nil
	case ManualNotifyEmail:
		return ManualNotifyEmail, nil
	case ManualNotifyBoth:
		return ManualNotifyBoth, nil
	default:
		return "", errors.New("invalid channel")
	}
}

func ManualNotifyChannels(channel string) []string {
	normalized, err := NormalizeManualNotifyChannel(channel)
	if err != nil {
		return nil
	}
	if normalized == ManualNotifyBoth {
		return []string{ManualNotifyTelegram, ManualNotifyEmail}
	}
	return []string{normalized}
}

// CreateManualNotification snapshots all eligible recipients in one transaction.
// targetType=all means every active non-admin user; selected validates and keeps
// only active non-admin users named by userIDs. Ineligible selected IDs are
// rejected so an admin cannot mistake a partial selection for a complete send.
// channel=telegram|email|both writes one delivery row per selected channel.
func (s *Store) CreateManualNotification(title, content, targetType string, userIDs []int64, createdBy int64) (*ManualNotification, error) {
	return s.CreateManualNotificationWithChannel(title, content, targetType, ManualNotifyTelegram, userIDs, createdBy)
}

func (s *Store) CreateManualNotificationWithChannel(title, content, targetType, channel string, userIDs []int64, createdBy int64) (*ManualNotification, error) {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" {
		return nil, errors.New("title required")
	}
	if targetType != "all" && targetType != "selected" {
		return nil, errors.New("invalid target type")
	}
	if targetType == "selected" && len(userIDs) == 0 {
		return nil, errors.New("recipients required")
	}
	channel, err := NormalizeManualNotifyChannel(channel)
	if err != nil {
		return nil, err
	}
	channels := ManualNotifyChannels(channel)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	res, err := tx.Exec(`INSERT INTO manual_notifications (title, content, target_type, channel, created_by, created_at) VALUES (?,?,?,?,?,?)`,
		title, content, targetType, channel, createdBy, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	q := `SELECT u.id, u.username, COALESCE(t.chat_id,0), COALESCE(u.email,'')
		FROM users u LEFT JOIN telegram_binds t ON t.user_id=u.id
		WHERE u.status='active' AND u.role<>'admin'`
	args := []any{}
	if targetType == "selected" {
		uniq := make([]int64, 0, len(userIDs))
		seen := map[int64]bool{}
		for _, userID := range userIDs {
			if userID > 0 && !seen[userID] {
				seen[userID] = true
				uniq = append(uniq, userID)
			}
		}
		if len(uniq) == 0 {
			return nil, errors.New("recipients required")
		}
		marks := make([]string, len(uniq))
		for i, userID := range uniq {
			marks[i] = "?"
			args = append(args, userID)
		}
		q += ` AND u.id IN (` + strings.Join(marks, ",") + `)`
		userIDs = uniq
	}
	q += ` ORDER BY u.id`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	type recipient struct {
		id, chat int64
		username string
		email    string
	}
	var recipients []recipient
	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.id, &r.username, &r.chat, &r.email); err != nil {
			rows.Close()
			return nil, err
		}
		r.email = strings.TrimSpace(r.email)
		recipients = append(recipients, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if targetType == "selected" && len(recipients) != len(userIDs) {
		return nil, errors.New("部分用户不存在、已禁用或为管理员")
	}
	for _, r := range recipients {
		for _, ch := range channels {
			status, reason := "pending", ""
			switch ch {
			case ManualNotifyTelegram:
				if r.chat == 0 {
					status, reason = "skipped", "未绑定 Telegram"
				}
			case ManualNotifyEmail:
				if r.email == "" {
					status, reason = "skipped", "未绑定邮箱"
				}
			}
			if _, err := tx.Exec(`INSERT INTO manual_notification_recipients
				(notification_id,user_id,username,channel,chat_id,email,status,error,sent_at) VALUES (?,?,?,?,?,?,?,?,0)`,
				id, r.id, r.username, ch, r.chat, r.email, status, reason); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ManualNotificationByID(id)
}

func scanManualNotification(sc scanner) (*ManualNotification, error) {
	var n ManualNotification
	err := sc.Scan(&n.ID, &n.Title, &n.Content, &n.TargetType, &n.Channel, &n.CreatedBy, &n.CreatedAt,
		&n.Total, &n.Pending, &n.Sent, &n.Failed, &n.Skipped)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &n, err
}

const manualNotificationSelect = `SELECT n.id,n.title,n.content,n.target_type,n.channel,n.created_by,n.created_at,
	COUNT(r.user_id),
	COALESCE(SUM(CASE WHEN r.status IN ('pending','sending') THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN r.status='sent' THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN r.status='failed' THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN r.status='skipped' THEN 1 ELSE 0 END),0)
	FROM manual_notifications n LEFT JOIN manual_notification_recipients r ON r.notification_id=n.id`

func (s *Store) ManualNotificationByID(id int64) (*ManualNotification, error) {
	return scanManualNotification(s.db.QueryRow(manualNotificationSelect+` WHERE n.id=? GROUP BY n.id`, id))
}

func (s *Store) ListManualNotifications(limit int) ([]*ManualNotification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(manualNotificationSelect+` GROUP BY n.id ORDER BY n.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ManualNotification{}
	for rows.Next() {
		n, err := scanManualNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListManualNotificationRecipients(notificationID int64) ([]*ManualNotificationRecipient, error) {
	rows, err := s.db.Query(`SELECT notification_id,user_id,username,channel,chat_id,email,status,error,sent_at
		FROM manual_notification_recipients WHERE notification_id=? ORDER BY user_id, channel`, notificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ManualNotificationRecipient{}
	for rows.Next() {
		var r ManualNotificationRecipient
		if err := rows.Scan(&r.NotificationID, &r.UserID, &r.Username, &r.Channel, &r.ChatID, &r.Email, &r.Status, &r.Error, &r.SentAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ClaimManualNotificationRecipient atomically moves one item into sending.
// This prevents two workers (or a restart scan racing a just-created goroutine)
// from delivering the same pending row.
func (s *Store) ClaimManualNotificationRecipient(notificationID int64) (*ManualNotificationRecipient, error) {
	var r ManualNotificationRecipient
	err := s.db.QueryRow(`UPDATE manual_notification_recipients SET status='sending'
		WHERE rowid=(
			SELECT rowid FROM manual_notification_recipients
			WHERE notification_id=? AND status='pending' ORDER BY user_id, channel LIMIT 1
		) AND status='pending'
		RETURNING notification_id,user_id,username,channel,chat_id,email,status,error,sent_at`, notificationID).
		Scan(&r.NotificationID, &r.UserID, &r.Username, &r.Channel, &r.ChatID, &r.Email, &r.Status, &r.Error, &r.SentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) SetManualNotificationRecipientResult(notificationID, userID int64, status, reason string) error {
	return s.SetManualNotificationRecipientChannelResult(notificationID, userID, ManualNotifyTelegram, status, reason)
}

func (s *Store) SetManualNotificationRecipientChannelResult(notificationID, userID int64, channel, status, reason string) error {
	if status != "sent" && status != "failed" && status != "skipped" {
		return fmt.Errorf("invalid notification status %q", status)
	}
	channel, err := NormalizeManualNotifyChannel(channel)
	if err != nil || channel == ManualNotifyBoth {
		return errors.New("invalid channel")
	}
	sentAt := int64(0)
	if status == "sent" {
		sentAt = time.Now().Unix()
	}
	res, err := s.db.Exec(`UPDATE manual_notification_recipients SET status=?,error=?,sent_at=?
		WHERE notification_id=? AND user_id=? AND channel=? AND status='sending'`,
		status, reason, sentAt, notificationID, userID, channel)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("notification recipient is not sending")
	}
	return nil
}

// FailInterruptedManualNotifications preserves an honest history after restart.
// Telegram has no caller-supplied idempotency key, so a row left in sending may
// already have reached Telegram; retrying it could duplicate the message. Email
// is treated the same way: SMTP has no send-id we can safely replay.
func (s *Store) FailInterruptedManualNotifications() error {
	_, err := s.db.Exec(`UPDATE manual_notification_recipients
		SET status='failed', error='服务重启，实际投递状态未知', sent_at=0
		WHERE status='sending'`)
	return err
}

func (s *Store) ListPendingManualNotificationIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT DISTINCT notification_id FROM manual_notification_recipients
		WHERE status='pending' ORDER BY notification_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
