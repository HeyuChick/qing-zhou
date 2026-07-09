package auth

import "golang.org/x/crypto/bcrypt"

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash is a valid bcrypt hash of a random value, used to spend roughly the
// same time as a real comparison when the account doesn't exist — so login
// response timing doesn't reveal whether a username is registered.
var dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// DummyCompare runs a bcrypt comparison against a fixed dummy hash purely to
// equalize timing on the "user not found" path. Its result is meaningless.
func DummyCompare(pw string) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(pw))
}
