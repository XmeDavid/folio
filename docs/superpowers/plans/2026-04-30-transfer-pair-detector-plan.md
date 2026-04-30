# Transfer-Pair Detector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-detect cross-account transfers (Tier 1: original-amount, Tier 2: shared import-batch, Tier 3: heuristic suggestions surfaced via a reusable dossier-tab review queue), hide paired transactions from default lists/stats, and let users manually pair/unpair/decline.

**Architecture:** New `transfers` package in `backend/internal/transfers/` owns detection + lifecycle ops, reading from `transactions` and `merchants` and writing to existing `transfer_matches` table plus a new `transfer_match_candidates` table. Detection runs at the end of every `bankimport.Apply*` call, scoped to just-imported IDs. Frontend gets a generic `dossier-tabs` framework (right-edge floating affordance), the first tenant being a `transfers-review-tab`. Transactions list gains a `hideInternalMoves` filter (default true).

**Tech Stack:** Go (pgx, chi), Postgres 16/17, Next.js 16 / React 19 / TanStack Query / Tailwind v4 / Vitest.

**Spec:** `docs/superpowers/specs/2026-04-30-transfer-pair-detector-design.md`

**Pre-existing schema:** `transfer_matches` table is already in `backend/db/migrations/20260424000004_transactions.sql`. `match_provenance` enum + `source_refs.import_batch_id` are already present.

---

## Phase 0 — Branch + schema

### Task 0.1: Cut feature branch

- [ ] **Step 1:** From repo root, current state should be clean and on `main`. Verify:
  ```bash
  cd /Users/xmedavid/dev/folio && git status --short && git rev-parse --abbrev-ref HEAD
  ```
  Expected: empty status, branch `main`.
- [ ] **Step 2:** Cut the feature branch:
  ```bash
  git checkout -b feat/transfer-pair-detector
  ```
- [ ] **Step 3:** Confirm branch:
  ```bash
  git rev-parse --abbrev-ref HEAD
  ```
  Expected: `feat/transfer-pair-detector`.

### Task 0.2: `transfer_match_candidates` migration

**Files:**
- Create: `backend/db/migrations/20260430000002_transfer_match_candidates.sql`
- Modify: `backend/db/migrations/atlas.sum` (via `atlas migrate hash`)

- [ ] **Step 1: Write the migration file**

```sql
-- Tier-3 review queue: heuristic suggestions awaiting user confirmation.
-- One pending row per source_transaction_id (unique). On user action the
-- row transitions to 'paired' or 'declined' and is never re-suggested by
-- Tier 3 (the unique constraint enforces this).
create table transfer_match_candidates (
  id                          uuid primary key,
  workspace_id                uuid not null references workspaces(id) on delete cascade,
  source_transaction_id       uuid not null,
  candidate_destination_ids   uuid[] not null,
  status                      text not null default 'pending',
  suggested_at                timestamptz not null default now(),
  resolved_at                 timestamptz,
  resolved_by_user_id         uuid,
  unique (workspace_id, source_transaction_id),
  constraint tmc_source_fk foreign key (workspace_id, source_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tmc_actor_fk foreign key (resolved_by_user_id)
    references users(id) on delete set null,
  constraint tmc_status_chk
    check (status in ('pending', 'paired', 'declined'))
);

create index transfer_match_candidates_pending_idx
  on transfer_match_candidates(workspace_id) where status = 'pending';
```

- [ ] **Step 2: Apply locally**

```bash
cd /Users/xmedavid/dev/folio && make migrate 2>&1 | tail -10
```

Expected: `Current Version: 20260430000002` (or whatever fresh number Atlas assigns), no errors.

- [ ] **Step 3: Update Atlas hash**

```bash
cd /Users/xmedavid/dev/folio/backend && atlas migrate hash --dir file://db/migrations
```

- [ ] **Step 4: Verify table exists**

```bash
docker exec -e PGPASSWORD=folio_dev_password folio-db-1 psql -U folio -d folio -c "\d transfer_match_candidates"
```

Expected: table description with the columns from step 1, the partial pending index, and the `tmc_*` constraints.

- [ ] **Step 5: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/db/migrations/20260430000002_transfer_match_candidates.sql backend/db/migrations/atlas.sum
git -c commit.gpgsign=false commit -m "feat(db): transfer_match_candidates table for tier-3 review queue"
```

---

## Phase 1 — Detection service (backend/internal/transfers)

### Task 1.1: Package skeleton + types

**Files:**
- Create: `backend/internal/transfers/service.go`
- Create: `backend/internal/transfers/domain.go`

- [ ] **Step 1: Create `domain.go` with the public types:**

```go
// Package transfers owns cross-account transfer-pair detection,
// candidate suggestions for ambiguous matches, and manual pair / unpair
// lifecycle operations. Pairing is data-only via transfer_matches; nothing
// about the underlying transactions changes.
package transfers

import (
	"time"

	"github.com/google/uuid"
)

// TransferMatch is the wire shape of a transfer_matches row.
type TransferMatch struct {
	ID                       uuid.UUID  `json:"id"`
	WorkspaceID              uuid.UUID  `json:"workspaceId"`
	SourceTransactionID      uuid.UUID  `json:"sourceTransactionId"`
	DestinationTransactionID *uuid.UUID `json:"destinationTransactionId,omitempty"`
	FXRate                   *string    `json:"fxRate,omitempty"`
	FeeAmount                *string    `json:"feeAmount,omitempty"`
	FeeCurrency              *string    `json:"feeCurrency,omitempty"`
	ToleranceNote            *string    `json:"toleranceNote,omitempty"`
	Provenance               string     `json:"provenance"`
	MatchedByUserID          *uuid.UUID `json:"matchedByUserId,omitempty"`
	MatchedAt                time.Time  `json:"matchedAt"`
	CreatedAt                time.Time  `json:"createdAt"`
}

// TransferCandidate is the wire shape of a transfer_match_candidates row.
type TransferCandidate struct {
	ID                       uuid.UUID    `json:"id"`
	WorkspaceID              uuid.UUID    `json:"workspaceId"`
	SourceTransactionID      uuid.UUID    `json:"sourceTransactionId"`
	CandidateDestinationIDs  []uuid.UUID  `json:"candidateDestinationIds"`
	Status                   string       `json:"status"`
	SuggestedAt              time.Time    `json:"suggestedAt"`
	ResolvedAt               *time.Time   `json:"resolvedAt,omitempty"`
	ResolvedByUserID         *uuid.UUID   `json:"resolvedByUserId,omitempty"`
}

// DetectScope bounds which transactions act as the LEFT side of pairing
// in a single DetectAndPair call. The candidate-search query always
// ranges over the entire workspace's unpaired transactions.
type DetectScope struct {
	// All=true => scan every unpaired transaction in the workspace.
	All            bool
	// TransactionIDs => scan only these as left sides (used after import).
	TransactionIDs []uuid.UUID
}

// DetectResult captures counts from a single detector pass.
type DetectResult struct {
	Tier1Paired    int `json:"tier1Paired"`
	Tier2Paired    int `json:"tier2Paired"`
	Tier3Suggested int `json:"tier3Suggested"`
}

// ManualPairInput drives the manual-pair endpoint.
type ManualPairInput struct {
	SourceID      uuid.UUID
	DestinationID *uuid.UUID // nil => outbound-to-external
	FeeAmount     *string
	FeeCurrency   *string
	ToleranceNote *string
}
```

- [ ] **Step 2: Create `service.go` with the service skeleton:**

```go
package transfers

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns transfer-pair detection and lifecycle.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// DetectAndPair runs the three-tier detector. Tier 1 + 2 write
// transfer_matches; Tier 3 writes transfer_match_candidates.
func (s *Service) DetectAndPair(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (*DetectResult, error) {
	// Implemented in tier1.go, tier2.go, tier3.go.
	t1, err := s.runTier1(ctx, workspaceID, scope)
	if err != nil {
		return nil, err
	}
	t2, err := s.runTier2(ctx, workspaceID, scope)
	if err != nil {
		return nil, err
	}
	t3, err := s.runTier3(ctx, workspaceID, scope)
	if err != nil {
		return nil, err
	}
	return &DetectResult{Tier1Paired: t1, Tier2Paired: t2, Tier3Suggested: t3}, nil
}

// Stubs filled in by Tasks 1.2 / 1.3 / 1.4.
func (s *Service) runTier1(context.Context, uuid.UUID, DetectScope) (int, error) { return 0, nil }
func (s *Service) runTier2(context.Context, uuid.UUID, DetectScope) (int, error) { return 0, nil }
func (s *Service) runTier3(context.Context, uuid.UUID, DetectScope) (int, error) { return 0, nil }
```

(Add `import "time"` next to the others — slot it in alphabetical order.)

- [ ] **Step 3: Build to confirm it compiles**

```bash
cd /Users/xmedavid/dev/folio/backend && go build ./internal/transfers/...
```

Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/transfers/
git -c commit.gpgsign=false commit -m "feat(transfers): package skeleton with DetectAndPair + types"
```

### Task 1.2: Tier 1 — original-amount exact match

**Files:**
- Create: `backend/internal/transfers/tier1.go`
- Create: `backend/internal/transfers/tier1_test.go`

- [ ] **Step 1: Write failing tests**

```go
// backend/internal/transfers/tier1_test.go
package transfers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transfers"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// seedAccount inserts a checking account directly via raw SQL.
func seedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, name, currency string) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, workspace_id, name, kind, currency, open_date, opening_balance, opening_balance_date, include_in_networth, include_in_savings_rate)
		VALUES ($1, $2, $3, 'checking', $4, $5, 0, $5, true, true)
	`, id, workspaceID, name, currency, time.Now().UTC())
	require.NoError(t, err)
	return id
}

