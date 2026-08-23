package store

import (
	"testing"
	"time"
)

// planLineUUID is the credential a plan份 actually authenticates with.
func planLineUUID(t *testing.T, st *Store, uid int64) string {
	t.Helper()
	bs, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID > 0 {
			return b.ClientUUID
		}
	}
	t.Fatal("no plan份")
	return ""
}

// Rotating node credentials is the answer to a leaked subscription: it exists to
// invalidate every link already handed out. Once a plan份's credentials moved to
// its subscription line, rotating only the buckets would leave the leaked links
// working while reporting success and restarting sing-box on every node.
func TestRotate_RevokesTheSubscriptionLine(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carl")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	before := planLineUUID(t, st, uid)
	if err := st.RotateNodeCredentials(uid, time.Now().Unix()+1); err != nil {
		t.Fatal(err)
	}
	after := planLineUUID(t, st, uid)

	if before == after {
		t.Fatal("credentials unchanged after a rotation — every leaked link still works")
	}
	// And the whole line moves together, so the queued份 behind it authenticate
	// with the new credential too.
	bs, _ := st.ListBuckets(uid)
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID > 0 && b.ClientUUID != after {
			t.Fatalf("份%d still reports %s after rotation, want %s", b.ID, b.ClientUUID, after)
		}
	}
}

// A rotation must not silently change the user's proxy login: an empty
// proxy_password falls back to client_secret, so it has to be pinned before that
// moves — on the line as well as on the row.
func TestRotate_PinsTheLineProxyPassword(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dina")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)

	if _, err := st.db.Exec(`UPDATE plan_identities SET proxy_username='dina_proxy', proxy_password=''
		WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	bs, _ := st.ListBuckets(uid)
	var pinned string
	for _, b := range bs {
		if b.Kind == "plan" && b.PackageID > 0 {
			pinned = b.ProxySecret() // the effective password before rotation
		}
	}
	if pinned == "" {
		t.Fatal("no effective proxy password before rotation")
	}

	if err := st.RotateNodeCredentials(uid, time.Now().Unix()+1); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := st.db.QueryRow(`SELECT proxy_password FROM plan_identities WHERE user_id=?`, uid).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != pinned {
		t.Fatalf("line proxy password = %q after rotation, want the pinned %q — the user's proxy login changed under them",
			after, pinned)
	}
}

// Deleting a user must take their subscription lines with them. Usernames are
// freed on deletion and a line's name is derived from one, so a leftover row
// makes the next account with that name unable to buy the same package at all —
// the insert collides with a line belonging to someone who no longer exists.
func TestIdentity_UserDeletionClearsTheLine(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPlan(t, st, "月付", 100, 100, 30)

	uid1 := mkUser(t, st, "alice")
	buy(t, st, uid1, pkg)
	if err := st.DeleteUser(uid1); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM plan_identities WHERE user_id=?`, uid1).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d subscription line(s) survived the user", left)
	}

	// The freed username must be usable again, all the way through a purchase.
	uid2 := mkUser(t, st, "alice")
	if _, err := st.Purchase(uid2, pkg, "", noopSync); err != nil {
		t.Fatalf("a new account reusing the freed username cannot buy: %v", err)
	}
}

// Saving a proxy account must never report success on a write that matched
// nothing — the user would be told it was saved and then fail to connect.
func TestProxyCred_LineMissingIsReported(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "elle")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)

	var bid int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind='plan' AND package_id>0`,
		uid).Scan(&bid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM plan_identities WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBucketProxyCred(bid, uid, "elle_proxy", "s3cretpassword", 0); err == nil {
		t.Fatal("saving a proxy account onto a missing line reported success")
	}
}

// Two live份 of one line share an identity, so the config must not list the same
// user twice in one inbound.
func TestBuildUsers_NoDuplicateUserInAnInbound(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "finn")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)
	// Force the state the queue normally prevents: both份 live at once.
	if _, err := st.db.Exec(`UPDATE user_plans SET status='active', expiry_at=?
		WHERE user_id=? AND kind='plan' AND package_id>0`, time.Now().Unix()+86400, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "in-1", Listen: "::", ListenPort: 8443, Options: "{}", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	bindPlanToInbound(t, st, pkg.ID, "in-1")

	byTag, err := st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, u := range byTag["in-1"] {
		seen[u.Name]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Fatalf("user %q appears %d times in one inbound", name, n)
		}
	}
}

// BucketByClientName must resolve a line's name to the份 in service, not to
// whichever row the query happened to return first.
func TestBucketByClientName_PicksTheInServiceShare(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "gus")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)
	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}

	var liveID int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans
		WHERE user_id=? AND kind='plan' AND status='active'`, uid).Scan(&liveID); err != nil {
		t.Fatal(err)
	}
	name := planLineName(t, st, uid)
	b, err := st.BucketByClientName(name)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil || b.ID != liveID {
		t.Fatalf("resolved to份 %v, want the one in service (%d)", b, liveID)
	}
}

func planLineName(t *testing.T, st *Store, uid int64) string {
	t.Helper()
	var n string
	if err := st.db.QueryRow(`SELECT client_name FROM plan_identities WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
