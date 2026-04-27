package investments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/investments/importevent"
	"github.com/xmedavid/folio/backend/internal/providers/ibkr"
	"github.com/xmedavid/folio/backend/internal/providers/revolut"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// SmartImportResult is the JSON shape returned to the client when a smart
// import has matched and ingested an investment file. AccountID points at
// either the existing brokerage account that absorbed the events or the
// freshly-created one.
type SmartImportResult struct {
	Detected     bool          `json:"detected"`
	Source       string        `json:"source"` // "ibkr" | "revolut_trading"
	AccountID    uuid.UUID     `json:"accountId"`
	AccountName  string        `json:"accountName"`
	BaseCurrency string        `json:"baseCurrency"`
	Created      bool          `json:"created"`
	Summary      ImportSummary `json:"summary"`
}

// SmartImport detects whether content is an IBKR Activity Statement / JSON or
// a Revolut Trading CSV, then auto-finds or creates a brokerage account in
// the workspace and ingests events with per-event dedupe. Returns Detected=
// false when the content is not an investment format; callers can then fall
// through to other parsers (e.g. bank import).
func (s *Service) SmartImport(ctx context.Context, workspaceID uuid.UUID, content []byte, fileName string) (*SmartImportResult, error) {
	source, events, baseCurrency, err := detectAndParse(content)
	if err != nil {
		return nil, err
	}
	if source == "" {
		return &SmartImportResult{Detected: false}, nil
	}
	if len(events) == 0 {
		return &SmartImportResult{Detected: true, Source: source, BaseCurrency: baseCurrency,
			Summary: ImportSummary{Warnings: []string{"file recognised but no investment events were found"}}}, nil
	}

	if baseCurrency == "" {
		// Fall back to the currency of the first event.
		baseCurrency = events[0].Currency
	}
	baseCurrency = strings.ToUpper(strings.TrimSpace(baseCurrency))

	account, created, err := s.findOrCreateBrokerageAccount(ctx, workspaceID, source, baseCurrency)
	if err != nil {
		return nil, err
	}

	summary, err := s.IngestImportDedup(ctx, workspaceID, account.ID, events)
	if err != nil {
		return nil, err
	}

	return &SmartImportResult{
		Detected:     true,
		Source:       source,
		AccountID:    account.ID,
		AccountName:  account.Name,
		BaseCurrency: baseCurrency,
		Created:      created,
		Summary:      *summary,
	}, nil
}

// detectAndParse routes content to the right parser. Returns ("", nil, "",
// nil) when the content is not recognised as investment data.
func detectAndParse(content []byte) (source string, events []importevent.Event, baseCurrency string, err error) {
	trimmed := strings.TrimLeft(string(content), " \t\r\n")
	switch {
	case strings.HasPrefix(trimmed, "Statement,Header,Field Name") ||
		strings.Contains(trimmed, "\"trade_amount_debited\""):
		res, perr := ibkr.Parse([]byte(trimmed))
		if perr != nil {
			return "", nil, "", perr
		}
		return "ibkr", res.Events, res.BaseCurrency, nil
	case looksLikeRevolutTradingHeader(trimmed):
		res, perr := revolut.ParseTradingCSV([]byte(trimmed))
		if perr != nil {
			return "", nil, "", perr
		}
		ccy := ""
		for _, ev := range res.Events {
			if ev.Currency != "" {
				ccy = ev.Currency
				break
			}
		}
		return "revolut_trading", res.Events, ccy, nil
	}
	return "", nil, "", nil
}

// looksLikeRevolutTradingHeader sniffs the first line for the canonical
// Revolut Trading CSV header columns. Tight check so we don't accidentally
// scoop up generic CSVs.
func looksLikeRevolutTradingHeader(content string) bool {
	nl := strings.IndexByte(content, '\n')
	first := content
	if nl > 0 {
		first = content[:nl]
	}
	first = strings.TrimSpace(first)
	required := []string{"Date", "Ticker", "Type", "Total Amount", "Currency"}
	for _, r := range required {
		if !strings.Contains(first, r) {
			return false
		}
	}
	return true
}

// brokerageAccountSummary is the slim row we hand back from the
// find-or-create helper.
type brokerageAccountSummary struct {
	ID   uuid.UUID
	Name string
}

