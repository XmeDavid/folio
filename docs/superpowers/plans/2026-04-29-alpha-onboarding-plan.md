# Folio Alpha Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Frontend rule (from `CLAUDE.md`):** before touching anything in `web/`, read the project skill at `/Users/xmedavid/dev/folio/.claude/skills/folio-frontend-design/SKILL.md` and follow its validation guidance. Use existing primitives in `web/components/ui/*` (Button, Input, Dialog, DataTable, PageHeader, Badge) — do not invent new ones.
>
> **TDD rule:** Backend changes follow `superpowers:test-driven-development` — write the failing Go test in `*_test.go` first, run it, then make it pass. Frontend doesn't have a strong unit-test culture in this repo; rely on browser smoke verification at the end of each phase.
>
> **Commit cadence:** one commit per task. Push at the end of each phase. Open a PR per phase if the user wants per-phase review (default: branch `feat/alpha-onboarding`, single PR at the end).

**Goal:** Reach a state where the bootstrapped admin can invite a new user, that user can sign up via the invite link, land in their own workspace, create additional workspaces, invite others into a workspace they own, and self-manage their security (TOTP disable, passkey delete, change password).

**Non-goals (deferred to later plans):** full onboarding wizard, audit log UI, exports, account delete, sessions/devices UI, sample data mode, advanced workspace switcher, encrypted export, import bundle, backup status, PWA offline transactions, accent colour / locale preferences. PWA stays as currently configured (no regression target).

**Architecture decisions (locked):**

1. **Two distinct invite flows.** Platform-level admin invites (admin dashboard → invite a new user to the instance, no workspace binding) and workspace-level owner invites (existing flow, owner invites a new email into their workspace). Signup accepts both kinds of token interchangeably.
2. **New `platform_invites` table.** Mirrors `workspace_invites` shape (hashed token, optional email, expiry, consumed_at, revoked_at, created_by) but no `workspace_id`. Keeps the existing `workspace_invites` semantics untouched.
3. **Copy-link affordance everywhere.** Both invite types return the plaintext token once on creation; UIs surface a "Copy invite link" button next to the email send so admin/owner can hand-share when SMTP isn't configured.
4. **Last-workspace redirect uses `lastWorkspaceID`** when valid; falls back to `workspaces[0]`; falls back to `/workspaces`.
5. **Reauth is wired** for sensitive ops (`/auth/reauth` already exists). New write endpoints (passkey delete, change password, platform invite create/revoke) sit behind `RequireFreshReauth`.
6. **`folio-admin` CLI stays the bootstrap path** for the very first admin (already exists). Plan doesn't change bootstrap.

**Tech stack reference:**

- Backend: Go 1.x, chi router, sqlc-generated queries (`backend/internal/db/dbq`), pgx, atlas migrations (`backend/migrations` — confirm path), River job queue, structured logging via `slog`. HTTP error helper `internal/httpx`. Audit via `writeAuditTx` / `Service.WriteAudit`.
- Frontend: Next.js 15 app router, React 19, TypeScript, Tailwind v4, shadcn/ui primitives in `web/components/ui/*`, TanStack Query for server state, `@simplewebauthn/browser` for WebAuthn, generated OpenAPI types in `web/lib/api/*`.
- API surface: `/api/v1/*` (proxied by Next dev server). Auth uses session cookies + CSRF middleware (`X-Folio-Request: 1` header).

---

## Phase 0 — Backend: platform admin invites

**Why first:** The admin invite UI in Phase 1 needs these endpoints. This phase ships a backend-only change covered by tests; safe to merge independently.

**Files:**

- Create: `backend/migrations/<timestamp>_create_platform_invites.sql` (atlas migration — match existing migration filename convention; check `ls backend/migrations` first)
- Create: `backend/internal/db/queries/platform_invites.sql` (sqlc input)
- Generated: `backend/internal/db/dbq/platform_invites.sql.go` (via `make sqlc`)
- Create: `backend/internal/identity/platform_invite.go` (service)
- Create: `backend/internal/identity/platform_invite_test.go`
- Create: `backend/internal/auth/http_admin_invites.go` (HTTP handler + routes)
- Create: `backend/internal/auth/http_admin_invites_test.go`
- Modify: `backend/internal/auth/service_signup.go` — extend invite consumption to also accept platform invite tokens
- Modify: `backend/internal/auth/service_signup_invite_test.go` — add platform-invite signup test
- Modify: wherever the admin router is mounted (find with `grep -rn "admin.NewHandler\|adminHandler.Mount\|/admin" backend/cmd backend/internal/server backend/internal/auth/http.go`)

### Task 0.1 — Add migration for `platform_invites`

- [ ] **Step 1 — Inspect existing migration style**

```bash
ls backend/migrations/ | tail -5
cat backend/migrations/<latest_workspace_invites_migration>.sql  # use as a template
```

- [ ] **Step 2 — Create the new migration**

Schema (mirror `workspace_invites` minus `workspace_id`):

```sql
-- platform-level invites issued by an admin to onboard a new user.
-- distinct from workspace_invites (which bind to a specific workspace).
CREATE TABLE platform_invites (
  id            uuid PRIMARY KEY,
  email         citext,                       -- nullable: admin can mint open tokens
  token_hash    bytea NOT NULL UNIQUE,
  created_by    uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  accepted_at   timestamptz,
  accepted_by   uuid REFERENCES users(id) ON DELETE SET NULL,
  revoked_at    timestamptz,
  revoked_by    uuid REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX platform_invites_pending_idx
  ON platform_invites (expires_at)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;
```

- [ ] **Step 3 — Apply migration**

```bash
make migrate
```

Expected: migration applies clean, `\d platform_invites` in psql shows the table.

- [ ] **Step 4 — Commit**

```bash
git add backend/migrations/
git commit -m "feat(auth): add platform_invites migration for admin-issued signup tokens"
```

