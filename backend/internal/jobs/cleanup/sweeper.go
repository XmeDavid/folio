// Package cleanup owns periodic maintenance jobs. Plan 2 ships Run as a
// one-shot function called by backend/cmd/folio-sweeper. Plan 3 wraps
// Run in a River PeriodicJob scheduled daily.
package cleanup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
)

// Report summarises a sweeper pass for logging / test assertions.
type Report struct {
	DeletedCount int64
	DeletedIDs   []string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// Run hard-deletes every workspace whose deleted_at is older than gracePeriod.
// Returns a Report listing the removed workspaces. Cascades propagate via the
// existing workspace_id FKs (accounts, transactions, memberships, …); this
// function is the only place in the codebase that deletes a workspace row
// permanently.
func Run(ctx context.Context, pool *pgxpool.Pool, gracePeriod time.Duration) (*Report, error) {
	r := &Report{StartedAt: time.Now()}
	ids, err := dbq.New(pool).SweepDeletedWorkspaces(ctx, gracePeriod.Seconds())
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	r.DeletedIDs = ids
	r.DeletedCount = int64(len(ids))
	r.FinishedAt = time.Now()
	slog.Default().Info("cleanup.sweeper.done",
		"deleted", r.DeletedCount, "elapsed", r.FinishedAt.Sub(r.StartedAt))
	return r, nil
}
