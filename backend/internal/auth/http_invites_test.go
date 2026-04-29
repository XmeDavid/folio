package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/mailer"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// inviteHTTPFixture wires the workspace-invite handler the way it's mounted
// in production: RequireSession + RequireMembership + RequireEmailVerified
// upstream, with RequireFreshReauth applied per-route inside
// MountWorkspaceInvites. "Stale" cookies are seeded with NULL reauth_at to
// prove the resend gate fires.
type inviteHTTPFixture struct {
	router           http.Handler
	svc              *Service
	pool             *pgxpool.Pool
	workspaceID      uuid.UUID
	otherWorkspaceID uuid.UUID
	ownerID          uuid.UUID
	ownerCookie      *http.Cookie
	ownerStaleCookie *http.Cookie
	strangerCookie   *http.Cookie
	mail             *mailer.LogMailer
	invSvc           *identity.InviteService
}

func setupInviteHTTPFixture(t *testing.T) *inviteHTTPFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		truncate audit_events, workspace_memberships, workspace_invites,
		         sessions, auth_tokens, auth_recovery_codes,
		         webauthn_credentials, totp_credentials, user_preferences,
		         users, workspaces cascade
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			truncate audit_events, workspace_memberships, workspace_invites,
			         sessions, auth_tokens, auth_recovery_codes,
			         webauthn_credentials, totp_credentials, user_preferences,
			         users, workspaces cascade
		`)
	})

	svc := NewService(pool, identity.NewService(pool), identity.NewPlatformInviteService(pool), Config{
		Registration:  RegistrationOpen,
		SecretKey:     []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		SecureCookies: false,
		ReauthWindow:  5 * time.Minute,
	})
	invSvc := identity.NewInviteService(pool)
	mail := mailer.NewLogMailer(slog.Default())
	h := NewInviteHandler(svc, invSvc, mail)

	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "TeamA")
	otherWorkspaceID, _ := testdb.CreateTestWorkspace(t, pool, "TeamB")
	ownerID := testdb.CreateTestUser(t, pool, "owner+"+uuid.New().String()+"@example.com", true)
	strangerID := testdb.CreateTestUser(t, pool, "stranger+"+uuid.New().String()+"@example.com", true)
	testdb.CreateTestMembership(t, pool, workspaceID, ownerID, "owner")
	// Owner is also an owner of the other workspace, so OtherWorkspaceContext
	// passes RequireMembership against that workspace and the not-found
	// surface comes from the service (workspace_id mismatch).
	testdb.CreateTestMembership(t, pool, otherWorkspaceID, ownerID, "owner")

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

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/t/{workspaceId}", func(r chi.Router) {
			r.Use(svc.RequireSession)
			r.Use(svc.RequireMembership)
			r.With(svc.RequireEmailVerified).Route("/invites", h.MountWorkspaceInvites)
		})
	})

	return &inviteHTTPFixture{
		router:           r,
		svc:              svc,
		pool:             pool,
		workspaceID:      workspaceID,
		otherWorkspaceID: otherWorkspaceID,
		ownerID:          ownerID,
		ownerCookie:      mkSession(ownerID, true),
		ownerStaleCookie: mkSession(ownerID, false),
		strangerCookie:   mkSession(strangerID, true),
		mail:             mail,
		invSvc:           invSvc,
	}
}

func doInviteReq(t *testing.T, h http.Handler, method, path string, body []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
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

// seedPendingInvite uses the service directly to create an invite so each
// test can avoid the whole HTTP round-trip when it just needs an id to
// resend against. Returns the invite id and the original plaintext token.
func seedPendingInvite(t *testing.T, f *inviteHTTPFixture, workspaceID, inviterID uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	bobEmail := "bob+" + uuid.New().String() + "@example.com"
	inv, plaintext, err := f.invSvc.Create(context.Background(), workspaceID, inviterID, bobEmail, identity.RoleMember)
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	return inv.ID, plaintext
}

func TestResendInvite_HappyPath(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	inviteID, originalPlaintext := seedPendingInvite(t, f, f.workspaceID, f.ownerID)

	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.workspaceID.String()+"/invites/"+inviteID.String()+"/resend",
		nil, f.ownerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Invite    *identity.Invite `json:"invite"`
		AcceptURL string           `json:"acceptUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Invite == nil || resp.Invite.ID != inviteID {
		t.Fatalf("invite mismatch: %+v", resp.Invite)
	}
	if resp.AcceptURL == "" || strings.HasSuffix(resp.AcceptURL, "/accept-invite/") {
		t.Fatalf("acceptUrl = %q, want non-empty with token", resp.AcceptURL)
	}
	// New token must differ from the original.
	if strings.HasSuffix(resp.AcceptURL, "/"+originalPlaintext) {
		t.Fatalf("acceptUrl reused old token: %s", resp.AcceptURL)
	}

	// Audit row should exist.
	var auditCount int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'member.invite_resent'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}

	// Old plaintext must no longer accept (preview returns NotFound).
	if _, err := f.invSvc.Preview(context.Background(), originalPlaintext); err == nil {
		t.Fatalf("old token still previews; want error")
	}
}