### Task 0.2 — Add sqlc queries for platform invites

- [ ] **Step 1 — Inspect sqlc query style**

```bash
ls backend/internal/db/queries/ | grep -i invite
cat backend/internal/db/queries/<workspace_invites_file>.sql
```

- [ ] **Step 2 — Create `backend/internal/db/queries/platform_invites.sql`**

Required queries (use sqlc annotations matching the existing repo style):

```sql
-- name: InsertPlatformInvite :one
INSERT INTO platform_invites (id, email, token_hash, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetPlatformInviteByTokenHash :one
SELECT * FROM platform_invites WHERE token_hash = $1;

-- name: ListPlatformInvitesActive :many
SELECT * FROM platform_invites
WHERE accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC;

-- name: ListPlatformInvitesAll :many
SELECT * FROM platform_invites
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: RevokePlatformInvite :exec
UPDATE platform_invites
SET revoked_at = now(), revoked_by = $2
WHERE id = $1 AND revoked_at IS NULL AND accepted_at IS NULL;

-- name: AcceptPlatformInvite :exec
UPDATE platform_invites
SET accepted_at = now(), accepted_by = $2
WHERE id = $1;
```

- [ ] **Step 3 — Generate**

```bash
make sqlc
```

Expected: new `platform_invites.sql.go` appears under `backend/internal/db/dbq/`. Build still passes: `cd backend && go build ./...`

- [ ] **Step 4 — Commit**

```bash
git add backend/internal/db/queries/platform_invites.sql backend/internal/db/dbq/
git commit -m "feat(auth): generate sqlc queries for platform invites"
```

### Task 0.3 — Identity service: `PlatformInviteService`

- [ ] **Step 1 — Read existing `InviteService` for structure**

```bash
cat backend/internal/identity/invite.go  # or whatever the workspace invite service file is
```

Note the patterns for: token generation (`HashInviteToken`), TTL (look for invite TTL constant), error sentinels (`ErrInviteNotFound`, etc.), audit hooks.

- [ ] **Step 2 — Write the failing test first**

`backend/internal/identity/platform_invite_test.go`:

```go
package identity_test

// Test cases:
// - Create returns plaintext + persists hashed
// - Create with empty email is allowed
// - Preview returns sanitized invite or sentinel for revoked/expired/used
// - Revoke marks invite revoked; second revoke is noop
// - Accept marks invite consumed; replay returns ErrInviteAlreadyUsed
// Use the same testdb harness used by invite_test.go (find with: grep -rn "func TestInvite" backend/internal/identity)
```

Write at minimum 5 table-driven tests covering Create, Preview (happy + 4 error sentinels), Revoke, Accept (happy + replay).

- [ ] **Step 3 — Run; expect FAIL (compilation error)**

```bash
cd backend && go test ./internal/identity/ -run TestPlatformInvite -v
```

- [ ] **Step 4 — Implement `backend/internal/identity/platform_invite.go`**

```go
package identity

// PlatformInviteService issues / revokes / accepts admin-minted signup tokens.
// Distinct from InviteService (workspace-scoped). Token plaintext returned only
// on Create; everything else uses the hashed lookup.

type PlatformInvite struct {
    ID         uuid.UUID
    Email      *string
    CreatedBy  uuid.UUID
    CreatedAt  time.Time
    ExpiresAt  time.Time
    AcceptedAt *time.Time
    AcceptedBy *uuid.UUID
    RevokedAt  *time.Time
    RevokedBy  *uuid.UUID
}

type PlatformInviteService struct {
    pool *pgxpool.Pool
    now  func() time.Time
    ttl  time.Duration  // suggest 14 days; expose via constructor
}

func NewPlatformInviteService(pool *pgxpool.Pool) *PlatformInviteService { ... }

// Create returns (invite, plaintext, error). Plaintext is the only chance to
// see the raw token — caller surfaces it in the API response.
func (s *PlatformInviteService) Create(ctx context.Context, createdBy uuid.UUID, email string) (PlatformInvite, string, error)

// Preview is the no-auth endpoint analog (returns sanitized invite or sentinel).
func (s *PlatformInviteService) Preview(ctx context.Context, plaintext string) (PlatformInvitePreview, error)

// Revoke marks an invite revoked. Idempotent: revoking an already-revoked invite is a noop (no error).
// Accepting a revoked invite returns ErrInviteRevoked.
func (s *PlatformInviteService) Revoke(ctx context.Context, id, by uuid.UUID) error

// AcceptTx consumes an invite inside an existing transaction (called from signup).
// Returns ErrInviteEmailMismatch if invite has an email and it doesn't match the signup email.
func (s *PlatformInviteService) AcceptTx(ctx context.Context, tx dbq.DBTX, plaintext, signupEmail string, userID uuid.UUID) error

// List returns active (non-expired, non-revoked, non-accepted) invites for the admin UI.
func (s *PlatformInviteService) ListActive(ctx context.Context) ([]PlatformInvite, error)
```

