package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// withKey swaps the compiled-in public key for the duration of a test.
func withKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	orig := releasePublicKeyOverride
	releasePublicKeyOverride = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { releasePublicKeyOverride = orig })
}

// The digest the updater checks comes from the same API response as the download
// URL, so it only proves the transfer was intact. The signature is what proves
// the project produced the bytes — the check that stops a compromised repo or
// maintainer account from shipping code to every panel.
func TestVerifySignature_AcceptsGenuineRejectsForged(t *testing.T) {
	pub, priv := testKey(t)
	withKey(t, pub)

	binary := []byte("the real release binary")
	good := ed25519.Sign(priv, binary)

	if err := verifySignature(binary, []byte(base64.StdEncoding.EncodeToString(good))); err != nil {
		t.Errorf("genuine base64 signature rejected: %v", err)
	}
	if err := verifySignature(binary, []byte(hex.EncodeToString(good))); err != nil {
		t.Errorf("genuine hex signature rejected: %v", err)
	}
	if err := verifySignature(binary, []byte("  "+base64.StdEncoding.EncodeToString(good)+"\n")); err != nil {
		t.Errorf("signature with surrounding whitespace rejected: %v", err)
	}

	// Tampered payload, genuine signature.
	if err := verifySignature([]byte("a malicious binary"), []byte(base64.StdEncoding.EncodeToString(good))); err == nil {
		t.Error("a modified binary passed verification")
	}
	// Genuine payload, signature from a different key — the compromised-publisher case.
	_, otherPriv := testKey(t)
	forged := ed25519.Sign(otherPriv, binary)
	if err := verifySignature(binary, []byte(base64.StdEncoding.EncodeToString(forged))); err == nil {
		t.Error("a signature from an unrelated key was accepted")
	}
	// Garbage and truncated signatures.
	for _, bad := range []string{"", "not-a-signature", base64.StdEncoding.EncodeToString(good[:32])} {
		if err := verifySignature(binary, []byte(bad)); err == nil {
			t.Errorf("malformed signature %q was accepted", bad)
		}
	}
}

// With no key compiled in, verification must report that rather than pass — the
// caller only invokes it when signingEnabled(), and a silent pass here would be
// the wrong default if that ever changed.
func TestVerifySignature_NoKeyIsAnError(t *testing.T) {
	orig := releasePublicKeyOverride
	releasePublicKeyOverride = ""
	t.Cleanup(func() { releasePublicKeyOverride = orig })

	if signingEnabled() {
		t.Fatal("signing should be disabled with an empty key")
	}
	if err := verifySignature([]byte("x"), []byte("y")); err == nil {
		t.Error("verification with no key must fail closed")
	}
}

// Apply re-fetches the release rather than carrying the one Check showed, so
// without a version gate an admin could approve vX and be served whatever
// /releases/latest returns — including an older, known-vulnerable build.
func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, cur string
		want     bool
	}{
		{"v1.2.3", "v1.2.2", true},
		{"v1.2.3", "v1.2.3", false}, // same version — nothing to do
		{"v1.2.2", "v1.2.3", false}, // downgrade to a possibly vulnerable build
		{"v1.10.0", "v1.9.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.tag, c.cur, false); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.tag, c.cur, got, c.want)
		}
	}
	// A dev build has no comparable version and may move onto any tagged release.
	if !isNewer("v0.0.1", "", true) {
		t.Error("a dev build should be allowed onto a tagged release")
	}
}

// A release that installs but cannot start must be recoverable. This deployment
// updates only through the panel and has no SSH, so without a local copy of the
// outgoing binary a bad release leaves nothing to fall back to.
func TestBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "qingzhou")
	if err := os.WriteFile(exe, []byte("old version"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Stand in for what run() does before the swap.
	if err := copyFile(exe, backupPath(exe)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new broken version"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := restoreBackup(exe); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old version" {
		t.Errorf("after rollback the binary is %q, want the previous one", got)
	}
}

// Rolling back when nothing was kept must report the problem rather than
// silently appear to succeed.
func TestRestoreBackup_NoBackup(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "qingzhou")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := restoreBackup(exe); err == nil {
		t.Error("restore with no backup should fail")
	}
}

func TestSignatureAssetName(t *testing.T) {
	if got := signatureAssetName("qingzhou-linux-amd64"); got != "qingzhou-linux-amd64.sig" {
		t.Errorf("signatureAssetName = %q", got)
	}
}
