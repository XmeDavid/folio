# Tenant → Workspace Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename "tenant" → "workspace" across DB, backend Go, OpenAPI, web, and docs. Pre-launch (0 users); no compatibility shims.

**Architecture:** Mechanical sweep in dependency order — DB schema → SQL queries → sqlc regen → OpenAPI → oapi-codegen regen → handwritten Go → web codegen → web code → docs. Each phase ends with a build/test gate before moving on.

**Tech Stack:** Postgres, atlas migrations, sqlc, oapi-codegen, Go, Next.js (Turbopack), TypeScript, openapi-typescript, pnpm, vitest.

**Spec:** `docs/superpowers/specs/2026-04-26-tenant-to-workspace-rename-design.md`

---

## Conventions

- All `sed`/`perl` commands below are macOS-compatible (`sed -i ''` form). Run them from the repo root unless stated.
- After each task, run the verification commands shown. Don't proceed until they pass.
- Commit after every task. Frequent commits make it easy to bisect if something later goes wrong.
- The string `tent` appears inside legitimate words (`consistent`, `intent`, `potentially`). Always use word-boundary regex (`\btenant\b` etc.), never bare substring replace.
- Generated artifacts to ignore in sweeps: `.git/`, `.pnpm-store/`, `.omc/`, `web/.next/`, `web/node_modules/`, `backend/internal/db/dbq/` (regen'd), `web/lib/api/schema.d.ts` (regen'd).

---

## Task 1: Rename in DB migrations

**Files:**
- Modify: `backend/db/migrations/2026042400000*.sql` (all 17 files)

- [ ] **Step 1: Inspect all `tenant` occurrences in migrations**

```bash
rg -n '\btenant' backend/db/migrations/ | head -40
```

Expected: `tenants` table definition, `tenant_id` columns, indexes named `*_tenant_id_*`, FKs, RLS predicates.

- [ ] **Step 2: Apply the rename**

```bash
# Identifiers (table, column, index, constraint names) — case-preserving
perl -i -pe 's/\btenants\b/workspaces/g; s/\btenant_id\b/workspace_id/g; s/\btenant\b/workspace/g; s/\bTenant\b/Workspace/g' backend/db/migrations/2026042400000*.sql
```

- [ ] **Step 3: Verify zero hits in migrations**

```bash
rg -i 'tenant' backend/db/migrations/
```

Expected: no output.

- [ ] **Step 4: Recompute atlas.sum**

```bash
cd backend && atlas migrate hash --dir file://db/migrations
```

Expected: `atlas.sum` is updated. If atlas isn't installed, run via `make`: skip and note that the sum file will be regenerated when migrations run.

- [ ] **Step 5: Commit**

```bash
git add backend/db/migrations/
git commit -m "refactor(db): rename tenant → workspace in migrations"
```

---

## Task 2: Rename in SQL queries (sqlc input)

**Files:**
- Modify: `backend/db/queries/**/*.sql`

- [ ] **Step 1: Inspect**

```bash
rg -n '\btenant' backend/db/queries/ | head -40
```

- [ ] **Step 2: Apply rename**

```bash
find backend/db/queries -name '*.sql' -print0 | xargs -0 perl -i -pe 's/\btenants\b/workspaces/g; s/\btenant_id\b/workspace_id/g; s/\btenant\b/workspace/g; s/\bTenant\b/Workspace/g'
```

Note: sqlc query names like `-- name: GetTenantByID` will become `GetWorkspaceByID`, which renames the generated Go function. Anything in handwritten Go that calls `dbq.GetTenantByID` will break — that's expected; Task 7 fixes it.

- [ ] **Step 3: Verify**

```bash
rg -i 'tenant' backend/db/queries/
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add backend/db/queries/
git commit -m "refactor(db): rename tenant → workspace in sqlc queries"
```

---

## Task 3: Regenerate sqlc

- [ ] **Step 1: Run sqlc**

```bash
make sqlc
```

Expected: `backend/internal/db/dbq/*.go` files updated. New types/functions use `Workspace*` naming.

- [ ] **Step 2: Confirm no stale tenant references in generated code**

```bash
rg -i 'tenant' backend/internal/db/dbq/
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/db/dbq/
git commit -m "chore(db): regenerate sqlc with workspace naming"
```

