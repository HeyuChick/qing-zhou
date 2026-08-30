package sbctl

import (
	"sync"
	"testing"
	"time"

	"qingzhou/internal/sbver"
	"qingzhou/internal/singbox"
	"qingzhou/internal/store"
)

// schedFakeStore is a minimal ConfigStore: no servers, trivial configs, so a
// Rebuild's only observable effect is one Applier.Apply (the local instance).
type schedFakeStore struct{}

func (schedFakeStore) BuildUsersByTag(int64) (map[string][]singbox.User, error) {
	return map[string][]singbox.User{}, nil
}
func (schedFakeStore) BuildSingboxConfig(string, string, map[string][]singbox.User) ([]byte, error) {
	return []byte("{}"), nil
}
func (schedFakeStore) BuildSingboxConfigForServer(int64, string, string, map[string][]singbox.User) ([]byte, error) {
	return []byte("{}"), nil
}
func (schedFakeStore) AddUsageBatchesByServer(map[int64]map[string]store.UsageDelta) (int, error) {
	return 0, nil
}
func (schedFakeStore) ListServers() ([]*store.Server, error)   { return nil, nil }
func (schedFakeStore) GetServer(int64) (*store.Server, error)  { return nil, nil }
func (schedFakeStore) SetNodeSingbox(int64, sbver.Info) error  { return nil }
func (schedFakeStore) SetNodeSingboxError(int64, string) error { return nil }

// slowApplier counts applies and sleeps, so a burst of schedules overlaps the
// first in-flight pass.
type slowApplier struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (a *slowApplier) Apply([]byte) error {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	time.Sleep(a.delay)
	return nil
}
func (a *slowApplier) count() int { a.mu.Lock(); defer a.mu.Unlock(); return a.calls }

func drained(c *Controller) bool {
	c.schedMu.Lock()
	defer c.schedMu.Unlock()
	return !c.schedRunning && !c.pendingAll && len(c.pendingServer) == 0
}

func waitDrained(t *testing.T, c *Controller) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if drained(c) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scheduler did not drain in time")
}

// A burst of full-rebuild schedules that arrive WHILE a pass is in flight must
// collapse into a single queued follow-up — not one pass per call.
func TestScheduleRebuildCoalesces(t *testing.T) {
	ap := &slowApplier{delay: 100 * time.Millisecond}
	c := New(schedFakeStore{}, ap, nil, "{}", "")

	// Kick off pass 1 and wait until it is actually running (inside Apply's sleep).
	c.ScheduleRebuild()
	deadline := time.Now().Add(2 * time.Second)
	for ap.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	// Now fire a burst; all of these land while pass 1 is mid-flight.
	for i := 0; i < 10; i++ {
		c.ScheduleRebuild()
	}
	waitDrained(t, c)

	if got := ap.count(); got != 2 {
		t.Fatalf("expected the burst to coalesce into exactly 2 passes, got %d", got)
	}
	if st := c.status(AllTarget); st.State != "ok" {
		t.Fatalf("expected final sync status ok, got %q (%s)", st.State, st.Error)
	}
}

// A single schedule runs exactly one pass and records success.
func TestScheduleRebuildSingle(t *testing.T) {
	ap := &slowApplier{}
	c := New(schedFakeStore{}, ap, nil, "{}", "")

	c.ScheduleRebuild()
	waitDrained(t, c)

	if got := ap.count(); got != 1 {
		t.Fatalf("expected 1 pass, got %d", got)
	}
	if _, ok := c.SyncStatuses()[AllTarget]; !ok {
		t.Fatal("expected a sync status entry for the full rebuild")
	}
}
