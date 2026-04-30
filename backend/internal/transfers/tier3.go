package transfers

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// transferKeywords is the multi-locale list of transfer phrases that
// trigger Tier-3 review when found in counterparty_raw.
var transferKeywords = []string{
	// English
	"transfer", "pocket", "between accounts",
	// Portuguese
	"transferência", "carregamento", "levantamento",
	// German
	"überweisung", "ueberweisung", "umbuchung", "einzahlung", "abhebung", "zwischen konten",
}

// runTier3 surfaces unpaired credits whose counterparty_raw fuzzy-matches
// either a tracked account name in this workspace, the workspace owner's
// display name, or a transferKeywords entry. Inserts a
// transfer_match_candidates row per source. Returns the number of NEW
// pending rows inserted.
func (s *Service) runTier3(ctx context.Context, workspaceID uuid.UUID, scope DetectScope) (int, error) {
	keywords, err := s.buildTier3Keywords(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	if len(keywords) == 0 {
		return 0, nil
	}

	// Tier 3 cannot reuse scopeLeftFilter (it hardcodes $2, which we use
	// for the keyword text[] array). Inline the scope filter at $3 here.
	args := []any{workspaceID, keywords}
	leftFilter := ""
	if !scope.All && len(scope.TransactionIDs) > 0 {
		leftFilter = " AND t1.id = ANY($3)"
		args = append(args, scope.TransactionIDs)
	}

	q := `
		SELECT t1.id,
		       (SELECT array_agg(t2.id ORDER BY abs(t2.booked_at - t1.booked_at),
		                          abs(t2.amount::numeric + t1.amount::numeric))
		        FROM (
		          SELECT t2.id, t2.booked_at, t2.amount
		          FROM transactions t2
		          WHERE t2.workspace_id = t1.workspace_id
		            AND t2.account_id != t1.account_id
		            AND sign(t2.amount::numeric) != sign(t1.amount::numeric)
		            AND abs(t2.booked_at - t1.booked_at) <= 5
		            AND (t2.currency = t1.currency OR t2.original_currency = t1.currency)
		            AND NOT EXISTS (
		              SELECT 1 FROM transfer_matches tm
		              WHERE tm.workspace_id = t2.workspace_id
		                AND (tm.source_transaction_id = t2.id OR tm.destination_transaction_id = t2.id)
		            )
		          ORDER BY abs(t2.booked_at - t1.booked_at), abs(t2.amount::numeric + t1.amount::numeric)
		          LIMIT 5
		        ) t2
		       ) AS candidate_dst_ids
		FROM transactions t1
		WHERE t1.workspace_id = $1
		  AND t1.amount::numeric > 0
		  AND t1.counterparty_raw IS NOT NULL
		  AND lower(t1.counterparty_raw) LIKE ANY (
		    SELECT '%' || k || '%' FROM unnest($2::text[]) AS k
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM transfer_matches tm
		    WHERE tm.workspace_id = t1.workspace_id
		      AND (tm.source_transaction_id = t1.id OR tm.destination_transaction_id = t1.id)
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM transfer_match_candidates tmc
		    WHERE tmc.workspace_id = t1.workspace_id
		      AND tmc.source_transaction_id = t1.id
		  )
		  ` + leftFilter

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("tier3 query: %w", err)
	}
	defer rows.Close()

	type cand struct {
		sourceID uuid.UUID
		dstIDs   []uuid.UUID
	}
	var cands []cand
	for rows.Next() {
		var sourceID uuid.UUID
		var dst []uuid.UUID
		if err := rows.Scan(&sourceID, &dst); err != nil {
			return 0, err
		}
		if len(dst) == 0 {
			continue
		}
		cands = append(cands, cand{sourceID: sourceID, dstIDs: dst})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, c := range cands {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO transfer_match_candidates
			  (id, workspace_id, source_transaction_id, candidate_destination_ids, status)
			VALUES ($1, $2, $3, $4, 'pending')
			ON CONFLICT (workspace_id, source_transaction_id) DO NOTHING
		`, uuidx.New(), workspaceID, c.sourceID, c.dstIDs)
		if err != nil {
			return count, fmt.Errorf("tier3 insert: %w", err)
		}
		if tag.RowsAffected() > 0 {
			count++
		}
	}
	return count, nil
}

// buildTier3Keywords returns the lowercase keyword set: built-in transfer
// keywords ∪ tracked account names (lowercased) ∪ workspace owner display
// names / emails (lowercased).
func (s *Service) buildTier3Keywords(ctx context.Context, workspaceID uuid.UUID) ([]string, error) {
	keywords := append([]string{}, transferKeywords...)

	// Account names.
	rows, err := s.pool.Query(ctx,
		`SELECT lower(name) FROM accounts WHERE workspace_id = $1 AND archived_at IS NULL`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("tier3 list accounts: %w", err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		if strings.TrimSpace(n) != "" {
			keywords = append(keywords, n)
		}
	}
	rows.Close()

	// Workspace owners' display names / emails.
	ownerRows, err := s.pool.Query(ctx,
		`SELECT lower(coalesce(u.display_name, u.email::text))
		 FROM users u
		 JOIN workspace_memberships m ON m.user_id = u.id
		 WHERE m.workspace_id = $1 AND m.role = 'owner'`,
		workspaceID,
	)
	if err == nil {
		for ownerRows.Next() {
			var n string
			if err := ownerRows.Scan(&n); err != nil {
				ownerRows.Close()
				return nil, err
			}
			if strings.TrimSpace(n) != "" {
				keywords = append(keywords, n)
			}
		}
		ownerRows.Close()
	}
	return keywords, nil
}
