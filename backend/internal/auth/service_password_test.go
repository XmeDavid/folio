package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// passwordFixture mirrors passkeyFixture's shape: a primary user with two
// real sessions (A = "other", B = "current") backed by a hashed password we
// know the plaintext for, plus a stale-reauth cookie for the gate test.
type passwordFixture struct {
	router       http.Handler
	svc          *Service
	pool         interface { /* matches *pgxpool.Pool minimal surface */ }
	userID       uuid.UUID
	currentPass  string
	currentSID   string
	otherSID     string
	freshCookie  *http.Cookie
	staleCookie  *http.Cookie
	otherCookie  *http.Cookie
}

func setupPasswordFixture(t *testing.T) *passwordFixture {
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

	// Test fixture only — production pepper comes from cfg.SecretKey.
	pepper := bytes.Repeat([]byte{'a'}, 32)
	svc := NewService(pool, identity.NewService(pool), identity.NewPlatformInviteService(pool), Config{
		Registration:  RegistrationOpen,
		SecretKey:     pepper,
		SecureCookies: false,
		ReauthWindow:  5 * time.Minute,
	})
	h := NewHandler(svc)

	email := "user+" + uuid.New().String() + "@example.com"
	userID := testdb.CreateTestUser(t, pool, email, true)

	// Replace the stubbed password hash so we can verify "current" via the
	// real path. Display name is the email (testdb default), so the policy's
	// "no name in password" rule won't trip on a generic correct-horse string.
	current := "Correct-Horse-Battery-Staple-9"
	hash, err := HashPassword(current, pepper)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := pool.Exec(ctx, `update users set password_hash=$1 where id=$2`, hash, userID); err != nil {
		t.Fatalf("set password: %v", err)
	}

	mkSession := func(fresh bool) (string, *http.Cookie) {
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
		`, sid, userID, now, now.Add(24*time.Hour), reauthAt); err != nil {
			t.Fatalf("insert session: %v", err)
		}
		return sid, &http.Cookie{Name: SessionCookieName(), Value: token}
	}
	currentSID, freshCookie := mkSession(true)
	_, staleCookie := mkSession(false)
	otherSID, otherCookie := mkSession(true)

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(svc.RequireSession)
			h.MountAuthed(r)
		})
	})

	return &passwordFixture{
		router:      r,
		svc:         svc,
		pool:        pool,
		userID:      userID,
		currentPass: current,
		currentSID:  currentSID,
		otherSID:    otherSID,
		freshCookie: freshCookie,
		staleCookie: staleCookie,
		otherCookie: otherCookie,
	}
}

// --- service-level tests ----------------------------------------------------

func TestChangePassword_HappyPath(t *testing.T) {
	f := setupPasswordFixture(t)
	ctx := context.Background()
	pool := testdb.Open(t)

	next := "Brand-New-Strong-Pass-123!"
	if err := f.svc.ChangePassword(ctx, f.userID, f.currentSID, f.currentPass, next); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// New password verifies.
	var newHash string
	if err := pool.QueryRow(ctx, `select password_hash from users where id=$1`, f.userID).Scan(&newHash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	ok, err := VerifyPassword(next, newHash, f.svc.cfg.SecretKey)
	if err != nil || !ok {
		t.Fatalf("new password did not verify (ok=%v err=%v)", ok, err)
	}

	// Current session B survives, other session A is gone.
	var hasCurrent, hasOther bool
	_ = pool.QueryRow(ctx, `select exists(select 1 from sessions where id=$1)`, f.currentSID).Scan(&hasCurrent)
	_ = pool.QueryRow(ctx, `select exists(select 1 from sessions where id=$1)`, f.otherSID).Scan(&hasOther)
	if !hasCurrent {
		t.Fatalf("current session was revoked — must stay")
	}
	if hasOther {
		t.Fatalf("other session was not revoked")
	}

	// Audit row exists.
	var auditCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from audit_events where action='user.password_changed' and actor_user_id=$1`, f.userID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	f := setupPasswordFixture(t)
	ctx := context.Background()
	pool := testdb.Open(t)

	var beforeHash string
	_ = pool.QueryRow(ctx, `select password_hash from users where id=$1`, f.userID).Scan(&beforeHash)

	err := f.svc.ChangePassword(ctx, f.userID, f.currentSID, "not-the-password", "Brand-New-Strong-Pass-123!")
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "current password is incorrect") {
		t.Fatalf("err = %q, want containing 'current password is incorrect'", err.Error())
	}

	var afterHash string
	_ = pool.QueryRow(ctx, `select password_hash from users where id=$1`, f.userID).Scan(&afterHash)
	if beforeHash != afterHash {
		t.Fatalf("password hash changed despite wrong current")
	}

	// Other session still alive.
	var hasOther bool
	_ = pool.QueryRow(ctx, `select exists(select 1 from sessions where id=$1)`, f.otherSID).Scan(&hasOther)
	if !hasOther {
		t.Fatalf("other session was revoked despite failed change")
	}
}

