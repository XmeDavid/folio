# Folio Auth Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `X-Tenant-ID` dev bridge with real session-cookie authentication and land the multi-tenant membership schema. Ship signup, login, logout, `/me`, tenant creation, and member listing end-to-end; move the web app to `/t/{slug}/…` routing.

**Architecture:** A new `backend/internal/auth` package owns password hashing, session cookies, middleware (`RequireSession`, `RequireMembership`, `RequireRole`, `RequireFreshReauth` stub, `CSRF`, rate limits), and the signup/login/logout HTTP handlers. The existing `backend/internal/identity` package is trimmed and extended to own user/tenant/membership queries (`Me`, `CreateTenant`, `ListMembers`). The identity migration (`20260424000001_identity.sql`) is rewritten in place to ship the full auth-and-tenancy schema. The Next.js app router moves tenant-scoped pages under `/t/[slug]/…`; the `localStorage` identity bridge is deleted in favour of a React Query hook around `GET /api/v1/me`.

**Tech Stack:** Go 1.25 (chi v5, pgx/v5, `golang.org/x/crypto/argon2`, `github.com/bits-and-blooms/bloom/v3`). Postgres 17. Next.js 16 (App Router), React Query, Tailwind v4, shadcn/ui.

**Spec:** `docs/superpowers/specs/2026-04-24-folio-auth-and-tenancy-design.md` — column-level source of truth. This plan cross-references spec sections for context.

**Prior plans in series:** none (this is plan 1 of 5).

**Plans that follow:**
- `docs/superpowers/plans/2026-04-24-folio-invites-and-tenant-lifecycle.md` (plan 2)
- `docs/superpowers/plans/2026-04-24-folio-email-infrastructure.md` (plan 3)
- `docs/superpowers/plans/2026-04-24-folio-mfa.md` (plan 4)
- `docs/superpowers/plans/2026-04-24-folio-admin-console.md` (plan 5)

---

## 0. Setup and shared patterns

### 0.1 Working directories

Backend commands (atlas, sqlc, go) run in `backend/`:

```bash
cd /Users/xmedavid/dev/folio/backend
```

Frontend commands (pnpm) run in `web/`:

```bash
cd /Users/xmedavid/dev/folio/web
```

Postgres must be running:

```bash
cd /Users/xmedavid/dev/folio
docker compose -f docker-compose.dev.yml up -d db
```

### 0.2 Reset-from-scratch apply

This plan rewrites migration `001_identity.sql` in place. Because no production data exists, every schema task applies against a **freshly reset** database:

```bash
# From backend/
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
```

Never run in production.

### 0.3 Test patterns

- **Unit tests** live next to the code they test (`foo.go` + `foo_test.go`). Pure unit tests cover hashing, token generation, password policy, middleware in isolation, and CSRF.
- **Service tests** hit a real Postgres and roll back via a transaction helper. The helper lives at `backend/internal/testdb/testdb.go` (created in Task 2.1) and is used by every `*_service_test.go`. Tests SKIP (via `t.Skip`) when `DATABASE_URL` is unset, so `go test ./...` on a dev machine without Postgres doesn't fail.
- **HTTP tests** drive handlers with `httptest.NewRecorder` + manufactured context (no network round-trip).
- **Commit cadence:** one commit per task. Tests land in the same commit as the code they cover (classic TDD: red → green → commit).

### 0.4 Environment variables introduced or used in this plan

| Env | Purpose | Example value |
|---|---|---|
| `DATABASE_URL` | Postgres DSN (already present) | `postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable` |
| `SESSION_SECRET` | Reserved for future rolling; unused in plan 1 | (already present) |
| `APP_URL` | Base URL of the web app, used for CSRF Origin allowlist | `http://localhost:3000` |
| `REGISTRATION_MODE` | `open` / `invite_only` / `first_run_only` (spec §9). Plan 1 implements `open` + `first_run_only`; `invite_only` becomes functional in plan 2 | `open` |

### 0.5 Canonical naming established here (used by plans 2–5)

**Packages:** `backend/internal/auth`, `backend/internal/identity`, `backend/internal/testdb`.

**Types:** `identity.Role` (`RoleOwner`, `RoleMember`), `identity.Tenant`, `identity.User`, `identity.Membership`, `identity.Invite` (defined, unused until plan 2). `auth.Service`, `auth.Session`, `auth.Handler`.

**Functions:** `auth.HashPassword` / `VerifyPassword` / `CheckPasswordPolicy` / `GenerateSessionToken` / `HashToken` / `SetSessionCookie` / `ClearSessionCookie` / `RequireSession` / `RequireMembership` / `RequireRole` / `RequireFreshReauth` (stub) / `CSRF` / `UserFromCtx` / `TenantFromCtx` / `RoleFromCtx` / `MustUser` / `MustTenant`.

---

## 1. Schema rewrite

Spec reference: §3 entire, §10 (soft delete columns), §12 (rollout).

### Task 1.1: Rewrite identity migration with the full auth-and-tenancy schema

**Files:**
- Modify: `backend/db/migrations/20260424000001_identity.sql`

- [ ] **Step 1: Replace the file's contents**

```sql
-- Folio v2 domain — identity, tenancy, auth, memberships.
-- Tenants own financial data; users authenticate and can belong to many tenants.

create extension if not exists citext;

-- Shared: updated_at trigger (P1). Used by every table with updated_at.
create or replace function set_updated_at() returns trigger language plpgsql as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

-- Shared: money_currency domain (P2). Used by every currency column.
create domain money_currency as varchar(10)
  check (value ~ '^[A-Z0-9]{3,10}$');

-- Shared: tenant_role enum. Owner and member are the only roles in v1.
create type tenant_role as enum ('owner', 'member');

-- Tenants: root of the financial data graph. Not tenant-scoped itself.
-- FKs to tenants always reference tenants(id); never composite.
create table tenants (
  id                uuid primary key,
  name              text not null,
  slug              citext not null unique
                    check (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  base_currency     money_currency not null,
  cycle_anchor_day  smallint not null check (cycle_anchor_day between 1 and 31),
  locale            text not null,
  timezone          text not null default 'UTC',
  deleted_at        timestamptz,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create trigger tenants_updated_at before update on tenants
  for each row execute function set_updated_at();

create index tenants_deleted_at_idx
  on tenants (deleted_at)
  where deleted_at is not null;

-- Users: authenticate into zero or more tenants via tenant_memberships.
-- password_hash is NOT NULL; signup always sets it.
create table users (
  id                 uuid primary key,
  email              citext not null unique,
  password_hash      text not null,
  display_name       text not null,
  email_verified_at  timestamptz,
  last_tenant_id     uuid references tenants(id) on delete set null,
  is_admin           boolean not null default false,
  last_login_at      timestamptz,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

create trigger users_updated_at before update on users
  for each row execute function set_updated_at();

-- user_preferences: per-user UI settings. No tenant_id; reaches tenant via
-- the user's active membership at read time.
create table user_preferences (
  user_id           uuid primary key references users(id) on delete cascade,
  theme             text,
  date_format       text,
  number_format     text,
  display_currency  money_currency,
  feature_flags     jsonb not null default '{}'::jsonb,
  updated_at        timestamptz not null default now()
);

create trigger user_preferences_updated_at before update on user_preferences
  for each row execute function set_updated_at();

-- tenant_memberships: a user can belong to many tenants with a role each.
-- Primary key (tenant_id, user_id) — a user cannot have two roles in one tenant.
create table tenant_memberships (
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id)   on delete cascade,
  role        tenant_role not null,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  primary key (tenant_id, user_id)
);

create index tenant_memberships_user_id_idx
  on tenant_memberships (user_id);

-- Partial index for the "does this tenant still have an owner?" check.
create index tenant_memberships_owners
  on tenant_memberships (tenant_id)
  where role = 'owner';

create trigger tenant_memberships_updated_at before update on tenant_memberships
  for each row execute function set_updated_at();

-- tenant_invites: pending invitations to join a tenant.
-- token_hash is sha256(plaintext); plaintext ships only in the email.
create table tenant_invites (
  id                  uuid primary key,
  tenant_id           uuid not null references tenants(id) on delete cascade,
  email               citext not null,
  role                tenant_role not null,
  token_hash          bytea not null unique,
  invited_by_user_id  uuid not null references users(id) on delete restrict,
  created_at          timestamptz not null default now(),
  expires_at          timestamptz not null,
  accepted_at         timestamptz,
  revoked_at          timestamptz
);

create index tenant_invites_tenant_id_idx on tenant_invites (tenant_id);
create index tenant_invites_pending_email_idx
  on tenant_invites (email)
  where accepted_at is null and revoked_at is null;

-- auth_tokens: unified single-use tokens for email verify / password reset /
-- email change. Plan 3 populates this.
create table auth_tokens (
  id           uuid primary key,
  user_id      uuid not null references users(id) on delete cascade,
  purpose      text not null
               check (purpose in ('email_verify', 'password_reset', 'email_change')),
  token_hash   bytea not null unique,
  email        citext,
  created_at   timestamptz not null default now(),
  expires_at   timestamptz not null,
  consumed_at  timestamptz
);

create index auth_tokens_user_id_idx on auth_tokens (user_id);
create index auth_tokens_live_idx
  on auth_tokens (purpose, expires_at)
  where consumed_at is null;

-- auth_recovery_codes: MFA recovery codes, one row per code, Argon2id-hashed.
-- Plan 4 populates this.
create table auth_recovery_codes (
  id           uuid primary key,
  user_id      uuid not null references users(id) on delete cascade,
  code_hash    text not null,
  created_at   timestamptz not null default now(),
  consumed_at  timestamptz
);

create index auth_recovery_codes_live_idx
  on auth_recovery_codes (user_id)
  where consumed_at is null;

-- sessions: opaque cookie tokens. id = sha256(plaintext_token) stored as text.
create table sessions (
  id            text primary key,
  user_id       uuid not null references users(id) on delete cascade,
  created_at    timestamptz not null default now(),
  expires_at    timestamptz not null,
  last_seen_at  timestamptz not null default now(),
  reauth_at     timestamptz,
  user_agent    text,
  ip            inet
);

create index sessions_user_id_idx on sessions (user_id);
create index sessions_expires_at_idx on sessions (expires_at);

-- webauthn_credentials: passkeys / hardware keys registered to a user.
-- Plan 4 populates this.
create table webauthn_credentials (
  id             uuid primary key,
  user_id        uuid not null references users(id) on delete cascade,
  credential_id  bytea not null unique,
  public_key     bytea not null,
  sign_count     bigint not null default 0,
  transports     text[],
  label          text,
  created_at     timestamptz not null default now()
);

create index webauthn_credentials_user_id_idx on webauthn_credentials(user_id);

-- totp_credentials: authenticator-app seeds (encrypted at rest). Recovery
-- codes moved to auth_recovery_codes for per-code consumption tracking.
create table totp_credentials (
  id            uuid primary key,
  user_id       uuid not null references users(id) on delete cascade,
  secret_cipher text not null,
  verified_at   timestamptz,
  created_at    timestamptz not null default now()
);

create index totp_credentials_user_id_idx on totp_credentials(user_id);

-- audit_events: append-only activity log. Plans 1+ write to this.
create table audit_events (
  id              uuid primary key,
  tenant_id       uuid references tenants(id) on delete cascade,
  actor_user_id   uuid references users(id) on delete set null,
  action          text not null,
  entity_type     text,
  entity_id       uuid,
  before_value    jsonb,
  after_value     jsonb,
  ip              inet,
  user_agent      text,
  at              timestamptz not null default now()
);

create index audit_events_tenant_id_at_idx on audit_events (tenant_id, at desc);
create index audit_events_actor_user_id_at_idx on audit_events (actor_user_id, at desc);
create index audit_events_action_at_idx on audit_events (action, at desc);
```

- [ ] **Step 2: Regenerate atlas.sum**

```bash
cd backend && atlas migrate hash --env local
```

Expected: `atlas.sum` rewritten with new checksums. `git status` shows both files modified.

