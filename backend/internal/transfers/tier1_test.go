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

func strPtr(s string) *string { return &s }

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
	require.True(t, fxRate.Sub(expected).Abs().LessThan(decimal.RequireFromString("0.0001")),
		"got fx_rate=%s, expected ≈ %s", fxRate, expected)
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
	_ = seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"100.00", "CHF", nil, nil)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src}})
	require.NoError(t, err)
	require.Equal(t, 1, res.Tier1Paired)
}

func TestTier1_AmbiguousMultipleCandidatesSkips(t *testing.T) {
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

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_matches WHERE workspace_id = $1`, wsID,
	).Scan(&count))
	require.Equal(t, 0, count)
}

func TestTier1_AlreadyPairedSkips(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier1-already")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"-100.00", "CHF", strPtr("100.00"), strPtr("CHF"))
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"100.00", "CHF", nil, nil)

	// Pre-pair via raw SQL.
	_, err := pool.Exec(ctx, `
		INSERT INTO transfer_matches (id, workspace_id, source_transaction_id, destination_transaction_id, provenance)
		VALUES ($1, $2, $3, $4, 'manual')
	`, uuidx.New(), wsID, src, dst)
	require.NoError(t, err)

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src}})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier1Paired)
}
