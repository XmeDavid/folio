package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestSignupHTTP_createsUserTenantMembershipAndCookie(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	// Clean slate (cascades through tenants, memberships, sessions).
	if _, err := pool.Exec(ctx, `
		truncate audit_events, tenant_memberships, tenant_invites, sessions,
		         auth_tokens, auth_recovery_codes, webauthn_credentials,
		         totp_credentials, user_preferences, users, tenants cascade
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	svc := NewService(pool, identity.NewService(pool), Config{Registration: RegistrationOpen})
	h := NewHandler(svc)

	body, _ := json.Marshal(signupReq{
		Email: "alice@example.com", Password: "correct horse battery staple",
		DisplayName: "Alice", BaseCurrency: "CHF", Locale: "en-CH",
	})
	req := httptest.NewRequest("POST", "/auth/signup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.signup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Cookie set?
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName() {
			found = true
		}
	}
	if !found {
		t.Fatalf("session cookie not set")
	}
	// Duplicate email should 400.
	req2 := httptest.NewRequest("POST", "/auth/signup", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.signup(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("duplicate code = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	// Clean up what this test created so it doesn't leak into the next test.
	_, _ = pool.Exec(ctx, `
		truncate audit_events, tenant_memberships, tenant_invites, sessions,
		         auth_tokens, auth_recovery_codes, webauthn_credentials,
		         totp_credentials, user_preferences, users, tenants cascade
	`)
}

func TestLogoutHTTP_writesAuditEventAndDeletesSession(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		truncate audit_events, tenant_memberships, tenant_invites, sessions,
		         auth_tokens, auth_recovery_codes, webauthn_credentials,
		         totp_credentials, user_preferences, users, tenants cascade
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	svc := NewService(pool, identity.NewService(pool), Config{Registration: RegistrationOpen})
	h := NewHandler(svc)

	// Seed: signup to get a valid session cookie + user_id.
	body, _ := json.Marshal(signupReq{
		Email: "logout@example.com", Password: "correct horse battery staple",
		DisplayName: "LogoutUser", BaseCurrency: "CHF", Locale: "en-CH",
	})
	req := httptest.NewRequest("POST", "/auth/signup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.signup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName() {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatalf("no session cookie set by signup")
	}

	// Act: POST /auth/logout with the cookie.
	req2 := httptest.NewRequest("POST", "/auth/logout", nil)
	req2.AddCookie(sessionCookie)
	rec2 := httptest.NewRecorder()
	h.logout(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("logout code = %d", rec2.Code)
	}

	// Assert: session row deleted.
	var sessionCount int
	if err := pool.QueryRow(ctx, `select count(*) from sessions`).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("expected 0 sessions after logout, got %d", sessionCount)
	}

	// Assert: exactly one user.logout audit event exists.
	var auditCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from audit_events where action = 'user.logout'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("expected 1 user.logout audit event, got %d", auditCount)
	}

	// Act 2: logout again with the now-stale cookie — should NOT add another audit event.
	req3 := httptest.NewRequest("POST", "/auth/logout", nil)
	req3.AddCookie(sessionCookie)
	rec3 := httptest.NewRecorder()
	h.logout(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("second logout code = %d", rec3.Code)
	}
	if err := pool.QueryRow(ctx,
		`select count(*) from audit_events where action = 'user.logout'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("recount audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("stale-cookie logout should not audit; got %d events", auditCount)
	}

	// Clean up.
	_, _ = pool.Exec(ctx, `
		truncate audit_events, tenant_memberships, tenant_invites, sessions,
		         auth_tokens, auth_recovery_codes, webauthn_credentials,
		         totp_credentials, user_preferences, users, tenants cascade
	`)
}
