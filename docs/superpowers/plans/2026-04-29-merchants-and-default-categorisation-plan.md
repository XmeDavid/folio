# Merchants & Default-Categorisation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make merchants the default driver of transaction categorisation: auto-attach by `counterparty_raw` on import, alias capture on rename/merge, default-category cascade on user opt-in, full /merchants list + detail UI.

**Architecture:** Backend in Go (`backend/internal/classification` and `backend/internal/transactions`); SQL via raw `pgx` (existing pattern in this package — sqlc is used for some queries but most of classification uses inline SQL). Frontend in Next.js 16 / React 19 / TanStack Query under `web/`.

**Tech Stack:** Postgres 16, pgx/v5, chi, Go 1.22, Next.js 16, React 19, TanStack Query v5, Tailwind v4, shadcn/ui.

**Spec:** `docs/superpowers/specs/2026-04-29-merchants-and-default-categorisation-design.md`

**Schema reconciliation already done in repo:**
- `merchant_aliases` table exists with shape `(id, workspace_id, merchant_id, raw_pattern, is_regex, created_at)` and `UNIQUE (workspace_id, raw_pattern)`. We use `raw_pattern` everywhere `raw_alias` appears in the spec; `is_regex` stays `false` in v1.
- `merchants` has `UNIQUE (workspace_id, canonical_name)` (unconditional). Phase 0 below converts this to a partial unique so archived merchants don't block active names.
- `transactions.merchant_id` filter and HTTP query parameter (`?merchantId=...`) are already wired in `backend/internal/transactions/service.go` and `http.go`.

---

## Phase 0 — Schema reconciliation

### Task 0.1: Make `merchants.canonical_name` uniqueness partial-on-active

**Files:**
- Create: `backend/db/migrations/20260429000002_merchants_partial_unique.sql`
- Reference: `backend/db/migrations/20260424000003_classification.sql:101` (existing constraint)

- [ ] **Step 1: Write the migration**

```sql
-- Allow archived merchants to share a canonical_name with an active one,
-- so users can archive "MIGROSEXP-7711" and later create a clean "Migros"
-- without the archived row blocking the namespace.
begin;

-- Drop the unconditional unique constraint added by 20260424000003.
alter table merchants drop constraint merchants_workspace_id_canonical_name_key;

-- Replace it with a partial unique that only constrains active rows.
create unique index merchants_active_canonical_name_uniq
  on merchants(workspace_id, canonical_name)
  where archived_at is null;

commit;
```

- [ ] **Step 2: Apply locally and verify**

```bash
cd backend && atlas migrate apply --env local
psql "$DATABASE_URL" -c "\d merchants" | grep -i canonical_name
```

Expected: `merchants_active_canonical_name_uniq` partial index visible; old constraint gone.

- [ ] **Step 3: Run existing tests to confirm nothing regressed**

```bash
cd backend && go test ./internal/classification/...
```

Expected: PASS (existing merchant CRUD tests should not be affected; they don't insert duplicate names).

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260429000002_merchants_partial_unique.sql
git commit -m "feat(db): partial unique on merchants.canonical_name (active rows only)"
```

---

## Phase 1 — `AttachByRaw` helper

### Task 1.1: Service helper with unit-style integration test

**Files:**
- Create: `backend/internal/classification/attach_by_raw.go`
- Create: `backend/internal/classification/attach_by_raw_test.go`

- [ ] **Step 1: Write the failing test**

```go
// backend/internal/classification/attach_by_raw_test.go
package classification_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestAttachByRaw_EmptyReturnsNil(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)

	got, err := svc.AttachByRaw(ctx, ws.ID, "")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestAttachByRaw_CreatesOnFirstSight(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)

	got, err := svc.AttachByRaw(ctx, ws.ID, "COOP-4382 ZUR")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "COOP-4382 ZUR", got.CanonicalName)

	again, err := svc.AttachByRaw(ctx, ws.ID, "COOP-4382 ZUR")
	require.NoError(t, err)
	require.Equal(t, got.ID, again.ID, "second call must reuse the same merchant")
}

func TestAttachByRaw_ResolvesViaAlias(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)

	canonical, err := svc.CreateMerchant(ctx, ws.ID, classification.MerchantCreateInput{
		CanonicalName: "Coop",
	})
	require.NoError(t, err)
	_, err = svc.AddAlias(ctx, ws.ID, canonical.ID, "COOP-4382 ZUR")
	require.NoError(t, err)

	got, err := svc.AttachByRaw(ctx, ws.ID, "COOP-4382 ZUR")
	require.NoError(t, err)
	require.Equal(t, canonical.ID, got.ID)
}

func TestAttachByRaw_ArchivedIgnored(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)

	first, err := svc.AttachByRaw(ctx, ws.ID, "Old Coop")
	require.NoError(t, err)
	require.NoError(t, svc.ArchiveMerchant(ctx, ws.ID, first.ID))

	second, err := svc.AttachByRaw(ctx, ws.ID, "Old Coop")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID, "archived match must not be reused")
}
```

(Tests reference `svc.AddAlias` which doesn't exist yet — that's task 3.1. For now, the test file won't compile. We'll write `AttachByRaw` first, then `AddAlias`. To keep the TDD loop honest, comment out `TestAttachByRaw_ResolvesViaAlias` until task 3.1, or split the test file.)

**Pragmatic substitution:** for step 1 commit only the first, second, and fourth tests. Add the alias test as part of task 3.1.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd backend && go test ./internal/classification/ -run TestAttachByRaw -v
```

Expected: compile error `svc.AttachByRaw undefined`.

- [ ] **Step 3: Implement `AttachByRaw`**

```go
// backend/internal/classification/attach_by_raw.go
package classification

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// AttachByRaw resolves the counterparty_raw string to a Merchant for
// workspaceID, creating one on first sight. Returns nil if raw is empty
// or only whitespace. Archived merchants and archived aliases are ignored.
//
// Concurrency: relies on the partial unique index
// merchants_active_canonical_name_uniq for create-on-conflict idempotency.
func (s *Service) AttachByRaw(ctx context.Context, workspaceID uuid.UUID, raw string) (*Merchant, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	if m, err := s.lookupMerchantByRaw(ctx, workspaceID, raw); err != nil {
		return nil, err
	} else if m != nil {
		return m, nil
	}

	id := uuidx.New()
	row := s.pool.QueryRow(ctx, `
		insert into merchants (id, workspace_id, canonical_name)
		values ($1, $2, $3)
		on conflict (workspace_id, canonical_name) where archived_at is null
		do nothing
		returning `+merchantCols,
		id, workspaceID, raw,
	)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, mapWriteError("merchant", err)
		}
		// Conflict — another concurrent insert won. Re-resolve.
		again, err := s.lookupMerchantByRaw(ctx, workspaceID, raw)
		if err != nil {
			return nil, err
		}
		if again == nil {
			return nil, fmt.Errorf("attach_by_raw: lost-race resolve returned no merchant for %q", raw)
		}
		return again, nil
	}
	return &m, nil
}

func (s *Service) lookupMerchantByRaw(ctx context.Context, workspaceID uuid.UUID, raw string) (*Merchant, error) {
	row := s.pool.QueryRow(ctx, `
		select `+merchantCols+`
		from merchants
		where workspace_id = $1
		  and canonical_name = $2
		  and archived_at is null
		union all
		select `+prefixedMerchantCols("m")+`
		from merchants m
		join merchant_aliases a on a.merchant_id = m.id and a.workspace_id = m.workspace_id
		where a.workspace_id = $1
		  and a.raw_pattern = $2
		  and m.archived_at is null
		limit 1`,
		workspaceID, raw,
	)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup merchant by raw: %w", err)
	}
	return &m, nil
}

// prefixedMerchantCols returns merchantCols with each column prefixed by
// the given alias (used in a UNION where one side joins).
func prefixedMerchantCols(alias string) string {
	cols := strings.Split(merchantCols, ",")
	for i, c := range cols {
		cols[i] = " " + alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ",")
}
```

