package sshctl

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeKey(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// The stored value is a file NAME. Anything that could reach outside the key
// directory turns an admin-controlled string into an arbitrary-file-read on the
// panel host, so these have to be refused before the file is ever opened.
func TestResolveKeyFile_RejectsAnythingButAPlainName(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, dir, "id_ed25519", "key", 0o600)

	for _, name := range []string{
		"../../../etc/shadow",
		"..",
		"sub/id_ed25519",
		`sub\id_ed25519`,
		"/etc/shadow",
		"",
		"   ",
	} {
		if _, err := ResolveKeyFile(dir, name); err == nil {
			t.Errorf("ResolveKeyFile(%q) was accepted; it must not be", name)
		}
	}

	if _, err := ResolveKeyFile(dir, "id_ed25519"); err != nil {
		t.Errorf("a plain name in the directory was rejected: %v", err)
	}
}

// A name can be innocent and the file still point anywhere: validating the
// string is not the same as validating where it lands.
func TestResolveKeyFile_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	outside := t.TempDir()
	secret := writeKey(t, outside, "secret", "not yours", 0o600)

	dir := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(dir, "innocent")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if _, err := ResolveKeyFile(dir, "innocent"); err == nil {
		t.Error("a symlink pointing outside the key directory was accepted")
	}
}

// Empty directory means the feature is off. It must fail loudly rather than
// resolving against the process's working directory.
func TestResolveKeyFile_UnsetDir(t *testing.T) {
	if _, err := ResolveKeyFile("", "id_ed25519"); err != ErrKeyDirUnset {
		t.Errorf("got %v, want ErrKeyDirUnset", err)
	}
	if _, err := ListKeyFiles(""); err != ErrKeyDirUnset {
		t.Errorf("ListKeyFiles: got %v, want ErrKeyDirUnset", err)
	}
}

// A directory that does not exist yet is the normal state of a fresh install —
// nothing to offer, not an error to show.
func TestListKeyFiles_MissingDirIsEmptyNotAnError(t *testing.T) {
	files, err := ListKeyFiles(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("got %v, want no error", err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want none", len(files))
	}
}

func TestReadKeyFile_RefusesAWorldReadableKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful here")
	}
	dir := t.TempDir()
	writeKey(t, dir, "loose", "key", 0o644)
	_, err := ReadKeyFile(dir, "loose")
	if err == nil {
		t.Fatal("a group/world-readable key was accepted")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("error should say how to fix it, got: %v", err)
	}
}

// .pub files and ssh client furniture live in the same directory on a real box
// and are never the answer, so they should not be offered as choices.
func TestListKeyFiles_SkipsNonKeys(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, dir, "id_ed25519", "key", 0o600)
	writeKey(t, dir, "id_ed25519.pub", "pub", 0o644)
	writeKey(t, dir, "known_hosts", "hosts", 0o644)
	writeKey(t, dir, "config", "cfg", 0o644)

	files, err := ListKeyFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "id_ed25519" {
		var got []string
		for _, f := range files {
			got = append(got, f.Name)
		}
		t.Fatalf("offered %v, want only [id_ed25519]", got)
	}
	if !files[0].Readable {
		t.Error("a readable key was reported unreadable")
	}
	// Docker runs the panel as uid 10001; a key the process cannot open is the
	// most common failure, and the UI shows this flag so it is visible up front.
	if runtime.GOOS != "windows" && !files[0].ModeOK {
		t.Error("a 0600 key was reported as having loose permissions")
	}
}

// A file that is not a key at all must be rejected at save time, not at deploy.
func TestValidateKeyFile_RejectsNonKeyContent(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, dir, "notakey", "hello", 0o600)
	if err := ValidateKeyFile(dir, "notakey", ""); err == nil {
		t.Error("a file that is not a private key was accepted")
	}
}
