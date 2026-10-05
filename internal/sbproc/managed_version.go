package sbproc

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ManagedVersionScript identifies the actual live systemd executable, rather
// than the binary currently installed at its old pathname. A replaced file can
// have a fixed version while /proc/MainPID/exe still points at the old inode.
// Keep the same unit, invocation and exact managed config checks as namespace
// inspection, and reject a restart racing the version command.
func ManagedVersionScript(unit, path string) (string, error) {
	script, err := ManagedProcessScript(unit, path)
	if err != nil {
		return "", err
	}
	return script + `
version=$("/proc/$pid1/exe" version)
pid3=$(systemctl show --property=MainPID --value -- ` + processQuote(unit) + `)
inv3=$(systemctl show --property=InvocationID --value -- ` + processQuote(unit) + `)
systemctl is-active --quiet -- ` + processQuote(unit) + `
test "$pid1" = "$pid3"; test "$inv1" = "$inv3"
printf '\n%s' "$version"`, nil
}

func ParseManagedVersion(out string) (string, error) {
	parts := strings.SplitN(out, "\n", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("cannot verify running sing-box version")
	}
	if _, err := ParseManagedProcess(parts[0]); err != nil {
		return "", err
	}
	if strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("running sing-box returned an empty version")
	}
	return parts[1], nil
}

func InspectManagedVersion(ctx context.Context, unit, path string) (string, error) {
	script, err := ManagedVersionScript(unit, path)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cannot verify running sing-box version: %w", err)
	}
	return ParseManagedVersion(string(out))
}