- [ ] **Step 4: Run tests, expect PASS for the three uncommented**

```bash
cd backend && go test ./internal/classification/ -run TestAttachByRaw -v
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/classification/attach_by_raw.go backend/internal/classification/attach_by_raw_test.go
git commit -m "feat(classification): AttachByRaw resolves or creates merchant from counterparty"
```

---

## Phase 2 — Wire `AttachByRaw` into import + manual create

### Task 2.1: Test that bankimport attaches merchant on imported transactions

**Files:**
- Modify: `backend/internal/bankimport/service.go:634` (`insertImportableTx`)
- Modify: `backend/internal/bankimport/service.go` constructor — add classification dependency
- Modify: `backend/cmd/server/main.go` (or wherever `bankimport.NewService` is wired) — pass classification service
- Test: `backend/internal/bankimport/synthetic_test.go` (extend existing test or new file `attach_test.go`)

- [ ] **Step 1: Add a test that the imported transactions have non-null `merchant_id` matching the raw counterparty**

```go
// backend/internal/bankimport/attach_test.go
package bankimport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/bankimport"
	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestImport_AttachesMerchantByRaw(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	classSvc := classification.NewService(pool)
	bsvc := bankimport.NewService(pool, classSvc)

	ws := testdb.SeedWorkspace(t, pool)
	acct := testdb.SeedAccount(t, pool, ws.ID, "Revolut CHF", "CHF")

	// Apply a synthetic file with two distinct counterparties.
	rows := []bankimport.ParsedTransaction{
		{Date: testdb.Date(2026, 4, 1), Amount: "-42.50", Currency: "CHF", Description: "COOP-4382 ZUR"},
		{Date: testdb.Date(2026, 4, 2), Amount: "-18.20", Currency: "CHF", Description: "COOP-4382 ZUR"},
		{Date: testdb.Date(2026, 4, 3), Amount: "-9.90",  Currency: "CHF", Description: "MIGROSEXP-7711"},
	}
	res, err := bsvc.ImportSynthetic(ctx, ws.ID, acct.ID, rows)  // helper added in step 2 below
	require.NoError(t, err)
	require.Len(t, res.TransactionIDs, 3)

	// Two transactions should share a merchant; the third has its own.
	merchants := testdb.MerchantIDsForTransactions(t, pool, res.TransactionIDs)
	require.Len(t, merchants, 3)
	require.Equal(t, merchants[0], merchants[1], "same raw → same merchant")
	require.NotEqual(t, merchants[0], merchants[2], "different raw → different merchant")
}
```

This test depends on (a) `bankimport.NewService` accepting a `classification.Service`, (b) `bankimport.ImportSynthetic` test helper, and (c) `testdb.MerchantIDsForTransactions`. Implementation in next steps.

- [ ] **Step 2: Run test to verify failure**

```bash
cd backend && go test ./internal/bankimport/ -run TestImport_AttachesMerchantByRaw -v
```

Expected: compile error `NewService` takes wrong number of arguments.

- [ ] **Step 3: Plumb `classification.Service` into bankimport**

Modify `backend/internal/bankimport/service.go`:

```go
type Service struct {
	pool     *pgxpool.Pool
	classSvc *classification.Service
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, classSvc *classification.Service) *Service {
	return &Service{pool: pool, classSvc: classSvc, now: time.Now}
}
```

In `insertImportableTx` (line 634), for each `ParsedTransaction` resolve the merchant **before** the insert:

```go
// Around line 634 inside insertImportableTx, immediately before the row is materialised:
merchantID, defaultCategoryID, err := s.resolveMerchant(ctx, workspaceID, row.Description)
if err != nil {
	return nil, fmt.Errorf("attach merchant: %w", err)
}
// then pass merchantID and defaultCategoryID into the insert (existing parameter list).
// The insert SHOULD set category_id = COALESCE(<existing>, defaultCategoryID).
```

Add the helper:

```go
func (s *Service) resolveMerchant(ctx context.Context, workspaceID uuid.UUID, raw string) (*uuid.UUID, *uuid.UUID, error) {
	m, err := s.classSvc.AttachByRaw(ctx, workspaceID, raw)
	if err != nil || m == nil {
		return nil, nil, err
	}
	return &m.ID, m.DefaultCategoryID, nil
}
```

Adjust the existing INSERT in `insertImportableTx` to write `merchant_id` and (when `defaultCategoryID != nil`) `category_id` from `defaultCategoryID` if the parsed row didn't already carry one.

- [ ] **Step 4: Update `cmd/server/main.go` (or wherever `bankimport.NewService` is called) to pass the classification service**

```go
classSvc := classification.NewService(pool)
bankimportSvc := bankimport.NewService(pool, classSvc)
```

Search-and-replace any other call site (tests, sweeper, admin tool).

- [ ] **Step 5: Add `ImportSynthetic` test helper**

`backend/internal/bankimport/synthetic.go` already has synthetic infrastructure. Add a thin test-only helper:

```go
// In backend/internal/bankimport/synthetic.go (or a new export_test.go):
// ImportSynthetic inserts rows into the DB as if they came from a real
// import, exercising the full attach-and-categorise path. Test helper.
func (s *Service) ImportSynthetic(ctx context.Context, workspaceID, accountID uuid.UUID, rows []ParsedTransaction) (*ApplyResult, error) {
	// Reuse the existing ApplyPlan path with a single-currency, single-account plan.
	// (Concrete arguments depend on existing code; copy from an existing test.)
}
```

Mirror the simplest existing test invocation (e.g., from `synthetic_test.go`).

- [ ] **Step 6: Add `testdb.MerchantIDsForTransactions` helper**

```go
// backend/internal/testdb/queries.go (or similar)
func MerchantIDsForTransactions(t *testing.T, pool *pgxpool.Pool, txIDs []uuid.UUID) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, 0, len(txIDs))
	for _, id := range txIDs {
		var m *uuid.UUID
		require.NoError(t, pool.QueryRow(context.Background(),
			`select merchant_id from transactions where id = $1`, id).Scan(&m))
		require.NotNil(t, m, "transaction %s has nil merchant_id", id)
		out = append(out, *m)
	}
	return out
}
```

