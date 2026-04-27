package investments

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal {
	v, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return v
}

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// approxEq asserts a == b within an epsilon, friendly to float-like decimals
// produced by division.
func approxEq(t *testing.T, label string, got, want decimal.Decimal) {
	t.Helper()
	diff := got.Sub(want).Abs()
	if diff.GreaterThan(decimal.New(1, -6)) {
		t.Fatalf("%s: got %s, want %s", label, got.String(), want.String())
	}
}

func TestReplay_SingleBuy(t *testing.T) {
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("100"), Fee: d("5"), Currency: "USD"},
	})
	approxEq(t, "qty", res.Quantity, d("10"))
	approxEq(t, "avgCost", res.AverageCost, d("100.5")) // (10*100 + 5) / 10
	approxEq(t, "costBasis", res.CostBasisTotal, d("1005"))
	approxEq(t, "realised", res.RealisedPnL, d("0"))
	approxEq(t, "fees", res.FeesPaid, d("5"))
}

func TestReplay_FIFOPartialSell(t *testing.T) {
	// Two buys at different prices, sell some to consume the FIFO lot.
	buy1 := uuid.New()
	buy2 := uuid.New()
	sell := uuid.New()
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: buy1,
			Quantity: d("10"), Price: d("100"), Currency: "USD"},
		{Date: date("2026-02-15"), Kind: EventBuy, TradeID: buy2,
			Quantity: d("5"), Price: d("120"), Currency: "USD"},
		{Date: date("2026-03-01"), Kind: EventSell, TradeID: sell,
			Quantity: d("8"), Price: d("130"), Currency: "USD"},
	})
	// After selling 8 from FIFO buy1 (10 @ 100): remaining = 2 @ 100 + 5 @ 120.
	approxEq(t, "qty", res.Quantity, d("7"))
	approxEq(t, "costBasis", res.CostBasisTotal, d("800")) // 2*100 + 5*120
	// avg cost = 800 / 7 ≈ 114.285714
	approxEq(t, "avgCost", res.AverageCost, d("114.2857143"))
	// realised = (130 - 100) * 8 = 240
	approxEq(t, "realised", res.RealisedPnL, d("240"))
	// 1 consumption row.
	if len(res.Consumptions) != 1 {
		t.Fatalf("want 1 consumption, got %d", len(res.Consumptions))
	}
	if res.Consumptions[0].LotTradeID != buy1 {
		t.Fatalf("FIFO consumption should hit buy1")
	}
}

func TestReplay_FIFOAcrossLots(t *testing.T) {
	buy1 := uuid.New()
	buy2 := uuid.New()
	sell := uuid.New()
	// Sell more than buy1 has so we straddle two lots.
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: buy1,
			Quantity: d("10"), Price: d("100"), Currency: "USD"},
		{Date: date("2026-02-15"), Kind: EventBuy, TradeID: buy2,
			Quantity: d("5"), Price: d("120"), Currency: "USD"},
		{Date: date("2026-03-01"), Kind: EventSell, TradeID: sell,
			Quantity: d("12"), Price: d("130"), Currency: "USD"},
	})
	// Remaining: 0 from buy1, 3 from buy2 @ 120 = cost 360, qty 3.
	approxEq(t, "qty", res.Quantity, d("3"))
	approxEq(t, "costBasis", res.CostBasisTotal, d("360"))
	// realised = (130-100)*10 + (130-120)*2 = 300 + 20 = 320.
	approxEq(t, "realised", res.RealisedPnL, d("320"))
	if len(res.Consumptions) != 2 {
		t.Fatalf("want 2 consumption rows, got %d", len(res.Consumptions))
	}
}

func TestReplay_DividendsAccrueButDontTouchQuantity(t *testing.T) {
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("100"), Price: d("50"), Currency: "USD"},
		{Date: date("2026-04-01"), Kind: EventDividend, Amount: d("75"), Currency: "USD"},
	})
	approxEq(t, "qty", res.Quantity, d("100"))
	approxEq(t, "dividends", res.DividendsReceived, d("75"))
	approxEq(t, "realised", res.RealisedPnL, d("0"))
}

func TestReplay_ForwardSplit(t *testing.T) {
	// 4-for-1 split should multiply quantity, divide cost basis per unit.
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("400"), Currency: "USD"},
		{Date: date("2026-02-01"), Kind: EventStockSplit, SplitFactor: d("4"), Currency: "USD"},
	})
	approxEq(t, "qty", res.Quantity, d("40"))
	approxEq(t, "avgCost", res.AverageCost, d("100"))
	approxEq(t, "costBasis", res.CostBasisTotal, d("4000"))
}

func TestReplay_PositionClosure_DelistedAtZero(t *testing.T) {
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("50"), Currency: "USD"},
		{Date: date("2026-08-06"), Kind: EventDelisting, Amount: d("0"), Currency: "USD"},
	})
	approxEq(t, "qty", res.Quantity, d("0"))
	// Realised = -10 * 50 = -500.
	approxEq(t, "realised", res.RealisedPnL, d("-500"))
}

func TestReplay_PositionClosure_WithCashDistribution(t *testing.T) {
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("50"), Currency: "USD"},
		// Received $23 per share cash on closure.
		{Date: date("2026-08-06"), Kind: EventDelisting, Amount: d("230"), Currency: "USD"},
	})
	approxEq(t, "qty", res.Quantity, d("0"))
	// Per-share proceeds = 230/10 = 23. Realised = (23-50)*10 = -270.
	approxEq(t, "realised", res.RealisedPnL, d("-270"))
}

func TestReplay_FullExitRestoresZeroPosition(t *testing.T) {
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("100"), Currency: "USD"},
		{Date: date("2026-03-01"), Kind: EventSell, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("110"), Currency: "USD"},
	})
	approxEq(t, "qty", res.Quantity, d("0"))
	approxEq(t, "avgCost", res.AverageCost, d("0"))
	approxEq(t, "costBasis", res.CostBasisTotal, d("0"))
	approxEq(t, "realised", res.RealisedPnL, d("100"))
	if len(res.OpenLots) != 0 {
		t.Fatalf("expected no open lots after full exit")
	}
}

func TestReplay_FeesAccumulateAcrossSides(t *testing.T) {
	res := ReplayPosition([]ReplayEvent{
		{Date: date("2026-01-10"), Kind: EventBuy, TradeID: uuid.New(),
			Quantity: d("10"), Price: d("100"), Fee: d("3"), Currency: "USD"},
		{Date: date("2026-03-01"), Kind: EventSell, TradeID: uuid.New(),
			Quantity: d("4"), Price: d("110"), Fee: d("2"), Currency: "USD"},
	})
	approxEq(t, "fees", res.FeesPaid, d("5"))
	approxEq(t, "qty", res.Quantity, d("6"))
}

// Sanity: domain helper exposes decimal.Zero.
func TestDecimalZeroHelper(t *testing.T) {
	if !fpDecimalZero().Equal(decimal.Zero) {
		t.Fatalf("expected zero")
	}
}
