package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/store"
)

// trafficDay is one day's up/down totals for the charts.
type trafficDay struct {
	Date string `json:"date"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// fillDays turns sparse per-day rows into a continuous trailing window so the
// chart has no gaps.
func fillDays(rows []store.DayTraffic, days int) []trafficDay {
	byDate := map[string]store.DayTraffic{}
	for _, r := range rows {
		byDate[r.Date] = r
	}
	out := make([]trafficDay, 0, days)
	now := time.Now()
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		if r, ok := byDate[d]; ok {
			out = append(out, trafficDay{Date: d, Up: r.Up, Down: r.Down})
		} else {
			out = append(out, trafficDay{Date: d})
		}
	}
	return out
}

// fillPoints does for the two-series day charts what fillDays does for traffic:
// turns sparse rows into a continuous trailing window. Without it a caller that
// slices "the last N entries" gets the last N days *that had activity*, which on
// a quiet panel can reach back months while the axis claims to show a fortnight.
func fillPoints(rows []store.DayPoint, days int) []store.DayPoint {
	byDate := map[string]store.DayPoint{}
	for _, r := range rows {
		byDate[r.Date] = r
	}
	out := make([]store.DayPoint, 0, days)
	now := time.Now()
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		if r, ok := byDate[d]; ok {
			out = append(out, r)
		} else {
			out = append(out, store.DayPoint{Date: d})
		}
	}
	return out
}

func rangeDays(r string) int {
	switch r {
	case "90d":
		return 90
	case "30d":
		return 30
	case "14d":
		return 14
	default:
		return 7
	}
}

// GET /api/user/stats/traffic?range=7d|30d
func (a *API) handleUserTrafficStats(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		fail(w, http.StatusUnauthorized, "未登录")
		return
	}
	days := rangeDays(r.URL.Query().Get("range"))
	rows, _ := a.st.UserDailyTraffic(u.ID, days)
	ok(w, fillDays(rows, days))
}

// GET /api/admin/stats/overview
func (a *API) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := a.st.Overview()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取概览失败")
		return
	}
	win := a.st.UserOnlineWindowSec()
	online, _ := a.st.OnlineCount(win)
	onlineUsers, _ := a.st.OnlineUsers(win, 20)
	// Period totals alongside the lifetime ones: "累计流量 3.2 TB" is a fact with
	// no direction, and the operator's actual question is whether this week is up
	// or down on last week.
	days := rangeDays(r.URL.Query().Get("range"))
	cur, prev, _ := a.st.PeriodStats(days)
	ok(w, J{
		"total_users":   ov.TotalUsers,
		"active_users":  ov.ActiveUsers,
		"new_today":     ov.NewToday,
		"total_traffic": ov.TotalTraffic,
		"points_issued": ov.PointsIssued,
		"packages_on":   ov.PackagesOn,
		"online":        online,
		"online_users":  onlineUsers,
		"online_window": win,
		"range_days":    days,
		"period":        cur,
		"period_prev":   prev,
	})
}

// GET /api/admin/stats/packages — per-package sales and consumption.
func (a *API) handleAdminPackageStats(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.PackageStats()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取套餐统计失败")
		return
	}
	ok(w, rows)
}

// GET /api/admin/stats/users — the filterable user table behind 用户分析.
// Filters: q, status, package_id, expiry, online, range, sort, desc, limit, offset.
func (a *API) handleAdminUserStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.UserStatFilter{
		Query:     strings.TrimSpace(q.Get("q")),
		Status:    q.Get("status"),
		PackageID: atoi(q.Get("package_id")),
		Expiry:    q.Get("expiry"),
		Online:    q.Get("online") == "1",
		Days:      rangeDays(q.Get("range")),
		Sort:      q.Get("sort"),
		Desc:      q.Get("desc") != "0",
		Limit:     int(atoi(q.Get("limit"))),
		Offset:    int(atoi(q.Get("offset"))),
	}
	rows, total, err := a.st.UserStats(f)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户统计失败")
		return
	}
	ok(w, J{"rows": rows, "total": total, "range_days": f.Days})
}

// GET /api/admin/stats/user/{id}/traffic?range= — one user's daily traffic, for
// the drill-down chart on a row.
func (a *API) handleAdminUserTraffic(w http.ResponseWriter, r *http.Request) {
	id := atoi(chi.URLParam(r, "id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, "无效的用户 id")
		return
	}
	days := rangeDays(r.URL.Query().Get("range"))
	rows, _ := a.st.UserDailyTraffic(id, days)
	ok(w, fillDays(rows, days))
}

// GET /api/admin/stats/traffic?range=7d|30d — site-wide daily traffic.
func (a *API) handleAdminTrafficStats(w http.ResponseWriter, r *http.Request) {
	days := rangeDays(r.URL.Query().Get("range"))
	rows, _ := a.st.SiteDailyTraffic(days)
	ok(w, fillDays(rows, days))
}

// GET /api/admin/stats/distribution?range=
//
// The two trends honor the same range as every other chart and come back as a
// gap-free window, so the caller can plot them directly against the traffic
// chart's x-axis instead of trying to reconcile three different day sets.
func (a *API) handleAdminDistribution(w http.ResponseWriter, r *http.Request) {
	days := rangeDays(r.URL.Query().Get("range"))
	dist, _ := a.st.StatusDistribution()
	reg, _ := a.st.RegistrationTrend(days)
	rev, _ := a.st.RevenueTrend(days)
	ok(w, J{
		"distribution": dist,
		"registration": fillPoints(reg, days),
		"revenue":      fillPoints(rev, days),
		"range_days":   days,
	})
}
