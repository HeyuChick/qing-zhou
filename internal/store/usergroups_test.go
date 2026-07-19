package store

import (
	"errors"
	"testing"
)

// mkPkg creates an enabled, unlimited-stock traffic package.
func mkPkg(t *testing.T, st *Store, name string) *Package {
	t.Helper()
	id, err := st.CreatePackage(Package{
		Type: "traffic", Name: name, PricePoints: 10, TrafficBytes: giB, Stock: -1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.GetPackage(id)
	if err != nil || p == nil {
		t.Fatalf("get package: %v", err)
	}
	return p
}

func mkGroup(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	id, err := st.CreateUserGroup(UserGroup{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A package with no group bindings is public: everyone may buy it. This is the
// upgrade-compatibility guarantee — every pre-existing package must stay
// buyable after this feature ships.
func TestUnboundPackageIsPublic(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "公开流量包")
	user := mkUser(t, st, "nobody")

	allowed, err := st.CanBuyPackage(user, pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("unbound package must be buyable by a user with no groups")
	}
	buy(t, st, user, pkg) // must not error

	pkgs, err := st.ListPackagesForUser(user)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("shop should show the public package, got %d items", len(pkgs))
	}
}

// A bound package is buyable only by members of a bound group.
func TestBoundPackageRequiresMembership(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "内测专属包")
	vip := mkGroup(t, st, "内测组")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{vip}); err != nil {
		t.Fatal(err)
	}

	outsider := mkUser(t, st, "outsider")
	member := mkUser(t, st, "member")
	if err := st.SetUserGroups(member, []int64{vip}); err != nil {
		t.Fatal(err)
	}

	if allowed, _ := st.CanBuyPackage(outsider, pkg.ID); allowed {
		t.Fatal("non-member must not be allowed to buy a restricted package")
	}
	if allowed, _ := st.CanBuyPackage(member, pkg.ID); !allowed {
		t.Fatal("member must be allowed to buy")
	}

	// The listing hides it from the outsider and shows it to the member.
	if pkgs, _ := st.ListPackagesForUser(outsider); len(pkgs) != 0 {
		t.Fatalf("restricted package must be hidden from non-members, got %d", len(pkgs))
	}
	if pkgs, _ := st.ListPackagesForUser(member); len(pkgs) != 1 {
		t.Fatalf("member should see the package, got %d", len(pkgs))
	}
}

// The gate must live inside the purchase transaction, not only in the listing:
// a non-member who POSTs the id directly must still be refused, and keep their
// points.
func TestPurchaseRejectsNonMember(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "内测专属包")
	vip := mkGroup(t, st, "内测组")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{vip}); err != nil {
		t.Fatal(err)
	}
	outsider := mkUser(t, st, "outsider")

	_, err := st.Purchase(outsider, pkg, "", noopSync)
	if !errors.Is(err, ErrPackageNotAllowed) {
		t.Fatalf("want ErrPackageNotAllowed, got %v", err)
	}
	u, _ := st.UserByID(outsider)
	if u.Points != 1_000_000 {
		t.Fatalf("points must be untouched after a refused buy, got %d", u.Points)
	}
	if u.TrafficLimit != 0 {
		t.Fatalf("entitlement must be untouched, got traffic_limit=%d", u.TrafficLimit)
	}
}

// Membership in ANY bound group is enough (multi-group packages are OR, not AND).
func TestAnyBoundGroupGrantsAccess(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "双组包")
	a := mkGroup(t, st, "A组")
	b := mkGroup(t, st, "B组")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{a, b}); err != nil {
		t.Fatal(err)
	}
	onlyB := mkUser(t, st, "onlyb")
	if err := st.SetUserGroups(onlyB, []int64{b}); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := st.CanBuyPackage(onlyB, pkg.ID); !allowed {
		t.Fatal("membership in one of several bound groups must suffice")
	}
}

