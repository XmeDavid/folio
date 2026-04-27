package investments

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// quantityEpsilon is the rounding-noise threshold below which a remaining
// quantity is treated as zero. Brokerage quantities can carry up to 8 dp;
// 1e-9 is conservative.
var quantityEpsilon = decimal.New(1, -9) // 1e-9

// ReplayEvent is the event-sourced input to ReplayPosition. It abstracts away
// the storage layer so the replay engine can be unit-tested without a DB.
type ReplayEvent struct {
	Date         time.Time
	Kind         EventKind
	TradeID      uuid.UUID
	Quantity     decimal.Decimal // always positive
	Price        decimal.Decimal // unit price for trades; ignored for dividends
	Fee          decimal.Decimal // total fee for trades; ignored otherwise
	Amount       decimal.Decimal // dividend total / split factor
	Currency     string
	SplitFactor  decimal.Decimal // split: new shares = old * factor (e.g. 4 for 4-for-1)
	CashPerShare decimal.Decimal // cash distribution per share
}

// EventKind enumerates the canonical event taxonomy used by the replay
// engine. Broker-specific labels are mapped into this set at the import
// boundary; the engine only cares about kind.
type EventKind string

const (
	EventBuy              EventKind = "buy"
	EventSell             EventKind = "sell"
	EventDividend         EventKind = "dividend"
	EventStockSplit       EventKind = "split"
	EventReverseSplit     EventKind = "reverse_split"
	EventCashDistribution EventKind = "cash_distribution"
	EventDelisting        EventKind = "delisting"
	// EventPositionClosure forces the remaining quantity to zero (no proceeds);
	// useful for delisted/dead positions.
	EventPositionClosure EventKind = "position_closure"
)

// ReplayResult captures the materialised state after walking the event
// stream for a single (account, instrument) pair.
type ReplayResult struct {
	Quantity          decimal.Decimal
	AverageCost       decimal.Decimal
	CostBasisTotal    decimal.Decimal
	RealisedPnL       decimal.Decimal
	DividendsReceived decimal.Decimal
	FeesPaid          decimal.Decimal
	LastDate          time.Time
	Lots              []OpenLot
	OpenLots          []OpenLot
	Consumptions      []Consumption
}

// OpenLot is a tax lot that still has shares remaining after replay.
type OpenLot struct {
	TradeID    uuid.UUID
	AcquiredAt time.Time
	Opening    decimal.Decimal
	Remaining  decimal.Decimal
	CostBasis  decimal.Decimal
	Currency   string
}

// Consumption records how a sell trade drew down a buy lot.
type Consumption struct {
	LotTradeID   uuid.UUID
	SellTradeID  uuid.UUID
	Quantity     decimal.Decimal
	RealisedGain decimal.Decimal
	ConsumedAt   time.Time
	Currency     string
	CostPerUnit  decimal.Decimal
	ProceedsUnit decimal.Decimal
}

