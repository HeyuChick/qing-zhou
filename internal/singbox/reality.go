package singbox

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/curve25519"
)

// GenerateRealityKeypair returns a fresh x25519 keypair for VLESS Reality,
// base64-url encoded the same way `sing-box generate reality-keypair` does
// (no padding). The private key goes in the server inbound's
// tls.reality.private_key; the public key goes in the client link (pbk=).
func GenerateRealityKeypair() (priv, pub string, err error) {
	var p [32]byte
	if _, err = rand.Read(p[:]); err != nil {
		return "", "", err
	}
	pubBytes, err := curve25519.X25519(p[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(p[:]), enc.EncodeToString(pubBytes), nil
}

// GenerateShortID returns a random Reality short_id (hex). n is the byte length
// (1..8); the resulting hex string is 2n characters.
func GenerateShortID(n int) (string, error) {
	if n < 1 || n > 8 {
		n = 8
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
