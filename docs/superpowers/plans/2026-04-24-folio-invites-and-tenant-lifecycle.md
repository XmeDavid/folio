# Folio Invites & Tenant Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land tenant administration — settings edits, soft-delete/restore, member role changes, leave/remove, invite create/revoke/accept — plus the `/accept-invite` flow, the soft-delete sweeper, and the `mailer.Mailer` interface used by all transactional email from plan 3 onwards.

**Architecture:** Extends `backend/internal/identity` with tenant-lifecycle and member-management methods, adds an `InviteService` for the invite lifecycle, and introduces `backend/internal/mailer` (interface + log-only stub). Tenant-scoped routes mount under `/api/v1/t/{tenantId}` behind the middleware chain from plan 1 (`RequireSession` → `RequireMembership` → `RequireRole` → `RequireFreshReauth`). Soft-delete cleanup ships as a standalone `backend/cmd/folio-sweeper` binary that plan 3 re-uses inside a River periodic job.

**Tech Stack:** Go 1.25, pgx/v5, chi v5, go-chi routing; Next.js 16, React Query, Tailwind.

**Spec:** `docs/superpowers/specs/2026-04-24-folio-auth-and-tenancy-design.md` — §3.4 invariants, §4.2 invite routes, §4.4 tenant-scoped routes, §8.3 audit events, §10 soft-delete sweeper, §13 web pages.

**Prior plans in series:** `docs/superpowers/plans/2026-04-24-folio-auth-foundation.md` (plan 1) — ships schema, `auth.Service`, `auth.RequireSession`, `auth.RequireMembership`, `auth.RequireRole`, `auth.RequireFreshReauth` (stub), `auth.UserFromCtx`, `auth.TenantFromCtx`, `auth.RoleFromCtx`, signup/login/logout, `GET /me`, `POST /tenants`, `GET /t/{id}/members` (list-only), and the frontend shell (login/signup/switcher/`/t/[slug]`).

---

## 0. Setup and shared patterns

### 0.1 Working directory and reset

All `go`, `atlas`, and `sqlc` commands run in `backend/`:

```bash
cd /Users/xmedavid/dev/folio/backend
```

Postgres must be running (`docker compose -f docker-compose.dev.yml up -d` from the repo root). `DATABASE_URL`, `SESSION_COOKIE_NAME`, `APP_URL`, and the plan-1 env vars (`PASSWORD_ARGON2_*`, `SECRET_ENCRYPTION_KEY`, `REGISTRATION_MODE`) must be set — see `.env.example`.

Frontend commands run in `web/`:

```bash
cd /Users/xmedavid/dev/folio/web
```

### 0.2 Conventions

- **Naming.** Go packages: `backend/internal/identity` (extended), `backend/internal/mailer` (new). Frontend paths: `web/app/t/[slug]/settings/{tenant,members,invites}/page.tsx`, `web/app/accept-invite/[token]/page.tsx`. See canonical list in the plan-series overview.
- **Commit style.** Conventional Commits. Feature scopes: `feat(tenants): …`, `feat(invites): …`, `feat(mailer): …`, `feat(jobs): …`, `feat(web): …`, `test(…): …`.
- **TDD.** Every task writes service-layer tests first (`_test.go` alongside the implementation file), then the handler, then (where applicable) the frontend page.
- **Tenant-scope rule.** Every service method takes `tenantID uuid.UUID` as the first argument after `ctx`. Handlers read tenant and user from `auth.TenantFromCtx` / `auth.UserFromCtx`, never from the request body.
- **RequireFreshReauth caveat.** Plan 1 ships a stub: the middleware always returns `403 { code: "reauth_required" }` unless `sessions.reauth_at > now() - window`. Plan 4 adds the re-auth flow that bumps that column. **Until plan 4 lands, routes gated by `RequireFreshReauth` are mounted but unusable from the UI.** Tests in this plan call `testdb.SetSessionReauth(t, pool, sessionID, time.Now())` to simulate fresh re-auth; that helper is introduced in §0.4.

### 0.3 Mailer interface (defined in this plan, implemented in plan 3)

`backend/internal/mailer/mailer.go`:

```go
// Package mailer defines the transactional email transport used by Folio.
//
// Plan 2 ships the interface + a LogMailer stub. Plan 3 replaces the stub
// with a Resend-backed implementation driven by River jobs. Plan 2's
// invite handler calls Send directly; plan 3 rewires it to enqueue.
package mailer

import "context"

// Message is the wire shape for a transactional email.
type Message struct {
    To       string            // primary recipient (lowercase, normalized)
    Subject  string
    Template string            // template name; mailer looks it up
    Data     map[string]any    // template data
    TenantID string            // optional — for audit / inbound routing
}

// Mailer sends transactional email. Implementations MUST be safe for
// concurrent use; they MAY batch, retry, or buffer internally.
type Mailer interface {
    Send(ctx context.Context, msg Message) error
}
```

`backend/internal/mailer/stub.go`:

```go
package mailer

import (
    "context"
    "log/slog"
    "sync"
)

// LogMailer records every message in memory and logs a one-line summary.
// Use it in dev, CI, and tests. Plan 3 replaces the default in prod with
// ResendMailer but keeps LogMailer wired in tests.
type LogMailer struct {
    Logger *slog.Logger
    mu     sync.Mutex
    sent   []Message
}

func NewLogMailer(l *slog.Logger) *LogMailer { return &LogMailer{Logger: l} }

func (m *LogMailer) Send(_ context.Context, msg Message) error {
    m.mu.Lock()
    m.sent = append(m.sent, msg)
    m.mu.Unlock()
    if m.Logger != nil {
        m.Logger.Info("mailer.send (stub)",
            "to", msg.To, "template", msg.Template, "subject", msg.Subject)
    }
    return nil
}

// Sent returns a snapshot of every message this mailer has recorded.
func (m *LogMailer) Sent() []Message {
    m.mu.Lock()
    defer m.mu.Unlock()
    out := make([]Message, len(m.sent))
    copy(out, m.sent)
    return out
}

// Reset clears the recorded slice. Useful between test cases.
func (m *LogMailer) Reset() {
    m.mu.Lock()
    m.sent = nil
    m.mu.Unlock()
}
```

### 0.4 Test helpers — `backend/internal/testdb/fixtures.go`

Plan 1 introduces `backend/internal/testdb` with a pool factory. This plan adds `fixtures.go` alongside it. Skeleton (real code — extend per use in later tasks):

