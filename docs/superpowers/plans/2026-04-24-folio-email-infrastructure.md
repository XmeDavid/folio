# Folio Email Infrastructure & Flows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land Folio's transactional email stack — Resend delivery, River queue/retries/periodic jobs, a pluggable `mailer.Mailer` abstraction, and every email-driven auth flow (email verification, password reset, email change) with corresponding Next.js pages and an email-verified gate middleware.

**Architecture:** `mailer.Mailer` is an interface implemented by `LogMailer` (from plan 2, used in tests and empty-key dev) and `ResendMailer` (new, prod). River owns queuing, retry, and periodic scheduling; workers dequeue `SendEmailArgs` jobs and call `mailer.Mailer.Send`. Auth flows mutate `auth_tokens` rows synchronously in the service layer, then enqueue a single `SendEmail` River job — the HTTP response does not wait for SMTP. A `SoftDeletedWorkspaceSweeper` periodic River job wraps plan 2's `folio-sweeper` `Sweep()` logic. `auth.RequireEmailVerified` is applied to sensitive routes.

**Tech Stack:** Go 1.25; `github.com/riverqueue/river` (v0.12+); `github.com/riverqueue/river/riverdriver/riverpgxv5`; `github.com/riverqueue/river/rivermigrate`; `github.com/resend/resend-go/v2`; `html/template` + `text/template`; Postgres 17; Next.js 16; shared Resend delivery for dev (or `LogMailer` fallback when `RESEND_API_KEY` is empty).

**Spec:** `docs/superpowers/specs/2026-04-24-folio-auth-and-workspace-design.md`

**Prior plans in series:**
- `docs/superpowers/plans/2026-04-24-folio-auth-foundation.md` (plan 1) — `auth` package, middleware, signup/login/logout, `auth_tokens` & `sessions` schema, frontend login/signup/workspaces picker.
- `docs/superpowers/plans/2026-04-24-folio-invites-and-workspace-lifecycle.md` (plan 2) — `identity.InviteService`, interface-only `mailer.Mailer` with `LogMailer` stub, `folio-sweeper` binary.

**Follow-up plans in series:**
- Plan 4 — passkeys, TOTP, MFA, step-up re-auth.
- Plan 5 — admin console, billing hooks.

---

## 0. Shared setup

### 0.1 Working directory

All Go commands run in `backend/`:

```bash
cd /Users/xmedavid/dev/folio/backend
```

Postgres must be running (same compose stack as prior plans):

```bash
cd /Users/xmedavid/dev/folio
docker compose -f docker-compose.dev.yml up -d
```

`DATABASE_URL` must be exported (see `.env.example`). River uses the same connection.

### 0.2 Environment variables

Add to `.env.example` (committed) and `docker-compose.dev.yml` (dev service env):

| Name | Dev default | Purpose |
|---|---|---|
| `RESEND_API_KEY` | *(empty)* | Resend API key. Empty → `LogMailer` is wired in prod factory (dev-convenience). |
| `EMAIL_FROM` | `Folio <onboarding@localhost>` | RFC-5322 From header. |
| `APP_URL` | `http://localhost:3000` | Base URL used to build `{{.VerifyURL}}`, `{{.ResetURL}}`, etc. |
| `DATABASE_URL` | unchanged | Shared with River. |

When `RESEND_API_KEY` is empty the prod factory falls back to `LogMailer` so developers don't need a real Resend account. Tests use `LogMailer` unconditionally.

### 0.3 Migration strategy — Atlas + River side-by-side

Atlas continues to own Folio's application schema under `backend/db/migrations/`. River ships its own migration set for its job tables; we run it through `rivermigrate` from a small Go command so River's table versions track the River library version rather than Atlas.

The dev reset flow becomes:

```bash
# from backend/
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
go run ./cmd/folio-river-migrate up
```

`cmd/folio-river-migrate` is created in Task 2. Document this order in `backend/README.md` (or project `README.md` if that's the convention — inspect `git log` first) as part of Task 2.

### 0.4 Canonical package layout added by this plan

```
backend/internal/mailer/
  mailer.go                    # (exists from plan 2: interface + LogMailer)
  resend.go                    # NEW: ResendMailer
  resend_test.go               # NEW
  template.go                  # NEW: Template type + render helpers
  template_test.go             # NEW
  templates/                   # NEW
    verify_email.html.tmpl
    verify_email.txt.tmpl
    password_reset.html.tmpl
    password_reset.txt.tmpl
    email_change_new.html.tmpl
    email_change_new.txt.tmpl
    email_change_old_notice.html.tmpl
    email_change_old_notice.txt.tmpl
    invite.html.tmpl            # (plan 2 declares the struct; this plan renders it)
    invite.txt.tmpl
    _layout.html.tmpl           # shared header/footer
  templates_embed.go           # NEW: //go:embed templates/*

backend/internal/jobs/
  client.go                    # NEW: River client wrapper + config
  client_test.go               # NEW
  args.go                      # NEW: SendEmailArgs, SweepSoftDeletedWorkspacesArgs, etc.
  send_email_worker.go         # NEW
  send_email_worker_test.go    # NEW
  sweep_soft_deleted_workspaces_worker.go  # NEW
  sweep_soft_deleted_workspaces_worker_test.go  # NEW
  periodic.go                  # NEW: periodic-job registration

backend/cmd/folio-river-migrate/main.go  # NEW

backend/internal/auth/
  email_flows.go               # NEW: SendEmailVerification, VerifyEmail, ResendEmailVerification,
                               #      RequestPasswordReset, ResetPassword,
                               #      RequestEmailChange, ConfirmEmailChange
  email_flows_test.go          # NEW
  require_email_verified.go    # NEW
  require_email_verified_test.go  # NEW
  ratelimit_email_flows.go     # NEW: wires plan 1's token-bucket limiter to the new endpoints
  handlers_email_flows.go      # NEW: HTTP handlers for /auth/verify, /auth/password/*, /auth/email/*
  handlers_email_flows_test.go # NEW
```

```
web/app/auth/verify/[token]/page.tsx
web/app/forgot/page.tsx
web/app/reset/[token]/page.tsx
web/app/auth/email/confirm/[token]/page.tsx
web/app/settings/account/page.tsx
web/components/verify-email-banner.tsx
web/lib/auth/email-flows.ts           # fetch wrappers for the 6 new endpoints
```

### 0.5 Shared code patterns

#### P1 — Token generation and hashing

All single-use tokens use the same shape (matches plan 1's `auth_tokens` table and plan 2's `workspace_invites.token_hash`):

```go
// In backend/internal/auth/tokens.go (exists from plan 1).
//   GenerateToken()        -> (plaintext string, hash []byte) where plaintext is
//                             base64url(32 random bytes) and hash is sha256(plaintext).
//   HashToken(plaintext)   -> []byte
```

Every new email flow in this plan uses `GenerateToken` for issuance and `HashToken` for verification.

#### P2 — Enqueue-after-commit

Every flow that mutates DB state and fires an email uses `river.Client.InsertTx` inside the same `pgx.Tx` that wrote the DB row. If the tx rolls back, the job never exists. Example skeleton:

```go
tx, err := s.pool.Begin(ctx)
defer tx.Rollback(ctx)
// insert auth_tokens row via tx
if _, err := s.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{...}, nil); err != nil {
    return err
}
return tx.Commit(ctx)
```

#### P3 — Template rendering

Templates are rendered from per-template structs and emit both HTML and text parts. `mailer.Template.Render(data any) (mailer.Message, error)` returns a fully populated `Message`. Templates share `_layout.html.tmpl` via `{{ template "layout" . }}`.

#### P4 — Rate-limit keys

| Endpoint | Bucket key | Limit |
|---|---|---|
| `POST /auth/verify/resend` | `verify-resend:user:<userID>` | 1 / min, 5 / hr |
| `POST /auth/password/reset-request` | `reset-request:ip:<ip>` AND `reset-request:email:<lowercased>` | 3 / hr each |
| `POST /auth/email/change-request` | `email-change:user:<userID>` | 3 / hr |

Uses plan 1's in-memory token-bucket limiter (`auth.RateLimiter`) swappable per spec §8.2.

#### P5 — Audit-event writes

Every successful flow writes an `audit_events` row via the existing helper in `backend/internal/audit` (from domain-v2 plan). Events: `user.email_verified`, `user.password_reset_completed`, `user.email_change_requested`, `user.email_change_confirmed`. `workspace_id = NULL` because these are user-scoped.

### 0.6 Per-task verification baseline

Every task that touches Go code ends with:

```bash
cd /Users/xmedavid/dev/folio/backend
go build ./...
go test ./internal/mailer/... ./internal/jobs/... ./internal/auth/...
```

Every task that touches frontend ends with:

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm test
```

Each task section lists additional verification specific to the code it wrote.

---

## Task 1: Add dependencies and template layout

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`
- Create: `backend/internal/mailer/templates/_layout.html.tmpl`
- Create: `backend/internal/mailer/templates_embed.go`

- [ ] **Step 1: Add Go module dependencies**

```bash
cd /Users/xmedavid/dev/folio/backend
go get github.com/riverqueue/river@v0.12.0
go get github.com/riverqueue/river/riverdriver/riverpgxv5@v0.12.0
go get github.com/riverqueue/river/rivermigrate@v0.12.0
go get github.com/resend/resend-go/v2@v2.10.0
go mod tidy
```

Expected: `go.mod` gains the four `require` lines; `go.sum` updates.

- [ ] **Step 2: Create the shared layout template**

Write `backend/internal/mailer/templates/_layout.html.tmpl`:

```html
{{ define "layout" -}}
<!doctype html>
<html>
  <head><meta charset="utf-8"><title>{{ .Subject }}</title></head>
  <body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
               max-width: 560px; margin: 40px auto; color: #222;">
    <h1 style="font-size: 20px; margin: 0 0 24px;">Folio</h1>
    {{ template "content" . }}
    <hr style="border: none; border-top: 1px solid #eee; margin: 32px 0;">
    <p style="font-size: 12px; color: #888;">
      You received this email because someone used your address at Folio.
      If that wasn't you, you can safely ignore this message.
    </p>
  </body>
</html>
{{- end }}
```

- [ ] **Step 3: Create the embed file**

Write `backend/internal/mailer/templates_embed.go`:

```go
package mailer

import "embed"

//go:embed templates/*.tmpl
var templateFS embed.FS
```

- [ ] **Step 4: Verification**

```bash
go build ./...
```

Expected: exit 0. (No templates use this FS yet; embedding a non-empty file pattern is sufficient.)

- [ ] **Step 5: Commit**

```bash
git add backend/go.mod backend/go.sum backend/internal/mailer/
git commit -m "$(cat <<'EOF'
feat(mailer): add River + Resend deps, shared email layout

Introduces the shared HTML layout template and the embed.FS used by
subsequent template tasks. River and Resend are pulled in ahead of the
worker and mailer implementations.
EOF
)"
```

---

## Task 2: River migrations and `folio-river-migrate` binary

**Files:**
- Create: `backend/cmd/folio-river-migrate/main.go`
- Modify: `README.md` (or `backend/README.md` if present) — document the two-phase migrate flow.

- [ ] **Step 1: Write the migrator command**

`backend/cmd/folio-river-migrate/main.go`:

```go
// Command folio-river-migrate applies River's queue tables.
// Atlas owns the app schema; this binary owns River's internal tables only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func main() {
	direction := flag.String("direction", "up", "up|down")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrator:", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	switch *direction {
	case "up":
		res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "migrate up:", err)
			os.Exit(1)
		}
		for _, v := range res.Versions {
			logger.Info("river migrate up", "version", v.Version, "name", v.Name)
		}
	case "down":
		res, err := migrator.Migrate(ctx, rivermigrate.DirectionDown, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "migrate down:", err)
			os.Exit(1)
		}
		for _, v := range res.Versions {
			logger.Info("river migrate down", "version", v.Version, "name", v.Name)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown direction:", *direction)
		os.Exit(2)
	}
}
```

- [ ] **Step 2: Document the flow**

Append to `README.md` under a "Local development" section (preserve existing heading levels):

```markdown
### Database migrations

Folio's own schema is managed by Atlas (`atlas migrate apply --env local`).
River's queue tables are separate — they track the River library's own
version schedule. Apply them with:

```bash
go run ./cmd/folio-river-migrate up
```

Run Atlas first, then the River migrator. The full reset is:

```bash
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
go run ./cmd/folio-river-migrate up
```
```
```

(Inspect `README.md` first to locate the right subsection; if a "Getting Started" or "Backend" block already exists, insert the snippet there and leave surrounding prose intact.)

- [ ] **Step 3: Verification**

```bash
cd /Users/xmedavid/dev/folio/backend
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
go run ./cmd/folio-river-migrate up
psql "$DATABASE_URL" -c "\dt river*"
```

Expected: the last command lists `river_job`, `river_leader`, `river_queue` (exact names depend on River version — any non-empty list confirms the migrator ran).

- [ ] **Step 4: Commit**

```bash
git add backend/cmd/folio-river-migrate/ README.md
git commit -m "$(cat <<'EOF'
feat(jobs): add folio-river-migrate command

River's migrations are tracked by the River library, not Atlas.
This binary applies or rolls them back against DATABASE_URL and is
invoked in the documented dev reset flow.
EOF
)"
```

---

## Task 3: `jobs.Client` wrapper and config

**Files:**
- Create: `backend/internal/jobs/client.go`
- Create: `backend/internal/jobs/client_test.go`
- Create: `backend/internal/jobs/args.go`

- [ ] **Step 1: Write `args.go`**

```go
// Package jobs hosts River job arguments and the Client wrapper.
package jobs

