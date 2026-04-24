package auth

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestSignupInput_normalize_ok(t *testing.T) {
	in := SignupInput{
		Email: "  A@B.com ", Password: "correct horse battery staple",
		DisplayName: "Alice",
	}
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Email != "a@b.com" {
		t.Errorf("email = %q", out.Email)
	}
	if out.TenantName != "Alice's Workspace" {
		t.Errorf("tenant = %q", out.TenantName)
	}
	if out.Locale != "en-US" || out.Timezone != "UTC" || out.BaseCurrency != "USD" {
		t.Errorf("defaults not applied: %+v", out)
	}
}

func TestSignupInput_normalize_errors(t *testing.T) {
	cases := []struct {
		name string
		in   SignupInput
	}{
		{"no email", SignupInput{Password: "correct horse battery staple", DisplayName: "A"}},
		{"no display", SignupInput{Email: "a@b.com", Password: "correct horse battery staple"}},
		{"weak password", SignupInput{Email: "a@b.com", Password: "short", DisplayName: "A"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.normalize()
			if err == nil {
				t.Fatalf("expected error")
			}
			var verr *httpx.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected validation error, got %T: %v", err, err)
			}
		})
	}
}
