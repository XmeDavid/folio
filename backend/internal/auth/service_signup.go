package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// firstRunSignupLockKey serialises concurrent first-run signups. Picked once
// from /dev/urandom; the only contract is "stable across processes".
const firstRunSignupLockKey int64 = 0x46_4F_4C_49_4F_5F_46_52 // "FOLIO_FR"

// SignupInput is the validated input to Signup.
type SignupInput struct {
	Email          string
	Password       string
	DisplayName    string
	TenantName     string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
	InviteToken    string // plan 2 wires consumption; plan 1 ignores
	IP             net.IP
	UserAgent      string
}

func (in SignupInput) normalize() (SignupInput, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.TenantName = strings.TrimSpace(in.TenantName)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		return in, httpx.NewValidationError("email is required")
	}
	if in.DisplayName == "" {
		return in, httpx.NewValidationError("displayName is required")
	}
	if err := CheckPasswordPolicy(in.Password, in.Email, in.DisplayName); err != nil {
		return in, err
	}
	if in.TenantName == "" {
		fields := strings.Fields(in.DisplayName)
		first := in.DisplayName
		if len(fields) > 0 {
			first = fields[0]
		}
		in.TenantName = fmt.Sprintf("%s's Workspace", first)
	}
	if in.Locale == "" {
		in.Locale = "en-US"
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.CycleAnchorDay == 0 {
		in.CycleAnchorDay = 1
	}
	if in.CycleAnchorDay < 1 || in.CycleAnchorDay > 31 {
		return in, httpx.NewValidationError("cycleAnchorDay must be 1-31")
	}
	if in.BaseCurrency == "" {
		in.BaseCurrency = "USD"
	}
	cur, err := money.ParseCurrency(in.BaseCurrency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.BaseCurrency = string(cur)
	return in, nil
}

// SignupResult is returned by Signup.
type SignupResult struct {
	User         identity.User
	Tenant       identity.Tenant
	Membership   identity.Membership
	SessionToken string
}

// Signup creates a user, their Personal tenant, an owner membership, and a
// session — all in one transaction. Returns the plaintext session token for
// the handler to set in a cookie.
func (s *Service) Signup(ctx context.Context, raw SignupInput) (*SignupResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password, s.cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.enforceRegistrationModeTx(ctx, tx, in.InviteToken); err != nil {
		return nil, err
	}

	userID := uuidx.New()
	var user identity.User
	err = tx.QueryRow(ctx, `
		insert into users (id, email, password_hash, display_name)
		values ($1, $2, $3, $4)
		returning id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at
	`, userID, in.Email, hash, in.DisplayName).Scan(
		&user.ID, &user.Email, &user.DisplayName, &user.EmailVerifiedAt,
		&user.IsAdmin, &user.LastTenantID, &user.CreatedAt,
	)
	if err != nil {
		if isUsersEmailKey(err) {
			return nil, httpx.NewValidationError("that email is already registered")
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	tenantCI := identity.CreateTenantInput{
		Name: in.TenantName, BaseCurrency: in.BaseCurrency,
		CycleAnchorDay: in.CycleAnchorDay, Locale: in.Locale, Timezone: in.Timezone,
	}
	if _, err := tenantCI.Normalize(); err != nil {
		return nil, err
	}
	tenant, err := identity.InsertTenantTx(ctx, tx, uuidx.New(), tenantCI)
	if err != nil {
		return nil, err
	}
	membership, err := identity.InsertMembershipTx(ctx, tx, tenant.ID, userID, identity.RoleOwner)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `update users set last_tenant_id = $1 where id = $2`, tenant.ID, userID); err != nil {
		return nil, fmt.Errorf("set last_tenant_id: %w", err)
	}
	user.LastTenantID = &tenant.ID

	plaintext, _ := GenerateSessionToken()
	sid := SessionIDFromToken(plaintext)
	now := s.now().UTC()
	if _, err := tx.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		values ($1, $2, $3, $4, $3, $5, $6)
	`, sid, userID, now, now.Add(s.cfg.SessionAbsolute), in.UserAgent, ipString(in.IP)); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	// Audit: user.signup and tenant.created.
	if err := writeAuditTx(ctx, tx, &tenant.ID, &userID, "user.signup", "user", userID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	if err := writeAuditTx(ctx, tx, &tenant.ID, &userID, "tenant.created", "tenant", tenant.ID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	verifyPlaintext, verifyHash := GenerateSessionToken()
	verifyTokenID := uuidx.New()
	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, verifyTokenID, userID, purposeEmailVerify, verifyHash, user.Email, now.Add(verifyEmailTTL)); err != nil {
		return nil, fmt.Errorf("insert verify token: %w", err)
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "verify_email",
		ToAddress:      user.Email,
		IdempotencyKey: fmt.Sprintf("verify_email:%s", verifyTokenID),
		Data: map[string]any{
			"DisplayName": user.DisplayName,
			"VerifyURL":   s.cfg.AppURL + "/auth/verify/" + verifyPlaintext,
		},
	}); err != nil {
		return nil, err
	}

	// Consume an invite if one was supplied. The invite must match the
	// signup email (spec §4.2). Verification is bypassed on purpose: signing
	// up with an invite token sent to this email proves the address.
	if in.InviteToken != "" {
		var (
			inviteID     uuid.UUID
			invTenantID  uuid.UUID
			inviteEmail  string
			inviteRole   string
			inviteExpiry time.Time
			revokedAt    *time.Time
			acceptedAt   *time.Time
		)
		err := tx.QueryRow(ctx, `
			select id, tenant_id, email, role::text, expires_at, revoked_at, accepted_at
			from tenant_invites where token_hash = $1 for update
		`, identity.HashInviteToken(in.InviteToken)).Scan(&inviteID, &invTenantID, &inviteEmail,
			&inviteRole, &inviteExpiry, &revokedAt, &acceptedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, identity.ErrInviteNotFound
			}
			return nil, fmt.Errorf("select invite: %w", err)
		}
		if revokedAt != nil {
			return nil, identity.ErrInviteRevoked
		}
		if acceptedAt != nil {
			return nil, identity.ErrInviteAlreadyUsed
		}
		if inviteExpiry.Before(s.now()) {
			return nil, identity.ErrInviteExpired
		}
		if strings.ToLower(inviteEmail) != strings.ToLower(in.Email) {
			return nil, identity.ErrInviteEmailMismatch
		}
		if _, err := tx.Exec(ctx, `
			insert into tenant_memberships (tenant_id, user_id, role)
			values ($1, $2, $3::tenant_role)
		`, invTenantID, userID, inviteRole); err != nil {
			return nil, fmt.Errorf("insert invited membership: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`update tenant_invites set accepted_at = now() where id = $1`, inviteID); err != nil {
			return nil, fmt.Errorf("consume invite: %w", err)
		}
		if err := writeAuditTx(ctx, tx, &invTenantID, &userID, "member.invite_accepted",
			"invite", inviteID, nil, map[string]any{"role": inviteRole, "email": inviteEmail},
			in.IP, in.UserAgent); err != nil {
			return nil, err
		}
	}

	if s.cfg.AdminBootstrapHook != nil {
		if err := s.cfg.AdminBootstrapHook(ctx, tx, user.ID, user.Email); err != nil {
			return nil, fmt.Errorf("admin bootstrap: %w", err)
		}
		// Keep the returned user in sync with the in-tx grant so the first
		// signup response reflects is_admin=true without a refetch.
		if err := tx.QueryRow(ctx, `select is_admin from users where id = $1`, user.ID).Scan(&user.IsAdmin); err != nil {
			return nil, fmt.Errorf("refresh is_admin: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &SignupResult{User: user, Tenant: tenant, Membership: membership, SessionToken: plaintext}, nil
}

// enforceRegistrationModeTx runs inside the signup transaction so the
// first-run "is this the first user?" check sees a consistent snapshot,
// guarded by an advisory lock that serialises concurrent first-run signups.
// invite_only allows the first-ever user to bootstrap the instance; after
// that it requires a non-empty token. Token validity is verified later in the
// same transaction.
func (s *Service) enforceRegistrationModeTx(ctx context.Context, tx pgx.Tx, inviteToken string) error {
	switch s.cfg.Registration {
	case RegistrationOpen:
		return nil
	case RegistrationFirstRunOnly:
		exists, err := s.userExistsForRegistrationTx(ctx, tx)
		if err != nil {
			return err
		}
		if exists {
			return httpx.NewValidationError("registration is closed on this instance")
		}
		return nil
	case RegistrationInviteOnly:
		if strings.TrimSpace(inviteToken) == "" {
			exists, err := s.userExistsForRegistrationTx(ctx, tx)
			if err != nil {
				return err
			}
			if exists {
				return httpx.NewValidationError("invite-only mode: signup requires an invite token")
			}
		}
		return nil
	default:
		return errors.New("unknown registration mode")
	}
}

func (s *Service) userExistsForRegistrationTx(ctx context.Context, tx pgx.Tx) (bool, error) {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, firstRunSignupLockKey); err != nil {
		return false, fmt.Errorf("first-run lock: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("first-run check: %w", err)
	}
	return exists, nil
}

func isUsersEmailKey(err error) bool {
	// *pgconn.PgError exposes Code and ConstraintName as fields (not methods),
	// so an interface-based errors.As would never match. Unwrap to the concrete
	// type instead.
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.Code == "23505" && pe.ConstraintName == "users_email_key"
}

// ipString renders an IP for storage in `sessions.ip` / `audit_events.ip`
// (both `inet` columns). Returns nil for a nil IP so pgx writes SQL NULL —
// returning "" produces a malformed-inet error from Postgres.
func ipString(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}
