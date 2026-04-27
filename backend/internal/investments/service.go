package investments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/marketdata"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Service is the investments service.
type Service struct {
	pool *pgxpool.Pool
	md   *marketdata.Service
	now  func() time.Time
}

// NewService returns a Service. md may be nil; in that case price/FX
// reads return cached rows only.
func NewService(pool *pgxpool.Pool, md *marketdata.Service) *Service {
	return &Service{pool: pool, md: md, now: time.Now}
}

// SetClock overrides the clock for tests.
func (s *Service) SetClock(fn func() time.Time) { s.now = fn }

// MarketData returns the underlying marketdata service (may be nil).
func (s *Service) MarketData() *marketdata.Service { return s.md }

// validAssetClass mirrors the asset_class enum in db/migrations.
var validAssetClass = map[string]bool{
	"equity": true, "etf": true, "bond": true, "fund": true,
	"reit": true, "option": true, "future": true, "crypto": true,
	"commodity": true, "cash_equivalent": true,
}

// validSide mirrors the trade_side enum.
var validSide = map[string]bool{"buy": true, "sell": true}

// decimalToNumeric converts a decimal.Decimal to pgtype.Numeric for sqlc params.
func decimalToNumeric(d decimal.Decimal) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(d.String())
	return n
}

// stringToNumeric converts a decimal string to pgtype.Numeric for sqlc params.
func stringToNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(s)
	return n
}

// numericToDecimal converts a pgtype.Numeric to decimal.Decimal.
func numericToDecimal(n pgtype.Numeric) decimal.Decimal {
	if !n.Valid {
		return decimal.Zero
	}
	d, _ := decimal.NewFromString(n.Int.String())
	if n.Exp != 0 {
		d = d.Shift(int32(n.Exp))
	}
	return d
}

// ---------------------------------------------------------------------------
// Instruments
// ---------------------------------------------------------------------------

// InstrumentInput captures the fields a caller (manual create or import) can
// set when inserting/finding an instrument.
type InstrumentInput struct {
	Symbol     string
	ISIN       *string
	Name       string
	AssetClass string
	Currency   string
	Exchange   *string
}

// UpsertInstrument finds or creates a global instrument. ISIN is used as the
// dedupe key when present; otherwise (symbol, exchange) is used per the
// db schema partial-unique indexes.
func (s *Service) UpsertInstrument(ctx context.Context, raw InstrumentInput) (*Instrument, error) {
	in, err := normalizeInstrument(raw)
	if err != nil {
		return nil, err
	}

	q := dbq.New(s.pool)

	// Lookup by ISIN first when present.
	if in.ISIN != nil && *in.ISIN != "" {
		inst, err := s.findInstrumentByISIN(ctx, q, *in.ISIN)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			return inst, nil
		}
	} else {
		inst, err := s.findInstrumentBySymbol(ctx, q, in.Symbol, in.Exchange)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			return inst, nil
		}
	}

	id := uuidx.New()
	row, err := q.InsertInstrument(ctx, dbq.InsertInstrumentParams{
		ID:         id,
		Symbol:     in.Symbol,
		Isin:       in.ISIN,
		Name:       in.Name,
		AssetClass: dbq.AssetClass(in.AssetClass),
		Currency:   in.Currency,
		Exchange:   in.Exchange,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert instrument: %w", err)
	}
	// ON CONFLICT DO NOTHING returns no rows when a conflict occurs; sqlc
	// surfaces that as pgx.ErrNoRows.
	inst := instrumentFromRow(row)
	return &inst, nil
}

