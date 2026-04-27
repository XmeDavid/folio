package investments

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
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
	rows, err := s.pool.Query(ctx, `
		select distinct on (p.instrument_id)
			p.instrument_id, i.symbol, lp.as_of
		from investment_positions p
		join instruments i on i.id = p.instrument_id
		left join lateral (
			select as_of from instrument_prices
			where instrument_id = p.instrument_id
			order by as_of desc
			limit 1
		) lp on true
		where p.workspace_id = $1 and p.quantity > 0 and i.active
		order by p.instrument_id
	`, workspaceID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	pairs := make([]pair, 0, 32)
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.instrumentID, &p.symbol, &p.lastAsOf); err != nil {
			return 0, err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
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
