package classification_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// seedAccountForCascade inserts a checking account directly via raw SQL.
// Mirrors the helper in transactions/merchant_default_test.go but lives here
// so this package's tests don't need to import that test file.
func seedAccountForCascade(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, currency string) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, workspace_id, name, kind, currency, open_date, opening_balance, opening_balance_date, include_in_networth, include_in_savings_rate)
		VALUES ($1, $2, $3, 'checking', $4, $5, 0, $5, true, true)
	`, id, workspaceID, "Test Account "+id.String()[:8], currency, time.Now().UTC())
	if err != nil {
		t.Fatalf("seedAccountForCascade: %v", err)
	}
	return id
}

// insertTxForCascade inserts a transactions row directly via raw SQL so the
// cascade tests can stage merchant_id+category_id pairs without going through
// the transactions service (which has its own merchant-default logic that
// would interfere with the test setup).
func insertTxForCascade(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, accountID uuid.UUID, merchantID *uuid.UUID, categoryID *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, workspace_id, account_id, status, booked_at, amount, currency, merchant_id, category_id)
		VALUES ($1, $2, $3, 'posted', $4, $5, 'CHF', $6, $7)
	`, id, workspaceID, accountID, time.Now().UTC(), "-10.00", merchantID, categoryID)
	if err != nil {
		t.Fatalf("insertTxForCascade: %v", err)
	}
	return id
}

// readTxCategoryID returns the category_id for a transaction (nil if NULL).
func readTxCategoryID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var got *uuid.UUID
	if err := pool.QueryRow(ctx,
		`select category_id from transactions where id = $1`, id,
	).Scan(&got); err != nil {
		t.Fatalf("readTxCategoryID: %v", err)
	}
	return got
}

// TestUpdateMerchant_RenameCapturesAlias verifies that renaming a merchant
// captures the old canonical_name as an alias, so future imports of the old
// raw string still resolve to the (renamed) merchant.
func TestUpdateMerchant_RenameCapturesAlias(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-alias")

	original := "coop-zrh-4567"
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: original})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	newName := "Coop"
	res, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &newName})
	if err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}
	got := res.Merchant
	if got.CanonicalName != newName {
		t.Errorf("CanonicalName = %q, want %q", got.CanonicalName, newName)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("alias count = %d, want 1", len(aliases))
	}
	if aliases[0].RawPattern != original {
		t.Errorf("alias.RawPattern = %q, want %q", aliases[0].RawPattern, original)
	}

	// AttachByRaw with the old raw string should resolve to the renamed
	// merchant via the captured alias.
	resolved, err := svc.AttachByRaw(ctx, wsID, original)
	if err != nil {
		t.Fatalf("AttachByRaw: %v", err)
	}
	if resolved == nil {
		t.Fatal("AttachByRaw returned nil merchant")
	}
	if resolved.ID != m.ID {
		t.Errorf("AttachByRaw resolved to %v, want %v (renamed merchant)", resolved.ID, m.ID)
	}
	if resolved.CanonicalName != newName {
		t.Errorf("resolved.CanonicalName = %q, want %q", resolved.CanonicalName, newName)
	}
}

// TestUpdateMerchant_NoOpRenameDoesNotCaptureAlias verifies that PATCHing
// canonicalName with the same value does not produce an alias row.
func TestUpdateMerchant_NoOpRenameDoesNotCaptureAlias(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-noop")

	name := "Coop"
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: name})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	same := "Coop"
	if _, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &same}); err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("alias count = %d, want 0 (no-op rename should not capture alias)", len(aliases))
	}
}

// TestUpdateMerchant_RenameTwiceCapturesBothNames verifies multiple renames
// each capture their respective old name as a separate alias.
func TestUpdateMerchant_RenameTwiceCapturesBothNames(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-twice")

	a := "A"
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: a})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	b := "B"
	if _, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &b}); err != nil {
		t.Fatalf("UpdateMerchant A->B: %v", err)
	}
	c := "C"
	if _, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &c}); err != nil {
		t.Fatalf("UpdateMerchant B->C: %v", err)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 2 {
		t.Fatalf("alias count = %d, want 2", len(aliases))
	}
	got := map[string]bool{}
	for _, al := range aliases {
		got[al.RawPattern] = true
	}
	if !got["A"] || !got["B"] {
		t.Errorf("aliases = %v, want both A and B captured", got)
	}
}

