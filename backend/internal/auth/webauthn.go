package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type webAuthnUser struct {
	user        identity.User
	credentials []webauthn.Credential
}

func (u webAuthnUser) WebAuthnID() []byte          { return []byte(u.user.ID.String()) }
func (u webAuthnUser) WebAuthnName() string        { return u.user.Email }
func (u webAuthnUser) WebAuthnDisplayName() string { return u.user.DisplayName }
func (u webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func (s *Service) loadWebAuthnUser(ctx context.Context, userID uuid.UUID) (webAuthnUser, error) {
	user, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return webAuthnUser{}, err
	}
	creds, err := s.loadWebAuthnCredentials(ctx, userID)
	if err != nil {
		return webAuthnUser{}, err
	}
	return webAuthnUser{user: user, credentials: creds}, nil
}

func (s *Service) loadWebAuthnCredentials(ctx context.Context, userID uuid.UUID) ([]webauthn.Credential, error) {
	rows, err := dbq.New(s.pool).ListWebAuthnCredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	var creds []webauthn.Credential
	for _, row := range rows {
		var c webauthn.Credential
		c.ID = row.CredentialID
		c.PublicKey = row.PublicKey
		c.Authenticator.SignCount = uint32(row.SignCount)
		for _, t := range row.Transports {
			c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
		}
		creds = append(creds, c)
	}
	return creds, nil
}

// BeginPasskeyEnrollment returns a credential-creation challenge and persists
// the webauthn SessionData server-side in auth_mfa_challenges. The client gets
// back an opaque challengeID which it must present to FinishPasskeyEnrollment.
// This prevents a client-tampered session from being replayed on completion.
func (s *Service) BeginPasskeyEnrollment(ctx context.Context, userID uuid.UUID, ip net.IP, ua string) (*protocol.CredentialCreation, uuid.UUID, error) {
	if s.webauthn == nil {
		return nil, uuid.Nil, errors.New("webauthn not configured")
	}
	u, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	creation, session, err := s.webauthn.BeginRegistration(u, webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
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
	return creation, challengeID, nil
}

func (s *Service) FinishPasskeyEnrollment(ctx context.Context, userID uuid.UUID, challengeID uuid.UUID, ip net.IP, ua string, label string, r *http.Request) error {
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
	u, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(c.WebAuthnState, &session); err != nil {
		return err
	}
	cred, err := s.webauthn.FinishRegistration(u, session, r)
	if err != nil {
		return err
	}
	transports := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		transports = append(transports, string(t))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ConsumeMFAChallenge(ctx, tx, c.ID, now); err != nil {
		return err
	}
	if err := dbq.New(tx).InsertWebAuthnCredential(ctx, dbq.InsertWebAuthnCredentialParams{
		ID:           uuidx.New(),
		UserID:       userID,
		CredentialID: cred.ID,
		PublicKey:    cred.PublicKey,
		SignCount:    int64(cred.Authenticator.SignCount),
		Transports:   transports,
		Label:        &label,
		CreatedAt:    now,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) BeginWebAuthnAssertion(ctx context.Context, challengeID uuid.UUID, ip net.IP, ua string) (*protocol.CredentialAssertion, error) {
	if s.webauthn == nil {
		return nil, errors.New("webauthn not configured")
	}
	c, err := LoadAndBindMFAChallenge(ctx, s.pool, challengeID, ip, ua, s.now().UTC())
	if err != nil {
		return nil, err
	}
	u, err := s.loadWebAuthnUser(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	assertion, session, err := s.webauthn.BeginLogin(u)
	if err != nil {
		return nil, err
	}
	if err := UpdateWebAuthnState(ctx, s.pool, challengeID, session); err != nil {
		return nil, err
	}
	return assertion, nil
}

func (s *Service) completeWebAuthnAssertion(ctx context.Context, tx pgx.Tx, c MFAChallenge, r *http.Request) error {
	if s.webauthn == nil {
		return errors.New("webauthn not configured")
	}
	u, err := s.loadWebAuthnUser(ctx, c.UserID)
	if err != nil {
		return err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(c.WebAuthnState, &session); err != nil {
		return err
	}
	cred, err := s.webauthn.FinishLogin(u, session, r)
	if err != nil {
		return err
	}
	return dbq.New(tx).UpdateWebAuthnSignCount(ctx, dbq.UpdateWebAuthnSignCountParams{
		UserID:       c.UserID,
		SignCount:    int64(cred.Authenticator.SignCount),
		CredentialID: cred.ID,
	})
}

func (s *Service) userForDiscoverableCredential(rawID, userHandle []byte) (webauthn.User, error) {
	userID, err := uuid.ParseBytes(userHandle)
	if err != nil {
		return nil, err
	}
	u, err := s.loadWebAuthnUser(context.Background(), userID)
	if err != nil {
		return nil, err
	}
	for _, c := range u.credentials {
		if bytes.Equal(c.ID, rawID) {
			return u, nil
		}
	}
	return nil, pgx.ErrNoRows
}
