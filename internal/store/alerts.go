package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ServerAlert represents one alert *episode* for a server: a condition that
// started at FirstTs and was last observed at Ts. Repeated observations of a
// still-open episode merge into the same row (Count++) instead of piling up new
// ones, so one broken server stays one line in the panel however long it lasts.
type ServerAlert struct {
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"`
	Type     string `json:"type"` // offline, high_cpu, high_mem, disk_full, expiring, expired
	Message  string `json:"message"`
	Ts       int64  `json:"ts"`       // last observed
	FirstTs  int64  `json:"first_ts"` // episode start
	Hits     int64  `json:"hits"`     // times observed in this episode
	Read     bool   `json:"read"`
	Resolved bool   `json:"resolved"` // condition cleared on its own
}

// reAlertWindow is how long an acknowledged (dismissed) alert stays quiet while
// its condition persists — one reminder per day instead of one per check.
const reAlertWindow = 24 * time.Hour

// alertTypes are the conditions CheckProbeAlerts evaluates and auto-resolves.
var alertTypes = []string{"offline", "expiring", "expired", "high_cpu", "high_mem", "disk_full"}

// AlertRestartLoop marks a node that keeps restarting without anyone asking it
// to — every restart cuts all connections on that node, so a loop is a user-
// visible outage even while the panel reports every deploy as successful.
//
// Deliberately NOT in alertTypes: those are derived from probe metrics and are
// auto-resolved by CheckProbeAlerts when the metric recovers. This one is
// derived from the config-deploy path, has no probe data behind it, and is
// resolved by the watcher that raised it (see api/restartalert.go).
const AlertRestartLoop = "restart_loop"

// flappyAlertTypes are sampled conditions: each check reads one instant, and one
// instant is not a problem. A build, a backup or a log rotation drives CPU over
// any threshold for a few seconds, and alerting on that first sample trains the
// admin to ignore the panel — which is worse than not alerting at all.
//
// Expiry-based conditions are absent on purpose: a date does not flap, and
// delaying "this server expires in 3 days" to make it look steadier would only
// shorten the warning.
var flappyAlertTypes = map[string]bool{
	"offline": true, "high_cpu": true, "high_mem": true, "disk_full": true,
}

// SettingAlertStreak is how many consecutive checks a flappy condition must hold
// before it is raised. 1 restores the old alert-on-first-sample behaviour.
const SettingAlertStreak = "alert_consecutive"

const defaultAlertStreak = 2

// alertStreaks counts consecutive observations per (server, type) between
// checks. Memory, not a table: the count is only meaningful within one run of
// the panel, and after a restart re-arming from zero merely delays the first
// alert of a still-broken condition by one check interval — the alternative,
// a write per server per type per check, buys nothing.
type alertStreakKey struct {
	server int64
	typ    string
}

// observeStreak records one observation and reports whether the condition has
// now held for `need` consecutive checks. Once the threshold is reached the
// counter is held there rather than growing, so a long outage keeps reporting
// true without overflowing or needing a reset.
func (s *Store) observeStreak(key alertStreakKey, need int) bool {
	s.streakMu.Lock()
	defer s.streakMu.Unlock()
	if s.streaks == nil {
		s.streaks = map[alertStreakKey]int{}
	}
	if s.streaks[key] < need {
		s.streaks[key]++
	}
	return s.streaks[key] >= need
}

// clearStreak forgets a condition that no longer holds, so the next occurrence
// has to earn its alert from scratch instead of inheriting a stale count.
func (s *Store) clearStreak(key alertStreakKey) {
	s.streakMu.Lock()
	defer s.streakMu.Unlock()
	delete(s.streaks, key)
}

// pruneStreaks drops counters for servers that no longer exist. Deleting a
// server is the one path that removes it from every check without ever taking
// its conditions through "resolved", so without this its counters would sit in
// the map for the lifetime of the process.
func (s *Store) pruneStreaks(live map[int64]bool) {
	s.streakMu.Lock()
	defer s.streakMu.Unlock()
	for k := range s.streaks {
		if !live[k.server] {
			delete(s.streaks, k)
		}
	}
}

// alertStreakRequired reads the configured threshold, clamped to something sane:
// below 1 it would alert on nothing observed, and a very large value would mean
// a genuinely dead server never surfaces.
func (s *Store) alertStreakRequired() int {
	n, _ := s.GetSettingInt64(SettingAlertStreak, defaultAlertStreak)
	switch {
	case n < 1:
		return 1
	case n > 10:
		return 10
	default:
		return int(n)
	}
}

const alertCols = `id, server_id, type, message, ts, first_ts, hits, read, resolved`