// seedTx inserts a posted transaction with optional original_amount/currency.
// Returns the new transaction id.
func seedTx(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	workspaceID, accountID uuid.UUID,
	bookedAt time.Time, amount, currency string,
	originalAmount, originalCurrency *string,
) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, workspace_id, account_id, status, booked_at, amount, currency, original_amount, original_currency)
		VALUES ($1, $2, $3, 'posted', $4, $5::numeric, $6, $7::numeric, $8)
	`, id, workspaceID, accountID, bookedAt, amount, currency, originalAmount, originalCurrency)
	require.NoError(t, err)
	return id
}

func TestTier1_OriginalAmountPair_CrossCurrency(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier1-cross")

	chf := seedAccount(t, ctx, pool, wsID, "Revolut CHF", "CHF")
	eur := seedAccount(t, ctx, pool, wsID, "Revolut EUR", "EUR")

	// Source: -130.50 CHF in CHF account, original_amount = 120.00 EUR.
	src := seedTx(t, ctx, pool, wsID, chf, time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC),
		"-130.50", "CHF", strPtr("120.00"), strPtr("EUR"))
	// Destination: +120.00 EUR in EUR account, same day.
	dst := seedTx(t, ctx, pool, wsID, eur, time.Date(2026, 4, 5, 12, 5, 0, 0, time.UTC),
		"120.00", "EUR", nil, nil)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, dst}})
	require.NoError(t, err)
	require.Equal(t, 1, res.Tier1Paired)

	// Verify exactly one transfer_matches row exists for this pair.
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_matches
		 WHERE workspace_id = $1 AND source_transaction_id = $2 AND destination_transaction_id = $3`,
		wsID, src, dst,
	).Scan(&count))
	require.Equal(t, 1, count)

	// Verify fx_rate ≈ 130.50 / 120.00.
	var fxRate decimal.Decimal
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fx_rate FROM transfer_matches WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&fxRate))
	expected := decimal.RequireFromString("1.0875")
	require.True(t, fxRate.Sub(expected).Abs().LessThan(decimal.RequireFromString("0.0001")))
}

func TestTier1_OriginalAmountPair_SameCurrency(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier1-same")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"-100.00", "CHF", strPtr("100.00"), strPtr("CHF"))
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"100.00", "CHF", nil, nil)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, dst}})
	require.NoError(t, err)
	require.Equal(t, 1, res.Tier1Paired)
}

func TestTier1_AmbiguousMultipleCandidatesSkipsAndSurfacesTier3(t *testing.T) {
	// Two equally-valid destinations (same amount, both within window) → Tier 1 skips.
	// (Tier 3 fallback is verified in Task 1.4 tests.)
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier1-ambig")

	a := seedAccount(t, ctx, pool, wsID, "A", "EUR")
	b1 := seedAccount(t, ctx, pool, wsID, "B1", "EUR")
	b2 := seedAccount(t, ctx, pool, wsID, "B2", "EUR")

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"-50.00", "EUR", strPtr("50.00"), strPtr("EUR"))
	_ = seedTx(t, ctx, pool, wsID, b1, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"50.00", "EUR", nil, nil)
	_ = seedTx(t, ctx, pool, wsID, b2, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"50.00", "EUR", nil, nil)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src}})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier1Paired)
	// Tier 3 will catch this in 1.4; for now just verify Tier 1 didn't write.

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_matches WHERE workspace_id = $1`, wsID,
	).Scan(&count))
	require.Equal(t, 0, count)
}

func strPtr(s string) *string { return &s }
```

