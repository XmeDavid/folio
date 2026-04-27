// Package marketdata centralises external market-data access (FX rates and
// instrument prices) behind a thin provider interface, with a Postgres-backed
// cache that mirrors the durable-reference-data shape from the spec
// (FEATURE-BIBLE.md §11): historical closes and FX rates are global, shared
// across workspaces, and treated as durable; current/latest quotes are
// short-lived cache entries.
//
// Providers are pluggable so that Yahoo (the default for instruments) and
// Frankfurter/ECB (the default for FX) can be replaced without rippling
// through the investments service.
package marketdata

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// PriceQuote is a single instrument observation.
type PriceQuote struct {
	Symbol   string
	AsOf     time.Time
	Price    decimal.Decimal
	Currency string
	// Source indicates the provider that produced the row. Mirrors the
	// price_source enum on instrument_prices.
	Source string
}

// FXObservation is a single FX rate observation: 1 unit of Base = Rate units
// of Quote, valid AsOf the given date.
type FXObservation struct {
	Base  string
	Quote string
	AsOf  time.Time
	Rate  decimal.Decimal
	// Source mirrors the fx_source enum on fx_rates.
	Source string
}

// PriceProvider fetches instrument prices from an external source. Implementers
// MUST be safe for concurrent use.
type PriceProvider interface {
	Name() string
	// LatestQuote returns the latest available quote for symbol.
	LatestQuote(ctx context.Context, symbol string) (PriceQuote, error)
	// HistoricalRange returns daily-close observations for symbol over [from, to].
	// Implementations should return at least one row per trading day in the
	// window when data exists; missing weekends/holidays are expected.
	HistoricalRange(ctx context.Context, symbol string, from, to time.Time) ([]PriceQuote, error)
}

// FXProvider fetches FX rates from an external source.
type FXProvider interface {
	Name() string
	// HistoricalRate returns the rate for base->quote on a specific date. The
	// provider is responsible for falling back to the prior business day when
	// the requested date is a non-trading day.
	HistoricalRate(ctx context.Context, base, quote string, asOf time.Time) (FXObservation, error)
	// LatestRate returns the most recent published rate for base->quote.
	LatestRate(ctx context.Context, base, quote string) (FXObservation, error)
}
