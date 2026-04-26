# Cross-Format Revolut Import Dedup & Synthetic Retirement Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Revolut **banking** export and **consolidated v2** export interchangeable, so the same Folio account can ingest either file (or both, in any order) without producing duplicate transactions or stale synthetic balance-reconcile rows.

**Architecture:**
The dedup primitives currently require an exact `BookedAt` timestamp match. The two formats stamp the same transaction with different dates (banking uses auth-date as `booked_at` + settle-date as `posted_at`; consolidated v2 uses settle-date as `booked_at` only). We extend dedup to consider both dates, accept ±1 day drift automatically, route ±7 day drift to the existing manual-review queue, and add bidirectional synthetic-row retirement so the parser-emitted balance-adjustment rows don't fight with the real rows that explain them.

**Tech Stack:** Go 1.22 (`backend/internal/bankimport`), Postgres (`transactions`, `source_refs` tables), Next.js + TypeScript (`web/lib/api/client.ts`, `web/app/t/[slug]/accounts/page.tsx`).

---

## Background

The current Revolut consolidated v2 export omits real transactions that the legacy banking export captures. Concrete example from the user's CSV:

- Consolidated v2 shows `Dec 16  Amazon -152.98 CHF  bal 1.23` — but the running balance dropped by 258.75 CHF, not 152.98.
- The legacy banking export contains two extra rows the consolidated file silently drops: `Dec 15 21:44:36 CHF → Revolut X -93.79` and `Dec 15 21:44:55 CHF → Revolut X -11.98` (sum 105.77, exactly the gap).

We already inserted **synthetic balance-adjustment rows** (`raw->>'synthetic' = 'balance_reconcile'`) in the consolidated parser to make the running balance reconcile when the file is imported alone. Those synthetic rows must retire when the legacy banking file later contributes the real rows, and must not be re-inserted when the legacy file is imported first.

## File Structure

**New files**
- `backend/internal/bankimport/dedup.go` — pure dedup primitives (description normalisation, date-tolerant match, residual matcher). Pulls existing logic out of `service.go` and adds the new fuzzy variants.
- `backend/internal/bankimport/dedup_test.go` — table-driven unit tests for the primitives.
- `backend/internal/bankimport/synthetic.go` — synthetic-row helpers (already present in `parser.go::buildReconcileTx`; move it here for cohesion) plus the residual-explained matcher and the post-apply retirement query.
- `backend/internal/bankimport/synthetic_test.go` — unit tests for the residual matcher.
- `backend/internal/bankimport/service_dedup_test.go` — integration tests on `classify` covering both directions of the workflow.

**Modified files**
- `backend/internal/bankimport/service.go` — `existingTx` gains `PostedAt`; `loadExisting` query selects it; `classify`/`stableMatch`/`duplicateByFingerprint`/`conflictByStableFields` delegate to `dedup.go` helpers; `applyPlan` calls the synthetic retirement step after inserts.
- `backend/internal/bankimport/types.go` — `ConflictPreview` gains `Reason` (`"description_mismatch"` | `"date_drift"`).
- `backend/internal/bankimport/parser.go` — drop `buildReconcileTx` (moves to `synthetic.go`); the synthetic loop keeps emitting rows but tags `Raw["synthetic_residual"]` with the residual decimal as a string (so the apply path can match without reparsing).
- `web/lib/api/client.ts` — `ImportConflictPreview` gains `reason?: "description_mismatch" | "date_drift"`.
- `web/app/t/[slug]/accounts/page.tsx` — render the conflict reason inline so the user can tell drifted-date matches from description-disagreement matches.

---

## Phase 1 — Fuzzy Dedup (PR1)

This phase makes existing data dedupe correctly across the two formats. Synthetic-row behaviour is unchanged here.

### Task 1: Description normalisation helper

**Files:**
- Create: `backend/internal/bankimport/dedup.go`
- Test: `backend/internal/bankimport/dedup_test.go`

- [ ] **Step 1: Write the failing test**

```go
package bankimport

import "testing"

func TestNormalizeDescription(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Amazon", "amazon"},
		{"  AMAZON  ", "amazon"},
		{"Amazon.com", "amazoncom"},
		{"Pagamento com cartão Amazon", "pagamento com cartao amazon"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := normalizeDescription(tc.in); got != tc.want {
				t.Fatalf("normalizeDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bankimport/ -run TestNormalizeDescription -v`
