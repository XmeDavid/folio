# Folio Admin Console Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the instance-admin console — CLI bootstrap, `users.is_admin` grant/revoke, `/api/v1/admin/…` HTTP surface, and the `/admin/…` Next.js screens — so a Folio operator can debug workspaces/users, inspect the audit feed, and retry River jobs without SQL.

**Architecture:** `users.is_admin` is a boolean orthogonal to workspace roles; admins have no default access to financial data. Granting goes through the `folio-admin` CLI (self-host primary) or the `ADMIN_BOOTSTRAP_EMAIL` env-var first-run hook (wired into plan 1's `auth.Service.Signup`). `auth.RequireAdmin` returns **404** on a miss so admin-ness is not enumerable. All write endpoints sit behind `RequireFreshReauth(5m)`; both reads and writes emit `admin.*` audit events. A last-admin guard in the CLI and the HTTP revoke endpoint parallels the last-owner workspace invariant.

**Tech Stack:** Go 1.25; `github.com/spf13/cobra` for CLI subcommands; `github.com/go-chi/chi/v5` for routing; `github.com/jackc/pgx/v5` for Postgres; Next.js 16 (App Router) + React Query on the web side.

**Spec:** docs/superpowers/specs/2026-04-24-folio-auth-and-workspace-design.md (§11 primary; §8.3 `admin.*` audit actions; §13 web layout)

**Prior plans in series:**
- `docs/superpowers/plans/2026-04-24-folio-auth-foundation.md` (plan 1) — `auth` package, `identity` package, `users.is_admin` column, `RequireSession`, `RequireFreshReauth` stub, `auth.Service.Signup` with optional `AdminBootstrapHook` parameter
- `docs/superpowers/plans/2026-04-24-folio-invites-and-workspace-lifecycle.md` (plan 2) — invite flows, workspace soft delete, `identity.InviteService`
- `docs/superpowers/plans/2026-04-24-folio-email-infrastructure.md` (plan 3) — `mailer.ResendMailer`, `jobs.Client` for River, transactional email tables
- `docs/superpowers/plans/2026-04-24-folio-mfa.md` (plan 4) — MFA, real `RequireFreshReauth`, step-up reauth endpoint

---

## 0. Setup and shared patterns

### 0.1 Working directory

Most `go` commands run from `backend/`:

```bash
cd /Users/xmedavid/dev/folio/backend
```

Postgres must be running and `DATABASE_URL` must be exported (see `.env.example`).

### 0.2 Dependencies

Install cobra for the CLI:

```bash
cd /Users/xmedavid/dev/folio/backend
go get github.com/spf13/cobra@latest
go mod tidy
```

Expected: `backend/go.mod` gains `github.com/spf13/cobra vX.Y.Z` in the `require` block. `backend/go.sum` is updated.

### 0.3 Environment variables

Document the new env var in `/Users/xmedavid/dev/folio/.env.example`:

```bash
# Admin bootstrap: the first signup whose lowercased email matches this
# value is auto-granted is_admin=true (idempotent; writes
# admin.bootstrap_granted to audit_events). Leave unset in SaaS — use the
# folio-admin CLI instead.
ADMIN_BOOTSTRAP_EMAIL=
```

### 0.4 Build targets

Document the new Makefile target; see Task 10 for the exact edit. Builds drop the binary at `backend/bin/folio-admin`:

```bash
cd /Users/xmedavid/dev/folio/backend
go build -o bin/folio-admin ./cmd/folio-admin
```

### 0.5 Middleware chain (admin routes)

Every `/api/v1/admin/…` route is chained:

1. `auth.RequireSession` (plan 1)
2. `auth.RequireAdmin` — **404 on miss** (plan 5 — Task 2)
3. `auth.RequireFreshReauth(5 * time.Minute)` (plan 4) — **writes only**

Reads skip `RequireFreshReauth` but still emit an `admin.viewed_*` audit event at the service layer.

### 0.6 Test patterns

Admin tests use plan 1's `testdb` helper + a `CreateTestUser(t, pool, WithIsAdmin(true))` variant. Handler tests assemble a chi router with the full admin middleware chain and a test session cookie. Service tests talk to Postgres directly and assert `audit_events` rows after each operation.

Seed a non-admin user + an admin user in every handler suite to verify the 404-on-miss behaviour.

### 0.7 Per-task verification baseline

Every code task ends with:

```bash
cd /Users/xmedavid/dev/folio/backend
go build ./...
go test ./internal/admin/... ./cmd/folio-admin/... -count=1
```

Plus `pnpm --filter web lint && pnpm --filter web typecheck` for frontend tasks. Additional curl / CLI invocation checks are spelled out per task.

---

## Task 1: Wire `ADMIN_BOOTSTRAP_EMAIL` into signup

**Spec:** §11.1 first-run bootstrap.

Plan 1 leaves `auth.Service.Signup` with an optional `AdminBootstrapHook func(ctx context.Context, userID uuid.UUID, email string) error` field. Plan 5 creates `admin.Service.EnsureBootstrapAdmin` and wires it.

**Files:**
- Create: `backend/internal/admin/bootstrap.go`
- Create: `backend/internal/admin/bootstrap_test.go`
- Modify: `backend/cmd/folio-api/main.go` (or wherever plan 1 wires the server — inject the hook)

- [ ] **Step 1: Red — write the bootstrap test**

Create `backend/internal/admin/bootstrap_test.go`. Cover three cases:

1. `ADMIN_BOOTSTRAP_EMAIL` unset → `EnsureBootstrapAdmin` is a no-op; `users.is_admin` stays false; no audit row.
2. Env matches user email (case-insensitive, trimmed) → `users.is_admin=true`; one `admin.bootstrap_granted` audit row with `actor_user_id = NULL` and `workspace_id = NULL`.
3. Called twice with same matching email → second call is a no-op; still exactly one audit row.

Use plan 1's `testdb.New(t)` + a helper that creates a user and sets `ADMIN_BOOTSTRAP_EMAIL` via `t.Setenv`.

Expected initially: test fails because `admin.Service.EnsureBootstrapAdmin` does not exist.

- [ ] **Step 2: Green — implement `admin.Service.EnsureBootstrapAdmin`**

Write `backend/internal/admin/bootstrap.go`:

```go
// Package admin implements the Folio instance-admin console: HTTP + CLI.
package admin

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns writes for admin grants, read queries for workspaces/users/audit/jobs,
// and operational actions (retry job, resend email). All methods audit.
type Service struct {
	pool   *pgxpool.Pool
	getEnv func(string) string // injectable for tests
}

// NewService constructs the admin service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, getEnv: os.Getenv}
}

// EnsureBootstrapAdmin is called from auth.Service.Signup (plan 1 hook). If
// ADMIN_BOOTSTRAP_EMAIL is set and (case-insensitively) matches the new user's
// email, flips is_admin=true and writes admin.bootstrap_granted. Idempotent —
// safe to call on every signup.
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, userID uuid.UUID, email string) error {
	target := strings.ToLower(strings.TrimSpace(s.getEnv("ADMIN_BOOTSTRAP_EMAIL")))
	if target == "" {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(email)) != target {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var already bool
	err = tx.QueryRow(ctx, `select is_admin from users where id = $1`, userID).Scan(&already)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // shouldn't happen from Signup, but be defensive
		}
		return err
	}
	if already {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`update users set is_admin = true, updated_at = now() where id = $1`,
		userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into audit_events (id, workspace_id, actor_user_id, entity_type, entity_id, action, before_jsonb, after_jsonb, occurred_at)
		values ($1, null, null, 'user', $2, 'admin.bootstrap_granted', null, jsonb_build_object('is_admin', true), now())
	`, uuid.Must(uuid.NewV7()), userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

Run the tests; expected all three green.

- [ ] **Step 3: Wire the hook into the HTTP server bootstrap**

In the server entry point that constructs `auth.Service` (plan 1), pass the hook:

```go
adminSvc := admin.NewService(db)
authSvc := auth.NewService(db, identitySvc, auth.Config{
    AdminBootstrapHook: adminSvc.EnsureBootstrapAdmin,
})
```

Expected: existing plan 1 tests continue to pass; new integration test: signup with a matching `ADMIN_BOOTSTRAP_EMAIL` produces a user whose `GET /api/v1/me` reports `isAdmin: true`.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/admin/ backend/cmd/folio-api/ .env.example
git commit -m "$(cat <<'EOF'
feat(admin): add EnsureBootstrapAdmin signup hook

First signup matching ADMIN_BOOTSTRAP_EMAIL is granted is_admin=true
and writes admin.bootstrap_granted. Idempotent; safe across restarts.
EOF
)"
```

---

## Task 2: `auth.RequireAdmin` middleware (404-on-miss)

**Spec:** §11.2 middleware chain bullet 2.

**Files:**
- Modify: `backend/internal/auth/middleware.go` (plan 1 file)
- Create: `backend/internal/auth/middleware_require_admin_test.go`

- [ ] **Step 1: Red — write the middleware test**

Cases:
1. No session → plan 1's `RequireSession` returns 401 (already tested — skip).
2. Session for non-admin user → `RequireAdmin` writes **404** with `code: "not_found"`; downstream handler is not called.
3. Session for admin user → downstream handler runs; `auth.UserFromCtx` inside it returns `user.IsAdmin == true`.

Fail initially (no `RequireAdmin` symbol).

- [ ] **Step 2: Green — implement**

Add to `backend/internal/auth/middleware.go`:

```go
// RequireAdmin gates a route on users.is_admin. Returns 404 on miss (not 403)
// so admin-ness is not enumerable from anonymous probes. Must be chained
// AFTER RequireSession.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromCtx(r.Context())
		if !ok || !user.IsAdmin {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

Run the tests; expected green.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/auth/middleware.go backend/internal/auth/middleware_require_admin_test.go
git commit -m "feat(auth): add RequireAdmin middleware (404 on miss)"
```

---

## Task 3: Admin service — shared filters + pagination

**Spec:** §11.2 envelope for every list endpoint.

**Files:**
- Create: `backend/internal/admin/filters.go`
- Create: `backend/internal/admin/filters_test.go`

- [ ] **Step 1: Red — write the filter + pagination tests**

In `backend/internal/admin/filters_test.go`:

- `AdminListFilter{Limit: 0}.Normalize()` returns `Limit: 50, MaxLimit: 200`.
- `AdminListFilter{Limit: 500}.Normalize()` clamps to `Limit: 200`.
- `AdminListFilter{Cursor: "xyz"}.Decode()` returns an error on malformed cursor.
- `(WorkspaceListFilter|UserListFilter|AuditFilter|JobFilter)` each round-trip via encode/decode.

- [ ] **Step 2: Green — filters + pagination**

Create `backend/internal/admin/filters.go`:

```go
package admin

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type pagination struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

// AdminListFilter is the shared pagination envelope.
type AdminListFilter struct {
	Limit  int
	Cursor string
}

func (f AdminListFilter) Normalize() AdminListFilter {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	return f
}

// WorkspaceListFilter: search by name/slug/id substring, include soft-deleted.
type WorkspaceListFilter struct {
	AdminListFilter
	Search          string
	IncludeDeleted  bool
}

// UserListFilter: search by email substring.
type UserListFilter struct {
	AdminListFilter
	Search      string
	IsAdminOnly bool
}

// AuditFilter: filter the audit feed across all workspaces.
type AuditFilter struct {
	AdminListFilter
	ActorUserID *uuid.UUID
	WorkspaceID    *uuid.UUID
	Action      string
	Since       *time.Time
	Until       *time.Time
}

// JobFilter: River queue view.
type JobFilter struct {
	AdminListFilter
	State string // 'running' | 'scheduled' | 'retryable' | 'available' | 'completed' | 'discarded' | 'cancelled'
	Kind  string
}

// encodeCursor / decodeCursor: opaque base64-json cursor (e.g. {"at":"...","id":"..."}).
// Used only in SQL where-clauses, never exposed to the client in a parseable form.
func encodeCursor(v any) string { /* base64(json) */ return "" }
func decodeCursor(s string, into any) error { /* inverse */ return nil }
```

Implement the two helpers with `encoding/json` + `base64.StdEncoding`. Export a test helper `MarshalCursor`/`UnmarshalCursor` only via `export_test.go` if needed.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/admin/filters.go backend/internal/admin/filters_test.go
git commit -m "feat(admin): add shared list filters and opaque cursor"
```

---

## Task 4: Admin service — `ListWorkspaces` + `WorkspaceDetail`

**Spec:** §11.2 workspace list + detail (metadata only, no financial rows).

**Files:**
- Create: `backend/internal/admin/workspaces.go`
- Create: `backend/internal/admin/workspaces_test.go`

- [ ] **Step 1: Red — write the tests**

In `backend/internal/admin/workspaces_test.go`:

- Seed three workspaces; one soft-deleted via `update workspaces set deleted_at = now()`.
- `ListWorkspaces(ctx, WorkspaceListFilter{IncludeDeleted: false})` returns 2, ordered by `created_at desc`.
- `ListWorkspaces(ctx, WorkspaceListFilter{IncludeDeleted: true})` returns 3.
- `ListWorkspaces(ctx, WorkspaceListFilter{Search: "<slug>"})` narrows correctly (name/slug/id substring match).
- `WorkspaceDetail(ctx, workspaceID)` returns member count (seed 2 memberships), settings, `DeletedAt`, `LastActivityAt` (max of `updated_at` across workspace-scoped tables — start simple: `max(audit_events.occurred_at)` where `workspace_id = $1`).
- `WorkspaceDetail` emits an `admin.viewed_workspace` audit row keyed to the caller.
- Regression guard: a test seeding a transaction + account for the workspace asserts the `WorkspaceDetail` return value does **not** include any financial fields (use reflection to assert no `decimal` or money-typed fields exist).

**Note:** `ListWorkspaces` / `WorkspaceDetail` return metadata only — never joins `accounts`, `transactions`, or other financial tables.

- [ ] **Step 2: Green — implement**

Create `backend/internal/admin/workspaces.go`:

```go
func (s *Service) ListWorkspaces(ctx context.Context, filter WorkspaceListFilter) ([]identity.Workspace, pagination, error) {
	filter.AdminListFilter = filter.Normalize()
	// Build WHERE: (deleted_at is null OR $includeDeleted) AND
	// (search empty OR name ILIKE $s OR slug ILIKE $s OR id::text = $s).
	// ORDER BY created_at desc, id desc. LIMIT filter.Limit + 1 to detect next page.
	// Return up to filter.Limit rows + encoded cursor for the spillover.
}

type WorkspaceDetail struct {
	Workspace         identity.Workspace `json:"workspace"`
	MemberCount    int             `json:"memberCount"`
	DeletedAt      *time.Time      `json:"deletedAt,omitempty"`
	LastActivityAt *time.Time      `json:"lastActivityAt,omitempty"`
}

func (s *Service) WorkspaceDetail(ctx context.Context, workspaceID uuid.UUID, actorUserID uuid.UUID) (WorkspaceDetail, error) {
	// 1. SELECT workspace row (including deleted_at).
	// 2. SELECT count(*) FROM workspace_memberships WHERE workspace_id = $1.
	// 3. SELECT max(occurred_at) FROM audit_events WHERE workspace_id = $1.
	// 4. INSERT into audit_events (..., action='admin.viewed_workspace', actor_user_id=$2, ...).
	// 5. return.
}
```

SQL queries use `references workspaces(id)` unchanged — no soft-delete filter so admins can still see deleted workspaces.

Expected: tests green.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/admin/workspaces.go backend/internal/admin/workspaces_test.go
git commit -m "$(cat <<'EOF'
feat(admin): add ListWorkspaces and WorkspaceDetail

Includes soft-deleted workspaces; detail returns metadata only (member
count, last activity) — never joins financial tables. Emits
admin.viewed_workspace on every detail read.
EOF
)"
```

---

## Task 5: Admin service — `ListUsers` + `UserDetail`

**Spec:** §11.2 user list + detail.

**Files:**
- Create: `backend/internal/admin/users.go`
- Create: `backend/internal/admin/users_test.go`

- [ ] **Step 1: Red — write the tests**

Cases:
- `ListUsers(Search: "alice")` filters by `email ILIKE '%alice%'`.
- `ListUsers(IsAdminOnly: true)` returns only admins.
- `UserDetail(ctx, userID, actorUserID)` returns `Memberships` from `workspace_memberships` join `workspaces` (slug, name, role), active `sessions` rows where `expires_at > now()`, MFA state from plan 4's `webauthn_credentials` + `totp_credentials` tables (counts + booleans), and `users.last_login_at`.
- `UserDetail` emits an `admin.viewed_user` audit row.
- Regression: seeding a user with `is_admin=true` exposes it in `IsAdminOnly=true` and flips a field on `UserDetail.User.IsAdmin`.

- [ ] **Step 2: Green — implement**

```go
type MembershipSummary struct {
	WorkspaceID   uuid.UUID `json:"workspaceId"`
	WorkspaceName string    `json:"workspaceName"`
	WorkspaceSlug string    `json:"workspaceSlug"`
	Role       string    `json:"role"`
	JoinedAt   time.Time `json:"joinedAt"`
}