import (
	"github.com/google/uuid"
)

// SendEmailArgs is the only job args type needed by the auth flows — the
// worker looks up the template, renders it, and hands off to the configured
// mailer.Mailer.
type SendEmailArgs struct {
	TemplateName string         `json:"template"`          // e.g. "verify_email"
	ToAddress    string         `json:"to"`                // the recipient email
	Data         map[string]any `json:"data"`              // template data as JSON-safe values
	IdempotencyKey string       `json:"idempotency_key"`   // e.g. "verify_email:<tokenID>"
}

func (SendEmailArgs) Kind() string { return "send_email" }

// SweepSoftDeletedWorkspacesArgs drives plan 2's folio-sweeper Sweep() logic
// from a periodic River job rather than the standalone binary.
type SweepSoftDeletedWorkspacesArgs struct{}

func (SweepSoftDeletedWorkspacesArgs) Kind() string { return "sweep_soft_deleted_workspaces" }

// ResolveWorkspaceMembershipArgs is reserved for future workspace-invite flows
// that need asynchronous post-processing; left empty here intentionally.
// (Delete this comment if plan 4 doesn't materialise the job.)
type _ struct{ _ uuid.UUID }
```

(Drop the trailing blank type if linting complains — it's there only to keep `uuid` imported for plan 4.)

- [ ] **Step 2: Write `client.go`**

```go
package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Client wraps *river.Client[pgx.Tx] so callers never have to import River
// types directly. Workers register via RegisterWorker; enqueues go through
// Insert or InsertTx.
type Client struct {
	inner *river.Client[pgx.Tx]
}

// Config controls Client construction.
type Config struct {
	Queues map[string]river.QueueConfig
}

// NewClient builds a Client with registered workers. Callers pass the
// *river.Workers bundle after RegisterWorker has been called on it.
func NewClient(pool *pgxpool.Pool, workers *river.Workers, cfg Config) (*Client, error) {
	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  cfg.Queues,
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Client{inner: rc}, nil
}

// Start launches the worker pool. Close with Stop.
func (c *Client) Start(ctx context.Context) error { return c.inner.Start(ctx) }

// Stop drains in-flight jobs up to the ctx deadline.
func (c *Client) Stop(ctx context.Context) error { return c.inner.Stop(ctx) }

// Insert enqueues a job outside a transaction.
func (c *Client) Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*river.JobInsertResult, error) {
	return c.inner.Insert(ctx, args, opts)
}

// InsertTx enqueues a job inside an existing pgx.Tx so the job lifecycle is
// bound to the surrounding commit.
func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*river.JobInsertResult, error) {
	return c.inner.InsertTx(ctx, tx, args, opts)
}

// Inner exposes the raw River client for test harnesses (e.g. synchronous
// inline execution via rivertest.InsertInline).
func (c *Client) Inner() *river.Client[pgx.Tx] { return c.inner }
```

- [ ] **Step 3: Write `client_test.go`**

Test: construct a Client against the dev DB with zero workers registered and confirm Start/Stop round-trips cleanly. Uses `testing.Short()` to skip in CI if no DB.

```go
package jobs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

