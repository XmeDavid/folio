package bankimport

import (
	"time"

	"github.com/shopspring/decimal"
)

// residualExplainedByExisting returns true when a synthetic balance-reconcile
// row's residual is already covered by real (non-synthetic) transactions in
// the destination account inside the synthetic's gap interval (extended by
// reviewDedupDays on either side).
func residualExplainedByExisting(syntheticDate time.Time, gapStart time.Time, currency string, residual decimal.Decimal, existing []existingTx) bool {
	matched, _ := matchResidualSubset(syntheticDate, gapStart, currency, residual, existing)
	return matched
}

// matchResidualSubset returns whether the residual is explained by the
// existing rows AND which row positions (indices into the input slice) made
// up the matching subset. Callers that need to "consume" matched rows so
// they don't double-cover a later synthetic should pass the indices to
// downstream logic. Used in two places:
//
//   - At classify time on banking-first → consolidated-second imports, to
//     skip inserting a synthetic whose residual is already present.
//   - At post-apply time on consolidated-first → banking-second imports, to
//     void synthetics now redundant after the banking rows arrive — there,
//     the consume-once policy prevents two synthetics with the same
//     residual from both retiring against the same single banking row.
//
// gapStart is the booked date of the consolidated row preceding the gap. A
// zero gapStart degrades to "syntheticDate alone", preserving the original
// ±7d behaviour for callers that don't have gap context. The matcher
// accepts two shapes: a single existing row whose amount equals the
// residual, or a subset whose sum equals it. We keep this conservative —
// same currency, same sign, inside the gap window — to avoid voiding a
// real synthetic that just happens to coincide with normal transaction
// noise.
func matchResidualSubset(syntheticDate time.Time, gapStart time.Time, currency string, residual decimal.Decimal, existing []existingTx) (bool, []int) {
	if gapStart.IsZero() || gapStart.After(syntheticDate) {
		gapStart = syntheticDate
	}
	from := gapStart.AddDate(0, 0, -reviewDedupDays)
	to := syntheticDate.AddDate(0, 0, reviewDedupDays)
	type cand struct {
		idx    int
		amount decimal.Decimal
	}
	cands := make([]cand, 0, len(existing))
	for i, e := range existing {
		if e.Currency != currency {
			continue
		}
		if e.BookedAt.Before(from) || e.BookedAt.After(to) {
			continue
		}
		if residual.Sign() != 0 && e.Amount.Sign() != 0 && residual.Sign() != e.Amount.Sign() {
			continue
		}
		cands = append(cands, cand{idx: i, amount: e.Amount})
	}
	if len(cands) == 0 {
		return false, nil
	}
	// Prefer a single-row exact match — keeps the consume-once policy
	// minimal-impact and avoids consuming several real rows when only one
	// is the "real" missing transaction.
	for _, c := range cands {
		if c.amount.Equal(residual) {
			return true, []int{c.idx}
		}
	}
	if len(cands) > 16 {
		// Hard cap to avoid pathological 2^N blow-up.
		return false, nil
	}
	tolerance := decimal.RequireFromString("0.02")
	for mask := 1; mask < 1<<len(cands); mask++ {
		sum := decimal.Zero
		for i, c := range cands {
			if mask&(1<<i) != 0 {
				sum = sum.Add(c.amount)
			}
		}
		if sum.Sub(residual).Abs().LessThanOrEqual(tolerance) {
			used := make([]int, 0)
			for i, c := range cands {
				if mask&(1<<i) != 0 {
					used = append(used, c.idx)
				}
			}
			return true, used
		}
	}
	return false, nil
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
