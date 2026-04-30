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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

type workspaceAdminFixture struct {
	router     http.Handler
	pool       *pgxpool.Pool
	workspace  uuid.UUID
	otherWS    uuid.UUID
	owner      uuid.UUID
	otherOwner uuid.UUID
	member     uuid.UUID
	freshOwner *http.Cookie
	staleOwner *http.Cookie
}

func setupWorkspaceAdminFixture(t *testing.T) *workspaceAdminFixture {
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

	workspace, _ := testdb.CreateTestWorkspace(t, pool, "Workspace Admin")
	otherWS, _ := testdb.CreateTestWorkspace(t, pool, "Other Workspace")
	owner := testdb.CreateTestUser(t, pool, "owner+"+uuid.New().String()+"@example.com", true)
	otherOwner := testdb.CreateTestUser(t, pool, "other-owner+"+uuid.New().String()+"@example.com", true)
	member := testdb.CreateTestUser(t, pool, "member+"+uuid.New().String()+"@example.com", true)
	testdb.CreateTestMembership(t, pool, workspace, owner, "owner")
	testdb.CreateTestMembership(t, pool, workspace, otherOwner, "owner")
	testdb.CreateTestMembership(t, pool, workspace, member, "member")
	testdb.CreateTestMembership(t, pool, otherWS, owner, "owner")

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
			h.MountWorkspaceAdmin(r)
		})
	})

	return &workspaceAdminFixture{
		router:     r,
		pool:       pool,
		workspace:  workspace,
		otherWS:    otherWS,
		owner:      owner,
		otherOwner: otherOwner,
		member:     member,
		freshOwner: mkSession(owner, true),
		staleOwner: mkSession(owner, false),
	}
}

func doWorkspaceAdminDelete(t *testing.T, f *workspaceAdminFixture, userID uuid.UUID, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/api/v1/t/"+f.workspace.String()+"/members/"+userID.String(), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func TestRemoveOtherMemberRequiresFreshReauth(t *testing.T) {
	f := setupWorkspaceAdminFixture(t)
	rec := doWorkspaceAdminDelete(t, f, f.member, f.staleOwner)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "reauth_required" {
		t.Fatalf("code = %v, want reauth_required (body = %s)", body["code"], rec.Body.String())
	}

	var count int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from workspace_memberships where workspace_id = $1 and user_id = $2`,
		f.workspace, f.member,
	).Scan(&count); err != nil {
		t.Fatalf("membership count: %v", err)
	}
	if count != 1 {
		t.Fatalf("membership count = %d, want 1", count)
	}
}

func TestSelfLeaveDoesNotRequireFreshReauth(t *testing.T) {
	f := setupWorkspaceAdminFixture(t)
	rec := doWorkspaceAdminDelete(t, f, f.owner, f.staleOwner)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}