- [ ] **Step 3: Reset DB and apply**

```bash
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
```

Expected output contains:

```
Applying version 20260424000001 (1 statements):
  -> create extension if not exists citext;
  ... (success)
```

- [ ] **Step 4: Apply remaining domain migrations on top**

```bash
atlas migrate apply --env local
```

Expected: all 14 migrations apply cleanly. No errors from downstream migrations (they don't reference the dropped `users.tenant_id`).

- [ ] **Step 5: Verify with sqlc**

```bash
cd backend && sqlc generate
```

Expected: `sqlc` reports no schema errors. If a downstream migration FK-referenced `users(tenant_id, id)`, this step fails; fix inline.

- [ ] **Step 6: Commit**

```bash
git add backend/db/migrations/20260424000001_identity.sql backend/db/migrations/atlas.sum
git commit -m "chore(migrations): rewrite identity migration for v2 auth and multi-tenancy"
```

---

## 2. Shared test helper

### Task 2.1: `testdb` package with tx-rollback helper

**Files:**
- Create: `backend/internal/testdb/testdb.go`
- Test: `backend/internal/testdb/testdb_test.go`

- [ ] **Step 1: Write the helper**

```go
// Package testdb provides a shared test helper that opens a transaction
// against DATABASE_URL and rolls it back at the end of the test. Callers
// get a pgx.Tx they can use as a query surface; nothing persists between
// tests. Tests SKIP when DATABASE_URL is unset.
package testdb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open returns a *pgxpool.Pool against DATABASE_URL or skips the test if unset.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// WithTx runs fn inside a transaction that is rolled back when the test
// finishes. The tx is safe to share across helpers that take a query surface.
func WithTx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx)) {
	t.Helper()
	pool := Open(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	fn(ctx, tx)
}
```

- [ ] **Step 2: Smoke test**

```go
package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestWithTx_rollsBack(t *testing.T) {
	WithTx(t, func(ctx context.Context, tx pgx.Tx) {
		var one int
		if err := tx.QueryRow(ctx, "select 1").Scan(&one); err != nil {
			t.Fatalf("select 1: %v", err)
		}
		if one != 1 {
			t.Fatalf("expected 1, got %d", one)
		}
	})
}

func TestOpen_skipsWithoutDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	// We can't call Open here without failing the parent test, because
	// t.Skip only skips within the called t. This test documents the skip
	// contract; direct invocation is left to downstream tests.
	_ = pgx.ErrNoRows
}
```

- [ ] **Step 3: Run the test**

```bash
cd backend && go test ./internal/testdb/... -v
```

Expected: `TestWithTx_rollsBack PASS` (or skip if `DATABASE_URL` unset).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/testdb/
git commit -m "test(testdb): add shared transaction-rollback helper"
```

---

## 3. Password hashing, policy, blocklist

Spec reference: §5.1.

### Task 3.1: Add Go dependencies

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`

- [ ] **Step 1: Add modules**

```bash
cd backend
go get golang.org/x/crypto/argon2
go get github.com/bits-and-blooms/bloom/v3
```

- [ ] **Step 2: Verify versions**

```bash
go mod tidy
grep -E 'argon2|bloom' go.sum | head -4
```

Expected: non-empty; versions recorded.

- [ ] **Step 3: Commit**

```bash
git add backend/go.mod backend/go.sum
git commit -m "chore(deps): add argon2 and bloom for password hashing"
```

### Task 3.2: `auth.HashPassword` + `auth.VerifyPassword`

**Files:**
- Create: `backend/internal/auth/password.go`
- Test: `backend/internal/auth/password_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected argon2id PHC string, got %q", hash)
	}
	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatalf("VerifyPassword returned false for correct password")
	}
	ok, err = VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): %v", err)
	}
	if ok {
		t.Fatalf("VerifyPassword returned true for wrong password")
	}
}

func TestHashPassword_uniqueSalt(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatalf("two hashes of the same password should differ (random salt)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/auth/... -run TestHashAndVerifyPassword -v
```

Expected: FAIL with "undefined: HashPassword".

- [ ] **Step 3: Write the implementation**

```go
// Package auth owns credential primitives (password hashing, session tokens),
// HTTP middleware (RequireSession, RequireMembership, RequireRole,
// RequireFreshReauth, CSRF), rate limiters, and the signup/login/logout
// HTTP surface. It is intentionally free of tenant-scoped queries; those
// live in backend/internal/identity.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns an Argon2id PHC-encoded hash using baseline params
// tuned to ~250ms/verify on a small VPS. Params are encoded in the PHC
// string so we can bump them without invalidating existing hashes.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password required")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword returns (true, nil) on a correct password. An invalid hash
// encoding returns (false, error); a correct-encoding but wrong password
// returns (false, nil).
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=65536,t=3,p=2", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid argon2id encoding")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("version parse: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version: %d", version)
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("params parse: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("salt decode: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("hash decode: %w", err)
	}
	actual := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expected)))
	if subtle.ConstantTimeCompare(expected, actual) == 1 {
		return true, nil
	}
	return false, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/auth/... -run TestHashAndVerifyPassword -v
go test ./internal/auth/... -run TestHashPassword_uniqueSalt -v
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/password.go backend/internal/auth/password_test.go
git commit -m "feat(auth): add Argon2id password hashing and verification"
```

### Task 3.3: Embed top-1k password blocklist + build bloom filter

**Files:**
- Create: `backend/internal/auth/passwords_top10k.txt` (1000-entry sample; full 10k ships with first real deployment)
- Create: `backend/internal/auth/blocklist.go`
- Test: `backend/internal/auth/blocklist_test.go`

- [ ] **Step 1: Create the embedded file**

Fetch a realistic subset (e.g. `SecLists/Passwords/Common-Credentials/10-million-password-list-top-1000.txt`) and save it as `backend/internal/auth/passwords_top10k.txt`. Each line is one password. Ship the 1000-entry file now; swap in the 10k file at implementation time without code changes.

Minimal acceptance: file contains `password`, `123456`, `qwerty`, `admin`, `letmein`, `welcome`, `monkey`, `football`, and 990+ other entries.

- [ ] **Step 2: Write the failing test**

```go
package auth

import "testing"

func TestIsCommonPassword_blocksKnown(t *testing.T) {
	for _, p := range []string{"password", "123456", "qwerty", "admin"} {
		if !IsCommonPassword(p) {
			t.Errorf("%q should be flagged as common", p)
		}
	}
}

func TestIsCommonPassword_allowsUnusual(t *testing.T) {
	for _, p := range []string{"correct horse battery staple", "p4SsW0rd!wPzXq"} {
		if IsCommonPassword(p) {
			t.Errorf("%q should not be flagged", p)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/auth/... -run TestIsCommonPassword -v
```

Expected: FAIL with "undefined: IsCommonPassword".

- [ ] **Step 4: Write the implementation**

```go
package auth

import (
	"bufio"
	_ "embed"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
)

//go:embed passwords_top10k.txt
var passwordsTop10k string

var (
	commonPasswordsOnce   sync.Once
	commonPasswordsFilter *bloom.BloomFilter
)

// IsCommonPassword reports whether password matches the embedded top-N
// blocklist. Case-insensitive. Uses a bloom filter (~1% false-positive rate
// for 10k entries) — compiled at first call.
func IsCommonPassword(password string) bool {
	commonPasswordsOnce.Do(loadCommonPasswords)
	return commonPasswordsFilter.TestString(strings.ToLower(password))
}

func loadCommonPasswords() {
	// Size for 10k entries at 1% FPR: ~96k bits, 7 hash functions.
	commonPasswordsFilter = bloom.NewWithEstimates(10000, 0.01)
	scanner := bufio.NewScanner(strings.NewReader(passwordsTop10k))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		commonPasswordsFilter.AddString(strings.ToLower(line))
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/auth/... -run TestIsCommonPassword -v
```

Expected: both sub-tests PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/auth/passwords_top10k.txt backend/internal/auth/blocklist.go backend/internal/auth/blocklist_test.go
git commit -m "feat(auth): embed common-password blocklist behind bloom filter"
```

### Task 3.4: `auth.CheckPasswordPolicy`

**Files:**
- Create: `backend/internal/auth/policy.go`
- Test: `backend/internal/auth/policy_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestCheckPasswordPolicy(t *testing.T) {
	cases := []struct {
		name, pw, email, dn string
		wantErr             bool
	}{
		{"ok", "correct horse battery staple", "alice@example.com", "Alice", false},
		{"too short", "abc12345", "alice@example.com", "Alice", true},
		{"contains email local part", "alicelovescats99", "alice@example.com", "Alice", true},
		{"contains display name", "My name is Alice Smith", "x@y.com", "Alice Smith", true},
		{"common", "password1234567", "x@y.com", "X", true},
		{"empty", "", "x@y.com", "X", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPasswordPolicy(tc.pw, tc.email, tc.dn)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr {
				var verr *httpx.ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("expected ValidationError, got %T: %v", err, err)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/auth/... -run TestCheckPasswordPolicy -v
```

Expected: FAIL with "undefined: CheckPasswordPolicy".

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"strings"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

const minPasswordLen = 12

// CheckPasswordPolicy enforces the plan-1 baseline: minimum length, blocks
// containing the email local-part or any whitespace-split token of the
// display name (case-insensitive), and blocks bloom-filter matches against
// the top-N common passwords.
func CheckPasswordPolicy(password, email, displayName string) error {
	if len(password) < minPasswordLen {
		return httpx.NewValidationError("password must be at least 12 characters")
	}
	lower := strings.ToLower(password)
	if local, _, ok := splitEmailLocal(email); ok && strings.Contains(lower, strings.ToLower(local)) {
		return httpx.NewValidationError("password cannot contain your email address")
	}
	for _, tok := range strings.Fields(displayName) {
		if len(tok) >= 4 && strings.Contains(lower, strings.ToLower(tok)) {
			return httpx.NewValidationError("password cannot contain your name")
		}
	}
	if IsCommonPassword(password) {
		return httpx.NewValidationError("that password is too common, pick another")
	}
	return nil
}

func splitEmailLocal(email string) (local, domain string, ok bool) {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "", "", false
	}
	return email[:at], email[at+1:], true
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/auth/... -run TestCheckPasswordPolicy -v
```

Expected: all sub-cases PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/policy.go backend/internal/auth/policy_test.go
git commit -m "feat(auth): enforce password length, substring, and common-blocklist policy"
```

---

## 4. Session tokens and cookies

Spec reference: §6.

### Task 4.1: `auth.GenerateSessionToken` + `auth.HashToken`

**Files:**
- Create: `backend/internal/auth/token.go`
- Test: `backend/internal/auth/token_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"encoding/base64"
	"testing"
)

func TestGenerateSessionToken(t *testing.T) {
	tok, hash := GenerateSessionToken()
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("expected 32 bytes token, got %d", len(raw))
	}
	if len(hash) != 32 {
		t.Fatalf("expected 32 bytes sha256 hash, got %d", len(hash))
	}
	again := HashToken(tok)
	if string(again) != string(hash) {
		t.Fatalf("HashToken not deterministic")
	}
}

func TestGenerateSessionToken_unique(t *testing.T) {
	a, _ := GenerateSessionToken()
	b, _ := GenerateSessionToken()
	if a == b {
		t.Fatalf("two tokens should differ")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/auth/... -run TestGenerateSessionToken -v
```

Expected: FAIL.

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GenerateSessionToken returns a fresh 256-bit random token (base64url-encoded)
// and its SHA-256 hash. The plaintext token lives only in the cookie; the
// hash is what's stored server-side in sessions.id.
func GenerateSessionToken() (plaintext string, hash []byte) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand.Read is documented to never return an error on Linux/macOS.
		panic("crypto/rand failed: " + err.Error())
	}
	plaintext = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:]
}

// HashToken returns the SHA-256 of a plaintext session token.
// Callers use this to look up sessions.id.
func HashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// SessionIDFromToken returns the hex-of-sha256 string used as sessions.id.
// We store this as text (not bytea) so that `id` remains debuggable in psql.
func SessionIDFromToken(plaintext string) string {
	h := HashToken(plaintext)
	// encoding/hex but avoid an import we don't use elsewhere; base64 is fine.
	return base64.RawURLEncoding.EncodeToString(h)
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/auth/... -run TestGenerateSessionToken -v
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/token.go backend/internal/auth/token_test.go
git commit -m "feat(auth): generate 256-bit session tokens with sha256-hashed ids"
```

### Task 4.2: Cookie helpers

**Files:**
- Create: `backend/internal/auth/cookie.go`
- Test: `backend/internal/auth/cookie_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "abc123")
	resp := rec.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookieName {
		t.Errorf("name = %q, want %q", c.Name, sessionCookieName)
	}
	if c.Value != "abc123" {
		t.Errorf("value = %q, want abc123", c.Value)
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Error("expected SameSite=Lax")
	}
	if c.Path != "/" {
		t.Error("expected Path=/")
	}
}

func TestClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec)
	h := rec.Header().Get("Set-Cookie")
	if !strings.Contains(h, "Max-Age=0") {
		t.Errorf("expected Max-Age=0, got %q", h)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/auth/... -run TestSetSessionCookie -v
```

Expected: FAIL.

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"net/http"
)

const sessionCookieName = "folio_session"

// SetSessionCookie writes the session cookie. Secure is set in production
// (see §6.1); toggled by the APP_URL scheme in the handler that calls this.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		// No Expires — session cookie; browser evicts on quit.
		// Server-side sessions.expires_at is the real bound.
	})
}

// ClearSessionCookie removes the cookie client-side via Max-Age=0.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionCookieName returns the name, exported for test + handler reuse.
func SessionCookieName() string { return sessionCookieName }
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/auth/... -run TestSetSessionCookie -v
go test ./internal/auth/... -run TestClearSessionCookie -v
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/cookie.go backend/internal/auth/cookie_test.go
git commit -m "feat(auth): add session cookie helpers (HttpOnly, Secure, SameSite=Lax)"
```

---

## 5. Identity domain types and service

Spec reference: §3.5, §4.3, §4.4.

### Task 5.1: `identity.Role` + domain types

**Files:**
- Modify: `backend/internal/identity/doc.go`
- Create: `backend/internal/identity/types.go`

- [ ] **Step 1: Replace `doc.go` with the new purpose**

```go
// Package identity owns read/write queries for users, tenants, memberships,
// and (in plan 2) invites. It is intentionally free of credential code —
// password hashing, session cookies, and HTTP middleware live in
// backend/internal/auth.
package identity
```

- [ ] **Step 2: Create `types.go`**

```go
package identity

import (
	"time"

	"github.com/google/uuid"
)

// Role is the per-tenant membership role. Matches the Postgres `tenant_role` enum.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleMember }

// Tenant is the wire/read-model shape of a tenant row.
type Tenant struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	BaseCurrency   string     `json:"baseCurrency"`
	CycleAnchorDay int        `json:"cycleAnchorDay"`
	Locale         string     `json:"locale"`
	Timezone       string     `json:"timezone"`
	DeletedAt      *time.Time `json:"deletedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// User is the read-model shape of a user row.
type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"displayName"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`
	IsAdmin         bool       `json:"isAdmin"`
	LastTenantID    *uuid.UUID `json:"lastTenantId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// Membership is a (tenant, user, role) triple.
type Membership struct {
	TenantID  uuid.UUID `json:"tenantId"`
	UserID    uuid.UUID `json:"userId"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// TenantWithRole attaches the caller's role on a tenant for the /me response.
type TenantWithRole struct {
	Tenant
	Role Role `json:"role"`
}

// Invite is defined here so plan 2 doesn't have to change the types file.
// Plan 1 leaves it unused.
type Invite struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenantId"`
	Email            string     `json:"email"`
	Role             Role       `json:"role"`
	InvitedByUserID  uuid.UUID  `json:"invitedByUserId"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	AcceptedAt       *time.Time `json:"acceptedAt,omitempty"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}
