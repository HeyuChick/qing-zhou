package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ServerAlert represents one alert event for a server.
type ServerAlert struct {
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"`
	Type     string `json:"type"`    // offline, high_cpu, high_mem, disk_full, expiring, expired
	Message  string `json:"message"`
	Ts       int64  `json:"ts"`
	Read     bool   `json:"read"`
}

const alertCols = `id, server_id, type, message, ts, read`

func scanAlert(sc scanner) (*ServerAlert, error) {
	var a ServerAlert
	var rd int
	err := sc.Scan(&a.ID, &a.ServerID, &a.Type, &a.Message, &a.Ts, &rd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Read = rd == 1
	return &a, nil
}

// InsertAlert writes a new alert, deduplicating by (server_id, type) within
// the last hour to avoid spam.
func (s *Store) InsertAlert(a ServerAlert) error {
	// Skip if same server+type already fired in the last hour.
	var n int
	hourAgo := time.Now().Add(-1 * time.Hour).Unix()
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM server_alerts WHERE server_id=? AND type=? AND ts>=?`,
		a.ServerID, a.Type, hourAgo).Scan(&n)
	if n > 0 {
		return nil // already alerted recently
	}
	if a.Ts == 0 {
		a.Ts = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO server_alerts (server_id, type, message, ts, read) VALUES (?,?,?,?,0)`,
		a.ServerID, a.Type, a.Message, a.Ts)
	return err
}

// ListAlerts returns alerts. If unreadOnly is true, only unread alerts.
func (s *Store) ListAlerts(unreadOnly bool) ([]*ServerAlert, error) {
	q := `SELECT ` + alertCols + ` FROM server_alerts`
	if unreadOnly {
		q += ` WHERE read=0`
	}
	q += ` ORDER BY ts DESC LIMIT 200`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServerAlert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkAlertRead marks an alert as read.
func (s *Store) MarkAlertRead(id int64) error {
	_, err := s.db.Exec(`UPDATE server_alerts SET read=1 WHERE id=?`, id)
	return err
}

// UnreadAlertCount returns the number of unread alerts.
func (s *Store) UnreadAlertCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM server_alerts WHERE read=0`).Scan(&n)
	return n, err
}

// CheckProbeAlerts evaluates all probe-enabled servers and generates alerts
// for offline, expiring, and expired conditions. Should be called periodically.
func (s *Store) CheckProbeAlerts() error {
	servers, err := s.ListServers()
	if err != nil {
		return err
	}
	now := time.Now()
	twoMinAgo := now.Add(-2 * time.Minute).Unix()
	threeDaysLater := now.AddDate(0, 0, 3)

	for _, sv := range servers {
		if !sv.ProbeEnabled {
			continue
		}

		// Offline: last_seen > 2 minutes ago
		if sv.LastSeen > 0 && sv.LastSeen < twoMinAgo {
			_ = s.InsertAlert(ServerAlert{
				ServerID: sv.ID,
				Type:     "offline",
				Message:  fmt.Sprintf("服务器「%s」离线，最后上报: %s", sv.Name, time.Unix(sv.LastSeen, 0).Format("2006-01-02 15:04")),
			})
		}

		// Expiring: within 3 days
		if sv.ExpiryDate > 0 && sv.ExpiryDate <= threeDaysLater.Unix() && sv.ExpiryDate > now.Unix() {
			days := int(time.Unix(sv.ExpiryDate, 0).Sub(now).Hours() / 24)
			_ = s.InsertAlert(ServerAlert{
				ServerID: sv.ID,
				Type:     "expiring",
				Message:  fmt.Sprintf("服务器「%s」将在 %d 天后到期", sv.Name, days),
			})
		}

		// Expired
		if sv.ExpiryDate > 0 && sv.ExpiryDate <= now.Unix() {
			_ = s.InsertAlert(ServerAlert{
				ServerID: sv.ID,
				Type:     "expired",
				Message:  fmt.Sprintf("服务器「%s」已过期", sv.Name),
			})
		}
	}

	// Check metric-based alerts for servers with recent data.
	for _, sv := range servers {
		if !sv.ProbeEnabled {
			continue
		}
		m, _ := s.GetLatestMetrics(sv.ID)
		if m == nil || m.Ts < twoMinAgo {
			continue // no recent data
		}
		if m.CPUPercent > 90 {
			_ = s.InsertAlert(ServerAlert{
				ServerID: sv.ID,
				Type:     "high_cpu",
				Message:  fmt.Sprintf("服务器「%s」CPU 使用率 %.1f%%", sv.Name, m.CPUPercent),
			})
		}
		if m.MemTotal > 0 && float64(m.MemUsed)/float64(m.MemTotal)*100 > 90 {
			_ = s.InsertAlert(ServerAlert{
				ServerID: sv.ID,
				Type:     "high_mem",
				Message:  fmt.Sprintf("服务器「%s」内存使用率 %.0f%%", sv.Name, float64(m.MemUsed)/float64(m.MemTotal)*100),
			})
		}
		if m.DiskTotal > 0 && float64(m.DiskUsed)/float64(m.DiskTotal)*100 > 85 {
			_ = s.InsertAlert(ServerAlert{
				ServerID: sv.ID,
				Type:     "disk_full",
				Message:  fmt.Sprintf("服务器「%s」磁盘使用 %.0f%%", sv.Name, float64(m.DiskUsed)/float64(m.DiskTotal)*100),
			})
		}
	}
	return nil
}