// ReplayPosition walks events in date-then-trade-id order and produces the
// materialised state. Cost basis follows FIFO. Average cost is derived from
// total cost basis ÷ remaining quantity.
//
// Pure function — no I/O — so it is exhaustively unit-testable.
func ReplayPosition(events []ReplayEvent) ReplayResult {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Date.Equal(events[j].Date) {
			// Same day: buys before sells before dividends/splits keeps cost-basis
			// math intuitive when a user records a same-day cycle.
			return eventOrder(events[i].Kind) < eventOrder(events[j].Kind)
		}
		return events[i].Date.Before(events[j].Date)
	})

	var (
		lots         []OpenLot
		consumptions []Consumption
		realised     = decimal.Zero
		dividends    = decimal.Zero
		fees         = decimal.Zero
		lastDate     time.Time
	)

	for _, ev := range events {
		if ev.Date.After(lastDate) {
			lastDate = ev.Date
		}
		switch ev.Kind {
		case EventBuy:
			gross := ev.Price.Mul(ev.Quantity)
			cost := gross.Add(ev.Fee)
			fees = fees.Add(ev.Fee)
			perUnit := decimal.Zero
			if !ev.Quantity.IsZero() {
				perUnit = cost.Div(ev.Quantity)
			}
			lots = append(lots, OpenLot{
				TradeID:    ev.TradeID,
				AcquiredAt: ev.Date,
				Opening:    ev.Quantity,
				Remaining:  ev.Quantity,
				CostBasis:  perUnit,
				Currency:   ev.Currency,
			})
		case EventSell:
			fees = fees.Add(ev.Fee)
			toSell := ev.Quantity
			grossProceeds := ev.Price.Mul(ev.Quantity).Sub(ev.Fee)
			perUnitProceeds := decimal.Zero
			if !ev.Quantity.IsZero() {
				perUnitProceeds = grossProceeds.Div(ev.Quantity)
			}
			for i := range lots {
				if toSell.Cmp(quantityEpsilon) <= 0 {
					break
				}
				if lots[i].Remaining.Cmp(quantityEpsilon) <= 0 {
					continue
				}
				take := toSell
				if lots[i].Remaining.Cmp(take) < 0 {
					take = lots[i].Remaining
				}
				gain := perUnitProceeds.Sub(lots[i].CostBasis).Mul(take)
				realised = realised.Add(gain)
				consumptions = append(consumptions, Consumption{
					LotTradeID:   lots[i].TradeID,
					SellTradeID:  ev.TradeID,
					Quantity:     take,
					RealisedGain: gain,
					ConsumedAt:   ev.Date,
					Currency:     ev.Currency,
					CostPerUnit:  lots[i].CostBasis,
					ProceedsUnit: perUnitProceeds,
				})
				lots[i].Remaining = lots[i].Remaining.Sub(take)
				toSell = toSell.Sub(take)
			}
			// Any residual sell beyond available lots is treated as a short;
			// for v1 we ignore it (no short modelling yet) and simply log
			// the leftover as realised based on zero cost basis. In practice
			// this only happens when import data is broken.
			if toSell.Cmp(quantityEpsilon) > 0 {
				gain := perUnitProceeds.Mul(toSell)
				realised = realised.Add(gain)
			}
		case EventDividend:
			dividends = dividends.Add(ev.Amount)
		case EventStockSplit:
			factor := ev.SplitFactor
			if factor.IsZero() {
				continue
			}
			for i := range lots {
				lots[i].Remaining = lots[i].Remaining.Mul(factor)
				lots[i].Opening = lots[i].Opening.Mul(factor)
				if !factor.IsZero() {
					lots[i].CostBasis = lots[i].CostBasis.Div(factor)
				}
			}
		case EventReverseSplit:
			factor := ev.SplitFactor
			if factor.IsZero() {
				continue
			}
			for i := range lots {
				lots[i].Remaining = lots[i].Remaining.Mul(factor)
				lots[i].Opening = lots[i].Opening.Mul(factor)
				if !factor.IsZero() {
					lots[i].CostBasis = lots[i].CostBasis.Div(factor)
				}
			}
		case EventCashDistribution:
			realised = realised.Add(ev.Amount)
		case EventPositionClosure, EventDelisting:
			// Force-close: all remaining lots realise zero (or `Amount` if cash
			// was received) against current cost basis.
			cashTotal := ev.Amount
			totalRemaining := decimal.Zero
			for _, l := range lots {
				totalRemaining = totalRemaining.Add(l.Remaining)
			}
			perUnitProceeds := decimal.Zero
			if !totalRemaining.IsZero() {
				perUnitProceeds = cashTotal.Div(totalRemaining)
			}
			for i := range lots {
				if lots[i].Remaining.Cmp(quantityEpsilon) <= 0 {
					continue
				}
				gain := perUnitProceeds.Sub(lots[i].CostBasis).Mul(lots[i].Remaining)
				realised = realised.Add(gain)
				consumptions = append(consumptions, Consumption{
					LotTradeID:   lots[i].TradeID,
					Quantity:     lots[i].Remaining,
					RealisedGain: gain,
					ConsumedAt:   ev.Date,
					Currency:     lots[i].Currency,
					CostPerUnit:  lots[i].CostBasis,
					ProceedsUnit: perUnitProceeds,
				})
				lots[i].Remaining = decimal.Zero
			}
		}
	}

	totalQty := decimal.Zero
	totalCost := decimal.Zero
	openLots := make([]OpenLot, 0, len(lots))
	for _, l := range lots {
		if l.Remaining.Cmp(quantityEpsilon) > 0 {
			totalQty = totalQty.Add(l.Remaining)
			totalCost = totalCost.Add(l.Remaining.Mul(l.CostBasis))
			openLots = append(openLots, l)
		}
	}
	avgCost := decimal.Zero
	if !totalQty.IsZero() {
		avgCost = totalCost.Div(totalQty)
	}
	return ReplayResult{
		Quantity:          totalQty,
		AverageCost:       avgCost,
		CostBasisTotal:    totalCost,
		RealisedPnL:       realised,
		DividendsReceived: dividends,
		FeesPaid:          fees,
		LastDate:          lastDate,
		OpenLots:          openLots,
		Lots:              lots,
		Consumptions:      consumptions,
	}
}

func eventOrder(k EventKind) int {
	switch k {
	case EventBuy:
		return 0
	case EventStockSplit, EventReverseSplit:
		return 1
	case EventSell:
		return 2
	case EventDividend, EventCashDistribution:
		return 3
	case EventDelisting, EventPositionClosure:
		return 4
	default:
		return 5
	}
}