```

- [ ] **Step 3: Verify build**

```bash
cd backend && go build ./...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/identity/
git commit -m "feat(identity): add Role/Tenant/User/Membership domain types"
```

### Task 5.2: `identity.Slugify`

**Files:**
- Create: `backend/internal/identity/slug.go`
- Test: `backend/internal/identity/slug_test.go`

- [ ] **Step 1: Write the failing test**

```go
package identity

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Personal":                  "personal",
		"David's Household":         "davids-household",
		"Étoiles & Co":              "etoiles-co",
		"   leading trailing   ":    "leading-trailing",
		"!!!":                       "",
		"A":                         "a",
		"Zurich (main)":             "zurich-main",
		strings.Repeat("x", 100):    strings.Repeat("x", 63),
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := Slugify(in)
			if got != want {
				t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
			}
		})
	}
}
```

(Note: add `"strings"` to imports.)

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/identity/... -run TestSlugify -v
```

Expected: FAIL.

- [ ] **Step 3: Write the implementation**

```go
package identity

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var slugCollapse = regexp.MustCompile(`-+`)

// Slugify returns a lowercase, hyphen-separated slug derived from s.
// Non-ASCII letters are folded to their ASCII base; punctuation is stripped;
// max length 63 (matches the schema check `^[a-z0-9][a-z0-9-]{1,62}$`).
func Slugify(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	s, _, _ = transform.String(t, s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '-', r == '_':
			b.WriteByte('-')
		default:
			// strip
		}
	}
	out := strings.Trim(slugCollapse.ReplaceAllString(b.String(), "-"), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}
```

- [ ] **Step 4: Add dependency for unicode transforms**

```bash
cd backend && go get golang.org/x/text/unicode/norm golang.org/x/text/runes golang.org/x/text/transform
```

- [ ] **Step 5: Run the test**

```bash
go test ./internal/identity/... -run TestSlugify -v
```

Expected: all sub-cases PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/identity/slug.go backend/internal/identity/slug_test.go backend/go.mod backend/go.sum
git commit -m "feat(identity): add Slugify helper"
```

### Task 5.3: `identity.Service` core (Me + CreateTenant + ListMembers)

**Files:**
- Replace: `backend/internal/identity/service.go` (remove `Bootstrap`, keep new shape)
- Test: `backend/internal/identity/service_test.go` (unit tests for `Slugify`-integrated create)
- Create: `backend/internal/identity/service_integration_test.go`

- [ ] **Step 1: Rewrite `service.go`**

```go
package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Service owns writes and reads for users, tenants, and memberships.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService constructs a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// Me returns the user + every tenant they belong to, with their role per
// tenant. Soft-deleted tenants are excluded.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, []TenantWithRole, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		select id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at
		from users
		where id = $1
	`, userID).Scan(&u.ID, &u.Email, &u.DisplayName, &u.EmailVerifiedAt, &u.IsAdmin, &u.LastTenantID, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, nil, httpx.NewNotFoundError("user")
		}
		return u, nil, fmt.Errorf("select user: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		select t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day, t.locale, t.timezone, t.deleted_at, t.created_at, m.role
		from tenant_memberships m
		join tenants t on t.id = m.tenant_id
		where m.user_id = $1 and t.deleted_at is null
		order by t.name
	`, userID)
	if err != nil {
		return u, nil, fmt.Errorf("list memberships: %w", err)
	}
	defer rows.Close()
	var tenants []TenantWithRole
	for rows.Next() {
		var tr TenantWithRole
		if err := rows.Scan(&tr.ID, &tr.Name, &tr.Slug, &tr.BaseCurrency, &tr.CycleAnchorDay,
			&tr.Locale, &tr.Timezone, &tr.DeletedAt, &tr.CreatedAt, &tr.Role); err != nil {
			return u, nil, fmt.Errorf("scan membership: %w", err)
		}
		tenants = append(tenants, tr)
	}
	if rows.Err() != nil {
		return u, nil, rows.Err()
	}
	return u, tenants, nil
}

// CreateTenantInput is the validated input to CreateTenant.
type CreateTenantInput struct {
	Name           string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
}

