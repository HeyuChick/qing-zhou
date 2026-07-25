package store

import (
	"testing"
	"time"
)

// planStatusCount counts a user's plan buckets in the given status.
func planStatusCount(t *testing.T, st *Store, uid int64, status string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE user_id=? AND kind='plan' AND status=?`,
		uid, status).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// planIdentities is how many distinct plan identities orderBuckets would inject
// into the config for this user (i.e. how many nodes the subscription renders).
func planIdentities(t *testing.T, st *Store, uid, now int64) int {
	t.Helper()
	bs, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	ord := orderBuckets(bs, now, 0, func(int64) []int64 { return nil })
	n := 0
	for _, o := range ord {
		if o.b.Kind == "plan" {
			n++
		}
	}
	return n
}

// exhaustActive fills every ACTIVE plan bucket to its limit (simulating traffic
// use on the current head only — queued buckets keep used=0).
func exhaustActive(t *testing.T, st *Store, uid int64) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE user_plans SET used_down=traffic_limit
		WHERE user_id=? AND kind='plan' AND status='active' AND traffic_limit>0`, uid); err != nil {
		t.Fatal(err)
	}
}

// expireActive pushes every ACTIVE plan bucket's expiry into the past.
func expireActive(t *testing.T, st *Store, uid, past int64) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE user_plans SET expiry_at=?
		WHERE user_id=? AND kind='plan' AND status='active'`, past, uid); err != nil {
		t.Fatal(err)
	}
}

// Buying the SAME package multiple times no longer stacks: one head is active,
// the rest queue, and only the head renders as an identity.
func TestQueue_SamePackageQueues(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	if got := planStatusCount(t, st, uid, "active"); got != 1 {
		t.Fatalf("active plan buckets = %d, want 1", got)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 2 {
		t.Fatalf("queued plan buckets = %d, want 2", got)
	}
	if got := planIdentities(t, st, uid, time.Now().Unix()); got != 1 {
		t.Fatalf("rendered plan identities = %d, want 1 (no duplicate nodes)", got)
	}
	// The aggregate still reflects ALL purchased traffic (3×100G), even though only
	// one份 is usable now.
	u, _ := st.UserByID(uid)
	if u.TrafficLimit != 300*giB {
		t.Fatalf("aggregate traffic_limit = %d, want %d (3×100G)", u.TrafficLimit, int64(300*giB))
	}
}

// Exhausting the head promotes the next queued份, whose duration starts now.
func TestQueue_AdvanceOnExhaust(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bob")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	exhaustActive(t, st, uid)
	now := time.Now().Unix()
	changed, err := st.AdvanceAllQueues()
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != uid {
		t.Fatalf("AdvanceAllQueues changed = %v, want [%d]", changed, uid)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued after advance = %d, want 0", got)
	}
	// The promoted份 is usable now with a fresh ~30d expiry, so exactly one identity renders.
	if got := planIdentities(t, st, uid, now); got != 1 {
		t.Fatalf("identities after advance = %d, want 1", got)
	}
	var expiry int64
	if err := st.db.QueryRow(`SELECT expiry_at FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='active' AND used_up+used_down=0`, uid).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if wantMin := now + 29*86400; expiry < wantMin {
		t.Fatalf("promoted expiry=%d, want >= now+~30d (%d) — duration must start at activation", expiry, wantMin)
	}
}

// The head expiring (even with traffic left) promotes the next份.
func TestQueue_AdvanceOnExpiry(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carol")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	now := time.Now().Unix()
	expireActive(t, st, uid, now-3600) // head expired an hour ago, traffic untouched
	if _, err := st.AdvanceAllQueues(); err != nil {
		t.Fatal(err)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued after expiry advance = %d, want 0", got)
	}
	if got := planIdentities(t, st, uid, now); got != 1 {
		t.Fatalf("identities after expiry advance = %d, want 1", got)
	}
}

// Different packages are NOT queued — they stay independent and both active.
func TestQueue_DifferentPackagesParallel(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dave")
	a := mkPlan(t, st, "A", 100, 100, 30)
	b := mkPlan(t, st, "B", 100, 200, 30)
	buy(t, st, uid, a)
	buy(t, st, uid, b)

	if got := planStatusCount(t, st, uid, "active"); got != 2 {
		t.Fatalf("active = %d, want 2 (different packages run in parallel)", got)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued = %d, want 0", got)
	}
	if got := planIdentities(t, st, uid, time.Now().Unix()); got != 2 {
		t.Fatalf("identities = %d, want 2", got)
	}
}

// Refunding a queued order removes just that份 (full refund); refunding the active
// head removes it and promotes the next份.
func TestQueue_Refund(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "erin")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	o1 := buy(t, st, uid, pkg) // active head
	o2 := buy(t, st, uid, pkg) // queued

	// Refund the QUEUED order → full refund, its bucket vanishes, head untouched.
	_, q, err := st.RefundOrder(o2, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 100 {
		t.Fatalf("queued refund points = %d, want 100 (fully unused)", q.RefundPoints)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued after refund = %d, want 0", got)
	}
	if got := planStatusCount(t, st, uid, "active"); got != 1 {
		t.Fatalf("active after queued refund = %d, want 1 (head intact)", got)
	}

	// Now queue another and refund the ACTIVE head → next份 promoted.
	o3 := buy(t, st, uid, pkg) // queued behind o1
	_ = o3
	if _, _, err := st.RefundOrder(o1, 0, "prorated", noopSync); err != nil {
		t.Fatal(err)
	}
	if got := planIdentities(t, st, uid, time.Now().Unix()); got != 1 {
		t.Fatalf("identities after head refund = %d, want 1 (o3 promoted)", got)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued after head refund = %d, want 0 (o3 promoted)", got)
	}
}
