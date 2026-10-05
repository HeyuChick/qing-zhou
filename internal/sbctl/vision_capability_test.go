package sbctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"qingzhou/internal/sbver"
	"qingzhou/internal/sshctl"
	"qingzhou/internal/store"
)

func fixedVisionVersion() string {
	return "sing-box version " + sbver.VisionFramingFixVersion + "\nTags: with_v2ray_api"
}

type visionStore struct {
	dependencies    map[int64][]int64
	runtimeInfo     map[int64]sbver.Info
	runtimeErrors   map[int64]string
	runtimeWriteErr error
	schedFakeStore
	mu       sync.Mutex
	ids      []int64
	servers  []*store.Server
	info     map[int64]sbver.Info
	failures map[int64]string
	acks     map[int64]int
}

func (s *visionStore) RelayUserVisionServerIDs() ([]int64, error) { return s.ids, nil }
func (s *visionStore) RelayUserVisionDependencies() (map[int64][]int64, error) {
	if s.dependencies != nil {
		return s.dependencies, nil
	}
	deps := map[int64][]int64{}
	for _, id := range s.ids {
		deps[id] = s.ids
	}
	return deps, nil
}
func (s *visionStore) ListServers() ([]*store.Server, error) { return s.servers, nil }
func (s *visionStore) GetServer(id int64) (*store.Server, error) {
	for _, sv := range s.servers {
		if sv.ID == id {
			return sv, nil
		}
	}
	return nil, nil
}
func (s *visionStore) SetNodeSingbox(id int64, info sbver.Info) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info[id] = info
	delete(s.failures, id)
	return nil
}
func (s *visionStore) SetNodeSingboxError(id int64, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[id] = msg
	return nil
}
func (*visionStore) RelayMeteringEnabled() bool             { return true }
func (*visionStore) PrepareRelayMetering() error            { return nil }
func (*visionStore) RelayMeteringProgress() (string, error) { return "fixture", nil }
func (s *visionStore) RecordRelayConfigApplied(id int64, _ []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acks[id]++
	return nil
}

func (s *visionStore) SetNodeVisionRuntime(id int64, info sbver.Info) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeWriteErr != nil {
		return s.runtimeWriteErr
	}
	if s.runtimeInfo == nil {
		s.runtimeInfo = map[int64]sbver.Info{}
	}
	s.runtimeInfo[id] = info
	delete(s.runtimeErrors, id)
	return nil
}
func (s *visionStore) SetNodeVisionRuntimeError(id int64, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeErrors == nil {
		s.runtimeErrors = map[int64]string{}
	}
	s.runtimeErrors[id] = msg
	return nil
}

type visionPanel struct {
	disk, running, afterApply      string
	runningErr                     error
	versions, inspections, applies int
	unit, path                     string
}

func (p *visionPanel) Apply([]byte) error {
	p.applies++
	if p.afterApply != "" {
		p.running = p.afterApply
	}
	return nil
}
func (p *visionPanel) Version(context.Context) (string, error) { p.versions++; return p.disk, nil }
func (p *visionPanel) InspectManagedVersion(_ context.Context, unit, path string) (string, error) {
	p.inspections++
	p.unit, p.path = unit, path
	return p.running, p.runningErr
}
func (*visionPanel) ConfigPath() string { return "/fixture/panel.json" }

type visionRemote struct {
	rolloutRemote
	muVision                      sync.Mutex
	disk, running, afterApply     map[int64]string
	versions, inspections, forgot map[int64]int
}

func (r *visionRemote) SupportsStatsAPI(_ context.Context, cfg *sshctl.ServerConfig) (bool, string, error) {
	r.muVision.Lock()
	defer r.muVision.Unlock()
	r.versions[cfg.ID]++
	return true, r.disk[cfg.ID], nil
}
func (r *visionRemote) InspectManagedVersion(_ context.Context, cfg *sshctl.ServerConfig) (string, error) {
	r.muVision.Lock()
	defer r.muVision.Unlock()
	r.inspections[cfg.ID]++
	return r.running[cfg.ID], nil
}
func (r *visionRemote) ForgetSingBoxBin(id int64) {
	r.muVision.Lock()
	defer r.muVision.Unlock()
	r.forgot[id]++
}
func (r *visionRemote) ApplyConfig(ctx context.Context, cfg *sshctl.ServerConfig, raw []byte) (bool, error) {
	changed, err := r.rolloutRemote.ApplyConfig(ctx, cfg, raw)
	r.muVision.Lock()
	defer r.muVision.Unlock()
	if next := r.afterApply[cfg.ID]; next != "" {
		r.running[cfg.ID] = next
	}
	return changed, err
}

