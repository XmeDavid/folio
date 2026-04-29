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
	wantPrefix := []string{"VUAA.DE", "VUAA.MI", "VUAA.AS"}
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

func TestYahooLatestQuoteSearchesBeforeSuffixFallback(t *testing.T) {
	seen := make([]string, 0, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/search" {
			seen = append(seen, "search:"+r.URL.Query().Get("q"))
			_, _ = w.Write([]byte(`{"quotes":[
				{"symbol":"VUAA.DU","quoteType":"ETF","exchange":"DUS"},
				{"symbol":"VUAA.DE","quoteType":"ETF","exchange":"GER"},
				{"symbol":"VUAAN.MX","quoteType":"ETF","exchange":"MEX"}
			]}`))
			return
		}
		symbol := strings.TrimPrefix(r.URL.Path, "/chart/")
		seen = append(seen, "chart:"+symbol)
		if symbol == "VUAA.DE" {
			_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{"symbol":"VUAA.DE","currency":"EUR","regularMarketPrice":101.23,"regularMarketTime":1710000000}}],"error":null}}`))
			return
		}
		_, _ = w.Write([]byte(`{"chart":{"result":[],"error":{"description":"not found"}}}`))
	}))
	defer srv.Close()

	p := NewYahooProvider()
	p.baseURL = srv.URL + "/chart"
	p.searchBaseURL = srv.URL + "/search"
	q, err := p.LatestQuote(context.Background(), "VUAA")
	if err != nil {
		t.Fatal(err)
	}
	if q.Symbol != "VUAA.DE" || q.Currency != "EUR" || !q.Price.Equal(decimalFromString("101.23")) {
		t.Fatalf("quote = %+v", q)
	}
	want := []string{"search:VUAA", "chart:VUAA.DE"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
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
