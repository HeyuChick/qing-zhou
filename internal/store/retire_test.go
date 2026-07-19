package store

import "testing"

func userPoints(t *testing.T, st *Store, uid int64) int64 {
	t.Helper()
	var p int64
	if err := st.db.QueryRow(`SELECT points FROM users WHERE id=?`, uid).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func hasPlanBucket(t *testing.T, st *Store, uid, pkgID int64) bool {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE user_id=? AND kind='plan' AND package_id=?`,
		uid, pkgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// A user who buys plan A then plan B has current_plan_id=B, but still holds a live
// A bucket. Retiring A must find that user (via buckets, not the current_plan_id
// pointer), refund A, and clear A's bucket — while leaving B untouched. This is the
// P0-2 regression: keying retire off current_plan_id silently skipped this holder.
func TestRetire_HolderNotPointedTo(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	planA := mkPlan(t, st, "A", 100, 50, 30)
	planB := mkPlan(t, st, "B", 200, 50, 30)

	orderA := buy(t, st, uid, planA)
	buy(t, st, uid, planB)

	// current_plan_id now points at B, yet A must still be discoverable.
	holders, err := st.PackagePlanHolders(planA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(holders) != 1 || holders[0] != uid {
		t.Fatalf("PackagePlanHolders(A) = %v, want [%d]", holders, uid)
	}

	before := userPoints(t, st, uid)

	// Retire A: refund every refundable order, then clear the bucket (mirrors handler).
	orders, err := st.RefundableOrdersForPackage(uid, planA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0] != orderA {
		t.Fatalf("RefundableOrdersForPackage(A) = %v, want [%d]", orders, orderA)
	}
	for _, oid := range orders {
		if _, _, err := st.RefundOrder(oid, 0, "", noopSync); err != nil {
			t.Fatalf("refund order %d: %v", oid, err)
		}
	}
	if err := st.ClearPlanBucket(uid, planA.ID); err != nil {
		t.Fatal(err)
	}

	// A fully refunded (unused) → full 100 points back.
	if got := userPoints(t, st, uid); got != before+100 {
		t.Errorf("points after retire = %d, want %d", got, before+100)
	}
	if hasPlanBucket(t, st, uid, planA.ID) {
		t.Error("A bucket should be gone after retire")
	}
	if !hasPlanBucket(t, st, uid, planB.ID) {
		t.Error("B bucket must survive A's retire")
	}
}

// Retiring a plan the user renewed (bought twice, stacked into one bucket) must
// refund BOTH orders, not just the latest, and leave no remnant bucket.
func TestRetire_RefundsStackedRenewals(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bob")
	plan := mkPlan(t, st, "P", 100, 50, 30)

	buy(t, st, uid, plan)
	buy(t, st, uid, plan) // renewal — stacks into the same bucket

	before := userPoints(t, st, uid)

	orders, err := st.RefundableOrdersForPackage(uid, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 2 {
		t.Fatalf("RefundableOrdersForPackage = %v, want 2 orders", orders)
	}
	for _, oid := range orders {
		if _, _, err := st.RefundOrder(oid, 0, "", noopSync); err != nil {
			t.Fatalf("refund order %d: %v", oid, err)
		}
	}
	if err := st.ClearPlanBucket(uid, plan.ID); err != nil {
		t.Fatal(err)
	}

	if got := userPoints(t, st, uid); got != before+200 {
		t.Errorf("points after retiring both renewals = %d, want %d", got, before+200)
	}
	if hasPlanBucket(t, st, uid, plan.ID) {
		t.Error("bucket should be gone after refunding all orders + clear")
	}
}
