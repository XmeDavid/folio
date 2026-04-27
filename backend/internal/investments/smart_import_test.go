package investments

import (
	"strings"
	"testing"
)

// TestDetectAndParse_BOMPrefixedIBKR is the regression that pinned the
// production bug: IBKR exports the Activity Statement with a leading
// UTF-8 BOM (0xEF 0xBB 0xBF). Without an explicit TrimPrefix("\ufeff"),
// HasPrefix(... "Statement,Header,Field Name") fails and the smart-import
// path silently misses, falling through to the bank-import code which
// emits "this looks like an Interactive Brokers activity statement —
// upload it via Investments → Import" — confusing because the user *was*
// using the smart-import endpoint.
func TestDetectAndParse_BOMPrefixedIBKR(t *testing.T) {
	body := "\ufeffStatement,Header,Field Name,Field Value\n" +
		"Account Information,Data,Base Currency,CHF\n" +
		`Trades,Data,Order,Stocks,USD,GOOGL,"2025-12-03, 10:53:11",1,319.29,0,-319.29,1,320.29,0,0,0.34,O` + "\n"
	src, events, base, err := detectAndParse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if src != "ibkr" {
		t.Fatalf("source = %q, want ibkr", src)
	}
	if base != "CHF" {
		t.Fatalf("base = %q, want CHF", base)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Symbol != "GOOGL" {
		t.Fatalf("symbol = %q", events[0].Symbol)
	}
}

func TestDetectAndParse_PlainIBKRStatement(t *testing.T) {
	body := strings.Join([]string{
		"Statement,Header,Field Name,Field Value",
		"Account Information,Data,Base Currency,USD",
	}, "\n") + "\n"
	src, _, _, err := detectAndParse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if src != "ibkr" {
		t.Fatalf("source = %q, want ibkr", src)
	}
}

func TestDetectAndParse_RevolutTradingHeader(t *testing.T) {
	body := strings.Join([]string{
		"Date,Ticker,Type,Quantity,Price per share,Total Amount,Currency,FX Rate",
		`2025-08-01T10:30:00.000Z,AAPL,BUY - MARKET,10,USD 200.00,USD 2000.05,USD,1.0`,
	}, "\n") + "\n"
	src, events, _, err := detectAndParse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if src != "revolut_trading" {
		t.Fatalf("source = %q, want revolut_trading", src)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestDetectAndParse_UnknownFileFallsThrough(t *testing.T) {
	body := "Date,Description,Amount\n2025-01-01,Coffee,3.50\n"
	src, _, _, err := detectAndParse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if src != "" {
		t.Fatalf("source = %q, want empty so caller falls through to bank import", src)
	}
}