```go
package testdb

import (
    "context"
    "crypto/sha256"
    "encoding/base64"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/xmedavid/folio/backend/internal/uuidx"
)

// CreateTestTenant inserts a tenant row and returns it. name is used both
// for the display name and as the slug (via slugify).
func CreateTestTenant(t *testing.T, pool *pgxpool.Pool, name string) (id uuid.UUID, slug string) {
    t.Helper()
    id = uuidx.New()
    slug = Slugify(name)
    _, err := pool.Exec(context.Background(), `
        insert into tenants (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
        values ($1, $2, $3, 'CHF', 1, 'en', 'UTC')
    `, id, name, slug)
    if err != nil {
        t.Fatalf("CreateTestTenant: %v", err)
    }
    return id, slug
}

// CreateTestUser inserts a user row with a bcrypt-stubbed password hash and
// optional email_verified_at. Returns the user id.
func CreateTestUser(t *testing.T, pool *pgxpool.Pool, email string, verified bool) uuid.UUID {
    t.Helper()
    id := uuidx.New()
    var verifiedAt any
    if verified {
        verifiedAt = time.Now()
    }
    _, err := pool.Exec(context.Background(), `
        insert into users (id, email, display_name, password_hash, email_verified_at)
        values ($1, $2, $2, '$argon2id$stub', $3)
    `, id, email, verifiedAt)
    if err != nil {
        t.Fatalf("CreateTestUser: %v", err)
    }
    return id
}

// CreateTestMembership inserts a tenant_memberships row with the given role.
func CreateTestMembership(t *testing.T, pool *pgxpool.Pool, tenantID, userID uuid.UUID, role string) {
    t.Helper()
    _, err := pool.Exec(context.Background(), `
        insert into tenant_memberships (tenant_id, user_id, role) values ($1, $2, $3::tenant_role)
    `, tenantID, userID, role)
    if err != nil {
        t.Fatalf("CreateTestMembership: %v", err)
    }
}

// SetSessionReauth bumps sessions.reauth_at to ts, simulating completed
// step-up re-auth. Used in tests that exercise RequireFreshReauth-gated
// routes before plan 4's re-auth flow exists.
func SetSessionReauth(t *testing.T, pool *pgxpool.Pool, sessionID string, ts time.Time) {
    t.Helper()
    _, err := pool.Exec(context.Background(),
        `update sessions set reauth_at = $1 where id = $2`,
        ts, sessionID)
    if err != nil {
        t.Fatalf("SetSessionReauth: %v", err)
    }
}

// HashInviteToken matches the production hashing rule: SHA-256 over the
// base64url plaintext, stored raw in tenant_invites.token_hash.
func HashInviteToken(plaintext string) []byte {
    h := sha256.Sum256([]byte(plaintext))
    return h[:]
}

// Base64URL encodes b with no padding (matches crypto/rand invite tokens).
func Base64URL(b []byte) string {
    return base64.RawURLEncoding.EncodeToString(b)
}

// Slugify lowercases, replaces non-alphanumeric runs with '-', trims edges.
// Must match identity.slugify (wire it identically in Task 1).
func Slugify(name string) string { /* inline duplicate of identity.slugify */ }
```

The `Slugify` body is kept identical to the production helper in Task 1 so tests and production agree. After Task 1 lands, this helper imports the production function directly.

### 0.5 Per-task verification baseline

Every task with Go changes ends with the same checks:

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./...
go vet ./...
go build ./...
```

Every task with web changes also runs:

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm build
```

Expected: all commands exit 0. Tests added by the task pass; no new vet warnings.

---

## Task 1: Extend identity.Service with tenant-update / soft-delete / restore

**Spec:** §4.4 (`PATCH /api/v1/t/{id}`, `DELETE`, `POST …/restore`), §3.4 invariant #4 (soft-deleted tenants invisible), §8.3 (`tenant.settings_changed`, `tenant.deleted`, `tenant.restored`).

**Files:**
- Modify: `backend/internal/identity/service.go`
- Create: `backend/internal/identity/tenants_test.go`

- [ ] **Step 1: Define the `UpdateTenantInput` shape and validation**

Prepend to `service.go`:

```go
// UpdateTenantInput is the PATCH body for a tenant settings update. Pointer
// fields mean "absent"; a non-nil pointer to the zero value means "clear".
type UpdateTenantInput struct {
    Name           *string
    Slug           *string
    BaseCurrency   *string
    CycleAnchorDay *int
    Locale         *string
    Timezone       *string
}

// normalize validates provided fields and canonicalises strings. Pure.
func (in UpdateTenantInput) normalize() (UpdateTenantInput, error) {
    if in.Name != nil {
        n := strings.TrimSpace(*in.Name)
        if n == "" {
            return in, httpx.NewValidationError("name cannot be empty")
        }
        in.Name = &n
    }
    if in.Slug != nil {
        s := strings.ToLower(strings.TrimSpace(*in.Slug))
        if !slugPattern.MatchString(s) {
            return in, httpx.NewValidationError("slug must match ^[a-z0-9][a-z0-9-]{1,62}$")
        }
        in.Slug = &s
    }
    if in.BaseCurrency != nil {
        cur, err := money.ParseCurrency(*in.BaseCurrency)
        if err != nil {
            return in, httpx.NewValidationError(err.Error())
        }
        s := string(cur)
        in.BaseCurrency = &s
    }
    if in.CycleAnchorDay != nil {
        d := *in.CycleAnchorDay
        if d < 1 || d > 31 {
            return in, httpx.NewValidationError("cycleAnchorDay must be between 1 and 31")
        }
        in.CycleAnchorDay = &d
    }
    if in.Locale != nil {
        l := strings.TrimSpace(*in.Locale)
        if l == "" {
            return in, httpx.NewValidationError("locale cannot be empty")
        }
        in.Locale = &l
    }
    if in.Timezone != nil {
        tz := strings.TrimSpace(*in.Timezone)
        if tz == "" {
            return in, httpx.NewValidationError("timezone cannot be empty")
        }
        in.Timezone = &tz
    }
    return in, nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
```

Add the `regexp` import.

- [ ] **Step 2: Write `tenants_test.go` (red)**

```go
package identity_test

import (
    "context"
    "testing"
    "time"

    "github.com/xmedavid/folio/backend/internal/identity"
    "github.com/xmedavid/folio/backend/internal/testdb"
)

func TestService_UpdateTenant_RenamesAndChangesSlug(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")

    newName := "Alice Home"
    newSlug := "alice-home"
    updated, err := svc.UpdateTenant(context.Background(), tenantID, identity.UpdateTenantInput{
        Name: &newName,
        Slug: &newSlug,
    })
    if err != nil {
        t.Fatalf("UpdateTenant: %v", err)
    }
    if updated.Name != newName || updated.Slug != newSlug {
        t.Fatalf("tenant not updated: %+v", updated)
    }
}

func TestService_UpdateTenant_RejectsBadSlug(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")

    bad := "Not a Slug!"
    _, err := svc.UpdateTenant(context.Background(), tenantID, identity.UpdateTenantInput{Slug: &bad})
    if err == nil {
        t.Fatal("expected validation error for bad slug")
    }
}

func TestService_UpdateTenant_SlugCollision(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    _, existingSlug := testdb.CreateTestTenant(t, pool, "Shared")
    targetID, _ := testdb.CreateTestTenant(t, pool, "Other")

    _, err := svc.UpdateTenant(context.Background(), targetID, identity.UpdateTenantInput{Slug: &existingSlug})
    if err == nil {
        t.Fatal("expected slug-collision error")
    }
}

func TestService_SoftDeleteAndRestoreTenant(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")

    if err := svc.SoftDeleteTenant(context.Background(), tenantID); err != nil {
        t.Fatalf("SoftDeleteTenant: %v", err)
    }

    // Reads must filter deleted_at.
    if _, err := svc.GetTenant(context.Background(), tenantID); err == nil {
        t.Fatal("expected GetTenant to miss soft-deleted tenant")
    }

    if err := svc.RestoreTenant(context.Background(), tenantID); err != nil {
        t.Fatalf("RestoreTenant: %v", err)
    }
    got, err := svc.GetTenant(context.Background(), tenantID)
    if err != nil {
        t.Fatalf("GetTenant after restore: %v", err)
    }
    if got.DeletedAt != nil {
        t.Fatalf("expected deleted_at cleared, got %v", got.DeletedAt)
    }
    _ = time.Now()
}
```

Run `go test ./internal/identity/...` — all four fail (no `UpdateTenant`, `SoftDeleteTenant`, `RestoreTenant`, `GetTenant` yet).

- [ ] **Step 3: Implement the four methods (green)**

Add to `backend/internal/identity/service.go`:

```go
// Tenant is the read model. Extend it here: add DeletedAt and Slug.
// (Plan 1 already added Slug to Tenant; add DeletedAt now.)
// type Tenant struct {
//     ...
//     Slug      string     `json:"slug"`
//     DeletedAt *time.Time `json:"deletedAt,omitempty"`
// }

// GetTenant returns a tenant by id, skipping soft-deleted rows.
func (s *Service) GetTenant(ctx context.Context, tenantID uuid.UUID) (*Tenant, error) {
    var t Tenant
    err := s.pool.QueryRow(ctx, `
        select id, name, slug, base_currency, cycle_anchor_day, locale, timezone,
               created_at, deleted_at
        from tenants where id = $1 and deleted_at is null
    `, tenantID).Scan(
        &t.ID, &t.Name, &t.Slug, &t.BaseCurrency, &t.CycleAnchorDay,
        &t.Locale, &t.Timezone, &t.CreatedAt, &t.DeletedAt,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, httpx.NewNotFoundError("tenant")
        }
        return nil, err
    }
    return &t, nil
}

// UpdateTenant applies the PATCH and returns the updated tenant. Soft-deleted
// tenants are NOT updatable — callers should restore first.
func (s *Service) UpdateTenant(ctx context.Context, tenantID uuid.UUID, raw UpdateTenantInput) (*Tenant, error) {
    in, err := raw.normalize()
    if err != nil {
        return nil, err
    }

    sets := make([]string, 0, 6)
    args := make([]any, 0, 8)
    args = append(args, tenantID) // $1 in WHERE

    next := func(val any) string {
        args = append(args, val)
        return fmt.Sprintf("$%d", len(args))
    }

    if in.Name != nil {
        sets = append(sets, "name = "+next(*in.Name))
    }
    if in.Slug != nil {
        sets = append(sets, "slug = "+next(*in.Slug))
    }
    if in.BaseCurrency != nil {
        sets = append(sets, "base_currency = "+next(*in.BaseCurrency))
    }
    if in.CycleAnchorDay != nil {
        sets = append(sets, "cycle_anchor_day = "+next(*in.CycleAnchorDay))
    }
    if in.Locale != nil {
        sets = append(sets, "locale = "+next(*in.Locale))
    }
    if in.Timezone != nil {
        sets = append(sets, "timezone = "+next(*in.Timezone))
    }

    if len(sets) == 0 {
        return s.GetTenant(ctx, tenantID)
    }

    q := fmt.Sprintf(
        `update tenants set %s where id = $1 and deleted_at is null returning id`,
        strings.Join(sets, ", "),
    )
    var gotID uuid.UUID
    err = s.pool.QueryRow(ctx, q, args...).Scan(&gotID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, httpx.NewNotFoundError("tenant")
        }
        // Slug collision: unique index violation.
        if isUniqueViolation(err, "tenants_slug_key") {
            return nil, httpx.NewValidationError("slug is already in use")
        }
        return nil, fmt.Errorf("update tenant: %w", err)
    }
    return s.GetTenant(ctx, tenantID)
}

// SoftDeleteTenant sets deleted_at = now(). A tenant cannot be soft-deleted
// if it is already deleted (idempotent — returns the existing timestamp).
func (s *Service) SoftDeleteTenant(ctx context.Context, tenantID uuid.UUID) error {
    ct, err := s.pool.Exec(ctx,
        `update tenants set deleted_at = coalesce(deleted_at, now()) where id = $1`, tenantID)
    if err != nil {
        return fmt.Errorf("soft-delete tenant: %w", err)
    }
    if ct.RowsAffected() == 0 {
        return httpx.NewNotFoundError("tenant")
    }
    return nil
}

// RestoreTenant clears deleted_at. Idempotent — restoring a non-deleted
// tenant is a no-op (not an error).
func (s *Service) RestoreTenant(ctx context.Context, tenantID uuid.UUID) error {
    ct, err := s.pool.Exec(ctx,
        `update tenants set deleted_at = null where id = $1`, tenantID)
    if err != nil {
        return fmt.Errorf("restore tenant: %w", err)
    }
    if ct.RowsAffected() == 0 {
        return httpx.NewNotFoundError("tenant")
    }
    return nil
}
```

Add a small helper for the unique-violation check (PGSQL error code `23505`):

```go
import "github.com/jackc/pgx/v5/pgconn"

func isUniqueViolation(err error, constraint string) bool {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23505" {
        if constraint == "" {
            return true
        }
        return pgErr.ConstraintName == constraint
    }
    return false
}
```

- [ ] **Step 4: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... -run 'TestService_Update|TestService_Soft'
go vet ./...
go build ./...
```

Expected: all four tests pass.

- [ ] **Step 5: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/service.go backend/internal/identity/tenants_test.go
git commit -m "$(cat <<'EOF'
feat(tenants): add UpdateTenant / SoftDelete / Restore

Extends identity.Service with settings-update, soft-delete, and restore
methods. Soft-deleted tenants are filtered from reads; restore is
idempotent. Slug uniqueness surfaces as a validation error.
EOF
)"
```

---

## Task 2: Extend identity test fixtures and slugify helper

**Spec:** §0.4 above; needed by Tasks 3 onwards.

**Files:**
- Modify: `backend/internal/identity/service.go` — export `Slugify`
- Create: `backend/internal/testdb/fixtures.go`

- [ ] **Step 1: Export `identity.Slugify`**

Plan 1 introduces an internal `slugify` helper used by `CreateTenant`. Rename it to the exported `Slugify` so tests can reuse it:

```go
// Slugify lowercases name, replaces non-alphanumeric runs with '-',
// and trims leading/trailing '-'. Guaranteed to satisfy slugPattern.
func Slugify(name string) string {
    var b strings.Builder
    prevDash := false
    for _, r := range strings.ToLower(strings.TrimSpace(name)) {
        switch {
        case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
            b.WriteRune(r)
            prevDash = false
        default:
            if !prevDash && b.Len() > 0 {
                b.WriteByte('-')
                prevDash = true
            }
        }
    }
    s := strings.TrimRight(b.String(), "-")
    if s == "" || !slugPattern.MatchString(s) {
        return "t"
    }
    return s
}
```

Update every call site in plan 1's code from `slugify(...)` to `Slugify(...)`.

- [ ] **Step 2: Write `backend/internal/testdb/fixtures.go`**

Use the skeleton in §0.4 verbatim, importing `identity.Slugify` rather than duplicating:

```go
package testdb

import (
    "context"
    "crypto/sha256"
    "encoding/base64"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/xmedavid/folio/backend/internal/identity"
    "github.com/xmedavid/folio/backend/internal/uuidx"
)

func CreateTestTenant(t *testing.T, pool *pgxpool.Pool, name string) (uuid.UUID, string) {
    t.Helper()
    id := uuidx.New()
    slug := identity.Slugify(name)
    _, err := pool.Exec(context.Background(), `
        insert into tenants (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
        values ($1, $2, $3, 'CHF', 1, 'en', 'UTC')
    `, id, name, slug)
    if err != nil {
        t.Fatalf("CreateTestTenant: %v", err)
    }
    return id, slug
}

func CreateTestUser(t *testing.T, pool *pgxpool.Pool, email string, verified bool) uuid.UUID {
    t.Helper()
    id := uuidx.New()
    var verifiedAt any
    if verified {
        verifiedAt = time.Now()
    }
    _, err := pool.Exec(context.Background(), `
        insert into users (id, email, display_name, password_hash, email_verified_at)
        values ($1, $2, $2, '$argon2id$stub', $3)
    `, id, email, verifiedAt)
    if err != nil {
        t.Fatalf("CreateTestUser: %v", err)
    }
    return id
}

func CreateTestMembership(t *testing.T, pool *pgxpool.Pool, tenantID, userID uuid.UUID, role string) {
    t.Helper()
    _, err := pool.Exec(context.Background(), `
        insert into tenant_memberships (tenant_id, user_id, role) values ($1, $2, $3::tenant_role)
    `, tenantID, userID, role)
    if err != nil {
        t.Fatalf("CreateTestMembership: %v", err)
    }
}

func CreateTestSession(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) (sessionID, plaintextToken string) {
    t.Helper()
    plaintextToken = Base64URL(RandomBytes(32))
    sessionID = HashSessionToken(plaintextToken)
    _, err := pool.Exec(context.Background(), `
        insert into sessions (id, user_id, expires_at, last_seen_at)
        values ($1, $2, now() + interval '90 days', now())
    `, sessionID, userID)
    if err != nil {
        t.Fatalf("CreateTestSession: %v", err)
    }
    return sessionID, plaintextToken
}

func SetSessionReauth(t *testing.T, pool *pgxpool.Pool, sessionID string, ts time.Time) {
    t.Helper()
    _, err := pool.Exec(context.Background(),
        `update sessions set reauth_at = $1 where id = $2`, ts, sessionID)
    if err != nil {
        t.Fatalf("SetSessionReauth: %v", err)
    }
}

func HashInviteToken(plaintext string) []byte {
    h := sha256.Sum256([]byte(plaintext))
    return h[:]
}

func HashSessionToken(plaintext string) string {
    h := sha256.Sum256([]byte(plaintext))
    return base64.RawURLEncoding.EncodeToString(h[:])
}

func Base64URL(b []byte) string {
    return base64.RawURLEncoding.EncodeToString(b)
}

func RandomBytes(n int) []byte {
    b := make([]byte, n)
    if _, err := cryptoRandRead(b); err != nil {
        panic(err)
    }
    return b
}
```

Add the `cryptoRandRead` thin wrapper to avoid `crypto/rand` import cycles in `package testdb`:

```go
// fixtures_rand.go
package testdb

import cr "crypto/rand"

func cryptoRandRead(b []byte) (int, error) { return cr.Read(b) }
```

- [ ] **Step 3: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... -run 'TestService_Update|TestService_Soft'
go vet ./...
go build ./...
```

Expected: identity tests still green; `go build ./internal/testdb/...` succeeds.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/service.go backend/internal/testdb/
git commit -m "$(cat <<'EOF'
test: add shared testdb fixtures for tenants, users, sessions

Introduces CreateTestTenant / CreateTestUser / CreateTestMembership /
CreateTestSession and the SetSessionReauth helper that simulates
completed step-up re-auth until plan 4 lands.
EOF
)"
```

---

## Task 3: Ship the mailer package (interface + log stub)

**Spec:** §7 (Resend is plan 3's job; the interface lands now so plan 2's invite handler can depend on it).

**Files:**
- Create: `backend/internal/mailer/mailer.go`
- Create: `backend/internal/mailer/stub.go`
- Create: `backend/internal/mailer/stub_test.go`

- [ ] **Step 1: Write `mailer.go` and `stub.go`**

Use the code in §0.3 verbatim.

- [ ] **Step 2: Write `stub_test.go` (red/green together — pure in-memory)**

```go
package mailer_test

import (
    "context"
    "testing"

    "github.com/xmedavid/folio/backend/internal/mailer"
)

func TestLogMailer_RecordsSentMessages(t *testing.T) {
    m := mailer.NewLogMailer(nil)
    msg := mailer.Message{
        To:       "alice@example.com",
        Subject:  "Hello",
        Template: "test",
        Data:     map[string]any{"x": 1},
    }
    if err := m.Send(context.Background(), msg); err != nil {
        t.Fatalf("Send: %v", err)
    }

    got := m.Sent()
    if len(got) != 1 {
        t.Fatalf("want 1 message, got %d", len(got))
    }
    if got[0].To != msg.To || got[0].Template != msg.Template {
        t.Fatalf("recorded message mismatch: %+v", got[0])
    }

    m.Reset()
    if len(m.Sent()) != 0 {
        t.Fatal("Reset did not clear sent slice")
    }
}
```

- [ ] **Step 3: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/mailer/...
go vet ./...
go build ./...
```

Expected: pass.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/mailer/
git commit -m "$(cat <<'EOF'
feat(mailer): add Mailer interface and in-memory LogMailer stub

Defines the transport contract every transactional email will use.
Plan 3 replaces the default with ResendMailer and keeps LogMailer
wired in tests.
EOF
)"
```

---

## Task 4: Wire `mailer.LogMailer` into router deps

**Spec:** §7 (every transactional email goes through `mailer.Mailer`); needed for Task 9.

**Files:**
- Modify: `backend/internal/http/router.go`
- Modify: `backend/cmd/folio/main.go` (or wherever `http.Deps` is constructed — see plan 1)

- [ ] **Step 1: Extend `http.Deps`**

```go
type Deps struct {
    Logger *slog.Logger
    DB     *pgxpool.Pool
    Cfg    *config.Config
    Mailer mailer.Mailer
}
```

Thread it through `NewRouter`; plan 5's handlers will accept it.

- [ ] **Step 2: Wire `LogMailer` in `main.go`**

```go
var m mailer.Mailer = mailer.NewLogMailer(logger)
// plan 3 replaces with:
// m = mailer.NewResendMailer(cfg.ResendAPIKey, logger)
deps := http.Deps{Logger: logger, DB: pool, Cfg: cfg, Mailer: m}
```

- [ ] **Step 3: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go build ./...
go vet ./...
```

Expected: pass; no tests changed.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/http/router.go backend/cmd/folio/
git commit -m "$(cat <<'EOF'
feat(mailer): wire LogMailer through http.Deps

Makes the mailer available to every handler via the dependency struct.
Plan 3 swaps the implementation to ResendMailer without touching the
handler signatures.
EOF
)"
```

---

## Task 5: identity.Service member management — ChangeRole / RemoveMember / LeaveTenant

**Spec:** §4.4 member routes; §3.4 invariants #1 (last-owner guard) and #2 (can't leave last tenant); §8.3 (`member.role_changed`, `member.removed`, `member.left`).

**Files:**
- Modify: `backend/internal/identity/service.go`
- Create: `backend/internal/identity/members_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package identity_test

import (
    "context"
    "errors"
    "testing"

    "github.com/xmedavid/folio/backend/internal/identity"
    "github.com/xmedavid/folio/backend/internal/testdb"
)

func TestService_ChangeRole_DemotionBlockedOnLastOwner(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    userID := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, userID, "owner")

    err := svc.ChangeRole(context.Background(), tenantID, userID, identity.RoleMember)
    if !errors.Is(err, identity.ErrLastOwner) {
        t.Fatalf("expected ErrLastOwner, got %v", err)
    }
}

func TestService_ChangeRole_AllowsDemotionWhenOtherOwnerPresent(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    a := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    b := testdb.CreateTestUser(t, pool, "bob@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, a, "owner")
    testdb.CreateTestMembership(t, pool, tenantID, b, "owner")

    if err := svc.ChangeRole(context.Background(), tenantID, a, identity.RoleMember); err != nil {
        t.Fatalf("ChangeRole: %v", err)
    }
}

func TestService_RemoveMember_BlockedOnLastOwner(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    a := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, a, "owner")

    err := svc.RemoveMember(context.Background(), tenantID, a)
    if !errors.Is(err, identity.ErrLastOwner) {
        t.Fatalf("expected ErrLastOwner, got %v", err)
    }
}

func TestService_LeaveTenant_BlockedWhenOnlyMembership(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    a := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, a, "member")

    err := svc.LeaveTenant(context.Background(), tenantID, a)
    if !errors.Is(err, identity.ErrLastTenant) {
        t.Fatalf("expected ErrLastTenant, got %v", err)
    }
}

func TestService_LeaveTenant_Succeeds_WhenOtherTenantExists(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    t1, _ := testdb.CreateTestTenant(t, pool, "Personal")
    t2, _ := testdb.CreateTestTenant(t, pool, "Household")
    a := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, t1, a, "member")
    testdb.CreateTestMembership(t, pool, t2, a, "owner")

    if err := svc.LeaveTenant(context.Background(), t1, a); err != nil {
        t.Fatalf("LeaveTenant: %v", err)
    }
}
```

- [ ] **Step 2: Implement the three methods (green)**

```go
// Role is the per-membership role enum.
type Role string

const (
    RoleOwner  Role = "owner"
    RoleMember Role = "member"
)

// Membership is the read-model row returned by ListMembers.
type Membership struct {
    TenantID  uuid.UUID `json:"tenantId"`
    UserID    uuid.UUID `json:"userId"`
    Email     string    `json:"email"`
    DisplayName string  `json:"displayName"`
    Role      Role      `json:"role"`
    CreatedAt time.Time `json:"createdAt"`
}

// Sentinel errors surfaced by the member-management methods.
var (
    ErrLastOwner     = errors.New("identity: operation would leave tenant without an owner")
    ErrLastTenant    = errors.New("identity: user cannot leave their last tenant")
    ErrNotAMember    = errors.New("identity: user is not a member of the tenant")
)

// ChangeRole updates (tenantID, userID)'s role. Blocks demotion that would
// remove the last owner.
func (s *Service) ChangeRole(ctx context.Context, tenantID, userID uuid.UUID, newRole Role) error {
    if newRole != RoleOwner && newRole != RoleMember {
        return httpx.NewValidationError("role must be 'owner' or 'member'")
    }

    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return fmt.Errorf("begin: %w", err)
    }
    defer func() { _ = tx.Rollback(ctx) }()

    // Lock the membership row to serialise concurrent role changes.
    var current Role
    err = tx.QueryRow(ctx, `
        select role from tenant_memberships
        where tenant_id = $1 and user_id = $2 for update
    `, tenantID, userID).Scan(&current)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return ErrNotAMember
        }
        return fmt.Errorf("lock membership: %w", err)
    }
    if current == newRole {
        return tx.Commit(ctx) // no-op
    }

    // Demoting the last owner is forbidden.
    if current == RoleOwner && newRole == RoleMember {
        var ownerCount int
        if err := tx.QueryRow(ctx, `
            select count(*) from tenant_memberships
            where tenant_id = $1 and role = 'owner'
        `, tenantID).Scan(&ownerCount); err != nil {
            return fmt.Errorf("count owners: %w", err)
        }
        if ownerCount <= 1 {
            return ErrLastOwner
        }
    }

    if _, err := tx.Exec(ctx, `
        update tenant_memberships set role = $3, updated_at = now()
        where tenant_id = $1 and user_id = $2
    `, tenantID, userID, newRole); err != nil {
        return fmt.Errorf("update role: %w", err)
    }
    return tx.Commit(ctx)
}

// RemoveMember deletes the (tenantID, userID) membership. Blocks removing
// the last owner. Does NOT revoke the user's sessions (§6.3).
func (s *Service) RemoveMember(ctx context.Context, tenantID, userID uuid.UUID) error {
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return fmt.Errorf("begin: %w", err)
    }
    defer func() { _ = tx.Rollback(ctx) }()

    var role Role
    err = tx.QueryRow(ctx, `
        select role from tenant_memberships
        where tenant_id = $1 and user_id = $2 for update
    `, tenantID, userID).Scan(&role)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return ErrNotAMember
        }
        return err
    }

    if role == RoleOwner {
        var ownerCount int
        if err := tx.QueryRow(ctx, `
            select count(*) from tenant_memberships
            where tenant_id = $1 and role = 'owner'
        `, tenantID).Scan(&ownerCount); err != nil {
            return err
        }
        if ownerCount <= 1 {
            return ErrLastOwner
        }
    }

    if _, err := tx.Exec(ctx, `
        delete from tenant_memberships where tenant_id = $1 and user_id = $2
    `, tenantID, userID); err != nil {
        return err
    }
    return tx.Commit(ctx)
}

// LeaveTenant is the self-serve variant of RemoveMember. Blocks if it would
// leave the tenant without an owner OR if it would leave the user with zero
// memberships.
func (s *Service) LeaveTenant(ctx context.Context, tenantID, userID uuid.UUID) error {
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return err
    }
    defer func() { _ = tx.Rollback(ctx) }()

    var role Role
    err = tx.QueryRow(ctx, `
        select role from tenant_memberships
        where tenant_id = $1 and user_id = $2 for update
    `, tenantID, userID).Scan(&role)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return ErrNotAMember
        }
        return err
    }

    // Last-tenant guard.
    var membershipCount int
    if err := tx.QueryRow(ctx, `
        select count(*) from tenant_memberships where user_id = $1
    `, userID).Scan(&membershipCount); err != nil {
        return err
    }
    if membershipCount <= 1 {
        return ErrLastTenant
    }

    // Last-owner guard.
    if role == RoleOwner {
        var ownerCount int
        if err := tx.QueryRow(ctx, `
            select count(*) from tenant_memberships
            where tenant_id = $1 and role = 'owner'
        `, tenantID).Scan(&ownerCount); err != nil {
            return err
        }
        if ownerCount <= 1 {
            return ErrLastOwner
        }
    }

    if _, err := tx.Exec(ctx, `
        delete from tenant_memberships where tenant_id = $1 and user_id = $2
    `, tenantID, userID); err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```

- [ ] **Step 3: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... -run 'TestService_ChangeRole|TestService_RemoveMember|TestService_LeaveTenant'
go vet ./...
go build ./...
```

Expected: five tests green.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/service.go backend/internal/identity/members_test.go
git commit -m "$(cat <<'EOF'
feat(tenants): add ChangeRole / RemoveMember / LeaveTenant

Service-layer methods with row-level locking and the last-owner +
last-tenant guards from spec §3.4. Sentinel errors surface the guard
name so handlers can map 422 with stable codes.
EOF
)"
```

---

## Task 6: Extend ListMembers to include pending invites

**Spec:** §4.4 (`GET /api/v1/t/{id}/members` returns memberships + pending invites).

**Files:**
- Modify: `backend/internal/identity/service.go` — extend `ListMembers`
- Modify: `backend/internal/identity/tenants_test.go` — add a test

- [ ] **Step 1: Update the read model**

```go
// PendingInvite is the pending-invite row embedded in the members response.
type PendingInvite struct {
    ID          uuid.UUID `json:"id"`
    Email       string    `json:"email"`
    Role        Role      `json:"role"`
    InvitedBy   uuid.UUID `json:"invitedByUserId"`
    InvitedAt   time.Time `json:"invitedAt"`
    ExpiresAt   time.Time `json:"expiresAt"`
}

// MembersResponse is the payload for GET /t/{id}/members.
type MembersResponse struct {
    Members        []Membership    `json:"members"`
    PendingInvites []PendingInvite `json:"pendingInvites"`
}
```

- [ ] **Step 2: Rewrite `ListMembers`**

```go
// ListMembers returns memberships + currently-pending invites for tenantID.
// "Pending" means accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now().
func (s *Service) ListMembers(ctx context.Context, tenantID uuid.UUID) (*MembersResponse, error) {
    out := &MembersResponse{Members: []Membership{}, PendingInvites: []PendingInvite{}}

    rows, err := s.pool.Query(ctx, `
        select m.tenant_id, m.user_id, u.email, u.display_name, m.role::text, m.created_at
        from tenant_memberships m
        join users u on u.id = m.user_id
        where m.tenant_id = $1
        order by m.created_at
    `, tenantID)
    if err != nil {
        return nil, fmt.Errorf("list memberships: %w", err)
    }
    for rows.Next() {
        var m Membership
        var role string
        if err := rows.Scan(&m.TenantID, &m.UserID, &m.Email, &m.DisplayName, &role, &m.CreatedAt); err != nil {
            rows.Close()
            return nil, err
        }
        m.Role = Role(role)
        out.Members = append(out.Members, m)
    }
    rows.Close()
    if err := rows.Err(); err != nil {
        return nil, err
    }

    iRows, err := s.pool.Query(ctx, `
        select id, email, role::text, invited_by_user_id, created_at, expires_at
        from tenant_invites
        where tenant_id = $1
          and accepted_at is null
          and revoked_at is null
          and expires_at > now()
        order by created_at desc
    `, tenantID)
    if err != nil {
        return nil, fmt.Errorf("list invites: %w", err)
    }
    defer iRows.Close()
    for iRows.Next() {
        var inv PendingInvite
        var role string
        if err := iRows.Scan(&inv.ID, &inv.Email, &role, &inv.InvitedBy, &inv.InvitedAt, &inv.ExpiresAt); err != nil {
            return nil, err
        }
        inv.Role = Role(role)
        out.PendingInvites = append(out.PendingInvites, inv)
    }
    return out, iRows.Err()
}
```

- [ ] **Step 3: Test**

```go
func TestService_ListMembers_IncludesPendingInvite(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    owner := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, owner, "owner")

    // Seed a pending invite directly (InviteService is task 7).
    _, err := pool.Exec(context.Background(), `
        insert into tenant_invites (id, tenant_id, email, role, token_hash,
                                    invited_by_user_id, expires_at)
        values ($1, $2, 'bob@example.com', 'member', $3, $4, now() + interval '7 days')
    `, uuidx.New(), tenantID, testdb.HashInviteToken("raw"), owner)
    if err != nil {
        t.Fatalf("seed invite: %v", err)
    }

    res, err := svc.ListMembers(context.Background(), tenantID)
    if err != nil {
        t.Fatalf("ListMembers: %v", err)
    }
    if len(res.Members) != 1 || len(res.PendingInvites) != 1 {
        t.Fatalf("want 1 member + 1 invite, got %d + %d", len(res.Members), len(res.PendingInvites))
    }
    if res.PendingInvites[0].Email != "bob@example.com" {
        t.Fatalf("unexpected invite email: %s", res.PendingInvites[0].Email)
    }
}
```

- [ ] **Step 4: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/...
go vet ./...
```

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/service.go backend/internal/identity/tenants_test.go
git commit -m "$(cat <<'EOF'
feat(tenants): extend ListMembers with pending invites

ListMembers now returns a MembersResponse containing both memberships
and invites where accepted_at is null, revoked_at is null, and
expires_at is still in the future.
EOF
)"
```

---

## Task 7: InviteService — Create + Preview + Accept + Revoke

**Spec:** §4.2 (`GET /invites/{token}`, `POST /invites/{token}/accept`), §4.4 (`POST /t/{id}/invites`, `DELETE /t/{id}/invites/{inviteId}`), §7 (7-day token, SHA-256 hash).

**Files:**
- Create: `backend/internal/identity/invites.go`
- Create: `backend/internal/identity/invites_test.go`

- [ ] **Step 1: Write the failing tests first**

```go
package identity_test

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/xmedavid/folio/backend/internal/identity"
    "github.com/xmedavid/folio/backend/internal/testdb"
)

func TestInviteService_Create_ReturnsPlaintextTokenOnce(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)

    inv, plaintext, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatalf("Create: %v", err)
    }
    if plaintext == "" {
        t.Fatal("expected non-empty plaintext token")
    }
    if inv.Email != "bob@example.com" || inv.Role != identity.RoleMember {
        t.Fatalf("invite mismatch: %+v", inv)
    }
    // Plaintext must NOT match what's in the DB.
    var dbHash []byte
    if err := pool.QueryRow(context.Background(),
        `select token_hash from tenant_invites where id = $1`, inv.ID).Scan(&dbHash); err != nil {
        t.Fatalf("read hash: %v", err)
    }
    if string(dbHash) == plaintext {
        t.Fatal("plaintext stored in DB")
    }
}

func TestInviteService_Preview_NoAuth_ReturnsSanitizedShape(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    _, plaintext, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }

    prev, err := svc.Preview(context.Background(), plaintext)
    if err != nil {
        t.Fatalf("Preview: %v", err)
    }
    if prev.TenantName != "Alice" || prev.InviterDisplayName != "alice@example.com" {
        t.Fatalf("preview: %+v", prev)
    }
    if prev.Email != "bob@example.com" {
        t.Fatalf("email: %s", prev.Email)
    }
}

func TestInviteService_Preview_ExpiredReturnsError(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    _, plaintext, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }
    // Force-expire.
    if _, err := pool.Exec(context.Background(),
        `update tenant_invites set expires_at = now() - interval '1 hour' where tenant_id = $1`, tenantID); err != nil {
        t.Fatal(err)
    }

    if _, err := svc.Preview(context.Background(), plaintext); !errors.Is(err, identity.ErrInviteExpired) {
        t.Fatalf("want ErrInviteExpired, got %v", err)
    }
}

func TestInviteService_Accept_MatchesEmailAndCreatesMembership(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    _, plaintext, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }
    bob := testdb.CreateTestUser(t, pool, "bob@example.com", true)

    mem, err := svc.Accept(context.Background(), plaintext, bob)
    if err != nil {
        t.Fatalf("Accept: %v", err)
    }
    if mem.UserID != bob || mem.Role != identity.RoleMember {
        t.Fatalf("membership mismatch: %+v", mem)
    }

    // Invite now accepted_at is not null.
    var acceptedAt *time.Time
    _ = pool.QueryRow(context.Background(),
        `select accepted_at from tenant_invites where tenant_id = $1`, tenantID).Scan(&acceptedAt)
    if acceptedAt == nil {
        t.Fatal("expected accepted_at set")
    }
}

func TestInviteService_Accept_MismatchedEmailRejected(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    _, plaintext, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }
    other := testdb.CreateTestUser(t, pool, "carol@example.com", true)

    _, err = svc.Accept(context.Background(), plaintext, other)
    if !errors.Is(err, identity.ErrInviteEmailMismatch) {
        t.Fatalf("want ErrInviteEmailMismatch, got %v", err)
    }
}

func TestInviteService_Accept_UnverifiedEmailRejected(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    _, plaintext, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }
    bob := testdb.CreateTestUser(t, pool, "bob@example.com", false) // unverified

    _, err = svc.Accept(context.Background(), plaintext, bob)
    if !errors.Is(err, identity.ErrEmailUnverified) {
        t.Fatalf("want ErrEmailUnverified, got %v", err)
    }
}

func TestInviteService_Revoke_BlockedForUnrelatedRequester(t *testing.T) {
    pool := testdb.Pool(t)
    svc := identity.NewInviteService(pool)
    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    inviter := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    inv, _, err := svc.Create(context.Background(), tenantID, inviter, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }
    stranger := testdb.CreateTestUser(t, pool, "mallory@example.com", true)

    err = svc.Revoke(context.Background(), inv.ID, stranger)
    if !errors.Is(err, identity.ErrNotAuthorized) {
        t.Fatalf("want ErrNotAuthorized, got %v", err)
    }
}
```

- [ ] **Step 2: Implement `invites.go`**

```go
package identity

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/xmedavid/folio/backend/internal/httpx"
    "github.com/xmedavid/folio/backend/internal/uuidx"
)

// InviteLifetime is the validity window for a tenant invite token.
const InviteLifetime = 7 * 24 * time.Hour

// Sentinel errors for the invite flow.
var (
    ErrInviteNotFound       = errors.New("invite: not found")
    ErrInviteExpired        = errors.New("invite: expired")
    ErrInviteRevoked        = errors.New("invite: revoked")
    ErrInviteAlreadyUsed    = errors.New("invite: already accepted")
    ErrInviteEmailMismatch  = errors.New("invite: email does not match authenticated user")
    ErrEmailUnverified      = errors.New("invite: user email not verified")
    ErrNotAuthorized        = errors.New("invite: not authorised to revoke")
    ErrOwnerInviteByMember  = errors.New("invite: members cannot invite owners")
)

// Invite is the read-model row.
type Invite struct {
    ID        uuid.UUID `json:"id"`
    TenantID  uuid.UUID `json:"tenantId"`
    Email     string    `json:"email"`
    Role      Role      `json:"role"`
    InvitedBy uuid.UUID `json:"invitedByUserId"`
    CreatedAt time.Time `json:"createdAt"`
    ExpiresAt time.Time `json:"expiresAt"`
}

// InvitePreview is the payload returned by the no-auth preview endpoint.
type InvitePreview struct {
    TenantID           uuid.UUID `json:"tenantId"`
    TenantName         string    `json:"tenantName"`
    TenantSlug         string    `json:"tenantSlug"`
    InviterDisplayName string    `json:"inviterDisplayName"`
    Email              string    `json:"email"`
    Role               Role      `json:"role"`
    ExpiresAt          time.Time `json:"expiresAt"`
}

// InviteService owns writes to tenant_invites.
type InviteService struct {
    pool *pgxpool.Pool
    now  func() time.Time
}

func NewInviteService(pool *pgxpool.Pool) *InviteService {
    return &InviteService{pool: pool, now: time.Now}
}

func hashToken(plaintext string) []byte {
    h := sha256.Sum256([]byte(plaintext))
    return h[:]
}

func generateInviteToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create issues a new invite. inviterRole (fetched from tenant_memberships)
// gates "members can only invite members"; callers pass role after checking.
// Returns the row and the plaintext token (shown only once — the caller
// emails it to the invitee via mailer.Mailer).
func (s *InviteService) Create(
    ctx context.Context, tenantID, inviterID uuid.UUID, email string, role Role,
) (*Invite, string, error) {
    email = strings.ToLower(strings.TrimSpace(email))
    if email == "" || !strings.Contains(email, "@") {
        return nil, "", httpx.NewValidationError("email is required and must look like an email")
    }
    if role != RoleOwner && role != RoleMember {
        return nil, "", httpx.NewValidationError("role must be 'owner' or 'member'")
    }

    // Block duplicate pending invites to the same email in the same tenant.
    var pendingExists bool
    if err := s.pool.QueryRow(ctx, `
        select exists(
            select 1 from tenant_invites
            where tenant_id = $1 and email = $2
              and accepted_at is null and revoked_at is null
              and expires_at > now()
        )
    `, tenantID, email).Scan(&pendingExists); err != nil {
        return nil, "", fmt.Errorf("check pending: %w", err)
    }
    if pendingExists {
        return nil, "", httpx.NewValidationError("a pending invite already exists for this email")
    }

    plaintext, err := generateInviteToken()
    if err != nil {
        return nil, "", fmt.Errorf("rand: %w", err)
    }
    id := uuidx.New()
    expiresAt := s.now().Add(InviteLifetime)

    var inv Invite
    err = s.pool.QueryRow(ctx, `
        insert into tenant_invites (id, tenant_id, email, role, token_hash,
                                    invited_by_user_id, expires_at)
        values ($1, $2, $3, $4::tenant_role, $5, $6, $7)
        returning id, tenant_id, email, role::text, invited_by_user_id,
                  created_at, expires_at
    `, id, tenantID, email, role, hashToken(plaintext), inviterID, expiresAt).Scan(
        &inv.ID, &inv.TenantID, &inv.Email, new(string), &inv.InvitedBy,
        &inv.CreatedAt, &inv.ExpiresAt,
    )
    if err != nil {
        return nil, "", fmt.Errorf("insert invite: %w", err)
    }
    inv.Role = role
    return &inv, plaintext, nil
}

// Preview is a no-auth endpoint. Returns tenant name, inviter display name,
// role, and expiry; omits token/hash and any sensitive fields.
func (s *InviteService) Preview(ctx context.Context, plaintext string) (*InvitePreview, error) {
    var p InvitePreview
    var revokedAt, acceptedAt *time.Time
    err := s.pool.QueryRow(ctx, `
        select i.tenant_id, t.name, t.slug, u.display_name,
               i.email, i.role::text, i.expires_at, i.revoked_at, i.accepted_at
        from tenant_invites i
        join tenants t on t.id = i.tenant_id
        join users   u on u.id = i.invited_by_user_id
        where i.token_hash = $1 and t.deleted_at is null
    `, hashToken(plaintext)).Scan(
        &p.TenantID, &p.TenantName, &p.TenantSlug, &p.InviterDisplayName,
        &p.Email, new(string), &p.ExpiresAt, &revokedAt, &acceptedAt,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrInviteNotFound
        }
        return nil, err
    }
    if revokedAt != nil {
        return nil, ErrInviteRevoked
    }
    if acceptedAt != nil {
        return nil, ErrInviteAlreadyUsed
    }
    if p.ExpiresAt.Before(s.now()) {
        return nil, ErrInviteExpired
    }
    // Fill role using a second, cheaper read rather than double-scan.
    var role string
    _ = s.pool.QueryRow(ctx,
        `select role::text from tenant_invites where token_hash = $1`,
        hashToken(plaintext)).Scan(&role)
    p.Role = Role(role)
    return &p, nil
}

// Accept consumes the invite on behalf of userID. Requires the user's
// email matches the invite's and that email is verified. Creates the
// membership in a transaction with the invite update.
func (s *InviteService) Accept(ctx context.Context, plaintext string, userID uuid.UUID) (*Membership, error) {
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return nil, err
    }
    defer func() { _ = tx.Rollback(ctx) }()

    var (
        inviteID   uuid.UUID
        tenantID   uuid.UUID
        inviteEmail string
        role       string
        expiresAt  time.Time
        revokedAt  *time.Time
        acceptedAt *time.Time
    )
    err = tx.QueryRow(ctx, `
        select id, tenant_id, email, role::text, expires_at, revoked_at, accepted_at
        from tenant_invites
        where token_hash = $1
        for update
    `, hashToken(plaintext)).Scan(&inviteID, &tenantID, &inviteEmail, &role,
        &expiresAt, &revokedAt, &acceptedAt)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrInviteNotFound
        }
        return nil, err
    }
    if revokedAt != nil {
        return nil, ErrInviteRevoked
    }
    if acceptedAt != nil {
        return nil, ErrInviteAlreadyUsed
    }
    if expiresAt.Before(s.now()) {
        return nil, ErrInviteExpired
    }

    // Load caller email + verification.
    var userEmail string
    var verifiedAt *time.Time
    if err := tx.QueryRow(ctx,
        `select email, email_verified_at from users where id = $1`, userID).
        Scan(&userEmail, &verifiedAt); err != nil {
        return nil, err
    }
    if strings.ToLower(userEmail) != strings.ToLower(inviteEmail) {
        return nil, ErrInviteEmailMismatch
    }
    if verifiedAt == nil {
        return nil, ErrEmailUnverified
    }

    // Insert membership (unique on (tenant_id, user_id); if they're already
    // a member, treat this as an upsert and just consume the invite).
    _, err = tx.Exec(ctx, `
        insert into tenant_memberships (tenant_id, user_id, role)
        values ($1, $2, $3::tenant_role)
        on conflict (tenant_id, user_id) do nothing
    `, tenantID, userID, role)
    if err != nil {
        return nil, fmt.Errorf("insert membership: %w", err)
    }

    // Mark invite consumed.
    if _, err := tx.Exec(ctx,
        `update tenant_invites set accepted_at = now() where id = $1`, inviteID); err != nil {
        return nil, err
    }

    if err := tx.Commit(ctx); err != nil {
        return nil, err
    }

    return &Membership{
        TenantID: tenantID, UserID: userID, Email: userEmail,
        Role: Role(role), CreatedAt: s.now(),
    }, nil
}

// Revoke marks an invite revoked. Allowed for the original inviter or any
// owner of the tenant — the handler enforces the latter via RequireRole.
// This method checks the former and returns ErrNotAuthorized if neither.
func (s *InviteService) Revoke(ctx context.Context, inviteID, requesterUserID uuid.UUID) error {
    tx, err := s.pool.Begin(ctx)
    if err != nil {
        return err
    }
    defer func() { _ = tx.Rollback(ctx) }()

    var tenantID, invitedBy uuid.UUID
    var revokedAt, acceptedAt *time.Time
    err = tx.QueryRow(ctx, `
        select tenant_id, invited_by_user_id, revoked_at, accepted_at
        from tenant_invites where id = $1 for update
    `, inviteID).Scan(&tenantID, &invitedBy, &revokedAt, &acceptedAt)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return ErrInviteNotFound
        }
        return err
    }
    if revokedAt != nil || acceptedAt != nil {
        return nil // idempotent
    }

    // Authorisation: requester is the inviter OR an owner of the tenant.
    if invitedBy != requesterUserID {
        var isOwner bool
        if err := tx.QueryRow(ctx, `
            select exists(
                select 1 from tenant_memberships
                where tenant_id = $1 and user_id = $2 and role = 'owner'
            )
        `, tenantID, requesterUserID).Scan(&isOwner); err != nil {
            return err
        }
        if !isOwner {
            return ErrNotAuthorized
        }
    }

    if _, err := tx.Exec(ctx,
        `update tenant_invites set revoked_at = now() where id = $1`, inviteID); err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```

- [ ] **Step 3: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... -run TestInviteService
go vet ./...
go build ./...
```

Expected: seven tests green.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/invites.go backend/internal/identity/invites_test.go
git commit -m "$(cat <<'EOF'
feat(invites): add InviteService with create/preview/accept/revoke

32-byte base64url plaintext tokens hashed with SHA-256. Create enforces
no-duplicate-pending rule, Preview is no-auth and sanitised, Accept
requires email match + verified email and writes membership + invite
in one transaction, Revoke is idempotent and authorises inviter-or-owner.
EOF
)"
```

---

## Task 8: Extend auth.Service.Signup to consume an optional invite token

**Spec:** §4.2 (signup accepts `inviteToken`; when present and email matches, also creates membership in invited tenant in addition to the Personal tenant).

**Files:**
- Modify: `backend/internal/auth/service.go` (from plan 1) — add optional `InviteToken` field to `SignupInput`
- Modify: `backend/internal/auth/handler.go` — parse `inviteToken` from body
- Modify: `backend/internal/auth/service_test.go` — new test
- Modify (pre-condition): `backend/internal/http/router.go` — no change; the auth handler already sits on `POST /api/v1/auth/signup`

- [ ] **Step 1: Extend `SignupInput`**

```go
type SignupInput struct {
    Email        string
    DisplayName  string
    Password     string
    TenantName   string        // default "Personal" if blank
    InviteToken  string        // optional; empty means no invite
}
```

- [ ] **Step 2: Test first (red)**

```go
func TestAuth_Signup_WithInviteToken_JoinsInvitedTenant(t *testing.T) {
    pool := testdb.Pool(t)
    authSvc := auth.NewService(pool, testLogger(t))
    inviteSvc := identity.NewInviteService(pool)

    // Alice has a tenant; she invites bob.
    aliceTenant, _ := testdb.CreateTestTenant(t, pool, "Alice")
    alice := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, aliceTenant, alice, "owner")
    _, plaintext, err := inviteSvc.Create(context.Background(), aliceTenant, alice, "bob@example.com", identity.RoleMember)
    if err != nil {
        t.Fatal(err)
    }

    // Bob signs up with the invite token.
    res, err := authSvc.Signup(context.Background(), auth.SignupInput{
        Email:       "bob@example.com",
        DisplayName: "Bob",
        Password:    "correcthorsebatterystaple",
        InviteToken: plaintext,
    })
    if err != nil {
        t.Fatalf("Signup: %v", err)
    }
    // Bob has TWO memberships: his Personal tenant + Alice's tenant.
    var count int
    _ = pool.QueryRow(context.Background(),
        `select count(*) from tenant_memberships where user_id = $1`, res.User.ID).Scan(&count)
    if count != 2 {
        t.Fatalf("want 2 memberships, got %d", count)
    }

    // Invite is consumed.
    var acceptedAt *time.Time
    _ = pool.QueryRow(context.Background(),
        `select accepted_at from tenant_invites where tenant_id = $1`, aliceTenant).Scan(&acceptedAt)
    if acceptedAt == nil {
        t.Fatal("invite not consumed")
    }
}

func TestAuth_Signup_WithInviteToken_EmailMismatchStillCreatesPersonalTenant(t *testing.T) {
    pool := testdb.Pool(t)
    authSvc := auth.NewService(pool, testLogger(t))
    inviteSvc := identity.NewInviteService(pool)

    aliceTenant, _ := testdb.CreateTestTenant(t, pool, "Alice")
    alice := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, aliceTenant, alice, "owner")
    _, plaintext, _ := inviteSvc.Create(context.Background(), aliceTenant, alice, "bob@example.com", identity.RoleMember)

    // Someone else tries to sign up with Bob's token.
    _, err := authSvc.Signup(context.Background(), auth.SignupInput{
        Email:       "carol@example.com",
        DisplayName: "Carol",
        Password:    "correcthorsebatterystaple",
        InviteToken: plaintext,
    })
    if !errors.Is(err, identity.ErrInviteEmailMismatch) {
        t.Fatalf("want ErrInviteEmailMismatch, got %v", err)
    }
}
```

- [ ] **Step 3: Implement (green)**

Add to `auth.Service.Signup`, immediately after user creation and Personal-tenant creation, inside the same transaction:

```go
// Consume an invite if one was supplied. The invite MUST match the
// authoritative user email (normalized to lowercase) and the user must
// be email-verified — but since we just signed up, we bypass the
// verified check here: signup IS the verification in the invite flow.
// Spec §7: "Accepting a tenant invite" requires verification; the
// spec-deferred pragma for signup-with-invite is that the signup email
// IS the email the invite was sent to, so verification is implicit.
if in.InviteToken != "" {
    var (
        inviteID   uuid.UUID
        tenantID   uuid.UUID
        inviteEmail string
        role       string
        expiresAt  time.Time
        revokedAt  *time.Time
        acceptedAt *time.Time
    )
    err := tx.QueryRow(ctx, `
        select id, tenant_id, email, role::text, expires_at, revoked_at, accepted_at
        from tenant_invites where token_hash = $1 for update
    `, hashInviteToken(in.InviteToken)).Scan(&inviteID, &tenantID, &inviteEmail,
        &role, &expiresAt, &revokedAt, &acceptedAt)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, identity.ErrInviteNotFound
        }
        return nil, err
    }
    if revokedAt != nil {
        return nil, identity.ErrInviteRevoked
    }
    if acceptedAt != nil {
        return nil, identity.ErrInviteAlreadyUsed
    }
    if expiresAt.Before(s.now()) {
        return nil, identity.ErrInviteExpired
    }
    if strings.ToLower(inviteEmail) != strings.ToLower(in.Email) {
        return nil, identity.ErrInviteEmailMismatch
    }

    // Add membership and consume invite.
    if _, err := tx.Exec(ctx, `
        insert into tenant_memberships (tenant_id, user_id, role)
        values ($1, $2, $3::tenant_role)
    `, tenantID, user.ID, role); err != nil {
        return nil, fmt.Errorf("insert invited membership: %w", err)
    }
    if _, err := tx.Exec(ctx,
        `update tenant_invites set accepted_at = now() where id = $1`, inviteID); err != nil {
        return nil, err
    }

    // Audit event — member.invite_accepted.
    if err := writeAuditEvent(ctx, tx, tenantID, user.ID, "member.invite_accepted",
        "invite", inviteID, nil, map[string]any{"role": role, "email": inviteEmail}); err != nil {
        return nil, err
    }
}
```

Add `hashInviteToken` as an unexported alias of `identity.hashToken` (or expose the latter).

Update `auth.Handler.signup` to accept `inviteToken` in the JSON body:

```go
type signupReq struct {
    Email       string `json:"email"`
    DisplayName string `json:"displayName"`
    Password    string `json:"password"`
    TenantName  string `json:"tenantName"`
    InviteToken string `json:"inviteToken"`
}
```

…and pass it through.

- [ ] **Step 4: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/auth/...
go vet ./...
```

Expected: both new tests pass; existing signup tests still green.

- [ ] **Step 5: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/auth/
git commit -m "$(cat <<'EOF'
feat(invites): extend auth.Signup to consume invite tokens

When inviteToken is supplied and matches the signup email, the signup
transaction also creates a membership in the invited tenant and marks
the invite accepted. Personal tenant creation is unaffected.
EOF
)"
```

---

## Task 9: Tenant-scoped HTTP — PATCH / DELETE / restore

**Spec:** §4.4 (`PATCH /t/{id}`, `DELETE /t/{id}`, `POST /t/{id}/restore`), §4.5 (middleware chain), §8.3 (`tenant.*` audit events).

**Files:**
- Create: `backend/internal/identity/http_tenants.go`
- Modify: `backend/internal/http/router.go` — mount new routes under `/api/v1/t/{tenantId}`
- Create: `backend/internal/identity/http_tenants_test.go`

- [ ] **Step 1: Define the handlers**

```go
// http_tenants.go
package identity

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "github.com/xmedavid/folio/backend/internal/auth"
    "github.com/xmedavid/folio/backend/internal/httpx"
)

// TenantHandler owns tenant-scoped administration routes.
type TenantHandler struct{ svc *Service }

func NewTenantHandler(svc *Service) *TenantHandler { return &TenantHandler{svc: svc} }

// Mount installs tenant-admin routes behind the plan-1 middleware chain.
// The caller is responsible for wrapping with RequireSession +
// RequireMembership before this Mount; this function only adds role
// and fresh-reauth gates as required by spec §4.4.
func (h *TenantHandler) Mount(r chi.Router) {
    // PATCH /t/{id} — owner; settings change implies sensitive change, so fresh.
    r.With(auth.RequireRole(RoleOwner), auth.RequireFreshReauth(5*60)).
        Patch("/", h.update)
    // DELETE /t/{id} — owner; fresh.
    r.With(auth.RequireRole(RoleOwner), auth.RequireFreshReauth(5*60)).
        Delete("/", h.softDelete)
    // POST /t/{id}/restore — owner; fresh.
    r.With(auth.RequireRole(RoleOwner), auth.RequireFreshReauth(5*60)).
        Post("/restore", h.restore)
}

type patchTenantReq struct {
    Name           *string `json:"name"`
    Slug           *string `json:"slug"`
    BaseCurrency   *string `json:"baseCurrency"`
    CycleAnchorDay *int    `json:"cycleAnchorDay"`
    Locale         *string `json:"locale"`
    Timezone       *string `json:"timezone"`
}

func (h *TenantHandler) update(w http.ResponseWriter, r *http.Request) {
    tenantID, ok := auth.TenantFromCtx(r.Context())
    if !ok {
        httpx.WriteError(w, http.StatusInternalServerError, "tenant_missing", "tenant context absent")
        return
    }
    var req patchTenantReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "body must be valid JSON")
        return
    }
    in := UpdateTenantInput{
        Name: req.Name, Slug: req.Slug, BaseCurrency: req.BaseCurrency,
        CycleAnchorDay: req.CycleAnchorDay, Locale: req.Locale, Timezone: req.Timezone,
    }
    before, _ := h.svc.GetTenant(r.Context(), tenantID)
    updated, err := h.svc.UpdateTenant(r.Context(), tenantID, in)
    if err != nil {
        httpx.WriteServiceError(w, err)
        return
    }
    userID, _ := auth.UserFromCtx(r.Context())
    h.svc.WriteAudit(r.Context(), tenantID, userID,
        "tenant.settings_changed", "tenant", tenantID,
        before, updated)
    httpx.WriteJSON(w, http.StatusOK, updated)
}

func (h *TenantHandler) softDelete(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    userID, _ := auth.UserFromCtx(r.Context())

    if err := h.svc.SoftDeleteTenant(r.Context(), tenantID); err != nil {
        httpx.WriteServiceError(w, err)
        return
    }
    h.svc.WriteAudit(r.Context(), tenantID, userID,
        "tenant.deleted", "tenant", tenantID, nil, map[string]any{"deletedAt": "now"})
    w.WriteHeader(http.StatusNoContent)
}

func (h *TenantHandler) restore(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    userID, _ := auth.UserFromCtx(r.Context())

    if err := h.svc.RestoreTenant(r.Context(), tenantID); err != nil {
        httpx.WriteServiceError(w, err)
        return
    }
    h.svc.WriteAudit(r.Context(), tenantID, userID,
        "tenant.restored", "tenant", tenantID, nil, nil)

    tenant, err := h.svc.GetTenant(r.Context(), tenantID)
    if err != nil {
        httpx.WriteServiceError(w, err)
        return
    }
    _ = uuid.Nil // unused import linter guard
    _ = chi.URLParam
    httpx.WriteJSON(w, http.StatusOK, tenant)
}
```

- [ ] **Step 2: Add a small `WriteAudit` helper on `Service`**

```go
// WriteAudit writes a row to audit_events. Best-effort — errors are
// logged, not surfaced to the caller, so an audit-table blip doesn't
// bring down the foreground request.
func (s *Service) WriteAudit(
    ctx context.Context,
    tenantID, actorID uuid.UUID,
    action, entityType string, entityID uuid.UUID,
    before, after any,
) {
    beforeJSON, _ := json.Marshal(before)
    afterJSON, _ := json.Marshal(after)
    _, err := s.pool.Exec(ctx, `
        insert into audit_events (tenant_id, actor_user_id, action,
                                  entity_type, entity_id, before_jsonb, after_jsonb, occurred_at)
        values ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, now())
    `, nullUUID(tenantID), nullUUID(actorID), action, entityType, entityID,
        beforeJSON, afterJSON)
    if err != nil {
        slog.Default().Warn("audit.write_failed", "action", action, "err", err)
    }
}

func nullUUID(u uuid.UUID) any {
    if u == uuid.Nil {
        return nil
    }
    return u
}
```

- [ ] **Step 3: Wire into router**

In `backend/internal/http/router.go`, inside the `/api/v1` block, after plan 1's tenant-scoped group:

```go
tenantH := identity.NewTenantHandler(identitySvc)

r.Route("/t/{tenantId}", func(r chi.Router) {
    r.Use(auth.RequireSession(authSvc))
    r.Use(auth.RequireMembership(identitySvc))
    // Plan 1 already mounts GET /t/{tenantId}/members under this prefix;
    // plan 2 extends it with invites routes and tenantH.Mount below.
    tenantH.Mount(r)
    // Members + invites come in tasks 10 and 11.
})
```

- [ ] **Step 4: Handler test (route + middleware chain)**

```go
func TestTenantHandler_Patch_RequiresOwnerRoleAndFreshReauth(t *testing.T) {
    pool := testdb.Pool(t)
    srv := newTestServer(t, pool)

    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    alice := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, alice, "owner")
    sessionID, rawToken := testdb.CreateTestSession(t, pool, alice)

    // Without fresh reauth, handler returns 403.
    body := bytes.NewBufferString(`{"name":"Renamed"}`)
    req := httptest.NewRequest("PATCH", "/api/v1/t/"+tenantID.String(), body)
    req.Header.Set("Cookie", "folio_session="+rawToken)
    req.Header.Set("X-Folio-Request", "1")
    w := httptest.NewRecorder()
    srv.ServeHTTP(w, req)
    if w.Code != http.StatusForbidden {
        t.Fatalf("want 403 (reauth_required), got %d", w.Code)
    }

    // Bump reauth_at and try again.
    testdb.SetSessionReauth(t, pool, sessionID, time.Now())
    req2 := httptest.NewRequest("PATCH", "/api/v1/t/"+tenantID.String(),
        bytes.NewBufferString(`{"name":"Renamed"}`))
    req2.Header.Set("Cookie", "folio_session="+rawToken)
    req2.Header.Set("X-Folio-Request", "1")
    w2 := httptest.NewRecorder()
    srv.ServeHTTP(w2, req2)
    if w2.Code != http.StatusOK {
        t.Fatalf("want 200, got %d (body=%s)", w2.Code, w2.Body.String())
    }
}
```

`newTestServer(t, pool)` mirrors plan 1's helper; it wires `auth.Service`, `identity.Service`, and calls `http.NewRouter`.

- [ ] **Step 5: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... ./internal/http/...
go vet ./...
go build ./...
```

- [ ] **Step 6: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/http_tenants.go \
        backend/internal/identity/http_tenants_test.go \
        backend/internal/identity/service.go \
        backend/internal/http/router.go
git commit -m "$(cat <<'EOF'
feat(tenants): add PATCH /t/{id}, DELETE /t/{id}, POST /t/{id}/restore

Gated behind RequireSession + RequireMembership + RequireRole(owner) +
RequireFreshReauth(5m). Plan 4 activates fresh-reauth; until then the
UI surfaces a 403 { code: "reauth_required" } and prompts the user.
Audit events: tenant.settings_changed / tenant.deleted / tenant.restored.
EOF
)"
```

---

## Task 10: Tenant-scoped HTTP — members list + role change + remove/leave

**Spec:** §4.4 members routes; §8.3 member audit events.

**Files:**
- Modify: `backend/internal/identity/http_tenants.go` — extend `Mount`
- Create: `backend/internal/identity/http_members_test.go`

- [ ] **Step 1: Extend `Mount`**

```go
func (h *TenantHandler) Mount(r chi.Router) {
    // (existing PATCH/DELETE/restore routes unchanged) ...

    // Any-member: list members + pending invites.
    r.Get("/members", h.listMembers)

    // Owner + fresh: change role, remove member.
    r.With(auth.RequireRole(RoleOwner), auth.RequireFreshReauth(5*60)).
        Patch("/members/{userId}", h.changeRole)
    // DELETE /members/{userId} has split semantics:
    //   userId == self → LeaveTenant (any member, fresh)
    //   userId != self → RemoveMember (owner, fresh)
    // The handler decides; both paths require fresh reauth.
    r.With(auth.RequireFreshReauth(5*60)).
        Delete("/members/{userId}", h.removeOrLeave)
}

func (h *TenantHandler) listMembers(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    res, err := h.svc.ListMembers(r.Context(), tenantID)
    if err != nil {
        httpx.WriteServiceError(w, err)
        return
    }
    httpx.WriteJSON(w, http.StatusOK, res)
}

type patchMemberReq struct {
    Role string `json:"role"`
}

func (h *TenantHandler) changeRole(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    actor, _ := auth.UserFromCtx(r.Context())
    userID, err := uuid.Parse(chi.URLParam(r, "userId"))
    if err != nil {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "userId must be a UUID")
        return
    }
    var req patchMemberReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "body must be valid JSON")
        return
    }

    err = h.svc.ChangeRole(r.Context(), tenantID, userID, Role(req.Role))
    switch {
    case errors.Is(err, ErrLastOwner):
        httpx.WriteError(w, http.StatusUnprocessableEntity, "last_owner",
            "cannot demote the last owner of this tenant")
        return
    case errors.Is(err, ErrNotAMember):
        httpx.WriteError(w, http.StatusNotFound, "not_a_member", "membership not found")
        return
    case err != nil:
        httpx.WriteServiceError(w, err)
        return
    }
    h.svc.WriteAudit(r.Context(), tenantID, actor,
        "member.role_changed", "membership", userID, nil,
        map[string]any{"role": req.Role})
    w.WriteHeader(http.StatusNoContent)
}

func (h *TenantHandler) removeOrLeave(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    actor, _ := auth.UserFromCtx(r.Context())
    userID, err := uuid.Parse(chi.URLParam(r, "userId"))
    if err != nil {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "userId must be a UUID")
        return
    }

    role, _ := auth.RoleFromCtx(r.Context())
    var action string
    if userID == actor {
        // self-leave — any member.
        err = h.svc.LeaveTenant(r.Context(), tenantID, userID)
        action = "member.left"
    } else {
        // remove-other — owner only.
        if role != RoleOwner {
            httpx.WriteError(w, http.StatusForbidden, "forbidden",
                "only owners can remove other members")
            return
        }
        err = h.svc.RemoveMember(r.Context(), tenantID, userID)
        action = "member.removed"
    }
    switch {
    case errors.Is(err, ErrLastOwner):
        httpx.WriteError(w, http.StatusUnprocessableEntity, "last_owner",
            "cannot remove the last owner of this tenant")
        return
    case errors.Is(err, ErrLastTenant):
        httpx.WriteError(w, http.StatusUnprocessableEntity, "last_tenant",
            "cannot leave your last tenant — create another or delete the account")
        return
    case errors.Is(err, ErrNotAMember):
        httpx.WriteError(w, http.StatusNotFound, "not_a_member", "membership not found")
        return
    case err != nil:
        httpx.WriteServiceError(w, err)
        return
    }
    h.svc.WriteAudit(r.Context(), tenantID, actor, action,
        "membership", userID, nil, nil)
    w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Handler tests covering all branches**

`http_members_test.go` exercises:

1. `GET /members` returns members + pending invites (seeded via `pool.Exec`).
2. `PATCH /members/{userId}` promotes a member to owner (fresh reauth set; 204).
3. `PATCH /members/{userId}` demoting the last owner returns 422 `last_owner`.
4. `DELETE /members/{userId}` where `userId == actor.ID` calls LeaveTenant; succeeds when the actor has a second tenant.
5. `DELETE /members/{userId}` where `userId != actor.ID` returns 403 unless actor is owner.

Template for test 1 (others follow the same pattern as Task 9):

```go
func TestTenantHandler_ListMembers_IncludesPendingInvites(t *testing.T) {
    pool := testdb.Pool(t)
    srv := newTestServer(t, pool)

    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    alice := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, alice, "owner")
    _, rawToken := testdb.CreateTestSession(t, pool, alice)

    // Seed a pending invite.
    _, err := pool.Exec(context.Background(), `
        insert into tenant_invites (id, tenant_id, email, role, token_hash,
                                    invited_by_user_id, expires_at)
        values ($1, $2, 'bob@example.com', 'member', $3, $4, now() + interval '7 days')
    `, uuidx.New(), tenantID, testdb.HashInviteToken("raw"), alice)
    if err != nil { t.Fatal(err) }

    req := httptest.NewRequest("GET", "/api/v1/t/"+tenantID.String()+"/members", nil)
    req.Header.Set("Cookie", "folio_session="+rawToken)
    w := httptest.NewRecorder()
    srv.ServeHTTP(w, req)
    if w.Code != 200 {
        t.Fatalf("want 200, got %d", w.Code)
    }
    var body struct {
        Members        []map[string]any `json:"members"`
        PendingInvites []map[string]any `json:"pendingInvites"`
    }
    _ = json.Unmarshal(w.Body.Bytes(), &body)
    if len(body.Members) != 1 || len(body.PendingInvites) != 1 {
        t.Fatalf("body shape: %+v", body)
    }
}
```

- [ ] **Step 3: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... ./internal/http/...
go vet ./...
```

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/http_tenants.go \
        backend/internal/identity/http_members_test.go
