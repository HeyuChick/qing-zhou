package sbproc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T, reload func() error) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	m := &Manager{configPath: path, reload: reload}
	m.check = func(string) error { return nil } // no sing-box binary in tests
	return m, path
}

// The config file is swapped BEFORE the reload runs, and the no-op fast path
// compares against that file. So a failed reload used to be unrecoverable: the
// next tick saw the desired bytes already on disk and returned success without
// retrying, leaving sing-box down while Rebuild reported healthy every minute.
// Only an unrelated user/inbound edit could break the loop.
func TestApply_RetriesAfterFailedReload(t *testing.T) {
	var reloads int
	fail := true
	m, path := newTestManager(t, func() error {
		reloads++
		if fail {
			return errors.New("systemctl restart failed")
		}
		return nil
	})

	cfg := []byte(`{"a":1}`)
	if err := m.Apply(cfg); err == nil {
		t.Fatal("Apply should surface the reload failure")
	}
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	// The config did land on disk — that is exactly what used to poison the
	// fast path.
	if b, _ := os.ReadFile(path); string(b) != string(cfg) {
		t.Fatalf("config on disk = %q, want %q", b, cfg)
	}

	// Same bytes again: must retry the reload rather than short-circuit.
	if err := m.Apply(cfg); err == nil {
		t.Fatal("second Apply should still surface the failure")
	}
	if reloads != 2 {
		t.Errorf("reloads = %d after retry, want 2 — the failure was short-circuited away", reloads)
	}

	// Once the reload succeeds, the no-op fast path comes back.
	fail = false
	if err := m.Apply(cfg); err != nil {
		t.Fatalf("third Apply: %v", err)
	}
	if reloads != 3 {
		t.Fatalf("reloads = %d, want 3", reloads)
	}
	if err := m.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if reloads != 3 {
		t.Errorf("reloads = %d — unchanged config should not restart sing-box again", reloads)
	}
}

// The fast path is the reason sing-box isn't restarted every tick; it must stay
// intact on the healthy path.
func TestApply_NoOpOnUnchangedConfig(t *testing.T) {
	var reloads int
	m, _ := newTestManager(t, func() error { reloads++; return nil })

	cfg := []byte(`{"a":1}`)
	for i := 0; i < 3; i++ {
		if err := m.Apply(cfg); err != nil {
			t.Fatal(err)
		}
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1 — identical config restarted sing-box repeatedly", reloads)
	}
}

// A changed config must always reload, and an invalid one must never reach the
// live path.
func TestApply_ChangedConfigReloads_InvalidNeverLands(t *testing.T) {
	var reloads int
	m, path := newTestManager(t, func() error { reloads++; return nil })

	if err := m.Apply([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply([]byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if reloads != 2 {
		t.Errorf("reloads = %d, want 2", reloads)
	}

	m.check = func(string) error { return errors.New("bad config") }
	if err := m.Apply([]byte(`{"a":3}`)); err == nil {
		t.Fatal("invalid config should be rejected")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"a":2}` {
		t.Errorf("live config = %q, want the last good one — an invalid config was installed", b)
	}
	if reloads != 2 {
		t.Errorf("reloads = %d — a rejected config must not reload", reloads)
	}
}
