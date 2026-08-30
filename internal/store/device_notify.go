package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TrafficCycleStart returns the start of the current provider billing month in
// the panel's timezone. A reset day that does not exist in a short month means
// that month's final day (31 -> February 28/29), matching common VPS billing.
func TrafficCycleStart(now time.Time, resetDay, resetMinute int) time.Time {
	if resetDay < 1 {
		resetDay = 1
	} else if resetDay > 31 {
		resetDay = 31
	}
	if resetMinute < 0 {
		resetMinute = 0
	} else if resetMinute > 1439 {
		resetMinute = 1439
	}
	loc := now.Location()
	cycleIn := func(year int, month time.Month) time.Time {
		last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
		day := resetDay
		if day > last {
			day = last
		}
		return time.Date(year, month, day, resetMinute/60, resetMinute%60, 0, 0, loc)
	}
	candidate := cycleIn(now.Year(), now.Month())
	if !now.Before(candidate) {
		return candidate
	}
	prev := now.AddDate(0, -1, 0)
	return cycleIn(prev.Year(), prev.Month())
}

func TrafficCycleNext(now time.Time, resetDay, resetMinute int) time.Time {
	start := TrafficCycleStart(now, resetDay, resetMinute)
	// Thirty-five days is always inside the following billing cycle. Re-running
	// the clamping logic there handles 31 -> February 28 -> March 31 correctly.
	return TrafficCycleStart(start.AddDate(0, 0, 35), resetDay, resetMinute)
}

// TrafficUsageForCycles aggregates different per-server billing windows in one
// SQL query, avoiding an N+1 query on the monitor page and hourly alert sweep.
func (s *Store) TrafficUsageForCycles(starts map[int64]int64) (map[int64]ServerTrafficUsage, error) {
	out := make(map[int64]ServerTrafficUsage, len(starts))
	if len(starts) == 0 {
		return out, nil
	}
	values := make([]string, 0, len(starts))
	args := make([]any, 0, len(starts)*2)
	for id, since := range starts {
		values = append(values, "(?,?)")
		args = append(args, id, since)
	}
	q := `WITH starts(server_id, since) AS (VALUES ` + strings.Join(values, ",") + `)
		SELECT s.server_id, COALESCE(SUM(m.net_rx_bytes),0), COALESCE(SUM(m.net_tx_bytes),0),
		COALESCE(MIN(m.ts),0), COALESCE(MAX(m.ts),0), COUNT(m.id),
		COALESCE(c.offset_bytes,0), COALESCE(c.calibrated_at,0),
		CASE WHEN c.server_id IS NULL THEN 0 ELSE 1 END
		FROM starts s
		LEFT JOIN server_metrics m ON m.server_id=s.server_id AND m.ts>=s.since AND m.net_totals_valid=1
		LEFT JOIN server_traffic_calibrations c ON c.server_id=s.server_id AND c.cycle_start=s.since
		GROUP BY s.server_id, c.offset_bytes, c.calibrated_at, c.server_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var u ServerTrafficUsage
		var offset int64
		var calibrated int
		if err := rows.Scan(&id, &u.Rx, &u.Tx, &u.CoverageStart, &u.CoverageEnd, &u.SampleCount,
			&offset, &u.CalibratedAt, &calibrated); err != nil {
			return nil, err
		}
		u.Total = u.Rx + u.Tx
		if calibrated == 1 {
			u.Calibrated = true
			if offset < 0 && u.Total < -offset {
				u.Total = 0
			} else {
				u.Total += offset
			}
		}
		out[id] = u
	}
	return out, rows.Err()
}

// SetTrafficCalibration makes usedBytes the current displayed total for one
// device billing cycle. Future probe deltas continue increasing it; a different
// cycleStart ignores it automatically. Keeping this in a separate table also
// supports LocalNodeID, whose asset metadata has no servers row.
func (s *Store) SetTrafficCalibration(serverID, cycleStart, usedBytes, calibratedAt int64) error {
	if usedBytes < 0 {
		return fmt.Errorf("traffic calibration cannot be negative")
	}
	_, err := s.db.Exec(`INSERT INTO server_traffic_calibrations
		(server_id,cycle_start,offset_bytes,calibrated_at)
		VALUES (?,?,?-(SELECT COALESCE(SUM(net_rx_bytes+net_tx_bytes),0)
			FROM server_metrics WHERE server_id=? AND ts>=? AND net_totals_valid=1),?)
		ON CONFLICT(server_id) DO UPDATE SET cycle_start=excluded.cycle_start,
		offset_bytes=excluded.offset_bytes, calibrated_at=excluded.calibrated_at`,
		serverID, cycleStart, usedBytes, serverID, cycleStart, calibratedAt)
	return err
}

type DeviceNotifyState struct {
	SentCount   int
	LastSentDay string
}

func (s *Store) DeviceNotifyState(serverID int64, kind, cycleKey string) (DeviceNotifyState, error) {
	var st DeviceNotifyState
	err := s.db.QueryRow(`SELECT sent_count, last_sent_day FROM device_notify_state
		WHERE server_id=? AND kind=? AND cycle_key=?`, serverID, kind, cycleKey).
		Scan(&st.SentCount, &st.LastSentDay)
	if err == sql.ErrNoRows {
		return DeviceNotifyState{}, nil
	}
	return st, err
}

func (s *Store) MarkDeviceNotifySent(serverID int64, kind, cycleKey, day string, now int64) error {
	_, err := s.db.Exec(`INSERT INTO device_notify_state
		(server_id, kind, cycle_key, sent_count, last_sent_day, updated_at)
		VALUES (?,?,?,1,?,?)
		ON CONFLICT(server_id,kind,cycle_key) DO UPDATE SET
		 sent_count=sent_count+1, last_sent_day=excluded.last_sent_day, updated_at=excluded.updated_at`,
		serverID, kind, cycleKey, day, now)
	return err
}

func (s *Store) PruneDeviceNotifyState(before int64) error {
	_, err := s.db.Exec(`DELETE FROM device_notify_state WHERE updated_at<?`, before)
	return err
}