git commit -m "$(cat <<'EOF'
feat(tenants): add GET/PATCH/DELETE /t/{id}/members routes

List returns memberships + pending invites. Role change and remove
share the last-owner guard; DELETE /members/{userId} dispatches
between LeaveTenant (userId == self, any member) and RemoveMember
(userId != self, owner). Audit events: member.role_changed /
member.removed / member.left.
EOF
)"
```

---

## Task 11: Tenant-scoped HTTP — invite create, list implicit (via members), revoke

**Spec:** §4.4 (`POST /t/{id}/invites`, `DELETE /t/{id}/invites/{inviteId}`), §8.3 (`member.invited`, `member.invite_revoked`).

**Files:**
- Create: `backend/internal/identity/http_invites.go`
- Create: `backend/internal/identity/http_invites_test.go`
- Modify: `backend/internal/http/router.go` — mount invite routes
- Modify: `backend/internal/identity/http_tenants.go` — remove any duplicate invite mount if present

- [ ] **Step 1: Handler definition**

```go
// http_invites.go
package identity

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "github.com/xmedavid/folio/backend/internal/auth"
    "github.com/xmedavid/folio/backend/internal/httpx"
    "github.com/xmedavid/folio/backend/internal/mailer"
)

type InviteHandler struct {
    svc     *Service        // for audit + caller role lookups
    invites *InviteService
    mail    mailer.Mailer
}

