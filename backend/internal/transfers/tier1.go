package transfers

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// runTier1 finds pairs where source.original_amount + destination.amount = 0
// AND source.original_currency = destination.currency, with opposite signs
// and a ±1 day window. Source must have original_amount populated.
//
// Match cardinality: exactly-one. If multiple destinations match, Tier 1
// skips (Tier 3 may surface the source for manual review).
func (s *Service) runTier1(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (int, error) {
	leftFilter, leftArgs := scopeLeftFilter(scope, "t1")
	args := []any{workspaceID}
	args = append(args, leftArgs...)

	q := `
		WITH candidates AS (
			SELECT t1.id AS source_id, t1.amount, t1.original_amount,
			       (SELECT array_agg(t2.id) FROM transactions t2
			        WHERE t2.workspace_id = t1.workspace_id
			          AND t2.account_id != t1.account_id
			          AND t2.currency = t1.original_currency
			          AND abs(t2.amount::numeric) = abs(t1.original_amount::numeric)
			          AND sign(t2.amount::numeric) != sign(t1.amount::numeric)
			          AND abs(t2.booked_at - t1.booked_at) <= 1
			          AND NOT EXISTS (
			            SELECT 1 FROM transfer_matches tm
			            WHERE tm.workspace_id = t2.workspace_id
			              AND (tm.source_transaction_id = t2.id
			                   OR tm.destination_transaction_id = t2.id)
			          )
			       ) AS candidate_dst_ids
			FROM transactions t1
			WHERE t1.workspace_id = $1
			  AND t1.original_amount IS NOT NULL
			  AND t1.original_currency IS NOT NULL
			  AND t1.amount::numeric < 0
			  AND NOT EXISTS (
			    SELECT 1 FROM transfer_matches tm
			    WHERE tm.workspace_id = t1.workspace_id
			      AND (tm.source_transaction_id = t1.id
			           OR tm.destination_transaction_id = t1.id)
			  )
			  ` + leftFilter + `
		)
		SELECT source_id, amount::text, original_amount::text, candidate_dst_ids
		FROM candidates
		WHERE candidate_dst_ids IS NOT NULL AND array_length(candidate_dst_ids, 1) = 1
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("tier1 query: %w", err)
	}
	defer rows.Close()

	type pair struct {
		sourceID, destID uuid.UUID
		amount, original string
	}
	var pairs []pair
	for rows.Next() {
		var sourceID uuid.UUID
		var amount, original string
		var dst []uuid.UUID
		if err := rows.Scan(&sourceID, &amount, &original, &dst); err != nil {
			return 0, fmt.Errorf("tier1 scan: %w", err)
		}
		if len(dst) != 1 {
			continue
		}
		pairs = append(pairs, pair{sourceID, dst[0], amount, original})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, p := range pairs {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO transfer_matches (
				id, workspace_id, source_transaction_id, destination_transaction_id,
				fx_rate, provenance, matched_at
			) VALUES (
				$1, $2, $3, $4,
				abs($5::numeric / $6::numeric), 'auto_detected', $7
			)
			ON CONFLICT DO NOTHING
		`, uuidx.New(), workspaceID, p.sourceID, p.destID, p.amount, p.original, s.now().UTC())
		if err != nil {
			return count, fmt.Errorf("tier1 insert: %w", err)
		}
		count++
	}
	return count, nil
}

// scopeLeftFilter renders an additional WHERE clause that restricts the
// LEFT side (source) of detection to the scope's TransactionIDs (when
// scope.All is false). Returns the SQL fragment and the args to append
// after the workspace_id at $1.
func scopeLeftFilter(scope DetectScope, alias string) (string, []any) {
	if scope.All || len(scope.TransactionIDs) == 0 {
		return "", nil
	}
	return fmt.Sprintf(" AND %s.id = ANY($2)", alias), []any{scope.TransactionIDs}
}
