package store

import (
	"crypto/rand"
	"strings"
	"time"
)

type RegCode struct {
	ID        int64        `json:"id"`
	Code      string       `json:"code"`
	MaxUses   int64        `json:"max_uses"` // 0 = unlimited
	Used      int64        `json:"used"`
	Enabled   bool         `json:"enabled"`
	Note      string       `json:"note"`
	CreatedAt int64        `json:"created_at"`
	Uses      []RegCodeUse `json:"uses"` // who consumed it (registration records)
}

// RegCodeUse is one consumption of a reg code by a registering user. The
// username/email are snapshotted so the record survives user deletion.
type RegCodeUse struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	UsedAt   int64  `json:"used_at"`
}

// unambiguous alphabet (no 0/O/1/I)
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func genRegCode(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out)
}

func (s *Store) ListRegCodes() ([]*RegCode, error) {
	rows, err := s.db.Query(`SELECT id, code, max_uses, used, enabled, note, created_at FROM reg_codes ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RegCode{}
	byID := map[int64]*RegCode{}
	for rows.Next() {
		var c RegCode
		if err := rows.Scan(&c.ID, &c.Code, &c.MaxUses, &c.Used, &c.Enabled, &c.Note, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
		byID[c.ID] = &c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// attach consumption records
	urows, err := s.db.Query(`SELECT code_id, user_id, username, email, used_at FROM reg_code_uses ORDER BY used_at`)
	if err != nil {
		return out, nil // codes are still useful without uses
	}
	defer urows.Close()
	for urows.Next() {
		var codeID int64
		var u RegCodeUse
		if err := urows.Scan(&codeID, &u.UserID, &u.Username, &u.Email, &u.UsedAt); err != nil {
			continue
		}
		if c := byID[codeID]; c != nil {
			c.Uses = append(c.Uses, u)
		}
	}
	return out, nil
}

// RecordRegCodeUse logs which user consumed a reg code, snapshotting the
// username/email so the record stands even if the user is later deleted.
func (s *Store) RecordRegCodeUse(codeID, userID int64, username, email string) error {
	_, err := s.db.Exec(`INSERT INTO reg_code_uses (code_id, user_id, username, email, used_at) VALUES (?,?,?,?,?)`,
		codeID, userID, username, email, time.Now().Unix())
	return err
}

// GenerateRegCodes creates count codes with the given per-code usage cap.
func (s *Store) GenerateRegCodes(count int, maxUses int64, note string) ([]string, error) {
	if count <= 0 {
		count = 1
	}
	if count > 200 {
		count = 200
	}
	now := time.Now().Unix()
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code := genRegCode(8)
		if _, err := s.db.Exec(`INSERT INTO reg_codes (code, max_uses, used, enabled, note, created_at)
			VALUES (?,?,0,1,?,?)`, code, maxUses, note, now); err != nil {
			continue // collision (rare) — skip
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// ConsumeRegCode atomically validates and uses one slot of a code. On success
// it returns the code's id (for recording who used it) and true.
func (s *Store) ConsumeRegCode(code string) (int64, bool) {
	code = strings.TrimSpace(strings.ToUpper(code))
	if code == "" {
		return 0, false
	}
	res, err := s.db.Exec(
		`UPDATE reg_codes SET used=used+1 WHERE code=? AND enabled=1 AND (max_uses<=0 OR used<max_uses)`, code)
	if err != nil {
		return 0, false
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, false
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM reg_codes WHERE code=?`, code).Scan(&id); err != nil {
		return 0, true
	}
	return id, true
}

func (s *Store) SetRegCodeEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE reg_codes SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	return err
}

func (s *Store) DeleteRegCode(id int64) error {
	_, err := s.db.Exec(`DELETE FROM reg_codes WHERE id=?`, id)
	return err
}
