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

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// InviteLifetime is the validity window for a tenant invite token.
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

// InvitePreview is the payload for the no-auth preview endpoint — tenant
// name, inviter display name, role, expiry. No token / hash surfaced.
type InvitePreview struct {
	TenantID           uuid.UUID `json:"tenantId"`
	TenantName         string    `json:"tenantName"`
	TenantSlug         string    `json:"tenantSlug"`
	InviterDisplayName string    `json:"inviterDisplayName"`
	Email              string    `json:"email"`
	Role               Role      `json:"role"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

// InviteService owns writes to tenant_invites.
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
// duplicate pending invite to the same email in the same tenant.
func (s *InviteService) Create(
	ctx context.Context, tenantID, inviterID uuid.UUID, email string, role Role,
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

	// The partial unique index `tenant_invites_pending_email_unique` makes
	// the duplicate-check authoritative: two concurrent Create calls for the
	// same (tenant_id, email) pair can no longer both succeed.
	var inv Invite
	var roleText string
	err = s.pool.QueryRow(ctx, `
		insert into tenant_invites (id, tenant_id, email, role, token_hash,
		                            invited_by_user_id, expires_at)
		values ($1, $2, $3, $4::tenant_role, $5, $6, $7)
		returning id, tenant_id, email, role::text, invited_by_user_id,
		          created_at, expires_at
	`, id, tenantID, email, role, HashInviteToken(plaintext), inviterID, expiresAt).Scan(
		&inv.ID, &inv.TenantID, &inv.Email, &roleText, &inv.InvitedByUserID,
		&inv.CreatedAt, &inv.ExpiresAt,
	)
	if err != nil {
		if isPendingInviteUnique(err) {
			return nil, "", httpx.NewValidationError("a pending invite already exists for this email")
		}
		return nil, "", fmt.Errorf("insert invite: %w", err)
	}
	inv.Role = Role(roleText)
	return &inv, plaintext, nil
}

// isPendingInviteUnique reports whether err is a 23505 unique violation on
// the `tenant_invites_pending_email_unique` partial index added in
// 20260424000016_auth_hardening.sql.
func isPendingInviteUnique(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.Code == "23505" && pe.ConstraintName == "tenant_invites_pending_email_unique"
}

// Preview is a no-auth endpoint. Returns tenant name, inviter display name,
// role, and expiry — plus the invited email (so the UI can gate "sign up
// with a different email"). Omits token/hash.
func (s *InviteService) Preview(ctx context.Context, plaintext string) (*InvitePreview, error) {
	var p InvitePreview
	var roleText string
	var revokedAt, acceptedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		select i.tenant_id, t.name, t.slug, u.display_name,
		       i.email, i.role::text, i.expires_at, i.revoked_at, i.accepted_at
		from tenant_invites i
		join tenants t on t.id = i.tenant_id
		join users   u on u.id = i.invited_by_user_id
		where i.token_hash = $1 and t.deleted_at is null
	`, HashInviteToken(plaintext)).Scan(
		&p.TenantID, &p.TenantName, &p.TenantSlug, &p.InviterDisplayName,
		&p.Email, &roleText, &p.ExpiresAt, &revokedAt, &acceptedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	if revokedAt != nil {
		return nil, ErrInviteRevoked
	}
	if acceptedAt != nil {
		return nil, ErrInviteAlreadyUsed
	}
	if p.ExpiresAt.Before(s.now()) {
		return nil, ErrInviteExpired
	}
	p.Role = Role(roleText)
	return &p, nil
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

	var (
		inviteID    uuid.UUID
		tenantID    uuid.UUID
		inviteEmail string
		roleText    string
		expiresAt   time.Time
		revokedAt   *time.Time
		acceptedAt  *time.Time
	)
	err = tx.QueryRow(ctx, `
		select id, tenant_id, email, role::text, expires_at, revoked_at, accepted_at
		from tenant_invites
		where token_hash = $1
		for update
	`, HashInviteToken(plaintext)).Scan(&inviteID, &tenantID, &inviteEmail, &roleText,
		&expiresAt, &revokedAt, &acceptedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	if revokedAt != nil {
		return nil, ErrInviteRevoked
	}
	if acceptedAt != nil {
		return nil, ErrInviteAlreadyUsed
	}
	if expiresAt.Before(s.now()) {
		return nil, ErrInviteExpired
	}

	var userEmail string
	var verifiedAt *time.Time
	if err := tx.QueryRow(ctx,
		`select email, email_verified_at from users where id = $1`, userID).
		Scan(&userEmail, &verifiedAt); err != nil {
		return nil, err
	}
	if strings.ToLower(userEmail) != strings.ToLower(inviteEmail) {
		return nil, ErrInviteEmailMismatch
	}
	if verifiedAt == nil {
		return nil, ErrEmailUnverified
	}

	// Upsert-style: if user is already a member of this tenant, keep the
	// existing row and just consume the invite.
	if _, err := tx.Exec(ctx, `
		insert into tenant_memberships (tenant_id, user_id, role)
		values ($1, $2, $3::tenant_role)
		on conflict (tenant_id, user_id) do nothing
	`, tenantID, userID, roleText); err != nil {
		return nil, fmt.Errorf("insert membership: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`update tenant_invites set accepted_at = now() where id = $1`, inviteID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &Membership{
		TenantID:  tenantID,
		UserID:    userID,
		Role:      Role(roleText),
		CreatedAt: s.now(),
	}, nil
}

// Revoke marks an invite revoked. Authorised for the original inviter or any
// owner of the route tenant. Idempotent — revoking an already-revoked or
// accepted invite is a no-op (no error).
func (s *InviteService) Revoke(ctx context.Context, tenantID, inviteID, requesterUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var invitedBy uuid.UUID
	var revokedAt, acceptedAt *time.Time
	err = tx.QueryRow(ctx, `
		select invited_by_user_id, revoked_at, accepted_at
		from tenant_invites where id = $1 and tenant_id = $2 for update
	`, inviteID, tenantID).Scan(&invitedBy, &revokedAt, &acceptedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteNotFound
		}
		return err
	}
	if revokedAt != nil || acceptedAt != nil {
		return nil // idempotent
	}

	if invitedBy != requesterUserID {
		var isOwner bool
		if err := tx.QueryRow(ctx, `
			select exists(
				select 1 from tenant_memberships
				where tenant_id = $1 and user_id = $2 and role = 'owner'
			)
		`, tenantID, requesterUserID).Scan(&isOwner); err != nil {
			return err
		}
		if !isOwner {
			return ErrNotAuthorized
		}
	}

	if _, err := tx.Exec(ctx,
		`update tenant_invites set revoked_at = now() where id = $1`, inviteID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