func TestResendInvite_NotFound(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.workspaceID.String()+"/invites/"+uuid.New().String()+"/resend",
		nil, f.ownerCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestResendInvite_RevokedRejected(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	inviteID, _ := seedPendingInvite(t, f, f.workspaceID, f.ownerID)
	if err := f.invSvc.Revoke(context.Background(), f.workspaceID, inviteID, f.ownerID); err != nil {
		t.Fatalf("seed revoke: %v", err)
	}
	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.workspaceID.String()+"/invites/"+inviteID.String()+"/resend",
		nil, f.ownerCookie)
	if rec.Code != http.StatusGone {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invite_revoked" {
		t.Fatalf("code = %v, want invite_revoked (body = %s)", body["code"], rec.Body.String())
	}
}

func TestResendInvite_AlreadyUsedRejected(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	inviteID, _ := seedPendingInvite(t, f, f.workspaceID, f.ownerID)
	if _, err := f.pool.Exec(context.Background(),
		`update workspace_invites set accepted_at = now() where id = $1`, inviteID); err != nil {
		t.Fatalf("force accept: %v", err)
	}
	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.workspaceID.String()+"/invites/"+inviteID.String()+"/resend",
		nil, f.ownerCookie)
	if rec.Code != http.StatusGone {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invite_already_used" {
		t.Fatalf("code = %v, want invite_already_used (body = %s)", body["code"], rec.Body.String())
	}
}

func TestResendInvite_OtherWorkspaceContext(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	// Invite belongs to workspaceID; we POST against otherWorkspaceID.
	// RequireMembership accepts (owner is a member of both); the service
	// then sees no row for (otherWorkspaceID, inviteID) and returns 404.
	inviteID, _ := seedPendingInvite(t, f, f.workspaceID, f.ownerID)
	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.otherWorkspaceID.String()+"/invites/"+inviteID.String()+"/resend",
		nil, f.ownerCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestResendInvite_RequiresFreshReauth(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	inviteID, _ := seedPendingInvite(t, f, f.workspaceID, f.ownerID)
	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.workspaceID.String()+"/invites/"+inviteID.String()+"/resend",
		nil, f.ownerStaleCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "reauth_required" {
		t.Fatalf("code = %v, want reauth_required (body = %s)", body["code"], rec.Body.String())
	}
}

func TestResendInvite_StrangerForbidden(t *testing.T) {
	f := setupInviteHTTPFixture(t)
	inviteID, _ := seedPendingInvite(t, f, f.workspaceID, f.ownerID)
	// Stranger has no membership in workspaceID, so RequireMembership
	// returns 404 (membership is not enumerable).
	rec := doInviteReq(t, f.router, "POST",
		"/api/v1/t/"+f.workspaceID.String()+"/invites/"+inviteID.String()+"/resend",
		nil, f.strangerCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}
