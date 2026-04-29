package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// ChangePassword verifies the current password, applies the password policy to
// the new password, hashes it, persists it, then invalidates every session for
// the user EXCEPT currentSessionID (so the user stays signed in here). Audits
// "user.password_changed" with no workspace.
//
// We treat next == current as a real change: the user explicitly asked for it,
// the policy already permits it, and refusing would force a special branch in
// the UI. Side effects (revocation, audit) still happen so a security-driven
// "rotate everything" workflow remains predictable.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentSessionID, current, next string) error {
	q := dbq.New(s.pool)

	// Pull email + display_name for the policy, and the password hash for verification.
	info, err := q.GetUserEmailAndDisplayName(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	currentHash, err := q.GetUserPasswordHash(ctx, userID)
	if err != nil {
		return fmt.Errorf("load password hash: %w", err)
	}

	ok, err := VerifyPassword(current, currentHash, s.cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("verify current password: %w", err)
	}
	if !ok {
		return httpx.NewValidationError("current password is incorrect")
	}

	if err := CheckPasswordPolicy(next, info.Email, info.DisplayName); err != nil {
		return err
	}

	newHash, err := HashPassword(next, s.cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Wrap the persist + revoke pair in a transaction so a partial failure
	// doesn't leave the new password active with stale sessions still valid.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := dbq.New(tx)
	if err := qtx.UpdateUserPassword(ctx, dbq.UpdateUserPasswordParams{
		PasswordHash: newHash, ID: userID,
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if err := qtx.DeleteOtherSessionsByUser(ctx, dbq.DeleteOtherSessionsByUserParams{
		UserID: userID, ID: currentSessionID,
	}); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Best-effort audit after the commit so a transient audit-table blip
	// doesn't block the rotation.
	s.WriteAudit(ctx, uuid.Nil, userID, "user.password_changed", "user", userID, nil, nil)
	return nil
}