Expected: FAIL — `undefined: normalizeDescription`

- [ ] **Step 3: Implement the helper**

```go
package bankimport

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// normalizeDescription folds a transaction description for fuzzy comparison.
// It lowercases, strips punctuation, removes diacritics (so "cartão" matches
// "cartao"), and collapses runs of whitespace. Designed to make the same
// merchant name match across Revolut's banking and consolidated v2 exports
// without overgenerating false positives — we keep word boundaries so
// "Amazon" and "AmazonPrime" stay distinct.
func normalizeDescription(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err == nil {
		s = folded
	}
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevSpace = false
		case unicode.IsSpace(r):
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimRight(b.String(), " ")
}
```

- [ ] **Step 4: Add the dependency to go.mod if needed**

Run: `cd backend && go mod tidy`

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/bankimport/ -run TestNormalizeDescription -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/bankimport/dedup.go backend/internal/bankimport/dedup_test.go backend/go.mod backend/go.sum
git commit -m "bankimport: add normalizeDescription helper for fuzzy dedup"
```

### Task 2: Plumb `posted_at` through `existingTx`

**Files:**
- Modify: `backend/internal/bankimport/service.go` (struct + `loadExisting` query + scan)

- [ ] **Step 1: Extend the struct**

In `service.go`, change `type existingTx` to:

```go
type existingTx struct {
	ID          uuid.UUID
	BookedAt    time.Time
	PostedAt    *time.Time
	Amount      decimal.Decimal
	Currency    string
	Description string
	SourceID    *string
}
```

- [ ] **Step 2: Update the SQL**

In `loadExisting`, replace the SELECT list with:

```sql
select t.id, t.booked_at, t.posted_at, t.amount::text, t.currency,
       coalesce(t.description, t.counterparty_raw, ''),
       sr.external_id
```

Update the `rows.Scan` call to scan `&e.PostedAt` after `&e.BookedAt`.

- [ ] **Step 3: Run existing tests to verify nothing regressed**

Run: `go test ./internal/bankimport/...`
Expected: PASS (no behaviour change yet)

- [ ] **Step 4: Commit**

```bash
git add backend/internal/bankimport/service.go
git commit -m "bankimport: load posted_at into existingTx for cross-format dedup"
```

### Task 3: Date-tolerant match primitive

**Files:**
- Modify: `backend/internal/bankimport/dedup.go`
- Modify: `backend/internal/bankimport/dedup_test.go`

- [ ] **Step 1: Write the failing test**

Append to `dedup_test.go`:

```go
import "time"