func visionFixture(ids ...int64) (*Controller, *visionStore, *visionPanel, *visionRemote) {
	st := &visionStore{ids: ids, info: map[int64]sbver.Info{}, failures: map[int64]string{}, acks: map[int64]int{}}
	p := &visionPanel{disk: fixedVisionVersion(), running: fixedVisionVersion()}
	r := &visionRemote{rolloutRemote: rolloutRemote{configs: map[int64][]byte{}, calls: map[int64]int{}, restarts: map[int64]int{}}, disk: map[int64]string{}, running: map[int64]string{}, afterApply: map[int64]string{}, versions: map[int64]int{}, inspections: map[int64]int{}, forgot: map[int64]int{}}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		st.servers = append(st.servers, &store.Server{ID: id, Host: fmt.Sprintf("192.0.2.%d", id), Enabled: true})
		r.disk[id] = fixedVisionVersion()
		r.running[id] = fixedVisionVersion()
	}
	c := New(st, p, nil, "{}", "")
	c.remoteMgr = r
	return c, st, p, r
}

func TestVisionGateRejectsUnverifiedLocalCoreBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name, disk, running string
		err                 error
	}{
		{"stock_same_version", "sing-box version 1.14.2", "sing-box version 1.14.2", nil},
		{"older", "sing-box version 1.13.14", fixedVisionVersion(), nil},
		{"marker_missing", "sing-box version 1.14.2+other", fixedVisionVersion(), nil},
		{"unknown", "sing-box version unknown", fixedVisionVersion(), nil},
		{"disk_swapped_process_stale", fixedVisionVersion(), "sing-box version 1.14.2", nil},
		{"not_running", fixedVisionVersion(), "", errors.New("unit inactive")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, st, p, _ := visionFixture(0)
			p.disk, p.running, p.runningErr = tc.disk, tc.running, tc.err
			if err := c.Rebuild(); err == nil || !strings.Contains(err.Error(), "P1 Vision") {
				t.Fatalf("gate=%v", err)
			}
			if p.applies != 0 || st.acks[0] != 0 {
				t.Fatal("incompatible core changed config or acknowledged readiness")
			}
			if st.failures[0] == "" || st.runtimeErrors[0] == "" {
				t.Fatal("runtime failure not recorded")
			}
		})
	}
}

func TestVisionGateFreshProbesEveryTopologyNodeAndNoop(t *testing.T) {
	c, st, p, r := visionFixture(0, 1, 2)
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if p.versions != 1 || p.inspections != 2 || p.unit != "sing-box" || p.path != "/fixture/panel.json" {
		t.Fatalf("local probe %+v", p)
	}
	for _, id := range []int64{1, 2} {
		if r.versions[id] != 1 || r.inspections[id] != 2 || st.acks[id] != 1 {
			t.Fatalf("node %d probes=%d/%d ack=%d", id, r.versions[id], r.inspections[id], st.acks[id])
		}
	}
	if err := c.rebuildPeriodic(); err != nil {
		t.Fatal(err)
	}
	if p.applies != 1 || p.versions != 2 || p.inspections != 3 {
		t.Fatalf("unchanged config bypassed fresh version probe: %+v", p)
	}
	for _, id := range []int64{1, 2} {
		if r.versions[id] != 2 || r.inspections[id] != 3 || st.acks[id] != 1 {
			t.Fatalf("node %d no-op probes=%d/%d ack=%d", id, r.versions[id], r.inspections[id], st.acks[id])
		}
	}
}

