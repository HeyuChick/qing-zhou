package sbctl

import (
	"errors"
	"sync"
	"testing"
	"time"

	"qingzhou/internal/store"
)

// countingApplier records applies and reports each one as a reload, standing in
// for the real manager's ApplyChanged.
type countingApplier struct {
	mu     sync.Mutex
	calls  int
	reload bool
	err    error
}

func (a *countingApplier) Apply([]byte) error {
	_, err := a.ApplyChanged(nil)
	return err
}

func (a *countingApplier) ApplyChanged([]byte) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.reload, a.err
}

// TestRestartObserverOnlyReportsUnrequestedRestarts is what keeps the
// restart-loop alert from crying wolf.
//
// A node restarting right after an admin saved an inbound is the system doing
// its job; a node restarting on the timer with nobody touching it is the
// failure this alert exists for. Only the second is reported, so anything the
// watcher receives is unexplained by construction and the threshold can stay
// low enough to be useful.
func TestRestartObserverOnlyReportsUnrequestedRestarts(t *testing.T) {
	applier := &countingApplier{reload: true}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")

	var mu sync.Mutex
	var seen []int64
	c.SetRestartObserver(func(serverID int64, name string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, serverID)
	})

	// An admin's own rebuild: restarts here were asked for.
	if err := c.Rebuild(); err != nil {
		t.Fatalf("manual rebuild: %v", err)
	}
	mu.Lock()
	if len(seen) != 0 {
		t.Fatalf("admin-triggered restart was reported as unexplained: %v", seen)
	}
	mu.Unlock()

	// The timer's pass: nobody asked for this one.
	if err := c.reconcilePeriodic(); err != nil {
		t.Fatalf("periodic rebuild: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != store.LocalNodeID {
		t.Fatalf("periodic restart not reported once for the local node: %v", seen)
	}
}

// TestRestartObserverSilentWhenNothingRestarted guards the other half: a pass
// that changed nothing must produce no events at all, or the watcher would see
// a steady stream on a perfectly healthy panel.
func TestRestartObserverSilentWhenNothingRestarted(t *testing.T) {
	applier := &countingApplier{reload: false}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")

	fired := 0
	c.SetRestartObserver(func(int64, string) { fired++ })
	for i := 0; i < 3; i++ {
		if err := c.reconcilePeriodic(); err != nil {
			t.Fatalf("periodic rebuild: %v", err)
		}
	}
	if fired != 0 {
		t.Fatalf("a no-op pass reported %d restart(s)", fired)
	}
}

func TestMinutePassSkipsUnchangedDesiredConfig(t *testing.T) {
	applier := &countingApplier{reload: false}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")

	if err := c.reconcilePeriodic(); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	applier.mu.Lock()
	want := applier.calls
	applier.mu.Unlock()
	for i := 0; i < 5; i++ {
		if err := c.rebuildPeriodic(); err != nil {
			t.Fatalf("minute pass %d: %v", i+1, err)
		}
	}
	applier.mu.Lock()
	defer applier.mu.Unlock()
	if applier.calls != want {
		t.Fatalf("unchanged desired config reached applier: calls=%d want=%d", applier.calls, want)
	}
}

func TestCachedMinutePassPreservesFailedHealthStatus(t *testing.T) {
	applier := &countingApplier{reload: false}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")

	if err := c.reconcilePeriodic(); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	applier.mu.Lock()
	applier.err = errors.New("health verification unavailable")
	applier.mu.Unlock()
	if err := c.reconcilePeriodic(); err == nil {
		t.Fatal("failed health reconciliation was reported as success")
	}
	if got := c.SyncStatuses()[store.LocalNodeID]; got.State != "failed" {
		t.Fatalf("health failure status = %#v", got)
	}

	if err := c.rebuildPeriodic(); err != nil {
		t.Fatalf("cached minute pass: %v", err)
	}
	if got := c.SyncStatuses()[store.LocalNodeID]; got.State != "failed" {
		t.Fatalf("cached skip hid previous health failure: %#v", got)
	}

	applier.mu.Lock()
	applier.err = nil
	applier.mu.Unlock()
	if err := c.reconcilePeriodic(); err != nil {
		t.Fatalf("recovery reconcile: %v", err)
	}
	if got := c.SyncStatuses()[store.LocalNodeID]; got.State != "ok" {
		t.Fatalf("successful health check did not recover status: %#v", got)
	}
}