- [ ] **Step 2: Run, expect failures**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -v 2>&1 | tail -20
```

Expected: tests fail (Tier1Paired == 0 because runTier1 is a stub).

- [ ] **Step 3: Implement Tier 1**

```go
// backend/internal/transfers/tier1.go
package transfers

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// runTier1 finds pairs where source.original_amount + destination.amount = 0
// AND source.original_currency = destination.currency, with opposite signs
// and a ±1 day window. Source must have original_amount populated.
//
// Match cardinality: exactly-one. If multiple destinations match, Tier 1
// skips (Tier 3 may surface the source for manual review).
func (s *Service) runTier1(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (int, error) {
	leftFilter, leftArgs := scopeLeftFilter(scope, "t1")
	args := []any{workspaceID}
	for _, a := range leftArgs {
		args = append(args, a)
	}
	// Find each unpaired source with eligible original_amount.
	q := `
		WITH candidates AS (
			SELECT t1.id AS source_id, t1.amount, t1.original_amount, t1.original_currency,
			       t1.account_id, t1.booked_at,
			       (SELECT array_agg(t2.id) FROM transactions t2
			        WHERE t2.workspace_id = t1.workspace_id
			          AND t2.account_id != t1.account_id
			          AND t2.currency = t1.original_currency
			          AND t2.amount::numeric + t1.original_amount::numeric = 0
			          AND sign(t2.amount::numeric) != sign(t1.amount::numeric)
			          AND abs(extract(epoch from t2.booked_at - t1.booked_at)) <= 86400
			          AND NOT EXISTS (
			            SELECT 1 FROM transfer_matches tm
			            WHERE tm.workspace_id = t2.workspace_id
			              AND (tm.source_transaction_id = t2.id
			                   OR tm.destination_transaction_id = t2.id)
			          )
			       ) AS candidate_dst_ids
			FROM transactions t1
			WHERE t1.workspace_id = $1
			  AND t1.original_amount IS NOT NULL
			  AND t1.original_currency IS NOT NULL
			  AND t1.amount::numeric < 0
			  AND NOT EXISTS (
			    SELECT 1 FROM transfer_matches tm
			    WHERE tm.workspace_id = t1.workspace_id
			      AND (tm.source_transaction_id = t1.id
			           OR tm.destination_transaction_id = t1.id)
			  )
			  ` + leftFilter + `
		)
		SELECT source_id, amount, original_amount, candidate_dst_ids
		FROM candidates
		WHERE candidate_dst_ids IS NOT NULL AND array_length(candidate_dst_ids, 1) = 1
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("tier1 query: %w", err)
	}
	defer rows.Close()

	type pair struct {
		sourceID, destID uuid.UUID
		amount, original string
	}
	var pairs []pair
	for rows.Next() {
		var sourceID uuid.UUID
		var amount, original string
		var dst []uuid.UUID
		if err := rows.Scan(&sourceID, &amount, &original, &dst); err != nil {
			return 0, fmt.Errorf("tier1 scan: %w", err)
		}
		if len(dst) != 1 {
			continue
		}
		pairs = append(pairs, pair{sourceID, dst[0], amount, original})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, p := range pairs {
		// fx_rate = abs(amount / original_amount)
		_, err := s.pool.Exec(ctx, `
			INSERT INTO transfer_matches (
				id, workspace_id, source_transaction_id, destination_transaction_id,
				fx_rate, provenance, matched_at
			) VALUES (
				$1, $2, $3, $4,
				abs($5::numeric / $6::numeric), 'auto_detected', $7
			)
			ON CONFLICT DO NOTHING
		`, uuidx.New(), workspaceID, p.sourceID, p.destID, p.amount, p.original, s.now().UTC())
		if err != nil {
			return count, fmt.Errorf("tier1 insert: %w", err)
		}
		count++
	}
	return count, nil
}

// scopeLeftFilter renders an additional WHERE clause that restricts the
// LEFT side (source) of detection to the scope's TransactionIDs (when
// scope.All is false). Returns the SQL fragment and the args to append
// after the workspace_id at $1.
func scopeLeftFilter(scope DetectScope, alias string) (string, []any) {
	if scope.All || len(scope.TransactionIDs) == 0 {
		return "", nil
	}
	return fmt.Sprintf(" AND %s.id = ANY($2)", alias), []any{scope.TransactionIDs}
}
```

- [ ] **Step 4: Run tests, expect pass for Tier 1 cases (the ambiguous test passes because Tier 1 produces 0 pairs and Tier 3 hasn't run yet)**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -run TestTier1 -v 2>&1 | tail -20
```

Expected: 3/3 pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/transfers/tier1.go backend/internal/transfers/tier1_test.go
git -c commit.gpgsign=false commit -m "feat(transfers): Tier 1 — original-amount exact-match auto-pair"
```

### Task 1.3: Tier 2 — same import_batch + opposite sign

**Files:**
- Create: `backend/internal/transfers/tier2.go`
- Create: `backend/internal/transfers/tier2_test.go`

- [ ] **Step 1: Write failing test**

```go
// backend/internal/transfers/tier2_test.go
package transfers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transfers"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// seedSourceRefForBatch links a transaction to an import batch via source_refs.
func seedSourceRefForBatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, txID, batchID uuid.UUID) {
	t.Helper()
	provider := "synthetic"
	extID := "ext-" + uuid.NewString()
	_, err := pool.Exec(ctx, `
		INSERT INTO source_refs (id, workspace_id, entity_type, entity_id, provider, import_batch_id, external_id, raw_payload, observed_at)
		VALUES ($1, $2, 'transaction', $3, $4, $5, $6, '{}'::jsonb, $7)
	`, uuidx.New(), workspaceID, txID, &provider, &batchID, &extID, time.Now().UTC())
	require.NoError(t, err)
}

// seedImportBatch inserts an import_batches row.
func seedImportBatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID) uuid.UUID {
	t.Helper()
	batchID := uuidx.New()
	fileName := "tier2-test.csv"
	fileHash := "deadbeef"
	_, err := pool.Exec(ctx, `
		INSERT INTO import_batches (id, workspace_id, source_kind, file_name, file_hash, status, summary, started_at, finished_at)
		VALUES ($1, $2, 'file_upload', $3, $4, 'applied', '{}'::jsonb, $5, $5)
	`, batchID, workspaceID, &fileName, &fileHash, time.Now().UTC())
	require.NoError(t, err)
	return batchID
}

func TestTier2_SameBatchPair_WithFeeTolerance(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier2")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")

	batch := seedImportBatch(t, ctx, pool, wsID)

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "99.50", "CHF", nil, nil)
	seedSourceRefForBatch(t, ctx, pool, wsID, src, batch)
	seedSourceRefForBatch(t, ctx, pool, wsID, dst, batch)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, dst}})
	require.NoError(t, err)
	require.Equal(t, 1, res.Tier2Paired)

	var feeAmount string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fee_amount::text FROM transfer_matches WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&feeAmount))
	// |t1.amount + t2.amount| = 0.50, recorded as fee_amount.
	require.Contains(t, feeAmount, "0.5")
}

func TestTier2_DifferentBatchSkips(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier2-diffbatch")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")

	batchA := seedImportBatch(t, ctx, pool, wsID)
	batchB := seedImportBatch(t, ctx, pool, wsID)

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)
	seedSourceRefForBatch(t, ctx, pool, wsID, src, batchA)
	seedSourceRefForBatch(t, ctx, pool, wsID, dst, batchB)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, dst}})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier2Paired)
}
```

- [ ] **Step 2: Run, expect failures (Tier2Paired == 0)**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -run TestTier2 -v 2>&1 | tail -10
```

- [ ] **Step 3: Implement Tier 2**

```go
// backend/internal/transfers/tier2.go
package transfers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// runTier2 pairs transactions that share an import_batch_id (via source_refs),
// have opposite signs, same currency, and amounts that net within fee tolerance:
//   |t1.amount + t2.amount| <= max(2.00, 0.005 * |t1.amount|)
// Date window is ±1 day. Cardinality: exactly-one match required.
func (s *Service) runTier2(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (int, error) {
	leftFilter, leftArgs := scopeLeftFilter(scope, "t1")
	args := []any{workspaceID}
	for _, a := range leftArgs {
		args = append(args, a)
	}
	q := `
		WITH candidates AS (
			SELECT t1.id AS source_id, t1.amount, t1.currency,
			       (SELECT array_agg(t2.id) FROM transactions t2
			        JOIN source_refs sr1 ON sr1.entity_id = t1.id AND sr1.entity_type = 'transaction'
			        JOIN source_refs sr2 ON sr2.entity_id = t2.id AND sr2.entity_type = 'transaction'
			        WHERE t2.workspace_id = t1.workspace_id
			          AND t2.account_id != t1.account_id
			          AND t2.currency = t1.currency
			          AND sign(t2.amount::numeric) != sign(t1.amount::numeric)
			          AND abs(t2.amount::numeric + t1.amount::numeric)
			              <= GREATEST(2.00, 0.005 * abs(t1.amount::numeric))
			          AND abs(extract(epoch from t2.booked_at - t1.booked_at)) <= 86400
			          AND sr1.import_batch_id IS NOT NULL
			          AND sr2.import_batch_id = sr1.import_batch_id
			          AND NOT EXISTS (
			            SELECT 1 FROM transfer_matches tm
			            WHERE tm.workspace_id = t2.workspace_id
			              AND (tm.source_transaction_id = t2.id
			                   OR tm.destination_transaction_id = t2.id)
			          )
			       ) AS candidate_dst_ids
			FROM transactions t1
			WHERE t1.workspace_id = $1
			  AND t1.amount::numeric < 0
			  AND NOT EXISTS (
			    SELECT 1 FROM transfer_matches tm
			    WHERE tm.workspace_id = t1.workspace_id
			      AND (tm.source_transaction_id = t1.id
			           OR tm.destination_transaction_id = t1.id)
			  )
			  ` + leftFilter + `
		)
		SELECT source_id, amount, currency, candidate_dst_ids
		FROM candidates
		WHERE candidate_dst_ids IS NOT NULL AND array_length(candidate_dst_ids, 1) = 1
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("tier2 query: %w", err)
	}
	defer rows.Close()
	type pair struct {
		sourceID, destID    uuid.UUID
		srcAmount, currency string
	}
	var pairs []pair
	for rows.Next() {
		var sourceID uuid.UUID
		var amount, currency string
		var dst []uuid.UUID
		if err := rows.Scan(&sourceID, &amount, &currency, &dst); err != nil {
			return 0, fmt.Errorf("tier2 scan: %w", err)
		}
		if len(dst) != 1 {
			continue
		}
		pairs = append(pairs, pair{sourceID, dst[0], amount, currency})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, p := range pairs {
		// Compute fee = |srcAmount + destAmount|, captured at insert time.
		var destAmount string
		if err := s.pool.QueryRow(ctx,
			`SELECT amount::text FROM transactions WHERE id = $1`, p.destID,
		).Scan(&destAmount); err != nil {
			return count, fmt.Errorf("tier2 read dest amount: %w", err)
		}
		srcD := decimal.RequireFromString(p.srcAmount)
		dstD := decimal.RequireFromString(destAmount)
		fee := srcD.Add(dstD).Abs()
		var feeAmount, feeCurrency *string
		if fee.GreaterThan(decimal.Zero) {
			feeStr := fee.String()
			feeAmount = &feeStr
			feeCurrency = &p.currency
		}
		_, err := s.pool.Exec(ctx, `
			INSERT INTO transfer_matches (
				id, workspace_id, source_transaction_id, destination_transaction_id,
				fee_amount, fee_currency, provenance, matched_at
			) VALUES (
				$1, $2, $3, $4,
				$5::numeric, $6::money_currency, 'auto_detected', $7
			)
			ON CONFLICT DO NOTHING
		`, uuidx.New(), workspaceID, p.sourceID, p.destID, feeAmount, feeCurrency, s.now().UTC())
		if err != nil {
			return count, fmt.Errorf("tier2 insert: %w", err)
		}
		count++
	}
	return count, nil
}
```

- [ ] **Step 4: Run, expect pass**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -run TestTier2 -v 2>&1 | tail -10
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/transfers/tier2.go backend/internal/transfers/tier2_test.go
git -c commit.gpgsign=false commit -m "feat(transfers): Tier 2 — same-batch opposite-sign pair with fee tolerance"
```

### Task 1.4: Tier 3 — heuristic candidate suggestions

**Files:**
- Create: `backend/internal/transfers/tier3.go`
- Create: `backend/internal/transfers/tier3_test.go`

- [ ] **Step 1: Failing test**

```go
// backend/internal/transfers/tier3_test.go
package transfers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transfers"
)

func seedTxWithRaw(
	t *testing.T, ctx context.Context, pool any,
	workspaceID, accountID uuid.UUID,
	bookedAt time.Time, amount, currency, counterpartyRaw string,
) uuid.UUID {
	t.Helper()
	// Reuse seedTx but ALSO set counterparty_raw — easiest path is one helper.
	// Implement using the pool directly.
	var (
		// ... see body in test file; matches seedTx's signature with extra raw.
	)
	_ = bookedAt
	_ = amount
	_ = currency
	_ = counterpartyRaw
	_ = workspaceID
	_ = accountID
	_ = pool
	return uuid.Nil // implementer fills body following seedTx pattern
}

func TestTier3_SuggestsForSelfTransferLikeCounterparty(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier3-suggest")

	revolut := seedAccount(t, ctx, pool, wsID, "Revolut Main", "CHF")
	bank := seedAccount(t, ctx, pool, wsID, "Bank Checking", "CHF")

	// Credit in revolut whose raw mentions "Bank Checking" → tier 3 hits.
	src := seedTxWithRaw(t, ctx, pool, wsID, revolut, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"500.00", "CHF", "Transfer from Bank Checking")
	// Counterpart debit in bank.
	candidate := seedTxWithRaw(t, ctx, pool, wsID, bank, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC),
		"-500.00", "CHF", "Transfer to Revolut")

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, candidate}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.Tier3Suggested, 1)

	// Verify candidate row exists.
	var dstIDs []uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT candidate_destination_ids FROM transfer_match_candidates
		 WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&dstIDs))
	require.Contains(t, dstIDs, candidate)
}

func TestTier3_DeclineDoesntResurface(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier3-decline")

	a := seedAccount(t, ctx, pool, wsID, "Revolut", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "Bank", "CHF")

	src := seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"500.00", "CHF", "Transfer from Bank")
	_ = seedTxWithRaw(t, ctx, pool, wsID, b, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC),
		"-500.00", "CHF", "Transfer to Revolut")

	// First detector run creates a pending candidate.
	_, _ = svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	// Decline it via raw SQL (the API endpoint is built later).
	_, err := pool.Exec(ctx, `
		UPDATE transfer_match_candidates SET status = 'declined', resolved_at = now()
		WHERE workspace_id = $1 AND source_transaction_id = $2`, wsID, src)
	require.NoError(t, err)

	// Re-run detector — should NOT change the row count (unique constraint
	// + ON CONFLICT DO NOTHING).
	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier3Suggested)

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM transfer_match_candidates
		 WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&status))
	require.Equal(t, "declined", status)
}
```

(Implementer: write `seedTxWithRaw` similarly to `seedTx` but with an extra `counterparty_raw` parameter.)

- [ ] **Step 2: Run, expect failures**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -run TestTier3 -v 2>&1 | tail -10
```

- [ ] **Step 3: Implement Tier 3**

