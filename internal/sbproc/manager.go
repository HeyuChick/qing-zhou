// Package sbproc manages the external sing-box process (B2 integration model):
// it writes the generated config, validates it with `sing-box check`, and only
// then atomically swaps the live config and reloads sing-box. An invalid config
// is NEVER written to the live path or reloaded — a generation bug must not take
// every user offline.
package sbproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager owns the live sing-box config file and the reload mechanism.
type Manager struct {
	bin        string // sing-box binary path (for the default checker)
	configPath string // live config.json path
	check      func(path string) error
	reload     func() error
	mu         sync.Mutex

	// reloadFailed records that the last reload attempt errored. The config file
	// is swapped BEFORE the reload runs, so without this the no-op fast path
	// (which compares against the file on disk) would see the desired bytes
	// already in place on the next tick and return success forever, leaving
	// sing-box down until something happened to change the config. Recovery
	// otherwise required an unrelated user/inbound edit.
	reloadFailed bool
}

// New builds a Manager. reload is how sing-box is (re)started after a config
// swap — e.g. `systemctl restart sing-box`. If reload is nil, Apply only writes
// the validated config (useful for dry runs / tests).
func New(bin, configPath string, reload func() error) *Manager {
	m := &Manager{bin: bin, configPath: configPath, reload: reload}
	m.check = m.defaultCheck
	return m
}

// defaultCheck runs `sing-box check -c <path>` and returns the tool's output on
// failure so the operator sees exactly what's wrong.
func (m *Manager) defaultCheck(path string) error {
	if m.bin == "" {
		return fmt.Errorf("sing-box binary path not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.bin, "check", "-c", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %v: %s", err, out)
	}
	return nil
}

// Validate writes config to a temp file and runs the checker against it without
// touching the live config.
func (m *Manager) Validate(config []byte) error {
	tmp, err := os.CreateTemp("", "qz-sbcheck-*.json")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(config); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return m.check(path)
}

// Apply validates the config, then (only on success) atomically replaces the
// live config file and reloads sing-box. Serialized so concurrent applies can't
// interleave a half-written file with a reload.
func (m *Manager) Apply(config []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// No-op when the config is byte-identical to what's already live. The
	// controller calls Apply on a timer; without this it would `sing-box check`
	// and restart sing-box every tick, dropping every user's connection each
	// minute. Generated config is deterministic so equal input → equal bytes.
	//
	// Skipped while a reload is outstanding: matching bytes on disk only means
	// the file was swapped, not that sing-box picked it up.
	if !m.reloadFailed {
		if cur, err := os.ReadFile(m.configPath); err == nil && bytes.Equal(cur, config) {
			return nil
		}
	}

	if err := m.Validate(config); err != nil {
		return err // invalid: live config untouched, no reload
	}

	// Atomic swap: write to a sibling temp file then rename over the live path.
	dir := filepath.Dir(m.configPath)
	tmp, err := os.CreateTemp(dir, ".qz-sbcfg-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(config); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, m.configPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if m.reload != nil {
		if err := m.reload(); err != nil {
			// Leave the flag set so the next Apply retries the reload instead of
			// short-circuiting on the already-swapped file.
			m.reloadFailed = true
			return err
		}
	}
	m.reloadFailed = false
	return nil
}

// FindSingBoxBin auto-detects the sing-box binary path. It checks the
// QZ_SINGBOX_BIN env var first, then probes common installation paths, and
// finally falls back to `exec.LookPath("sing-box")`. Returns "" if not found.
func FindSingBoxBin() string {
	if v := os.Getenv("QZ_SINGBOX_BIN"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	for _, p := range []string{
		"/opt/qingzhou/sing-box",
		"/usr/local/bin/sing-box",
		"/usr/bin/sing-box",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	return ""
}

// SystemdReload returns a reload func that restarts a systemd unit.
func SystemdReload(unit string) func() error {
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "systemctl", "restart", unit).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl restart %s: %v: %s", unit, err, out)
		}
		return nil
	}
}

// Version runs `sing-box version` on the local binary.
//
// The panel manages a sing-box on its own machine as well as on the remote
// nodes, but only the remote ones get probed over SSH — so without this the one
// node an operator is most likely to have installed by hand is the one the
// panel can say the least about.
//
// Returns the raw output; interpreting it is sbver's job.
func (m *Manager) Version(ctx context.Context) (string, error) {
	if m.bin == "" {
		return "", errors.New("未配置 sing-box 可执行文件路径")
	}
	out, err := exec.CommandContext(ctx, m.bin, "version").CombinedOutput()
	if err != nil {
		// The output carries the real reason (missing file, permission denied),
		// which is far more useful than "exit status 1".
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
