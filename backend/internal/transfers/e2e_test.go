package transfers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/transactions"
	"github.com/xmedavid/folio/backend/internal/transfers"
)

// TestE2E_DetectListUnpair walks the full happy path:
//
//	import → tier 2 pair → list-hides → toggle-shows → unpair → list-shows-again.
//
// We use raw SQL helpers from tier1_test.go / tier2_test.go to seed data
// without driving the full bankimport pipeline (which the Phase 2.1 wiring
// already covered in its own tests).
func TestE2E_DetectListUnpair(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	transfersSvc := transfers.NewService(pool)
	classSvc := classification.NewService(pool)
	txSvc := transactions.NewService(pool, classSvc)

	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-e2e-detect")

	a := seedAccount(t, ctx, pool, wsID, "A", "CHF")
	b := seedAccount(t, ctx, pool, wsID, "B", "CHF")

	src := seedTx(t, ctx, pool, wsID, a, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"-100.00", "CHF", nil, nil)
	dst := seedTx(t, ctx, pool, wsID, b, time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
		"100.00", "CHF", nil, nil)

	// Step 3: nothing pairs because no original_amount, no shared batch, no keyword raw.
	res, err := transfersSvc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.Equal(t, 0, res.Tier1Paired)
	require.Equal(t, 0, res.Tier2Paired)
	require.Equal(t, 0, res.Tier3Suggested)

	// Step 4: link both rows to the same import batch via source_refs.
	batch := seedImportBatch(t, ctx, pool, wsID)
	seedSourceRefForBatch(t, ctx, pool, wsID, src, batch)
	seedSourceRefForBatch(t, ctx, pool, wsID, dst, batch)

	// Re-run detector → Tier 2 pairs.
	res2, err := transfersSvc.DetectAndPair(ctx, wsID, transfers.DetectScope{All: true})
	require.NoError(t, err)
	require.Equal(t, 1, res2.Tier2Paired)

	// Step 5: assert transfer_matches row exists.
	var matchID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM transfer_matches WHERE workspace_id = $1 AND source_transaction_id = $2 AND destination_transaction_id = $3`,
		wsID, src, dst,
	).Scan(&matchID))

	// Step 6a: list with hideInternalMoves=true excludes both legs.
	hide := transactions.ListFilter{HideInternalMoves: true, Limit: 100}
	hidden, err := txSvc.List(ctx, wsID, hide)
	require.NoError(t, err)
	for _, t1 := range hidden {
		require.NotEqual(t, src, t1.ID, "src should be hidden")
		require.NotEqual(t, dst, t1.ID, "dst should be hidden")
	}

	// Step 6b: list with hideInternalMoves=false shows both with TransferMatchID set.
	show := transactions.ListFilter{HideInternalMoves: false, Limit: 100}
	shown, err := txSvc.List(ctx, wsID, show)
	require.NoError(t, err)

	var foundSrc, foundDst bool
	for _, t1 := range shown {
		if t1.ID == src {
			foundSrc = true
			require.NotNil(t, t1.TransferMatchID)
			require.Equal(t, matchID, *t1.TransferMatchID)
			require.NotNil(t, t1.TransferCounterpartID)
			require.Equal(t, dst, *t1.TransferCounterpartID)
		}
		if t1.ID == dst {
			foundDst = true
			require.NotNil(t, t1.TransferMatchID)
			require.Equal(t, src, *t1.TransferCounterpartID)
		}
	}
	require.True(t, foundSrc, "src should appear with toggle on")
	require.True(t, foundDst, "dst should appear with toggle on")

	// Step 7: unpair → both legs reappear in the default list.
	require.NoError(t, transfersSvc.Unpair(ctx, wsID, matchID))

	postUnpair, err := txSvc.List(ctx, wsID, hide)
	require.NoError(t, err)
	var seenSrc, seenDst bool
	for _, t1 := range postUnpair {
		if t1.ID == src {
			seenSrc = true
			require.Nil(t, t1.TransferMatchID, "post-unpair, no transfer_match")
		}
		if t1.ID == dst {
			seenDst = true
			require.Nil(t, t1.TransferMatchID)
		}
	}
	require.True(t, seenSrc, "src should reappear after unpair")
	require.True(t, seenDst, "dst should reappear after unpair")
}
