package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestWithTx_rollsBack(t *testing.T) {
	WithTx(t, func(ctx context.Context, tx pgx.Tx) {
		var one int
		if err := tx.QueryRow(ctx, "select 1").Scan(&one); err != nil {
			t.Fatalf("select 1: %v", err)
		}
		if one != 1 {
			t.Fatalf("expected 1, got %d", one)
		}
	})
}
