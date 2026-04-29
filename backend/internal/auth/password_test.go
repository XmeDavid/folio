package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple", nil)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected argon2id PHC string, got %q", hash)
	}
	ok, err := VerifyPassword("correct horse battery staple", hash, nil)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatalf("VerifyPassword returned false for correct password")
	}
	ok, err = VerifyPassword("wrong", hash, nil)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): %v", err)
	}
	if ok {
		t.Fatalf("VerifyPassword returned true for wrong password")
	}
}

func TestHashPassword_uniqueSalt(t *testing.T) {
	h1, _ := HashPassword("same", nil)
	h2, _ := HashPassword("same", nil)
	if h1 == h2 {
		t.Fatalf("two hashes of the same password should differ (random salt)")
	}
}

func TestHashPassword_empty(t *testing.T) {
	if _, err := HashPassword("", nil); err == nil {
		t.Fatalf("expected error on empty password")
	}
}

func TestHashAndVerifyPassword_pepper(t *testing.T) {
	pepper := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	hash, err := HashPassword("hunter2", pepper)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.Contains(hash, ",pep=1") {
		t.Fatalf("expected pep=1 marker in encoded hash, got %q", hash)
	}
	ok, err := VerifyPassword("hunter2", hash, pepper)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected verification to succeed with matching pepper")
	}
	if _, err := VerifyPassword("hunter2", hash, nil); err == nil {
		t.Fatal("expected error when pepper missing for peppered hash")
	}
	ok, err = VerifyPassword("hunter2", hash, []byte("wrong-pepper-wrong-pepper-wrong!"))
	if err != nil {
		t.Fatalf("VerifyPassword(wrong pepper): %v", err)
	}
	if ok {
		t.Fatal("verification must fail when pepper differs")
	}
}
