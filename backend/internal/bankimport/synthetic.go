package bankimport

import (
	"time"

	"github.com/shopspring/decimal"
)

// residualExplainedByExisting returns true when a synthetic balance-reconcile
// row's residual is already covered by real (non-synthetic) transactions in
// the destination account inside the synthetic's gap interval (extended by
// reviewDedupDays on either side). Used in two places:
//
//   - At classify time on banking-first → consolidated-second imports, to
//     skip inserting a synthetic whose residual is already present.
//   - At post-apply time on consolidated-first → banking-second imports, to
//     void synthetics now redundant after the banking rows arrive.
//
// gapStart is the booked date of the consolidated row preceding the gap. A
// zero gapStart degrades to "syntheticDate alone", preserving the original
// ±7d behaviour for callers that don't have gap context. The matcher accepts
// two shapes: a single existing row whose amount equals the residual, or a
// subset whose sum equals it. We keep this conservative — same currency,
// same sign, inside the gap window — to avoid voiding a real synthetic that
// just happens to coincide with normal transaction noise.
func residualExplainedByExisting(syntheticDate time.Time, gapStart time.Time, currency string, residual decimal.Decimal, existing []existingTx) bool {
	if gapStart.IsZero() || gapStart.After(syntheticDate) {
		gapStart = syntheticDate
	}
	from := gapStart.AddDate(0, 0, -reviewDedupDays)
	to := syntheticDate.AddDate(0, 0, reviewDedupDays)
	candidates := make([]decimal.Decimal, 0, len(existing))
	for _, e := range existing {
		if e.Currency != currency {
			continue
		}
		if e.BookedAt.Before(from) || e.BookedAt.After(to) {
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
//
// gapStart is the booked date of the preceding consolidated row that
// established prevBal — i.e. the lower bound of the interval where the
// missing real transaction must have happened. It's recorded so the retire
// step can search the entire gap span (gap_start..trigger) for explaining
// rows, not just ±7d around the trigger date. Without this, missing rows
// dated more than 7d before the trigger never void the synthetic, and
// re-importing the matching banking export double-counts.
func buildReconcileTx(trigger ParsedTransaction, residual decimal.Decimal, balanceRaw string, gapStart time.Time) ParsedTransaction {
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
		"gap_start_date":     gapStart.Format(dateOnly),
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
