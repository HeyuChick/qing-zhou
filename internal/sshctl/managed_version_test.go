package sshctl

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestInspectManagedVersionUsesElevatedLiveExecutable(t *testing.T) {
	for _, sudo := range []bool{false, true} {
		t.Run(fmt.Sprint(sudo), func(t *testing.T) {
			var command string
			version := "sing-box version 1.14.2+qz-vmess.9b95ab8c9478\nTags: with_v2ray_api"
			host, port := startApplyTestSSHServer(t, func(cmd string) (string, uint32) {
				command = cmd
				return "11111111-1111-1111-1111-111111111111:11111111111111111111111111111111 " + strings.Repeat("a", 64) + "\n" + version, 0
			})
			m := New(WithTimeout(2 * time.Second))
			cfg := &ServerConfig{ID: 9, Host: host, Port: port, SSHUser: "fixture", SSHPassword: "fixture", UseSudo: sudo, ConfigPath: "/fixture/custom.json", SystemdUnit: "fixture.service"}
			got, err := m.InspectManagedVersion(context.Background(), cfg)
			if err != nil || got != version {
				t.Fatalf("version=%q err=%v", got, err)
			}
			prefix := "sh -c "
			if sudo {
				prefix = "sudo -n -- sh -c "
			}
			for _, want := range []string{"/proc/$pid1/exe", "fixture.service", "/fixture/custom.json", "--property=MainPID", "--property=InvocationID"} {
				if !strings.Contains(command, want) {
					t.Errorf("missing %q in %s", want, command)
				}
			}
			if !strings.HasPrefix(command, prefix) {
				t.Fatalf("wrong privilege path %s", command)
			}
		})
	}
}
