package auth

import (
	"encoding/base64"
	"testing"
)

func TestGenerateSessionToken(t *testing.T) {
	tok, hash := GenerateSessionToken()
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("expected 32 bytes token, got %d", len(raw))
	}
	if len(hash) != 32 {
		t.Fatalf("expected 32 bytes sha256 hash, got %d", len(hash))
	}
	again := HashToken(tok)
	if string(again) != string(hash) {
		t.Fatalf("HashToken not deterministic")
	}
}

func TestGenerateSessionToken_unique(t *testing.T) {
	a, _ := GenerateSessionToken()
	b, _ := GenerateSessionToken()
	if a == b {
		t.Fatalf("two tokens should differ")
	}
}

func TestSessionIDFromToken_matchesHash(t *testing.T) {
	tok, hash := GenerateSessionToken()
	id := SessionIDFromToken(tok)
	expected := base64.RawURLEncoding.EncodeToString(hash)
	if id != expected {
		t.Fatalf("SessionIDFromToken = %q, want %q", id, expected)
	}
}