func NewInviteHandler(svc *Service, inv *InviteService, m mailer.Mailer) *InviteHandler {
    return &InviteHandler{svc: svc, invites: inv, mail: m}
}

func (h *InviteHandler) Mount(r chi.Router) {
    // POST /t/{id}/invites — member may invite members; owner may invite any.
    // RequireRole not used because rule depends on target role.
    r.With(auth.RequireFreshReauth(5*60)).Post("/", h.create)
    // DELETE /t/{id}/invites/{inviteId} — inviter OR owner (service enforces).
    r.With(auth.RequireFreshReauth(5*60)).Delete("/{inviteId}", h.revoke)
}

type createInviteReq struct {
    Email string `json:"email"`
    Role  string `json:"role"`
}

func (h *InviteHandler) create(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    inviter, _ := auth.UserFromCtx(r.Context())
    callerRole, _ := auth.RoleFromCtx(r.Context())

    var req createInviteReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "body must be valid JSON")
        return
    }
    role := Role(req.Role)
    // Non-owners may only invite members.
    if callerRole != RoleOwner && role == RoleOwner {
        httpx.WriteError(w, http.StatusForbidden, "forbidden",
            "only owners can invite owners")
        return
    }

    inv, plaintext, err := h.invites.Create(r.Context(), tenantID, inviter, req.Email, role)
    if err != nil {
        httpx.WriteServiceError(w, err)
        return
    }

    // Enqueue the email via mailer. The URL points at the web page;
    // the web page handles signup-vs-accept routing.
    if err := h.mail.Send(r.Context(), mailer.Message{
        To:       inv.Email,
        Subject:  "You're invited to Folio",
        Template: "invite",
        Data: map[string]any{
            "inviteURL": inviteURL(plaintext),
            "tenantID":  tenantID,
            "role":      role,
        },
        TenantID: tenantID.String(),
    }); err != nil {
        // Non-fatal: log but return 201 with the invite row. The admin
        // console has an email-resend action for bounced messages.
        slog.Default().Warn("mailer.send_failed", "err", err, "invite_id", inv.ID)
    }

    h.svc.WriteAudit(r.Context(), tenantID, inviter,
        "member.invited", "invite", inv.ID, nil,
        map[string]any{"email": inv.Email, "role": inv.Role})

    httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *InviteHandler) revoke(w http.ResponseWriter, r *http.Request) {
    tenantID, _ := auth.TenantFromCtx(r.Context())
    requester, _ := auth.UserFromCtx(r.Context())
    inviteID, err := uuid.Parse(chi.URLParam(r, "inviteId"))
    if err != nil {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "inviteId must be a UUID")
        return
    }

    err = h.invites.Revoke(r.Context(), inviteID, requester)
    switch {
    case errors.Is(err, ErrInviteNotFound):
        httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
        return
    case errors.Is(err, ErrNotAuthorized):
        httpx.WriteError(w, http.StatusForbidden, "forbidden",
            "only the inviter or a tenant owner can revoke this invite")
        return
    case err != nil:
        httpx.WriteServiceError(w, err)
        return
    }
    h.svc.WriteAudit(r.Context(), tenantID, requester,
        "member.invite_revoked", "invite", inviteID, nil, nil)
    w.WriteHeader(http.StatusNoContent)
}

