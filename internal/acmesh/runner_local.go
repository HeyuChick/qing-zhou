package acmesh

import (
	"context"
	"os/exec"
)

// LocalRunner executes commands on the panel's own host via `sh -c`. Used when
// the TLS profile targets the local server (server_id 0). acme.sh's standalone
// HTTP-01 needs :80 free and the process needs permission to write CertDir and
// run the reload command (typically the panel runs as root on a Linux VPS).
type LocalRunner struct{}

// Run executes cmd through /bin/sh and returns combined stdout+stderr.
func (LocalRunner) Run(ctx context.Context, cmd string) (string, error) {
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	return string(out), err
}
