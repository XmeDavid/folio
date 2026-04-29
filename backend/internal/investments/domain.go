// Package investments owns Folio's brokerage aggregate: instruments (global
// reference data), trades, dividend events, and the materialised position
// cache. The source of truth for any (account, instrument) holding is the
// underlying event stream — current positions are derivable by replay
// (FEATURE-BIBLE.md §11). Replay produces lots, lot consumptions on sells,
// realised P/L, and quantity. Valuation against current/historical prices
// and FX conversion to a reporting currency are layered on top via the
// marketdata package.
package investments

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Instrument is the read-model for a global instrument row.
type Instrument struct {
	ID         uuid.UUID `json:"id"`
	Symbol     string    `json:"symbol"`
	ISIN       *string   `json:"isin,omitempty"`
	Name       string    `json:"name"`
	AssetClass string    `json:"assetClass"`
	Currency   string    `json:"currency"`
	Exchange   *string   `json:"exchange,omitempty"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Trade is a buy or sell event.
type Trade struct {
	ID                      uuid.UUID  `json:"id"`
	WorkspaceID             uuid.UUID  `json:"workspaceId"`
	AccountID               uuid.UUID  `json:"accountId"`
	InstrumentID            uuid.UUID  `json:"instrumentId"`
	Symbol                  string     `json:"symbol"`
	Side                    string     `json:"side"` // buy | sell
	Quantity                string     `json:"quantity"`
	Price                   string     `json:"price"`
	Currency                string     `json:"currency"`
	FeeAmount               string     `json:"feeAmount"`
	FeeCurrency             string     `json:"feeCurrency"`
	TradeDate               time.Time  `json:"tradeDate"`
	SettleDate              *time.Time `json:"settleDate,omitempty"`
	LinkedCashTransactionID *uuid.UUID `json:"linkedCashTransactionId,omitempty"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
}

// DividendEvent is a per-position dividend payment (gross, with optional
// withholding tax).
type DividendEvent struct {
	ID                      uuid.UUID  `json:"id"`
	WorkspaceID             uuid.UUID  `json:"workspaceId"`
	AccountID               uuid.UUID  `json:"accountId"`
	InstrumentID            uuid.UUID  `json:"instrumentId"`
	Symbol                  string     `json:"symbol"`
	ExDate                  time.Time  `json:"exDate"`
	PayDate                 time.Time  `json:"payDate"`
	AmountPerUnit           string     `json:"amountPerUnit"`
	Currency                string     `json:"currency"`
	TotalAmount             string     `json:"totalAmount"`
	TaxWithheld             string     `json:"taxWithheld"`
	LinkedCashTransactionID *uuid.UUID `json:"linkedCashTransactionId,omitempty"`
	CreatedAt               time.Time  `json:"createdAt"`
}

// Position is the materialised holding for an (account, instrument) pair,
// after replay. Quantity == 0 means the position is closed but kept around
// so that historical realised P/L and dividend totals remain visible on
// drilldowns.
type Position struct {
	AccountID         uuid.UUID  `json:"accountId"`
	InstrumentID      uuid.UUID  `json:"instrumentId"`
	WorkspaceID       uuid.UUID  `json:"workspaceId"`
	Symbol            string     `json:"symbol"`
	Name              string     `json:"name"`
	AssetClass        string     `json:"assetClass"`
	InstrumentCcy     string     `json:"instrumentCurrency"`
	AccountCurrency   string     `json:"accountCurrency"`
	Quantity          string     `json:"quantity"`
	AverageCost       string     `json:"averageCost"`
	CostBasisTotal    string     `json:"costBasisTotal"`
	RealisedPnL       string     `json:"realisedPnL"`
	DividendsReceived string     `json:"dividendsReceived"`
	FeesPaid          string     `json:"feesPaid"`
	LastTradeDate     *time.Time `json:"lastTradeDate,omitempty"`
	LastPrice         *string    `json:"lastPrice,omitempty"`
	LastPriceAt       *time.Time `json:"lastPriceAt,omitempty"`
	MarketValue       *string    `json:"marketValue,omitempty"`
	UnrealisedPnL     *string    `json:"unrealisedPnL,omitempty"`
}

// Lot is a tax lot opened by a buy trade; its quantity_remaining shrinks as
// sells consume it via FIFO (default) or other accounting methods.
type Lot struct {
	ID                uuid.UUID  `json:"id"`
	AccountID         uuid.UUID  `json:"accountId"`
	InstrumentID      uuid.UUID  `json:"instrumentId"`
	AcquiredAt        time.Time  `json:"acquiredAt"`
	QuantityOpening   string     `json:"quantityOpening"`
	QuantityRemaining string     `json:"quantityRemaining"`
	CostBasisPerUnit  string     `json:"costBasisPerUnit"`
	Currency          string     `json:"currency"`
	SourceTradeID     *uuid.UUID `json:"sourceTradeId,omitempty"`
	ClosedAt          *time.Time `json:"closedAt,omitempty"`
}