func TestChangePassword_RejectsWeakNewPassword(t *testing.T) {
	f := setupPasswordFixture(t)
	ctx := context.Background()

	// "short" — under 12 chars; policy rejects before hashing.
	err := f.svc.ChangePassword(ctx, f.userID, f.currentSID, f.currentPass, "short")
	if err == nil {
		t.Fatalf("expected policy error, got nil")
	}
	if !strings.Contains(err.Error(), "12 characters") {
		t.Fatalf("err = %q, want containing '12 characters'", err.Error())
	}

	// Other session still alive — policy failure must abort side effects.
	pool := testdb.Open(t)
	var hasOther bool
	_ = pool.QueryRow(ctx, `select exists(select 1 from sessions where id=$1)`, f.otherSID).Scan(&hasOther)
	if !hasOther {
		t.Fatalf("other session was revoked despite policy failure")
	}
}

func TestChangePassword_NextSameAsCurrent_RevokesOtherSessions(t *testing.T) {
	// Documented behaviour: we treat next == current as a real change — the
	// policy doesn't disallow it, the user explicitly asked for it, and the
	// audit + revocation side effects still fire so a "rotate everything"
	// workflow stays predictable.
	f := setupPasswordFixture(t)
	ctx := context.Background()
	pool := testdb.Open(t)

	if err := f.svc.ChangePassword(ctx, f.userID, f.currentSID, f.currentPass, f.currentPass); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	var hasOther bool
	_ = pool.QueryRow(ctx, `select exists(select 1 from sessions where id=$1)`, f.otherSID).Scan(&hasOther)
	if hasOther {
		t.Fatalf("other session not revoked when next == current")
	}
	var auditCount int
	_ = pool.QueryRow(ctx,
		`select count(*) from audit_events where action='user.password_changed' and actor_user_id=$1`, f.userID,
	).Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

// --- handler tests ----------------------------------------------------------

func doPasswordReq(t *testing.T, h http.Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/me/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestChangePasswordHTTP_Success(t *testing.T) {
	f := setupPasswordFixture(t)
	body, _ := json.Marshal(changePasswordReq{Current: f.currentPass, Next: "Brand-New-Strong-Pass-123!"})
	rec := doPasswordReq(t, f.router, string(body), f.freshCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestChangePasswordHTTP_RequiresFreshReauth(t *testing.T) {
	f := setupPasswordFixture(t)
	body, _ := json.Marshal(changePasswordReq{Current: f.currentPass, Next: "Brand-New-Strong-Pass-123!"})
	rec := doPasswordReq(t, f.router, string(body), f.staleCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != "reauth_required" {
		t.Fatalf("code = %v, want reauth_required (body = %s)", resp["code"], rec.Body.String())
	}
}

func TestChangePasswordHTTP_BadJSON(t *testing.T) {
	f := setupPasswordFixture(t)
	rec := doPasswordReq(t, f.router, "not-json", f.freshCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != "invalid_body" {
		t.Fatalf("code = %v, want invalid_body", resp["code"])
	}
}
