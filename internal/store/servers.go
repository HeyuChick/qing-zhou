package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// hashProbeToken returns the hex SHA-256 of a probe token, used as the lookup
// key so the token itself can be stored encrypted at rest.
func hashProbeToken(tok string) string {
	if tok == "" {
		return ""
	}
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

type Server struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Host         string  `json:"host"`
	Port         int     `json:"port"`
	SSHUser      string  `json:"ssh_user"`
	SSHKey       string  `json:"ssh_key"`
	SSHKeyPass   string  `json:"ssh_key_pass"`
	SSHPassword  string  `json:"ssh_password"`
	ConfigPath   string  `json:"config_path"`
	SystemdUnit  string  `json:"systemd_unit"`
	SingBoxBin   string  `json:"sing_box_bin"`
	V2rayListen  string  `json:"v2ray_listen"`
	Enabled      bool    `json:"enabled"`
	Status       string  `json:"status"`
	LastSeen     int64   `json:"last_seen"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
	// Monitor probe fields
	ProbeEnabled bool    `json:"probe_enabled"`
	ProbeToken   string  `json:"probe_token"`
	ExpiryDate   int64   `json:"expiry_date"`
	Provider     string  `json:"provider"`
	Location     string  `json:"location"`
	Spec         string  `json:"spec"`
	Price        float64 `json:"price"`
	Notes        string  `json:"notes"`
	// HostKey is the pinned SSH host key (authorized_keys line). Empty until the
	// first successful connection pins it (TOFU). Never exposed to the client.
	HostKey string `json:"-"`
}

const serverCols = `id, name, host, port, ssh_user, ssh_key, ssh_key_pass, ssh_password, config_path, systemd_unit, sing_box_bin, v2ray_listen, enabled, status, last_seen, created_at, updated_at, probe_enabled, probe_token, expiry_date, provider, location, spec, price, notes, host_key`

func (s *Store) ListServers() ([]*Server, error) {
	rows, err := s.db.Query(`SELECT ` + serverCols + ` FROM servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Server
	for rows.Next() {
		sv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		s.decryptServer(sv)
		out = append(out, sv)
	}
	return out, rows.Err()
}

func (s *Store) GetServer(id int64) (*Server, error) {
	sv, err := scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE id=?`, id))
	if sv != nil {
		s.decryptServer(sv)
	}
	return sv, err
}

func (s *Store) CreateServer(sv Server) (int64, error) {
	now := time.Now().Unix()
	if sv.Port == 0 {
		sv.Port = 22
	}
	if sv.SSHUser == "" {
		sv.SSHUser = "root"
	}
	if sv.ConfigPath == "" {
		sv.ConfigPath = "/etc/sing-box/config.json"
	}
	if sv.SystemdUnit == "" {
		sv.SystemdUnit = "sing-box"
	}
	// SingBoxBin left empty when not specified — the applier will auto-detect
	// the binary on the target host (local: FindSingBoxBin; remote: PATH lookup).
	if sv.V2rayListen == "" {
		sv.V2rayListen = "127.0.0.1:18080"
	}
	if sv.Status == "" {
		sv.Status = "unknown"
	}
	res, err := s.db.Exec(`INSERT INTO servers (name, host, port, ssh_user, ssh_key, ssh_key_pass, ssh_password, config_path, systemd_unit, sing_box_bin, v2ray_listen, enabled, status, last_seen, created_at, updated_at, probe_enabled, probe_token, probe_token_hash, expiry_date, provider, location, spec, price, notes)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
	sv.Name, sv.Host, sv.Port, sv.SSHUser, s.encrypt(sv.SSHKey), s.encrypt(sv.SSHKeyPass), s.encrypt(sv.SSHPassword),
		sv.ConfigPath, sv.SystemdUnit, sv.SingBoxBin, sv.V2rayListen,
		b2i(sv.Enabled), sv.Status, sv.LastSeen, now, now,
		b2i(sv.ProbeEnabled), s.encrypt(sv.ProbeToken), hashProbeToken(sv.ProbeToken), sv.ExpiryDate, sv.Provider, sv.Location, sv.Spec, sv.Price, sv.Notes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateServer(sv Server) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`UPDATE servers SET name=?, host=?, port=?, ssh_user=?, ssh_key=?, ssh_key_pass=?, ssh_password=?, config_path=?, systemd_unit=?, sing_box_bin=?, v2ray_listen=?, enabled=?, updated_at=?, probe_enabled=?, probe_token=?, probe_token_hash=?, expiry_date=?, provider=?, location=?, spec=?, price=?, notes=? WHERE id=?`,
	sv.Name, sv.Host, sv.Port, sv.SSHUser, s.encrypt(sv.SSHKey), s.encrypt(sv.SSHKeyPass), s.encrypt(sv.SSHPassword),
		sv.ConfigPath, sv.SystemdUnit, sv.SingBoxBin, sv.V2rayListen,
		b2i(sv.Enabled), now, b2i(sv.ProbeEnabled), s.encrypt(sv.ProbeToken), hashProbeToken(sv.ProbeToken), sv.ExpiryDate, sv.Provider, sv.Location, sv.Spec, sv.Price, sv.Notes, sv.ID)
	return err
}

