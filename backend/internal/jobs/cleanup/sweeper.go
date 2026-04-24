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
)

// Report summarises a sweeper pass for logging / test assertions.
type Report struct {
	DeletedCount int64
	DeletedIDs   []string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// Run hard-deletes every tenant whose deleted_at is older than gracePeriod.
// Returns a Report listing the removed tenants. Cascades propagate via the
// existing tenant_id FKs (accounts, transactions, memberships, …); this
// function is the only place in the codebase that deletes a tenant row
// permanently.
func Run(ctx context.Context, pool *pgxpool.Pool, gracePeriod time.Duration) (*Report, error) {
	r := &Report{StartedAt: time.Now()}
	// pgx can bind a time.Duration to an interval via the string form.
	rows, err := pool.Query(ctx, `
		delete from tenants
		where deleted_at is not null
		  and deleted_at < now() - make_interval(secs => $1)
		returning id::text
	`, gracePeriod.Seconds())
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		r.DeletedIDs = append(r.DeletedIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.DeletedCount = int64(len(r.DeletedIDs))
	r.FinishedAt = time.Now()
	slog.Default().Info("cleanup.sweeper.done",
		"deleted", r.DeletedCount, "elapsed", r.FinishedAt.Sub(r.StartedAt))
	return r, nil
}
