package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// patchMeFixture wires RequireSession for PATCH /me and seeds one user.
type patchMeFixture struct {
	router http.Handler
	svc    *Service
	cookie *http.Cookie
}

func setupPatchMeFixture(t *testing.T) *patchMeFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		truncate audit_events, workspace_memberships, workspace_invites, sessions,
		         auth_tokens, auth_recovery_codes, webauthn_credentials,
		         totp_credentials, user_preferences, users, workspaces cascade
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			truncate audit_events, workspace_memberships, workspace_invites, sessions,
			         auth_tokens, auth_recovery_codes, webauthn_credentials,
			         totp_credentials, user_preferences, users, workspaces cascade
		`)
	})

	svc := NewService(pool, identity.NewService(pool), identity.NewPlatformInviteService(pool), Config{
		Registration:  RegistrationOpen,
		SecretKey:     []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		SecureCookies: false,
	})
	h := NewHandler(svc)

	userEmail := "patchme+" + "test" + "@example.com"
	userID := testdb.CreateTestUser(t, pool, userEmail, true)

	token, _ := GenerateSessionToken()
	sid := SessionIDFromToken(token)
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at)
		values ($1, $2, $3, $4, $3)
	`, sid, userID, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	cookie := &http.Cookie{Name: SessionCookieName(), Value: token}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(svc.RequireSession)
			h.MountAuthed(r)
		})
	})

	return &patchMeFixture{
		router: r,
		svc:    svc,
		cookie: cookie,
	}
}

func doPatchMe(t *testing.T, h http.Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/api/v1/me", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPatchMe_HappyPath(t *testing.T) {
	f := setupPatchMeFixture(t)
	rec := doPatchMe(t, f.router, `{"displayName":"New Name"}`, f.cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Verify DB updated.
	var displayName string
	if err := f.svc.pool.QueryRow(context.Background(),
		`select display_name from users where email = 'patchme+test@example.com'`,
	).Scan(&displayName); err != nil {
		t.Fatalf("select display_name: %v", err)
	}
	if displayName != "New Name" {
		t.Fatalf("display_name = %q, want %q", displayName, "New Name")
	}

	// Verify audit row written.
	var auditCount int
	if err := f.svc.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'user.profile_updated'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("expected 1 user.profile_updated audit event, got %d", auditCount)
	}
}

func TestPatchMe_TooLong(t *testing.T) {
	f := setupPatchMeFixture(t)
	longName := make([]byte, 81)
	for i := range longName {
		longName[i] = 'a'
	}
	body, _ := json.Marshal(map[string]string{"displayName": string(longName)})
	rec := doPatchMe(t, f.router, string(body), f.cookie)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != "invalid_display_name" {
		t.Fatalf("code = %v, want invalid_display_name", resp["code"])
	}
}

func TestPatchMe_TooShort(t *testing.T) {
	f := setupPatchMeFixture(t)
	// Empty after trim.
	body, _ := json.Marshal(map[string]string{"displayName": "   "})
	rec := doPatchMe(t, f.router, string(body), f.cookie)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != "invalid_display_name" {
		t.Fatalf("code = %v, want invalid_display_name", resp["code"])
	}
}

func TestPatchMe_BadJSON(t *testing.T) {
	f := setupPatchMeFixture(t)
	rec := doPatchMe(t, f.router, `not json`, f.cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != "invalid_body" {
		t.Fatalf("code = %v, want invalid_body", resp["code"])
	}
}

func TestPatchMe_NoDisplayName(t *testing.T) {
	f := setupPatchMeFixture(t)
	// Omitting displayName entirely is a no-op: should still return 204.
	rec := doPatchMe(t, f.router, `{}`, f.cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Audit count should be 0 (no-op, nothing changed).
	var auditCount int
	if err := f.svc.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'user.profile_updated'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 0 {
		t.Errorf("expected 0 audit events for no-op, got %d", auditCount)
	}
}
