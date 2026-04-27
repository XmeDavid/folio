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
