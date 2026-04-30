package transactions_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transactions"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// seedAccountForTransferFilter inserts a minimal account row for use by the
// transfer-filter tests. Mirrors the helpers in the transfers package.
func seedAccountForTransferFilter(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, name, currency string) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, workspace_id, name, kind, currency, open_date, opening_balance, opening_balance_date, include_in_networth, include_in_savings_rate)
		VALUES ($1, $2, $3, 'checking', $4, $5, 0, $5, true, true)
	`, id, workspaceID, name, currency, time.Now().UTC())
	require.NoError(t, err)
	return id
}

func seedTxRaw(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, accountID uuid.UUID, amount string) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, workspace_id, account_id, status, booked_at, amount, currency)
		VALUES ($1, $2, $3, 'posted', $4, $5::numeric, 'CHF')
	`, id, workspaceID, accountID, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), amount)
	require.NoError(t, err)
	return id
}

func seedManualMatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, src, dst uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO transfer_matches (id, workspace_id, source_transaction_id, destination_transaction_id, provenance)
		VALUES ($1, $2, $3, $4, 'manual')
	`, id, workspaceID, src, dst)
	require.NoError(t, err)
	return id
}

func TestList_HideInternalMovesDefault(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-list-hide")

	a := seedAccountForTransferFilter(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccountForTransferFilter(t, ctx, pool, wsID, "B", "CHF")
	c := seedAccountForTransferFilter(t, ctx, pool, wsID, "C", "CHF")
	src := seedTxRaw(t, ctx, pool, wsID, a, "-100.00")
	dst := seedTxRaw(t, ctx, pool, wsID, b, "100.00")
	other := seedTxRaw(t, ctx, pool, wsID, c, "-25.00")
	seedManualMatch(t, ctx, pool, wsID, src, dst)

	hide := transactions.ListFilter{HideInternalMoves: true, Limit: 100}
	list, err := svc.List(ctx, wsID, hide)
	require.NoError(t, err)
	ids := txIDs(list)
	require.Contains(t, ids, other)
	require.NotContains(t, ids, src)
	require.NotContains(t, ids, dst)

	show := transactions.ListFilter{HideInternalMoves: false, Limit: 100}
	list2, err := svc.List(ctx, wsID, show)
	require.NoError(t, err)
	ids2 := txIDs(list2)
	require.Contains(t, ids2, src)
	require.Contains(t, ids2, dst)
	for _, t1 := range list2 {
		if t1.ID == src {
			require.NotNil(t, t1.TransferMatchID)
			require.NotNil(t, t1.TransferCounterpartID)
			require.Equal(t, dst, *t1.TransferCounterpartID)
		}
		if t1.ID == dst {
			require.NotNil(t, t1.TransferMatchID)
			require.Equal(t, src, *t1.TransferCounterpartID)
		}
	}
}

func TestGet_PairedTransactionReturnsTransferFields(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	classSvc := classification.NewService(pool)
	svc := transactions.NewService(pool, classSvc)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-get-paired")

	a := seedAccountForTransferFilter(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccountForTransferFilter(t, ctx, pool, wsID, "B", "CHF")
	src := seedTxRaw(t, ctx, pool, wsID, a, "-100.00")
	dst := seedTxRaw(t, ctx, pool, wsID, b, "100.00")
	matchID := seedManualMatch(t, ctx, pool, wsID, src, dst)

	got, err := svc.Get(ctx, wsID, src)
	require.NoError(t, err)
	require.NotNil(t, got.TransferMatchID)
	require.Equal(t, matchID, *got.TransferMatchID)
	require.NotNil(t, got.TransferCounterpartID)
	require.Equal(t, dst, *got.TransferCounterpartID)
}

func txIDs(list []transactions.Transaction) []uuid.UUID {
	out := make([]uuid.UUID, len(list))
	for i, t := range list {
		out[i] = t.ID
	}
	return out
}
