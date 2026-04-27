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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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

	// Lookup by ISIN first when present.
	if in.ISIN != nil && *in.ISIN != "" {
		inst, err := s.findInstrumentByISIN(ctx, *in.ISIN)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			return inst, nil
		}
	} else {
		inst, err := s.findInstrumentBySymbol(ctx, in.Symbol, in.Exchange)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			return inst, nil
		}
	}

	id := uuidx.New()
	row := s.pool.QueryRow(ctx, `
		insert into instruments (id, symbol, isin, name, asset_class, currency, exchange)
		values ($1, $2, $3, $4, $5::asset_class, $6, $7)
		on conflict do nothing
		returning id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
	`, id, in.Symbol, in.ISIN, in.Name, in.AssetClass, in.Currency, in.Exchange)
	var inst Instrument
	if err := scanInstrument(row, &inst); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Race: someone else inserted between our SELECT and INSERT.
			if in.ISIN != nil && *in.ISIN != "" {
				if got, _ := s.findInstrumentByISIN(ctx, *in.ISIN); got != nil {
					return got, nil
				}
			}
			if got, _ := s.findInstrumentBySymbol(ctx, in.Symbol, in.Exchange); got != nil {
				return got, nil
			}
			return nil, fmt.Errorf("upsert instrument: race lost")
		}
		return nil, fmt.Errorf("upsert instrument: %w", err)
	}
	return &inst, nil
}

func (s *Service) findInstrumentByISIN(ctx context.Context, isin string) (*Instrument, error) {
	var inst Instrument
	err := scanInstrument(s.pool.QueryRow(ctx, `
		select id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
		from instruments where isin = $1
	`, isin), &inst)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

func (s *Service) findInstrumentBySymbol(ctx context.Context, symbol string, exchange *string) (*Instrument, error) {
	var inst Instrument
	var err error
	if exchange != nil {
		err = scanInstrument(s.pool.QueryRow(ctx, `
			select id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
			from instruments where symbol = $1 and exchange = $2 and isin is null
		`, symbol, *exchange), &inst)
	} else {
		err = scanInstrument(s.pool.QueryRow(ctx, `
			select id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
			from instruments where symbol = $1 and exchange is null and isin is null
			limit 1
		`, symbol), &inst)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Fallback: if no exchange-specific row, try a symbol-only match.
			if exchange != nil {
				return s.findInstrumentBySymbol(ctx, symbol, nil)
			}
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// GetInstrument returns an instrument by id.
func (s *Service) GetInstrument(ctx context.Context, id uuid.UUID) (*Instrument, error) {
	var inst Instrument
	err := scanInstrument(s.pool.QueryRow(ctx, `
		select id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
		from instruments where id = $1
	`, id), &inst)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("instrument")
		}
		return nil, err
	}
	return &inst, nil
}

// GetInstrumentBySymbol returns an instrument by symbol (case-insensitive),
// preferring an active row. Returns NotFoundError when no match exists.
func (s *Service) GetInstrumentBySymbol(ctx context.Context, symbol string) (*Instrument, error) {
	var inst Instrument
	err := scanInstrument(s.pool.QueryRow(ctx, `
		select id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
		from instruments where upper(symbol) = upper($1)
		order by active desc, updated_at desc
		limit 1
	`, symbol), &inst)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("instrument")
		}
		return nil, err
	}
	return &inst, nil
}

