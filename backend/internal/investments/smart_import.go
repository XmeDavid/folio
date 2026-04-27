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

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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
	// IBKR exports always start with a UTF-8 BOM. Drop it explicitly via
	// TrimPrefix and then strip leading whitespace so HasPrefix below sees
	// the real first character.
	s := strings.TrimPrefix(string(content), "\ufeff")
	trimmed := strings.TrimLeft(s, " \t\r\n")
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

	q := dbq.New(s.pool)

	// Existing brokerage account with matching currency wins; institution and
	// nickname are best-effort matched on the source label.
	row, err := q.FindBrokerageAccount(ctx, dbq.FindBrokerageAccountParams{
		WorkspaceID: workspaceID,
		Currency:    currency,
		SourceLabel: &displayName,
	})
	if err == nil {
		acct := brokerageAccountSummary{ID: row.ID, Name: row.Name}
		return acct, false, ensureExtension(ctx, s, workspaceID, acct.ID, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return brokerageAccountSummary{}, false, fmt.Errorf("find brokerage account: %w", err)
	}

	// Create one. Opening balance defaults to zero on the file's earliest
	// trade date if we have one; otherwise use today.
	id := uuidx.New()
	openDate := s.now().UTC()
	institution := defaultBrokerageInstitution(source)
	if err := q.InsertBrokerageAccount(ctx, dbq.InsertBrokerageAccountParams{
		ID:          id,
		WorkspaceID: workspaceID,
		Name:        displayName,
		Currency:    currency,
		Institution: &institution,
		OpenDate:    openDate,
	}); err != nil {
		return brokerageAccountSummary{}, false, fmt.Errorf("create brokerage account: %w", err)
	}
	// Opening snapshot so balance reads work.
	if err := q.InsertBrokerageOpeningSnapshot(ctx, dbq.InsertBrokerageOpeningSnapshotParams{
		ID:          uuidx.New(),
		WorkspaceID: workspaceID,
		AccountID:   id,
		AsOf:        openDate,
		Currency:    currency,
	}); err != nil {
		return brokerageAccountSummary{}, false, fmt.Errorf("insert opening snapshot: %w", err)
	}
	if err := s.ensureInvestmentAccount(ctx, workspaceID, id); err != nil {
		return brokerageAccountSummary{}, false, err
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

	q := dbq.New(s.pool)
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
			dup, err := q.TradeExists(ctx, dbq.TradeExistsParams{
				WorkspaceID:  workspaceID,
				AccountID:    accountID,
				InstrumentID: inst.ID,
				Side:         dbq.TradeSide(side),
				TradeDate:    dateOnlyUTC(ev.Date),
				Quantity:     decimalToNumeric(ev.Quantity),
				Price:        decimalToNumeric(ev.Price),
			})
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
			dup, err := q.DividendExists(ctx, dbq.DividendExistsParams{
				WorkspaceID:  workspaceID,
				AccountID:    accountID,
				InstrumentID: inst.ID,
				PayDate:      dateOnlyUTC(ev.Date),
				TotalAmount:  decimalToNumeric(ev.AmountTotal),
			})
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

func dateOnlyUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
