package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// FrankfurterProvider implements FXProvider against the public Frankfurter
// service (https://www.frankfurter.dev) which fronts ECB reference rates.
// No API key required.
type FrankfurterProvider struct {
	baseURL string
	client  *http.Client
}

// NewFrankfurterProvider constructs a provider with sensible defaults.
func NewFrankfurterProvider() *FrankfurterProvider {
	return &FrankfurterProvider{
		baseURL: "https://api.frankfurter.dev/v1",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Name implements FXProvider.
func (p *FrankfurterProvider) Name() string { return "ecb" }

type frankfurterResp struct {
	Date  string             `json:"date"`
	Base  string             `json:"base"`
	Rates map[string]float64 `json:"rates"`
}

func (p *FrankfurterProvider) fetch(ctx context.Context, path string) (*frankfurterResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("frankfurter %s: status %d", path, res.StatusCode)
	}
	var body frankfurterResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	return &body, nil
}

// HistoricalRate implements FXProvider. Frankfurter falls back to the prior
// business day automatically when asOf is a non-trading day.
func (p *FrankfurterProvider) HistoricalRate(ctx context.Context, base, quote string, asOf time.Time) (FXObservation, error) {
	base = strings.ToUpper(base)
	quote = strings.ToUpper(quote)
	if base == quote {
		return FXObservation{Base: base, Quote: quote, AsOf: asOf, Rate: decimal.NewFromInt(1), Source: p.Name()}, nil
	}
	day := asOf.UTC().Format("2006-01-02")
	q := url.Values{}
	q.Set("base", base)
	q.Set("symbols", quote)
	body, err := p.fetch(ctx, "/"+day+"?"+q.Encode())
	if err != nil {
		return FXObservation{}, err
	}
	rate, ok := body.Rates[quote]
	if !ok {
		return FXObservation{}, fmt.Errorf("frankfurter: no rate for %s in %s", quote, body.Base)
	}
	parsed, err := time.Parse("2006-01-02", body.Date)
	if err != nil {
		parsed = asOf
	}
	return FXObservation{
		Base:   base,
		Quote:  quote,
		AsOf:   parsed,
		Rate:   decimal.NewFromFloat(rate),
		Source: p.Name(),
	}, nil
}

// LatestRate implements FXProvider.
func (p *FrankfurterProvider) LatestRate(ctx context.Context, base, quote string) (FXObservation, error) {
	base = strings.ToUpper(base)
	quote = strings.ToUpper(quote)
	if base == quote {
		return FXObservation{Base: base, Quote: quote, AsOf: time.Now().UTC(), Rate: decimal.NewFromInt(1), Source: p.Name()}, nil
	}
	q := url.Values{}
	q.Set("base", base)
	q.Set("symbols", quote)
	body, err := p.fetch(ctx, "/latest?"+q.Encode())
	if err != nil {
		return FXObservation{}, err
	}
	rate, ok := body.Rates[quote]
	if !ok {
		return FXObservation{}, fmt.Errorf("frankfurter: no rate for %s in %s", quote, body.Base)
	}
	parsed, _ := time.Parse("2006-01-02", body.Date)
	return FXObservation{
		Base:   base,
		Quote:  quote,
		AsOf:   parsed,
		Rate:   decimal.NewFromFloat(rate),
		Source: p.Name(),
	}, nil
}
