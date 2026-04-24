// Package testdb provides a shared test helper that opens a transaction
// against DATABASE_URL and rolls it back at the end of the test. Callers
// get a pgx.Tx they can use as a query surface; nothing persists between
// tests. Tests SKIP when DATABASE_URL is unset.
package testdb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open returns a *pgxpool.Pool against DATABASE_URL or skips the test if unset.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// WithTx runs fn inside a transaction that is rolled back when the test
// finishes. The tx is safe to share across helpers that take a query surface.
func WithTx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx)) {
	t.Helper()
	pool := Open(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	fn(ctx, tx)
}
