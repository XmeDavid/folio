package transfers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// runTier2 pairs transactions that share an import_batch_id (via source_refs),
// have opposite signs, same currency, and amounts that net within fee tolerance:
//
//	|t1.amount + t2.amount| <= GREATEST(2.00, 0.5%)
//
// Date window is ±1 day. Cardinality: exactly-one match required.
func (s *Service) runTier2(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (int, error) {
	leftFilter, leftArgs := scopeLeftFilter(scope, "t1")
	args := []any{workspaceID}
	args = append(args, leftArgs...)

	q := `
		WITH candidates AS (
			SELECT t1.id AS source_id, t1.amount, t1.currency,
			       (SELECT array_agg(t2.id) FROM transactions t2
			        JOIN source_refs sr1 ON sr1.entity_id = t1.id AND sr1.entity_type = 'transaction'
			        JOIN source_refs sr2 ON sr2.entity_id = t2.id AND sr2.entity_type = 'transaction'
			        WHERE t2.workspace_id = t1.workspace_id
			          AND t2.account_id != t1.account_id
			          AND t2.currency = t1.currency
			          AND sign(t2.amount::numeric) != sign(t1.amount::numeric)
			          AND abs(t2.amount::numeric + t1.amount::numeric)
			              <= GREATEST(2.00, 0.005 * abs(t1.amount::numeric))
			          AND abs(t2.booked_at - t1.booked_at) <= 1
			          AND sr1.import_batch_id IS NOT NULL
			          AND sr2.import_batch_id = sr1.import_batch_id
			          AND NOT EXISTS (
			            SELECT 1 FROM transfer_matches tm
			            WHERE tm.workspace_id = t2.workspace_id
			              AND (tm.source_transaction_id = t2.id
			                   OR tm.destination_transaction_id = t2.id)
			          )
			       ) AS candidate_dst_ids
			FROM transactions t1
			WHERE t1.workspace_id = $1
			  AND t1.amount::numeric < 0
			  AND NOT EXISTS (
			    SELECT 1 FROM transfer_matches tm
			    WHERE tm.workspace_id = t1.workspace_id
			      AND (tm.source_transaction_id = t1.id
			           OR tm.destination_transaction_id = t1.id)
			  )
			  ` + leftFilter + `
		)
		SELECT source_id, amount::text, currency::text, candidate_dst_ids
		FROM candidates
		WHERE candidate_dst_ids IS NOT NULL AND array_length(candidate_dst_ids, 1) = 1
	`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("tier2 query: %w", err)
	}
	defer rows.Close()

	type pair struct {
		sourceID, destID    uuid.UUID
		srcAmount, currency string
	}
	var pairs []pair
	for rows.Next() {
		var sourceID uuid.UUID
		var amount, currency string
		var dst []uuid.UUID
		if err := rows.Scan(&sourceID, &amount, &currency, &dst); err != nil {
			return 0, fmt.Errorf("tier2 scan: %w", err)
		}
		if len(dst) != 1 {
			continue
		}
		pairs = append(pairs, pair{sourceID, dst[0], amount, currency})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, p := range pairs {
		// Compute fee = |srcAmount + destAmount|.
		var destAmount string
		if err := s.pool.QueryRow(ctx,
			`SELECT amount::text FROM transactions WHERE id = $1`, p.destID,
		).Scan(&destAmount); err != nil {
			return count, fmt.Errorf("tier2 read dest amount: %w", err)
		}
		srcD := decimal.RequireFromString(p.srcAmount)
		dstD := decimal.RequireFromString(destAmount)
		fee := srcD.Add(dstD).Abs()
		var feeAmount, feeCurrency *string
		if fee.GreaterThan(decimal.Zero) {
			feeStr := fee.String()
			feeAmount = &feeStr
			feeCurrency = &p.currency
		}
		_, err := s.pool.Exec(ctx, `
			INSERT INTO transfer_matches (
				id, workspace_id, source_transaction_id, destination_transaction_id,
				fee_amount, fee_currency, provenance, matched_at
			) VALUES (
				$1, $2, $3, $4,
				$5::numeric, $6::money_currency, 'auto_detected', $7
			)
			ON CONFLICT DO NOTHING
		`, uuidx.New(), workspaceID, p.sourceID, p.destID, feeAmount, feeCurrency, s.now().UTC())
		if err != nil {
			return count, fmt.Errorf("tier2 insert: %w", err)
		}
		count++
	}
	return count, nil
}
