package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// YahooProvider implements PriceProvider against the unofficial-but-stable
// Yahoo Finance v8 chart endpoint. No key required. Best-effort: Yahoo may
// throttle or return empty payloads, in which case errors propagate up to
// the cache service which falls back to last-known cached rows.
type YahooProvider struct {
	baseURL       string
	searchBaseURL string
	client        *http.Client
}

// NewYahooProvider constructs a provider with sensible defaults.
func NewYahooProvider() *YahooProvider {
	return &YahooProvider{
		baseURL:       "https://query1.finance.yahoo.com/v8/finance/chart",
		searchBaseURL: "https://query2.finance.yahoo.com/v1/finance/search",
		client:        &http.Client{Timeout: 12 * time.Second},
	}
}

// Name implements PriceProvider.
func (p *YahooProvider) Name() string { return "provider_primary" }

type yahooChartResp struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				Currency           string  `json:"currency"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error any `json:"error"`
	} `json:"chart"`
}

var yahooEuropeanSuffixes = []string{".DE", ".MI", ".AS", ".PA", ".L", ".SW"}

type yahooSearchResp struct {
	Quotes []struct {
		Symbol    string `json:"symbol"`
		QuoteType string `json:"quoteType"`
		Exchange  string `json:"exchange"`
	} `json:"quotes"`
}

func (p *YahooProvider) fetch(ctx context.Context, symbol, range_, interval string, period1, period2 int64) (*yahooChartResp, error) {
	q := url.Values{}
	if range_ != "" {
		q.Set("range", range_)
	} else {
		q.Set("period1", fmt.Sprintf("%d", period1))
		q.Set("period2", fmt.Sprintf("%d", period2))
	}
	q.Set("interval", interval)
	q.Set("includePrePost", "false")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s?%s", p.baseURL, url.PathEscape(symbol), q.Encode()), nil)
	if err != nil {
		return nil, err
	}
	// Yahoo blocks default Go user-agents.
	req.Header.Set("User-Agent", "FolioBot/1.0 (+https://folio.local)")

	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo %s: status %d", symbol, res.StatusCode)
	}
	var body yahooChartResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo %s: empty result", symbol)
	}
	return &body, nil
}

func (p *YahooProvider) fetchWithFallback(ctx context.Context, symbol, range_, interval string, period1, period2 int64) (*yahooChartResp, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	candidates := p.lookupSymbols(ctx, symbol)
	var firstErr error
	for _, candidate := range candidates {
		body, err := p.fetch(ctx, candidate, range_, interval, period1, period2)
		if firstErr == nil && err != nil {
			firstErr = err
		}
		if err == nil {
			return body, nil
		}
	}
	return nil, firstErr
}

func (p *YahooProvider) lookupSymbols(ctx context.Context, symbol string) []string {
	if symbol == "" {
		return nil
	}
	if strings.Contains(symbol, ".") {
		return []string{symbol}
	}
	candidates := p.searchSymbols(ctx, symbol)
	candidates = append(candidates, yahooFallbackSymbols(symbol)...)
	candidates = append(candidates, symbol)

	out := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.ToUpper(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func (p *YahooProvider) searchSymbols(ctx context.Context, symbol string) []string {
	if p.searchBaseURL == "" {
		return nil
	}
	q := url.Values{}
	q.Set("q", symbol)
	q.Set("quotesCount", "12")
	q.Set("newsCount", "0")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchBaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "FolioBot/1.0 (+https://folio.local)")
	res, err := p.client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	var body yahooSearchResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil
	}

	matches := make([]struct {
		symbol string
		score  int
	}, 0, len(body.Quotes))
	for _, q := range body.Quotes {
		candidate := strings.ToUpper(strings.TrimSpace(q.Symbol))
		if candidate != symbol && !strings.HasPrefix(candidate, symbol+".") {
			continue
		}
		score := yahooQuoteTypeScore(q.QuoteType)*100 + yahooExchangeScore(q.Exchange)
		if candidate == symbol {
			score -= 20
		}
		matches = append(matches, struct {
			symbol string
			score  int
		}{symbol: candidate, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].symbol < matches[j].symbol
		}
		return matches[i].score < matches[j].score
	})
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.symbol)
	}
	return out
}

func yahooQuoteTypeScore(quoteType string) int {
	switch strings.ToUpper(strings.TrimSpace(quoteType)) {
	case "ETF", "EQUITY":
		return 0
	case "MUTUALFUND":
		return 2
	default:
		return 1
	}
}

func yahooExchangeScore(exchange string) int {
	switch strings.ToUpper(strings.TrimSpace(exchange)) {
	case "NMS", "NYQ", "NGM", "PCX":
		return 0
	case "GER", "MIL", "AMS", "EBS":
		return 1
	case "LSE":
		return 2
	case "MUN", "DUS", "STU", "HAM", "HAN":
		return 3
	default:
		return 5
	}
}

func yahooFallbackSymbols(symbol string) []string {
	if symbol == "" || strings.Contains(symbol, ".") {
		return nil
	}
	out := make([]string, 0, len(yahooEuropeanSuffixes))
	for _, suffix := range yahooEuropeanSuffixes {
		out = append(out, symbol+suffix)
	}
	return out
}

// LatestQuote implements PriceProvider.
func (p *YahooProvider) LatestQuote(ctx context.Context, symbol string) (PriceQuote, error) {
	body, err := p.fetchWithFallback(ctx, symbol, "1d", "1m", 0, 0)
	if err != nil {
		return PriceQuote{}, err
	}
	r := body.Chart.Result[0]
	if r.Meta.RegularMarketPrice == 0 || math.IsNaN(r.Meta.RegularMarketPrice) {
		return PriceQuote{}, fmt.Errorf("yahoo %s: no regular price", symbol)
	}
	asOf := time.Unix(r.Meta.RegularMarketTime, 0).UTC()
	if r.Meta.RegularMarketTime == 0 {
		asOf = time.Now().UTC()
	}
	return PriceQuote{
		Symbol:   r.Meta.Symbol,
		AsOf:     asOf,
		Price:    decimal.NewFromFloat(r.Meta.RegularMarketPrice),
		Currency: strings.ToUpper(r.Meta.Currency),
		Source:   p.Name(),
	}, nil
}

// HistoricalRange implements PriceProvider.
func (p *YahooProvider) HistoricalRange(ctx context.Context, symbol string, from, to time.Time) ([]PriceQuote, error) {
	body, err := p.fetchWithFallback(ctx, symbol, "", "1d", from.Unix(), to.Add(24*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	r := body.Chart.Result[0]
	currency := strings.ToUpper(r.Meta.Currency)
	if currency == "" {
		currency = "USD"
	}
	if len(r.Indicators.Quote) == 0 || len(r.Timestamp) == 0 {
		return nil, fmt.Errorf("yahoo %s: empty timeseries", symbol)
	}
	closes := r.Indicators.Quote[0].Close
	out := make([]PriceQuote, 0, len(r.Timestamp))
	for i, ts := range r.Timestamp {
		if i >= len(closes) || closes[i] == nil {
			continue
		}
		c := *closes[i]
		if math.IsNaN(c) || c <= 0 {
			continue
		}
		t := time.Unix(ts, 0).UTC()
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		out = append(out, PriceQuote{
			Symbol:   r.Meta.Symbol,
			AsOf:     t,
			Price:    decimal.NewFromFloat(c),
			Currency: currency,
			Source:   p.Name(),
		})
	}
	return out, nil
}
