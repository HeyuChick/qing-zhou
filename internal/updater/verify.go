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

// ReleasePublicKey is the base64 ed25519 public key that release assets are
// signed with. It is injected at build time:
//
//	go build -ldflags "-X qingzhou/internal/updater.ReleasePublicKey=<base64>"
//
// WHY IT EXISTS: the sha256 digest the updater checks comes from the same GitHub
// API response as the download URL, so it proves the bytes arrived intact — not
// that this project produced them. Anyone able to publish a release (a
// compromised repo or maintainer account) could otherwise ship arbitrary code to
// every panel, which replaces its own binary and execs it. Only a signature made
// with a key that never touches CI closes that.
//
// WHY IT IS EMPTY BY DEFAULT: `git clone && go build` has to just work. A
// source build gets no key and therefore no signature requirement — exactly the
// behaviour before signing existed. The official release workflow injects the
// key, so published binaries do enforce it. Nobody has to generate or manage a
// key to use, fork, or hack on this project; only whoever publishes releases
// does, and .github/workflows/release.yml wires that up from one repo secret.
//
// Once non-empty this is fail-closed: a release with no signature asset, or one
// that doesn't verify, is refused rather than falling back to the digest alone.
var ReleasePublicKey = ""

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
	s := strings.TrimSpace(ReleasePublicKey)
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
func signingEnabled() bool { return strings.TrimSpace(ReleasePublicKey) != "" }

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
