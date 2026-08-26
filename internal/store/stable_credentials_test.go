package store

import (
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

// Registration allowance, paid plans and parallel plan types are entitlement
// buckets, not identities. Moving between them must never change what the
// client authenticates with.
func TestStableCredential_IsSharedByWelcomeAndPaidPlans(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "seamless")
	if err := st.EnsureWelcomeBucket(uid, "seamless", 10*giB, time.Now().Unix()+86400); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByID(uid)
	if !u.ClientUUID.Valid || u.ClientUUID.String == "" {
		t.Fatal("welcome grant did not provision the user's primary credential")
	}
	wantUUID, wantSecret := u.ClientUUID.String, u.ClientSecret.String

	a := mkPlan(t, st, "A", 100, 100, 30)
	b := mkPlan(t, st, "B", 100, 100, 30)
	buy(t, st, uid, a)
	buy(t, st, uid, b)

	buckets, err := st.ListBuckets(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.ClientUUID != wantUUID || bucket.ClientSecret != wantSecret {
			t.Fatalf("bucket %q credential = %s/%s, want stable user credential %s/%s",
				bucket.Name, bucket.ClientUUID, bucket.ClientSecret, wantUUID, wantSecret)
		}
	}
}

// Logical routes used to hash the internal stats name, so changing the bucket
// that owned a node also changed its UUID. The v2 derivation hashes only the
// user credential and node id; billing names may change without touching links.
func TestStableCredential_RouteDerivationIgnoresMeteringName(t *testing.T) {
	a := deriveRouteUser(singbox.User{Name: "plan-a", UUID: "user-uuid", Password: "user-secret"}, 42)
	b := deriveRouteUser(singbox.User{Name: "plan-b", UUID: "user-uuid", Password: "user-secret"}, 42)
	if a.UUID != b.UUID || a.Password != b.Password {
		t.Fatalf("route wire credential changed with owner: %s/%s -> %s/%s", a.UUID, a.Password, b.UUID, b.Password)
	}
	if a.Name == b.Name {
		t.Fatal("route stats names collapsed; traffic could no longer follow the owning plan")
	}
}

func TestStableCredential_FreshInstallHasNoCompatibilityAliases(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "fresh-alias")
	if err := st.EnsureWelcomeBucket(uid, "fresh-alias", giB, time.Now().Unix()+86400); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_credential_aliases`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("fresh install created %d legacy credential aliases", n)
	}
}

// An online upgrade must keep the UUID already imported by a client accepted,
// while new subscriptions move to the user's stable primary. Re-running the
// migration must not extend the grace window or duplicate rows.
func TestStableCredential_UpgradeCapturesOldPlanCredentialOnce(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "legacy-cred")
	if err := st.SetUserClient(uid, 0, "qz_legacy-cred", "primary-uuid", "primary-secret"); err != nil {
		t.Fatal(err)
	}
	pkg := mkPlan(t, st, "旧套餐", 100, 100, 30)
	buy(t, st, uid, pkg)
	if _, err := st.db.Exec(`UPDATE plan_identities SET client_uuid='old-plan-uuid', client_secret='old-plan-secret'
		WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM user_credential_aliases; DELETE FROM schema_migrations WHERE version=?`,
		stableProtocolCredentialMigration); err != nil {
		t.Fatal(err)
	}

	if err := st.migrateStableProtocolCredentials(); err != nil {
		t.Fatal(err)
	}
	var n int
	var until int64
	if err := st.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(valid_until),0) FROM user_credential_aliases
		WHERE user_id=? AND client_uuid='old-plan-uuid'`, uid).Scan(&n, &until); err != nil {
		t.Fatal(err)
	}
	if n != 1 || until <= time.Now().Unix() {
		t.Fatalf("captured aliases = %d, valid_until=%d", n, until)
	}
	u, _ := st.UserByID(uid)
	if u.ClientUUID.String != "primary-uuid" || u.ClientSecret.String != "primary-secret" {
		t.Fatalf("migration replaced primary credential with %s/%s", u.ClientUUID.String, u.ClientSecret.String)
	}

	if err := st.migrateStableProtocolCredentials(); err != nil {
		t.Fatal(err)
	}
	var againN int
	var againUntil int64
	if err := st.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(valid_until),0) FROM user_credential_aliases
		WHERE user_id=? AND client_uuid='old-plan-uuid'`, uid).Scan(&againN, &againUntil); err != nil {
		t.Fatal(err)
	}
	if againN != n || againUntil != until {
		t.Fatalf("second migration changed aliases: %d/%d -> %d/%d", n, until, againN, againUntil)
	}
}

func TestStableCredential_AliasStatsResolveToCurrentBucket(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alias-meter")
	pkg := mkPlan(t, st, "B", 100, 100, 30)
	buy(t, st, uid, pkg)
	if _, err := st.SaveSbInbound(&SbInbound{Type: "vless", Tag: "alias-in", Listen: "::", ListenPort: 443, Options: "{}", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	bindPlanToInbound(t, st, pkg.ID, "alias-in")
	name, _, _ := liveCreds(t, st, uid, time.Now().Unix())
	now := time.Now().Unix()
	res, err := st.db.Exec(`INSERT INTO user_credential_aliases
		(user_id, source_name, client_uuid, client_secret, valid_until, created_at)
		VALUES (?,?,?,?,?,?)`, uid, "old-line", "old-uuid", "old-secret", now+3600, now)
	if err != nil {
		t.Fatal(err)
	}
	aliasID, _ := res.LastInsertId()
	byTag, err := st.BuildUsersByTag(now)
	if err != nil {
		t.Fatal(err)
	}
	accepted := false
	for _, u := range byTag["alias-in"] {
		if u.UUID == "old-uuid" {
			accepted = true
		}
	}
	if !accepted {
		t.Fatal("old UUID alias was not accepted by the generated inbound")
	}
	if err := st.AddBucketUsage(credentialAliasStatsName(name, aliasID), giB, 0); err != nil {
		t.Fatal(err)
	}
	var used int64
	if err := st.db.QueryRow(`SELECT used_up FROM user_plans WHERE user_id=? AND package_id=? AND status='active'`,
		uid, pkg.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != giB {
		t.Fatalf("alias traffic charged %d, want %d", used, int64(giB))
	}
}
