package bankimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestParseRevolutBankingSample(t *testing.T) {
	content := readLegacySample(t, "account-statement.csv")
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Profile != "revolut_banking_csv" {
		t.Fatalf("profile = %q", parsed.Profile)
	}
	if parsed.Institution != "Revolut" {
		t.Fatalf("institution = %q", parsed.Institution)
	}
	if len(parsed.Transactions) == 0 {
		t.Fatal("expected transactions")
	}
	first := parsed.Transactions[0]
	if got := first.BookedAt.Format(dateOnly); got != "2019-04-18" {
		t.Fatalf("first booked date = %s", got)
	}
	if !first.Amount.Equal(decimal.RequireFromString("1")) {
		t.Fatalf("first amount = %s", first.Amount)
	}
	if first.Currency != "EUR" {
		t.Fatalf("first currency = %s", first.Currency)
	}
	if first.ExternalID == "" {
		t.Fatal("expected deterministic external id")
	}
	if parsed.DateFrom == nil || parsed.DateFrom.Format(dateOnly) != "2019-04-18" {
		t.Fatalf("date from = %#v", parsed.DateFrom)
	}
}

func TestClassifyDuplicatesAndConflicts(t *testing.T) {
	parsed := ParsedFile{
		Profile:  "test",
		Currency: "CHF",
		Transactions: []ParsedTransaction{
			{
				BookedAt:    mustDate(t, "2026-01-10"),
				Amount:      decimal.RequireFromString("-12.30"),
				Currency:    "CHF",
				Description: strPtr("COOP 123"),
				ExternalID:  "same-source",
			},
			{
				BookedAt:    mustDate(t, "2026-01-11"),
				Amount:      decimal.RequireFromString("-9.90"),
				Currency:    "CHF",
				Description: strPtr("Migros"),
				ExternalID:  "new-source-conflict",
			},
			{
				BookedAt:    mustDate(t, "2026-01-12"),
				Amount:      decimal.RequireFromString("-5"),
				Currency:    "CHF",
				Description: strPtr("Bakery"),
				ExternalID:  "new",
			},
		},
	}
	existingSource := "same-source"
	existing := []existingTx{
		{
			BookedAt:    mustDate(t, "2026-01-10"),
			Amount:      decimal.RequireFromString("-12.30"),
			Currency:    "CHF",
			Description: "COOP 123",
			SourceID:    &existingSource,
		},
		{
			BookedAt:    mustDate(t, "2026-01-11"),
			Amount:      decimal.RequireFromString("-9.90"),
			Currency:    "CHF",
			Description: "MIGROS OLD TEXT",
		},
	}

	got := classify(parsed, existing)
	if len(got.duplicates) != 1 {
		t.Fatalf("duplicates = %d", len(got.duplicates))
	}
	if len(got.conflicts) != 1 {
		t.Fatalf("conflicts = %d", len(got.conflicts))
	}
	if len(got.importable) != 1 {
		t.Fatalf("importable = %d", len(got.importable))
	}
}

func readLegacySample(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "legacy", "data", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	return string(b)
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateOnly, s)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	return d
}

func strPtr(s string) *string { return &s }
