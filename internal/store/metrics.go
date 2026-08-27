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
	ProbeVersion   string  `json:"probe_version"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemUsed        int64   `json:"mem_used"`
	MemTotal       int64   `json:"mem_total"`
	SwapUsed       int64   `json:"swap_used"`
	SwapTotal      int64   `json:"swap_total"`
	DiskUsed       int64   `json:"disk_used"`
	DiskTotal      int64   `json:"disk_total"`
	NetRx          int64   `json:"net_rx"`
	NetTx          int64   `json:"net_tx"`
	NetRxTotal     int64   `json:"net_rx_total"`
	NetTxTotal     int64   `json:"net_tx_total"`
	NetTotalsValid bool    `json:"net_totals_valid"`
	// Derived server-side from successive totals; never accepted from a probe or
	// exposed as a raw metric (range summaries publish the aggregate instead).
	NetRxBytes     int64   `json:"-"`
	NetTxBytes     int64   `json:"-"`
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

const metricsCols = `id, server_id, ts, probe_version, cpu_percent, mem_used, mem_total, swap_used, swap_total, disk_used, disk_total, net_rx, net_tx, net_rx_total, net_tx_total, net_totals_valid, net_rx_bytes, net_tx_bytes, load1, load5, load15, tcp_connections, process_count, uptime, hostname, platform, kernel, arch`

func scanMetrics(sc scanner) (*ServerMetrics, error) {
	var m ServerMetrics
	err := sc.Scan(&m.ID, &m.ServerID, &m.Ts, &m.ProbeVersion, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
		&m.SwapUsed, &m.SwapTotal, &m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx,
		&m.NetRxTotal, &m.NetTxTotal, &m.NetTotalsValid,
		&m.NetRxBytes, &m.NetTxBytes,
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

// clamp bounds reported metrics to sane ranges so a misbehaving or hostile probe
// (token-authenticated but otherwise untrusted) can't skew dashboard aggregates
// and heatmap classification with negative or impossible values.
func (m *ServerMetrics) clamp() {
	nn := func(v int64) int64 {
		if v < 0 {
			return 0
		}
		return v
	}
	if m.CPUPercent < 0 {
		m.CPUPercent = 0
	} else if m.CPUPercent > 100 {
		m.CPUPercent = 100
	}
	m.MemUsed, m.MemTotal = nn(m.MemUsed), nn(m.MemTotal)
	m.SwapUsed, m.SwapTotal = nn(m.SwapUsed), nn(m.SwapTotal)
	m.DiskUsed, m.DiskTotal = nn(m.DiskUsed), nn(m.DiskTotal)
	m.NetRx, m.NetTx = nn(m.NetRx), nn(m.NetTx)
	m.NetRxTotal, m.NetTxTotal = nn(m.NetRxTotal), nn(m.NetTxTotal)
	m.NetRxBytes, m.NetTxBytes = nn(m.NetRxBytes), nn(m.NetTxBytes)
	if m.MemTotal > 0 && m.MemUsed > m.MemTotal {
		m.MemUsed = m.MemTotal
	}
	if m.SwapTotal > 0 && m.SwapUsed > m.SwapTotal {
		m.SwapUsed = m.SwapTotal
	}
	if m.DiskTotal > 0 && m.DiskUsed > m.DiskTotal {
		m.DiskUsed = m.DiskTotal
	}
	if m.TCPConnections < 0 {
		m.TCPConnections = 0
	}
	if m.ProcessCount < 0 {
		m.ProcessCount = 0
	}
}

// InsertMetrics writes one metrics snapshot.
func (s *Store) InsertMetrics(serverID int64, m ServerMetrics) error {
	now := time.Now().Unix()
	if m.Ts == 0 {
		m.Ts = now
	}
	if len(m.ProbeVersion) > 64 {
		m.ProbeVersion = m.ProbeVersion[:64]
	}
	m.clamp() // reject nonsense values from a misbehaving/hostile probe
	// These are trusted derived fields, not part of the probe wire format.
	m.NetRxBytes, m.NetTxBytes = 0, 0
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Turn raw cumulative counters into a per-row delta once, at ingestion. The
	// dashboard can then SUM an indexed month instead of re-running a window over
	// every historical sample every 30 seconds.
	if m.NetTotalsValid {
		var prevRx, prevTx, prevUptime int64
		var prevValid bool
		err := tx.QueryRow(`SELECT net_rx_total, net_tx_total, net_totals_valid, uptime
			FROM server_metrics WHERE server_id=? ORDER BY id DESC LIMIT 1`, serverID).
			Scan(&prevRx, &prevTx, &prevValid, &prevUptime)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && prevValid {
			rebooted := m.Uptime < prevUptime
			if m.NetRxTotal >= prevRx {
				m.NetRxBytes = m.NetRxTotal - prevRx
			} else if rebooted {
				m.NetRxBytes = m.NetRxTotal
			}
			if m.NetTxTotal >= prevTx {
				m.NetTxBytes = m.NetTxTotal - prevTx
			} else if rebooted {
				m.NetTxBytes = m.NetTxTotal
			}
		}
	}

	_, err = tx.Exec(`INSERT INTO server_metrics
		(server_id, ts, probe_version, cpu_percent, mem_used, mem_total, swap_used, swap_total,
		 disk_used, disk_total, net_rx, net_tx, net_rx_total, net_tx_total, net_totals_valid, net_rx_bytes, net_tx_bytes,
		 load1, load5, load15, tcp_connections, process_count, uptime, hostname, platform, kernel, arch)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		serverID, m.Ts, m.ProbeVersion, m.CPUPercent, m.MemUsed, m.MemTotal, m.SwapUsed, m.SwapTotal,
		m.DiskUsed, m.DiskTotal, m.NetRx, m.NetTx, m.NetRxTotal, m.NetTxTotal, m.NetTotalsValid,
		m.NetRxBytes, m.NetTxBytes,
		m.Load1, m.Load5, m.Load15,
		m.TCPConnections, m.ProcessCount, m.Uptime, m.Hostname, m.Platform, m.Kernel, m.Arch)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ServerTrafficUsage is physical-interface traffic observed over a time range.
