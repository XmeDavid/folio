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

// TestDateDriftConflictRequiresDescriptionMatch covers the GBP -1 regression:
// "+1 Balance migration" arriving 2 days after an unrelated "+1 Carregamento"
// should NOT be silently dropped as a date_drift conflict. The pass2 conflict
// originally fired on (amount, currency, ±7d) regardless of description,
// which classified two separate transactions of the same amount as the same
// row and dropped the second.
func TestDateDriftConflictRequiresDescriptionMatch(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	descIncoming := "Balance migration to another region or legal entity"
	incoming := ParsedTransaction{
		BookedAt:    day("2026-01-21"),
		Amount:      decimal.RequireFromString("1"),
		Currency:    "GBP",
		Description: &descIncoming,
	}
	existing := []existingTx{
		{
			ID:          uuid.New(),
			BookedAt:    day("2026-01-19"), // 2 days earlier — inside review window, outside auto window
			Amount:      decimal.RequireFromString("1"),
			Currency:    "GBP",
			Description: "Carregamento com cartão *2835",
		},
	}
	if _, ok := conflictByStableFields(incoming, existing); ok {
		t.Fatal("rows with clearly different descriptions must not be marked as date_drift conflicts")
	}

	// Sanity: when descriptions agree, pass2 still fires.
	descMatch := "Carregamento com cartão *2835"
	incoming2 := ParsedTransaction{
		BookedAt:    day("2026-01-21"),
		Amount:      decimal.RequireFromString("1"),
		Currency:    "GBP",
		Description: &descMatch,
	}
	if _, ok := conflictByStableFields(incoming2, existing); !ok {
		t.Fatal("matching-description ±7d drift should still raise a date_drift conflict")
	}
}

// TestMatchResidualSubsetReturnsSubset locks in the API contract used by
// retireExplainedSynthetics' consume-once policy: the matcher must report
// which existing-row indices made up the matching subset so the caller can
// strike them off the pool before evaluating the next synthetic.
func TestMatchResidualSubsetReturnsSubset(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	syntheticDate := day("2025-12-03")
	residual := decimal.RequireFromString("-15")

	existing := []existingTx{
		{ID: uuid.New(), BookedAt: day("2025-11-30"), Amount: decimal.RequireFromString("-7"), Currency: "EUR"},
		{ID: uuid.New(), BookedAt: day("2025-12-01"), Amount: decimal.RequireFromString("-15"), Currency: "EUR"}, // exact single-row match
		{ID: uuid.New(), BookedAt: day("2025-12-02"), Amount: decimal.RequireFromString("-8"), Currency: "EUR"},
	}
	ok, used := matchResidualSubset(syntheticDate, time.Time{}, "EUR", residual, existing)
	if !ok {
		t.Fatal("expected a match")
	}
	// Single-row exact match preferred over subset-sum solutions.
	if len(used) != 1 || used[0] != 1 {
		t.Fatalf("expected single-row match at index 1, got %v", used)
	}
}

// TestImportCandidatesFiltersByKind covers the GBP=2.4 / USD=29.46 leak:
// Flexible Cash Funds (brokerage) interest rows were auto-imported into
// Conta Pessoal (checking) accounts because they were the only same-
// currency targets. The wizard should not propose mixing brokerage rows
// into a cash account or vice versa.
func TestImportCandidatesFiltersByKind(t *testing.T) {
	cashAcct := importAccountMatch{
		ID: uuid.New(), Name: "Conta Pessoal GBP", Currency: "GBP", Kind: "checking",
	}
	brokerageAcct := importAccountMatch{
		ID: uuid.New(), Name: "Flexible Cash Funds GBP", Currency: "GBP", Kind: "brokerage",
	}
	accounts := []importAccountMatch{cashAcct, brokerageAcct}

	cashCands := importCandidates(accounts, "Revolut", "GBP", "checking")
	if len(cashCands) != 1 || cashCands[0].ID != cashAcct.ID {
		t.Fatalf("checking group: expected only cash account, got %+v", cashCands)
	}
	brokerCands := importCandidates(accounts, "Revolut", "GBP", "brokerage")
	if len(brokerCands) != 1 || brokerCands[0].ID != brokerageAcct.ID {
		t.Fatalf("brokerage group: expected only brokerage account, got %+v", brokerCands)
	}
	// Crypto wallet kind treated as investment-like and matches brokerage.
	cryptoCands := importCandidates(accounts, "Revolut", "GBP", "crypto_wallet")
	if len(cryptoCands) != 1 || cryptoCands[0].ID != brokerageAcct.ID {
		t.Fatalf("crypto group: expected brokerage account as compatible target, got %+v", cryptoCands)
	}
}