// Revoking membership blocks future buys. It must NOT retroactively refund or
// disturb what the user already bought — that's the "老用户保护" requirement.
func TestRevokingMembershipBlocksRebuyButKeepsEntitlement(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "内测专属包")
	vip := mkGroup(t, st, "内测组")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{vip}); err != nil {
		t.Fatal(err)
	}
	member := mkUser(t, st, "member")
	if err := st.SetUserGroups(member, []int64{vip}); err != nil {
		t.Fatal(err)
	}
	buy(t, st, member, pkg)

	before, _ := st.UserByID(member)
	if err := st.SetUserGroups(member, []int64{}); err != nil { // kicked out
		t.Fatal(err)
	}

	if _, err := st.Purchase(member, pkg, "", noopSync); !errors.Is(err, ErrPackageNotAllowed) {
		t.Fatalf("want ErrPackageNotAllowed after removal, got %v", err)
	}
	after, _ := st.UserByID(member)
	if after.TrafficLimit != before.TrafficLimit || after.ExpiryAt != before.ExpiryAt {
		t.Fatal("removing a user from a group must not touch what they already bought")
	}
	// The bucket they bought survives too.
	buckets, err := st.ListBuckets(member)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) == 0 {
		t.Fatal("already-purchased bucket must survive group removal")
	}
}

// Deleting a group removes it everywhere, and the packages that were restricted
// to only it fall back to public (DeleteUserGroup's documented behaviour).
func TestDeleteUserGroupCascades(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "内测专属包")
	vip := mkGroup(t, st, "内测组")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{vip}); err != nil {
		t.Fatal(err)
	}
	member := mkUser(t, st, "member")
	if err := st.SetUserGroups(member, []int64{vip}); err != nil {
		t.Fatal(err)
	}

	orphaned, err := st.PackagesRestrictedToOnly(vip)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphaned) != 1 || orphaned[0] != pkg.ID {
		t.Fatalf("want the package flagged as would-become-public, got %v", orphaned)
	}

	if err := st.DeleteUserGroup(vip); err != nil {
		t.Fatal(err)
	}
	if gids, _ := st.UserGroupIDs(member); len(gids) != 0 {
		t.Fatalf("membership rows must be cleaned up, got %v", gids)
	}
	if gids, _ := st.PackageUserGroupIDs(pkg.ID); len(gids) != 0 {
		t.Fatalf("package bindings must be cleaned up, got %v", gids)
	}
	if allowed, _ := st.CanBuyPackage(mkUser(t, st, "stranger"), pkg.ID); !allowed {
		t.Fatal("package with its last binding deleted becomes public")
	}
}

// Bindings must not accept ids that aren't real groups, or a typo would create
// a binding no one can ever satisfy — silently making the package unbuyable.
func TestBindingsIgnoreUnknownGroupIDs(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "包")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{9999}); err != nil {
		t.Fatal(err)
	}
	if gids, _ := st.PackageUserGroupIDs(pkg.ID); len(gids) != 0 {
		t.Fatalf("unknown group id must not be bound, got %v", gids)
	}
	user := mkUser(t, st, "u")
	if err := st.SetUserGroups(user, []int64{9999}); err != nil {
		t.Fatal(err)
	}
	if gids, _ := st.UserGroupIDs(user); len(gids) != 0 {
		t.Fatalf("unknown group id must not be assigned, got %v", gids)
	}
}