func TestClient_StartStop(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	workers := river.NewWorkers()
	c, err := NewClient(pool, workers, Config{
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 4: Verification**

```bash
go build ./...
go test ./internal/jobs/...
```

Expected: tests pass (or skip with `DATABASE_URL not set`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/jobs/
git commit -m "$(cat <<'EOF'
feat(jobs): add River client wrapper and job args

Client wraps river.Client[pgx.Tx] for Insert/InsertTx without leaking
River types to service callers. SendEmailArgs and
SweepSoftDeletedWorkspacesArgs are declared; workers land in later tasks.
EOF
)"
```

---

## Task 4: `mailer.ResendMailer` implementation (TDD)

**Files:**
- Create: `backend/internal/mailer/resend.go`
- Create: `backend/internal/mailer/resend_test.go`

- [ ] **Step 1: Write the failing test first**

`backend/internal/mailer/resend_test.go`:

```go
package mailer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendMailer_Send_PostsToResendAPI(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"id":"abc123"}`))
	}))
	defer srv.Close()

	m := NewResendMailer("key_test", "Folio <no-reply@folio.app>", WithBaseURL(srv.URL))
	err := m.Send(context.Background(), Message{
		To:      "user@example.com",
		Subject: "Verify your email",
		HTML:    "<p>hi</p>",
		Text:    "hi",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("missing bearer auth: %q", gotAuth)
	}
	if gotBody["from"] != "Folio <no-reply@folio.app>" {
		t.Fatalf("bad from: %v", gotBody["from"])
	}
	if gotBody["subject"] != "Verify your email" {
		t.Fatalf("bad subject: %v", gotBody["subject"])
	}
	if gotBody["html"] != "<p>hi</p>" {
		t.Fatalf("bad html: %v", gotBody["html"])
	}
	to, ok := gotBody["to"].([]any)
	if !ok || len(to) != 1 || to[0] != "user@example.com" {
		t.Fatalf("bad to: %v", gotBody["to"])
	}
}

func TestResendMailer_Send_Returns4xxAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad"}`))
	}))
	defer srv.Close()

	m := NewResendMailer("k", "f@x.co", WithBaseURL(srv.URL))
	if err := m.Send(context.Background(), Message{To: "u@x.co", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Implement the mailer**

`backend/internal/mailer/resend.go`:

```go
package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResendMailer posts to Resend's /emails endpoint. It uses our own HTTP
// client rather than resend-go directly so that tests can run without the
// SDK and so we can plug in tracing / retries uniformly.
type ResendMailer struct {
	apiKey  string
	from    string
	baseURL string
	client  *http.Client
}

// Option configures ResendMailer.
type Option func(*ResendMailer)

// WithBaseURL overrides the Resend base URL (used in tests).
func WithBaseURL(u string) Option { return func(m *ResendMailer) { m.baseURL = u } }

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(c *http.Client) Option { return func(m *ResendMailer) { m.client = c } }

// NewResendMailer builds a ResendMailer. apiKey and from must be non-empty.
func NewResendMailer(apiKey, from string, opts ...Option) *ResendMailer {
	m := &ResendMailer{
		apiKey:  apiKey,
		from:    from,
		baseURL: "https://api.resend.com",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

// Send implements Mailer.
func (m *ResendMailer) Send(ctx context.Context, msg Message) error {
	body, err := json.Marshal(resendRequest{
		From:    m.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		HTML:    msg.HTML,
		Text:    msg.Text,
	})
	if err != nil {
		return fmt.Errorf("resend: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: build: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend: %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
```

(If plan 2's `mailer.go` already exports `Message`, reuse that type verbatim. If not, extend `mailer.go` with `type Message struct { To, Subject, HTML, Text, ReplyTo string }` as part of this task.)

- [ ] **Step 3: Verification**

```bash
go test ./internal/mailer/...
```

Expected: both tests pass.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/mailer/resend.go backend/internal/mailer/resend_test.go
git commit -m "$(cat <<'EOF'
feat(mailer): implement ResendMailer

POSTs rendered Messages to Resend's /emails endpoint with bearer auth
and configurable base URL. Tests exercise happy path and 4xx error
propagation against httptest.NewServer.
EOF
)"
```

---

## Task 5: `mailer.Template` and per-flow templates (TDD)

**Files:**
- Create: `backend/internal/mailer/template.go`
- Create: `backend/internal/mailer/template_test.go`
- Create: `backend/internal/mailer/templates/verify_email.html.tmpl`
- Create: `backend/internal/mailer/templates/verify_email.txt.tmpl`
- Create: `backend/internal/mailer/templates/password_reset.html.tmpl`
- Create: `backend/internal/mailer/templates/password_reset.txt.tmpl`
- Create: `backend/internal/mailer/templates/email_change_new.html.tmpl`
- Create: `backend/internal/mailer/templates/email_change_new.txt.tmpl`
- Create: `backend/internal/mailer/templates/email_change_old_notice.html.tmpl`
- Create: `backend/internal/mailer/templates/email_change_old_notice.txt.tmpl`
- Create: `backend/internal/mailer/templates/invite.html.tmpl`
- Create: `backend/internal/mailer/templates/invite.txt.tmpl`

- [ ] **Step 1: Write the failing test first**

`backend/internal/mailer/template_test.go`:

```go
package mailer

import (
	"strings"
	"testing"
)

func TestTemplates_Render_VerifyEmail(t *testing.T) {
	tmpl, err := LoadTemplate("verify_email")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := tmpl.Render(VerifyEmailData{
		DisplayName: "Alice",
		VerifyURL:   "https://folio.app/auth/verify/tok123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject == "" {
		t.Fatal("empty subject")
	}
	if !strings.Contains(msg.HTML, "tok123") {
		t.Fatalf("html missing token: %q", msg.HTML)
	}
	if !strings.Contains(msg.Text, "tok123") {
		t.Fatalf("text missing token: %q", msg.Text)
	}
	if !strings.Contains(msg.HTML, "Alice") {
		t.Fatalf("html missing name")
	}
}

func TestTemplates_Render_PasswordReset(t *testing.T) {
	tmpl, _ := LoadTemplate("password_reset")
	msg, err := tmpl.Render(PasswordResetData{
		DisplayName: "Bob",
		ResetURL:    "https://folio.app/reset/xyz",
		ExpiresIn:   "30 minutes",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTML, "xyz") || !strings.Contains(msg.Text, "30 minutes") {
		t.Fatalf("missing fields")
	}
}

func TestTemplates_Render_UnknownTemplate(t *testing.T) {
	if _, err := LoadTemplate("nope"); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Implement `template.go`**

```go
package mailer

import (
	"bytes"
	"fmt"
	htmltmpl "html/template"
	texttmpl "text/template"
)

// Template is a (subject, html, text) triple backed by html/template and
// text/template. Render takes the per-template data struct and returns a
// ready-to-send Message.
type Template struct {
	name    string
	subject func(data any) string
	html    *htmltmpl.Template
	text    *texttmpl.Template
}

// Render evaluates the template into a Message. To is left blank — the
// caller fills it (the recipient varies per flow).
func (t *Template) Render(data any) (Message, error) {
	var hbuf, tbuf bytes.Buffer
	if err := t.html.ExecuteTemplate(&hbuf, "layout", data); err != nil {
		return Message{}, fmt.Errorf("template %s html: %w", t.name, err)
	}
	if err := t.text.Execute(&tbuf, data); err != nil {
		return Message{}, fmt.Errorf("template %s text: %w", t.name, err)
	}
	return Message{
		Subject: t.subject(data),
		HTML:    hbuf.String(),
		Text:    tbuf.String(),
	}, nil
}

// Per-template data types. One struct per template; fields used in the
// template must appear here.

type VerifyEmailData struct {
	DisplayName string
	VerifyURL   string
}

type PasswordResetData struct {
	DisplayName string
	ResetURL    string
	ExpiresIn   string
}

type EmailChangeNewData struct {
	DisplayName   string
	ConfirmURL    string
	OldEmail      string
	NewEmail      string
}

type EmailChangeOldNoticeData struct {
	DisplayName string
	OldEmail    string
	NewEmail    string
}

type InviteData struct {
	InviterName string
	WorkspaceName  string
	Role        string
	AcceptURL   string
}

// LoadTemplate loads a named template from the embedded FS.
func LoadTemplate(name string) (*Template, error) {
	subjects := map[string]func(any) string{
		"verify_email":            func(_ any) string { return "Verify your Folio email" },
		"password_reset":          func(_ any) string { return "Reset your Folio password" },
		"email_change_new":        func(_ any) string { return "Confirm your new Folio email" },
		"email_change_old_notice": func(_ any) string { return "Your Folio email address was changed" },
		"invite": func(d any) string {
			if i, ok := d.(InviteData); ok {
				return fmt.Sprintf("You're invited to join %s on Folio", i.WorkspaceName)
			}
			return "You're invited on Folio"
		},
	}
	subj, ok := subjects[name]
	if !ok {
		return nil, fmt.Errorf("mailer: unknown template %q", name)
	}

	htmlFiles := []string{
		"templates/_layout.html.tmpl",
		"templates/" + name + ".html.tmpl",
	}
	htmlT, err := htmltmpl.ParseFS(templateFS, htmlFiles...)
	if err != nil {
		return nil, err
	}
	textT, err := texttmpl.ParseFS(templateFS, "templates/"+name+".txt.tmpl")
	if err != nil {
		return nil, err
	}
	return &Template{name: name, subject: subj, html: htmlT, text: textT}, nil
}
```

- [ ] **Step 3: Write the template files**

`verify_email.html.tmpl`:

```html
{{ define "content" -}}
<p>Hi {{ .DisplayName }},</p>
<p>Confirm your email so Folio can keep your account safe:</p>
<p><a href="{{ .VerifyURL }}"
   style="display:inline-block;padding:10px 18px;background:#111;color:#fff;
          text-decoration:none;border-radius:6px;">
  Verify email
</a></p>
<p>This link expires in 24 hours.</p>
<p style="font-size:12px;color:#888;">Or paste this URL into your browser: {{ .VerifyURL }}</p>
{{- end }}
{{ define "root" }}{{ template "layout" (mergeSubject . "Verify your Folio email") }}{{ end }}
{{ template "layout" . }}
```

(Simpler: drop the `define "root"` block — `Render` already executes `"layout"` directly. Keep the file as two `define` blocks for `content` and let the layout handle the rest.)

Pragmatic final form:

```html
{{ define "content" -}}
<p>Hi {{ .DisplayName }},</p>
<p>Confirm your email so Folio can keep your account safe:</p>
<p><a href="{{ .VerifyURL }}">Verify email</a></p>
<p>This link expires in 24 hours.</p>
{{- end }}
```

`verify_email.txt.tmpl`:

```
Hi {{ .DisplayName }},

Confirm your email so Folio can keep your account safe:
{{ .VerifyURL }}

This link expires in 24 hours.
```

`password_reset.html.tmpl`:

```html
{{ define "content" -}}
<p>Hi {{ .DisplayName }},</p>
<p>Click to reset your Folio password:</p>
<p><a href="{{ .ResetURL }}">Reset password</a></p>
<p>This link expires in {{ .ExpiresIn }}.</p>
<p>If you didn't ask for a reset, ignore this email.</p>
{{- end }}
```

`password_reset.txt.tmpl`:

```
Hi {{ .DisplayName }},

Reset your Folio password: {{ .ResetURL }}
This link expires in {{ .ExpiresIn }}.
```

`email_change_new.html.tmpl`:

```html
{{ define "content" -}}
<p>Hi {{ .DisplayName }},</p>
<p>Your Folio email is about to change from <strong>{{ .OldEmail }}</strong> to
<strong>{{ .NewEmail }}</strong>. Confirm to complete the change:</p>
<p><a href="{{ .ConfirmURL }}">Confirm new email</a></p>
<p>This link expires in 24 hours.</p>
{{- end }}
```

`email_change_new.txt.tmpl`:

```
Hi {{ .DisplayName }},

Your Folio email is about to change from {{ .OldEmail }} to {{ .NewEmail }}.
Confirm: {{ .ConfirmURL }}
Expires in 24 hours.
```

`email_change_old_notice.html.tmpl`:

```html
{{ define "content" -}}
<p>Hi {{ .DisplayName }},</p>
<p>The email on your Folio account was changed from
<strong>{{ .OldEmail }}</strong> to <strong>{{ .NewEmail }}</strong>.</p>
<p>If this wasn't you, reply to this email immediately.</p>
{{- end }}
```

`email_change_old_notice.txt.tmpl`:

```
Hi {{ .DisplayName }},

The email on your Folio account was changed from {{ .OldEmail }} to {{ .NewEmail }}.
If this wasn't you, reply immediately.
```

`invite.html.tmpl`:

```html
{{ define "content" -}}
<p>{{ .InviterName }} invited you to join <strong>{{ .WorkspaceName }}</strong> on
Folio as <strong>{{ .Role }}</strong>.</p>
<p><a href="{{ .AcceptURL }}">Accept invitation</a></p>
<p>The invite expires in 7 days.</p>
{{- end }}
```

`invite.txt.tmpl`:

```
{{ .InviterName }} invited you to join {{ .WorkspaceName }} on Folio as {{ .Role }}.
Accept: {{ .AcceptURL }}
Expires in 7 days.
```

- [ ] **Step 4: Verification**

```bash
go test ./internal/mailer/...
```

Expected: all three template tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/mailer/template.go backend/internal/mailer/template_test.go backend/internal/mailer/templates/
git commit -m "$(cat <<'EOF'
feat(mailer): add Template type and flow templates

One Template per flow (verify_email, password_reset, email_change_new,
email_change_old_notice, invite). Each flow has an HTML and text variant;
all HTML variants share the _layout.html.tmpl frame. Render returns a
ready-to-send Message with Subject/HTML/Text populated.
EOF
)"
```

---

## Task 6: `SendEmailWorker` (TDD)

**Files:**
- Create: `backend/internal/jobs/send_email_worker.go`
- Create: `backend/internal/jobs/send_email_worker_test.go`

- [ ] **Step 1: Write the failing test first**

`backend/internal/jobs/send_email_worker_test.go`:

```go
package jobs

import (
	"context"
	"testing"

	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/mailer"
)

func TestSendEmailWorker_RendersAndSends(t *testing.T) {
	rec := &mailer.LogMailer{}
	w := NewSendEmailWorker(rec)
	err := w.Work(context.Background(), &river.Job[SendEmailArgs]{
		Args: SendEmailArgs{
			TemplateName: "verify_email",
			ToAddress:    "alice@example.com",
			Data: map[string]any{
				"DisplayName": "Alice",
				"VerifyURL":   "https://folio.app/auth/verify/t1",
			},
		},
	})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(rec.Sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(rec.Sent))
	}
	m := rec.Sent[0]
	if m.To != "alice@example.com" || m.Subject == "" || m.HTML == "" || m.Text == "" {
		t.Fatalf("bad message: %+v", m)
	}
}

func TestSendEmailWorker_UnknownTemplateErrors(t *testing.T) {
	w := NewSendEmailWorker(&mailer.LogMailer{})
	err := w.Work(context.Background(), &river.Job[SendEmailArgs]{
		Args: SendEmailArgs{TemplateName: "nope", ToAddress: "x@x.com"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Implement the worker**

`backend/internal/jobs/send_email_worker.go`:

```go
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/mailer"
)

// SendEmailWorker renders a named template and delegates delivery to the
// configured Mailer. Errors bubble up so River can retry with its default
// exponential backoff.
type SendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	mailer mailer.Mailer
}

// NewSendEmailWorker wires a Mailer into the worker.
func NewSendEmailWorker(m mailer.Mailer) *SendEmailWorker {
	return &SendEmailWorker{mailer: m}
}

// Work implements river.Worker.
func (w *SendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
	tmpl, err := mailer.LoadTemplate(job.Args.TemplateName)
	if err != nil {
		return fmt.Errorf("send_email: %w", err)
	}
	data, err := decodeData(job.Args.TemplateName, job.Args.Data)
	if err != nil {
		return err
	}
	msg, err := tmpl.Render(data)
	if err != nil {
		return err
	}
	msg.To = job.Args.ToAddress
	return w.mailer.Send(ctx, msg)
}

// decodeData maps the JSON-marshaled job payload back into the typed data
// struct expected by the template.
func decodeData(name string, raw map[string]any) (any, error) {
	bs, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	switch name {
	case "verify_email":
		var d mailer.VerifyEmailData
		return d, json.Unmarshal(bs, &d)
	case "password_reset":
		var d mailer.PasswordResetData
		return d, json.Unmarshal(bs, &d)
	case "email_change_new":
		var d mailer.EmailChangeNewData
		return d, json.Unmarshal(bs, &d)
	case "email_change_old_notice":
		var d mailer.EmailChangeOldNoticeData
		return d, json.Unmarshal(bs, &d)
	case "invite":
		var d mailer.InviteData
		return d, json.Unmarshal(bs, &d)
	default:
		return nil, fmt.Errorf("send_email: unknown template data %q", name)
	}
}
```

(Return-path on decodeData needs a nested pattern that returns the decoded struct even on error — adjust to `if err := json.Unmarshal(bs, &d); err != nil { return nil, err }; return d, nil` per branch during implementation.)

- [ ] **Step 3: Verification**

```bash
go test ./internal/jobs/...
```

Expected: both tests pass.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/jobs/send_email_worker.go backend/internal/jobs/send_email_worker_test.go
git commit -m "$(cat <<'EOF'
feat(jobs): add SendEmailWorker

Renders the named template with the JSON-decoded data payload and
delegates delivery to the injected mailer.Mailer. Unknown templates
error so River can mark the job as failed after retries exhaust.
EOF
)"
```

---

## Task 7: `SoftDeletedWorkspaceSweeper` worker and periodic registration

**Files:**
- Create: `backend/internal/jobs/sweep_soft_deleted_workspaces_worker.go`
- Create: `backend/internal/jobs/sweep_soft_deleted_workspaces_worker_test.go`
- Create: `backend/internal/jobs/periodic.go`

- [ ] **Step 1: Write the failing test**

```go
// sweep_soft_deleted_workspaces_worker_test.go
package jobs

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
)

type fakeSweeper struct{ called int }

func (f *fakeSweeper) Sweep(ctx context.Context) (int, error) {
	f.called++
	return 3, nil
}

func TestSweepSoftDeletedWorkspacesWorker_CallsSweeper(t *testing.T) {
	fs := &fakeSweeper{}
	w := NewSweepSoftDeletedWorkspacesWorker(fs)
	if err := w.Work(context.Background(), &river.Job[SweepSoftDeletedWorkspacesArgs]{}); err != nil {
		t.Fatal(err)
	}
	if fs.called != 1 {
		t.Fatalf("sweeper called %d times; want 1", fs.called)
	}
}
```

- [ ] **Step 2: Implement the worker**

`sweep_soft_deleted_workspaces_worker.go`:

```go
package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
)

// WorkspaceSweeper is the minimum surface needed from plan 2's sweeper; that
// plan defines a concrete type whose Sweep matches this signature. This
// interface lets tests inject a fake without dragging in the Postgres pool.
type WorkspaceSweeper interface {
	// Sweep hard-deletes soft-deleted workspaces past the grace window and
	// returns the number of workspaces removed.
	Sweep(ctx context.Context) (int, error)
}

// SweepSoftDeletedWorkspacesWorker runs the sweeper periodically.
type SweepSoftDeletedWorkspacesWorker struct {
	river.WorkerDefaults[SweepSoftDeletedWorkspacesArgs]
	sweeper WorkspaceSweeper
	logger  *slog.Logger
}

// NewSweepSoftDeletedWorkspacesWorker wires a WorkspaceSweeper into the worker.
func NewSweepSoftDeletedWorkspacesWorker(s WorkspaceSweeper) *SweepSoftDeletedWorkspacesWorker {
	return &SweepSoftDeletedWorkspacesWorker{sweeper: s, logger: slog.Default()}
}

// Work implements river.Worker.
func (w *SweepSoftDeletedWorkspacesWorker) Work(ctx context.Context, _ *river.Job[SweepSoftDeletedWorkspacesArgs]) error {
	n, err := w.sweeper.Sweep(ctx)
	if err != nil {
		return err
	}
	w.logger.Info("workspace sweeper", "hard_deleted", n)
	return nil
}
```

- [ ] **Step 3: Register the periodic schedule**

`periodic.go`:

```go
package jobs

import (
	"time"

	"github.com/riverqueue/river"
)

// PeriodicJobs returns the periodic job bundle for the River client. Call
// at wiring time and hand to river.Config.PeriodicJobs.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return SweepSoftDeletedWorkspacesArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}
}
```

Update `client.go`'s `NewClient` to accept and forward `PeriodicJobs`:

```go
type Config struct {
	Queues       map[string]river.QueueConfig
	PeriodicJobs []*river.PeriodicJob
}
// ...
rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
    Queues:       cfg.Queues,
    Workers:      workers,
    PeriodicJobs: cfg.PeriodicJobs,
})
```

- [ ] **Step 4: Verification**

```bash
go test ./internal/jobs/...
```

Expected: new test passes alongside existing ones.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/jobs/sweep_soft_deleted_workspaces_worker.go \
        backend/internal/jobs/sweep_soft_deleted_workspaces_worker_test.go \
        backend/internal/jobs/periodic.go \
        backend/internal/jobs/client.go
git commit -m "$(cat <<'EOF'
feat(jobs): add SoftDeletedWorkspaceSweeper worker and periodic schedule

Wraps plan 2's sweeper behind a WorkspaceSweeper interface so tests can
inject fakes. The periodic registration runs Sweep once per day; plan 2's
folio-sweeper binary stays as a CLI backstop but is no longer required in
production.
EOF
)"
```

---

## Task 8: Prod wiring — factory, router, graceful shutdown

**Files:**
- Modify: `backend/internal/http/router.go` — stop mounting `httpx.RequireWorkspace`; accept `jobs.Client` + `mailer.Mailer` via `Deps`.
- Modify: `backend/cmd/folio-server/main.go` (or whichever `main.go` builds the server — inspect first) — build the `mailer.Mailer` (ResendMailer when `RESEND_API_KEY` set, else `LogMailer`), register workers, start the River client, pass into `Deps`.
- Modify: `docker-compose.dev.yml` — append `RESEND_API_KEY`, `EMAIL_FROM`, `APP_URL` to the backend service env.
- Modify: `.env.example` — add the three env vars.

- [ ] **Step 1: Define the factory**

Add `backend/internal/mailer/factory.go`:

```go
package mailer

import "log/slog"

// New returns a Mailer chosen by the presence of apiKey. Empty apiKey
// returns the LogMailer so local dev and tests don't need Resend.
func New(apiKey, from string, logger *slog.Logger) Mailer {
	if apiKey == "" {
		logger.Info("mailer: using LogMailer (RESEND_API_KEY empty)")
		return &LogMailer{Logger: logger}
	}
	return NewResendMailer(apiKey, from)
}
```

(Ensure `LogMailer` has a `Logger *slog.Logger` field — adjust plan 2 if needed; alternatively, have `LogMailer` use `slog.Default()` unconditionally.)

- [ ] **Step 2: Wire River at server startup**

In the server's `main.go`:

```go
// Build mailer
mlr := mailer.New(os.Getenv("RESEND_API_KEY"), os.Getenv("EMAIL_FROM"), logger)

// Register workers
workers := river.NewWorkers()
river.AddWorker(workers, jobs.NewSendEmailWorker(mlr))
river.AddWorker(workers, jobs.NewSweepSoftDeletedWorkspacesWorker(workspaceSweeper))

// Build & start River client
jobClient, err := jobs.NewClient(pool, workers, jobs.Config{
    Queues: map[string]river.QueueConfig{
        river.QueueDefault: {MaxWorkers: 10},
    },
    PeriodicJobs: jobs.PeriodicJobs(),
})
if err != nil { log.Fatal(err) }
if err := jobClient.Start(ctx); err != nil { log.Fatal(err) }
defer func() {
    sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    _ = jobClient.Stop(sctx)
}()

// Pass into HTTP Deps
h := http.NewRouter(http.Deps{
    Logger: logger,
    DB:     pool,
    Cfg:    cfg,
    Jobs:   jobClient,
    Mailer: mlr,
})
```

- [ ] **Step 3: Update `router.go`**

Add `Jobs *jobs.Client` and `Mailer mailer.Mailer` to `Deps`. Construct `auth.Service` (plan 1) with `jobClient` dependency injection so `SendEmailVerification` etc. can enqueue jobs. Drop the `r.Use(httpx.RequireWorkspace)` block — plan 1 already replaced it with `auth.RequireSession` + `auth.RequireMembership` on `/t/{workspaceId}`.

- [ ] **Step 4: Update compose + env example**

`docker-compose.dev.yml` backend service `environment:`:

```yaml
      RESEND_API_KEY: ${RESEND_API_KEY:-}
      EMAIL_FROM: ${EMAIL_FROM:-Folio <no-reply@localhost>}
      APP_URL: ${APP_URL:-http://localhost:3000}
```

`.env.example`:

```
# Transactional email (leave empty for LogMailer-only dev)
RESEND_API_KEY=
EMAIL_FROM=Folio <no-reply@localhost>
APP_URL=http://localhost:3000
```

- [ ] **Step 5: Verification**

```bash
go build ./...
go test ./...
docker compose -f docker-compose.dev.yml config | grep -E 'RESEND_API_KEY|EMAIL_FROM|APP_URL'
```

Expected: all three env vars appear in the rendered config.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/mailer/factory.go backend/internal/http/router.go \
        backend/cmd/ docker-compose.dev.yml .env.example
git commit -m "$(cat <<'EOF'
feat(mailer): wire Resend + River into the server runtime

mailer.New returns LogMailer when RESEND_API_KEY is empty so local dev
and tests don't need a real Resend account. Server startup registers
workers, starts the River client, and hands a Mailer + Client into the
router. Replaces plan 2's interim LogMailer-only wiring.
EOF
)"
```

---

## Task 9: `auth.Service.SendEmailVerification` + `VerifyEmail` (TDD)

**Files:**
- Create: `backend/internal/auth/email_flows.go` (start of file; later tasks append)
- Create: `backend/internal/auth/email_flows_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// email_flows_test.go — excerpt for Task 9
func TestSendEmailVerification_WritesTokenAndEnqueuesJob(t *testing.T) {
    // Arrange: fresh DB, create user (unverified).
    // Act: call svc.SendEmailVerification(ctx, userID)
    // Assert:
    //   - one auth_tokens row with purpose='email_verify', consumed_at NULL,
    //     expires_at within [now+23h, now+25h], email = user's email,
    //     token_hash non-nil.
    //   - one River job of kind "send_email" inserted (query river_job table
    //     or use rivertest.RequireInserted).
}