func scanAlert(sc scanner) (*ServerAlert, error) {
	var a ServerAlert
	var rd, rs int
	err := sc.Scan(&a.ID, &a.ServerID, &a.Type, &a.Message, &a.Ts, &a.FirstTs, &a.Hits, &rd, &rs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Read = rd == 1
	a.Resolved = rs == 1
	return &a, nil
}

// InsertAlert records one observation of a condition.
//
// If an episode for (server_id, type) is still open (unread), the observation
// merges into it. Otherwise the episode was either acknowledged by an admin —
// then it stays quiet for reAlertWindow so a permanently broken server nags at
// most once a day — or closed by ResolveAlert, in which case this is a genuinely
// new episode and alerts immediately.
//
// The bool reports whether this call opened a NEW alert episode, as opposed to
// folding into one that is already on screen. Only a new episode is worth a
// push notification: the hits+1 path fires on every recurrence and would turn a
// single bad night into a stream of identical Telegram messages.
func (s *Store) InsertAlert(a ServerAlert) (bool, error) {
	if a.Ts == 0 {
		a.Ts = time.Now().Unix()
	}
	res, err := s.db.Exec(`UPDATE server_alerts SET message=?, ts=?, hits=hits+1
		WHERE server_id=? AND type=? AND read=0`, a.Message, a.Ts, a.ServerID, a.Type)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return false, nil
	}
	// resolved=0 rows that are read = acknowledged but never cleared.
	var lastAck int64
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM server_alerts
		WHERE server_id=? AND type=? AND resolved=0`, a.ServerID, a.Type).Scan(&lastAck)
	if lastAck > 0 && a.Ts-lastAck < int64(reAlertWindow.Seconds()) {
		return false, nil
	}
	_, err = s.db.Exec(`INSERT INTO server_alerts (server_id, type, message, ts, first_ts, hits, read, resolved)
		VALUES (?,?,?,?,?,1,0,0)`, a.ServerID, a.Type, a.Message, a.Ts, a.Ts)
	return err == nil, err
}

// ResolveAlertsByType closes every open episode of one type, whatever server it
// is on. Used at startup for conditions the panel tracks in memory: the evidence
// for them died with the previous process, so an alert nobody can clear would
// otherwise sit on the page until an admin dismissed it by hand. A condition
// that is still happening re-raises itself within one window.
func (s *Store) ResolveAlertsByType(typ string) error {
	_, err := s.db.Exec(`UPDATE server_alerts SET read=1, resolved=1 WHERE type=? AND resolved=0`, typ)
	return err
}

// ResolveAlert closes the current episode for (server_id, type) because the
// condition no longer holds: open alerts disappear from the panel by themselves,
// and the next occurrence counts as a new episode rather than being suppressed
// by the acknowledge window.
func (s *Store) ResolveAlert(serverID int64, typ string) error {
	_, err := s.db.Exec(`UPDATE server_alerts SET read=1, resolved=1
		WHERE server_id=? AND type=? AND resolved=0`, serverID, typ)
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

// MarkAllAlertsRead acknowledges every unread alert at once.
func (s *Store) MarkAllAlertsRead() (int64, error) {
	res, err := s.db.Exec(`UPDATE server_alerts SET read=1 WHERE read=0`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// UnreadAlertCount returns the number of unread alerts.
func (s *Store) UnreadAlertCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM server_alerts WHERE read=0`).Scan(&n)
	return n, err
}

// CheckProbeAlerts evaluates all probe-enabled servers, raising alerts for the
// conditions that hold and resolving the ones that no longer do. Should be
// called periodically.
// localAlertNode is the panel's own machine as the alert checker wants to see
// it: a server row that does not exist in the table. Nil when the panel has
// never sampled itself (not Linux, or only just started), so nothing is judged
// on absent data.
//
// LastSeen comes from the newest sample rather than a last_seen column, which
// is what makes the offline alert work for it too: if the collector goroutine
// dies, the samples stop and the machine goes offline like any other.
func (s *Store) localAlertNode() *Server {
	m, err := s.GetLatestMetrics(LocalNodeID)
	if err != nil || m == nil {
		return nil
	}
	return &Server{
		ID:           LocalNodeID,
		Name:         LocalNodeName,
		Enabled:      true,
		ProbeEnabled: true,
		LastSeen:     m.Ts,
		// Whatever expiry the admin recorded for this box. Of every machine the
		// panel warns about, this is the one worth warning about most: a landing
		// node lapsing costs a node, this one lapsing costs the panel.
		ExpiryDate: s.LocalAsset().ExpiryDate,
	}
}