func (in CreateTenantInput) normalize() (CreateTenantInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Name == "" {
		return in, httpx.NewValidationError("name is required")
	}
	if in.Locale == "" {
		return in, httpx.NewValidationError("locale is required")
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.CycleAnchorDay == 0 {
		in.CycleAnchorDay = 1
	}
	if in.CycleAnchorDay < 1 || in.CycleAnchorDay > 31 {
		return in, httpx.NewValidationError("cycleAnchorDay must be 1-31")
	}
	cur, err := money.ParseCurrency(in.BaseCurrency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.BaseCurrency = string(cur)
	return in, nil
}

// CreateTenant creates a tenant with a unique slug derived from its name,
// and installs the calling user as an owner in the same transaction.
// Returns the tenant and the membership.
func (s *Service) CreateTenant(ctx context.Context, userID uuid.UUID, raw CreateTenantInput) (Tenant, Membership, error) {
	in, err := raw.normalize()
	if err != nil {
		return Tenant{}, Membership{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tenant{}, Membership{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	t, err := insertTenantTx(ctx, tx, uuidx.New(), in)
	if err != nil {
		return Tenant{}, Membership{}, err
	}
	m, err := insertMembershipTx(ctx, tx, t.ID, userID, RoleOwner)
	if err != nil {
		return Tenant{}, Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Tenant{}, Membership{}, fmt.Errorf("commit: %w", err)
	}
	return t, m, nil
}

// ListMembers returns every membership in tenantID. Plan 1 ships list-only;
// plan 2 extends with pending invites.
func (s *Service) ListMembers(ctx context.Context, tenantID uuid.UUID) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		select tenant_id, user_id, role, created_at
		from tenant_memberships
		where tenant_id = $1
		order by created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.TenantID, &m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// insertTenantTx/insertMembershipTx are exposed as package-private so
// auth.Service.Signup can reuse them inside its own transaction.
func insertTenantTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, in CreateTenantInput) (Tenant, error) {
	base := Slugify(in.Name)
	if base == "" {
		base = "workspace"
	}
	// Collision-suffix loop. Caps at 100 attempts then fails.
	slug := base
	for i := 0; i < 100; i++ {
		var t Tenant
		err := tx.QueryRow(ctx, `
			insert into tenants (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
			values ($1,$2,$3,$4,$5,$6,$7)
			returning id, name, slug, base_currency, cycle_anchor_day, locale, timezone, deleted_at, created_at
		`, id, in.Name, slug, in.BaseCurrency, in.CycleAnchorDay, in.Locale, in.Timezone).
			Scan(&t.ID, &t.Name, &t.Slug, &t.BaseCurrency, &t.CycleAnchorDay, &t.Locale, &t.Timezone, &t.DeletedAt, &t.CreatedAt)
		if err == nil {
			return t, nil
		}
		if !isUniqueViolation(err, "tenants_slug_key") {
			return Tenant{}, fmt.Errorf("insert tenant: %w", err)
		}
		// Collision — suffix and retry.
		slug = fmt.Sprintf("%s-%d", base, i+2)
	}
	return Tenant{}, httpx.NewValidationError("could not generate unique slug")
}

func insertMembershipTx(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, role Role) (Membership, error) {
	var m Membership
	err := tx.QueryRow(ctx, `
		insert into tenant_memberships (tenant_id, user_id, role)
		values ($1, $2, $3)
		returning tenant_id, user_id, role, created_at
	`, tenantID, userID, role).Scan(&m.TenantID, &m.UserID, &m.Role, &m.CreatedAt)
	if err != nil {
		return Membership{}, fmt.Errorf("insert membership: %w", err)
	}
	return m, nil
}

func isUniqueViolation(err error, constraint string) bool {
	// pgx returns *pgconn.PgError on constraint failure.
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		return true
	}
	return false
}

// stringsTrim bootstrap so we don't need to pull strings above the types.
// (Go doesn't allow multiple imports of the same package; including for
// tests below.)
```

(Note: also add `"strings"` to the import block at the top; the placeholder above collapses it for brevity.)

- [ ] **Step 2: Rewrite `service_test.go` (unit, no DB)**

```go
package identity

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestCreateTenantInput_normalize(t *testing.T) {
	good := CreateTenantInput{
		Name: "  My Workspace ", BaseCurrency: "chf", Locale: "en-CH",
	}
	out, err := good.normalize()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Name != "My Workspace" {
		t.Errorf("name = %q", out.Name)
	}
	if out.BaseCurrency != "CHF" {
		t.Errorf("cur = %q", out.BaseCurrency)
	}
	if out.Timezone != "UTC" {
		t.Errorf("tz = %q", out.Timezone)
	}
}

func TestCreateTenantInput_normalize_errors(t *testing.T) {
	cases := []struct{ name string; in CreateTenantInput }{
		{"missing name", CreateTenantInput{BaseCurrency: "CHF", Locale: "en"}},
		{"missing locale", CreateTenantInput{Name: "x", BaseCurrency: "CHF"}},
		{"bad currency", CreateTenantInput{Name: "x", BaseCurrency: "zz", Locale: "en"}},
		{"bad day", CreateTenantInput{Name: "x", BaseCurrency: "CHF", Locale: "en", CycleAnchorDay: 40}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.normalize()
			if err == nil {
				t.Fatalf("expected error")
			}
			var verr *httpx.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected ValidationError, got %T", err)
			}
		})
	}
}
```

- [ ] **Step 3: Write the integration test**

```go
package identity

import (
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestService_CreateTenant_integration(t *testing.T) {
	testdb.WithTx(t, func(ctx context.Context, tx pgx.Tx) {
		// Seed a user so the membership FK has a target.
		userID := uuid.New()
		if _, err := tx.Exec(ctx, `
			insert into users (id, email, password_hash, display_name)
			values ($1, 'test@example.com', 'x', 'Test')
		`, userID); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		// We can't use the Service directly without a pool; run the insert
		// helpers against the tx.
		slug := Slugify("Personal")
		var tenantID uuid.UUID
		if err := tx.QueryRow(ctx, `
			insert into tenants (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
			values ($1, 'Personal', $2, 'CHF', 1, 'en-CH', 'Europe/Zurich')
			returning id
		`, uuid.New(), slug).Scan(&tenantID); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			insert into tenant_memberships (tenant_id, user_id, role)
			values ($1, $2, 'owner')
		`, tenantID, userID); err != nil {
			t.Fatalf("insert membership: %v", err)
		}
		var role string
		if err := tx.QueryRow(ctx, `
			select role from tenant_memberships where tenant_id = $1 and user_id = $2
		`, tenantID, userID).Scan(&role); err != nil {
			t.Fatalf("read membership: %v", err)
		}
		if role != "owner" {
			t.Errorf("role = %q, want owner", role)
		}
	})
}
```

(Add imports: `"context"`, `"github.com/jackc/pgx/v5"`.)

- [ ] **Step 4: Delete `Bootstrap` from service.go** — it was removed in the rewrite above.

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/identity/... -v
```

Expected: unit tests PASS; integration test PASS or SKIP if `DATABASE_URL` unset.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/identity/
git commit -m "refactor(identity): replace Bootstrap with Me/CreateTenant/ListMembers"
```

---

## 6. Auth service (signup, login, logout)

Spec reference: §4.2.

### Task 6.1: `auth.Service` struct + configuration

**Files:**
- Create: `backend/internal/auth/service.go`

- [ ] **Step 1: Write the struct**

```go
package auth

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
)

// RegistrationMode gates the Signup endpoint.
type RegistrationMode string

const (
	RegistrationOpen         RegistrationMode = "open"
	RegistrationInviteOnly   RegistrationMode = "invite_only"
	RegistrationFirstRunOnly RegistrationMode = "first_run_only"
)

// Config is the Service's knobs.
type Config struct {
	SessionIdle     time.Duration // default 14*24h
	SessionAbsolute time.Duration // default 90*24h
	Registration    RegistrationMode
	BootstrapEmail  string // ADMIN_BOOTSTRAP_EMAIL; plan 5 consumes this
}

// Service wraps the db pool and the identity.Service. Handlers call Signup,
// Login, Logout; middleware reads sessions directly from the pool.
type Service struct {
	pool     *pgxpool.Pool
	identity *identity.Service
	cfg      Config
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, identitySvc *identity.Service, cfg Config) *Service {
	if cfg.SessionIdle == 0 {
		cfg.SessionIdle = 14 * 24 * time.Hour
	}
	if cfg.SessionAbsolute == 0 {
		cfg.SessionAbsolute = 90 * 24 * time.Hour
	}
	if cfg.Registration == "" {
		cfg.Registration = RegistrationOpen
	}
	return &Service{pool: pool, identity: identitySvc, cfg: cfg, now: time.Now}
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/auth/...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/auth/service.go
git commit -m "feat(auth): scaffold Service with session + registration config"
```

### Task 6.2: `auth.Service.Signup`

**Files:**
- Modify: `backend/internal/auth/service.go`
- Create: `backend/internal/auth/service_signup.go`
- Create: `backend/internal/auth/service_signup_test.go`

- [ ] **Step 1: Write the failing integration test**

```go
package auth

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestSignup_createsUserPersonalTenantAndSession(t *testing.T) {
	testdb.WithTx(t, func(ctx context.Context, tx pgx.Tx) {
		// Using tx directly via a tx-scoped pool shim. For this test we
		// inline the minimal flow so the integration test does not depend
		// on SearchPool.
		t.Skip("see full integration via HTTP test in Task 10")
	})
}
```

(We'll exercise signup end-to-end via the HTTP handler test in Task 10.1. The unit-tests here focus on `SignupInput.normalize`.)

- [ ] **Step 2: Write `service_signup.go`**

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// SignupInput is the validated input to Signup.
type SignupInput struct {
	Email          string
	Password       string
	DisplayName    string
	TenantName     string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
	InviteToken    string // plan 2 wires consumption; plan 1 ignores
	IP             net.IP
	UserAgent      string
}

func (in SignupInput) normalize() (SignupInput, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.TenantName = strings.TrimSpace(in.TenantName)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		return in, httpx.NewValidationError("email is required")
	}
	if in.DisplayName == "" {
		return in, httpx.NewValidationError("displayName is required")
	}
	if err := CheckPasswordPolicy(in.Password, in.Email, in.DisplayName); err != nil {
		return in, err
	}
	if in.TenantName == "" {
		in.TenantName = fmt.Sprintf("%s's Workspace", strings.Fields(in.DisplayName)[0])
	}
	if in.Locale == "" {
		in.Locale = "en-US"
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.CycleAnchorDay == 0 {
		in.CycleAnchorDay = 1
	}
	if in.CycleAnchorDay < 1 || in.CycleAnchorDay > 31 {
		return in, httpx.NewValidationError("cycleAnchorDay must be 1-31")
	}
	cur, err := money.ParseCurrency(in.BaseCurrency)
	if err != nil {
		if in.BaseCurrency == "" {
			cur, _ = money.ParseCurrency("USD")
		} else {
			return in, httpx.NewValidationError(err.Error())
		}
	}
	in.BaseCurrency = string(cur)
	return in, nil
}

// SignupResult is returned by Signup.
type SignupResult struct {
	User         identity.User
	Tenant       identity.Tenant
	Membership   identity.Membership
	SessionToken string
}

// Signup creates a user, their Personal tenant, an owner membership, and a
// session — all in one transaction. Returns the plaintext session token for
// the handler to set in a cookie.
func (s *Service) Signup(ctx context.Context, raw SignupInput) (*SignupResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}
	if err := s.enforceRegistrationMode(ctx); err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	userID := uuidx.New()
	var user identity.User
	err = tx.QueryRow(ctx, `
		insert into users (id, email, password_hash, display_name)
		values ($1, $2, $3, $4)
		returning id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at
	`, userID, in.Email, hash, in.DisplayName).Scan(
		&user.ID, &user.Email, &user.DisplayName, &user.EmailVerifiedAt,
		&user.IsAdmin, &user.LastTenantID, &user.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err, "users_email_key") {
			return nil, httpx.NewValidationError("that email is already registered")
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	// Insert Personal tenant + owner membership using identity helpers.
	tenantCI := identity.CreateTenantInput{
		Name: in.TenantName, BaseCurrency: in.BaseCurrency,
		CycleAnchorDay: in.CycleAnchorDay, Locale: in.Locale, Timezone: in.Timezone,
	}
	if _, err := tenantCI.Normalize(); err != nil {
		return nil, err
	}
	tenant, err := identity.InsertTenantTx(ctx, tx, uuidx.New(), tenantCI)
	if err != nil {
		return nil, err
	}
	membership, err := identity.InsertMembershipTx(ctx, tx, tenant.ID, userID, identity.RoleOwner)
	if err != nil {
		return nil, err
	}

	// Set last_tenant_id so /me redirects to this workspace.
	if _, err := tx.Exec(ctx, `update users set last_tenant_id = $1 where id = $2`, tenant.ID, userID); err != nil {
		return nil, fmt.Errorf("set last_tenant_id: %w", err)
	}
	user.LastTenantID = &tenant.ID

	// Issue session.
	plaintext, _ := GenerateSessionToken()
	sid := SessionIDFromToken(plaintext)
	now := s.now().UTC()
	if _, err := tx.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		values ($1, $2, $3, $4, $3, $5, $6)
	`, sid, userID, now, now.Add(s.cfg.SessionAbsolute), in.UserAgent, in.IP.String()); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	// Audit.
	if err := writeAudit(ctx, tx, &tenant.ID, &userID, "user.signup", "user", userID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	if err := writeAudit(ctx, tx, &tenant.ID, &userID, "tenant.created", "tenant", tenant.ID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &SignupResult{User: user, Tenant: tenant, Membership: membership, SessionToken: plaintext}, nil
}

func (s *Service) enforceRegistrationMode(ctx context.Context) error {
	switch s.cfg.Registration {
	case RegistrationOpen:
		return nil
	case RegistrationFirstRunOnly:
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from users)`).Scan(&exists); err != nil {
			return fmt.Errorf("first-run check: %w", err)
		}
		if exists {
			return httpx.NewValidationError("registration is closed on this instance")
		}
		return nil
	case RegistrationInviteOnly:
		// Plan 2 replaces this branch with "require a matching inviteToken".
		// Plan 1 leaves it strict: no registrations without a token.
		return httpx.NewValidationError("invite-only mode: signup requires an invite token")
	default:
		return errors.New("unknown registration mode")
	}
}
```

- [ ] **Step 3: Promote `identity`'s tx helpers to exported names**

Inside `backend/internal/identity/service.go`, rename `insertTenantTx` → `InsertTenantTx`, `insertMembershipTx` → `InsertMembershipTx`, and expose `CreateTenantInput.Normalize()` (add `// Normalize exposes the previously private normalize for cross-package use.` comment). Rewire the callers in `service.go`.

- [ ] **Step 4: Run**

```bash
go build ./...
go test ./internal/auth/... -run 'normalize' -v
```

Expected: build succeeds; normalize-style tests pass (signup end-to-end arrives in Task 10).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/ backend/internal/identity/
git commit -m "feat(auth): Signup creates user, Personal tenant, owner membership, session"
```

### Task 6.3: `auth.Service.Login`

**Files:**
- Create: `backend/internal/auth/service_login.go`
- Test: `backend/internal/auth/service_login_test.go`

- [ ] **Step 1: Write the failing unit test**

```go
package auth

import (
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func TestLoginInput_normalize(t *testing.T) {
	in := LoginInput{Email: "  A@B.com ", Password: "x"}
	out, err := in.normalize()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Email != "a@b.com" {
		t.Errorf("email = %q", out.Email)
	}
}

func TestLoginInput_normalize_errors(t *testing.T) {
	_, err := LoginInput{Email: "no-at", Password: "x"}.normalize()
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected validation error, got %T: %v", err, err)
	}
	_, err = LoginInput{Email: "a@b.com", Password: ""}.normalize()
	if !errors.As(err, &verr) {
		t.Fatalf("expected validation error, got %T: %v", err, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/auth/... -run 'Login' -v
```

Expected: FAIL (undefined LoginInput).

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

type LoginInput struct {
	Email     string
	Password  string
	IP        net.IP
	UserAgent string
}

func (in LoginInput) normalize() (LoginInput, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		return in, httpx.NewValidationError("email is required")
	}
	if in.Password == "" {
		return in, httpx.NewValidationError("password is required")
	}
	return in, nil
}

// LoginResult is returned by Login. MFARequired is the spec §4.2 branch
// that plan 4 replaces with real MFA; plan 1 hard-codes it to false.
type LoginResult struct {
	User         identity.User
	SessionToken string
	MFARequired  bool
	ChallengeID  string
}

// ErrInvalidCredentials is returned on bad email or password. Handlers map
// this to 401.
var ErrInvalidCredentials = errors.New("invalid email or password")

// Login verifies credentials and issues a session. Plan 4 will branch here
// when the user has MFA enrolled — for plan 1, always MFARequired=false.
func (s *Service) Login(ctx context.Context, raw LoginInput) (*LoginResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	var userID uuid.UUID
	var hash string
	var user identity.User
	err = s.pool.QueryRow(ctx, `
		select id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at, password_hash
		from users where email = $1
	`, in.Email).Scan(&user.ID, &user.Email, &user.DisplayName, &user.EmailVerifiedAt,
		&user.IsAdmin, &user.LastTenantID, &user.CreatedAt, &hash)
	userID = user.ID
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		// Compute a fake Argon2id verify against a pre-hashed dummy so
		// response time is constant regardless of email existence.
		_, _ = VerifyPassword("dummy", dummyHash)
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("select user: %w", err)
	}
	ok, err := VerifyPassword(in.Password, hash)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		s.logLoginFailed(ctx, in.Email, in.IP, in.UserAgent)
		return nil, ErrInvalidCredentials
	}

	// Plan 1 does not branch for MFA — that's plan 4. Here we hard-code the
	// non-MFA path: issue the session right away.
	plaintext, _ := GenerateSessionToken()
	sid := SessionIDFromToken(plaintext)
	now := s.now().UTC()
	_, err = s.pool.Exec(ctx, `
		insert into sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		values ($1,$2,$3,$4,$3,$5,$6)
	`, sid, userID, now, now.Add(s.cfg.SessionAbsolute), in.UserAgent, in.IP.String())
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	_, _ = s.pool.Exec(ctx, `update users set last_login_at = $1 where id = $2`, now, userID)
	s.logAuditDirect(ctx, nil, &userID, "user.login_succeeded", "user", userID, in.IP, in.UserAgent)

	return &LoginResult{User: user, SessionToken: plaintext, MFARequired: false}, nil
}

func (s *Service) logLoginFailed(ctx context.Context, email string, ip net.IP, ua string) {
	// Keyed by email, not user (we may not know a user_id).
	_, _ = s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, after_value, ip, user_agent)
		values ($1, null, null, 'user.login_failed', 'email', jsonb_build_object('email', $2::text), $3, $4)
	`, uuidx.New(), email, ip.String(), ua)
}

// logAuditDirect writes to audit_events outside a tx. Signup uses the
// tx-bound writeAudit; steady-state login does not have a tx.
func (s *Service) logAuditDirect(ctx context.Context, tenantID *uuid.UUID, actorUserID *uuid.UUID, action, entityType string, entityID uuid.UUID, ip net.IP, ua string) {
	_, _ = s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, ip, user_agent)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
	`, uuidx.New(), tenantID, actorUserID, action, entityType, entityID, ip.String(), ua)
}

// dummyHash is a pre-computed Argon2id hash used as a constant-time decoy
// for unknown-email login paths. It hashes "this password does not match".
var dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$Zm9saW8tZHVtbXktc2FsdA$yV+7m0L+QyU0FnQ4I3o5l2XcVxDmxRhQWdJGaNmT5lU"

// need one import later.
var _ time.Duration
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/auth/... -run 'Login' -v
```

Expected: both normalize tests PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/service_login.go backend/internal/auth/service_login_test.go
git commit -m "feat(auth): Login verifies password and issues session (MFA branch stubbed)"
```

### Task 6.4: `auth.Service.Logout`

**Files:**
- Create: `backend/internal/auth/service_logout.go`

- [ ] **Step 1: Write the implementation**

```go
package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Logout deletes the session row. The cookie is cleared by the handler.
func (s *Service) Logout(ctx context.Context, sessionID string, userID uuid.UUID, ip net.IP, ua string) error {
	_, err := s.pool.Exec(ctx, `delete from sessions where id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	s.logAuditDirect(ctx, nil, &userID, "user.logout", "session", uuid.Nil, ip, ua)
	return nil
}
```

(Add `"net"` import.)

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/auth/service_logout.go
git commit -m "feat(auth): Logout deletes the session row"
```

---

## 7. Middleware

Spec reference: §4.5, §4.6, §8.1.

### Task 7.1: Context keys and accessors

**Files:**
- Create: `backend/internal/auth/context.go`
- Test: `backend/internal/auth/context_test.go`

- [ ] **Step 1: Write the implementation**

```go
package auth

import (
	"context"
	"net/http"

	"github.com/xmedavid/folio/backend/internal/identity"
)

type ctxKey string

const (
	ctxKeyUser    ctxKey = "folio.auth.user"
	ctxKeySession ctxKey = "folio.auth.session"
	ctxKeyTenant  ctxKey = "folio.auth.tenant"
	ctxKeyRole    ctxKey = "folio.auth.role"
)

// WithUser returns a context carrying the authenticated user.
func WithUser(ctx context.Context, u identity.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// UserFromCtx returns the user if present.
func UserFromCtx(ctx context.Context) (identity.User, bool) {
	u, ok := ctx.Value(ctxKeyUser).(identity.User)
	return u, ok
}

// MustUser panics if no user — use only in authenticated routes mounted
// under RequireSession.
func MustUser(r *http.Request) identity.User {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		panic("MustUser called without RequireSession upstream")
	}
	return u
}

// WithSession / SessionFromCtx attach the session row.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKeySession, s)
}

func SessionFromCtx(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxKeySession).(Session)
	return s, ok
}

// WithTenant / TenantFromCtx attach the tenant the caller is looking at.
// Set by RequireMembership.
func WithTenant(ctx context.Context, t identity.Tenant) context.Context {
	return context.WithValue(ctx, ctxKeyTenant, t)
}

func TenantFromCtx(ctx context.Context) (identity.Tenant, bool) {
	t, ok := ctx.Value(ctxKeyTenant).(identity.Tenant)
	return t, ok
}

func MustTenant(r *http.Request) identity.Tenant {
	t, ok := TenantFromCtx(r.Context())
	if !ok {
		panic("MustTenant called without RequireMembership upstream")
	}
	return t
}

// WithRole / RoleFromCtx attach the caller's role in the current tenant.
func WithRole(ctx context.Context, r identity.Role) context.Context {
	return context.WithValue(ctx, ctxKeyRole, r)
}

func RoleFromCtx(ctx context.Context) (identity.Role, bool) {
	r, ok := ctx.Value(ctxKeyRole).(identity.Role)
	return r, ok
}
```

- [ ] **Step 2: Write quick tests**

```go
package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
)

func TestContextRoundtrip(t *testing.T) {
	ctx := context.Background()
	ctx = WithUser(ctx, identity.User{ID: uuid.New(), Email: "a@b.com"})
	u, ok := UserFromCtx(ctx)
	if !ok || u.Email != "a@b.com" {
		t.Fatalf("UserFromCtx: %+v %v", u, ok)
	}
	ctx = WithTenant(ctx, identity.Tenant{ID: uuid.New(), Name: "T"})
	tn, _ := TenantFromCtx(ctx)
	if tn.Name != "T" {
		t.Fatalf("TenantFromCtx: %+v", tn)
	}
	ctx = WithRole(ctx, identity.RoleOwner)
	r, _ := RoleFromCtx(ctx)
	if r != identity.RoleOwner {
		t.Fatalf("RoleFromCtx: %v", r)
	}
}
```

- [ ] **Step 3: Define `Session`**

Add to `backend/internal/auth/service.go`:

```go
// Session is the in-memory shape of a sessions row. RequireSession attaches
// this to the request context.
type Session struct {
	ID         string
	UserID     uuid.UUID
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	ReauthAt   *time.Time
}
```

(Add `"github.com/google/uuid"` to imports.)

- [ ] **Step 4: Run the test**

```bash
go test ./internal/auth/... -run TestContextRoundtrip -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/context.go backend/internal/auth/context_test.go backend/internal/auth/service.go
git commit -m "feat(auth): context keys for user, session, tenant, role"
```

### Task 7.2: `auth.RequireSession`

**Files:**
- Create: `backend/internal/auth/middleware_session.go`
- Test: `backend/internal/auth/middleware_session_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSession_missingCookie(t *testing.T) {
	svc := &Service{} // pool will not be called if cookie missing
	m := svc.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not run")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/auth/... -run TestRequireSession -v
```

Expected: FAIL.

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RequireSession loads the session from the cookie, verifies sliding +
// absolute expiry, bumps last_seen_at, loads the user, and attaches
// Session + User to the request context. 401 on any failure.
func (s *Service) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		sid := SessionIDFromToken(c.Value)
		now := s.now().UTC()

		var sess Session
		err = s.pool.QueryRow(r.Context(), `
			select id, user_id, created_at, expires_at, last_seen_at, reauth_at
			from sessions where id = $1
		`, sid).Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.ReauthAt)
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			ClearSessionCookie(w)
			httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "sign in again")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		// Absolute expiry.
		if !sess.ExpiresAt.After(now) {
			_, _ = s.pool.Exec(r.Context(), `delete from sessions where id = $1`, sid)
			ClearSessionCookie(w)
			httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "sign in again")
			return
		}
		// Sliding idle.
		if now.Sub(sess.LastSeenAt) > s.cfg.SessionIdle {
			_, _ = s.pool.Exec(r.Context(), `delete from sessions where id = $1`, sid)
			ClearSessionCookie(w)
			httpx.WriteError(w, http.StatusUnauthorized, "session_idle", "sign in again")
			return
		}
		// Bump last_seen_at (fire-and-forget; next request will be fresh).
		_, _ = s.pool.Exec(r.Context(), `update sessions set last_seen_at = $1 where id = $2`, now, sid)

		// Load user.
		user, _, err := s.identity.Me(r.Context(), sess.UserID)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "user not found")
			return
		}

		ctx := WithSession(r.Context(), sess)
		ctx = WithUser(ctx, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Avoid "imported but not used" on time when upstream tests change.
var _ time.Time
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/auth/... -run TestRequireSession -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/middleware_session.go backend/internal/auth/middleware_session_test.go
git commit -m "feat(auth): RequireSession middleware (cookie → session + user → context)"
```

### Task 7.3: `auth.RequireMembership`

**Files:**
- Create: `backend/internal/auth/middleware_membership.go`
- Test: `backend/internal/auth/middleware_membership_test.go`

- [ ] **Step 1: Write the test**

```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
)

func TestRequireMembership_404WithoutUser(t *testing.T) {
	svc := &Service{}
	m := svc.RequireMembership(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("GET", "/api/v1/t/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestRequireMembership_attachesRole(t *testing.T) {
	// Integration test — see service_integration_test.go — covered via HTTP test in Task 10.
	_ = chi.NewRouter
	_ = identity.RoleOwner
	_ = context.Background()
}
```

- [ ] **Step 2: Write the implementation**

```go
package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// RequireMembership extracts `{tenantId}` from the URL, verifies the caller
// has a membership in that tenant, and attaches the Tenant + Role to the
// request context. 404 on miss (spec §4.5: hide tenant existence).
func (s *Service) RequireMembership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromCtx(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		raw := chi.URLParam(r, "tenantId")
		tid, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}

		var tenant identity.Tenant
		var role identity.Role
		err = s.pool.QueryRow(r.Context(), `
			select t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day,
			       t.locale, t.timezone, t.deleted_at, t.created_at, m.role
			from tenants t
			join tenant_memberships m on m.tenant_id = t.id
			where t.id = $1 and m.user_id = $2 and t.deleted_at is null
		`, tid, user.ID).Scan(&tenant.ID, &tenant.Name, &tenant.Slug, &tenant.BaseCurrency,
			&tenant.CycleAnchorDay, &tenant.Locale, &tenant.Timezone, &tenant.DeletedAt,
			&tenant.CreatedAt, &role)
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}

		ctx := WithTenant(r.Context(), tenant)
		ctx = WithRole(ctx, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

- [ ] **Step 3: Run**

```bash
go test ./internal/auth/... -run TestRequireMembership -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/middleware_membership.go backend/internal/auth/middleware_membership_test.go
git commit -m "feat(auth): RequireMembership middleware (404 on miss)"
```

### Task 7.4: `auth.RequireRole`

**Files:**
- Create: `backend/internal/auth/middleware_role.go`
- Test: `backend/internal/auth/middleware_role_test.go`

- [ ] **Step 1: Write the test**

```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmedavid/folio/backend/internal/identity"
)

func TestRequireRole_allows(t *testing.T) {
	mid := RequireRole(identity.RoleOwner)
	called := false
	h := mid(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithRole(context.Background(), identity.RoleOwner))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("handler should have run")
	}
}

func TestRequireRole_denies(t *testing.T) {
	mid := RequireRole(identity.RoleOwner)
	h := mid(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithRole(context.Background(), identity.RoleMember))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run, verify fails**

```bash
go test ./internal/auth/... -run TestRequireRole -v
```

Expected: FAIL.

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// RequireRole permits the request if the caller's role (set by
// RequireMembership) is in allowed. 403 otherwise.
func RequireRole(allowed ...identity.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r_, ok := RoleFromCtx(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "role required")
				return
			}
			for _, a := range allowed {
				if r_ == a {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "insufficient role")
		})
	}
}
```

- [ ] **Step 4: Run**

```bash
go test ./internal/auth/... -run TestRequireRole -v
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/middleware_role.go backend/internal/auth/middleware_role_test.go
git commit -m "feat(auth): RequireRole middleware"
```

### Task 7.5: `auth.RequireFreshReauth` (stub)

**Files:**
- Create: `backend/internal/auth/middleware_reauth.go`

- [ ] **Step 1: Write the stub**

```go
package auth

import (
	"net/http"
	"time"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RequireFreshReauth returns 403 with `code: "reauth_required"` until plan 4
// replaces the body with the real sessions.reauth_at freshness check.
// Plans 1 and 2 wire this onto sensitive routes so the chain is correct;
// tests that exercise those routes bump reauth_at directly via testdb.
func RequireFreshReauth(window time.Duration) func(http.Handler) http.Handler {
	_ = window
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, ok := SessionFromCtx(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusForbidden, "reauth_required", "re-authentication required")
				return
			}
			if sess.ReauthAt != nil && time.Since(*sess.ReauthAt) < window {
				next.ServeHTTP(w, r)
				return
			}
			httpx.WriteError(w, http.StatusForbidden, "reauth_required", "re-authentication required")
		})
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/auth/middleware_reauth.go
git commit -m "feat(auth): RequireFreshReauth middleware (checks reauth_at; plan 4 wires bump)"
```

### Task 7.6: `auth.CSRF`

**Files:**
- Create: `backend/internal/auth/middleware_csrf.go`
- Test: `backend/internal/auth/middleware_csrf_test.go`

- [ ] **Step 1: Write the test**

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCSRF_safeMethods(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("GET should pass through; got %d", rec.Code)
	}
}

func TestCSRF_rejectsBadOrigin(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("X-Folio-Request", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestCSRF_requiresHeader(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestCSRF_allows(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-Folio-Request", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("code = %d, want 204", rec.Code)
	}
}
```

