package acmesh

import (
	"context"
	"os"
	"os/exec"
)

// LocalRunner executes acme.sh on the panel host itself.
//
// HTTP-01 needs :80 free and the process needs permission to write CertDir and
// run the reload command (typically the panel runs as root on a Linux VPS).
type LocalRunner struct{}

// Run executes cmd through /bin/sh and returns combined stdout+stderr.
func (LocalRunner) Run(ctx context.Context, cmd string) (string, error) {
	return LocalRunner{}.RunEnv(ctx, cmd, nil)
}

// RunEnv is Run with extra environment variables for the child process.
//
// Secrets belong here rather than in cmd: everything in cmd becomes an argv
// element of the `sh` process and is world-readable from /proc/<pid>/cmdline for
// as long as it runs. That window is not short — the Cloudflare DNS method waits
// on record propagation, and the caller allows four minutes for it.
func (LocalRunner) RunEnv(ctx context.Context, cmd string, env map[string]string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	if len(env) > 0 {
		c.Env = os.Environ()
		for k, v := range env {
			c.Env = append(c.Env, k+"="+v)
		}
	}
	out, err := c.CombinedOutput()
	return string(out), err
}
