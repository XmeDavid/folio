package investments

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// loadEvents pulls the canonical event stream for (account, instrument) from
// trades, dividends, and corporate_actions. Sorted in the order ReplayPosition
// expects (ReplayPosition resorts internally as well, so this is just for
// determinism on inspection).
func (s *Service) loadEvents(ctx context.Context, workspaceID, accountID, instrumentID uuid.UUID) ([]ReplayEvent, error) {
	out := make([]ReplayEvent, 0, 64)
	q := dbq.New(s.pool)

	tradeRows, err := q.LoadTradeEvents(ctx, dbq.LoadTradeEventsParams{
		WorkspaceID:  workspaceID,
		AccountID:    accountID,
		InstrumentID: instrumentID,
	})
	if err != nil {
		return nil, fmt.Errorf("load trades: %w", err)
	}
	for _, r := range tradeRows {
		quantity, _ := decimal.NewFromString(r.Quantity)
		price, _ := decimal.NewFromString(r.Price)
		feeAmt, _ := decimal.NewFromString(r.FeeAmount)
		kind := EventBuy
		if r.Side == "sell" {
			kind = EventSell
		}
		out = append(out, ReplayEvent{
			Date:     r.TradeDate,
			Kind:     kind,
			TradeID:  r.ID,
			Quantity: quantity,
			Price:    price,
			Fee:      feeAmt,
			Currency: r.Currency,
		})
	}

	dvRows, err := q.LoadDividendEvents(ctx, dbq.LoadDividendEventsParams{
		WorkspaceID:  workspaceID,
		AccountID:    accountID,
		InstrumentID: instrumentID,
	})
	if err != nil {
		return nil, fmt.Errorf("load dividends: %w", err)
	}
	for _, r := range dvRows {
		amt, _ := decimal.NewFromString(r.TotalAmount)
		out = append(out, ReplayEvent{
			Date:     r.PayDate,
			Kind:     EventDividend,
			Amount:   amt,
			Currency: r.Currency,
		})
	}

	caRows, err := q.LoadCorporateActionEvents(ctx, dbq.LoadCorporateActionEventsParams{
		WorkspaceID:  &workspaceID,
		AccountID:    &accountID,
		InstrumentID: instrumentID,
	})
	if err != nil {
		return nil, fmt.Errorf("load corporate actions: %w", err)
	}
	for _, r := range caRows {
		ev := ReplayEvent{Date: r.EffectiveDate}
		switch r.Kind {
		case "split":
			ev.Kind = EventStockSplit
			ev.SplitFactor = parsePayloadDecimal(r.Payload, "factor", decimal.NewFromInt(1))
		case "reverse_split":
			ev.Kind = EventReverseSplit
			ev.SplitFactor = parsePayloadDecimal(r.Payload, "factor", decimal.NewFromInt(1))
		case "cash_distribution":
			ev.Kind = EventCashDistribution
			ev.Amount = parsePayloadDecimal(r.Payload, "amount", decimal.Zero)
		case "delisting":
			ev.Kind = EventDelisting
			ev.Amount = parsePayloadDecimal(r.Payload, "cash_total", decimal.Zero)
		default:
			continue
		}
		out = append(out, ev)
	}

	return out, nil
}

// parsePayloadDecimal extracts a decimal field from a corporate-action JSON
// payload; returns dflt on miss.
func parsePayloadDecimal(payload []byte, key string, dflt decimal.Decimal) decimal.Decimal {
	if len(payload) == 0 {
		return dflt
	}
	// Tiny hand-roll to avoid bringing json into hot replay paths; payloads
	// are short and trusted.
	s := string(payload)
	idx := strings.Index(s, "\""+key+"\"")
	if idx < 0 {
		return dflt
	}
	colon := strings.Index(s[idx:], ":")
	if colon < 0 {
		return dflt
	}
	rest := s[idx+colon+1:]
	rest = strings.TrimLeft(rest, " \t\n")
	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		end = len(rest)
	}
	val := strings.TrimSpace(rest[:end])
	val = strings.Trim(val, "\"")
	d, err := decimal.NewFromString(val)
	if err != nil {
		return dflt
	}
	return d
}