func TestRestartCircuitStopsPeriodicAppliesAndManualApplyRecovers(t *testing.T) {
	applier := &countingApplier{reload: true}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")
	var events []RestartCircuitEvent
	c.SetRestartCircuit(
		func() RestartCircuitPolicy {
			return RestartCircuitPolicy{Enabled: true, Window: 10 * time.Minute, Threshold: 3}
		},
		nil,
		func(ev RestartCircuitEvent) { events = append(events, ev) },
	)

	for i := 0; i < 3; i++ {
		if err := c.reconcilePeriodic(); i < 2 && err != nil {
			t.Fatalf("periodic reconcile %d: %v", i+1, err)
		} else if i == 2 && err == nil {
			t.Fatal("threshold reconcile did not report the opened circuit")
		}
	}
	if len(events) != 1 || !events[0].Open || events[0].Count != 3 {
		t.Fatalf("open events = %#v, want one threshold event", events)
	}

	applier.mu.Lock()
	before := applier.calls
	applier.mu.Unlock()
	if err := c.reconcilePeriodic(); err == nil {
		t.Fatal("open circuit allowed another periodic apply")
	}
	applier.mu.Lock()
	if applier.calls != before {
		t.Fatalf("open circuit called applier: calls=%d before=%d", applier.calls, before)
	}
	applier.mu.Unlock()

	// An ordinary full rebuild may be triggered by a user entitlement change;
	// it must not silently reset the latch.
	if err := c.Rebuild(); err != nil {
		t.Fatalf("ordinary rebuild through circuit: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ordinary rebuild unexpectedly closed circuit: %#v", events)
	}
	if err := c.RebuildServer(0); err != nil {
		t.Fatalf("admin server rebuild through circuit: %v", err)
	}
	if len(events) != 2 || events[1].Open {
		t.Fatalf("recovery events = %#v, want one close event", events)
	}
	if err := c.reconcilePeriodic(); err != nil {
		t.Fatalf("periodic reconcile after recovery: %v", err)
	}
}

func TestPersistedCircuitBlocksAfterPanelRestart(t *testing.T) {
	applier := &countingApplier{reload: false}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")
	open := true
	var events []RestartCircuitEvent
	c.SetRestartCircuit(
		func() RestartCircuitPolicy {
			return RestartCircuitPolicy{Enabled: true, Window: 30 * time.Minute, Threshold: 5}
		},
		func(int64) bool { return open },
		func(ev RestartCircuitEvent) { events = append(events, ev) },
	)

	if err := c.reconcilePeriodic(); err == nil {
		t.Fatal("persisted circuit did not block startup reconciliation")
	}
	applier.mu.Lock()
	if applier.calls != 0 {
		t.Fatalf("persisted circuit touched the node %d time(s)", applier.calls)
	}
	applier.mu.Unlock()

	// The administrator-forced path is allowed through. Its successful health
	// check emits recovery; the API resolves the persisted latch asynchronously.
	if err := c.RebuildServer(0); err != nil {
		t.Fatalf("admin server rebuild through persisted circuit: %v", err)
	}
	if len(events) != 1 || events[0].Open {
		t.Fatalf("persisted recovery events = %#v", events)
	}
	open = false // stand in for the API resolving its DB alert
	if err := c.reconcilePeriodic(); err != nil {
		t.Fatalf("reconcile after persisted recovery: %v", err)
	}
}

func TestFailedRestartAttemptsAlsoTripCircuit(t *testing.T) {
	applier := &countingApplier{reload: true, err: errors.New("systemctl restart failed")}
	c := New(schedFakeStore{}, applier, nil, "{}", "127.0.0.1:18080")
	var events []RestartCircuitEvent
	c.SetRestartCircuit(
		func() RestartCircuitPolicy {
			return RestartCircuitPolicy{Enabled: true, Window: 10 * time.Minute, Threshold: 2}
		},
		nil,
		func(ev RestartCircuitEvent) { events = append(events, ev) },
	)

	if err := c.reconcilePeriodic(); err == nil {
		t.Fatal("first failed restart was reported as success")
	}
	if err := c.reconcilePeriodic(); err == nil {
		t.Fatal("threshold failed restart was reported as success")
	}
	if len(events) != 1 || !events[0].Open || events[0].Count != 2 {
		t.Fatalf("failed restart circuit events = %#v", events)
	}
	applier.mu.Lock()
	before := applier.calls
	applier.mu.Unlock()
	if err := c.reconcilePeriodic(); err == nil {
		t.Fatal("open circuit did not report a blocked pass")
	}
	applier.mu.Lock()
	defer applier.mu.Unlock()
	if applier.calls != before {
		t.Fatalf("open circuit retried a failed restart: calls=%d before=%d", applier.calls, before)
	}
}