// inviteURL builds the accept-invite URL for plaintextToken. Reads APP_URL
// from the environment; in tests the default is http://localhost:3000.
func inviteURL(plaintext string) string {
    base := os.Getenv("APP_URL")
    if base == "" {
        base = "http://localhost:3000"
    }
    return base + "/accept-invite/" + plaintext
}
```

- [ ] **Step 2: Wire into router**

```go
inviteH := identity.NewInviteHandler(identitySvc, identity.NewInviteService(d.DB), d.Mailer)

r.Route("/t/{tenantId}", func(r chi.Router) {
    r.Use(auth.RequireSession(authSvc))
    r.Use(auth.RequireMembership(identitySvc))
    tenantH.Mount(r)
    r.Route("/invites", inviteH.Mount)
    // accounts etc., re-mounted here
    r.Route("/accounts", accountsH.Mount)
    // …
})
```

- [ ] **Step 3: Handler tests**

Cover:

1. Member creating a `member` invite succeeds (201; `LogMailer.Sent()` has one message; audit row written).
2. Member trying to create an `owner` invite returns 403.
3. Duplicate pending invite returns 400 with `validation_error`.
4. Revoke by the original inviter succeeds (204; invite row `revoked_at IS NOT NULL`).
5. Revoke by a stranger (non-owner, not the inviter) returns 403.

Template for test 1:

```go
func TestInviteHandler_Create_MemberInvitesMember(t *testing.T) {
    pool := testdb.Pool(t)
    mockMail := mailer.NewLogMailer(nil)
    srv := newTestServerWithMailer(t, pool, mockMail)

    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    alice := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, alice, "member")
    sid, rawToken := testdb.CreateTestSession(t, pool, alice)
    testdb.SetSessionReauth(t, pool, sid, time.Now())

    req := httptest.NewRequest("POST", "/api/v1/t/"+tenantID.String()+"/invites",
        bytes.NewBufferString(`{"email":"bob@example.com","role":"member"}`))
    req.Header.Set("Cookie", "folio_session="+rawToken)
    req.Header.Set("X-Folio-Request", "1")
    w := httptest.NewRecorder()
    srv.ServeHTTP(w, req)
    if w.Code != http.StatusCreated {
        t.Fatalf("want 201, got %d (%s)", w.Code, w.Body.String())
    }
    sent := mockMail.Sent()
    if len(sent) != 1 || sent[0].Template != "invite" {
        t.Fatalf("mailer state: %+v", sent)
    }
}
```

- [ ] **Step 4: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... ./internal/http/...
go vet ./...
```

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/http_invites.go \
        backend/internal/identity/http_invites_test.go \
        backend/internal/http/router.go
