package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TestDateProximityMatchIncomingPosted covers the case where the *incoming*
// row carries booked/posted dates and the existing row has only one date —
// the shape produced when the consolidated v2 export is ingested first
// (settle date only) and the banking export is ingested second (carries
// auth-vs-settle pair). Without considering incoming.PostedAt, the dedup
// matcher missed matches whose auth/settle gap exceeded the tolerance.
func TestDateProximityMatchIncomingPosted(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }

	// Banking shows Pingo Doce auth=10-16, settle=10-30.
	// Consolidated shows it as a single date 10-30.
	incomingBooked := day("2019-10-16")
	incomingPosted := postedPtr("2019-10-30")
	existingBooked := day("2019-10-30")

	if !dateProximityMatch(incomingBooked, incomingPosted, existingBooked, nil, autoDedupDays) {
		t.Fatal("expected match via incoming.PostedAt vs existing.BookedAt")
	}
	if dateProximityMatch(incomingBooked, nil, existingBooked, nil, autoDedupDays) {
		t.Fatal("sanity: without incoming.PostedAt the spread is 14 days, must not match")
	}
}

// TestResidualExplainedByGapStart covers the case where a synthetic carries
// a gap_start_date pointing 8+ days before the trigger date. The matching
// real row sits inside the gap interval. Without gap_start_date, the
// matcher's ±7d window around the trigger would miss it.
func TestResidualExplainedByGapStart(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	syntheticDate := day("2025-12-03") // trigger
	gapStart := day("2025-11-25")      // 8 days earlier
	residual := decimal.RequireFromString("-16.04")

	existing := []existingTx{
		{ID: uuid.New(), BookedAt: day("2025-11-25"), Amount: residual, Currency: "EUR"},
	}

	if !residualExplainedByExisting(syntheticDate, gapStart, "EUR", residual, existing) {
		t.Fatal("expected match: real row inside gap interval should explain residual")
	}
	if residualExplainedByExisting(syntheticDate, time.Time{}, "EUR", residual, existing) {
		t.Fatal("sanity: without gapStart the row falls outside ±7d window of trigger and should not match")
	}
}

// TestSyntheticDoesNotAbsorbRealRows covers the original 13.56 bug: a real
// banking row with the same amount/currency/date as a synthetic in the
// destination account should NOT be classified as a duplicate of that
// synthetic. Synthetics are placeholders — the retire pass voids them once
// the real row arrives. Treating them as duplicates here drops the real
// row and leaves the synthetic in place.
func TestSyntheticDoesNotAbsorbRealRows(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	desc := "EUR → Revolut X"
	incoming := ParsedTransaction{
		BookedAt:    day("2025-11-25"),
		Amount:      decimal.RequireFromString("-16.04"),
		Currency:    "EUR",
		Description: &desc,
	}
	synthDesc := "Revolut balance adjustment (Conversão cambial para EUR)"
	existing := []existingTx{
		{
			ID:          uuid.New(),
			BookedAt:    day("2025-11-25"),
			Amount:      decimal.RequireFromString("-16.04"),
			Currency:    "EUR",
			Description: synthDesc,
			Synthetic:   true,
		},
	}
	if duplicateByFingerprint(incoming, existing) {
		t.Fatal("real banking row must not be marked duplicate of a synthetic")
	}
	if _, ok := conflictByStableFields(incoming, existing); ok {
		t.Fatal("real banking row must not be marked as conflict against a synthetic")
	}
}
