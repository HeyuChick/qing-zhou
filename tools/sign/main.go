// Command sign generates the release signing key and signs release assets.
//
// Only whoever publishes releases needs this. Using or building 轻舟 does not:
// a source build has no key compiled in and therefore requires no signature.
//
// One-time, on your own machine (NOT in CI):
//
//	go run ./tools/sign -genkey
//
// It prints a private key and a public key. Put the private key in the repo's
// Actions secret RELEASE_SIGNING_KEY; paste the public key into the workflow's
// RELEASE_PUBLIC_KEY env (or leave it to the secret-derived one). The release
// workflow then does the rest — see .github/workflows/release.yml.
//
// Signing (what CI runs):
//
//	QZ_SIGNING_KEY=<private> go run ./tools/sign qingzhou-linux-amd64
//
// writes qingzhou-linux-amd64.sig next to it.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	genkey := flag.Bool("genkey", false, "generate a new signing keypair and print it")
	flag.Parse()

	if *genkey {
		if err := generate(); err != nil {
			fatal(err)
		}
		return
	}
	if flag.NArg() == 0 {
		fatal(fmt.Errorf("usage: sign [-genkey] <file>...\n  signing reads the private key from QZ_SIGNING_KEY"))
	}
	key, err := privateKey()
	if err != nil {
		fatal(err)
	}
	for _, path := range flag.Args() {
		out, err := signFile(key, path)
		if err != nil {
			fatal(err)
		}
		fmt.Println("signed", path, "->", out)
	}
}

func generate() error {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	fmt.Println("PRIVATE KEY — store as the Actions secret RELEASE_SIGNING_KEY, never commit it:")
	fmt.Println(base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	fmt.Println("PUBLIC KEY — safe to publish; the release workflow bakes it into the binary:")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func privateKey() (ed25519.PrivateKey, error) {
	s := strings.TrimSpace(os.Getenv("QZ_SIGNING_KEY"))
	if s == "" {
		return nil, fmt.Errorf("QZ_SIGNING_KEY is not set")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("QZ_SIGNING_KEY is not valid base64: %w", err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("QZ_SIGNING_KEY is %d bytes, want %d", len(b), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(b), nil
}

func signFile(key ed25519.PrivateKey, path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(key, body)
	out := path + ".sig"
	return out, os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "sign:", err)
	os.Exit(1)
}
