// Package importevent defines the canonical wire format between investment
// importers (broker-specific parsers under internal/providers/...) and the
// investments service. It lives in its own package so providers can stay
// import-cycle-free of the larger investments package.
package importevent

import (
	"time"

	"github.com/shopspring/decimal"
)

// Kind enumerates the canonical event types an importer can emit.
// Importers translate broker-specific taxonomies into this set; the
// ingestion path is a single funnel from there.
type Kind string

const (
	Trade    Kind = "trade"
	Dividend Kind = "dividend"
)

// Event is the wire format between parsers and the ingestion service.
// Trade fields are required for Kind=Trade; dividend fields are required
// for Kind=Dividend.
type Event struct {
	Kind Kind

	// Common
	Symbol     string
	ISIN       string
	Name       string
	AssetClass string
	Exchange   string
	Currency   string
	Date       time.Time

	// Trade-only
	TradeSide string // "buy" | "sell"
	Quantity  decimal.Decimal
	Price     decimal.Decimal
	Fee       decimal.Decimal

	// Dividend-only
	AmountTotal   decimal.Decimal
	AmountPerUnit decimal.Decimal
	TaxWithheld   decimal.Decimal
}
