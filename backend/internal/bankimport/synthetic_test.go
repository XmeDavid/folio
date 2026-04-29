package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestResidualExplainedByExisting(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	syntheticDate := day("2025-12-16")
	residual := decimal.RequireFromString("-105.77")

	cases := []struct {
		name     string
		existing []existingTx
		want     bool
	}{
		{
			name: "two real rows summing to residual within window",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-93.79"), Currency: "CHF", Description: "CHF → Revolut X"},
				{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-11.98"), Currency: "CHF", Description: "CHF → Revolut X"},
			},
			want: true,
		},
		{
			name: "single row matching residual",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-14"), Amount: decimal.RequireFromString("-105.77"), Currency: "CHF"},
			},
			want: true,
		},
		{
			name: "rows outside ±7d window — no match",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-01"), Amount: decimal.RequireFromString("-105.77"), Currency: "CHF"},
			},
			want: false,
		},
		{
			name: "rows in different currency",
			existing: []existingTx{
				{ID: uuid.New(), BookedAt: day("2025-12-15"), Amount: decimal.RequireFromString("-105.77"), Currency: "EUR"},
			},
			want: false,
		},
		{
			name:     "no rows at all",
			existing: nil,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := residualExplainedByExisting(syntheticDate, time.Time{}, "CHF", residual, tc.existing); got != tc.want {
				t.Fatalf("residualExplainedByExisting = %v, want %v", got, tc.want)
			}
		})
	}
}
