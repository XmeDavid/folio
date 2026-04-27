package marketdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// CurrentQuoteFreshness is how long a "latest" quote is considered fresh
// before the service falls back to the upstream provider. The spec calls
// out current quotes as short-lived cache data (§11).
const CurrentQuoteFreshness = 15 * time.Minute

// ErrNotAvailable is returned when neither cache nor provider can produce
// the requested observation.
var ErrNotAvailable = errors.New("marketdata: observation not available")

// Service wraps the fx_rates and instrument_prices tables in a single API
// surface for the investments domain. It serves cache hits cheaply, falls
// back to providers on miss, and persists provider rows for next time.
type Service struct {
	pool   *pgxpool.Pool
	prices PriceProvider
	fx     FXProvider
	now    func() time.Time
}

// NewService returns a Service. Either provider may be nil — in that case
// the corresponding fallback is disabled and only cached rows are returned.
func NewService(pool *pgxpool.Pool, prices PriceProvider, fx FXProvider) *Service {
	return &Service{pool: pool, prices: prices, fx: fx, now: time.Now}
}

// SetClock overrides the clock for tests.
func (s *Service) SetClock(fn func() time.Time) { s.now = fn }

// HasPriceProvider reports whether a price provider is configured.
func (s *Service) HasPriceProvider() bool { return s.prices != nil }

// HasFXProvider reports whether an FX provider is configured.
func (s *Service) HasFXProvider() bool { return s.fx != nil }

// FXRate returns the rate for converting base -> quote on asOf. It first
// looks up the exact-date cache, then the most recent prior-business-day
// cache row, then asks the configured FXProvider, persisting the result. A
// rate of 1 is returned when base == quote.
func (s *Service) FXRate(ctx context.Context, base, quote string, asOf time.Time) (decimal.Decimal, error) {
	base = strings.ToUpper(strings.TrimSpace(base))
	quote = strings.ToUpper(strings.TrimSpace(quote))
	if base == "" || quote == "" {
		return decimal.Zero, fmt.Errorf("marketdata: base/quote required")
	}
	if base == quote {
		return decimal.NewFromInt(1), nil
	}

	day := asOfDate(asOf)

	// 1. Cache lookup, preferring exact date over fallback.
	if rate, ok, err := s.lookupCachedFX(ctx, base, quote, day); err != nil {
		return decimal.Zero, err
	} else if ok {
		return rate, nil
	}

	// 2. Provider fallback.
	if s.fx == nil {
		return decimal.Zero, ErrNotAvailable
	}
	obs, err := s.fx.HistoricalRate(ctx, base, quote, day)
	if err != nil {
		return decimal.Zero, fmt.Errorf("fx provider: %w", err)
	}
	if err := s.persistFX(ctx, obs); err != nil {
		// Non-fatal: we still return the rate even if persisting failed,
		// because the caller asked for a number, not a side-effect.
		// Logging is the caller's job.
		_ = err
	}
	return obs.Rate, nil
}

// FXRateInverse computes a quote->base rate by taking 1 / FXRate(base, quote).
// Useful when the cached/provider rate is in the opposite direction.
func (s *Service) FXRateInverse(ctx context.Context, base, quote string, asOf time.Time) (decimal.Decimal, error) {
	r, err := s.FXRate(ctx, quote, base, asOf)
	if err != nil {
		return decimal.Zero, err
	}
	if r.IsZero() {
		return decimal.Zero, ErrNotAvailable
	}
	return decimal.NewFromInt(1).Div(r), nil
}

// Convert applies an FX conversion: returns amount * FXRate(from, to, asOf).
// from and to may be equal, in which case amount is returned unchanged.
func (s *Service) Convert(ctx context.Context, amount decimal.Decimal, from, to string, asOf time.Time) (decimal.Decimal, error) {
	if amount.IsZero() {
		return decimal.Zero, nil
	}
	if strings.EqualFold(from, to) {
		return amount, nil
	}
	rate, err := s.FXRate(ctx, from, to, asOf)
	if err != nil {
		return decimal.Zero, err
	}
	return amount.Mul(rate), nil
}

// LatestPrice returns the latest cached quote for instrumentID. If the cached
// quote is older than CurrentQuoteFreshness (or none exists) and a provider
// is configured, the provider is consulted and the result persisted.
func (s *Service) LatestPrice(ctx context.Context, instrumentID uuid.UUID, symbol string) (PriceQuote, error) {
	// Try fresh cache.
	if q, ok, err := s.lookupCachedLatestPrice(ctx, instrumentID); err != nil {
		return PriceQuote{}, err
	} else if ok && s.now().Sub(q.AsOf) < CurrentQuoteFreshness {
		return q, nil
	}

	if s.prices == nil {
		// No provider; return whatever cached row we have, even if stale.
		if q, ok, _ := s.lookupCachedLatestPrice(ctx, instrumentID); ok {
			return q, nil
		}
		return PriceQuote{}, ErrNotAvailable
	}

	q, err := s.prices.LatestQuote(ctx, symbol)
	if err != nil {
		// Fall back to stale cache if the provider is down.
		if cached, ok, _ := s.lookupCachedLatestPrice(ctx, instrumentID); ok {
			return cached, nil
		}
		return PriceQuote{}, fmt.Errorf("price provider: %w", err)
	}
	q.Symbol = symbol
	if err := s.persistPrice(ctx, instrumentID, q); err != nil {
		_ = err
	}
	return q, nil
}

