# Tenant → Workspace Rename

**Date:** 2026-04-26
**Status:** Approved for planning

## Motivation

The codebase uses "tenant" for what is, in user terms, a workspace: a slug-addressed
collaboration scope with members, invites, and settings. "Tenant" is correct backend
jargon for an isolation boundary, but it leaks into UI copy, URLs, and documentation
where it's the wrong word for the concept users see. Pre-launch (0 users) is the
cheapest possible time to do this rename.

## Scope

Full rename across DB, backend, web, OpenAPI, and all docs — including historical
specs and plans. No compatibility shims, no aliases, no rename migrations.

DB migrations are edited in place. Local developers re-run migrations against a
fresh database.

## Mapping

| Old                                                                | New                                       |
| ------------------------------------------------------------------ | ----------------------------------------- |
| `tenant` / `tenants` (DB table, Go vars, route segment)            | `workspace` / `workspaces`                |
| `tenant_id` (column, struct tag, query param, JSON field)          | `workspace_id`                            |
| `Tenant`, `TenantID`, `TenantWithRole`, `TenantDetail`, `TenantListFilter` (Go types) | `Workspace`, `WorkspaceID`, `WorkspaceWithRole`, `WorkspaceDetail`, `WorkspaceListFilter` |
| `TenantContext`, `useTenant`, `tenantSwitcher`, `tenant-shell.tsx` | `WorkspaceContext`, `useWorkspace`, `workspaceSwitcher`, `workspace-shell.tsx` |
| `/t/[slug]`, `/admin/tenants/[tenantId]` (web URLs)                | `/w/[slug]`, `/admin/workspaces/[workspaceId]` |
| `/api/tenants/...` (API routes)                                    | `/api/workspaces/...`                     |
| User-facing copy: "Tenant", "Select tenant"                        | "Workspace", "Select workspace"           |
| Filenames: `tenant-shell.tsx`, `http_tenants.go`, `admin/tenants.go`, `identity/tenants_test.go` | `workspace-shell.tsx`, `http_workspaces.go`, `admin/workspaces.go`, `identity/workspaces_test.go` |
| Doc filename: `2026-04-24-folio-invites-and-tenant-lifecycle.md`   | `2026-04-24-folio-invites-and-workspace-lifecycle.md` |

The DB `slug` column itself is not renamed — only the URL prefix changes.

## Execution Order

1. **DB migrations** — edit `backend/db/migrations/2026042400000*.sql` in place. Rename
   the `tenants` table, every `tenant_id` foreign key column, and any indexes,
   constraints, or triggers that mention tenant.

2. **Backend Go** — rename types, struct fields, `db:"…"` tags, SQL queries,
   handler functions, package identifiers, and registered routes. Rename files
   where the filename mentions tenant. Run `go test ./...` after.

3. **OpenAPI spec** — `openapi/openapi.yaml`: rename schemas, paths, parameters,
   operation IDs. Regenerate the web client types into `web/lib/api/schema.d.ts`
   using whatever generator the project already uses.

4. **Web** — rename folders (`web/app/t` → `web/app/w`,
   `web/app/admin/tenants` → `web/app/admin/workspaces`,
   `web/app/w/[slug]/settings/tenant` → `web/app/w/[slug]/settings/workspace`),
   components, hooks, types, and all UI copy. Run `pnpm build` after.

5. **Docs** — rewrite `tenant` references in `README.md`, `FEATURE-BIBLE.md`,
   `docs/domain-model-v2.md`, `docs/superpowers/specs/*.md`, and
   `docs/superpowers/plans/*.md`. Rename the one historical plan filename. Treat
   `tenancy` as a synonym for `workspace` here (the codebase isn't doing
   multi-tenant infrastructure in any meaningful sense — every "tenancy" reference
   is conceptually about workspaces).

## Verification

After all five steps:

- `rg -i 'tenant' --hidden -g '!.git' -g '!.pnpm-store' -g '!.omc'` returns zero
  hits. Any remaining hit must be an explicit, justified exception called out in
  the implementation plan.
- `go test ./...` passes in `backend/`.
- `pnpm build` succeeds in `web/`. `pnpm test` passes if tests exist.
- Manual smoke walk in dev: signup → workspace creation → invite a member →
  switch workspace → workspace settings page.

## Risks / Watch-outs

- **Struct tags**: `db:"tenant_id"` and JSON tags `json:"tenant_id"` are easy to
  miss in a generic search. Sweep `rg 'tenant_id'` separately across `.go`.
- **Casing variants**: `tenant`, `Tenant`, `TENANT`, `tenantId`, `TenantID`,
  `tenant_id` each need a sweep. Use case-preserving renames where possible.
- **Word boundaries**: words like "consistent", "potentially", "intent" embed the
  substring `tent`. Use `\btenant\b` patterns, not bare substring matches.
- **"multi-tenant"** in prose: rephrase or drop — the new wording is "workspace"
  and "multi-workspace" is awkward. Usually the sentence reads better with the
  qualifier removed.
- **Generated files**: `web/lib/api/schema.d.ts` is generated from the OpenAPI
  spec. Don't hand-edit it — regenerate after the OpenAPI rename.

## Out of Scope

- Renaming the `slug` DB column or any other concept that isn't "tenant".
- Reworking authorization, membership, or invite flows beyond the rename.
- Touching the `legacy/` directory unless it imports current backend code.
