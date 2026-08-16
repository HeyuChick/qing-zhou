package store

import (
	"strings"
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
// user. Each rollover must be invisible to their client — same uuid, same
// password, same everything — because the links the client already imported name
// the份 that just retired, and a client only refetches the subscription every 12h.
func TestQueueIdentity_SurvivesEveryHandoff(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "zoe")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	const months = 6
	for i := 0; i < months; i++ {
		buy(t, st, uid, pkg)
	}

	now := time.Now().Unix()
	name0, uuid0, secret0 := liveCreds(t, st, uid, now)
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
		now = time.Now().Unix()
		name, uuid, secret := liveCreds(t, st, uid, now)
		if name != name0 || uuid != uuid0 || secret != secret0 {
			t.Fatalf("month %d: credentials changed (%s/%s → %s/%s) — every client would drop",
				m, name0, uuid0, name, uuid)
		}
	}

	// All six份 were consumed, one at a time.
	if got := planStatusCount(t, st, uid, "queued"); got != 0 {
		t.Fatalf("queued份 left = %d, want 0", got)
	}
	// Exactly one份 holds the live name; the retired ones were renamed out of the
	// way so the UNIQUE index on client_name still holds.
	bs, _ := st.ListBuckets(uid)
	live, retired := 0, 0
	for _, b := range bs {
		switch {
		case b.ClientName == name0:
			live++
		case strings.HasPrefix(b.ClientName, "zz_retired_"):
			retired++
		}
	}
	if live != 1 {
		t.Fatalf("%d份 hold the live name, want exactly 1", live)
	}
	if retired != months-1 {
		t.Fatalf("retired份 = %d, want %d", retired, months-1)
	}
}

// Metering must still be per-份: after a handoff, traffic on the (unchanged)
// identity has to bill to the份 that is actually live, and the retired份 must
// keep the usage it accumulated while it was in service.
func TestQueueIdentity_MeteringFollowsTheLiveShare(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "yuri")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	now := time.Now().Unix()
	name, _, _ := liveCreds(t, st, uid, now)
	if err := st.AddBucketUsage(name, 7*giB, 0); err != nil { // first month's usage
		t.Fatal(err)
	}
	var firstID int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans WHERE client_name=?`, name).Scan(&firstID); err != nil {
		t.Fatal(err)
	}

	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBucketUsage(name, 3*giB, 0); err != nil { // second month, same identity
		t.Fatal(err)
	}

	var firstUsed, secondUsed int64
	if err := st.db.QueryRow(`SELECT used_up FROM user_plans WHERE id=?`, firstID).Scan(&firstUsed); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT used_up FROM user_plans WHERE client_name=?`, name).Scan(&secondUsed); err != nil {
		t.Fatal(err)
	}
	if firstUsed != 7*giB {
		t.Fatalf("retired份 used_up = %d, want the 7G it spent while in service", firstUsed)
	}
	if secondUsed != 3*giB {
		t.Fatalf("live份 used_up = %d, want 3G — traffic must bill to the份 actually in use", secondUsed)
	}
}

// A user's custom mixed-proxy account (their HTTP/SOCKS5 login) must survive a
// handoff too — it is a credential they typed into a client by hand.
func TestQueueIdentity_CarriesTheProxyAccount(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "xena")
	pkg := mkPlan(t, st, "月付", 100, 100, 30)
	buy(t, st, uid, pkg)
	buy(t, st, uid, pkg)

	name, _, _ := liveCreds(t, st, uid, time.Now().Unix())
	if _, err := st.db.Exec(`UPDATE user_plans SET proxy_username='xena_proxy', proxy_password='pw'
		WHERE client_name=?`, name); err != nil {
		t.Fatal(err)
	}

	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}

	var user, pass string
	if err := st.db.QueryRow(`SELECT proxy_username, proxy_password FROM user_plans WHERE client_name=?`,
		name).Scan(&user, &pass); err != nil {
		t.Fatal(err)
	}
	if user != "xena_proxy" || pass != "pw" {
		t.Fatalf("proxy account after handoff = %q/%q, want xena_proxy/pw", user, pass)
	}
	// And only one row may hold it — proxy_username has a unique partial index.
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_plans WHERE proxy_username='xena_proxy'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d rows hold the proxy account, want 1", n)
	}
}

// Different packages have independent identity chains — advancing one must not
// hand another package's credentials around.
func TestQueueIdentity_ChainsArePerPackage(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "wade")
	a := mkPlan(t, st, "香港", 100, 100, 30)
	b := mkPlan(t, st, "日本", 100, 200, 30)
	buy(t, st, uid, a)
	buy(t, st, uid, a)
	buy(t, st, uid, b)

	before := map[int64]string{}
	bs, _ := st.ListBuckets(uid)
	for _, x := range bs {
		if x.Kind == "plan" && x.PackageID == b.ID {
			before[x.PackageID] = x.ClientName
		}
	}

	expireActive(t, st, uid, time.Now().Unix()-10)
	if _, err := st.AdvanceQueueFor(uid); err != nil {
		t.Fatal(err)
	}

	bs, _ = st.ListBuckets(uid)
	for _, x := range bs {
		if x.Kind == "plan" && x.PackageID == b.ID && x.ClientName != before[b.ID] {
			t.Fatalf("package B's identity changed (%s → %s) while package A advanced",
				before[b.ID], x.ClientName)
		}
	}
}
