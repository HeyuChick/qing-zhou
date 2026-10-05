package sbctl

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"qingzhou/internal/singbox"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/store"
)

// Only the process/transport boundary is fake. Entitlements, graph planning,
// staged readiness, config generation and controller convergence use Store.
// No test method opens SSH, invokes systemctl or contacts any configured host.
type rolloutRemote struct {
	mu       sync.Mutex
	configs  map[int64][]byte
	restarts map[int64]int
	calls    map[int64]int
	order    []int64
}

func (r *rolloutRemote) apply(id int64, raw []byte) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[id]++
	if bytes.Equal(r.configs[id], raw) {
		return false, nil
	}
	r.configs[id] = append([]byte(nil), raw...)
	r.restarts[id]++
	r.order = append(r.order, id)
	return true, nil
}
func (r *rolloutRemote) ApplyConfig(_ context.Context, cfg *sshctl.ServerConfig, raw []byte) (bool, error) {
	return r.apply(cfg.ID, raw)
}
func (*rolloutRemote) SupportsStatsAPI(context.Context, *sshctl.ServerConfig) (bool, string, error) {
	return true, "sing-box version 1.13.14\nTags: with_v2ray_api", nil
}
func (*rolloutRemote) ForgetSingBoxBin(int64) {}
func (*rolloutRemote) DialTunnel(context.Context, *sshctl.ServerConfig, string) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected tunnel in configuration-only fixture")
}
func (*rolloutRemote) RunCommand(context.Context, *sshctl.ServerConfig, string) (string, error) {
	return "", fmt.Errorf("unexpected command in configuration-only fixture")
}
func (r *rolloutRemote) snapshot() (map[int64]int, map[int64][]byte, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := map[int64]int{}
	configs := map[int64][]byte{}
	for id, n := range r.restarts {
		counts[id] = n
		configs[id] = append([]byte(nil), r.configs[id]...)
	}
	return counts, configs, len(r.order)
}

type rolloutPanel struct{ remote *rolloutRemote }

func (p rolloutPanel) Apply(raw []byte) error                { _, err := p.remote.apply(0, raw); return err }
func (p rolloutPanel) ApplyChanged(raw []byte) (bool, error) { return p.remote.apply(0, raw) }

func TestRelayUserControllerThreeHopRolloutNoRestartStorm(t *testing.T) {
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
	_ = addUser("existing-a")
	_ = addUser("existing-b")
	if err = st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	remote := &rolloutRemote{configs: map[int64][]byte{}, restarts: map[int64]int{}, calls: map[int64]int{}}
	c := New(st, rolloutPanel{remote}, nil, singbox.DefaultBaseConfig, "127.0.0.1:18080")
	c.remoteMgr = remote
	all := append([]int64{store.LocalNodeID}, ids...)
	assertDelta := func(stage string, before map[int64]int, changed map[int64]bool) {
		t.Helper()
		after, _, _ := remote.snapshot()
		for _, id := range all {
			want := 0
			if changed[id] {
				want = 1
			}
			if got := after[id] - before[id]; got != want {
				t.Errorf("%s: server %d restarted %d times, want exactly %d (counts %v -> %v)", stage, id, got, want, before, after)
			}
		}
	}
	assertActive := func(want int) {
		t.Helper()
		rows, err := st.RelayMeteringUsers()
		if err != nil {
			t.Fatal(err)
		}
		active := 0
		for _, row := range rows {
			if row.Enabled {
				if row.State != "active" {
					t.Errorf("user %d link %d not converged: %s", row.UserID, row.LinkID, row.State)
				}
				active++
			}
		}
		if active != want {
			t.Errorf("active per-user hop mappings=%d want=%d", active, want)
		}
	}
	assertNoop := func(stage string) {
		t.Helper()
		before, configs, _ := remote.snapshot()
		// Covers admin rebuild, the cheap timer pass, health reconciliation and a
		// fresh controller after a panel process restart (empty desired-hash cache).
		for name, rebuild := range map[string]func() error{"manual": c.Rebuild, "periodic": c.rebuildPeriodic, "health": c.reconcilePeriodic} {
			if err := rebuild(); err != nil {
				t.Fatalf("%s %s: %v", stage, name, err)
			}
		}
		fresh := New(st, rolloutPanel{remote}, nil, singbox.DefaultBaseConfig, "127.0.0.1:18080")
		fresh.remoteMgr = remote
		if err := fresh.Rebuild(); err != nil {
			t.Fatalf("%s panel restart: %v", stage, err)
		}
		assertDelta(stage, before, nil)
		_, after, _ := remote.snapshot()
		for _, id := range all {
			if !bytes.Equal(configs[id], after[id]) {
				t.Errorf("%s changed server %d config on no-op", stage, id)
			}
		}
	}
	before, _, offset := remote.snapshot()
	if err = c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	changed := map[int64]bool{}
	for _, id := range all {
		changed[id] = true
	}
	assertDelta("initial staged rollout", before, changed)
	assertActive(4)
	// Initial dependencies must be acknowledged downstream first. Unrelated
	// applies may interleave, but cannot reverse the route's acceptance boundary.
	remote.mu.Lock()
	order := append([]int64(nil), remote.order[offset:]...)
	remote.mu.Unlock()
	last := -1
	for _, id := range []int64{ids[2], ids[1], ids[0]} {
		at := -1
		for i, event := range order {
			if event == id {
				at = i
				break
			}
		}
		if at <= last {
			t.Errorf("downstream-first rollout violated: order=%v", order)
		}
		last = at
	}
	assertNoop("settled initial rollout")
	before, unchanged, _ := remote.snapshot()
	added := addUser("new-route-member")
	if err = c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	path := map[int64]bool{ids[0]: true, ids[1]: true, ids[2]: true}
	assertDelta("add one user", before, path)
	assertActive(6)
	_, after, _ := remote.snapshot()
	for _, id := range []int64{0, ids[3]} {
		if !bytes.Equal(unchanged[id], after[id]) {
			t.Errorf("add one user changed unrelated server %d", id)
		}
	}
	assertNoop("settled user addition")
	before, unchanged, _ = remote.snapshot()
	if err = st.DeleteUser(added); err != nil {
		t.Fatal(err)
	}
	if err = c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	assertDelta("delete one user", before, path)
	assertActive(4)
	_, after, _ = remote.snapshot()
	for _, id := range []int64{0, ids[3]} {
		if !bytes.Equal(unchanged[id], after[id]) {
			t.Errorf("delete one user changed unrelated server %d", id)
		}
	}
	assertNoop("settled user deletion")
}
