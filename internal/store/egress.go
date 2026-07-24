package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SbEgress is a third-party proxy egress (e.g. a purchased static-IP SOCKS5 or
// HTTP proxy). An inbound with EgressID set routes its traffic out through this
// proxy instead of exiting directly, so the exit IP becomes the proxy's IP.
// Password is stored encrypted at rest and returned decrypted from these methods.
type SbEgress struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // socks | http
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// DecryptFailed is set when Password was stored encrypted but could not be
	// decrypted (wrong/changed QZ_SECRET_KEY). The outbound is still emitted with
	// an empty password so traffic fails closed at the proxy instead of silently
	// falling through to a direct (wrong-IP) exit.
	DecryptFailed bool  `json:"-"`
	CreatedAt     int64 `json:"created_at"`
	UpdatedAt     int64 `json:"updated_at"`
}

func (s *Store) ListSbEgresses() ([]*SbEgress, error) {
	rows, err := s.db.Query(`SELECT id, name, type, host, port, username, password, created_at, updated_at
		FROM sb_egresses ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SbEgress{}
	for rows.Next() {
		var e SbEgress
		if err := rows.Scan(&e.ID, &e.Name, &e.Type, &e.Host, &e.Port, &e.Username, &e.Password, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		var ok bool
		e.Password, ok = s.decryptOK(e.Password)
		e.DecryptFailed = !ok
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) GetSbEgress(id int64) (*SbEgress, error) {
	var e SbEgress
	err := s.db.QueryRow(`SELECT id, name, type, host, port, username, password, created_at, updated_at
		FROM sb_egresses WHERE id=?`, id).Scan(&e.ID, &e.Name, &e.Type, &e.Host, &e.Port, &e.Username, &e.Password, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ok bool
	e.Password, ok = s.decryptOK(e.Password)
	e.DecryptFailed = !ok
	return &e, nil
}

// SaveSbEgress inserts (id==0) or updates a proxy egress. Password is encrypted.
func (s *Store) SaveSbEgress(e *SbEgress) (int64, error) {
	now := time.Now().Unix()
	enc := s.encrypt(e.Password)
	if e.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO sb_egresses (name, type, host, port, username, password, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?)`, e.Name, e.Type, e.Host, e.Port, e.Username, enc, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE sb_egresses SET name=?, type=?, host=?, port=?, username=?, password=?, updated_at=? WHERE id=?`,
		e.Name, e.Type, e.Host, e.Port, e.Username, enc, now, e.ID)
	return e.ID, err
}

func (s *Store) DeleteSbEgress(id int64) error {
	// Refuse deletion while an inbound still references this egress: silently
	// clearing it would flip a live inbound from the purchased static IP back to
	// a direct exit, changing its public exit IP without warning.
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM sb_inbounds WHERE egress_id=?`, id).Scan(&n)
	if n > 0 {
		return fmt.Errorf("%w：仍有 %d 个入站在使用此代理出口", ErrInUse, n)
	}
	_, err := s.db.Exec(`DELETE FROM sb_egresses WHERE id=?`, id)
	return err
}