// findInstrumentByISIN wraps the sqlc query with nil-on-not-found semantics.
func (s *Service) findInstrumentByISIN(ctx context.Context, q *dbq.Queries, isin string) (*Instrument, error) {
	row, err := q.FindInstrumentByISIN(ctx, &isin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	inst := Instrument{
		ID: row.ID, Symbol: row.Symbol, ISIN: row.Isin, Name: row.Name,
		AssetClass: row.AssetClass, Currency: row.Currency, Exchange: row.Exchange,
		Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return &inst, nil
}

func (s *Service) findInstrumentBySymbol(ctx context.Context, q *dbq.Queries, symbol string, exchange *string) (*Instrument, error) {
	if exchange != nil {
		row, err := q.FindInstrumentBySymbolAndExchange(ctx, dbq.FindInstrumentBySymbolAndExchangeParams{
			Symbol:   symbol,
			Exchange: exchange,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Fallback: if no exchange-specific row, try a symbol-only match.
				return s.findInstrumentBySymbol(ctx, q, symbol, nil)
			}
			return nil, err
		}
		inst := Instrument{
			ID: row.ID, Symbol: row.Symbol, ISIN: row.Isin, Name: row.Name,
			AssetClass: row.AssetClass, Currency: row.Currency, Exchange: row.Exchange,
			Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		return &inst, nil
	}

	row, err := q.FindInstrumentBySymbolOnly(ctx, symbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	inst := Instrument{
		ID: row.ID, Symbol: row.Symbol, ISIN: row.Isin, Name: row.Name,
		AssetClass: row.AssetClass, Currency: row.Currency, Exchange: row.Exchange,
		Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return &inst, nil
}

// GetInstrument returns an instrument by id.
func (s *Service) GetInstrument(ctx context.Context, id uuid.UUID) (*Instrument, error) {
	row, err := dbq.New(s.pool).GetInstrumentByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("instrument")
		}
		return nil, err
	}
	inst := Instrument{
		ID: row.ID, Symbol: row.Symbol, ISIN: row.Isin, Name: row.Name,
		AssetClass: row.AssetClass, Currency: row.Currency, Exchange: row.Exchange,
		Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return &inst, nil
}

// GetInstrumentBySymbol returns an instrument by symbol (case-insensitive),
// preferring an active row. Returns NotFoundError when no match exists.
func (s *Service) GetInstrumentBySymbol(ctx context.Context, symbol string) (*Instrument, error) {
	row, err := dbq.New(s.pool).GetInstrumentBySymbol(ctx, symbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("instrument")
		}
		return nil, err
	}
	inst := Instrument{
		ID: row.ID, Symbol: row.Symbol, ISIN: row.Isin, Name: row.Name,
		AssetClass: row.AssetClass, Currency: row.Currency, Exchange: row.Exchange,
		Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return &inst, nil
}

// SearchInstruments returns instruments matching q (prefix on symbol or name).
// Used by the trade-creation autocomplete.
func (s *Service) SearchInstruments(ctx context.Context, query string, limit int) ([]Instrument, error) {
	if limit <= 0 {
		limit = 25
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []Instrument{}, nil
	}
	rows, err := dbq.New(s.pool).SearchInstruments(ctx, dbq.SearchInstrumentsParams{
		Query:      &query,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(rows))
	for _, r := range rows {
		out = append(out, Instrument{
			ID: r.ID, Symbol: r.Symbol, ISIN: r.Isin, Name: r.Name,
			AssetClass: r.AssetClass, Currency: r.Currency, Exchange: r.Exchange,
			Active: r.Active, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Trades
// ---------------------------------------------------------------------------

// TradeInput is the input to CreateTrade.
type TradeInput struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	Side         string
	Quantity     decimal.Decimal
	Price        decimal.Decimal
	Currency     string
	FeeAmount    decimal.Decimal
	TradeDate    time.Time
	SettleDate   *time.Time
}

func (in TradeInput) normalize() (TradeInput, error) {
	in.Side = strings.ToLower(strings.TrimSpace(in.Side))
	if !validSide[in.Side] {
		return in, httpx.NewValidationError("side must be 'buy' or 'sell'")
	}
	if in.Quantity.IsZero() || in.Quantity.IsNegative() {
		return in, httpx.NewValidationError("quantity must be > 0")
	}
	if in.Price.IsNegative() {
		return in, httpx.NewValidationError("price must be >= 0")
	}
	if in.FeeAmount.IsNegative() {
		return in, httpx.NewValidationError("feeAmount must be >= 0")
	}
	if in.TradeDate.IsZero() {
		return in, httpx.NewValidationError("tradeDate is required")
	}
	cur, err := money.ParseCurrency(in.Currency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.Currency = string(cur)
	return in, nil
}

// CreateTrade inserts a trade and refreshes the materialised position cache
// for (account, instrument). Returns the freshly created Trade.
func (s *Service) CreateTrade(ctx context.Context, workspaceID uuid.UUID, raw TradeInput) (*Trade, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}
	// Verify the investment account exists and belongs to the workspace,
	// auto-creating the investment_accounts extension row when only the
	// underlying brokerage account exists. The schema requires a row in
	// investment_accounts before any trades can land.
	if err := s.ensureInvestmentAccount(ctx, workspaceID, in.AccountID); err != nil {
		return nil, err
	}

	id := uuidx.New()
	q := dbq.New(s.pool)
	row, err := q.InsertInvestmentTrade(ctx, dbq.InsertInvestmentTradeParams{
		ID:           id,
		WorkspaceID:  workspaceID,
		AccountID:    in.AccountID,
		InstrumentID: in.InstrumentID,
		Side:         dbq.TradeSide(in.Side),
		Quantity:     decimalToNumeric(in.Quantity),
		Price:        decimalToNumeric(in.Price),
		Currency:     in.Currency,
		FeeAmount:    decimalToNumeric(in.FeeAmount),
		FeeCurrency:  in.Currency,
		TradeDate:    in.TradeDate,
		SettleDate:   in.SettleDate,
	})
	if err != nil {
		return nil, mapWriteError(err)
	}

	// Fetch the symbol from the instrument for the response.
	instCurrency, _ := q.GetInstrumentCurrency(ctx, in.InstrumentID)
	_ = instCurrency // used only for position refresh below
	instRow, _ := q.GetInstrumentByID(ctx, in.InstrumentID)

	tr := &Trade{
		ID: row.ID, WorkspaceID: row.WorkspaceID, AccountID: row.AccountID,
		InstrumentID: row.InstrumentID, Symbol: instRow.Symbol,
		Side: row.Side, Quantity: row.Quantity, Price: row.Price,
		Currency: row.Currency, FeeAmount: row.FeeAmount, FeeCurrency: row.FeeCurrency,
		TradeDate: row.TradeDate, SettleDate: row.SettleDate,
		LinkedCashTransactionID: row.LinkedCashTransactionID,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if err := s.RefreshPosition(ctx, workspaceID, in.AccountID, in.InstrumentID); err != nil {
		// Replay failure is recoverable on next read; surface as 500.
		return tr, fmt.Errorf("refresh position: %w", err)
	}
	return tr, nil
}

// DeleteTrade removes a trade and refreshes the affected position cache.
func (s *Service) DeleteTrade(ctx context.Context, workspaceID, tradeID uuid.UUID) error {
	q := dbq.New(s.pool)
	ids, err := q.GetTradeAccountInstrument(ctx, dbq.GetTradeAccountInstrumentParams{
		WorkspaceID: workspaceID,
		ID:          tradeID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("trade")
		}
		return err
	}
	if err := q.DeleteInvestmentTrade(ctx, dbq.DeleteInvestmentTradeParams{
		WorkspaceID: workspaceID,
		ID:          tradeID,
	}); err != nil {
		return fmt.Errorf("delete trade: %w", err)
	}
	if err := s.RefreshPosition(ctx, workspaceID, ids.AccountID, ids.InstrumentID); err != nil {
		return fmt.Errorf("refresh position: %w", err)
	}
	return nil
}

// ListTrades returns trades for the workspace, optionally filtered by account
// and/or instrument. Ordered by trade_date desc.
func (s *Service) ListTrades(ctx context.Context, workspaceID uuid.UUID, accountID, instrumentID *uuid.UUID) ([]Trade, error) {
	// Dynamic SQL: conditional WHERE clauses based on optional filters.
	args := []any{workspaceID}
	clauses := []string{"t.workspace_id = $1"}
	next := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if accountID != nil {
		clauses = append(clauses, "t.account_id = "+next(*accountID))
	}
	if instrumentID != nil {
		clauses = append(clauses, "t.instrument_id = "+next(*instrumentID))
	}
	q := `
		select ` + tradeCols + `
		from investment_trades t
		join instruments i on i.id = t.instrument_id
		where ` + strings.Join(clauses, " and ") + `
		order by t.trade_date desc, t.id desc
		limit 1000
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Trade, 0)
	for rows.Next() {
		t, err := scanTradeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Dividends
// ---------------------------------------------------------------------------

// DividendInput is the input to CreateDividend.
type DividendInput struct {
	AccountID     uuid.UUID
	InstrumentID  uuid.UUID
	ExDate        time.Time
	PayDate       time.Time
	AmountPerUnit decimal.Decimal
	Currency      string
	TotalAmount   decimal.Decimal
	TaxWithheld   decimal.Decimal
}

func (in DividendInput) normalize() (DividendInput, error) {
	if in.PayDate.IsZero() {
		return in, httpx.NewValidationError("payDate is required")
	}
	if in.ExDate.IsZero() {
		in.ExDate = in.PayDate
	}
	if in.AmountPerUnit.IsNegative() {
		return in, httpx.NewValidationError("amountPerUnit must be >= 0")
	}
	if in.TotalAmount.IsNegative() {
		return in, httpx.NewValidationError("totalAmount must be >= 0")
	}
	if in.TaxWithheld.IsNegative() {
		return in, httpx.NewValidationError("taxWithheld must be >= 0")
	}
	if in.TaxWithheld.GreaterThan(in.TotalAmount) {
		return in, httpx.NewValidationError("taxWithheld cannot exceed totalAmount")
	}
	cur, err := money.ParseCurrency(in.Currency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.Currency = string(cur)
	return in, nil
}

// CreateDividend inserts a dividend and refreshes the affected position.
func (s *Service) CreateDividend(ctx context.Context, workspaceID uuid.UUID, raw DividendInput) (*DividendEvent, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}
	if err := s.ensureInvestmentAccount(ctx, workspaceID, in.AccountID); err != nil {
		return nil, err
	}

	id := uuidx.New()
	q := dbq.New(s.pool)
	row, err := q.InsertDividendEvent(ctx, dbq.InsertDividendEventParams{
		ID:            id,
		WorkspaceID:   workspaceID,
		AccountID:     in.AccountID,
		InstrumentID:  in.InstrumentID,
		ExDate:        in.ExDate,
		PayDate:       in.PayDate,
		AmountPerUnit: decimalToNumeric(in.AmountPerUnit),
		Currency:      in.Currency,
		TotalAmount:   decimalToNumeric(in.TotalAmount),
		TaxWithheld:   decimalToNumeric(in.TaxWithheld),
	})
	if err != nil {
		return nil, mapWriteError(err)
	}

	// Fetch the symbol for the response.
	instRow, _ := q.GetInstrumentByID(ctx, in.InstrumentID)

	dv := &DividendEvent{
		ID: row.ID, WorkspaceID: row.WorkspaceID, AccountID: row.AccountID,
		InstrumentID: row.InstrumentID, Symbol: instRow.Symbol,
		ExDate: row.ExDate, PayDate: row.PayDate, AmountPerUnit: row.AmountPerUnit,
		Currency: row.Currency, TotalAmount: row.TotalAmount, TaxWithheld: row.TaxWithheld,
		LinkedCashTransactionID: row.LinkedCashTransactionID, CreatedAt: row.CreatedAt,
	}
	if err := s.RefreshPosition(ctx, workspaceID, in.AccountID, in.InstrumentID); err != nil {
		return dv, fmt.Errorf("refresh position: %w", err)
	}
	return dv, nil
}

// DeleteDividend removes a dividend and refreshes the affected position.
func (s *Service) DeleteDividend(ctx context.Context, workspaceID, dividendID uuid.UUID) error {
	q := dbq.New(s.pool)
	ids, err := q.GetDividendAccountInstrument(ctx, dbq.GetDividendAccountInstrumentParams{
		WorkspaceID: workspaceID,
		ID:          dividendID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("dividend")
		}
		return err
	}
	if err := q.DeleteDividendEvent(ctx, dbq.DeleteDividendEventParams{
		WorkspaceID: workspaceID,
		ID:          dividendID,
	}); err != nil {
		return err
	}
	return s.RefreshPosition(ctx, workspaceID, ids.AccountID, ids.InstrumentID)
}

// ListDividends returns dividend events for the workspace, optionally
// filtered by account and/or instrument. Ordered by pay_date desc.
func (s *Service) ListDividends(ctx context.Context, workspaceID uuid.UUID, accountID, instrumentID *uuid.UUID) ([]DividendEvent, error) {
	// Dynamic SQL: conditional WHERE clauses based on optional filters.
	args := []any{workspaceID}
	clauses := []string{"d.workspace_id = $1"}
	next := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if accountID != nil {
		clauses = append(clauses, "d.account_id = "+next(*accountID))
	}
	if instrumentID != nil {
		clauses = append(clauses, "d.instrument_id = "+next(*instrumentID))
	}
	q := `
		select ` + dividendCols + `
		from dividend_events d
		join instruments i on i.id = d.instrument_id
		where ` + strings.Join(clauses, " and ") + `
		order by d.pay_date desc, d.id desc
		limit 1000
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DividendEvent, 0)
	for rows.Next() {
		d, err := scanDividendRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ensureInvestmentAccount inserts a passthrough investment_accounts row if
// the underlying account exists in the workspace and the extension row is
// missing. Idempotent.
func (s *Service) ensureInvestmentAccount(ctx context.Context, workspaceID, accountID uuid.UUID) error {
	q := dbq.New(s.pool)
	exists, err := q.InvestmentAccountExists(ctx, dbq.InvestmentAccountExistsParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
	})
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	// Confirm the parent account exists and is workspace-scoped.
	kind, err := q.GetAccountKind(ctx, dbq.GetAccountKindParams{
		WorkspaceID: workspaceID,
		ID:          accountID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("account")
		}
		return err
	}
	if kind != "brokerage" && kind != "crypto_wallet" {
		return httpx.NewValidationError("investment trades require a brokerage or crypto_wallet account")
	}
	if err := q.InsertInvestmentAccount(ctx, dbq.InsertInvestmentAccountParams{
		AccountID:   accountID,
		WorkspaceID: workspaceID,
	}); err != nil {
		return fmt.Errorf("ensure investment_account: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// SQL column lists & scanners (kept for dynamic queries)
// ---------------------------------------------------------------------------

const tradeCols = `
	t.id, t.workspace_id, t.account_id, t.instrument_id, i.symbol,
	t.side::text, t.quantity::text, t.price::text, t.currency,
	t.fee_amount::text, t.fee_currency, t.trade_date, t.settle_date,
	t.linked_cash_transaction_id, t.created_at, t.updated_at
`

const dividendCols = `
	d.id, d.workspace_id, d.account_id, d.instrument_id, i.symbol,
	d.ex_date, d.pay_date, d.amount_per_unit::text, d.currency,
	d.total_amount::text, d.tax_withheld::text,
	d.linked_cash_transaction_id, d.created_at
`

type rowScanner interface{ Scan(...any) error }

func scanTradeRow(row rowScanner) (*Trade, error) {
	var t Trade
	if err := row.Scan(
		&t.ID, &t.WorkspaceID, &t.AccountID, &t.InstrumentID, &t.Symbol,
		&t.Side, &t.Quantity, &t.Price, &t.Currency,
		&t.FeeAmount, &t.FeeCurrency, &t.TradeDate, &t.SettleDate,
		&t.LinkedCashTransactionID, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

func scanDividendRow(row rowScanner) (*DividendEvent, error) {
	var d DividendEvent
	if err := row.Scan(
		&d.ID, &d.WorkspaceID, &d.AccountID, &d.InstrumentID, &d.Symbol,
		&d.ExDate, &d.PayDate, &d.AmountPerUnit, &d.Currency,
		&d.TotalAmount, &d.TaxWithheld,
		&d.LinkedCashTransactionID, &d.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &d, nil
}

func instrumentFromRow(r dbq.InsertInstrumentRow) Instrument {
	return Instrument{
		ID: r.ID, Symbol: r.Symbol, ISIN: r.Isin, Name: r.Name,
		AssetClass: r.AssetClass, Currency: r.Currency, Exchange: r.Exchange,
		Active: r.Active, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func normalizeInstrument(in InstrumentInput) (InstrumentInput, error) {
	in.Symbol = strings.ToUpper(strings.TrimSpace(in.Symbol))
	in.Name = strings.TrimSpace(in.Name)
	in.AssetClass = strings.ToLower(strings.TrimSpace(in.AssetClass))
	if in.Symbol == "" {
		return in, httpx.NewValidationError("symbol is required")
	}
	if in.Name == "" {
		in.Name = in.Symbol
	}
	if in.AssetClass == "" {
		in.AssetClass = "equity"
	}
	if !validAssetClass[in.AssetClass] {
		return in, httpx.NewValidationError("assetClass must be one of equity|etf|bond|fund|reit|option|future|crypto|commodity|cash_equivalent")
	}
	cur, err := money.ParseCurrency(in.Currency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.Currency = string(cur)
	if in.ISIN != nil {
		s := strings.ToUpper(strings.TrimSpace(*in.ISIN))
		if s == "" {
			in.ISIN = nil
		} else {
			in.ISIN = &s
		}
	}
	if in.Exchange != nil {
		s := strings.TrimSpace(*in.Exchange)
		if s == "" {
			in.Exchange = nil
		} else {
			in.Exchange = &s
		}
	}
	return in, nil
}

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return httpx.NewValidationError("referenced entity does not exist for this workspace")
		case "23514":
			return httpx.NewValidationError(pgErr.Message)
		case "P0001":
			return httpx.NewValidationError(pgErr.Message)
		}
	}
	return err
}
