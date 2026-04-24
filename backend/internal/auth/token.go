package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GenerateSessionToken returns a fresh 256-bit random token (base64url-encoded)
// and its SHA-256 hash. The plaintext token lives only in the cookie; the
// hash is what's stored server-side in sessions.id.
func GenerateSessionToken() (plaintext string, hash []byte) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand.Read is documented to never return an error on Linux/macOS.
		panic("crypto/rand failed: " + err.Error())
	}
	plaintext = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:]
}

// HashToken returns the SHA-256 of a plaintext session token.
func HashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// SessionIDFromToken returns the base64url-encoded SHA-256 of a plaintext
// session token — the value stored as sessions.id.
func SessionIDFromToken(plaintext string) string {
	return base64.RawURLEncoding.EncodeToString(HashToken(plaintext))
}
