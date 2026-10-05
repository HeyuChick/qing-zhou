package sbctl

import (
	"context"
	"errors"
	"testing"

	"qingzhou/internal/sbproc"
	"qingzhou/internal/sbstats"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/store"
)

var numericMixedConfig = []byte(`{"inbounds":[{"type":"mixed","users":[{"username":"relay_42","password":"unchanged"}]}]}`)

type namespaceRemote struct {
	epochRemote
	inspections []sbproc.ManagedProcess
	inspectAt   int
	inspectErr  error
	forced      []bool
	restarted   bool
	applyErr    error
}

func (r *namespaceRemote) InspectManagedProcess(context.Context, *sshctl.ServerConfig) (sbproc.ManagedProcess, error) {
	if r.inspectErr != nil {
		return sbproc.ManagedProcess{}, r.inspectErr
	}
	i := r.inspectAt
	r.inspectAt++
	if i >= len(r.inspections) {
		i = len(r.inspections) - 1
	}
	return r.inspections[i], nil
}
func (r *namespaceRemote) ApplyConfigForce(_ context.Context, _ *sshctl.ServerConfig, _ []byte, force bool) (bool, error) {
	r.forced = append(r.forced, force)
	return r.restarted, r.applyErr
}
func namespaceReceipt(epoch string) sbproc.ManagedProcess {
	return sbproc.ManagedProcess{Epoch: epoch, ConfigHash: sshctl.InstalledConfigHash(numericMixedConfig)}
}

func TestRelayNamespaceRequiresCompletedRestart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		restarted bool
		after     string
		applyErr  error
		prove     bool
	}{
		{"crash_file_written_old_core_noop", false, epochOne, nil, false},
		{"restart_returned_but_old_epoch", true, epochOne, nil, false},
		{"failed_restart", true, epochTwo, errors.New("restart failed"), false},
		{"completed_restart", true, epochTwo, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, st, _, sv := snapshotController(t)
			r := &namespaceRemote{inspections: []sbproc.ManagedProcess{namespaceReceipt(epochOne), namespaceReceipt(tc.after)}, restarted: tc.restarted, applyErr: tc.applyErr}
			c.remoteMgr = r
			_, err := c.applyRemoteConfig(context.Background(), sv, numericMixedConfig)
			if (err != nil) != (tc.applyErr != nil) {
				t.Fatalf("apply error %v", err)
			}
			if len(r.forced) != 1 || !r.forced[0] {
				t.Fatal("unknown running epoch must request a controlled restart")
			}
			known, err := st.RelayNamespaceEpochKnown(sv.ID, tc.after)
			if err != nil || known != tc.prove {
				t.Fatalf("proof=%v err=%v", known, err)
			}
		})
	}
}

func TestRelayNamespaceNoopRequiresExactExistingProof(t *testing.T) {
	c, st, _, sv := snapshotController(t)
	if err := st.RecordRelayNamespaceEpoch(sv.ID, epochOne, numericMixedConfig); err != nil {
		t.Fatal(err)
	}
	r := &namespaceRemote{inspections: []sbproc.ManagedProcess{namespaceReceipt(epochOne)}}
	c.remoteMgr = r
	if _, err := c.applyRemoteConfig(context.Background(), sv, numericMixedConfig); err != nil {
		t.Fatal(err)
	}
	if len(r.forced) != 1 || r.forced[0] {
		t.Fatal("proven no-op caused an unnecessary restart")
	}
	// An externally restarted process cannot borrow the old process's hash proof.
	r = &namespaceRemote{inspections: []sbproc.ManagedProcess{namespaceReceipt(epochTwo)}}
	c.remoteMgr = r
	if _, err := c.applyRemoteConfig(context.Background(), sv, numericMixedConfig); err != nil {
		t.Fatal(err)
	}
	known, _ := st.RelayNamespaceEpochKnown(sv.ID, epochTwo)
	if known || !r.forced[0] {
		t.Fatal("unknown no-op epoch borrowed old hash proof")
	}
}

