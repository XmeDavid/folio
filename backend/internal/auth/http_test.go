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
