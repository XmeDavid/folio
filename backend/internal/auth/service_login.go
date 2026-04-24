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
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// LoginInput is the validated input to Login.
type LoginInput struct {
	Email     string
	Password  string
	IP        net.IP
	UserAgent string
}

func (in LoginInput) normalize() (LoginInput, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		return in, httpx.NewValidationError("email is required")
	}
	if in.Password == "" {
		return in, httpx.NewValidationError("password is required")
	}
	return in, nil
}

// LoginResult is returned by Login. Plan 4 replaces the hard-coded
// MFARequired=false with real MFA branching.
type LoginResult struct {
	User         identity.User
	SessionToken string
	MFARequired  bool
	ChallengeID  string
}

// ErrInvalidCredentials is returned on bad email or password.
var ErrInvalidCredentials = errors.New("invalid email or password")

// dummyHash is a pre-computed Argon2id hash used as a constant-time decoy
// for unknown-email login paths.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$Zm9saW8tZHVtbXktc2FsdA$yV+7m0L+QyU0FnQ4I3o5l2XcVxDmxRhQWdJGaNmT5lU"

// Login verifies credentials and issues a session. Plan 4 will branch here
// when the user has MFA enrolled.
func (s *Service) Login(ctx context.Context, raw LoginInput) (*LoginResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	var hash string
	var user identity.User
	err = s.pool.QueryRow(ctx, `
		select id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at, password_hash
		from users where email = $1
	`, in.Email).Scan(&user.ID, &user.Email, &user.DisplayName, &user.EmailVerifiedAt,
		&user.IsAdmin, &user.LastTenantID, &user.CreatedAt, &hash)
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword("dummy", dummyHash)
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("select user: %w", err)
	}
	ok, err := VerifyPassword(in.Password, hash)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}

	plaintext, _ := GenerateSessionToken()
	sid := SessionIDFromToken(plaintext)
	now := s.now().UTC()
	_, err = s.pool.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		values ($1,$2,$3,$4,$3,$5,$6)
	`, sid, user.ID, now, now.Add(s.cfg.SessionAbsolute), in.UserAgent, ipString(in.IP))
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	_, _ = s.pool.Exec(ctx, `update users set last_login_at = $1 where id = $2`, now, user.ID)
	s.logAuditDirect(ctx, user.LastTenantID, &user.ID, "user.login_succeeded", "user", user.ID, in.IP, in.UserAgent)

	return &LoginResult{User: user, SessionToken: plaintext, MFARequired: false}, nil
}

func (s *Service) logLoginFailed(ctx context.Context, email string, ip net.IP, ua string) {
	// entity_id is required non-null; we use a fresh uuid as a placeholder for the failed-email event.
	_, _ = s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, after_jsonb, ip, user_agent)
		values ($1, null, null, 'user.login_failed', 'email', $2, jsonb_build_object('email', $3::text), $4, $5)
	`, uuidx.New(), uuidx.New(), email, ipString(ip), ua)
}

// logAuditDirect writes to audit_events outside a tx (steady-state, no
// surrounding write to bind to).
func (s *Service) logAuditDirect(ctx context.Context, tenantID *uuid.UUID, actorUserID *uuid.UUID, action, entityType string, entityID uuid.UUID, ip net.IP, ua string) {
	_, _ = s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, ip, user_agent)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
	`, uuidx.New(), tenantID, actorUserID, action, entityType, entityID, ipString(ip), ua)
}
