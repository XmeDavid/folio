package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

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
// for unknown-email login paths. Generated without a pepper; verifying it
// with pepper=nil exercises the same Argon2id cost as a peppered verify.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$Zm9saW8tZHVtbXktc2FsdA$yV+7m0L+QyU0FnQ4I3o5l2XcVxDmxRhQWdJGaNmT5lU"

// Login verifies credentials and issues a session. Reads + writes share a
// single transaction so the user lookup, password verify, MFA-status check,
// and session/challenge writes all see a consistent snapshot.
func (s *Service) Login(ctx context.Context, raw LoginInput) (*LoginResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var hash string
	var user identity.User
	err = tx.QueryRow(ctx, `
		select id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at, password_hash
		from users where email = $1
	`, in.Email).Scan(&user.ID, &user.Email, &user.DisplayName, &user.EmailVerifiedAt,
		&user.IsAdmin, &user.LastTenantID, &user.CreatedAt, &hash)
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword("dummy", dummyHash, nil)
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("select user: %w", err)
	}
	ok, err := VerifyPassword(in.Password, hash, s.cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}

	now := s.now().UTC()
	var hasMFA bool
	err = tx.QueryRow(ctx, `
		select exists(select 1 from totp_credentials where user_id = $1 and verified_at is not null)
		    or exists(select 1 from webauthn_credentials where user_id = $1)
	`, user.ID).Scan(&hasMFA)
	if err != nil {
		return nil, err
	}
	if hasMFA {
		challengeID := uuidx.New()
		if _, err := tx.Exec(ctx, `
			insert into auth_mfa_challenges
				(id, user_id, ip, user_agent, created_at, expires_at, webauthn_state)
			values ($1, $2, $3, $4, $5, $6, $7)
		`, challengeID, user.ID, ipString(in.IP), in.UserAgent, now, now.Add(s.cfg.MFAChallengeTTL), nil); err != nil {
			return nil, fmt.Errorf("insert mfa challenge: %w", err)
		}
		if err := writeAuditTx(ctx, tx, user.LastTenantID, &user.ID, "user.login_mfa_challenged", "user", user.ID, nil, nil, in.IP, in.UserAgent); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &LoginResult{User: user, MFARequired: true, ChallengeID: challengeID.String()}, nil
	}

	plaintext, sid, err := s.createSessionTx(ctx, tx, user.ID, in.IP, in.UserAgent, now)
	if err != nil {
		return nil, err
	}
	// Fix #6: invalidate any pre-existing sessions for this user. A session
	// the attacker stole or set up before the legitimate login no longer
	// survives a fresh successful login.
	if _, err := tx.Exec(ctx, `delete from sessions where user_id = $1 and id <> $2`, user.ID, sid); err != nil {
		return nil, fmt.Errorf("rotate sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `update users set last_login_at = $1 where id = $2`, now, user.ID); err != nil {
		return nil, fmt.Errorf("update last_login_at: %w", err)
	}
	if err := writeAuditTx(ctx, tx, user.LastTenantID, &user.ID, "user.login_succeeded", "user", user.ID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &LoginResult{User: user, SessionToken: plaintext, MFARequired: false}, nil
}

// createSessionTx is the tx-bound session insert used during Login so the
// session insert participates in the same transaction as the user/MFA reads.
func (s *Service) createSessionTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ip net.IP, ua string, now time.Time) (string, string, error) {
	plaintext, _ := GenerateSessionToken()
	sid := SessionIDFromToken(plaintext)
	_, err := tx.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		values ($1,$2,$3,$4,$3,$5,$6)
	`, sid, userID, now, now.Add(s.cfg.SessionAbsolute), ua, ipString(ip))
	if err != nil {
		return "", "", fmt.Errorf("insert session: %w", err)
	}
	return plaintext, sid, nil
}

func (s *Service) logLoginFailed(ctx context.Context, email string, ip net.IP, ua string) {
	// entity_id is required non-null; we use a fresh uuid as a placeholder for the failed-email event.
	_, err := s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, after_jsonb, ip, user_agent)
		values ($1, null, null, 'user.login_failed', 'email', $2, jsonb_build_object('email', $3::text), $4, $5)
	`, uuidx.New(), uuidx.New(), email, ipString(ip), ua)
	if err != nil {
		slog.Default().Warn("audit login_failed insert failed", "err", err)
	}
}

// logAuditDirect writes an audit event outside a transaction — used for
// steady-state events (login, logout) that don't have a surrounding write.
// Intentionally omits before_jsonb/after_jsonb to keep these events slim;
// use writeAuditTx inside a transaction for full-fidelity change audit.
func (s *Service) logAuditDirect(ctx context.Context, tenantID *uuid.UUID, actorUserID *uuid.UUID, action, entityType string, entityID uuid.UUID, ip net.IP, ua string) {
	_, _ = s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, ip, user_agent)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
	`, uuidx.New(), tenantID, actorUserID, action, entityType, entityID, ipString(ip), ua)
}
