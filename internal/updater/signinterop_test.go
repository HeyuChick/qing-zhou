package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end over the real release path: tools/sign generates a key and signs a
// file; the updater's verifier accepts it with the public key that the workflow
// bakes in, and rejects the same file once a byte changes.
func TestSignToolInteropWithVerifier(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to go run")
	}
	dir := t.TempDir()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	gen := exec.Command("go", "run", "./tools/sign", "-genkey")
	gen.Dir = root
	out, err := gen.CombinedOutput()
	if err != nil {
		t.Fatalf("genkey: %v\n%s", err, out)
	}
	var priv, pub string
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "PRIVATE KEY") && i+1 < len(lines) {
			priv = strings.TrimSpace(lines[i+1])
		}
		if strings.HasPrefix(l, "PUBLIC KEY") && i+1 < len(lines) {
			pub = strings.TrimSpace(lines[i+1])
		}
	}
	if priv == "" || pub == "" {
		t.Fatalf("could not parse keys from:\n%s", out)
	}

	asset := filepath.Join(dir, "qingzhou-linux-amd64")
	if err := os.WriteFile(asset, []byte("pretend release binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sign := exec.Command("go", "run", "./tools/sign", asset)
	sign.Dir = root
	sign.Env = append(os.Environ(), "QZ_SIGNING_KEY="+priv)
	if out, err := sign.CombinedOutput(); err != nil {
		t.Fatalf("sign: %v\n%s", err, out)
	}

	sig, err := os.ReadFile(asset + ".sig")
	if err != nil {
		t.Fatalf("the tool did not write %s: %v", signatureAssetName(asset), err)
	}
	body, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}

	orig := ReleasePublicKey
	ReleasePublicKey = pub
	t.Cleanup(func() { ReleasePublicKey = orig })

	if !signingEnabled() {
		t.Fatal("signing should be enabled with a key injected")
	}
	if err := verifySignature(body, sig); err != nil {
		t.Errorf("verifier rejected a signature its own tool produced: %v", err)
	}
	if err := verifySignature(append(body, '!'), sig); err == nil {
		t.Error("a tampered asset passed verification")
	}
}
