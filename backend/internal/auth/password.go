// Package auth owns credential primitives (password hashing, session tokens),
// HTTP middleware (RequireSession, RequireMembership, RequireRole,
// RequireFreshReauth, CSRF), rate limiters, and the signup/login/logout
// HTTP surface. It is intentionally free of tenant-scoped queries; those
// live in backend/internal/identity.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// applyPepper HMACs the password with the pepper. Returned bytes are fed into
// Argon2id in place of the plaintext, so a DB-only breach can't be brute
// forced without also stealing the pepper.
func applyPepper(password string, pepper []byte) []byte {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(password))
	return mac.Sum(nil)
}

// HashPassword returns an Argon2id PHC-encoded hash. Params encoded in the
// string so we can bump them without invalidating existing hashes. When
// pepper is non-empty, the password is HMAC-SHA256'd with it before Argon2id
// and the encoded form gains a `pep=1` flag so VerifyPassword can mirror it.
func HashPassword(password string, pepper []byte) (string, error) {
	if password == "" {
		return "", errors.New("password required")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	input := []byte(password)
	pepFlag := ""
	if len(pepper) > 0 {
		input = applyPepper(password, pepper)
		pepFlag = ",pep=1"
	}
	key := argon2.IDKey(input, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d%s$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads, pepFlag,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword returns (true, nil) on match, (false, nil) on mismatch,
// (false, err) on malformed encoding. Auto-detects the `pep=1` flag in the
// stored encoding and applies the pepper as needed; legacy unpeppered hashes
// keep verifying with pepper=nil.
func VerifyPassword(password, encoded string, pepper []byte) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid argon2id encoding")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("version parse: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version: %d", version)
	}
	paramsField := parts[3]
	peppered := strings.Contains(paramsField, ",pep=1")
	// fmt.Sscanf stops at the first unmatched character, so the trailing
	// `,pep=1` (when present) is read as part of the threads field's tail
	// and ignored — but to be safe we strip it before parsing.
	core := strings.TrimSuffix(paramsField, ",pep=1")
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(core, "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("params parse: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("salt decode: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("hash decode: %w", err)
	}
	input := []byte(password)
	if peppered {
		if len(pepper) == 0 {
			return false, errors.New("hash requires pepper but none configured")
		}
		input = applyPepper(password, pepper)
	}
	actual := argon2.IDKey(input, salt, time, memory, threads, uint32(len(expected)))
	if subtle.ConstantTimeCompare(expected, actual) == 1 {
		return true, nil
	}
	return false, nil
}