// SearchInstruments returns instruments matching q (prefix on symbol or name).
// Used by the trade-creation autocomplete.
func (s *Service) SearchInstruments(ctx context.Context, q string, limit int) ([]Instrument, error) {
	if limit <= 0 {
		limit = 25
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return []Instrument{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		select id, symbol, isin, name, asset_class::text, currency, exchange, active, created_at, updated_at
		from instruments
		where active and (symbol ilike $1 || '%' or name ilike '%' || $1 || '%' or isin ilike $1 || '%')
		order by case when upper(symbol) = upper($1) then 0 else 1 end, symbol
		limit $2
	`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Instrument, 0)
	for rows.Next() {
		var inst Instrument
		if err := scanInstrument(rows, &inst); err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, rows.Err()
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
	row := s.pool.QueryRow(ctx, `
		insert into investment_trades (
			id, workspace_id, account_id, instrument_id, side,
			quantity, price, currency, fee_amount, fee_currency,
			trade_date, settle_date
		) values (
			$1, $2, $3, $4, $5::trade_side,
			$6::numeric, $7::numeric, $8, $9::numeric, $10,
			$11, $12
		)
		returning `+tradeCols, id, workspaceID, in.AccountID, in.InstrumentID, in.Side,
		in.Quantity.String(), in.Price.String(), in.Currency,
		in.FeeAmount.String(), in.Currency, in.TradeDate, in.SettleDate)
	tr, err := scanTradeRow(row)
	if err != nil {
		return nil, mapWriteError(err)
	}
	if err := s.RefreshPosition(ctx, workspaceID, in.AccountID, in.InstrumentID); err != nil {
		// Replay failure is recoverable on next read; surface as 500.
		return tr, fmt.Errorf("refresh position: %w", err)
	}
	return tr, nil
}

// DeleteTrade removes a trade and refreshes the affected position cache.
func (s *Service) DeleteTrade(ctx context.Context, workspaceID, tradeID uuid.UUID) error {
	var accountID, instrumentID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		select account_id, instrument_id from investment_trades
		where workspace_id = $1 and id = $2
	`, workspaceID, tradeID).Scan(&accountID, &instrumentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("trade")
		}
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		delete from investment_trades where workspace_id = $1 and id = $2
	`, workspaceID, tradeID); err != nil {
		return fmt.Errorf("delete trade: %w", err)
	}
	if err := s.RefreshPosition(ctx, workspaceID, accountID, instrumentID); err != nil {
		return fmt.Errorf("refresh position: %w", err)
	}
	return nil
}

// ListTrades returns trades for the workspace, optionally filtered by account
// and/or instrument. Ordered by trade_date desc.
func (s *Service) ListTrades(ctx context.Context, workspaceID uuid.UUID, accountID, instrumentID *uuid.UUID) ([]Trade, error) {
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
	row := s.pool.QueryRow(ctx, `
		insert into dividend_events (
			id, workspace_id, account_id, instrument_id,
			ex_date, pay_date, amount_per_unit, currency, total_amount, tax_withheld
		) values (
			$1, $2, $3, $4,
			$5, $6, $7::numeric, $8, $9::numeric, $10::numeric
		)
		returning `+dividendCols, id, workspaceID, in.AccountID, in.InstrumentID,
		in.ExDate, in.PayDate, in.AmountPerUnit.String(), in.Currency,
		in.TotalAmount.String(), in.TaxWithheld.String())
	dv, err := scanDividendRow(row)
	if err != nil {
		return nil, mapWriteError(err)
	}
	if err := s.RefreshPosition(ctx, workspaceID, in.AccountID, in.InstrumentID); err != nil {
		return dv, fmt.Errorf("refresh position: %w", err)
	}
	return dv, nil
}

// DeleteDividend removes a dividend and refreshes the affected position.
func (s *Service) DeleteDividend(ctx context.Context, workspaceID, dividendID uuid.UUID) error {
	var accountID, instrumentID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		select account_id, instrument_id from dividend_events
		where workspace_id = $1 and id = $2
	`, workspaceID, dividendID).Scan(&accountID, &instrumentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("dividend")
		}
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		delete from dividend_events where workspace_id = $1 and id = $2
	`, workspaceID, dividendID); err != nil {
		return err
	}
	return s.RefreshPosition(ctx, workspaceID, accountID, instrumentID)
}

// ListDividends returns dividend events for the workspace, optionally
// filtered by account and/or instrument. Ordered by pay_date desc.
func (s *Service) ListDividends(ctx context.Context, workspaceID uuid.UUID, accountID, instrumentID *uuid.UUID) ([]DividendEvent, error) {
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
	var exists bool
	err := s.pool.QueryRow(ctx, `
		select exists(select 1 from investment_accounts where workspace_id = $1 and account_id = $2)
	`, workspaceID, accountID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	// Confirm the parent account exists and is workspace-scoped.
	var kind string
	err = s.pool.QueryRow(ctx, `
		select kind::text from accounts where workspace_id = $1 and id = $2
	`, workspaceID, accountID).Scan(&kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("account")
		}
		return err
	}
	if kind != "brokerage" && kind != "crypto_wallet" {
		return httpx.NewValidationError("investment trades require a brokerage or crypto_wallet account")
	}
	if _, err := s.pool.Exec(ctx, `
		insert into investment_accounts (account_id, workspace_id)
		values ($1, $2)
		on conflict do nothing
	`, accountID, workspaceID); err != nil {
		return fmt.Errorf("ensure investment_account: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// SQL column lists & scanners
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

func scanInstrument(row interface{ Scan(...any) error }, inst *Instrument) error {
	return row.Scan(
		&inst.ID, &inst.Symbol, &inst.ISIN, &inst.Name, &inst.AssetClass,
		&inst.Currency, &inst.Exchange, &inst.Active, &inst.CreatedAt, &inst.UpdatedAt,
	)
}

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