func TestRelayNamespaceUnsupportedAndUnrelatedConfigsDoNotForce(t *testing.T) {
	c, st, _, sv := snapshotController(t)
	r := &namespaceRemote{inspectErr: errors.New("not a single managed systemd config")}
	c.remoteMgr = r
	for i := 0; i < 2; i++ {
		if _, err := c.applyRemoteConfig(context.Background(), sv, numericMixedConfig); err != nil {
			t.Fatal(err)
		}
	}
	for _, force := range r.forced {
		if force {
			t.Fatal("unsupported capability caused a restart loop")
		}
	}
	known, _ := st.RelayNamespaceEpochKnown(sv.ID, epochOne)
	if known {
		t.Fatal("unsupported process gained proof")
	}
	r = &namespaceRemote{}
	c.remoteMgr = r
	if _, err := c.applyRemoteConfig(context.Background(), sv, []byte(`{"inbounds":[{"type":"mixed","users":[{"username":"ordinary"}]}]}`)); err != nil {
		t.Fatal(err)
	}
	if r.inspectAt != 0 || r.forced[0] {
		t.Fatal("another server's numeric customer affected this node")
	}
}

func TestRelayNamespaceRejectsWrongInstalledBytes(t *testing.T) {
	c, st, _, sv := snapshotController(t)
	wrong := namespaceReceipt(epochTwo)
	wrong.ConfigHash = namespaceConfigHash(numericMixedConfig) // remote adds newline
	r := &namespaceRemote{inspections: []sbproc.ManagedProcess{namespaceReceipt(epochOne), wrong}, restarted: true}
	c.remoteMgr = r
	_, err := c.applyRemoteConfig(context.Background(), sv, numericMixedConfig)
	if err != nil {
		t.Fatal(err)
	}
	known, _ := st.RelayNamespaceEpochKnown(sv.ID, epochTwo)
	if known {
		t.Fatal("wrong installed bytes acquired proof")
	}
}

func TestRelayNamespaceCustomLocalUnit(t *testing.T) {
	c, st, _, _ := snapshotController(t)
	if err := st.SetSetting("sb_systemd_unit", "saved-custom.service"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QZ_SINGBOX_UNIT", "")
	if c.trafficUnit(nil) != "saved-custom.service" {
		t.Fatal("saved unit ignored")
	}
	t.Setenv("QZ_SINGBOX_UNIT", "env-custom.service")
	if c.trafficUnit(nil) != "env-custom.service" {
		t.Fatal("environment unit ignored")
	}
	if c.trafficUnit(&store.Server{SystemdUnit: "server.service"}) != "server.service" {
		t.Fatal("per-server unit ignored")
	}
}

type namesFixture struct{ traffic map[string]*sbstats.Traffic }

func (f *namesFixture) QueryTraffic(context.Context, bool) (map[string]*sbstats.Traffic, error) {
	return f.traffic, nil
}

func TestRelayNamespaceUnknownEpochDoesNotBlockOrdinaryUsers(t *testing.T) {
	for _, cumulative := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "cumulative"}[cumulative], func(t *testing.T) {
			c, st, uid, sv := snapshotController(t)
			if err := st.SetSetting(cumulativeMeteringSetting, map[bool]string{false: "false", true: "true"}[cumulative]); err != nil {
				t.Fatal(err)
			}
			// A historic numeric customer has since moved to another node. This node's
			// current process is unknown, and its file is no longer the proven hash.
			if _, err := st.DB().Exec(`UPDATE users SET proxy_username='relay_42' WHERE id=?`, uid); err != nil {
				t.Fatal(err)
			}
			if err := st.RecordRelayNamespaceEpoch(sv.ID, epochTwo, numericMixedConfig); err != nil {
				t.Fatal(err)
			}
			f := &namesFixture{traffic: map[string]*sbstats.Traffic{"qz_snapshot": {Down: 10}, "relay_42": {Down: 900}}}
			if _, err := c.collectTrafficSnapshot(context.Background(), sv.ID, sv, f); err != nil {
				t.Fatal(err)
			}
			want := int64(10)
			if cumulative {
				want = 20
			} // final reset plus first cumulative delta
			if got := snapshotUsed(t, st, uid); got != want {
				t.Fatalf("ordinary metering=%d want %d", got, want)
			}
			known, _ := st.RelayNamespaceEpochKnown(sv.ID, epochOne)
			if known {
				t.Fatal("collector silently promoted an unknown epoch")
			}
			var n int
			if err := st.DB().QueryRow(`SELECT COUNT(*) FROM traffic_observations WHERE server_id=? AND counter_name='relay_42' AND source_kind='unknown'`, sv.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatal("raw numeric observation was not kept unresolved")
			}
		})
	}
}
