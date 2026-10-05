package sbctl

import (
	"context"
	"errors"
	"net"
	"qingzhou/internal/sshctl"
	"strings"
	"testing"

	"qingzhou/internal/sbver"
	"qingzhou/internal/store"
)

func fixedTransportVersion() string {
	return "sing-box version " + sbver.TransportReadBufferFixVersion + "\nTags: with_v2ray_api"
}

type transportStore struct {
	*visionStore
	required map[int64]store.RelayCoreRequirements
	coreDeps map[int64][]int64
}

func (s *transportStore) RelayUserCoreTopology() (map[int64]store.RelayCoreRequirements, map[int64][]int64, error) {
	return s.required, s.coreDeps, nil
}

func transportFixture(ids ...int64) (*Controller, *transportStore, *visionPanel, *visionRemote) {
	c, legacy, p, r := visionFixture(ids...)
	st := &transportStore{visionStore: legacy, required: map[int64]store.RelayCoreRequirements{}, coreDeps: map[int64][]int64{}}
	for _, id := range ids {
		st.required[id] = store.RelayCoreRequirements{TransportReadBuffer: true}
		st.coreDeps[id] = ids
		if id != 0 {
			r.disk[id] = fixedTransportVersion()
			r.running[id] = fixedTransportVersion()
		}
	}
	p.disk, p.running = fixedTransportVersion(), fixedTransportVersion()
	c.st = st
	return c, st, p, r
}

func TestTransportGateRequiresInstalledAndActualRunningFix(t *testing.T) {
	for _, id := range []int64{0, 1} {
		for _, bad := range []string{"old-vision-disk", "old-vision-running", "stock", "future-version", "unknown", "no-api", "inactive"} {
			t.Run(strings.Join([]string{map[bool]string{true: "local", false: "remote"}[id == 0], bad}, "/"), func(t *testing.T) {
				c, st, p, r := transportFixture(id)
				disk, running := fixedTransportVersion(), fixedTransportVersion()
				switch bad {
				case "old-vision-disk":
					disk = fixedVisionVersion()
				case "old-vision-running":
					running = fixedVisionVersion()
				case "stock":
					running = "sing-box version 1.14.2\nTags: with_v2ray_api"
				case "future-version":
					running = "sing-box version 1.99.0\nTags: with_v2ray_api"
				case "unknown":
					running = "sing-box version unknown"
				case "no-api":
					running = "sing-box version " + sbver.TransportReadBufferFixVersion
				case "inactive":
					running = ""
					if id == 0 {
						p.runningErr = errors.New("unit inactive")
					}
				}
				if id == 0 {
					p.disk, p.running = disk, running
				} else {
					r.disk[id], r.running[id] = disk, running
				}
				// Cached stats, desired bytes and a previous positive runtime receipt
				// are never substitutes for this rebuild's actual process inspection.
				c.statsCap[id] = statsProbe{ok: true}
				st.SetNodeVisionRuntime(id, sbver.Parse(fixedTransportVersion()))
				err := c.Rebuild()
				if err == nil || !strings.Contains(err.Error(), "P1 WebSocket/HTTPUpgrade") || strings.Contains(err.Error(), "P1 Vision") {
					t.Fatalf("bad gate diagnostic: %v", err)
				}
				if st.acks[id] != 0 || r.calls[id] != 0 || (id == 0 && p.applies != 0) {
					t.Fatal("unverified transport core applied/acknowledged")
				}
				if st.runtimeErrors[id] == "" {
					t.Fatal("unverified live core left a positive runtime receipt")
				}
			})
		}
	}
}

func TestTransportGateFreshRechecksNoopDowngradeAndRecovery(t *testing.T) {
	c, st, p, r := transportFixture(0, 1)
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if p.versions != 1 || p.inspections != 2 || r.versions[1] != 1 || r.inspections[1] != 2 {
		t.Fatal("initial installed+pre/post running probes missing")
	}
	if err := c.rebuildPeriodic(); err != nil {
		t.Fatal(err)
	}
	if p.versions != 2 || p.inspections != 3 || r.versions[1] != 2 || r.inspections[1] != 3 {
		t.Fatal("no-op used stale capability cache")
	}
	r.running[1] = fixedVisionVersion()
	if err := c.rebuildPeriodic(); err == nil {
		t.Fatal("downgraded process accepted during no-op")
	}
	if p.applies != 1 || st.acks[1] != 1 {
		t.Fatal("failed component changed config")
	}
	st.SetNodeSingbox(1, sbver.Parse(fixedTransportVersion()))
	if st.runtimeErrors[1] == "" {
		t.Fatal("disk-only refresh erased running failure")
	}
	r.running[1] = fixedTransportVersion()
	if err := c.rebuildPeriodic(); err != nil {
		t.Fatal(err)
	}
	if p.applies != 2 || st.acks[1] != 2 || st.runtimeErrors[1] != "" {
		t.Fatal("recovery skipped actual apply/health acknowledgement")
	}
}

