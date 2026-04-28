package revolut

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseTradingCSV_BuySell(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-08-01T10:30:00.000Z,AAPL,BUY - MARKET,10,USD 200.00,USD 2000.05,USD,1.0
2025-09-15T14:22:00.000Z,AAPL,SELL - LIMIT,4,USD 220.00,USD 879.95,USD,1.0
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d", len(res.Events))
	}
	buy := res.Events[0]
	if buy.TradeSide != "buy" || buy.Symbol != "AAPL" || buy.Currency != "USD" {
		t.Fatalf("buy: %+v", buy)
	}
	// Implicit fee = 2000.05 - 10*200 = 0.05
	wantFee := decimal.NewFromFloat(0.05)
	if buy.Fee.Sub(wantFee).Abs().GreaterThan(decimal.New(1, -6)) {
		t.Fatalf("buy fee = %s, want ~0.05", buy.Fee.String())
	}
	sell := res.Events[1]
	if sell.TradeSide != "sell" {
		t.Fatalf("sell side = %s", sell.TradeSide)
	}
	// Implicit fee = 4*220 - 879.95 = 0.05
	if sell.Fee.Sub(wantFee).Abs().GreaterThan(decimal.New(1, -6)) {
		t.Fatalf("sell fee = %s, want ~0.05", sell.Fee.String())
	}
}

func TestParseTradingCSV_Dividend(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-10-01T00:00:00.000Z,AAPL,DIVIDEND,10,,USD 8.50,USD,
2025-10-01T00:00:00.000Z,AAPL,DIVIDEND TAX (CORRECTION),,,USD -1.27,USD,
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(res.Events), res.Events)
	}
	div := res.Events[0]
	if div.Kind != "dividend" || div.AmountTotal.String() != "8.5" {
		t.Fatalf("dividend: %+v", div)
	}
	tax := res.Events[1]
	if tax.TaxWithheld.String() != "1.27" {
		t.Fatalf("tax: %+v", tax)
	}
}

// TestParseTradingCSV_StockSplit_SameTicker covers a forward split that
// keeps the ticker and arrives as a positive quantity delta.
func TestParseTradingCSV_StockSplit_SameTicker(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-10T10:00:00.000Z,AAPL,BUY - MARKET,10,USD 100.00,USD 1000.00,USD,1.0
2025-02-01T00:00:00.000Z,AAPL,STOCK SPLIT,30,,USD 0,USD,1.0
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(res.Events), res.Events)
	}
	split := res.Events[1]
	if split.Symbol != "AAPL" || split.TradeSide != "buy" || split.Quantity.String() != "30" {
		t.Fatalf("split: %+v", split)
	}
	if !split.Price.IsZero() {
		t.Fatalf("synthetic split trade should have price 0, got %s", split.Price.String())
	}
}

// TestParseTradingCSV_StockSplit_RenameAndReverse mirrors the failing real
// import: a position is built up under ticker EXE, then a reverse split row
// arrives under the renamed ticker CHKAQ with delta -198. The parser should
// retroactively rebrand the prior EXE buys to CHKAQ so the synthetic
// adjustment lines up with the position it consolidates, leaving the small
// post-split EXE buys untouched.
func TestParseTradingCSV_StockSplit_RenameAndReverse(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2019-11-20T20:13:21.952437Z,EXE,BUY - MARKET,50,USD 0.57,USD 28.71,USD,1.1075
2020-01-30T19:31:27.020863Z,EXE,BUY - MARKET,50,USD 0.51,USD 25.66,USD,1.1036
2020-02-18T18:29:08.466521Z,EXE,BUY - MARKET,15,USD 0.43,USD 6.46,USD,1.0806
2020-02-27T16:10:53.795564Z,EXE,BUY - MARKET,35,USD 0.26,USD 9.26,USD,1.0982
2020-04-08T18:44:49.222593Z,EXE,BUY - MARKET,7,USD 0.16,USD 1.14,USD,1.0873
2020-04-14T14:11:50.149506Z,EXE,BUY - MARKET,43,USD 0.14,USD 6.15,USD,1.0969
2020-04-15T10:12:48.680427Z,CHKAQ,STOCK SPLIT,-198,,USD 0,USD,1.0935
2020-05-01T15:33:33.058219Z,EXE,BUY - MARKET,1,USD 14.96,USD 14.96,USD,1.1010
2020-05-12T14:54:46.921989Z,EXE,BUY - MARKET,0.5,USD 10.74,USD 5.37,USD,1.0870
2020-07-09T20:13:37.225Z,CHKAQ,SELL - MARKET,2,USD 8.46,USD 16.90,USD,1.1284
2020-07-09T20:14:11.847Z,CHKAQ,SELL - MARKET,0.5,USD 8.46,USD 4.22,USD,1.1284
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}

	bySym := map[string]decimal.Decimal{}
	for _, ev := range res.Events {
		if ev.Kind != "trade" {
			continue
		}
		q := ev.Quantity
		if ev.TradeSide == "sell" {
			q = q.Neg()
		}
		bySym[ev.Symbol] = bySym[ev.Symbol].Add(q)
	}
	// The pre-split EXE buys should have been rebranded to CHKAQ:
	// 200 (rebranded buys) - 198 (split) - 2.5 (sells) = -0.5 short.
	wantCHKAQ := decimal.NewFromFloat(-0.5)
	if bySym["CHKAQ"].Sub(wantCHKAQ).Abs().GreaterThan(decimal.New(1, -6)) {
		t.Fatalf("CHKAQ net qty = %s, want %s", bySym["CHKAQ"].String(), wantCHKAQ.String())
	}
	// Post-split EXE buys are kept under EXE: 1 + 0.5 = 1.5.
	wantEXE := decimal.NewFromFloat(1.5)
	if bySym["EXE"].Sub(wantEXE).Abs().GreaterThan(decimal.New(1, -6)) {
		t.Fatalf("EXE net qty = %s, want %s", bySym["EXE"].String(), wantEXE.String())
	}
}