---

## Task 4: Rename in OpenAPI spec

**Files:**
- Modify: `openapi/openapi.yaml`

- [ ] **Step 1: Inspect**

```bash
rg -n '\b[Tt]enant\b|\bTENANT\b|tenant_id|tenantId|TenantId|TenantID' openapi/openapi.yaml | head -50
```

- [ ] **Step 2: Apply rename**

```bash
perl -i -pe '
  s/\bTenantID\b/WorkspaceID/g;
  s/\bTenantId\b/WorkspaceId/g;
  s/\btenantId\b/workspaceId/g;
  s/\btenant_id\b/workspace_id/g;
  s/\bTenants\b/Workspaces/g;
  s/\btenants\b/workspaces/g;
  s/\bTenant\b/Workspace/g;
  s/\btenant\b/workspace/g;
' openapi/openapi.yaml
```

- [ ] **Step 3: Update path operations: `/tenants` → `/workspaces`**

The previous step already covered URL path strings inside the YAML. Verify:

```bash
rg -n '/(tenants|tenant)\b' openapi/openapi.yaml
```

Expected: no output.

- [ ] **Step 4: Verify**

```bash
rg -i 'tenant' openapi/openapi.yaml
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add openapi/openapi.yaml
git commit -m "refactor(openapi): rename tenant → workspace"
```

---

## Task 5: Regenerate Go server stubs and TS client from OpenAPI

- [ ] **Step 1: Run codegen**

```bash
make openapi
```

Expected: oapi-codegen produces fresh Go types under wherever it writes (commonly `backend/internal/api/`), and `web/lib/api/schema.d.ts` is regenerated.

- [ ] **Step 2: Verify generated files**

```bash
# Find oapi-codegen output dir
cat openapi/oapi-codegen.yaml
rg -i 'tenant' web/lib/api/schema.d.ts
```

Expected: no `tenant` in `schema.d.ts`. The Go output dir per `oapi-codegen.yaml` should also have zero `tenant` hits.

- [ ] **Step 3: Commit**

```bash
git add web/lib/api/schema.d.ts backend/  # adjust path to oapi output
git commit -m "chore(api): regenerate openapi clients with workspace naming"
```

---

## Task 6: Rename handwritten Go identifiers

**Files:**
- Modify: every `.go` file under `backend/` except generated dirs (`internal/db/dbq/`, oapi-codegen output).

- [ ] **Step 1: Sweep code rename**

```bash
# Restrict to handwritten Go
find backend -type f -name '*.go' \
  ! -path 'backend/internal/db/dbq/*' \
  -print0 | xargs -0 perl -i -pe '
    s/\bTenantWithRole\b/WorkspaceWithRole/g;
    s/\bTenantDetail\b/WorkspaceDetail/g;
    s/\bTenantListFilter\b/WorkspaceListFilter/g;
    s/\bTenantID\b/WorkspaceID/g;
    s/\bTenantId\b/WorkspaceId/g;
    s/\bTenants\b/Workspaces/g;
    s/\bTenant\b/Workspace/g;
    s/\btenantID\b/workspaceID/g;
    s/\btenantId\b/workspaceId/g;
    s/\btenant_id\b/workspace_id/g;
    s/\btenants\b/workspaces/g;
    s/\btenant\b/workspace/g;
'
```

This catches struct fields, struct tags (`db:"workspace_id"` `json:"workspace_id"`), variable names, package-internal identifiers, comments, and string literals (route paths like `/tenants/...` → `/workspaces/...`).

- [ ] **Step 2: Verify zero hits**

```bash
rg -i 'tenant' backend/ -t go
```

Expected: no output. If any hit remains, inspect — it's likely a substring inside another word and should be left alone (revisit the regex).

- [ ] **Step 3: Don't commit yet — file renames in next task**

---

## Task 7: Rename Go files

**Files:**
- `backend/internal/auth/http_tenants.go` → `http_workspaces.go`
- `backend/internal/admin/tenants.go` → `workspaces.go`
- `backend/internal/identity/tenants_test.go` → `workspaces_test.go`
- `backend/internal/jobs/sweep_soft_deleted_tenants_worker.go` → `sweep_soft_deleted_workspaces_worker.go`

