package accounts

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestDefaultIncludeInSavingsRate(t *testing.T) {
	cases := map[string]bool{
		"checking":      true,
		"savings":       true,
		"cash":          true,
		"credit_card":   false,
		"brokerage":     false,
		"crypto_wallet": false,
		"loan":          false,
		"mortgage":      false,
		"asset":         false,
		"pillar_2":      false,
		"pillar_3a":     false,
		"other":         false,
	}
	for kind, want := range cases {
		if got := defaultIncludeInSavingsRate(kind); got != want {
			t.Errorf("kind=%s: got %v, want %v", kind, got, want)
		}
	}
}

func TestCreateInput_normalize_appliesDefaults(t *testing.T) {
	open := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	in := CreateInput{
		Name:           "  My Checking  ",
		Kind:           "CHECKING",
		Currency:       "chf",
		OpenDate:       open,
		OpeningBalance: decimal.NewFromInt(1000),
	}
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Kind != "checking" {
		t.Errorf("kind should be lowercased: %q", out.Kind)
	}
	if out.Currency != "CHF" {
		t.Errorf("currency should be uppercased: %q", out.Currency)
	}
	if out.OpeningBalanceDate == nil || !out.OpeningBalanceDate.Equal(open) {
		t.Errorf("opening_balance_date should default to openDate")
	}
	if out.IncludeInNetworth == nil || !*out.IncludeInNetworth {
		t.Errorf("includeInNetworth should default to true")
	}
	if out.IncludeInSavingsRate == nil || !*out.IncludeInSavingsRate {
		t.Errorf("includeInSavingsRate should default to true for checking")
	}
}

func TestCreateInput_normalize_respectsExplicitIncludeOverride(t *testing.T) {
	f := false
	in := CreateInput{
		Name:                 "Groceries cash",
		Kind:                 "cash",
		Currency:             "EUR",
		OpenDate:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		OpeningBalance:       decimal.Zero,
		IncludeInSavingsRate: &f, // override: cash normally true → user wants false
	}
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.IncludeInSavingsRate == nil || *out.IncludeInSavingsRate {
		t.Errorf("explicit false override for includeInSavingsRate should stick")
	}
}

func TestCreateInput_normalize_validationErrors(t *testing.T) {
	base := CreateInput{
		Name:     "x",
		Kind:     "checking",
		Currency: "EUR",
		OpenDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	cases := []struct {
		name string
		mod  func(CreateInput) CreateInput
	}{
		{"empty name", func(in CreateInput) CreateInput { in.Name = "   "; return in }},
		{"unknown kind", func(in CreateInput) CreateInput { in.Kind = "bogus"; return in }},
		{"bad currency", func(in CreateInput) CreateInput { in.Currency = "!!"; return in }},
		{"missing open date", func(in CreateInput) CreateInput { in.OpenDate = time.Time{}; return in }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.mod(base).normalize()
			if err == nil {
				t.Fatalf("expected error")
			}
			var verr *httpx.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want ValidationError, got %T", err)
			}
		})
	}
}

func TestPatchInput_normalize(t *testing.T) {
	empty := ""
	bad := "2026-13-40"
	good := "2026-05-05"
	blank := "   "

	t.Run("empty name rejected", func(t *testing.T) {
		_, err := PatchInput{Name: &blank}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("clear close_date via empty string is OK", func(t *testing.T) {
		_, err := PatchInput{CloseDate: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	})
	t.Run("bad close_date rejected", func(t *testing.T) {
		_, err := PatchInput{CloseDate: &bad}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("good close_date accepted", func(t *testing.T) {
		_, err := PatchInput{CloseDate: &good}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	})
	t.Run("invalid kind rejected", func(t *testing.T) {
		bogus := "bogus"
		_, err := PatchInput{Kind: &bogus}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("kind is lowercased", func(t *testing.T) {
		k := "  SAVINGS  "
		out, err := PatchInput{Kind: &k}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if got := *out.Kind; got != "savings" {
			t.Fatalf("kind = %q, want %q", got, "savings")
		}
	})
}