// TestUpdateMerchant_RenameCollisionWithActiveMerchant verifies that renaming
// a merchant to the canonical_name of another active merchant in the same
// workspace fails. Per the classification package convention (mapWriteError),
// the 23505 unique-violation surfaces as *httpx.ValidationError.
func TestUpdateMerchant_RenameCollisionWithActiveMerchant(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-collision")

	if _, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"}); err != nil {
		t.Fatalf("CreateMerchant Coop: %v", err)
	}
	migros, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant Migros: %v", err)
	}

	conflict := "Coop"
	_, err = svc.UpdateMerchant(ctx, wsID, migros.ID, classification.MerchantPatchInput{CanonicalName: &conflict})
	if err == nil {
		t.Fatal("expected error renaming Migros to Coop, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want *httpx.ValidationError (per mapWriteError convention), got %T: %v", err, err)
	}

	// And no alias should have been captured (transaction rolled back).
	aliases, err := svc.ListAliases(ctx, wsID, migros.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("alias count = %d, want 0 (failed rename should not leave alias)", len(aliases))
	}
}

// TestUpdateMerchant_RenameTakesArchivedMerchantsName verifies the partial
// unique index allows renaming an active merchant to the canonical_name of
// an archived merchant.
func TestUpdateMerchant_RenameTakesArchivedMerchantsName(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-archived")

	old, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "OldCoop"})
	if err != nil {
		t.Fatalf("CreateMerchant OldCoop: %v", err)
	}
	if err := svc.ArchiveMerchant(ctx, wsID, old.ID); err != nil {
		t.Fatalf("ArchiveMerchant OldCoop: %v", err)
	}

	migros, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant Migros: %v", err)
	}

	target := "OldCoop"
	res, err := svc.UpdateMerchant(ctx, wsID, migros.ID, classification.MerchantPatchInput{CanonicalName: &target})
	if err != nil {
		t.Fatalf("UpdateMerchant Migros->OldCoop (archived): %v", err)
	}
	got := res.Merchant
	if got.CanonicalName != target {
		t.Errorf("CanonicalName = %q, want %q", got.CanonicalName, target)
	}

	aliases, err := svc.ListAliases(ctx, wsID, migros.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("alias count = %d, want 1", len(aliases))
	}
	if aliases[0].RawPattern != "Migros" {
		t.Errorf("alias.RawPattern = %q, want Migros", aliases[0].RawPattern)
	}
}

