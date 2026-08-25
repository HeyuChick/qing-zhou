package sbctl

import (
	"sync"
	"testing"

	"qingzhou/internal/store"
)

// countingApplier records applies and reports each one as a reload, standing in
// for the real manager's ApplyChanged.
type countingApplier struct {
	mu     sync.Mutex
	calls  int
	reload bool
}

func (a *countingApplier) Apply([]byte) error {
	_, err := a.ApplyChanged(nil)
	return err
}

func (a *countingApplier) ApplyChanged([]byte) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return a.reload, nil
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
	if err := c.rebuildPeriodic(); err != nil {
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
		if err := c.rebuildPeriodic(); err != nil {
			t.Fatalf("periodic rebuild: %v", err)
		}
	}
	if fired != 0 {
		t.Fatalf("a no-op pass reported %d restart(s)", fired)
	}
}
