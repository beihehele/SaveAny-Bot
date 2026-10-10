// Package adminauth provides bounded password hashing for the admin console.
package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

const hashPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

// Verifier holds the salt and derived key, never the plaintext password.
type Verifier struct {
	salt []byte
	key  []byte
}

// Hash creates a salted Argon2id hash with fixed, bounded costs.
func Hash(password []byte) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("admin password must contain 12 to 1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey(password, salt, 2, 19456, 1, 32)
	return hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// Parse accepts only the supported hash format, preventing unbounded KDF costs.
func Parse(encoded string) (*Verifier, error) {
	if !strings.HasPrefix(encoded, hashPrefix) || len(encoded) > 150 {
		return nil, errors.New("unsupported admin password hash; generate it using admin-password")
	}
	parts := strings.Split(strings.TrimPrefix(encoded, hashPrefix), "$")
	if len(parts) != 2 {
		return nil, errors.New("invalid admin password hash")
	}
	salt, saltErr := base64.RawStdEncoding.Strict().DecodeString(parts[0])
	key, keyErr := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if saltErr != nil || keyErr != nil || len(salt) != 16 || len(key) != 32 {
		return nil, errors.New("invalid admin password hash")
	}
	return &Verifier{salt: salt, key: key}, nil
}

// Verify compares the derived key in constant time.
func (v *Verifier) Verify(password []byte) bool {
	if len(password) > 1024 {
		return false
	}
	key := argon2.IDKey(password, v.salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(key, v.key) == 1
}
