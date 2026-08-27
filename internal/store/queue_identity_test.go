package store

import (
	"testing"
	"time"
)

// liveCreds is the identity a user's client would actually be authenticating
// with right now: the credentials of the份 that renders into the config.
func liveCreds(t *testing.T, st *Store, uid, now int64) (name, uuid, secret string) {
	t.Helper()
	bs, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range orderBuckets(bs, now, 0, func(int64) []int64 { return nil }) {
		if o.b.Kind == "plan" && o.b.PackageID > 0 {
			return o.b.ClientName, o.b.ClientUUID, o.b.ClientSecret
		}
	}
	t.Fatal("no live plan identity")
	return
}

// The scenario this exists for: six months sold as six monthly份 assigned to one
// user. Every rollover must be invisible to their client, because the links it
// already imported are the ones it keeps using — a client only refetches the
// subscription every 12h.
func TestQueueIdentity_SurvivesEveryHandoff(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "zoe")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	const months = 6
	for i := 0; i < months; i++ {
		buy(t, st, uid, pkg)
	}

	name0, uuid0, secret0 := liveCreds(t, st, uid, time.Now().Unix())
	if name0 == "" || uuid0 == "" {
		t.Fatal("no credentials on the first month")
	}

	for m := 2; m <= months; m++ {
		expireActive(t, st, uid, time.Now().Unix()-10)
		changed, err := st.AdvanceQueueFor(uid)
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			t.Fatalf("month %d: nothing was activated", m)
		}
		name, uuid, secret := liveCreds(t, st, uid, time.Now().Unix())
		if name != name0 || uuid != uuid0 || secret != secret0 {
			t.Fatalf("month %d: credentials changed (%s/%s → %s/%s) — every client would drop",
				m, name0, uuid0, name, uuid)
		}
	}
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued份 left = %d, want 0", got)
	}
	// The line owns exactly one identity, however many份 have passed through it.
	var lines int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM plan_identities WHERE user_id=?`, uid).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if lines != 1 {
		t.Fatalf("plan_identities rows = %d, want 1 for a single-package user", lines)
	}
}

// The case the old carry-forward could not cover: the份 in service is DELETED
// (a refund, or an admin revoking it) rather than running out. The credentials
// belong to the line, not to that row, so the next份 must still take over with
// the identity the client is holding.
func TestQueueIdentity_SurvivesRemovalOfTheLiveShare(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "amy")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	var orders []int64
	for i := 0; i < 3; i++ {
		orders = append(orders, buy(t, st, uid, pkg))
	}
	_, uuid0, secret0 := liveCreds(t, st, uid, time.Now().Unix())

	// Roll over once, then refund the month that is now in service.
	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RefundOrder(orders[1], 0, "prorated", noopSync); err != nil {
		t.Fatal(err)
	}

	_, uuid, secret := liveCreds(t, st, uid, time.Now().Unix())
	if uuid != uuid0 || secret != secret0 {
		t.Fatalf("credentials changed after the live份 was removed (%s → %s) — the client would drop",
			uuid0, uuid)
	}
}

// An admin revoking the份 in service must not rotate the line's credentials
// either.
func TestQueueIdentity_SurvivesAdminRevoke(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bea")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)
	_, uuid0, _ := liveCreds(t, st, uid, time.Now().Unix())

	var liveID int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='active'`, uid).Scan(&liveID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteBucket(uid, liveID); err != nil {
		t.Fatal(err)
	}

	_, uuid, _ := liveCreds(t, st, uid, time.Now().Unix())
	if uuid != uuid0 {
		t.Fatalf("credentials changed after an admin revoke (%s → %s)", uuid0, uuid)
	}
}