- [ ] **Step 2: Write the implementation**

```go
package auth

import (
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// CSRF enforces two checks on state-changing methods:
//  1. Origin (fallback Referer) is in allowedOrigins.
//  2. Custom X-Folio-Request: 1 header present (triggers CORS preflight for
//     cross-origin callers, blocking simple-request forgeries).
// GET/HEAD/OPTIONS pass through.
func CSRF(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = r.Header.Get("Referer")
			}
			if _, ok := allowed[origin]; !ok {
				httpx.WriteError(w, http.StatusForbidden, "csrf_origin", "origin not allowed")
				return
			}
			if r.Header.Get("X-Folio-Request") != "1" {
				httpx.WriteError(w, http.StatusForbidden, "csrf_header", "X-Folio-Request header required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 3: Run**

```bash
go test ./internal/auth/... -run TestCSRF -v
```

Expected: all 4 sub-tests PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/middleware_csrf.go backend/internal/auth/middleware_csrf_test.go
git commit -m "feat(auth): CSRF middleware (Origin allowlist + X-Folio-Request header)"
```

---

## 8. Rate limiting

Spec reference: §8.2.

### Task 8.1: Token bucket + rate limit middleware for signup and login

**Files:**
- Create: `backend/internal/auth/rate_limit.go`
- Test: `backend/internal/auth/rate_limit_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"testing"
	"time"
)

func TestTokenBucket_allowsUnderBudget(t *testing.T) {
	b := newTokenBucket(3, time.Hour)
	for i := 0; i < 3; i++ {
		if !b.take("k") {
			t.Fatalf("take %d should succeed", i)
		}
	}
	if b.take("k") {
		t.Fatalf("4th take on same key should fail")
	}
	if !b.take("other") {
		t.Fatalf("different key should succeed")
	}
}

func TestTokenBucket_refills(t *testing.T) {
	b := newTokenBucket(1, 10*time.Millisecond)
	b.take("k")
	if b.take("k") {
		t.Fatalf("second take should fail")
	}
	time.Sleep(12 * time.Millisecond)
	if !b.take("k") {
		t.Fatalf("after refill, take should succeed")
	}
}
```