func TestTransportGateRechecksProcessAfterApplyBeforeAck(t *testing.T) {
	for _, id := range []int64{0, 1} {
		c, st, p, r := transportFixture(id)
		if id == 0 {
			p.afterApply = fixedVisionVersion()
		} else {
			r.afterApply[id] = fixedVisionVersion()
		}
		if err := c.Rebuild(); err == nil {
			t.Fatal("restart to Vision-only core accepted")
		}
		if st.acks[id] != 0 {
			t.Fatal("unverified restarted target acknowledged")
		}
		if _, ok := c.desiredHash[id]; ok {
			t.Fatal("unverified restarted core entered desired cache")
		}
	}
}

func TestTransportGateOnlyHoldsDependentComponents(t *testing.T) {
	c, st, p, r := transportFixture(0, 1, 2, 3, 4)
	st.required = map[int64]store.RelayCoreRequirements{1: {TransportReadBuffer: true}, 2: {TransportReadBuffer: true}, 3: {VisionFraming: true}}
	st.coreDeps = map[int64][]int64{0: {1, 2}, 1: {1, 2}, 2: {1, 2}, 3: {3}}
	r.disk[3], r.running[3] = fixedVisionVersion(), fixedVisionVersion()
	r.running[2] = fixedVisionVersion()
	if err := c.Rebuild(); err == nil {
		t.Fatal("transport downgrade not reported")
	}
	if p.applies != 0 || p.versions != 0 || p.inspections != 0 {
		t.Fatal("plain dependent applied or was unnecessarily probed")
	}
	for _, id := range []int64{1, 2} {
		if r.calls[id] != 0 || st.acks[id] != 0 {
			t.Fatal("failed component was applied")
		}
	}
	for _, id := range []int64{3, 4} {
		if r.calls[id] != 1 || st.acks[id] != 1 {
			t.Fatalf("unrelated component %d held", id)
		}
	}
	if r.versions[3] != 1 || r.inspections[3] != 2 || r.versions[4] != 0 {
		t.Fatal("core requirements were not scoped by actual capability")
	}
}

func TestTransportGateRequiresAvailableRuntimeInspector(t *testing.T) {
	c, st, _, r := transportFixture(1)
	// This adapter reports only installed versions; it deliberately lacks the
	// managed-process inspection interface. It must never stand in for /proc.
	c.remoteMgr = &transportDiskOnlyRemote{visionRemote: r}
	if err := c.Rebuild(); err == nil || !strings.Contains(err.Error(), "running-version inspection unavailable") {
		t.Fatalf("missing source boundary accepted: %v", err)
	}
	if st.acks[1] != 0 || r.calls[1] != 0 {
		t.Fatal("disk-only adapter applied transport path")
	}
}

type transportDiskOnlyRemote struct{ visionRemote *visionRemote }

func (r *transportDiskOnlyRemote) ApplyConfig(ctx context.Context, cfg *sshctl.ServerConfig, raw []byte) (bool, error) {
	return r.visionRemote.ApplyConfig(ctx, cfg, raw)
}
func (r *transportDiskOnlyRemote) SupportsStatsAPI(ctx context.Context, cfg *sshctl.ServerConfig) (bool, string, error) {
	return r.visionRemote.SupportsStatsAPI(ctx, cfg)
}
func (r *transportDiskOnlyRemote) ForgetSingBoxBin(id int64) { r.visionRemote.ForgetSingBoxBin(id) }
func (r *transportDiskOnlyRemote) DialTunnel(ctx context.Context, cfg *sshctl.ServerConfig, addr string) (net.Conn, error) {
	return r.visionRemote.DialTunnel(ctx, cfg, addr)
}
func (r *transportDiskOnlyRemote) RunCommand(ctx context.Context, cfg *sshctl.ServerConfig, cmd string) (string, error) {
	return r.visionRemote.RunCommand(ctx, cfg, cmd)
}