func (s *Store) DeleteServer(id int64) error {
	// Refuse deletion while inbounds/TLS are still bound to this server, else
	// they become undeployable orphans (server_id points at nothing).
	var n int
	_ = s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sb_inbounds WHERE server_id=?) +
		(SELECT COUNT(*) FROM sb_tls WHERE server_id=?)`, id, id).Scan(&n)
	if n > 0 {
		return fmt.Errorf("%w：仍有 %d 个入站/TLS 绑定到此服务器，请先改绑或删除", ErrInUse, n)
	}
	if _, err := s.db.Exec(`DELETE FROM servers WHERE id=?`, id); err != nil {
		return err
	}
	// Drop the observed sing-box state with it; a row describing a server that
	// no longer exists would keep showing up in the node version list.
	return s.DeleteNodeSingbox(id)
}

func (s *Store) UpdateServerStatus(id int64, status string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`UPDATE servers SET status=?, last_seen=?, updated_at=? WHERE id=?`, status, now, now, id)
	return err
}

// GetServerByProbeToken finds a server by its probe token (used by the agent
// report endpoint). Returns nil, nil if not found.
func (s *Store) GetServerByProbeToken(token string) (*Server, error) {
	// Match on the token hash — the token is stored encrypted, so a DB leak no
	// longer yields usable bearer tokens for the report endpoint.
	sv, err := scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE probe_token_hash=? AND probe_enabled=1`, hashProbeToken(token)))
	if sv != nil {
		s.decryptServer(sv)
	}
	return sv, err
}

// SetServerHostKey pins (or updates) the SSH host key for a server. Called on
// first successful connect (TOFU) so subsequent connects are verified against it.
func (s *Store) SetServerHostKey(id int64, hostKey string) error {
	_, err := s.db.Exec(`UPDATE servers SET host_key=? WHERE id=?`, hostKey, id)
	return err
}

// TouchProbeSeen updates last_seen to now for a probe-enabled server.
func (s *Store) TouchProbeSeen(id int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`UPDATE servers SET last_seen=?, updated_at=? WHERE id=?`, now, now, id)
	return err
}

func scanServer(sc scanner) (*Server, error) {
	var sv Server
	var enabled, probeEnabled int
	err := sc.Scan(&sv.ID, &sv.Name, &sv.Host, &sv.Port, &sv.SSHUser, &sv.SSHKey, &sv.SSHKeyPass, &sv.SSHPassword,
		&sv.ConfigPath, &sv.SystemdUnit, &sv.SingBoxBin, &sv.V2rayListen,
		&enabled, &sv.Status, &sv.LastSeen, &sv.CreatedAt, &sv.UpdatedAt,
		&probeEnabled, &sv.ProbeToken, &sv.ExpiryDate, &sv.Provider, &sv.Location, &sv.Spec, &sv.Price, &sv.Notes, &sv.HostKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sv.Enabled = enabled == 1
	sv.ProbeEnabled = probeEnabled == 1
	return &sv, nil
}

func (s *Store) decryptServer(sv *Server) {
	sv.SSHKey = s.decrypt(sv.SSHKey)
	sv.SSHKeyPass = s.decrypt(sv.SSHKeyPass)
	sv.SSHPassword = s.decrypt(sv.SSHPassword)
	sv.ProbeToken = s.decrypt(sv.ProbeToken)
}
