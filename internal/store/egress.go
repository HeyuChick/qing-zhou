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
	TLSInsecure bool `json:"tls_insecure"`
	// UDPMode decides what happens to UDP steered into this proxy:
	//
	//   passthrough — hand it to the proxy (SOCKS5 UDP ASSOCIATE) and hope it
	//                 relays. What every egress did before this field existed.
	//   block       — drop it at the route, before the outbound is reached.
	//
	// What "block" buys, stated precisely, because the obvious guess is wrong.
	// It does NOT make the client fail fast. Measured against sing-box 1.13.18: a
	// route rule {"network":"udp","action":"reject"} on a SOCKS5 inbound still
	// answers UDP ASSOCIATE with success (the reply is sent before any packet has
	// a destination to route on), and the packets that follow are then dropped
	// with nothing sent back — the client waits out its own timeout exactly as it
	// would against a black hole. There is no way to express a refusal to a
	// SOCKS5 UDP client, so the fast-fallback story does not hold.
	//
	// What it does buy is determinism. A vendor proxy that accepts UDP ASSOCIATE
	// and then relays it badly is worse than one that carries none: QUIC gets far
	// enough to be chosen and then stalls mid-stream, so the browser never falls
	// back at all. Blocking turns "sometimes" into "never", which every client
	// already knows how to handle. It also stops sing-box attempting — and
	// logging — a packet path that was never going to work.
	//
	// Fast fallback is the client's half of this, and it is now wired: an
	// inbound bound to a blocking egress renders its subscription entry as
	// UDP-incapable (singbox.LinkParams.NoUDP -> clash `udp: false`, sing-box
	// `"network": "tcp"`, Surge `udp-relay=false`), so the client refuses UDP
	// locally the moment an application asks — no timeout, no black hole. That
	// marker is per node, so nodes on other exits keep QUIC.
	//
	// A measured case, for calibration on what "relays it badly" looks like:
	// one vendor accepted UDP ASSOCIATE, kept sessions alive past 90s, held the
	// same exit IP as TCP, and relayed real datagrams — but capped a single
	// datagram at 512 bytes (the pre-EDNS DNS message size; their relay buffer
	// is DNS-shaped). RFC 9000 §14.1 pads a QUIC client Initial to ≥1200, so
	// every QUIC handshake through it was dropped with no ICMP and no error.
	// DNS was the one UDP service that worked, which is why hijackDNSRule
	// exists and why "UDP works, I tested it" is not evidence.
	//
	// "" means unset — resolved by EffectiveUDPMode from the type, because a
	// sensible default differs: sing-box's http outbound cannot carry UDP at all,
	// so blocking merely states what already happens; a socks proxy may genuinely
	// relay it, so passthrough stays the default there.
	UDPMode string `json:"udp_mode"`
	// ConnectTimeoutMS bounds the TCP connect to the proxy. 0 = the built-in
	// default (see EffectiveConnectTimeoutMS). Without it, a proxy that accepts
	// packets but never completes the handshake — the usual shape of "the
	// provider cut us off" — hangs every connection for the OS TCP timeout
	// (~2 min on Linux) instead of failing while a retry could still help.
	ConnectTimeoutMS int   `json:"connect_timeout_ms"`
	CreatedAt        int64 `json:"created_at"`
	UpdatedAt        int64 `json:"updated_at"`
}

// UDP mode values. Keep in sync with the admin API validation and the UI.
const (
	UDPModePassthrough = "passthrough"
	UDPModeBlock       = "block"
)

// ErrEgressUndecryptable is returned when an operation needs the egress's real
// password and the stored ciphertext can't be opened (QZ_SECRET_KEY changed).
// Handlers surface it as a 400 rather than a 500: it is a configuration state
// the admin can fix by re-entering the password, not a server fault.
var ErrEgressUndecryptable = errors.New("该出口的密码无法解密（QZ_SECRET_KEY 变更？），请重新编辑保存后再试")

// defaultEgressConnectTimeoutMS bounds the dial to the proxy. 5s is far past a
// healthy TCP handshake to anywhere on earth, so anything slower is a proxy
// that is down rather than one that is far away.
const defaultEgressConnectTimeoutMS = 5000

// EffectiveUDPMode resolves the stored UDPMode, including the "" = by-type case.
// Anything unrecognised falls back the same way, so a value written by a newer
// panel version can't turn into silently-wrong routing after a downgrade.
func (e *SbEgress) EffectiveUDPMode() string {
	switch e.UDPMode {
	case UDPModePassthrough, UDPModeBlock:
		return e.UDPMode
	}
	if e.Type == "http" {
		// sing-box's http outbound has no packet path at all; passthrough here
		// would just be a slower way of writing block.
		return UDPModeBlock
	}
	return UDPModePassthrough
}

