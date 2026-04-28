package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestYahooFallbackSymbols(t *testing.T) {
	got := yahooFallbackSymbols("VUAA")
	wantPrefix := []string{"VUAA.DE", "VUAA.MI"}
	for i, want := range wantPrefix {
		if i >= len(got) || got[i] != want {
			t.Fatalf("fallback[%d] = %q, want %q in %v", i, got[i], want, got)
		}
	}
	if got := yahooFallbackSymbols("AAPL"); len(got) == 0 || got[0] != "AAPL.DE" {
		t.Fatalf("generic fallback did not start with .DE: %v", got)
	}
	if got := yahooFallbackSymbols("VUAA.MI"); got != nil {
		t.Fatalf("qualified symbols should not fallback: %v", got)
	}
}

func TestYahooLatestQuoteRetriesEuropeanFallback(t *testing.T) {
	seen := make([]string, 0, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.TrimPrefix(r.URL.Path, "/")
		seen = append(seen, symbol)
		w.Header().Set("Content-Type", "application/json")
		if symbol == "VUAA.DE" {
			_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{"symbol":"VUAA.DE","currency":"EUR","regularMarketPrice":101.23,"regularMarketTime":1710000000}}],"error":null}}`))
			return
		}
		_, _ = w.Write([]byte(`{"chart":{"result":[],"error":{"description":"not found"}}}`))
	}))
	defer srv.Close()

	p := NewYahooProvider()
	p.baseURL = srv.URL
	q, err := p.LatestQuote(context.Background(), "VUAA")
	if err != nil {
		t.Fatal(err)
	}
	if q.Symbol != "VUAA.DE" || q.Currency != "EUR" || !q.Price.Equal(decimalFromString("101.23")) {
		t.Fatalf("quote = %+v", q)
	}
	if len(seen) < 2 || seen[0] != "VUAA" || seen[1] != "VUAA.DE" {
		t.Fatalf("unexpected lookup order: %v", seen)
	}
}

func decimalFromString(raw string) decimal.Decimal {
	d, err := decimal.NewFromString(raw)
	if err != nil {
		panic(err)
	}
	return d
}
