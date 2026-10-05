package sbproc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedProcessFixture(t *testing.T) {
	if os.Getenv("QZ_MANAGED_PROCESS_FIXTURE") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func startManagedFixture(t *testing.T, args ...string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestManagedProcessFixture$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "QZ_MANAGED_PROCESS_FIXTURE=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

func TestManagedProcessRequiresExactSingleConfigArgument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed with ' quote.json")
	raw := []byte(`{"inbounds":[]}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"single", []string{"run", "-c", path}, true},
		{"long_equals", []string{"run", "--config=" + path}, true},
		{"wrong_file", []string{"run", "-c", path + ".old"}, false},
		{"substring_only", []string{"run", "--note=" + path}, false},
		{"merged_files", []string{"run", "-c", path, "-c", path + ".other"}, false},
		{"config_directory", []string{"run", "-c", path, "-C", dir}, false},
		{"checker", []string{"check", "-c", path}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid := startManagedFixture(t, tc.args...)
			stub := filepath.Join(dir, "systemctl")
			script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *'custom.service'*) ;; *) exit 9;; esac\ncase \"$1 $2\" in 'show --property=InvocationID') echo 11111111111111111111111111111111;; 'show --property=MainPID') echo %d;; 'is-active --quiet') exit 0;; *) exit 1;; esac\n", pid)
			if err := os.WriteFile(stub, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			got, err := InspectManagedProcess(context.Background(), "custom.service", path)
			if (err == nil) != tc.valid {
				t.Fatalf("inspection=%+v err=%v want valid=%v", got, err, tc.valid)
			}
			if tc.valid {
				if got.ConfigHash != fmt.Sprintf("%x", sha256.Sum256(raw)) || !strings.HasSuffix(got.Epoch, ":11111111111111111111111111111111") {
					t.Fatalf("wrong evidence %+v", got)
				}
			}
		})
	}
}

func TestApplyForceRecoversFileWrittenBeforeCrash(t *testing.T) {
	reloads := 0
	m, path := newTestManager(t, func() error { reloads++; return nil })
	raw := []byte(`{"inbounds":[]}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	} // prior process died before restarting
	if changed, err := m.ApplyChanged(raw); err != nil || changed {
		t.Fatalf("normal no-op=%v %v", changed, err)
	}
	if changed, err := m.ApplyChangedForce(raw, true); err != nil || !changed || reloads != 1 {
		t.Fatalf("forced boundary=%v %v restarts=%d", changed, err, reloads)
	}
	if changed, err := m.ApplyChangedForce(raw, false); err != nil || changed || reloads != 1 {
		t.Fatal("proven no-op should not restart")
	}
}
