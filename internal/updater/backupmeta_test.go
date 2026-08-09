package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBinary writes something that passes the shallow checks: an ELF header
// followed by enough padding to clear the size floor.
func fakeBinary(t *testing.T, path string, marker byte) {
	t.Helper()
	buf := make([]byte, minBackupBytes+16)
	copy(buf, elfMagic)
	for i := len(elfMagic); i < len(buf); i++ {
		buf[i] = marker
	}
	if err := os.WriteFile(path, buf, 0o755); err != nil {
		t.Fatal(err)
	}
}

func exeWithBackup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exePath := filepath.Join(dir, "qingzhou")
	fakeBinary(t, exePath, 'C')
	fakeBinary(t, backupPath(exePath), 'P')
	return exePath
}

// The whole point of the checks: a damaged backup must be refused, because
// installing one on a panel-only deployment is unrecoverable.
func TestBackupChecksRejectDamagedBinaries(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, prev string)
		want  string
	}{
		{"missing", func(t *testing.T, prev string) {}, "没有保留"},
		{"empty", func(t *testing.T, prev string) {
			if err := os.WriteFile(prev, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "不完整"},
		{"truncated mid-copy", func(t *testing.T, prev string) {
			b := make([]byte, 4096)
			copy(b, elfMagic)
			if err := os.WriteFile(prev, b, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "不完整"},
		{"not an executable", func(t *testing.T, prev string) {
			b := make([]byte, minBackupBytes+16)
			copy(b, []byte("<!doctype html>"))
			if err := os.WriteFile(prev, b, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "不是可执行文件"},
		{"a directory", func(t *testing.T, prev string) {
			if err := os.Mkdir(prev, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "不是普通文件"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			exePath := filepath.Join(dir, "qingzhou")
			fakeBinary(t, exePath, 'C')
			c.write(t, backupPath(exePath))

			err := checkBackupShallow(exePath)
			if err == nil {
				t.Fatal("damaged backup was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("reason = %q, want it to mention %q", err, c.want)
			}
			// And the thorough check must not somehow let it through either.
			if verifyBackupContent(exePath) == nil {
				t.Error("verifyBackupContent accepted a damaged backup")
			}
		})
	}
}

func TestBackupMetaRoundTripAndVerify(t *testing.T) {
	exePath := exeWithBackup(t)
	writeBackupMeta(exePath, "v0.2.30")

	m := readBackupMeta(exePath)
	if m == nil || m.Version != "v0.2.30" || m.SHA256 == "" || m.Size == 0 {
		t.Fatalf("meta = %+v", m)
	}
	if got := backupVersion(exePath); got != "v0.2.30" {
		t.Errorf("backupVersion = %q", got)
	}
	if err := verifyBackupContent(exePath); err != nil {
		t.Errorf("intact backup rejected: %v", err)
	}
}

// Corruption that happens *after* the backup was taken is exactly what the
// digest is for — the size and ELF header can both still look fine.
func TestVerifyCatchesPostBackupCorruption(t *testing.T) {
	exePath := exeWithBackup(t)
	writeBackupMeta(exePath, "v0.2.30")

	prev := backupPath(exePath)
	b, err := os.ReadFile(prev)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0xff // same length, same header, one flipped byte
	if err := os.WriteFile(prev, b, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := checkBackupShallow(exePath); err != nil {
		t.Fatalf("shallow check should not notice a single flipped byte: %v", err)
	}
	err = verifyBackupContent(exePath)
	if err == nil {
		t.Fatal("digest mismatch was not caught")
	}
	if !strings.Contains(err.Error(), "校验不通过") {
		t.Errorf("reason = %q", err)
	}
}

// A size that disagrees with the record means the file changed under us; that
// is cheap to spot and worth spotting before the button is even offered.
func TestShallowCheckCatchesSizeDrift(t *testing.T) {
	exePath := exeWithBackup(t)
	writeBackupMeta(exePath, "v0.2.30")

	prev := backupPath(exePath)
	b, _ := os.ReadFile(prev)
	if err := os.WriteFile(prev, append(b, 'x'), 0o755); err != nil {
		t.Fatal(err)
	}
	err := checkBackupShallow(exePath)
	if err == nil || !strings.Contains(err.Error(), "大小与备份记录不符") {
		t.Errorf("size drift not caught: %v", err)
	}
}

// A backup kept by a build that predates the sidecar has no digest to check.
// It must still be usable — that is the first rollback after this ships — but
// only after passing the shallow checks.
func TestBackupWithoutMetaStillUsable(t *testing.T) {
	exePath := exeWithBackup(t)
	if readBackupMeta(exePath) != nil {
		t.Fatal("fixture unexpectedly has meta")
	}
	if err := verifyBackupContent(exePath); err != nil {
		t.Errorf("legacy backup rejected: %v", err)
	}
	if got := backupVersion(exePath); got != "" {
		t.Errorf("version should be unknown, got %q", got)
	}
}

func TestBackupMetaIgnoresGarbage(t *testing.T) {
	exePath := exeWithBackup(t)
	for _, junk := range []string{"", "not json", "{", `{"version":123}`} {
		if err := os.WriteFile(backupMetaPath(exePath), []byte(junk), 0o600); err != nil {
			t.Fatal(err)
		}
		if m := readBackupMeta(exePath); m != nil && m.SHA256 != "" {
			t.Errorf("garbage %q produced a usable meta: %+v", junk, m)
		}
	}
	// Oversized sidecar is ignored rather than parsed.
	big := make([]byte, 8<<10)
	for i := range big {
		big[i] = 'x'
	}
	_ = os.WriteFile(backupMetaPath(exePath), big, 0o600)
	if readBackupMeta(exePath) != nil {
		t.Error("oversized meta should be ignored")
	}
}

// The reason the atomic copy exists: a killed copy must not leave a truncated
// file at the destination.
func TestCopyFileAtomicLeavesNoPartialDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	fakeBinary(t, src, 'S')

	if err := copyFileAtomic(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(dst)
	if len(a) != len(b) {
		t.Fatalf("size %d != %d", len(a), len(b))
	}
	// A failed copy must leave the destination untouched, not half-written.
	if err := copyFileAtomic(filepath.Join(dir, "nope"), dst); err == nil {
		t.Fatal("copy from a missing source succeeded")
	}
	c, _ := os.ReadFile(dst)
	if len(c) != len(a) {
		t.Errorf("failed copy damaged the destination: %d bytes", len(c))
	}
	// No temp files left behind either way.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".qz-bak-") {
			t.Errorf("temp file survived: %s", e.Name())
		}
	}
}
