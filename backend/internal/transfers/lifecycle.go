package transfers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

const transferMatchCols = `
	id, workspace_id, source_transaction_id, destination_transaction_id,
	fx_rate::text, fee_amount::text, fee_currency::text, tolerance_note,
	provenance::text, matched_by_user_id, matched_at, created_at
`

func scanTransferMatch(r interface{ Scan(...any) error }, m *TransferMatch) error {
	return r.Scan(
		&m.ID, &m.WorkspaceID, &m.SourceTransactionID, &m.DestinationTransactionID,
		&m.FXRate, &m.FeeAmount, &m.FeeCurrency, &m.ToleranceNote,
		&m.Provenance, &m.MatchedByUserID, &m.MatchedAt, &m.CreatedAt,
	)
}

// ManualPair creates a manual transfer_matches row. DestinationID nil ⇒
// outbound-to-external. Closes any pending transfer_match_candidates row for
// the same source.
func (s *Service) ManualPair(ctx context.Context, workspaceID uuid.UUID, in ManualPairInput) (*TransferMatch, error) {
	if in.DestinationID != nil && in.SourceID == *in.DestinationID {
		return nil, httpx.NewValidationError("source and destination must differ")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin manual pair: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := assertExists(ctx, tx, workspaceID, in.SourceID); err != nil {
		return nil, err
	}
	if in.DestinationID != nil {
		if err := assertExists(ctx, tx, workspaceID, *in.DestinationID); err != nil {
			return nil, err
		}
	}
	if err := assertNotPaired(ctx, tx, workspaceID, in.SourceID, "source"); err != nil {
		return nil, err
	}
	if in.DestinationID != nil {
		if err := assertNotPaired(ctx, tx, workspaceID, *in.DestinationID, "destination"); err != nil {
			return nil, err
		}
	}

	id := uuidx.New()
	row := tx.QueryRow(ctx, `
		INSERT INTO transfer_matches (
			id, workspace_id, source_transaction_id, destination_transaction_id,
			fee_amount, fee_currency, tolerance_note, provenance, matched_at
		) VALUES (
			$1, $2, $3, $4, $5::numeric, $6::money_currency, $7, 'manual', now()
		)
		RETURNING `+transferMatchCols,
		id, workspaceID, in.SourceID, in.DestinationID, in.FeeAmount, in.FeeCurrency, in.ToleranceNote)
	var m TransferMatch
	if err := scanTransferMatch(row, &m); err != nil {
		return nil, fmt.Errorf("manual pair insert: %w", err)
	}

	// Close any pending candidate for this source.
	if _, err := tx.Exec(ctx, `
		UPDATE transfer_match_candidates
		SET status = 'paired', resolved_at = now()
		WHERE workspace_id = $1 AND source_transaction_id = $2 AND status = 'pending'
	`, workspaceID, in.SourceID); err != nil {
		return nil, fmt.Errorf("close candidate: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit manual pair: %w", err)
	}
	return &m, nil
}

func assertExists(ctx context.Context, tx pgx.Tx, workspaceID, txID uuid.UUID) error {
	var ok bool
	err := tx.QueryRow(ctx,
		`SELECT true FROM transactions WHERE workspace_id = $1 AND id = $2`,
		workspaceID, txID,
	).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("transaction")
	}
	return err
}

func assertNotPaired(ctx context.Context, tx pgx.Tx, workspaceID, txID uuid.UUID, role string) error {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM transfer_matches
			WHERE workspace_id = $1
			  AND (source_transaction_id = $2 OR destination_transaction_id = $2)
		)
	`, workspaceID, txID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check not-paired: %w", err)
	}
	if exists {
		code := "transfer_" + strings.ToLower(role) + "_already_paired"
		return httpx.NewConflictError(code, role+" is already part of another transfer pair", nil)
	}
	return nil
}

// Unpair removes a transfer_matches row by id. Returns NotFoundError if no
// row matches the workspace + id pair.
func (s *Service) Unpair(ctx context.Context, workspaceID, matchID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM transfer_matches WHERE workspace_id = $1 AND id = $2`,
		workspaceID, matchID,
	)
	if err != nil {
		return fmt.Errorf("unpair: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("transfer_match")
	}
	return nil
}

// ListPendingCandidates returns Tier-3 candidates with status='pending',
// newest first.
func (s *Service) ListPendingCandidates(ctx context.Context, workspaceID uuid.UUID) ([]TransferCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workspace_id, source_transaction_id, candidate_destination_ids,
		       status, suggested_at, resolved_at, resolved_by_user_id
		FROM transfer_match_candidates
		WHERE workspace_id = $1 AND status = 'pending'
		ORDER BY suggested_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()
	out := []TransferCandidate{}
	for rows.Next() {
		var c TransferCandidate
		if err := rows.Scan(
			&c.ID, &c.WorkspaceID, &c.SourceTransactionID, &c.CandidateDestinationIDs,
			&c.Status, &c.SuggestedAt, &c.ResolvedAt, &c.ResolvedByUserID,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountPendingCandidates returns the count of pending candidates for the
// dossier-tab badge.
func (s *Service) CountPendingCandidates(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM transfer_match_candidates
		 WHERE workspace_id = $1 AND status = 'pending'`,
		workspaceID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count candidates: %w", err)
	}
	return n, nil
}

// DeclineCandidate marks a pending candidate as declined. byUserID is
// optional. Returns NotFoundError if the candidate doesn't exist, or a
// ConflictError ("transfer_candidate_not_pending") if it isn't pending.
func (s *Service) DeclineCandidate(ctx context.Context, workspaceID, candidateID uuid.UUID, byUserID *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE transfer_match_candidates
		SET status = 'declined', resolved_at = now(), resolved_by_user_id = $3
		WHERE workspace_id = $1 AND id = $2 AND status = 'pending'
	`, workspaceID, candidateID, byUserID)
	if err != nil {
		return fmt.Errorf("decline candidate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var status string
		err := s.pool.QueryRow(ctx,
			`SELECT status FROM transfer_match_candidates WHERE workspace_id = $1 AND id = $2`,
			workspaceID, candidateID,
		).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("transfer_candidate")
		}
		if err != nil {
			return fmt.Errorf("decline lookup: %w", err)
		}
		return httpx.NewConflictError("transfer_candidate_not_pending", "candidate is not in pending status", nil)
	}
	return nil
}
