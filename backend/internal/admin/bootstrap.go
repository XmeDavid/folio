package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
)

// EnsureBootstrapAdminTx grants the ADMIN_BOOTSTRAP_EMAIL user is_admin
// inside the caller's transaction. Running inside the signup tx means a
// grant failure rolls back the whole signup rather than leaving an
// un-granted user behind.
func (s *Service) EnsureBootstrapAdminTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, email string) error {
	target := strings.ToLower(strings.TrimSpace(s.getEnv("ADMIN_BOOTSTRAP_EMAIL")))
	if target == "" || strings.ToLower(strings.TrimSpace(email)) != target {
		return nil
	}
	q := dbq.New(tx)
	already, err := q.AdminGetUserIsAdminForUpdate(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if already {
		return nil
	}
	if err := q.AdminSetUserAdmin(ctx, dbq.AdminSetUserAdminParams{IsAdmin: true, ID: userID}); err != nil {
		return err
	}
	return writeAdminAudit(ctx, tx, "admin.bootstrap_granted", uuid.Nil, "user", userID, nil, map[string]any{"is_admin": true})
}