func TestVisionGateDowngradeIgnoresPositiveStatsAndDesiredCaches(t *testing.T) {
	c, st, p, r := visionFixture(0, 1, 2)
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	// A cached stats-positive result and fixed disk are insufficient after the
	// operator started a different/old core, even on the cheap no-op timer pass.
	c.statsCap[1] = statsProbe{ok: true}
	r.running[1] = "sing-box version 1.14.2\nTags: with_v2ray_api"
	if err := c.rebuildPeriodic(); err == nil {
		t.Fatal("stale positive accepted downgraded process")
	}
	if p.applies != 1 || st.acks[1] != 1 || st.acks[2] != 1 {
		t.Fatal("failed preflight applied new config")
	}
	if st.failures[1] == "" || st.runtimeErrors[1] == "" || st.info[1].HasVisionFramingFix {
		t.Fatal("downgraded running core left positive observation")
	}
	if r.versions[2] != 2 {
		t.Fatal("did not inspect all topology nodes after another node failed")
	}
}

func TestVisionGateRechecksLiveCoreBeforeDownstreamAck(t *testing.T) {
	for _, id := range []int64{0, 1} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			c, st, p, r := visionFixture(id)
			if id == 0 {
				p.afterApply = "sing-box version 1.14.2"
			} else {
				r.afterApply[id] = "sing-box version 1.14.2"
			}
			if err := c.Rebuild(); err == nil {
				t.Fatal("apply selected old systemd core without error")
			}
			if st.acks[id] != 0 {
				t.Fatal("unverified downstream was acknowledged")
			}
			if _, ok := c.desiredHash[id]; ok {
				t.Fatal("unverified config entered desired success cache")
			}
			if st.failures[id] == "" || st.runtimeErrors[id] == "" {
				t.Fatal("post-apply runtime failure missing")
			}
		})
	}
}

func TestVisionGateNoVisionTopologyDoesNotProbeOrBlock(t *testing.T) {
	for _, name := range []string{"plain", "websocket", "p0", "off"} {
		t.Run(name, func(t *testing.T) {
			c, _, p, _ := visionFixture()
			p.disk = "sing-box version 1.13.14"
			p.runningErr = errors.New("inactive")
			if err := c.Rebuild(); err != nil {
				t.Fatal(err)
			}
			if p.versions != 0 || p.inspections != 0 || p.applies != 1 {
				t.Fatalf("unrelated topology affected: %+v", p)
			}
		})
	}
}

func TestVisionRuntimeProofPersistsIndependentlyOfDiskRefresh(t *testing.T) {
	c, st, _, r := visionFixture(1)
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if !st.runtimeInfo[1].HasVisionFramingFix {
		t.Fatal("running capability proof missing")
	}
	r.running[1] = "sing-box version 1.14.2"
	if err := c.RebuildServer(1); err == nil {
		t.Fatal("single-target rebuild bypassed runtime downgrade gate")
	}
	if err := st.SetNodeSingbox(1, sbver.Parse(fixedVisionVersion())); err != nil {
		t.Fatal(err)
	}
	if st.runtimeErrors[1] == "" {
		t.Fatal("disk refresh erased known running-core failure")
	}
	r.running[1] = fixedVisionVersion()
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if st.runtimeErrors[1] != "" {
		t.Fatal("fresh verified core did not recover")
	}
}

func TestVisionRuntimeProofWriteFailureStopsApply(t *testing.T) {
	c, st, p, _ := visionFixture(0)
	st.runtimeWriteErr = errors.New("runtime proof persistence unavailable")
	if err := c.Rebuild(); err == nil || !strings.Contains(err.Error(), st.runtimeWriteErr.Error()) {
		t.Fatalf("write failure=%v", err)
	}
	if p.applies != 0 {
		t.Fatal("apply proceeded without persisted live-core evidence")
	}
}

func TestVisionRemoteInstalledVersionFailurePreservesAffectedConfigs(t *testing.T) {
	for _, disk := range []string{"sing-box version 1.14.2\nTags: with_v2ray_api", "sing-box version unknown", "", "sing-box version " + sbver.VisionFramingFixVersion} {
		c, st, p, r := visionFixture(1, 2)
		r.disk[1] = disk
		if err := c.Rebuild(); err == nil {
			t.Fatalf("accepted remote version %q", disk)
		}
		if p.applies != 1 || len(r.order) != 0 || st.acks[1] != 0 || st.acks[2] != 0 {
			t.Fatal("invalid remote core did not preserve its path while allowing unrelated local config")
		}
		if st.runtimeErrors[1] == "" {
			t.Fatal("remote runtime readiness not invalidated")
		}
	}
}

