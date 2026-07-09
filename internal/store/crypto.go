package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

const encPrefix = "enc:v1:"

// encKeys are settings stored encrypted at rest.
var encKeys = map[string]bool{"smtp_pass": true}

// SetSecretKey derives the AES key used to encrypt secret settings. Pass
// QZ_SECRET_KEY (recommended, kept outside the DB) or fall back to jwt_secret.
func (s *Store) SetSecretKey(raw []byte) {
	h := sha256.Sum256(raw)
	s.secretKey = h[:]
}

// encrypt returns the ciphertext for a secret setting. It fails CLOSED: on any
// cipher/RNG error it returns "" rather than the plaintext, so a failure can
// never silently persist a secret in cleartext. (With a 32-byte key these error
// paths are effectively unreachable, but the behavior must be safe if they hit.)
func (s *Store) encrypt(plain string) string {
	if len(s.secretKey) == 0 || plain == "" || strings.HasPrefix(plain, encPrefix) {
		return plain
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct)
}

// decrypt returns the plaintext of an encrypted setting. Non-prefixed values are
// legacy cleartext and returned as-is. It fails CLOSED: once the enc:v1: prefix
// is present, any decode/decrypt failure returns "" instead of handing the raw
// ciphertext blob back to the caller.
func (s *Store) decrypt(val string) string {
	if len(s.secretKey) == 0 || !strings.HasPrefix(val, encPrefix) {
		return val
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(val, encPrefix))
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return ""
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return ""
	}
	return string(pt)
}
