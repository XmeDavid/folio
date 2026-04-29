package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// PlatformInviteLifetime is the validity window for a platform admin invite.
const PlatformInviteLifetime = 14 * 24 * time.Hour

// PlatformInvite is the domain model for a platform-level admin-issued invite.
// Email is nullable (open invite). The plaintext token is never stored or
// returned by reads — only by Create.
type PlatformInvite struct {
	ID         uuid.UUID  `json:"id"`
	Email      *string    `json:"email"`
	CreatedBy  uuid.UUID  `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	AcceptedBy *uuid.UUID `json:"acceptedBy,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	RevokedBy  *uuid.UUID `json:"revokedBy,omitempty"`
}

// PlatformInvitePreview is the no-auth preview payload. Intentionally lean —
// admin invites are deliberately low-context and do not surface the inviter.
type PlatformInvitePreview struct {
	Email     *string   `json:"email"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// PlatformInviteService owns writes to platform_invites.
type PlatformInviteService struct {
	pool *pgxpool.Pool
	now  func() time.Time
	ttl  time.Duration
}

// NewPlatformInviteService constructs a PlatformInviteService backed by pool.
func NewPlatformInviteService(pool *pgxpool.Pool) *PlatformInviteService {
	return &PlatformInviteService{
		pool: pool,
		now:  time.Now,
		ttl:  PlatformInviteLifetime,
	}
}

// Create issues a new platform invite. Returns the row and the plaintext
// token (shown to the admin only once). An empty / whitespace-only email is
// stored as NULL — an "open" invite that can be redeemed by any signup.
func (s *PlatformInviteService) Create(
	ctx context.Context, createdBy uuid.UUID, email string,
) (PlatformInvite, string, error) {
	plaintext, err := generateInviteToken()
	if err != nil {
		return PlatformInvite{}, "", fmt.Errorf("rand: %w", err)
	}

	var emailPtr *string
	if trimmed := strings.ToLower(strings.TrimSpace(email)); trimmed != "" {
		if !strings.Contains(trimmed, "@") {
			return PlatformInvite{}, "", httpx.NewValidationError("email must contain @ if provided")
		}
		emailPtr = &trimmed
	}

	id := uuidx.New()
	expiresAt := s.now().Add(s.ttl)

	row, err := dbq.New(s.pool).InsertPlatformInvite(ctx, dbq.InsertPlatformInviteParams{
		ID:        id,
		Email:     emailPtr,
		TokenHash: HashInviteToken(plaintext),
		CreatedBy: createdBy,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return PlatformInvite{}, "", fmt.Errorf("insert platform invite: %w", err)
	}

	return platformInviteFromRow(row), plaintext, nil
}

// Preview is a no-auth lookup. Returns sanitized info or an invite sentinel.
// Does not lock the row — this is read-only.
func (s *PlatformInviteService) Preview(ctx context.Context, plaintext string) (PlatformInvitePreview, error) {
	row, err := dbq.New(s.pool).GetPlatformInviteByTokenHash(ctx, HashInviteToken(plaintext))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformInvitePreview{}, ErrInviteNotFound
		}
		return PlatformInvitePreview{}, err
	}
	if row.RevokedAt != nil {
		return PlatformInvitePreview{}, ErrInviteRevoked
	}
	if row.AcceptedAt != nil {
		return PlatformInvitePreview{}, ErrInviteAlreadyUsed
	}
	if row.ExpiresAt.Before(s.now()) {
		return PlatformInvitePreview{}, ErrInviteExpired
	}
	return PlatformInvitePreview{
		Email:     row.Email,
		ExpiresAt: row.ExpiresAt,
	}, nil
}

// Revoke marks an invite revoked. Authorisation (admin-only) is enforced at
// the HTTP layer; this service trusts the caller.
//
// Returns:
//   - ErrInviteNotFound when the row does not exist;
//   - ErrInviteAlreadyUsed when the invite has already been accepted;
//   - ErrInviteRevoked when the invite is already revoked.
//
// Note: unlike workspace InviteService.Revoke (which is idempotent), this
// returns errors on terminal-state collisions so the admin UI can surface
// "already revoked / already accepted" feedback rather than silently no-op.
func (s *PlatformInviteService) Revoke(ctx context.Context, id, by uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	row, err := q.GetPlatformInviteForRevoke(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		return err
	}
	if row.AcceptedAt != nil {
		return ErrInviteAlreadyUsed
	}
	if row.RevokedAt != nil {
		return ErrInviteRevoked
	}

	if err := q.MarkPlatformInviteRevoked(ctx, dbq.MarkPlatformInviteRevokedParams{
		ID:        id,
		RevokedBy: &by,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcceptTx consumes a platform invite as part of an existing transaction
// (the signup flow opens its own transaction and threads the dbq.DBTX in).
//
// Validation:
//   - ErrInviteNotFound when no row matches;
//   - ErrInviteRevoked / ErrInviteAlreadyUsed / ErrInviteExpired on state;
//   - ErrInviteEmailMismatch when the invite has an email and it does not
//     match signupEmail (case-insensitive). Open invites (NULL email) accept
//     any signupEmail.
//
// Caller is responsible for committing / rolling back tx.
func (s *PlatformInviteService) AcceptTx(
	ctx context.Context, tx dbq.DBTX, plaintext, signupEmail string, userID uuid.UUID,
) error {
	q := dbq.New(tx)
	row, err := q.GetPlatformInviteForAccept(ctx, HashInviteToken(plaintext))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		return err
	}
	if row.RevokedAt != nil {
		return ErrInviteRevoked
	}
	if row.AcceptedAt != nil {
		return ErrInviteAlreadyUsed
	}
	if row.ExpiresAt.Before(s.now()) {
		return ErrInviteExpired
	}
	if row.Email != nil {
		if !strings.EqualFold(strings.TrimSpace(*row.Email), strings.TrimSpace(signupEmail)) {
			return ErrInviteEmailMismatch
		}
	}

	return q.MarkPlatformInviteAccepted(ctx, dbq.MarkPlatformInviteAcceptedParams{
		ID:         row.ID,
		AcceptedBy: &userID,
	})
}

// ListActive returns all non-expired, non-revoked, non-accepted invites,
// ordered by created_at desc — for the admin UI.
func (s *PlatformInviteService) ListActive(ctx context.Context) ([]PlatformInvite, error) {
	rows, err := dbq.New(s.pool).ListPlatformInvitesActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PlatformInvite, 0, len(rows))
	for _, r := range rows {
		out = append(out, platformInviteFromRow(r))
	}
	return out, nil
}

func platformInviteFromRow(row dbq.PlatformInvite) PlatformInvite {
	return PlatformInvite{
		ID:         row.ID,
		Email:      row.Email,
		CreatedBy:  row.CreatedBy,
		CreatedAt:  row.CreatedAt,
		ExpiresAt:  row.ExpiresAt,
		AcceptedAt: row.AcceptedAt,
		AcceptedBy: row.AcceptedBy,
		RevokedAt:  row.RevokedAt,
		RevokedBy:  row.RevokedBy,
	}
}
