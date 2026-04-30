package bankimport_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/bankimport"
	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transfers"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// seedAccount inserts a checking account directly via raw SQL — no helper
// exists in testdb for this and going through the accounts service drags
// in unrelated machinery (snapshots, currency validation) that this test
// doesn't care about. Returns the account id for use in import params.
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

// seedImportBatch inserts an import_batches row required by the source_refs
// FK that the InsertSourceRef helper writes.
func seedImportBatch(t *testing.T, ctx context.Context, q *dbq.Queries, workspaceID uuid.UUID) uuid.UUID {
	t.Helper()
	batchID := uuidx.New()
	fileName := "attach_test.csv"
	fileHash := "deadbeef"
	if err := q.InsertImportBatch(ctx, dbq.InsertImportBatchParams{
		ID:          batchID,
		WorkspaceID: workspaceID,
		FileName:    &fileName,
		FileHash:    &fileHash,
		Summary:     []byte(`{}`),
		StartedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertImportBatch: %v", err)
	}
	return batchID
}

// strPtr returns a pointer to s. Inline because tests fight test files for
// a shared `ptr` helper in the same package.
func strPtr(s string) *string { return &s }

func mkTx(booked time.Time, amount string, currency string, raw *string) bankimport.ParsedTransaction {
	return bankimport.ParsedTransaction{
		BookedAt:        booked,
		Amount:          decimal.RequireFromString(amount),
		Currency:        currency,
		CounterpartyRaw: raw,
		ExternalID:      "ext-" + uuid.NewString(),
		Raw:             map[string]string{},
	}
}

// TestApply_AttachesMerchantByRaw verifies the bank-import pipeline resolves
// counterparty_raw to merchants via classification.AttachByRaw. Specifically:
//   - Two rows with the same raw share one merchant_id (idempotent attach).
//   - Rows with a different raw get a different merchant_id.
//   - Rows with nil/empty raw keep merchant_id = NULL.
//   - When the merchant has a default_category_id, the inserted transaction
//     inherits it as category_id.
func TestApply_AttachesMerchantByRaw(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	bsvc := bankimport.NewService(pool, classSvc, transfers.NewService(pool))

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-import-attach")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	// Pre-create a category + merchant so we can verify default-category
	// inheritance for one of the imported rows.
	cat, err := classSvc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	preMerchantRaw := "PREEXISTING-MIGROS"
	preM, err := classSvc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     preMerchantRaw,
		DefaultCategoryID: &cat.ID,
	})
	if err != nil {
		t.Fatalf("create pre-merchant: %v", err)
	}

	q := dbq.New(pool)
	batchID := seedImportBatch(t, ctx, q, wsID)

	now := time.Now().UTC()
	coopRaw := "COOP-4382 ZUR"
	migrosRaw := "MIGROSEXP-7711"
	emptyStr := ""
	rows := []bankimport.ParsedTransaction{
		mkTx(now, "-12.50", "CHF", &coopRaw),
		mkTx(now.Add(time.Hour), "-9.95", "CHF", &coopRaw), // same merchant
		mkTx(now.Add(2*time.Hour), "-7.80", "CHF", &migrosRaw),
		mkTx(now.Add(3*time.Hour), "-100.00", "CHF", nil),       // nil raw
		mkTx(now.Add(4*time.Hour), "-5.00", "CHF", &emptyStr),   // empty raw
		mkTx(now.Add(5*time.Hour), "-21.00", "CHF", strPtr(preMerchantRaw)),
	}

	ids, err := bsvc.InsertImportableTxForTest(ctx, q, wsID, accountID, batchID, "file:test", rows)
	if err != nil {
		t.Fatalf("insertImportableTx: %v", err)
	}
	if len(ids) != len(rows) {
		t.Fatalf("inserted %d rows, want %d", len(ids), len(rows))
	}

	// Pull each row back and assert merchant_id / category_id by external_id
	// match. Using the inserted ids list keeps order stable.
	type rec struct {
		merchantID *uuid.UUID
		categoryID *uuid.UUID
	}
	got := make([]rec, len(ids))
	for i, id := range ids {
		var mID, cID pgtype.UUID
		if err := pool.QueryRow(ctx, `select merchant_id, category_id from transactions where id = $1`, id).Scan(&mID, &cID); err != nil {
			t.Fatalf("scan tx %d: %v", i, err)
		}
		if mID.Valid {
			u := uuid.UUID(mID.Bytes)
			got[i].merchantID = &u
		}
		if cID.Valid {
			u := uuid.UUID(cID.Bytes)
			got[i].categoryID = &u
		}
	}

	// 1) The two coop rows share a merchant_id.
	if got[0].merchantID == nil || got[1].merchantID == nil {
		t.Fatalf("coop rows missing merchant_id: %+v %+v", got[0], got[1])
	}
	if *got[0].merchantID != *got[1].merchantID {
		t.Errorf("identical raw should share merchant_id: %v vs %v", got[0].merchantID, got[1].merchantID)
	}

	// 2) Migros row has a distinct merchant_id.
	if got[2].merchantID == nil {
		t.Fatalf("migros row missing merchant_id")
	}
	if *got[2].merchantID == *got[0].merchantID {
		t.Errorf("different raws should map to different merchants: coop=%v migros=%v", got[0].merchantID, got[2].merchantID)
	}

	// 3) nil + empty raw → merchant_id NULL, category_id NULL.
	if got[3].merchantID != nil || got[3].categoryID != nil {
		t.Errorf("nil raw row should have null merchant/category: %+v", got[3])
	}
	if got[4].merchantID != nil || got[4].categoryID != nil {
		t.Errorf("empty raw row should have null merchant/category: %+v", got[4])
	}

	// 4) Pre-existing merchant with default category → row inherits category_id.
	if got[5].merchantID == nil || *got[5].merchantID != preM.ID {
		t.Fatalf("pre-merchant row should resolve to existing merchant id %v, got %v", preM.ID, got[5].merchantID)
	}
	if got[5].categoryID == nil || *got[5].categoryID != cat.ID {
		t.Errorf("pre-merchant row should inherit default_category_id %v, got %v", cat.ID, got[5].categoryID)
	}

	// 5) Coop and migros rows should NOT have a category (no default set).
	if got[0].categoryID != nil {
		t.Errorf("coop row should have null category_id, got %v", got[0].categoryID)
	}
	if got[2].categoryID != nil {
		t.Errorf("migros row should have null category_id, got %v", got[2].categoryID)
	}
}