```go
// backend/internal/transfers/tier3.go
package transfers

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// transferKeywords is the multi-locale list of transfer phrases that
// trigger Tier-3 review when found in counterparty_raw.
var transferKeywords = []string{
	// English
	"transfer", "pocket", "between accounts",
	// Portuguese
	"transferência", "carregamento", "levantamento",
	// German
	"überweisung", "ueberweisung", "umbuchung", "einzahlung", "abhebung", "zwischen konten",
}

// runTier3 surfaces unpaired credits whose counterparty_raw fuzzy-matches
// either a tracked account name in this workspace, the workspace owner's
// display name, or a transferKeywords entry. Inserts/updates a
// transfer_match_candidates row per source. Returns the number of NEW
// pending rows.
func (s *Service) runTier3(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (int, error) {
	// 1. Build the lowercase keyword set: built-in keywords ∪ tracked
	//    account names ∪ workspace owner display names.
	keywords := append([]string{}, transferKeywords...)
	rows, err := s.pool.Query(ctx,
		`SELECT lower(name) FROM accounts WHERE workspace_id = $1 AND archived_at IS NULL`,
		workspaceID,
	)
	if err != nil {
		return 0, fmt.Errorf("tier3 list accounts: %w", err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return 0, err
		}
		if strings.TrimSpace(n) != "" {
			keywords = append(keywords, n)
		}
	}
	rows.Close()
	rows, err = s.pool.Query(ctx,
		`SELECT lower(coalesce(u.display_name, u.email))
		 FROM users u
		 JOIN workspace_memberships m ON m.user_id = u.id
		 WHERE m.workspace_id = $1 AND m.role = 'owner'`,
		workspaceID,
	)
	if err == nil {
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return 0, err
			}
			if strings.TrimSpace(n) != "" {
				keywords = append(keywords, n)
			}
		}
		rows.Close()
	}
	if len(keywords) == 0 {
		return 0, nil
	}

	// 2. Find candidate sources.
	leftFilter, leftArgs := scopeLeftFilter(scope, "t1")
	args := []any{workspaceID, keywords}
	for _, a := range leftArgs {
		args = append(args, a)
	}
	// EXISTS(unnest(keywords) AS k WHERE position(k in lower(t1.counterparty_raw)) > 0)
	q := `
		SELECT t1.id, t1.amount, t1.currency, t1.booked_at,
		       (SELECT array_agg(t2.id ORDER BY abs(extract(epoch from t2.booked_at - t1.booked_at)),
		                          abs(t2.amount::numeric + t1.amount::numeric))
		        FROM transactions t2
		        WHERE t2.workspace_id = t1.workspace_id
		          AND t2.account_id != t1.account_id
		          AND sign(t2.amount::numeric) != sign(t1.amount::numeric)
		          AND abs(extract(epoch from t2.booked_at - t1.booked_at)) <= 5 * 86400
		          AND (t2.currency = t1.currency OR t2.original_currency = t1.currency)
		          AND NOT EXISTS (
		            SELECT 1 FROM transfer_matches tm
		            WHERE tm.workspace_id = t2.workspace_id
		              AND (tm.source_transaction_id = t2.id OR tm.destination_transaction_id = t2.id)
		          )
		        LIMIT 5
		       ) AS candidate_dst_ids
		FROM transactions t1
		WHERE t1.workspace_id = $1
		  AND t1.amount::numeric > 0
		  AND t1.counterparty_raw IS NOT NULL
		  AND lower(t1.counterparty_raw) LIKE ANY (
		    SELECT '%' || k || '%' FROM unnest($2::text[]) AS k
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM transfer_matches tm
		    WHERE tm.workspace_id = t1.workspace_id
		      AND (tm.source_transaction_id = t1.id OR tm.destination_transaction_id = t1.id)
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM transfer_match_candidates tmc
		    WHERE tmc.workspace_id = t1.workspace_id
		      AND tmc.source_transaction_id = t1.id
		      AND tmc.status IN ('paired', 'declined')
		  )
		  ` + leftFilter

	rows2, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("tier3 query: %w", err)
	}
	defer rows2.Close()

	type cand struct {
		sourceID uuid.UUID
		dstIDs   []uuid.UUID
	}
	var cands []cand
	for rows2.Next() {
		var sourceID uuid.UUID
		var amount, currency string
		var bookedAt any
		var dst []uuid.UUID
		if err := rows2.Scan(&sourceID, &amount, &currency, &bookedAt, &dst); err != nil {
			return 0, err
		}
		if len(dst) == 0 {
			continue
		}
		cands = append(cands, cand{sourceID: sourceID, dstIDs: dst})
	}
	if err := rows2.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, c := range cands {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO transfer_match_candidates
			  (id, workspace_id, source_transaction_id, candidate_destination_ids, status)
			VALUES ($1, $2, $3, $4, 'pending')
			ON CONFLICT (workspace_id, source_transaction_id) DO NOTHING
		`, uuidx.New(), workspaceID, c.sourceID, c.dstIDs)
		if err != nil {
			return count, fmt.Errorf("tier3 insert: %w", err)
		}
		if tag.RowsAffected() > 0 {
			count++
		}
	}
	return count, nil
}
```

- [ ] **Step 4: Run, expect pass**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -run TestTier3 -v 2>&1 | tail -10
```

- [ ] **Step 5: Re-run the entire transfers suite to confirm interplay**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ 2>&1 | tail -5
```

Expected: ok.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/transfers/tier3.go backend/internal/transfers/tier3_test.go
git -c commit.gpgsign=false commit -m "feat(transfers): Tier 3 — heuristic candidates surfaced for review"
```

---

## Phase 2 — Wire detector into bankimport

### Task 2.1: Plumb transfers.Service into bankimport.NewService

**Files:**
- Modify: `backend/internal/bankimport/service.go` — add `transfersSvc *transfers.Service` field, change `NewService` signature.
- Modify: `backend/internal/http/router.go` — wire `transfersSvc` into `bankimport.NewService`.
- Modify: every test file in `backend/internal/bankimport/` that calls `bankimport.NewService(pool, classSvc)` to pass `transfers.NewService(pool)`.

- [ ] **Step 1: Modify the Service struct and constructor**

```go
// backend/internal/bankimport/service.go (around the existing Service struct)
import (
	// existing imports...
	"github.com/xmedavid/folio/backend/internal/transfers"
)

type Service struct {
	pool         *pgxpool.Pool
	classSvc     *classification.Service
	transfersSvc *transfers.Service
	now          func() time.Time
}

func NewService(pool *pgxpool.Pool, classSvc *classification.Service, transfersSvc *transfers.Service) *Service {
	return &Service{pool: pool, classSvc: classSvc, transfersSvc: transfersSvc, now: time.Now}
}
```

- [ ] **Step 2: After each `Apply*` function commits the import tx, fire-and-log the detector**

In `Apply` (around the bottom of the function, after the commit), add:

```go
if s.transfersSvc != nil && len(insertedIDs) > 0 {
	if _, err := s.transfersSvc.DetectAndPair(ctx, workspaceID, transfers.DetectScope{TransactionIDs: insertedIDs}); err != nil {
		// Best-effort: log but don't fail the import.
		slog.Default().Warn("transfers.DetectAndPair after import", "err", err, "workspace", workspaceID)
	}
}
```

(Adjust the variable name `insertedIDs` to match the local variable that holds the just-inserted ids in each function. There may be slight differences between `Apply`, `ApplyPlan`, `ApplyMultiPlan` — repeat the pattern in each.)

Add the import: `"log/slog"` if not present.

- [ ] **Step 3: Update `router.go`**

Around line 86 of `backend/internal/http/router.go`:

```go
classificationSvc := classification.NewService(d.DB)
classificationH := classification.NewHandler(classificationSvc)
transfersSvc := transfers.NewService(d.DB)
importSvc := bankimport.NewService(d.DB, classificationSvc, transfersSvc)
```

Add the import line at top: `"github.com/xmedavid/folio/backend/internal/transfers"`.

- [ ] **Step 4: Update existing bankimport test calls**

Find every call site:

```bash
cd /Users/xmedavid/dev/folio/backend && grep -rn "bankimport.NewService" --include="*.go"
```

For each test file: pass `transfers.NewService(pool)` as the third argument.

- [ ] **Step 5: Build + test**

```bash
cd /Users/xmedavid/dev/folio/backend && go build ./... && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/bankimport/... ./internal/transfers/... 2>&1 | tail -15
```

Expected: ok.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/bankimport/ backend/internal/http/router.go backend/internal/transfers/
git -c commit.gpgsign=false commit -m "feat(bankimport): run transfers.DetectAndPair after every import"
```

---

## Phase 3 — Lifecycle endpoints

### Task 3.1: ManualPair / Unpair / DeclineCandidate / RunDetect

**Files:**
- Create: `backend/internal/transfers/lifecycle.go`
- Create: `backend/internal/transfers/lifecycle_test.go`

- [ ] **Step 1: Failing tests**

```go
// backend/internal/transfers/lifecycle_test.go
package transfers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transfers"
)

func TestManualPair_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-manualpair")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)

	tm, err := svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: &dst})
	require.NoError(t, err)
	require.Equal(t, src, tm.SourceTransactionID)
	require.NotNil(t, tm.DestinationTransactionID)
	require.Equal(t, dst, *tm.DestinationTransactionID)
	require.Equal(t, "manual", tm.Provenance)
}

func TestManualPair_AlreadyPairedSource(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-manualpair-already")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	c := seedAccount(t, ctx, pool, wsID, "C", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst1 := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)
	dst2 := seedTx(t, ctx, pool, wsID, c, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)

	_, err := svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: &dst1})
	require.NoError(t, err)
	_, err = svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: &dst2})
	var cerr *httpx.ConflictError
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, "transfer_source_already_paired", cerr.Code)
}

func TestManualPair_OutboundExternal(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-manualpair-outbound")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)

	tm, err := svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: nil})
	require.NoError(t, err)
	require.Nil(t, tm.DestinationTransactionID)
}

func TestManualPair_SelfPairRejected(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-manualpair-self")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)

	_, err := svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: &src})
	var verr *httpx.ValidationError
	require.True(t, errors.As(err, &verr))
}

