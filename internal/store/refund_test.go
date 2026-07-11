package store

import (
	"path/filepath"
	"testing"
	"time"
)

const giB = 1024 * 1024 * 1024

func noopSync(_ *User, _ bool) error { return nil }

func newRefundStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// mkUser creates a user with plenty of points to buy with.
func mkUser(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	id, err := st.CreateUser(NewUser{Username: name, PasswordHash: "x", Points: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// buy purchases pkg for user and returns the order id.
func buy(t *testing.T, st *Store, userID int64, pkg *Package) int64 {
	t.Helper()
	res, err := st.Purchase(userID, pkg, noopSync)
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	return res.Order.ID
}

// setBucketUsed forces a bucket's consumed bytes (simulating traffic use).
func setBucketUsed(t *testing.T, st *Store, userID int64, kind string, up, down int64) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE user_plans SET used_up=?, used_down=? WHERE user_id=? AND kind=?`,
		up, down, userID, kind); err != nil {
		t.Fatal(err)
	}
}

func setBucketExpiry(t *testing.T, st *Store, userID int64, expiry int64) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE user_plans SET expiry_at=? WHERE user_id=? AND kind='plan'`, expiry, userID); err != nil {
		t.Fatal(err)
	}
}

func mkPlan(t *testing.T, st *Store, name string, price, trafficGiB, days int64) *Package {
	t.Helper()
	id, err := st.CreatePackage(Package{
		Type: "plan", Name: name, PricePoints: price,
		TrafficBytes: trafficGiB * giB, DurationDays: days, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.GetPackage(id)
	return p
}

// --- Tests ---

// Half the traffic used, most of the time left → traffic governs min() → 50%.
func TestRefund_PlanHalfTrafficUsed(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "plan", 30*giB, 20*giB) // 50 GiB of 100

	_, q, err := st.RefundOrder(oid, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 50 {
		t.Fatalf("refund points = %d, want 50 (ratio=%.3f traffic=%.3f time=%.3f)", q.RefundPoints, q.Ratio, q.TrafficRatio, q.TimeRatio)
	}
	// Order records the actual refunded amount.
	o, _ := st.GetOrder(oid)
	if o.Status != "refunded" || o.RefundedPoints != 50 {
		t.Fatalf("order refunded_points=%d status=%s", o.RefundedPoints, o.Status)
	}
}

// Nothing used, full time left → 100% refund.
func TestRefund_PlanUnused(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bob")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)

	_, q, err := st.RefundOrder(oid, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 100 {
		t.Fatalf("refund points = %d, want 100", q.RefundPoints)
	}
}

// Fully consumed → 0 refund, but entitlement still reversed / order refunded.
func TestRefund_PlanFullyUsed(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carol")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "plan", 100*giB, 0)

	_, q, err := st.RefundOrder(oid, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 0 {
		t.Fatalf("refund points = %d, want 0", q.RefundPoints)
	}
	o, _ := st.GetOrder(oid)
	if o.Status != "refunded" {
		t.Fatalf("order not marked refunded")
	}
}

// min() takes the smaller dimension: half traffic left but only 10% time left → 10%.
func TestRefund_PlanTimeGovernsMin(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dave")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "plan", 50*giB, 0)               // traffic ratio 0.5
	setBucketExpiry(t, st, uid, time.Now().Unix()+3*86400+120) // ~3/30 days left → 0.1

	_, q, err := st.RefundOrder(oid, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 10 {
		t.Fatalf("refund points = %d, want 10 (traffic=%.3f time=%.3f)", q.RefundPoints, q.TrafficRatio, q.TimeRatio)
	}
}

// Renewal stacking: two purchases, half used. Refunding the latest returns it in
// full; refunding the first then returns the reduced remainder. Composes.
func TestRefund_RenewalStacking(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "erin")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	o1 := buy(t, st, uid, pkg)
	o2 := buy(t, st, uid, pkg)                  // renew → bucket 200G / 60d
	setBucketUsed(t, st, uid, "plan", 50*giB, 0) // 50 of 200 used

	_, q2, err := st.RefundOrder(o2, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q2.RefundPoints != 100 {
		t.Fatalf("refund o2 = %d, want 100 (traffic=%.3f)", q2.RefundPoints, q2.TrafficRatio)
	}
	_, q1, err := st.RefundOrder(o1, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q1.RefundPoints != 50 {
		t.Fatalf("refund o1 = %d, want 50 (traffic=%.3f)", q1.RefundPoints, q1.TrafficRatio)
	}
}

// Traffic pool (no expiry): prorate on traffic only, time dimension N/A.
func TestRefund_TrafficPool(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "frank")
	id, err := st.CreatePackage(Package{Type: "traffic", Name: "100G包", PricePoints: 50, TrafficBytes: 100 * giB, Stock: -1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := st.GetPackage(id)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "pool", 50*giB, 0)

	_, q, err := st.RefundOrder(oid, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 25 {
		t.Fatalf("refund = %d, want 25", q.RefundPoints)
	}
	if q.TimeRatio != -1 {
		t.Fatalf("pool time ratio should be N/A (-1), got %.3f", q.TimeRatio)
	}
}

// mode=full ignores usage and returns the whole price.
func TestRefund_FullMode(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "grace")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "plan", 80*giB, 0)

	_, q, err := st.RefundOrder(oid, 0, "full", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 100 {
		t.Fatalf("full refund = %d, want 100", q.RefundPoints)
	}
}

// Double refund is rejected idempotently.
func TestRefund_DoubleRefund(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "heidi")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)
	if _, _, err := st.RefundOrder(oid, 0, "prorated", noopSync); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RefundOrder(oid, 0, "prorated", noopSync); err != ErrAlreadyRefunded {
		t.Fatalf("second refund err = %v, want ErrAlreadyRefunded", err)
	}
}

// A handling fee reduces the refund proportionally.
func TestRefund_Fee(t *testing.T) {
	st := newRefundStore(t)
	if err := st.SetSetting("refund_fee_percent", "10"); err != nil {
		t.Fatal(err)
	}
	uid := mkUser(t, st, "ivan")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "plan", 50*giB, 0) // 50% remaining

	_, q, err := st.RefundOrder(oid, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	// 50% remaining × (1 - 10% fee) = 45%.
	if q.RefundPoints != 45 {
		t.Fatalf("refund with fee = %d, want 45", q.RefundPoints)
	}
}

// Points balance actually moves by the refunded amount (and only that).
func TestRefund_PointsBalance(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "judy")
	pkg := mkPlan(t, st, "100G/30d", 100, 100, 30)
	before, _ := st.UserByID(uid)
	oid := buy(t, st, uid, pkg)
	setBucketUsed(t, st, uid, "plan", 75*giB, 0) // 25% remaining → refund 25

	if _, _, err := st.RefundOrder(oid, 0, "prorated", noopSync); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByID(uid)
	// paid 100, refunded 25 → net -75 from the starting balance.
	if u.Points != before.Points-75 {
		t.Fatalf("balance = %d, want %d", u.Points, before.Points-75)
	}
}
