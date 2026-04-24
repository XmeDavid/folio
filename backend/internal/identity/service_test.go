package identity

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestOnboardInput_normalize_valid(t *testing.T) {
	in := OnboardInput{
		TenantName:   "  Household  ",
		BaseCurrency: "chf",
		Locale:       "en-CH",
		Email:        " User@Example.com ",
		DisplayName:  "Alice",
	}
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TenantName != "Household" {
		t.Errorf("tenantName not trimmed: %q", out.TenantName)
	}
	if out.BaseCurrency != "CHF" {
		t.Errorf("baseCurrency not uppercased: %q", out.BaseCurrency)
	}
	if out.Email != "user@example.com" {
		t.Errorf("email not lowercased: %q", out.Email)
	}
	if out.Timezone != "UTC" {
		t.Errorf("timezone default should be UTC, got %q", out.Timezone)
	}
	if out.CycleAnchorDay != 1 {
		t.Errorf("cycleAnchorDay default should be 1, got %d", out.CycleAnchorDay)
	}
}

func TestOnboardInput_normalize_validationErrors(t *testing.T) {
	cases := []struct {
		name string
		in   OnboardInput
	}{
		{"missing tenant", OnboardInput{BaseCurrency: "CHF", Locale: "en-CH", Email: "a@b.com", DisplayName: "x"}},
		{"bad email", OnboardInput{TenantName: "t", BaseCurrency: "CHF", Locale: "en-CH", Email: "notanemail", DisplayName: "x"}},
		{"missing displayName", OnboardInput{TenantName: "t", BaseCurrency: "CHF", Locale: "en-CH", Email: "a@b.com"}},
		{"missing locale", OnboardInput{TenantName: "t", BaseCurrency: "CHF", Email: "a@b.com", DisplayName: "x"}},
		{"bad currency", OnboardInput{TenantName: "t", BaseCurrency: "ch", Locale: "en-CH", Email: "a@b.com", DisplayName: "x"}},
		{"bad cycleAnchorDay", OnboardInput{TenantName: "t", BaseCurrency: "CHF", Locale: "en-CH", Email: "a@b.com", DisplayName: "x", CycleAnchorDay: 40}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.normalize()
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			var verr *httpx.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected ValidationError, got %T: %v", err, err)
			}
		})
	}
}
