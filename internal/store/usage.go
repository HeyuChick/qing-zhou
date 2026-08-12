package store

import (
	"fmt"
	"strings"
	"time"
)

// Usage reporting: per-user and per-package traffic over an arbitrary window.
//
// Two sources, deliberately, because they answer different questions and only
// one of them can answer both honestly:
//
//   - LIFETIME totals come from the running counters (users.used_*,
//     user_plans.used_*). They have been maintained since the account existed,
//     so "总计" is accurate all the way back — including for traffic that
//     predates this report.
//   - WINDOWED totals come from traffic_daily, which only starts when the
//     rollup ships. Asking the lifetime counters for "last 30 days" is not
//     possible (they are scalars), and asking traffic_daily for "all time"
//     would silently under-report every account older than the rollup.
//
// Callers pick by whether the window is open-ended; the report labels which one
// it used rather than presenting a windowed number as if it were lifetime.

// UsageWindow is a closed [From, To] range of LOCAL calendar dates, matching how
// traffic_daily.day is written. Empty From/To means unbounded on that side.
type UsageWindow struct {
	From string // YYYY-MM-DD inclusive; "" = from the first recorded day
	To   string // YYYY-MM-DD inclusive; "" = through the last recorded day
}

// UsageTotal is one subject's traffic. Subject is a user for the per-user
// breakdown and a (user, package) pair for the per-package one.
type UsageTotal struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	PackageID   int64  `json:"package_id"`   // 0 = shared pool, -1 = unattributed
	PackageName string `json:"package_name"` // resolved for display
	Up          int64  `json:"up"`
	Down        int64  `json:"down"`
}

// UsageDay is one calendar day of one series.
type UsageDay struct {
	Date string `json:"date"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// UsageSeries is one user's daily curve over the window.
type UsageSeries struct {
	UserID   int64      `json:"user_id"`
	Username string     `json:"username"`
	Days     []UsageDay `json:"days"`
}

// poolPackageName / unattributedPackageName label the two synthetic package ids
// in traffic_daily. They are not rows in `packages`, so no join can name them.
const (
	poolPackageName         = "流量包（公共池）"
	unattributedPackageName = "未记录套餐（升级前）"
)

// PackageIDUnattributed marks traffic recorded before per-bucket rollup existed.
const PackageIDUnattributed = -1

// inClause renders "(?,?,?)" plus the args for an IN list. Returns ok=false for
// an empty list so callers can choose between "no filter" and "match nothing"
// explicitly — SQLite has no valid syntax for `IN ()`, and building one by
// string-joining ids is how an id from a query param becomes injection.
func inClause(ids []int64) (string, []any, bool) {
	if len(ids) == 0 {
		return "", nil, false
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args, true
}

// where builds the shared user/day filter. userIDs empty = every user.
func usageWhere(userIDs []int64, w UsageWindow, userCol string) (string, []any) {
	var sb strings.Builder
	var args []any
	if clause, ids, ok := inClause(userIDs); ok {
		sb.WriteString(" AND " + userCol + " IN " + clause)
		args = append(args, ids...)
	}
	if w.From != "" {
		sb.WriteString(" AND day >= ?")
		args = append(args, w.From)
	}
	if w.To != "" {
		sb.WriteString(" AND day <= ?")
		args = append(args, w.To)
	}
	return sb.String(), args
}

// UsageDailyByUser returns each selected user's daily curve inside the window,
// sparse (only days with traffic). Callers fill gaps for plotting.
func (s *Store) UsageDailyByUser(userIDs []int64, w UsageWindow) ([]UsageSeries, error) {
	cond, args := usageWhere(userIDs, w, "t.user_id")
	rows, err := s.db.Query(`
		SELECT t.user_id, COALESCE(u.username,''), t.day,
		       COALESCE(SUM(t.up),0), COALESCE(SUM(t.down),0)
		  FROM traffic_daily t
		  LEFT JOIN users u ON u.id = t.user_id
		 WHERE 1=1`+cond+`
		 GROUP BY t.user_id, t.day
		 ORDER BY t.user_id, t.day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UsageSeries
	byUser := map[int64]int{} // user id -> index in out, preserving query order
	for rows.Next() {
		var uid int64
		var name string
		var d UsageDay
		if err := rows.Scan(&uid, &name, &d.Date, &d.Up, &d.Down); err != nil {
			return nil, err
		}
		idx, ok := byUser[uid]
		if !ok {
			out = append(out, UsageSeries{UserID: uid, Username: name})
			idx = len(out) - 1
			byUser[uid] = idx
		}
		out[idx].Days = append(out[idx].Days, d)
	}
	return out, rows.Err()
}

