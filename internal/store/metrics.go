package store

import (
	"database/sql"
	"errors"
	"time"
)

// ServerMetrics holds one snapshot of system metrics reported by a probe agent.
type ServerMetrics struct {
	ID             int64   `json:"id"`
	ServerID       int64   `json:"server_id"`
	Ts             int64   `json:"ts"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemUsed        int64   `json:"mem_used"`
	MemTotal       int64   `json:"mem_total"`
	SwapUsed       int64   `json:"swap_used"`
	SwapTotal      int64   `json:"swap_total"`
	DiskUsed       int64   `json:"disk_used"`
	DiskTotal      int64   `json:"disk_total"`
	NetRx          int64   `json:"net_rx"`
	NetTx          int64   `json:"net_tx"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	TCPConnections int     `json:"tcp_connections"`
	ProcessCount   int     `json:"process_count"`
	Uptime         int64   `json:"uptime"`
	Hostname       string  `json:"hostname"`
	Platform       string  `json:"platform"`
	Kernel         string  `json:"kernel"`
	Arch           string  `json:"arch"`
}

const metricsCols = `id, server_id, ts, cpu_percent, mem_used, mem_total, swap_used, swap_total, disk_used, disk_total, net_rx, net_tx, load1, load5, load15, tcp_connections, process_count, uptime, hostname, platform, kernel, arch`

func scanMetrics(sc scanner) (*ServerMetrics, error) {
	var m ServerMetrics
	err := sc.Scan(&m.ID, &m.ServerID, &m.Ts, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
		&m.SwapUsed, &m.SwapTotal, &m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx,
		&m.Load1, &m.Load5, &m.Load15, &m.TCPConnections, &m.ProcessCount, &m.Uptime,
		&m.Hostname, &m.Platform, &m.Kernel, &m.Arch)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// InsertMetrics writes one metrics snapshot.
func (s *Store) InsertMetrics(serverID int64, m ServerMetrics) error {
	now := time.Now().Unix()
	if m.Ts == 0 {
		m.Ts = now
	}
	_, err := s.db.Exec(`INSERT INTO server_metrics
		(server_id, ts, cpu_percent, mem_used, mem_total, swap_used, swap_total,
		 disk_used, disk_total, net_rx, net_tx, load1, load5, load15,
		 tcp_connections, process_count, uptime, hostname, platform, kernel, arch)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		serverID, m.Ts, m.CPUPercent, m.MemUsed, m.MemTotal, m.SwapUsed, m.SwapTotal,
		m.DiskUsed, m.DiskTotal, m.NetRx, m.NetTx, m.Load1, m.Load5, m.Load15,
		m.TCPConnections, m.ProcessCount, m.Uptime, m.Hostname, m.Platform, m.Kernel, m.Arch)
	return err
}

// GetLatestMetrics returns the most recent metrics row for a server, or nil.
func (s *Store) GetLatestMetrics(serverID int64) (*ServerMetrics, error) {
	return scanMetrics(s.db.QueryRow(`SELECT `+metricsCols+` FROM server_metrics WHERE server_id=? ORDER BY ts DESC LIMIT 1`, serverID))
}

// ListMetrics returns metrics for a server since the given Unix timestamp.
func (s *Store) ListMetrics(serverID int64, since int64) ([]*ServerMetrics, error) {
	rows, err := s.db.Query(`SELECT `+metricsCols+` FROM server_metrics WHERE server_id=? AND ts>=? ORDER BY ts`, serverID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServerMetrics
	for rows.Next() {
		m, err := scanMetrics(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PruneMetrics deletes metrics older than keepDays.
func (s *Store) PruneMetrics(keepDays int) error {
	threshold := time.Now().AddDate(0, 0, -keepDays).Unix()
	_, err := s.db.Exec(`DELETE FROM server_metrics WHERE ts < ?`, threshold)
	return err
}

// CountProbeServers returns total, online (last_seen within 2min), and expiring
// (within 3 days) counts for probe-enabled servers.
func (s *Store) CountProbeServers() (total, online, expiring int, err error) {
	onlineWindow := time.Now().Add(-2 * time.Minute).Unix()
	expiringWindow := time.Now().AddDate(0, 0, 3).Unix()
	now := time.Now().Unix()

	err = s.db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN last_seen >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN expiry_date > 0 AND expiry_date <= ? THEN 1 ELSE 0 END), 0)
		FROM servers WHERE probe_enabled=1`, onlineWindow, expiringWindow).Scan(&total, &online, &expiring)
	_ = now
	return
}

// ListProbeServersWithMetrics returns all probe-enabled servers joined with
// their latest metrics row (if any). Used by the dashboard and server list.
type ProbeServerView struct {
	Server  *Server         `json:"server"`
	Metrics *ServerMetrics  `json:"metrics"`
	DaysLeft *int           `json:"days_left"` // nil = no expiry set
}

func (s *Store) ListProbeServersWithMetrics() ([]ProbeServerView, error) {
	servers, err := s.ListServers()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []ProbeServerView
	for _, sv := range servers {
		if !sv.ProbeEnabled {
			continue
		}
		m, _ := s.GetLatestMetrics(sv.ID)
		var dl *int
		if sv.ExpiryDate > 0 {
			d := int(time.Unix(sv.ExpiryDate, 0).Sub(now).Hours() / 24)
			if d < 0 {
				d = 0
			}
			dl = &d
		}
		out = append(out, ProbeServerView{Server: sv, Metrics: m, DaysLeft: dl})
	}
	return out, nil
}