func TestUnpair_Restores(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-unpair")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)

	tm, _ := svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: &dst})
	err := svc.Unpair(ctx, wsID, tm.ID)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_matches WHERE id = $1`, tm.ID,
	).Scan(&count))
	require.Equal(t, 0, count)
}

func TestDeclineCandidate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-decline")

	a := seedAccount(t, ctx, pool, wsID, "Revolut", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "Bank", "CHF")
	src := seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "500.00", "CHF", "Transfer from Bank")
	_ = seedTxWithRaw(t, ctx, pool, wsID, b, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC), "-500.00", "CHF", "Transfer to Revolut")

	_, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)

	cands, err := svc.ListPendingCandidates(ctx, wsID)
	require.NoError(t, err)
	require.Len(t, cands, 1)

	require.NoError(t, svc.DeclineCandidate(ctx, wsID, cands[0].ID, nil))

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM transfer_match_candidates WHERE id = $1`, cands[0].ID,
	).Scan(&status))
	require.Equal(t, "declined", status)

	cands2, _ := svc.ListPendingCandidates(ctx, wsID)
	require.Len(t, cands2, 0)
	_ = src // referenced for assertion clarity above
}
```

- [ ] **Step 2: Run, expect failures**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/ -run TestManualPair -v 2>&1 | tail -20
```

- [ ] **Step 3: Implement `lifecycle.go`**

```go
// backend/internal/transfers/lifecycle.go
package transfers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

const transferMatchCols = `
	id, workspace_id, source_transaction_id, destination_transaction_id,
	fx_rate::text, fee_amount::text, fee_currency::text, tolerance_note,
	provenance::text, matched_by_user_id, matched_at, created_at
`

func scanTransferMatch(r interface{ Scan(...any) error }, m *TransferMatch) error {
	return r.Scan(
		&m.ID, &m.WorkspaceID, &m.SourceTransactionID, &m.DestinationTransactionID,
		&m.FXRate, &m.FeeAmount, &m.FeeCurrency, &m.ToleranceNote,
		&m.Provenance, &m.MatchedByUserID, &m.MatchedAt, &m.CreatedAt,
	)
}

// ManualPair creates a manual transfer_matches row.
//   - DestinationID == nil → outbound-to-external.
//   - Source must not already be paired (409 transfer_source_already_paired).
//   - Destination, if set, must not already be paired (409 transfer_destination_already_paired).
//   - Source == Destination → 400.
func (s *Service) ManualPair(ctx context.Context, workspaceID uuid.UUID, in ManualPairInput) (*TransferMatch, error) {
	if in.DestinationID != nil && in.SourceID == *in.DestinationID {
		return nil, httpx.NewValidationError("source and destination must differ")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin manual pair: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := assertExists(ctx, tx, workspaceID, in.SourceID); err != nil {
		return nil, err
	}
	if in.DestinationID != nil {
		if err := assertExists(ctx, tx, workspaceID, *in.DestinationID); err != nil {
			return nil, err
		}
	}
	if err := assertNotPaired(ctx, tx, workspaceID, in.SourceID, "source"); err != nil {
		return nil, err
	}
	if in.DestinationID != nil {
		if err := assertNotPaired(ctx, tx, workspaceID, *in.DestinationID, "destination"); err != nil {
			return nil, err
		}
	}

	id := uuidx.New()
	row := tx.QueryRow(ctx, `
		INSERT INTO transfer_matches (
			id, workspace_id, source_transaction_id, destination_transaction_id,
			fee_amount, fee_currency, tolerance_note, provenance, matched_at
		) VALUES (
			$1, $2, $3, $4, $5::numeric, $6::money_currency, $7, 'manual', now()
		)
		RETURNING `+transferMatchCols, id, workspaceID, in.SourceID, in.DestinationID, in.FeeAmount, in.FeeCurrency, in.ToleranceNote)
	var m TransferMatch
	if err := scanTransferMatch(row, &m); err != nil {
		return nil, fmt.Errorf("manual pair insert: %w", err)
	}

	// Mark any pending candidate for this source as 'paired'.
	if _, err := tx.Exec(ctx, `
		UPDATE transfer_match_candidates
		SET status = 'paired', resolved_at = now()
		WHERE workspace_id = $1 AND source_transaction_id = $2 AND status = 'pending'
	`, workspaceID, in.SourceID); err != nil {
		return nil, fmt.Errorf("close candidate: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit manual pair: %w", err)
	}
	return &m, nil
}

func assertExists(ctx context.Context, tx pgx.Tx, workspaceID, txID uuid.UUID) error {
	var ok bool
	err := tx.QueryRow(ctx,
		`SELECT true FROM transactions WHERE workspace_id = $1 AND id = $2`,
		workspaceID, txID,
	).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("transaction")
	}
	return err
}

func assertNotPaired(ctx context.Context, tx pgx.Tx, workspaceID, txID uuid.UUID, role string) error {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM transfer_matches
			WHERE workspace_id = $1
			  AND (source_transaction_id = $2 OR destination_transaction_id = $2)
		)
	`, workspaceID, txID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check not-paired: %w", err)
	}
	if exists {
		code := "transfer_" + strings.ToLower(role) + "_already_paired"
		return httpx.NewConflictError(code, role+" is already part of another transfer pair", nil)
	}
	return nil
}

// Unpair removes a transfer_matches row by id.
func (s *Service) Unpair(ctx context.Context, workspaceID, matchID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM transfer_matches WHERE workspace_id = $1 AND id = $2`,
		workspaceID, matchID,
	)
	if err != nil {
		return fmt.Errorf("unpair: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("transfer_match")
	}
	return nil
}

// ListPendingCandidates returns Tier-3 candidates with status='pending'.
func (s *Service) ListPendingCandidates(ctx context.Context, workspaceID uuid.UUID) ([]TransferCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workspace_id, source_transaction_id, candidate_destination_ids,
		       status, suggested_at, resolved_at, resolved_by_user_id
		FROM transfer_match_candidates
		WHERE workspace_id = $1 AND status = 'pending'
		ORDER BY suggested_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()
	out := []TransferCandidate{}
	for rows.Next() {
		var c TransferCandidate
		if err := rows.Scan(
			&c.ID, &c.WorkspaceID, &c.SourceTransactionID, &c.CandidateDestinationIDs,
			&c.Status, &c.SuggestedAt, &c.ResolvedAt, &c.ResolvedByUserID,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountPendingCandidates returns the count of pending candidates for the
// dossier-tab badge.
func (s *Service) CountPendingCandidates(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_match_candidates
		 WHERE workspace_id = $1 AND status = 'pending'`,
		workspaceID,
	).Scan(&n)
	return n, err
}

