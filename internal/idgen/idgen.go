// Package idgen mints the random identifiers the panel needs: proxy-client
// credentials (uuid + password), session/verification tokens, and sub tokens.
// (Extracted from the former sing-box integration, which is no longer a dependency.)
package idgen

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"

	"github.com/google/uuid"
)

// Credentials are the secrets that identify a proxy client across protocols.
type Credentials struct {
	UUID     string // vless / vmess / tuic
	Password string // trojan / hysteria2 / tuic / mixed / ...
}

// NewCredentials mints a fresh uuid + short password.
func NewCredentials() (Credentials, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return Credentials{}, err
	}
	pw, err := RandHex(8) // 16 hex chars
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{UUID: id.String(), Password: pw}, nil
}

// RandHex returns n random bytes as a hex string (2n chars).
func RandHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RandToken returns a URL-safe random token with ~n bytes of entropy.
func RandToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