- [ ] **Step 7: Run test, expect PASS**

```bash
cd backend && go test ./internal/bankimport/ -run TestImport_AttachesMerchantByRaw -v
```

- [ ] **Step 8: Run all tests to catch regressions**

```bash
cd backend && go test ./...
```

- [ ] **Step 9: Commit**

```bash
git add backend/internal/bankimport/ backend/internal/testdb/ backend/cmd/
git commit -m "feat(bankimport): auto-attach merchant from counterparty_raw on import"
```

### Task 2.2: Wire `AttachByRaw` into manual transaction create + update

**Files:**
- Modify: `backend/internal/transactions/service.go` — `Create` (line ~370) and `Update` (line ~570)
- Modify: `backend/internal/transactions/http.go` — keep API shape but allow `counterpartyRaw` to drive merchant resolution if `merchantId` is null
- Test: `backend/internal/transactions/service_test.go` — extend or new test file

- [ ] **Step 1: Failing test for manual create**

```go
func TestCreate_AttachesMerchantFromCounterpartyRaw(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)
	ws, acct := testdb.SeedWorkspaceAndAccount(t, pool)

	tx, err := svc.Create(ctx, ws.ID, transactions.CreateInput{
		AccountID:       acct.ID,
		Status:          "posted",
		BookedAt:        testdb.Date(2026, 4, 5),
		Amount:          decimal.RequireFromString("-12.30"),
		Currency:        "CHF",
		CounterpartyRaw: ptr("COOP-9000-GVA"),
	})
	require.NoError(t, err)
	require.NotNil(t, tx.MerchantID, "merchant should auto-attach from counterparty_raw")
}

func TestUpdate_AppliesMerchantDefaultCategoryWhenAttachingMerchant(t *testing.T) {
	// 1. Create category + merchant with default category
	// 2. Create transaction with no merchant, no category
	// 3. PATCH merchantId to that merchant
	// 4. Expect categoryId == merchant.defaultCategoryId
	// (full body in implementation)
}
```

- [ ] **Step 2: Run, expect compile error or red test**

```bash
cd backend && go test ./internal/transactions/ -run TestCreate_AttachesMerchant -v
```

- [ ] **Step 3: Plumb `classification.Service` into `transactions.Service` constructor**

```go
type Service struct {
	pool     *pgxpool.Pool
	classSvc *classification.Service
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, classSvc *classification.Service) *Service {
	return &Service{pool: pool, classSvc: classSvc, now: time.Now}
}
```

- [ ] **Step 4: Modify `Create` to resolve merchant when `MerchantID` is nil but `CounterpartyRaw` is non-empty**

In `transactions/service.go` `Create`:

```go
if in.MerchantID == nil && in.CounterpartyRaw != nil && *in.CounterpartyRaw != "" {
	m, err := s.classSvc.AttachByRaw(ctx, workspaceID, *in.CounterpartyRaw)
	if err != nil {
		return nil, fmt.Errorf("attach merchant on create: %w", err)
	}
	if m != nil {
		in.MerchantID = &m.ID
		if in.CategoryID == nil && m.DefaultCategoryID != nil {
			in.CategoryID = m.DefaultCategoryID
		}
	}
}
```

- [ ] **Step 5: Modify `Update` to apply merchant default category when `merchant_id` is being set and `category_id` is currently null**

Inside `Update`, after parsing the `merchantID` change but before the SQL UPDATE, if `merchantID` is being newly set (not cleared) and `categoryID` is not also being set, look up the merchant's `default_category_id` and include it in the update:

```go
if p.merchantIDSet && !p.merchantIDNull && !p.categoryIDSet {
	var defaultCat *uuid.UUID
	err := s.pool.QueryRow(ctx,
		`select default_category_id from merchants where workspace_id=$1 and id=$2`,
		workspaceID, p.merchantID).Scan(&defaultCat)
	if err != nil {
		return nil, fmt.Errorf("read merchant default: %w", err)
	}
	if defaultCat != nil {
		// Only fill if the existing transaction has no category.
		var existingCat *uuid.UUID
		err = s.pool.QueryRow(ctx,
			`select category_id from transactions where workspace_id=$1 and id=$2`,
			workspaceID, id).Scan(&existingCat)
		if err == nil && existingCat == nil {
			sets = append(sets, "category_id = "+next(*defaultCat))
		}
	}
}
```

(This is one extra round-trip; acceptable in v1.)

- [ ] **Step 6: Update `cmd/server/main.go` and tests to pass `classSvc` to `transactions.NewService`**

- [ ] **Step 7: Run tests**

```bash
cd backend && go test ./internal/transactions/... ./internal/bankimport/...
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/transactions/ backend/cmd/
git commit -m "feat(transactions): apply merchant default category on attach"
```

---

## Phase 3 — Aliases CRUD

### Task 3.1: `AddAlias`, `ListAliases`, `RemoveAlias` on the service

**Files:**
- Create: `backend/internal/classification/merchant_aliases.go`
- Test: `backend/internal/classification/merchant_aliases_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package classification_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestAddAlias_Inserts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)
	m, _ := svc.CreateMerchant(ctx, ws.ID, classification.MerchantCreateInput{CanonicalName: "Coop"})

	a, err := svc.AddAlias(ctx, ws.ID, m.ID, "COOP-4382 ZUR")
	require.NoError(t, err)
	require.Equal(t, m.ID, a.MerchantID)
	require.Equal(t, "COOP-4382 ZUR", a.RawPattern)
}

func TestAddAlias_DuplicatePerWorkspaceConflicts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)
	a, _ := svc.CreateMerchant(ctx, ws.ID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	b, _ := svc.CreateMerchant(ctx, ws.ID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	_, err := svc.AddAlias(ctx, ws.ID, a.ID, "FOO")
	require.NoError(t, err)
	_, err = svc.AddAlias(ctx, ws.ID, b.ID, "FOO")
	var verr *httpx.ServiceError
	require.ErrorAs(t, err, &verr)
	require.Equal(t, "alias_conflict", verr.Code)
}

func TestListAliases_ScopedToMerchant(t *testing.T) { /* ... */ }
func TestRemoveAlias_DeletesById(t *testing.T)       { /* ... */ }
```

- [ ] **Step 2: Run, expect compile failure**

```bash
cd backend && go test ./internal/classification/ -run TestAddAlias -v
```

- [ ] **Step 3: Implement**