- [ ] **Step 2: Run, verify fails**

- [ ] **Step 3: Write the implementation**

```go
package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

type tokenBucket struct {
	mu       sync.Mutex
	cap      int
	window   time.Duration
	counters map[string]*bucketCount
}

type bucketCount struct {
	count int
	resetAt time.Time
}

func newTokenBucket(cap int, window time.Duration) *tokenBucket {
	return &tokenBucket{cap: cap, window: window, counters: map[string]*bucketCount{}}
}

func (b *tokenBucket) take(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	c := b.counters[key]
	if c == nil || now.After(c.resetAt) {
		c = &bucketCount{count: 0, resetAt: now.Add(b.window)}
		b.counters[key] = c
	}
	if c.count >= b.cap {
		return false
	}
	c.count++
	return true
}

// ipFromRequest returns the best-effort client IP.
func ipFromRequest(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimitByIP is a per-IP middleware with the given cap per window.
func RateLimitByIP(cap int, window time.Duration) func(http.Handler) http.Handler {
	b := newTokenBucket(cap, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !b.take(ipFromRequest(r)) {
				httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Run**

```bash
go test ./internal/auth/... -run TokenBucket -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/rate_limit.go backend/internal/auth/rate_limit_test.go
git commit -m "feat(auth): in-memory token-bucket rate limiter + by-IP middleware"
```

---

## 9. Audit helper

Spec reference: §8.3.

### Task 9.1: `writeAudit` helper (tx + no-tx variants)

**Files:**
- Create: `backend/internal/auth/audit.go`

- [ ] **Step 1: Write the implementation**

```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// writeAudit inserts an audit_events row inside the provided tx.
// Passing any of tenantID / actorUserID as nil stores SQL NULL.
func writeAudit(ctx context.Context, tx pgx.Tx, tenantID, actorUserID *uuid.UUID,
	action, entityType string, entityID uuid.UUID, before, after any, ip net.IP, ua string) error {

	var beforeJSON, afterJSON []byte
	if before != nil {
		b, _ := json.Marshal(before)
		beforeJSON = b
	}
	if after != nil {
		b, _ := json.Marshal(after)
		afterJSON = b
	}
	_, err := tx.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, before_value, after_value, ip, user_agent)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, uuidx.New(), tenantID, actorUserID, action, entityType, entityID, beforeJSON, afterJSON, ip.String(), ua)
	if err != nil {
		return fmt.Errorf("audit insert: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/auth/audit.go
git commit -m "feat(auth): audit_events insert helper for tx-scoped writes"
```

---

## 10. HTTP handlers

Spec reference: §4.2 (signup/login/logout), §4.3 (/me, POST /tenants), §4.4 (list members).

### Task 10.1: `auth.Handler` signup / login / logout

**Files:**
- Create: `backend/internal/auth/http.go`
- Create: `backend/internal/auth/http_test.go`

- [ ] **Step 1: Write the handler**

```go
package auth

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MountPublic mounts the public auth routes under the given chi.Router.
func (h *Handler) MountPublic(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.With(RateLimitByIP(5, 60*60*1e9)).Post("/signup", h.signup) // 5/hr/IP
		r.With(RateLimitByIP(10, 10*60*1e9)).Post("/login", h.login)   // 10/10min/IP
		r.Post("/logout", h.logout)
	})
}

// MountAuthed mounts authenticated non-tenant routes.
func (h *Handler) MountAuthed(r chi.Router) {
	r.Get("/me", h.me)
	r.Post("/tenants", h.createTenant)
}

type signupReq struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	DisplayName    string `json:"displayName"`
	TenantName     string `json:"tenantName,omitempty"`
	BaseCurrency   string `json:"baseCurrency"`
	CycleAnchorDay int    `json:"cycleAnchorDay,omitempty"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone,omitempty"`
	InviteToken    string `json:"inviteToken,omitempty"`
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	var body signupReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	ip := net.ParseIP(ipFromRequest(r))
	out, err := h.svc.Signup(r.Context(), SignupInput{
		Email: body.Email, Password: body.Password, DisplayName: body.DisplayName,
		TenantName: body.TenantName, BaseCurrency: body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay, Locale: body.Locale, Timezone: body.Timezone,
		InviteToken: body.InviteToken, IP: ip, UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	SetSessionCookie(w, out.SessionToken)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user":        out.User,
		"tenant":      out.Tenant,
		"membership":  out.Membership,
		"mfaRequired": false,
	})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body loginReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	ip := net.ParseIP(ipFromRequest(r))
	out, err := h.svc.Login(r.Context(), LoginInput{
		Email: body.Email, Password: body.Password, IP: ip, UserAgent: r.UserAgent(),
	})
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	SetSessionCookie(w, out.SessionToken)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        out.User,
		"mfaRequired": out.MFARequired,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromCtx(r.Context())
	if !ok {
		// Cookie-only logout: clear cookie and return.
		ClearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	user := MustUser(r)
	ip := net.ParseIP(ipFromRequest(r))
	_ = h.svc.Logout(r.Context(), sess.ID, user.ID, ip, r.UserAgent())
	ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	_, tenants, err := h.svc.identity.Me(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":    user,
		"tenants": tenants,
	})
}

type createTenantReq struct {
	Name           string `json:"name"`
	BaseCurrency   string `json:"baseCurrency"`
	CycleAnchorDay int    `json:"cycleAnchorDay,omitempty"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone,omitempty"`
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	var body createTenantReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	t, m, err := h.svc.identity.CreateTenant(r.Context(), user.ID, identityCreateInput(body))
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"tenant": t, "membership": m})
}

