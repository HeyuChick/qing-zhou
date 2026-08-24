package store

import (
	"errors"
	"testing"
)

// planBuckets returns a user's plan buckets for the given package, oldest first.
func planBuckets(t *testing.T, st *Store, uid, pkgID int64) []*Bucket {
	t.Helper()
	all, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	var out []*Bucket
	for _, b := range all {
		if b.Kind == "plan" && b.PackageID == pkgID {
			out = append(out, b)
		}
	}
	return out
}

// Removing the ACTIVE head of a queue must free the slot immediately: the next
// queued份 is promoted in the same transaction and starts its countdown, rather
// than sitting invisible until the periodic ticker runs.
func TestDeleteBucket_PromotesQueuedHead(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dora")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg) // repeat purchase → queues behind the first

	bs := planBuckets(t, st, uid, pkg.ID)
	if len(bs) != 2 || bs[0].Status != "active" || bs[1].Status != "queued" {
		t.Fatalf("setup: want active+queued, got %+v", bs)
	}

	removed, err := st.DeleteBucket(uid, bs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != bs[0].ID {
		t.Errorf("returned bucket id %d, want %d", removed.ID, bs[0].ID)
	}

	after := planBuckets(t, st, uid, pkg.ID)
	if len(after) != 1 {
		t.Fatalf("want 1 bucket left, got %d", len(after))
	}
	if after[0].Status != "active" {
		t.Errorf("queued份 status = %q after head removal, want active", after[0].Status)
	}
	if after[0].ExpiryAt == 0 {
		t.Error("promoted份 has no expiry — its countdown never started")
	}

	// The legacy users.* mirror must follow the buckets, not keep the removed份's
	// quota around for the dashboard to advertise.
	var agg int64
	st.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, uid).Scan(&agg)
	if agg != 100*giB {
		t.Errorf("users.traffic_limit = %d GiB, want 100 (one份 left)", agg/giB)
	}
}

// A queued (not-yet-active)份 has never been spendable. Removing it must take
// its unused quota with it — otherwise "移除未生效套餐" leaves the user holding
// traffic they were never supposed to keep (issue #29).
func TestDeleteBucket_QueuedRemovesItsTraffic(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "quinn")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg) // queues behind the first

	bs := planBuckets(t, st, uid, pkg.ID)
	if len(bs) != 2 || bs[1].Status != "queued" {
		t.Fatalf("setup: want active+queued, got %+v", bs)
	}
	var before int64
	st.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, uid).Scan(&before)
	if before != 200*giB {
		t.Fatalf("setup: users.traffic_limit = %d GiB, want 200", before/giB)
	}

	if _, err := st.DeleteBucket(uid, bs[1].ID); err != nil {
		t.Fatal(err)
	}

	after := planBuckets(t, st, uid, pkg.ID)
	if len(after) != 1 || after[0].ID != bs[0].ID {
		t.Fatalf("want only the active head left, got %+v", after)
	}
	if after[0].Status != "active" {
		t.Errorf("head status = %q after queued removal, want active", after[0].Status)
	}
	var agg int64
	st.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, uid).Scan(&agg)
	if agg != 100*giB {
		t.Errorf("users.traffic_limit = %d GiB after removing queued份, want 100 (its 100G must go with it)", agg/giB)
	}
}

// The free bucket is the account's unmetered metering identity, not a granted
// 份 — removing it would re-route free-group traffic onto the paid pool.
func TestDeleteBucket_RefusesFreeBucket(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "eli")
	if err := st.EnsureFreeBucket(uid, "eli"); err != nil {
		t.Fatal(err)
	}
	var fid int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind=?`, uid, KindFree).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteBucket(uid, fid); !errors.Is(err, ErrBucketProtected) {
		t.Errorf("err = %v, want ErrBucketProtected", err)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE id=?`, fid).Scan(&n)
	if n != 1 {
		t.Error("free bucket was deleted despite the refusal")
	}
}

// Ownership is enforced by the query, not by the caller: one user's bucket id
// must not be removable through another user's route.
func TestDeleteBucket_WrongOwner(t *testing.T) {
	st := newRefundStore(t)
	owner := mkUser(t, st, "fay")
	other := mkUser(t, st, "gus")
	pkg := mkPlan(t, st, "50G/30d", 50, 50, 30)
	buy(t, st, owner, pkg)
	bs := planBuckets(t, st, owner, pkg.ID)

	if _, err := st.DeleteBucket(other, bs[0].ID); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("err = %v, want ErrBucketNotFound", err)
	}
	if got := planBuckets(t, st, owner, pkg.ID); len(got) != 1 {
		t.Error("owner's bucket was removed by another user's request")
	}
}

// ListBucketsBulk must return the same buckets ListBuckets does, keyed per user
// and not bleeding across accounts — the admin list's traffic roll-up rests on it.
func TestListBucketsBulk_MatchesPerUser(t *testing.T) {
	st := newRefundStore(t)
	a := mkUser(t, st, "hana")
	b := mkUser(t, st, "iris")
	pkg := mkPlan(t, st, "10G/30d", 10, 10, 30)
	buy(t, st, a, pkg)
	buy(t, st, a, pkg)
	buy(t, st, b, pkg)

	bulk, err := st.ListBucketsBulk([]int64{a, b})
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{a, b} {
		want, err := st.ListBuckets(uid)
		if err != nil {
			t.Fatal(err)
		}
		got := bulk[uid]
		if len(got) != len(want) {
			t.Fatalf("user %d: bulk returned %d buckets, ListBuckets %d", uid, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID || got[i].UserID != uid {
				t.Errorf("user %d bucket %d: got id=%d user=%d, want id=%d",
					uid, i, got[i].ID, got[i].UserID, want[i].ID)
			}
		}
	}
	if len(bulk) != 2 {
		t.Errorf("bulk has %d users, want 2", len(bulk))
	}
}