```go
// backend/internal/classification/merchant_aliases.go
package classification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type MerchantAlias struct {
	ID         uuid.UUID `json:"id"`
	WorkspaceID   uuid.UUID `json:"workspaceId"`
	MerchantID uuid.UUID `json:"merchantId"`
	RawPattern string    `json:"rawPattern"`
	IsRegex    bool      `json:"isRegex"`
	CreatedAt  time.Time `json:"createdAt"`
}

const aliasCols = `id, workspace_id, merchant_id, raw_pattern, is_regex, created_at`

func scanAlias(r interface{ Scan(...any) error }, a *MerchantAlias) error {
	return r.Scan(&a.ID, &a.WorkspaceID, &a.MerchantID, &a.RawPattern, &a.IsRegex, &a.CreatedAt)
}

func (s *Service) AddAlias(ctx context.Context, workspaceID, merchantID uuid.UUID, rawPattern string) (*MerchantAlias, error) {
	if rawPattern == "" {
		return nil, httpx.NewValidationError("rawPattern is required")
	}
	if err := s.assertMerchantBelongs(ctx, workspaceID, merchantID); err != nil {
		return nil, err
	}
	id := uuidx.New()
	row := s.pool.QueryRow(ctx,
		`insert into merchant_aliases (id, workspace_id, merchant_id, raw_pattern)
		 values ($1,$2,$3,$4)
		 returning `+aliasCols,
		id, workspaceID, merchantID, rawPattern,
	)
	var a MerchantAlias
	if err := scanAlias(row, &a); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, httpx.NewServiceError("alias_conflict", "raw pattern is already mapped to another merchant in this workspace", 409)
		}
		return nil, fmt.Errorf("insert alias: %w", err)
	}
	return &a, nil
}

func (s *Service) ListAliases(ctx context.Context, workspaceID, merchantID uuid.UUID) ([]MerchantAlias, error) {
	rows, err := s.pool.Query(ctx,
		`select `+aliasCols+` from merchant_aliases
		 where workspace_id = $1 and merchant_id = $2
		 order by created_at`,
		workspaceID, merchantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()
	out := []MerchantAlias{}
	for rows.Next() {
		var a MerchantAlias
		if err := scanAlias(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) RemoveAlias(ctx context.Context, workspaceID, merchantID, aliasID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`delete from merchant_aliases
		 where workspace_id = $1 and merchant_id = $2 and id = $3`,
		workspaceID, merchantID, aliasID,
	)
	if err != nil {
		return fmt.Errorf("delete alias: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("alias")
	}
	return nil
}

func (s *Service) assertMerchantBelongs(ctx context.Context, workspaceID, merchantID uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`select true from merchants where workspace_id = $1 and id = $2`,
		workspaceID, merchantID,
	).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("merchant")
	}
	return err
}
```

`httpx.NewServiceError` may not exist with that exact signature — check `internal/httpx`. If only `NewValidationError`, `NewNotFoundError`, `NewConflictError` exist, use the closest match (likely `NewConflictError("alias_conflict", "...")`).

- [ ] **Step 4: Run, PASS**

```bash
cd backend && go test ./internal/classification/ -run TestAddAlias -v
```

- [ ] **Step 5: Re-enable the alias test from Phase 1 (`TestAttachByRaw_ResolvesViaAlias`) and re-run**

```bash
cd backend && go test ./internal/classification/...
```

- [ ] **Step 6: Commit**

```bash
git add backend/internal/classification/merchant_aliases.go backend/internal/classification/merchant_aliases_test.go backend/internal/classification/attach_by_raw_test.go
git commit -m "feat(classification): merchant_aliases service CRUD"
```

### Task 3.2: HTTP routes for aliases

**Files:**
- Modify: `backend/internal/classification/http.go` — extend `MountMerchants` and add handlers
- Test: `backend/internal/classification/http_test.go` (extend)

- [ ] **Step 1: Failing HTTP test**

```go
func TestHTTP_MerchantAliases_Crud(t *testing.T) {
	srv, ws, _ := testdb.NewServer(t)
	m := testdb.CreateMerchant(t, srv, ws.ID, "Coop")

	// POST
	resp := srv.Do(t, "POST", "/api/v1/merchants/"+m.ID.String()+"/aliases", `{"rawPattern":"COOP-X"}`, ws)
	require.Equal(t, 201, resp.StatusCode)
	// GET list
	resp = srv.Do(t, "GET", "/api/v1/merchants/"+m.ID.String()+"/aliases", "", ws)
	require.Equal(t, 200, resp.StatusCode)
	// DELETE
	// ...
}
```

(Mirror the testing pattern used elsewhere in `classification/http_test.go`.)

- [ ] **Step 2: Run, expect 404**

- [ ] **Step 3: Add routes**

In `MountMerchants`:

```go
func (h *Handler) MountMerchants(r chi.Router) {
	r.Get("/", h.listMerchants)
	r.Post("/", h.createMerchant)
	r.Get("/{merchantId}", h.getMerchant)
	r.Patch("/{merchantId}", h.updateMerchant)
	r.Delete("/{merchantId}", h.deleteMerchant)

	// new
	r.Get("/{merchantId}/aliases", h.listAliases)
	r.Post("/{merchantId}/aliases", h.addAlias)
	r.Delete("/{merchantId}/aliases/{aliasId}", h.removeAlias)
}
```

Add the three handlers in `http.go` mirroring existing patterns (parse path UUIDs, decode JSON, call service, write response).

- [ ] **Step 4: Run tests, PASS**

- [ ] **Step 5: Commit**

```bash
git add backend/internal/classification/http.go backend/internal/classification/http_test.go
git commit -m "feat(api): merchant alias endpoints"
```

---

## Phase 4 — Rename captures alias + default-category cascade

### Task 4.1: Rename captures old canonical_name as alias

**Files:**
- Modify: `backend/internal/classification/merchants.go` — `UpdateMerchant`
- Modify: `backend/internal/classification/merchants_test.go` (or create)

- [ ] **Step 1: Failing test**

```go
func TestUpdateMerchant_RenameCapturesAlias(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := classification.NewService(pool)
	ws := testdb.SeedWorkspace(t, pool)

	m, _ := svc.CreateMerchant(ctx, ws.ID, classification.MerchantCreateInput{CanonicalName: "coop-zrh-4567"})
	newName := "Coop"
	_, err := svc.UpdateMerchant(ctx, ws.ID, m.ID, classification.MerchantPatchInput{CanonicalName: &newName})
	require.NoError(t, err)

	aliases, err := svc.ListAliases(ctx, ws.ID, m.ID)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	require.Equal(t, "coop-zrh-4567", aliases[0].RawPattern)

	// Re-import the old name attaches to the renamed merchant.
	got, err := svc.AttachByRaw(ctx, ws.ID, "coop-zrh-4567")
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
}

func TestUpdateMerchant_RenameCollisionConflict(t *testing.T) {
	// Create A=Coop, B=Migros. Try to rename B to "Coop". Expect 409.
}
```

- [ ] **Step 2: Run, fail**

- [ ] **Step 3: Wrap `UpdateMerchant` in a transaction; capture old name on rename; map unique violation**

In `merchants.go` `UpdateMerchant`, wrap the body in `tx, _ := s.pool.Begin(ctx); defer tx.Rollback(ctx)`. Before applying the SET, read the current canonical_name. If `p.canonicalNameSet && p.canonicalName != current`, after the UPDATE insert the old name into `merchant_aliases` with `ON CONFLICT (workspace_id, raw_pattern) DO NOTHING`. Map a `pgErr.Code == "23505"` to `httpx.NewConflictError("merchant_name_conflict", "another active merchant in this workspace already has that name")`.

