package api

import (
	"testing"
	"time"

	"qingzhou/internal/store"
)

// An uncapped份 must not poison the metered ratio. Before this, its Used() was
// added to the numerator while its TrafficLimit (0) added nothing to the
// denominator, so holding one unlimited plan next to a metered one showed a
// used-percentage far past what the metered份 had actually consumed — and the
// UI's "流量已用尽" banner fired while an unlimited plan was still live.
func TestDashboardTraffic_UnlimitedBucketKeptOutOfRatio(t *testing.T) {
	buckets := []*store.Bucket{
		{ID: 1, Kind: "plan", Status: "active", TrafficLimit: 100 << 30, UsedUp: 10 << 30, UsedDown: 10 << 30},
		{ID: 2, Kind: "plan", Status: "active", TrafficLimit: 0, UsedUp: 400 << 30, UsedDown: 100 << 30}, // 不限量
	}

	d := dashboardTraffic(buckets)

	if d.Total != 100<<30 {
		t.Errorf("Total = %d, want %d (uncapped份 contributes no quota)", d.Total, int64(100)<<30)
	}
	if d.Used != 20<<30 {
		t.Errorf("Used = %d, want %d (uncapped份 usage must stay out)", d.Used, int64(20)<<30)
	}
	if d.Remaining != 80<<30 {
		t.Errorf("Remaining = %d, want %d", d.Remaining, int64(80)<<30)
	}
	if !d.Unlimited {
		t.Error("Unlimited = false, want true — the account holds an uncapped份")
	}
	if d.UnmeteredUsed != 500<<30 {
		t.Errorf("UnmeteredUsed = %d, want %d", d.UnmeteredUsed, int64(500)<<30)
	}
}

// Total == 0 is ambiguous on its own: it is what both "只有不限量份额" and
// "根本没有份额" produce. Unlimited is what tells them apart, and the two render
// as 不限 vs 无额度 — opposite meanings.
func TestDashboardTraffic_ZeroTotalIsNotAlwaysUnlimited(t *testing.T) {
	noPlans := dashboardTraffic([]*store.Bucket{
		{ID: 1, Kind: store.KindFree, TrafficLimit: 0, UsedUp: 1 << 30},
	})
	if noPlans.Total != 0 || noPlans.Unlimited {
		t.Errorf("free-only account: Total=%d Unlimited=%v, want 0/false", noPlans.Total, noPlans.Unlimited)
	}
	if noPlans.UnmeteredUsed != 0 {
		t.Errorf("free bucket must be excluded entirely, got UnmeteredUsed=%d", noPlans.UnmeteredUsed)
	}

	onlyUnlimited := dashboardTraffic([]*store.Bucket{
		{ID: 2, Kind: "plan", Status: "active", TrafficLimit: 0, UsedUp: 1 << 30},
	})
	if onlyUnlimited.Total != 0 || !onlyUnlimited.Unlimited {
		t.Errorf("unlimited-only account: Total=%d Unlimited=%v, want 0/true", onlyUnlimited.Total, onlyUnlimited.Unlimited)
	}
}

// Every account carries an empty pool bucket (limit 0) as bookkeeping. Reading
// that 0 as "uncapped" the way a real unlimited plan's 0 is read would show 不限
// to every user on the panel, including one with a plain metered plan.
func TestDashboardTraffic_EmptyPoolIsNotUnlimited(t *testing.T) {
	d := dashboardTraffic([]*store.Bucket{
		{ID: 1, Kind: "plan", Status: "active", TrafficLimit: 10 << 30, UsedUp: 1 << 30},
		{ID: 2, Kind: "pool", Status: "active", TrafficLimit: 0},
	})
	if d.Unlimited {
		t.Error("Unlimited = true, want false — an empty pool grants nothing")
	}
	if d.Total != 10<<30 {
		t.Errorf("Total = %d, want %d", d.Total, int64(10)<<30)
	}

	// A pool that actually holds traffic still counts, as it always did.
	withPool := dashboardTraffic([]*store.Bucket{
		{ID: 3, Kind: "pool", Status: "active", TrafficLimit: 5 << 30, UsedUp: 1 << 30},
	})
	if withPool.Total != 5<<30 || withPool.Used != 1<<30 {
		t.Errorf("funded pool: Total=%d Used=%d, want %d/%d", withPool.Total, withPool.Used, int64(5)<<30, int64(1)<<30)
	}
}

// Queued份 are paid for but not usable yet; advertising their quota in the
// headline overstates what the user can spend today. An exhausted份 is the
// opposite case — still owned, just drained — and must stay in so the bar can
// read 100% instead of vanishing.
func TestDashboardTraffic_ExcludesQueuedKeepsExhausted(t *testing.T) {
	future := time.Now().Unix() + 86400
	buckets := []*store.Bucket{
		{ID: 1, Kind: "plan", Status: "active", TrafficLimit: 10 << 30, UsedUp: 12 << 30, ExpiryAt: future},
		{ID: 2, Kind: "plan", Status: "queued", TrafficLimit: 50 << 30, ExpiryAt: future},
		{ID: 3, Kind: store.KindFree, TrafficLimit: 5 << 30, UsedUp: 5 << 30},
	}

	d := dashboardTraffic(buckets)

	if d.Total != 10<<30 {
		t.Errorf("Total = %d, want %d (queued + free excluded, exhausted kept)", d.Total, int64(10)<<30)
	}
	if d.Remaining != 0 {
		t.Errorf("Remaining = %d, want 0 — an over-quota account must not go negative", d.Remaining)
	}
}

// An expired份 grants nothing at subscription time, so counting its quota puts
// "剩余流量 10 GB" directly under the "套餐已全部到期" banner.
func TestDashboardTraffic_ExcludesExpired(t *testing.T) {
	past := time.Now().Unix() - 86400
	future := time.Now().Unix() + 86400

	allExpired := dashboardTraffic([]*store.Bucket{
		{ID: 1, Kind: "plan", Status: "active", TrafficLimit: 10 << 30, ExpiryAt: past},
	})
	if allExpired.Total != 0 || allExpired.Remaining != 0 {
		t.Errorf("expired-only: Total=%d Remaining=%d, want 0/0", allExpired.Total, allExpired.Remaining)
	}
	if allExpired.Unlimited {
		t.Error("expired-only: Unlimited = true, want false")
	}

	// 不过期的份 (ExpiryAt 0) must not be mistaken for expired.
	mixed := dashboardTraffic([]*store.Bucket{
		{ID: 1, Kind: "plan", Status: "active", TrafficLimit: 10 << 30, ExpiryAt: past},
		{ID: 2, Kind: "plan", Status: "active", TrafficLimit: 20 << 30, ExpiryAt: future},
		{ID: 3, Kind: "plan", Status: "active", TrafficLimit: 30 << 30, ExpiryAt: 0},
	})
	if mixed.Total != 50<<30 {
		t.Errorf("Total = %d, want %d (only the expired份 drops out)", mixed.Total, int64(50)<<30)
	}
}
