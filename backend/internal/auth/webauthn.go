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
	rows, err := s.pool.Query(ctx, `
		select credential_id, public_key, sign_count, coalesce(transports, '{}')
		from webauthn_credentials where user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var creds []webauthn.Credential
	for rows.Next() {
		var c webauthn.Credential
		var transports []string
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.Authenticator.SignCount, &transports); err != nil {
			return nil, err
		}
		for _, t := range transports {
			c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
		}
		creds = append(creds, c)
	}
	return creds, rows.Err()
}

func (s *Service) BeginPasskeyEnrollment(ctx context.Context, userID uuid.UUID) (*protocol.CredentialCreation, string, error) {
	if s.webauthn == nil {
		return nil, "", errors.New("webauthn not configured")
	}
	u, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	creation, session, err := s.webauthn.BeginRegistration(u, webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
	if err != nil {
		return nil, "", err
	}
	b, err := json.Marshal(session)
	if err != nil {
		return nil, "", err
	}
	return creation, string(b), nil
}

func (s *Service) FinishPasskeyEnrollment(ctx context.Context, userID uuid.UUID, sessionJSON string, label string, r *http.Request) error {
	if s.webauthn == nil {
		return errors.New("webauthn not configured")
	}
	u, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(sessionJSON), &session); err != nil {
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
	_, err = s.pool.Exec(ctx, `
		insert into webauthn_credentials (id, user_id, credential_id, public_key, sign_count, transports, label, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
	`, uuidx.New(), userID, cred.ID, cred.PublicKey, cred.Authenticator.SignCount, transports, label, s.now().UTC())
	return err
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
	_, err = tx.Exec(ctx, `
		update webauthn_credentials set sign_count = $2
		where user_id = $1 and credential_id = $3
	`, c.UserID, cred.Authenticator.SignCount, cred.ID)
	return err
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
