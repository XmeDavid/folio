package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestCheckPasswordPolicy(t *testing.T) {
	cases := []struct {
		name, pw, email, dn string
		wantErr             bool
	}{
		{"ok", "correct horse battery staple", "alice@example.com", "Alice", false},
		{"too short", "abc12345", "alice@example.com", "Alice", true},
		{"contains email local part", "alicelovescats99", "alice@example.com", "Alice", true},
		{"short email local ignored", "correct horse battery staple", "a@b.com", "Alice", false},
		{"contains display name token", "Hello Alice Smith long", "x@y.com", "Alice Smith", true},
		{"common", "password1234567", "x@y.com", "Xyzabc", true},
		{"empty", "", "x@y.com", "Xyzabc", true},
		{"too long", strings.Repeat("a", 129), "x@y.com", "Xyzabc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPasswordPolicy(tc.pw, tc.email, tc.dn)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr {
				var verr *httpx.ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("expected ValidationError, got %T: %v", err, err)
				}
			}
		})
	}
}