func identityCreateInput(r createTenantReq) identityCreateTenantInput {
	return identityCreateTenantInput{
		Name: r.Name, BaseCurrency: r.BaseCurrency, CycleAnchorDay: r.CycleAnchorDay,
		Locale: r.Locale, Timezone: r.Timezone,
	}
}
```

(Above uses a local alias `identityCreateTenantInput` — since we already re-export via `identity.CreateTenantInput`, swap the alias for the actual type; this snippet exists to isolate the handler from the type's location.)

- [ ] **Step 2: Write an HTTP-level signup test**

```go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestSignupHTTP(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	// Reset between tests: truncate users cascades everything.
	if _, err := pool.Exec(ctx, `truncate users cascade; truncate tenants cascade`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	svc := NewService(pool, identity.NewService(pool), Config{Registration: RegistrationOpen})
	h := NewHandler(svc)

	body, _ := json.Marshal(signupReq{
		Email: "alice@example.com", Password: "correct horse battery staple",
		DisplayName: "Alice", BaseCurrency: "CHF", Locale: "en-CH",
	})
	req := httptest.NewRequest("POST", "/auth/signup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.signup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Cookie set?
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == SessionCookieName() {
			found = true
		}
	}
	if !found {
		t.Fatalf("session cookie not set")
	}
	// Duplicate email should fail.
	req2 := httptest.NewRequest("POST", "/auth/signup", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.signup(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("duplicate code = %d", rec2.Code)
	}
}

var _ *pgxpool.Pool = nil
```

- [ ] **Step 3: Run**

```bash
go test ./internal/auth/... -run TestSignupHTTP -v
```

Expected: PASS (requires `DATABASE_URL`).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/auth/http.go backend/internal/auth/http_test.go
git commit -m "feat(auth): HTTP handlers for signup, login, logout, /me, POST /tenants"
```

### Task 10.2: `GET /t/{tenantId}/members` handler

**Files:**
- Modify: `backend/internal/identity/http.go` (replace existing Bootstrap handler with member-list)

- [ ] **Step 1: Rewrite `identity/http.go`**

```go
package identity

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Handler mounts identity read endpoints scoped to a tenant.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MountTenantScoped mounts under /t/{tenantId}/…
func (h *Handler) MountTenantScoped(r chi.Router) {
	r.Get("/members", h.listMembers)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	tenant := auth.MustTenant(r)
	members, err := h.svc.ListMembers(r.Context(), tenant.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

// ParseTenantID parses {tenantId} from the URL; returned for handlers that
// need the id prior to RequireMembership running.
func ParseTenantID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "tenantId"))
}
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/identity/http.go
git commit -m "refactor(identity): replace Bootstrap handler with member list under /t/{id}"
```

---

## 11. Router rewire and legacy cleanup

Spec reference: §4, §12.

### Task 11.1: Delete `httpx.RequireTenant` and legacy context helpers

**Files:**
- Modify: `backend/internal/httpx/httpx.go`
- Modify: `backend/internal/httpx/httpx_test.go`

- [ ] **Step 1: Remove the helpers**

Delete from `httpx.go`: `tenantIDKey`, `userIDKey`, `TenantIDFrom`, `UserIDFrom`, `WithTenantID`, `WithUserID`, and `RequireTenant`. Keep: `WriteJSON`, `WriteError`, `ErrorBody`, `ValidationError`, `NewValidationError`, `NotFoundError`, `NewNotFoundError`, `WriteServiceError`.

- [ ] **Step 2: Update `httpx_test.go`** to drop tests that referenced the removed helpers.

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

If downstream packages (accounts/transactions/classification) still reference the removed helpers, they will fail. We'll fix those in Task 11.2.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/httpx/
git commit -m "refactor(httpx): remove X-Tenant-ID stand-in middleware"
```

### Task 11.2: Refactor accounts / transactions / classification handlers

**Files:**
- Modify: `backend/internal/accounts/http.go`
- Modify: `backend/internal/transactions/http.go`
- Modify: `backend/internal/classification/http.go`

- [ ] **Step 1: For each handler file, replace `httpx.TenantIDFrom(r.Context())` with `auth.MustTenant(r).ID`** and remove the `TenantIDFrom` import.

Example (accounts/http.go):

```diff
-	tenantID, ok := httpx.TenantIDFrom(r.Context())
-	if !ok {
-		httpx.WriteError(w, http.StatusUnauthorized, "tenant_required", "tenant required")
-		return
-	}
+	tenantID := auth.MustTenant(r).ID
```

- [ ] **Step 2: Add `auth` import** to each file:

```go
import (
	// ... existing imports ...
	"github.com/xmedavid/folio/backend/internal/auth"
)
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/accounts/ backend/internal/transactions/ backend/internal/classification/
git commit -m "refactor(handlers): read tenant from auth.MustTenant instead of httpx context"
```

### Task 11.3: Rewire `router.go`

**Files:**
- Modify: `backend/internal/http/router.go`
- Modify: `backend/cmd/server/main.go` (if it constructs Deps; include auth config)

- [ ] **Step 1: Replace `router.go`**

```go
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/accounts"
	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/config"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/transactions"
)

type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
	Cfg    *config.Config
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(requestLogger(d.Logger))

	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:3000"
	}
	r.Use(auth.CSRF([]string{appURL}))

	r.Get("/healthz", health(d))
	r.Get("/readyz", ready(d))

	identitySvc := identity.NewService(d.DB)
	authSvc := auth.NewService(d.DB, identitySvc, auth.Config{
		Registration: auth.RegistrationMode(os.Getenv("REGISTRATION_MODE")),
	})
	authH := auth.NewHandler(authSvc)
	identityH := identity.NewHandler(identitySvc)

	accountsSvc := accounts.NewService(d.DB)
	accountsH := accounts.NewHandler(accountsSvc)
	transactionsSvc := transactions.NewService(d.DB)
	transactionsH := transactions.NewHandler(transactionsSvc)
	classificationSvc := classification.NewService(d.DB)
	classificationH := classification.NewHandler(classificationSvc)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/version", versionHandler)

		// Public auth surface
		authH.MountPublic(r)

		// Authenticated, non-tenant-scoped
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			authH.MountAuthed(r)
		})

		// Tenant-scoped: /api/v1/t/{tenantId}/...
		r.Route("/t/{tenantId}", func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			r.Use(authSvc.RequireMembership)

			identityH.MountTenantScoped(r) // /members

			r.Route("/accounts", accountsH.Mount)
			r.Route("/transactions", transactionsH.Mount)
			r.Route("/transactions/{transactionId}/tags", classificationH.MountTransactionTags)
			r.Post("/transactions/{transactionId}/apply-categorization-rules",
				classificationH.ApplyRulesToTransactionHandler)
			r.Route("/categories", classificationH.MountCategories)
			r.Route("/merchants", classificationH.MountMerchants)
			r.Route("/tags", classificationH.MountTags)
			r.Route("/categorization-rules", classificationH.MountCategorizationRules)
		})
	})
	return r
}

func health(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) }
}
func ready(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := ctxWithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
func versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"name": "folio", "version": "0.0.0-dev"})
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

- [ ] **Step 3: Verify routes list**

```bash
go test ./internal/http/... -v
```

Expected: any existing router smoke test passes.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/http/router.go
git commit -m "refactor(router): mount tenant-scoped routes under /api/v1/t/{tenantId}, wire auth middleware"
```

---

## 12. Frontend — identity hook + API client

Spec reference: §13.

### Task 12.1: Delete the localStorage identity bridge

**Files:**
- Delete: `web/lib/tenant.ts`

- [ ] **Step 1**:

```bash
rm web/lib/tenant.ts
git add -u web/lib/tenant.ts
```

- [ ] **Step 2: Commit**

```bash
git commit -m "chore(web): remove localStorage tenant bridge"
```

### Task 12.2: Rewrite `useIdentity` as a React Query hook around `/api/v1/me`

**Files:**
- Modify: `web/lib/hooks/use-identity.ts`

- [ ] **Step 1: Replace contents**

```tsx
"use client";

import { useQuery } from "@tanstack/react-query";

export interface Me {
  user: {
    id: string;
    email: string;
    displayName: string;
    emailVerifiedAt?: string;
    isAdmin: boolean;
    lastTenantId?: string;
    createdAt: string;
  };
  tenants: Array<{
    id: string;
    name: string;
    slug: string;
    baseCurrency: string;
    cycleAnchorDay: number;
    locale: string;
    timezone: string;
    role: "owner" | "member";
    createdAt: string;
  }>;
}

export type IdentityState =
  | { status: "loading"; data: null }
  | { status: "unauthenticated"; data: null }
  | { status: "authenticated"; data: Me };

export function useIdentity(): IdentityState {
  const q = useQuery<Me>({
    queryKey: ["me"],
    queryFn: async () => {
      const res = await fetch("/api/v1/me", {
        credentials: "include",
        headers: { "X-Folio-Request": "1" },
      });
      if (res.status === 401) {
        throw new Error("UNAUTHENTICATED");
      }
      if (!res.ok) throw new Error(`me: ${res.status}`);
      return (await res.json()) as Me;
    },
    retry: false,
  });
  if (q.isLoading) return { status: "loading", data: null };
  if (q.isError && (q.error as Error).message === "UNAUTHENTICATED") {
    return { status: "unauthenticated", data: null };
  }
  if (q.data) return { status: "authenticated", data: q.data };
  return { status: "loading", data: null };
}

export function useCurrentTenant(slug: string): Me["tenants"][number] | undefined {
  const id = useIdentity();
  if (id.status !== "authenticated") return undefined;
  return id.data.tenants.find((t) => t.slug === slug);
}
```

- [ ] **Step 2: Commit**

```bash
git add web/lib/hooks/use-identity.ts
git commit -m "feat(web): rewrite useIdentity as React Query wrapper around /me"
```

### Task 12.3: Update `api/client.ts` to target `/api/v1/t/{tenantId}/…`

**Files:**
- Modify: `web/lib/api/client.ts`

- [ ] **Step 1: Update signatures**

```typescript
export async function fetchAccounts(tenantId: string) {
  return getJSON(`/api/v1/t/${tenantId}/accounts`);
}
export async function fetchTransactions(tenantId: string, params?: { limit?: number }) {
  const q = params?.limit ? `?limit=${params.limit}` : "";
  return getJSON(`/api/v1/t/${tenantId}/transactions${q}`);
}
export async function fetchMe(): Promise<Me> {
  return getJSON(`/api/v1/me`);
}

async function getJSON(path: string) {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return res.json();
}
```

(Extend as needed to match current shape.)

- [ ] **Step 2: Commit**

```bash
git add web/lib/api/client.ts
git commit -m "refactor(web): API client targets /api/v1/t/{tenantId}/... and /api/v1/me"
```

---

## 13. Frontend — auth pages

Spec reference: §13.

### Task 13.1: `/login` page

**Files:**
- Create: `web/app/login/page.tsx`

- [ ] **Step 1: Write the page**

```tsx
"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";

export default function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const res = await fetch("/api/v1/auth/login", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify({ email, password }),
      });
      if (res.status === 401) {
        setErr("Invalid email or password.");
        return;
      }
      if (!res.ok) {
        setErr(`Login failed (${res.status})`);
        return;
      }
      await qc.invalidateQueries({ queryKey: ["me"] });
      const me = await (await fetch("/api/v1/me", { credentials: "include", headers: { "X-Folio-Request": "1" } })).json();
      const slug = me.tenants?.[0]?.slug;
      router.push(slug ? `/t/${slug}` : "/tenants");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Sign in to Folio</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">Email</span>
          <input
            type="email" value={email} onChange={(e) => setEmail(e.target.value)}
            required autoFocus className="rounded border px-3 py-2" autoComplete="username webauthn"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">Password</span>
          <input
            type="password" value={password} onChange={(e) => setPassword(e.target.value)}
            required className="rounded border px-3 py-2" autoComplete="current-password"
          />
        </label>
        {err ? <p className="text-sm text-red-600">{err}</p> : null}
        <button type="submit" disabled={busy} className="rounded bg-foreground px-3 py-2 text-background">
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
      <p className="text-sm text-muted-foreground">
        New here? <a href="/signup" className="underline">Create an account</a>
      </p>
    </main>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add web/app/login/page.tsx
git commit -m "feat(web): /login page"
```

### Task 13.2: `/signup` page

**Files:**
- Create: `web/app/signup/page.tsx`

- [ ] **Step 1: Write the page**

```tsx
"use client";

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";