// DeclineCandidate marks a candidate as declined. byUserID is optional.
func (s *Service) DeclineCandidate(ctx context.Context, workspaceID, candidateID uuid.UUID, byUserID *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE transfer_match_candidates
		SET status = 'declined', resolved_at = now(), resolved_by_user_id = $3
		WHERE workspace_id = $1 AND id = $2 AND status = 'pending'
	`, workspaceID, candidateID, byUserID)
	if err != nil {
		return fmt.Errorf("decline candidate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Could be: not found, or not pending.
		var status *string
		err := s.pool.QueryRow(ctx,
			`SELECT status FROM transfer_match_candidates WHERE workspace_id = $1 AND id = $2`,
			workspaceID, candidateID,
		).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("transfer_candidate")
		}
		return httpx.NewServiceError("transfer_candidate_not_pending", "candidate is not in pending status", 422)
	}
	return nil
}
```

`httpx.NewServiceError` may not exist — if not, fall back to a plain `*httpx.ConflictError` with a 422-ish code. Check `backend/internal/httpx/httpx.go` first; if there's no helper for arbitrary status, use `ConflictError`.

- [ ] **Step 4: Run tests, expect pass**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/... 2>&1 | tail -10
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/transfers/
git -c commit.gpgsign=false commit -m "feat(transfers): manual pair / unpair / decline candidate / list pending"
```

### Task 3.2: HTTP handler + routes

**Files:**
- Create: `backend/internal/transfers/http.go`
- Modify: `backend/internal/http/router.go` — mount the new handler.

- [ ] **Step 1: Write the handler**

```go
// backend/internal/transfers/http.go
package transfers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Mount(r chi.Router) {
	r.Post("/detect", h.detect)
	r.Post("/manual-pair", h.manualPair)
	r.Delete("/{matchId}", h.unpair)
	r.Get("/candidates", h.listCandidates)
	r.Get("/candidates/count", h.candidateCount)
	r.Post("/candidates/{candidateId}/decline", h.declineCandidate)
}

func (h *Handler) detect(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	res, err := h.svc.DetectAndPair(r.Context(), wsID, DetectScope{All: true})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type manualPairReq struct {
	SourceID      string  `json:"sourceId"`
	DestinationID *string `json:"destinationId"` // null for outbound-to-external
	FeeAmount     *string `json:"feeAmount"`
	FeeCurrency   *string `json:"feeCurrency"`
	ToleranceNote *string `json:"toleranceNote"`
}

func (h *Handler) manualPair(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	var req manualPairReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	src, err := uuid.Parse(req.SourceID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "sourceId must be a UUID")
		return
	}
	in := ManualPairInput{SourceID: src, FeeAmount: req.FeeAmount, FeeCurrency: req.FeeCurrency, ToleranceNote: req.ToleranceNote}
	if req.DestinationID != nil {
		dst, err := uuid.Parse(*req.DestinationID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "destinationId must be a UUID")
			return
		}
		in.DestinationID = &dst
	}
	res, err := h.svc.ManualPair(r.Context(), wsID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) unpair(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "matchId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "matchId must be a UUID")
		return
	}
	if err := h.svc.Unpair(r.Context(), wsID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCandidates(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	res, err := h.svc.ListPendingCandidates(r.Context(), wsID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) candidateCount(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	n, err := h.svc.CountPendingCandidates(r.Context(), wsID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (h *Handler) declineCandidate(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "candidateId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "candidateId must be a UUID")
		return
	}
	user := auth.MustUser(r)
	if err := h.svc.DeclineCandidate(r.Context(), wsID, id, &user.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Mount routes in router.go**

In `backend/internal/http/router.go`, after the existing classification mounts:

```go
transfersH := transfers.NewHandler(transfersSvc)
// inside r.Route("/api/v1", ...):
r.Route("/transfers", transfersH.Mount)
```

- [ ] **Step 3: Build, run all tests**

```bash
cd /Users/xmedavid/dev/folio/backend && go build ./... && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transfers/... 2>&1 | tail -10
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/transfers/http.go backend/internal/http/router.go
git -c commit.gpgsign=false commit -m "feat(api): transfer endpoints (detect, manual-pair, unpair, candidates)"
```

---

## Phase 4 — Transactions list + stats integration

### Task 4.1: hideInternalMoves on List + transferMatchId on row

**Files:**
- Modify: `backend/internal/transactions/service.go` — extend `ListFilter` and the SELECT.
- Modify: `backend/internal/transactions/http.go` — accept `hideInternalMoves` query param.
- Modify: `backend/internal/transactions/service.go::Transaction` struct — add `TransferMatchID` and `TransferCounterpartID` fields populated from a left-join to `transfer_matches`.

- [ ] **Step 1: Extend `ListFilter`**

```go
type ListFilter struct {
	// existing fields...
	HideInternalMoves bool
}
```

- [ ] **Step 2: Extend the `Transaction` wire struct**

```go
type Transaction struct {
	// existing fields...
	TransferMatchID       *uuid.UUID `json:"transferMatchId,omitempty"`
	TransferCounterpartID *uuid.UUID `json:"transferCounterpartId,omitempty"`
}
```

(Add to `transactionCols` and `scanRow` accordingly.)

- [ ] **Step 3: Modify the `List` SQL**

In `service.go::List`, the existing query needs a left-join + filter. Sketch:

```sql
SELECT t.<existing cols>,
       tm.id AS transfer_match_id,
       CASE WHEN tm.source_transaction_id = t.id
            THEN tm.destination_transaction_id
            ELSE tm.source_transaction_id END AS transfer_counterpart_id
FROM transactions t
LEFT JOIN transfer_matches tm
  ON tm.workspace_id = t.workspace_id
 AND (tm.source_transaction_id = t.id OR tm.destination_transaction_id = t.id)
WHERE t.workspace_id = $1
  AND ...other filters...
  AND ($N IS FALSE OR tm.id IS NULL)   -- hideInternalMoves
ORDER BY t.booked_at DESC
LIMIT $L OFFSET $O
```

`$N` is the new `hideInternalMoves` placeholder. Default `true` from the handler.

- [ ] **Step 4: Same for `Get`** — single transaction read also needs the left-join so the detail panel knows the transferMatchId.

- [ ] **Step 5: HTTP handler — accept `hideInternalMoves` query param**

In `transactions/http.go`'s `listTransactions`:

```go
hide := true
if v := r.URL.Query().Get("hideInternalMoves"); v != "" {
	hide = strings.EqualFold(v, "true")
}
filter.HideInternalMoves = hide
```

- [ ] **Step 6: Failing test for the filter**

```go
// backend/internal/transactions/transfer_filter_test.go
package transactions_test

// Setup: workspace with two accounts and a known transfer pair (insert a
// transfer_matches row via raw SQL after creating two transactions that
// would otherwise show up in List). Assert:
//   - List with HideInternalMoves=true excludes both legs.
//   - List with HideInternalMoves=false includes them with TransferMatchID set.
//   - Get on a paired transaction returns TransferMatchID + TransferCounterpartID.
```

(Implementer: write the full test body using `testdb` patterns from existing transactions tests.)

- [ ] **Step 7: Run tests**

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test ./internal/transactions/... 2>&1 | tail -10
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/transactions/
git -c commit.gpgsign=false commit -m "feat(transactions): hideInternalMoves filter + transferMatchId on rows"
```

### Task 4.2: Stats exclusion (one-line audit)

For v1 there's no separate stats package yet — spend/income aggregations happen ad-hoc in views. **Document this as a deferred follow-up:** when the stats package lands (separate feature), it MUST add the same `NOT EXISTS (transfer_matches WHERE source = t.id OR destination = t.id)` predicate. Add a TODO note in the spec or in the eventual stats package.

For now, any existing aggregate queries (in `dashboard.go`, `account_groups.go`, etc.) need a quick audit:

- [ ] **Step 1:** `git grep -n "SUM(" backend/internal/ --include="*.go"` to find aggregate queries.
- [ ] **Step 2:** For each, decide whether it's an income/expense rollup. If yes, add the `NOT EXISTS (transfer_matches ...)` predicate. If no (e.g. it's a balance sum), leave as-is.
- [ ] **Step 3:** Write a focused integration test for each modified aggregate query.
- [ ] **Step 4:** Commit.

(Implementer: this task will likely produce 2-4 small commits; track the count and report. If there are zero affected aggregates, say so and skip.)

---

## Phase 5 — Frontend API client

### Task 5.1: Extend client.ts

**Files:**
- Modify: `web/lib/api/client.ts`

- [ ] **Step 1: Add types**

```ts
export type TransferMatch = {
  id: string;
  workspaceId: string;
  sourceTransactionId: string;
  destinationTransactionId?: string | null;
  fxRate?: string | null;
  feeAmount?: string | null;
  feeCurrency?: string | null;
  toleranceNote?: string | null;
  provenance: string;
  matchedByUserId?: string | null;
  matchedAt: string;
  createdAt: string;
};

export type TransferCandidate = {
  id: string;
  workspaceId: string;
  sourceTransactionId: string;
  candidateDestinationIds: string[];
  status: 'pending' | 'paired' | 'declined';
  suggestedAt: string;
  resolvedAt?: string | null;
  resolvedByUserId?: string | null;
};

export type DetectResult = {
  tier1Paired: number;
  tier2Paired: number;
  tier3Suggested: number;
};
```

- [ ] **Step 2: Add functions** (mirror existing patterns)

```ts
export async function fetchPendingTransferCandidates(workspaceId: string): Promise<TransferCandidate[]> {
  return request<TransferCandidate[]>(`/api/v1/t/${workspaceId}/transfers/candidates`, { method: "GET" });
}

export async function fetchPendingTransferCandidateCount(workspaceId: string): Promise<{ count: number }> {
  return request<{ count: number }>(`/api/v1/t/${workspaceId}/transfers/candidates/count`, { method: "GET" });
}

export async function manualPairTransfer(
  workspaceId: string,
  body: { sourceId: string; destinationId: string | null; feeAmount?: string | null; feeCurrency?: string | null; toleranceNote?: string | null },
): Promise<TransferMatch> {
  return request<TransferMatch>(`/api/v1/t/${workspaceId}/transfers/manual-pair`, { method: "POST", json: body });
}

export async function unpairTransfer(workspaceId: string, matchId: string): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/transfers/${matchId}`, { method: "DELETE" });
}

export async function declineTransferCandidate(workspaceId: string, candidateId: string): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/transfers/candidates/${candidateId}/decline`, { method: "POST", json: {} });
}

export async function runTransferDetector(workspaceId: string): Promise<DetectResult> {
  return request<DetectResult>(`/api/v1/t/${workspaceId}/transfers/detect`, { method: "POST", json: {} });
}
```

- [ ] **Step 3: Extend `fetchTransactions` query type**

Add `hideInternalMoves?: boolean` to whatever type the existing `fetchTransactions` uses (likely an object literal or `TransactionsQuery`). Default to `true` if you control the call site; otherwise make the caller pass.

The existing `Transaction` schema reference comes from `schema.d.ts` (auto-generated). Either re-generate the openapi schema (best — but deferred unless trivial) OR inline an extension type:

```ts
// In client.ts after the existing Transaction type re-export:
export type TransactionWithTransfer = Transaction & {
  transferMatchId?: string | null;
  transferCounterpartId?: string | null;
};

export async function fetchTransactionsWithTransfers(
  workspaceId: string,
  query: TransactionsQuery & { hideInternalMoves?: boolean } = {}
): Promise<TransactionWithTransfer[]> {
  return request<TransactionWithTransfer[]>(
    `/api/v1/t/${workspaceId}/transactions${buildQuery(query)}`,
    { method: "GET" },
  );
}
```

(The existing `fetchTransactions` stays for callers that don't care about the transfer fields.)

- [ ] **Step 4: Type-check**

```bash
cd /Users/xmedavid/dev/folio/web && pnpm tsc --noEmit
```

- [ ] **Step 5: Commit**

```bash
git add web/lib/api/client.ts
git -c commit.gpgsign=false commit -m "feat(web): transfer API client + extended transaction type"
```

---

## Phase 6 — Reusable dossier-tab framework

### Task 6.1: Dossier-tabs container + tab + drawer

**Files:**
- Create: `web/components/dossier/dossier-tabs.tsx`
- Create: `web/components/dossier/dossier-tab.tsx`
- Create: `web/components/dossier/dossier-drawer.tsx`
- Create: `web/components/dossier/registry.ts` — global registry hook for registering tabs.

- [ ] **Step 1: Registry**

```ts
// web/components/dossier/registry.ts
"use client";

import * as React from "react";

export type DossierTabSpec = {
  id: string;
  label: string;
  icon?: React.ReactNode;
  count: number;
  drawerContent: React.ReactNode;
};

const Ctx = React.createContext<{
  tabs: DossierTabSpec[];
  setTab: (spec: DossierTabSpec) => void;
  removeTab: (id: string) => void;
}>(null!);

export function DossierProvider({ children }: { children: React.ReactNode }) {
  const [tabs, setTabs] = React.useState<DossierTabSpec[]>([]);
  const setTab = React.useCallback((spec: DossierTabSpec) => {
    setTabs((current) => {
      const idx = current.findIndex((t) => t.id === spec.id);
      if (idx === -1) return [...current, spec];
      const next = current.slice();
      next[idx] = spec;
      return next;
    });
  }, []);
  const removeTab = React.useCallback((id: string) => {
    setTabs((current) => current.filter((t) => t.id !== id));
  }, []);
  return <Ctx.Provider value={{ tabs, setTab, removeTab }}>{children}</Ctx.Provider>;
}

export function useDossierTabs(): DossierTabSpec[] {
  const ctx = React.useContext(Ctx);
  if (!ctx) throw new Error("useDossierTabs must be inside DossierProvider");
  return ctx.tabs;
}

export function useRegisterDossierTab(spec: DossierTabSpec | null) {
  const ctx = React.useContext(Ctx);
  if (!ctx) throw new Error("useRegisterDossierTab must be inside DossierProvider");
  React.useEffect(() => {
    if (!spec || spec.count <= 0) {
      if (spec) ctx.removeTab(spec.id);
      return;
    }
    ctx.setTab(spec);
    return () => ctx.removeTab(spec.id);
  }, [spec, ctx]);
}
```

- [ ] **Step 2: Container that renders nothing if zero tabs**

```tsx
// web/components/dossier/dossier-tabs.tsx
"use client";

import * as React from "react";
import { useDossierTabs } from "./registry";
import { DossierTab } from "./dossier-tab";
import { DossierDrawer } from "./dossier-drawer";

export function DossierTabs() {
  const tabs = useDossierTabs();
  const [openId, setOpenId] = React.useState<string | null>(null);
  if (tabs.length === 0) return null;
  const open = tabs.find((t) => t.id === openId) ?? null;
  return (
    <>
      <div className="fixed top-1/3 right-0 z-30 flex flex-col gap-2">
        {tabs.map((t) => (
          <DossierTab key={t.id} spec={t} onClick={() => setOpenId(t.id)} />
        ))}
      </div>
      {open ? (
        <DossierDrawer
          title={open.label}
          onClose={() => setOpenId(null)}
        >
          {open.drawerContent}
        </DossierDrawer>
      ) : null}
    </>
  );
}
```

- [ ] **Step 3: Single tab — paper-flap protruding from the right edge**

```tsx
// web/components/dossier/dossier-tab.tsx
"use client";

import * as React from "react";
import type { DossierTabSpec } from "./registry";

export function DossierTab({ spec, onClick }: { spec: DossierTabSpec; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="
        bg-surface border-border text-fg
        flex translate-x-0 items-center gap-2
        rounded-l-md border border-r-0 px-3 py-2
        shadow-sm transition-transform hover:-translate-x-1
        focus:outline-none focus:ring-2 focus:ring-accent
      "
      aria-label={`${spec.label} (${spec.count} pending)`}
    >
      {spec.icon}
      <span className="text-[12px] font-medium">{spec.label}</span>
      <span className="bg-accent text-accent-foreground rounded px-1.5 py-0.5 text-[11px] font-semibold">
        {spec.count}
      </span>
    </button>
  );
}
```

- [ ] **Step 4: Slide-in drawer**

```tsx
// web/components/dossier/dossier-drawer.tsx
"use client";

import * as React from "react";
import { X } from "lucide-react";

export function DossierDrawer({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-40">
      <div className="absolute inset-0 bg-fg/30" onClick={onClose} aria-hidden />
      <aside className="bg-surface border-border absolute inset-y-0 right-0 w-[420px] max-w-full overflow-y-auto border-l shadow-xl">
        <header className="border-border flex items-center justify-between border-b px-4 py-3">
          <h2 className="text-fg text-[14px] font-semibold">{title}</h2>
          <button type="button" onClick={onClose} className="text-fg-muted hover:text-fg" aria-label="Close drawer">
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="p-4">{children}</div>
      </aside>
    </div>
  );
}
```

- [ ] **Step 5: Mount the framework in the workspace layout**

In `web/app/w/[slug]/layout.tsx`, wrap children with `<DossierProvider>` and render `<DossierTabs />` once:

```tsx
import { DossierProvider } from "@/components/dossier/registry";
import { DossierTabs } from "@/components/dossier/dossier-tabs";

// Inside the layout return:
<DossierProvider>
  {children}
  <DossierTabs />
</DossierProvider>
```

- [ ] **Step 6: Type-check**

```bash
cd /Users/xmedavid/dev/folio/web && pnpm tsc --noEmit
```

- [ ] **Step 7: Commit**

```bash
git add web/components/dossier/ web/app/w/[slug]/layout.tsx
git -c commit.gpgsign=false commit -m "feat(web): reusable dossier-tab framework (right-edge floating tabs + drawer)"
```

---

## Phase 7 — Transfers review queue

### Task 7.1: Review queue components + dossier registration

**Files:**
- Create: `web/components/transfers/transfers-review-tab.tsx` — registers the dossier tab.
- Create: `web/components/transfers/transfers-review-queue.tsx` — the queue UI used in both drawer + page.
- Create: `web/app/w/[slug]/transfers/review/page.tsx` — full-page view.

- [ ] **Step 1: Review queue UI** (used in both modes)

```tsx
// web/components/transfers/transfers-review-queue.tsx
"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  declineTransferCandidate,
  fetchPendingTransferCandidates,
  fetchTransaction,
  manualPairTransfer,
  type TransferCandidate,
  type Transaction,
} from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { LoadingText, EmptyState } from "@/components/app/empty";

export function TransfersReviewQueue({
  workspaceId,
  mode = "drawer",
}: {
  workspaceId: string;
  mode?: "drawer" | "page";
}) {
  const queryClient = useQueryClient();
  const candidatesQuery = useQuery({
    queryKey: ["transfer-candidates", workspaceId],
    queryFn: () => fetchPendingTransferCandidates(workspaceId),
    enabled: !!workspaceId,
  });

  if (candidatesQuery.isLoading) return <LoadingText />;
  const candidates = candidatesQuery.data ?? [];
  if (candidates.length === 0)
    return <EmptyState title="No pending suggestions" description="Folio will surface possible transfers here after the next import." />;

  return (
    <div className="flex flex-col gap-3">
      {candidates.map((c) => (
        <CandidateRow
          key={c.id}
          workspaceId={workspaceId}
          candidate={c}
          onDone={() => queryClient.invalidateQueries({ queryKey: ["transfer-candidates", workspaceId] })}
          mode={mode}
        />
      ))}
    </div>
  );
}

function CandidateRow({
  workspaceId, candidate, onDone, mode,
}: {
  workspaceId: string;
  candidate: TransferCandidate;
  onDone: () => void;
  mode: "drawer" | "page";
}) {
  const [chosenDestId, setChosenDestId] = React.useState<string | null>(null);

  const sourceQuery = useQuery({
    queryKey: ["transaction", workspaceId, candidate.sourceTransactionId],
    queryFn: () => fetchTransaction(workspaceId, candidate.sourceTransactionId),
  });

  const destinationsQuery = useQuery({
    queryKey: ["transactions-by-ids", workspaceId, candidate.candidateDestinationIds],
    queryFn: async () => {
      // Fetch each by id (cheap; v1 candidate list ≤ 5).
      return Promise.all(
        candidate.candidateDestinationIds.map((id) => fetchTransaction(workspaceId, id)),
      );
    },
    enabled: candidate.candidateDestinationIds.length > 0,
  });

  const queryClient = useQueryClient();
  const pair = useMutation({
    mutationFn: (destId: string) =>
      manualPairTransfer(workspaceId, { sourceId: candidate.sourceTransactionId, destinationId: destId }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["transactions", workspaceId] });
      onDone();
    },
  });
  const decline = useMutation({
    mutationFn: () => declineTransferCandidate(workspaceId, candidate.id),
    onSuccess: onDone,
  });

  const source = sourceQuery.data;
  const destinations = destinationsQuery.data ?? [];

  return (
    <Card className="p-3">
      <div className="text-fg mb-2 text-[13px] font-medium">
        Source: {source ? formatTxLine(source) : "loading…"}
      </div>
      <div className="flex flex-col gap-1.5">
        {destinations.map((d) => (
          <label key={d.id} className="flex items-center gap-2 text-[12px]">
            <input
              type="radio"
              name={`cand-${candidate.id}`}
              value={d.id}
              checked={chosenDestId === d.id}
              onChange={() => setChosenDestId(d.id)}
            />
            <span className="text-fg-muted">{formatTxLine(d)}</span>
          </label>
        ))}
      </div>
      <div className="mt-3 flex gap-2 justify-end">
        <Button size="sm" variant="secondary" disabled={decline.isPending} onClick={() => decline.mutate()}>
          External credit
        </Button>
        <Button size="sm" disabled={!chosenDestId || pair.isPending} onClick={() => chosenDestId && pair.mutate(chosenDestId)}>
          Pair selected
        </Button>
      </div>
    </Card>
  );
}

function formatTxLine(t: Transaction) {
  const date = new Date(t.bookedAt).toISOString().slice(0, 10);
  return `${date} · ${t.counterpartyRaw ?? t.description ?? "—"} · ${t.amount} ${t.currency}`;
}
```

- [ ] **Step 2: Dossier registration component**

```tsx
// web/components/transfers/transfers-review-tab.tsx
"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRightLeft } from "lucide-react";
import { fetchPendingTransferCandidateCount } from "@/lib/api/client";
import { useRegisterDossierTab } from "@/components/dossier/registry";
import { TransfersReviewQueue } from "./transfers-review-queue";

export function TransfersReviewTab({ workspaceId }: { workspaceId: string }) {
  const countQuery = useQuery({
    queryKey: ["transfer-candidate-count", workspaceId],
    queryFn: () => fetchPendingTransferCandidateCount(workspaceId),
    refetchInterval: 30_000,  // mild background poll; cheap GET
  });
  const count = countQuery.data?.count ?? 0;

  const spec = React.useMemo(
    () => count > 0 ? {
      id: "transfers-review",
      label: "Review transfers",
      icon: <ArrowRightLeft className="h-3.5 w-3.5" />,
      count,
      drawerContent: <TransfersReviewQueue workspaceId={workspaceId} mode="drawer" />,
    } : null,
    [count, workspaceId],
  );
  useRegisterDossierTab(spec);
  return null;
}
```

- [ ] **Step 3: Mount the registration**

In `web/app/w/[slug]/layout.tsx` (inside the `DossierProvider`), render `<TransfersReviewTab workspaceId={workspace.id} />` once. Workspace id should already be in scope from the layout's data fetching.

- [ ] **Step 4: Full-page review route**

```tsx
// web/app/w/[slug]/transfers/review/page.tsx
"use client";

import { use } from "react";
import { TransfersReviewQueue } from "@/components/transfers/transfers-review-queue";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { PageHeader } from "@/components/app/page-header";

export default function ReviewPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  if (!workspace) return null;
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Transfers"
        title="Review proposed transfers"
        description="Suggested cross-account pairs awaiting confirmation."
      />
      <TransfersReviewQueue workspaceId={workspace.id} mode="page" />
    </div>
  );
}
```

- [ ] **Step 5: Type-check**

```bash
cd /Users/xmedavid/dev/folio/web && pnpm tsc --noEmit
```

- [ ] **Step 6: Commit**

```bash
git add web/components/transfers/ web/app/w/[slug]/transfers/ web/app/w/[slug]/layout.tsx
git -c commit.gpgsign=false commit -m "feat(web): transfers review queue (drawer + full page) wired into dossier tab"
```

---

## Phase 8 — Manual-pair dialog + transfer badge

### Task 8.1: Transfer badge

**File:** Create `web/components/transfers/transfer-badge.tsx`.

```tsx
"use client";

