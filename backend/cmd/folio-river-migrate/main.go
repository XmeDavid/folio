package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func main() {
	direction := flag.String("direction", "up", "up|down")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrator:", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var dir rivermigrate.Direction
	switch *direction {
	case "up":
		dir = rivermigrate.DirectionUp
	case "down":
		dir = rivermigrate.DirectionDown
	default:
		fmt.Fprintln(os.Stderr, "unknown direction:", *direction)
		os.Exit(2)
	}
	res, err := migrator.Migrate(ctx, dir, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	for _, v := range res.Versions {
		logger.Info("river migrate", "version", v.Version, "name", v.Name, "direction", *direction)
	}
}
