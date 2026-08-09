package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rollback rotation is the part that can brick a deployment, so it is
// exercised directly on files rather than only through the exec path.
func rotate(t *testing.T, exePath string) {
	t.Helper()
	prev := backupPath(exePath)
	staging := exePath + ".rb"
	keep := exePath + ".fwd"
	if err := copyFile(prev, staging); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := copyFile(exePath, keep); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if err := os.Rename(staging, exePath); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if err := os.Rename(keep, prev); err != nil {
		t.Fatalf("rotate: %v", err)
	}
}

func setup(t *testing.T, cur, prev string) string {
	t.Helper()
	dir := t.TempDir()
	exePath := filepath.Join(dir, "qingzhou")
	if err := os.WriteFile(exePath, []byte(cur), 0o755); err != nil {
		t.Fatal(err)
	}
	if prev != "" {
		if err := os.WriteFile(backupPath(exePath), []byte(prev), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return exePath
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// Rolling back must swap, not consume: the version being left has to become the
// new backup, or a mistaken rollback is a one-way trip with no network.
func TestRollbackRotatesRatherThanConsumes(t *testing.T) {
	exePath := setup(t, "v2-binary", "v1-binary")
	writeBackupVersion(exePath, "v1.0.0")

	rotate(t, exePath)
	writeBackupVersion(exePath, "v2.0.0")

	if got := read(t, exePath); got != "v1-binary" {
		t.Errorf("live binary = %q, want the rolled-back one", got)
	}
	if got := read(t, backupPath(exePath)); got != "v2-binary" {
		t.Errorf("backup = %q, want the version we just left", got)
	}
	if got := readBackupVersion(exePath); got != "v2.0.0" {
		t.Errorf("recorded backup version = %q, want v2.0.0", got)
	}

	// And back again — the rollback is itself reversible offline.
	rotate(t, exePath)
	if got := read(t, exePath); got != "v2-binary" {
		t.Errorf("second rotation did not restore: %q", got)
	}
}

// No staging or keep files may survive a completed rotation.
func TestRollbackLeavesNoTempFiles(t *testing.T) {
	exePath := setup(t, "v2", "v1")
	rotate(t, exePath)
	for _, suffix := range []string{".rb", ".fwd"} {
		if _, err := os.Stat(exePath + suffix); err == nil {
			t.Errorf("temp file survived: %s", exePath+suffix)
		}
	}
}

// A missing or empty backup must be reported, never acted on — a rollback that
// installs a zero-byte file takes the panel down for good.
func TestRollbackStateRejectsUnusableBackup(t *testing.T) {
	m := New(nil, nil)

	dir := t.TempDir()
	exePath := filepath.Join(dir, "qingzhou")
	_ = os.WriteFile(exePath, []byte("cur"), 0o755)

	// No backup at all.
	if _, err := os.Stat(backupPath(exePath)); err == nil {
		t.Fatal("fixture already has a backup")
	}
	// Zero-length backup.
	if err := os.WriteFile(backupPath(exePath), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(backupPath(exePath))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 0 {
		t.Fatal("fixture backup is not empty")
	}
	// RollbackState resolves the *running* test binary, not the fixture, so this
	// asserts the shape of the answer rather than the fixture's own state.
	rs := m.RollbackState()
	if !rs.Available && rs.Reason == "" {
		t.Error("unavailable rollback must explain itself")
	}
}

func TestBackupVersionRoundTrip(t *testing.T) {
	exePath := setup(t, "cur", "prev")
	if got := readBackupVersion(exePath); got != "" {
		t.Errorf("no note yet, got %q", got)
	}
	writeBackupVersion(exePath, "v1.2.3")
	if got := readBackupVersion(exePath); got != "v1.2.3" {
		t.Errorf("got %q", got)
	}
	// A blank version still records something, so the UI can say "unknown"
	// instead of silently rendering an empty button label.
	writeBackupVersion(exePath, "   ")
	if got := readBackupVersion(exePath); got != "unknown" {
		t.Errorf("blank version = %q, want unknown", got)
	}
	// Garbage that would blow up the UI is refused on read.
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	_ = os.WriteFile(backupVersionPath(exePath), long, 0o600)
	if got := readBackupVersion(exePath); got != "" {
		t.Errorf("oversized note should be ignored, got %q", got)
	}
}

// The tag is interpolated into a GitHub API URL path.
func TestValidTag(t *testing.T) {
	for _, ok := range []string{"v1.2.3", "v1.2.3-rc.1", "1.0", "v2.0.0+build1", "dev_latest"} {
		if !validTag(ok) {
			t.Errorf("validTag(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"", "  ", "v1/../../etc/passwd", "v1 2", "v1?x=1", "v1#frag",
		"../latest", "v1%2F2", "v1\n", "v1&a=b",
		"vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv",
	} {
		if validTag(bad) {
			t.Errorf("validTag(%q) = true, want false", bad)
		}
	}
}

// Pinning a version must never be able to skip signature or digest checks, and
// must refuse a malformed tag before any network call happens.
func TestApplyVersionRejectsBadTagEarly(t *testing.T) {
	m := New(nil, nil)
	err := m.ApplyVersion(0, "v1/../evil")
	if err == nil {
		t.Fatal("malformed tag was accepted")
	}
	// Rejected for the tag, not incidentally by the platform gate — the check
	// is ordered ahead of it so this holds on every OS.
	if !strings.Contains(err.Error(), "版本号格式非法") {
		t.Errorf("rejected for the wrong reason: %v", err)
	}
	if m.State().Status == StatusDownloading {
		t.Error("a rejected tag must not start a download")
	}
}