// Metering must still be per-份: traffic on the (unchanged) line identity has to
// bill to the份 actually in service, and a spent份 must keep what it used while
// it was.
func TestQueueIdentity_MeteringFollowsTheLiveShare(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "yuri")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	name, _, _ := liveCreds(t, st, uid, time.Now().Unix())
	var firstID int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='active'`, uid).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBucketUsage(name, 7*giB, 0); err != nil { // first month's usage
		t.Fatal(err)
	}

	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}
	var secondID int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='active'`, uid).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBucketUsage(name, 3*giB, 0); err != nil { // second month, same identity
		t.Fatal(err)
	}

	var firstUsed, secondUsed int64
	if err := st.db.QueryRow(`SELECT used_up FROM user_plans WHERE id=?`, firstID).Scan(&firstUsed); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT used_up FROM user_plans WHERE id=?`, secondID).Scan(&secondUsed); err != nil {
		t.Fatal(err)
	}
	if firstUsed != 7*giB {
		t.Fatalf("retired份 used_up = %d, want the 7G it spent while in service", firstUsed)
	}
	if secondUsed != 3*giB {
		t.Fatalf("live份 used_up = %d, want 3G — traffic must bill to the份 actually in use", secondUsed)
	}
}

// A user's custom mixed-proxy account (their HTTP/SOCKS5 login) belongs to the
// line too, so it survives handoffs like everything else.
func TestQueueIdentity_ProxyAccountBelongsToTheLine(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "xena")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	if _, err := st.db.Exec(`UPDATE plan_identities SET proxy_username='xena_proxy', proxy_password='pw'
		WHERE user_id=? AND package_id=?`, uid, pkg.ID); err != nil {
		t.Fatal(err)
	}
	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}

	bs, _ := st.ListBuckets(uid)
	for _, o := range orderBuckets(bs, time.Now().Unix(), 0, func(int64) []int64 { return nil }) {
		if o.b.Kind == "plan" && o.b.PackageID > 0 {
			if o.b.ProxyName() != "xena_proxy" || o.b.ProxySecret() != "pw" {
				t.Fatalf("proxy account after handoff = %q/%q, want xena_proxy/pw",
					o.b.ProxyName(), o.b.ProxySecret())
			}
			return
		}
	}
	t.Fatal("no live份 after the handoff")
}

// Each package is its own subscription line with its own credentials — they
// cover different node groups, so they must not be conflated.
func TestQueueIdentity_LinesArePerPackage(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "wade")
	a := mkPlan(t, st, "香港", 100, 100, 30)
	b := mkPlan(t, st, "日本", 100, 200, 30)
	buy(t, st, uid, a)
	buy(t, st, uid, b)

	var nameA, nameB string
	if err := st.db.QueryRow(`SELECT client_name FROM plan_identities WHERE user_id=? AND package_id=?`,
		uid, a.ID).Scan(&nameA); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT client_name FROM plan_identities WHERE user_id=? AND package_id=?`,
		uid, b.ID).Scan(&nameB); err != nil {
		t.Fatal(err)
	}
	if nameA == nameB {
		t.Fatalf("both packages share the identity %q", nameA)
	}
}

