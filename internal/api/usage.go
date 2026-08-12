package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"qingzhou/internal/store"
)

// The usage report: per-user and per-package traffic over a chosen window.
//
// Two modes, and the response always says which one produced the numbers:
//
//	lifetime — running counters, accurate for the whole life of the account.
//	window   — the traffic_daily rollup, accurate only from the day it started
//	           recording (coverage.first). An account older than that has real
//	           traffic the rollup never saw, so presenting a windowed number as
//	           a lifetime one would understate it without saying so.
//
// The client shows coverage.first next to any windowed figure. This is the
// whole reason the modes are not merged: an admin reconciling a bill needs to
// know whether a number is "everything" or "everything we have".

const (
	usageModeLifetime = "lifetime"
	usageModeWindow   = "window"
)

// maxUsageUsers caps how many users one report may select. Well past any real
// comparison (the chart is unreadable long before this) and low enough that the
// IN list stays inside SQLite's parameter limit with room to spare.
const maxUsageUsers = 100

// parseUserIDs reads the repeated/comma-joined `users` param. An empty result
// means "every user", which is the report's default view.
func parseUserIDs(r *http.Request) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, raw := range r.URL.Query()["users"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id <= 0 || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
			if len(out) >= maxUsageUsers {
				return out
			}
		}
	}
	return out
}

// validDay accepts only a YYYY-MM-DD calendar date. Anything else is dropped
// rather than passed through: the value reaches a string comparison against
// traffic_daily.day, and a half-parsed date silently widens the window instead
// of erroring.
func validDay(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

// usageWindowFromQuery resolves preset/from/to into a mode and a window.
//
//	preset=total          -> lifetime
//	preset=30d | 7d | 90d -> trailing window
//	preset=custom         -> from/to (either side may be open)
//
// An unparseable or reversed custom range falls back to the 30-day preset
// rather than querying an empty window: a report that silently shows zero for
// everyone reads as "nobody used anything", which is a wrong answer, not a
// missing one.
func usageWindowFromQuery(r *http.Request) (mode string, w store.UsageWindow, preset string) {
	q := r.URL.Query()
	preset = q.Get("preset")
	switch preset {
	case "total":
		return usageModeLifetime, store.UsageWindow{}, "total"
	case "custom":
		from, to := validDay(q.Get("from")), validDay(q.Get("to"))
		if from != "" && to != "" && from > to {
			from, to = to, from // an inverted range is a slip, not a request for nothing
		}
		if from == "" && to == "" {
			break // nothing usable — fall through to the default preset
		}
		return usageModeWindow, store.UsageWindow{From: from, To: to}, "custom"
	case "7d", "14d", "90d":
		d := rangeDays(preset)
		return usageModeWindow, store.UsageWindow{From: store.DaysAgo(d - 1)}, preset
	}
	return usageModeWindow, store.UsageWindow{From: store.DaysAgo(29)}, "30d"
}

// GET /api/admin/stats/usage?preset=total|7d|14d|30d|90d|custom&from=&to=&users=1,2
//
// Returns everything the report renders in one round trip: the per-day series
// (per user and combined), per-user totals, per-package totals, and the rollup's
// coverage so the client can caveat the window.
func (a *API) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	userIDs := parseUserIDs(r)
	mode, win, preset := usageWindowFromQuery(r)

	first, last, err := a.st.TrafficDailyCoverage()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用量统计失败")
		return
	}

	resp := J{
		"mode":     mode,
		"preset":   preset,
		"from":     win.From,
		"to":       win.To,
		"users":    userIDs,
		"coverage": J{"first": first, "last": last},
	}

	if mode == usageModeLifetime {
		byUser, err := a.st.UsageLifetimeByUser(userIDs)
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取用量统计失败")
			return
		}
		byPkg, err := a.st.UsageByPackageLifetime(userIDs)
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取套餐用量失败")
			return
		}
		// The daily curve has no lifetime equivalent — the counters are scalars.
		// Send the rollup's full extent so the chart still has something true to
		// draw, flagged by series_scope so the client can label it.
		series, err := a.st.UsageDailyByUser(userIDs, store.UsageWindow{})
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取用量曲线失败")
			return
		}
		total, err := a.st.UsageDailyTotal(userIDs, store.UsageWindow{})
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取用量曲线失败")
			return
		}
		resp["by_user"] = nonNilTotals(trimZeroTotals(byUser))
		resp["by_package"] = nonNilTotals(byPkg)
		resp["series"] = nonNilSeries(series)
		resp["total_series"] = nonNilDays(total)
		resp["series_scope"] = "recorded" // curve covers only what the rollup holds
		ok(w, resp)
		return
	}

	byUser, err := a.st.UsageWindowedByUser(userIDs, win)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用量统计失败")
		return
	}
	byPkg, err := a.st.UsageByPackageWindowed(userIDs, win)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取套餐用量失败")
		return
	}
	series, err := a.st.UsageDailyByUser(userIDs, win)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用量曲线失败")
		return
	}
	total, err := a.st.UsageDailyTotal(userIDs, win)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用量曲线失败")
		return
	}
	resp["by_user"] = nonNilTotals(byUser)
	resp["by_package"] = nonNilTotals(byPkg)
	resp["series"] = nonNilSeries(series)
	resp["total_series"] = nonNilDays(total)
	resp["series_scope"] = "window"
	ok(w, resp)
}

// GET /api/admin/stats/usage/users?q=&limit= — the report's user picker.
func (a *API) handleAdminUsageUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.UsageUserCandidates(r.URL.Query().Get("q"), int(atoi(r.URL.Query().Get("limit"))))
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取用户列表失败")
		return
	}
	if rows == nil {
		rows = []store.UsageCandidate{}
	}
	ok(w, rows)
}

// trimZeroTotals drops users with no traffic at all. Only applied to the
// lifetime per-user list, which otherwise returns every account on the panel
// including the ones that never connected — hundreds of zero rows an admin has
// to scroll past to reach the data. The windowed query already excludes them by
// construction (no rows, no traffic).
func trimZeroTotals(in []store.UsageTotal) []store.UsageTotal {
	out := in[:0]
	for _, t := range in {
		if t.Up+t.Down > 0 {
			out = append(out, t)
		}
	}
	return out
}

// The three nonNil* helpers keep the JSON shape stable: encoding/json renders a
// nil slice as null, and the client's chart code would then have to guard every
// field it iterates.
func nonNilTotals(in []store.UsageTotal) []store.UsageTotal {
	if in == nil {
		return []store.UsageTotal{}
	}
	return in
}

func nonNilSeries(in []store.UsageSeries) []store.UsageSeries {
	if in == nil {
		return []store.UsageSeries{}
	}
	for i := range in {
		if in[i].Days == nil {
			in[i].Days = []store.UsageDay{}
		}
	}
	return in
}

func nonNilDays(in []store.UsageDay) []store.UsageDay {
	if in == nil {
		return []store.UsageDay{}
	}
	return in
}
