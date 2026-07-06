package store

import (
	"database/sql"
	"errors"
	"time"
)

type Announcement struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Pinned    bool   `json:"pinned"`
	Enabled   bool   `json:"enabled"`
	StartAt   int64  `json:"start_at"` // 0 = no start limit
	EndAt     int64  `json:"end_at"`   // 0 = no end limit
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Read      bool   `json:"read,omitempty"` // populated for user listing
}

const annCols = `id, title, content, pinned, enabled, start_at, end_at, created_at, updated_at`

func scanAnn(sc scanner) (*Announcement, error) {
	var a Announcement
	err := sc.Scan(&a.ID, &a.Title, &a.Content, &a.Pinned, &a.Enabled, &a.StartAt, &a.EndAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAnnouncements returns all announcements (admin view).
func (s *Store) ListAnnouncements(enabledOnly bool) ([]*Announcement, error) {
	q := `SELECT ` + annCols + ` FROM announcements`
	if enabledOnly {
		q += ` WHERE enabled=1`
	}
	q += ` ORDER BY pinned DESC, id DESC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Announcement{}
	for rows.Next() {
		a, err := scanAnn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListActiveForUser returns enabled announcements currently within their
// show window, each flagged read/unread for the given user.
func (s *Store) ListActiveForUser(userID int64) ([]*Announcement, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT a.id, a.title, a.content, a.pinned, a.enabled, a.start_at, a.end_at,
		a.created_at, a.updated_at, (r.user_id IS NOT NULL) AS read
		FROM announcements a
		LEFT JOIN announcement_reads r ON r.announcement_id=a.id AND r.user_id=?
		WHERE a.enabled=1 AND (a.start_at=0 OR a.start_at<=?) AND (a.end_at=0 OR a.end_at>?)
		ORDER BY a.pinned DESC, a.id DESC`, userID, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Announcement{}
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.Pinned, &a.Enabled, &a.StartAt, &a.EndAt,
			&a.CreatedAt, &a.UpdatedAt, &a.Read); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// MarkRead records the given announcement ids as read for the user.
func (s *Store) MarkRead(userID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO announcement_reads (user_id, announcement_id, read_at) VALUES (?,?,?)`,
			userID, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAnnouncement(id int64) (*Announcement, error) {
	return scanAnn(s.db.QueryRow(`SELECT `+annCols+` FROM announcements WHERE id=?`, id))
}

func (s *Store) CreateAnnouncement(a Announcement) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO announcements (title, content, pinned, enabled, start_at, end_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, a.Title, a.Content, boolToInt(a.Pinned), boolToInt(a.Enabled), a.StartAt, a.EndAt, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateAnnouncement(a Announcement) error {
	_, err := s.db.Exec(`UPDATE announcements SET title=?, content=?, pinned=?, enabled=?, start_at=?, end_at=?, updated_at=? WHERE id=?`,
		a.Title, a.Content, boolToInt(a.Pinned), boolToInt(a.Enabled), a.StartAt, a.EndAt, time.Now().Unix(), a.ID)
	return err
}

func (s *Store) DeleteAnnouncement(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM announcement_reads WHERE announcement_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM announcements WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
