package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"qingzhou/internal/version"
)

// ReleasePublicKey is the base64 (std encoding) ed25519 public key that release
// assets are signed with. Compiled in, so it is part of the artifact an operator
// already trusts rather than something fetched at update time.
//
// WHY: the sha256 digest the updater checks comes from the same GitHub API
// response as the download URL, so it proves the bytes arrived intact — not that
// the project produced them. Anyone who can publish a release (a compromised
// repo or maintainer account) can therefore ship arbitrary code to every panel,
// which then replaces its own binary and execs it. A signature made with a key
// that never touches CI is what closes that.
//
// Empty disables verification. It ships empty so upgrading to this build does
// not break updates for a deployment whose release pipeline does not sign yet.
// To turn it on:
//
//  1. Generate a key offline (keep the private half off CI):
//     openssl genpkey -algorithm ed25519 -out release.key
//     openssl pkey -in release.key -pubout -outform DER | tail -c 32 | base64
//  2. Paste the printed value here and rebuild.
//  3. Have the release job publish "<asset>.sig" next to each binary: the raw
//     64-byte ed25519 signature over the asset's bytes, base64 or hex encoded.
//
// Once non-empty, an unsigned or wrongly-signed release is refused — the
// fail-closed direction, so a stripped signature cannot silently downgrade the
// check.
const ReleasePublicKey = ""

// releasePublicKeyOverride is the key actually consulted. It exists so tests can
// exercise both the signed and unsigned paths; production never reassigns it.
var releasePublicKeyOverride = ReleasePublicKey

// signatureAssetName is the asset holding the detached signature for name.
func signatureAssetName(name string) string { return name + ".sig" }

var errNoPublicKey = errors.New("no release public key compiled in")

// verifySignature checks a detached ed25519 signature over binary.
//
// sig may be base64 or hex, with surrounding whitespace — release tooling
// differs and the encoding is not worth a failed update.
func verifySignature(binary, sig []byte) error {
	pub, err := releasePublicKey()
	if err != nil {
		return err
	}
	raw, err := decodeSignature(sig)
	if err != nil {
		return err
	}
	if len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, want %d", len(raw), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, binary, raw) {
		return errors.New("signature does not match the release public key")
	}
	return nil
}

func releasePublicKey() (ed25519.PublicKey, error) {
	s := strings.TrimSpace(releasePublicKeyOverride)
	if s == "" {
		return nil, errNoPublicKey
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("release public key is not valid base64: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("release public key is %d bytes, want %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// decodeSignature accepts base64 or hex.
//
// Both are attempted and the one that yields exactly a signature's worth of
// bytes wins. Trying base64 first and taking any success is wrong: a 128-char
// hex signature is also valid base64, and decodes to 96 meaningless bytes — so a
// hex-encoded signature was rejected as "96 bytes, want 64" instead of verifying.
func decodeSignature(sig []byte) ([]byte, error) {
	s := strings.TrimSpace(string(sig))
	if s == "" {
		return nil, errors.New("signature is empty")
	}
	b64, b64err := base64.StdEncoding.DecodeString(s)
	if b64err == nil && len(b64) == ed25519.SignatureSize {
		return b64, nil
	}
	if h, err := decodeHex(s); err == nil && len(h) == ed25519.SignatureSize {
		return h, nil
	}
	if b64err == nil {
		return b64, nil // let the caller report the length mismatch
	}
	return nil, errors.New("signature is neither base64 nor hex")
}

func decodeHex(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("odd length")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexVal(s[2*i])
		if err != nil {
			return nil, err
		}
		lo, err := hexVal(s[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexVal(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("invalid hex byte %q", c)
}

// signingEnabled reports whether a public key is compiled in.
func signingEnabled() bool { return strings.TrimSpace(releasePublicKeyOverride) != "" }

// maxAssetBytes caps a release download. The asset size GitHub reports only
// drove the progress bar, so nothing bounded the write: a wrong or hostile repo
// could stream unlimited bytes into the sibling temp file and fill the
// filesystem — taking the SQLite database down with it — long before the digest
// was ever checked.
const maxAssetBytes = 512 << 20 // 512 MiB; the real binary is tens of MB

// maxSignatureBytes caps the detached signature fetch. A signature is 64 raw
// bytes; the slack covers encoding and trailing whitespace.
const maxSignatureBytes = 4 << 10

// isNewer reports whether tag is a strictly newer release than cur. A dev build
// (no tagged version) has nothing to compare against and accepts anything, so an
// operator can move onto a tagged build.
//
// dev is a parameter rather than a version.IsDev() call inside, so the ordering
// rule is testable — a test binary is itself a dev build, which would otherwise
// make every case return true and assert nothing.
func isNewer(tag, cur string, dev bool) bool {
	if dev {
		return true
	}
	return version.Compare(tag, cur) > 0
}

// backupPath is where the outgoing binary is kept so a failed start can be
// rolled back. Sibling of the executable, so the rename stays on one filesystem.
func backupPath(exePath string) string { return exePath + ".prev" }

// restoreBackup puts the previous binary back. Best-effort: used on the path
// where the new binary is already installed but could not be started.
func restoreBackup(exePath string) error {
	prev := backupPath(exePath)
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary kept at %s: %w", prev, err)
	}
	return os.Rename(prev, exePath)
}
