package bankimport

import (
	"time"

	"github.com/shopspring/decimal"
)

// residualExplainedByExisting returns true when a synthetic balance-reconcile
// row's residual is already covered by real (non-synthetic) transactions in
// the destination account within ±7 days of the synthetic's date. Used in
// two places:
//
//   - At classify time on banking-first → consolidated-second imports, to
//     skip inserting a synthetic whose residual is already present.
//   - At post-apply time on consolidated-first → banking-second imports, to
//     void synthetics now redundant after the banking rows arrive.
//
// The matcher accepts two shapes: a single existing row whose amount equals
// the residual, or a subset whose sum equals it. We keep this conservative —
// only consider rows in the same currency, same sign, within window — to
// avoid voiding a real synthetic that just happens to coincide with normal
// transaction noise.
func residualExplainedByExisting(syntheticDate time.Time, currency string, residual decimal.Decimal, existing []existingTx) bool {
	candidates := make([]decimal.Decimal, 0, len(existing))
	for _, e := range existing {
		if e.Currency != currency {
			continue
		}
		if !datesWithin(e.BookedAt, syntheticDate, reviewDedupDays) {
			continue
		}
		if residual.Sign() != 0 && e.Amount.Sign() != 0 && residual.Sign() != e.Amount.Sign() {
			continue
		}
		candidates = append(candidates, e.Amount)
	}
	if len(candidates) == 0 {
		return false
	}
	for _, c := range candidates {
		if c.Equal(residual) {
			return true
		}
	}
	if len(candidates) > 16 {
		// Hard cap to avoid pathological 2^N blow-up. If a section has >16
		// same-sign-same-currency rows in a 14-day window, the heuristic
		// becomes unreliable anyway.
		return false
	}
	tolerance := decimal.RequireFromString("0.02")
	for mask := 1; mask < 1<<len(candidates); mask++ {
		sum := decimal.Zero
		for i, c := range candidates {
			if mask&(1<<i) != 0 {
				sum = sum.Add(c)
			}
		}
		if sum.Sub(residual).Abs().LessThanOrEqual(tolerance) {
			return true
		}
	}
	return false
}

const syntheticBalanceReconcile = "balance_reconcile"

// buildReconcileTx wraps a residual delta (= balance_delta − stated_amount)
// in a ParsedTransaction tagged so the importer / UI can identify it as a
// synthetic balance-adjustment paired with the preceding row. We carry the
// triggering row's date and account hint so the reconcile lands inside
// the same logical day as the cause. Raw["synthetic_residual"] echoes the
// amount as a decimal string so the apply path can match without reparsing.
func buildReconcileTx(trigger ParsedTransaction, residual decimal.Decimal, balanceRaw string) ParsedTransaction {
	cause := ""
	if trigger.Description != nil {
		cause = *trigger.Description
	}
	desc := "Revolut balance adjustment"
	if cause != "" {
		desc = "Revolut balance adjustment (" + cause + ")"
	}
	descPtr := desc
	raw := map[string]string{
		"section":            trigger.AccountHint,
		"currency":           trigger.Currency,
		"synthetic":          syntheticBalanceReconcile,
		"synthetic_residual": residual.String(),
		"trigger_amount":     trigger.Amount.String(),
		"trigger_balance":    balanceRaw,
	}
	if cause != "" {
		raw["trigger_description"] = cause
	}
	return ParsedTransaction{
		BookedAt:    trigger.BookedAt,
		Amount:      residual,
		Currency:    trigger.Currency,
		Description: &descPtr,
		AccountHint: trigger.AccountHint,
		KindHint:    trigger.KindHint,
		Raw:         raw,
	}
}
