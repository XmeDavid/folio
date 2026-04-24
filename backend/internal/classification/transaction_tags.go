package classification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// AddTransactionTag creates (or no-ops on) the transaction_tags row for
// (transactionID, tagID) scoped to tenantID. Returns NotFoundError if either
// the transaction or tag is missing for the tenant, ValidationError on any
// FK violation from the DB. Idempotent: re-applying an existing tag is a
// no-op and returns nil.
func (s *Service) AddTransactionTag(ctx context.Context, tenantID, transactionID, tagID uuid.UUID) error {
	if err := s.assertTransactionExists(ctx, tenantID, transactionID); err != nil {
		return err
	}
	if err := s.assertTagExists(ctx, tenantID, tagID); err != nil {
		return err
	}
	// composite FKs guarantee cross-tenant safety; do nothing on conflict
	// (idempotent apply).
	_, err := s.pool.Exec(ctx, `
		insert into transaction_tags (transaction_id, tag_id, tenant_id)
		values ($1, $2, $3)
		on conflict (transaction_id, tag_id) do nothing
	`, transactionID, tagID, tenantID)
	if err != nil {
		return mapWriteError("transaction_tag", err)
	}
	return nil
}

// RemoveTransactionTag deletes the transaction_tags row idempotently. No
// error when the pairing does not exist. NotFoundError when the transaction
// itself does not exist for tenant — we validate the transaction so callers
// get a clear 404 on a bad path id, while the tag side stays forgiving to
// match other delete/archive operations.
func (s *Service) RemoveTransactionTag(ctx context.Context, tenantID, transactionID, tagID uuid.UUID) error {
	if err := s.assertTransactionExists(ctx, tenantID, transactionID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		delete from transaction_tags
		where tenant_id = $1 and transaction_id = $2 and tag_id = $3
	`, tenantID, transactionID, tagID)
	if err != nil {
		return fmt.Errorf("delete transaction_tag: %w", err)
	}
	return nil
}

func (s *Service) assertTransactionExists(ctx context.Context, tenantID, txID uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`select true from transactions where tenant_id = $1 and id = $2`,
		tenantID, txID).Scan(&ok)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("transaction")
		}
		return fmt.Errorf("check transaction: %w", err)
	}
	return nil
}

func (s *Service) assertTagExists(ctx context.Context, tenantID, tagID uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`select true from tags where tenant_id = $1 and id = $2`,
		tenantID, tagID).Scan(&ok)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("tag")
		}
		return fmt.Errorf("check tag: %w", err)
	}
	return nil
}
