package store

import "fmt"

// ServerTrafficDay is one local calendar day's physical-interface deltas.
// Total follows the server's provider accounting mode; RX/TX remain available
// so the UI can explain the direction mix behind that total.
type ServerTrafficDay struct {
	Date  string `json:"date"`
	Rx    int64  `json:"rx"`
	Tx    int64  `json:"tx"`
	Total int64  `json:"total"`
}

type ServerTrafficSource struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Total    int64  `json:"total"`
}

type ServerTrafficAttribution struct {
	CoverageStart int64                 `json:"coverage_start"`
	CoverageEnd   int64                 `json:"coverage_end"`
	ActiveUsers   int                   `json:"active_users"`
	Total         int64                 `json:"total"`
	Sources       []ServerTrafficSource `json:"sources"`
}

func (s *Store) ServerTrafficDaily(serverID, since int64, accountingMode string) ([]ServerTrafficDay, error) {
	rows, err := s.db.Query(`SELECT strftime('%Y-%m-%d', ts, 'unixepoch', 'localtime') d,
		COALESCE(SUM(net_rx_bytes),0), COALESCE(SUM(net_tx_bytes),0)
		FROM server_metrics
		WHERE server_id=? AND ts>=? AND net_totals_valid=1
		GROUP BY d ORDER BY d`, serverID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerTrafficDay{}
	for rows.Next() {
		var d ServerTrafficDay
		if err := rows.Scan(&d.Date, &d.Rx, &d.Tx); err != nil {
			return nil, err
		}
		d.Total = trafficTotalForMode(d.Rx, d.Tx, accountingMode)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ServerTrafficAttribution(serverID, since int64, limit int) (ServerTrafficAttribution, error) {
	var out ServerTrafficAttribution
	if err := s.db.QueryRow(`SELECT COALESCE(MIN(ts),0), COALESCE(MAX(ts),0),
		COUNT(DISTINCT user_id), COALESCE(SUM(up+down),0)
		FROM server_user_traffic_samples WHERE server_id=? AND ts>=?`, serverID, since).
		Scan(&out.CoverageStart, &out.CoverageEnd, &out.ActiveUsers, &out.Total); err != nil {
		return out, err
	}
	if limit <= 0 {
		limit = 8
	} else if limit > 50 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT t.user_id,
		COALESCE(NULLIF(u.username,''), '已删除用户 #' || t.user_id),
		COALESCE(SUM(t.up),0), COALESCE(SUM(t.down),0)
		FROM server_user_traffic_samples t
		LEFT JOIN users u ON u.id=t.user_id
		WHERE t.server_id=? AND t.ts>=?
		GROUP BY t.user_id, u.username
		ORDER BY SUM(t.up+t.down) DESC, t.user_id
		LIMIT ?`, serverID, since, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Sources = []ServerTrafficSource{}
	for rows.Next() {
		var source ServerTrafficSource
		if err := rows.Scan(&source.UserID, &source.Username, &source.Up, &source.Down); err != nil {
			return out, err
		}
		source.Total = source.Up + source.Down
		out.Sources = append(out.Sources, source)
	}
	return out, rows.Err()
}

// RawServerTrafficSince is deliberately calibration-free. Capacity modelling
// uses the physical bytes observed over the same window as user attribution;
// applying a provider baseline from an earlier, incomplete period would make
// a short recent window look arbitrarily large.
func (s *Store) RawServerTrafficSince(serverID, since int64, accountingMode string) (ServerTrafficUsage, error) {
	var out ServerTrafficUsage
	out.AccountingMode = NormalizeTrafficAccountingMode(accountingMode)
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(net_rx_bytes),0), COALESCE(SUM(net_tx_bytes),0),
		COALESCE(MIN(ts),0), COALESCE(MAX(ts),0), COUNT(id)
		FROM server_metrics WHERE server_id=? AND ts>=? AND net_totals_valid=1`, serverID, since).
		Scan(&out.Rx, &out.Tx, &out.CoverageStart, &out.CoverageEnd, &out.SampleCount); err != nil {
		return out, fmt.Errorf("raw server traffic: %w", err)
	}
	out.Total = trafficTotalForMode(out.Rx, out.Tx, out.AccountingMode)
	return out, nil
}