Pseudocode at the top of `UpdateMerchant`:

```go
tx, err := s.pool.Begin(ctx)
if err != nil { return nil, err }
defer tx.Rollback(ctx)

var existingName string
err = tx.QueryRow(ctx,
	`select canonical_name from merchants where workspace_id=$1 and id=$2`,
	workspaceID, id).Scan(&existingName)
if err != nil { return nil, ... }

// existing PATCH SQL — change s.pool to tx
// after UPDATE returns successfully:
if p.canonicalNameSet && p.canonicalName != existingName {
	_, err = tx.Exec(ctx,
		`insert into merchant_aliases (id, workspace_id, merchant_id, raw_pattern)
		 values ($1, $2, $3, $4)
		 on conflict (workspace_id, raw_pattern) do nothing`,
		uuidx.New(), workspaceID, id, existingName)
	if err != nil { return nil, fmt.Errorf("capture old name as alias: %w", err) }
}
return &m, tx.Commit(ctx)
```

- [ ] **Step 4: Run tests, PASS**

- [ ] **Step 5: Commit**

```bash
git add backend/internal/classification/merchants.go backend/internal/classification/merchants_test.go
git commit -m "feat(classification): rename merchant captures old name as alias"
```

### Task 4.2: PATCH supports `cascade` flag for default-category change

**Files:**
- Modify: `backend/internal/classification/merchants.go` — `MerchantPatchInput`, `UpdateMerchant`, response
- Modify: `backend/internal/classification/http.go` — `merchantPatchReq`, response shape
- Test: extend `merchants_test.go`

- [ ] **Step 1: Failing test**

```go
func TestUpdateMerchant_DefaultCategoryCascade(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	classSvc := classification.NewService(pool)
	ws, acct := testdb.SeedWorkspaceAndAccount(t, pool)

	groceries := testdb.CreateLeafCategory(t, classSvc, ws.ID, "Groceries")
	other := testdb.CreateLeafCategory(t, classSvc, ws.ID, "Daily essentials")
	m, _ := classSvc.CreateMerchant(ctx, ws.ID, classification.MerchantCreateInput{
		CanonicalName: "Coop", DefaultCategoryID: &groceries.ID,
	})

	// 3 transactions:
	//   1: category=groceries (matches old default — should cascade)
	//   2: category=other     (manual override — should NOT cascade)
	//   3: category=null      (matches null-old-default predicate when old default is groceries? No — it doesn't. Stays null.)
	mkTx := func(catID *uuid.UUID) uuid.UUID {
		return testdb.CreateTransaction(t, acct, m.ID, catID)
	}
	t1 := mkTx(&groceries.ID)
	t2 := mkTx(&other.ID)
	t3 := mkTx(nil)

	res, err := classSvc.UpdateMerchant(ctx, ws.ID, m.ID, classification.MerchantPatchInput{
		DefaultCategoryID: ptr(other.ID.String()),
		Cascade:           ptr(true),
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.CascadedTransactionCount)

	require.Equal(t, &other.ID, testdb.TxCategoryID(t, pool, t1))
	require.Equal(t, &other.ID, testdb.TxCategoryID(t, pool, t2)) // unchanged: was already 'other'
	require.Nil(t, testdb.TxCategoryID(t, pool, t3))              // unchanged: was null, old default was groceries
}
```

Note the predicate uses `IS NOT DISTINCT FROM old_default`, so when `old_default = groceries`, only `category_id = groceries` matches. `null` doesn't match `groceries`, so `t3` correctly stays null. (When `old_default` is itself null on first set, all-null transactions would match; that case has its own test.)

Add a separate `TestUpdateMerchant_DefaultCategoryCascade_FromNull` for the null-old-default case.

- [ ] **Step 2: Run, fail**

- [ ] **Step 3: Add `Cascade *bool` to `MerchantPatchInput` and `merchantPatchReq`**

```go
// merchants.go
type MerchantPatchInput struct {
	// existing fields...
	Cascade *bool
}

// http.go
type merchantPatchReq struct {
	// existing...
	Cascade *bool `json:"cascade"`
}
```

The handler maps `req.Cascade` into `in.Cascade` then calls `UpdateMerchant`.

- [ ] **Step 4: Implement cascade in `UpdateMerchant`**

Inside the transaction (already wrapped from task 4.1), if `p.defaultCategoryIDSet` and `raw.Cascade != nil && *raw.Cascade`:

```go
var oldDefault *uuid.UUID
err := tx.QueryRow(ctx,
	`select default_category_id from merchants where workspace_id=$1 and id=$2`,
	workspaceID, id).Scan(&oldDefault)
// existing UPDATE merchants ... runs first
// then:
var newDefault any = nil
if !p.defaultCategoryIDNull { newDefault = p.defaultCategoryID }
tag, err := tx.Exec(ctx, `
	update transactions
	set category_id = $3
	where workspace_id = $1 and merchant_id = $2 and category_id is not distinct from $4`,
	workspaceID, id, newDefault, oldDefault)
result.CascadedTransactionCount = int(tag.RowsAffected())
```

Return shape:

```go
type MerchantPatchResult struct {
	Merchant                 *Merchant `json:"merchant"`
	CascadedTransactionCount int       `json:"cascadedTransactionCount,omitempty"`
}
```

Adjust handler `updateMerchant` to write `MerchantPatchResult` (back-compatible: when `cascade` was not requested, `cascadedTransactionCount` is 0 / omitted).

- [ ] **Step 5: Run tests**

```bash
cd backend && go test ./internal/classification/...
```

- [ ] **Step 6: Commit**

```bash
git add backend/internal/classification/merchants.go backend/internal/classification/http.go backend/internal/classification/merchants_test.go
git commit -m "feat(classification): merchant default-category cascade flag"
```

---

## Phase 5 — Merge

### Task 5.1: `MergeMerchants` service method (write-side)

**Files:**
- Create: `backend/internal/classification/merge.go`
- Test: `backend/internal/classification/merge_test.go`

- [ ] **Step 1: Failing tests** (one large parameterised test or 5 focused tests)

```go
func TestMergeMerchants_HappyPath(t *testing.T) {
	// Setup: source S with 2 txns, 1 alias, default=snacks; target T with default=groceries
	// Expect:
	//   - txns moved to T
	//   - S's alias reparented to T
	//   - S's canonical name added as alias of T
	//   - S row deleted
	//   - movedCount=2, capturedAliasCount=2 (1 reparented + 1 canonical-as-alias)
}

func TestMergeMerchants_FillsBlankMetadata(t *testing.T) {
	// S has logo, T does not. After merge, T.logo == S.logo.
}

func TestMergeMerchants_DefaultCategoryNotFilled(t *testing.T) {
	// T has no default; S has default. After merge, T.default still null.
}

func TestMergeMerchants_ApplyTargetDefaultCascadesOnlyMatchingMoved(t *testing.T) {
	// Of 2 moved txns, 1 has category=S.oldDefault, 1 has manual category.
	// applyTargetDefault=true → 1 cascaded.
}

func TestMergeMerchants_OverlappingAliasesNoConflict(t *testing.T) {
	// S has alias "FOO". T also has alias "FOO" (rare but possible).
	// Merge succeeds; S's alias is dropped (ON CONFLICT DO NOTHING).
}

func TestMergeMerchants_SourceEqualsTarget(t *testing.T) {
	// Returns 400 / merge_source_equals_target.
}

func TestMergeMerchants_TargetArchived(t *testing.T) {
	// Returns 422 / merge_target_archived.
}
```

