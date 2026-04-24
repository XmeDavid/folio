package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

var ErrLastAdmin = errors.New("cannot revoke the last admin")

func (s *Service) GrantAdmin(ctx context.Context, userID, actorUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var already bool
	err = tx.QueryRow(ctx, `select is_admin from users where id = $1 for update`, userID).Scan(&already)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("user")
	}
	if err != nil {
		return err
	}
	if already {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `update users set is_admin = true, updated_at = now() where id = $1`, userID); err != nil {
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

	var isAdmin bool
	err = tx.QueryRow(ctx, `select is_admin from users where id = $1 for update`, userID).Scan(&isAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("user")
	}
	if err != nil {
		return err
	}
	if !isAdmin {
		return tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `select id from users where is_admin = true order by id for update`)
	if err != nil {
		return err
	}
	var adminCount int
	for rows.Next() {
		adminCount++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if adminCount <= 1 {
		return ErrLastAdmin
	}
	if _, err := tx.Exec(ctx, `update users set is_admin = false, updated_at = now() where id = $1`, userID); err != nil {
		return err
	}
	if err := writeAdminAudit(ctx, tx, "admin.revoked", actorUserID, "user", userID, map[string]any{"is_admin": true}, map[string]any{"is_admin": false}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
