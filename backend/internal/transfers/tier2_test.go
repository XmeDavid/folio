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
	fileHash := "deadbeef-" + uuid.NewString()
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
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier2-fee")

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

	var feeAmount, feeCurrency *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fee_amount::text, fee_currency::text FROM transfer_matches
		 WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&feeAmount, &feeCurrency))
	require.NotNil(t, feeAmount)
	require.Contains(t, *feeAmount, "0.5")
	require.NotNil(t, feeCurrency)
	require.Equal(t, "CHF", *feeCurrency)
}

func TestTier2_SameBatchPair_ExactNoFee(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier2-exact")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	batch := seedImportBatch(t, ctx, pool, wsID)

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)
	seedSourceRefForBatch(t, ctx, pool, wsID, src, batch)
	seedSourceRefForBatch(t, ctx, pool, wsID, dst, batch)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, dst}})
	require.NoError(t, err)
	require.Equal(t, 1, res.Tier2Paired)

	// fee_amount should be NULL when amounts cancel exactly.
	var feeAmount *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fee_amount::text FROM transfer_matches
		 WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&feeAmount))
	require.Nil(t, feeAmount, "expected NULL fee_amount when amounts cancel exactly")
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

func TestTier2_FeeTooLargeSkips(t *testing.T) {
	// 100 CHF source, 95 CHF destination → 5.00 fee, exceeds GREATEST(2.00, 0.5%) = 2.00.
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier2-feetoobig")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	batch := seedImportBatch(t, ctx, pool, wsID)

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "95.00", "CHF", nil, nil)
	seedSourceRefForBatch(t, ctx, pool, wsID, src, batch)
	seedSourceRefForBatch(t, ctx, pool, wsID, dst, batch)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, dst}})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier2Paired)
}
