package sshctl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestInspectManagedProcessElevatesWholeScript(t *testing.T) {
	for _, sudo := range []bool{false, true} {
		t.Run(fmt.Sprintf("sudo_%v", sudo), func(t *testing.T) {
			raw := []byte(`{"inbounds":[]}`)
			wantHash := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte{}, raw...), '\n')))
			var command string
			host, port := startApplyTestSSHServer(t, func(cmd string) (string, uint32) {
				command = cmd
				return "11111111-1111-1111-1111-111111111111:11111111111111111111111111111111 " + wantHash, 0
			})
			m := New(WithTimeout(2 * time.Second))
			cfg := &ServerConfig{ID: 9, Host: host, Port: port, SSHUser: "fixture", SSHPassword: "fixture", UseSudo: sudo, ConfigPath: "/etc/sing-box/custom.json", SystemdUnit: "custom-unit.service"}
			p, err := m.InspectManagedProcess(context.Background(), cfg)
			if err != nil || p.ConfigHash != wantHash {
				t.Fatalf("process=%+v err=%v", p, err)
			}
			prefix := "sh -c "
			if sudo {
				prefix = "sudo -n -- sh -c "
			}
			if !strings.HasPrefix(command, prefix) || !strings.Contains(command, "--property=MainPID") || !strings.Contains(command, "custom-unit.service") || !strings.Contains(command, "/etc/sing-box/custom.json") {
				t.Fatalf("unprivileged or wrong-path verification %s", command)
			}
			if InstalledConfigHash(raw) != wantHash {
				t.Fatal("remote newline hash mismatch")
			}
		})
	}
}

func TestApplyConfigForceRestartsIdenticalActiveConfig(t *testing.T) {
	raw := []byte(`{"inbounds":[]}`)
	restarts := 0
	host, port := startApplyTestSSHServer(t, func(cmd string) (string, uint32) {
		switch {
		case strings.Contains(cmd, "sha256sum") && strings.Contains(cmd, "systemctl is-active"):
			return InstalledConfigHash(raw) + "\nactive\n", 0
		case strings.HasPrefix(cmd, "for c in "):
			return "/usr/local/bin/sing-box\n", 0
		case strings.Contains(cmd, "systemctl restart"):
			restarts++
			return "", 0
		default:
			return "", 0
		}
	})
	m := New(WithTimeout(2 * time.Second))
	cfg := &ServerConfig{ID: 9, Host: host, Port: port, SSHUser: "fixture", SSHPassword: "fixture", ConfigPath: "/etc/sing-box/custom.json", SystemdUnit: "custom-unit.service"}
	if changed, err := m.ApplyConfigForce(context.Background(), cfg, raw, false); err != nil || changed || restarts != 0 {
		t.Fatalf("no-op=%v %v %d", changed, err, restarts)
	}
	if changed, err := m.ApplyConfigForce(context.Background(), cfg, raw, true); err != nil || !changed || restarts != 1 {
		t.Fatalf("forced=%v %v %d", changed, err, restarts)
	}
}
