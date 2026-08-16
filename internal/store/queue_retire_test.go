package store

import (
	"testing"
	"time"
)

// chainBuckets returns a user's real (package-backed) plan buckets, id-ordered.
func chainBuckets(t *testing.T, st *Store, uid int64) []*Bucket {
	t.Helper()
	bs, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	var out []*Bucket
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID > 0 {
			out = append(out, b)
		}
	}
	return out
}

// rollOver ends the current月 and lets the next one take over.
func rollOver(t *testing.T, st *Store, uid int64) {
	t.Helper()
	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}
}

// A panel restart must not touch a queue that has already rolled over. Migrate
// runs mergeDuplicatePlanBuckets on every boot, and a progressed chain has
// several same-package buckets by design — collapsing them destroys the
// per-month accounting and deletes whichever份 is currently in service.
func TestRetire_RestartLeavesTheChainIntact(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "sam")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	const months = 6
	for i := 0; i < months; i++ {
		buy(t, st, uid, pkg)
	}
	rollOver(t, st, uid)
	rollOver(t, st, uid)

	before := chainBuckets(t, st, uid)
	var liveName string
	for _, b := range before {
		if b.Status == "active" {
			liveName = b.ClientName
		}
	}
	if liveName == "" {
		t.Fatal("no live份 before the restart")
	}

	if err := st.Migrate(); err != nil { // every boot runs this
		t.Fatal(err)
	}

	after := chainBuckets(t, st, uid)
	if len(after) != len(before) {
		t.Fatalf("份数 %d → %d across a restart — the chain was flattened", len(before), len(after))
	}
	for i, b := range after {
		if b.TrafficLimit != 100*giB {
			t.Fatalf("份%d limit=%dG after restart, want 100G — months were merged into one bucket",
				b.ID, b.TrafficLimit/giB)
		}
		if b.ID != before[i].ID {
			t.Fatalf("份 ids changed across restart: %v → %v", before[i].ID, b.ID)
		}
	}
	// The line's credentials must be the same ones the client had before the
	// restart — every份 of a line reports them, so compare against the live one.
	for _, b := range after {
		if b.Status == "active" && b.ClientName != liveName {
			t.Fatalf("live identity changed across restart: %s → %s", liveName, b.ClientName)
		}
	}
}

// Refunding the month currently in service must work on an account that has
// already rolled over. The consumed份 must not be mistaken for the credential
// holder, which used to make the promotion collide with its own retired name and
// fail the whole refund.
func TestRetire_RefundLiveMonthAfterRollover(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "rex")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	var orders []int64
	for i := 0; i < 3; i++ {
		orders = append(orders, buy(t, st, uid, pkg))
	}
	rollOver(t, st, uid) // month 2 is now in service, holding month 1's identity

	if _, _, err := st.RefundOrder(orders[1], 0, "prorated", noopSync); err != nil {
		t.Fatalf("退款当前生效的月份失败: %v", err)
	}

	// Month 3 must have taken over, and exactly one份 may be live.
	live := 0
	for _, b := range chainBuckets(t, st, uid) {
		if b.Status == "active" {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("退款后生效中的份 = %d, want 1", live)
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("退款后仍有 %d 份排队, want 0 (month 3 should have taken over)", got)
	}
}

// The already-consumed份 of a chain created before this status existed must be
// marked on upgrade, or they keep exposing the account to both failures above.
func TestRetire_BackfillMarksExistingChains(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "tess")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	for i := 0; i < 4; i++ {
		buy(t, st, uid, pkg)
	}
	rollOver(t, st, uid)
	rollOver(t, st, uid)

	// Put the rows back the way the old code left them: everything 'active'.
	if _, err := st.db.Exec(`UPDATE user_plans SET status='active' WHERE user_id=? AND status=?`,
		uid, StatusRetired); err != nil {
		t.Fatal(err)
	}
	if got := planStatusCount(t, st, uid, StatusRetired); got != 0 {
		t.Fatalf("setup: %d份 still retired", got)
	}

	if err := st.backfillRetiredBuckets(); err != nil {
		t.Fatal(err)
	}
	if got := planStatusCount(t, st, uid, StatusRetired); got != 2 {
		t.Fatalf("retired份 = %d, want 2 (the two consumed months)", got)
	}
	// The newest份 is the one in service and must stay active.
	bs := chainBuckets(t, st, uid)
	if bs[2].Status != "active" {
		t.Fatalf("the in-service份 was marked %q", bs[2].Status)
	}
	// Idempotent.
	if err := st.backfillRetiredBuckets(); err != nil {
		t.Fatal(err)
	}
	if got := planStatusCount(t, st, uid, StatusRetired); got != 2 {
		t.Fatalf("second pass changed the count to %d", got)
	}
}

// The backfill must never shelve a份 the user can still spend. Having a newer
// sibling is how a chain is recognised, but it is not proof this份 is finished,
// and a migration that quietly took away usable entitlement would be worse than
// the bug it fixes.
func TestRetire_BackfillLeavesUsableSharesAlone(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "vic")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	now := time.Now().Unix()
	// Two same-package份 that are BOTH still good — whatever produced this state,
	// the older one still has time and traffic on it.
	for i := 0; i < 2; i++ {
		if _, err := insertBucket(st.db, &Bucket{
			UserID: uid, Kind: "plan", PackageID: pkg.ID, Name: "月付",
			ClientName:   "qz_vic_ok" + string(rune('a'+i)),
			TrafficLimit: 100 * giB, ExpiryAt: now + 30*86400,
			DurationDays: 30,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.backfillRetiredBuckets(); err != nil {
		t.Fatal(err)
	}
	if got := planStatusCount(t, st, uid, StatusRetired); got != 0 {
		t.Fatalf("%d份 were retired despite still being usable — the migration took entitlement away", got)
	}

	// But once the older one is genuinely spent, it is marked.
	if _, err := st.db.Exec(`UPDATE user_plans SET used_down=traffic_limit
		WHERE user_id=? AND client_name='qz_vic_oka'`, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.backfillRetiredBuckets(); err != nil {
		t.Fatal(err)
	}
	if got := planStatusCount(t, st, uid, StatusRetired); got != 1 {
		t.Fatalf("retired份 = %d, want 1 (the exhausted one)", got)
	}
}

// The legacy repair must still do its job: genuinely pre-queue duplicates
// (duration_days=0) are still collapsed.
func TestRetire_LegacyDuplicatesStillMerge(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "una")
	pkg := mkPlan(t, st, "老套餐", 100, 100, 30)
	now := time.Now().Unix()
	for i := 0; i < 2; i++ {
		if _, err := insertBucket(st.db, &Bucket{
			UserID: uid, Kind: "plan", PackageID: pkg.ID, Name: "老套餐",
			ClientName:   "qz_una_legacy" + string(rune('a'+i)),
			TrafficLimit: 100 * giB, ExpiryAt: now + 86400,
			DurationDays: 0, // pre-queue rows never had this set
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.mergeDuplicatePlanBuckets(); err != nil {
		t.Fatal(err)
	}
	bs := chainBuckets(t, st, uid)
	if len(bs) != 1 {
		t.Fatalf("legacy duplicates = %d份, want 1 after the merge", len(bs))
	}
	if bs[0].TrafficLimit != 200*giB {
		t.Fatalf("merged limit = %dG, want 200G", bs[0].TrafficLimit/giB)
	}
}
