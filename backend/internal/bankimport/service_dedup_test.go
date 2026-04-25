package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestCrossFormatInterchange(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }
	desc := "Amazon"

	bankingExisting := []existingTx{{
		ID:          uuid.New(),
		BookedAt:    day("2025-12-10"),
		PostedAt:    postedPtr("2025-12-16"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: "Amazon",
	}}

	consolidatedIncoming := ParsedTransaction{
		BookedAt:    day("2025-12-16"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: &desc,
	}

	t.Run("banking first, consolidated second — auto dedup via posted_at", func(t *testing.T) {
		got := classify(ParsedFile{Currency: "CHF", Transactions: []ParsedTransaction{consolidatedIncoming}}, bankingExisting)
		if len(got.duplicates) != 1 || len(got.importable) != 0 {
			t.Fatalf("want 1 dup, 0 imp; got dup=%d imp=%d conflict=%d", len(got.duplicates), len(got.importable), len(got.conflicts))
		}
	})

	t.Run("consolidated first, banking second — date_drift conflict", func(t *testing.T) {
		consolidatedExisting := []existingTx{{
			ID:          uuid.New(),
			BookedAt:    day("2025-12-16"),
			PostedAt:    nil,
			Amount:      decimal.RequireFromString("-152.98"),
			Currency:    "CHF",
			Description: "Amazon",
		}}
		bankingIncoming := ParsedTransaction{
			BookedAt:    day("2025-12-10"),
			Amount:      decimal.RequireFromString("-152.98"),
			Currency:    "CHF",
			Description: &desc,
		}
		got := classify(ParsedFile{Currency: "CHF", Transactions: []ParsedTransaction{bankingIncoming}}, consolidatedExisting)
		if len(got.conflicts) != 1 {
			t.Fatalf("want 1 conflict (date_drift); got dup=%d imp=%d conflict=%d", len(got.duplicates), len(got.importable), len(got.conflicts))
		}
		if got.conflicts[0].Reason != "date_drift" {
			t.Fatalf("conflict reason = %q, want date_drift", got.conflicts[0].Reason)
		}
	})
}

func TestClassifySkipsSyntheticWhenExplained(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	desc := "Revolut balance adjustment (Amazon)"
	synthetic := ParsedTransaction{
		BookedAt:    day("2025-12-16"),
		Amount:      decimal.RequireFromString("-105.77"),
		Currency:    "CHF",
		Description: &desc,
		Raw: map[string]string{
			"synthetic":          syntheticBalanceReconcile,
			"synthetic_residual": "-105.77",
		},
	}
	parsed := ParsedFile{
		Profile:      "revolut_consolidated_v2",
		Currency:     "CHF",
		Transactions: []ParsedTransaction{synthetic},
	}
	existing := []existingTx{
		{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-93.79"), Currency: "CHF", Description: "CHF → Revolut X"},
		{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-11.98"), Currency: "CHF", Description: "CHF → Revolut X"},
	}
	got := classify(parsed, existing)
	if len(got.importable) != 0 {
		t.Fatalf("expected synthetic to be classified as duplicate; got %d importable", len(got.importable))
	}
	if len(got.duplicates) != 1 {
		t.Fatalf("expected 1 duplicate; got %d", len(got.duplicates))
	}
}
