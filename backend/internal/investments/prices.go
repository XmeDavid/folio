package investments

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
)

// PrefetchPrices fetches the latest quote for every distinct instrument held
// in an open position in the workspace, in parallel, and persists the
// observations to the global instrument_prices table. Stale-but-present
// quotes are refreshed only when older than `staleAfter`. Errors per symbol
// are swallowed so a single Yahoo blip doesn't blank the dashboard — the
// market-data service falls back to the most recent cached row in that case.
func (s *Service) PrefetchPrices(ctx context.Context, workspaceID uuid.UUID, staleAfter time.Duration) (int, error) {
	if s.md == nil || !s.md.HasPriceProvider() {
		return 0, nil
	}

	type pair struct {
		instrumentID uuid.UUID
		symbol       string
		lastAsOf     *time.Time
	}
	rows, err := dbq.New(s.pool).ListOpenPositionInstrumentsWithPrice(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	pairs := make([]pair, 0, len(rows))
	for _, r := range rows {
		p := pair{instrumentID: r.InstrumentID, symbol: r.Symbol}
		if !r.LastPriceAsOf.IsZero() {
			t := r.LastPriceAsOf
			p.lastAsOf = &t
		}
		pairs = append(pairs, p)
	}

	now := s.now()
	const maxParallel = 4
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	updated := 0

	for _, p := range pairs {
		// Skip if a cached row exists and is fresher than staleAfter.
		if p.lastAsOf != nil && now.Sub(*p.lastAsOf) < staleAfter {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p pair) {
			defer wg.Done()
			defer func() { <-sem }()
			fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			if _, err := s.md.LatestPrice(fetchCtx, p.instrumentID, p.symbol); err == nil {
				mu.Lock()
				updated++
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	return updated, nil
}
