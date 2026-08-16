package store

import "testing"

// Free-group traffic must never count against the paid quota in the users.*
// mirror. recomputeUserAggregate already excluded the free bucket, but the
// per-poll mirror (applyBucketUsage) added every bucket's delta — free included —
// so between entitlement events users.used_up drifted above the sum of the
// metered buckets until the quota check tripped on traffic the user never spent.
func TestUsage_FreeBucketDoesNotDriftUserAggregate(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "hank")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	if err := st.EnsureFreeBucket(uid, "hank"); err != nil {
		t.Fatal(err)
	}

	var freeName string
	if err := st.db.QueryRow(`SELECT client_name FROM user_plans WHERE user_id=? AND kind=?`,
		uid, KindFree).Scan(&freeName); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBucketUsage(freeName, 30*giB, 30*giB); err != nil {
		t.Fatal(err)
	}

	u, _ := st.UserByID(uid)
	if got := u.UsedUp + u.UsedDown; got != 0 {
		t.Fatalf("users.used = %d after 60G of FREE traffic, want 0 — free usage must not eat the paid quota", got)
	}
	// The free bucket itself still records it: the traffic happened, it just is
	// not billable.
	var bucketUsed int64
	if err := st.db.QueryRow(`SELECT used_up+used_down FROM user_plans WHERE user_id=? AND kind=?`,
		uid, KindFree).Scan(&bucketUsed); err != nil {
		t.Fatal(err)
	}
	if bucketUsed != 60*giB {
		t.Fatalf("free bucket recorded %d, want 60G — the usage itself must still be tracked", bucketUsed)
	}
}

// Paid traffic must still reach the mirror — the free-bucket skip must not have
// switched the counter off for everyone.
func TestUsage_PaidBucketStillMirrors(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "ivy")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)

	var planName string
	if err := st.db.QueryRow(`SELECT client_name FROM user_plans WHERE user_id=? AND kind='plan' AND package_id>0`,
		uid).Scan(&planName); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBucketUsage(planName, 5*giB, 3*giB); err != nil {
		t.Fatal(err)
	}

	u, _ := st.UserByID(uid)
	if u.UsedUp != 5*giB || u.UsedDown != 3*giB {
		t.Fatalf("users.used = %d/%d, want 5G/3G", u.UsedUp, u.UsedDown)
	}
}
