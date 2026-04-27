package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

var ErrLastAdmin = errors.New("cannot revoke the last admin")

func (s *Service) GrantAdmin(ctx context.Context, userID, actorUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	already, err := q.AdminGetUserIsAdminForUpdate(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("user")
	}
	if err != nil {
		return err
	}
	if already {
		return tx.Commit(ctx)
	}
	if err := q.AdminSetUserAdmin(ctx, dbq.AdminSetUserAdminParams{IsAdmin: true, ID: userID}); err != nil {
		return err
	}
	if err := writeAdminAudit(ctx, tx, "admin.granted", actorUserID, "user", userID, nil, map[string]any{"is_admin": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) RevokeAdmin(ctx context.Context, userID, actorUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	isAdmin, err := q.AdminGetUserIsAdminForUpdate(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("user")
	}
	if err != nil {
		return err
	}
	if !isAdmin {
		return tx.Commit(ctx)
	}
	adminCount, err := q.AdminCountAdmins(ctx)
	if err != nil {
		return err
	}
	if adminCount <= 1 {
		return ErrLastAdmin
	}
	if err := q.AdminSetUserAdmin(ctx, dbq.AdminSetUserAdminParams{IsAdmin: false, ID: userID}); err != nil {
		return err
	}
	if err := writeAdminAudit(ctx, tx, "admin.revoked", actorUserID, "user", userID, map[string]any{"is_admin": true}, map[string]any{"is_admin": false}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
