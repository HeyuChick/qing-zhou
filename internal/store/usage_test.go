package store

import "testing"

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
