package api

import (
	"testing"
	"time"

	"qingzhou/internal/store"
)

func TestTrafficCapacityProjectionReservesExistingUsers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	next := now.Add(5 * 24 * time.Hour)
	p := buildTrafficCapacity(now, next, 2_000, store.ServerTrafficUsage{
		Total: 200, SampleCount: 10,
	}, store.ServerTrafficAttribution{
		CoverageStart: now.Add(-48 * time.Hour).Unix(), ActiveUsers: 2,
	}, store.ServerTrafficUsage{
		CoverageStart: now.Add(-48 * time.Hour).Unix(), Total: 400,
	})
	if !p.Available {
		t.Fatalf("projection unavailable: %+v", p)
	}
	// 400 bytes / 2 days = 200/day total = 100/user/day. Existing users need
	// another 1000 bytes; of the 1800 remaining, 800 can carry one full extra
	// user (500 bytes) through reset, but not two.
	if p.DailyRateBytes != 200 || p.PerUserDailyBytes != 100 || p.ProjectedCycleTotalBytes != 1200 || p.EstimatedAdditionalUsers != 1 {
		t.Fatalf("projection = %+v", p)
	}
}

func TestTrafficCapacityProjectionWaitsForAttributionWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	p := buildTrafficCapacity(now, now.AddDate(0, 0, 5), 2_000,
		store.ServerTrafficUsage{Total: 200, SampleCount: 10},
		store.ServerTrafficAttribution{CoverageStart: now.Add(-time.Hour).Unix(), ActiveUsers: 2},
		store.ServerTrafficUsage{CoverageStart: now.Add(-time.Hour).Unix(), Total: 50})
	if p.Available || p.EstimatedAdditionalUsers != 0 {
		t.Fatalf("one-hour window should not produce capacity: %+v", p)
	}
}
