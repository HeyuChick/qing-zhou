package store

import "testing"

// A repeated purchase carrying the SAME idempotency key must charge once and return
// the same order — a client retry after a lost response must not double-charge. A
// different key (or none) is a distinct purchase.
func TestPurchase_Idempotent(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "erin")
	pkg := mkPlan(t, st, "P", 100, 50, 30)

	before := userPoints(t, st, uid)
	r1, err := st.Purchase(uid, pkg, "key-abc", noopSync)
	if err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	r2, err := st.Purchase(uid, pkg, "key-abc", noopSync)
	if err != nil {
		t.Fatalf("retry purchase: %v", err)
	}
	if r1.Order.ID != r2.Order.ID {
		t.Errorf("retry returned order %d, want same as first %d", r2.Order.ID, r1.Order.ID)
	}
	if got := userPoints(t, st, uid); got != before-100 {
		t.Errorf("charged %d points, want 100 (single charge)", before-got)
	}

	// A different key is a genuine second purchase (renewal): charges again.
	if _, err := st.Purchase(uid, pkg, "key-xyz", noopSync); err != nil {
		t.Fatalf("second distinct purchase: %v", err)
	}
	if got := userPoints(t, st, uid); got != before-200 {
		t.Errorf("after distinct purchase charged total %d, want 200", before-got)
	}
}

// AddUsageBatch must apply every known identity's delta in one shot: bucket
// counter, mirrored user aggregate, and a time-series sample per identity — while
// silently skipping identities that don't resolve to a bucket (a just-removed
// client) rather than failing the whole poll.
func TestAddUsageBatch(t *testing.T) {
	st := newRefundStore(t)
	a := mkUser(t, st, "carol")
	b := mkUser(t, st, "dave")
	if err := st.EnsurePoolBucket(a, "qz_carol", "uuid-a", "sec-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(b, "qz_dave", "uuid-b", "sec-b"); err != nil {
		t.Fatal(err)
	}

	applied, err := st.AddUsageBatch(map[string]UsageDelta{
		"qz_carol": {Up: 100, Down: 200},
		"qz_dave":  {Up: 10, Down: 20},
		"qz_ghost": {Up: 5, Down: 5}, // no bucket → skipped, not an error
		"qz_zero":  {Up: 0, Down: 0}, // no delta → skipped
	})
	if err != nil {
		t.Fatalf("AddUsageBatch: %v", err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}

	check := func(uid, wantUp, wantDown int64) {
		t.Helper()
		var bUp, bDown, uUp, uDown int64
		if err := st.db.QueryRow(`SELECT used_up, used_down FROM user_plans WHERE user_id=? AND kind='pool'`, uid).Scan(&bUp, &bDown); err != nil {
			t.Fatal(err)
		}
		if bUp != wantUp || bDown != wantDown {
			t.Errorf("uid %d bucket used = %d/%d, want %d/%d", uid, bUp, bDown, wantUp, wantDown)
		}
		if err := st.db.QueryRow(`SELECT used_up, used_down FROM users WHERE id=?`, uid).Scan(&uUp, &uDown); err != nil {
			t.Fatal(err)
		}
		if uUp != wantUp || uDown != wantDown {
			t.Errorf("uid %d user aggregate = %d/%d, want %d/%d", uid, uUp, uDown, wantUp, wantDown)
		}
	}
	check(a, 100, 200)
	check(b, 10, 20)

	// Exactly one sample per applied identity; ghost/zero wrote none.
	var totalSamples int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_samples`).Scan(&totalSamples); err != nil {
		t.Fatal(err)
	}
	if totalSamples != 2 {
		t.Errorf("total samples = %d, want 2 (ghost/zero wrote none)", totalSamples)
	}

	// A second batch accumulates (deltas, not absolutes).
	if _, err := st.AddUsageBatch(map[string]UsageDelta{"qz_carol": {Up: 1, Down: 2}}); err != nil {
		t.Fatal(err)
	}
	check(a, 101, 202)
}

func TestAddUsageBatchesPreservesServerSource(t *testing.T) {
	st := newRefundStore(t)
	carol := mkUser(t, st, "source-carol")
	dave := mkUser(t, st, "source-dave")
	if err := st.EnsurePoolBucket(carol, "qz_source_carol", "uuid-a", "sec-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePoolBucket(dave, "qz_source_dave", "uuid-b", "sec-b"); err != nil {
		t.Fatal(err)
	}
	applied, err := st.AddUsageBatchesByServer(map[int64]map[string]UsageDelta{
		1: {"qz_source_carol": {Up: 100, Down: 50}},
		2: {
			"qz_source_carol": {Up: 20, Down: 30},
			"qz_source_dave":  {Up: 10, Down: 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 3 {
		t.Fatalf("applied=%d, want three server/identity deltas", applied)
	}
	var up, down int64
	if err := st.db.QueryRow(`SELECT used_up,used_down FROM users WHERE id=?`, carol).Scan(&up, &down); err != nil {
		t.Fatal(err)
	}
	if up != 120 || down != 80 {
		t.Fatalf("global billing lost cross-server sum: %d/%d", up, down)
	}
	one, err := st.ServerTrafficAttribution(1, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 150 || one.ActiveUsers != 1 || len(one.Sources) != 1 || one.Sources[0].Username != "source-carol" {
		t.Fatalf("server 1 attribution = %+v", one)
	}
	two, err := st.ServerTrafficAttribution(2, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if two.Total != 70 || two.ActiveUsers != 2 || len(two.Sources) != 2 || two.Sources[0].Total != 50 {
		t.Fatalf("server 2 attribution = %+v", two)
	}
}