func (s *Store) CheckProbeAlerts() error {
	servers, err := s.ListServers()
	if err != nil {
		return err
	}
	// The panel's own machine is monitored without a servers row, so it would
	// otherwise be the one machine that can fill its disk in silence — the same
	// machine whose disk filling up takes the whole panel down with it. It has
	// no expiry and no SSH, but CPU / memory / disk are the same measurements.
	if local := s.localAlertNode(); local != nil {
		servers = append(servers, local)
	}
	now := time.Now()
	twoMinAgo := now.Add(-2 * time.Minute).Unix()
	threeDaysLater := now.AddDate(0, 0, 3)

	// Thresholds are configurable via settings (alert_cpu/mem/disk_threshold).
	cpuThreshold, _ := s.GetSettingInt64("alert_cpu_threshold", 90)
	memThreshold, _ := s.GetSettingInt64("alert_mem_threshold", 90)
	diskThreshold, _ := s.GetSettingInt64("alert_disk_threshold", 85)
	if cpuThreshold <= 0 {
		cpuThreshold = 90
	}
	if memThreshold <= 0 {
		memThreshold = 90
	}
	if diskThreshold <= 0 {
		diskThreshold = 85
	}

	streakNeeded := s.alertStreakRequired()
	live := make(map[int64]bool, len(servers))
	for _, sv := range servers {
		live[sv.ID] = true
	}
	s.pruneStreaks(live)

	for _, sv := range servers {
		if !sv.ProbeEnabled {
			// Probe turned off: nothing is being watched, so close whatever is
			// still open instead of leaving stale alerts in the panel.
			for _, typ := range alertTypes {
				_ = s.ResolveAlert(sv.ID, typ)
				s.clearStreak(alertStreakKey{sv.ID, typ})
			}
			continue
		}
		active := map[string]bool{}
		// raise marks the condition as holding right now. For a sampled condition
		// that is not yet the same as alerting: it has to hold for streakNeeded
		// consecutive checks first. `active` is set either way, so a condition
		// still building its streak is not treated as resolved below — otherwise
		// an alternating pattern would resolve and re-arm forever.
		raise := func(typ, msg string) {
			active[typ] = true
			if flappyAlertTypes[typ] && !s.observeStreak(alertStreakKey{sv.ID, typ}, streakNeeded) {
				return
			}
			_, _ = s.InsertAlert(ServerAlert{ServerID: sv.ID, Type: typ, Message: msg})
		}

		// Offline: last_seen > 2 minutes ago
		if sv.LastSeen > 0 && sv.LastSeen < twoMinAgo {
			raise("offline", fmt.Sprintf("服务器「%s」离线，最后上报: %s", sv.Name, time.Unix(sv.LastSeen, 0).Format("2006-01-02 15:04")))
		}

		// Expiring: within 3 days
		if sv.ExpiryDate > 0 && sv.ExpiryDate <= threeDaysLater.Unix() && sv.ExpiryDate > now.Unix() {
			days := int(time.Unix(sv.ExpiryDate, 0).Sub(now).Hours() / 24)
			raise("expiring", fmt.Sprintf("服务器「%s」将在 %d 天后到期", sv.Name, days))
		}

		// Expired
		if sv.ExpiryDate > 0 && sv.ExpiryDate <= now.Unix() {
			raise("expired", fmt.Sprintf("服务器「%s」已过期", sv.Name))
		}

		// Metric-based conditions need recent data; without it their state is
		// unknown, so they are neither raised nor resolved (the offline alert
		// already covers that case).
		m, _ := s.GetLatestMetrics(sv.ID)
		fresh := m != nil && m.Ts >= twoMinAgo
		if fresh {
			if m.CPUPercent > float64(cpuThreshold) {
				raise("high_cpu", fmt.Sprintf("服务器「%s」CPU 使用率 %.1f%%", sv.Name, m.CPUPercent))
			}
			if m.MemTotal > 0 && float64(m.MemUsed)/float64(m.MemTotal)*100 > float64(memThreshold) {
				raise("high_mem", fmt.Sprintf("服务器「%s」内存使用率 %.0f%%", sv.Name, float64(m.MemUsed)/float64(m.MemTotal)*100))
			}
			if m.DiskTotal > 0 && float64(m.DiskUsed)/float64(m.DiskTotal)*100 > float64(diskThreshold) {
				raise("disk_full", fmt.Sprintf("服务器「%s」磁盘使用 %.0f%%", sv.Name, float64(m.DiskUsed)/float64(m.DiskTotal)*100))
			}
		}

		for _, typ := range alertTypes {
			if active[typ] {
				continue
			}
			if !fresh && (typ == "high_cpu" || typ == "high_mem" || typ == "disk_full") {
				continue
			}
			// The condition genuinely does not hold: close the episode and drop
			// the streak, so a later recurrence has to hold for the full run
			// again rather than being alerted on its first sample.
			s.clearStreak(alertStreakKey{sv.ID, typ})
			_ = s.ResolveAlert(sv.ID, typ)
		}
	}
	return nil
}
