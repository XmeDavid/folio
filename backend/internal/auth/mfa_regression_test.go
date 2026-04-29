package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

func testMFAService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		truncate audit_events, workspace_memberships, workspace_invites, sessions,
		         auth_tokens, auth_recovery_codes, webauthn_credentials,
		         totp_credentials, user_preferences, users, workspaces cascade
	`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			truncate audit_events, workspace_memberships, workspace_invites, sessions,
			         auth_tokens, auth_recovery_codes, webauthn_credentials,
			         totp_credentials, user_preferences, users, workspaces cascade
		`)
	})
	return NewService(pool, identity.NewService(pool), identity.NewPlatformInviteService(pool), Config{
		Registration:  RegistrationOpen,
		SecretKey:     []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		SecureCookies: false,
	}), ctx
}

func insertMFAUser(t *testing.T, svc *Service, ctx context.Context, password string) uuid.UUID {
	t.Helper()
	hash, err := HashPassword(password, svc.cfg.SecretKey)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	userID := uuidx.New()
	_, err = svc.pool.Exec(ctx, `
		insert into users (id, email, display_name, password_hash)
		values ($1, $2, 'MFA User', $3)
	`, userID, "mfa+"+userID.String()+"@example.com", hash)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return userID
}

func insertSession(t *testing.T, svc *Service, ctx context.Context, userID uuid.UUID) string {
	t.Helper()
	token, _ := GenerateSessionToken()
	sessionID := SessionIDFromToken(token)
	now := time.Now().UTC()
	_, err := svc.pool.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at)
		values ($1, $2, $3, $4, $3)
	`, sessionID, userID, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return sessionID
}

func TestCompleteReauth_PasskeyOnlyRequiresSecondFactor(t *testing.T) {
	svc, ctx := testMFAService(t)
	userID := insertMFAUser(t, svc, ctx, "correct horse battery staple")
	sessionID := insertSession(t, svc, ctx, userID)
	_, err := svc.pool.Exec(ctx, `
		insert into webauthn_credentials (id, user_id, credential_id, public_key, sign_count, created_at)
		values ($1, $2, $3, $4, 0, now())
	`, uuidx.New(), userID, []byte("credential"), []byte("public-key"))
	if err != nil {
		t.Fatalf("insert webauthn credential: %v", err)
	}

	err = svc.CompleteReauth(ctx, sessionID, userID, "correct horse battery staple", "")
	if !errors.Is(err, ErrUseWebAuthnReauth) {
		t.Fatalf("CompleteReauth error = %v, want ErrUseWebAuthnReauth", err)
	}

	var reauthAt *time.Time
	if err := svc.pool.QueryRow(ctx, `select reauth_at from sessions where id = $1`, sessionID).Scan(&reauthAt); err != nil {
		t.Fatalf("select reauth_at: %v", err)
	}
	if reauthAt != nil {
		t.Fatalf("reauth_at = %v, want nil", reauthAt)
	}
}

func TestDisableTOTP_PreservesCurrentSession(t *testing.T) {
	svc, ctx := testMFAService(t)
	userID := insertMFAUser(t, svc, ctx, "correct horse battery staple")
	currentSessionID := insertSession(t, svc, ctx, userID)
	otherSessionID := insertSession(t, svc, ctx, userID)
	_, err := svc.pool.Exec(ctx, `
		insert into totp_credentials (id, user_id, secret_cipher, verified_at, created_at)
		values ($1, $2, $3, now(), now())
	`, uuidx.New(), userID, "sealed")
	if err != nil {
		t.Fatalf("insert totp credential: %v", err)
	}

	if err := svc.DisableTOTP(ctx, userID, currentSessionID); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}

	var currentExists bool
	if err := svc.pool.QueryRow(ctx, `select exists(select 1 from sessions where id = $1)`, currentSessionID).Scan(&currentExists); err != nil {
		t.Fatalf("check current session: %v", err)
	}
	if !currentExists {
		t.Fatal("current session should remain after disabling TOTP")
	}
	var otherExists bool
	if err := svc.pool.QueryRow(ctx, `select exists(select 1 from sessions where id = $1)`, otherSessionID).Scan(&otherExists); err != nil {
		t.Fatalf("check other session: %v", err)
	}
	if otherExists {
		t.Fatal("other sessions should be revoked after disabling TOTP")
	}
}
