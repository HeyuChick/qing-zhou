package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

// proxyScene is a user holding two plans on two different node groups, with one
// mixed (HTTP/SOCKS5) node that can be moved between them — the exact shape that
// used to change the user's proxy password.
type proxyScene struct {
	st       *Store
	uid      int64
	nodeID   int64
	groupHK  int64
	groupJP  int64
	pkgHKID  int64
	pkgJPID  int64
	acctName string
}

func newProxyScene(t *testing.T) *proxyScene {
	t.Helper()
	st := newRefundStore(t)
	uid := mkUser(t, st, "ann")
	if err := st.EnsureProxyAccount(uid); err != nil {
		t.Fatalf("EnsureProxyAccount: %v", err)
	}
	hk, err := st.CreateGroup(NodeGroup{Name: "香港"})
	if err != nil {
		t.Fatal(err)
	}
	jp, err := st.CreateGroup(NodeGroup{Name: "日本"})
	if err != nil {
		t.Fatal(err)
	}
	// Different durations so the two plans also differ in ownership priority.
	pkgHK := mkPlan(t, st, "香港套餐", 10, 100, 30)
	pkgJP := mkPlan(t, st, "日本套餐", 10, 100, 60)
	if err := st.SetPlanGroups(pkgHK.ID, []int64{hk}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlanGroups(pkgJP.ID, []int64{jp}); err != nil {
		t.Fatal(err)
	}
	buy(t, st, uid, pkgHK)
	buy(t, st, uid, pkgJP)

	if _, err := st.SaveSbInbound(&SbInbound{Type: "mixed", Tag: "mixed-proxy", Listen: "::", ListenPort: 7890, Options: "{}", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.CreateNode(Node{Type: "self_built", Name: "代理节点", InboundTag: "mixed-proxy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeGroups(nodeID, []int64{hk}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByID(uid)
	return &proxyScene{st: st, uid: uid, nodeID: nodeID, groupHK: hk, groupJP: jp,
		pkgHKID: pkgHK.ID, pkgJPID: pkgJP.ID, acctName: u.ProxyUsername}
}

func (sc *proxyScene) proxy(t *testing.T) UserProxy {
	t.Helper()
	u, err := sc.st.UserByID(sc.uid)
	if err != nil || u == nil {
		t.Fatalf("UserByID: %v", err)
	}
	list := sc.st.BuildUserProxies(u, "example.com")
	if len(list) != 1 {
		t.Fatalf("want exactly one mixed proxy, got %d", len(list))
	}
	return list[0]
}

// The whole point: moving a node to another group hands it to another套餐, and
// that must no longer change what the user has to paste into 1Panel/Docker/git.
func TestProxyAccount_CredentialSurvivesGroupMove(t *testing.T) {
	sc := newProxyScene(t)
	before := sc.proxy(t)
	if !before.Account || before.Username != sc.acctName {
		t.Fatalf("node should present the account credential, got %+v", before)
	}

	if err := sc.st.SetNodeGroups(sc.nodeID, []int64{sc.groupJP}); err != nil {
		t.Fatal(err)
	}
	after := sc.proxy(t)

	if after.BucketID == before.BucketID {
		t.Fatal("scene is not exercising the bug: the node did not change owner份")
	}
	if after.Username != before.Username || after.Password != before.Password {
		t.Errorf("credential changed with the group move: %q/%q → %q/%q",
			before.Username, before.Password, after.Username, after.Password)
	}
}

// The credential the user sees must be the one sing-box will actually accept —
// on every node, and on both sides of a group move.
func TestProxyAccount_ConfigCarriesAccountAndLegacyCreds(t *testing.T) {
	sc := newProxyScene(t)
	bkt := sc.proxy(t)

	now := time.Now().Unix()
	usersByTag, err := sc.st.BuildUsersByTag(now)
	if err != nil {
		t.Fatal(err)
	}
	users := usersByTag["mixed-proxy"]
	names := map[string]string{}
	for _, u := range users {
		names[u.Name] = u.Password
	}
	if names[sc.acctName] == "" {
		t.Fatalf("mixed inbound must accept the account credential %q, got %v", sc.acctName, names)
	}
	// The owning bucket's own credential stays valid: someone may already have it
	// pasted somewhere, and the upgrade must not be what breaks their proxy.
	var owner *Bucket
	all, _ := sc.st.ListBuckets(sc.uid)
	for _, b := range all {
		if b.ID == bkt.BucketID {
			owner = b
		}
	}
	if owner == nil {
		t.Fatalf("owner bucket %d not found", bkt.BucketID)
	}
	if _, ok := names[owner.ProxyName()]; !ok {
		t.Errorf("legacy per-bucket credential %q was dropped from the inbound: %v", owner.ProxyName(), names)
	}

	cfg, err := sc.st.BuildSingboxConfig(singbox.DefaultBaseConfig, "127.0.0.1:18080", usersByTag)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"username": "`+sc.acctName+`"`) {
		t.Errorf("account credential missing from the generated config:\n%s", cfg)
	}
}

// One name, one number from sing-box: the bytes have to land on one份. It is the
// same份 ownership itself prefers — the soonest-expiring usable one.
func TestProxyAccount_MeteringLandsOnPriorityBucket(t *testing.T) {
	sc := newProxyScene(t)

	if err := sc.st.AddBucketUsage(sc.acctName, 1000, 2000); err != nil {
		t.Fatal(err)
	}
	buckets, _ := sc.st.ListBuckets(sc.uid)
	for _, b := range buckets {
		want := int64(0)
		if b.PackageID == sc.pkgHKID { // 30 days — expires first
			want = 1000
		}
		if b.UsedUp != want {
			t.Errorf("bucket pkg=%d used_up=%d, want %d", b.PackageID, b.UsedUp, want)
		}
	}
	// The user aggregate is mirrored exactly once, not once per candidate bucket.
	u, _ := sc.st.UserByID(sc.uid)
	if u.UsedUp != 1000 || u.UsedDown != 2000 {
		t.Errorf("user aggregate = %d/%d, want 1000/2000", u.UsedUp, u.UsedDown)
	}
}

// The poll path resolves account identities in a pre-pass outside the write
// transaction, so it is a second implementation of "which份 pays" and has to
// agree with the single-shot path — including on names that are not identities.
func TestProxyAccount_BatchMeteringMatchesSingleShot(t *testing.T) {
	sc := newProxyScene(t)
	applied, err := sc.st.AddUsageBatch(map[string]UsageDelta{
		sc.acctName: {Up: 7, Down: 9},
		"ghost":     {Up: 999, Down: 999},
	})
	if err != nil {
		t.Fatalf("AddUsageBatch: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (the account identity; ghost is unknown)", applied)
	}
	buckets, _ := sc.st.ListBuckets(sc.uid)
	total := int64(0)
	for _, b := range buckets {
		total += b.UsedUp
		if b.PackageID == sc.pkgHKID && (b.UsedUp != 7 || b.UsedDown != 9) {
			t.Errorf("charged份 got %d/%d, want 7/9", b.UsedUp, b.UsedDown)
		}
	}
	if total != 7 {
		t.Errorf("total metered up = %d, want 7 — the delta landed on more than one份", total)
	}
}

// Which份 pays for account-level traffic, spelled out. It is asked of
// userBucketOrder — the same answer ownership gives — so the rule below is the
// ownership rule minus the free bucket, and stays that way by construction.
func TestProxyAccount_ChargeTargetFollowsOwnership(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bo")
	if err := st.EnsureProxyAccount(uid); err != nil {
		t.Fatal(err)
	}
	group, err := st.CreateGroup(NodeGroup{Name: "线路"})
	if err != nil {
		t.Fatal(err)
	}
	plan := func(name string, days int64) *Package {
		pkg := mkPlan(t, st, name, 10, 100, days)
		if err := st.SetPlanGroups(pkg.ID, []int64{group}); err != nil {
			t.Fatal(err)
		}
		buy(t, st, uid, pkg)
		return pkg
	}
	// charged reports which份 the account credential spends, as "kind:package".
	charged := func() string {
		b, err := st.accountMeterBucket(uid)
		if errors.Is(err, sql.ErrNoRows) {
			return "none"
		}
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s:%d", b.Kind, b.PackageID)
	}
	expire := func(pkgID int64) {
		if _, err := st.db.Exec(`UPDATE user_plans SET expiry_at=? WHERE user_id=? AND package_id=?`,
			time.Now().Unix()-1, uid, pkgID); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.EnsureFreeBucket(uid, "bo"); err != nil {
		t.Fatal(err)
	}
	// The free bucket is never a target: it carries its own credential precisely
	// so its bytes stay off a paid counter.
	if got := charged(); got != "none" {
		t.Errorf("free bucket only: charged %s, want none", got)
	}
	// A pool with no plan alongside it grants access to nothing, so no traffic can
	// arrive under this credential in the first place.
	if err := st.EnsurePoolBucket(uid, "qz_bo", "uuid-bo", "secret-bo"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE user_plans SET traffic_limit=? WHERE user_id=? AND kind='pool'`,
		100*giB, uid); err != nil {
		t.Fatal(err)
	}
	if got := charged(); got != "none" {
		t.Errorf("funded pool, no plans: charged %s, want none", got)
	}

	long := plan("长期", 60)
	if got, want := charged(), fmt.Sprintf("plan:%d", long.ID); got != want {
		t.Errorf("single plan: charged %s, want %s", got, want)
	}
	// Soonest expiry first — the份 the user is actually spending down.
	short := plan("短期", 30)
	if got, want := charged(), fmt.Sprintf("plan:%d", short.ID); got != want {
		t.Errorf("two plans: charged %s, want the soonest-expiring %s", got, want)
	}
	// An exhausted份 is skipped, not charged into the negative.
	if _, err := st.db.Exec(`UPDATE user_plans SET used_up=traffic_limit WHERE user_id=? AND package_id=?`,
		uid, short.ID); err != nil {
		t.Fatal(err)
	}
	if got, want := charged(), fmt.Sprintf("plan:%d", long.ID); got != want {
		t.Errorf("exhausted份: charged %s, want %s", got, want)
	}
	// A never-expiring grant sorts after dated plans, and steps in when they go.
	if err := st.EnsureWelcomeBucket(uid, "bo", 5*giB, 0); err != nil {
		t.Fatal(err)
	}
	if got, want := charged(), fmt.Sprintf("plan:%d", long.ID); got != want {
		t.Errorf("grant present: charged %s, want the dated plan %s", got, want)
	}
	expire(long.ID)
	if got, want := charged(), fmt.Sprintf("plan:%d", WelcomePackageID); got != want {
		t.Errorf("only the grant left: charged %s, want %s", got, want)
	}
	// The pool is the last resort, once the user has plans for it to back.
	expire(WelcomePackageID)
	if got := charged(); got != "pool:0" {
		t.Errorf("everything else spent: charged %s, want pool:0", got)
	}
}

// Free-group traffic is metered apart from paid quota on purpose. The account
// credential is charged to a paid份, so it must not reach a free-owned node —
// that would put unmetered bytes back on the paid counter.
func TestProxyAccount_FreeNodeKeepsItsOwnIdentity(t *testing.T) {
	sc := newProxyScene(t)
	free, err := sc.st.CreateGroup(NodeGroup{Name: "免费"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.st.SetSetting("free_group_id", fmt.Sprint(free)); err != nil {
		t.Fatal(err)
	}
	if err := sc.st.EnsureFreeBucket(sc.uid, "ann"); err != nil {
		t.Fatal(err)
	}
	if err := sc.st.SetNodeGroups(sc.nodeID, []int64{free}); err != nil {
		t.Fatal(err)
	}

	p := sc.proxy(t)
	if p.Account || p.Username == sc.acctName {
		t.Fatalf("free-owned node must keep the free bucket's identity, got %+v", p)
	}
	now := time.Now().Unix()
	usersByTag, err := sc.st.BuildUsersByTag(now)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range usersByTag["mixed-proxy"] {
		if u.Name == sc.acctName {
			t.Fatal("account credential must not authenticate on a free-owned inbound")
		}
	}
	// And its traffic still lands on the free bucket, off the paid counter.
	if err := sc.st.AddBucketUsage(p.Username, 3*giB, 0); err != nil {
		t.Fatal(err)
	}
	if b := bucketOfKind(t, sc.st, sc.uid, KindFree); b.UsedUp != 3*giB {
		t.Errorf("free bucket used_up = %d, want %d", b.UsedUp, 3*giB)
	}
	if u, _ := sc.st.UserByID(sc.uid); u.UsedUp != 0 {
		t.Errorf("free traffic leaked onto the paid aggregate: used_up = %d", u.UsedUp)
	}
}

// All three identity kinds share one namespace — a duplicate would meter another
// user's traffic — so a name taken anywhere is refused everywhere.
func TestProxyAccount_UsernameUniqueAcrossIdentityKinds(t *testing.T) {
	st := newRefundStore(t)
	alice := mkUser(t, st, "alice")
	bob := mkUser(t, st, "bob")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, alice, pkg)
	bkt := planBucket(t, st, alice)

	if err := st.SetUserProxyCred(alice, "shared-name", "password123", 0); err != nil {
		t.Fatalf("SetUserProxyCred: %v", err)
	}
	if err := st.SetUserProxyCred(bob, "shared-name", "password123", 0); err == nil {
		t.Error("another user's account credential must not take the same name")
	}
	if err := st.SetBucketProxyCred(bkt.ID, alice, "shared-name", "password123", 0); err == nil {
		t.Error("a bucket credential must not take a name held by an account credential")
	}
	// Re-saving one's own name is not a collision with oneself.
	if err := st.SetUserProxyCred(alice, "shared-name", "newpassword", 0); err != nil {
		t.Errorf("re-saving the same username must be allowed: %v", err)
	}
	// And the reverse direction: a bucket name is out of reach for an account.
	if err := st.SetBucketProxyCred(bkt.ID, alice, "bucket-name", "password123", 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserProxyCred(bob, "bucket-name", "password123", 0); err == nil {
		t.Error("an account credential must not take a name held by a bucket")
	}
	// The system prefix is still reserved, whichever kind asks for it.
	if err := st.SetUserProxyCred(bob, "qz_hack", "password123", 0); err == nil {
		t.Error("qz_-prefixed account username must be rejected")
	}
}

// An expired account credential stops authenticating; the user is not locked out
// of their own nodes, because the bucket credential is still there.
func TestProxyAccount_ExpiryDropsAccountCredOnly(t *testing.T) {
	sc := newProxyScene(t)
	now := time.Now().Unix()
	if err := sc.st.SetUserProxyCred(sc.uid, "ann-proxy", "password123", now-1); err != nil {
		t.Fatal(err)
	}
	usersByTag, err := sc.st.BuildUsersByTag(now)
	if err != nil {
		t.Fatal(err)
	}
	users := usersByTag["mixed-proxy"]
	if len(users) != 1 {
		t.Fatalf("want only the bucket credential left, got %d users", len(users))
	}
	if users[0].Name == "ann-proxy" {
		t.Error("expired account credential must not authenticate")
	}
	if p := sc.proxy(t); p.Account {
		t.Errorf("page must fall back to the bucket credential, got %+v", p)
	}
}

// EnsureProxyAccount is a backfill as much as a provisioning step, so a second
// call must leave a credential the user may already be using alone.
func TestProxyAccount_EnsureIsIdempotent(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "cy")
	if err := st.EnsureProxyAccount(uid); err != nil {
		t.Fatal(err)
	}
	first, _ := st.UserByID(uid)
	if !strings.HasPrefix(first.ProxyUsername, proxyAccountPrefix) || first.ProxyPassword == "" {
		t.Fatalf("mint produced %q/%q", first.ProxyUsername, first.ProxyPassword)
	}
	if err := ValidateProxyUsername(first.ProxyUsername); err != nil {
		t.Errorf("minted name must satisfy the same rules a user's does: %v", err)
	}
	if err := st.EnsureProxyAccount(uid); err != nil {
		t.Fatal(err)
	}
	again, _ := st.UserByID(uid)
	if again.ProxyUsername != first.ProxyUsername || again.ProxyPassword != first.ProxyPassword {
		t.Error("second EnsureProxyAccount rotated a live credential")
	}
}

// The份 credential authenticates the node just as the account one does (both are
// emitted into the inbound), so the panel has to keep handing it out. Hiding it
// left users with a working login they could not read anywhere — including the
// one they had already pasted into 1Panel/Docker before the upgrade.
func TestProxyAccount_PlanCredentialStaysVisible(t *testing.T) {
	sc := newProxyScene(t)
	p := sc.proxy(t)
	if !p.Account {
		t.Fatalf("scene should be presenting the account credential, got %+v", p)
	}
	if p.Plan == nil {
		t.Fatal("the owning份's own credential was dropped from the panel payload")
	}

	var owner *Bucket
	all, _ := sc.st.ListBuckets(sc.uid)
	for _, b := range all {
		if b.ID == p.BucketID {
			owner = b
		}
	}
	if owner == nil {
		t.Fatalf("owner bucket %d not found", p.BucketID)
	}
	if p.Plan.Username != owner.ProxyName() || p.Plan.Password != owner.ProxySecret() {
		t.Errorf("plan credential = %q/%q, want the owning bucket's %q/%q",
			p.Plan.Username, p.Plan.Password, owner.ProxyName(), owner.ProxySecret())
	}
	if p.Plan.BucketID != owner.ID {
		t.Errorf("plan credential points at bucket %d, want %d — the edit would hit the wrong份",
			p.Plan.BucketID, owner.ID)
	}
	if p.Plan.Name == "" {
		t.Error("plan credential has no份 name; the page cannot say which套餐 it belongs to")
	}
	// It must be the份's own credential, not a second copy of the account one.
	if p.Plan.Username == p.Username {
		t.Errorf("plan credential is the account credential %q, not the份's own", p.Username)
	}

	// The credential shown must be one sing-box actually accepts on this inbound.
	usersByTag, err := sc.st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range usersByTag[p.Tag] {
		if u.Name == p.Plan.Username && u.Password == p.Plan.Password {
			found = true
		}
	}
	if !found {
		t.Errorf("panel shows plan credential %q but the inbound does not accept it: %+v",
			p.Plan.Username, usersByTag[p.Tag])
	}
}

// A free-owned node has no account credential (free bytes stay off a paid
// counter), so its份 credential is both what the page recommends and what the
// 套餐账号 block renders — it must still be there, or the block goes blank.
func TestProxyAccount_PlanCredentialPresentWithoutAccountCred(t *testing.T) {
	sc := newProxyScene(t)
	if err := sc.st.SetUserProxyCred(sc.uid, "ann_px", "s3cret!", time.Now().Unix()-1); err != nil {
		t.Fatal(err)
	}
	p := sc.proxy(t)
	if p.Account {
		t.Fatal("an expired account credential must not be presented as usable")
	}
	if p.Plan == nil || p.Plan.Username != p.Username || p.Plan.Password != p.Password {
		t.Errorf("fallback node must still report its份 credential: %+v", p.Plan)
	}
}