// HistoricalRange returns a date->close map for instrumentID over [from, to],
// preferring cached rows. Missing days are filled by the configured provider
// when one exists. The returned map keys are the asOf dates from cache rows
// (UTC midnight).
func (s *Service) HistoricalRange(ctx context.Context, instrumentID uuid.UUID, symbol string, from, to time.Time) (map[time.Time]PriceQuote, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("marketdata: to before from")
	}
	cached, err := s.lookupCachedRange(ctx, instrumentID, from, to)
	if err != nil {
		return nil, err
	}
	// Decide whether to refresh. Same heuristic as the legacy app:
	// if the cache covers the window edges within a 7-day buffer, accept it.
	needsFetch := s.prices != nil && rangeNeedsFetch(cached, from, to)
	if needsFetch {
		fetched, err := s.prices.HistoricalRange(ctx, symbol, from, to)
		if err == nil {
			for _, q := range fetched {
				q.Symbol = symbol
				if e := s.persistPrice(ctx, instrumentID, q); e != nil {
					// Non-fatal; carry on.
					continue
				}
			}
			cached, err = s.lookupCachedRange(ctx, instrumentID, from, to)
			if err != nil {
				return nil, err
			}
		}
	}
	return cached, nil
}

func rangeNeedsFetch(cached map[time.Time]PriceQuote, from, to time.Time) bool {
	if len(cached) == 0 {
		return true
	}
	earliest := to
	latest := from
	for d := range cached {
		if d.Before(earliest) {
			earliest = d
		}
		if d.After(latest) {
			latest = d
		}
	}
	startThreshold := from.Add(7 * 24 * time.Hour)
	endThreshold := to.Add(-7 * 24 * time.Hour)
	hasStart := !earliest.After(startThreshold)
	hasEnd := !latest.Before(endThreshold)
	return !hasStart || !hasEnd
}

func (s *Service) lookupCachedFX(ctx context.Context, base, quote string, day time.Time) (decimal.Decimal, bool, error) {
	var rate decimal.Decimal
	err := s.pool.QueryRow(ctx, `
		select rate
		from fx_rates
		where base_currency = $1 and quote_currency = $2 and as_of <= $3
		order by as_of desc
		limit 1
	`, base, quote, day).Scan(&rate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return decimal.Zero, false, nil
		}
		return decimal.Zero, false, fmt.Errorf("query fx_rates: %w", err)
	}
	return rate, true, nil
}

func (s *Service) persistFX(ctx context.Context, obs FXObservation) error {
	src := obs.Source
	if src == "" {
		src = "manual"
	}
	_, err := s.pool.Exec(ctx, `
		insert into fx_rates (id, base_currency, quote_currency, as_of, rate, source)
		values ($1, $2, $3, $4, $5::numeric, $6::fx_source)
		on conflict (base_currency, quote_currency, as_of, source) do nothing
	`, uuidx.New(), strings.ToUpper(obs.Base), strings.ToUpper(obs.Quote), asOfDate(obs.AsOf), obs.Rate.String(), src)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			// check_violation: usually base = quote sneaking through.
			return nil
		}
		return fmt.Errorf("persist fx: %w", err)
	}
	return nil
}

func (s *Service) lookupCachedLatestPrice(ctx context.Context, instrumentID uuid.UUID) (PriceQuote, bool, error) {
	var q PriceQuote
	var price decimal.Decimal
	err := s.pool.QueryRow(ctx, `
		select as_of, price, currency, source::text
		from instrument_prices
		where instrument_id = $1
		order by as_of desc
		limit 1
	`, instrumentID).Scan(&q.AsOf, &price, &q.Currency, &q.Source)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceQuote{}, false, nil
		}
		return PriceQuote{}, false, fmt.Errorf("query instrument_prices: %w", err)
	}
	q.Price = price
	return q, true, nil
}

func (s *Service) lookupCachedRange(ctx context.Context, instrumentID uuid.UUID, from, to time.Time) (map[time.Time]PriceQuote, error) {
	rows, err := s.pool.Query(ctx, `
		select as_of, price, currency, source::text
		from instrument_prices
		where instrument_id = $1 and as_of >= $2 and as_of <= $3
		order by as_of asc
	`, instrumentID, from, to.Add(24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("query instrument_prices range: %w", err)
	}
	defer rows.Close()
	out := make(map[time.Time]PriceQuote)
	for rows.Next() {
		var q PriceQuote
		var price decimal.Decimal
		if err := rows.Scan(&q.AsOf, &price, &q.Currency, &q.Source); err != nil {
			return nil, err
		}
		q.Price = price
		// Bucket on UTC date; if a day already has a primary row, don't overwrite.
		key := dayKey(q.AsOf)
		if _, exists := out[key]; !exists {
			out[key] = q
		}
	}
	return out, rows.Err()
}

func (s *Service) persistPrice(ctx context.Context, instrumentID uuid.UUID, q PriceQuote) error {
	src := q.Source
	if src == "" {
		src = "provider_primary"
	}
	cur := strings.ToUpper(q.Currency)
	_, err := s.pool.Exec(ctx, `
		insert into instrument_prices (id, instrument_id, as_of, price, currency, source)
		values ($1, $2, $3, $4::numeric, $5, $6::price_source)
		on conflict (instrument_id, as_of, source) do nothing
	`, uuidx.New(), instrumentID, q.AsOf, q.Price.String(), cur, src)
	if err != nil {
		return fmt.Errorf("persist instrument_price: %w", err)
	}
	return nil
}

// asOfDate normalises a timestamp to UTC midnight of its date.
func asOfDate(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func dayKey(t time.Time) time.Time { return asOfDate(t) }