- [ ] **Step 2: Run, fail**

- [ ] **Step 3: Implement (full body)**

```go
// backend/internal/classification/merge.go
package classification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type MergeMerchantsInput struct {
	TargetID            uuid.UUID
	ApplyTargetDefault  bool
}

type MergeMerchantsResult struct {
	Target              *Merchant `json:"target"`
	MovedCount          int       `json:"movedCount"`
	CascadedCount       int       `json:"cascadedCount"`
	CapturedAliasCount  int       `json:"capturedAliasCount"`
}

func (s *Service) MergeMerchants(ctx context.Context, workspaceID, sourceID uuid.UUID, in MergeMerchantsInput) (*MergeMerchantsResult, error) {
	if sourceID == in.TargetID {
		return nil, httpx.NewServiceError("merge_source_equals_target", "source and target must differ", 400)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil { return nil, err }
	defer tx.Rollback(ctx)

	// Lock both rows in deterministic order to avoid deadlocks.
	first, second := sourceID, in.TargetID
	if first.String() > second.String() {
		first, second = second, first
	}
	if _, err := tx.Exec(ctx,
		`select id from merchants where workspace_id=$1 and id in ($2,$3) order by id for update`,
		workspaceID, first, second); err != nil {
		return nil, fmt.Errorf("lock merchants: %w", err)
	}

	// Read both.
	var src, tgt Merchant
	if err := scanMerchant(tx.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id=$1 and id=$2`,
		workspaceID, sourceID), &src); err != nil {
		if errors.Is(err, pgx.ErrNoRows) { return nil, httpx.NewNotFoundError("merchant") }
		return nil, err
	}
	if err := scanMerchant(tx.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id=$1 and id=$2`,
		workspaceID, in.TargetID), &tgt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) { return nil, httpx.NewNotFoundError("merchant") }
		return nil, err
	}
	if tgt.ArchivedAt != nil {
		return nil, httpx.NewServiceError("merge_target_archived", "merge target is archived", 422)
	}

	// 1. Reparent existing aliases from source to target.
	tag1, err := tx.Exec(ctx, `
		insert into merchant_aliases (id, workspace_id, merchant_id, raw_pattern, is_regex)
		select gen_random_uuid(), workspace_id, $2, raw_pattern, is_regex
		from merchant_aliases
		where workspace_id = $1 and merchant_id = $3
		on conflict (workspace_id, raw_pattern) do nothing
	`, workspaceID, in.TargetID, sourceID)
	if err != nil { return nil, fmt.Errorf("reparent aliases: %w", err) }
	if _, err := tx.Exec(ctx,
		`delete from merchant_aliases where workspace_id=$1 and merchant_id=$2`,
		workspaceID, sourceID); err != nil { return nil, err }

	// 2. Capture source canonical name as alias of target.
	tag2, err := tx.Exec(ctx, `
		insert into merchant_aliases (id, workspace_id, merchant_id, raw_pattern)
		values ($1, $2, $3, $4)
		on conflict (workspace_id, raw_pattern) do nothing
	`, uuidx.New(), workspaceID, in.TargetID, src.CanonicalName)
	if err != nil { return nil, fmt.Errorf("capture canonical alias: %w", err) }

	capturedAliasCount := int(tag1.RowsAffected() + tag2.RowsAffected())

	// 3. Move transactions, capture moved IDs.
	rows, err := tx.Query(ctx,
		`update transactions set merchant_id=$2, updated_at=now()
		 where workspace_id=$1 and merchant_id=$3
		 returning id`,
		workspaceID, in.TargetID, sourceID)
	if err != nil { return nil, fmt.Errorf("move transactions: %w", err) }
	var movedIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil { rows.Close(); return nil, err }
		movedIDs = append(movedIDs, id)
	}
	rows.Close()

	// 4. Fill blanks on target metadata (NOT default_category_id).
	if _, err := tx.Exec(ctx, `
		update merchants t set
		  logo_url = coalesce(t.logo_url, s.logo_url),
		  industry = coalesce(t.industry, s.industry),
		  website  = coalesce(t.website,  s.website),
		  notes    = coalesce(t.notes,    s.notes)
		from merchants s
		where t.workspace_id = $1 and t.id = $2 and s.id = $3
	`, workspaceID, in.TargetID, sourceID); err != nil {
		return nil, fmt.Errorf("fill blanks: %w", err)
	}

	// 5. Optional cascade.
	cascadedCount := 0
	if in.ApplyTargetDefault && len(movedIDs) > 0 {
		// Re-read tgt's default in case fill-blanks changed it (it doesn't for default_category_id, but defensive).
		var newDefault *uuid.UUID
		if err := tx.QueryRow(ctx,
			`select default_category_id from merchants where workspace_id=$1 and id=$2`,
			workspaceID, in.TargetID).Scan(&newDefault); err != nil { return nil, err }
		if newDefault != nil {
			tag, err := tx.Exec(ctx, `
				update transactions set category_id = $2, updated_at = now()
				where id = any($1)
				  and category_id is not distinct from $3
			`, movedIDs, newDefault, src.DefaultCategoryID)
			if err != nil { return nil, fmt.Errorf("cascade default: %w", err) }
			cascadedCount = int(tag.RowsAffected())
		}
	}

	// 6. Delete source.
	if _, err := tx.Exec(ctx,
		`delete from merchants where workspace_id=$1 and id=$2`,
		workspaceID, sourceID); err != nil {
		return nil, fmt.Errorf("delete source merchant: %w", err)
	}

	// Re-read target for response.
	var finalTgt Merchant
	if err := scanMerchant(tx.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id=$1 and id=$2`,
		workspaceID, in.TargetID), &finalTgt); err != nil { return nil, err }

	if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit merge: %w", err) }

	return &MergeMerchantsResult{
		Target:             &finalTgt,
		MovedCount:         len(movedIDs),
		CascadedCount:      cascadedCount,
		CapturedAliasCount: capturedAliasCount,
	}, nil
}
```

- [ ] **Step 4: Run, PASS**

- [ ] **Step 5: Commit**

```bash
git add backend/internal/classification/merge.go backend/internal/classification/merge_test.go
git commit -m "feat(classification): MergeMerchants moves txns, reparents aliases, fills blanks"
```

### Task 5.2: `PreviewMerge` (read-side counts)

**Files:**
- Modify: `backend/internal/classification/merge.go`
- Test: `backend/internal/classification/merge_test.go`

- [ ] **Step 1: Failing test — preview counts equal merge result counts**

```go
func TestPreviewMerge_MatchesActualMerge(t *testing.T) {
	// Set up source/target/aliases/transactions.
	pre, err := classSvc.PreviewMerge(ctx, ws.ID, src.ID, target.ID)
	require.NoError(t, err)
	got, err := classSvc.MergeMerchants(ctx, ws.ID, src.ID, classification.MergeMerchantsInput{
		TargetID: target.ID, ApplyTargetDefault: true,
	})
	require.NoError(t, err)
	require.Equal(t, pre.MovedCount, got.MovedCount)
	require.Equal(t, pre.CapturedAliasCount, got.CapturedAliasCount)
	require.Equal(t, pre.CascadedCountIfApplied, got.CascadedCount)
}
```

- [ ] **Step 2: Run, fail**

- [ ] **Step 3: Implement**

```go
type MergePreview struct {
	SourceCanonicalName    string `json:"sourceCanonicalName"`
	TargetCanonicalName    string `json:"targetCanonicalName"`
	MovedCount             int    `json:"movedCount"`
	CapturedAliasCount     int    `json:"capturedAliasCount"`
	CascadedCountIfApplied int    `json:"cascadedCountIfApplied"`
	BlankFillFields        []string `json:"blankFillFields"` // fields that would be filled
}

