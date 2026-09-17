package store

import (
	"testing"
	"time"
)

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := st.db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAutoRenewDefaultsAndFollowsRenewalLine(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "auto-renew-default")
	pkg := mkPlan(t, st, "100G / 30 天", 100, 100, 30)

	buy(t, st, uid, pkg)
	buckets, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || !buckets[0].AutoRenew {
		t.Fatalf("first bucket auto_renew = %#v, want enabled", buckets)
	}

	buy(t, st, uid, pkg)
	if err := st.SetPlanAutoRenew(uid, buckets[0].ID, false); err != nil {
		t.Fatal(err)
	}
	buckets, err = st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.Kind == "plan" && bucket.AutoRenew {
			t.Fatalf("bucket %d kept auto_renew enabled after line opt-out", bucket.ID)
		}
	}

	// A later manual purchase must inherit the line preference instead of silently
	// turning the next renewal back on.
	buy(t, st, uid, pkg)
	buckets, err = st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.Kind == "plan" && bucket.AutoRenew {
			t.Fatalf("new queued bucket %d re-enabled automatic renewal", bucket.ID)
		}
	}
}

func TestAutoRenewDuePlanChargesCurrentPriceAndActivatesReplacement(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "auto-renew-success")
	pkg := mkPlan(t, st, "100G / 30 天", 250, 100, 30)
	buy(t, st, uid, pkg)
	before, err := st.UserByID(uid)
	if err != nil {
		t.Fatal(err)
	}
	expireActive(t, st, uid, time.Now().Unix()-1)

	changed, err := st.AutoRenewDuePlans()
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != uid {
		t.Fatalf("renewed users = %v, want [%d]", changed, uid)
	}
	if got := planStatusCount(t, st, uid, StatusRetired); got != 1 {
		t.Fatalf("retired plan buckets = %d, want 1", got)
	}
	if got := planStatusCount(t, st, uid, "active"); got != 1 {
		t.Fatalf("active plan buckets = %d, want 1", got)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued plan buckets = %d, want 0", got)
	}
	after, err := st.UserByID(uid)
	if err != nil {
		t.Fatal(err)
	}
	if after.Points != before.Points-pkg.PricePoints {
		t.Fatalf("points = %d, want %d", after.Points, before.Points-pkg.PricePoints)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM orders WHERE user_id=? AND status='success'`, uid); got != 2 {
		t.Fatalf("successful orders = %d, want 2", got)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM point_transactions WHERE user_id=? AND type='auto_renew'`, uid); got != 1 {
		t.Fatalf("automatic renewal ledger rows = %d, want 1", got)
	}
	buckets, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.Status == "active" && !bucket.AutoRenew {
			t.Fatalf("replacement bucket %d lost auto_renew", bucket.ID)
		}
	}
}

func TestAutoRenewDuePlanWaitsForFundsOrManualQueue(t *testing.T) {
	t.Run("insufficient points", func(t *testing.T) {
		st := newRefundStore(t)
		uid := mkUser(t, st, "auto-renew-no-funds")
		pkg := mkPlan(t, st, "100G / 30 天", 100, 100, 30)
		buy(t, st, uid, pkg)
		if _, err := st.db.Exec(`UPDATE users SET points=0 WHERE id=?`, uid); err != nil {
			t.Fatal(err)
		}
		expireActive(t, st, uid, time.Now().Unix()-1)

		changed, err := st.AutoRenewDuePlans()
		if err != nil {
			t.Fatal(err)
		}
		if len(changed) != 0 {
			t.Fatalf("renewed users = %v, want none", changed)
		}
		if got := countRows(t, st, `SELECT COUNT(*) FROM orders WHERE user_id=?`, uid); got != 1 {
			t.Fatalf("orders = %d, want 1", got)
		}
		buckets, _ := st.ListBuckets(uid)
		if len(buckets) != 1 || !buckets[0].AutoRenew || buckets[0].Status != "active" {
			t.Fatalf("due bucket = %#v, want active auto-renew retry state", buckets)
		}
	})

	t.Run("queued manual renewal wins", func(t *testing.T) {
		st := newRefundStore(t)
		uid := mkUser(t, st, "auto-renew-manual-queue")
		pkg := mkPlan(t, st, "100G / 30 天", 100, 100, 30)
		buy(t, st, uid, pkg)
		buy(t, st, uid, pkg)
		expireActive(t, st, uid, time.Now().Unix()-1)

		changed, err := st.AutoRenewDuePlans()
		if err != nil {
			t.Fatal(err)
		}
		if len(changed) != 0 {
			t.Fatalf("renewed users = %v, want none", changed)
		}
		if got := countRows(t, st, `SELECT COUNT(*) FROM orders WHERE user_id=?`, uid); got != 2 {
			t.Fatalf("orders = %d, want the two manual purchases only", got)
		}
	})
}
