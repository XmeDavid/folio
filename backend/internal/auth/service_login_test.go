package auth

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestLoginInput_normalize_ok(t *testing.T) {
	in := LoginInput{Email: "  A@B.com ", Password: "x"}
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Email != "a@b.com" {
		t.Errorf("email = %q", out.Email)
	}
}

func TestLoginInput_normalize_errors(t *testing.T) {
	cases := []LoginInput{
		{Email: "no-at", Password: "x"},
		{Email: "a@b.com", Password: ""},
	}
	for _, tc := range cases {
		_, err := tc.normalize()
		var verr *httpx.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
	}
}