// SetGroupMembers replaces the whole membership from the group side.
func TestSetGroupMembers(t *testing.T) {
	st := newRefundStore(t)
	g := mkGroup(t, st, "内测组")
	other := mkGroup(t, st, "亲友组")
	u1 := mkUser(t, st, "u1")
	u2 := mkUser(t, st, "u2")
	u3 := mkUser(t, st, "u3")

	// u3 is in the other group — replacing this group's members must not touch it.
	if err := st.SetUserGroups(u3, []int64{other}); err != nil {
		t.Fatal(err)
	}

	if err := st.SetGroupMembers(g, []int64{u1, u2}); err != nil {
		t.Fatal(err)
	}
	members, err := st.ListUserGroupMembers(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("want 2 members, got %d", len(members))
	}

	// Replace with a different set: u1 out, u3 in.
	if err := st.SetGroupMembers(g, []int64{u2, u3}); err != nil {
		t.Fatal(err)
	}
	if gids, _ := st.UserGroupIDs(u1); len(gids) != 0 {
		t.Fatalf("u1 should have been removed, got %v", gids)
	}
	if gids, _ := st.UserGroupIDs(u3); len(gids) != 2 {
		t.Fatalf("u3 should be in both groups, got %v", gids) // other group untouched
	}

	// Empty list clears the group.
	if err := st.SetGroupMembers(g, nil); err != nil {
		t.Fatal(err)
	}
	if members, _ := st.ListUserGroupMembers(g); len(members) != 0 {
		t.Fatalf("want empty group, got %d members", len(members))
	}
	// ...and still doesn't touch the other group.
	if gids, _ := st.UserGroupIDs(u3); len(gids) != 1 {
		t.Fatalf("u3 should still be in 亲友组, got %v", gids)
	}
}

// A typo'd user id must not create a membership row for a user that isn't there.
func TestSetGroupMembersIgnoresUnknownUsers(t *testing.T) {
	st := newRefundStore(t)
	g := mkGroup(t, st, "内测组")
	u1 := mkUser(t, st, "u1")
	if err := st.SetGroupMembers(g, []int64{u1, 999999}); err != nil {
		t.Fatal(err)
	}
	members, _ := st.ListUserGroupMembers(g)
	if len(members) != 1 || members[0].ID != u1 {
		t.Fatalf("want only the real user, got %d members", len(members))
	}
}

// Membership edits must never disturb what a user already bought — the gate is
// purchase-time only.
func TestSetGroupMembersLeavesEntitlementsAlone(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPkg(t, st, "内测专属包")
	g := mkGroup(t, st, "内测组")
	if err := st.SetPackageUserGroups(pkg.ID, []int64{g}); err != nil {
		t.Fatal(err)
	}
	u := mkUser(t, st, "member")
	if err := st.SetGroupMembers(g, []int64{u}); err != nil {
		t.Fatal(err)
	}
	buy(t, st, u, pkg)
	before, _ := st.UserByID(u)

	if err := st.SetGroupMembers(g, nil); err != nil { // everyone kicked out
		t.Fatal(err)
	}
	after, _ := st.UserByID(u)
	if after.TrafficLimit != before.TrafficLimit || after.ExpiryAt != before.ExpiryAt {
		t.Fatal("clearing a group must not change what members already bought")
	}
	if allowed, _ := st.CanBuyPackage(u, pkg.ID); allowed {
		t.Fatal("removed member must no longer be able to buy")
	}
}

func TestUserGroupIDsBulk(t *testing.T) {
	st := newRefundStore(t)
	a := mkGroup(t, st, "A")
	b := mkGroup(t, st, "B")
	u1 := mkUser(t, st, "u1")
	u2 := mkUser(t, st, "u2")
	u3 := mkUser(t, st, "u3") // in no group
	if err := st.SetUserGroups(u1, []int64{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserGroups(u2, []int64{b}); err != nil {
		t.Fatal(err)
	}

	got, err := st.UserGroupIDsBulk([]int64{u1, u2, u3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[u1]) != 2 || len(got[u2]) != 1 {
		t.Fatalf("unexpected bulk result: %v", got)
	}
	if _, present := got[u3]; present {
		t.Fatal("a user with no groups must be absent from the map")
	}
	// Empty input must not build a broken IN () clause.
	if m, err := st.UserGroupIDsBulk(nil); err != nil || len(m) != 0 {
		t.Fatalf("empty input: %v %v", m, err)
	}
}
