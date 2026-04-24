package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// H2 — VerifyEmail must refuse to re-verify if the user's email has
// changed since the token was issued.
func TestVerifyEmail_BindsToTokenEmail(t *testing.T) {
	svc, ctx := testMFAService(t)
	userID := insertMFAUser(t, svc, ctx, "correct horse battery staple")

	plaintext, hash := GenerateSessionToken()
	tokenID := uuidx.New()
	if _, err := svc.pool.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposeEmailVerify, hash, "old+"+userID.String()+"@example.com", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("insert verify token: %v", err)
	}

	// User rotates email before clicking the old link.
	if _, err := svc.pool.Exec(ctx,
		`update users set email = $1 where id = $2`,
		"new+"+userID.String()+"@example.com", userID,
	); err != nil {
		t.Fatalf("update users email: %v", err)
	}

	if err := svc.VerifyEmail(ctx, plaintext); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("VerifyEmail err = %v, want ErrTokenInvalid", err)
	}

	// email_verified_at must still be null — the old token didn't prove the new address.
	var verifiedAt *time.Time
	if err := svc.pool.QueryRow(ctx,
		`select email_verified_at from users where id = $1`, userID).Scan(&verifiedAt); err != nil {
		t.Fatalf("select email_verified_at: %v", err)
	}
	if verifiedAt != nil {
		t.Fatalf("email_verified_at = %v, want nil", verifiedAt)
	}
}

// M3 — RequestEmailChange must not leak "already in use" to an attacker.
func TestRequestEmailChange_SilentOnConflict(t *testing.T) {
	svc, ctx := testMFAService(t)
	// A user who will request the change.
	requester := insertMFAUser(t, svc, ctx, "correct horse battery staple")
	// A different user already owning the target email.
	taken := "taken+" + uuid.New().String() + "@example.com"
	other := uuidx.New()
	if _, err := svc.pool.Exec(ctx, `
		insert into users (id, email, display_name, password_hash)
		values ($1, $2, 'Other', '$argon2id$stub')
	`, other, taken); err != nil {
		t.Fatalf("insert other user: %v", err)
	}

	if err := svc.RequestEmailChange(ctx, requester, taken); err != nil {
		t.Fatalf("RequestEmailChange err = %v, want nil (silent)", err)
	}

	var tokenCount int
	if err := svc.pool.QueryRow(ctx,
		`select count(*) from auth_tokens where user_id = $1 and purpose = $2`,
		requester, purposeEmailChange,
	).Scan(&tokenCount); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Fatalf("auth_tokens count = %d, want 0 (silent conflict issues no token)", tokenCount)
	}
}

// L1 — RegenerateRecoveryCodes must refuse users without any MFA enrolled.
func TestRegenerateRecoveryCodes_RequiresMFAEnrolled(t *testing.T) {
	svc, ctx := testMFAService(t)
	userID := insertMFAUser(t, svc, ctx, "correct horse battery staple")

	_, err := svc.RegenerateRecoveryCodes(ctx, userID)
	if !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Fatalf("RegenerateRecoveryCodes err = %v, want ErrTOTPNotEnrolled", err)
	}

	// Adding a passkey should unblock it (codes are a fallback for passkey-only users too).
	if _, err := svc.pool.Exec(ctx, `
		insert into webauthn_credentials (id, user_id, credential_id, public_key, sign_count, created_at)
		values ($1, $2, $3, $4, 0, now())
	`, uuidx.New(), userID, []byte("cred-"+userID.String()), []byte("pk")); err != nil {
		t.Fatalf("insert passkey: %v", err)
	}
	codes, err := svc.RegenerateRecoveryCodes(ctx, userID)
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes err = %v, want nil", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), recoveryCodeCount)
	}
}

