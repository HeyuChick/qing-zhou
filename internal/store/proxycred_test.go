package store

import (
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

func planBucket(t *testing.T, st *Store, uid int64) *Bucket {
	t.Helper()
	buckets, _ := st.ListBuckets(uid)
	for _, b := range buckets {
		if b.Kind == "plan" {
			return b
		}
	}
	t.Fatal("no plan bucket")
	return nil
}

// A custom mixed-proxy username must (a) be injected into the mixed inbound's
// config, (b) meter traffic to the owning bucket just like client_name, and (c)
// coexist with the client_name identity (both attribute to the same bucket).
func TestProxyCred_MeteringAndConfig(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)
	bkt := planBucket(t, st, uid)

	if err := st.SetBucketProxyCred(bkt.ID, uid, "alice-proxy", "s3cretpassword", 0); err != nil {
		t.Fatalf("SetBucketProxyCred: %v", err)
	}

	// Zero-config (no groups) → the active bucket owns every inbound.
	if _, err := st.SaveSbInbound(&SbInbound{Type: "mixed", Tag: "mixed-proxy", Listen: "::", ListenPort: 7890, Options: "{}", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	usersByTag, err := st.BuildUsersByTag(now)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "127.0.0.1:18080", usersByTag)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if !strings.Contains(s, `"username": "alice-proxy"`) || !strings.Contains(s, `"password": "s3cretpassword"`) {
		t.Fatalf("mixed inbound must use the custom proxy credential:\n%s", s)
	}
	if strings.Contains(s, bkt.ClientName) {
		// only the mixed inbound exists here; its identity must be the proxy name, not client_name
		t.Fatalf("mixed inbound leaked client_name %q instead of proxy_username:\n%s", bkt.ClientName, s)
	}

	// Metering under the custom proxy_username lands on the bucket.
	if err := st.AddBucketUsage("alice-proxy", 1000, 2000); err != nil {
		t.Fatal(err)
	}
	if b := planBucket(t, st, uid); b.UsedUp != 1000 || b.UsedDown != 2000 {
		t.Fatalf("proxy_username metering wrong: up=%d down=%d", b.UsedUp, b.UsedDown)
	}
	// The client_name identity still meters the same bucket (non-mixed protocols).
	if err := st.AddBucketUsage(bkt.ClientName, 5, 7); err != nil {
		t.Fatal(err)
	}
	if b := planBucket(t, st, uid); b.UsedUp != 1005 || b.UsedDown != 2007 {
		t.Fatalf("client_name metering wrong: up=%d down=%d", b.UsedUp, b.UsedDown)
	}
	// An unknown identity is ignored, not misattributed.
	if err := st.AddBucketUsage("ghost", 999, 999); err != nil {
		t.Fatal(err)
	}
	if b := planBucket(t, st, uid); b.UsedUp != 1005 {
		t.Fatalf("unknown identity must not be metered: up=%d", b.UsedUp)
	}
}

// An expired proxy credential drops the bucket from the mixed inbound; a mixed
// inbound left with no users must be omitted entirely (no open proxy).
func TestProxyCred_ExpiryDropsInbound(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bob")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)
	bkt := planBucket(t, st, uid)

	if _, err := st.SaveSbInbound(&SbInbound{Type: "mixed", Tag: "mixed-proxy", Listen: "::", ListenPort: 7890, Options: "{}", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// Proxy credential already expired.
	if err := st.SetBucketProxyCred(bkt.ID, uid, "bob-proxy", "s3cretpassword", now-1); err != nil {
		t.Fatal(err)
	}
	usersByTag, _ := st.BuildUsersByTag(now)
	cfg, _ := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "127.0.0.1:18080", usersByTag)
	if strings.Contains(string(cfg), "mixed-proxy") {
		t.Fatalf("expired proxy cred must drop the userless mixed inbound:\n%s", cfg)
	}
}

func TestProxyCred_Validation(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carol")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)
	bkt := planBucket(t, st, uid)

	// qz_ prefix is reserved (would collide with system client_names).
	if err := st.SetBucketProxyCred(bkt.ID, uid, "qz_hack", "password123", 0); err == nil {
		t.Fatal("qz_-prefixed username must be rejected")
	}
	// bad charset / too short.
	if err := st.SetBucketProxyCred(bkt.ID, uid, "a b", "password123", 0); err == nil {
		t.Fatal("username with space must be rejected")
	}
	if err := st.SetBucketProxyCred(bkt.ID, uid, "goodname", "123", 0); err == nil {
		t.Fatal("too-short password must be rejected")
	}
	// Wrong owner can't edit someone else's bucket.
	if err := st.SetBucketProxyCred(bkt.ID, uid+999, "goodname", "password123", 0); err == nil {
		t.Fatal("ownership must be enforced")
	}

	// Uniqueness across buckets: a second user can't take an existing proxy_username.
	if err := st.SetBucketProxyCred(bkt.ID, uid, "shared-name", "password123", 0); err != nil {
		t.Fatalf("first set should succeed: %v", err)
	}
	uid2 := mkUser(t, st, "dave")
	buy(t, st, uid2, pkg)
	bkt2 := planBucket(t, st, uid2)
	if err := st.SetBucketProxyCred(bkt2.ID, uid2, "shared-name", "password123", 0); err == nil {
		t.Fatal("duplicate proxy_username across buckets must be rejected")
	}
}
