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

// seedTxWithRaw inserts a transaction with a counterparty_raw value.
func seedTxWithRaw(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	workspaceID, accountID uuid.UUID,
	bookedAt time.Time, amount, currency, counterpartyRaw string,
) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, workspace_id, account_id, status, booked_at, amount, currency, counterparty_raw)
		VALUES ($1, $2, $3, 'posted', $4, $5::numeric, $6, $7)
	`, id, workspaceID, accountID, bookedAt, amount, currency, counterpartyRaw)
	require.NoError(t, err)
	return id
}

func TestTier3_SuggestsForAccountNameMatch(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier3-acctmatch")

	revolut := seedAccount(t, ctx, pool, wsID, "Revolut Main", "CHF")
	bank := seedAccount(t, ctx, pool, wsID, "Bank Checking", "CHF")

	// Credit in revolut whose raw mentions "Bank Checking" → Tier 3 hits.
	src := seedTxWithRaw(t, ctx, pool, wsID, revolut, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"500.00", "CHF", "Transfer from Bank Checking")
	candidate := seedTxWithRaw(t, ctx, pool, wsID, bank, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC),
		"-500.00", "CHF", "Outgoing")

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{TransactionIDs: []uuid.UUID{src, candidate}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.Tier3Suggested, 1)

	var dstIDs []uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT candidate_destination_ids FROM transfer_match_candidates
		 WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&dstIDs))
	require.Contains(t, dstIDs, candidate)
}

func TestTier3_SuggestsForKeywordMatch(t *testing.T) {
	// "Überweisung" → German keyword.
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier3-keyword")

	a := seedAccount(t, ctx, pool, wsID, "UBS", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "Raiffeisen", "CHF")

	src := seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"200.00", "CHF", "Überweisung von Konto")
	candidate := seedTxWithRaw(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"-200.00", "CHF", "Outgoing")

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.Tier3Suggested, 1)

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
		"-500.00", "CHF", "Outgoing")

	// First detector run → 1 pending candidate row.
	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.Tier3Suggested, 1)

	// Decline it via raw SQL.
	_, err = pool.Exec(ctx, `
		UPDATE transfer_match_candidates SET status = 'declined', resolved_at = now()
		WHERE workspace_id = $1 AND source_transaction_id = $2`, wsID, src)
	require.NoError(t, err)

	// Second detector run → ON CONFLICT DO NOTHING; no new pending row.
	res2, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.Equal(t, 0, res2.Tier3Suggested)

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM transfer_match_candidates
		 WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&status))
	require.Equal(t, "declined", status)
}

func TestTier3_NoKeywordNoSuggest(t *testing.T) {
	// Counterparty "ACME Coffee" doesn't match any account name or keyword → no suggestion.
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-tier3-nohit")

	a := seedAccount(t, ctx, pool, wsID, "UBS", "CHF")

	_ = seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"42.00", "CHF", "ACME Coffee")

	res, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier3Suggested)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_match_candidates WHERE workspace_id = $1`, wsID,
	).Scan(&count))
	require.Equal(t, 0, count)
}
