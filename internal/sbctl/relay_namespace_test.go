package sbctl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qingzhou/internal/store"
)

func TestRelayNamespaceVerifiesInstalledHashAndLiveProcess(t *testing.T) {
	c, _, _, _ := snapshotController(t)
	dir := t.TempDir()
	// Only a local test double; no system service or actual host config is used.
	stub := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncase \"$1\" in show) echo 11111111111111111111111111111111;; is-active) exit 0;; *) exit 1;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	raw := []byte(`{"inbounds":[{"type":"mixed","users":[{"username":"relay_42"}]}]}`)
	path := filepath.Join(dir, "config with spaces.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	sv := &store.Server{Host: "127.0.0.1", ConfigPath: path, SystemdUnit: "test-unit"}
	epoch, err := c.verifiedAppliedNamespaceEpoch(context.Background(), sv, raw)
	if err != nil || !strings.HasSuffix(epoch, ":11111111111111111111111111111111") {
		t.Fatalf("proof=%q %v", epoch, err)
	}
	if _, err = c.verifiedAppliedNamespaceEpoch(context.Background(), sv, []byte(`{"other":true}`)); err == nil {
		t.Fatal("desired config hash bypassed installed file verification")
	}
	if err = os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = c.verifiedAppliedNamespaceEpoch(context.Background(), sv, raw); err == nil {
		t.Fatal("unverified live process acquired proof")
	}
	if !configNeedsRelayNamespaceProof(raw) || configNeedsRelayNamespaceProof([]byte(`{"inbounds":[{"type":"mixed","users":[{"username":"relay_customer"}]}]}`)) {
		t.Fatal("preflight did not isolate old numeric customer names")
	}
}