// UsageDailyTotal returns the combined daily curve across the selected users —
// the stacked chart's total line. Computed in SQL rather than by summing the
// per-user series so a caller that only needs the total doesn't pay for the
// per-user grouping.
func (s *Store) UsageDailyTotal(userIDs []int64, w UsageWindow) ([]UsageDay, error) {
	cond, args := usageWhere(userIDs, w, "user_id")
	rows, err := s.db.Query(`
		SELECT day, COALESCE(SUM(up),0), COALESCE(SUM(down),0)
		  FROM traffic_daily
		 WHERE 1=1`+cond+`
		 GROUP BY day ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageDay
	for rows.Next() {
		var d UsageDay
		if err := rows.Scan(&d.Date, &d.Up, &d.Down); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UsageByPackageWindowed returns per (user, package) totals inside the window,
// from the rollup.
//
// The package name is resolved in three steps because a package row can be
// deleted while its traffic history must stay readable: the live packages row
// first, then the name snapshot the user's own bucket kept, then a placeholder.
func (s *Store) UsageByPackageWindowed(userIDs []int64, w UsageWindow) ([]UsageTotal, error) {
	cond, args := usageWhere(userIDs, w, "t.user_id")
	rows, err := s.db.Query(`
		SELECT t.user_id, COALESCE(u.username,''), t.package_id,
		       COALESCE(NULLIF(p.name,''),
		                NULLIF((SELECT b.name FROM user_plans b
		                         WHERE b.user_id=t.user_id AND b.package_id=t.package_id
		                         ORDER BY b.id DESC LIMIT 1), ''),
		                '') AS pkg_name,
		       COALESCE(SUM(t.up),0), COALESCE(SUM(t.down),0)
		  FROM traffic_daily t
		  LEFT JOIN users u ON u.id = t.user_id
		  LEFT JOIN packages p ON p.id = t.package_id
		 WHERE 1=1`+cond+`
		 GROUP BY t.user_id, t.package_id
		 ORDER BY (SUM(t.up)+SUM(t.down)) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageTotals(rows)
}

// UsageByPackageLifetime returns per (user, package) totals from the bucket
// counters — accurate for the whole life of the account, including before the
// rollup existed. Buckets of the same package are summed, so an account that
// repurchased before renewal stacking reads as one package.
func (s *Store) UsageByPackageLifetime(userIDs []int64) ([]UsageTotal, error) {
	var cond string
	var args []any
	if clause, ids, ok := inClause(userIDs); ok {
		cond = " AND b.user_id IN " + clause
		args = append(args, ids...)
	}
	rows, err := s.db.Query(`
		SELECT b.user_id, COALESCE(u.username,''), b.package_id,
		       COALESCE(NULLIF(p.name,''), NULLIF(MAX(b.name),''), '') AS pkg_name,
		       COALESCE(SUM(b.used_up),0), COALESCE(SUM(b.used_down),0)
		  FROM user_plans b
		  LEFT JOIN users u ON u.id = b.user_id
		  LEFT JOIN packages p ON p.id = b.package_id
		 WHERE 1=1`+cond+`
		 GROUP BY b.user_id, b.package_id
		HAVING SUM(b.used_up) + SUM(b.used_down) > 0
		 ORDER BY (SUM(b.used_up)+SUM(b.used_down)) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageTotals(rows)
}

func scanUsageTotals(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]UsageTotal, error) {
	var out []UsageTotal
	for rows.Next() {
		var t UsageTotal
		if err := rows.Scan(&t.UserID, &t.Username, &t.PackageID, &t.PackageName, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		t.PackageName = usagePackageLabel(t.PackageID, t.PackageName)
		out = append(out, t)
	}
	return out, rows.Err()
}

// usagePackageLabel names the two synthetic ids and rescues a package whose row
// was deleted and whose bucket kept no snapshot — an empty legend entry reads
// as a rendering bug rather than as deleted history.
func usagePackageLabel(id int64, name string) string {
	switch {
	case id == PackageIDUnattributed:
		return unattributedPackageName
	case id == 0:
		return poolPackageName
	case name != "":
		return name
	default:
		return fmt.Sprintf("已删除套餐 #%d", id)
	}
}

// UsageLifetimeByUser returns each selected user's all-time total from the
// mirrored user counter.
func (s *Store) UsageLifetimeByUser(userIDs []int64) ([]UsageTotal, error) {
	cond := ""
	var args []any
	if clause, ids, ok := inClause(userIDs); ok {
		cond = " AND id IN " + clause
		args = append(args, ids...)
	}
	rows, err := s.db.Query(`
		SELECT id, username, 0, '', used_up, used_down
		  FROM users
		 WHERE role='user'`+cond+`
		 ORDER BY (used_up+used_down) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageTotal
	for rows.Next() {
		var t UsageTotal
		if err := rows.Scan(&t.UserID, &t.Username, &t.PackageID, &t.PackageName, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UsageWindowedByUser returns each selected user's total inside the window.
func (s *Store) UsageWindowedByUser(userIDs []int64, w UsageWindow) ([]UsageTotal, error) {
	cond, args := usageWhere(userIDs, w, "t.user_id")
	rows, err := s.db.Query(`
		SELECT t.user_id, COALESCE(u.username,''), 0, '',
		       COALESCE(SUM(t.up),0), COALESCE(SUM(t.down),0)
		  FROM traffic_daily t
		  LEFT JOIN users u ON u.id = t.user_id
		 WHERE 1=1`+cond+`
		 GROUP BY t.user_id
		 ORDER BY (SUM(t.up)+SUM(t.down)) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageTotal
	for rows.Next() {
		var t UsageTotal
		if err := rows.Scan(&t.UserID, &t.Username, &t.PackageID, &t.PackageName, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TrafficDailyCoverage reports the first and last day the rollup holds, so the
// report can state the period it can actually speak for. Empty strings mean the
// table is empty (a fresh install, before the first stats poll).
func (s *Store) TrafficDailyCoverage() (first, last string, err error) {
	var f, l *string
	err = s.db.QueryRow(`SELECT MIN(day), MAX(day) FROM traffic_daily`).Scan(&f, &l)
	if f != nil {
		first = *f
	}
	if l != nil {
		last = *l
	}
	return
}

// UsageCandidate is one selectable user for the report's picker.
type UsageCandidate struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Traffic  int64  `json:"traffic"` // lifetime, for ordering the list usefully
}

// UsageUserCandidates lists users for the picker, heaviest first so the accounts
// an admin is most likely looking for are at the top. q filters by username.
func (s *Store) UsageUserCandidates(q string, limit int) ([]UsageCandidate, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args := []any{}
	cond := ""
	if q = strings.TrimSpace(q); q != "" {
		cond = " AND username LIKE ? ESCAPE '\\'"
		args = append(args, "%"+escapeLike(q)+"%")
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT id, username, used_up+used_down AS v
		  FROM users WHERE role='user'`+cond+`
		 ORDER BY v DESC, username LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageCandidate
	for rows.Next() {
		var c UsageCandidate
		if err := rows.Scan(&c.ID, &c.Username, &c.Traffic); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// escapeLike neutralises the LIKE wildcards in user input, so a username search
// for "100%" doesn't match everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// LocalDay renders a unix timestamp as the local calendar date, the same form
// traffic_daily.day stores.
func LocalDay(ts int64) string { return time.Unix(ts, 0).Format("2006-01-02") }

// DaysAgo renders the local calendar date n days before today.
func DaysAgo(n int) string { return time.Now().AddDate(0, 0, -n).Format("2006-01-02") }
