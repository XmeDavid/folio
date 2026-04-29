package transactions_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transactions"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// seedAccount inserts a checking account directly via raw SQL for tests
// that don't need the accounts service's full machinery.
func seedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, currency string) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, workspace_id, name, kind, currency, open_date, opening_balance, opening_balance_date, include_in_networth, include_in_savings_rate)
		VALUES ($1, $2, $3, 'checking', $4, $5, 0, $5, true, true)
	`, id, workspaceID, "Test Account "+id.String()[:8], currency, time.Now().UTC())
	if err != nil {
		t.Fatalf("seedAccount: %v", err)
	}
	return id
}

func ptrStr(s string) *string { return &s }

// TestCreate_AttachesMerchantFromCounterpartyRaw covers the happy-path of
// create-with-counterparty: when no merchantId is passed but counterpartyRaw
// is non-empty, the service resolves (or creates) a merchant and writes its
// id onto the new transaction.
func TestCreate_AttachesMerchantFromCounterpartyRaw(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tx-attach")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	raw := "COOP-4382 ZUR"
	tx, err := svc.Create(ctx, wsID, transactions.CreateInput{
		AccountID:       accountID,
		Status:          "posted",
		BookedAt:        time.Now().UTC(),
		Amount:          decimal.RequireFromString("-12.50"),
		Currency:        "CHF",
		CounterpartyRaw: &raw,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tx.MerchantID == nil {
		t.Fatalf("expected merchant_id to be resolved, got nil")
	}

	// Verify the merchant exists in the workspace and its canonical_name
	// matches the trimmed raw.
	m, err := classSvc.GetMerchant(ctx, wsID, *tx.MerchantID)
	if err != nil {
		t.Fatalf("GetMerchant: %v", err)
	}
	if m.CanonicalName != raw {
		t.Errorf("merchant canonical_name = %q, want %q", m.CanonicalName, raw)
	}
}

// TestCreate_InheritsMerchantDefaultCategory verifies that when the resolved
// merchant has a default category and the input has no categoryId, the new
// transaction inherits the merchant's default category.
func TestCreate_InheritsMerchantDefaultCategory(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tx-default-cat")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	cat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	merchantName := "MIGROS-7711"
	if _, err := classSvc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     merchantName,
		DefaultCategoryID: &cat.ID,
	}); err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	raw := merchantName
	tx, err := svc.Create(ctx, wsID, transactions.CreateInput{
		AccountID:       accountID,
		Status:          "posted",
		BookedAt:        time.Now().UTC(),
		Amount:          decimal.RequireFromString("-22.10"),
		Currency:        "CHF",
		CounterpartyRaw: &raw,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tx.MerchantID == nil {
		t.Fatalf("expected merchant_id to be resolved")
	}
	if tx.CategoryID == nil {
		t.Fatalf("expected category_id to inherit merchant default, got nil")
	}
	if *tx.CategoryID != cat.ID {
		t.Errorf("category_id = %v, want %v", *tx.CategoryID, cat.ID)
	}
}

// TestCreate_ManualCategoryOverridesMerchantDefault verifies the manual-
// override invariant: when the caller passes a categoryId explicitly, we do
// NOT overwrite it with the merchant's default category.
func TestCreate_ManualCategoryOverridesMerchantDefault(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tx-manual-override")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	defaultCat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("CreateCategory groceries: %v", err)
	}
	manualCat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Travel"})
	if err != nil {
		t.Fatalf("CreateCategory travel: %v", err)
	}
	merchantName := "AMAZON-DE"
	if _, err := classSvc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     merchantName,
		DefaultCategoryID: &defaultCat.ID,
	}); err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	raw := merchantName
	tx, err := svc.Create(ctx, wsID, transactions.CreateInput{
		AccountID:       accountID,
		Status:          "posted",
		BookedAt:        time.Now().UTC(),
		Amount:          decimal.RequireFromString("-99.00"),
		Currency:        "CHF",
		CounterpartyRaw: &raw,
		CategoryID:      &manualCat.ID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tx.CategoryID == nil {
		t.Fatalf("expected category_id to be set to manual choice, got nil")
	}
	if *tx.CategoryID != manualCat.ID {
		t.Errorf("category_id = %v, want manual %v (default was %v)", *tx.CategoryID, manualCat.ID, defaultCat.ID)
	}
}

// TestUpdate_AttachingMerchantAppliesDefaultCategoryWhenNull verifies that
// PATCHing a transaction with a non-null merchantId — whose merchant has a
// default category — applies that default when the row has no category yet.
func TestUpdate_AttachingMerchantAppliesDefaultCategoryWhenNull(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tx-update-attach-cat")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	cat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	merchant, err := classSvc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "COOP-LOCAL",
		DefaultCategoryID: &cat.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	// Create with no merchant + no category to set the baseline.
	tx, err := svc.Create(ctx, wsID, transactions.CreateInput{
		AccountID: accountID,
		Status:    "posted",
		BookedAt:  time.Now().UTC(),
		Amount:    decimal.RequireFromString("-7.40"),
		Currency:  "CHF",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tx.CategoryID != nil {
		t.Fatalf("baseline tx should have null category, got %v", *tx.CategoryID)
	}

	// Now PATCH only the merchantId.
	merchantIDStr := merchant.ID.String()
	updated, err := svc.Update(ctx, wsID, tx.ID, transactions.PatchInput{
		MerchantID: &merchantIDStr,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.MerchantID == nil || *updated.MerchantID != merchant.ID {
		t.Fatalf("merchant_id = %v, want %v", updated.MerchantID, merchant.ID)
	}
	if updated.CategoryID == nil {
		t.Fatalf("expected category_id to inherit merchant default, got nil")
	}
	if *updated.CategoryID != cat.ID {
		t.Errorf("category_id = %v, want %v", *updated.CategoryID, cat.ID)
	}
}

// TestUpdate_AttachingMerchantDoesNotOverrideExistingCategory verifies the
// manual-override invariant on Update: if the transaction already has a
// category, attaching a merchant whose default differs does NOT overwrite.
func TestUpdate_AttachingMerchantDoesNotOverrideExistingCategory(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tx-update-no-override")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	defaultCat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("CreateCategory groceries: %v", err)
	}
	existingCat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Dining"})
	if err != nil {
		t.Fatalf("CreateCategory dining: %v", err)
	}
	merchant, err := classSvc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "WALMART",
		DefaultCategoryID: &defaultCat.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	// Create with an explicit category so the row already has one.
	tx, err := svc.Create(ctx, wsID, transactions.CreateInput{
		AccountID:  accountID,
		Status:     "posted",
		BookedAt:   time.Now().UTC(),
		Amount:     decimal.RequireFromString("-31.00"),
		Currency:   "CHF",
		CategoryID: &existingCat.ID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tx.CategoryID == nil || *tx.CategoryID != existingCat.ID {
		t.Fatalf("baseline tx category mismatch: got %v, want %v", tx.CategoryID, existingCat.ID)
	}

	// Now PATCH only the merchant; the existing category must NOT change.
	merchantIDStr := merchant.ID.String()
	updated, err := svc.Update(ctx, wsID, tx.ID, transactions.PatchInput{
		MerchantID: &merchantIDStr,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.MerchantID == nil || *updated.MerchantID != merchant.ID {
		t.Fatalf("merchant_id = %v, want %v", updated.MerchantID, merchant.ID)
	}
	if updated.CategoryID == nil {
		t.Fatalf("category_id should be preserved, got nil")
	}
	if *updated.CategoryID != existingCat.ID {
		t.Errorf("category_id = %v, want %v (must not be overridden by merchant default %v)", *updated.CategoryID, existingCat.ID, defaultCat.ID)
	}
	// Sanity: ensure the merchant default differs from the existing one so this
	// test actually exercises the override-prevention path.
	if defaultCat.ID == existingCat.ID {
		t.Fatal("test setup bug: defaultCat and existingCat are the same")
	}
	_ = ptrStr // keep helper available for future test additions
}
