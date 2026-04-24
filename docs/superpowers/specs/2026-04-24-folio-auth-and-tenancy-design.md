# Folio — Authentication, Users, and Multi-Tenancy Design

**Status:** Approved for implementation
**Date:** 2026-04-24
**Source:** `FEATURE-BIBLE.md` (§1 Overview, §39 Security), `docs/superpowers/specs/2026-04-24-folio-domain-v2-design.md`
**Supersedes:**
- The 1:1 tenant↔user invariant on `users.tenant_id` in `backend/db/migrations/20260424000001_identity.sql`.
- The `X-Tenant-ID` / `X-User-ID` header stand-in middleware `httpx.RequireTenant` in `backend/internal/httpx/httpx.go`.
- The password-less `Bootstrap` handler on `backend/internal/identity/service.go`.

## 1. Goal

Land the identity, authentication, and authorisation layer for the final product — self-hosted and SaaS deployments from the same codebase. This is not an MVP slice: password + passkey + TOTP, invites, email verification, password reset, step-up MFA, soft-deleted tenants, and per-tenant membership roles are all in scope from day one.

After this lands:
- There is no `X-Tenant-ID` dev bridge anywhere in the stack.
- A user can belong to many tenants with a role per membership; tenants can have many owners.
- Login uses passkeys-first or password-first at the user's choice, with optional TOTP as 2FA.
- Tenant-scoped URLs (`/t/{slug}/…`) replace the global dashboard shape.

## 2. Non-goals

- **Billing / plans / metering.** SaaS commercials come later; the tenant has no billing column yet.
- **SSO** (SAML, OIDC, social login). Layer on later as additional credential types.
- **SCIM / directory sync.** Not in scope.
- **Magic-link (email-only) login.** Passkeys are the passwordless path.
- **Row-level security.** Defer per the v2 domain design; isolation continues to come from service-layer scoping + composite FKs.
- **Admin impersonation** — viewing a tenant's financial data as if a member. Privacy/legal-sensitive for fintech; wants its own spec (reason-required, tightly audited, read-only, feature-flagged).
- **Cross-tenant aggregate views** (e.g. "all my net worth across Personal + Household"). Different base currencies, different cycle anchors — non-trivial and out of scope.

## 3. Domain model

### 3.1 Cross-cutting conventions (unchanged from v2 design)

- UUIDv7 IDs generated app-side.
- `money_currency` domain for every currency column.
- Composite FK target `UNIQUE (tenant_id, id)` on every tenant-scoped table.
- `set_updated_at()` trigger on every table that carries `updated_at`.

### 3.2 Changes to existing tables

**`users`** (from `20260424000001_identity.sql`):

- **Remove** `tenant_id uuid` column, its `UNIQUE (tenant_id)` constraint, and the composite `UNIQUE (tenant_id, id)` target.
- **Add:**
  - `email_verified_at timestamptz` — set by the email-verification flow.
  - `last_tenant_id uuid references tenants(id) on delete set null` — last-used tenant for post-login redirect.
  - `is_admin boolean not null default false` — instance-level admin flag for the admin console (§11). Orthogonal to tenant membership; granted via CLI (`folio-admin grant`) or first-run env bootstrap.
- `password_hash text not null` — tightened from nullable. Signup must supply a password (the existing `Bootstrap` handler becomes `auth.Signup` and takes a password).

**`tenants`:**

- **Add:**
  - `slug citext not null unique check (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$')`.
  - `deleted_at timestamptz` for soft delete.
