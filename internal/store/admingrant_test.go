package store

import "testing"

func adminBucket(t *testing.T, st *Store, uid int64) *Bucket {
	t.Helper()
	bs, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID == 0 {
			return b
		}
	}
	return nil
}

// An admin manual grant must land in a real bucket (so it's actually enforced),
// survive the user's next purchase recompute (the old bug reverted it), and be
// removable.
func TestAdminGrant_LandsInBucketAndSurvivesPurchase(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "frank")

	// Grant 100 GiB, no expiry.
	if err := st.AdminUpdateUser(uid, "active", false, &ManualGrant{Enabled: true, Traffic: 100 * giB}); err != nil {
		t.Fatal(err)
	}
	b := adminBucket(t, st, uid)
	if b == nil || b.TrafficLimit != 100*giB {
		t.Fatalf("admin bucket = %+v, want 100GiB grant", b)
	}
	// Aggregate mirrors it.
	var agg int64
	st.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, uid).Scan(&agg)
	if agg != 100*giB {
		t.Errorf("users.traffic_limit = %d, want 100GiB (mirrored from bucket)", agg)
	}

	// A subsequent purchase recomputes the aggregate — the grant must NOT vanish
	// (this is exactly what the legacy users.* write failed at).
	pkg := mkPlan(t, st, "P", 100, 50, 30)
	if _, err := st.Purchase(uid, pkg, "", noopSync); err != nil {
		t.Fatal(err)
	}
	if adminBucket(t, st, uid) == nil {
		t.Error("admin grant bucket vanished after a purchase")
	}
	st.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, uid).Scan(&agg)
	if agg != 150*giB { // 100 grant + 50 plan
		t.Errorf("users.traffic_limit = %d GiB, want 150 (grant 100 + plan 50)", agg/giB)
	}

	// Disabling the grant removes the bucket; the plan stays.
	if err := st.AdminUpdateUser(uid, "active", false, &ManualGrant{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if adminBucket(t, st, uid) != nil {
		t.Error("admin grant bucket should be gone after disabling")
	}
	st.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, uid).Scan(&agg)
	if agg != 50*giB {
		t.Errorf("users.traffic_limit = %d GiB after removing grant, want 50 (plan only)", agg/giB)
	}
}

// A nil grant leaves any existing grant untouched (edit that only flips status).
func TestAdminGrant_NilLeavesGrantUntouched(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "grace")
	if err := st.AdminUpdateUser(uid, "active", false, &ManualGrant{Enabled: true, Traffic: 10 * giB}); err != nil {
		t.Fatal(err)
	}
	// Ban with no grant field — grant must remain.
	if err := st.AdminUpdateUser(uid, "banned", false, nil); err != nil {
		t.Fatal(err)
	}
	if b := adminBucket(t, st, uid); b == nil || b.TrafficLimit != 10*giB {
		t.Errorf("grant lost on a status-only edit: %+v", b)
	}
	var status string
	st.db.QueryRow(`SELECT status FROM users WHERE id=?`, uid).Scan(&status)
	if status != "banned" {
		t.Errorf("status = %q, want banned", status)
	}
}
