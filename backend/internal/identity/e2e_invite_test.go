package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/config"
	foliohttp "github.com/xmedavid/folio/backend/internal/http"
	"github.com/xmedavid/folio/backend/internal/mailer"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// TestE2E_InviteRoundTrip exercises the full wire path: Alice signs up,
// creates an invite for Bob, the stub mailer captures the URL, Bob's
// unauthenticated preview works, Bob signs up consuming the token, and
// ends up with two memberships — Personal + Alice's workspace.
func TestE2E_InviteRoundTrip(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	// Per-run unique emails keep the test hermetic when running alongside
	// other identity tests that touch the same DB (no cross-run collisions
	// on users.email, no leftover audit rows to trip append-only triggers).
	suffix := strings.ReplaceAll(uuid.New().String(), "-", "")
	aliceEmail := "alice+" + suffix + "@example.com"
	bobEmail := "bob+" + suffix + "@example.com"
	t.Cleanup(func() { cleanupE2EByEmails(t, pool, aliceEmail, bobEmail) })

	// Required-env-variables for the router to boot; use local values.
	t.Setenv("APP_URL", "http://localhost:3000")
	t.Setenv("APP_ENV", "development")
	t.Setenv("REGISTRATION_MODE", "open")

	mockMail := mailer.NewLogMailer(nil)
	cfg := &config.Config{
		AppEnv:   "development",
		AppURL:   "http://localhost:3000",
		LogLevel: "info",
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	// Route httpx.WriteServiceError's warn through our buffer too.
	prevDefault := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(prevDefault)
		if t.Failed() {
			t.Logf("server logs:\n%s", logBuf.String())
		}
	})
	_ = io.Discard // keep import alive
	router := foliohttp.NewRouter(foliohttp.Deps{
		Logger: logger,
		DB:     pool,
		Cfg:    cfg,
		Mailer: mockMail,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// --- 1. Alice signs up ---------------------------------------------------
	aliceJar := &sessionJar{}
	aliceBody := doJSON(t, srv, aliceJar, http.MethodPost, "/api/v1/auth/signup",
		`{"email":"`+aliceEmail+`","password":"correct horse battery staple","displayName":"Alice","baseCurrency":"CHF","locale":"en-CH"}`,
		http.StatusCreated)
	var signupResp struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		Workspace struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"workspace"`
	}
	mustJSON(t, aliceBody, &signupResp)
	if signupResp.Workspace.ID == "" {
		t.Fatalf("no workspace id on signup: %s", aliceBody)
	}

	// The user needs email_verified_at to accept an invite. Signup-via-
	// invite bypasses that check; the reverse (authenticated accept)
	// doesn't. Seed verification for Alice directly (no email flow in
	// Plan 2 yet — Plan 3 wires it).
	if _, err := pool.Exec(ctx,
		`update users set email_verified_at = now() where id = $1`, signupResp.User.ID); err != nil {
		t.Fatalf("seed email verification: %v", err)
	}

	aliceWorkspaceID := signupResp.Workspace.ID

	// --- 2. Alice invites Bob ------------------------------------------------
	inviteBody := doJSON(t, srv, aliceJar, http.MethodPost,
		"/api/v1/t/"+aliceWorkspaceID+"/invites",
		`{"email":"`+bobEmail+`","role":"member"}`,
		http.StatusCreated)
	var inviteResp struct {
		Invite struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"invite"`
		AcceptURL string `json:"acceptUrl"`
	}
	mustJSON(t, inviteBody, &inviteResp)
	if inviteResp.Invite.Email != bobEmail {
		t.Fatalf("invite email = %q, want %q", inviteResp.Invite.Email, bobEmail)
	}
	if inviteResp.AcceptURL == "" {
		t.Fatalf("expected acceptUrl in response, got empty")
	}

	// --- 3. Mailer captured the invite (URL is also returned in the
	// createInvite response now; we extract the plaintext from there so the
	// test doesn't depend on the mailer template's data-key name).
	sent := mockMail.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 email, got %d", len(sent))
	}
	plaintext := extractInviteToken(t, inviteResp.AcceptURL)

	// --- 4. Unauthenticated preview works -----------------------------------
	previewBody := doRaw(t, srv, &sessionJar{}, http.MethodGet,
		"/api/v1/auth/invites/"+plaintext, "", http.StatusOK)
	var preview struct {
		WorkspaceName         string `json:"workspaceName"`
		InviterDisplayName string `json:"inviterDisplayName"`
		Email              string `json:"email"`
		Role               string `json:"role"`
	}
	mustJSON(t, previewBody, &preview)
	if preview.Email != bobEmail {
		t.Errorf("preview email = %q, want %q", preview.Email, bobEmail)
	}
	if preview.Role != "member" {
		t.Errorf("preview role = %q, want member", preview.Role)
	}

	// --- 5. Bob signs up consuming the invite -------------------------------
	bobJar := &sessionJar{}
	bobBody := doJSON(t, srv, bobJar, http.MethodPost, "/api/v1/auth/signup",
		`{"email":"`+bobEmail+`","password":"correct horse battery staple","displayName":"Bob","baseCurrency":"EUR","locale":"en-CH","inviteToken":"`+plaintext+`"}`,
		http.StatusCreated)
	var bobSignup struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	mustJSON(t, bobBody, &bobSignup)

	// --- 6. DB invariants ----------------------------------------------------
	var membershipCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from workspace_memberships where user_id = $1`, bobSignup.User.ID).Scan(&membershipCount); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if membershipCount != 2 {
		t.Fatalf("bob should have 2 memberships (Personal + Alice's), got %d", membershipCount)
	}

	var accepted bool
	if err := pool.QueryRow(ctx,
		`select accepted_at is not null from workspace_invites where email = $1`, bobEmail).Scan(&accepted); err != nil {
		t.Fatalf("check invite: %v", err)
	}
	if !accepted {
		t.Fatal("invite.accepted_at is still NULL — signup did not consume the invite")
	}

	// Audit: one member.invite_accepted row for Alice's workspace (Bob's actor).
	var auditCount int
	_ = pool.QueryRow(ctx,
		`select count(*) from audit_events where action = 'member.invite_accepted' and workspace_id = $1 and actor_user_id = $2`,
		aliceWorkspaceID, bobSignup.User.ID).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("want 1 member.invite_accepted audit row, got %d", auditCount)
	}
}

// ---- helpers ---------------------------------------------------------------

// cleanupE2EByEmails wipes the rows this test writes for the given email
// pair. audit_events has append-only UPDATE/DELETE triggers from Plan 1
// §1; we bypass them in a scoped tx via session_replication_role = replica
// (same session only). Parallel tests that run outside this tx are
// unaffected. Accepts email parameters so each test run scopes cleanup
// to its own per-run suffix and doesn't disturb other tests' data.
func cleanupE2EByEmails(t *testing.T, pool *pgxpool.Pool, emails ...string) {
	t.Helper()
	if len(emails) == 0 {
		return
	}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("cleanup begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	// Delete any pending invites that reference the test emails but
	// belong to workspaces we don't own (inviter is someone else).
	if _, err := tx.Exec(ctx,
		`delete from workspace_invites where email = any($1)`, emails); err != nil {
		t.Fatalf("cleanup invites: %v", err)
	}
	// Delete workspaces where any test user is a member — cascades to
	// audit_events and workspace_memberships (triggers disabled).
	if _, err := tx.Exec(ctx, `
		delete from workspaces where id in (
			select distinct m.workspace_id from workspace_memberships m
			join users u on u.id = m.user_id
			where u.email = any($1)
		)
	`, emails); err != nil {
		t.Fatalf("cleanup workspaces: %v", err)
	}
	// Delete the users — cascades sessions and sets audit.actor_user_id
	// to NULL for any cross-workspace audit rows left behind.
	if _, err := tx.Exec(ctx,
		`delete from users where email = any($1)`, emails); err != nil {
		t.Fatalf("cleanup users: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("cleanup commit: %v", err)
	}
}

type sessionJar struct {
	cookie string
}

func doJSON(t *testing.T, srv *httptest.Server, jar *sessionJar, method, path, body string, wantStatus int) []byte {
	t.Helper()
	return doRaw(t, srv, jar, method, path, body, wantStatus)
}

func doRaw(t *testing.T, srv *httptest.Server, jar *sessionJar, method, path, body string, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("%s %s: build req: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// CSRF gate: every state-changing request needs an allowed Origin + the
	// X-Folio-Request header. Default-allow the dev URL from APP_URL.
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-Folio-Request", "1")
	if jar != nil && jar.cookie != "" {
		req.Header.Set("Cookie", jar.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: got %d (want %d), body=%s", method, path, resp.StatusCode, wantStatus, respBody)
	}
	// Capture the session cookie if one was set. Ignore Max-Age=0 clears.
	for _, c := range resp.Cookies() {
		if c.Name == "folio_session" && c.Value != "" {
			jar.cookie = c.Name + "=" + c.Value
		}
	}
	return respBody
}

func mustJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
}

var inviteTokenPattern = regexp.MustCompile(`/accept-invite/([A-Za-z0-9_-]+)`)

func extractInviteToken(t *testing.T, inviteURL string) string {
	t.Helper()
	m := inviteTokenPattern.FindStringSubmatch(inviteURL)
	if len(m) != 2 {
		t.Fatalf("could not extract invite token from URL: %q", inviteURL)
	}
	return m[1]
}
