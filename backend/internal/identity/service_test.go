package identity

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestCreateWorkspaceInput_Normalize_ok(t *testing.T) {
	out, err := CreateWorkspaceInput{
		Name: "  My Workspace ", BaseCurrency: "chf", Locale: "en-CH",
	}.Normalize()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Name != "My Workspace" {
		t.Errorf("name = %q", out.Name)
	}
	if out.BaseCurrency != "CHF" {
		t.Errorf("cur = %q", out.BaseCurrency)
	}
	if out.Timezone != "UTC" {
		t.Errorf("tz = %q", out.Timezone)
	}
	if out.CycleAnchorDay != 1 {
		t.Errorf("day = %d", out.CycleAnchorDay)
	}
}

func TestCreateWorkspaceInput_Normalize_errors(t *testing.T) {
	cases := []struct {
		name string
		in   CreateWorkspaceInput
	}{
		{"missing name", CreateWorkspaceInput{BaseCurrency: "CHF", Locale: "en"}},
		{"missing locale", CreateWorkspaceInput{Name: "x", BaseCurrency: "CHF"}},
		{"bad currency", CreateWorkspaceInput{Name: "x", BaseCurrency: "zz", Locale: "en"}},
		{"bad day", CreateWorkspaceInput{Name: "x", BaseCurrency: "CHF", Locale: "en", CycleAnchorDay: 40}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.Normalize()
			if err == nil {
				t.Fatalf("expected error")
			}
			var verr *httpx.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected ValidationError, got %T", err)
			}
		})
	}
}
