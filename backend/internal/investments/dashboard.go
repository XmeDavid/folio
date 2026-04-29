package investments

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/marketdata"
)

// DashboardFilter scopes a dashboard summary build.
type DashboardFilter struct {
	AccountID      *uuid.UUID
	ReportCurrency string // e.g. "CHF"
}

// DashboardHistoryFilter scopes the portfolio value history.
type DashboardHistoryFilter struct {
	AccountID      *uuid.UUID
	ReportCurrency string
	Range          string
}

// BuildDashboardSummary computes the headline numbers for the investment
// dashboard, converting all per-position figures into ReportCurrency at the
// most recent FX rate. Historical FX-by-trade-date is used for cost-basis to
// honour the spec's "FX rate for that valuation date" rule (§11) — we apply
// the FX rate at trade time for the cost basis component, and current FX for
// the market-value component, so FX impact is honestly visible on a hold.
//
// The trade-date FX would ideally be applied per-trade then aggregated; for
// v1 we use the position's last-trade-date FX as a single per-position
// approximation, with a note in the warnings field. Fully per-trade FX
// attribution lands once daily valuation snapshots are introduced.
func (s *Service) BuildDashboardSummary(ctx context.Context, workspaceID uuid.UUID, f DashboardFilter) (*DashboardSummary, error) {
	report := strings.ToUpper(strings.TrimSpace(f.ReportCurrency))
	if report == "" {
		// Fall back to the workspace base currency.
		if base, err := dbq.New(s.pool).GetWorkspaceBaseCurrency(ctx, workspaceID); err == nil {
			report = base
		}
	}
	if report == "" {
		report = "CHF"
	}

	// Best-effort: prefetch latest quotes for every open position before the
	// list query reads from instrument_prices. Stale rows older than 1h are
	// refreshed; fresh rows are left alone. Failures are swallowed so a Yahoo
	// hiccup never blanks the dashboard.
	_, _ = s.PrefetchPrices(ctx, workspaceID, time.Hour)

	positions, err := s.ListPositions(ctx, workspaceID, PositionFilter{AccountID: f.AccountID, OpenOnly: false})
	if err != nil {
		return nil, err
	}

	now := s.now()
	holdings := make([]Holding, 0, len(positions))
	warnings := make([]string, 0)

	totalMV := decimal.Zero
	totalCost := decimal.Zero
	totalUnreal := decimal.Zero
	totalReal := decimal.Zero
	totalDiv := decimal.Zero
	totalFees := decimal.Zero
	stale := 0
	missing := 0
	open := 0

	allocByCcy := map[string]decimal.Decimal{}
	allocByAccount := map[uuid.UUID]decimal.Decimal{}
	allocByAssetClass := map[string]decimal.Decimal{}

	for _, p := range positions {
		// FX rate for cost components: ideally per-trade, but for the dashboard
		// we use trade-date for the position (the latest trade date provides a
		// stable, explainable single rate per position).
		fxAsOf := now
		if p.LastTradeDate != nil {
			fxAsOf = *p.LastTradeDate
		}

		costFXRate, costErr := s.fxOrIdentity(ctx, p.InstrumentCcy, report, fxAsOf)
		marketFXRate, mvErr := s.fxOrIdentity(ctx, p.InstrumentCcy, report, now)
		if costErr != nil || mvErr != nil {
			warnings = append(warnings, fmt.Sprintf("FX %s→%s unavailable; using identity for %s", p.InstrumentCcy, report, p.Symbol))
		}

		costNative, _ := decimal.NewFromString(p.CostBasisTotal)
		realNative, _ := decimal.NewFromString(p.RealisedPnL)
		divNative, _ := decimal.NewFromString(p.DividendsReceived)
		feeNative, _ := decimal.NewFromString(p.FeesPaid)
		costReport := costNative.Mul(costFXRate)
		realReport := realNative.Mul(costFXRate)
		divReport := divNative.Mul(costFXRate)
		feeReport := feeNative.Mul(costFXRate)

		var mvReport *string
		var unrealReport *string
		var totalReturnReport *string
		var totalReturnPct *string
		hasMV := false
		if p.MarketValue != nil {
			mv, _ := decimal.NewFromString(*p.MarketValue)
			mvR := mv.Mul(marketFXRate)
			unrR := mvR.Sub(costReport)
			mvStr := mvR.String()
			unrStr := unrR.String()
			mvReport = &mvStr
			unrealReport = &unrStr
			totalMV = totalMV.Add(mvR)
			totalUnreal = totalUnreal.Add(unrR)
			tr := unrR.Add(realReport).Add(divReport).Sub(feeReport)
			trStr := tr.String()
			totalReturnReport = &trStr
			if !costReport.IsZero() {
				pct := tr.Div(costReport).Mul(decimal.NewFromInt(100))
				pctStr := pct.StringFixed(2)
				totalReturnPct = &pctStr
			}
			hasMV = true
			if p.LastPriceAt != nil && now.Sub(*p.LastPriceAt) > 24*time.Hour {
				stale++
			}
		} else {
			qty, _ := decimal.NewFromString(p.Quantity)
			if qty.GreaterThan(decimal.Zero) {
				missing++
			}
		}

		qty, _ := decimal.NewFromString(p.Quantity)
		if qty.GreaterThan(decimal.Zero) {
			open++
			if hasMV {
				mv, _ := decimal.NewFromString(*mvReport)
				allocByCcy[p.InstrumentCcy] = allocByCcy[p.InstrumentCcy].Add(mv)
				allocByAccount[p.AccountID] = allocByAccount[p.AccountID].Add(mv)
				allocByAssetClass[p.AssetClass] = allocByAssetClass[p.AssetClass].Add(mv)
			}
		}

		totalCost = totalCost.Add(costReport)
		totalReal = totalReal.Add(realReport)
		totalDiv = totalDiv.Add(divReport)
		totalFees = totalFees.Add(feeReport)

		holdings = append(holdings, Holding{
			Position:                 p,
			ReportCurrency:           report,
			FXRate:                   marketFXRate.StringFixed(6),
			MarketValueReport:        mvReport,
			CostBasisReport:          costReport.StringFixed(2),
			UnrealisedPnLReport:      unrealReport,
			RealisedPnLReport:        realReport.StringFixed(2),
			DividendsReport:          divReport.StringFixed(2),
			FeesReport:               feeReport.StringFixed(2),
			TotalReturnReport:        totalReturnReport,
			TotalReturnPercentReport: totalReturnPct,
		})
	}

	// Sort holdings by absolute market value desc.
	sort.Slice(holdings, func(i, j int) bool {
		mvA := holdingMV(holdings[i])
		mvB := holdingMV(holdings[j])
		return mvA.Abs().GreaterThan(mvB.Abs())
	})

	// Movers: largest absolute unrealised P/L (top 5).
	movers := make([]HoldingMover, 0, 5)
	for _, h := range holdings {
		if h.UnrealisedPnLReport == nil {
			continue
		}
		unr, _ := decimal.NewFromString(*h.UnrealisedPnLReport)
		if unr.IsZero() {
			continue
		}
		pct := decimal.Zero
		costR, _ := decimal.NewFromString(h.CostBasisReport)
		if !costR.IsZero() {
			pct = unr.Div(costR).Mul(decimal.NewFromInt(100))
		}
		movers = append(movers, HoldingMover{
			Symbol:         h.Symbol,
			Name:           h.Name,
			UnrealisedPnL:  unr.StringFixed(2),
			UnrealisedPct:  pct.StringFixed(2),
			ReportCurrency: report,
		})
	}
	sort.Slice(movers, func(i, j int) bool {
		ai, _ := decimal.NewFromString(movers[i].UnrealisedPnL)
		aj, _ := decimal.NewFromString(movers[j].UnrealisedPnL)
		return ai.Abs().GreaterThan(aj.Abs())
	})
	if len(movers) > 5 {
		movers = movers[:5]
	}

	totalReturn := totalUnreal.Add(totalReal).Add(totalDiv).Sub(totalFees)
	totalReturnPct := decimal.Zero
	totalUnrealPct := decimal.Zero
	if !totalCost.IsZero() {
		totalReturnPct = totalReturn.Div(totalCost).Mul(decimal.NewFromInt(100))
		totalUnrealPct = totalUnreal.Div(totalCost).Mul(decimal.NewFromInt(100))
	}

	allocCcy := allocationsFromMap(allocByCcy, totalMV, func(k string) string { return k })
	allocAcct := allocationsFromMap(allocByAccount, totalMV, func(k uuid.UUID) string { return k.String() })
	allocClass := allocationsFromMap(allocByAssetClass, totalMV, func(k string) string { return k })

	return &DashboardSummary{
		ReportCurrency:         report,
		GeneratedAt:            now,
		TotalMarketValue:       totalMV.StringFixed(2),
		TotalCostBasis:         totalCost.StringFixed(2),
		TotalUnrealisedPnL:     totalUnreal.StringFixed(2),
		TotalUnrealisedPnLPct:  totalUnrealPct.StringFixed(2),
		TotalRealisedPnL:       totalReal.StringFixed(2),
		TotalDividends:         totalDiv.StringFixed(2),
		TotalFees:              totalFees.StringFixed(2),
		TotalReturn:            totalReturn.StringFixed(2),
		TotalReturnPct:         totalReturnPct.StringFixed(2),
		OpenPositionsCount:     open,
		StaleQuotes:            stale,
		MissingQuotes:          missing,
		Holdings:               holdings,
		AllocationByCurrency:   allocCcy,
		AllocationByAccount:    allocAcct,
		AllocationByAssetClass: allocClass,
		TopMovers:              movers,
		Warnings:               dedupe(warnings),
	}, nil
}

