package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// InviteLifetime is the validity window for a workspace invite token.
const InviteLifetime = 7 * 24 * time.Hour

// Sentinel errors for the invite flow.
var (
	ErrInviteNotFound      = errors.New("invite: not found")
	ErrInviteExpired       = errors.New("invite: expired")
	ErrInviteRevoked       = errors.New("invite: revoked")
	ErrInviteAlreadyUsed   = errors.New("invite: already accepted")
	ErrInviteEmailMismatch = errors.New("invite: email does not match authenticated user")
	ErrEmailUnverified     = errors.New("invite: user email not verified")
	ErrNotAuthorized       = errors.New("invite: not authorised to revoke")
)

// InvitePreview is the payload for the no-auth preview endpoint — workspace
// name, inviter display name, role, expiry. No token / hash surfaced.
type InvitePreview struct {
	WorkspaceID        uuid.UUID `json:"workspaceId"`
	WorkspaceName      string    `json:"workspaceName"`
	WorkspaceSlug      string    `json:"workspaceSlug"`
	InviterDisplayName string    `json:"inviterDisplayName"`
	Email              string    `json:"email"`
	Role               Role      `json:"role"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

// InviteService owns writes to workspace_invites.
type InviteService struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewInviteService constructs an InviteService backed by pool.
func NewInviteService(pool *pgxpool.Pool) *InviteService {
	return &InviteService{pool: pool, now: time.Now}
}

func HashInviteToken(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

func generateInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create issues a new invite. Returns the row + the plaintext token (shown
// to callers only once — the caller emails it via mailer.Mailer). Blocks a
// duplicate pending invite to the same email in the same workspace.
func (s *InviteService) Create(
	ctx context.Context, workspaceID, inviterID uuid.UUID, email string, role Role,
) (*Invite, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, "", httpx.NewValidationError("email is required and must look like an email")
	}
	if !role.Valid() {
		return nil, "", httpx.NewValidationError("role must be 'owner' or 'member'")
	}

	plaintext, err := generateInviteToken()
	if err != nil {
		return nil, "", fmt.Errorf("rand: %w", err)
	}
	id := uuidx.New()
	expiresAt := s.now().Add(InviteLifetime)

	// The partial unique index `workspace_invites_pending_email_unique` makes
	// the duplicate-check authoritative: two concurrent Create calls for the
	// same (workspace_id, email) pair can no longer both succeed.
	row, err := dbq.New(s.pool).InsertWorkspaceInvite(ctx, dbq.InsertWorkspaceInviteParams{
		ID:              id,
		WorkspaceID:     workspaceID,
		Email:           email,
		Column4:         dbq.WorkspaceRole(role),
		TokenHash:       HashInviteToken(plaintext),
		InvitedByUserID: inviterID,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		if isPendingInviteUnique(err) {
			return nil, "", httpx.NewValidationError("a pending invite already exists for this email")
		}
		return nil, "", fmt.Errorf("insert invite: %w", err)
	}
	inv := &Invite{
		ID:              row.ID,
		WorkspaceID:     row.WorkspaceID,
		Email:           row.Email,
		Role:            Role(row.Role),
		InvitedByUserID: row.InvitedByUserID,
		CreatedAt:       row.CreatedAt,
		ExpiresAt:       row.ExpiresAt,
	}
	return inv, plaintext, nil
}

// isPendingInviteUnique reports whether err is a 23505 unique violation on
// the `workspace_invites_pending_email_unique` partial index added in
// 20260424000016_auth_hardening.sql.
func isPendingInviteUnique(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.Code == "23505" && pe.ConstraintName == "workspace_invites_pending_email_unique"
}

// Preview is a no-auth endpoint. Returns workspace name, inviter display name,
// role, and expiry — plus the invited email (so the UI can gate "sign up
// with a different email"). Omits token/hash.
func (s *InviteService) Preview(ctx context.Context, plaintext string) (*InvitePreview, error) {
	row, err := dbq.New(s.pool).GetInvitePreview(ctx, HashInviteToken(plaintext))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	if row.RevokedAt != nil {
		return nil, ErrInviteRevoked
	}
	if row.AcceptedAt != nil {
		return nil, ErrInviteAlreadyUsed
	}
	if row.ExpiresAt.Before(s.now()) {
		return nil, ErrInviteExpired
	}
	p := &InvitePreview{
		WorkspaceID:        row.WorkspaceID,
		WorkspaceName:      row.WorkspaceName,
		WorkspaceSlug:      row.WorkspaceSlug,
		InviterDisplayName: row.InviterDisplayName,
		Email:              row.Email,
		Role:               Role(row.Role),
		ExpiresAt:          row.ExpiresAt,
	}
	return p, nil
}

// Accept consumes the invite on behalf of userID. Requires the user's email
// to match the invite's, the email to be verified, and the invite to be
// active. Creates the membership + marks the invite accepted in a single
// transaction. Idempotent on the membership (already-member -> invite is
// still consumed).
func (s *InviteService) Accept(ctx context.Context, plaintext string, userID uuid.UUID) (*Membership, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	inv, err := q.GetInviteForAccept(ctx, HashInviteToken(plaintext))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	if inv.RevokedAt != nil {
		return nil, ErrInviteRevoked
	}
	if inv.AcceptedAt != nil {
		return nil, ErrInviteAlreadyUsed
	}
	if inv.ExpiresAt.Before(s.now()) {
		return nil, ErrInviteExpired
	}

	uRow, err := q.GetUserEmailAndVerification(ctx, userID)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(uRow.Email) != strings.ToLower(inv.Email) {
		return nil, ErrInviteEmailMismatch
	}
	if uRow.EmailVerifiedAt == nil {
		return nil, ErrEmailUnverified
	}

	// Upsert-style: if user is already a member of this workspace, keep the
	// existing row and just consume the invite.
	if err := q.UpsertMembershipOnInvite(ctx, dbq.UpsertMembershipOnInviteParams{
		WorkspaceID: inv.WorkspaceID,
		UserID:      userID,
		Column3:     dbq.WorkspaceRole(inv.Role),
	}); err != nil {
		return nil, fmt.Errorf("insert membership: %w", err)
	}

	if err := q.MarkInviteAccepted(ctx, inv.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &Membership{
		WorkspaceID: inv.WorkspaceID,
		UserID:      userID,
		Role:        Role(inv.Role),
		CreatedAt:   s.now(),
	}, nil
}

// Resend rotates the invite's token and extends the expiry, returning the
// fresh row + the new plaintext token so the caller can re-send the email
// and surface a copy link in the UI. Resending DOES NOT reject expired
// invites — the whole point is to extend a stale-but-not-yet-revoked
// invite by issuing a new token + expiry. Returns:
//   - ErrInviteNotFound if the invite doesn't exist or doesn't belong to
//     the route workspace.
//   - ErrInviteRevoked if revoked_at is set.
//   - ErrInviteAlreadyUsed if accepted_at is set.
func (s *InviteService) Resend(
	ctx context.Context, workspaceID, inviteID uuid.UUID,
) (*Invite, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	row, err := q.GetInviteForResend(ctx, dbq.GetInviteForResendParams{
		ID:          inviteID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrInviteNotFound
		}
		return nil, "", err
	}
	if row.RevokedAt != nil {
		return nil, "", ErrInviteRevoked
	}
	if row.AcceptedAt != nil {
		return nil, "", ErrInviteAlreadyUsed
	}

	plaintext, err := generateInviteToken()
	if err != nil {
		return nil, "", fmt.Errorf("rand: %w", err)
	}
	expiresAt := s.now().Add(InviteLifetime)

	updated, err := q.RotateWorkspaceInviteToken(ctx, dbq.RotateWorkspaceInviteTokenParams{
		ID:        inviteID,
		TokenHash: HashInviteToken(plaintext),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		// The WHERE clause includes revoked_at/accepted_at IS NULL, so a
		// concurrent revoke/accept between the SELECT FOR UPDATE and the
		// UPDATE would surface as ErrNoRows. Treat as the appropriate
		// already-consumed sentinel by re-reading state — but since we hold
		// the row lock for the duration of the tx, this should never fire.
		// Surface as ErrInviteNotFound so callers see a stable shape.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrInviteNotFound
		}
		return nil, "", fmt.Errorf("rotate invite token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}

	return &Invite{
		ID:              updated.ID,
		WorkspaceID:     updated.WorkspaceID,
		Email:           updated.Email,
		Role:            Role(updated.Role),
		InvitedByUserID: updated.InvitedByUserID,
		CreatedAt:       updated.CreatedAt,
		ExpiresAt:       updated.ExpiresAt,
	}, plaintext, nil
}

// Revoke marks an invite revoked. Authorised for the original inviter or any
// owner of the route workspace. Idempotent — revoking an already-revoked or
// accepted invite is a no-op (no error).
func (s *InviteService) Revoke(ctx context.Context, workspaceID, inviteID, requesterUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	row, err := q.GetInviteForRevoke(ctx, dbq.GetInviteForRevokeParams{
		ID:          inviteID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		return err
	}
	if row.RevokedAt != nil || row.AcceptedAt != nil {
		return nil // idempotent
	}

	if row.InvitedByUserID != requesterUserID {
		isOwner, err := q.CheckIsWorkspaceOwner(ctx, dbq.CheckIsWorkspaceOwnerParams{
			WorkspaceID: workspaceID,
			UserID:      requesterUserID,
		})
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotAuthorized
		}
	}

	if err := q.MarkInviteRevoked(ctx, inviteID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
