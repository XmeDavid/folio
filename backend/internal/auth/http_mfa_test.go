package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// passkeyFixture wires the production middleware stack for /me/mfa/passkeys
// (RequireSession + RequireFreshReauth on DELETE) and seeds a primary user
// with both fresh and stale session cookies, plus a second "other" user with
// their own passkey (used to verify cross-user delete is a silent no-op).
type passkeyFixture struct {
	router       http.Handler
	svc          *Service
	userID       uuid.UUID
	freshCookie  *http.Cookie
	staleCookie  *http.Cookie
	otherUserID  uuid.UUID
	otherCookie  *http.Cookie
	myPasskeyID  uuid.UUID
	otherPasskey uuid.UUID
}

func setupPasskeyFixture(t *testing.T) *passkeyFixture {
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
		ReauthWindow:  5 * time.Minute,
	})
	h := NewHandler(svc)

	userEmail := "user+" + uuid.New().String() + "@example.com"
	userID := testdb.CreateTestUser(t, pool, userEmail, true)
	otherEmail := "other+" + uuid.New().String() + "@example.com"
	otherID := testdb.CreateTestUser(t, pool, otherEmail, true)

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
	freshCookie := mkSession(userID, true)
	staleCookie := mkSession(userID, false)
	otherCookie := mkSession(otherID, true)

	// Seed two passkeys for the primary user and one for the other user.
	myPasskey := uuidx.New()
	if _, err := pool.Exec(ctx, `
		insert into webauthn_credentials (id, user_id, credential_id, public_key, sign_count, label, created_at)
		values ($1, $2, $3, $4, 0, $5, now())
	`, myPasskey, userID, []byte("cred-mine-1"), []byte("pk-1"), strPtr("My Laptop")); err != nil {
		t.Fatalf("insert my passkey: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into webauthn_credentials (id, user_id, credential_id, public_key, sign_count, label, created_at)
		values ($1, $2, $3, $4, 0, NULL, now() - interval '1 minute')
	`, uuidx.New(), userID, []byte("cred-mine-2"), []byte("pk-2")); err != nil {
		t.Fatalf("insert my passkey 2: %v", err)
	}
	otherPasskey := uuidx.New()
	if _, err := pool.Exec(ctx, `
		insert into webauthn_credentials (id, user_id, credential_id, public_key, sign_count, label, created_at)
		values ($1, $2, $3, $4, 0, $5, now())
	`, otherPasskey, otherID, []byte("cred-other"), []byte("pk-o"), strPtr("Other's Phone")); err != nil {
		t.Fatalf("insert other passkey: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(svc.RequireSession)
			h.MountAuthed(r)
		})
	})

	return &passkeyFixture{
		router:       r,
		svc:          svc,
		userID:       userID,
		freshCookie:  freshCookie,
		staleCookie:  staleCookie,
		otherUserID:  otherID,
		otherCookie:  otherCookie,
		myPasskeyID:  myPasskey,
		otherPasskey: otherPasskey,
	}
}

func strPtr(s string) *string { return &s }

func doPasskeyReq(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestListPasskeys_HappyPath(t *testing.T) {
	f := setupPasskeyFixture(t)
	rec := doPasskeyReq(t, f.router, "GET", "/api/v1/me/mfa/passkeys", f.freshCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []passkeyOut
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (own passkeys only — must not leak the other user's row)", len(got))
	}
	// ORDER BY created_at DESC: the most recent ("My Laptop") comes first.
	if got[0].ID != f.myPasskeyID {
		t.Fatalf("got[0].ID = %v, want %v (newest first)", got[0].ID, f.myPasskeyID)
	}
	if got[0].Label != "My Laptop" {
		t.Fatalf("got[0].Label = %q, want %q", got[0].Label, "My Laptop")
	}
	if got[1].Label != "" {
		t.Fatalf("got[1].Label = %q, want empty (NULL label maps to \"\")", got[1].Label)
	}
	if got[0].CreatedAt.IsZero() {
		t.Fatalf("got[0].CreatedAt is zero")
	}
}

func TestListPasskeys_EmptyForNewUser(t *testing.T) {
	f := setupPasskeyFixture(t)
	// Insert a brand-new user with no passkeys.
	freshUser := testdb.CreateTestUser(t, f.svc.pool, "fresh+"+uuid.New().String()+"@example.com", true)
	token, _ := GenerateSessionToken()
	sid := SessionIDFromToken(token)
	now := time.Now().UTC()
	if _, err := f.svc.pool.Exec(context.Background(), `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, reauth_at)
		values ($1, $2, $3, $4, $3, $3)
	`, sid, freshUser, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	cookie := &http.Cookie{Name: SessionCookieName(), Value: token}

	rec := doPasskeyReq(t, f.router, "GET", "/api/v1/me/mfa/passkeys", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Body must be `[]`, not `null` — clients iterate without a nil check.
	if body := rec.Body.String(); body != "[]\n" && body != "[]" {
		t.Fatalf("body = %q, want [] (must not be null)", body)
	}
}

func TestDeletePasskey_HappyPath(t *testing.T) {
	f := setupPasskeyFixture(t)
	rec := doPasskeyReq(t, f.router, "DELETE", "/api/v1/me/mfa/passkeys/"+f.myPasskeyID.String(), f.freshCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var exists bool
	if err := f.svc.pool.QueryRow(context.Background(),
		`select exists(select 1 from webauthn_credentials where id = $1)`, f.myPasskeyID,
	).Scan(&exists); err != nil {
		t.Fatalf("exists check: %v", err)
	}
	if exists {
		t.Fatalf("passkey row still present after delete")
	}
	// Audit row should exist.
	var auditCount int
	if err := f.svc.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'passkey.removed' and entity_id = $1`, f.myPasskeyID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

func TestDeletePasskey_OtherUsersPasskey(t *testing.T) {
	f := setupPasskeyFixture(t)
	// Primary user attempts to delete another user's passkey by id. The
	// query is scoped on (id, user_id) so the row should remain. We accept
	// the silent no-op (204).
	rec := doPasskeyReq(t, f.router, "DELETE", "/api/v1/me/mfa/passkeys/"+f.otherPasskey.String(), f.freshCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var exists bool
	if err := f.svc.pool.QueryRow(context.Background(),
		`select exists(select 1 from webauthn_credentials where id = $1)`, f.otherPasskey,
	).Scan(&exists); err != nil {
		t.Fatalf("exists check: %v", err)
	}
	if !exists {
		t.Fatalf("other user's passkey was deleted — row scoping broken")
	}
}

func TestDeletePasskey_NotFound(t *testing.T) {
	f := setupPasskeyFixture(t)
	missing := uuid.New()
	rec := doPasskeyReq(t, f.router, "DELETE", "/api/v1/me/mfa/passkeys/"+missing.String(), f.freshCookie)
	// DELETE on a non-existent id is a silent no-op (acceptable for v1).
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeletePasskey_RequiresFreshReauth(t *testing.T) {
	f := setupPasskeyFixture(t)
	rec := doPasskeyReq(t, f.router, "DELETE", "/api/v1/me/mfa/passkeys/"+f.myPasskeyID.String(), f.staleCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stale reauth code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "reauth_required" {
		t.Fatalf("code = %v, want reauth_required (body = %s)", body["code"], rec.Body.String())
	}
	// Row must still be there — the gate must short-circuit before the handler.
	var exists bool
	if err := f.svc.pool.QueryRow(context.Background(),
		`select exists(select 1 from webauthn_credentials where id = $1)`, f.myPasskeyID,
	).Scan(&exists); err != nil {
		t.Fatalf("exists check: %v", err)
	}
	if !exists {
		t.Fatalf("passkey deleted despite stale-reauth gate")
	}
}

func TestDeletePasskey_InvalidUUID(t *testing.T) {
	f := setupPasskeyFixture(t)
	rec := doPasskeyReq(t, f.router, "DELETE", "/api/v1/me/mfa/passkeys/not-a-uuid", f.freshCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "invalid_id" {
		t.Fatalf("code = %v, want invalid_id (body = %s)", body["code"], rec.Body.String())
	}
}