// BuildDashboardHistory aggregates instrument value histories into a portfolio
// line in the requested reporting currency. It replays the investment event
// stream, so buys/sells and split corporate actions affect the time series.
func (s *Service) BuildDashboardHistory(ctx context.Context, workspaceID uuid.UUID, f DashboardHistoryFilter) ([]PortfolioHistoryPoint, error) {
	report := strings.ToUpper(strings.TrimSpace(f.ReportCurrency))
	if report == "" {
		if base, err := dbq.New(s.pool).GetWorkspaceBaseCurrency(ctx, workspaceID); err == nil {
			report = base
		}
	}
	if report == "" {
		report = "CHF"
	}

	from := historyRangeStart(s.now(), f.Range)
	positions, err := s.ListPositions(ctx, workspaceID, PositionFilter{AccountID: f.AccountID, OpenOnly: false})
	if err != nil {
		return nil, err
	}

	instruments := map[uuid.UUID]Instrument{}
	for _, p := range positions {
		if _, ok := instruments[p.InstrumentID]; ok {
			continue
		}
		instruments[p.InstrumentID] = Instrument{
			ID:         p.InstrumentID,
			Symbol:     p.Symbol,
			Name:       p.Name,
			AssetClass: p.AssetClass,
			Currency:   p.InstrumentCcy,
			Active:     true,
		}
	}

	byDate := map[time.Time]decimal.Decimal{}
	for instrumentID, inst := range instruments {
		trades, err := s.ListTrades(ctx, workspaceID, f.AccountID, &instrumentID)
		if err != nil {
			return nil, err
		}
		if len(trades) == 0 {
			continue
		}
		corpActions, err := s.ListCorporateActions(ctx, workspaceID, instrumentID)
		if err != nil {
			return nil, err
		}
		history, err := s.instrumentValueHistory(ctx, inst, instrumentID, trades, corpActions, report)
		if err != nil {
			return nil, err
		}
		for _, point := range history {
			d := dayUTC(point.Date)
			if d.Before(from) || point.Value == nil {
				continue
			}
			value, err := decimal.NewFromString(*point.Value)
			if err != nil {
				continue
			}
			byDate[d] = byDate[d].Add(value)
		}
	}

	dates := make([]time.Time, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })

	out := make([]PortfolioHistoryPoint, 0, len(dates))
	for _, d := range dates {
		out = append(out, PortfolioHistoryPoint{
			Date:           d,
			Value:          byDate[d].StringFixed(2),
			ReportCurrency: report,
		})
	}
	return out, nil
}

