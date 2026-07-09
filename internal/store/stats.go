package store

import "time"

type Overview struct {
	TotalUsers   int64 `json:"total_users"`
	ActiveUsers  int64 `json:"active_users"` // have a provisioned client
	NewToday     int64 `json:"new_today"`
	TotalTraffic int64 `json:"total_traffic"` // sum of used up+down
	PointsIssued int64 `json:"points_issued"`
	PackagesOn   int64 `json:"packages_on"`
}

// DayTraffic is one calendar day's up/down totals (bytes).
type DayTraffic struct {
	Date string `json:"date"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// UserDailyTraffic returns a user's per-day traffic over the last `days` days,
// from the traffic_samples time-series (native sing-box era). Sparse: only days
// with traffic are returned; callers fill the window.
func (s *Store) UserDailyTraffic(userID int64, days int) ([]DayTraffic, error) {
	return s.dailyTraffic(`SELECT strftime('%Y-%m-%d', ts, 'unixepoch', 'localtime') d,
		COALESCE(SUM(up),0), COALESCE(SUM(down),0)
		FROM traffic_samples WHERE user_id=? AND ts>=? GROUP BY d ORDER BY d`,
		userID, time.Now().AddDate(0, 0, -days).Unix())
}

// SiteDailyTraffic returns site-wide per-day traffic over the last `days` days.
func (s *Store) SiteDailyTraffic(days int) ([]DayTraffic, error) {
	return s.dailyTraffic(`SELECT strftime('%Y-%m-%d', ts, 'unixepoch', 'localtime') d,
		COALESCE(SUM(up),0), COALESCE(SUM(down),0)
		FROM traffic_samples WHERE ts>=? GROUP BY d ORDER BY d`,
		time.Now().AddDate(0, 0, -days).Unix())
}

func (s *Store) dailyTraffic(q string, args ...any) ([]DayTraffic, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayTraffic
	for rows.Next() {
		var d DayTraffic
		if err := rows.Scan(&d.Date, &d.Up, &d.Down); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// OnlineCount counts users seen transferring traffic within the last `withinSec`
// seconds (a stats poll with non-zero delta updates last_online_at).
func (s *Store) OnlineCount(withinSec int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE last_online_at >= ?`,
		time.Now().Unix()-withinSec).Scan(&n)
	return n, err
}

// OnlineUsers returns usernames seen online within the last `withinSec` seconds,
// most-recent first.
func (s *Store) OnlineUsers(withinSec int64, limit int) ([]NameValue, error) {
	return s.nameValues(`SELECT username, last_online_at FROM users
		WHERE last_online_at >= ? ORDER BY last_online_at DESC LIMIT ?`,
		time.Now().Unix()-withinSec, limit)
}

// PruneTrafficSamples deletes samples older than `keepDays` days.
func (s *Store) PruneTrafficSamples(keepDays int) error {
	_, err := s.db.Exec(`DELETE FROM traffic_samples WHERE ts < ?`,
		time.Now().AddDate(0, 0, -keepDays).Unix())
	return err
}