func TestVerifyEmail_ConsumesTokenAndSetsEmailVerifiedAt(t *testing.T) {
    // Arrange: create user, call SendEmailVerification, capture plaintext
    // token (use a fake token-generator swap-in).
    // Act: call svc.VerifyEmail(ctx, plaintext)
    // Assert:
    //   - auth_tokens.consumed_at IS NOT NULL.
    //   - users.email_verified_at IS NOT NULL.
    //   - audit_events has one row with action='user.email_verified'.
}

func TestVerifyEmail_RejectsExpiredToken(t *testing.T) {
    // Seed an auth_tokens row with expires_at = now() - 1 hour.
    // Act: VerifyEmail returns a specific "token_expired" error;
    // users.email_verified_at stays NULL.
}

func TestVerifyEmail_RejectsConsumedToken(t *testing.T) {
    // Seed a row with consumed_at = now().
    // Expect: "token_consumed" error.
}
```

- [ ] **Step 2: Implement the flows**

```go
// email_flows.go — Task 9 portion
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Token purposes in auth_tokens.
const (
	purposeEmailVerify  = "email_verify"
	purposePasswordReset = "password_reset"
	purposeEmailChange  = "email_change"
)

// Token lifetimes.
const (
	verifyEmailTTL    = 24 * time.Hour
	passwordResetTTL  = 30 * time.Minute
	emailChangeTTL    = 24 * time.Hour
)

