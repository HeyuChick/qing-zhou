package sbproc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The isolated copied test executable is the core for this fixture. Its
// version is tied to a fixture trailer on that inode, including after its
// installed path is replaced. No actual daemon, production config or systemctl is contacted.
func init() {
	if os.Getenv("QZ_MANAGED_VERSION_FIXTURE") == "1" && len(os.Args) == 2 && os.Args[1] == "version" {
		f, err := os.Open("/proc/self/exe")
		if err != nil {
			os.Exit(2)
		}
		defer f.Close()
		if _, err = f.Seek(-int64(len("QZ_TEST_VERSION_OLD\n")), io.SeekEnd); err != nil {
			os.Exit(2)
		}
		tail, err := io.ReadAll(f)
		if err != nil {
			os.Exit(2)
		}
		if string(tail) == "QZ_TEST_VERSION_OLD\n" {
			fmt.Println("sing-box version 1.14.2")
		} else {
			fmt.Println("sing-box version 1.14.2+qz-vmess.9b95ab8c9478\nTags: with_v2ray_api")
		}
		os.Exit(0)
	}
}

func copyVersionFixture(t *testing.T, path string, old bool) {
	t.Helper()
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	trailer := "QZ_TEST_VERSION_NEW\n"
	if old {
		trailer = "QZ_TEST_VERSION_OLD\n"
	}
	if _, err = dst.WriteString(trailer); err != nil {
		t.Fatal(err)
	}
	if err = dst.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedVersionUsesLiveExecutableInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old-core")
	replacement := filepath.Join(dir, "fixed-core")
	copyVersionFixture(t, old, true)
	copyVersionFixture(t, replacement, false)
	t.Setenv("QZ_MANAGED_VERSION_FIXTURE", "1")
	cmd := exec.Command(old, "-test.run=^TestManagedProcessFixture$", "--", "run", "-c", path)
	cmd.Env = append(os.Environ(), "QZ_MANAGED_PROCESS_FIXTURE=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *'fixture.service'*) ;; *) exit 9;; esac\ncase \"$1 $2\" in 'show --property=InvocationID') echo 11111111111111111111111111111111;; 'show --property=MainPID') echo %d;; 'is-active --quiet') exit 0;; *) exit 1;; esac\n", cmd.Process.Pid)
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	// Replace its installed pathname while the original executable still runs.
	if err := os.Rename(replacement, old); err != nil {
		t.Fatal(err)
	}
	installed, err := exec.Command(old, "version").Output()
	if err != nil || !strings.Contains(string(installed), "qz-vmess.9b95ab8c9478") {
		t.Fatalf("replacement fixture is not fixed: %q %v", installed, err)
	}
	out, err := InspectManagedVersion(context.Background(), "fixture.service", path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "sing-box version 1.14.2" {
		t.Fatalf("accepted replacement instead of old running inode: %q", out)
	}
}

func TestManagedVersionRejectsMissingEvidence(t *testing.T) {
	for _, out := range []string{"", "sing-box version 1.14.2+qz-vmess.9b95ab8c9478", "bad header\nsing-box version 1.14.2+qz-vmess.9b95ab8c9478"} {
		if _, err := ParseManagedVersion(out); err == nil {
			t.Fatalf("accepted %q", out)
		}
	}
}

func TestManagedVersionRejectsRestartDuringInspection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QZ_MANAGED_VERSION_FIXTURE", "1")
	pid := startManagedFixture(t, "run", "-c", path)
	count := filepath.Join(dir, "invocation-count")
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
 'show --property=InvocationID')
  n=0; if [ -f %s ]; then n=$(cat %s); fi
  n=$((n+1)); echo "$n" > %s
  if [ "$n" -ge 3 ]; then echo 22222222222222222222222222222222; else echo 11111111111111111111111111111111; fi;;
 'show --property=MainPID') echo %d;;
 'is-active --quiet') exit 0;;
 *) exit 1;;
esac
`, processQuote(count), processQuote(count), processQuote(count), pid)
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	if out, err := InspectManagedVersion(context.Background(), "fixture.service", path); err == nil {
		t.Fatalf("accepted changing invocation: %q", out)
	}
}
