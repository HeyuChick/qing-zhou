package sbctl

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestRunOnServerHonoursDeadlineWithLingeringChild is the regression test for a
// deadline that did not actually bound anything.
//
// RunOnServer's contract says ctx bounds the whole operation, and callers size
// their deadline against the server's 30s WriteTimeout on that promise. But
// exec.CommandContext only kills the shell; a child the shell spawned inherits
// the output pipe and keeps it open, and CombinedOutput waits for the last
// writer. Observed in the wild via the egress connectivity check: a 25s deadline
// returned at 40s, past WriteTimeout, so the panel showed a torn connection
// instead of the failure the script had already established.
//
// The shape reproduced here is that exact one — a shell that backgrounds a
// long-lived child and exits — and the assertion is on WALL CLOCK, because the
// bug is entirely about how long the caller is held.
func TestRunOnServerHonoursDeadlineWithLingeringChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("no POSIX sh on PATH")
		}
	}
	c := New(nil, nil, nil, "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	// The backgrounded sleep inherits stdout/stderr and outlives the shell, which
	// is what used to keep CombinedOutput blocked long after the kill.
	_, err := c.RunOnServer(ctx, 0, `sleep 30 & echo started; sleep 30`)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("a command killed by its deadline should report an error")
	}
	// Deadline 1s + WaitDelay 2s, with room for process teardown. The old
	// behaviour parked here for the full 30s of the lingering child.
	if elapsed > 10*time.Second {
		t.Fatalf("RunOnServer returned after %v; ctx must bound it even when a child holds the pipe", elapsed)
	}
	t.Logf("returned in %v", elapsed)
}