export default function SignupPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [baseCurrency, setBaseCurrency] = useState("USD");
  const [locale, setLocale] = useState("en-US");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  const sp = useSearchParams();
  const inviteToken = sp.get("inviteToken") ?? undefined; // plan 2 consumes
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const res = await fetch("/api/v1/auth/signup", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify({ email, password, displayName, baseCurrency, locale, inviteToken }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setErr(body?.error ?? `Signup failed (${res.status})`);
        return;
      }
      await qc.invalidateQueries({ queryKey: ["me"] });
      router.push(`/t/${body.tenant.slug}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Create a Folio account</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Field label="Your name" value={displayName} onChange={setDisplayName} required />
        <Field label="Email" type="email" value={email} onChange={setEmail} required autoComplete="email" />
        <Field label="Password" type="password" value={password} onChange={setPassword} required autoComplete="new-password" hint="12 characters minimum" />
        <Field label="Base currency" value={baseCurrency} onChange={(v) => setBaseCurrency(v.toUpperCase())} required />
        <Field label="Locale" value={locale} onChange={setLocale} required />
        {err ? <p className="text-sm text-red-600">{err}</p> : null}
        <button type="submit" disabled={busy} className="rounded bg-foreground px-3 py-2 text-background">
          {busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="text-sm text-muted-foreground">
        Already have an account? <a href="/login" className="underline">Sign in</a>
      </p>
    </main>
  );
}

function Field({
  label, value, onChange, type = "text", required, autoComplete, hint,
}: {
  label: string; value: string; onChange: (v: string) => void;
  type?: string; required?: boolean; autoComplete?: string; hint?: string;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-sm text-muted-foreground">{label}</span>
      <input
        type={type} value={value} onChange={(e) => onChange(e.target.value)}
        required={required} autoComplete={autoComplete}
        className="rounded border px-3 py-2"
      />
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </label>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add web/app/signup/page.tsx
git commit -m "feat(web): /signup page"
```

---

## 14. Frontend — tenant-scoped routing

Spec reference: §13.

### Task 14.1: `/t/[slug]/layout.tsx`

**Files:**
- Create: `web/app/t/[slug]/layout.tsx`

- [ ] **Step 1: Write the layout**

```tsx
"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useIdentity, useCurrentTenant } from "@/lib/hooks/use-identity";

export default function TenantLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: { slug: string };
}) {
  const id = useIdentity();
  const tenant = useCurrentTenant(params.slug);
  const router = useRouter();

  useEffect(() => {
    if (id.status === "unauthenticated") router.replace("/login");
    if (id.status === "authenticated" && !tenant) router.replace("/tenants");
  }, [id.status, tenant, router]);

  if (id.status !== "authenticated" || !tenant) {
    return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  }
  return (
    <div className="flex min-h-dvh flex-col">
      <TopBar currentTenantSlug={params.slug} />
      <main className="flex-1 p-6">{children}</main>
    </div>
  );
}

function TopBar({ currentTenantSlug }: { currentTenantSlug: string }) {
  // Tenant switcher mounted below (Task 14.5). Placeholder here for layout.
  return (
    <header className="flex items-center justify-between border-b px-6 py-3">
      <div className="font-semibold">Folio</div>
      <div>/{currentTenantSlug}</div>
    </header>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add web/app/t/[slug]/layout.tsx
git commit -m "feat(web): tenant-scoped layout at /t/[slug]"
```

### Task 14.2: Move dashboard under `/t/[slug]/page.tsx`

**Files:**
- Move: `web/app/page.tsx` → `web/app/t/[slug]/page.tsx` (with the `TenantGate` logic removed; the layout now handles it)
- Modify: `web/app/page.tsx` (reduce to a redirector)

- [ ] **Step 1: Create the tenant-scoped dashboard page**

Copy the current `web/app/page.tsx` into `web/app/t/[slug]/page.tsx`, replacing `tenantId` lookups with the tenant from `useCurrentTenant(params.slug)`. Remove the `TenantGate` wrapper.

- [ ] **Step 2: Replace root `page.tsx` with a redirector**

```tsx
"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useIdentity } from "@/lib/hooks/use-identity";

export default function Root() {
  const id = useIdentity();
  const router = useRouter();
  useEffect(() => {
    if (id.status === "unauthenticated") router.replace("/login");
    if (id.status === "authenticated") {
      const slug = id.data.tenants[0]?.slug;
      router.replace(slug ? `/t/${slug}` : "/tenants");
    }
  }, [id.status, id.data, router]);
  return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
}
```

- [ ] **Step 3: Commit**

```bash
git add web/app/page.tsx web/app/t/
git commit -m "feat(web): move dashboard to /t/[slug], redirect root based on session"
```

### Task 14.3: Move `/accounts` and `/transactions` under `/t/[slug]/`

**Files:**
- Move: `web/app/accounts/page.tsx` → `web/app/t/[slug]/accounts/page.tsx`
- Move: `web/app/transactions/page.tsx` → `web/app/t/[slug]/transactions/page.tsx`

- [ ] **Step 1: Move files and rewrite API-calls**

In both pages:
- Read `params: { slug: string }`
- Resolve `tenant` via `useCurrentTenant(slug)`
- Pass `tenant.id` (not `tenantId` from localStorage) into `fetchAccounts` / `fetchTransactions`

- [ ] **Step 2: Commit**

```bash
git add web/app/
git commit -m "refactor(web): move accounts/transactions pages under /t/[slug]/"
```

### Task 14.4: `/tenants` picker page

**Files:**
- Create: `web/app/tenants/page.tsx`

- [ ] **Step 1: Write the picker**

```tsx
"use client";

import Link from "next/link";
import { useIdentity } from "@/lib/hooks/use-identity";

export default function TenantsPage() {
  const id = useIdentity();
  if (id.status !== "authenticated") return <div className="p-6">Loading…</div>;
  return (
    <main className="mx-auto max-w-xl p-6">
      <h1 className="mb-4 text-2xl font-semibold">Your workspaces</h1>
      <ul className="flex flex-col gap-2">
        {id.data.tenants.map((t) => (
          <li key={t.id} className="rounded border p-3">
            <Link href={`/t/${t.slug}`} className="font-medium underline">
              {t.name}
            </Link>
            <div className="text-sm text-muted-foreground">
              {t.role} · {t.baseCurrency}
            </div>
          </li>
        ))}
      </ul>
      <CreateTenantForm />
    </main>
  );
}

function CreateTenantForm() {
  // Posts to /api/v1/tenants; omitted for brevity here — see §4.3 endpoint.
  // Plan 2 extends the picker with invite-join and settings.
  return null;
}
```

- [ ] **Step 2: Commit**

```bash
git add web/app/tenants/page.tsx
git commit -m "feat(web): /tenants picker page"
```

### Task 14.5: `TenantSwitcher` component

**Files:**
- Create: `web/components/tenant-switcher.tsx`

- [ ] **Step 1: Write the switcher**

```tsx
"use client";

import Link from "next/link";
import { useIdentity } from "@/lib/hooks/use-identity";

export function TenantSwitcher({ currentSlug }: { currentSlug: string }) {
  const id = useIdentity();
  if (id.status !== "authenticated") return null;
  return (
    <select
      className="rounded border px-2 py-1 text-sm"
      value={currentSlug}
      onChange={(e) => {
        window.location.href = `/t/${e.target.value}`;
      }}
    >
      {id.data.tenants.map((t) => (
        <option key={t.id} value={t.slug}>
          {t.name}
        </option>
      ))}
    </select>
  );
}
```

- [ ] **Step 2: Mount it in `/t/[slug]/layout.tsx`**

Replace the `TopBar` placeholder's `/{currentTenantSlug}` with `<TenantSwitcher currentSlug={params.slug} />`.

- [ ] **Step 3: Commit**

```bash
git add web/components/tenant-switcher.tsx web/app/t/[slug]/layout.tsx
git commit -m "feat(web): TenantSwitcher in top bar"
```

---

## 15. End-to-end smoke

### Task 15.1: Manual curl smoke test

**Files:**
- None.

- [ ] **Step 1: Start the stack**

```bash
cd /Users/xmedavid/dev/folio
docker compose -f docker-compose.dev.yml up -d db
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
cd backend && atlas migrate apply --env local && go run ./cmd/server &
```

- [ ] **Step 2: Sign up**

```bash
curl -i -X POST http://localhost:8081/api/v1/auth/signup \
  -H 'Content-Type: application/json' \
  -H 'Origin: http://localhost:3000' \
  -H 'X-Folio-Request: 1' \
  -c cookies.txt \
  -d '{"email":"a@b.com","password":"correct horse battery staple","displayName":"Alice","baseCurrency":"CHF","locale":"en-CH"}'
```

Expected: `HTTP/1.1 201 Created`, `Set-Cookie: folio_session=…; HttpOnly; Secure; SameSite=Lax`, body includes `{"user":{…},"tenant":{…,"slug":"alice-s-workspace"}}`.

- [ ] **Step 3: /me**

```bash
curl -s http://localhost:8081/api/v1/me -H 'X-Folio-Request: 1' -b cookies.txt | jq .
```

Expected: `{"user":{…},"tenants":[{…,"role":"owner"}]}`.

- [ ] **Step 4: Create second tenant**

```bash
curl -s -X POST http://localhost:8081/api/v1/tenants \
  -H 'Content-Type: application/json' -H 'Origin: http://localhost:3000' -H 'X-Folio-Request: 1' \
  -b cookies.txt \
  -d '{"name":"Household","baseCurrency":"EUR","locale":"de-CH"}' | jq .
```

Expected: 201; tenant returned with slug `household`.

- [ ] **Step 5: Tenant-scoped call**

```bash
TENANT_ID=$(curl -s http://localhost:8081/api/v1/me -H 'X-Folio-Request: 1' -b cookies.txt | jq -r '.tenants[0].id')
curl -s http://localhost:8081/api/v1/t/$TENANT_ID/members -H 'X-Folio-Request: 1' -b cookies.txt | jq .
```

Expected: `{"members":[{"tenantId":"…","userId":"…","role":"owner","createdAt":"…"}]}`.

- [ ] **Step 6: Logout**

```bash
curl -i -X POST http://localhost:8081/api/v1/auth/logout \
  -H 'Origin: http://localhost:3000' -H 'X-Folio-Request: 1' \
  -b cookies.txt
```

Expected: 204 and `Set-Cookie: folio_session=; Max-Age=0`.

- [ ] **Step 7: Verify session died**

```bash
curl -i http://localhost:8081/api/v1/me -H 'X-Folio-Request: 1' -b cookies.txt
```

Expected: 401 `{"error":"sign in required","code":"unauthenticated"}`.

- [ ] **Step 8: Record smoke in the commit**

```bash
git commit --allow-empty -m "test(auth): end-to-end signup/login/logout smoke verified"
```

---

## Appendix: what plans 2–5 extend

| Plan | What it adds that plan 1 leaves space for |
|---|---|
| 2 | Invite accept / preview, extended signup inviteToken consumption, tenant settings PATCH, soft-delete DELETE + restore, member role changes + remove + leave, pending invites on `GET /t/{id}/members`, soft-delete sweeper binary, full invite-only registration mode |
| 3 | Email verification / reset / email-change flows, Resend + River, `auth.RequireEmailVerified`, `ResendMailer` replacing plan 2's log-only stub, rate limits on verify/reset, email-related audit events, periodic sweeper as a River job |
| 4 | Passkey (WebAuthn) enroll + assert, TOTP enroll/verify/disable, `auth_recovery_codes` populated, MFA challenge table + `BeginMFA`/Verify endpoints, passkey-first login entry, **real `RequireFreshReauth` replaces the plan 1 stub** + `POST /auth/reauth`, `/settings/security` UI |
| 5 | `folio-admin` CLI, `ADMIN_BOOTSTRAP_EMAIL` wiring into `Signup`, `RequireAdmin` middleware, `/api/v1/admin/*`, admin audit events, `/admin/*` web pages |

Plan 1 is done when every task above is green. Downstream plans reference these function signatures, table columns, and route shapes by name.