// TestParseTradingCSV_StockSplit_NoFalseRename guards against the rename
// heuristic firing when no prior position is anywhere near the split delta:
// the synthetic adjustment must stay on the row's own ticker.
func TestParseTradingCSV_StockSplit_NoFalseRename(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-01T00:00:00.000Z,AAPL,BUY - MARKET,10,USD 100.00,USD 1000.00,USD,1.0
2025-02-01T00:00:00.000Z,XYZ,STOCK SPLIT,-50,,USD 0,USD,1.0
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d", len(res.Events))
	}
	if res.Events[0].Symbol != "AAPL" {
		t.Fatalf("AAPL buy must not be rebranded: %+v", res.Events[0])
	}
	if res.Events[1].Symbol != "XYZ" || res.Events[1].TradeSide != "sell" {
		t.Fatalf("split trade should stay on XYZ as a sell: %+v", res.Events[1])
	}
}

// TestParseTradingCSV_PositionClosure_ClosesRunningQty verifies that a
// POSITION CLOSURE row with empty quantity but a cash credit is materialised
// as a synthetic SELL of the held quantity, with a per-unit price derived
// from the credit.
func TestParseTradingCSV_PositionClosure_ClosesRunningQty(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-10T10:00:00.000Z,GME.WS,BUY - MARKET,4,USD 1.00,USD 4.00,USD,1.0
2025-10-16T12:43:17.077719Z,GME.WS,POSITION CLOSURE,,,USD 8.63,USD,1.1680
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(res.Events), res.Events)
	}
	closure := res.Events[1]
	if closure.TradeSide != "sell" || closure.Symbol != "GME.WS" {
		t.Fatalf("closure: %+v", closure)
	}
	if closure.Quantity.String() != "4" {
		t.Fatalf("closure qty = %s, want 4", closure.Quantity.String())
	}
	wantPrice := decimal.NewFromFloat(2.1575) // 8.63 / 4
	if closure.Price.Sub(wantPrice).Abs().GreaterThan(decimal.New(1, -6)) {
		t.Fatalf("closure price = %s, want %s", closure.Price.String(), wantPrice.String())
	}
}

// TestParseTradingCSV_PositionClosure_NoHoldingsSkipped guards the case
// where the broker emits a closure for a ticker the parser has never seen
// (partial CSV import). Without a held quantity to consume, we skip the row
// rather than insert a phantom sell.
func TestParseTradingCSV_PositionClosure_NoHoldingsSkipped(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-10-16T12:43:17.077719Z,GME.WS,POSITION CLOSURE,,,USD 8.63,USD,1.1680
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 0 {
		t.Fatalf("want 0 events, got %d: %+v", len(res.Events), res.Events)
	}
}

// TestParseTradingCSV_TransferIn_AddsShares confirms that a ticker-bearing
// TRANSFER FROM ... row with positive quantity emits a synthetic BUY at
// price 0, so the receiving account picks up the moved shares.
func TestParseTradingCSV_TransferIn_AddsShares(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2023-09-09T09:51:59.189976Z,GME,TRANSFER FROM REVOLUT TRADING LTD TO REVOLUT SECURITIES EUROPE UAB,32.5,,USD 0,USD,1.0719
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.TradeSide != "buy" || ev.Symbol != "GME" || ev.Quantity.String() != "32.5" {
		t.Fatalf("transfer: %+v", ev)
	}
	if !ev.Price.IsZero() {
		t.Fatalf("transfer price should be 0, got %s", ev.Price.String())
	}
}