- Soft-delete partial index: `create index tenants_deleted_at_idx on tenants (deleted_at) where deleted_at is not null;` — supports the hard-delete sweeper job.
- Existing `tenants` FKs elsewhere (every financial table's `tenant_id`) continue pointing at `tenants(id)` unchanged; they'll see soft-deleted tenants unless the service layer filters on `deleted_at is null`, which it must do everywhere except the restore endpoint.

**`sessions`:**

- **Add:**
  - `last_seen_at timestamptz not null default now()` — sliding-expiry anchor and device-list UX.
  - `reauth_at timestamptz` — nullable; set on fresh password/MFA re-auth; consumed by the step-up gate (5-minute freshness window).
- `id` remains the SHA-256 hash of the session token (the plaintext token lives only in the cookie).

**`totp_credentials`:**

- **Drop** `recovery_codes_cipher`. Recovery codes move to `auth_recovery_codes` so per-code consumption is tracked cleanly.

### 3.3 New tables

```sql
-- Role within a tenant
create type tenant_role as enum ('owner', 'member');

-- A user can belong to many tenants; a tenant can have many users
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
create index tenant_memberships_owners
  on tenant_memberships (tenant_id)
  where role = 'owner';
create trigger tenant_memberships_updated_at
  before update on tenant_memberships
  for each row execute function set_updated_at();

-- Invitations to join a tenant
create table tenant_invites (
  id                  uuid primary key,
  tenant_id           uuid not null references tenants(id) on delete cascade,
  email               citext not null,
  role                tenant_role not null,
  token_hash          bytea not null unique,                 -- SHA-256 of the emailed token
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

-- Unified single-use tokens for email-verify / password-reset / email-change
create table auth_tokens (
  id           uuid primary key,
  user_id      uuid not null references users(id) on delete cascade,
  purpose      text not null
               check (purpose in ('email_verify', 'password_reset', 'email_change')),
  token_hash   bytea not null unique,
  email        citext,                                       -- target address for verify/change flows
  created_at   timestamptz not null default now(),
  expires_at   timestamptz not null,
  consumed_at  timestamptz
);
create index auth_tokens_user_id_idx on auth_tokens (user_id);
create index auth_tokens_live_idx
  on auth_tokens (purpose, expires_at)
  where consumed_at is null;

-- MFA recovery codes, one row per code, Argon2id-hashed
create table auth_recovery_codes (
  id           uuid primary key,
  user_id      uuid not null references users(id) on delete cascade,
  code_hash    text not null,                                -- Argon2id PHC string
  created_at   timestamptz not null default now(),
  consumed_at  timestamptz
);
create index auth_recovery_codes_live_idx
  on auth_recovery_codes (user_id)
  where consumed_at is null;
```

### 3.4 Invariants enforced by the service layer

1. **Every tenant has ≥1 owner at all times.** Demote / remove / leave is blocked if it would drop `tenant_memberships_owners` count to zero. The partial index makes the check a single-digit-ms lookup.
2. **A user cannot leave their last tenant.** UX instead guides them to "create a Personal tenant" or "delete account". Keeps the "every user has ≥1 membership" invariant trivially true (signup already establishes it).
3. **Cross-tenant data references are impossible.** Financial rows keep the composite-FK convention from the v2 design.
4. **Soft-deleted tenants are invisible.** All service-layer reads filter `deleted_at is null` except the explicit "restore" and "list deleted" paths.

### 3.5 Permissions matrix

| Action | Owner | Member |
|---|:---:|:---:|
| Read tenant data (accounts, transactions, goals, …) | ✓ | ✓ |
| Create / edit / delete financial data | ✓ | ✓ |
| Link bank accounts / provider tokens | ✓ | ✓ |
| Invite a **member** | ✓ | ✓ |
| Invite an **owner** | ✓ | ✗ |
| Promote member → owner | ✓ | ✗ |
| Demote owner → member (not self if last owner) | ✓ | ✗ |
| Remove a member | ✓ | ✗ |
| Rename / change slug / change base currency / other settings | ✓ | ✗ |
| Soft-delete or restore tenant | ✓ | ✗ |
| Leave tenant (not if last owner) | ✓ | ✓ |

## 4. HTTP surface

### 4.1 URL shape

- **Web:** slug-based — `folio.app/t/{slug}/accounts`, `folio.app/t/{slug}/transactions`, etc. Top-level namespace (`/login`, `/signup`, `/accept-invite/{token}`, `/settings/account`, `/settings/security`, `/settings/sessions`, `/pricing`, …) is reserved for auth + product pages.
- **API:** UUID-based — `/api/v1/t/{tenantId}/…`. Slug is strictly a web concern; the API never dereferences a slug.
- **Translation:** `GET /api/v1/me` returns `{user, tenants: [{id, slug, name, role}, …]}`. The web client resolves slug → UUID locally on navigation.
- **Slug generation:** auto-slugify from tenant name at create time; on collision append `-2`, `-3`, … If a user types a slug explicitly and it collides, return a validation error and let them choose.

### 4.2 Auth endpoints (public, not tenant-scoped)

| Route | Purpose |
|---|---|
| `POST /api/v1/auth/signup` | Create user + Personal tenant + owner membership + session. Accepts optional `inviteToken`; when present and email matches, also creates a membership in the invited tenant. |
| `POST /api/v1/auth/login` | Email + password. Returns `{mfa_required, challenge_id}` if MFA is enrolled; otherwise sets the session cookie and returns user+tenants. |
| `POST /api/v1/auth/login/mfa/totp` | Exchange `{challenge_id, code}` for a session. |
| `POST /api/v1/auth/login/mfa/recovery` | Exchange `{challenge_id, code}` (recovery code) for a session; marks code consumed. |
| `POST /api/v1/auth/login/webauthn/options` | Begin a passkey assertion ceremony (either password-step-replaced or passkey-first). |
| `POST /api/v1/auth/login/webauthn/verify` | Complete the passkey ceremony and issue a session. |
| `POST /api/v1/auth/logout` | Revoke the current session. |
| `POST /api/v1/auth/verify` | Body `{token}`. Consume email-verification token; set `email_verified_at`. Called by the web page the email link lands on. |
| `POST /api/v1/auth/verify/resend` | Authenticated — re-send verification email, rate-limited. |
| `POST /api/v1/auth/password/reset-request` | Always 200 — enqueues a reset email if the address matches a user. |
| `POST /api/v1/auth/password/reset-confirm` | `{token, newPassword}` — verifies, updates hash, revokes all other sessions. |
| `POST /api/v1/auth/email/change-request` | Authenticated — starts an email-change flow, sends verify link to the new address. |
| `POST /api/v1/auth/email/change-confirm` | Verify-token-driven — updates `users.email`, notifies the old address. |
| `GET  /api/v1/auth/invites/{token}` | Preview an invite: tenant name, inviter display name, role, expiry. No auth required. |
| `POST /api/v1/auth/invites/{token}/accept` | Authenticated — must match invite email; creates membership, consumes token. |

### 4.3 Authenticated, non-tenant-scoped endpoints

| Route | Purpose |
|---|---|
| `GET  /api/v1/me` | Current user + tenant list (id, slug, name, role). |
| `PATCH /api/v1/me` | Update display name, theme, preferences. |
| `POST /api/v1/me/password` | Change password (requires step-up + current password). |
| `GET  /api/v1/me/sessions` | Device list. |
| `DELETE /api/v1/me/sessions/{sessionId}` | Revoke one session. |
| `POST /api/v1/me/sessions/revoke-all?keepCurrent=true` | Sign out everywhere. |
| `GET  /api/v1/me/mfa` | Current MFA state (passkeys list, TOTP enabled, recovery codes remaining). |
| `POST /api/v1/me/mfa/webauthn/register/options` | Begin passkey registration. |
| `POST /api/v1/me/mfa/webauthn/register/verify` | Complete passkey registration. |
| `DELETE /api/v1/me/mfa/webauthn/{credentialId}` | Remove a passkey. |
| `POST /api/v1/me/mfa/totp/enroll` | Begin TOTP setup (returns secret + QR data). |
| `POST /api/v1/me/mfa/totp/confirm` | Confirm with one valid code; reveal recovery codes. |
| `DELETE /api/v1/me/mfa/totp` | Disable TOTP (step-up required). |
| `POST /api/v1/me/mfa/recovery-codes/regenerate` | Replace recovery codes (step-up required). |
| `POST /api/v1/tenants` | Create a new tenant; caller becomes owner. |

### 4.4 Tenant-scoped endpoints (under `/api/v1/t/{tenantId}`)

| Route | Role |
|---|---|
| `PATCH /api/v1/t/{id}` — rename, change slug / base currency / cycle anchor / locale / timezone | owner |
| `DELETE /api/v1/t/{id}` — soft-delete (30d grace) | owner |
| `POST  /api/v1/t/{id}/restore` | owner |
| `GET   /api/v1/t/{id}/members` — list memberships + pending invites | any member |
| `POST  /api/v1/t/{id}/invites` — create invite, enqueue email | member (role=member only); owner (any role) |
| `DELETE /api/v1/t/{id}/invites/{inviteId}` — revoke | owner or original inviter |
| `PATCH /api/v1/t/{id}/members/{userId}` — change role | owner; last-owner guard |
| `DELETE /api/v1/t/{id}/members/{userId}` — remove-other or leave-self | owner (remove); self (leave); last-owner guard |

Everything else in `/api/v1/t/{id}/…` (accounts, transactions, categories, etc.) is the existing financial surface, re-mounted under this tenant-scoped prefix instead of the current flat `/api/v1/accounts` shape.

### 4.5 Middleware chain

On every tenant-scoped route:

1. **`RequireSession`** — reads the session cookie, looks up `sessions.id = sha256(token)`, checks expiries (sliding + absolute), bumps `last_seen_at`, loads the user, attaches to context.
2. **`RequireMembership`** — extracts `{tenantId}` from the URL, looks up `(tenant_id, user_id)` in `tenant_memberships`, attaches the role. **Returns 404, not 403, on a miss** to avoid leaking tenant existence.
3. **`RequireRole("owner")`** — on owner-gated routes.
4. **`RequireFreshReauth(5 * time.Minute)`** — on sensitive routes: `now() - sessions.reauth_at < 5min`; otherwise 403 with `code: reauth_required` so the client opens the re-auth modal.

Public auth routes skip `RequireSession`; authenticated-non-tenant routes use `RequireSession` only.

### 4.6 Tenant scope enforcement (service layer)

Every service method that reads or writes tenant-scoped data takes `tenantID` as its first argument (after `context.Context`). Handlers read it from the context populated by `RequireMembership`, never from the request body. Soft-deleted tenants are filtered out of all reads except explicit restore/list-deleted paths.

## 5. Credentials

### 5.1 Password (Argon2id)

- Library: `golang.org/x/crypto/argon2`, `IDKey` mode.
- Baseline params: `m = 64 MiB`, `t = 3`, `p = 2`, salt length 16 bytes, key length 32 bytes. Tuned to ~250ms/verify on a small VPS; re-benchmark and bump on deploy when hardware improves.
- Stored as encoded PHC string in `users.password_hash`.
- **Policy:** min 12 chars, no upper bound. Server-side bloom filter over the top 10,000 common passwords (compile-time-embedded, ~16 KiB for 1% FPR). Substring check against email + display name.
- **Client-side strength meter:** zxcvbn (JS, no server round-trip) for live feedback. Server decision is authoritative.

### 5.2 Passkeys (WebAuthn)

- Library: `github.com/go-webauthn/webauthn`.
- Relying Party ID: `folio.app` in prod; `localhost` in dev; config-driven (`WEBAUTHN_RP_ID` env — already in `docker-compose.dev.yml`).
- Discoverable / resident credentials for passkey-first login (no email required).
- Browser conditional-mediation (`mediation: 'conditional'`) on the email field so passkey autofill surfaces without a separate button.
- Multiple passkeys per user. Each has a user-editable label (`webauthn_credentials.label`). Remove is step-up-gated.
- Passkey-first login flow is visible as the primary login button ("Continue with passkey"). A dev account without passkeys sees only the password form — passkey is never required.

### 5.3 TOTP

- Library: `github.com/pquerna/otp/totp`.
- Secret is generated server-side, AES-GCM-encrypted with `SECRET_ENCRYPTION_KEY`, stored in `totp_credentials.secret_cipher`.
- Verification window: ±1 period (30s clock skew either way).
- One TOTP credential per user. Enable / disable is step-up-gated.

### 5.4 Recovery codes

- 10 single-use codes, 10-char base32 each.
- Generated at first MFA enable (passkey or TOTP).
- Stored per-code as Argon2id hashes in `auth_recovery_codes` (one row per code, with `consumed_at`).
- Shown once. User can regenerate (step-up-gated); regenerate invalidates all previous codes.
- Usable at login when `mfa_required=true` (via `/auth/login/mfa/recovery`).

### 5.5 MFA at login

1. `/auth/login` verifies email + password.
2. If user has any MFA credential (passkey or TOTP), returns `{mfa_required: true, challenge_id}`. The `challenge_id` is an opaque 5-minute ephemeral token keyed to user + IP + UA; not a session.
3. Client presents available factors (passkey if enrolled → TOTP if enrolled → "use a recovery code").
4. The verify endpoint consumes `challenge_id`, issues the session cookie, and writes an audit event.

### 5.6 Step-up re-authentication

Freshness window: 5 minutes on `sessions.reauth_at`. Completing any of these bumps `reauth_at`:
- Successful password re-entry.
- Successful MFA ceremony (passkey or TOTP).

Routes that require fresh re-auth:
- Change password / email.
- Add/remove passkey, enable/disable TOTP, regenerate recovery codes.
- Revoke-all-sessions.
- Change a member's role, remove a member, transfer ownership, demote self.
- Soft-delete a tenant (and restore).
- Trigger full data export or account delete.

A stale session hitting one of these routes gets `403 { code: "reauth_required" }`. The client renders the re-auth modal (password prompt + MFA if enrolled) and retries on success.

## 6. Sessions

### 6.1 Cookie

- Name: `folio_session`.
- Flags: `HttpOnly; Secure; SameSite=Lax; Path=/`.
- No `Domain` attribute — host-only, never leaks to subdomains.
- Value: 256 bits from `crypto/rand`, base64url-encoded (~43 chars).
- Server stores `sha256(token)` as `sessions.id`; plaintext lives only in the cookie.

### 6.2 Lifetime

- **Sliding idle:** each authenticated request bumps `last_seen_at`; if `now() - last_seen_at > 14d`, session is expired.
- **Absolute:** `created_at + 90d`. Hard cutoff regardless of activity.
- Expiry check on every request; expired sessions are deleted lazily or via a periodic sweeper.

### 6.3 Automatic revocation

- Password changed → revoke all sessions of that user **except the current one**.
- Email changed → same.
- Passkey / TOTP credential removed → same.
- Account deleted → cascades via FK.
- User removed from a tenant → **no** session revocation; their other tenant memberships are untouched.

### 6.4 Device list UX

`GET /api/v1/me/sessions` returns `[{id, createdAt, lastSeenAt, userAgent, ip, isCurrent}]`. User can revoke individual sessions or "sign out everywhere" (`keepCurrent=true` by default, so the user isn't accidentally logged out by their own click).

## 7. Email flows

All transactional emails dispatch via **Resend**, sent by a River worker. The service-layer API is synchronous (enqueue a job + return); the send itself is retried by River on failure.

| Flow | Token lifetime | Content |
|---|---|---|
| Email verification (signup) | 24 h | Link: `folio.app/auth/verify/{token}`. |
| Email verification resend | 24 h (new token) | Same. |
| Password reset | 30 min | Link: `folio.app/auth/reset/{token}`. |
| Email change (new address) | 24 h | Link: `folio.app/auth/email/confirm/{token}` (sent to **new** address). |
| Email change notice (old address) | — | Informational; "your account email was changed to {new}". |
| Tenant invite | 7 d | Link: `folio.app/accept-invite/{token}`. |

Verify / reset / email-change tokens live in `auth_tokens`; invite tokens in `tenant_invites`. All tokens stored as SHA-256 hashes; plaintext appears only in the emailed URL.

**Email verification gating:** unverified users can access the dashboard and read their tenant, but the following actions require `email_verified_at IS NOT NULL`:
- Accepting a tenant invite.
- Linking a bank account / provider token.
- Creating an invite.
- Starting a data export.

A persistent banner nags until verification completes.

## 8. Security hardening

### 8.1 CSRF

Three layers, no double-submit token:

1. `SameSite=Lax` on the session cookie.
2. On every state-changing method (POST/PUT/PATCH/DELETE), validate `Origin` (fallback `Referer`) against the configured `APP_URL` allowlist. Reject on mismatch with 403.
3. Require a custom request header `X-Folio-Request: 1` on all state-changing API routes. The header triggers a CORS preflight for cross-origin callers, blocking simple-request forgeries.

GET is never state-changing (standard REST) and needs none of these.

### 8.2 Rate limiting

Per-IP and per-email where relevant. Implemented as a token bucket; storage starts as in-memory (single-node, fine for v1) with a swap-in Postgres-backed bucket behind the same interface for future horizontal scaling.

| Endpoint | Limit |
|---|---|
| `POST /auth/signup` | 5 / hr / IP |
| `POST /auth/login` | 10 / 10min / IP; 5 / 10min / email |
| `POST /auth/login/mfa/*` | 10 / 5min / challenge_id (invalidate on exhaust) |
| `POST /auth/password/reset-request` | 3 / hr / IP; 3 / hr / email |
| `POST /auth/verify/resend` | 1 / min / user; 5 / hr / user |
| `POST /auth/invites/*/accept` | 10 / hr / IP |
| All state-changing tenant routes | 60 / min / user |

Failed-login back-off (per email, over 10 min): response latency `50ms → 200 → 800 → 3200 → 10000` across 5 consecutive failures. Clears on any success. No lockout (DoS vector).

### 8.3 Audit events

Written to `audit_events` (existing schema) with `actor_user_id`, `tenant_id` (nullable for user-scoped events), `action`, `entity_type`, `entity_id`, `before`, `after`, `at`.

| Domain | Actions |
|---|---|
| auth | `user.signup`, `user.login_succeeded`, `user.login_failed`, `user.logout`, `user.password_changed`, `user.password_reset_completed`, `user.email_change_requested`, `user.email_change_confirmed`, `user.email_verified`, `user.session_revoked`, `user.sessions_revoked_all` |
| MFA | `mfa.passkey_added`, `mfa.passkey_removed`, `mfa.totp_enabled`, `mfa.totp_disabled`, `mfa.recovery_codes_regenerated`, `mfa.reauth_completed` |
| tenant | `tenant.created`, `tenant.renamed`, `tenant.settings_changed`, `tenant.deleted`, `tenant.restored` |
| membership | `member.invited`, `member.invite_revoked`, `member.invite_accepted`, `member.role_changed`, `member.removed`, `member.left` |
| admin | `admin.granted`, `admin.revoked`, `admin.bootstrap_granted`, `admin.viewed_user`, `admin.viewed_tenant`, `admin.viewed_audit`, `admin.retried_job`, `admin.resent_email` |

Failed-login events are keyed by email (not user) to avoid creating a row for every typo. User-scoped events (password change, session revoke, etc.) are written with `tenant_id = NULL`. Admin events are always `tenant_id = NULL` (they're cross-tenant by definition).

## 9. Registration modes

Same codebase, different config via `REGISTRATION_MODE` env var:

- **`open`** (default for SaaS) — the marketing `/signup` page works for anyone.
- **`invite_only`** — `/signup` is 403 unless called with a valid `inviteToken`. Useful for private SaaS beta.
- **`first_run_only`** — `/signup` works exactly once (checks `select exists(select 1 from users)`). Subsequent signups 403. Self-hosted single-household default.

The signup endpoint is one code path; the env toggle gates it at the handler layer.

## 10. Soft delete and the tenant-sweeper

- `DELETE /api/v1/t/{id}` sets `tenants.deleted_at = now()`. The row survives; all dependent data is untouched.
- A periodic River job (daily) hard-deletes tenants with `deleted_at < now() - interval '30 days'`. Hard delete cascades via existing FKs.
- `POST /api/v1/t/{id}/restore` clears `deleted_at`. Owner-only. Step-up required.
- While soft-deleted, a tenant is invisible to every API path except explicit restore / list-deleted.

## 11. Admin console

Read-only debugging surface + a small set of operational writes for the person who runs the Folio instance (self-host owner or SaaS operator). Orthogonal to tenant roles — admin status is a user-level boolean (`users.is_admin`), not a tenant role. An admin has no default read/write access to any tenant's financial data; accessing tenant data would require impersonation, which is deferred (§2 non-goals).

### 11.1 Granting admin

- **CLI (`backend/cmd/folio-admin`):** `folio-admin grant <email>`, `folio-admin revoke <email>`, `folio-admin list`. Talks to Postgres directly using `DATABASE_URL`. Shipped alongside the server binary.
- **First-run bootstrap:** if the env var `ADMIN_BOOTSTRAP_EMAIL` is set, the signup whose email matches is auto-granted `is_admin=true` exactly once. Writes `admin.bootstrap_granted`. The env var is ignored after a matching grant exists (idempotent).
- **Last-admin guard:** both the CLI and the `/admin/users/{id}/revoke-admin` endpoint refuse if the target would become the last admin (parallel to the last-owner tenant invariant).

### 11.2 HTTP surface

Mounted at `/api/v1/admin/…` (API) with Next.js pages at `/admin/…` (web). Middleware chain:

1. `RequireSession`
2. `RequireAdmin` — **404 on miss** (not 403) so admin-ness isn't enumerable.
3. `RequireFreshReauth(5min)` on every write.

| Route | Purpose |
|---|---|
| `GET /api/v1/admin/tenants` | Paginated list + search by name/slug/id. |
| `GET /api/v1/admin/tenants/{id}` | Detail: member count, settings, `deleted_at`, last activity. Metadata only — no financial rows. |
| `GET /api/v1/admin/users` | Paginated list + search by email. |
| `GET /api/v1/admin/users/{id}` | Detail: memberships, active sessions, MFA state, last login. |
| `GET /api/v1/admin/audit` | Cross-tenant feed of `audit_events` with filters (actor, tenant, action, time range). |
| `GET /api/v1/admin/jobs` | River queue view (running / scheduled / retryable / dead-letter). |
| `POST /api/v1/admin/jobs/{id}/retry` | Re-enqueue a failed job (step-up). |
| `POST /api/v1/admin/emails/{id}/resend` | Re-send a transactional email that bounced (step-up). |
| `POST /api/v1/admin/users/{id}/grant-admin` | Promote (step-up). |
| `POST /api/v1/admin/users/{id}/revoke-admin` | Demote (step-up; last-admin guard). |

### 11.3 Audit on every admin action

Reads audit too. Every admin route writes an audit event with `tenant_id = NULL` — fintech staff access to customer data (even metadata) should always be answerable: "who looked at this, and when?". Actions: see the `admin` row in §8.3.

## 12. Rollout / migration strategy

The identity migration has not been shipped to any user. Rather than pile a v2 migration on top, we **rewrite `20260424000001_identity.sql` in place** to reflect the new shape:

- Drop `users.tenant_id`.
- Add `email_verified_at`, `last_tenant_id`, `is_admin` to `users`.
- Add `slug`, `deleted_at` to `tenants`.
- Add `last_seen_at`, `reauth_at` to `sessions`.
- Drop `totp_credentials.recovery_codes_cipher`.
- Add `tenant_memberships`, `tenant_invites`, `auth_tokens`, `auth_recovery_codes`.
- Add the `tenant_role` enum.

All downstream migrations need a review pass to update anything that currently FK-ed `users(tenant_id, id)` — there shouldn't be any in the tree today, but a grep is required as part of implementation.

The existing `backend/internal/identity.Bootstrap` code and `backend/internal/httpx.RequireTenant` middleware are deleted entirely and replaced by the new `backend/internal/auth` package.

## 13. Web surface (high-level)

Page inventory on the Next.js side, each with its own route and component tree:

- Public: `/`, `/login`, `/signup`, `/forgot`, `/reset/{token}`, `/accept-invite/{token}`, `/auth/verify/{token}`.
- Authed, non-tenant: `/settings/account`, `/settings/security`, `/settings/sessions`, `/tenants` (tenant picker / create new).
- Tenant-scoped: `/t/{slug}/…` covers the existing dashboard, accounts, transactions, categories, etc., plus `/t/{slug}/settings/members`, `/t/{slug}/settings/invites`, `/t/{slug}/settings/tenant`.
- Admin (only visible to `is_admin=true` users): `/admin/tenants`, `/admin/users`, `/admin/audit`, `/admin/jobs`. Linked from the user menu with a distinct "Admin" badge so admins don't forget what hat they're wearing.
- Tenant switcher lives in the top bar / sidebar; backed by `/me.tenants`.

The current `web/lib/hooks/use-identity.ts` localStorage dev-bridge is removed. The identity hook becomes a React Query wrapper around `GET /api/v1/me`.

## 14. Open / deferred

Things we deliberately chose not to design now. Each is a future spec of its own.

- **Billing & plans** — tenants will grow `billing_*` columns and a separate `plans` table.
- **SSO / social login** — adds an `auth_identities(provider, provider_user_id, user_id)` table; login ceremony branches.
- **Admin impersonation** — "view this tenant as a member" for support. Feature-flagged, reason-required, fully audited, read-only. Complements the admin console in §11.
- **Cross-tenant aggregate views** — "show me net worth across all my tenants".
- **Account / data deletion UX** — requires a full-data-export trigger first (§21 in the bible).
- **Advanced MFA**: hardware-key-only enforcement, YubiKey OTP, WebAuthn attestation policies.
- **Shared-device / kiosk sessions** — short-lived session profiles.
- **Org-level audit exports** — structured download of `audit_events`.
