package store

import (
	"errors"
	"testing"
)

// mkMultiPlan creates a plan sold at several lengths. The first option is the
// default one; the store mirrors it onto the package's own columns.
func mkMultiPlan(t *testing.T, st *Store, name string, opts ...PlanOption) *Package {
	t.Helper()
	id, err := st.CreatePackage(Package{
		Type: "plan", Name: name, Stock: -1, Enabled: true, Options: opts,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.GetPackage(id)
	return p
}

// boughtBucket returns the bucket a package purchase/grant minted (package_id>0),
// as opposed to the shared pool or an admin manual grant.
func boughtBucket(t *testing.T, st *Store, uid int64) *Bucket {
	t.Helper()
	bs, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID > 0 {
			return b
		}
	}
	t.Fatal("no plan bucket")
	return nil
}

func points(t *testing.T, st *Store, uid int64) int64 {
	t.Helper()
	u, err := st.UserByID(uid)
	if err != nil || u == nil {
		t.Fatalf("load user: %v", err)
	}
	return u.Points
}

// The package's own columns must equal its first option — they are what the shop
// card, the admin list and a default grant read, so a stale combination there
// would sell a length at another length's price.
func TestPlanDurations_DefaultMirrorsFirstOption(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkMultiPlan(t, st, "M",
		PlanOption{Days: 30, PricePoints: 100, TrafficBytes: 100 * giB},
		PlanOption{Days: 90, PricePoints: 270, TrafficBytes: 300 * giB},
	)
	if pkg.DurationDays != 30 || pkg.PricePoints != 100 || pkg.TrafficBytes != 100*giB {
		t.Fatalf("package columns = %d天/%d分/%dGiB, want the first option (30/100/100)",
			pkg.DurationDays, pkg.PricePoints, pkg.TrafficBytes/giB)
	}

	// Reordering the options moves the default with them.
	pkg.Options = []PlanOption{{Days: 90, PricePoints: 270, TrafficBytes: 300 * giB}, {Days: 30, PricePoints: 100, TrafficBytes: 100 * giB}}
	if err := st.UpdatePackage(*pkg); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetPackage(pkg.ID)
	if got.DurationDays != 90 || got.PricePoints != 270 {
		t.Errorf("after reorder: %d天/%d分, want 90/270", got.DurationDays, got.PricePoints)
	}
}

// Buying a non-default length must charge THAT length's price and grant its
// quota — the whole point of the feature, and the thing an out-of-date price
// path would get wrong.
func TestPlanDurations_BuyNonDefaultCharges(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	before := points(t, st, uid)
	pkg := mkMultiPlan(t, st, "M",
		PlanOption{Days: 30, PricePoints: 100, TrafficBytes: 100 * giB},
		PlanOption{Days: 365, PricePoints: 900, TrafficBytes: 1200 * giB},
	)

	if _, err := st.PurchaseDuration(uid, pkg, 365, "", noopSync); err != nil {
		t.Fatal(err)
	}
	if spent := before - points(t, st, uid); spent != 900 {
		t.Errorf("charged %d points, want 900 (the 365-day option)", spent)
	}
	b := boughtBucket(t, st, uid)
	if b.DurationDays != 365 || b.TrafficLimit != 1200*giB {
		t.Errorf("bucket = %d天/%dGiB, want 365/1200", b.DurationDays, b.TrafficLimit/giB)
	}
}

// days == 0 keeps the old single-argument behaviour: buy the default.
func TestPlanDurations_ZeroBuysDefault(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bob")
	before := points(t, st, uid)
	pkg := mkMultiPlan(t, st, "M",
		PlanOption{Days: 30, PricePoints: 100, TrafficBytes: 100 * giB},
		PlanOption{Days: 90, PricePoints: 270, TrafficBytes: 300 * giB},
	)

	if _, err := st.Purchase(uid, pkg, "", noopSync); err != nil {
		t.Fatal(err)
	}
	if spent := before - points(t, st, uid); spent != 100 {
		t.Errorf("charged %d points, want 100 (default option)", spent)
	}
	if b := boughtBucket(t, st, uid); b.DurationDays != 30 {
		t.Errorf("bucket duration = %d days, want 30", b.DurationDays)
	}
}

// A length that isn't on sale must be refused outright — no order, no charge —
// rather than falling back to some other option's price.
func TestPlanDurations_UnknownLengthRejected(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carol")
	before := points(t, st, uid)
	pkg := mkMultiPlan(t, st, "M",
		PlanOption{Days: 30, PricePoints: 100, TrafficBytes: 100 * giB},
		PlanOption{Days: 90, PricePoints: 270, TrafficBytes: 300 * giB},
	)

	if _, err := st.PurchaseDuration(uid, pkg, 31, "", noopSync); !errors.Is(err, ErrOptionNotFound) {
		t.Fatalf("purchase of an unlisted length: err = %v, want ErrOptionNotFound", err)
	}
	if got := points(t, st, uid); got != before {
		t.Errorf("points changed to %d after a rejected purchase, want %d", got, before)
	}
	orders, _ := st.ListOrders(uid, 10)
	if len(orders) != 0 {
		t.Errorf("%d orders written for a rejected purchase, want 0", len(orders))
	}
}

// A single-duration package still accepts its own length (and only that) — the
// shop posts 0, but an older client posting the visible number must not break.
func TestPlanDurations_SingleDurationPackage(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dave")
	pkg := mkPlan(t, st, "S", 100, 50, 30)

	if _, err := st.PurchaseDuration(uid, pkg, 30, "", noopSync); err != nil {
		t.Fatalf("buying the package's own length: %v", err)
	}
	if _, err := st.PurchaseDuration(uid, pkg, 60, "", noopSync); !errors.Is(err, ErrOptionNotFound) {
		t.Errorf("buying a length it doesn't sell: err = %v, want ErrOptionNotFound", err)
	}
}

// The refund prorates against the length actually bought (carried in the order
// snapshot), not the package's default one.
func TestPlanDurations_RefundUsesPurchasedLength(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "erin")
	pkg := mkMultiPlan(t, st, "M",
		PlanOption{Days: 30, PricePoints: 100, TrafficBytes: 100 * giB},
		PlanOption{Days: 365, PricePoints: 900, TrafficBytes: 1200 * giB},
	)

	res, err := st.PurchaseDuration(uid, pkg, 365, "", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing used yet → a full-value refund of what was actually paid.
	_, q, err := st.RefundOrder(res.Order.ID, 0, "prorated", noopSync)
	if err != nil {
		t.Fatal(err)
	}
	if q.RefundPoints != 900 {
		t.Errorf("refund = %d points, want 900 (the 365-day price that was charged)", q.RefundPoints)
	}
}

// An admin comp can hand out the short trial length instead of the headline one.
func TestPlanDurations_AssignPicksLength(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "frank")
	pkg := mkMultiPlan(t, st, "M",
		PlanOption{Days: 30, PricePoints: 100, TrafficBytes: 100 * giB},
		PlanOption{Days: 7, PricePoints: 30, TrafficBytes: 25 * giB},
	)

	if _, err := st.AssignPackageDuration(uid, pkg, 7, 0, noopSync); err != nil {
		t.Fatal(err)
	}
	b := boughtBucket(t, st, uid)
	if b.DurationDays != 7 || b.TrafficLimit != 25*giB {
		t.Errorf("granted %d天/%dGiB, want 7/25", b.DurationDays, b.TrafficLimit/giB)
	}
	if _, err := st.AssignPackageDuration(uid, pkg, 14, 0, noopSync); !errors.Is(err, ErrOptionNotFound) {
		t.Errorf("granting an unlisted length: err = %v, want ErrOptionNotFound", err)
	}
}
