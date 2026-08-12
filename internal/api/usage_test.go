package api

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"qingzhou/internal/store"
)

// parseUserIDs is the report's only untrusted input path. It must accept the
// shapes a client actually sends, and drop everything else rather than letting
// it reach the query builder.
func TestParseUserIDs(t *testing.T) {
	for _, c := range []struct {
		query string
		want  []int64
	}{
		{"", nil},
		{"?users=3", []int64{3}},
		{"?users=3,1,2", []int64{3, 1, 2}},        // order preserved
		{"?users=3&users=4", []int64{3, 4}},       // repeated param
		{"?users=3,3,3", []int64{3}},              // de-duplicated
		{"?users=%205%20,%206%20", []int64{5, 6}}, // whitespace tolerated
		{"?users=0,-1,abc,1", []int64{1}},         // junk and non-positives dropped
		{"?users=,,,", nil},                       // empty segments
		{"?users=1;DROP+TABLE+users", nil},        // not a number, not passed on
		{"?users=99999999999999999999", nil},      // overflows int64
	} {
		r := httptest.NewRequest("GET", "/api/admin/stats/usage"+c.query, nil)
		got := parseUserIDs(r)
		if len(got) != len(c.want) {
			t.Errorf("%q -> %v, want %v", c.query, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q -> %v, want %v", c.query, got, c.want)
				break
			}
		}
	}
}

// A client must not be able to blow the IN list (and SQLite's parameter limit)
// by sending thousands of ids.
func TestParseUserIDs_Capped(t *testing.T) {
	q := "?users="
	for i := 1; i <= maxUsageUsers+50; i++ {
		if i > 1 {
			q += ","
		}
		q += strconv.Itoa(i)
	}
	r := httptest.NewRequest("GET", "/api/admin/stats/usage"+q, nil)
	if got := parseUserIDs(r); len(got) != maxUsageUsers {
		t.Errorf("selected %d users, want the cap %d", len(got), maxUsageUsers)
	}
}

// validDay is what stands between a query param and a string comparison against
// traffic_daily.day. A partially-valid date must be rejected outright, not
// silently widen the window.
func TestValidDay(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"2026-08-12", "2026-08-12"},
		{"  2026-08-12  ", "2026-08-12"},
		{"2026-8-12", ""},   // not zero-padded
		{"2026-13-01", ""},  // month out of range
		{"2026-02-30", ""},  // day out of range for the month
		{"20260812", ""},    // wrong format
		{"yesterday", ""},   // not a date
		{"2026-08-12'", ""}, // quote smuggling
		{"", ""},
	} {
		if got := validDay(c.in); got != c.want {
			t.Errorf("validDay(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The preset decides which of the two data sources answers, so a wrong mode is
// a wrong number, not a cosmetic issue.
func TestUsageWindowFromQuery(t *testing.T) {
	for _, c := range []struct {
		query      string
		wantMode   string
		wantPreset string
		wantFromNZ bool // From should be non-empty
		wantTo     string
	}{
		{"", usageModeWindow, "30d", true, ""},
		{"?preset=total", usageModeLifetime, "total", false, ""},
		{"?preset=7d", usageModeWindow, "7d", true, ""},
		{"?preset=90d", usageModeWindow, "90d", true, ""},
		{"?preset=bogus", usageModeWindow, "30d", true, ""},
		{"?preset=custom&from=2026-01-01&to=2026-02-01", usageModeWindow, "custom", true, "2026-02-01"},
		// Open-ended custom ranges are legitimate: "everything since launch day".
		{"?preset=custom&from=2026-01-01", usageModeWindow, "custom", true, ""},
		// Neither side usable — fall back to the default rather than query nothing.
		{"?preset=custom&from=nonsense", usageModeWindow, "30d", true, ""},
	} {
		r := httptest.NewRequest("GET", "/api/admin/stats/usage"+c.query, nil)
		mode, win, preset := usageWindowFromQuery(r)
		if mode != c.wantMode || preset != c.wantPreset {
			t.Errorf("%q -> mode=%q preset=%q, want %q/%q", c.query, mode, preset, c.wantMode, c.wantPreset)
		}
		if (win.From != "") != c.wantFromNZ {
			t.Errorf("%q -> From=%q, wantNonEmpty=%v", c.query, win.From, c.wantFromNZ)
		}
		if win.To != c.wantTo {
			t.Errorf("%q -> To=%q, want %q", c.query, win.To, c.wantTo)
		}
	}
}

// An inverted custom range is a slip (the admin picked the dates backwards);
// swapping is what they meant, and returning nothing is not.
func TestUsageWindowFromQuery_InvertedRangeSwapped(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/admin/stats/usage?preset=custom&from=2026-03-01&to=2026-01-01", nil)
	_, win, preset := usageWindowFromQuery(r)
	if preset != "custom" {
		t.Fatalf("preset = %q, want custom", preset)
	}
	if win.From != "2026-01-01" || win.To != "2026-03-01" {
		t.Errorf("range = %q..%q, want 2026-01-01..2026-03-01", win.From, win.To)
	}
}

// The JSON shape must not contain nulls where the client iterates.
func TestUsageNonNilHelpers(t *testing.T) {
	if got := nonNilTotals(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNilTotals(nil) = %v, want empty slice", got)
	}
	if got := nonNilDays(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNilDays(nil) = %v, want empty slice", got)
	}
	s := nonNilSeries([]store.UsageSeries{{UserID: 1}})
	if s[0].Days == nil {
		t.Error("nonNilSeries left Days nil")
	}
	if got := nonNilSeries(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNilSeries(nil) = %v, want empty slice", got)
	}
}

// trimZeroTotals exists so the lifetime view isn't hundreds of never-connected
// accounts; it must keep every row that has any traffic.
func TestTrimZeroTotals(t *testing.T) {
	in := []store.UsageTotal{
		{UserID: 1, Up: 0, Down: 0},
		{UserID: 2, Up: 1, Down: 0},
		{UserID: 3, Up: 0, Down: 0},
		{UserID: 4, Up: 0, Down: 5},
	}
	got := trimZeroTotals(in)
	if len(got) != 2 || got[0].UserID != 2 || got[1].UserID != 4 {
		t.Errorf("trimZeroTotals = %+v, want users 2 and 4", got)
	}
}
