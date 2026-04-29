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
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// lastWorkspaceFixture wires the production middleware stack
// (RequireSession) for /me/last-workspace and seeds a user with a session
// cookie plus two workspaces — one the user is a member of, one they're not.
type lastWorkspaceFixture struct {
	router      http.Handler
	svc         *Service
	userID      uuid.UUID
	cookie      *http.Cookie
	memberWS    uuid.UUID
	nonMemberWS uuid.UUID
	deletedWS   uuid.UUID
}

func setupLastWorkspaceFixture(t *testing.T) *lastWorkspaceFixture {
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

	userEmail := "user+" + uuid.New().String() + "@example.com"
	userID := testdb.CreateTestUser(t, pool, userEmail, true)

	memberWS, _ := testdb.CreateTestWorkspace(t, pool, "Member Workspace")
	nonMemberWS, _ := testdb.CreateTestWorkspace(t, pool, "Stranger Workspace")
	testdb.CreateTestMembership(t, pool, memberWS, userID, "owner")

	// A workspace the user joined that is then soft-deleted: should be
	// indistinguishable from a missing workspace (404), not 403.
	deletedWS, _ := testdb.CreateTestWorkspace(t, pool, "Deleted Workspace")
	testdb.CreateTestMembership(t, pool, deletedWS, userID, "owner")
	if _, err := pool.Exec(ctx, `update workspaces set deleted_at = now() where id = $1`, deletedWS); err != nil {
		t.Fatalf("soft-delete workspace: %v", err)
	}

	// Insert a session row + matching plaintext cookie.
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

	return &lastWorkspaceFixture{
		router:      r,
		svc:         svc,
		userID:      userID,
		cookie:      cookie,
		memberWS:    memberWS,
		nonMemberWS: nonMemberWS,
		deletedWS:   deletedWS,
	}
}

func patchLastWorkspace(t *testing.T, h http.Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/api/v1/me/last-workspace", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPatchLastWorkspace_HappyPath(t *testing.T) {
	f := setupLastWorkspaceFixture(t)
	body := `{"workspaceId":"` + f.memberWS.String() + `"}`
	rec := patchLastWorkspace(t, f.router, body, f.cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got *uuid.UUID
	if err := f.svc.pool.QueryRow(context.Background(),
		`select last_workspace_id from users where id = $1`, f.userID,
	).Scan(&got); err != nil {
		t.Fatalf("select last_workspace_id: %v", err)
	}
	if got == nil || *got != f.memberWS {
		t.Fatalf("last_workspace_id = %v, want %v", got, f.memberWS)
	}
}

func TestPatchLastWorkspace_NotAMember(t *testing.T) {
	f := setupLastWorkspaceFixture(t)
	body := `{"workspaceId":"` + f.nonMemberWS.String() + `"}`
	rec := patchLastWorkspace(t, f.router, body, f.cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != "not_a_member" {
		t.Fatalf("code = %v, want not_a_member (body = %s)", resp["code"], rec.Body.String())
	}
}

func TestPatchLastWorkspace_WorkspaceNotFound(t *testing.T) {
	f := setupLastWorkspaceFixture(t)

	// A random UUID that simply doesn't exist.
	missing := uuid.New()
	body := `{"workspaceId":"` + missing.String() + `"}`
	rec := patchLastWorkspace(t, f.router, body, f.cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing workspace code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != "workspace_not_found" {
		t.Fatalf("code = %v, want workspace_not_found", resp["code"])
	}

	// Soft-deleted workspace the user *was* a member of should also map to
	// 404 — soft-deleted is treated as "not there" by the membership check.
	deletedBody := `{"workspaceId":"` + f.deletedWS.String() + `"}`
	rec2 := patchLastWorkspace(t, f.router, deletedBody, f.cookie)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("deleted workspace code = %d, body = %s", rec2.Code, rec2.Body.String())
	}
}

func TestPatchLastWorkspace_InvalidUUID(t *testing.T) {
	f := setupLastWorkspaceFixture(t)
	rec := patchLastWorkspace(t, f.router, `{"workspaceId":"not-a-uuid"}`, f.cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"] != "invalid_id" {
		t.Fatalf("code = %v, want invalid_id (body = %s)", resp["code"], rec.Body.String())
	}
}