// Holding is a Position augmented with reporting-currency totals.
type Holding struct {
	Position
	ReportCurrency           string  `json:"reportCurrency"`
	FXRate                   string  `json:"fxRate"`
	MarketValueReport        *string `json:"marketValueReport,omitempty"`
	CostBasisReport          string  `json:"costBasisReport"`
	UnrealisedPnLReport      *string `json:"unrealisedPnLReport,omitempty"`
	RealisedPnLReport        string  `json:"realisedPnLReport"`
	DividendsReport          string  `json:"dividendsReport"`
	FeesReport               string  `json:"feesReport"`
	TotalReturnReport        *string `json:"totalReturnReport,omitempty"`
	TotalReturnPercentReport *string `json:"totalReturnPercentReport,omitempty"`
}

// DashboardSummary is the top-of-page rollup for the investment dashboard.
type DashboardSummary struct {
	ReportCurrency         string            `json:"reportCurrency"`
	GeneratedAt            time.Time         `json:"generatedAt"`
	TotalMarketValue       string            `json:"totalMarketValue"`
	TotalCostBasis         string            `json:"totalCostBasis"`
	TotalUnrealisedPnL     string            `json:"totalUnrealisedPnL"`
	TotalUnrealisedPnLPct  string            `json:"totalUnrealisedPnLPct"`
	TotalRealisedPnL       string            `json:"totalRealisedPnL"`
	TotalDividends         string            `json:"totalDividends"`
	TotalFees              string            `json:"totalFees"`
	TotalReturn            string            `json:"totalReturn"`
	TotalReturnPct         string            `json:"totalReturnPct"`
	OpenPositionsCount     int               `json:"openPositionsCount"`
	StaleQuotes            int               `json:"staleQuotes"`
	MissingQuotes          int               `json:"missingQuotes"`
	Holdings               []Holding         `json:"holdings"`
	AllocationByCurrency   []AllocationSlice `json:"allocationByCurrency"`
	AllocationByAccount    []AllocationSlice `json:"allocationByAccount"`
	AllocationByAssetClass []AllocationSlice `json:"allocationByAssetClass"`
	TopMovers              []HoldingMover    `json:"topMovers"`
	TopProfits             []HoldingMover    `json:"topProfits"`
	TopLosses              []HoldingMover    `json:"topLosses"`
	Warnings               []string          `json:"warnings,omitempty"`
}

// PortfolioHistoryPoint is an aggregated portfolio valuation point in the
// requested reporting currency.
type PortfolioHistoryPoint struct {
	Date           time.Time `json:"date"`
	Value          string    `json:"value"`
	ReportCurrency string    `json:"reportCurrency"`
}

// AllocationSlice is one entry in an exposure breakdown.
type AllocationSlice struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
	Pct   string `json:"pct"`
}

// HoldingMover is a holding ranked by absolute unrealised P/L for the "top
// movers" widget on the dashboard.
type HoldingMover struct {
	Symbol         string `json:"symbol"`
	Name           string `json:"name"`
	UnrealisedPnL  string `json:"unrealisedPnL"`
	UnrealisedPct  string `json:"unrealisedPct"`
	DailyChange    string `json:"dailyChange,omitempty"`
	DailyChangePct string `json:"dailyChangePct,omitempty"`
	ReportCurrency string `json:"reportCurrency"`
}

// PositionFilter scopes a positions query.
type PositionFilter struct {
	AccountID    *uuid.UUID
	OpenOnly     bool
	ClosedOnly   bool
	Search       string
	InstrumentID *uuid.UUID
}

// InstrumentDetail bundles everything the per-instrument page needs.
type InstrumentDetail struct {
	Instrument     Instrument         `json:"instrument"`
	ReportCurrency string             `json:"reportCurrency"`
	Positions      []Position         `json:"positions"`
	Trades         []Trade            `json:"trades"`
	Dividends      []DividendEvent    `json:"dividends"`
	History        []HistoryDataPoint `json:"history"`
	LastQuote      *QuoteSnapshot     `json:"lastQuote,omitempty"`
}

// HistoryDataPoint is a single point on the holdings-over-time chart.
type HistoryDataPoint struct {
	Date           time.Time `json:"date"`
	Quantity       string    `json:"quantity"`
	Price          *string   `json:"price,omitempty"`
	Value          *string   `json:"value,omitempty"`
	ValueNative    *string   `json:"valueNative,omitempty"`
	Currency       string    `json:"currency"`
	NativeCurrency string    `json:"nativeCurrency"`
}

// QuoteSnapshot is the latest price observation for an instrument.
type QuoteSnapshot struct {
	Price    string    `json:"price"`
	Currency string    `json:"currency"`
	AsOf     time.Time `json:"asOf"`
	Source   string    `json:"source"`
	Stale    bool      `json:"stale"`
}

// fpDecimalZero returns decimal.Zero — defined here so tests that import
// the package don't need a separate decimal import for assertions.
func fpDecimalZero() decimal.Decimal { return decimal.Zero }