// SendEmailVerification issues a fresh token row and enqueues the email.
// Idempotent from the caller's perspective: repeated calls simply mint more
// tokens (rate-limited at the HTTP layer).
func (s *Service) SendEmailVerification(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var email, displayName string
	if err := tx.QueryRow(ctx, `
		select email, display_name from users where id = $1
	`, userID).Scan(&email, &displayName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}

	plaintext, hash := GenerateToken()
	tokenID := uuidx.New()
	expiresAt := s.now().Add(verifyEmailTTL)

	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposeEmailVerify, hash, email, expiresAt); err != nil {
		return fmt.Errorf("insert auth_tokens: %w", err)
	}

	if _, err := s.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "verify_email",
		ToAddress:      email,
		IdempotencyKey: fmt.Sprintf("verify_email:%s", tokenID),
		Data: map[string]any{
			"DisplayName": displayName,
			"VerifyURL":   s.appURL + "/auth/verify/" + plaintext,
		},
	}, nil); err != nil {
		return fmt.Errorf("enqueue send_email: %w", err)
	}

	return tx.Commit(ctx)
}

// VerifyEmail consumes a pending verify token and flips email_verified_at.
func (s *Service) VerifyEmail(ctx context.Context, plaintext string) error {
	hash := HashToken(plaintext)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	var userID uuid.UUID
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		select id, user_id, expires_at, consumed_at
		from auth_tokens
		where token_hash = $1 and purpose = $2
	`, hash, purposeEmailVerify).Scan(&id, &userID, &expiresAt, &consumedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		}
		return err
	}
	if consumedAt != nil {
		return ErrTokenConsumed
	}
	if s.now().After(expiresAt) {
		return ErrTokenExpired
	}
	if _, err := tx.Exec(ctx, `update auth_tokens set consumed_at = now() where id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update users set email_verified_at = now() where id = $1`, userID); err != nil {
		return err
	}
	if err := writeAuditEvent(ctx, tx, userID, nil, "user.email_verified", "user", userID, nil, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

Error sentinels (declared in `auth/errors.go` alongside plan 1's set):

```go
var (
	ErrTokenInvalid  = errors.New("auth: token invalid")
	ErrTokenExpired  = errors.New("auth: token expired")
	ErrTokenConsumed = errors.New("auth: token already consumed")
	ErrUserNotFound  = errors.New("auth: user not found")
	ErrEmailTaken    = errors.New("auth: email already in use")
)
```

`writeAuditEvent` is a helper in `backend/internal/audit` from the domain-v2 plan; if not present at call time, add a minimal version in `auth/audit.go` that inserts into `audit_events`.

- [ ] **Step 3: Verification**

```bash
go test ./internal/auth/...
```

Expected: all four tests pass. Use `rivertest.SubscribeJobs` or raw `select from river_job` to verify enqueue.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/email_flows.go backend/internal/auth/email_flows_test.go \
        backend/internal/auth/errors.go
git commit -m "$(cat <<'EOF'
feat(auth): add SendEmailVerification / VerifyEmail

Issues a 24h auth_tokens row, enqueues a SendEmail River job inside the
same tx (rolls back cleanly on failure), and on verify flips
users.email_verified_at + writes a user.email_verified audit event.
EOF
)"
```

---

## Task 10: `ResendEmailVerification` + rate-limit wiring (TDD)

**Files:**
- Modify: `backend/internal/auth/email_flows.go`
- Modify: `backend/internal/auth/email_flows_test.go`
- Create: `backend/internal/auth/ratelimit_email_flows.go`

- [ ] **Step 1: Add the failing tests**

```go
func TestResendEmailVerification_NoOpIfAlreadyVerified(t *testing.T) {
    // seed verified user; call Resend; expect nil error, zero auth_tokens
    // rows, zero River jobs.
}

func TestResendEmailVerification_RateLimited(t *testing.T) {
    // call Resend 2 times in rapid succession; expect second call returns
    // ErrRateLimited, only one auth_tokens row, only one River job.
}

func TestResendEmailVerification_AllowsAfterCooldown(t *testing.T) {
    // call once, advance s.now() by 61 seconds, call again; expect both
    // succeed.
}
```

- [ ] **Step 2: Implement**

```go
// ratelimit_email_flows.go
package auth

// ResendLimiter provides the two bucket checks used by
// ResendEmailVerification: 1/min and 5/hr per user.
type ResendLimiter interface {
	Allow(key string, rate int, per time.Duration) bool
}

// email_flows.go additions
func (s *Service) ResendEmailVerification(ctx context.Context, userID uuid.UUID) error {
	var verifiedAt *time.Time
	if err := s.pool.QueryRow(ctx, `select email_verified_at from users where id = $1`, userID).Scan(&verifiedAt); err != nil {
		return err
	}
	if verifiedAt != nil {
		return nil // no-op
	}

	key := fmt.Sprintf("verify-resend:user:%s", userID)
	if !s.limiter.Allow(key, 1, time.Minute) {
		return ErrRateLimited
	}
	if !s.limiter.Allow(key, 5, time.Hour) {
		return ErrRateLimited
	}
	return s.SendEmailVerification(ctx, userID)
}
```

Plan 1 already declares `ErrRateLimited`; if not, add to `errors.go`.

- [ ] **Step 3: Verification**

```bash
go test ./internal/auth/...
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/
git commit -m "$(cat <<'EOF'
feat(auth): add rate-limited ResendEmailVerification

Enforces per-spec §8.2 limits (1/min and 5/hr per user) before
delegating to SendEmailVerification. No-ops for already-verified users.
EOF
)"
```

---

## Task 11: `RequestPasswordReset` + `ResetPassword` (TDD)

**Files:**
- Modify: `backend/internal/auth/email_flows.go`
- Modify: `backend/internal/auth/email_flows_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestRequestPasswordReset_AlwaysReturnsNil(t *testing.T) {
    // Non-existent email: Request returns nil, zero auth_tokens, zero jobs.
    // Existent email: Request returns nil, one auth_tokens row
    //   (purpose='password_reset', expires_at ~ now+30min), one River job.
}

func TestRequestPasswordReset_RateLimitedPerIPAndEmail(t *testing.T) {
    // 4th call within same hour on same IP → returns nil but emits no token.
    // Same for 4th call on same email from different IPs.
}

func TestResetPassword_UpdatesHashAndRevokesOtherSessions(t *testing.T) {
    // Seed user with 3 sessions; issue reset token. Call ResetPassword with
    // the token and new password.
    // Assert:
    //   - users.password_hash changed.
    //   - All sessions for the user deleted EXCEPT none (reset path has no
    //     "current session" concept — revoke all).
    //   - auth_tokens.consumed_at not null for the used token.
    //   - audit_events: user.password_reset_completed.
}

func TestResetPassword_RejectsShortPassword(t *testing.T) {
    // password < 12 chars → ErrPasswordTooShort; no mutation.
}

func TestResetPassword_RejectsExpiredToken(t *testing.T) {
    // expires_at < now; ErrTokenExpired; no mutation.
}
```

- [ ] **Step 2: Implement**

```go
func (s *Service) RequestPasswordReset(ctx context.Context, emailRaw, ip string) error {
	email := strings.ToLower(strings.TrimSpace(emailRaw))

	if !s.limiter.Allow("reset-request:ip:"+ip, 3, time.Hour) {
		return nil
	}
	if !s.limiter.Allow("reset-request:email:"+email, 3, time.Hour) {
		return nil
	}

	var userID uuid.UUID
	var displayName string
	err := s.pool.QueryRow(ctx, `select id, display_name from users where email = $1`, email).Scan(&userID, &displayName)
	if err != nil {
		// Deliberately silent on missing user — spec §4.2 "always 200".
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	plaintext, hash := GenerateToken()
	tokenID := uuidx.New()
	expiresAt := s.now().Add(passwordResetTTL)
	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposePasswordReset, hash, email, expiresAt); err != nil {
		return err
	}
	if _, err := s.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "password_reset",
		ToAddress:      email,
		IdempotencyKey: fmt.Sprintf("password_reset:%s", tokenID),
		Data: map[string]any{
			"DisplayName": displayName,
			"ResetURL":    s.appURL + "/reset/" + plaintext,
			"ExpiresIn":   "30 minutes",
		},
	}, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPassword(ctx context.Context, plaintext, newPassword string) error {
	if len(newPassword) < 12 {
		return ErrPasswordTooShort
	}
	hash := HashToken(plaintext)

	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	var tokenID, userID uuid.UUID
	var expiresAt time.Time
	var consumedAt *time.Time
	if err := tx.QueryRow(ctx, `
		select id, user_id, expires_at, consumed_at
		from auth_tokens where token_hash = $1 and purpose = $2
	`, hash, purposePasswordReset).Scan(&tokenID, &userID, &expiresAt, &consumedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) { return ErrTokenInvalid }
		return err
	}
	if consumedAt != nil { return ErrTokenConsumed }
	if s.now().After(expiresAt) { return ErrTokenExpired }

	pwHash, err := HashPassword(newPassword) // from plan 1
	if err != nil { return err }

	if _, err := tx.Exec(ctx, `update users set password_hash = $1 where id = $2`, pwHash, userID); err != nil { return err }
	if _, err := tx.Exec(ctx, `update auth_tokens set consumed_at = now() where id = $1`, tokenID); err != nil { return err }
	if _, err := tx.Exec(ctx, `delete from sessions where user_id = $1`, userID); err != nil { return err }
	if err := writeAuditEvent(ctx, tx, userID, nil, "user.password_reset_completed", "user", userID, nil, nil); err != nil { return err }

	return tx.Commit(ctx)
}
```

- [ ] **Step 3: Verification**

```bash
go test ./internal/auth/...
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/email_flows.go backend/internal/auth/email_flows_test.go
git commit -m "$(cat <<'EOF'
feat(auth): add RequestPasswordReset + ResetPassword

Reset-request is always 200 (no existence leak), rate-limited 3/hr per IP
and per email. Confirm revokes every session for the user (hard cutover),
consumes the token, and writes a user.password_reset_completed audit.
EOF
)"
```

---

## Task 12: `RequestEmailChange` + `ConfirmEmailChange` (TDD)

**Files:**
- Modify: `backend/internal/auth/email_flows.go`
- Modify: `backend/internal/auth/email_flows_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestRequestEmailChange_SendsVerifyToNewAddress(t *testing.T) {
    // Seed user. Call RequestEmailChange(userID, "new@x.com").
    // Assert: auth_tokens row has email='new@x.com', purpose='email_change',
    // TTL 24h. One River job with template='email_change_new',
    // ToAddress='new@x.com'.
    // users.email unchanged until confirm.
}

func TestRequestEmailChange_RejectsExistingEmail(t *testing.T) {
    // new email already belongs to another user → ErrEmailTaken.
}

func TestRequestEmailChange_RateLimited(t *testing.T) {
    // 4th call within 1h per user → ErrRateLimited.
}

func TestConfirmEmailChange_UpdatesEmailAndNotifiesOld(t *testing.T) {
    // Run Request then Confirm. Assert:
    //   - users.email = new address, email_verified_at = now().
    //   - auth_tokens.consumed_at NOT NULL.
    //   - Two River jobs total (one from Request, one enqueued by Confirm
    //     for the old-email notice with template='email_change_old_notice').
    //   - audit_events: user.email_change_requested, user.email_change_confirmed.
}
```

- [ ] **Step 2: Implement**

```go
func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmailRaw string) error {
	newEmail := strings.ToLower(strings.TrimSpace(newEmailRaw))
	if !strings.Contains(newEmail, "@") {
		return ErrEmailInvalid
	}
	if !s.limiter.Allow("email-change:user:"+userID.String(), 3, time.Hour) {
		return ErrRateLimited
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	var currentEmail, displayName string
	if err := tx.QueryRow(ctx, `select email, display_name from users where id = $1`, userID).
		Scan(&currentEmail, &displayName); err != nil {
		return err
	}
	if currentEmail == newEmail { return ErrEmailSame }

	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from users where email = $1)`, newEmail).Scan(&exists); err != nil {
		return err
	}
	if exists { return ErrEmailTaken }

	plaintext, hash := GenerateToken()
	tokenID := uuidx.New()
	expiresAt := s.now().Add(emailChangeTTL)
	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposeEmailChange, hash, newEmail, expiresAt); err != nil {
		return err
	}

	if _, err := s.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "email_change_new",
		ToAddress:      newEmail,
		IdempotencyKey: fmt.Sprintf("email_change_new:%s", tokenID),
		Data: map[string]any{
			"DisplayName": displayName,
			"ConfirmURL":  s.appURL + "/auth/email/confirm/" + plaintext,
			"OldEmail":    currentEmail,
			"NewEmail":    newEmail,
		},
	}, nil); err != nil {
		return err
	}
	if err := writeAuditEvent(ctx, tx, userID, nil, "user.email_change_requested", "user", userID, nil,
		map[string]any{"new_email": newEmail}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Service) ConfirmEmailChange(ctx context.Context, plaintext string) error {
	hash := HashToken(plaintext)
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	var tokenID, userID uuid.UUID
	var newEmail string
	var expiresAt time.Time
	var consumedAt *time.Time
	if err := tx.QueryRow(ctx, `
		select id, user_id, email, expires_at, consumed_at
		from auth_tokens where token_hash = $1 and purpose = $2
	`, hash, purposeEmailChange).Scan(&tokenID, &userID, &newEmail, &expiresAt, &consumedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) { return ErrTokenInvalid }
		return err
	}
	if consumedAt != nil { return ErrTokenConsumed }
	if s.now().After(expiresAt) { return ErrTokenExpired }

	var oldEmail, displayName string
	if err := tx.QueryRow(ctx, `select email, display_name from users where id = $1`, userID).
		Scan(&oldEmail, &displayName); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		update users set email = $1, email_verified_at = now()
		where id = $2
	`, newEmail, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update auth_tokens set consumed_at = now() where id = $1`, tokenID); err != nil { return err }
	if _, err := tx.Exec(ctx, `delete from sessions where user_id = $1`, userID); err != nil { return err }

	// Notify old address.
	if _, err := s.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName: "email_change_old_notice",
		ToAddress:    oldEmail,
		Data: map[string]any{
			"DisplayName": displayName,
			"OldEmail":    oldEmail,
			"NewEmail":    newEmail,
		},
	}, nil); err != nil {
		return err
	}
	if err := writeAuditEvent(ctx, tx, userID, nil, "user.email_change_confirmed", "user", userID,
		map[string]any{"email": oldEmail},
		map[string]any{"email": newEmail},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

New sentinels in `errors.go`: `ErrEmailInvalid`, `ErrEmailSame`.

- [ ] **Step 3: Verification**

```bash
go test ./internal/auth/...
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/email_flows.go backend/internal/auth/email_flows_test.go backend/internal/auth/errors.go
git commit -m "$(cat <<'EOF'
feat(auth): add RequestEmailChange + ConfirmEmailChange

Verify link lands on the new address; on confirm, users.email flips,
email_verified_at is stamped, all sessions are revoked, and the old
address is notified via an email_change_old_notice email.
EOF
)"
```

---

## Task 13: `auth.RequireEmailVerified` middleware (TDD)

**Files:**
- Create: `backend/internal/auth/require_email_verified.go`
- Create: `backend/internal/auth/require_email_verified_test.go`

- [ ] **Step 1: Failing test**

```go
func TestRequireEmailVerified_AllowsVerifiedUser(t *testing.T) {
    // Request carries session for a verified user → next handler runs,
    // status 200.
}

func TestRequireEmailVerified_Rejects403ForUnverified(t *testing.T) {
    // Request carries session for an unverified user → status 403,
    // body.code == "email_not_verified".
}

func TestRequireEmailVerified_RequiresSessionContext(t *testing.T) {
    // No user in ctx → 500? no — this middleware is always composed AFTER
    // RequireSession, so missing ctx user is a programmer error. Test
    // panics or returns 500 and document the invariant.
}
```

- [ ] **Step 2: Implement**

```go
package auth

import (
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RequireEmailVerified refuses requests whose authenticated user has not yet
// confirmed their email. Compose AFTER RequireSession; plan 1's
// auth.UserFrom(ctx) returns the authenticated user.
func (s *Service) RequireEmailVerified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "auth_required", "session required")
			return
		}
		if u.EmailVerifiedAt == nil {
			httpx.WriteError(w, http.StatusForbidden, "email_not_verified",
				"please verify your email before continuing")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 3: Verification**

```bash
go test ./internal/auth/...
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/require_email_verified.go backend/internal/auth/require_email_verified_test.go
git commit -m "$(cat <<'EOF'
feat(auth): add RequireEmailVerified middleware

Returns 403 email_not_verified for authenticated users with NULL
email_verified_at. Plan 2's invite-accept handler and any future
bank-link / data-export handlers compose this middleware.
EOF
)"
```

---

## Task 14: Apply `RequireEmailVerified` to invite-accept + stubs

**Files:**
- Modify: `backend/internal/identity/invite_handler.go` (or whichever file plan 2 introduces for invite-accept) — prepend `s.auth.RequireEmailVerified` in the middleware chain.
- Modify: `backend/internal/http/router.go` — document the application sites in a code comment; leave bank-link and data-export as TODO stubs.

- [ ] **Step 1: Locate plan 2's invite-accept route**

```bash
cd /Users/xmedavid/dev/folio
rg -n 'accept-invite|InviteHandler|AcceptInvite' backend/
```

Identify the route registration. It should live in plan 2's `identity.InviteHandler` or `auth.Handler`; the `POST /api/v1/auth/invites/{token}/accept` entry is the target.

- [ ] **Step 2: Compose the middleware**

In the router, wrap the accept handler:

```go
r.With(authSvc.RequireSession, authSvc.RequireEmailVerified).
    Post("/api/v1/auth/invites/{token}/accept", inviteH.Accept)
