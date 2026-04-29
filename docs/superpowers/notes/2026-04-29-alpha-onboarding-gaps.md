# Alpha Onboarding — Documented Gaps

**Date:** 2026-04-29
**Branch shipped:** `feat/alpha-onboarding`
**Plan:** `docs/superpowers/plans/2026-04-29-alpha-onboarding-plan.md`

After the alpha-onboarding branch lands, the platform supports the full invite-driven flow: bootstrapped admin invites users (platform invites), users sign up via the link, land in their fresh workspace, create more workspaces, invite others into a workspace they own, and self-manage password / TOTP / passkeys. Login remembers the last workspace.

This document lists what is **knowingly deferred** for later plans.

---

## Identity & security

- **Reauth UI** — backend `/auth/reauth` exists, but the UI shows "Sign out and back in" as the workaround when fresh-reauth is required (TOTP disable, passkey delete, change password, admin invite create/revoke). A proper in-app password prompt that re-elevates the session is the highest-impact follow-up here.
- **Sessions / devices list with revoke** — no UI. Backend has session records, just no surface to view/manage them.
- **Passkey rename** — only add/delete, not relabel.
- **TOTP rotation** — to change the authenticator key, you must disable + re-enrol.
- **Email change UI in settings/account** — backend supports `/auth/email/change`; the account page only shows the current email. Wire up the change form (the `requestEmailChange` helper already exists in `web/lib/auth/email-flows.ts`).
- **`platform_invite` email template** — `LogMailer` silently no-ops when the template doesn't exist. Add `backend/internal/mailer/templates/platform_invite.{txt,html}.tmpl` so admin platform invites actually email when SMTP/Resend is configured.

## Workspace lifecycle

- **Soft-deleted workspace owner-restore UI on `/workspaces`** — backend supports restore; no UI surface yet.
- **Workspace settings polish (slug edit visibility, slug-collision messaging)** — exists but UX could be clearer.
- **Workspace switcher search/filter** — fine for users with 2-5 workspaces, will need a search box at higher counts.

## Onboarding

- **First-run wizard** — completely deferred. Suggested steps from the plan: Account identity → Workspace basics → First account → Import or manual entry → Categories review → Income source → Recurring expenses → Goal → Invite members → AI features.
- **"Continue setup" CTA on workspace dashboard** — deferred until the wizard exists.
- **Sample data mode** — deferred.

## Data ownership

- **Transaction CSV export** (filtered + whole workspace).
- **Full workspace bundle export** (JSON + CSV ZIP).
- **All-workspaces export bundle**.
- **Bundle import** — paired with full export.
- **Encrypted export** — needs a chosen encryption design first.
- **User account delete** — backend support is partial; full delete flow with sole-owned-workspace handling is deferred.

## Audit / admin

- **Workspace-scoped audit log UI** for owners/members — backend audit storage exists, no surface.
- **Admin instance usage dashboard** — totals (users/workspaces/accounts/transactions), storage, queue health.
- **Backup status / runbook page** — deferred; ship runbook docs first.

## API contract

- **OpenAPI sweep** — these new routes are NOT yet in `openapi/openapi.yaml`:
  - `POST   /api/v1/admin/invites`
  - `GET    /api/v1/admin/invites`
  - `DELETE /api/v1/admin/invites/{id}`
  - `GET    /api/v1/auth/platform-invites/{token}`
  - `PATCH  /api/v1/me/last-workspace`
  - `PATCH  /api/v1/me`
  - `POST   /api/v1/me/password`
  - `GET    /api/v1/me/mfa/passkeys`
  - `DELETE /api/v1/me/mfa/passkeys/{id}`
  - `POST   /api/v1/t/{workspaceId}/invites/{inviteId}/resend`
  - The workspace invite create response shape changed from raw invite to `{invite, acceptUrl}` — also needs spec update.

  Run `make openapi` after updating `openapi/openapi.yaml` to regenerate Go server stubs and TS client types.

## PWA

- **Offline transaction draft + sync queue** — deferred per plan ("PWA stays as currently configured").
- **Offline-aware workspace shell** beyond what's already cached — deferred.

## UX polish (cosmetic / inconsistencies surfaced during this work)

- **Design-token migration**: `web/app/signup/page.tsx`, `web/app/workspaces/new/page.tsx`, and `web/app/login/page.tsx` use raw HTML inputs/buttons + shadcn-default tokens (`bg-foreground`, `text-background`, `text-muted-foreground`) instead of the project's `Field`, `Input`, `Select`, `Button` primitives + Folio tokens (`bg-surface`, `text-fg-muted`, etc.). The settings pages and workspace pages have migrated; the auth/onboarding pages have not. A small sweep would make the visual language uniform.
- **No toast primitive** — revoke errors on invite tables silently fail (TanStack Query swallows; UI doesn't surface). Adding a toast component would let row-level mutations surface success/error consistently.
- **Two-press confirmation pattern** doesn't reset on click-outside (TOTP disable, passkey delete). Acceptable for v1.
- **Workspace invite email subject** says "You're invited to Folio" on resend vs "You're invited on Folio" on create — minor copy alignment.

## Test isolation

- The full backend `go test ./...` exhibits FK-violation failures in `internal/identity` when packages run in parallel against the shared dev DB. These pre-date this branch (verified on `main`) and are caused by the shared-DB harness not being package-scoped. Workaround: run packages sequentially (`-p 1`) for clean results. A proper fix is to scope the testdb harness per-package or add explicit cleanup ordering.

---

## What "alpha-ready" means after this branch

A new user can:

1. Receive an invite link from the bootstrapped admin (or from a workspace owner).
2. Sign up via the link, land in their workspace.
3. Create additional workspaces.
4. Invite others into a workspace they own (with copy-link or email).
5. Manage their own security: change password, enable/disable TOTP, add/remove passkeys.
6. Edit their display name.
7. Reload / sign back in and land on the last workspace they were using.

Everything else in the original feature plan (`docs/superpowers/plans/2026-04-29-identity-workspaces-feature-plan.md`) remains future work, scoped above.