// TestParseTradingCSV_TransferOut_RemovesShares mirrors the sending side of
// a between-entity move: a negative quantity becomes a synthetic SELL at
// price 0 so the source account's position drops.
func TestParseTradingCSV_TransferOut_RemovesShares(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2023-09-09T09:51:59.189976Z,GME,TRANSFER FROM REVOLUT TRADING LTD TO REVOLUT SECURITIES EUROPE UAB,-32.5,,USD 0,USD,1.0719
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.TradeSide != "sell" || ev.Symbol != "GME" || ev.Quantity.String() != "32.5" {
		t.Fatalf("transfer: %+v", ev)
	}
}

// TestParseTradingCSV_CashTransferSkipped keeps the existing behaviour of
// dropping no-ticker cash-transfer rows now that the type prefix is matched.
func TestParseTradingCSV_CashTransferSkipped(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2023-09-09T08:13:21.886316Z,,TRANSFER FROM REVOLUT BANK UAB TO REVOLUT SECURITIES EUROPE UAB,,,USD 4.27,USD,1.0719
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 0 {
		t.Fatalf("cash transfer must not emit events, got %+v", res.Events)
	}
}

// TestParseTradingCSV_Merger_StockForStock_TransfersCostBasis covers the
// canonical paired MERGER - STOCK rows from a stock-for-stock deal: the
// old ticker (PARAA) is closed at average cost so realized P&L on the
// merger is zero, and the new ticker (PSKY) opens with the prior cost
// basis carried over.
func TestParseTradingCSV_Merger_StockForStock_TransfersCostBasis(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-10T10:00:00.000Z,PARAA,BUY - MARKET,7,USD 10.00,USD 70.00,USD,1.0
2025-08-08T13:52:54.198654Z,PSKY,MERGER - STOCK,10.733331,,USD 0,USD,1.1676
2025-08-08T13:52:54.296206Z,PARAA,MERGER - STOCK,-7,,USD 0,USD,1.1676
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(res.Events), res.Events)
	}

	var paraaSell, pskyBuy bool
	for _, ev := range res.Events {
		if ev.Symbol == "PARAA" && ev.TradeSide == "sell" {
			paraaSell = true
			if ev.Quantity.String() != "7" {
				t.Fatalf("PARAA sell qty = %s, want 7", ev.Quantity.String())
			}
			// Average cost = 70 / 7 = 10
			want := decimal.NewFromInt(10)
			if ev.Price.Sub(want).Abs().GreaterThan(decimal.New(1, -6)) {
				t.Fatalf("PARAA sell price = %s, want 10", ev.Price.String())
			}
		}
		if ev.Symbol == "PSKY" && ev.TradeSide == "buy" {
			pskyBuy = true
			if ev.Quantity.String() != "10.733331" {
				t.Fatalf("PSKY buy qty = %s", ev.Quantity.String())
			}
			// Cost transferred = 70; per-unit = 70 / 10.733331
			want := decimal.NewFromInt(70).Div(decimal.NewFromFloat(10.733331))
			if ev.Price.Sub(want).Abs().GreaterThan(decimal.New(1, -6)) {
				t.Fatalf("PSKY buy price = %s, want %s", ev.Price.String(), want.String())
			}
		}
	}
	if !paraaSell || !pskyBuy {
		t.Fatalf("expected paired sell+buy, got %+v", res.Events)
	}
}

// TestParseTradingCSV_Merger_PairOrderIndependent verifies that the merger
// pair is recognised regardless of which side appears first in the file.
func TestParseTradingCSV_Merger_PairOrderIndependent(t *testing.T) {
	// Negative side first.
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-10T10:00:00.000Z,PARAA,BUY - MARKET,7,USD 10.00,USD 70.00,USD,1.0
2025-08-08T13:52:54.100Z,PARAA,MERGER - STOCK,-7,,USD 0,USD,1.1676
2025-08-08T13:52:54.200Z,PSKY,MERGER - STOCK,10.733331,,USD 0,USD,1.1676
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("want 3 events, got %d", len(res.Events))
	}
	var sawSell, sawBuy bool
	for _, ev := range res.Events {
		if ev.Symbol == "PARAA" && ev.TradeSide == "sell" && !ev.Price.IsZero() {
			sawSell = true
		}
		if ev.Symbol == "PSKY" && ev.TradeSide == "buy" && !ev.Price.IsZero() {
			sawBuy = true
		}
	}
	if !sawSell || !sawBuy {
		t.Fatalf("pairing should be order-independent: %+v", res.Events)
	}
}

