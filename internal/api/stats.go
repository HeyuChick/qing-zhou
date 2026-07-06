package api

import (
	"net/http"
	"time"

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

// onlineWindow is how recently a user must have transferred traffic to count as
// "online" (the stats poll runs ~every minute).
const onlineWindow = 300

func rangeDays(r string) int {
	switch r {
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
	online, _ := a.st.OnlineCount(onlineWindow)
	onlineUsers, _ := a.st.OnlineUsers(onlineWindow, 20)
	_ = a.st.PruneTrafficSamples(35) // opportunistic retention trim
	ok(w, J{
		"total_users":   ov.TotalUsers,
		"active_users":  ov.ActiveUsers,
		"new_today":     ov.NewToday,
		"total_traffic": ov.TotalTraffic,
		"points_issued": ov.PointsIssued,
		"packages_on":   ov.PackagesOn,
		"online":        online,
		"online_users":  onlineUsers,
	})
}

// GET /api/admin/stats/traffic?range=7d|30d — site-wide daily traffic.
func (a *API) handleAdminTrafficStats(w http.ResponseWriter, r *http.Request) {
	days := rangeDays(r.URL.Query().Get("range"))
	rows, _ := a.st.SiteDailyTraffic(days)
	ok(w, fillDays(rows, days))
}

// GET /api/admin/stats/top
func (a *API) handleAdminTopStats(w http.ResponseWriter, r *http.Request) {
	traffic, _ := a.st.TopByTraffic(10)
	spend, _ := a.st.TopBySpend(10)
	sales, _ := a.st.PackageSales(10)
	ok(w, J{"traffic": traffic, "spend": spend, "package_sales": sales})
}

// GET /api/admin/stats/distribution
func (a *API) handleAdminDistribution(w http.ResponseWriter, r *http.Request) {
	dist, _ := a.st.StatusDistribution()
	reg, _ := a.st.RegistrationTrend(30)
	rev, _ := a.st.RevenueTrend(30)
	ok(w, J{"distribution": dist, "registration": reg, "revenue": rev})
}
