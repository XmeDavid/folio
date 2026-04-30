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
)

// TestE2E_ImportMergeReimport exercises the full merchants + default-categorisation
// pipeline end to end:
//
//  1. Bank-import attaches merchants by counterparty_raw — two rows sharing a raw
//     resolve to the same merchant, a third row with a distinct raw gets its own.
//  2. Merging the smaller merchant into the other moves all its transactions and
//     captures the source canonical_name as an alias of the target.
//  3. A subsequent import of the now-aliased raw resolves to the surviving
//     (target) merchant via the alias path — proof the round-trip interlocks.
func TestE2E_ImportMergeReimport(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	bsvc := bankimport.NewService(pool, classSvc, transfers.NewService(pool))

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-e2e-merchants")
	accountID := seedAccount(t, ctx, pool, wsID, "CHF")

	q := dbq.New(pool)
	batchID := seedImportBatch(t, ctx, q, wsID)

	// ---- Step 1: import three rows with two distinct raws. ----
	migrosExp := "MIGROSEXP-7711"
	migrosBern := "MIGROS BERN"
	rows1 := []bankimport.ParsedTransaction{
		{
			BookedAt:        e2eDate(2026, 4, 1),
			Amount:          decimal.RequireFromString("-9.20"),
			Currency:        "CHF",
			CounterpartyRaw: &migrosExp,
			ExternalID:      "ext-1",
			Raw:             map[string]string{},
		},
		{
			BookedAt:        e2eDate(2026, 4, 2),
			Amount:          decimal.RequireFromString("-12.40"),
			Currency:        "CHF",
			CounterpartyRaw: &migrosExp,
			ExternalID:      "ext-2",
			Raw:             map[string]string{},
		},
		{
			BookedAt:        e2eDate(2026, 4, 3),
			Amount:          decimal.RequireFromString("-87.10"),
			Currency:        "CHF",
			CounterpartyRaw: &migrosBern,
			ExternalID:      "ext-3",
			Raw:             map[string]string{},
		},
	}

	inserted1, err := bsvc.InsertImportableTxForTest(ctx, q, wsID, accountID, batchID, "synthetic", rows1)
	if err != nil {
		t.Fatalf("import #1: %v", err)
	}
	if len(inserted1) != 3 {
		t.Fatalf("inserted1 len = %d, want 3", len(inserted1))
	}

	merchants1 := readMerchantIDs(t, ctx, pool, inserted1)
	if merchants1[0] == nil || merchants1[1] == nil || merchants1[2] == nil {
		t.Fatalf("expected non-nil merchant_id on every imported row, got %v", merchants1)
	}
	if *merchants1[0] != *merchants1[1] {
		t.Errorf("rows 0 and 1 (same raw) should share merchant: %v vs %v", merchants1[0], merchants1[1])
	}
	if *merchants1[0] == *merchants1[2] {
		t.Errorf("rows 0 and 2 (different raw) should have distinct merchants")
	}

	sourceMerchantID := *merchants1[0]   // "MIGROSEXP-7711"
	targetMerchantID := *merchants1[2]   // "MIGROS BERN"

	// ---- Step 2: merge source -> target with applyTargetDefault=false. ----
	res, err := classSvc.MergeMerchants(ctx, wsID, sourceMerchantID, classification.MergeMerchantsInput{
		TargetID:           targetMerchantID,
		ApplyTargetDefault: false,
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.MovedCount != 2 {
		t.Errorf("MovedCount = %d, want 2", res.MovedCount)
	}
	if res.CapturedAliasCount < 1 {
		t.Errorf("CapturedAliasCount = %d, want >= 1 (source canonical name)", res.CapturedAliasCount)
	}

	// All 3 transactions should now point at the target.
	merchants2 := readMerchantIDs(t, ctx, pool, inserted1)
	for i, m := range merchants2 {
		if m == nil || *m != targetMerchantID {
			t.Errorf("transaction[%d] merchant_id = %v after merge, want %v", i, m, targetMerchantID)
		}
	}

	// Source merchant row should be gone.
	var srcCount int
	if err := pool.QueryRow(ctx,
		"select count(*) from merchants where workspace_id = $1 and id = $2",
		wsID, sourceMerchantID).Scan(&srcCount); err != nil {
		t.Fatalf("count source: %v", err)
	}
	if srcCount != 0 {
		t.Errorf("source merchant still exists after merge")
	}

	// ---- Step 3: re-import a 4th row with the OLD raw — should attach to
	// target via the captured alias. ----
	rows2 := []bankimport.ParsedTransaction{
		{
			BookedAt:        e2eDate(2026, 4, 4),
			Amount:          decimal.RequireFromString("-5.50"),
			Currency:        "CHF",
			CounterpartyRaw: &migrosExp,
			ExternalID:      "ext-4",
			Raw:             map[string]string{},
		},
	}
	inserted2, err := bsvc.InsertImportableTxForTest(ctx, q, wsID, accountID, batchID, "synthetic", rows2)
	if err != nil {
		t.Fatalf("import #2: %v", err)
	}
	if len(inserted2) != 1 {
		t.Fatalf("inserted2 len = %d, want 1", len(inserted2))
	}

	fourth := readMerchantIDs(t, ctx, pool, inserted2)
	if fourth[0] == nil || *fourth[0] != targetMerchantID {
		t.Errorf("post-merge re-import: merchant_id = %v, want %v (resolved via alias)", fourth[0], targetMerchantID)
	}
}

// readMerchantIDs returns the merchant_id column for each transaction id, in
// the same order. nil entries indicate NULL merchant_id. Uses pgtype.UUID to
// scan nullable values cleanly.
func readMerchantIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids []uuid.UUID) []*uuid.UUID {
	t.Helper()
	out := make([]*uuid.UUID, len(ids))
	for i, id := range ids {
		var m pgtype.UUID
		if err := pool.QueryRow(ctx, `select merchant_id from transactions where id = $1`, id).Scan(&m); err != nil {
			t.Fatalf("read merchant_id for %v: %v", id, err)
		}
		if m.Valid {
			u := uuid.UUID(m.Bytes)
			out[i] = &u
		}
	}
	return out
}

func e2eDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