```

For bank-link and data-export — which land in later plans — add a comment block in `router.go` recording the dependency:

```go
// Plans 6+: bank-account linking (identity.BankLinkHandler.Create) and
// data export (reports.ExportHandler.Start) MUST compose
// authSvc.RequireEmailVerified per spec §7. This middleware is already
// available at authSvc.RequireEmailVerified.
```

- [ ] **Step 3: Update plan 2's create-invite handler to verify too**

Per spec §7: "Creating an invite" also requires verified email. Wrap:

```go
r.With(authSvc.RequireSession, authSvc.RequireMembership, authSvc.RequireEmailVerified).
    Post("/api/v1/t/{workspaceId}/invites", inviteH.Create)
```

- [ ] **Step 4: Verification**

Unit test new middleware composition with an unverified session:

```go
func TestInviteAccept_Rejects_UnverifiedUser(t *testing.T) {
    // seed unverified user + valid invite
    // POST /api/v1/auth/invites/<token>/accept → 403 email_not_verified
    // assert no workspace_membership created
}
```

```bash
go build ./...
go test ./internal/...
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/http/router.go backend/internal/identity/ backend/internal/auth/
git commit -m "$(cat <<'EOF'
feat(auth): gate invite accept/create on verified email

Composes auth.RequireEmailVerified into plan 2's invite-accept and
invite-create routes per spec §7. Documents the same requirement for
bank-link and data-export handlers landing in later plans.
EOF
)"
```

---

## Task 15: HTTP handlers for the 6 email-driven endpoints (TDD)

**Files:**
- Create: `backend/internal/auth/handlers_email_flows.go`
- Create: `backend/internal/auth/handlers_email_flows_test.go`
- Modify: `backend/internal/http/router.go` — mount the new routes.
- Modify: `openapi/openapi.yaml` — document the new endpoints.

- [ ] **Step 1: Failing tests**

One table-driven handler test per endpoint: valid body → expected side effects; invalid/missing body → 400; rate-limited → still 200 for reset-request (spec says always 200), 429 for verify-resend and email-change-request.

```go
func TestHandler_VerifyEmail_Success(t *testing.T) {
    // POST /api/v1/auth/verify  body: {"token":"<plain>"}
    // → 204 (or 200 with empty body). users.email_verified_at set.
}
func TestHandler_VerifyEmail_InvalidToken(t *testing.T) {
    // → 400 token_invalid.
}
func TestHandler_ResendVerification_AuthedOnly(t *testing.T) {
    // No session → 401.
    // With session → 204.
}
func TestHandler_ResendVerification_RateLimited(t *testing.T) {
    // Second call within 60s → 429 rate_limited.
}
func TestHandler_RequestPasswordReset_AlwaysOK(t *testing.T) {
    // Unknown email → 200.
    // Known email → 200 + one job enqueued.
}
func TestHandler_RequestPasswordReset_BadBody(t *testing.T) {
    // → 400.
}
func TestHandler_ConfirmPasswordReset_Success(t *testing.T) {
    // body {"token": "...", "newPassword": "..."} → 204.
}
func TestHandler_RequestEmailChange_Authed(t *testing.T) {
    // No session → 401; with session → 204.
}
func TestHandler_ConfirmEmailChange_Success(t *testing.T) {
    // body {"token": "..."} → 204. users.email flipped.
}
```

- [ ] **Step 2: Implement handlers**

```go
package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var body struct{ Token string `json:"token"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "token required")
		return
	}
	switch err := h.svc.VerifyEmail(r.Context(), body.Token); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrTokenConsumed):
		httpx.WriteError(w, http.StatusBadRequest, "token_invalid", "invalid or used token")
	case errors.Is(err, ErrTokenExpired):
		httpx.WriteError(w, http.StatusBadRequest, "token_expired", "token expired")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFrom(r.Context())
	if !ok { httpx.WriteError(w, http.StatusUnauthorized, "auth_required", "session required"); return }
	switch err := h.svc.ResendEmailVerification(r.Context(), u.ID); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrRateLimited):
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "try again later")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (h *Handler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email string `json:"email"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "email required")
		return
	}
	ip := httpx.ClientIP(r)
	// Always 200, even on unknown email or rate limit.
	_ = h.svc.RequestPasswordReset(r.Context(), body.Email, ip)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" || body.NewPassword == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "token and newPassword required")
		return
	}
	switch err := h.svc.ResetPassword(r.Context(), body.Token, body.NewPassword); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrTokenConsumed):
		httpx.WriteError(w, http.StatusBadRequest, "token_invalid", "invalid or used token")
	case errors.Is(err, ErrTokenExpired):
		httpx.WriteError(w, http.StatusBadRequest, "token_expired", "token expired")
	case errors.Is(err, ErrPasswordTooShort):
		httpx.WriteError(w, http.StatusBadRequest, "password_too_short", "password must be at least 12 characters")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (h *Handler) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFrom(r.Context())
	if !ok { httpx.WriteError(w, http.StatusUnauthorized, "auth_required", "session required"); return }
	var body struct{ NewEmail string `json:"newEmail"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NewEmail == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "newEmail required")
		return
	}
	switch err := h.svc.RequestEmailChange(r.Context(), u.ID, body.NewEmail); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrEmailInvalid):
		httpx.WriteError(w, http.StatusBadRequest, "email_invalid", "email looks invalid")
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteError(w, http.StatusConflict, "email_taken", "email already in use")
	case errors.Is(err, ErrEmailSame):
		httpx.WriteError(w, http.StatusBadRequest, "email_same", "new email matches current")
	case errors.Is(err, ErrRateLimited):
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "try again later")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (h *Handler) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var body struct{ Token string `json:"token"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "token required")
		return
	}
	switch err := h.svc.ConfirmEmailChange(r.Context(), body.Token); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrTokenConsumed):
		httpx.WriteError(w, http.StatusBadRequest, "token_invalid", "invalid or used token")
	case errors.Is(err, ErrTokenExpired):
		httpx.WriteError(w, http.StatusBadRequest, "token_expired", "token expired")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
