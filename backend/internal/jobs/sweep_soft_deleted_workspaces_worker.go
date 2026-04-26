package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/jobs/cleanup"
)

type SweepSoftDeletedWorkspacesWorker struct {
	river.WorkerDefaults[SweepSoftDeletedWorkspacesArgs]
	pool        *pgxpool.Pool
	gracePeriod time.Duration
}

func NewSweepSoftDeletedWorkspacesWorker(pool *pgxpool.Pool, gracePeriod time.Duration) *SweepSoftDeletedWorkspacesWorker {
	return &SweepSoftDeletedWorkspacesWorker{pool: pool, gracePeriod: gracePeriod}
}

func (w *SweepSoftDeletedWorkspacesWorker) Work(ctx context.Context, _ *river.Job[SweepSoftDeletedWorkspacesArgs]) error {
	_, err := cleanup.Run(ctx, w.pool, w.gracePeriod)
	return err
}
