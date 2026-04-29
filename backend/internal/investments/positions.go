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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := dbq.New(tx)

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

	if res.Quantity.LessThanOrEqual(decimal.Zero) && len(events) == 0 {
		if err := qtx.DeleteInvestmentPosition(ctx, dbq.DeleteInvestmentPositionParams{
			WorkspaceID:  workspaceID,
			AccountID:    accountID,
			InstrumentID: instrumentID,
		}); err != nil {
			return fmt.Errorf("delete investment_position: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		s.invalidateDashboardHistory(workspaceID)
		return nil
	}

	if err := qtx.UpsertInvestmentPosition(ctx, dbq.UpsertInvestmentPositionParams{
		AccountID:    accountID,
		InstrumentID: instrumentID,
		WorkspaceID:  workspaceID,
		Quantity:     decimalToNumeric(res.Quantity),
		AverageCost:  decimalToNumeric(res.AverageCost),
		RealisedPnl:  decimalToNumeric(res.RealisedPnL),
		Currency:     instCurrency,
		RefreshedAt:  s.now().UTC(),
	}); err != nil {
		return fmt.Errorf("upsert investment_position: %w", err)
	}

	// Lots/consumptions cache rebuild. Consumptions depend on lots, so clear in
	// child->parent order and reinsert deterministic rows for both open and
	// closed lots.
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

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidateDashboardHistory(workspaceID)
	return nil
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
	pairs, err := dbq.New(s.pool).ListTouchedInvestmentPairs(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	for _, p := range pairs {
		if err := s.RefreshPosition(ctx, workspaceID, p.AccountID, p.InstrumentID); err != nil {
			return 0, err
		}
	}
	if err := s.refreshLatestPrices(ctx, workspaceID); err != nil {
		return 0, err
	}
	s.invalidateDashboardHistory(workspaceID)
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
	search := strings.TrimSpace(f.Search)
	searchPattern := ""
	if search != "" {
		searchPattern = "%" + strings.ToLower(search) + "%"
	}
	rows, err := dbq.New(s.pool).ListInvestmentPositions(ctx, dbq.ListInvestmentPositionsParams{
		WorkspaceID:   workspaceID,
		AccountID:     f.AccountID,
		InstrumentID:  f.InstrumentID,
		OpenOnly:      f.OpenOnly,
		ClosedOnly:    f.ClosedOnly,
		Search:        search,
		SearchPattern: searchPattern,
	})
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}

	out := make([]Position, 0, len(rows))
	for _, r := range rows {
		pos := Position{
			AccountID:         r.AccountID,
			InstrumentID:      r.InstrumentID,
			WorkspaceID:       r.WorkspaceID,
			Symbol:            r.Symbol,
			Name:              r.Name,
			AssetClass:        r.AssetClass,
			InstrumentCcy:     r.InstrumentCurrency,
			AccountCurrency:   r.AccountCurrency,
			Quantity:          r.Quantity,
			AverageCost:       r.AverageCost,
			CostBasisTotal:    r.CostBasisTotal,
			RealisedPnL:       r.RealisedPnl,
			DividendsReceived: r.DividendsReceived,
			FeesPaid:          r.FeesPaid,
		}
		pos.LastTradeDate = timeFromSQLC(r.LastTradeDate)
		if lastPrice := stringFromSQLC(r.LastPrice); lastPrice != "" {
			pos.LastPrice = &lastPrice
		}
		if !r.LastPriceAt.IsZero() && r.LastPriceAt.Year() > 1 {
			lastPriceAt := r.LastPriceAt
			pos.LastPriceAt = &lastPriceAt
		}
		if pos.LastPrice != nil {
			lp, _ := decimal.NewFromString(*pos.LastPrice)
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
	return out, nil
}

// GetInstrumentDetail bundles instrument metadata with the workspace's
// trades/dividends/positions for that instrument and a pricing time series.
func (s *Service) GetInstrumentDetail(ctx context.Context, workspaceID, instrumentID uuid.UUID, reportCurrency string) (*InstrumentDetail, error) {
	inst, err := s.GetInstrument(ctx, instrumentID)
	if err != nil {
		return nil, err
	}
	reportCurrency = strings.ToUpper(strings.TrimSpace(reportCurrency))
	if reportCurrency == "" {
		if base, err := dbq.New(s.pool).GetWorkspaceBaseCurrency(ctx, workspaceID); err == nil {
			reportCurrency = base
		}
	}
	if reportCurrency == "" {
		reportCurrency = inst.Currency
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

	corpActions, err := s.ListCorporateActions(ctx, workspaceID, instrumentID)
	if err != nil {
		return nil, err
	}
	history, err := s.instrumentValueHistory(ctx, *inst, instrumentID, trades, corpActions, reportCurrency)
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
		Instrument:     *inst,
		ReportCurrency: reportCurrency,
		Positions:      positions,
		Trades:         trades,
		Dividends:      dividends,
		History:        history,
		LastQuote:      lastQuote,
	}, nil
}

func (s *Service) instrumentValueHistory(ctx context.Context, inst Instrument, instrumentID uuid.UUID, trades []Trade, corpActions []CorporateAction, reportCurrency string) ([]HistoryDataPoint, error) {
	if len(trades) == 0 {
		return []HistoryDataPoint{}, nil
	}

	ascTrades := append([]Trade(nil), trades...)
	sort.SliceStable(ascTrades, func(i, j int) bool {
		return ascTrades[i].TradeDate.Before(ascTrades[j].TradeDate)
	})

	// Sort splits ascending by effective date so we can walk them in lock-step
	// with the daily timeline. Other corporate-action kinds (cash distributions,
	// delistings, symbol changes) don't affect the share-count timeline so we
	// ignore them here.
	type splitEvent struct {
		Date   time.Time
		Factor decimal.Decimal
	}
	splits := make([]splitEvent, 0, len(corpActions))
	for _, ca := range corpActions {
		if ca.Kind != "split" && ca.Kind != "reverse_split" {
			continue
		}
		factor := payloadFactor(ca.Payload)
		if factor.IsZero() {
			continue
		}
		splits = append(splits, splitEvent{Date: dayUTC(ca.EffectiveDate), Factor: factor})
	}
	sort.SliceStable(splits, func(i, j int) bool {
		return splits[i].Date.Before(splits[j].Date)
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

	// Phase 1: walk the timeline forward, applying trades and splits at their
	// effective dates so qty is in time-of-event units.
	type rawPoint struct {
		Date  time.Time
		Qty   decimal.Decimal
		Price *decimal.Decimal
	}
	rawSeries := make([]rawPoint, 0, int(to.Sub(from).Hours()/24)+1)
	tradeIdx := 0
	splitIdx := 0
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
		for splitIdx < len(splits) && !splits[splitIdx].Date.After(d) {
			qty = qty.Mul(splits[splitIdx].Factor)
			splitIdx++
		}
		if p, ok := priceMap[d]; ok {
			cp := p
			lastPrice = &cp
		}
		rp := rawPoint{Date: d, Qty: qty}
		if lastPrice != nil {
			cp := *lastPrice
			rp.Price = &cp
		}
		rawSeries = append(rawSeries, rp)
	}

	// Phase 2: express historical qty in *today's* split-adjusted units. Walk
	// the timeline backward, accumulating a futureFactor of every split whose
	// effective date is strictly after the current point. Multiplying the raw
	// qty by futureFactor gives the qty in today's units, so the chart line is
	// continuous across split boundaries instead of stair-stepping.
	out := make([]HistoryDataPoint, len(rawSeries))
	futureFactor := decimal.NewFromInt(1)
	splitBackIdx := len(splits) - 1
	for i := len(rawSeries) - 1; i >= 0; i-- {
		rp := rawSeries[i]
		for splitBackIdx >= 0 && splits[splitBackIdx].Date.After(rp.Date) {
			futureFactor = futureFactor.Mul(splits[splitBackIdx].Factor)
			splitBackIdx--
		}
		adjQty := rp.Qty.Mul(futureFactor)
		point := HistoryDataPoint{
			Date:           rp.Date,
			Quantity:       adjQty.String(),
			Currency:       reportCurrency,
			NativeCurrency: inst.Currency,
		}
		if rp.Price != nil {
			price := rp.Price.String()
			nativeValue := rp.Price.Mul(adjQty)
			value := nativeValue
			if !strings.EqualFold(inst.Currency, reportCurrency) {
				if fx, err := s.fxOrIdentity(ctx, inst.Currency, reportCurrency, rp.Date); err == nil {
					value = nativeValue.Mul(fx)
				}
			}
			nativeValueStr := nativeValue.String()
			valueStr := value.String()
			point.Price = &price
			point.Value = &valueStr
			point.ValueNative = &nativeValueStr
		}
		out[i] = point
	}
	return out, nil
}

func timeFromSQLC(v any) *time.Time {
	switch t := v.(type) {
	case time.Time:
		return &t
	case *time.Time:
		return t
	default:
		return nil
	}
}

func stringFromSQLC(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

// payloadFactor extracts the "factor" value from a corporate-action payload
// (which may be a JSON object decoded into map[string]any with string or
// numeric values). Returns Zero when missing or unparseable.
func payloadFactor(payload any) decimal.Decimal {
	m, ok := payload.(map[string]any)
	if !ok {
		return decimal.Zero
	}
	v, ok := m["factor"]
	if !ok {
		return decimal.Zero
	}
	switch t := v.(type) {
	case string:
		d, err := decimal.NewFromString(t)
		if err != nil {
			return decimal.Zero
		}
		return d
	case float64:
		return decimal.NewFromFloat(t)
	}
	return decimal.Zero
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
		CreatedAt:               row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}