func (s *Store) Overview() (*Overview, error) {
	o := &Overview{}
	row := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM users WHERE role='user'),
		(SELECT COUNT(*) FROM users WHERE client_name IS NOT NULL AND client_name<>''),
		(SELECT COUNT(*) FROM users WHERE created_at >= ?),
		(SELECT COALESCE(SUM(used_up+used_down),0) FROM users),
		(SELECT COALESCE(SUM(amount),0) FROM point_transactions WHERE amount>0),
		(SELECT COUNT(*) FROM packages WHERE enabled=1)`,
		startOfToday())
	if err := row.Scan(&o.TotalUsers, &o.ActiveUsers, &o.NewToday, &o.TotalTraffic, &o.PointsIssued, &o.PackagesOn); err != nil {
		return nil, err
	}
	return o, nil
}

type NameValue struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// TopByTraffic returns users with the most used traffic.
func (s *Store) TopByTraffic(limit int) ([]NameValue, error) {
	return s.nameValues(`SELECT username, used_up+used_down AS v FROM users
		WHERE role='user' ORDER BY v DESC LIMIT ?`, limit)
}

// TopBySpend returns users who spent the most points.
func (s *Store) TopBySpend(limit int) ([]NameValue, error) {
	return s.nameValues(`SELECT u.username, COALESCE(-SUM(t.amount),0) AS v
		FROM point_transactions t JOIN users u ON u.id=t.user_id
		WHERE t.type='purchase' GROUP BY t.user_id ORDER BY v DESC LIMIT ?`, limit)
}

// PackageSales counts successful orders per package name (from snapshot id).
func (s *Store) PackageSales(limit int) ([]NameValue, error) {
	return s.nameValues(`SELECT COALESCE(p.name, 'pkg#'||o.package_id) AS name, COUNT(*) AS v
		FROM orders o LEFT JOIN packages p ON p.id=o.package_id
		WHERE o.status='success' GROUP BY o.package_id ORDER BY v DESC LIMIT ?`, limit)
}

func (s *Store) nameValues(q string, args ...any) ([]NameValue, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NameValue{}
	for rows.Next() {
		var nv NameValue
		if err := rows.Scan(&nv.Name, &nv.Value); err != nil {
			return nil, err
		}
		out = append(out, nv)
	}
	return out, rows.Err()
}

// StatusDistribution returns counts of users by status plus expiry buckets.
func (s *Store) StatusDistribution() (map[string]int64, error) {
	now := time.Now().Unix()
	out := map[string]int64{}
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM users WHERE role='user' GROUP BY status`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st string
		var c int64
		if err := rows.Scan(&st, &c); err != nil {
			rows.Close()
			return nil, err
		}
		out["status_"+st] = c
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	buckets := []struct {
		key  string
		cond string
		args []any
	}{
		{"expired", `expiry_at>0 AND expiry_at<=?`, []any{now}},
		{"expire_7d", `expiry_at>? AND expiry_at<=?`, []any{now, now + 7*86400}},
		{"expire_30d", `expiry_at>? AND expiry_at<=?`, []any{now + 7*86400, now + 30*86400}},
	}
	for _, b := range buckets {
		var c int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='user' AND `+b.cond, b.args...).Scan(&c); err != nil {
			return nil, err
		}
		out[b.key] = c
	}
	return out, nil
}

type DayPoint struct {
	Date string `json:"date"`
	A    int64  `json:"a"`
	B    int64  `json:"b"`
}

// RegistrationTrend returns new-user counts per day for the last n days.
func (s *Store) RegistrationTrend(days int) ([]DayPoint, error) {
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := s.db.Query(`SELECT date(created_at,'unixepoch','localtime') d, COUNT(*)
		FROM users WHERE created_at>=? GROUP BY d ORDER BY d`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayPoint{}
	for rows.Next() {
		var dp DayPoint
		if err := rows.Scan(&dp.Date, &dp.A); err != nil {
			return nil, err
		}
		out = append(out, dp)
	}
	return out, rows.Err()
}

// RevenueTrend returns points issued (a) and consumed (b) per day.
func (s *Store) RevenueTrend(days int) ([]DayPoint, error) {
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := s.db.Query(`SELECT date(created_at,'unixepoch','localtime') d,
		COALESCE(SUM(CASE WHEN amount>0 THEN amount ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN amount<0 THEN -amount ELSE 0 END),0)
		FROM point_transactions WHERE created_at>=? GROUP BY d ORDER BY d`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayPoint{}
	for rows.Next() {
		var dp DayPoint
		if err := rows.Scan(&dp.Date, &dp.A, &dp.B); err != nil {
			return nil, err
		}
		out = append(out, dp)
	}
	return out, rows.Err()
}

func startOfToday() int64 {
	now := time.Now()
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Unix()
}