// M4 — EnrollTOTP's upsert must not wipe verified_at on a concurrent retry.
func TestEnrollTOTP_PreservesVerifiedRow(t *testing.T) {
	svc, ctx := testMFAService(t)
	userID := insertMFAUser(t, svc, ctx, "correct horse battery staple")

	// Seed a verified TOTP row directly.
	if _, err := svc.pool.Exec(ctx, `
		insert into totp_credentials (id, user_id, secret_cipher, verified_at, created_at)
		values ($1, $2, $3, now(), now())
	`, uuidx.New(), userID, "sealed-secret"); err != nil {
		t.Fatalf("seed totp: %v", err)
	}

	_, err := svc.EnrollTOTP(ctx, userID)
	if !errors.Is(err, ErrTOTPAlreadyEnrolled) {
		t.Fatalf("EnrollTOTP err = %v, want ErrTOTPAlreadyEnrolled", err)
	}

	// Cipher text should not have been overwritten, verified_at preserved.
	var cipher string
	var verifiedAt *time.Time
	if err := svc.pool.QueryRow(ctx,
		`select secret_cipher, verified_at from totp_credentials where user_id = $1`, userID,
	).Scan(&cipher, &verifiedAt); err != nil {
		t.Fatalf("select totp: %v", err)
	}
	if cipher != "sealed-secret" {
		t.Fatalf("secret_cipher = %q, want unchanged sealed-secret", cipher)
	}
	if verifiedAt == nil {
		t.Fatalf("verified_at = nil, want preserved")
	}
}

// H4 — AdminBootstrapHook runs inside the signup tx. If it fails, the whole
// signup rolls back (no orphan user row).
func TestSignup_AdminBootstrapInsideTx_RollsBackOnError(t *testing.T) {
	pool := testdb.Open(t)
	bootstrapErr := errors.New("simulated bootstrap failure")
	email := "bootstrap+" + uuid.New().String() + "@example.com"
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `delete from tenant_memberships where user_id in (select id from users where email = $1)`, email)
		_, _ = pool.Exec(ctx,
			`delete from tenants where id in (select last_tenant_id from users where email = $1)`, email)
		_, _ = pool.Exec(ctx, `delete from users where email = $1`, email)
	})
	svc := NewService(pool, identity.NewService(pool), Config{
		Registration:  RegistrationOpen,
		SecureCookies: false,
		AdminBootstrapHook: func(ctx context.Context, tx pgx.Tx, userID uuid.UUID, userEmail string) error {
			return bootstrapErr
		},
	})

	_, err := svc.Signup(context.Background(), SignupInput{
		Email: email, Password: "correct horse battery staple", DisplayName: "Bootstrap",
		BaseCurrency: "USD", Locale: "en-US",
	})
	if !errors.Is(err, bootstrapErr) {
		t.Fatalf("Signup err = %v, want wrapped bootstrapErr", err)
	}

	var exists bool
	if err := pool.QueryRow(context.Background(),
		`select exists(select 1 from users where email = $1)`, email,
	).Scan(&exists); err != nil {
		t.Fatalf("select user: %v", err)
	}
	if exists {
		t.Fatalf("user row persisted after bootstrap failure — tx did not roll back")
	}
}

// H4 (happy path) — a successful bootstrap grant shows up on the first
// signup response without any refetch.
func TestSignup_AdminBootstrapGranted_FirstResponseHasIsAdmin(t *testing.T) {
	pool := testdb.Open(t)
	email := "granted+" + uuid.New().String() + "@example.com"
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `delete from tenant_memberships where user_id in (select id from users where email = $1)`, email)
		_, _ = pool.Exec(ctx,
			`delete from tenants where id in (select last_tenant_id from users where email = $1)`, email)
		_, _ = pool.Exec(ctx, `delete from users where email = $1`, email)
	})
	svc := NewService(pool, identity.NewService(pool), Config{
		Registration:  RegistrationOpen,
		SecureCookies: false,
		AdminBootstrapHook: func(ctx context.Context, tx pgx.Tx, userID uuid.UUID, userEmail string) error {
			_, err := tx.Exec(ctx, `update users set is_admin = true where id = $1`, userID)
			return err
		},
	})

	res, err := svc.Signup(context.Background(), SignupInput{
		Email: email, Password: "correct horse battery staple", DisplayName: "Granted",
		BaseCurrency: "USD", Locale: "en-US",
	})
	if err != nil {
		t.Fatalf("Signup err = %v", err)
	}
	if !res.User.IsAdmin {
		t.Fatalf("result.User.IsAdmin = false, want true (first response should reflect in-tx grant)")
	}
}