func historyRangeStart(now time.Time, raw string) time.Time {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "1W":
		return dayUTC(now.AddDate(0, 0, -7))
	case "3M":
		return dayUTC(now.AddDate(0, -3, 0))
	case "6M":
		return dayUTC(now.AddDate(0, -6, 0))
	case "YTD":
		n := now.UTC()
		return time.Date(n.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	case "1Y":
		return dayUTC(now.AddDate(-1, 0, 0))
	case "ALL":
		return time.Time{}
	default:
		return dayUTC(now.AddDate(0, -1, 0))
	}
}

// fxOrIdentity returns FX rate for from->to at the given time, falling back
// to identity (1) when no rate can be obtained (logs a warning via the
// caller).
func (s *Service) fxOrIdentity(ctx context.Context, from, to string, asOf time.Time) (decimal.Decimal, error) {
	if strings.EqualFold(from, to) {
		return decimal.NewFromInt(1), nil
	}
	if s.md == nil {
		return decimal.NewFromInt(1), marketdata.ErrNotAvailable
	}
	r, err := s.md.FXRate(ctx, from, to, asOf)
	if err != nil {
		return decimal.NewFromInt(1), err
	}
	return r, nil
}

func holdingMV(h Holding) decimal.Decimal {
	if h.MarketValueReport == nil {
		return decimal.Zero
	}
	v, _ := decimal.NewFromString(*h.MarketValueReport)
	return v
}

func allocationsFromMap[K comparable](m map[K]decimal.Decimal, total decimal.Decimal, label func(K) string) []AllocationSlice {
	out := make([]AllocationSlice, 0, len(m))
	for k, v := range m {
		pct := decimal.Zero
		if !total.IsZero() {
			pct = v.Div(total).Mul(decimal.NewFromInt(100))
		}
		out = append(out, AllocationSlice{
			Key:   fmt.Sprintf("%v", k),
			Label: label(k),
			Value: v.StringFixed(2),
			Pct:   pct.StringFixed(2),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := decimal.NewFromString(out[i].Value)
		b, _ := decimal.NewFromString(out[j].Value)
		return a.GreaterThan(b)
	})
	return out
}

func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
