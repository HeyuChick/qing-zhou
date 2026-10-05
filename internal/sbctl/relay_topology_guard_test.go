package sbctl

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"qingzhou/internal/singbox"
	"qingzhou/internal/store"
	"testing"
	"time"
)

func TestRelayTopologyGuardPreservesUnrelatedRevocation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "rollout.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	st.SetSecretKey([]byte("synthetic-controller-rollout-key"))
	// Three machines carry the route; a fourth unrelated node plus the panel
	// detect accidentally broad invalidation when one customer's access changes.
	ids := make([]int64, 4)
	ibs := make([]int64, 4)
	for i := range ids {
		ids[i], err = st.CreateServer(store.Server{Name: fmt.Sprintf("fixture-%d", i), Host: fmt.Sprintf("192.0.2.%d", 10+i), Enabled: true, V2rayListen: "127.0.0.1:18080"})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 3; i >= 0; i-- {
		var target int64
		if i < 2 {
			target = ibs[i+1]
		}
		ibs[i], err = st.SaveSbInbound(&store.SbInbound{ServerID: ids[i], Type: "vless", Tag: fmt.Sprintf("rollout-%d", i), ListenPort: 2443, Options: `{}`, Enabled: true, UpstreamInboundID: target})
		if err != nil {
			t.Fatal(err)
		}
	}
	pkgID, err := st.CreatePackage(store.Package{Type: "plan", Name: "three-hop-fixture", PricePoints: 10, TrafficBytes: 10 << 30, DurationDays: 30, Stock: -1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := st.GetPackage(pkgID)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := st.CreateGroup(store.NodeGroup{Name: "fixture-entry"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetPlanGroups(pkgID, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	nid, err := st.CreateNode(store.Node{Type: "self_built", Name: "rollout-0", InboundTag: "rollout-0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetNodeGroups(nid, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	addUser := func(name string) int64 {
		t.Helper()
		uid, err := st.CreateUser(store.NewUser{Username: name, PasswordHash: "fixture-only", Points: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.Purchase(uid, pkg, "", func(*store.User, bool) error { return nil }); err != nil {
			t.Fatal(err)
		}
		return uid
	}
	revoked := addUser("existing-a")
	_ = addUser("existing-b")
	if err = st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	remote := &rolloutRemote{configs: map[int64][]byte{}, restarts: map[int64]int{}, calls: map[int64]int{}}
	c := New(st, rolloutPanel{remote}, nil, singbox.DefaultBaseConfig, "127.0.0.1:18080")
	c.remoteMgr = remote
	nid2, err := st.CreateNode(store.Node{Type: "self_built", Name: "independent", InboundTag: "rollout-3", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetNodeGroups(nid2, []int64{gid}); err != nil {
		t.Fatal(err)
	}
	if err = c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	_, before, _ := remote.snapshot()
	landing, _ := st.GetSbInbound(ibs[2])
	landing.Enabled = false
	if _, err = st.SaveSbInbound(landing); !errors.Is(err, store.ErrRelayTopology) {
		t.Fatalf("expected rejected target disable, got %v", err)
	}
	stored, err := st.GetSbInbound(landing.ID)
	if err != nil || !stored.Enabled {
		t.Fatalf("target disable was not rolled back: %v", err)
	}
	if err = st.DeleteUser(revoked); err != nil {
		t.Fatal(err)
	}
	users, err := st.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	desired, err := st.BuildSingboxConfigForServer(ids[3], singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(desired, before[ids[3]]) {
		t.Fatal("fixture failed: isolated desired must remove revoked user")
	}
	remote.mu.Lock()
	callsBefore := remote.calls[ids[3]]
	remote.mu.Unlock()
	err = c.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	_, after, _ := remote.snapshot()
	remote.mu.Lock()
	callsAfter := remote.calls[ids[3]]
	remote.mu.Unlock()
	if bytes.Equal(before[ids[3]], after[ids[3]]) || callsAfter <= callsBefore {
		t.Fatal("unrelated live config did not revoke user")
	}
	if !bytes.Equal(desired, after[ids[3]]) {
		t.Fatal("unrelated live config differs from desired revoked-user config")
	}
	t.Logf("P1 target disable rejected atomically; independent server apply calls %d -> %d; live config equals revoked-user desired config", callsBefore, callsAfter)
}