```

Mount in `router.go`:

```go
// public
r.Post("/api/v1/auth/verify", authH.VerifyEmail)
r.Post("/api/v1/auth/password/reset-request", authH.RequestPasswordReset)
r.Post("/api/v1/auth/password/reset-confirm", authH.ConfirmPasswordReset)
r.Post("/api/v1/auth/email/change-confirm", authH.ConfirmEmailChange)

// authed
r.With(authSvc.RequireSession).Post("/api/v1/auth/verify/resend", authH.ResendVerification)
r.With(authSvc.RequireSession).Post("/api/v1/auth/email/change-request", authH.RequestEmailChange)
```

- [ ] **Step 3: OpenAPI documentation**

Append to `openapi/openapi.yaml` under the existing `paths:` block the six endpoints, each with a minimal request/response schema matching the handler bodies. Include `400 bad_request`, `401 auth_required`, `429 rate_limited`, and `204` responses.

- [ ] **Step 4: Verification**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/auth/...
go build ./...
```

End-to-end smoke (optional, manual):

```bash
# with server running
curl -X POST http://localhost:8080/api/v1/auth/password/reset-request \
     -H 'Content-Type: application/json' \
     -d '{"email":"someone@example.com"}' -i
# expect HTTP/1.1 204 No Content
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/handlers_email_flows.go backend/internal/auth/handlers_email_flows_test.go \
        backend/internal/http/router.go openapi/openapi.yaml
git commit -m "$(cat <<'EOF'
feat(auth): add HTTP handlers for email flows

Mounts the 6 endpoints from spec §4.2: /auth/verify,
/auth/verify/resend, /auth/password/reset-request,
/auth/password/reset-confirm, /auth/email/change-request,
/auth/email/change-confirm. OpenAPI documents the new surface.
EOF
)"
```

---

## Task 16: Frontend — verify / forgot / reset / confirm-change pages

**Files:**
- Create: `web/lib/auth/email-flows.ts`
- Create: `web/app/auth/verify/[token]/page.tsx`
- Create: `web/app/forgot/page.tsx`
- Create: `web/app/reset/[token]/page.tsx`
- Create: `web/app/auth/email/confirm/[token]/page.tsx`

Before starting, open `.claude/skills/folio-frontend-design/SKILL.md` and follow its guidance for component structure, form styling, and button choices.

- [ ] **Step 1: Fetch wrappers**

`web/lib/auth/email-flows.ts`:

```ts
import { apiClient } from "@/lib/api/client";

export async function verifyEmail(token: string): Promise<void> {
  const res = await apiClient.post("/api/v1/auth/verify", { token });
  if (!res.ok) throw new Error(await res.errorMessage());
}

export async function requestPasswordReset(email: string): Promise<void> {
  // Always resolves (backend is "always 200").
  await apiClient.post("/api/v1/auth/password/reset-request", { email });
}

export async function confirmPasswordReset(token: string, newPassword: string): Promise<void> {
  const res = await apiClient.post("/api/v1/auth/password/reset-confirm", { token, newPassword });
  if (!res.ok) throw new Error(await res.errorMessage());
}

export async function requestEmailChange(newEmail: string): Promise<void> {
  const res = await apiClient.post("/api/v1/auth/email/change-request", { newEmail });
  if (!res.ok) throw new Error(await res.errorMessage());
}

export async function confirmEmailChange(token: string): Promise<void> {
  const res = await apiClient.post("/api/v1/auth/email/change-confirm", { token });
  if (!res.ok) throw new Error(await res.errorMessage());
}

export async function resendVerification(): Promise<void> {
  const res = await apiClient.post("/api/v1/auth/verify/resend", {});
  if (!res.ok) throw new Error(await res.errorMessage());
}
```