Reuse `HashInviteToken` from the existing invite service (or extract to a shared helper if it's package-private to the workspace invite file). Reuse the same sentinel errors (`ErrInviteNotFound`, `ErrInviteRevoked`, `ErrInviteAlreadyUsed`, `ErrInviteExpired`, `ErrInviteEmailMismatch`).

- [ ] **Step 5 — Run tests; expect PASS**

```bash
cd backend && go test ./internal/identity/ -run TestPlatformInvite -v
```

- [ ] **Step 6 — Commit**

```bash
git add backend/internal/identity/platform_invite.go backend/internal/identity/platform_invite_test.go
git commit -m "feat(identity): add PlatformInviteService with create/revoke/accept/list"
```

### Task 0.4 — HTTP handler `/api/v1/admin/invites`

Routes to mount under the admin router (it's already gated by admin middleware):

- `GET    /admin/invites` — list active platform invites
- `POST   /admin/invites` — create one (returns invite + plaintext); requires fresh reauth
- `DELETE /admin/invites/{id}` — revoke; requires fresh reauth
- `GET    /auth/platform-invites/{token}` — public preview (no auth)

- [ ] **Step 1 — Write failing handler tests**

`backend/internal/auth/http_admin_invites_test.go`. Use the existing httptest harness pattern (search `httptest.NewRecorder` in `backend/internal/auth/http_test.go`). Cover:

```
- POST /admin/invites without admin → 403
- POST /admin/invites without fresh reauth → 401 (or whatever RequireFreshReauth returns; check existing tests)
- POST /admin/invites OK → 201 with {invite, token, acceptUrl}
- DELETE /admin/invites/{id} OK → 204
- GET /admin/invites → 200 list
- GET /auth/platform-invites/{bad-token} → 410 invite_not_found
- GET /auth/platform-invites/{good-token} → 200 sanitized preview
```

- [ ] **Step 2 — Run; expect FAIL**

```bash
cd backend && go test ./internal/auth/ -run TestAdminInvite -v
```

- [ ] **Step 3 — Implement handlers**

```go
// backend/internal/auth/http_admin_invites.go
package auth

type AdminInviteHandler struct {
    auth     *Service
    invites  *identity.PlatformInviteService
    mail     mailer.Mailer
}

func NewAdminInviteHandler(s *Service, inv *identity.PlatformInviteService, m mailer.Mailer) *AdminInviteHandler { ... }

// MountAdmin mounts under the admin subrouter. The caller wires admin gating.
// fresh is the RequireFreshReauth middleware (passed in to match admin.Handler.Mount style).
func (h *AdminInviteHandler) MountAdmin(r chi.Router, fresh func(http.Handler) http.Handler) {
    r.Get("/invites", h.list)
    r.With(fresh).Post("/invites", h.create)
    r.With(fresh).Delete("/invites/{id}", h.revoke)
}

// MountPublic mounts the no-auth preview at /auth/platform-invites/{token}.
func (h *AdminInviteHandler) MountPublic(r chi.Router) {
    r.Get("/auth/platform-invites/{token}", h.preview)
}
```

Response shape on create:

```json
{
  "invite": { "id": "...", "email": "...", "expiresAt": "...", "createdAt": "..." },
  "token":  "<plaintext, shown once>",
  "acceptUrl": "<APP_URL>/accept-invite/<plaintext>"
}
```

(Reuse the same `/accept-invite/{token}` frontend route — Phase 1 task 1.3 teaches the page to detect platform vs workspace invites.)

Best-effort send email if `email` was provided (mirror `http_invites.go:91-106` pattern). Audit each action via `s.WriteAudit` with action codes `admin.invite_created`, `admin.invite_revoked`.

- [ ] **Step 4 — Wire into the router**

Find the admin router mount (look in `backend/cmd/server/main.go` or `backend/internal/server/`):

```bash
grep -rn "admin.NewHandler\|adminHandler" backend/cmd backend/internal | head
```

Add:

```go
adminInviteHandler := auth.NewAdminInviteHandler(authSvc, platformInviteSvc, mailer)
adminInviteHandler.MountAdmin(adminSubrouter, requireFreshReauth)
adminInviteHandler.MountPublic(authSubrouter)  // public preview alongside other auth routes
```

- [ ] **Step 5 — Run tests; expect PASS**

```bash
cd backend && go test ./internal/auth/ -run TestAdminInvite -v
cd backend && go build ./...
```

- [ ] **Step 6 — Commit**

```bash
git add backend/internal/auth/http_admin_invites.go backend/internal/auth/http_admin_invites_test.go backend/cmd/server/main.go
git commit -m "feat(auth): admin platform invite endpoints (create/list/revoke/preview)"
```

### Task 0.5 — Signup consumes platform invite tokens

The signup path currently looks up `workspace_invites` only (`service_signup.go:200`). Extend it: if the workspace invite lookup misses, try platform invite lookup before erroring.

- [ ] **Step 1 — Add failing signup test for platform invite**

In `backend/internal/auth/service_signup_invite_test.go` add:

```go
func TestSignup_ConsumesPlatformInvite_NoWorkspaceMembership(t *testing.T) {
    // Arrange: instance in invite-only mode with one existing user; admin
    //   creates a platform invite for "alice@example.com".
    // Act: signup with that token.
    // Assert: user created, fresh workspace created (current default behaviour),
    //   NO membership added to anyone else's workspace, platform invite marked accepted_at.
}

func TestSignup_PlatformInviteEmailMismatchRejected(t *testing.T) { ... }
func TestSignup_RevokedPlatformInviteRejected(t *testing.T) { ... }
```

- [ ] **Step 2 — Run; expect FAIL**

```bash
cd backend && go test ./internal/auth/ -run TestSignup_.*PlatformInvite -v
```

- [ ] **Step 3 — Modify `service_signup.go:199-233` invite consumption block**

Pseudocode:

```go
if in.InviteToken != "" {
    hash := identity.HashInviteToken(in.InviteToken)

    // Try workspace invite first (existing behaviour).
    wsInv, wsErr := q.GetWorkspaceInviteByTokenHash(ctx, hash)
    switch {
    case wsErr == nil:
        // existing workspace-invite consumption block (unchanged)
    case errors.Is(wsErr, pgx.ErrNoRows):
        // Fall through to platform invite lookup.
    default:
        return nil, fmt.Errorf("select workspace invite: %w", wsErr)
    }

    if wsErr != nil { // i.e. pgx.ErrNoRows
        platInv, plErr := q.GetPlatformInviteByTokenHash(ctx, hash)
        switch {
        case plErr == nil:
            // Validate: not revoked, not accepted, not expired, email matches if set.
            // On success: q.AcceptPlatformInvite(ctx, platInv.ID, userID)
            // Audit: writeAuditTx(... action="user.signup_via_platform_invite" ...)
        case errors.Is(plErr, pgx.ErrNoRows):
            return nil, identity.ErrInviteNotFound
        default:
            return nil, fmt.Errorf("select platform invite: %w", plErr)
        }
    }
}
```

Important: platform invite acceptance does NOT add a membership to anyone's workspace — the user gets only their own workspace from the existing `InsertWorkspaceTx` call earlier in signup. That's the whole point of platform vs workspace invites.

- [ ] **Step 4 — Run all signup tests; expect PASS**

```bash
cd backend && go test ./internal/auth/ -run TestSignup -v
```

- [ ] **Step 5 — Commit**

```bash
git add backend/internal/auth/service_signup.go backend/internal/auth/service_signup_invite_test.go
git commit -m "feat(auth): signup accepts platform invite tokens"
```

### Phase 0 closeout

- [ ] **Step 1 — Full backend test sweep**

```bash
cd backend && go test ./...
```

Expected: all green. Fix any regressions from the signup-flow changes before proceeding.

- [ ] **Step 2 — Push branch**

```bash
git push -u origin feat/alpha-onboarding
```

---

## Phase 1 — Frontend: admin invite UI + accept-invite polish

**Files:**

- Modify: `web/lib/admin/client.ts` (or wherever `useAdminUsers` lives) — add `useAdminInvites`, `useCreateAdminInvite`, `useRevokeAdminInvite`
- Create: `web/components/admin/invite-user-dialog.tsx`
- Modify: `web/app/admin/users/page.tsx` — add "Invite user" action + invites tab/section
- Modify: `web/app/accept-invite/[token]/page.tsx` — handle both workspace + platform invite previews
- Create or modify: `web/lib/api/client.ts` — fetch helpers for `/auth/platform-invites/{token}` preview
- Modify: `web/components/invites/new-invite-dialog.tsx` — show "Copy link" button alongside email send

### Task 1.1 — Admin invites client hooks

- [ ] **Step 1 — Inspect existing admin client patterns**

```bash
cat web/lib/admin/client.ts
```

- [ ] **Step 2 — Add hooks in the same style as `useAdminUsers`**

```ts
// useAdminInvites: GET /api/v1/admin/invites
// useCreateAdminInvite: POST /api/v1/admin/invites — returns {invite, token, acceptUrl}
// useRevokeAdminInvite: DELETE /api/v1/admin/invites/{id}
// All use credentials:"include" + X-Folio-Request:"1" headers per existing convention.
```

If reauth is required and the request returns 401 with a reauth hint, throw a typed error so the caller can show a "Confirm your password to continue" dialog. Check how existing reauth-gated calls handle this (search: `grep -rn "reauth_required\|reauthRequired" web/lib`). If no pattern exists, just throw the error message and the dialog can show "Action requires recent sign-in — sign out and back in."

- [ ] **Step 3 — Build (typecheck)**

```bash
cd web && pnpm typecheck
```

- [ ] **Step 4 — Commit**

```bash
git add web/lib/admin/client.ts
git commit -m "feat(web): admin invite client hooks"
```

### Task 1.2 — `InviteUserDialog` component + admin page integration

**Read the frontend skill first:** `/Users/xmedavid/dev/folio/.claude/skills/folio-frontend-design/SKILL.md`. Use existing `Dialog`, `Input`, `Button` from `web/components/ui/`.

- [ ] **Step 1 — Create `web/components/admin/invite-user-dialog.tsx`**

Behavior:

1. Trigger button in dialog passes through children; admin page renders `<InviteUserDialog><Button>Invite user</Button></InviteUserDialog>`.
2. Form: email (optional — empty creates an open invite), submit.
3. On success: replace form with the returned `acceptUrl` + Copy button + "Send by email yourself if email isn't configured." Stays mounted until dismissed.
4. On 4xx error: render `result.error.message` inline.
5. Mutate query cache to refresh the invite list.

Critical UX: the plaintext token is shown ONCE. Make this obvious — copy button, "this won't be shown again," explicit dismiss.

- [ ] **Step 2 — Wire into `web/app/admin/users/page.tsx`**

Two changes:

1. Add `<InviteUserDialog><Button>Invite user</Button></InviteUserDialog>` to the `PageHeader` `actions` slot alongside the "Admins only" toggle.
2. Add a second section below the users table titled "Pending invites" — render `useAdminInvites().data` as a `DataTable` with columns: Email (or "Open invite"), Created, Expires, Created by, [Revoke button]. Revoke button calls `useRevokeAdminInvite` and confirms inline.

- [ ] **Step 3 — Browser smoke test**

Per the folio-frontend-design skill, start the dev server and exercise the flow.

```bash
make dev  # in a background terminal; or use docker compose up if you prefer
```

Walk through:

1. Sign in as the bootstrapped admin → `/admin/users` shows the "Invite user" button.
2. Click → enter `tester+1@example.com` → submit.
3. Confirm response shows accept URL with a Copy button.
4. Pending invite row appears below.
5. Open a private window → paste accept URL → arrives at signup page (Phase 1 task 1.3 will polish that).
6. Revoke the invite → row disappears or marks revoked.

- [ ] **Step 4 — Commit**

```bash
git add web/components/admin/invite-user-dialog.tsx web/app/admin/users/page.tsx
git commit -m "feat(web): admin invite user dialog + pending invites section"
```

### Task 1.3 — Accept-invite page handles both invite types

The frontend route `/accept-invite/[token]` currently only knows about workspace invites. Teach it to first try the workspace preview endpoint, fall back to the platform preview, and present the right CTA.

- [ ] **Step 1 — Inspect current accept page**

```bash
cat web/app/accept-invite/\[token\]/page.tsx
```

- [ ] **Step 2 — Update preview logic**

```ts
// Try workspace preview first.
const wsRes = await fetch(`/api/v1/auth/invites/${token}`, { headers: { "X-Folio-Request": "1" }});
if (wsRes.ok) { setKind("workspace"); setData(await wsRes.json()); return; }

// 404? Try platform invite.
if (wsRes.status === 404 || wsRes.status === 410) {
  const plRes = await fetch(`/api/v1/auth/platform-invites/${token}`, { headers: { "X-Folio-Request": "1" }});
  if (plRes.ok) { setKind("platform"); setData(await plRes.json()); return; }
}
// else: show "invalid invite" with the typed code from whichever endpoint returned 410.
```

Render copy:

- Workspace invite: existing copy ("Alice invited you to join Household as a member"). On accept (signed-in path), POST to `/api/v1/auth/invites/{token}/accept`. On signed-out path, route to `/signup?invite=<token>` — signup form pre-fills email and includes the token in the request body.
- Platform invite: "You've been invited to Folio. Create your account to get started." Always routes to `/signup?invite=<token>` (platform invites don't make sense for already-signed-in users; show a message if a session exists).

- [ ] **Step 3 — Update `/signup` page to read `?invite=` and submit it**

```bash
cat web/app/signup/page.tsx
```

If it already passes invite token to the API: confirm. If not, add it. The backend signup handler already accepts `inviteToken` in the body — verify with `grep -n inviteToken backend/internal/auth/http.go`.

- [ ] **Step 4 — Browser smoke test**

1. Continue the Task 1.2 walk-through: paste the platform invite URL → see "Create your account" copy → submit signup → land on `/w/<your-new-workspace>`.
2. Re-do for a workspace invite issued from `web/components/invites/new-invite-dialog.tsx` (use a workspace-owner account).

- [ ] **Step 5 — Commit**

```bash
git add web/app/accept-invite/\[token\]/page.tsx web/app/signup/page.tsx
git commit -m "feat(web): accept-invite page handles platform + workspace invites"
```

### Task 1.4 — Workspace invite dialog shows copy-link

- [ ] **Step 1 — Inspect current dialog**

```bash
cat web/components/invites/new-invite-dialog.tsx
```

- [ ] **Step 2 — Confirm backend response includes plaintext token**

Look at `http_invites.go:112` — it returns `inv` only (the model, no plaintext). The plaintext token is only captured in `plaintext` and embedded in the email URL. **This is a backend gap** — the API response should include `acceptUrl` too so the UI can show copy-link.

Add a small backend change: extend the `createInvite` response shape to `{invite: ..., acceptUrl: ...}`. Update the existing handler test to match. Do this as a sub-task with its own commit:

```bash
# Sub-task 1.4a — backend
# Modify http_invites.go createInvite to return {invite, acceptUrl}.
# Update any existing tests that assert on the old shape.
git commit -m "feat(auth): include acceptUrl in workspace invite create response"
```

- [ ] **Step 3 — Update the dialog**

After successful create, render the same "Copy link" UI block as the admin invite dialog (extract a small `<InviteSuccess url={...} onDismiss={...}/>` shared component if you want; otherwise duplicate — DRY can wait if it's just two call sites).

- [ ] **Step 4 — Update the typed client**

If `web/lib/api/client.ts` has a typed `createInvite` function, update its return type. Run `make openapi` if the OpenAPI spec describes invites and you choose to update the spec — otherwise leave the types as inline.

- [ ] **Step 5 — Browser smoke test**

Sign in as a workspace owner → workspace settings members tab → invite member → confirm copy-link button appears alongside email.

- [ ] **Step 6 — Commit**

```bash
git add web/components/invites/new-invite-dialog.tsx web/lib/api/client.ts
git commit -m "feat(web): workspace invite dialog shows copy-link affordance"
```

### Phase 1 closeout

- [ ] **Step 1 — Full typecheck + backend test sweep**

```bash
cd web && pnpm typecheck && pnpm lint
cd backend && go test ./...
```

- [ ] **Step 2 — Manual end-to-end smoke**

1. Bootstrap admin → invite `alpha-tester@example.com` (platform invite).
2. Tester opens accept URL → signs up → lands in their own workspace.
3. Tester invites `partner@example.com` (workspace invite).
4. Partner opens link → signs up via `?invite=` → accepts → joins tester's workspace.
5. Tester revokes a third pending invite from the workspace settings.

If anything breaks, fix and amend the relevant commit.

---

## Phase 2 — Workspace creation UI + last-workspace redirect

### Task 2.1 — `web/app/workspaces/new/page.tsx`

- [ ] **Step 1 — Inspect signup page form for the field shape (currency, locale, timezone, cycleAnchorDay)**

```bash
cat web/app/signup/page.tsx
```

- [ ] **Step 2 — Inspect backend create endpoint**

`backend/internal/auth/http.go:230` `createWorkspace` accepts `createWorkspaceReq`. Read it to confirm the request schema.

- [ ] **Step 3 — Create the page**

```tsx
// web/app/workspaces/new/page.tsx
"use client";
// Form fields: name (required), baseCurrency (default USD, dropdown of common currencies), cycleAnchorDay (1, default), locale (en-US default), timezone (Intl.DateTimeFormat().resolvedOptions().timeZone default).
// On submit: POST /api/v1/workspaces { name, baseCurrency, cycleAnchorDay, locale, timezone }.
// On 201: invalidate ["me"] query, router.push(`/w/${created.slug}`).
// On 422 slug collision: show inline error.
```

Use existing form primitives from `web/components/ui`. Mirror styling of `web/app/signup/page.tsx`.

- [ ] **Step 4 — Add CTA on `/workspaces`**

In `web/app/workspaces/page.tsx`, add a "Create workspace" button at the top of the list (use `Button` + `Link` to `/workspaces/new`).

- [ ] **Step 5 — Empty state on `/workspaces`**

If `id.data.workspaces.length === 0`, replace the list with a centred CTA: "You don't have any workspaces yet. Create one to get started." with a primary "Create workspace" button.

- [ ] **Step 6 — Browser smoke test**

Sign in → `/workspaces` → click create → fill form → submit → land on the new workspace's `/w/<slug>` route. Switcher includes the new workspace.

- [ ] **Step 7 — Commit**

```bash
git add web/app/workspaces/
git commit -m "feat(web): workspace creation page with CTA on /workspaces"
```

### Task 2.2 — Last-workspace redirect on login

- [ ] **Step 1 — Read `web/app/login/page.tsx:52-67`** (already seen). The function `finishLogin` always picks `me.workspaces[0]`.

- [ ] **Step 2 — Confirm `/me` returns `lastWorkspaceId`**

```bash
grep -n "lastWorkspaceId\|LastWorkspaceID" backend/internal/auth/http.go web/lib/api/types.ts web/lib/hooks/use-identity.ts
```

If not present in the response, add it to `Handler.me` in `backend/internal/auth/http.go`. Update the typed `MeUser` in the web client.

- [ ] **Step 3 — Update `finishLogin` redirect logic**

```ts
const me = await meRes.json();
const last = me.lastWorkspaceId
  ? me.workspaces.find((w) => w.id === me.lastWorkspaceId)
  : null;
const target = last ?? me.workspaces?.[0];
router.push((target ? `/w/${target.slug}` : "/workspaces") as Route);
```

- [ ] **Step 4 — Update `lastWorkspaceId` on workspace switch**

The switcher at `web/components/workspace-switcher.tsx:7` navigates to `/w/{slug}` but doesn't tell the backend the user switched. Look for an existing endpoint that updates `last_workspace_id` (search: `grep -rn "UpdateUserLastWorkspace\|/me/last-workspace\|last_workspace" backend/internal/auth`). If none exists, add one:

- Backend: `PATCH /api/v1/me/last-workspace { workspaceId }` — verifies membership, calls `UpdateUserLastWorkspace`. Add a unit test.
- Frontend: in the switcher's `onChange`, fire-and-forget `fetch("/api/v1/me/last-workspace", {method:"PATCH", body: JSON.stringify({workspaceId: nextId})})` before navigating.

Do these as two commits: backend first (with test), then frontend.

- [ ] **Step 5 — Browser smoke test**

1. User with 2+ workspaces. Switch to workspace B. Sign out. Sign back in. Confirm landing on B, not A.
2. Delete B (when delete is supported), sign in again, confirm fall-through to first remaining workspace.

- [ ] **Step 6 — Commits**

```bash
git commit -m "feat(auth): /me/last-workspace endpoint + me response carries lastWorkspaceId"
git commit -m "feat(web): login redirects to last workspace; switcher updates server-side"
```

### Phase 2 closeout

```bash
cd web && pnpm typecheck && pnpm lint
cd backend && go test ./...
```

---

## Phase 3 — Security UX completion

### Task 3.1 — TOTP disable button

Backend already exposes `DELETE /api/v1/me/mfa/totp` (see `http.go:55`). Frontend just needs a button.

- [ ] **Step 1 — Add `disableTOTP` to `web/lib/api/client.ts`**

```ts
export async function disableTOTP() {
  const res = await fetch("/api/v1/me/mfa/totp", {
    method: "DELETE",
    credentials: "include",
    headers: { "X-Folio-Request": "1" },
  });
  if (!res.ok) throw new Error(await res.text());
}
```

- [ ] **Step 2 — Update `web/app/settings/security/page.tsx` Authenticator section**

When `data?.totpEnrolled` is true, replace the "Set up" button with a "Disable" button (destructive variant, confirm via inline button-press-twice or `<ConfirmDialog>` if one exists). On confirm: call `disableTOTP`, invalidate `mfa-status`, show "Authenticator removed."

If a 401 reauth-required response comes back, show an inline message "Sign out and back in to remove your authenticator" (the existing `RequireFreshReauth` window is brief — actual reauth UI is out of scope for this plan; document in gaps).

- [ ] **Step 3 — Browser smoke test**

Enroll TOTP → recovery codes shown → click Disable → confirm → "Not enabled" again. Recovery codes count drops to 0 (verify via `mfa-status` query refresh).

- [ ] **Step 4 — Commit**

```bash
git commit -m "feat(web): TOTP disable button on security settings"
```

### Task 3.2 — Passkey list / delete (backend + frontend)

**Backend missing:** there is no `ListPasskeys` or `DeletePasskey` handler. Add both.

- [ ] **Step 1 — Add sqlc queries**

`backend/internal/db/queries/passkeys.sql` (or wherever passkey credentials are queried — search: `grep -rn "passkey_credentials\|webauthn_credentials" backend/internal/db/queries`):

```sql
-- name: ListPasskeysByUser :many
SELECT id, label, created_at, last_used_at
FROM passkey_credentials  -- adjust table name to actual schema
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: DeletePasskey :exec
DELETE FROM passkey_credentials
WHERE id = $1 AND user_id = $2;
```

Run `make sqlc`. Inspect the actual passkey table name first via `\d` in psql or `grep -rn "passkey" backend/internal/db/queries`.

- [ ] **Step 2 — Failing handler tests**

`backend/internal/auth/http_mfa_test.go` (or new file): test list returns user's passkeys; delete removes by id and only when owned by caller.

- [ ] **Step 3 — Add handlers**

```go
// In http_mfa.go
func (h *Handler) listPasskeys(w http.ResponseWriter, r *http.Request) { ... }
func (h *Handler) deletePasskey(w http.ResponseWriter, r *http.Request) { ... }
```

Wire in `http.go`:

```go
r.Get("/me/mfa/passkeys", h.listPasskeys)
r.With(reauth).Delete("/me/mfa/passkeys/{id}", h.deletePasskey)
```

Audit: `passkey.removed` action.

- [ ] **Step 4 — Tests pass + build green**

```bash
cd backend && go test ./... && go build ./...
git commit -m "feat(auth): list + delete passkey endpoints"
```

- [ ] **Step 5 — Frontend client functions**

```ts
// web/lib/api/client.ts
export async function listPasskeys(): Promise<{id, label, createdAt, lastUsedAt}[]> { ... }
export async function deletePasskey(id: string) { ... }
```

- [ ] **Step 6 — Update Passkeys section in `web/app/settings/security/page.tsx`**

Replace the simple "X registered" line with an inline list. Each row: label, created date, last used date, [Delete button]. Plus the existing "Add" button below. Wrap delete in confirmation. Use TanStack Query `useQuery({ queryKey: ["passkeys"], queryFn: listPasskeys })`.

- [ ] **Step 7 — Browser smoke test**

Add a passkey via Touch ID / fake authenticator → row appears with the credential label → delete → row disappears.

- [ ] **Step 8 — Commit**

```bash
git commit -m "feat(web): passkey list + delete on security settings"
```

### Task 3.3 — Change password while logged in

**Backend missing.** Add it.

- [ ] **Step 1 — Failing test in `backend/internal/auth/service_login_test.go`** (or new `service_password_test.go`):

```go
// TestChangePassword_HappyPath
// TestChangePassword_WrongCurrentPassword
// TestChangePassword_RevokesOtherSessionsButKeepsCurrent
// TestChangePassword_FailsPasswordPolicy
```

- [ ] **Step 2 — Implement service method**

```go
// backend/internal/auth/service.go (or new file service_password.go)
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentSessionID, current, next string) error {
    // 1. fetch user; verify current password matches PasswordHash
    // 2. CheckPasswordPolicy(next, email, displayName)
    // 3. HashPassword(next, secret)
    // 4. UpdateUserPasswordHash
    // 5. Revoke all sessions for user EXCEPT currentSessionID (so the user stays signed in here)
    // 6. Audit "user.password_changed"
}
```

- [ ] **Step 3 — Add HTTP handler**

```go
// backend/internal/auth/http.go (or http_password.go)
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) { ... }

// In MountAuthed:
r.With(reauth).Post("/me/password", h.changePassword)
```

Test the route via httptest.

- [ ] **Step 4 — Backend tests pass + build**

```bash
cd backend && go test ./... && go build ./...
git commit -m "feat(auth): change password while logged in (revokes other sessions)"
```

- [ ] **Step 5 — Frontend page `web/app/settings/security/page.tsx` add Password section**

Section above Authenticator. Form: current password, new password, confirm new. Submit → POST `/api/v1/me/password`. On success: success message + "Other sessions have been signed out for safety."

If reauth-required (401): inline message + link to log out and back in.

- [ ] **Step 6 — Browser smoke test**

1. Change password → success → reload — still signed in.
2. Other browser/device sessions are gone (you can verify by signing in from a private window before the change, then refreshing it after).

- [ ] **Step 7 — Commit**

```bash
git commit -m "feat(web): change password section on security settings"
```

### Phase 3 closeout

```bash
cd backend && go test ./...
cd web && pnpm typecheck && pnpm lint
```

---

## Phase 4 — Workspace member / invite polish

### Task 4.1 — Pending invites list with resend + revoke + copy-link in workspace settings

- [ ] **Step 1 — Find the workspace members settings page**

```bash
grep -rn "members\|Members" web/app/w/\[slug\]/ | head
```

- [ ] **Step 2 — Add backend "list invites" endpoint if missing**

```bash
grep -rn "ListInvites\|listInvites" backend/internal/auth backend/internal/identity
```

If the workspace currently has no `GET /t/{workspaceId}/invites`, add it (mirror the workspace-scoped invite handler structure). Return active invites only by default; `?include=all` for revoked/expired. Mount under `MountWorkspaceInvites` adjacent to `Post("/")` and `Delete("/{inviteId}")`.

Add a handler test.

```bash
git commit -m "feat(auth): list workspace invites endpoint"
```

- [ ] **Step 3 — Add resend endpoint**

```go
// POST /t/{workspaceId}/invites/{inviteId}/resend
// Validates: invite is active and not consumed.
// Re-issues a new plaintext token (rotates token_hash + extends expiry by ttl).
// Re-sends email best-effort.
// Audit "member.invite_resent".
// Returns {invite, acceptUrl}.
```

This requires a new sqlc query `RotateInviteToken` (UPDATE token_hash + expires_at, RETURNING). Add it.

```bash
git commit -m "feat(auth): resend workspace invite (rotates token)"
```

- [ ] **Step 4 — Frontend: members tab shows invites table**

Below the existing members list, render "Pending invites" as a `DataTable`:

| Email | Role | Inviter | Expires | Actions |
| --- | --- | --- | --- | --- |
| ... | ... | ... | ... | [Copy link] [Resend] [Revoke] |

Copy link uses the current invite's URL (you'll need to either store the plaintext from create response in local state, or have resend rotate + return a fresh link). Decision: **Copy link only works on freshly created/resent invites**, since the server can't reproduce plaintext from the hash. UI should explain: "Copy link is available right after sending or resending an invite."

Simpler: after resend, show the same `<InviteSuccess>` dialog as create.

- [ ] **Step 5 — Browser smoke test**

Owner invites; sees pending row; clicks resend; gets fresh copy-link; clicks revoke; row disappears.

- [ ] **Step 6 — Commit**

```bash
git commit -m "feat(web): workspace invites table with resend, revoke, copy-link"
```

### Task 4.2 — Better blocked-action error messages

The backend already returns typed error codes (`last_owner`, `last_workspace`, `not_a_member`, `email_mismatch`, `email_unverified`, `invite_expired`, etc.). The frontend mostly shows raw HTTP error text.

- [ ] **Step 1 — Audit current error handling**

```bash
grep -rn "res\.ok\|response\.error\|\.message" web/components/invites web/components/members 2>/dev/null | head -20
```

- [ ] **Step 2 — Add a small typed error helper**

```ts
// web/lib/api/errors.ts
export const FRIENDLY: Record<string, string> = {
  last_owner: "You're the only owner — promote someone else first.",
  last_workspace: "You can't leave your only workspace. Create another one first.",
  email_mismatch: "This invite was sent to a different email.",
  email_unverified: "Verify your email address before accepting this invite.",
  invite_expired: "This invite has expired. Ask the inviter to send a new one.",
  invite_revoked: "This invite was revoked.",
  invite_already_used: "This invite has already been used.",
  invite_not_found: "We couldn't find that invite.",
};
export function friendlyError(code: string | undefined, fallback: string) {
  return (code && FRIENDLY[code]) ?? fallback;
}
```

- [ ] **Step 3 — Use it in invite + members components**

Wherever you currently throw `new Error(res.statusText)`, parse the response body for `{error: {code, message}}` and call `friendlyError(code, message)`.

- [ ] **Step 4 — Browser smoke test**

Force each failure mode (invite already accepted, invite to different email, last-owner demote attempt) and confirm the friendly text appears.

- [ ] **Step 5 — Commit**

```bash
git commit -m "feat(web): friendly error messages for membership/invite failures"
```

---

## Phase 5 — Profile basics

### Task 5.1 — Display name edit

- [ ] **Step 1 — Backend: confirm endpoint exists**

```bash
grep -rn "displayName\|DisplayName" backend/internal/auth/http.go | head
```

If `PATCH /api/v1/me { displayName }` doesn't exist, add it (validates 1-80 chars, trims, audit `user.profile_updated`). Test it.

- [ ] **Step 2 — Frontend: settings/account page**

```bash
ls web/app/settings/account
```

If page exists, add a small form near the top: display name input + Save. Submits PATCH `/api/v1/me`. On success, invalidate `["me"]` query so the topbar/avatar updates.

If page doesn't exist, create `web/app/settings/account/page.tsx` with display name + email (read-only for now; existing email change flow lives elsewhere — link to it).

- [ ] **Step 3 — Browser smoke test**

Change name → reload → reflected in topbar / member list.

- [ ] **Step 4 — Commit**

```bash
git commit -m "feat: display name edit on account settings"
```

---

## Phase 6 — Manual end-to-end alpha smoke test

Not a coding task — a checklist the executing agent runs in a browser at the end. Reports results back to the user.

- [ ] **Bootstrap admin sign-in** — admin lands somewhere usable.
- [ ] **Admin invites user A** (platform invite) — copy-link path works without SMTP.
- [ ] **User A signs up via invite** — lands in their fresh workspace.
- [ ] **User A creates a second workspace** — switcher shows both.
- [ ] **User A switches workspaces** — refresh / re-login lands on the last one.
- [ ] **User A invites user B to workspace 1** (workspace invite) — copy-link works.
- [ ] **User B signs up via workspace invite** — lands in workspace 1 as a member.
- [ ] **User B leaves the workspace** — gone from member list.
- [ ] **User A enrols TOTP** — recovery codes shown.
- [ ] **User A signs out and back in** — MFA challenge required, recovery code accepted.
- [ ] **User A disables TOTP** — works.
- [ ] **User A registers a passkey** — appears in list.
- [ ] **User A deletes the passkey** — gone.
- [ ] **User A changes password** — still signed in here; private window session signed out.
- [ ] **User A requests password reset** — email link (or log mailer output) works end-to-end.
- [ ] **Admin revokes a pending invite** — accept URL now shows "invite revoked."

If any step fails, file as a TODO inline in the test report, not as a silent fix — the user is supervising.

---

## Documented gaps (not fixed in this plan, deferred to follow-up plans)

Write `docs/superpowers/notes/2026-04-29-alpha-onboarding-gaps.md` listing what's intentionally not done here, so the user has a single inventory to plan against later. Do this as the final commit.

Contents (template):

```markdown
# Alpha Onboarding — Documented Gaps

After 2026-04-29-alpha-onboarding-plan completes, these are knowingly missing:

## Identity & security
- Reauth UI (in-app password prompt for sensitive ops). Backend supports it; UI just shows "sign out and back in" as workaround.
- Sessions / devices list with revoke.
- Passkey rename.
- TOTP rotation (regenerate authenticator key without disable+re-enrol).
- Email change UI in settings (current backend supports it; surface in account page).

## Workspaces
- Soft-deleted workspace owner-restore UI on /workspaces.
- Workspace settings polish (slug edit visibility).
- Workspace switcher search/filter when N is large.

## Onboarding
- First-run wizard (account → workspace → first account → import → invites).
- Sample data mode.
- "Continue setup" CTA on workspace dashboard.

## Data ownership
- Transaction CSV export.
- Full workspace bundle export (JSON+CSV ZIP).
- Bundle import.
- User account delete.

## Audit / admin
- Workspace-scoped audit log UI for owners/members.
- Admin instance usage dashboard (totals, storage, queue health).
- Backup status / runbook page.

## API contract
- OpenAPI sweep — new admin invite, list invites, resend invite, last-workspace, change password, list/delete passkey routes need to be added to openapi/openapi.yaml and types regenerated.

## PWA
- Offline transaction draft + sync queue.
- Offline-aware workspace shell beyond what's already cached.
```

```bash
git add docs/superpowers/notes/2026-04-29-alpha-onboarding-gaps.md
git commit -m "docs: alpha onboarding deferred gaps inventory"
```

---

## Final sign-off

```bash
cd backend && go test ./...
cd web && pnpm typecheck && pnpm lint && pnpm build
git push -u origin feat/alpha-onboarding
gh pr create --title "Alpha onboarding: invite, workspace creation, security UX" --body "..."
```

Then surface the PR URL to the user with the smoke-test checklist results.