git commit -m "$(cat <<'EOF'
feat(invites): add POST/DELETE /t/{id}/invites routes

Create enqueues an email via mailer.Mailer and writes member.invited.
Non-owners may only invite members; owners may invite owners. Revoke
is inviter-or-owner (spec §4.4) and idempotent. Email send is
non-fatal so a transient Resend outage does not fail the request.
EOF
)"
```

---

## Task 12: Public `/auth/invites/{token}` preview + accept endpoints

**Spec:** §4.2 invite entry points.

**Files:**
- Modify: `backend/internal/auth/handler.go` — add two new routes
- Create: `backend/internal/auth/invite_routes_test.go`

- [ ] **Step 1: Add handlers**

```go
// In backend/internal/auth/handler.go (plan 1)
func (h *Handler) MountPublic(r chi.Router) {
    // (existing plan-1 routes) …
    r.Get("/auth/invites/{token}", h.previewInvite)
    // Authenticated — requires RequireSession only (no membership yet).
    r.With(RequireSession(h.svc)).
        Post("/auth/invites/{token}/accept", h.acceptInvite)
}

func (h *Handler) previewInvite(w http.ResponseWriter, r *http.Request) {
    token := chi.URLParam(r, "token")
    if token == "" {
        httpx.WriteError(w, http.StatusBadRequest, "invalid_token", "token is required")
        return
    }
    prev, err := h.invites.Preview(r.Context(), token)
    switch {
    case errors.Is(err, identity.ErrInviteNotFound),
         errors.Is(err, identity.ErrInviteRevoked),
         errors.Is(err, identity.ErrInviteAlreadyUsed),
         errors.Is(err, identity.ErrInviteExpired):
        // Spec §4.2: preview is no-auth; surface each with a distinct code
        // so the UI can differentiate (expired-CTA vs already-accepted).
        httpx.WriteError(w, http.StatusGone, errCodeFor(err), err.Error())
        return
    case err != nil:
        httpx.WriteServiceError(w, err)
        return
    }
    httpx.WriteJSON(w, http.StatusOK, prev)
}

func (h *Handler) acceptInvite(w http.ResponseWriter, r *http.Request) {
    token := chi.URLParam(r, "token")
    userID, _ := UserFromCtx(r.Context())
    mem, err := h.invites.Accept(r.Context(), token, userID)
    switch {
    case errors.Is(err, identity.ErrInviteEmailMismatch):
        httpx.WriteError(w, http.StatusForbidden, "email_mismatch",
            "invite email does not match your account")
        return
    case errors.Is(err, identity.ErrEmailUnverified):
        httpx.WriteError(w, http.StatusForbidden, "email_unverified",
            "verify your email before accepting this invite")
        return
    case errors.Is(err, identity.ErrInviteExpired),
         errors.Is(err, identity.ErrInviteRevoked),
         errors.Is(err, identity.ErrInviteAlreadyUsed),
         errors.Is(err, identity.ErrInviteNotFound):
        httpx.WriteError(w, http.StatusGone, errCodeFor(err), err.Error())
        return
    case err != nil:
        httpx.WriteServiceError(w, err)
        return
    }
    // Audit — member.invite_accepted.
    h.identity.WriteAudit(r.Context(), mem.TenantID, userID,
        "member.invite_accepted", "membership", mem.UserID, nil,
        map[string]any{"role": mem.Role})
    httpx.WriteJSON(w, http.StatusOK, mem)
}

func errCodeFor(err error) string {
    switch {
    case errors.Is(err, identity.ErrInviteNotFound):   return "invite_not_found"
    case errors.Is(err, identity.ErrInviteRevoked):    return "invite_revoked"
    case errors.Is(err, identity.ErrInviteAlreadyUsed): return "invite_already_used"
    case errors.Is(err, identity.ErrInviteExpired):    return "invite_expired"
    }
    return "invite_invalid"
}
```

Ensure `auth.Handler` has an `invites *identity.InviteService` and `identity *identity.Service` field populated in its constructor; thread in `router.go`.

- [ ] **Step 2: Tests**

`invite_routes_test.go` covers:

1. `GET /auth/invites/{token}` returns 200 with the preview shape for a fresh invite.
2. Same endpoint returns 410 `invite_expired` once `expires_at` is in the past.
3. `POST /auth/invites/{token}/accept` without a session returns 401.
4. `POST /auth/invites/{token}/accept` with a mismatched-email session returns 403 `email_mismatch`.
5. Successful accept returns 200 with `{tenantId, userId, role}` and writes the membership row.

Skeleton for test 5:

```go
func TestAuth_AcceptInvite_Succeeds(t *testing.T) {
    pool := testdb.Pool(t)
    srv := newTestServer(t, pool)
    inviteSvc := identity.NewInviteService(pool)

    tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice")
    alice := testdb.CreateTestUser(t, pool, "alice@example.com", true)
    testdb.CreateTestMembership(t, pool, tenantID, alice, "owner")
    _, plaintext, _ := inviteSvc.Create(context.Background(), tenantID, alice, "bob@example.com", identity.RoleMember)

    bob := testdb.CreateTestUser(t, pool, "bob@example.com", true)
    _, rawToken := testdb.CreateTestSession(t, pool, bob)

    req := httptest.NewRequest("POST", "/api/v1/auth/invites/"+plaintext+"/accept", nil)
    req.Header.Set("Cookie", "folio_session="+rawToken)
    req.Header.Set("X-Folio-Request", "1")
    w := httptest.NewRecorder()
    srv.ServeHTTP(w, req)
    if w.Code != 200 {
        t.Fatalf("want 200, got %d", w.Code)
    }
    var count int
    _ = pool.QueryRow(context.Background(),
        `select count(*) from tenant_memberships where tenant_id = $1 and user_id = $2`,
        tenantID, bob).Scan(&count)
    if count != 1 {
        t.Fatalf("want 1 membership, got %d", count)
    }
}
```

- [ ] **Step 3: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/auth/...
go vet ./...
```

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/auth/handler.go backend/internal/auth/invite_routes_test.go
git commit -m "$(cat <<'EOF'
feat(invites): add GET /auth/invites/{token} + POST accept

Preview is no-auth and sanitises the response. Accept requires a
session and enforces email-match + email-verified, then creates the
membership. Distinct error codes (invite_expired, invite_revoked,
invite_already_used, email_mismatch) let the UI differentiate CTAs.
EOF
)"
```

---

## Task 13: folio-sweeper binary — hard-delete soft-deleted tenants >30d

**Spec:** §10 soft-delete grace period.

**Files:**
- Create: `backend/internal/jobs/cleanup/sweeper.go`
- Create: `backend/internal/jobs/cleanup/sweeper_test.go`
- Create: `backend/cmd/folio-sweeper/main.go`

- [ ] **Step 1: Write the sweeper function (test first)**

`backend/internal/jobs/cleanup/sweeper_test.go`:

```go
package cleanup_test

import (
    "context"
    "testing"
    "time"

    "github.com/xmedavid/folio/backend/internal/jobs/cleanup"
    "github.com/xmedavid/folio/backend/internal/testdb"
)

func TestSweeper_Run_HardDeletesTenantsOlderThan30Days(t *testing.T) {
    pool := testdb.Pool(t)
    old, _ := testdb.CreateTestTenant(t, pool, "Old")
    recent, _ := testdb.CreateTestTenant(t, pool, "Recent")
    fresh, _ := testdb.CreateTestTenant(t, pool, "Fresh")

    _, _ = pool.Exec(context.Background(),
        `update tenants set deleted_at = now() - interval '31 days' where id = $1`, old)
    _, _ = pool.Exec(context.Background(),
        `update tenants set deleted_at = now() - interval '5 days' where id = $1`, recent)
    // 'fresh' is not deleted.

    report, err := cleanup.Run(context.Background(), pool, 30*24*time.Hour)
    if err != nil {
        t.Fatalf("Run: %v", err)
    }
    if report.DeletedCount != 1 {
        t.Fatalf("want 1 deleted, got %d", report.DeletedCount)
    }

    // 'old' is gone; 'recent' and 'fresh' remain.
    var count int
    _ = pool.QueryRow(context.Background(),
        `select count(*) from tenants where id = any($1)`,
        []any{old, recent, fresh}).Scan(&count)
    if count != 2 {
        t.Fatalf("want 2 remaining tenants, got %d", count)
    }
}
```

- [ ] **Step 2: Implement `sweeper.go`**

```go
// Package cleanup owns periodic maintenance jobs. Plan 2 ships Run as a
// one-shot function called by backend/cmd/folio-sweeper. Plan 3 wraps
// Run in a River PeriodicJob scheduled daily.
package cleanup

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)

// Report summarises a sweeper pass for logging / test assertions.
type Report struct {
    DeletedCount int64
    DeletedIDs   []string
    StartedAt    time.Time
    FinishedAt   time.Time
}

