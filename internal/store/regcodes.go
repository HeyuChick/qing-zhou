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
	Uses      []RegCodeUse `json:"uses"`      // who consumed it (registration records)
	GroupIDs  []int64      `json:"group_ids"` // user groups the redeemer joins (not a column)
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
	// attach granted user groups
	grows, err := s.db.Query(`SELECT code_id, group_id FROM reg_code_user_groups ORDER BY group_id`)
	if err != nil {
		return out, nil
	}
	defer grows.Close()
	for grows.Next() {
		var codeID, gid int64
		if err := grows.Scan(&codeID, &gid); err != nil {
			continue
		}
		if c := byID[codeID]; c != nil {
			c.GroupIDs = append(c.GroupIDs, gid)
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

// UserUsedRegCode reports whether this account was created with an invite
// code. Those users are allowed to skip email verification — the code is the
// admission ticket — so the subscription gate must not treat them as
// pending-verify signups.
func (s *Store) UserUsedRegCode(userID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM reg_code_uses WHERE user_id=?`, userID).Scan(&n)
	return n > 0, err
}

// GenerateRegCodes creates count codes with the given per-code usage cap. Every
// code in the batch grants the same user groups on redemption (groupIDs may be
// empty, meaning the code grants no group).
func (s *Store) GenerateRegCodes(count int, maxUses int64, note string, groupIDs []int64) ([]string, error) {
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
		res, err := s.db.Exec(`INSERT INTO reg_codes (code, max_uses, used, enabled, note, created_at)
			VALUES (?,?,0,1,?,?)`, code, maxUses, note, now)
		if err != nil {
			continue // collision (rare) — skip
		}
		// A code that was asked to grant groups but doesn't is worse than no
		// code: the admin hands it out believing it unlocks their packages, and
		// the redeemer silently gets nothing. Drop the code and report instead.
		if len(groupIDs) > 0 {
			id, err := res.LastInsertId()
			if err != nil {
				return codes, err
			}
			if err := s.SetRegCodeUserGroups(id, groupIDs); err != nil {
				_, _ = s.db.Exec(`DELETE FROM reg_codes WHERE id=?`, id)
				return codes, err
			}
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// RegCodeRedeemable reports whether a code currently has a free slot, WITHOUT
// consuming one — a fast pre-check so registration can reject an invalid code
// before creating anything. The authoritative, atomic decrement is still
// ConsumeRegCode, which must be called (and its result re-checked) once the
// account is durably created, so a slot is never burned on a failed signup.
func (s *Store) RegCodeRedeemable(code string) (int64, bool) {
	code = strings.TrimSpace(strings.ToUpper(code))
	if code == "" {
		return 0, false
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM reg_codes WHERE code=? AND enabled=1 AND (max_uses<=0 OR used<max_uses)`, code).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM reg_code_user_groups WHERE code_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reg_codes WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
