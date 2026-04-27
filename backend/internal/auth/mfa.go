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

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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
	q := dbq.New(tx)
	if err := q.InsertSession(ctx, dbq.InsertSessionParams{
		ID: sid, UserID: c.UserID, CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.SessionAbsolute),
		UserAgent: &in.UserAgent, Ip: netIPToAddr(in.IP),
	}); err != nil {
		return nil, err
	}
	// Fix #6: invalidate any pre-existing sessions for this user. After a
	// successful MFA-gated login, prior sessions (e.g. attacker-set) die.
	if err := q.DeleteOtherSessionsByUser(ctx, dbq.DeleteOtherSessionsByUserParams{
		UserID: c.UserID, ID: sid,
	}); err != nil {
		return nil, err
	}
	if err := q.UpdateUserLastLoginAt(ctx, dbq.UpdateUserLastLoginAtParams{
		LastLoginAt: &now, ID: c.UserID,
	}); err != nil {
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

	hash, err := dbq.New(tx).GetUserPasswordHash(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(password, hash, s.cfg.SecretKey)
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
	now := s.now().UTC()
	if err := dbq.New(tx).UpdateSessionReauthAt(ctx, dbq.UpdateSessionReauthAtParams{
		ID: sessionID, UserID: userID, ReauthAt: &now,
	}); err != nil {
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
	if err := dbq.New(tx).UpdateSessionReauthAt(ctx, dbq.UpdateSessionReauthAtParams{
		ID: sessionID, UserID: userID, ReauthAt: &now,
	}); err != nil {
		return err
	}
	if err := writeAuditTx(ctx, tx, nil, &userID, "user.reauth_succeeded", "user", userID, nil, map[string]any{"method": "webauthn"}, ip, ua); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
