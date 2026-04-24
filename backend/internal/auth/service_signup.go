package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

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
	if err := s.enforceRegistrationMode(ctx); err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &SignupResult{User: user, Tenant: tenant, Membership: membership, SessionToken: plaintext}, nil
}

func (s *Service) enforceRegistrationMode(ctx context.Context) error {
	switch s.cfg.Registration {
	case RegistrationOpen:
		return nil
	case RegistrationFirstRunOnly:
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from users)`).Scan(&exists); err != nil {
			return fmt.Errorf("first-run check: %w", err)
		}
		if exists {
			return httpx.NewValidationError("registration is closed on this instance")
		}
		return nil
	case RegistrationInviteOnly:
		// Plan 2 wires inviteToken consumption; for plan 1, reject.
		return httpx.NewValidationError("invite-only mode: signup requires an invite token")
	default:
		return errors.New("unknown registration mode")
	}
}

func isUsersEmailKey(err error) bool {
	type pgErr interface {
		SQLState() string
		ConstraintName() string
	}
	var pe pgErr
	if !errors.As(err, &pe) {
		return false
	}
	return pe.SQLState() == "23505" && pe.ConstraintName() == "users_email_key"
}

// ipString renders an IP for storage in `sessions.ip` / `audit_events.ip`.
// Empty IP → empty string (which pgx coerces to SQL NULL when column is inet).
func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

// ensure these are used to avoid unused-import complaints from refactors.
var _ = uuid.Nil
var _ pgx.Tx = nil
