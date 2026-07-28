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
	DecryptFailed bool `json:"-"`
	// TLSEnabled wraps the hop to the proxy in TLS — an "HTTPS proxy", where the
	// CONNECT exchange and the credentials below travel inside a TLS session
	// instead of in the clear. sing-box offers this on its http outbound only
	// (its socks outbound has no tls option), so the admin API rejects the
	// tls+socks combination rather than silently emitting a config that ignores
	// the flag.
	TLSEnabled bool `json:"tls_enabled"`
	// SNI is the name sent in the handshake and checked against the proxy's
	// certificate. Empty falls back to Host — which fails when Host is a bare IP
	// and the cert has no IP SAN (the usual shape of a domain-fronted proxy).
	SNI string `json:"sni"`
	// TLSCertID pins a managed certificate (certificates.id) as the TRUST ANCHOR
	// for that handshake, instead of the node's system root store. We are the
	// client on this hop: the cert is used to verify the proxy, never presented
	// to it, and its private-key half is unused (sing-box outbounds have no
	// client-certificate option). Only meaningful when the upstream proxy is
	// one you run yourself with a cert from the panel's certificate center —
	// self-signed included. 0 = system roots, which is what a commercial proxy
	// holding a publicly-trusted cert needs.
	TLSCertID int64 `json:"tls_cert_id"`
	// TLSInsecure skips verification entirely. Diagnostics only: the proxy
	// credentials ride inside this session, so anyone able to intercept it can
	// take them and reuse the egress.
	TLSInsecure bool  `json:"tls_insecure"`
	CreatedAt   int64 `json:"created_at"`
	UpdatedAt   int64 `json:"updated_at"`
}

const egressCols = `id, name, type, host, port, username, password, tls_enabled, sni, tls_cert_id, tls_insecure, created_at, updated_at`

func (s *Store) scanEgress(row interface{ Scan(...any) error }) (*SbEgress, error) {
	var e SbEgress
	var tlsEnabled, tlsInsecure int
	if err := row.Scan(&e.ID, &e.Name, &e.Type, &e.Host, &e.Port, &e.Username, &e.Password,
		&tlsEnabled, &e.SNI, &e.TLSCertID, &tlsInsecure, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	e.TLSEnabled = tlsEnabled != 0
	e.TLSInsecure = tlsInsecure != 0
	var ok bool
	e.Password, ok = s.decryptOK(e.Password)
	e.DecryptFailed = !ok
	return &e, nil
}

func (s *Store) ListSbEgresses() ([]*SbEgress, error) {
	rows, err := s.db.Query(`SELECT ` + egressCols + ` FROM sb_egresses ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SbEgress{}
	for rows.Next() {
		e, err := s.scanEgress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetSbEgress(id int64) (*SbEgress, error) {
	e, err := s.scanEgress(s.db.QueryRow(`SELECT `+egressCols+` FROM sb_egresses WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// SaveSbEgress inserts (id==0) or updates a proxy egress. Password is encrypted.
func (s *Store) SaveSbEgress(e *SbEgress) (int64, error) {
	now := time.Now().Unix()
	enc := s.encrypt(e.Password)
	if e.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO sb_egresses
			(name, type, host, port, username, password, tls_enabled, sni, tls_cert_id, tls_insecure, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.Name, e.Type, e.Host, e.Port, e.Username, enc,
			b2i(e.TLSEnabled), e.SNI, e.TLSCertID, b2i(e.TLSInsecure), now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE sb_egresses SET name=?, type=?, host=?, port=?, username=?, password=?,
		tls_enabled=?, sni=?, tls_cert_id=?, tls_insecure=?, updated_at=? WHERE id=?`,
		e.Name, e.Type, e.Host, e.Port, e.Username, enc,
		b2i(e.TLSEnabled), e.SNI, e.TLSCertID, b2i(e.TLSInsecure), now, e.ID)
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