func (s *Service) PreviewMerge(ctx context.Context, workspaceID, sourceID, targetID uuid.UUID) (*MergePreview, error) {
	// Read source, target. Validate same as merge.
	// Compute counts via SELECTs without writing:
	//   - movedCount = count(*) from transactions where merchant_id = source
	//   - capturedAliasCount = (count of source aliases not already on target) + (1 if source canonical not already an alias of target)
	//   - cascadedCountIfApplied = count of transactions where merchant_id=source and category_id IS NOT DISTINCT FROM source.default_category_id, IF target.default_category_id is not null; else 0
	//   - blankFillFields = []string{"logoUrl"} when target.logo_url is null and source.logo_url is not null, etc.
	// Return.
}
```

- [ ] **Step 4: Run, PASS**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(classification): PreviewMerge read-only count endpoint logic"
```

### Task 5.3: HTTP routes for merge + preview

**Files:**
- Modify: `backend/internal/classification/http.go`
- Test: `backend/internal/classification/http_test.go`

- [ ] **Step 1: Failing HTTP test for both endpoints**

```go
func TestHTTP_MergeMerchant_PreviewAndApply(t *testing.T) {
	// POST /api/v1/merchants/{src}/merge/preview {targetId}
	// → 200, counts in body
	// POST /api/v1/merchants/{src}/merge {targetId, applyTargetDefault: true}
	// → 200, target + counts; src is gone
}
```

- [ ] **Step 2: Run, fail (404)**

- [ ] **Step 3: Add routes inside `MountMerchants`**

```go
r.Post("/{merchantId}/merge/preview", h.previewMerge)
r.Post("/{merchantId}/merge", h.mergeMerchant)
```

Add the two handlers — parse `merchantId` from path, decode body `{targetId, applyTargetDefault}`, call service, write JSON.

- [ ] **Step 4: Run, PASS**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(api): merchant merge + preview endpoints"
```

---

## Phase 6 — Frontend API client

### Task 6.1: Extend `web/lib/api/client.ts` and add merchant types

**Files:**
- Modify: `web/lib/api/client.ts`
- Modify: `web/lib/api/schema.d.ts` if it's auto-generated; otherwise add types in `client.ts`

- [ ] **Step 1: Add types**

```ts
export type Merchant = {
  id: string;
  workspaceId: string;
  canonicalName: string;
  logoUrl?: string | null;
  defaultCategoryId?: string | null;
  industry?: string | null;
  website?: string | null;
  notes?: string | null;
  archivedAt?: string | null;
  createdAt: string;
  updatedAt: string;
};

export type MerchantAlias = {
  id: string;
  workspaceId: string;
  merchantId: string;
  rawPattern: string;
  isRegex: boolean;
  createdAt: string;
};

export type MergePreview = {
  sourceCanonicalName: string;
  targetCanonicalName: string;
  movedCount: number;
  capturedAliasCount: number;
  cascadedCountIfApplied: number;
  blankFillFields: string[];
};

