package classification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// AddTransactionTag creates (or no-ops on) the transaction_tags row for
// (transactionID, tagID) scoped to workspaceID. Returns NotFoundError if either
// the transaction or tag is missing for the workspace, ValidationError on any
// FK violation from the DB. Idempotent: re-applying an existing tag is a
// no-op and returns nil.
func (s *Service) AddTransactionTag(ctx context.Context, workspaceID, transactionID, tagID uuid.UUID) error {
	if err := s.assertTransactionExists(ctx, workspaceID, transactionID); err != nil {
		return err
	}
	if err := s.assertTagExists(ctx, workspaceID, tagID); err != nil {
		return err
	}
	// composite FKs guarantee cross-workspace safety; do nothing on conflict
	// (idempotent apply).
	if err := dbq.New(s.pool).InsertTransactionTag(ctx, dbq.InsertTransactionTagParams{
		TransactionID: transactionID,
		TagID:         tagID,
		WorkspaceID:   workspaceID,
	}); err != nil {
		return mapWriteError("transaction_tag", err)
	}
	return nil
}

// RemoveTransactionTag deletes the transaction_tags row idempotently. No
// error when the pairing does not exist. NotFoundError when the transaction
// itself does not exist for workspace — we validate the transaction so callers
// get a clear 404 on a bad path id, while the tag side stays forgiving to
// match other delete/archive operations.
func (s *Service) RemoveTransactionTag(ctx context.Context, workspaceID, transactionID, tagID uuid.UUID) error {
	if err := s.assertTransactionExists(ctx, workspaceID, transactionID); err != nil {
		return err
	}
	if err := dbq.New(s.pool).DeleteTransactionTag(ctx, dbq.DeleteTransactionTagParams{
		WorkspaceID:   workspaceID,
		TransactionID: transactionID,
		TagID:         tagID,
	}); err != nil {
		return fmt.Errorf("delete transaction_tag: %w", err)
	}
	return nil
}

func (s *Service) assertTransactionExists(ctx context.Context, workspaceID, txID uuid.UUID) error {
	_, err := dbq.New(s.pool).TransactionExists(ctx, dbq.TransactionExistsParams{
		WorkspaceID: workspaceID,
		ID:          txID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("transaction")
		}
		return fmt.Errorf("check transaction: %w", err)
	}
	return nil
}

func (s *Service) assertTagExists(ctx context.Context, workspaceID, tagID uuid.UUID) error {
	_, err := dbq.New(s.pool).TagExists(ctx, dbq.TagExistsParams{
		WorkspaceID: workspaceID,
		ID:          tagID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("tag")
		}
		return fmt.Errorf("check tag: %w", err)
	}
	return nil
}
