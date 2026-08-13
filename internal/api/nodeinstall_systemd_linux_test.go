//go:build linux

package api

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The local install is the one code path whose whole point is a property of the
// machine it runs on — that the panel's own systemd sandbox makes /usr read-only
// for anything it forks — and no unit test can see that property. This file is
// the check that actually runs it, and it needs a host it is allowed to install
// sing-box on, so it is opt-in:
//
//	GOOS=linux GOARCH=amd64 go test -c ./internal/api -o api.test
//	# on a throwaway Linux box with systemd, as root:
//	systemd-run --pipe --wait --collect -p ProtectSystem=full -p NoNewPrivileges=true \
//	  -E QZ_SYSTEMD_INSTALL_TEST=1 -- ./api.test -test.run TestLocalInstall -test.v
//
// The systemd-run wrapper is how the panel's own service is reproduced: those
// are the two hardening directives deploy/qingzhou.service sets. Run without it
// and the test skips, because /usr would be writable and there would be nothing
// to prove.
const systemdInstallTestEnv = "QZ_SYSTEMD_INSTALL_TEST"

func requireSandbox(t *testing.T) {
	t.Helper()
	if os.Getenv(systemdInstallTestEnv) != "1" {
		t.Skipf("set %s=1 and run under a ProtectSystem=full unit; see the comment in this file", systemdInstallTestEnv)
	}
	if os.Geteuid() != 0 {
		t.Fatalf("must run as root")
	}
	if !dirReadOnly(localSingboxBinDir) {
		t.Fatalf("%s is writable here, so this test proves nothing — run it under a ProtectSystem=full unit", localSingboxBinDir)
	}
}

// TestLocalInstallDirectlyFailsUnderProtectSystem pins the bug itself: a child
// forked by the panel inherits the service's mount namespace and simply cannot
// write the binary, which is why the local 重装 button failed on every host that
// followed the project's own deployment instructions.
func TestLocalInstallDirectlyFailsUnderProtectSystem(t *testing.T) {
	requireSandbox(t)
	argv := localInstallArgv(false, "") // pretend it is writable → the old, direct path
	if argv[0] != "bash" {
		t.Fatalf("argv = %q, want the direct path", argv)
	}
	cmd := exec.Command(argv[0], "-c", "install -m755 /etc/hostname "+localSingboxBinDir+"/sing-box")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install into %s unexpectedly succeeded: %s", localSingboxBinDir, out)
	}
	if !strings.Contains(string(out), "Read-only file system") {
		t.Fatalf("want a read-only filesystem error, got %v: %s", err, out)
	}
	t.Logf("reproduced: %s", strings.TrimSpace(string(out)))
}

// TestLocalInstallEscapesSandbox is the fix: the same write, run through the
// argv the panel now builds, has to succeed — and the plumbing around it has to
// survive the trip through PID1, since the operator sees nothing else. stdin
// must reach the script, its output must come back, and a non-zero exit must
// still be an error rather than a silent success.
func TestLocalInstallEscapesSandbox(t *testing.T) {
	requireSandbox(t)
	systemdRun, err := exec.LookPath("systemd-run")
	if err != nil {
		t.Fatalf("systemd-run is what this test is about: %v", err)
	}
	target := localSingboxBinDir + "/qz-sandbox-probe"
	defer os.Remove(target)

	run := func(script string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		argv := localInstallArgv(true, systemdRun)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// The script arrives on stdin, writes where a forked child could not, and
	// its stdout comes back to us.
	out, err := run("echo marker-in-output; install -m755 /etc/hostname " + target)
	if err != nil {
		t.Fatalf("install through systemd-run failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "marker-in-output") {
		t.Fatalf("--pipe did not return the script's output: %q", out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("%s was not created: %v", target, err)
	}

	// A failing script must fail the call. Without --wait propagating the status
	// the panel would report every broken install as a success.
	out, err = run("echo about-to-fail; exit 7")
	if err == nil {
		t.Fatalf("a script exiting 7 must be an error; output %q", out)
	}
	if !strings.Contains(err.Error(), "7") {
		t.Fatalf("exit status not propagated: %v", err)
	}
	if !strings.Contains(out, "about-to-fail") {
		t.Fatalf("output of a failing script was lost: %q", out)
	}
	if h := localInstallHint(localInstallArgv(true, systemdRun), out); h == "" {
		t.Fatal("a failure on the wrapped path must carry the sandbox hint")
	}
}

// TestLocalInstallEndToEnd runs the real thing: the embedded install script,
// through the real entry point, downloading the real sing-box. It is the only
// check that covers what the operator actually clicks.
//
// Destructive by nature — it installs sing-box and rewrites its unit — so it
// belongs on a throwaway machine, and needs network access to GitHub.
func TestLocalInstallEndToEnd(t *testing.T) {
	requireSandbox(t)
	if os.Getenv("QZ_SYSTEMD_INSTALL_TEST_E2E") != "1" {
		t.Skip("set QZ_SYSTEMD_INSTALL_TEST_E2E=1 to install a real sing-box on this machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := upgradeLocalSingBox(ctx)
	t.Logf("install output:\n%s", out)
	if err != nil {
		t.Fatalf("upgradeLocalSingBox: %v", err)
	}
	if _, err := os.Stat(localSingboxBinDir + "/sing-box"); err != nil {
		t.Fatalf("sing-box was not installed: %v", err)
	}
	// The build that matters is the panel's own: the upstream release has no
	// v2ray_api, and a node without it meters no traffic at all.
	ver, err := exec.CommandContext(ctx, localSingboxBinDir+"/sing-box", "version").CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box version: %v: %s", err, ver)
	}
	if !strings.Contains(string(ver), "with_v2ray_api") {
		t.Fatalf("installed sing-box lacks v2ray_api:\n%s", ver)
	}
	t.Logf("installed: %s", strings.TrimSpace(string(ver)))
}
