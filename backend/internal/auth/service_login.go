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

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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

	q := dbq.New(tx)
	row, err := q.GetUserByEmailWithPassword(ctx, in.Email)
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword("dummy", dummyHash, nil)
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("select user: %w", err)
	}
	user := identity.User{
		ID: row.ID, Email: row.Email, DisplayName: row.DisplayName,
		EmailVerifiedAt: row.EmailVerifiedAt, IsAdmin: row.IsAdmin,
		LastWorkspaceID: row.LastWorkspaceID, CreatedAt: row.CreatedAt,
	}
	ok, err := VerifyPassword(in.Password, row.PasswordHash, s.cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}

	now := s.now().UTC()
	hasMFA, err := q.HasMFAEnrolled(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if hasMFA != nil && *hasMFA {
		challengeID := uuidx.New()
		if err := q.InsertMFAChallenge(ctx, dbq.InsertMFAChallengeParams{
			ID:        challengeID,
			UserID:    user.ID,
			Ip:        netIPToAddrVal(in.IP),
			UserAgent: in.UserAgent,
			CreatedAt: now,
			ExpiresAt: now.Add(s.cfg.MFAChallengeTTL),
		}); err != nil {
			return nil, fmt.Errorf("insert mfa challenge: %w", err)
		}
		if err := writeAuditTx(ctx, tx, user.LastWorkspaceID, &user.ID, "user.login_mfa_challenged", "user", user.ID, nil, nil, in.IP, in.UserAgent); err != nil {
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
	if err := q.DeleteOtherSessionsByUser(ctx, dbq.DeleteOtherSessionsByUserParams{
		UserID: user.ID, ID: sid,
	}); err != nil {
		return nil, fmt.Errorf("rotate sessions: %w", err)
	}
	if err := q.UpdateUserLastLoginAt(ctx, dbq.UpdateUserLastLoginAtParams{
		LastLoginAt: &now, ID: user.ID,
	}); err != nil {
		return nil, fmt.Errorf("update last_login_at: %w", err)
	}
	if err := writeAuditTx(ctx, tx, user.LastWorkspaceID, &user.ID, "user.login_succeeded", "user", user.ID, nil, nil, in.IP, in.UserAgent); err != nil {
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
	if err := dbq.New(tx).InsertSession(ctx, dbq.InsertSessionParams{
		ID: sid, UserID: userID, CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.SessionAbsolute),
		UserAgent: &ua, Ip: netIPToAddr(ip),
	}); err != nil {
		return "", "", fmt.Errorf("insert session: %w", err)
	}
	return plaintext, sid, nil
}

func (s *Service) logLoginFailed(ctx context.Context, email string, ip net.IP, ua string) {
	// entity_id is required non-null; we use a fresh uuid as a placeholder for the failed-email event.
	err := dbq.New(s.pool).InsertLoginFailedAudit(ctx, dbq.InsertLoginFailedAuditParams{
		ID:        uuidx.New(),
		EntityID:  uuidx.New(),
		Column3:   email,
		Ip:        netIPToAddr(ip),
		UserAgent: &ua,
	})
	if err != nil {
		slog.Default().Warn("audit login_failed insert failed", "err", err)
	}
}

// logAuditDirect writes an audit event outside a transaction — used for
// steady-state events (login, logout) that don't have a surrounding write.
// Intentionally omits before_jsonb/after_jsonb to keep these events slim;
// use writeAuditTx inside a transaction for full-fidelity change audit.
func (s *Service) logAuditDirect(ctx context.Context, workspaceID *uuid.UUID, actorUserID *uuid.UUID, action, entityType string, entityID uuid.UUID, ip net.IP, ua string) {
	_ = dbq.New(s.pool).InsertAuditDirect(ctx, dbq.InsertAuditDirectParams{
		ID:          uuidx.New(),
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		Ip:          netIPToAddr(ip),
		UserAgent:   &ua,
	})
}
