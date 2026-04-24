// Package crypto wraps AES-256-GCM for encrypting provider secrets at rest.
//
// Ciphertext layout (base64-encoded for storage): nonce(12) || ciphertext || tag(16).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var ErrMalformed = errors.New("crypto: malformed ciphertext")

type AEAD struct {
	gcm cipher.AEAD
}

// New returns an AEAD using the given 32-byte key.
func New(key []byte) (*AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AEAD{gcm: gcm}, nil
}

// EncryptString encrypts plaintext and returns base64(nonce || ciphertext || tag).
// The optional aad is authenticated but not encrypted (e.g. "user:<id>").
func (a *AEAD) EncryptString(plaintext string, aad []byte) (string, error) {
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := a.gcm.Seal(nonce, nonce, []byte(plaintext), aad)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptString reverses EncryptString.
func (a *AEAD) DecryptString(b64 string, aad []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	ns := a.gcm.NonceSize()
	if len(raw) < ns+a.gcm.Overhead() {
		return "", ErrMalformed
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := a.gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return string(pt), nil
}