(Adjust names to match plan 1's `web/lib/api/client.ts` actual API surface.)

- [ ] **Step 2: `/auth/verify/[token]/page.tsx`**

```tsx
"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { verifyEmail } from "@/lib/auth/email-flows";

type Status = "verifying" | "ok" | "error";

export default function VerifyEmailPage() {
  const { token } = useParams<{ token: string }>();
  const router = useRouter();
  const [status, setStatus] = useState<Status>("verifying");
  const [message, setMessage] = useState<string>("");

  useEffect(() => {
    (async () => {
      try {
        await verifyEmail(token);
        setStatus("ok");
        setTimeout(() => router.push("/workspaces"), 1500);
      } catch (err) {
        setStatus("error");
        setMessage(err instanceof Error ? err.message : "verification failed");
      }
    })();
  }, [token, router]);

  if (status === "verifying") return <main className="p-8">Verifying your email…</main>;
  if (status === "ok") return <main className="p-8">Email verified. Redirecting…</main>;
  return (
    <main className="p-8">
      <h1 className="text-xl font-semibold">Verification failed</h1>
      <p className="mt-2 text-sm text-muted-foreground">{message}</p>
      <p className="mt-4">
        The link may have expired. Sign in and request a new verification email.
      </p>
    </main>
  );
}
```

- [ ] **Step 3: `/forgot/page.tsx`**

Single email input, submit → call `requestPasswordReset(email)`, show a "check your inbox" message regardless of outcome (no existence leak).

- [ ] **Step 4: `/reset/[token]/page.tsx`**

Two password fields (new + confirm), client-side length ≥ 12 check, submit → `confirmPasswordReset(token, newPassword)`. On success redirect to `/login?reset=1`. On specific error codes (`token_expired`, `token_invalid`, `password_too_short`), render contextual copy.

- [ ] **Step 5: `/auth/email/confirm/[token]/page.tsx`**

Mirror `/auth/verify` — call `confirmEmailChange(token)` on mount, show success / failure states. On success redirect to `/settings/account`.

- [ ] **Step 6: Verification**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm test
pnpm build  # catches missing `use client` / bad imports
```

Manual smoke (optional): run backend + web; signup creates the verify email; copy the URL from backend logs (LogMailer), paste into browser, land on `/auth/verify/[token]` and see success.

- [ ] **Step 7: Commit**

```bash
git add web/lib/auth/ web/app/auth/verify/ web/app/forgot/ web/app/reset/ web/app/auth/email/
git commit -m "$(cat <<'EOF'
feat(web): add verify / forgot / reset / email-change pages

Four new Next.js pages wrapping the email-driven auth flows:
/auth/verify/[token], /forgot, /reset/[token],
/auth/email/confirm/[token]. Shared client helpers live in
web/lib/auth/email-flows.ts.
EOF
)"
```

---

## Task 17: Frontend — VerifyEmailBanner + /settings/account

**Files:**
- Create: `web/components/verify-email-banner.tsx`
- Create: `web/app/settings/account/page.tsx`
- Modify: `web/app/t/[slug]/layout.tsx` — mount the banner.

Read `.claude/skills/folio-frontend-design/SKILL.md` for banner color/spacing conventions before editing.

- [ ] **Step 1: `<VerifyEmailBanner>`**

`web/components/verify-email-banner.tsx`:

```tsx
"use client";

import { useState } from "react";
import { useIdentity } from "@/lib/hooks/use-identity";
import { resendVerification } from "@/lib/auth/email-flows";

export function VerifyEmailBanner() {
  const { user } = useIdentity();
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!user || user.emailVerifiedAt) return null;

  const onResend = async () => {
    setSending(true);
    setError(null);
    try {
      await resendVerification();
      setSent(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not resend");
    } finally {
      setSending(false);
    }
  };

  return (
    <div
      role="alert"
      className="border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-900"
    >
      <div className="flex items-center gap-3">
        <span>Verify your email to unlock invites, bank linking, and exports.</span>
        {sent ? (
          <span className="font-medium">Sent. Check your inbox.</span>
        ) : (
          <button
            type="button"
            onClick={onResend}
            disabled={sending}
            className="underline hover:no-underline disabled:opacity-60"
          >
            {sending ? "Sending…" : "Resend email"}
          </button>
        )}
        {error ? <span className="text-red-700">{error}</span> : null}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Mount in workspace layout**

In `web/app/t/[slug]/layout.tsx`, render `<VerifyEmailBanner />` above `{children}` so it appears on every authenticated workspace page.

- [ ] **Step 3: `/settings/account/page.tsx`**

Two sections:

1. **Email**: shows current email + `email_verified_at` status. If unverified, a "Resend verification email" button wired to `resendVerification`.
2. **Change email**: input for new email → calls `requestEmailChange(newEmail)` → shows "We've sent a confirmation email to <new>. Click the link there to complete the change."

Full page uses the skill's recommended form styles (label-on-top, `Input` from shadcn, error states).

- [ ] **Step 4: Verification**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

Manual smoke: unverified user signs up → sees banner on every `/t/{slug}/*` page → clicks "Resend email" → sees "Sent. Check your inbox." → reload and banner persists until verified.

- [ ] **Step 5: Commit**

```bash
git add web/components/verify-email-banner.tsx web/app/settings/account/ web/app/t/
git commit -m "$(cat <<'EOF'
feat(web): add VerifyEmailBanner and /settings/account

VerifyEmailBanner nags unverified users on every /t/{slug}/* page with
a one-click resend. /settings/account exposes email status plus the
change-email form wired to the new endpoints.
EOF
)"
```

---

## Task 18: End-to-end integration test + final verification

**Files:**
- Create: `backend/internal/auth/email_flows_e2e_test.go` — full signup → verify flow with a real `LogMailer` capturing the rendered email, plus a real River client running inline.

- [ ] **Step 1: E2E test**

```go
//go:build integration

package auth_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/mailer"
)

func TestE2E_SignupThenVerify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := testDBPool(t)             // helper from plan 1
	resetSchema(t, pool)              // helper from plan 1
	logMailer := &mailer.LogMailer{}

	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewSendEmailWorker(logMailer))

	jc, err := jobs.NewClient(pool, workers, jobs.Config{
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	if err != nil { t.Fatal(err) }
	if err := jc.Start(ctx); err != nil { t.Fatal(err) }
	defer jc.Stop(ctx)

	svc := auth.NewService(auth.Config{Pool: pool, Jobs: jc, AppURL: "https://folio.app", Now: time.Now})

	// Signup (from plan 1)
	user, _, err := svc.Signup(ctx, auth.SignupInput{
		Email: "alice@example.com", Password: "correcthorsebatterystaple", DisplayName: "Alice", WorkspaceName: "Personal",
		BaseCurrency: "CHF", CycleAnchorDay: 1, Locale: "en", Timezone: "UTC",
	})
	if err != nil { t.Fatal(err) }

	if err := svc.SendEmailVerification(ctx, user.ID); err != nil { t.Fatal(err) }

	// Give River a moment to dequeue the send_email job.
	waitForCondition(t, 5*time.Second, func() bool { return len(logMailer.Sent) == 1 })

	msg := logMailer.Sent[0]
	re := regexp.MustCompile(`https://folio\.app/auth/verify/(\S+)`)
	matches := re.FindStringSubmatch(msg.HTML)
	if len(matches) != 2 { t.Fatalf("no verify URL in email: %s", msg.HTML) }
	tokenPlain := strings.TrimRight(matches[1], `"`)

	if err := svc.VerifyEmail(ctx, tokenPlain); err != nil { t.Fatal(err) }

	var verifiedAt *time.Time
	if err := pool.QueryRow(ctx, `select email_verified_at from users where id = $1`, user.ID).Scan(&verifiedAt); err != nil {
		t.Fatal(err)
	}
	if verifiedAt == nil { t.Fatal("email_verified_at is NULL") }
}
```

- [ ] **Step 2: Run the full test matrix**

```bash
cd /Users/xmedavid/dev/folio/backend
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
go run ./cmd/folio-river-migrate up
go build ./...
go test ./...
go test -tags integration ./internal/auth/...
```

Expected: clean apply, all unit tests pass, integration test passes.

Frontend:

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

- [ ] **Step 3: Smoke the end-to-end HTTP surface**

With the dev stack running:

```bash
# Fresh user
curl -s -X POST http://localhost:8080/api/v1/auth/signup \
  -H 'Content-Type: application/json' -H 'X-Folio-Request: 1' \
  -d '{"email":"e2e@example.com","password":"abcdefghijkl","displayName":"E2E","workspaceName":"P","baseCurrency":"CHF","cycleAnchorDay":1,"locale":"en","timezone":"UTC"}' \
  -c /tmp/folio.cookies
# Reset request
curl -s -X POST http://localhost:8080/api/v1/auth/password/reset-request \
  -H 'Content-Type: application/json' -H 'X-Folio-Request: 1' \
  -d '{"email":"e2e@example.com"}' -i
# Expect 204
```

Backend logs should show `LogMailer` lines with the rendered reset URL (in dev with `RESEND_API_KEY` empty).

- [ ] **Step 4: Confirm no uncommitted changes**

```bash
cd /Users/xmedavid/dev/folio
git status
```

Expected: clean tree.

- [ ] **Step 5: Commit the integration test**

```bash
git add backend/internal/auth/email_flows_e2e_test.go
git commit -m "$(cat <<'EOF'
test(auth): add end-to-end signup → verify integration test

Covers the full happy path: signup → SendEmailVerification enqueues a
River job → SendEmailWorker renders the verify_email template via
LogMailer → extract token from rendered HTML → VerifyEmail flips
email_verified_at. Tagged `integration` because it requires Postgres
and River migrations.
EOF
)"
```

---

## Self-review checklist (run after writing all tasks, before declaring done)

- [ ] Every spec §7 email flow (verify, verify-resend, password reset, email-change-new, email-change-old-notice, invite) has a template (Task 5) and either a service method (Tasks 9–12) or is owned by plan 2 (invite).
- [ ] Every spec §4.2 email-driven endpoint is implemented (Task 15).
- [ ] `auth.RequireEmailVerified` exists (Task 13) and is applied to invite-accept and invite-create (Task 14). TODO stubs recorded for bank-link and data-export.
- [ ] `ResendMailer` implements `mailer.Mailer` (Task 4).
- [ ] `ResendMailer` replaces `LogMailer` in prod wiring; factory falls back to `LogMailer` when `RESEND_API_KEY` is empty (Task 8).
- [ ] `jobs.Client`, `jobs.SendEmailArgs`, `jobs.SweepSoftDeletedWorkspacesArgs` match canonical names (Task 3).
- [ ] `SoftDeletedWorkspaceSweeper` periodic job registered (Task 7).
- [ ] Plan 2's `folio-sweeper` CLI remains available as a manual backstop (unchanged).
- [ ] Spec §8.2 rate limits enforced: verify-resend (1/min, 5/hr/user), reset-request (3/hr/IP, 3/hr/email), email-change-request (3/hr/user) (Tasks 10–12, 15).
- [ ] Spec §8.3 audit events written: `user.email_verified`, `user.password_reset_completed`, `user.email_change_requested`, `user.email_change_confirmed` (Tasks 9, 11, 12).
- [ ] Frontend pages exist: `/auth/verify/[token]`, `/forgot`, `/reset/[token]`, `/auth/email/confirm/[token]`, `/settings/account` (Tasks 16, 17).
- [ ] `<VerifyEmailBanner>` mounted in `app/t/[slug]/layout.tsx` (Task 17).
- [ ] `RESEND_API_KEY`, `EMAIL_FROM`, `APP_URL` documented in `.env.example` and `docker-compose.dev.yml` (Task 8).
- [ ] River migrations applied via `folio-river-migrate`; dev reset flow documented (Task 2).
- [ ] Template rendering uses `{{.VerifyURL}}`, `{{.ResetURL}}`, `{{.ConfirmURL}}`, etc. — URLs built from `APP_URL` (Tasks 5, 9–12).
- [ ] Tests use `LogMailer` + inline River execution (Tasks 9–12, 18).
- [ ] Every task ends with a verification block and conventional-commit message.
- [ ] Every task commits its own scope; no cross-task artifacts.
