package sbctl

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"qingzhou/internal/singbox"
	"qingzhou/internal/store"
)

// Build is the deterministic barrier: controller preflight has finished, but
// these exact bytes have not yet reached an applier. Production raw inference
// is used for source configs; they need no receiver-registry database lookup.
type coreBarrierStore struct {
	*transportStore
	once           sync.Once
	afterPreflight func()
	raw            map[int64][]byte
}

func (s *coreBarrierStore) build(id int64) ([]byte, error) {
	s.once.Do(func() {
		if s.afterPreflight != nil {
			s.afterPreflight()
		}
	})
	if raw, ok := s.raw[id]; ok {
		return raw, nil
	}
	return []byte(`{}`), nil
}
func (s *coreBarrierStore) BuildSingboxConfig(string, string, map[string][]singbox.User) ([]byte, error) {
	return s.build(0)
}
func (s *coreBarrierStore) BuildSingboxConfigForServer(id int64, _ string, _ string, _ map[string][]singbox.User) ([]byte, error) {
	return s.build(id)
}
func (s *coreBarrierStore) RelayCoreRequirementsForConfig(id int64, raw []byte) (store.RelayCoreRequirements, error) {
	return new(store.Store).RelayCoreRequirementsForConfig(id, raw)
}

func rawP1Source(transport string) []byte {
	return []byte(fmt.Sprintf(`{"inbounds":[{"type":"vless","tag":"source","transport":{"type":%q},"users":[{"name":"client","uuid":"fixture"}]}],"outbounds":[{"type":"vless","tag":"relay-link-1-u1-g1"}],"route":{"rules":[{"inbound":["source"],"auth_user":["client"],"outbound":"relay-link-1-u1-g1"}]}}`, transport))
}

func TestCoreApplyBarrierRejectsRequirementsChangeAndRecovers(t *testing.T) {
	for _, transport := range []string{"ws", "httpupgrade"} {
		t.Run(transport, func(t *testing.T) {
			c, st, p, _ := transportFixture(0)
			st.required = map[int64]store.RelayCoreRequirements{}
			st.coreDeps = map[int64][]int64{}
			barrier := &coreBarrierStore{transportStore: st, raw: map[int64][]byte{0: rawP1Source(transport)}}
			barrier.afterPreflight = func() {
				st.required[0] = store.RelayCoreRequirements{TransportReadBuffer: true}
				st.coreDeps[0] = []int64{0}
			}
			c.st = barrier
			p.disk, p.running = fixedVisionVersion(), fixedVisionVersion()
			if err := c.Rebuild(); err == nil || !strings.Contains(err.Error(), "预检后发生变化") {
				t.Fatalf("changed source escaped snapshot: %v", err)
			}
			if p.applies != 0 || st.acks[0] != 0 {
				t.Fatal("changed source applied/acknowledged")
			}
			p.disk, p.running = fixedTransportVersion(), fixedTransportVersion()
			if err := c.Rebuild(); err != nil {
				t.Fatal("stable repaired pass did not converge:", err)
			}
			if p.applies != 1 || st.acks[0] != 1 {
				t.Fatal("stable pass did not apply/ack")
			}
		})
	}
}

func TestCoreApplyBarrierRawSurvivesABAAndDisabledTopology(t *testing.T) {
	for _, transport := range []string{"ws", "httpupgrade"} {
		c, st, p, _ := transportFixture(0)
		st.required = map[int64]store.RelayCoreRequirements{}
		st.coreDeps = map[int64][]int64{}
		barrier := &coreBarrierStore{transportStore: st, raw: map[int64][]byte{0: rawP1Source(transport)}, afterPreflight: func() {
			st.required[0] = store.RelayCoreRequirements{TransportReadBuffer: true}
			st.coreDeps[0] = []int64{0}
			// Simulate the desired options/feature switches moving back to A.
			delete(st.required, 0)
			delete(st.coreDeps, 0)
		}}
		c.st = barrier
		p.disk, p.running = fixedVisionVersion(), fixedVisionVersion()
		if err := c.Rebuild(); err == nil || !strings.Contains(err.Error(), "WebSocket/HTTPUpgrade") {
			t.Fatalf("ABA/new opt-out bypassed raw requirements: %v", err)
		}
		if p.applies != 0 || st.acks[0] != 0 {
			t.Fatal("raw P1 path applied through disabled/newer desired topology")
		}
	}
}

func TestCoreApplyBarrierRejectsMovedDependencies(t *testing.T) {
	c, st, p, r := transportFixture(0, 1, 2)
	st.required = map[int64]store.RelayCoreRequirements{1: {TransportReadBuffer: true}}
	st.coreDeps = map[int64][]int64{0: {1}, 1: {1}}
	barrier := &coreBarrierStore{transportStore: st, raw: map[int64][]byte{}, afterPreflight: func() {
		delete(st.required, 1)
		st.required[2] = store.RelayCoreRequirements{TransportReadBuffer: true}
		delete(st.coreDeps, 1)
		st.coreDeps[0] = []int64{2}
		st.coreDeps[2] = []int64{2}
	}}
	c.st = barrier
	r.disk[2], r.running[2] = fixedVisionVersion(), fixedVisionVersion()
	if err := c.Rebuild(); err == nil || !strings.Contains(err.Error(), "线路依赖") {
		t.Fatalf("moved unpatched dependency escaped: %v", err)
	}
	if p.applies != 0 || r.calls[1] != 0 || r.calls[2] != 0 {
		t.Fatal("moved component applied using obsolete probe scope")
	}
}

func TestCoreApplyRawProofParallelRemoteAndPostApplyDowngrade(t *testing.T) {
	c, st, _, r := transportFixture(1, 2)
	barrier := &coreBarrierStore{transportStore: st, raw: map[int64][]byte{1: rawP1Source("ws"), 2: rawP1Source("httpupgrade")}}
	c.st = barrier
	for _, id := range []int64{1, 2} {
		r.afterApply[id] = fixedVisionVersion()
	}
	if err := c.Rebuild(); err == nil {
		t.Fatal("running downgrade after apply accepted")
	}
	for _, id := range []int64{1, 2} {
		if r.calls[id] != 1 || st.acks[id] != 0 {
			t.Fatalf("server %d wrong apply/ack %d/%d", id, r.calls[id], st.acks[id])
		}
		if r.versions[id] != 2 || r.inspections[id] != 3 {
			t.Fatalf("server %d missing fresh raw pre/post probes: %d/%d", id, r.versions[id], r.inspections[id])
		}
		if _, ok := c.desiredHash[id]; ok {
			t.Fatal("post-apply downgrade entered success cache")
		}
	}
}