func TestDateProximityMatch(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }
	cases := []struct {
		name           string
		incomingBooked time.Time
		existingBooked time.Time
		existingPosted *time.Time
		toleranceDays  int
		wantMatch      bool
	}{
		{"same day", day("2025-12-16"), day("2025-12-16"), nil, 1, true},
		{"one day off booked", day("2025-12-15"), day("2025-12-16"), nil, 1, true},
		{"two days off booked", day("2025-12-14"), day("2025-12-16"), nil, 1, false},
		{"matches existing posted", day("2025-12-16"), day("2025-12-10"), postedPtr("2025-12-16"), 1, true},
		{"posted one day off", day("2025-12-17"), day("2025-12-10"), postedPtr("2025-12-16"), 1, true},
		{"both far away", day("2025-12-01"), day("2025-12-10"), postedPtr("2025-12-16"), 1, false},
		{"7d window catches gap", day("2025-12-09"), day("2025-12-16"), nil, 7, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dateProximityMatch(tc.incomingBooked, tc.existingBooked, tc.existingPosted, tc.toleranceDays)
			if got != tc.wantMatch {
				t.Fatalf("dateProximityMatch = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bankimport/ -run TestDateProximityMatch -v`
Expected: FAIL — `undefined: dateProximityMatch`

- [ ] **Step 3: Implement the primitive**

Append to `dedup.go`:

```go
import "time"

// dateProximityMatch returns true when incomingBooked is within toleranceDays
// of either the existing row's booked_at or its posted_at (when present).
// Comparison is done in UTC date space — auth/settle pairs across midnight
// timezones still match. The two-axis check exists because Revolut's
// banking export carries both auth (booked) and settle (posted) dates while
// the consolidated v2 export only carries one date stamp; matching either
// covers same-tx pairs in both files.
func dateProximityMatch(incomingBooked, existingBooked time.Time, existingPosted *time.Time, toleranceDays int) bool {
	if datesWithin(incomingBooked, existingBooked, toleranceDays) {
		return true
	}
	if existingPosted != nil && datesWithin(incomingBooked, *existingPosted, toleranceDays) {
		return true
	}
	return false
}

func datesWithin(a, b time.Time, days int) bool {
	const day = 24 * time.Hour
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= time.Duration(days)*day
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bankimport/ -run TestDateProximityMatch -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/bankimport/dedup.go backend/internal/bankimport/dedup_test.go
git commit -m "bankimport: add dateProximityMatch primitive"
```

### Task 4: Fuzzy stable-match (auto-dedup ±1d)

**Files:**
- Modify: `backend/internal/bankimport/dedup.go`
- Modify: `backend/internal/bankimport/service.go`
- Modify: `backend/internal/bankimport/dedup_test.go`

- [ ] **Step 1: Write the failing test**

Append to `dedup_test.go`:

```go
import (
	"github.com/shopspring/decimal"
	"github.com/google/uuid"
)

func TestFuzzyStableMatchAutoWindow(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }
	desc := "Amazon"
	existing := existingTx{
		ID:          uuid.New(),
		BookedAt:    day("2025-12-10"),
		PostedAt:    postedPtr("2025-12-16"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: "Amazon",
	}
	cases := []struct {
		name      string
		incoming  ParsedTransaction
		wantMatch bool
	}{
		{
			name: "amazon settle vs auth — auto match via posted",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-16"),
				Amount:      decimal.RequireFromString("-152.98"),
				Currency:    "CHF",
				Description: &desc,
			},
			wantMatch: true,
		},
		{
			name: "different amount no match",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-16"),
				Amount:      decimal.RequireFromString("-152.99"),
				Currency:    "CHF",
				Description: &desc,
			},
			wantMatch: false,
		},
		{
			name: "different currency no match",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-16"),
				Amount:      decimal.RequireFromString("-152.98"),
				Currency:    "EUR",
				Description: &desc,
			},
			wantMatch: false,
		},
		{
			name: "8 days off — outside auto window",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-24"),
				Amount:      decimal.RequireFromString("-152.98"),
				Currency:    "CHF",
				Description: &desc,
			},
			wantMatch: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fuzzyStableMatch(tc.incoming, existing, autoDedupDays); got != tc.wantMatch {
				t.Fatalf("fuzzyStableMatch = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bankimport/ -run TestFuzzyStableMatchAutoWindow -v`
Expected: FAIL — `undefined: fuzzyStableMatch` / `undefined: autoDedupDays`

- [ ] **Step 3: Implement**

Append to `dedup.go`:

```go
const (
	autoDedupDays   = 1
	reviewDedupDays = 7
)

// fuzzyStableMatch tests whether an incoming row is the same transaction as
// an existing one, accepting up to `toleranceDays` of date drift while
// requiring an exact amount + currency match. Use autoDedupDays for the
// auto-skip path and reviewDedupDays for the user-confirms path.
func fuzzyStableMatch(incoming ParsedTransaction, existing existingTx, toleranceDays int) bool {
	if !incoming.Amount.Equal(existing.Amount) {
		return false
	}
	if incoming.Currency != existing.Currency {
		return false
	}
	return dateProximityMatch(incoming.BookedAt, existing.BookedAt, existing.PostedAt, toleranceDays)
}
```

- [ ] **Step 4: Wire into `classify`**

In `service.go`, replace:

```go
func duplicateByFingerprint(incoming ParsedTransaction, existing []existingTx) bool {
	for _, e := range existing {
		if stableMatch(incoming, e) && normalizeText(valueOf(incoming.Description)) == normalizeText(e.Description) {
			return true
		}
	}
	return false
}
```

with:

```go
func duplicateByFingerprint(incoming ParsedTransaction, existing []existingTx) bool {
	incomingDesc := normalizeDescription(valueOf(incoming.Description))
	for _, e := range existing {
		if !fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue
		}
		if incomingDesc == "" || normalizeDescription(e.Description) == incomingDesc {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run all bankimport tests**

Run: `go test ./internal/bankimport/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/bankimport/dedup.go backend/internal/bankimport/dedup_test.go backend/internal/bankimport/service.go
git commit -m "bankimport: auto-dedup across ±1d and posted-date for cross-format imports"
```

### Task 5: Manual-review window (±7d) surfaced as conflicts

**Files:**
- Modify: `backend/internal/bankimport/types.go`
- Modify: `backend/internal/bankimport/service.go`
- Modify: `backend/internal/bankimport/dedup_test.go`

- [ ] **Step 1: Add the reason field to `ConflictPreview`**

In `types.go`:

```go
type ConflictPreview struct {
	Reason   string     `json:"reason,omitempty"`
	Incoming PreviewRow `json:"incoming"`
	Existing PreviewRow `json:"existing"`
}
```

- [ ] **Step 2: Update conflict detection to set reason and accept date drift**

In `service.go`, replace `conflictByStableFields` with:

```go
func conflictByStableFields(incoming ParsedTransaction, existing []existingTx) (ConflictPreview, bool) {
	incomingDesc := normalizeDescription(valueOf(incoming.Description))
	// Pass 1: exact (or auto-window) match with description disagreement.
	for _, e := range existing {
		if !fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue
		}
		if incomingDesc != "" && normalizeDescription(e.Description) != incomingDesc {
			return previewConflict("description_mismatch", incoming, e), true
		}
	}
	// Pass 2: ±7d drift on amount+currency, regardless of description.
	for _, e := range existing {
		if fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue // already handled
		}
		if fuzzyStableMatch(incoming, e, reviewDedupDays) {
			return previewConflict("date_drift", incoming, e), true
		}
	}
	return ConflictPreview{}, false
}

func previewConflict(reason string, incoming ParsedTransaction, e existingTx) ConflictPreview {
	return ConflictPreview{
		Reason:   reason,
		Incoming: previewRow(incoming),
		Existing: PreviewRow{
			BookedAt:    e.BookedAt.Format(dateOnly),
			Amount:      e.Amount.String(),
			Currency:    e.Currency,
			Description: e.Description,
		},
	}
}
```

- [ ] **Step 3: Add a date-drift conflict test**

Append to `dedup_test.go`:

```go
func TestConflictByStableFieldsDateDrift(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	desc := "Amazon"
	existing := []existingTx{{
		ID:          uuid.New(),
		BookedAt:    day("2025-12-09"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: "Amazon",
	}}
	incoming := ParsedTransaction{
		BookedAt:    day("2025-12-15"), // 6 days off — outside auto, inside review
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: &desc,
	}
	got, ok := conflictByStableFields(incoming, existing)
	if !ok {
		t.Fatal("expected a conflict for 6-day drift")
	}
	if got.Reason != "date_drift" {
		t.Fatalf("conflict reason = %q, want date_drift", got.Reason)
	}
}
```

- [ ] **Step 4: Run all bankimport tests**

Run: `go test ./internal/bankimport/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/bankimport/types.go backend/internal/bankimport/service.go backend/internal/bankimport/dedup_test.go
git commit -m "bankimport: surface ±7d date drift as date_drift conflict for manual review"
```

### Task 6: Surface conflict reason in the wizard

**Files:**
- Modify: `web/lib/api/client.ts`
- Modify: `web/app/t/[slug]/accounts/page.tsx`

- [ ] **Step 1: Extend the type**

In `client.ts`, update `ImportConflictPreview`:

```ts
export type ImportConflictPreview = {
  reason?: "description_mismatch" | "date_drift";
  incoming: ImportPreviewRow;
  existing: ImportPreviewRow;
};
```

- [ ] **Step 2: Render the reason inline**

In `page.tsx`, wherever the conflict list renders each row's existing/incoming preview, add a small label:

```tsx
{conflict.reason === "date_drift" ? (
  <span className="rounded-full bg-amber-100 px-2 py-[1px] text-[11px] font-medium text-amber-900">
    Possible duplicate (different dates)
  </span>
) : conflict.reason === "description_mismatch" ? (
  <span className="rounded-full bg-amber-100 px-2 py-[1px] text-[11px] font-medium text-amber-900">
    Same amount, different description
  </span>
) : null}
```

(Insert it near the existing row's date column. Look for the conflict-list rendering — it currently shows side-by-side previews.)

- [ ] **Step 3: Verify type-check**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/lib/api/client.ts web/app/t/[slug]/accounts/page.tsx
git commit -m "web: surface import conflict reason (date_drift vs description_mismatch)"
```

---

## Phase 2 — Synthetic Retirement (PR2)

This phase keeps the consolidated v2 parser's synthetic balance-adjustment rows from fighting with real banking-export rows.

### Task 7: Tag synthetic rows with their residual amount

**Files:**
- Modify: `backend/internal/bankimport/parser.go`

- [ ] **Step 1: Move `buildReconcileTx` to a new file**

Create `backend/internal/bankimport/synthetic.go`:

```go
package bankimport

import (
	"github.com/shopspring/decimal"
)

const syntheticBalanceReconcile = "balance_reconcile"

// buildReconcileTx wraps a residual delta (= balance_delta − stated_amount)
// in a ParsedTransaction tagged so the importer / UI can identify it as a
// synthetic balance-adjustment paired with the preceding row. We carry the
// triggering row's date and account hint so the reconcile lands inside
// the same logical day as the cause. Raw["synthetic_residual"] echoes the
// amount as a decimal string so the apply path can match without reparsing.
func buildReconcileTx(trigger ParsedTransaction, residual decimal.Decimal, balanceRaw string) ParsedTransaction {
	cause := ""
	if trigger.Description != nil {
		cause = *trigger.Description
	}
	desc := "Revolut balance adjustment"
	if cause != "" {
		desc = "Revolut balance adjustment (" + cause + ")"
	}
	descPtr := desc
	raw := map[string]string{
		"section":            trigger.AccountHint,
		"currency":           trigger.Currency,
		"synthetic":          syntheticBalanceReconcile,
		"synthetic_residual": residual.String(),
		"trigger_amount":     trigger.Amount.String(),
		"trigger_balance":    balanceRaw,
	}
	if cause != "" {
		raw["trigger_description"] = cause
	}
	return ParsedTransaction{
		BookedAt:    trigger.BookedAt,
		Amount:      residual,
		Currency:    trigger.Currency,
		Description: &descPtr,
		AccountHint: trigger.AccountHint,
		KindHint:    trigger.KindHint,
		Raw:         raw,
	}
}
```

- [ ] **Step 2: Remove the same function from `parser.go`**

Delete the `buildReconcileTx` definition from `parser.go` (it's now in `synthetic.go`). Leave the call sites untouched.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/bankimport/...`
Expected: PASS (no behavioural change, just code move + new raw key).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/bankimport/parser.go backend/internal/bankimport/synthetic.go
git commit -m "bankimport: move synthetic helpers to dedicated file, tag residual"
```

### Task 8: Residual-explained matcher

**Files:**
- Modify: `backend/internal/bankimport/synthetic.go`
- Create: `backend/internal/bankimport/synthetic_test.go`

- [ ] **Step 1: Write the failing test**

```go
package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestResidualExplainedByExisting(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	syntheticDate := day("2025-12-16")
	residual := decimal.RequireFromString("-105.77")

	cases := []struct {
		name     string
		existing []existingTx
		want     bool
	}{
		{
			name: "two real rows summing to residual within window",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-93.79"), Currency: "CHF", Description: "CHF → Revolut X"},
				{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-11.98"), Currency: "CHF", Description: "CHF → Revolut X"},
			},
			want: true,
		},
		{
			name: "single row matching residual",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-14"), Amount: decimal.RequireFromString("-105.77"), Currency: "CHF"},
			},
			want: true,
		},
		{
			name: "rows outside ±7d window — no match",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-01"), Amount: decimal.RequireFromString("-105.77"), Currency: "CHF"},
			},
			want: false,
		},
		{
			name: "rows in different currency",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-105.77"), Currency: "EUR"},
			},
			want: false,
		},
		{
			name:     "no rows at all",
			existing: nil,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := residualExplainedByExisting(syntheticDate, "CHF", residual, tc.existing); got != tc.want {
				t.Fatalf("residualExplainedByExisting = %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bankimport/ -run TestResidualExplainedByExisting -v`
Expected: FAIL — `undefined: residualExplainedByExisting`

- [ ] **Step 3: Implement**

Append to `synthetic.go`:

```go
import (
	"time"
)

// residualExplainedByExisting returns true when a synthetic balance-reconcile
// row's residual is already covered by real (non-synthetic) transactions in
// the destination account within ±7 days of the synthetic's date. Used in
// two places:
//
//   - At classify time on banking-first → consolidated-second imports, to
//     skip inserting a synthetic whose residual is already present.
//   - At post-apply time on consolidated-first → banking-second imports, to
//     void synthetics now redundant after the banking rows arrive.
//
// The matcher accepts two shapes: a single existing row whose amount equals
// the residual, or a subset whose sum equals it. We keep this conservative —
// only consider rows in the same currency, same sign, within window — to
// avoid voiding a real synthetic that just happens to coincide with normal
// transaction noise.
func residualExplainedByExisting(syntheticDate time.Time, currency string, residual decimal.Decimal, existing []existingTx) bool {
	candidates := make([]decimal.Decimal, 0, len(existing))
	for _, e := range existing {
		if e.Currency != currency {
			continue
		}
		if !datesWithin(e.BookedAt, syntheticDate, reviewDedupDays) {
			continue
		}
		if residual.Sign() != 0 && e.Amount.Sign() != 0 && residual.Sign() != e.Amount.Sign() {
			continue
		}
		candidates = append(candidates, e.Amount)
	}
	if len(candidates) == 0 {
		return false
	}
	// Single-row match — fast path.
	for _, c := range candidates {
		if c.Equal(residual) {
			return true
		}
	}
	// Subset-sum: at most ~10 candidates per window in practice; full search is fine.
	if len(candidates) > 16 {
		// Hard cap to avoid pathological 2^N blow-up. If a section has >16
		// same-sign-same-currency rows in a 14-day window, the heuristic
		// becomes unreliable anyway.
		return false
	}
	target := residual
	tolerance := decimal.RequireFromString("0.02")
	for mask := 1; mask < 1<<len(candidates); mask++ {
		sum := decimal.Zero
		for i, c := range candidates {
			if mask&(1<<i) != 0 {
				sum = sum.Add(c)
			}
		}
		if sum.Sub(target).Abs().LessThanOrEqual(tolerance) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/bankimport/ -run TestResidualExplainedByExisting -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/bankimport/synthetic.go backend/internal/bankimport/synthetic_test.go
git commit -m "bankimport: residual-explained matcher for synthetic retirement"
```

### Task 9: Direction A — skip synthetic when residual already explained (classify path)

**Files:**
- Modify: `backend/internal/bankimport/service.go`
- Create: `backend/internal/bankimport/service_dedup_test.go`

- [ ] **Step 1: Write the failing test**

```go
package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bankimport/ -run TestClassifySkipsSyntheticWhenExplained -v`
Expected: FAIL — synthetic ends up in `importable`.

- [ ] **Step 3: Wire the matcher into `classify`**

In `service.go`, modify `classify`:

```go
func classify(parsed ParsedFile, existing []existingTx) classifiedRows {
	var out classifiedRows
	for _, incoming := range parsed.Transactions {
		if incoming.Raw[syntheticTagKey] == syntheticBalanceReconcile {
			if residualAlreadyImported(incoming, existing) {
				out.duplicates = append(out.duplicates, incoming)
				continue
			}
		}
		if duplicateBySource(incoming, existing) || duplicateByFingerprint(incoming, existing) {
			out.duplicates = append(out.duplicates, incoming)
			continue
		}
		if conflict, ok := conflictByStableFields(incoming, existing); ok {
			out.conflicts = append(out.conflicts, conflict)
			continue
		}
		out.importable = append(out.importable, incoming)
	}
	return out
}

const syntheticTagKey = "synthetic"

func residualAlreadyImported(incoming ParsedTransaction, existing []existingTx) bool {
	residualStr := incoming.Raw["synthetic_residual"]
	if residualStr == "" {
		residualStr = incoming.Amount.String()
	}
	residual, err := decimal.NewFromString(residualStr)
	if err != nil {
		return false
	}
	// Only consider non-synthetic existing rows when computing the cover.
	real := make([]existingTx, 0, len(existing))
	for _, e := range existing {
		// Existing rows don't carry the synthetic flag in our struct yet;
		// treat all loaded rows as real here. (They're filtered in
		// loadExisting via raw->>'synthetic' once we add that filter in
		// Task 10.)
		real = append(real, e)
	}
	return residualExplainedByExisting(incoming.BookedAt, incoming.Currency, residual, real)
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/bankimport/ -run TestClassifySkipsSyntheticWhenExplained -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/bankimport/service.go backend/internal/bankimport/service_dedup_test.go
git commit -m "bankimport: skip synthetic reconcile rows when residual already imported"
```

### Task 10: Tag synthetic rows in DB and exclude from real-row matching

**Files:**
- Modify: `backend/internal/bankimport/service.go` (insertImportableTx, loadExisting)

- [ ] **Step 1: Persist the synthetic tag on insert**

In `insertImportableTx`, before the `transactions` insert, decide whether the row is synthetic and stamp the row's `raw` with `"synthetic": "balance_reconcile"`. The parser already populates this; we just need to ensure it survives the JSON marshal — `json.Marshal(incoming.Raw)` already preserves all keys, so no code change is required *here*. Add a comment:

```go
// incoming.Raw includes "synthetic" = "balance_reconcile" for parser-emitted
// reconcile rows. This is what loadExisting filters on (see Task 11).
```

- [ ] **Step 2: Add a synthetic flag to `existingTx` and load it**

In `service.go`:

```go
type existingTx struct {
	ID          uuid.UUID
	BookedAt    time.Time
	PostedAt    *time.Time
	Amount      decimal.Decimal
	Currency    string
	Description string
	SourceID    *string
	Synthetic   bool
}
```

Update `loadExisting`:

```sql
select t.id, t.booked_at, t.posted_at, t.amount::text, t.currency,
       coalesce(t.description, t.counterparty_raw, ''),
       sr.external_id,
       coalesce(t.raw->>'synthetic' = 'balance_reconcile', false)
```

And the scan:

```go
if err := rows.Scan(&e.ID, &e.BookedAt, &e.PostedAt, &amount, &e.Currency, &e.Description, &e.SourceID, &e.Synthetic); err != nil {
```

- [ ] **Step 3: Filter in `residualAlreadyImported`**

Replace the `real := …` loop body to skip synthetic rows:

```go
for _, e := range existing {
    if e.Synthetic {
        continue
    }
    real = append(real, e)
}
```

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/bankimport/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/bankimport/service.go
git commit -m "bankimport: load synthetic flag from transactions.raw and exclude from cover match"
```

### Task 11: Direction B — retire stale synthetics after apply

**Files:**
- Modify: `backend/internal/bankimport/service.go`

- [ ] **Step 1: Implement the retire query**

Append to `service.go`:

```go
// retireExplainedSynthetics scans synthetic balance-reconcile rows in the
// destination account that fall in the imported file's date range, and
// voids any whose residual is now covered by real (non-synthetic) rows
// within ±7d. Called once per affected account after the apply transaction
// commits inserts. Voiding (status='voided') keeps the row visible in
// audit history while removing it from the running balance.
func (s *Service) retireExplainedSynthetics(ctx context.Context, tx importTx, workspaceID, accountID uuid.UUID, dateFrom, dateTo time.Time) error {
	rows, err := tx.Query(ctx, `
		select t.id, t.booked_at, t.posted_at, t.amount::text, t.currency,
		       coalesce(t.raw->>'synthetic_residual', t.amount::text)
		from transactions t
		where t.workspace_id = $1
		  and t.account_id = $2
		  and t.status = 'posted'
		  and t.raw->>'synthetic' = 'balance_reconcile'
		  and t.booked_at between $3 - interval '7 days' and $4 + interval '7 days'
	`, workspaceID, accountID, dateFrom, dateTo)
	if err != nil {
		return fmt.Errorf("scan synthetic rows: %w", err)
	}
	defer rows.Close()
	type synthCandidate struct {
		id       uuid.UUID
		bookedAt time.Time
		currency string
		residual decimal.Decimal
	}
	var candidates []synthCandidate
	for rows.Next() {
		var c synthCandidate
		var amount, residualStr string
		var posted *time.Time
		if err := rows.Scan(&c.id, &c.bookedAt, &posted, &amount, &c.currency, &residualStr); err != nil {
			return fmt.Errorf("scan synthetic row: %w", err)
		}
		residual, err := decimal.NewFromString(residualStr)
		if err != nil {
			continue
		}
		c.residual = residual
		candidates = append(candidates, c)
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(candidates) == 0 {
		return nil
	}
	// For each candidate, load real rows in its ±7d window and re-run the
	// residual-explained check. Void any that the new world now explains.
	for _, c := range candidates {
		from := c.bookedAt.Add(-time.Duration(reviewDedupDays) * 24 * time.Hour)
		to := c.bookedAt.Add(time.Duration(reviewDedupDays) * 24 * time.Hour)
		realRows, err := tx.Query(ctx, `
			select id, booked_at, posted_at, amount::text, currency,
			       coalesce(description, counterparty_raw, '')
			from transactions
			where workspace_id = $1
			  and account_id = $2
			  and status = 'posted'
			  and currency = $3
			  and booked_at between $4 and $5
			  and coalesce(raw->>'synthetic', '') <> 'balance_reconcile'
			  and id <> $6
		`, workspaceID, accountID, c.currency, from, to, c.id)
		if err != nil {
			return fmt.Errorf("scan real rows for synthetic: %w", err)
		}
		var existing []existingTx
		for realRows.Next() {
			var e existingTx
			var amount string
			if err := realRows.Scan(&e.ID, &e.BookedAt, &e.PostedAt, &amount, &e.Currency, &e.Description); err != nil {
				realRows.Close()
				return err
			}
			d, err := decimal.NewFromString(amount)
			if err != nil {
				continue
			}
			e.Amount = d
			existing = append(existing, e)
		}
		realRows.Close()
		if !residualExplainedByExisting(c.bookedAt, c.currency, c.residual, existing) {
			continue
		}
		if _, err := tx.Exec(ctx, `update transactions set status = 'voided' where id = $1 and workspace_id = $2`, c.id, workspaceID); err != nil {
			return fmt.Errorf("void synthetic %s: %w", c.id, err)
		}
	}
	return nil
}
```

- [ ] **Step 2: Call it from `applyPlan` after each section's inserts**

In the apply loop, after `insertImportableTx`, add:

```go
if group.parsed.DateFrom != nil && group.parsed.DateTo != nil {
    if err := s.retireExplainedSynthetics(ctx, tx, workspaceID, accountID, *group.parsed.DateFrom, *group.parsed.DateTo); err != nil {
        return nil, err
    }
}
```

- [ ] **Step 3: Run all tests**

Run: `go test ./internal/bankimport/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add backend/internal/bankimport/service.go
git commit -m "bankimport: retire stale synthetics post-apply when real rows now explain them"
```

### Task 12: Integration test — end-to-end interchange in both directions

**Files:**
- Create: `backend/internal/bankimport/service_dedup_test.go` (append)

- [ ] **Step 1: Add a test that exercises classify across both orderings**

Append to `service_dedup_test.go`:

```go
func TestCrossFormatInterchange(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }
	desc := "Amazon"

	// Banking row (auth=Dec 10, settle=Dec 16) already in the account.
	bankingExisting := []existingTx{{
		ID:          uuid.New(),
		BookedAt:    day("2025-12-10"),
		PostedAt:    postedPtr("2025-12-16"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: "Amazon",
	}}

	// Consolidated v2 row for the same Amazon transaction.
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

	t.Run("consolidated first, banking second — same dedup direction", func(t *testing.T) {
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
		// 6-day gap: outside auto window, inside review window -> conflict.
		if len(got.conflicts) != 1 {
			t.Fatalf("want 1 conflict (date_drift); got dup=%d imp=%d conflict=%d", len(got.duplicates), len(got.importable), len(got.conflicts))
		}
		if got.conflicts[0].Reason != "date_drift" {
			t.Fatalf("conflict reason = %q, want date_drift", got.conflicts[0].Reason)
		}
	})
}
```

- [ ] **Step 2: Run all tests**

Run: `go test ./internal/bankimport/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add backend/internal/bankimport/service_dedup_test.go
git commit -m "bankimport: end-to-end test for cross-format interchange in both directions"
```

---

## Validation Checklist

After all tasks are complete, run an end-to-end validation against the user's actual files (don't ship without this):

1. Fresh local DB / test workspace.
2. Import `/Users/xmedavid/Downloads/consolidated-statement-v2_2019-04-18_2026-04-25_en_8d7c14.csv` into a CHF account. Confirm the account closes at **0 CHF** (synthetic rows fill the 678.50 gap).
3. Without resetting, import `/Users/xmedavid/dev/folio/legacy/data/account-statement.csv` into the same account. Confirm:
   - Most banking rows show as auto-dedupes (the ones inside ±1d of consolidated rows).
   - Some Amazon-like settle/auth pairs may surface in the date_drift review queue.
   - At least one synthetic row gets retired (look for `status = 'voided'` rows tagged `raw->>'synthetic' = 'balance_reconcile'`).
   - Final CHF balance still **0**.
4. Reset DB. Import in opposite order (banking first, then consolidated). Confirm:
   - Banking import gives a complete, balanced CHF account.
   - Consolidated import auto-skips synthetic rows whose residual is already covered.
   - Final CHF balance still **0**.
5. Run the existing `TestParseRevolutConsolidated*` and `TestParseRevolutBanking*` tests — must all pass.
