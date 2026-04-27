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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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

// decimalToNumeric converts a decimal.Decimal to pgtype.Numeric for sqlc params.
func decimalToNumeric(d decimal.Decimal) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(d.String())
	return n
}

// numericToDecimal converts a pgtype.Numeric to decimal.Decimal.
func numericToDecimal(n pgtype.Numeric) decimal.Decimal {
	if !n.Valid {
		return decimal.Zero
	}
	d, _ := decimal.NewFromString(n.Int.String())
	if n.Exp != 0 {
		d = d.Shift(int32(n.Exp))
	}
	return d
}

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
	rateNum, err := dbq.New(s.pool).LookupCachedFXRate(ctx, dbq.LookupCachedFXRateParams{
		BaseCurrency:  base,
		QuoteCurrency: quote,
		AsOf:          day,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return decimal.Zero, false, nil
		}
		return decimal.Zero, false, fmt.Errorf("query fx_rates: %w", err)
	}
	return numericToDecimal(rateNum), true, nil
}

func (s *Service) persistFX(ctx context.Context, obs FXObservation) error {
	src := obs.Source
	if src == "" {
		src = "manual"
	}
	err := dbq.New(s.pool).PersistFXRate(ctx, dbq.PersistFXRateParams{
		ID:            uuidx.New(),
		BaseCurrency:  strings.ToUpper(obs.Base),
		QuoteCurrency: strings.ToUpper(obs.Quote),
		AsOf:          asOfDate(obs.AsOf),
		Rate:          decimalToNumeric(obs.Rate),
		Source:        dbq.FxSource(src),
	})
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
	row, err := dbq.New(s.pool).LookupCachedLatestPrice(ctx, instrumentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceQuote{}, false, nil
		}
		return PriceQuote{}, false, fmt.Errorf("query instrument_prices: %w", err)
	}
	return PriceQuote{
		AsOf:     row.AsOf,
		Price:    numericToDecimal(row.Price),
		Currency: row.Currency,
		Source:   row.Source,
	}, true, nil
}

func (s *Service) lookupCachedRange(ctx context.Context, instrumentID uuid.UUID, from, to time.Time) (map[time.Time]PriceQuote, error) {
	rows, err := dbq.New(s.pool).LookupCachedPriceRange(ctx, dbq.LookupCachedPriceRangeParams{
		InstrumentID: instrumentID,
		FromDate:     from,
		ToDate:       to.Add(24 * time.Hour),
	})
	if err != nil {
		return nil, fmt.Errorf("query instrument_prices range: %w", err)
	}
	out := make(map[time.Time]PriceQuote)
	for _, r := range rows {
		q := PriceQuote{
			AsOf:     r.AsOf,
			Price:    numericToDecimal(r.Price),
			Currency: r.Currency,
			Source:   r.Source,
		}
		// Bucket on UTC date; if a day already has a primary row, don't overwrite.
		key := dayKey(q.AsOf)
		if _, exists := out[key]; !exists {
			out[key] = q
		}
	}
	return out, nil
}

func (s *Service) persistPrice(ctx context.Context, instrumentID uuid.UUID, q PriceQuote) error {
	src := q.Source
	if src == "" {
		src = "provider_primary"
	}
	cur := strings.ToUpper(q.Currency)
	return dbq.New(s.pool).PersistInstrumentPrice(ctx, dbq.PersistInstrumentPriceParams{
		ID:           uuidx.New(),
		InstrumentID: instrumentID,
		AsOf:         q.AsOf,
		Price:        decimalToNumeric(q.Price),
		Currency:     cur,
		Source:       dbq.PriceSource(src),
	})
}

// asOfDate normalises a timestamp to UTC midnight of its date.
func asOfDate(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func dayKey(t time.Time) time.Time { return asOfDate(t) }