// TestUpdateMerchant_DefaultCategoryCascade_TrueUpdatesMatching verifies that
// PATCHing defaultCategoryId with cascade=true re-categorises only the
// merchant's transactions whose category equals the previous default,
// preserving manual overrides and other-merchant rows.
func TestUpdateMerchant_DefaultCategoryCascade_TrueUpdatesMatching(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-cascade-true")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	catA, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "A"})
	if err != nil {
		t.Fatalf("CreateCategory A: %v", err)
	}
	catB, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "B"})
	if err != nil {
		t.Fatalf("CreateCategory B: %v", err)
	}
	merchant, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "M",
		DefaultCategoryID: &catA.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}
	other, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "Other",
		DefaultCategoryID: &catA.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant other: %v", err)
	}

	// t1: this merchant + category A → should cascade to B.
	t1 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, &catA.ID)
	// t2: this merchant + category B (manual override) → should NOT change.
	t2 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, &catB.ID)
	// t3: this merchant + null category → should NOT change (NULL is distinct from A).
	t3 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, nil)
	// t4: other merchant + category A → should NOT change (different merchant).
	t4 := insertTxForCascade(t, ctx, pool, wsID, accountID, &other.ID, &catA.ID)

	cascade := true
	newDefault := catB.ID.String()
	res, err := svc.UpdateMerchant(ctx, wsID, merchant.ID, classification.MerchantPatchInput{
		DefaultCategoryID: &newDefault,
		Cascade:           &cascade,
	})
	if err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}
	if res.CascadedTransactionCount != 1 {
		t.Errorf("CascadedTransactionCount = %d, want 1", res.CascadedTransactionCount)
	}

	if got := readTxCategoryID(t, ctx, pool, t1); got == nil || *got != catB.ID {
		t.Errorf("t1 category_id = %v, want %v (cascaded)", got, catB.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, t2); got == nil || *got != catB.ID {
		t.Errorf("t2 category_id = %v, want %v (manual override unchanged)", got, catB.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, t3); got != nil {
		t.Errorf("t3 category_id = %v, want nil (null distinct from old default)", got)
	}
	if got := readTxCategoryID(t, ctx, pool, t4); got == nil || *got != catA.ID {
		t.Errorf("t4 category_id = %v, want %v (different merchant unchanged)", got, catA.ID)
	}
}

// TestUpdateMerchant_DefaultCategoryCascade_FalseDoesNothing verifies that
// PATCHing defaultCategoryId without cascade (or with cascade=false) leaves
// existing transaction category_ids untouched.
func TestUpdateMerchant_DefaultCategoryCascade_FalseDoesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-cascade-false")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	catA, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "A"})
	if err != nil {
		t.Fatalf("CreateCategory A: %v", err)
	}
	catB, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "B"})
	if err != nil {
		t.Fatalf("CreateCategory B: %v", err)
	}
	merchant, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "M",
		DefaultCategoryID: &catA.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	t1 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, &catA.ID)
	t2 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, &catB.ID)
	t3 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, nil)

	cascade := false
	newDefault := catB.ID.String()
	res, err := svc.UpdateMerchant(ctx, wsID, merchant.ID, classification.MerchantPatchInput{
		DefaultCategoryID: &newDefault,
		Cascade:           &cascade,
	})
	if err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}
	if res.CascadedTransactionCount != 0 {
		t.Errorf("CascadedTransactionCount = %d, want 0", res.CascadedTransactionCount)
	}

	if got := readTxCategoryID(t, ctx, pool, t1); got == nil || *got != catA.ID {
		t.Errorf("t1 category_id = %v, want %v (cascade=false leaves it alone)", got, catA.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, t2); got == nil || *got != catB.ID {
		t.Errorf("t2 category_id = %v, want %v unchanged", got, catB.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, t3); got != nil {
		t.Errorf("t3 category_id = %v, want nil unchanged", got)
	}

	// And re-confirm with cascade absent (Cascade=nil) on a different merchant
	// state: PATCHing again with no cascade flag should still be a no-op
	// against the new default.
	res2, err := svc.UpdateMerchant(ctx, wsID, merchant.ID, classification.MerchantPatchInput{
		DefaultCategoryID: &newDefault,
	})
	if err != nil {
		t.Fatalf("UpdateMerchant (no cascade): %v", err)
	}
	if res2.CascadedTransactionCount != 0 {
		t.Errorf("CascadedTransactionCount (no cascade flag) = %d, want 0", res2.CascadedTransactionCount)
	}
}

// TestUpdateMerchant_DefaultCategoryCascade_NullOldDefaultFillsNullCategories
// verifies the null-old-default case: when previous default is NULL and
// cascade=true, we update transactions whose category is also NULL (because
// IS NOT DISTINCT FROM treats NULL=NULL as true).
func TestUpdateMerchant_DefaultCategoryCascade_NullOldDefaultFillsNullCategories(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-cascade-null-old")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	catA, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "A"})
	if err != nil {
		t.Fatalf("CreateCategory A: %v", err)
	}
	merchant, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName: "M",
		// no DefaultCategoryID → NULL
	})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	// t1: null category (matches null old-default) → should cascade to A.
	t1 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, nil)
	// t2: already category A (manual) → should NOT change (A is distinct from NULL).
	t2 := insertTxForCascade(t, ctx, pool, wsID, accountID, &merchant.ID, &catA.ID)

	cascade := true
	newDefault := catA.ID.String()
	res, err := svc.UpdateMerchant(ctx, wsID, merchant.ID, classification.MerchantPatchInput{
		DefaultCategoryID: &newDefault,
		Cascade:           &cascade,
	})
	if err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}
	if res.CascadedTransactionCount != 1 {
		t.Errorf("CascadedTransactionCount = %d, want 1", res.CascadedTransactionCount)
	}

	if got := readTxCategoryID(t, ctx, pool, t1); got == nil || *got != catA.ID {
		t.Errorf("t1 category_id = %v, want %v (cascaded from null)", got, catA.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, t2); got == nil || *got != catA.ID {
		t.Errorf("t2 category_id = %v, want %v (manual, value happens to match)", got, catA.ID)
	}
}