// Rx and Tx stay separate because providers differ on whether they bill one
// direction or both. Total is always Rx+Tx and therefore answers the common
// "IN+OUT" quota view directly.
type ServerTrafficUsage struct {
	Rx            int64 `json:"rx"`
	Tx            int64 `json:"tx"`
	Total         int64 `json:"total"`
	CoverageStart int64 `json:"coverage_start"`
	CoverageEnd   int64 `json:"coverage_end"`
	SampleCount   int64 `json:"sample_count"`
}

// TrafficUsageForAllSince sums the per-report deltas derived from cumulative NIC
// counters. It intentionally does not integrate sampled B/s rates: missing or
// delayed reports would make that estimate drift.
func (s *Store) TrafficUsageForAllSince(since int64) (map[int64]ServerTrafficUsage, error) {
	rows, err := s.db.Query(`SELECT server_id, COALESCE(SUM(net_rx_bytes),0),
		COALESCE(SUM(net_tx_bytes),0), MIN(ts), MAX(ts), COUNT(*)
		FROM server_metrics WHERE ts>=? AND net_totals_valid=1 GROUP BY server_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]ServerTrafficUsage)
	for rows.Next() {
		var id int64
		var u ServerTrafficUsage
		if err := rows.Scan(&id, &u.Rx, &u.Tx, &u.CoverageStart, &u.CoverageEnd, &u.SampleCount); err != nil {
			return nil, err
		}
		u.Total = u.Rx + u.Tx
		out[id] = u
	}
	return out, rows.Err()
}

// GetLatestMetrics returns the most recent metrics row for a server, or nil.
func (s *Store) GetLatestMetrics(serverID int64) (*ServerMetrics, error) {
	return scanMetrics(s.db.QueryRow(`SELECT `+metricsCols+` FROM server_metrics WHERE server_id=? ORDER BY ts DESC LIMIT 1`, serverID))
}

// GetLatestMetricsForAll returns each server's most recent metrics row keyed by
// server_id, in a SINGLE query — replacing loops that called GetLatestMetrics once
// per server (an N+1, including on the unauthenticated public monitor endpoints).
// The latest row per server is the one with the greatest id (ids are monotonic
// with insertion, so newest-inserted == newest metrics).
func (s *Store) GetLatestMetricsForAll() (map[int64]*ServerMetrics, error) {
	rows, err := s.db.Query(`SELECT ` + metricsCols + ` FROM server_metrics
		WHERE id IN (SELECT MAX(id) FROM server_metrics GROUP BY server_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*ServerMetrics{}
	for rows.Next() {
		m, err := scanMetrics(rows)
		if err != nil {
			return nil, err
		}
		if m != nil {
			out[m.ServerID] = m
		}
	}
	return out, rows.Err()
}

// ListMetrics returns metrics for a server since the given Unix timestamp. The
// row count is capped (most-recent-first within the window, returned in
// chronological order) so a long range on a high-frequency probe can't serialize
// hundreds of thousands of rows into a single response.
func (s *Store) ListMetrics(serverID int64, since int64) ([]*ServerMetrics, error) {
	const maxMetricsRows = 5000
	rows, err := s.db.Query(`SELECT `+metricsCols+` FROM (
		SELECT `+metricsCols+` FROM server_metrics WHERE server_id=? AND ts>=? ORDER BY ts DESC LIMIT ?
	) ORDER BY ts`, serverID, since, maxMetricsRows)
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

// maxHeatmapRows caps ListAllMetricsSince. Its callers are reachable
// unauthenticated (/api/monitor/heatmap, /api/monitor/public/sparklines) and
// accept a range as wide as 7d; at the default 30s probe interval that is ~2880
// rows per server per day, so an uncapped query lets an anonymous request
// materialise hundreds of thousands of rows on a 1H1G box. Everything is then
// collapsed into a fixed-width matrix anyway, so the tail is discarded.
const maxHeatmapRows = 200000

// ListAllMetricsSince returns metrics rows across all servers with ts>=since,
// ordered by server_id then ts, capped at maxHeatmapRows. Used by the heatmap
// aggregator.
func (s *Store) ListAllMetricsSince(since int64) ([]*ServerMetrics, error) {
	rows, err := s.db.Query(`SELECT `+metricsCols+` FROM server_metrics WHERE ts>=? ORDER BY server_id, ts LIMIT ?`, since, maxHeatmapRows)
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

// CountProbeServers returns total, online (last_seen within 2min), and expiring
// (within 3 days) counts for probe-enabled servers.
func (s *Store) CountProbeServers() (total, online, expiring int, err error) {
	return s.CountProbeServersSince(time.Now().Add(-2 * time.Minute).Unix())
}

// CountProbeServersSince is CountProbeServers with a caller-selected freshness
// cutoff. The API uses the live probe cadence; keeping CountProbeServers as the
// compatibility wrapper avoids spreading configuration concerns into callers
// that only need the historical two-minute default.
func (s *Store) CountProbeServersSince(onlineWindow int64) (total, online, expiring int, err error) {
	expiringWindow := time.Now().AddDate(0, 0, 3).Unix()
	now := time.Now().Unix()

	err = s.db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN last_seen >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN expiry_date > ? AND expiry_date <= ? THEN 1 ELSE 0 END), 0)
		FROM servers WHERE probe_enabled=1`, onlineWindow, now, expiringWindow).Scan(&total, &online, &expiring)
	return
}

// ListProbeServersWithMetrics returns all probe-enabled servers joined with
// their latest metrics row (if any). Used by the dashboard and server list.
type ProbeServerView struct {
	Server   *Server        `json:"server"`
	Metrics  *ServerMetrics `json:"metrics"`
	DaysLeft *int           `json:"days_left"` // nil = no expiry set
}

func (s *Store) ListProbeServersWithMetrics() ([]ProbeServerView, error) {
	servers, err := s.ListServers()
	if err != nil {
		return nil, err
	}
	latest, _ := s.GetLatestMetricsForAll() // one query instead of one per server
	now := time.Now()
	var out []ProbeServerView
	for _, sv := range servers {
		if !sv.ProbeEnabled {
			continue
		}
		m := latest[sv.ID]
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
