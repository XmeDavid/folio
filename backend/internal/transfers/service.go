package transfers

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns transfer-pair detection and lifecycle.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService returns a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// DetectAndPair runs the three-tier detector. Tier 1 + 2 write
// transfer_matches; Tier 3 writes transfer_match_candidates.
//
// Tier implementations live in tier1.go, tier2.go, tier3.go (Tasks 1.2-1.4).
func (s *Service) DetectAndPair(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (*DetectResult, error) {
	t1, err := s.runTier1(ctx, workspaceID, scope)
	if err != nil {
		return nil, err
	}
	t2, err := s.runTier2(ctx, workspaceID, scope)
	if err != nil {
		return nil, err
	}
	t3, err := s.runTier3(ctx, workspaceID, scope)
	if err != nil {
		return nil, err
	}
	return &DetectResult{Tier1Paired: t1, Tier2Paired: t2, Tier3Suggested: t3}, nil
}

// Stubs filled in by Tasks 1.3 / 1.4. Tier 1 lives in tier1.go.
func (s *Service) runTier2(context.Context, uuid.UUID, DetectScope) (int, error) { return 0, nil }
func (s *Service) runTier3(context.Context, uuid.UUID, DetectScope) (int, error) { return 0, nil }