type SessionSummary struct {
	ID          string     `json:"id"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	UserAgent   string     `json:"userAgent"`
	IP          *string    `json:"ip,omitempty"`
}

type MFASummary struct {
	Passkeys              []PasskeySummary `json:"passkeys"`
	TOTPEnabled           bool             `json:"totpEnabled"`
	RecoveryCodesRemaining int             `json:"recoveryCodesRemaining"`
}

type UserDetail struct {
	User           identity.User       `json:"user"`
	Memberships    []MembershipSummary `json:"memberships"`
	ActiveSessions []SessionSummary    `json:"activeSessions"`
	MFA            MFASummary          `json:"mfa"`
	LastLoginAt    *time.Time          `json:"lastLoginAt,omitempty"`
}

func (s *Service) ListUsers(ctx context.Context, filter UserListFilter) ([]identity.User, pagination, error) { /* ... */ }
func (s *Service) UserDetail(ctx context.Context, userID uuid.UUID, actorUserID uuid.UUID) (UserDetail, error) { /* ... */ }
```

Run; green.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/admin/users.go backend/internal/admin/users_test.go
git commit -m "feat(admin): add ListUsers and UserDetail with audit.viewed_user"
```

---

## Task 6: Admin service — `ListAudit`

**Spec:** §11.2 cross-workspace audit feed.

**Files:**
- Create: `backend/internal/admin/audit.go`
- Create: `backend/internal/admin/audit_test.go`

- [ ] **Step 1: Red — write the tests**

Cases:
- Cross-workspace query — seeding audit rows under two workspaces returns rows from both.
- Filters by `ActorUserID`, `WorkspaceID`, `Action` substring (LIKE `action_prefix%`), `Since`, `Until` each narrow results correctly.
- Combined filters intersect (e.g. `ActorUserID + Action='user.login_succeeded'`).
- Emits exactly one `admin.viewed_audit` audit row per call (with filter params captured in `after_jsonb` for forensic reconstruction).
- Pagination: seeding 60 rows and calling with `Limit: 25` returns 25 + a non-empty `nextCursor`; second call with that cursor returns the next 25.

- [ ] **Step 2: Green — implement**

```go
type AuditEvent struct {
	ID           uuid.UUID       `json:"id"`
	WorkspaceID     *uuid.UUID      `json:"workspaceId,omitempty"`
	ActorUserID  *uuid.UUID      `json:"actorUserId,omitempty"`
	EntityType   string          `json:"entityType"`
	EntityID     uuid.UUID       `json:"entityId"`
	Action       string          `json:"action"`
	BeforeJSONB  json.RawMessage `json:"before,omitempty"`
	AfterJSONB   json.RawMessage `json:"after,omitempty"`
	OccurredAt   time.Time       `json:"occurredAt"`
}

func (s *Service) ListAudit(ctx context.Context, filter AuditFilter, actorUserID uuid.UUID) ([]AuditEvent, pagination, error) {
	// Build dynamic WHERE. ORDER BY occurred_at desc, id desc.
	// Cursor = {last_occurred_at, last_id}; next-page WHERE is
	// (occurred_at, id) < (cursor.occurred_at, cursor.id).
	// After reading, INSERT one admin.viewed_audit row with the filter as after_jsonb.
}
```

SQL uses `audit_events_entity_idx` where applicable.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/admin/audit.go backend/internal/admin/audit_test.go
git commit -m "feat(admin): add cross-workspace ListAudit with filter capture"
```

---

## Task 7: Admin service — `ListJobs`

**Spec:** §11.2 River queue view.

**Files:**
- Create: `backend/internal/admin/jobs.go`
- Create: `backend/internal/admin/jobs_test.go`

- [ ] **Step 1: Red — write the tests**

Relies on plan 3's `jobs.Client` and River's `river_job` table. Cases:

- Seed three jobs via `riverClient.InsertMany` in one of states `available`, `retryable`, `completed`.
- `ListJobs(ctx, JobFilter{State: "retryable"})` returns only the retryable job.
- `ListJobs(ctx, JobFilter{Kind: "email.send"})` filters by kind.
- Default (empty filter) orders by `scheduled_at desc, id desc` and returns all seeded jobs.
- Pagination envelope populated correctly.

- [ ] **Step 2: Green — implement**

```go
type Job struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	Queue       string          `json:"queue"`
	State       string          `json:"state"`
	AttemptedAt *time.Time      `json:"attemptedAt,omitempty"`
	ScheduledAt time.Time       `json:"scheduledAt"`
	Errors      []string        `json:"errors,omitempty"`
	Args        json.RawMessage `json:"args"`
}

func (s *Service) ListJobs(ctx context.Context, filter JobFilter) ([]Job, pagination, error) {
	// SELECT id, kind, queue, state, attempted_at, scheduled_at, errors, args
	// FROM river_job
	// WHERE ($state = '' OR state = $state) AND ($kind = '' OR kind = $kind)
	// ORDER BY scheduled_at desc, id desc LIMIT $limit + 1;
}
```

Note: River exposes the `river_job` schema publicly. Use `pgx` directly; don't depend on private River APIs.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/admin/jobs.go backend/internal/admin/jobs_test.go
git commit -m "feat(admin): add ListJobs over River river_job table"
```

---

## Task 8: Admin service — write operations

**Spec:** §11.1 last-admin guard; §11.2 grant/revoke, retry job, resend email.

**Files:**
- Create: `backend/internal/admin/grants.go`
- Create: `backend/internal/admin/operations.go`
- Create: `backend/internal/admin/grants_test.go`
- Create: `backend/internal/admin/operations_test.go`

- [ ] **Step 1: Red — `GrantAdmin` tests**

Cases:
- Call on non-admin user → `is_admin=true`; audit row `admin.granted` with `actor_user_id=<caller>`.
- Call on already-admin user → no-op; no new audit row.
- Returns `httpx.NewNotFoundError` if user does not exist.

- [ ] **Step 2: Green — `GrantAdmin`**

```go
func (s *Service) GrantAdmin(ctx context.Context, userID, actorUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	var already bool
	err = tx.QueryRow(ctx, `select is_admin from users where id = $1`, userID).Scan(&already)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("user")
	}
	if err != nil { return err }
	if already { return tx.Commit(ctx) }

	if _, err := tx.Exec(ctx,
		`update users set is_admin = true, updated_at = now() where id = $1`, userID); err != nil {
		return err
	}
	if err := writeAdminAudit(ctx, tx, "admin.granted", actorUserID, "user", userID, nil,
		map[string]any{"is_admin": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

Where `writeAdminAudit` is a package-private helper that inserts a row into `audit_events` with `workspace_id = NULL`.

- [ ] **Step 3: Red — `RevokeAdmin` tests**

Cases:
- Two admins seeded — revoking one succeeds; audit row `admin.revoked`.
- One admin seeded — revoking fails with `ErrLastAdmin` (export `var ErrLastAdmin = errors.New("cannot revoke the last admin")`); `is_admin` stays `true`; no audit row.
- Call on non-admin → no-op.
- Returns `NotFoundError` on missing user.

- [ ] **Step 4: Green — `RevokeAdmin` with last-admin guard**

```go
var ErrLastAdmin = errors.New("cannot revoke the last admin")

func (s *Service) RevokeAdmin(ctx context.Context, userID, actorUserID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	var isAdmin bool
	err = tx.QueryRow(ctx,
		`select is_admin from users where id = $1 for update`, userID).Scan(&isAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("user")
	}
	if err != nil { return err }
	if !isAdmin { return tx.Commit(ctx) }

	rows, err := tx.Query(ctx,
		`select id from users where is_admin = true order by id for update`)
	if err != nil {
		return err
	}
	var adminCount int
	for rows.Next() { adminCount++ }
	if err := rows.Err(); err != nil { rows.Close(); return err }
	rows.Close()
	if adminCount <= 1 {
		return ErrLastAdmin
	}

	if _, err := tx.Exec(ctx,
		`update users set is_admin = false, updated_at = now() where id = $1`, userID); err != nil {
		return err
	}
	if err := writeAdminAudit(ctx, tx, "admin.revoked", actorUserID, "user", userID,
		map[string]any{"is_admin": true}, map[string]any{"is_admin": false}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

Locking all current admin rows serialises concurrent revokes of different admins before counting, preventing two revokes from both observing `adminCount > 1`. Run; green.

- [ ] **Step 5: Red — `RetryJob` + `ResendEmail` tests**

Create `backend/internal/admin/operations_test.go`. Uses plan 3's `jobs.Client`:

- `RetryJob(ctx, <failed job id>, actorUserID)` → re-enqueues via `jobClient.JobRetry(ctx, id)`; audit `admin.retried_job`; works on `state IN ('cancelled','discarded','retryable')`.
- `RetryJob` on a running job → returns error (bubble up River's error); no audit row.
- `ResendEmail(ctx, <email id>, actorUserID)` → re-enqueues the bounced email via `mailer.Enqueue` (plan 3); audit `admin.resent_email`; updates `transactional_emails.last_resent_at` (column added in plan 3).

- [ ] **Step 6: Green — `RetryJob` + `ResendEmail`**

```go
func (s *Service) RetryJob(ctx context.Context, jobID int64, actorUserID uuid.UUID) error {
	if _, err := s.jobs.JobRetry(ctx, jobID); err != nil {
		return err
	}
	return s.writeAdminAuditRow(ctx, "admin.retried_job", actorUserID, "job",
		uuid.Must(uuid.NewV7()), nil, map[string]any{"job_id": jobID})
}

func (s *Service) ResendEmail(ctx context.Context, emailID uuid.UUID, actorUserID uuid.UUID) error {
	// 1. SELECT transactional_emails row.
	// 2. Enqueue a new SendEmail job with the same args.
	// 3. UPDATE transactional_emails SET last_resent_at = now() WHERE id = $1.
	// 4. Audit admin.resent_email.
}
```

Inject `jobs.Client` via a new `NewServiceWithJobs` constructor so existing `NewService` callers (Task 1 did not need jobs) don't break:

```go
type Service struct {
	pool   *pgxpool.Pool
	jobs   *jobs.Client
	mailer mailer.Mailer
	getEnv func(string) string
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool, getEnv: os.Getenv} }
func (s *Service) WithJobs(c *jobs.Client) *Service { s.jobs = c; return s }
func (s *Service) WithMailer(m mailer.Mailer) *Service { s.mailer = m; return s }
```

The server bootstrap calls `admin.NewService(db).WithJobs(jobsClient).WithMailer(resend)`.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/admin/
git commit -m "$(cat <<'EOF'
feat(admin): add GrantAdmin/RevokeAdmin + RetryJob/ResendEmail

GrantAdmin is idempotent. RevokeAdmin has a last-admin guard
(ErrLastAdmin) parallel to the last-owner workspace invariant. RetryJob
delegates to River; ResendEmail re-enqueues the transactional email job.
EOF
)"
```

---

## Task 9: Admin HTTP handler — mount and endpoints

**Spec:** §11.2 (10 endpoints).

**Files:**
- Create: `backend/internal/admin/handler.go`
- Create: `backend/internal/admin/handler_test.go`
- Modify: `backend/internal/http/router.go` (mount `/api/v1/admin/…`)

- [ ] **Step 1: Red — handler tests**

For each endpoint in the table below, seed a suitable fixture, issue a `httptest.NewRequest`, and assert the response.

| Method | Route | Handler func | Middleware |
|---|---|---|---|
| GET | `/api/v1/admin/workspaces` | `ListWorkspacesHandler` | Session + Admin |
| GET | `/api/v1/admin/workspaces/{workspaceId}` | `WorkspaceDetailHandler` | Session + Admin |
| GET | `/api/v1/admin/users` | `ListUsersHandler` | Session + Admin |
| GET | `/api/v1/admin/users/{userId}` | `UserDetailHandler` | Session + Admin |
| GET | `/api/v1/admin/audit` | `ListAuditHandler` | Session + Admin |
| GET | `/api/v1/admin/jobs` | `ListJobsHandler` | Session + Admin |
| POST | `/api/v1/admin/jobs/{jobId}/retry` | `RetryJobHandler` | Session + Admin + FreshReauth(5m) |
| POST | `/api/v1/admin/emails/{emailId}/resend` | `ResendEmailHandler` | Session + Admin + FreshReauth(5m) |
| POST | `/api/v1/admin/users/{userId}/grant-admin` | `GrantAdminHandler` | Session + Admin + FreshReauth(5m) |
| POST | `/api/v1/admin/users/{userId}/revoke-admin` | `RevokeAdminHandler` | Session + Admin + FreshReauth(5m) |

Tests MUST cover:

1. **404-on-miss**: non-admin user hitting any of the 10 routes → 404 `{"error":"not found","code":"not_found"}`. This is the enumeration-safety property.
2. **Stale session on write**: admin user without fresh reauth hitting a write route → 403 `{"code":"reauth_required"}` (plan 4).
3. **Happy path**: admin + fresh reauth → 200 with expected body.
4. **Last-admin guard**: seed a single admin, call `POST /users/{me}/revoke-admin` → 409 `{"code":"last_admin"}`; body includes a human-readable error.
5. **Pagination**: `GET /users?limit=2&cursor=<next>` returns the second page.

- [ ] **Step 2: Green — implement the handler**

Create `backend/internal/admin/handler.go`:

```go
package admin

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Mount attaches the admin routes onto r. Caller is responsible for the outer
// middleware chain (RequireSession + RequireAdmin); Mount itself adds
// RequireFreshReauth(5m) to the write subrouter.
func (h *Handler) Mount(r chi.Router, requireFreshReauth func(http.Handler) http.Handler) {
	r.Get("/workspaces",            h.listWorkspaces)
	r.Get("/workspaces/{workspaceId}", h.workspaceDetail)
	r.Get("/users",              h.listUsers)
	r.Get("/users/{userId}",     h.userDetail)
	r.Get("/audit",              h.listAudit)
	r.Get("/jobs",               h.listJobs)

	r.Group(func(r chi.Router) {
		r.Use(requireFreshReauth)
		r.Post("/jobs/{jobId}/retry",            h.retryJob)
		r.Post("/emails/{emailId}/resend",       h.resendEmail)
		r.Post("/users/{userId}/grant-admin",    h.grantAdmin)
		r.Post("/users/{userId}/revoke-admin",   h.revokeAdmin)
	})
}
```

Each handler parses URL params + query string, calls the Service method, maps errors:
- `ErrLastAdmin` → 409 `{"code":"last_admin"}`.
- `httpx.NotFoundError` → 404.
- `httpx.ValidationError` → 400.
- Everything else → `httpx.WriteServiceError` → 500.

Response bodies are the typed structs (`identity.Workspace`, `admin.UserDetail`, etc.) with an envelope:

```json
{"data": [...], "pagination": {"limit": 50, "nextCursor": "..."}}
```

- [ ] **Step 3: Mount in the router**

Edit `backend/internal/http/router.go` — inside `r.Route("/api/v1", …)`, add:

```go
adminSvc := admin.NewService(d.DB).WithJobs(d.Jobs).WithMailer(d.Mailer)
adminH := admin.NewHandler(adminSvc)

r.Route("/admin", func(r chi.Router) {
    r.Use(authSvc.RequireSession)
    r.Use(authSvc.RequireAdmin)
    adminH.Mount(r, auth.RequireFreshReauth(5*time.Minute))
})
```

`Deps` in this file gains `Jobs *jobs.Client` and `Mailer mailer.Mailer` (already added in plans 3; this task just consumes them).

- [ ] **Step 4: Verify end-to-end with curl**

Run the server locally; sign in as an admin user (plan 1's login endpoint); paste the resulting cookie into:

```bash
# List workspaces — expect 200 with data array
curl -s -b 'folio_session=<token>' -H 'X-Folio-Request: 1' \
  http://localhost:8080/api/v1/admin/workspaces | jq .

# Revoke self when only admin — expect 409 last_admin
curl -s -b 'folio_session=<token>' -H 'X-Folio-Request: 1' -X POST \
  http://localhost:8080/api/v1/admin/users/<me>/revoke-admin | jq .

# As non-admin — expect 404 not_found
curl -s -b 'folio_session=<non-admin token>' \
  http://localhost:8080/api/v1/admin/workspaces | jq .
```

- [ ] **Step 5: Commit**

```bash
git add backend/internal/admin/handler.go backend/internal/admin/handler_test.go backend/internal/http/router.go
git commit -m "$(cat <<'EOF'
feat(admin): mount /api/v1/admin/* HTTP surface

10 endpoints per spec §11.2. Writes are gated behind
RequireFreshReauth(5m). 404-on-miss from RequireAdmin keeps
admin-ness non-enumerable.
EOF
)"
```

---

## Task 10: CLI binary — `folio-admin`

**Spec:** §11.1 CLI.

**Files:**
- Create: `backend/cmd/folio-admin/main.go`
- Create: `backend/cmd/folio-admin/grant.go`
- Create: `backend/cmd/folio-admin/revoke.go`
- Create: `backend/cmd/folio-admin/list.go`
- Create: `backend/cmd/folio-admin/main_test.go`
- Modify: `/Users/xmedavid/dev/folio/Makefile` (add `admin-cli` target)

- [ ] **Step 1: Red — CLI integration tests**

Use Go's `testscript` pattern or direct `exec.Command` against a built binary. For speed, prefer exporting the cobra root cmd and calling `cmd.Execute()` with a custom `SetArgs` in-process, reusing the test Postgres pool.

Cases:
- `folio-admin list` — prints a TSV table with `EMAIL\tUSER_ID\tGRANTED_AT` for every admin; exit 0; works on empty admin set (prints header only).
- `folio-admin grant alice@example.com` — flips `is_admin=true`; prints `granted: alice@example.com`; exit 0.
- `folio-admin grant no-such-user@example.com` — exit 1 with `error: user not found`.
- `folio-admin revoke alice@example.com` — when two admins exist, succeeds; exit 0.
- `folio-admin revoke alice@example.com` — when only one admin (alice), exit 1 with `error: cannot revoke the last admin`; `is_admin` stays true.
- All three subcommands read `DATABASE_URL` from env; missing → exit 2 with usage.

- [ ] **Step 2: Green — implement cobra root + subcommands**

`backend/cmd/folio-admin/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "folio-admin",
		Short: "Folio instance-admin management",
		Long:  "Grant, revoke, and list Folio instance admins. Talks to Postgres directly via DATABASE_URL.",
	}
	root.AddCommand(newGrantCmd(), newRevokeCmd(), newListCmd())
	return root
}

func openPool(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, fmt.Errorf("DATABASE_URL not set")
	}
	return pgxpool.New(ctx, url)
}
```

`backend/cmd/folio-admin/grant.go`:

```go
func newGrantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "grant <email>",
		Short: "Promote a user to admin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			pool, err := openPool(ctx)
			if err != nil { return err }
			defer pool.Close()

			email := strings.ToLower(strings.TrimSpace(args[0]))
			svc := admin.NewService(pool)
			var userID uuid.UUID
			err = pool.QueryRow(ctx, `select id from users where email = $1`, email).Scan(&userID)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("user not found")
			}
			if err != nil { return err }

			// actorUserID = NULL for CLI calls → audit row shows nil actor.
			if err := svc.GrantAdmin(ctx, userID, uuid.Nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "granted: %s\n", email)
			return nil
		},
	}
}
```

`revoke.go` is parallel; on `ErrLastAdmin` returns the error so cobra prints `error: cannot revoke the last admin` and exits 1.

`list.go`:

```go
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all admins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			pool, err := openPool(ctx)
			if err != nil { return err }
			defer pool.Close()

			rows, err := pool.Query(ctx,
				`select email, id, updated_at from users where is_admin = true order by email`)
			if err != nil { return err }
			defer rows.Close()

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "EMAIL\tUSER_ID\tUPDATED_AT")
			for rows.Next() {
				var email string
				var id uuid.UUID
				var at time.Time
				if err := rows.Scan(&email, &id, &at); err != nil { return err }
				fmt.Fprintf(w, "%s\t%s\t%s\n", email, id, at.Format(time.RFC3339))
			}
			return w.Flush()
		},
	}
}
```

`admin.Service.GrantAdmin` / `RevokeAdmin` accept `actorUserID uuid.UUID`. Pass `uuid.Nil`; the service writes `actor_user_id = NULL` when `uuid.Nil`.

Update `writeAdminAudit` to coerce `uuid.Nil` → SQL NULL.

- [ ] **Step 3: Update `admin.Service` to handle nil actor**

In `grants.go`, change the audit insert to:

```go
var actor any
if actorUserID == uuid.Nil { actor = nil } else { actor = actorUserID }
```

And pass `actor` into the insert's `$1` placeholder.

Run the CLI tests; green.

- [ ] **Step 4: Build binary + add Makefile target**

Edit `/Users/xmedavid/dev/folio/Makefile`:

```makefile
.PHONY: admin-cli
admin-cli: ## Build folio-admin CLI binary
	cd backend && go build -o bin/folio-admin ./cmd/folio-admin
	@echo "built: backend/bin/folio-admin"
```

Verify:

```bash
cd /Users/xmedavid/dev/folio
make admin-cli
./backend/bin/folio-admin --help
./backend/bin/folio-admin list
```

Expected: help text lists `grant`, `revoke`, `list`. `list` prints the header with zero rows (fresh DB) or matching admins.

- [ ] **Step 5: Commit**

```bash
git add backend/cmd/folio-admin/ backend/internal/admin/grants.go Makefile
git commit -m "$(cat <<'EOF'
feat(cli): add folio-admin CLI (grant/revoke/list)

Cobra-based; reads DATABASE_URL directly. Shares the last-admin guard
with the HTTP revoke endpoint by reusing admin.Service.RevokeAdmin.
make admin-cli builds backend/bin/folio-admin.
EOF
)"
```

---

## Task 11: OpenAPI — add `/api/v1/admin/*` paths

**Spec:** §11.2.

**Files:**
- Modify: `openapi/openapi.yaml`

- [ ] **Step 1: Add admin schemas**

Under `components.schemas`, add:

```yaml
WorkspaceDetail:
  type: object
  required: [workspace, memberCount]
  properties:
    workspace:     { $ref: '#/components/schemas/Workspace' }
    memberCount: { type: integer }
    deletedAt:  { type: string, format: date-time, nullable: true }
    lastActivityAt: { type: string, format: date-time, nullable: true }

UserDetail:
  type: object
  required: [user, memberships, activeSessions, mfa]
  properties:
    user:            { $ref: '#/components/schemas/User' }
    memberships:     { type: array, items: { $ref: '#/components/schemas/MembershipSummary' } }
    activeSessions:  { type: array, items: { $ref: '#/components/schemas/SessionSummary' } }
    mfa:             { $ref: '#/components/schemas/MFASummary' }
    lastLoginAt:     { type: string, format: date-time, nullable: true }

AuditEvent:
  type: object
  required: [id, entityType, entityId, action, occurredAt]
  properties:
    id:           { type: string, format: uuid }
    workspaceId:     { type: string, format: uuid, nullable: true }
    actorUserId:  { type: string, format: uuid, nullable: true }
    entityType:   { type: string }
    entityId:     { type: string, format: uuid }
    action:       { type: string }
    before:       { type: object, nullable: true }
    after:        { type: object, nullable: true }
    occurredAt:   { type: string, format: date-time }

Job:
  type: object
  required: [id, kind, queue, state, scheduledAt]
  properties:
    id:           { type: integer, format: int64 }
    kind:         { type: string }
    queue:        { type: string }
    state:        { type: string, enum: [running, scheduled, retryable, available, completed, discarded, cancelled] }
    attemptedAt:  { type: string, format: date-time, nullable: true }
    scheduledAt:  { type: string, format: date-time }
    errors:       { type: array, items: { type: string } }
    args:         { type: object }

Pagination:
  type: object
  properties:
    limit:      { type: integer }
    nextCursor: { type: string, nullable: true }
```

- [ ] **Step 2: Add paths**

Under `paths`, add the 10 routes from Task 9's endpoint table with matching OperationIds (`adminListWorkspaces`, `adminGetWorkspace`, `adminListUsers`, `adminGetUser`, `adminListAudit`, `adminListJobs`, `adminRetryJob`, `adminResendEmail`, `adminGrantAdmin`, `adminRevokeAdmin`).

Each response envelope is:

```yaml
type: object
required: [data]
properties:
  data: { type: array, items: { $ref: '#/components/schemas/...' } }
  pagination: { $ref: '#/components/schemas/Pagination' }
```

401/403/404/409 error schemas reuse the existing `Error` schema from plan 1.

- [ ] **Step 3: Regenerate typed client**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm openapi:generate   # or whatever script plan 1 defined
```

Expected: `web/lib/api/schema.d.ts` picks up new admin types.

- [ ] **Step 4: Commit**

```bash
git add openapi/openapi.yaml web/lib/api/schema.d.ts
git commit -m "feat(admin): add /admin/* paths and schemas to OpenAPI"
```

---

## Task 12: Web — admin layout + navigation badge

**Spec:** §13 admin pages; "Admin" badge in user menu.

**Files:**
- Create: `web/app/admin/layout.tsx`
- Modify: existing top-bar user menu component (plan 1 landed `web/components/nav/user-menu.tsx` or similar) — add conditional "Admin" link
- Create: `web/lib/hooks/use-admin-guard.ts`

- [ ] **Step 1: Add `use-admin-guard` hook**

```tsx
// web/lib/hooks/use-admin-guard.ts
"use client";
import { redirect } from "next/navigation";
import { useIdentity } from "@/lib/hooks/use-identity";

/**
 * Client-side guard for admin-only pages. If the current user is not admin,
 * redirects to /404 (NOT /login — we don't want to advertise that /admin
 * exists). Returns the user once confirmed.
 */
export function useAdminGuard() {
  const { data, isLoading } = useIdentity();
  if (isLoading) return { user: null, isLoading: true } as const;
  if (!data?.user?.isAdmin) redirect("/not-found");
  return { user: data.user, isLoading: false } as const;
}
```

- [ ] **Step 2: Create `web/app/admin/layout.tsx`**

Read the folio-frontend-design skill first. Then create the layout:

```tsx
// web/app/admin/layout.tsx
"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useAdminGuard } from "@/lib/hooks/use-admin-guard";
import { Badge } from "@/components/ui/badge";

const NAV = [
  { href: "/admin/workspaces", label: "Workspaces" },
  { href: "/admin/users",   label: "Users" },
  { href: "/admin/audit",   label: "Audit" },
  { href: "/admin/jobs",    label: "Jobs" },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { isLoading } = useAdminGuard();
  const pathname = usePathname();
  if (isLoading) return null;

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b bg-card px-6 py-4 flex items-center gap-4">
        <h1 className="text-xl font-semibold">Folio Admin</h1>
        <Badge variant="destructive">Admin</Badge>
      </header>
      <div className="flex">
        <aside className="w-52 border-r min-h-[calc(100vh-4rem)] p-4">
          <nav className="flex flex-col gap-1">
            {NAV.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className={`px-3 py-2 rounded-md text-sm ${
                  pathname.startsWith(item.href) ? "bg-accent" : "hover:bg-accent/50"
                }`}
              >
                {item.label}
              </Link>
            ))}
          </nav>
        </aside>
        <main className="flex-1 p-6">{children}</main>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Conditional "Admin" link in user menu**

Edit plan 1's user-menu component. Add inside the dropdown:

```tsx
{user.isAdmin && (
  <>
    <DropdownMenuSeparator />
    <DropdownMenuItem asChild>
      <Link href="/admin/workspaces" className="flex items-center gap-2">
        <Badge variant="destructive" className="text-[10px] px-1.5 py-0">Admin</Badge>
        <span>Admin console</span>
      </Link>
    </DropdownMenuItem>
  </>
)}
```

Non-admins never see the item.

- [ ] **Step 4: Verify**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm lint
pnpm typecheck
pnpm build
```

Manually: sign in as a non-admin → no Admin link in menu; visiting `/admin/workspaces` directly → redirected by `useAdminGuard` to the 404 page. Sign in as admin → Admin link visible; navigation works.

- [ ] **Step 5: Commit**

```bash
git add web/app/admin/layout.tsx web/lib/hooks/use-admin-guard.ts web/components/nav/user-menu.tsx
git commit -m "feat(admin): add /admin layout + conditional user-menu link"
```

---

## Task 13: Web — workspaces list + detail page

**Files:**
- Create: `web/app/admin/workspaces/page.tsx`
- Create: `web/app/admin/workspaces/[workspaceId]/page.tsx`
- Create: `web/lib/admin/client.ts` (React Query hooks bundle)

- [ ] **Step 1: Add admin API client hooks**

`web/lib/admin/client.ts`:

```ts
import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api/client";
import type { paths } from "@/lib/api/schema";

type WorkspacesResp = paths["/api/v1/admin/workspaces"]["get"]["responses"]["200"]["content"]["application/json"];
type WorkspaceDetailResp = paths["/api/v1/admin/workspaces/{workspaceId}"]["get"]["responses"]["200"]["content"]["application/json"];

export function useAdminWorkspaces(params: { search?: string; includeDeleted?: boolean; cursor?: string }) {
  return useQuery({
    queryKey: ["admin", "workspaces", params],
    queryFn: () => apiGet<WorkspacesResp>("/api/v1/admin/workspaces", { params }),
  });
}

export function useAdminWorkspaceDetail(workspaceId: string) {
  return useQuery({
    queryKey: ["admin", "workspace", workspaceId],
    queryFn: () => apiGet<WorkspaceDetailResp>(`/api/v1/admin/workspaces/${workspaceId}`),
    enabled: !!workspaceId,
  });
}
```

Repeat the pattern for `useAdminUsers`, `useAdminUserDetail`, `useAdminAudit`, `useAdminJobs`, and mutations `useAdminGrantAdmin`, `useAdminRevokeAdmin`, `useAdminRetryJob`, `useAdminResendEmail` (these use plan 4's step-up-aware wrapper that auto-opens the re-auth modal on 403 reauth_required).

- [ ] **Step 2: Workspaces list page**

`web/app/admin/workspaces/page.tsx`: search input, include-deleted toggle, table with columns `Name | Slug | Created | Deleted | Members`. Row click → `/admin/workspaces/{id}`. Use `<Table>` from shadcn.

- [ ] **Step 3: Workspace detail page**

`web/app/admin/workspaces/[workspaceId]/page.tsx`: top section shows workspace meta (name, slug, base currency, cycle anchor, created/updated/deleted); "Deleted" banner if `deletedAt`. Sections: Members (count + link to `/admin/users?workspace={id}` — future enhancement), Last activity timestamp. **No financial data is fetched or rendered.**

- [ ] **Step 4: Verify + commit**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm lint && pnpm typecheck && pnpm build
```

```bash
git add web/app/admin/workspaces/ web/lib/admin/client.ts
git commit -m "feat(admin): add workspaces list + detail pages"
```

---

## Task 14: Web — users list + detail page

**Files:**
- Create: `web/app/admin/users/page.tsx`
- Create: `web/app/admin/users/[userId]/page.tsx`

- [ ] **Step 1: Users list page**

Search by email input, is-admin filter toggle. Table columns: `Email | Name | Admin | Created | Last login`. Row click → `/admin/users/{id}`.

- [ ] **Step 2: User detail page**

Sections:
- User card: email, display name, `emailVerifiedAt`, `isAdmin` badge, created/last login.
- Memberships table: workspace name, role, joined date.
- Active sessions: device / IP / last seen / revoke button (disabled — admins don't revoke user sessions; future plan).
- MFA summary: passkey count + labels, TOTP enabled yes/no, recovery codes remaining.
- **Grant / Revoke admin buttons** at the top-right of the user card:
  - Visible only when viewing another user (`user.id !== me.id`) OR when viewing self and `adminCount > 1`.
  - Button opens a confirmation dialog → calls `useAdminGrantAdmin` / `useAdminRevokeAdmin` mutation.
  - On 409 `last_admin`, show toast "Cannot revoke the last admin. Promote another user first."
  - On 403 `reauth_required`, plan 4's wrapper opens the re-auth modal and retries on success.

- [ ] **Step 3: Verify + commit**

```bash
pnpm lint && pnpm typecheck && pnpm build
```

```bash
git add web/app/admin/users/
git commit -m "feat(admin): add users list + detail with grant/revoke"
```

---

## Task 15: Web — audit + jobs pages

**Files:**
- Create: `web/app/admin/audit/page.tsx`
- Create: `web/app/admin/jobs/page.tsx`

- [ ] **Step 1: Audit feed page**

Filters in a top row: actor email autocomplete (hits `/api/v1/admin/users?search=`), workspace search, action substring, since/until date pickers. Default view: most recent 50 events, newest first. Each row shows `occurredAt | actor email | workspace slug or "—" | action | entityType/id`; click expands to show before/after JSON in a collapsible.

Support infinite-scroll via `useInfiniteQuery` using the `nextCursor` pagination envelope.

- [ ] **Step 2: Jobs page**

State tabs: `Running | Scheduled | Retryable | Discarded | Completed`. Per-row columns: `Kind | Queue | State | Scheduled | Attempted | Errors (badge count)`. "Retry" button on `Retryable` and `Discarded` rows → calls `useAdminRetryJob`; disabled while mutation in flight. Clicking a row opens a side panel with full `args` JSON.

"Resend" action is scoped to a future email-specific view; for now link `Kind = "email.send"` rows to a modal with a "Resend" button calling `useAdminResendEmail` with the job's email ID (parsed from `args.email_id`).

- [ ] **Step 3: Verify + commit**

```bash
pnpm lint && pnpm typecheck && pnpm build
```

```bash
git add web/app/admin/audit/ web/app/admin/jobs/
git commit -m "feat(admin): add audit feed + jobs queue views"
```

---

## Task 16: Final verification

**Files:** none modified.

- [ ] **Step 1: Backend builds, tests pass**

```bash
cd /Users/xmedavid/dev/folio/backend
go build ./...
go test ./internal/admin/... ./cmd/folio-admin/... -count=1 -race
```

Expected: exit 0.

- [ ] **Step 2: CLI smoke**

```bash
cd /Users/xmedavid/dev/folio
make admin-cli
./backend/bin/folio-admin list
./backend/bin/folio-admin grant app@davidsbatista.com  # if matching user exists
./backend/bin/folio-admin list     # shows 1 row
./backend/bin/folio-admin revoke app@davidsbatista.com
# Expected: error: cannot revoke the last admin (exit 1), because plan 1's
# bootstrap seeded admin is still the sole admin.
```

- [ ] **Step 3: HTTP smoke**

Start the API server. Sign in as a non-admin; confirm every `/api/v1/admin/*` route returns 404.

Sign in as admin (via env bootstrap on fresh DB). Curl:

```bash
# Should list workspaces
curl -s -b 'folio_session=<admin token>' -H 'X-Folio-Request: 1' \
  http://localhost:8080/api/v1/admin/workspaces | jq '.data | length'

# Grant without fresh reauth — expect 403 reauth_required
curl -s -b 'folio_session=<admin token>' -H 'X-Folio-Request: 1' -X POST \
  http://localhost:8080/api/v1/admin/users/<userId>/grant-admin
# {"error":"reauth required","code":"reauth_required"}

# Step up via plan 4's endpoint, retry — expect 200
curl -s -b 'folio_session=<admin token>' -H 'X-Folio-Request: 1' -X POST \
  -d '{"password":"<admin password>"}' -H 'Content-Type: application/json' \
  http://localhost:8080/api/v1/auth/reauth
curl -s -b 'folio_session=<admin token>' -H 'X-Folio-Request: 1' -X POST \
  http://localhost:8080/api/v1/admin/users/<userId>/grant-admin
```

- [ ] **Step 4: Audit feed populated**

```bash
psql "$DATABASE_URL" -c "
  select action, count(*) from audit_events
  where action like 'admin.%' group by action order by 1;
"
```

Expected: rows for `admin.granted`, `admin.revoked`, `admin.bootstrap_granted`, `admin.viewed_workspace`, `admin.viewed_user`, `admin.viewed_audit`, `admin.retried_job`, `admin.resent_email` as exercised.

- [ ] **Step 5: Web build**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm lint && pnpm typecheck && pnpm build
```

Expected: clean build. Manual walkthrough: `/admin` pages render as admin; non-admin session redirects from direct URL access; user-menu "Admin" link only appears for admins.

- [ ] **Step 6: No uncommitted changes**

```bash
cd /Users/xmedavid/dev/folio
git status
```

Expected: clean working tree.

---

## Self-review checklist

- [ ] Spec §11.1 CLI covered (Task 10: `grant` / `revoke` / `list` with cobra).
- [ ] Spec §11.1 `ADMIN_BOOTSTRAP_EMAIL` covered (Task 1: `EnsureBootstrapAdmin` wired into plan 1's `AdminBootstrapHook`).
- [ ] Spec §11.1 last-admin guard covered in both CLI (Task 10) and HTTP service/handler (Tasks 8 and 9).
- [ ] Spec §11.2 all 10 routes mounted with the correct middleware chain (Task 9).
- [ ] Spec §11.2 `RequireAdmin` returns 404, not 403 (Task 2; asserted by Task 9 tests).
- [ ] Spec §11.3 audit on every admin action, reads and writes — emitted by service-layer helpers (Tasks 4, 5, 6, 7 for reads; Task 8 for writes); verified in Task 16 step 4. All 8 `admin.*` actions from §8.3 exercised (`granted`, `revoked`, `bootstrap_granted`, `viewed_user`, `viewed_workspace`, `viewed_audit`, `retried_job`, `resent_email`).
- [ ] Spec §13 web: `/admin/layout.tsx` (Task 12); `/admin/workspaces` + detail (Task 13); `/admin/users` + detail (Task 14); `/admin/audit` + `/admin/jobs` (Task 15); conditional Admin link in user menu (Task 12).
- [ ] Workspace detail returns metadata only — no financial data joins (Task 4; asserted in test).
- [ ] OpenAPI updated with admin paths and schemas (Task 11).
- [ ] Makefile gains `admin-cli` target (Task 10 step 4).
- [ ] `.env.example` documents `ADMIN_BOOTSTRAP_EMAIL` (Setup §0.3).
- [ ] No placeholders left in this plan.

---

## Decisions resolved inline

1. **`actor_user_id` for CLI-initiated admin actions:** `NULL`. Parallel to system-initiated audit rows. Downstream UIs render as "System (CLI)".
2. **Cursor shape:** opaque base64(JSON) — details hidden from the client; server decides the sort key per endpoint (e.g. `occurred_at desc, id desc` for audit).
3. **Response envelope:** `{data, pagination}`. Singular detail endpoints return `{data: {...}}` for shape consistency.
4. **`GrantAdmin` idempotency:** no-op + no audit row when already admin. Prevents audit noise from retry storms.
5. **Self-revoke:** allowed as long as another admin exists. The last-admin guard is count-based, not identity-based. UI hides the button when viewing self *only if* `adminCount == 1`.
6. **Audit on reads is synchronous, same transaction as the read.** Keeps the read + audit atomic; an admin who reads but fails midway still produces the audit row for attempted access.

## Gaps deferred

1. **Admin impersonation** ("view this workspace as a member") — spec §2 non-goal; not in this plan. Listed as a future spec.
2. **Revoking user sessions from the admin UI** — the user-detail page shows sessions read-only. A future plan adds the revoke mutation.
3. **Bulk actions** on audit/jobs (export, bulk retry) — not in scope; single-row actions only.
4. **Admin audit-feed export** (CSV / ndjson download) — spec §14 lists `Org-level audit exports` as open. This plan's `/admin/audit` is browse-only.
5. **Rate limiting for admin routes** — admins are trusted; plan 1's per-user `60/min` limit still applies. A dedicated admin rate-limit bucket is deferred.