// RefreshPosition replays events for (account, instrument) and upserts the
// materialised investment_positions row plus lot/consumption caches. The trade
// and dividend tables remain the source of truth; these rows are rebuilt so
// reporting queries can stay cheap and reproducible.
func (s *Service) RefreshPosition(ctx context.Context, workspaceID, accountID, instrumentID uuid.UUID) error {
	events, err := s.loadEvents(ctx, workspaceID, accountID, instrumentID)
	if err != nil {
		return err
	}

	// Determine the instrument's primary currency for the position row.
	q := dbq.New(s.pool)
	instCurrency, err := q.GetInstrumentCurrency(ctx, instrumentID)
	if err != nil {
		return fmt.Errorf("load instrument currency: %w", err)
	}

	res := ReplayPosition(events)

	if res.Quantity.LessThanOrEqual(decimal.Zero) && len(events) == 0 {
		// Nothing to cache.
		return q.DeleteInvestmentPosition(ctx, dbq.DeleteInvestmentPositionParams{
			WorkspaceID:  workspaceID,
			AccountID:    accountID,
			InstrumentID: instrumentID,
		})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := dbq.New(tx)

	if err := qtx.UpsertInvestmentPosition(ctx, dbq.UpsertInvestmentPositionParams{
		AccountID:    accountID,
		InstrumentID: instrumentID,
		WorkspaceID:  workspaceID,
		Quantity:     decimalToNumeric(res.Quantity),
		AverageCost:  decimalToNumeric(res.AverageCost),
		Currency:     instCurrency,
		RefreshedAt:  s.now().UTC(),
	}); err != nil {
		return fmt.Errorf("upsert investment_position: %w", err)
	}

	// Lots/consumptions cache rebuild. Consumptions depend on lots, so clear in
	// child->parent order and reinsert deterministic rows for both open and
	// closed lots.
	posParams := dbq.DeleteLotConsumptionsForPositionParams{
		WorkspaceID:  workspaceID,
		AccountID:    accountID,
		InstrumentID: instrumentID,
	}
	if err := qtx.DeleteLotConsumptionsForPosition(ctx, posParams); err != nil {
		return fmt.Errorf("clear lot consumptions: %w", err)
	}
	if err := qtx.DeleteLotsForPosition(ctx, dbq.DeleteLotsForPositionParams{
		WorkspaceID:  workspaceID,
		AccountID:    accountID,
		InstrumentID: instrumentID,
	}); err != nil {
		return fmt.Errorf("clear lots: %w", err)
	}

	lotIDsByTrade := make(map[uuid.UUID]uuid.UUID, len(res.Lots))
	for _, lot := range res.Lots {
		lotID := uuidx.New()
		lotIDsByTrade[lot.TradeID] = lotID
		var closedAt *time.Time
		if lot.Remaining.Cmp(quantityEpsilon) <= 0 {
			t := res.LastDate
			if t.IsZero() {
				t = s.now().UTC()
			}
			closedAt = &t
		}
		if err := qtx.InsertInvestmentLot(ctx, dbq.InsertInvestmentLotParams{
			ID:                lotID,
			WorkspaceID:       workspaceID,
			AccountID:         accountID,
			InstrumentID:      instrumentID,
			AcquiredAt:        lot.AcquiredAt,
			QuantityOpening:   decimalToNumeric(lot.Opening),
			QuantityRemaining: decimalToNumeric(lot.Remaining),
			CostBasisPerUnit:  decimalToNumeric(lot.CostBasis),
			Currency:          nilIfZero(lot.Currency, instCurrency),
			SourceTradeID:     &lot.TradeID,
			ClosedAt:          closedAt,
		}); err != nil {
			return fmt.Errorf("insert lot: %w", err)
		}
	}
	for _, c := range res.Consumptions {
		lotID, ok := lotIDsByTrade[c.LotTradeID]
		if !ok {
			continue
		}
		sellTradeID := c.SellTradeID
		if sellTradeID == uuid.Nil {
			// Synthetic closure events have no sell trade and cannot satisfy the
			// non-null FK. Their realised gain is still represented by the closed
			// lots; a dedicated corporate-action consumption cache can follow.
			continue
		}
		if err := qtx.InsertLotConsumption(ctx, dbq.InsertLotConsumptionParams{
			ID:               uuidx.New(),
			WorkspaceID:      workspaceID,
			LotID:            lotID,
			SellTradeID:      sellTradeID,
			QuantityConsumed: decimalToNumeric(c.Quantity),
			RealisedGain:     decimalToNumeric(c.RealisedGain),
			Currency:         nilIfZero(c.Currency, instCurrency),
			ConsumedAt:       c.ConsumedAt,
		}); err != nil {
			return fmt.Errorf("insert lot consumption: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func nilIfZero(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// RefreshAllPositions walks every (account, instrument) pair the workspace
// has ever traded and refreshes the cache. Used by the manual refresh
// endpoint and after bulk imports.
func (s *Service) RefreshAllPositions(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	// Dynamic SQL: UNION across two tables.
	rows, err := s.pool.Query(ctx, `
		select distinct account_id, instrument_id from investment_trades where workspace_id = $1
		union
		select distinct account_id, instrument_id from dividend_events where workspace_id = $1
	`, workspaceID)
	if err != nil {
		return 0, err
	}
	pairs := make([][2]uuid.UUID, 0, 32)
	for rows.Next() {
		var a, i uuid.UUID
		if err := rows.Scan(&a, &i); err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, [2]uuid.UUID{a, i})
	}
	rows.Close()
	for _, p := range pairs {
		if err := s.RefreshPosition(ctx, workspaceID, p[0], p[1]); err != nil {
			return 0, err
		}
	}
	if err := s.refreshLatestPrices(ctx, workspaceID); err != nil {
		return 0, err
	}
	return len(pairs), nil
}

func (s *Service) refreshLatestPrices(ctx context.Context, workspaceID uuid.UUID) error {
	if s.md == nil || !s.md.HasPriceProvider() {
		return nil
	}
	instruments, err := dbq.New(s.pool).ListOpenPositionInstruments(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("load instruments for price refresh: %w", err)
	}
	for _, inst := range instruments {
		// Best-effort: one failing provider call should not block replayed
		// positions from becoming visible.
		_, _ = s.md.LatestPrice(ctx, inst.ID, inst.Symbol)
	}
	return nil
}

// ListPositions returns positions for the workspace augmented with instrument
// metadata. Filter f scopes the result.
func (s *Service) ListPositions(ctx context.Context, workspaceID uuid.UUID, f PositionFilter) ([]Position, error) {
	// Dynamic SQL: conditional WHERE with search, open/closed, account, instrument filters.
	args := []any{workspaceID}
	clauses := []string{"p.workspace_id = $1"}
	next := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	if f.AccountID != nil {
		clauses = append(clauses, "p.account_id = "+next(*f.AccountID))
	}
	if f.InstrumentID != nil {
		clauses = append(clauses, "p.instrument_id = "+next(*f.InstrumentID))
	}
	if f.OpenOnly {
		clauses = append(clauses, "p.quantity > 0")
	}
	if f.ClosedOnly {
		clauses = append(clauses, "p.quantity = 0")
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		needle := "%" + strings.ToLower(s) + "%"
		clauses = append(clauses, "(lower(i.symbol) like "+next(needle)+" or lower(i.name) like "+next(needle)+")")
	}

	q := `
		select
			p.account_id, p.instrument_id, p.workspace_id,
			i.symbol, i.name, i.asset_class::text, i.currency,
			a.currency, a.id,
			p.quantity::text, p.average_cost::text,
			(p.quantity * p.average_cost)::text,
			coalesce(realised.gain, 0)::text,
			coalesce(div.total, 0)::text,
			coalesce(fees.total, 0)::text,
			last_trade.last_date,
			lp.price::text,
			lp.as_of
		from investment_positions p
		join instruments i on i.id = p.instrument_id
		join accounts a on a.id = p.account_id
		left join lateral (
			select coalesce(sum(realised_gain), 0) as gain
			from investment_lot_consumptions c
			where c.workspace_id = p.workspace_id
			  and c.lot_id in (
				select l.id from investment_lots l
				where l.workspace_id = p.workspace_id
				  and l.account_id = p.account_id
				  and l.instrument_id = p.instrument_id
			  )
		) realised on true
		left join lateral (
			select coalesce(sum(total_amount), 0) as total
			from dividend_events d
			where d.workspace_id = p.workspace_id
			  and d.account_id = p.account_id
			  and d.instrument_id = p.instrument_id
		) div on true
		left join lateral (
			select coalesce(sum(fee_amount), 0) as total
			from investment_trades t
			where t.workspace_id = p.workspace_id
			  and t.account_id = p.account_id
			  and t.instrument_id = p.instrument_id
		) fees on true
		left join lateral (
			select max(trade_date) as last_date
			from investment_trades t
			where t.workspace_id = p.workspace_id
			  and t.account_id = p.account_id
			  and t.instrument_id = p.instrument_id
		) last_trade on true
		left join lateral (
			select price, as_of from instrument_prices
			where instrument_id = p.instrument_id
			order by as_of desc
			limit 1
		) lp on true
		where ` + strings.Join(clauses, " and ") + `
		order by (p.quantity * coalesce(lp.price, p.average_cost)) desc, i.symbol
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	defer rows.Close()

	out := make([]Position, 0)
	for rows.Next() {
		var (
			pos             Position
			lastDate        *time.Time
			lastPrice       *string
			lastPriceAt     *time.Time
			accountIDForPos uuid.UUID
		)
		if err := rows.Scan(
			&pos.AccountID, &pos.InstrumentID, &pos.WorkspaceID,
			&pos.Symbol, &pos.Name, &pos.AssetClass, &pos.InstrumentCcy,
			&pos.AccountCurrency, &accountIDForPos,
			&pos.Quantity, &pos.AverageCost, &pos.CostBasisTotal,
			&pos.RealisedPnL, &pos.DividendsReceived, &pos.FeesPaid,
			&lastDate, &lastPrice, &lastPriceAt,
		); err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		pos.LastTradeDate = lastDate
		pos.LastPrice = lastPrice
		pos.LastPriceAt = lastPriceAt
		// Compute market value & unrealised P/L when we have a price.
		if lastPrice != nil {
			lp, _ := decimal.NewFromString(*lastPrice)
			qty, _ := decimal.NewFromString(pos.Quantity)
			mv := lp.Mul(qty)
			cost, _ := decimal.NewFromString(pos.CostBasisTotal)
			mvStr := mv.String()
			unr := mv.Sub(cost).String()
			pos.MarketValue = &mvStr
			pos.UnrealisedPnL = &unr
		}
		out = append(out, pos)
	}
	return out, rows.Err()
}

// GetInstrumentDetail bundles instrument metadata with the workspace's
// trades/dividends/positions for that instrument and a pricing time series.
func (s *Service) GetInstrumentDetail(ctx context.Context, workspaceID, instrumentID uuid.UUID) (*InstrumentDetail, error) {
	inst, err := s.GetInstrument(ctx, instrumentID)
	if err != nil {
		return nil, err
	}
	positions, err := s.ListPositions(ctx, workspaceID, PositionFilter{InstrumentID: &instrumentID})
	if err != nil {
		return nil, err
	}
	trades, err := s.ListTrades(ctx, workspaceID, nil, &instrumentID)
	if err != nil {
		return nil, err
	}
	dividends, err := s.ListDividends(ctx, workspaceID, nil, &instrumentID)
	if err != nil {
		return nil, err
	}

	history, err := s.instrumentValueHistory(ctx, *inst, instrumentID, trades)
	if err != nil {
		return nil, err
	}

	var lastQuote *QuoteSnapshot
	if s.md != nil {
		q, err := s.md.LatestPrice(ctx, instrumentID, inst.Symbol)
		if err == nil {
			stale := s.now().Sub(q.AsOf) > 24*time.Hour
			snap := QuoteSnapshot{
				Price:    q.Price.String(),
				Currency: q.Currency,
				AsOf:     q.AsOf,
				Source:   q.Source,
				Stale:    stale,
			}
			lastQuote = &snap
		}
	}
	return &InstrumentDetail{
		Instrument: *inst,
		Positions:  positions,
		Trades:     trades,
		Dividends:  dividends,
		History:    history,
		LastQuote:  lastQuote,
	}, nil
}

func (s *Service) instrumentValueHistory(ctx context.Context, inst Instrument, instrumentID uuid.UUID, trades []Trade) ([]HistoryDataPoint, error) {
	if len(trades) == 0 {
		return []HistoryDataPoint{}, nil
	}

	ascTrades := append([]Trade(nil), trades...)
	sort.SliceStable(ascTrades, func(i, j int) bool {
		return ascTrades[i].TradeDate.Before(ascTrades[j].TradeDate)
	})

	from := dayUTC(ascTrades[0].TradeDate)
	to := dayUTC(s.now())
	priceMap := map[time.Time]decimal.Decimal{}
	if s.md != nil {
		prices, err := s.md.HistoricalRange(ctx, instrumentID, inst.Symbol, from, to)
		if err != nil && len(prices) == 0 {
			return nil, fmt.Errorf("load historical prices: %w", err)
		}
		for d, q := range prices {
			priceMap[dayUTC(d)] = q.Price
		}
	} else {
		// Cached prices fallback path.
		priceRows, err := dbq.New(s.pool).LookupCachedPriceRange(ctx, dbq.LookupCachedPriceRangeParams{
			InstrumentID: instrumentID,
			FromDate:     from,
			ToDate:       to.Add(24 * time.Hour),
		})
		if err != nil {
			return nil, fmt.Errorf("load cached historical prices: %w", err)
		}
		for _, r := range priceRows {
			priceMap[dayUTC(r.AsOf)] = numericToDecimal(r.Price)
		}
	}

	out := make([]HistoryDataPoint, 0, int(to.Sub(from).Hours()/24)+1)
	tradeIdx := 0
	qty := decimal.Zero
	var lastPrice *decimal.Decimal
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		for tradeIdx < len(ascTrades) && !dayUTC(ascTrades[tradeIdx].TradeDate).After(d) {
			t := ascTrades[tradeIdx]
			tradeQty, _ := decimal.NewFromString(t.Quantity)
			if t.Side == "sell" {
				qty = qty.Sub(tradeQty)
			} else {
				qty = qty.Add(tradeQty)
			}
			tradeIdx++
		}
		if p, ok := priceMap[d]; ok {
			cp := p
			lastPrice = &cp
		}
		point := HistoryDataPoint{Date: d, Quantity: qty.String()}
		if lastPrice != nil {
			price := lastPrice.String()
			value := lastPrice.Mul(qty).String()
			point.Price = &price
			point.Value = &value
		}
		out = append(out, point)
	}
	return out, nil
}

func dayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// GetTrade fetches a single trade by id (used by API delete to assert
// existence cleanly).
func (s *Service) GetTrade(ctx context.Context, workspaceID, tradeID uuid.UUID) (*Trade, error) {
	row, err := dbq.New(s.pool).GetInvestmentTrade(ctx, dbq.GetInvestmentTradeParams{
		WorkspaceID: workspaceID,
		ID:          tradeID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("trade")
		}
		return nil, err
	}
	return &Trade{
		ID: row.ID, WorkspaceID: row.WorkspaceID, AccountID: row.AccountID,
		InstrumentID: row.InstrumentID, Symbol: row.Symbol,
		Side: row.Side, Quantity: row.Quantity, Price: row.Price,
		Currency: row.Currency, FeeAmount: row.FeeAmount, FeeCurrency: row.FeeCurrency,
		TradeDate: row.TradeDate, SettleDate: row.SettleDate,
		LinkedCashTransactionID: row.LinkedCashTransactionID,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}
