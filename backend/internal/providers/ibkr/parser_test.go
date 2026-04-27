package ibkr

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseJSON_BuyOnly(t *testing.T) {
	in := `{
		"account": "U12345",
		"currency": "USD",
		"transactions": [
			{"date": "2025-12-03", "symbol": "GOOGL", "quantity": 1,
			 "unit_price": 319.29, "trade_amount_debited": 319.30, "commission": 0.01}
		]
	}`
	res, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != FormatJSON {
		t.Fatalf("want json, got %s", res.Format)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.Symbol != "GOOGL" || ev.TradeSide != "buy" || ev.Currency != "USD" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if !ev.Quantity.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("qty = %s", ev.Quantity.String())
	}
	if !ev.Fee.Equal(decimal.NewFromFloat(0.01)) {
		t.Fatalf("fee = %s", ev.Fee.String())
	}
}

func TestParseActivityCSV_BuyAndSell(t *testing.T) {
	csv := strings.Join([]string{
		`Account Information,Data,Base Currency,CHF`,
		`Trades,Data,Order,Stocks,USD,GOOGL,"2025-12-03, 10:53:11",10,200,0,-2000,1.5`,
		`Trades,Data,Order,Stocks,USD,GOOGL,"2026-02-04, 14:30:00",-3,250,0,750,1.5`,
		`Trades,Data,Order,Forex,CHF,USD.CHF,"2025-11-12, 17:00:00",-1.633,0.797,,1.302,0`,
	}, "\n") + "\n"
	res, err := Parse([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != FormatActivityCSV {
		t.Fatalf("want activity_csv, got %s", res.Format)
	}
	if res.BaseCurrency != "CHF" {
		t.Fatalf("base = %s", res.BaseCurrency)
	}
	// Forex row is intentionally skipped in v1.
	if len(res.Events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(res.Events), res.Events)
	}
	if res.Events[0].TradeSide != "buy" || res.Events[1].TradeSide != "sell" {
		t.Fatalf("sides wrong: %+v", res.Events)
	}
	if res.Events[1].Quantity.IsNegative() {
		t.Fatalf("qty must be positive after parse")
	}
}

func TestParseActivityCSV_BadRowsSkipped(t *testing.T) {
	csv := `Account Information,Data,Base Currency,USD
Trades,Data,Order,Stocks,USD,,"2025-12-03, 10:53:11",10,200,0,-2000,1.5
Trades,Header,Asset Category,Currency,Symbol
Trades,Data,Order,Stocks,USD,GOOGL,"2025-12-04, 11:00:00",10,200,0,-2000,1.5
`
	res, err := Parse([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	// Row with empty symbol must be skipped.
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(res.Events))
	}
	if res.Events[0].Symbol != "GOOGL" {
		t.Fatalf("unexpected symbol: %s", res.Events[0].Symbol)
	}
}
