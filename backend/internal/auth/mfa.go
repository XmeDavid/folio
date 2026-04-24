package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type CompleteMFAInput struct {
	ChallengeID uuid.UUID
	Method      string
	Code        string
	IP          net.IP
	UserAgent   string
	Request     *http.Request
}

type CompleteMFAResult struct {
	User         any    `json:"user"`
	SessionToken string `json:"-"`
}

func (s *Service) CompleteMFA(ctx context.Context, in CompleteMFAInput) (*CompleteMFAResult, error) {
	now := s.now().UTC()
	c, err := LoadAndBindMFAChallenge(ctx, s.pool, in.ChallengeID, in.IP, in.UserAgent, now)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	switch in.Method {
	case "totp":
		if err := s.verifyTOTPCode(ctx, c.UserID, in.Code); err != nil {
			_, _ = BumpAttempts(ctx, s.pool, c.ID, now)
			return nil, err
		}
	case "recovery":
		if err := s.consumeRecoveryCode(ctx, tx, c.UserID, in.Code, now); err != nil {
			_, _ = BumpAttempts(ctx, s.pool, c.ID, now)
			return nil, err
		}
	case "webauthn":
		if in.Request == nil {
			return nil, errors.New("webauthn request required")
		}
		if err := s.completeWebAuthnAssertion(ctx, tx, c, in.Request); err != nil {
			_, _ = BumpAttempts(ctx, s.pool, c.ID, now)
			return nil, err
		}
	default:
		return nil, http.ErrNotSupported
	}
	if err := ConsumeMFAChallenge(ctx, tx, c.ID, now); err != nil {
		return nil, err
	}
	token, _ := GenerateSessionToken()
	sid := SessionIDFromToken(token)
	if _, err := tx.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		values ($1,$2,$3,$4,$3,$5,$6)
	`, sid, c.UserID, now, now.Add(s.cfg.SessionAbsolute), in.UserAgent, ipString(in.IP)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update users set last_login_at = $1 where id = $2`, now, c.UserID); err != nil {
		return nil, err
	}
	if err := writeAuditTx(ctx, tx, nil, &c.UserID, "user.login_succeeded", "user", c.UserID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	user, err := s.GetUserByID(ctx, c.UserID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return &CompleteMFAResult{User: user, SessionToken: token}, nil
}

// ErrUseWebAuthnReauth signals that a passkey-only user must reauth via
// the webauthn endpoints instead of password+TOTP. Handler maps to 409.
var ErrUseWebAuthnReauth = errors.New("reauth requires webauthn assertion")

func (s *Service) CompleteReauth(ctx context.Context, sessionID string, userID uuid.UUID, password, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var hash string
	if err := tx.QueryRow(ctx, `select password_hash from users where id = $1 for update`, userID).Scan(&hash); err != nil {
		return err
	}
	ok, err := VerifyPassword(password, hash)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	st, err := s.MFAStatus(ctx, userID)
	if err != nil {
		return err
	}
	if st.TOTPEnrolled {
		if err := s.verifyTOTPCode(ctx, userID, code); err != nil {
			return err
		}
	} else if st.PasskeyCount > 0 {
		// No TOTP but passkeys exist — client must run the webauthn reauth.
		return ErrUseWebAuthnReauth
	}
	if _, err := tx.Exec(ctx, `update sessions set reauth_at = $3 where id = $1 and user_id = $2`, sessionID, userID, s.now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BeginReauthWebAuthn creates a short-lived MFA challenge scoped to the
// authenticated user and returns a webauthn assertion for their passkeys.
// The challenge's IP/UA are recorded so Complete* can bind them.
func (s *Service) BeginReauthWebAuthn(ctx context.Context, userID uuid.UUID, ip net.IP, ua string) (*protocol.CredentialAssertion, uuid.UUID, error) {
	if s.webauthn == nil {
		return nil, uuid.Nil, errors.New("webauthn not configured")
	}
	u, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if len(u.credentials) == 0 {
		return nil, uuid.Nil, ErrTOTPNotEnrolled
	}
	assertion, session, err := s.webauthn.BeginLogin(u)
	if err != nil {
		return nil, uuid.Nil, err
	}
	stateJSON, err := json.Marshal(session)
	if err != nil {
		return nil, uuid.Nil, err
	}
	now := s.now().UTC()
	challengeID := uuidx.New()
	if err := InsertMFAChallenge(ctx, s.pool, MFAChallenge{
		ID: challengeID, UserID: userID, IP: ip, UserAgent: ua,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.MFAChallengeTTL),
		WebAuthnState: stateJSON,
	}); err != nil {
		return nil, uuid.Nil, err
	}
	return assertion, challengeID, nil
}

// CompleteReauthWebAuthn verifies a webauthn assertion against the challenge
// created by BeginReauthWebAuthn and bumps sessions.reauth_at on success.
// Refuses challenges owned by a different user.
func (s *Service) CompleteReauthWebAuthn(ctx context.Context, sessionID string, userID uuid.UUID, challengeID uuid.UUID, ip net.IP, ua string, r *http.Request) error {
	if s.webauthn == nil {
		return errors.New("webauthn not configured")
	}
	now := s.now().UTC()
	c, err := LoadAndBindMFAChallenge(ctx, s.pool, challengeID, ip, ua, now)
	if err != nil {
		return err
	}
	if c.UserID != userID {
		return ErrChallengeNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.completeWebAuthnAssertion(ctx, tx, c, r); err != nil {
		return err
	}
	if err := ConsumeMFAChallenge(ctx, tx, c.ID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update sessions set reauth_at = $3 where id = $1 and user_id = $2`, sessionID, userID, now); err != nil {
		return err
	}
	if err := writeAuditTx(ctx, tx, nil, &userID, "user.reauth_succeeded", "user", userID, nil, map[string]any{"method": "webauthn"}, ip, ua); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
