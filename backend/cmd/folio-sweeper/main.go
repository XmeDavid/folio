// Command folio-sweeper is a one-shot cron-invokable entry point that
// hard-deletes workspaces past their 30-day soft-delete grace period.
//
// Plan 2 ships this as a standalone binary so the sweeper works without
// River. Plan 3 wires River to call cleanup.Run directly inside the
// server process on a daily schedule; this binary stays available for
// cron-based self-hosted deployments.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/xmedavid/folio/backend/internal/jobs/cleanup"
)

func main() {
	grace := flag.Duration("grace", 30*24*time.Hour, "grace period before hard-deleting soft-deleted workspaces")
	flag.Parse()

	// Load .env if we're running from a checkout; no-op if not present.
	_ = godotenv.Load(".env", "../.env", "../../.env")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		logger.Error("pool open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	report, err := cleanup.Run(ctx, pool, *grace)
	if err != nil {
		logger.Error("sweeper run failed", "err", err)
		os.Exit(1)
	}
	logger.Info("sweeper done",
		"deleted_count", report.DeletedCount,
		"deleted_ids", report.DeletedIDs,
		"elapsed", report.FinishedAt.Sub(report.StartedAt),
	)
}
