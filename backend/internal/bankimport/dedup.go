package bankimport

import (
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// dateProximityMatch returns true when incomingBooked is within toleranceDays
// of either the existing row's booked_at or its posted_at (when present).
// The two-axis check exists because Revolut's banking export carries both
// auth (booked) and settle (posted) dates while the consolidated v2 export
// only carries one date stamp; matching either covers same-tx pairs in both
// files.
func dateProximityMatch(incomingBooked, existingBooked time.Time, existingPosted *time.Time, toleranceDays int) bool {
	if datesWithin(incomingBooked, existingBooked, toleranceDays) {
		return true
	}
	if existingPosted != nil && datesWithin(incomingBooked, *existingPosted, toleranceDays) {
		return true
	}
	return false
}

func datesWithin(a, b time.Time, days int) bool {
	const day = 24 * time.Hour
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= time.Duration(days)*day
}

const (
	autoDedupDays   = 1
	reviewDedupDays = 7
)

// fuzzyStableMatch tests whether an incoming row is the same transaction as
// an existing one, accepting up to `toleranceDays` of date drift while
// requiring an exact amount + currency match. Use autoDedupDays for the
// auto-skip path and reviewDedupDays for the user-confirms path.
func fuzzyStableMatch(incoming ParsedTransaction, existing existingTx, toleranceDays int) bool {
	if !incoming.Amount.Equal(existing.Amount) {
		return false
	}
	if incoming.Currency != existing.Currency {
		return false
	}
	return dateProximityMatch(incoming.BookedAt, existing.BookedAt, existing.PostedAt, toleranceDays)
}

// normalizeDescription folds a transaction description for fuzzy comparison.
// It lowercases, strips punctuation, removes diacritics (so "cartão" matches
// "cartao"), and collapses runs of whitespace. Designed to make the same
// merchant name match across Revolut's banking and consolidated v2 exports
// without overgenerating false positives — we keep word boundaries so
// "Amazon" and "AmazonPrime" stay distinct.
func normalizeDescription(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err == nil {
		s = folded
	}
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevSpace = false
		case unicode.IsSpace(r):
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimRight(b.String(), " ")
}