func TestVisionGateHoldsOnlyDependentComponents(t *testing.T) {
	c, st, p, r := visionFixture(0, 1, 2, 3, 4, 5)
	// 0 -> 1 -> 2 is the failed Vision route; node 0 is a plaintext
	// upstream that still must be held. 3 is independent and uses Vision.
	// 4/5 are an unrelated plaintext/WS component and must not be probed.
	st.dependencies = map[int64][]int64{0: {1, 2}, 1: {1, 2}, 2: {1, 2}, 3: {3}}
	r.running[2] = "sing-box version 1.14.2"
	if err := c.Rebuild(); err == nil {
		t.Fatal("failed Vision component not reported")
	}
	if p.applies != 0 || p.versions != 0 || p.inspections != 0 {
		t.Fatalf("plaintext dependency was applied or unnecessarily version-probed: %+v", p)
	}
	for _, id := range []int64{1, 2} {
		if r.calls[id] != 0 || st.acks[id] != 0 {
			t.Fatalf("failed component server %d changed config", id)
		}
		if r.versions[id] != 1 {
			t.Fatalf("server %d was not freshly probed", id)
		}
	}
	for _, id := range []int64{3, 4, 5} {
		if r.calls[id] != 1 || st.acks[id] != 1 {
			t.Fatalf("unrelated server %d did not apply", id)
		}
	}
	if r.versions[3] != 1 || r.inspections[3] != 2 {
		t.Fatal("independent healthy Vision component not verified")
	}
	for _, id := range []int64{4, 5} {
		if r.versions[id] != 0 || r.inspections[id] != 0 {
			t.Fatalf("unrelated plain/WS server %d probed", id)
		}
	}
}

func TestVisionGateRecoveryRechecksHealthAndClearsFailedStatus(t *testing.T) {
	c, _, p, _ := visionFixture(0)
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	p.running = "sing-box version 1.14.2"
	if err := c.rebuildPeriodic(); err == nil {
		t.Fatal("downgrade accepted")
	}
	if c.syncStatus[0].State != "failed" {
		t.Fatal("gate failure not visible")
	}
	p.running = fixedVisionVersion()
	if err := c.rebuildPeriodic(); err != nil {
		t.Fatal(err)
	}
	if p.applies != 2 {
		t.Fatal("recovery skipped apply/health verification through desired cache")
	}
	if c.syncStatus[0].State != "ok" {
		t.Fatalf("recovery kept failed status: %+v", c.syncStatus[0])
	}
	if c.visionRetry[0] {
		t.Fatal("successful recovery retained retry flag")
	}
}

func TestVisionGateHoldsLocalAliasesOfBlockedPanelConfig(t *testing.T) {
	c, st, p, _ := visionFixture(0)
	p.running = "sing-box version 1.14.2"
	st.servers = append(st.servers, &store.Server{ID: 7, Host: "127.0.0.1", Enabled: true, ConfigPath: p.ConfigPath()})
	if err := c.Rebuild(); err == nil {
		t.Fatal("old core accepted")
	}
	if p.applies != 0 || st.acks[7] != 0 {
		t.Fatal("blocked config applied through local alias")
	}
	if !strings.Contains(c.syncStatus[7].Error, "同一个本机配置文件") {
		t.Fatalf("alias not explicitly held: %+v", c.syncStatus[7])
	}
}

func TestVisionGateHoldsPanelAliasOfBlockedLocalConfig(t *testing.T) {
	c, st, p, _ := visionFixture(7)
	bin := filepath.Join(t.TempDir(), "sing-box-fixture")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'sing-box version 1.14.2\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	st.servers[0].Host = "127.0.0.1"
	st.servers[0].ConfigPath = p.ConfigPath()
	st.servers[0].SingBoxBin = bin
	if err := c.Rebuild(); err == nil {
		t.Fatal("old local core accepted")
	}
	if p.applies != 0 || st.acks[0] != 0 {
		t.Fatal("blocked local-row config overwritten through panel alias")
	}
	if !strings.Contains(c.syncStatus[0].Error, "同一个本机配置文件") {
		t.Fatalf("panel alias not explicitly held: %+v", c.syncStatus[0])
	}
}