- [ ] **Step 1: Rename via git mv**

```bash
git mv backend/internal/auth/http_tenants.go backend/internal/auth/http_workspaces.go
git mv backend/internal/admin/tenants.go backend/internal/admin/workspaces.go
git mv backend/internal/identity/tenants_test.go backend/internal/identity/workspaces_test.go
git mv backend/internal/jobs/sweep_soft_deleted_tenants_worker.go backend/internal/jobs/sweep_soft_deleted_workspaces_worker.go
```

- [ ] **Step 2: Confirm no Go file or dir still has tenant in its path**

```bash
find backend -name '*tenant*'
```

Expected: no output.

- [ ] **Step 3: Commit (combined: code rename + file moves)**

```bash
git add -A backend/
git commit -m "refactor(backend): rename tenant → workspace"
```

---

## Task 8: Backend build and test gate

- [ ] **Step 1: Reset and re-apply migrations against fresh DB**

```bash
make db-reset
sleep 5  # wait for postgres to come up
make migrate
```

Expected: migrations apply cleanly. If atlas complains about the `atlas.sum` checksum from Task 1, re-run `cd backend && atlas migrate hash --dir file://db/migrations`, commit the change, then retry.

- [ ] **Step 2: Build backend**

```bash
cd backend && go build ./...
```

Expected: zero errors. If any reference is broken, it's almost certainly something the regex missed — fix it, recommit.

- [ ] **Step 3: Run tests**

```bash
cd backend && go test ./...
```

Expected: all green. Test fixtures referencing tenant should already be renamed by Task 6.

- [ ] **Step 4: If atlas.sum changed, commit it**

```bash
git add backend/db/migrations/atlas.sum
git diff --cached --quiet || git commit -m "chore(db): update atlas migration checksum"
```

---

## Task 9: Rename web folders

**Files (folders):**
- `web/app/t` → `web/app/w`
- `web/app/tenants` → `web/app/workspaces`
- `web/app/admin/tenants` → `web/app/admin/workspaces`
- `web/app/admin/tenants/[tenantId]` → `web/app/admin/workspaces/[workspaceId]`
- `web/app/t/[slug]/settings/tenant` → `web/app/w/[slug]/settings/workspace`

- [ ] **Step 1: Rename top-level web app folders**

```bash
git mv web/app/t web/app/w
git mv web/app/tenants web/app/workspaces
git mv web/app/admin/tenants web/app/admin/workspaces
```

- [ ] **Step 2: Rename nested folders**

```bash
git mv 'web/app/admin/workspaces/[tenantId]' 'web/app/admin/workspaces/[workspaceId]'
git mv 'web/app/w/[slug]/settings/tenant' 'web/app/w/[slug]/settings/workspace'
```

- [ ] **Step 3: Verify no tenant in app folder structure**

```bash
find web/app -type d | rg -i tenant
```

Expected: no output.

- [ ] **Step 4: Don't commit yet — file/code renames next**

---

## Task 10: Rename web component files

**Files:**
- `web/components/tenant-switcher.tsx` → `workspace-switcher.tsx`
- `web/components/app/tenant-shell.tsx` → `workspace-shell.tsx`

- [ ] **Step 1: git mv the components**

```bash
git mv web/components/tenant-switcher.tsx web/components/workspace-switcher.tsx
git mv web/components/app/tenant-shell.tsx web/components/app/workspace-shell.tsx
```

- [ ] **Step 2: Confirm no tenant filenames remain in web (excluding `.next/`, `node_modules/`)**

```bash
find web -name '*tenant*' \
  ! -path 'web/.next/*' \
  ! -path 'web/node_modules/*'
```

Expected: no output.

---

## Task 11: Sweep web code

**Files:**
- All `.ts`, `.tsx`, `.css`, `.md` under `web/`, excluding `.next/`, `node_modules/`, and the generated `lib/api/schema.d.ts`.

- [ ] **Step 1: Apply rename**

