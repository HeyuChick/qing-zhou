package sshctl

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Fingerprint must accept exactly what hostKeyCallback stores — a trimmed
// authorized_keys line — because that stored string is its only input. If the
// two formats ever drift apart it returns "" and the admin is asked to compare
// a fingerprint that isn't shown, with no error anywhere to explain why.
func TestFingerprintRoundTripsWhatWePin(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly the transformation hostKeyCallback applies before persisting.
	pinned := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer)))

	got := Fingerprint(pinned)
	if want := ssh.FingerprintSHA256(signer); got != want {
		t.Fatalf("Fingerprint(%q) = %q, want %q", pinned, got, want)
	}
	if !strings.HasPrefix(got, "SHA256:") {
		t.Fatalf("fingerprint should be the SHA256: form ssh-keygen -lf prints, got %q", got)
	}
}

// Rows pinned before this helper existed, or corrupted ones, must not panic or
// yield a misleading value — the caller renders "" as "no fingerprint shown".
func TestFingerprintRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "   ", "not-a-key", "ssh-ed25519 AAAAnotbase64!!"} {
		if got := Fingerprint(s); got != "" {
			t.Fatalf("Fingerprint(%q) = %q, want empty", s, got)
		}
	}
}