// Run hard-deletes every tenant where deleted_at < now() - gracePeriod.
// Returns a Report of which tenants were removed. Cascades propagate via
// existing FKs (accounts → transactions → …); this is the only place in
// the codebase that deletes a tenant row permanently.
func Run(ctx context.Context, pool *pgxpool.Pool, gracePeriod time.Duration) (*Report, error) {
    r := &Report{StartedAt: time.Now()}
    rows, err := pool.Query(ctx, `
        delete from tenants
        where deleted_at is not null
          and deleted_at < now() - $1::interval
        returning id::text
    `, gracePeriod)
    if err != nil {
        return nil, fmt.Errorf("sweep: %w", err)
    }
    defer rows.Close()
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil {
            return nil, err
        }
        r.DeletedIDs = append(r.DeletedIDs, id)
    }
    if err := rows.Err(); err != nil {
        return nil, err
    }
    r.DeletedCount = int64(len(r.DeletedIDs))
    r.FinishedAt = time.Now()
    slog.Default().Info("cleanup.sweeper.done",
        "deleted", r.DeletedCount, "elapsed", r.FinishedAt.Sub(r.StartedAt))
    return r, nil
}
```

- [ ] **Step 3: Write `cmd/folio-sweeper/main.go`**

```go
// Command folio-sweeper is a one-shot cron-invokable entry point that
// hard-deletes tenants past their 30-day soft-delete grace period.
//
// Plan 2 ships this as a standalone binary so the sweeper works without
// River. Plan 3 wires River to call cleanup.Run directly inside the
// server process on a daily schedule; this binary remains available
// for cron-based self-hosted deployments.
package main

import (
    "context"
    "flag"
    "log/slog"
    "os"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/xmedavid/folio/backend/internal/jobs/cleanup"
)

func main() {
    grace := flag.Duration("grace", 30*24*time.Hour, "grace period before hard delete")
    flag.Parse()

    dsn := os.Getenv("DATABASE_URL")
    if dsn == "" {
        slog.Error("DATABASE_URL unset")
        os.Exit(2)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

    pool, err := pgxpool.New(ctx, dsn)
    if err != nil {
        slog.Error("pool", "err", err)
        os.Exit(1)
    }
    defer pool.Close()

    r, err := cleanup.Run(ctx, pool, *grace)
    if err != nil {
        slog.Error("sweeper.run", "err", err)
        os.Exit(1)
    }
    slog.Info("sweeper.done", "deleted", r.DeletedCount, "ids", r.DeletedIDs)
}
```

- [ ] **Step 4: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/jobs/cleanup/...
go build ./...
go build -o /tmp/folio-sweeper ./cmd/folio-sweeper
/tmp/folio-sweeper -grace 30d 2>&1 | head -5 || true  # smoke test
```

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/jobs/cleanup/ backend/cmd/folio-sweeper/
git commit -m "$(cat <<'EOF'
feat(jobs): add tenant soft-delete sweeper

cleanup.Run hard-deletes tenants whose deleted_at exceeds the grace
period (default 30 days). Ships as the standalone folio-sweeper binary
so cron-based self-hosted deployments work without River; plan 3 adds
a River periodic job that calls cleanup.Run on the same schedule.
EOF
)"
```

---

## Task 14: Remove legacy `httpx.RequireTenant` once plan-1's auth middleware is in place

**Spec:** §12 rollout — `httpx.RequireTenant` is deleted.

**Files:**
- Modify: `backend/internal/httpx/httpx.go` — delete `RequireTenant` and context helpers
- Modify: call sites that still referenced the old header auth
- Modify: `.env.example` — remove `X-Tenant-ID` documentation

- [ ] **Step 1: Grep for remaining `httpx.RequireTenant` usages**

```bash
cd /Users/xmedavid/dev/folio/backend
rg 'httpx\.RequireTenant|X-Tenant-ID|httpx\.TenantIDFrom' .
```

Every hit must be replaced by `auth.RequireSession` + `auth.RequireMembership` + `auth.TenantFromCtx`. Existing accounts/transactions/classification handlers that read `httpx.TenantIDFrom` are migrated to `auth.TenantFromCtx`. Frontend code must not send `X-Tenant-ID` headers (it didn't since plan 1).

- [ ] **Step 2: Delete the middleware and helpers**

In `backend/internal/httpx/httpx.go`, remove:

- `RequireTenant`
- `TenantIDFrom`, `UserIDFrom`, `WithTenantID`, `WithUserID`
- the `tenantIDKey`, `userIDKey` constants

Keep `WriteJSON`, `WriteError`, `ValidationError`, `NotFoundError`, `WriteServiceError`.

Replace the package doc comment:

```go
// Package httpx contains small HTTP helpers shared across feature packages:
// typed errors and JSON response writers. Authentication lives in
// backend/internal/auth.
```

- [ ] **Step 3: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go build ./...
go test ./...
go vet ./...
```

Expected: clean build + green tests. Any residual caller of the deleted symbols surfaces at compile time.

- [ ] **Step 4: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/httpx/httpx.go backend/internal/accounts/ \
        backend/internal/transactions/ backend/internal/classification/ \
        .env.example
git commit -m "$(cat <<'EOF'
refactor(httpx): drop RequireTenant and legacy context helpers

Replaces every call site with auth.RequireSession + auth.RequireMembership
and auth.TenantFromCtx / auth.UserFromCtx. httpx is now purely
response / error helpers.
EOF
)"
```

---

## Task 15: Frontend — `/t/[slug]/settings/tenant` page (rename, slug, base currency, soft-delete)

**Spec:** §13 web surface.

**Files:**
- Create: `web/app/t/[slug]/settings/tenant/page.tsx`
- Create: `web/lib/hooks/use-tenant-settings.ts`
- Modify: `web/lib/api/client.ts` — add `patchTenant`, `deleteTenant`, `restoreTenant` wrappers

- [ ] **Step 1: Design compliance gate**

Read `/Users/xmedavid/dev/folio/.claude/skills/folio-frontend-design/SKILL.md` **before writing any JSX**. Every form pattern below MUST match its guidance.

- [ ] **Step 2: API client wrappers**

```ts
// web/lib/api/client.ts — append
export async function patchTenant(tenantID: string, body: {
  name?: string; slug?: string; baseCurrency?: string;
  cycleAnchorDay?: number; locale?: string; timezone?: string;
}) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}`, {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw await toApiError(res);
  return (await res.json()) as Tenant;
}

export async function deleteTenant(tenantID: string) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}`, {
    method: "DELETE",
    credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw await toApiError(res);
}

export async function restoreTenant(tenantID: string) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}/restore`, {
    method: "POST",
    credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw await toApiError(res);
  return (await res.json()) as Tenant;
}
```

`toApiError` parses `{error, code}` and throws a tagged `ApiError` whose `code === "reauth_required"` surfaces the re-auth modal (stub until plan 4 — show a neutral message).

- [ ] **Step 3: Page component**

```tsx
// web/app/t/[slug]/settings/tenant/page.tsx
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useIdentity } from "@/lib/hooks/use-identity";
import { patchTenant, deleteTenant } from "@/lib/api/client";
import { FormField } from "@/components/forms/form-field";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Button } from "@/components/ui/button";
import { useState } from "react";

export default function TenantSettingsPage({ params }: { params: { slug: string } }) {
  const { data: me } = useIdentity();
  const tenant = me?.tenants.find((t) => t.slug === params.slug);
  const qc = useQueryClient();
  const router = useRouter();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const patch = useMutation({
    mutationFn: (body: Parameters<typeof patchTenant>[1]) =>
      patchTenant(tenant!.id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
  const softDelete = useMutation({
    mutationFn: () => deleteTenant(tenant!.id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["me"] }); router.push("/tenants"); },
  });

  if (!tenant) return null;
  const isOwner = tenant.role === "owner";

  return (
    <section className="space-y-8 max-w-2xl">
      <header>
        <h1 className="text-2xl font-semibold">Tenant settings</h1>
        <p className="text-sm text-muted-foreground">
          {isOwner ? "You are an owner of this tenant." : "Only owners can edit settings."}
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          patch.mutate({
            name: (f.get("name") as string) || undefined,
            slug: (f.get("slug") as string) || undefined,
            baseCurrency: (f.get("baseCurrency") as string) || undefined,
            cycleAnchorDay: Number(f.get("cycleAnchorDay")) || undefined,
          });
        }}
        className="space-y-4"
      >
        <FormField label="Name" name="name" defaultValue={tenant.name} disabled={!isOwner} required />
        <FormField label="Slug" name="slug" defaultValue={tenant.slug} disabled={!isOwner}
                   pattern="^[a-z0-9][a-z0-9-]{1,62}$" />
        <FormField label="Base currency" name="baseCurrency" defaultValue={tenant.baseCurrency}
                   disabled={!isOwner} pattern="^[A-Z0-9]{3,10}$" />
        <FormField label="Cycle anchor day" name="cycleAnchorDay" type="number"
                   min={1} max={31} defaultValue={String(tenant.cycleAnchorDay)}
                   disabled={!isOwner} />
        {patch.error && <ApiError error={patch.error} />}
        <Button type="submit" disabled={!isOwner || patch.isPending}>
          {patch.isPending ? "Saving…" : "Save"}
        </Button>
      </form>

      {isOwner && (
        <section className="border-t pt-6 space-y-2">
          <h2 className="text-lg font-semibold text-destructive">Danger zone</h2>
          <p className="text-sm text-muted-foreground">
            Soft-deletes the tenant. You have 30 days to restore before data is
            permanently removed.
          </p>
          <Button variant="destructive" onClick={() => setConfirmDelete(true)}>
            Delete tenant
          </Button>
          <ConfirmDialog
            open={confirmDelete}
            onOpenChange={setConfirmDelete}
            title={`Delete ${tenant.name}?`}
            description="You have 30 days to restore before data is permanently removed."
            confirmLabel="Delete tenant"
            onConfirm={() => softDelete.mutate()}
          />
        </section>
      )}
    </section>
  );
}
```

`FormField`, `Button`, `ConfirmDialog`, and `ApiError` are the design-system components already shipped in plan 1's `web/components/*`.

- [ ] **Step 4: Verify**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm build
```

- [ ] **Step 5: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add web/app/t/[slug]/settings/tenant/ web/lib/api/client.ts
git commit -m "$(cat <<'EOF'
feat(web): tenant settings page with rename / slug / soft-delete

Read-only for non-owners; owners see a danger zone for soft-delete.
Form posts PATCH /t/{id}; delete routes to /tenants on success. Surfaces
reauth_required as a pending modal — plan 4 lands the actual prompt.
EOF
)"
```

---

## Task 16: Frontend — `/t/[slug]/settings/members` page

**Spec:** §13.

**Files:**
- Create: `web/app/t/[slug]/settings/members/page.tsx`
- Modify: `web/lib/api/client.ts` — add `getMembers`, `patchMember`, `removeMember`

- [ ] **Step 1: API client**

```ts
export async function getMembers(tenantID: string) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}/members`, {
    credentials: "include",
  });
  if (!res.ok) throw await toApiError(res);
  return (await res.json()) as {
    members: { userId: string; email: string; displayName: string; role: "owner" | "member"; createdAt: string }[];
    pendingInvites: { id: string; email: string; role: "owner" | "member"; expiresAt: string }[];
  };
}

export async function patchMember(tenantID: string, userID: string, role: "owner" | "member") {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}/members/${userID}`, {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
    body: JSON.stringify({ role }),
  });
  if (!res.ok) throw await toApiError(res);
}

export async function removeMember(tenantID: string, userID: string) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}/members/${userID}`, {
    method: "DELETE",
    credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw await toApiError(res);
}
```

- [ ] **Step 2: Page**

```tsx
// web/app/t/[slug]/settings/members/page.tsx
"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useIdentity } from "@/lib/hooks/use-identity";
import { getMembers, patchMember, removeMember } from "@/lib/api/client";
import { DataTable } from "@/components/ui/data-table";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { ApiError } from "@/components/ui/api-error";

export default function MembersPage({ params }: { params: { slug: string } }) {
  const { data: me } = useIdentity();
  const tenant = me?.tenants.find((t) => t.slug === params.slug);
  const qc = useQueryClient();
  const membersQ = useQuery({
    queryKey: ["members", tenant?.id],
    queryFn: () => getMembers(tenant!.id),
    enabled: !!tenant,
  });
  const change = useMutation({
    mutationFn: ({ userID, role }: { userID: string; role: "owner" | "member" }) =>
      patchMember(tenant!.id, userID, role),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["members", tenant?.id] }),
  });
  const remove = useMutation({
    mutationFn: (userID: string) => removeMember(tenant!.id, userID),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["members", tenant?.id] }),
  });

  if (!tenant || !membersQ.data) return null;
  const isOwner = tenant.role === "owner";

  return (
    <section className="space-y-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Members</h1>
      </header>
      <DataTable
        rows={membersQ.data.members}
        columns={[
          { key: "displayName", header: "Name" },
          { key: "email", header: "Email" },
          {
            key: "role",
            header: "Role",
            cell: (m) => isOwner ? (
              <Select
                value={m.role}
                onChange={(e) => change.mutate({ userID: m.userId, role: e.target.value as any })}
                options={[{ value: "owner", label: "Owner" }, { value: "member", label: "Member" }]}
              />
            ) : <span>{m.role}</span>,
          },
          {
            key: "actions",
            header: "",
            cell: (m) => (
              <Button
                variant="destructive"
                onClick={() => remove.mutate(m.userId)}
                aria-label={m.userId === me!.user.id ? "Leave tenant" : "Remove member"}
              >
                {m.userId === me!.user.id ? "Leave" : isOwner ? "Remove" : ""}
              </Button>
            ),
          },
        ]}
      />
      {change.error && <ApiError error={change.error} />}
      {remove.error && <ApiError error={remove.error} />}
    </section>
  );
}
```

`ApiError` surfaces `last_owner` / `last_tenant` / `reauth_required` codes with helpful copy.

- [ ] **Step 3: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck && pnpm lint && pnpm build
```

```bash
cd /Users/xmedavid/dev/folio
git add web/app/t/[slug]/settings/members/ web/lib/api/client.ts
git commit -m "$(cat <<'EOF'
feat(web): members settings page with role change / remove / leave

Role dropdown is owner-only; remove is owner-only; every member can
leave (row shows "Leave" for userId == self). ApiError surfaces
last_owner / last_tenant / reauth_required codes with actionable copy.
EOF
)"
```

---

## Task 17: Frontend — `/t/[slug]/settings/invites` page

**Spec:** §13.

**Files:**
- Create: `web/app/t/[slug]/settings/invites/page.tsx`
- Create: `web/components/invites/new-invite-dialog.tsx`
- Modify: `web/lib/api/client.ts` — add `createInvite`, `revokeInvite`

- [ ] **Step 1: API client**

```ts
export async function createInvite(tenantID: string, body: { email: string; role: "owner" | "member" }) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}/invites`, {
    method: "POST", credentials: "include",
    headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw await toApiError(res);
  return await res.json();
}

