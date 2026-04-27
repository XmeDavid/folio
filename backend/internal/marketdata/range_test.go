package marketdata

import (
	"testing"
	"time"
)

func TestRangeNeedsFetch_EmptyCache(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if !rangeNeedsFetch(map[time.Time]PriceQuote{}, from, to) {
		t.Fatalf("empty cache must trigger fetch")
	}
}

func TestRangeNeedsFetch_FullCoverage(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	cached := map[time.Time]PriceQuote{
		from:                     {AsOf: from},
		from.Add(48 * time.Hour): {AsOf: from.Add(48 * time.Hour)},
		to:                       {AsOf: to},
	}
	if rangeNeedsFetch(cached, from, to) {
		t.Fatalf("cache covers both edges within 7 days; should not fetch")
	}
}

func TestRangeNeedsFetch_PartialCoverage(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// Cache stops 30 days short of `to`; should refetch.
	cached := map[time.Time]PriceQuote{
		from:                         {AsOf: from},
		to.Add(-30 * 24 * time.Hour): {AsOf: to.Add(-30 * 24 * time.Hour)},
	}
	if !rangeNeedsFetch(cached, from, to) {
		t.Fatalf("cache stops 30 days short; must trigger fetch")
	}
}

func TestAsOfDateNormalisesToMidnightUTC(t *testing.T) {
	in := time.Date(2026, 4, 27, 13, 45, 0, 0, time.FixedZone("CEST", 7200))
	got := asOfDate(in)
	want := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	// Same instant in UTC after fixedzone offset = 11:45 UTC; date is still 2026-04-27.
	if !got.Equal(want) {
		t.Fatalf("asOfDate(%s) = %s; want %s", in, got, want)
	}
}