export type MergeResult = {
  target: Merchant;
  movedCount: number;
  cascadedCount: number;
  capturedAliasCount: number;
};
```

- [ ] **Step 2: Add API client functions**

```ts
export async function fetchMerchants(workspaceId: string, opts?: { includeArchived?: boolean }) { ... }
export async function fetchMerchant(workspaceId: string, id: string) { ... }
export async function createMerchant(workspaceId: string, body: { canonicalName: string; defaultCategoryId?: string | null; ... }) { ... }
export async function updateMerchant(workspaceId: string, id: string, body: Partial<MerchantPatchBody> & { cascade?: boolean }) { ... }
export async function archiveMerchant(workspaceId: string, id: string) { ... }
export async function listMerchantAliases(workspaceId: string, merchantId: string) { ... }
export async function addMerchantAlias(workspaceId: string, merchantId: string, body: { rawPattern: string }) { ... }
export async function removeMerchantAlias(workspaceId: string, merchantId: string, aliasId: string) { ... }
export async function previewMergeMerchants(workspaceId: string, sourceId: string, body: { targetId: string }) { ... }
export async function mergeMerchants(workspaceId: string, sourceId: string, body: { targetId: string; applyTargetDefault: boolean }) { ... }
```

Mirror the existing `fetchCategories` / `createCategory` style in the same file. Use existing `apiFetch` / `ApiError` helpers.

- [ ] **Step 3: Type-check**

```bash
cd web && pnpm tsc --noEmit
```

- [ ] **Step 4: Commit**

```bash
git add web/lib/api/client.ts web/lib/api/schema.d.ts
git commit -m "feat(web): merchant + alias API client"
```

---

## Phase 7 — Merchants list page

### Task 7.1: Replace placeholder with real list

**Files:**
- Modify: `web/app/w/[slug]/merchants/page.tsx`
- Create: `web/components/classification/merchants-table.tsx`

- [ ] **Step 1: Replace `merchants/page.tsx`** — swap placeholder for `<MerchantsTable />`. Mirror the structure of `categories/page.tsx`.

- [ ] **Step 2: Build `MerchantsTable`** — uses `useQuery(['merchants', workspaceId, includeArchived], fetchMerchants)`. Columns: name (with logo), default category (resolve from `categories` query), txn count, last seen (these last two require backend extension — see step 3 below; for now show `—`). Search box (client-side filter on canonical name). "Show archived" toggle. "New merchant" button opens an inline create form.

- [ ] **Step 3 (deferred):** Adding `transactionCount` and `lastSeenAt` to merchant list is a separate Phase 12 task — too much SQL aggregation work for v1. Show `—` for now.

- [ ] **Step 4: Verify in dev server**

```bash
cd web && pnpm dev
# Navigate to /w/<slug>/merchants
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(web): merchants list page (search, archive toggle, create form)"
```

---

## Phase 8 — Merchant detail page

### Task 8.1: Route + sidebar + transactions table

**Files:**
- Create: `web/app/w/[slug]/merchants/[merchantId]/page.tsx`
- Create: `web/components/classification/merchant-detail-sidebar.tsx`
- Create: `web/components/classification/merchant-aliases.tsx`
- Reuse / extract: a transactions table component from `web/app/w/[slug]/transactions/page.tsx` if not already extracted

- [ ] **Step 1: Build the route page** with two-column grid (sidebar + table). React Query: `useQuery(['merchant', wsId, id], fetchMerchant)`, `useQuery(['merchant-aliases', wsId, id], listMerchantAliases)`, `useQuery(['transactions', wsId, { merchantId }], () => fetchTransactions({ merchantId: id }))`.

- [ ] **Step 2: Build `MerchantDetailSidebar`** — editable canonical name (calls `updateMerchant` on blur/save), editable default category (Select of categories, fires the cascade dialog from Task 9.1 if merchant has > 0 transactions), readonly stats (count from txn query), action buttons: "Merge into…", "Archive". Handles 409 `merchant_name_conflict` from rename — show error banner.

- [ ] **Step 3: Build `MerchantAliases`** — list, X to remove, "Add alias" button with input. Show `is_regex` badge if true (always false in v1, but UI is honest about it).

- [ ] **Step 4: Reuse transactions table** — if a `<TransactionsTable filter={{ merchantId }} />` component doesn't exist, extract one from the existing `transactions/page.tsx`. Otherwise just use it.

- [ ] **Step 5: Manual smoke** — navigate to a merchant from the list, edit name, refresh the page, check the alias was captured.

- [ ] **Step 6: Commit**

```bash
git commit -am "feat(web): merchant detail page (sidebar + aliases + transactions)"
```

---

## Phase 9 — Dialogs

### Task 9.1: Default-category cascade dialog

**Files:**
- Create: `web/components/classification/merchant-default-category-dialog.tsx`

- [ ] **Step 1: Component** — props: `merchantId`, `oldDefaultId`, `newDefaultId`, `transactionCount`, `onConfirm(cascade: boolean) => void`, `onCancel()`. If `transactionCount === 0`, the wrapper skips the dialog and calls `onConfirm(false)` directly. Otherwise renders a small modal: "Apply [newCategoryName] as the default category to all [N] transactions of this merchant whose category currently matches [oldCategoryName]?" with [Apply to all] and [Only future]. Uses shadcn `Dialog` (or whatever the existing app uses for confirmations — check `components/ui` for an existing dialog primitive).

- [ ] **Step 2: Wire into sidebar's default-category change handler.**

- [ ] **Step 3: Commit**

```bash
git commit -am "feat(web): merchant default-category cascade dialog"
```

### Task 9.2: Merge dialog

**Files:**
- Create: `web/components/classification/merchant-merge-dialog.tsx`

- [ ] **Step 1: Component** — async-search target (debounced query against `fetchMerchants` with name filter; exclude source and archived). On select → fire `previewMergeMerchants`. Render preview block (counts, blank-fill fields). Checkbox "Apply target's default to N matching transactions" — disabled when `cascadedCountIfApplied === 0`. Confirm calls `mergeMerchants`; on success, redirect to `/merchants/[targetId]`.

- [ ] **Step 2: Wire "Merge into…" button in sidebar to open this dialog.**

- [ ] **Step 3: Commit**

```bash
git commit -am "feat(web): merchant merge dialog with preview"
```

---

## Phase 10 — Cross-page wiring

### Task 10.1: Merchant filter + link on /transactions

**Files:**
- Modify: `web/app/w/[slug]/transactions/page.tsx`

- [ ] **Step 1: Add a Merchant filter** to the existing filter bar. Reuse the same async-search picker from the merge dialog (extract a shared `MerchantPicker` if both need it). On change, append `merchantId` to the transactions query key + URL.

- [ ] **Step 2: Make the merchant cell a link** to `/w/<slug>/merchants/<id>`.

- [ ] **Step 3: Verify in dev** — filter works, deep-links roundtrip via `?merchantId=...`.

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(web): transactions page merchant filter + link to detail"
```

---

## Phase 11 — Final integration tests

### Task 11.1: End-to-end smoke test (Go)

**Files:**
- Create: `backend/internal/classification/e2e_test.go`

- [ ] **Step 1: Test the full flow**

```go
func TestE2E_ImportMergeReimport(t *testing.T) {
	// 1. Import 3 transactions: 2 with raw "MIGROSEXP-7711", 1 with raw "MIGROS BERN"
	// 2. Verify two distinct merchants exist
	// 3. Merge "MIGROSEXP-7711" into "MIGROS BERN" with applyTargetDefault=false
	// 4. Verify all 3 transactions point at the surviving merchant
	// 5. Re-import a 4th transaction with raw "MIGROSEXP-7711"
	// 6. Verify it attaches to the surviving merchant via the alias path
}
```

- [ ] **Step 2: Run the full test suite**

```bash
cd backend && go test ./...
```

- [ ] **Step 3: Frontend type-check + lint**

```bash
cd web && pnpm tsc --noEmit && pnpm lint
```

- [ ] **Step 4: Commit**

```bash
git commit -am "test(classification): end-to-end import → merge → re-import"
```

---

## Phase 12 — Deferred (out of v1, list for the record)

- `transactionCount` / `lastSeenAt` / `totalSpend` aggregation on the merchants list endpoint (requires a denormalised count or window query).
- Bulk merge (multi-select in the list).
- Merge undo (24-hour soft-delete).
- Auto-suggest merge targets via prefix/Levenshtein.

---

## Self-review notes (carried out by the plan author)

**Spec coverage:** All 12 spec sections covered.
- §1–3 (goals/principles/out-of-scope) — informational, no tasks.
- §4 (data model) — Phase 0.
- §5 (import behavior) — Phase 1 + 2.
- §6 (lifecycle ops: rename / cascade / merge / archive) — Phases 4 + 5. Archive already implemented; no task needed.
- §7 (API surface) — Phases 1, 3.2, 4.2, 5.3.
- §8 (frontend) — Phases 6–10.
- §9 (concurrency) — covered inside Phase 5 (FOR UPDATE + ON CONFLICT).
- §10 (testing) — interleaved per phase, plus Phase 11.
- §11 (migration) — Phase 0.
- §12 (open questions) — Phase 12.

**Type consistency:**
- Backend uses `RawPattern` consistently (matches existing column).
- Frontend uses `rawPattern` consistently in TS types.
- Merchant patch result `CascadedTransactionCount` matches between Go struct and TS hook.

**Schema reconciliation footnote:** Phase 0 fixes the unconditional unique on `merchants.canonical_name`. If the user prefers to keep the unconditional unique, drop Phase 0 and update §4.1 of the spec to match. The rest of the plan still works — archived merchants block names. Worth confirming with the user before starting Phase 0 if there's any data already in production where archived rows would hold popular names.
