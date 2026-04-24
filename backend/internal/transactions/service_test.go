package transactions

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func validBase() CreateInput {
	return CreateInput{
		AccountID: uuid.New(),
		Currency:  "CHF",
		BookedAt:  time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount:    decimal.NewFromInt(-4200),
	}
}

func TestCreateInput_normalize_appliesDefaults(t *testing.T) {
	in := validBase()
	in.Currency = "chf "
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "posted" {
		t.Errorf("status should default to posted, got %q", out.Status)
	}
	if out.Currency != "CHF" {
		t.Errorf("currency should be normalized: %q", out.Currency)
	}
}

func TestCreateInput_normalize_acceptsValidStatuses(t *testing.T) {
	for _, s := range []string{"draft", "posted", "reconciled", "voided"} {
		t.Run(s, func(t *testing.T) {
			in := validBase()
			in.Status = s
			out, err := in.normalize()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Status != s {
				t.Errorf("status = %q, want %q", out.Status, s)
			}
		})
	}
}

func TestCreateInput_normalize_validationErrors(t *testing.T) {
	cases := []struct {
		name string
		mod  func(CreateInput) CreateInput
	}{
		{"missing accountId", func(in CreateInput) CreateInput { in.AccountID = uuid.Nil; return in }},
		{"unknown status", func(in CreateInput) CreateInput { in.Status = "bogus"; return in }},
		{"missing bookedAt", func(in CreateInput) CreateInput { in.BookedAt = time.Time{}; return in }},
		{"bad currency", func(in CreateInput) CreateInput { in.Currency = "!!"; return in }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.mod(validBase()).normalize()
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

func TestPatchInput_normalize_statusValidation(t *testing.T) {
	good := "reconciled"
	out, err := PatchInput{Status: &good}.normalize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.statusSet || out.status != "reconciled" {
		t.Errorf("status not applied")
	}

	bad := "bogus"
	_, err = PatchInput{Status: &bad}.normalize()
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestPatchInput_normalize_dateFields(t *testing.T) {
	t.Run("bookedAt empty rejected", func(t *testing.T) {
		empty := ""
		_, err := PatchInput{BookedAt: &empty}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bookedAt bad format", func(t *testing.T) {
		bad := "2026/04/15"
		_, err := PatchInput{BookedAt: &bad}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bookedAt valid", func(t *testing.T) {
		good := "2026-04-15"
		out, err := PatchInput{BookedAt: &good}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.bookedAtSet {
			t.Fatal("bookedAt should be set")
		}
	})
	t.Run("valueAt empty clears", func(t *testing.T) {
		empty := ""
		out, err := PatchInput{ValueAt: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.valueAtSet || !out.valueAtNull {
			t.Error("valueAt empty should clear")
		}
	})
	t.Run("postedAt RFC3339 parsed", func(t *testing.T) {
		ts := "2026-04-15T10:30:00Z"
		out, err := PatchInput{PostedAt: &ts}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.postedAtSet || out.postedAtNull {
			t.Error("postedAt should be set, not null")
		}
	})
	t.Run("postedAt non-RFC3339 rejected", func(t *testing.T) {
		ts := "2026-04-15"
		_, err := PatchInput{PostedAt: &ts}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestPatchInput_normalize_amountCurrency(t *testing.T) {
	amt := "42.50"
	cur := "eur"
	out, err := PatchInput{Amount: &amt, Currency: &cur}.normalize()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !out.amountSet || out.amount.String() != "42.5" {
		t.Errorf("amount = %q, want 42.5", out.amount.String())
	}
	if !out.currencySet || out.currency != "EUR" {
		t.Errorf("currency = %q, want EUR", out.currency)
	}

	badAmt := "not-a-number"
	_, err = PatchInput{Amount: &badAmt}.normalize()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPatchInput_normalize_nullableIDs(t *testing.T) {
	t.Run("categoryId valid UUID", func(t *testing.T) {
		id := uuid.New().String()
		out, err := PatchInput{CategoryID: &id}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.categoryIDSet || out.categoryIDNull {
			t.Error("categoryId should be set, not null")
		}
	})
	t.Run("categoryId empty clears", func(t *testing.T) {
		empty := ""
		out, err := PatchInput{CategoryID: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.categoryIDSet || !out.categoryIDNull {
			t.Error("categoryId empty should clear")
		}
	})
	t.Run("categoryId bad UUID rejected", func(t *testing.T) {
		bad := "not-a-uuid"
		_, err := PatchInput{CategoryID: &bad}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestPatchInput_normalize_countAsExpense(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		out, err := PatchInput{}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if out.countAsExpenseSet {
			t.Error("countAsExpense should not be set when absent")
		}
	})
	t.Run("explicit null", func(t *testing.T) {
		out, err := PatchInput{CountAsExpense: json.RawMessage("null")}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.countAsExpenseSet || !out.countAsExpenseNull {
			t.Error("countAsExpense null should clear")
		}
	})
	t.Run("true", func(t *testing.T) {
		out, err := PatchInput{CountAsExpense: json.RawMessage("true")}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.countAsExpenseSet || out.countAsExpenseNull || !out.countAsExpense {
			t.Error("countAsExpense true not applied")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		_, err := PatchInput{CountAsExpense: json.RawMessage(`"maybe"`)}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestIsValidStatus(t *testing.T) {
	for _, s := range []string{"draft", "posted", "reconciled", "voided"} {
		if !IsValidStatus(s) {
			t.Errorf("expected %q valid", s)
		}
	}
	if IsValidStatus("nope") {
		t.Error("expected nope invalid")
	}
}