// EffectiveConnectTimeoutMS resolves the stored value, 0 meaning "the default".
func (e *SbEgress) EffectiveConnectTimeoutMS() int {
	if e.ConnectTimeoutMS > 0 {
		return e.ConnectTimeoutMS
	}
	return defaultEgressConnectTimeoutMS
}

const egressCols = `id, name, type, host, port, username, password, tls_enabled, sni, tls_cert_id, tls_insecure, udp_mode, connect_timeout_ms, created_at, updated_at`

func (s *Store) scanEgress(row interface{ Scan(...any) error }) (*SbEgress, error) {
	var e SbEgress
	var tlsEnabled, tlsInsecure int
	if err := row.Scan(&e.ID, &e.Name, &e.Type, &e.Host, &e.Port, &e.Username, &e.Password,
		&tlsEnabled, &e.SNI, &e.TLSCertID, &tlsInsecure, &e.UDPMode, &e.ConnectTimeoutMS,
		&e.CreatedAt, &e.UpdatedAt); err != nil {
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
			(name, type, host, port, username, password, tls_enabled, sni, tls_cert_id, tls_insecure,
			 udp_mode, connect_timeout_ms, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.Name, e.Type, e.Host, e.Port, e.Username, enc,
			b2i(e.TLSEnabled), e.SNI, e.TLSCertID, b2i(e.TLSInsecure),
			e.UDPMode, e.ConnectTimeoutMS, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE sb_egresses SET name=?, type=?, host=?, port=?, username=?, password=?,
		tls_enabled=?, sni=?, tls_cert_id=?, tls_insecure=?, udp_mode=?, connect_timeout_ms=?, updated_at=? WHERE id=?`,
		e.Name, e.Type, e.Host, e.Port, e.Username, enc,
		b2i(e.TLSEnabled), e.SNI, e.TLSCertID, b2i(e.TLSInsecure),
		e.UDPMode, e.ConnectTimeoutMS, now, e.ID)
	return e.ID, err
}

// CloneSbEgress duplicates an egress under a fresh name and returns the new row.
//
// Deliberately a store method rather than a "read it, POST it back" round trip
// through the admin API: the list/get responses mask the password as "***", and
// SaveSbEgress only interprets that sentinel as "keep the stored value" when an
// ID is present. A clone has no ID, so a client-side copy would store the three
// literal asterisks as the password — an egress that looks right in the panel
// and answers 407 on the wire. Copying here keeps the plaintext server-side.
func (s *Store) CloneSbEgress(id int64) (*SbEgress, error) {
	src, err := s.GetSbEgress(id)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, nil
	}
	// Refuse rather than clone an unreadable password into a second row that
	// will fail the same way: the copy would look healthy in the panel (a
	// non-empty masked password) while failing closed at the proxy.
	if src.DecryptFailed {
		return nil, ErrEgressUndecryptable
	}
	cp := *src
	cp.ID = 0
	cp.Name = s.uniqueEgressName(src.Name)
	newID, err := s.SaveSbEgress(&cp)
	if err != nil {
		return nil, err
	}
	return s.GetSbEgress(newID)
}

// uniqueEgressName returns base + a 副本 suffix that no egress currently uses.
// Names aren't a key, so this is only about not handing the admin three rows
// called "静态IP-香港（副本）" that they then have to tell apart by id.
func (s *Store) uniqueEgressName(base string) string {
	taken := map[string]bool{}
	rows, err := s.db.Query(`SELECT name FROM sb_egresses`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				taken[n] = true
			}
		}
	}
	// The column is TEXT so SQLite won't truncate, but a runaway name is still
	// worth capping before it reaches every dropdown in the UI. Capped here on
	// the base rather than on the finished candidate: cutting afterwards would
	// take the （副本）suffix back off, so every attempt would collapse to the
	// same prefix and the loop below could never find a free name. By runes, not
	// bytes — these names are Chinese, and half a rune renders as garbage.
	base = truncateRunes(base, 60)
	for i := 1; ; i++ {
		cand := base + "（副本）"
		if i > 1 {
			cand = fmt.Sprintf("%s（副本 %d）", base, i)
		}
		if !taken[cand] {
			return cand
		}
	}
}

// truncateRunes cuts s to at most n runes, never mid-character.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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
