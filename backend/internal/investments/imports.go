package investments

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/investments/importevent"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// ImportEvent is re-exported here so callers in the investments package can
// reference it without a long import path. The canonical definition lives
// in the importevent subpackage so providers can produce events without
// pulling in the investments package (avoids an import cycle).
type ImportEvent = importevent.Event

// ImportSummary reports per-import counts back to the caller.
type ImportSummary struct {
	TradesCreated      int      `json:"tradesCreated"`
	DividendsCreated   int      `json:"dividendsCreated"`
	InstrumentsTouched int      `json:"instrumentsTouched"`
	Skipped            int      `json:"skipped"`
	Warnings           []string `json:"warnings,omitempty"`
}

// IngestImport routes a parsed batch into the storage layer. Instruments are
// upserted by symbol (ISIN-keyed when present), trades and dividends are
// inserted, and every affected (account, instrument) position is refreshed
// at the end. Errors on individual rows are collected as warnings rather
// than aborting the whole batch — the caller can re-import after fixing
// the source data.
func (s *Service) IngestImport(ctx context.Context, workspaceID, accountID uuid.UUID, events []ImportEvent) (*ImportSummary, error) {
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
		if ev.Exchange != "" {
			ex := ev.Exchange
			instInput.Exchange = &ex
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
			tradeInput := TradeInput{
				AccountID:    accountID,
				InstrumentID: inst.ID,
				Side:         side,
				Quantity:     ev.Quantity,
				Price:        ev.Price,
				Currency:     ev.Currency,
				FeeAmount:    ev.Fee,
				TradeDate:    ev.Date,
			}
			// Insert without per-trade refresh; we batch the refresh at the end.
			if err := s.insertTradeNoRefresh(ctx, workspaceID, tradeInput); err != nil {
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
			divInput := DividendInput{
				AccountID:     accountID,
				InstrumentID:  inst.ID,
				ExDate:        ev.Date,
				PayDate:       ev.Date,
				AmountPerUnit: ev.AmountPerUnit,
				Currency:      ev.Currency,
				TotalAmount:   ev.AmountTotal,
				TaxWithheld:   ev.TaxWithheld,
			}
			if err := s.insertDividendNoRefresh(ctx, workspaceID, divInput); err != nil {
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

// insertTradeNoRefresh persists a trade row without rebuilding the position
// cache; callers refresh in bulk after the batch finishes.
func (s *Service) insertTradeNoRefresh(ctx context.Context, workspaceID uuid.UUID, raw TradeInput) error {
	in, err := raw.normalize()
	if err != nil {
		return err
	}
	id := uuidx.New()
	_, err = s.pool.Exec(ctx, `
		insert into investment_trades (
			id, workspace_id, account_id, instrument_id, side,
			quantity, price, currency, fee_amount, fee_currency,
			trade_date, settle_date
		) values (
			$1, $2, $3, $4, $5::trade_side,
			$6::numeric, $7::numeric, $8, $9::numeric, $10,
			$11, $12
		)
	`, id, workspaceID, in.AccountID, in.InstrumentID, in.Side,
		in.Quantity.String(), in.Price.String(), in.Currency,
		in.FeeAmount.String(), in.Currency, in.TradeDate, in.SettleDate)
	if err != nil {
		return mapWriteError(err)
	}
	return nil
}

// insertDividendNoRefresh persists a dividend without refreshing.
func (s *Service) insertDividendNoRefresh(ctx context.Context, workspaceID uuid.UUID, raw DividendInput) error {
	in, err := raw.normalize()
	if err != nil {
		return err
	}
	id := uuidx.New()
	_, err = s.pool.Exec(ctx, `
		insert into dividend_events (
			id, workspace_id, account_id, instrument_id,
			ex_date, pay_date, amount_per_unit, currency, total_amount, tax_withheld
		) values (
			$1, $2, $3, $4,
			$5, $6, $7::numeric, $8, $9::numeric, $10::numeric
		)
	`, id, workspaceID, in.AccountID, in.InstrumentID,
		in.ExDate, in.PayDate, in.AmountPerUnit.String(), in.Currency,
		in.TotalAmount.String(), in.TaxWithheld.String())
	if err != nil {
		return mapWriteError(err)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