func (s *Service) findOrCreateBrokerageAccount(ctx context.Context, workspaceID uuid.UUID, source, currency string) (brokerageAccountSummary, bool, error) {
	displayName := defaultBrokerageName(source)

	// Existing brokerage account with matching currency wins; institution and
	// nickname are best-effort matched on the source label.
	var (
		acct     brokerageAccountSummary
		existing bool
	)
	err := s.pool.QueryRow(ctx, `
		select a.id, a.name
		from accounts a
		where a.workspace_id = $1 and a.kind = 'brokerage'
		  and a.archived_at is null
		  and (
		    a.currency = $2
		    or coalesce(a.institution, '') ilike '%' || $3 || '%'
		    or coalesce(a.nickname, '') ilike '%' || $3 || '%'
		    or a.name ilike '%' || $3 || '%'
		  )
		order by case when a.currency = $2 then 0 else 1 end,
		         a.created_at asc
		limit 1
	`, workspaceID, currency, displayName).Scan(&acct.ID, &acct.Name)
	if err == nil {
		existing = true
		return acct, false, ensureExtension(ctx, s, workspaceID, acct.ID, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return acct, false, fmt.Errorf("find brokerage account: %w", err)
	}

	// Create one. Opening balance defaults to zero on the file's earliest
	// trade date if we have one; otherwise use today.
	id := uuidx.New()
	openDate := s.now().UTC()
	institution := defaultBrokerageInstitution(source)
	if _, err := s.pool.Exec(ctx, `
		insert into accounts (
			id, workspace_id, name, kind, currency, institution,
			open_date, opening_balance, opening_balance_date,
			include_in_networth, include_in_savings_rate
		) values (
			$1, $2, $3, 'brokerage'::account_kind, $4, $5,
			$6, 0, $6,
			true, false
		)
	`, id, workspaceID, displayName, currency, institution, openDate); err != nil {
		return acct, false, fmt.Errorf("create brokerage account: %w", err)
	}
	// Opening snapshot so balance reads work.
	if _, err := s.pool.Exec(ctx, `
		insert into account_balance_snapshots (
			id, workspace_id, account_id, as_of, balance, currency, source
		) values (
			$1, $2, $3, $4, 0, $5, 'opening'
		)
	`, uuidx.New(), workspaceID, id, openDate, currency); err != nil {
		return acct, false, fmt.Errorf("insert opening snapshot: %w", err)
	}
	if err := s.ensureInvestmentAccount(ctx, workspaceID, id); err != nil {
		return acct, false, err
	}
	return brokerageAccountSummary{ID: id, Name: displayName}, true, nil
}

func defaultBrokerageName(source string) string {
	switch source {
	case "ibkr":
		return "Interactive Brokers"
	case "revolut_trading":
		return "Revolut Trading"
	default:
		return "Brokerage"
	}
}

func defaultBrokerageInstitution(source string) string {
	switch source {
	case "ibkr":
		return "Interactive Brokers"
	case "revolut_trading":
		return "Revolut"
	default:
		return ""
	}
}

func ensureExtension(ctx context.Context, s *Service, workspaceID, accountID uuid.UUID, existing bool) error {
	if !existing {
		return nil
	}
	return s.ensureInvestmentAccount(ctx, workspaceID, accountID)
}

// IngestImportDedup is IngestImport plus a per-event dedupe check: a trade
// with identical (account, instrument, side, trade_date, quantity, price) is
// skipped, and a dividend with identical (account, instrument, pay_date,
// total_amount) is skipped. Lets users re-upload overlapping monthly
// statements (March + April that re-includes March) without producing
// duplicates.
func (s *Service) IngestImportDedup(ctx context.Context, workspaceID, accountID uuid.UUID, events []importevent.Event) (*ImportSummary, error) {
	if err := s.ensureInvestmentAccount(ctx, workspaceID, accountID); err != nil {
		return nil, err
	}

	summary := &ImportSummary{}
	touchedInstruments := make(map[uuid.UUID]struct{})
	touchedPairs := make(map[[2]uuid.UUID]struct{})

	for i, ev := range events {
		if ev.Symbol == "" || ev.Currency == "" {
			summary.Skipped++
			summary.Warnings = append(summary.Warnings, fmt.Sprintf("row %d: missing symbol or currency", i))
			continue
		}
		instInput := InstrumentInput{
			Symbol:     ev.Symbol,
			Name:       firstNonEmpty(ev.Name, ev.Symbol),
			AssetClass: firstNonEmpty(ev.AssetClass, "equity"),
			Currency:   ev.Currency,
		}
		if ev.ISIN != "" {
			isin := ev.ISIN
			instInput.ISIN = &isin
		}
		inst, err := s.UpsertInstrument(ctx, instInput)
		if err != nil {
			summary.Skipped++
			summary.Warnings = append(summary.Warnings, fmt.Sprintf("row %d: upsert instrument %s: %v", i, ev.Symbol, err))
			continue
		}
		touchedInstruments[inst.ID] = struct{}{}

		switch ev.Kind {
		case importevent.Trade:
			side := strings.ToLower(strings.TrimSpace(ev.TradeSide))
			if !validSide[side] {
				summary.Skipped++
				continue
			}
			if ev.Quantity.LessThanOrEqual(decimal.Zero) {
				summary.Skipped++
				continue
			}
			dup, err := s.tradeExists(ctx, workspaceID, accountID, inst.ID, side, ev.Date, ev.Quantity, ev.Price)
			if err != nil {
				summary.Skipped++
				summary.Warnings = append(summary.Warnings, fmt.Sprintf("row %d: dedupe trade %s: %v", i, ev.Symbol, err))
				continue
			}
			if dup {
				summary.Skipped++
				continue
			}
			if err := s.insertTradeNoRefresh(ctx, workspaceID, TradeInput{
				AccountID:    accountID,
				InstrumentID: inst.ID,
				Side:         side,
				Quantity:     ev.Quantity,
				Price:        ev.Price,
				Currency:     ev.Currency,
				FeeAmount:    ev.Fee,
				TradeDate:    ev.Date,
			}); err != nil {
				summary.Skipped++
				summary.Warnings = append(summary.Warnings, fmt.Sprintf("row %d: insert trade %s: %v", i, ev.Symbol, err))
				continue
			}
			summary.TradesCreated++
			touchedPairs[[2]uuid.UUID{accountID, inst.ID}] = struct{}{}
		case importevent.Dividend:
			if ev.AmountTotal.LessThanOrEqual(decimal.Zero) && ev.TaxWithheld.LessThanOrEqual(decimal.Zero) {
				summary.Skipped++
				continue
			}
			dup, err := s.dividendExists(ctx, workspaceID, accountID, inst.ID, ev.Date, ev.AmountTotal)
			if err != nil {
				summary.Skipped++
				summary.Warnings = append(summary.Warnings, fmt.Sprintf("row %d: dedupe dividend %s: %v", i, ev.Symbol, err))
				continue
			}
			if dup {
				summary.Skipped++
				continue
			}
			if err := s.insertDividendNoRefresh(ctx, workspaceID, DividendInput{
				AccountID:     accountID,
				InstrumentID:  inst.ID,
				ExDate:        ev.Date,
				PayDate:       ev.Date,
				AmountPerUnit: ev.AmountPerUnit,
				Currency:      ev.Currency,
				TotalAmount:   ev.AmountTotal,
				TaxWithheld:   ev.TaxWithheld,
			}); err != nil {
				summary.Skipped++
				summary.Warnings = append(summary.Warnings, fmt.Sprintf("row %d: insert dividend %s: %v", i, ev.Symbol, err))
				continue
			}
			summary.DividendsCreated++
			touchedPairs[[2]uuid.UUID{accountID, inst.ID}] = struct{}{}
		}
	}

	for pair := range touchedPairs {
		if err := s.RefreshPosition(ctx, workspaceID, pair[0], pair[1]); err != nil {
			summary.Warnings = append(summary.Warnings, fmt.Sprintf("refresh %s: %v", pair[1], err))
		}
	}
	summary.InstrumentsTouched = len(touchedInstruments)
	return summary, nil
}

func (s *Service) tradeExists(ctx context.Context, workspaceID, accountID, instrumentID uuid.UUID, side string, tradeDate time.Time, quantity, price decimal.Decimal) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		select exists(
			select 1 from investment_trades
			where workspace_id = $1 and account_id = $2 and instrument_id = $3
			  and side = $4::trade_side
			  and trade_date = $5
			  and quantity = $6::numeric
			  and price = $7::numeric
		)
	`, workspaceID, accountID, instrumentID, side,
		dateOnlyUTC(tradeDate), quantity.String(), price.String()).Scan(&ok)
	return ok, err
}

func (s *Service) dividendExists(ctx context.Context, workspaceID, accountID, instrumentID uuid.UUID, payDate time.Time, totalAmount decimal.Decimal) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		select exists(
			select 1 from dividend_events
			where workspace_id = $1 and account_id = $2 and instrument_id = $3
			  and pay_date = $4
			  and total_amount = $5::numeric
		)
	`, workspaceID, accountID, instrumentID,
		dateOnlyUTC(payDate), totalAmount.String()).Scan(&ok)
	return ok, err
}

func dateOnlyUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
