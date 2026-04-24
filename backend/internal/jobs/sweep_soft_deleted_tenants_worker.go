package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/jobs/cleanup"
)

type SweepSoftDeletedTenantsWorker struct {
	river.WorkerDefaults[SweepSoftDeletedTenantsArgs]
	pool        *pgxpool.Pool
	gracePeriod time.Duration
}

func NewSweepSoftDeletedTenantsWorker(pool *pgxpool.Pool, gracePeriod time.Duration) *SweepSoftDeletedTenantsWorker {
	return &SweepSoftDeletedTenantsWorker{pool: pool, gracePeriod: gracePeriod}
}

func (w *SweepSoftDeletedTenantsWorker) Work(ctx context.Context, _ *river.Job[SweepSoftDeletedTenantsArgs]) error {
	_, err := cleanup.Run(ctx, w.pool, w.gracePeriod)
	return err
}