// TestParseTradingCSV_Merger_NoPriorPosition handles a partial-CSV import
// where the old ticker was never bought in this file. The pair still
// matches but transfers no cost basis — both legs price at 0 so quantity
// at least tracks.
func TestParseTradingCSV_Merger_NoPriorPosition(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-08-08T13:52:54.198654Z,PSKY,MERGER - STOCK,10.733331,,USD 0,USD,1.1676
2025-08-08T13:52:54.296206Z,PARAA,MERGER - STOCK,-7,,USD 0,USD,1.1676
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d", len(res.Events))
	}
	for _, ev := range res.Events {
		if !ev.Price.IsZero() {
			t.Fatalf("no prior cost basis -> price 0, got %+v", ev)
		}
	}
}

// TestParseTradingCSV_Merger_TimestampsTooFarApart guards the pairing
// window: rows on different days do not pair, even if they have opposite
// signs and look like a deal. Each falls back to a price-0 synthetic
// trade.
func TestParseTradingCSV_Merger_TimestampsTooFarApart(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-10T10:00:00.000Z,PARAA,BUY - MARKET,7,USD 10.00,USD 70.00,USD,1.0
2025-08-08T13:52:54.198654Z,PSKY,MERGER - STOCK,10.733331,,USD 0,USD,1.1676
2025-08-09T13:52:54.296206Z,PARAA,MERGER - STOCK,-7,,USD 0,USD,1.1676
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	// Initial buy + two unpaired fallback trades.
	if len(res.Events) != 3 {
		t.Fatalf("want 3 events, got %d", len(res.Events))
	}
	for _, ev := range res.Events {
		if ev.Symbol == "PSKY" && !ev.Price.IsZero() {
			t.Fatalf("unpaired PSKY merger should price at 0, got %s", ev.Price.String())
		}
		if ev.Symbol == "PARAA" && ev.TradeSide == "sell" && !ev.Price.IsZero() {
			t.Fatalf("unpaired PARAA merger should price at 0, got %s", ev.Price.String())
		}
	}
}

// TestParseTradingCSV_Merger_OrphanPositive emits a price-0 BUY when only
// the new ticker side is in the file (e.g. spinoff reported as a single
// row). Quantity tracks but cost basis cannot be reconstructed.
func TestParseTradingCSV_Merger_OrphanPositive(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-08-08T13:52:54.198654Z,PSKY,MERGER - STOCK,10.733331,,USD 0,USD,1.1676
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.TradeSide != "buy" || !ev.Price.IsZero() || ev.Quantity.String() != "10.733331" {
		t.Fatalf("orphan +merger: %+v", ev)
	}
}

// TestParseTradingCSV_Merger_OrphanNegative is the matching case for the
// negative-only side: emit a price-0 SELL of the disappearing ticker.
// Without this the old position would stay open forever.
func TestParseTradingCSV_Merger_OrphanNegative(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-01-10T10:00:00.000Z,PARAA,BUY - MARKET,7,USD 10.00,USD 70.00,USD,1.0
2025-08-08T13:52:54.296206Z,PARAA,MERGER - STOCK,-7,,USD 0,USD,1.1676
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d", len(res.Events))
	}
	merger := res.Events[1]
	if merger.TradeSide != "sell" || merger.Symbol != "PARAA" || !merger.Price.IsZero() {
		t.Fatalf("orphan -merger: %+v", merger)
	}
}

// TestParseTradingCSV_Merger_SameSignNotPaired guards against pairing two
// merger rows that happen to land in the same window with the same sign
// (e.g. two spinoff legs from one parent). Each emits independently.
func TestParseTradingCSV_Merger_SameSignNotPaired(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-08-08T13:52:54.100Z,SPIN1,MERGER - STOCK,5,,USD 0,USD,1.0
2025-08-08T13:52:54.200Z,SPIN2,MERGER - STOCK,3,,USD 0,USD,1.0
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d", len(res.Events))
	}
	for _, ev := range res.Events {
		if ev.TradeSide != "buy" || !ev.Price.IsZero() {
			t.Fatalf("same-sign rows should not pair: %+v", ev)
		}
	}
}

func TestParseTradingCSV_SkipsCashEvents(t *testing.T) {
	csv := `Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate
2025-08-01T10:30:00.000Z,,DEPOSIT,,,USD 5000.00,USD,
2025-08-15T11:00:00.000Z,,CASH TOP-UP,,,USD 1000.00,USD,
`
	res, err := ParseTradingCSV([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 0 {
		t.Fatalf("cash events must not produce ImportEvents, got %+v", res.Events)
	}
}
