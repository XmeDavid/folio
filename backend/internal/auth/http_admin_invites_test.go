package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/mailer"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// adminInviteFixture holds the wired-up router + handles needed by every
// admin-invite test: a real chi router with the production middleware stack
// (RequireSession + RequireAdmin + optional RequireFreshReauth), an admin
// user with a session cookie that's "fresh" by default, and a stub mailer.
type adminInviteFixture struct {
	router      http.Handler
	svc         *Service
	invSvc      *identity.PlatformInviteService
	mail        *mailer.LogMailer
	adminID     uuid.UUID
	adminCookie *http.Cookie
	// staleCookie is a separate session for the same admin with no reauth_at
	// set — used to verify the reauth gate fires.
	staleCookie *http.Cookie
	// nonAdminCookie is a session for a regular (non-admin) user; used to
	// verify the admin gate returns 404 (RequireAdmin's behaviour).
	nonAdminCookie *http.Cookie
}

func setupAdminInviteFixture(t *testing.T) *adminInviteFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		truncate platform_invites, audit_events, workspace_memberships,
		         workspace_invites, sessions, auth_tokens, auth_recovery_codes,
		         webauthn_credentials, totp_credentials, user_preferences,
		         users, workspaces cascade
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			truncate platform_invites, audit_events, workspace_memberships,
			         workspace_invites, sessions, auth_tokens, auth_recovery_codes,
			         webauthn_credentials, totp_credentials, user_preferences,
			         users, workspaces cascade
		`)
	})

	svc := NewService(pool, identity.NewService(pool), Config{
		Registration:  RegistrationOpen,
		SecretKey:     []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		SecureCookies: false,
		ReauthWindow:  5 * time.Minute,
	})
	invSvc := identity.NewPlatformInviteService(pool)
	mail := mailer.NewLogMailer(slog.Default())
	h := NewAdminInviteHandler(svc, invSvc, mail)

	// Seed an admin user.
	adminEmail := "admin+" + uuid.New().String() + "@example.com"
	adminID := testdb.CreateTestUser(t, pool, adminEmail, true)
	if _, err := pool.Exec(ctx, `update users set is_admin = true where id = $1`, adminID); err != nil {
		t.Fatalf("grant admin: %v", err)
	}

	// Seed a non-admin user for the negative gate test.
	nonAdminEmail := "user+" + uuid.New().String() + "@example.com"
	nonAdminID := testdb.CreateTestUser(t, pool, nonAdminEmail, true)

	// Helper: insert a session row and return the plaintext cookie.
	mkSession := func(uid uuid.UUID, fresh bool) *http.Cookie {
		token, _ := GenerateSessionToken()
		sid := SessionIDFromToken(token)
		now := time.Now().UTC()
		var reauthAt *time.Time
		if fresh {
			r := now
			reauthAt = &r
		}
		if _, err := pool.Exec(ctx, `
			insert into sessions (id, user_id, created_at, expires_at, last_seen_at, reauth_at)
			values ($1, $2, $3, $4, $3, $5)
		`, sid, uid, now, now.Add(24*time.Hour), reauthAt); err != nil {
			t.Fatalf("insert session: %v", err)
		}
		return &http.Cookie{Name: SessionCookieName(), Value: token}
	}
	adminCookie := mkSession(adminID, true)
	staleCookie := mkSession(adminID, false)
	nonAdminCookie := mkSession(nonAdminID, true)

	// Wire a chi router that mirrors the production mount: RequireSession +
	// RequireAdmin + RequireFreshReauth on mutations.
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		h.MountPublic(r)
		r.Route("/admin", func(r chi.Router) {
			r.Use(svc.RequireSession)
			r.Use(svc.RequireAdmin)
			h.MountAdmin(r, RequireFreshReauth(svc.cfg.ReauthWindow))
		})
	})

	return &adminInviteFixture{
		router:         r,
		svc:            svc,
		invSvc:         invSvc,
		mail:           mail,
		adminID:        adminID,
		adminCookie:    adminCookie,
		staleCookie:    staleCookie,
		nonAdminCookie: nonAdminCookie,
	}
}

func doReq(t *testing.T, h http.Handler, method, path string, body []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestAdminInvite_Create_RequiresAdminSession(t *testing.T) {
	f := setupAdminInviteFixture(t)
	// No cookie at all — RequireSession returns 401.
	rec := doReq(t, f.router, "POST", "/api/v1/admin/invites", []byte(`{}`), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Non-admin session — RequireAdmin returns 404 ("not found"; admin-ness
	// is not enumerable).
	rec2 := doReq(t, f.router, "POST", "/api/v1/admin/invites", []byte(`{}`), f.nonAdminCookie)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("non-admin: code = %d, body = %s", rec2.Code, rec2.Body.String())
	}
}

func TestAdminInvite_Create_RequiresFreshReauth(t *testing.T) {
	f := setupAdminInviteFixture(t)
	rec := doReq(t, f.router, "POST", "/api/v1/admin/invites", []byte(`{}`), f.staleCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stale reauth: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "reauth_required" {
		t.Fatalf("code = %v, want reauth_required (body = %s)", body["code"], rec.Body.String())
	}
}

func TestAdminInvite_Create_HappyPath(t *testing.T) {
	f := setupAdminInviteFixture(t)
	body, _ := json.Marshal(map[string]string{"email": "newuser@example.com"})
	rec := doReq(t, f.router, "POST", "/api/v1/admin/invites", body, f.adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp createPlatformInviteResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatalf("token empty")
	}
	if resp.AcceptURL == "" || resp.AcceptURL == "/accept-invite/" {
		t.Fatalf("acceptUrl = %q, want non-empty with token", resp.AcceptURL)
	}
	if resp.Invite.ID == uuid.Nil {
		t.Fatalf("invite id is nil")
	}
	if resp.Invite.Email == nil || *resp.Invite.Email != "newuser@example.com" {
		t.Fatalf("invite email = %v, want newuser@example.com", resp.Invite.Email)
	}
	if resp.Invite.CreatedBy != f.adminID {
		t.Fatalf("createdBy = %v, want %v", resp.Invite.CreatedBy, f.adminID)
	}
	// Audit row should exist.
	var auditCount int
	if err := f.svc.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'admin.invite_created'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

func TestAdminInvite_Create_EmptyEmailIsOpenInvite(t *testing.T) {
	f := setupAdminInviteFixture(t)
	rec := doReq(t, f.router, "POST", "/api/v1/admin/invites", []byte(`{}`), f.adminCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp createPlatformInviteResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Invite.Email != nil {
		t.Fatalf("invite email = %v, want nil (open invite)", *resp.Invite.Email)
	}
}

func TestAdminInvite_Create_MalformedEmailReturns400(t *testing.T) {
	f := setupAdminInviteFixture(t)
	body, _ := json.Marshal(map[string]string{"email": "no-at-sign"})
	rec := doReq(t, f.router, "POST", "/api/v1/admin/invites", body, f.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminInvite_Revoke_HappyPath(t *testing.T) {
	f := setupAdminInviteFixture(t)
	inv, _, err := f.invSvc.Create(context.Background(), f.adminID, "torevoke@example.com")
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	rec := doReq(t, f.router, "DELETE", "/api/v1/admin/invites/"+inv.ID.String(), nil, f.adminCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var auditCount int
	if err := f.svc.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'admin.invite_revoked'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

func TestAdminInvite_Revoke_NotFound(t *testing.T) {
	f := setupAdminInviteFixture(t)
	rec := doReq(t, f.router, "DELETE", "/api/v1/admin/invites/"+uuid.New().String(), nil, f.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminInvite_Revoke_AlreadyRevoked(t *testing.T) {
	f := setupAdminInviteFixture(t)
	inv, _, err := f.invSvc.Create(context.Background(), f.adminID, "doublerevoke@example.com")
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	if err := f.invSvc.Revoke(context.Background(), inv.ID, f.adminID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	rec := doReq(t, f.router, "DELETE", "/api/v1/admin/invites/"+inv.ID.String(), nil, f.adminCookie)
	if rec.Code != http.StatusGone {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invite_revoked" {
		t.Fatalf("code = %v, want invite_revoked (body = %s)", body["code"], rec.Body.String())
	}
}

func TestAdminInvite_List_ReturnsActive(t *testing.T) {
	f := setupAdminInviteFixture(t)
	if _, _, err := f.invSvc.Create(context.Background(), f.adminID, "a@example.com"); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, _, err := f.invSvc.Create(context.Background(), f.adminID, "b@example.com"); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	rec := doReq(t, f.router, "GET", "/api/v1/admin/invites", nil, f.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rows []identity.PlatformInvite
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
}

func TestAdminInvite_PreviewPublic_Good(t *testing.T) {
	f := setupAdminInviteFixture(t)
	_, plaintext, err := f.invSvc.Create(context.Background(), f.adminID, "preview@example.com")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := doReq(t, f.router, "GET", "/api/v1/auth/platform-invites/"+plaintext, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var prev identity.PlatformInvitePreview
	if err := json.Unmarshal(rec.Body.Bytes(), &prev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prev.Email == nil || *prev.Email != "preview@example.com" {
		t.Fatalf("email = %v, want preview@example.com", prev.Email)
	}
	if prev.ExpiresAt.IsZero() {
		t.Fatalf("expiresAt zero")
	}
}

func TestAdminInvite_PreviewPublic_Revoked(t *testing.T) {
	f := setupAdminInviteFixture(t)
	inv, plaintext, err := f.invSvc.Create(context.Background(), f.adminID, "revoked@example.com")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.invSvc.Revoke(context.Background(), inv.ID, f.adminID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rec := doReq(t, f.router, "GET", "/api/v1/auth/platform-invites/"+plaintext, nil, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invite_revoked" {
		t.Fatalf("code = %v, want invite_revoked (body = %s)", body["code"], rec.Body.String())
	}
}

func TestAdminInvite_PreviewPublic_BadToken(t *testing.T) {
	f := setupAdminInviteFixture(t)
	rec := doReq(t, f.router, "GET", "/api/v1/auth/platform-invites/this-token-does-not-exist", nil, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invite_not_found" {
		t.Fatalf("code = %v, want invite_not_found (body = %s)", body["code"], rec.Body.String())
	}
}