// Existing accounts must come through the upgrade on the credentials their
// clients are already holding — the migration must take them from the份 in
// service, not from an arbitrary row.
func TestQueueIdentity_BackfillKeepsTheInServiceCredentials(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "vera")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)
	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}
	// Put the account back the way an upgrading panel finds it: no line, and the
	// credentials sitting on the份 that is in service (where the old carry-forward
	// left them). The queued份 keep their own, never-used ones.
	if _, err := st.db.Exec(`DELETE FROM plan_identities WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	const liveUUID = "11111111-2222-3333-4444-555555555555"
	if _, err := st.db.Exec(`UPDATE user_plans SET client_uuid=?
		WHERE user_id=? AND kind='plan' AND status='active'`, liveUUID, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.backfillPlanIdentities(); err != nil {
		t.Fatal(err)
	}

	var got string
	if err := st.db.QueryRow(`SELECT client_uuid FROM plan_identities WHERE user_id=? AND package_id=?`,
		uid, pkg.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != liveUUID {
		t.Fatalf("backfilled uuid = %s, want the in-service one %s — the upgrade would disconnect this user",
			got, liveUUID)
	}
	// Re-running must never rotate a live credential.
	if err := st.backfillPlanIdentities(); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := st.db.QueryRow(`SELECT client_uuid FROM plan_identities WHERE user_id=? AND package_id=?`,
		uid, pkg.ID).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("a second backfill rotated the credential (%s → %s)", got, again)
	}
}

// When two份 of a line are somehow both usable, the migration must lift the
// credentials of the one a node would actually be served from — the soonest
// expiry, then lowest id, exactly as orderBuckets/pickOwner choose. Picking any
// other row disconnects every client of that account at upgrade, which is the
// worst thing a migration can do.
func TestQueueIdentity_BackfillMatchesWhoActuallyServes(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "ulla")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	now := time.Now().Unix()

	// Two usable份: the LOWER id expires sooner, so it is the one in service.
	serving, err := insertBucket(st.db, &Bucket{
		UserID: uid, Kind: "plan", PackageID: pkg.ID, Name: "月付",
		ClientName: "qz_ulla_serving", ClientUUID: "serving-uuid", ClientSecret: "s1",
		TrafficLimit: 100 * giB, ExpiryAt: now + 10*86400, DurationDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE user_plans SET client_uuid='serving-uuid', client_secret='s1' WHERE id=?`, serving); err != nil {
		t.Fatal(err)
	}
	later, err := insertBucket(st.db, &Bucket{
		UserID: uid, Kind: "plan", PackageID: pkg.ID, Name: "月付",
		ClientName: "qz_ulla_later", ClientUUID: "later-uuid", ClientSecret: "s2",
		TrafficLimit: 100 * giB, ExpiryAt: now + 20*86400, DurationDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE user_plans SET client_uuid='later-uuid', client_secret='s2' WHERE id=?`, later); err != nil {
		t.Fatal(err)
	}

	// Whoever pickOwner would serve is the credential that must survive.
	bs, _ := st.ListBuckets(uid)
	ord := orderBuckets(bs, now, 0, func(int64) []int64 { return nil })
	if len(ord) == 0 || ord[0].b.ID != serving {
		t.Fatalf("setup: expected份%d to be the one in service, got %v", serving, ord)
	}

	if err := st.backfillPlanIdentities(); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := st.db.QueryRow(`SELECT client_uuid FROM plan_identities WHERE user_id=? AND package_id=?`,
		uid, pkg.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "serving-uuid" {
		t.Fatalf("backfilled %q, want the credential actually in service — every client of this account would drop", got)
	}
}

// When the whole chain is spent, nothing is in service, so the credentials worth
// keeping are the ones the account last authenticated with — the most recently
// activated份. Buying the package again reuses the line, and getting this wrong
// is the difference between the client resuming and staying broken until its next
// refresh.
func TestQueueIdentity_BackfillSpentChainKeepsTheLastServed(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "nate")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	const past = int64(1000000)
	for i, e := range []int64{past, past + 86400} { // 份2 served last
		id, err := insertBucket(st.db, &Bucket{
			UserID: uid, Kind: "plan", PackageID: pkg.ID, Name: "月付",
			ClientName:   "qz_nate_" + string(rune('a'+i)),
			ClientUUID:   []string{"first-uuid", "last-served-uuid"}[i],
			ClientSecret: "s", TrafficLimit: 100 * giB, ExpiryAt: e, DurationDays: 30,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.Exec(`UPDATE user_plans SET client_uuid=?, client_secret='s' WHERE id=?`,
			[]string{"first-uuid", "last-served-uuid"}[i], id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.backfillPlanIdentities(); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := st.db.QueryRow(`SELECT client_uuid FROM plan_identities WHERE user_id=?`, uid).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "last-served-uuid" {
		t.Fatalf("backfilled %q, want the last-served credential", got)
	}
}