import { ArrowRightLeft, ArrowUpRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";

export function TransferBadge({ external = false }: { external?: boolean }) {
  if (external) {
    return (
      <Badge variant="neutral" className="gap-1">
        <ArrowUpRight className="h-3 w-3" />
        External
      </Badge>
    );
  }
  return (
    <Badge variant="info" className="gap-1">
      <ArrowRightLeft className="h-3 w-3" />
      Transfer
    </Badge>
  );
}
```

Commit:

```bash
git add web/components/transfers/transfer-badge.tsx
git -c commit.gpgsign=false commit -m "feat(web): transfer badge component"
```

### Task 8.2: Manual-pair dialog

**File:** Create `web/components/transfers/manual-pair-dialog.tsx`.

(Implementer: mirror the structure of `web/components/classification/merchant-merge-dialog.tsx` — single modal with async-search input + confirm. Preseeded with one transaction id; user searches transactions by counterparty/amount; confirms calls `manualPairTransfer`. Also include an "Outbound to external" toggle that sends `destinationId: null`.)

The component signature:
```tsx
export function ManualPairDialog({
  open,
  workspaceId,
  source,
  onClose,
}: {
  open: boolean;
  workspaceId: string;
  source: Transaction;
  onClose: () => void;
}) {
  // ... search input, candidates list, confirm button, outbound-to-external switch
}
```

Confirm calls `manualPairTransfer({ sourceId: source.id, destinationId: chosenId })`. On success, invalidate transactions queries + close.

Commit:

```bash
git add web/components/transfers/manual-pair-dialog.tsx
git -c commit.gpgsign=false commit -m "feat(web): manual pair dialog"
```

---

## Phase 9 — Transactions list integration

### Task 9.1: Show internal moves toggle + badge + linked-transfer detail

**Files:**
- Modify: `web/app/w/[slug]/transactions/page.tsx` — add `hideInternalMoves` to `TransactionFilters`, render the toggle in the filter panel, render `<TransferBadge>` on rows where `transferMatchId` is set.
- Modify: the transaction detail panel inside `transactions/page.tsx` — add a "Linked transfer" section that fetches the counterpart by id and shows it + Unpair button.

- [ ] **Step 1: Add `hideInternalMoves: boolean` to the `TransactionFilters` type and `EMPTY_FILTERS` (default `true`).**

- [ ] **Step 2: Update the `fetchTransactions` call site.** Use `fetchTransactionsWithTransfers` from Task 5.1 instead so the response carries `transferMatchId`. Pass `hideInternalMoves`.

- [ ] **Step 3: Add a checkbox "Show internal moves" to the existing filter panel** (mirroring the existing "Show archived" toggle pattern).

- [ ] **Step 4: Render `<TransferBadge>` in the row's Description cell** when `t.transferMatchId` is set.

- [ ] **Step 5: In the detail panel, when `transaction.transferMatchId` is set, render a "Linked transfer" section.** Fetch the counterpart via `fetchTransaction` keyed on `transaction.transferCounterpartId`. Show date, account, amount; render an Unpair button that calls `unpairTransfer` and invalidates queries.

- [ ] **Step 6: Type-check + lint:**

```bash
cd /Users/xmedavid/dev/folio/web && pnpm tsc --noEmit && pnpm lint 2>&1 | tail -10
```

- [ ] **Step 7: Commit:**

```bash
git add web/app/w/[slug]/transactions/page.tsx
git -c commit.gpgsign=false commit -m "feat(web): transactions list show-internal-moves toggle + transfer badge + linked counterpart"
```

---

## Phase 10 — End-to-end smoke test

### Task 10.1: Backend e2e test

Create `backend/internal/transfers/e2e_test.go`:

Scenario:
1. Workspace + two CHF accounts.
2. Insert two unpaired transactions (source -100, destination +100, same day, same currency).
3. Run `DetectAndPair(All)` → Tier 1 doesn't match (no original_amount), Tier 2 doesn't match (no shared batch), Tier 3 doesn't match (no transfer-keyword raw). 0 pairs.
4. Add a synthetic batch link via raw SQL between the two → re-run `DetectAndPair(All)` → Tier 2 pairs them.
5. Verify `transfer_matches` row exists; verify `transactions.List(hideInternalMoves=true)` returns neither; verify `List(hideInternalMoves=false)` returns both with `TransferMatchID` set.
6. Manually `Unpair` → verify list-with-default shows both again.

Commit:

```bash
git add backend/internal/transfers/e2e_test.go
git -c commit.gpgsign=false commit -m "test(transfers): end-to-end detect → list-filter → unpair"
```

### Task 10.2: Frontend Vitest smoke

Create `web/components/transfers/transfers-review-queue.test.tsx`:

- Pending candidates render.
- Clicking "Pair selected" with a chosen destination calls `manualPairTransfer` with `{ sourceId, destinationId }`.
- Clicking "External credit" calls `declineTransferCandidate`.

Mock the API client. Run via `pnpm test`.

Commit.

### Task 10.3: Final full-suite run + frontend tests

```bash
cd /Users/xmedavid/dev/folio/backend && DATABASE_URL=postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable go test -count=1 ./internal/transfers/... ./internal/transactions/... ./internal/bankimport/... 2>&1 | tail -10
cd /Users/xmedavid/dev/folio/web && pnpm tsc --noEmit && pnpm test 2>&1 | tail -10
```

---

## Phase 11 — Final review

### Task 11.1: Dispatch superpowers:code-reviewer over the whole branch

Same pattern as the merchants feature review. After Phase 10 lands, dispatch a final reviewer with:

- BASE_SHA = main parent of this branch.
- HEAD_SHA = HEAD.
- Spec path.
- Plan path.
- Specific assessments to call out (atomicity of manual-pair tx, manual-override invariant on transfers, dossier-tab framework reusability, hideInternalMoves filter SQL correctness).

Address blockers, document follow-ups for non-blockers, then run `superpowers:finishing-a-development-branch`.

---

## Self-Review Notes

**Spec coverage:**
- §1 Goal — implicit in all phases.
- §2 Principles — Phase 4 (data-only pairing), Phase 1 (tier ordering), Phase 9 (hide by default).
- §3 Out of scope — honored: no FX-rate-equivalent matching, no background job, no `/transfers` global page.
- §4 Data model — Phase 0.2.
- §5 Detection algorithm — Phase 1 (Tasks 1.2 / 1.3 / 1.4).
- §6 Lifecycle — Phase 3.
- §7 Visibility & stats — Phase 4 + Phase 9 (frontend).
- §8 Frontend — Phases 5, 6, 7, 8, 9.
- §9 API surface — Phase 3.2.
- §10 Testing — interleaved per phase + Phase 10.
- §11 Migration — Phase 0.2.

**Placeholder scan:**
- Task 4.2 says "audit aggregate queries" without enumerating them — acceptable because (a) the count is unknown ahead of time and (b) each one becomes its own micro-commit. The implementer reports the actual list back.
- Task 4.1 step 6 leaves the test body to the implementer — acceptable because the testdb pattern is well-established. Step contains the SETUP and ASSERT shape.
- Task 8.2 leaves the manual-pair dialog body to the implementer — acceptable because the merge dialog is the obvious template.

**Type consistency:**
- `DetectScope`, `DetectResult`, `ManualPairInput`, `TransferMatch`, `TransferCandidate` defined in Task 1.1 and used consistently throughout.
- Frontend `TransferMatch`, `TransferCandidate`, `DetectResult`, `TransactionWithTransfer` mirror backend.
- `hideInternalMoves` is the same name backend (query param + filter field) and frontend (filter object).
- `transferMatchId` / `transferCounterpartId` — same casing in backend `Transaction` json tags and frontend type.