export async function revokeInvite(tenantID: string, inviteID: string) {
  const res = await fetch(`${API_BASE}/api/v1/t/${tenantID}/invites/${inviteID}`, {
    method: "DELETE", credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw await toApiError(res);
}
```

- [ ] **Step 2: Dialog + page**

```tsx
// web/components/invites/new-invite-dialog.tsx
"use client";
import { Dialog } from "@/components/ui/dialog";
import { FormField } from "@/components/forms/form-field";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { useState } from "react";

export function NewInviteDialog({
  open, onOpenChange, canInviteOwner, onSubmit,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  canInviteOwner: boolean;
  onSubmit: (email: string, role: "owner" | "member") => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Invite a member">
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          setBusy(true);
          try {
            await onSubmit(f.get("email") as string, f.get("role") as any);
            onOpenChange(false);
          } finally { setBusy(false); }
        }}
        className="space-y-4"
      >
        <FormField label="Email" name="email" type="email" required />
        <Select
          name="role"
          label="Role"
          defaultValue="member"
          options={canInviteOwner
            ? [{ value: "member", label: "Member" }, { value: "owner", label: "Owner" }]
            : [{ value: "member", label: "Member" }]}
        />
        <Button type="submit" disabled={busy}>{busy ? "Sending…" : "Send invite"}</Button>
      </form>
    </Dialog>
  );
}
```

```tsx
// web/app/t/[slug]/settings/invites/page.tsx
"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getMembers, createInvite, revokeInvite } from "@/lib/api/client";
import { useIdentity } from "@/lib/hooks/use-identity";
import { NewInviteDialog } from "@/components/invites/new-invite-dialog";
import { DataTable } from "@/components/ui/data-table";
import { Button } from "@/components/ui/button";
import { useState } from "react";
import { formatDate } from "@/lib/format";

export default function InvitesPage({ params }: { params: { slug: string } }) {
  const { data: me } = useIdentity();
  const tenant = me?.tenants.find((t) => t.slug === params.slug);
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["members", tenant?.id],
    queryFn: () => getMembers(tenant!.id),
    enabled: !!tenant,
  });
  const create = useMutation({
    mutationFn: (body: { email: string; role: "owner" | "member" }) => createInvite(tenant!.id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["members", tenant?.id] }),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => revokeInvite(tenant!.id, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["members", tenant?.id] }),
  });
  const [dlg, setDlg] = useState(false);
  if (!tenant || !q.data) return null;

  return (
    <section className="space-y-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Pending invites</h1>
        <Button onClick={() => setDlg(true)}>New invite</Button>
      </header>

      <DataTable
        rows={q.data.pendingInvites}
        columns={[
          { key: "email", header: "Email" },
          { key: "role", header: "Role" },
          { key: "expiresAt", header: "Expires", cell: (i) => formatDate(i.expiresAt) },
          { key: "actions", header: "", cell: (i) => (
            <Button variant="destructive" onClick={() => revoke.mutate(i.id)}>Revoke</Button>
          ) },
        ]}
        emptyMessage="No pending invites."
      />

      <NewInviteDialog
        open={dlg}
        onOpenChange={setDlg}
        canInviteOwner={tenant.role === "owner"}
        onSubmit={async (email, role) => { await create.mutateAsync({ email, role }); }}
      />
    </section>
  );
}
```

- [ ] **Step 3: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck && pnpm lint && pnpm build
```

```bash
cd /Users/xmedavid/dev/folio
git add web/app/t/[slug]/settings/invites/ \
        web/components/invites/ \
        web/lib/api/client.ts
git commit -m "$(cat <<'EOF'
feat(web): invites settings page with create + revoke

New-invite dialog restricts the role dropdown to member for
non-owners. Pending invites table shows email, role, expiry, and a
per-row Revoke button. Revocation is idempotent; success invalidates
the shared members query.
EOF
)"
```

---

## Task 18: Frontend — `/accept-invite/[token]` page

**Spec:** §4.2 preview + accept; §13.

**Files:**
- Create: `web/app/accept-invite/[token]/page.tsx`
- Modify: `web/lib/api/client.ts` — add `previewInvite`, `acceptInvite`

- [ ] **Step 1: API client**

```ts
export async function previewInvite(token: string) {
  const res = await fetch(`${API_BASE}/api/v1/auth/invites/${encodeURIComponent(token)}`);
  if (!res.ok) throw await toApiError(res);
  return await res.json() as {
    tenantID: string; tenantName: string; tenantSlug: string;
    inviterDisplayName: string; email: string;
    role: "owner" | "member"; expiresAt: string;
  };
}

export async function acceptInvite(token: string) {
  const res = await fetch(`${API_BASE}/api/v1/auth/invites/${encodeURIComponent(token)}/accept`, {
    method: "POST", credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw await toApiError(res);
  return await res.json() as { tenantId: string; userId: string; role: string };
}
```

- [ ] **Step 2: Page**

```tsx
// web/app/accept-invite/[token]/page.tsx
"use client";

import { use, useEffect } from "react";
import { useRouter } from "next/navigation";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { previewInvite, acceptInvite } from "@/lib/api/client";
import { useIdentity } from "@/lib/hooks/use-identity";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/components/ui/api-error";
import { Card } from "@/components/ui/card";

export default function AcceptInvitePage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params);
  const router = useRouter();
  const qc = useQueryClient();
  const { data: me, isLoading: meLoading } = useIdentity();
  const preview = useQuery({
    queryKey: ["invite-preview", token],
    queryFn: () => previewInvite(token),
    retry: false,
  });
  const accept = useMutation({
    mutationFn: () => acceptInvite(token),
    onSuccess: async (res) => {
      await qc.invalidateQueries({ queryKey: ["me"] });
      // Resolve slug from the /me payload after invalidation.
      const fresh = qc.getQueryData<{ tenants: { id: string; slug: string }[] }>(["me"]);
      const slug = fresh?.tenants.find((t) => t.id === res.tenantId)?.slug ?? "";
      router.push(`/t/${slug}`);
    },
  });

  // Redirect to /signup with inviteToken preserved when not authenticated.
  useEffect(() => {
    if (meLoading) return;
    if (!me && preview.data) {
      router.replace(`/signup?inviteToken=${encodeURIComponent(token)}&email=${encodeURIComponent(preview.data.email)}`);
    }
  }, [me, meLoading, preview.data, router, token]);

  if (preview.isLoading || meLoading) return <Card>Loading…</Card>;
  if (preview.error) return <ApiError error={preview.error} />;
  if (!me || !preview.data) return null;

  const emailMatches = me.user.email.toLowerCase() === preview.data.email.toLowerCase();

  return (
    <Card className="max-w-md mx-auto space-y-4">
      <h1 className="text-xl font-semibold">Join {preview.data.tenantName}</h1>
      <dl className="text-sm space-y-1">
        <dt className="inline font-medium">Invited by </dt>
        <dd className="inline">{preview.data.inviterDisplayName}</dd>
        <br />
        <dt className="inline font-medium">Role </dt>
        <dd className="inline">{preview.data.role}</dd>
        <br />
        <dt className="inline font-medium">Invite email </dt>
        <dd className="inline">{preview.data.email}</dd>
      </dl>

      {!emailMatches && (
        <ApiError error={{ code: "email_mismatch",
          message: `This invite was sent to ${preview.data.email} but you're signed in as ${me.user.email}.` }} />
      )}

      <Button disabled={!emailMatches || accept.isPending} onClick={() => accept.mutate()}>
        {accept.isPending ? "Joining…" : `Join ${preview.data.tenantName}`}
      </Button>
      {accept.error && <ApiError error={accept.error} />}
    </Card>
  );
}
```

- [ ] **Step 3: Update `/signup` page to read `inviteToken` + `email` from URL**

Minimal patch to plan 1's `/signup` page component:

```tsx
// In plan 1's web/app/signup/page.tsx
import { useSearchParams } from "next/navigation";
// inside component
const params = useSearchParams();
const inviteToken = params.get("inviteToken") ?? "";
const presetEmail = params.get("email") ?? "";
// …pass inviteToken into the signup mutation body; preset/lock the email input
```

- [ ] **Step 4: Verify & commit**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck && pnpm lint && pnpm build
```

```bash
cd /Users/xmedavid/dev/folio
git add web/app/accept-invite/ web/app/signup/ web/lib/api/client.ts
git commit -m "$(cat <<'EOF'
feat(web): accept-invite flow with preview, signup fallback, accept

/accept-invite/[token] previews the invite without auth and either
redirects unauthed users to /signup?inviteToken=… or renders a Join
button for signed-in users whose email matches. Signup page reads
inviteToken + email from the URL so the backend completes the
membership during signup.
EOF
)"
```

---

## Task 19: End-to-end smoke test for the invite round-trip

**Spec:** §3.4 invariants — verify the full wire path.

**Files:**
- Create: `backend/internal/identity/e2e_invite_test.go`

- [ ] **Step 1: Write the test**

```go
package identity_test

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/xmedavid/folio/backend/internal/identity"
    "github.com/xmedavid/folio/backend/internal/mailer"
    "github.com/xmedavid/folio/backend/internal/testdb"
)

// The full flow: Alice signs up, creates an invite for Bob, Bob
// previews it, signs up consuming the token, and is a member of
// Alice's tenant + his new Personal tenant.
func TestE2E_InviteRoundTrip(t *testing.T) {
    pool := testdb.Pool(t)
    mockMail := mailer.NewLogMailer(nil)
    srv := newTestServerWithMailer(t, pool, mockMail)

    // 1. Alice signs up (plan 1 handler).
    post(t, srv, "/api/v1/auth/signup", `{
      "email":"alice@example.com","displayName":"Alice",
      "password":"correcthorsebatterystaple","tenantName":"Alice"
    }`, 201)
    aliceCookie := lastSessionCookie(t, srv, "alice@example.com", "correcthorsebatterystaple")

    // 2. Resolve Alice's tenant id from /me.
    var me struct {
        User    struct{ ID string `json:"id"` }
        Tenants []struct{ ID string; Role string }
    }
    mustGetJSON(t, srv, "/api/v1/me", aliceCookie, &me)
    tenantID := me.Tenants[0].ID

    // Fresh reauth so owner-gated invite create works.
    _ = pool.QueryRow(context.Background(),
        `update sessions set reauth_at = now() where user_id = $1`, me.User.ID)

    // 3. Alice creates an invite for Bob.
    body, status := post(t, srv, "/api/v1/t/"+tenantID+"/invites",
        `{"email":"bob@example.com","role":"member"}`, 201, withCookie(aliceCookie))
    _ = status
    var inv struct {
        ID string `json:"id"`; Email string
    }
    _ = json.Unmarshal(body, &inv)

    // 4. Mailer captured one message; pull the plaintext token out of its URL.
    if len(mockMail.Sent()) != 1 { t.Fatal("expected 1 email") }
    plaintext := extractToken(t, mockMail.Sent()[0].Data["inviteURL"].(string))

    // 5. Unauthenticated preview works.
    var preview map[string]any
    mustGetJSON(t, srv, "/api/v1/auth/invites/"+plaintext, "", &preview)
    if preview["tenantName"] != "Alice" {
        t.Fatalf("preview tenantName: %v", preview["tenantName"])
    }

    // 6. Bob signs up with the invite.
    post(t, srv, "/api/v1/auth/signup", `{
      "email":"bob@example.com","displayName":"Bob",
      "password":"correcthorsebatterystaple","tenantName":"Bob",
      "inviteToken":"`+plaintext+`"
    }`, 201)

    // 7. Bob has 2 memberships.
    bobID := lookupUserID(t, pool, "bob@example.com")
    var count int
    _ = pool.QueryRow(context.Background(),
        `select count(*) from tenant_memberships where user_id = $1`, bobID).Scan(&count)
    if count != 2 {
        t.Fatalf("want 2 memberships, got %d", count)
    }

    // 8. Invite is consumed.
    var accepted *time.Time
    _ = pool.QueryRow(context.Background(),
        `select accepted_at from tenant_invites where email = 'bob@example.com'`).Scan(&accepted)
    if accepted == nil {
        t.Fatal("invite not accepted")
    }

    _ = bytes.Buffer{}; _ = http.MethodPost; _ = httptest.NewRequest; _ = time.Now() // keep imports
}
```

`newTestServerWithMailer`, `post`, `lastSessionCookie`, `mustGetJSON`, `extractToken`, `lookupUserID` live in `backend/internal/testdb` (extend the file from Task 2).

- [ ] **Step 2: Verify**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./internal/identity/... -run TestE2E_InviteRoundTrip -v
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/internal/identity/e2e_invite_test.go backend/internal/testdb/
git commit -m "$(cat <<'EOF'
test(invites): end-to-end round-trip covering signup + invite + accept

Exercises the full wire path: Alice creates an invite, the stub mailer
captures the URL, unauthenticated preview works, Bob signs up with
the invite and ends up with two memberships, invite is marked
accepted. Plan 3's River implementation must keep this test green.
EOF
)"
```

---

## Task 20: Final verification

**Files:** none changed.

- [ ] **Step 1: Full test suite**

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./...
go vet ./...
go build ./...
go build -o /tmp/folio-sweeper ./cmd/folio-sweeper
```

Expected: exit 0 on every command.

- [ ] **Step 2: Frontend**

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
pnpm build
```

Expected: exit 0.

- [ ] **Step 3: Spot-check routes are mounted**

```bash
cd /Users/xmedavid/dev/folio/backend
go run ./cmd/folio &
sleep 1
curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/v1/auth/invites/abc123
# expect 410 or 404 (no such token)
curl -sS -o /dev/null -w "%{http_code}\n" -X PATCH http://localhost:8080/api/v1/t/$(uuidgen)
# expect 401 (no session) — confirms the route is mounted and RequireSession runs first
kill %1
```

- [ ] **Step 4: Confirm the canonical functions exist for plan 3+**

```bash
cd /Users/xmedavid/dev/folio/backend
rg -l 'UpdateTenant|SoftDeleteTenant|RestoreTenant|ChangeRole|RemoveMember|LeaveTenant' internal/identity/
rg -l 'InviteService.*Create|InviteService.*Preview|InviteService.*Accept|InviteService.*Revoke' internal/identity/
rg -l 'mailer\.Mailer|mailer\.LogMailer' internal/
```

Expected: each grep lists at least one source file (the names are load-bearing for plan 3).

- [ ] **Step 5: Confirm clean tree**

```bash
cd /Users/xmedavid/dev/folio
git status
```

Expected: clean (all prior tasks committed their own work).

---

## Self-review checklist (run after writing all tasks, before declaring done)

- [ ] Every spec section in scope (§3.4 invariants, §4.2 invite routes, §4.4 tenant-scoped routes, §8.3 audit events, §10 sweeper, §13 web pages) has at least one task.
- [ ] Canonical function names (`UpdateTenant`, `SoftDeleteTenant`, `RestoreTenant`, `ChangeRole`, `RemoveMember`, `LeaveTenant`, `InviteService.Create`, `InviteService.Preview`, `InviteService.Accept`, `InviteService.Revoke`, `mailer.Mailer`, `mailer.LogMailer`) appear exactly as named.
- [ ] Every task has a commit step using Conventional Commits scopes (`feat(tenants)`, `feat(invites)`, `feat(mailer)`, `feat(jobs)`, `feat(web)`, `refactor(httpx)`, `test(invites)`).
- [ ] No "TODO", "TBD", "similar to Task N", "pseudocode", or "add appropriate error handling" strings remain.
- [ ] Every code block is real code the implementer can paste and compile, not sketches.
- [ ] Fresh-reauth middleware is mounted on every spec-required route and the stub-period caveat is documented in §0.2 and Task 9.
- [ ] Plan 3's dependency on the sweeper is satisfied by `folio-sweeper` (Task 13) — no River-adjacent placeholders.
- [ ] Web pages follow the folio-frontend-design skill: Task 15's step 1 enforces the pre-read.
- [ ] Task count: 20 (in range 20–30).