```bash
find web -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.css' -o -name '*.mdx' -o -name '*.md' \) \
  ! -path 'web/.next/*' \
  ! -path 'web/node_modules/*' \
  ! -path 'web/lib/api/schema.d.ts' \
  -print0 | xargs -0 perl -i -pe '
    s/\bTenantWithRole\b/WorkspaceWithRole/g;
    s/\bTenantContext\b/WorkspaceContext/g;
    s/\bTenantSwitcher\b/WorkspaceSwitcher/g;
    s/\bTenantShell\b/WorkspaceShell/g;
    s/\bTenantId\b/WorkspaceId/g;
    s/\bTenantID\b/WorkspaceID/g;
    s/\bTenants\b/Workspaces/g;
    s/\bTenant\b/Workspace/g;
    s/\buseTenant\b/useWorkspace/g;
    s/\btenantSwitcher\b/workspaceSwitcher/g;
    s/\btenantId\b/workspaceId/g;
    s/\btenant_id\b/workspace_id/g;
    s/\btenants\b/workspaces/g;
    s/\btenant\b/workspace/g;
    s{/admin/tenants}{/admin/workspaces}g;
    s{/tenants/}{/workspaces/}g;
    s{href="/t/}{href="/w/}g;
    s{`/t/}{`/w/}g;
    s{"/t/}{"/w/}g;
    s{/t/\$\{}{/w/\${}g;
  '
```

The trailing path-rewrite rules cover the most common `/t/[slug]` URL string patterns. Verify next.

- [ ] **Step 2: Sweep for any remaining `/t/` route literals**

```bash
rg -nE '"/t/|`/t/|href="/t/' web/ \
  -g '!web/.next/**' \
  -g '!web/node_modules/**'
```

Expected: no output. If anything remains, edit by hand.

- [ ] **Step 3: Sweep for any remaining tenant**

```bash
rg -i 'tenant' web/ \
  -g '!web/.next/**' \
  -g '!web/node_modules/**'
```

Expected: no output. If hits remain (likely in error messages, alt text, comments), fix by hand.

- [ ] **Step 4: Commit (folder rename + file rename + code sweep)**

```bash
git add -A web/
git commit -m "refactor(web): rename tenant → workspace"
```

---

## Task 12: Web build and typecheck gate

- [ ] **Step 1: Typecheck**

```bash
cd web && pnpm typecheck
```

Expected: zero errors.

- [ ] **Step 2: Build**

```bash
cd web && pnpm build
```

Expected: build succeeds. Run in background if it takes >30s.

- [ ] **Step 3: Run tests if any exist**

```bash
cd web && pnpm test
```

Expected: pass, or "no tests found" — both acceptable.

- [ ] **Step 4: If `.next/` was rebuilt, no commit needed (gitignored).**

---

## Task 13: Rewrite living docs

**Files:**
- `README.md`
- `FEATURE-BIBLE.md`
- `docs/domain-model-v2.md`
- `CLAUDE.md` (project)
- `design_language_spec.html` (if it mentions tenant)

- [ ] **Step 1: Sweep**

```bash
for f in README.md FEATURE-BIBLE.md docs/domain-model-v2.md CLAUDE.md design_language_spec.html; do
  [ -f "$f" ] && perl -i -pe '
    s/\bTenants\b/Workspaces/g;
    s/\bTenant\b/Workspace/g;
    s/\btenants\b/workspaces/g;
    s/\btenant_id\b/workspace_id/g;
    s/\btenant\b/workspace/g;
    s/\btenancy\b/workspace/g;
    s/\bmulti-workspace\b/multi-tenant/g;  # we want NO multi-workspace; revisit
  ' "$f"
done
```

After sed, search the resulting files manually for "multi-workspace" or other awkward phrasing, and rewrite the sentence. The `multi-workspace` reverse rule above neutralizes the bad output back to `multi-tenant` so you can spot it and decide per occurrence; usually the qualifier is best dropped.

- [ ] **Step 2: Manually fix awkward phrasings**

```bash
rg -n 'multi-tenant|multi-workspace' README.md FEATURE-BIBLE.md docs/domain-model-v2.md CLAUDE.md design_language_spec.html 2>/dev/null
```

For each hit: rewrite the sentence by hand to drop or rephrase the qualifier so the result reads naturally.

- [ ] **Step 3: Verify**

```bash
rg -i 'tenant' README.md FEATURE-BIBLE.md docs/domain-model-v2.md CLAUDE.md design_language_spec.html 2>/dev/null
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add README.md FEATURE-BIBLE.md docs/domain-model-v2.md CLAUDE.md design_language_spec.html
git commit -m "docs: rename tenant → workspace in living docs"
```

---

## Task 14: Rewrite specs/plans and rename plan filename

**Files:**
- All `.md` under `docs/superpowers/specs/` and `docs/superpowers/plans/` that currently mention tenant
- Filename: `docs/superpowers/plans/2026-04-24-folio-invites-and-tenant-lifecycle.md` → `…-folio-invites-and-workspace-lifecycle.md`

- [ ] **Step 1: Inventory**

```bash
rg -l 'tenant' docs/superpowers/
```

- [ ] **Step 2: Sweep**

```bash
find docs/superpowers -type f -name '*.md' -print0 | xargs -0 perl -i -pe '
  s/\bTenants\b/Workspaces/g;
  s/\bTenant\b/Workspace/g;
  s/\btenants\b/workspaces/g;
  s/\btenant_id\b/workspace_id/g;
  s/\btenant\b/workspace/g;
  s/\btenancy\b/workspace/g;
'
```

- [ ] **Step 3: Rename the one plan with "tenant" in its filename**

```bash
git mv docs/superpowers/plans/2026-04-24-folio-invites-and-tenant-lifecycle.md \
       docs/superpowers/plans/2026-04-24-folio-invites-and-workspace-lifecycle.md
```

- [ ] **Step 4: Sweep for awkward "multi-tenant" phrasing in plans**

```bash
rg -n 'multi-tenant|tenancy' docs/superpowers/
```

For each hit: rewrite the sentence by hand. "Multi-tenancy" in the auth/tenancy spec usually means "the workspace concept" — replace with whatever reads naturally.

- [ ] **Step 5: Verify**

```bash
rg -i 'tenant' docs/superpowers/
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add -A docs/superpowers/
git commit -m "docs: rename tenant → workspace in specs and plans"
```

---

## Task 15: Final verification sweep

- [ ] **Step 1: Repo-wide sweep**

```bash
rg -i 'tenant' \
  --hidden \
  -g '!.git/**' \
  -g '!.pnpm-store/**' \
  -g '!.omc/**' \
  -g '!web/.next/**' \
  -g '!web/node_modules/**' \
  -g '!legacy/**'
```

Expected: zero hits, OR a tiny list of acknowledged exceptions documented inline. If `legacy/` mentions tenant but doesn't import current backend code, leave it (excluded above).

- [ ] **Step 2: Build everything one more time**

```bash
make build
```

Or, if `make build` doesn't exist as expected, run backend and web builds separately:

```bash
cd backend && go build ./... && cd ..
cd web && pnpm build && cd ..
```

Expected: clean build.

- [ ] **Step 3: Run all tests**

```bash
cd backend && go test ./... && cd ..
cd web && pnpm test && cd ..
```

Expected: green.

- [ ] **Step 4: Commit if anything changed (it shouldn't)**

```bash
git status
# if clean, no commit needed
```

---

## Task 16: Manual smoke test

- [ ] **Step 1: Start dev stack**

```bash
make dev
```

Run in background; wait for backend + web to be ready.

- [ ] **Step 2: Walk the golden path**

In a browser at `http://localhost:3000`:
1. Sign up a new user.
2. Create a workspace.
3. Confirm URL is `/w/<slug>/...`, not `/t/<slug>/...`.
4. Open workspace settings (`/w/<slug>/settings/workspace`).
5. Invite a member (settings → invites).
6. Switch workspace via the workspace switcher.
7. Hit the admin console at `/admin/workspaces` and confirm it lists workspaces.

- [ ] **Step 3: Tear down**

```bash
make dev-down
```

- [ ] **Step 4: No commit needed unless smoke test surfaced an issue.**

---

## Done

The codebase now uses "workspace" everywhere except for excluded paths (`legacy/`, generated artifacts in `.next/`/`node_modules/`, and `next/dist/.../multi-tenant.md` which is a Next.js docs file inside `node_modules/` and not our code).
