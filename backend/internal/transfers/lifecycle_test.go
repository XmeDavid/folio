package transfers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transfers"
	"github.com/xmedavid/folio/backend/internal/uuidx"
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

func TestManualPair_AlreadyPairedDestination(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-manualpair-destpair")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	c := seedAccount(t, ctx, pool, wsID, "C", "CHF")
	src1 := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	src2 := seedTx(t, ctx, pool, wsID, c, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)

	_, err := svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src1, DestinationID: &dst})
	require.NoError(t, err)
	_, err = svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src2, DestinationID: &dst})
	var cerr *httpx.ConflictError
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, "transfer_destination_already_paired", cerr.Code)
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

func TestManualPair_ClosesPendingCandidate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-manualpair-candidate")

	a := seedAccount(t, ctx, pool, wsID, "Revolut", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "Bank", "CHF")
	src := seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "500.00", "CHF", "Transfer from Bank")
	dst := seedTxWithRaw(t, ctx, pool, wsID, b, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC), "-500.00", "CHF", "Outgoing")

	// Tier 3 surfaces a pending candidate.
	_, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)

	cands, _ := svc.ListPendingCandidates(ctx, wsID)
	require.GreaterOrEqual(t, len(cands), 1)

	_, err = svc.ManualPair(ctx, wsID, transfers.ManualPairInput{SourceID: src, DestinationID: &dst})
	require.NoError(t, err)

	// Candidate row should be marked 'paired'.
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM transfer_match_candidates WHERE workspace_id = $1 AND source_transaction_id = $2`,
		wsID, src,
	).Scan(&status))
	require.Equal(t, "paired", status)
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
	require.NoError(t, svc.Unpair(ctx, wsID, tm.ID))

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_matches WHERE id = $1`, tm.ID,
	).Scan(&count))
	require.Equal(t, 0, count)
}

func TestUnpair_NotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-unpair-404")

	err := svc.Unpair(ctx, wsID, uuid.New())
	var nferr *httpx.NotFoundError
	require.True(t, errors.As(err, &nferr))
}

func TestDeclineCandidate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-decline")

	a := seedAccount(t, ctx, pool, wsID, "Revolut", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "Bank", "CHF")
	_ = seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "500.00", "CHF", "Transfer from Bank")
	_ = seedTxWithRaw(t, ctx, pool, wsID, b, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC), "-500.00", "CHF", "Outgoing")

	_, err := svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)

	cands, err := svc.ListPendingCandidates(ctx, wsID)
	require.NoError(t, err)
	require.Len(t, cands, 1)

	require.NoError(t, svc.DeclineCandidate(ctx, wsID, cands[0].ID, nil))

	cands2, _ := svc.ListPendingCandidates(ctx, wsID)
	require.Len(t, cands2, 0)
}

func TestCountPendingCandidates(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := transfers.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-count")

	a := seedAccount(t, ctx, pool, wsID, "Revolut", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "Bank", "CHF")
	_ = seedTxWithRaw(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "500.00", "CHF", "Transfer from Bank")
	_ = seedTxWithRaw(t, ctx, pool, wsID, b, time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC), "-500.00", "CHF", "Outgoing")

	n, err := svc.CountPendingCandidates(ctx, wsID)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	_, err = svc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)

	n, err = svc.CountPendingCandidates(ctx, wsID)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestTransferMatchesParticipantGuardRejectsCrossRoleDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-participant-guard")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")
	c := seedAccount(t, ctx, pool, wsID, "C", "CHF")
	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)
	other := seedTx(t, ctx, pool, wsID, c, time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC), "100.00", "CHF", nil, nil)

	_, err := pool.Exec(ctx, `
		INSERT INTO transfer_matches (id, workspace_id, source_transaction_id, destination_transaction_id, provenance)
		VALUES ($1, $2, $3, $4, 'manual')
	`, uuidx.New(), wsID, src, dst)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO transfer_matches (id, workspace_id, source_transaction_id, destination_transaction_id, provenance)
		VALUES ($1, $2, $3, $4, 'manual')
	`, uuidx.New(), wsID, dst, other)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23505", pgErr.Code)
	require.Equal(t, "transfer_matches_participant_uq", pgErr.ConstraintName)
}
