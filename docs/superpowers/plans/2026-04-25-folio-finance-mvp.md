# Folio Finance MVP Implementation Plan

> **For agentic workers:** This plan is intended for multi-agent execution. Pick one task with a clear ownership boundary, update the checkbox when complete, and do not rewrite unrelated files. Frontend workers should follow the `folio-frontend-design` skill.

**Goal:** Turn Folio's current auth, workspace, accounts, transactions, and classification foundation into a usable finance MVP: a calm workspace shell, maintainable manual ledger, classification workflows, basic insights, CSV import, and planning v0.

**Why now:** The database and backend are ahead of the product surface. The next direction is not more schema breadth; it is making the existing ledger/classification core pleasant enough to use for one real month.

**Current baseline, as of 2026-04-25:**
- Backend implemented: auth/session/MFA/passkeys, workspace, invites, admin console, accounts, manual transactions, categories, merchants, tags, categorization rules, transaction tags.
- Web implemented: login/signup/email flows, workspace switcher/settings, admin console, accounts list/create, transactions list/create.
- Schema-only or mostly stubbed: imports/providers, planning, goals, investments, assets, travel/splits, wishlist, attachments/OCR, FX/reports, notifications.
- Known mismatch: `openapi/openapi.yaml` is stale relative to real workspace-scoped routes under `/api/v1/t/{workspaceId}/...`.

**Success metric:** A user can create a workspace, add accounts, enter or import transactions, classify them, see basic spend/net-worth insights, and plan the next cycle without touching SQL or raw JSON.

---

## 0. Shared Rules

### 0.1 Working directories

Backend:

```bash
cd /Users/xmedavid/dev/folio/backend
```

Frontend:

```bash
cd /Users/xmedavid/dev/folio/web
```

Repo root:

```bash
cd /Users/xmedavid/dev/folio
```

### 0.2 Verification baseline

Run these after any backend change:

```bash
cd /Users/xmedavid/dev/folio/backend
go test ./...
```

Run these after any frontend change:

```bash
cd /Users/xmedavid/dev/folio/web
pnpm typecheck
pnpm lint
```

For visual frontend changes, also run the app and inspect desktop and mobile:

```bash
cd /Users/xmedavid/dev/folio
make dev
```

### 0.3 Ownership boundaries

- Backend API and service changes live under `backend/internal/<domain>/`, `backend/internal/http/router.go`, migrations, and tests.
- Frontend API helpers live in `web/lib/api/client.ts` and generated types in `web/lib/api/schema.d.ts`.
- Workspace workspace UI lives under `web/app/t/[slug]/`, `web/components/app/`, and feature folders such as `web/components/transactions/`.
- Reusable UI primitives live under `web/components/ui/`.
- Do not mix broad UI polish with backend domain work in the same task unless the task explicitly says end-to-end.

### 0.4 Design direction

Folio should feel like a restrained finance application:

- Dense but breathable layouts.
- Numbers first, with `tabular-nums`.
- Side navigation, list/detail views, tables, compact cards, and clear forms.
- Use color for state/action only.
- Avoid marketing layouts, gradients, decorative panels, and oversized hero treatment inside the app.

---

## Phase 1: Workspace App Shell and Dashboard

**Goal:** Make the workspace workspace feel like a real application instead of a collection of isolated pages.

**Primary ownership:** Frontend.

**Dependencies:** Existing identity hook, workspace routes, account and transaction API helpers.

### Task 1.1: Add workspace navigation shell

**Files likely touched:**
- `web/components/app/workspace-shell.tsx`
- `web/app/t/[slug]/layout.tsx`
- `web/components/app/*`

**Steps:**
- [x] Add a persistent sidebar for desktop with links to Dashboard, Accounts, Transactions, Categories, Merchants, Tags, Rules, Settings.
- [x] Add a compact mobile navigation pattern.
- [x] Show active route state.
- [x] Keep the workspace switcher in the header.
- [x] Add quick actions for "Add account" and "Record transaction" where they naturally fit.
- [x] Ensure shell works when user belongs to multiple workspaces.

**Acceptance:**
- [x] Every existing workspace page is reachable from navigation.
- [ ] No text overlap at mobile width.
- [x] Current route is visibly active.
- [x] `pnpm typecheck` and `pnpm lint` pass.

### Task 1.2: Replace dashboard JSON with real summary

**Files likely touched:**
- `web/app/t/[slug]/page.tsx`
- `web/lib/api/client.ts`
- `web/lib/format.ts`

**Steps:**
- [x] Remove raw `JSON.stringify` dashboard cards.
- [x] Show account count and total balance grouped by currency.
- [x] Show recent transactions.
- [x] Show uncategorized transaction count.
- [x] Show onboarding-style empty states when there are no accounts or no transactions.
- [x] Add clear links to Accounts, Transactions, and Uncategorized workflow.

**Acceptance:**
- [x] Dashboard is useful with zero data, partial data, and populated data.
- [x] Amounts use `tabular-nums` and locale-aware formatting.
- [x] Dashboard does not require new backend endpoints unless clearly justified.
- [x] `pnpm typecheck` and `pnpm lint` pass.

### Task 1.3: Align workspace page primitives

**Files likely touched:**
- `web/components/app/page-header.tsx`
- `web/components/app/empty.tsx`
- `web/components/ui/*`

**Steps:**
- [ ] Standardize page header spacing and actions.
- [ ] Standardize loading, error, and empty states.
- [ ] Add missing UI primitives only when needed by Phase 1 and Phase 2.
- [ ] Keep primitives owned and small.

**Acceptance:**
- [ ] Accounts, Transactions, and Dashboard share visual structure.
- [ ] No nested cards or decorative containers.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

---

## Phase 2: Classification UI

**Goal:** Expose the backend classification system so transactions become meaningful data.

**Primary ownership:** Frontend, with small backend adjustments only if missing endpoint behavior is discovered.

**Dependencies:** Existing backend services for categories, merchants, tags, categorization rules, transaction tags, and apply-rules.

### Task 2.1: Add typed classification API helpers

**Files likely touched:**
- `web/lib/api/client.ts`
- `web/lib/api/schema.d.ts`
- `openapi/openapi.yaml`

**Steps:**
- [ ] Audit real backend classification routes under `/api/v1/t/{workspaceId}`.
- [ ] Update OpenAPI if stale or missing workspace-scoped contracts.
- [ ] Regenerate TS types with `make openapi` if OpenAPI is changed.
- [ ] Add client helpers for categories, merchants, tags, rules, transaction tags, and apply-rules.

**Acceptance:**
- [ ] Frontend helpers match real backend routes.
- [ ] No ad hoc untyped fetch calls are introduced.
- [ ] `go test ./...`, `pnpm typecheck`, and `pnpm lint` pass.

### Task 2.2: Categories management page

**Files likely touched:**
- `web/app/t/[slug]/categories/page.tsx`
- `web/components/classification/*`

**Steps:**
- [ ] List categories with hierarchy, color, archived state, and parent relationship.
- [ ] Create category.
- [ ] Edit name, color, parent, and archived state.
- [ ] Prevent obvious parent/child confusion in the UI.
- [ ] Provide empty state with first-category action.

**Acceptance:**
- [ ] User can create and edit categories without leaving the page.
- [ ] Hierarchy is readable.
- [ ] Archived categories are visually distinct.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 2.3: Merchants management page

**Files likely touched:**
- `web/app/t/[slug]/merchants/page.tsx`
- `web/components/classification/*`

**Steps:**
- [ ] List merchants with canonical name, default category, logo URL if present, website if present, archived state.
- [ ] Create merchant.
- [ ] Edit merchant fields.
- [ ] Archive/unarchive merchant.
- [ ] Link default category selector to existing categories.

**Acceptance:**
- [ ] User can maintain merchant cleanup metadata.
- [ ] Default category selection works.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 2.4: Tags management page

**Files likely touched:**
- `web/app/t/[slug]/tags/page.tsx`
- `web/components/classification/*`

**Steps:**
- [ ] List tags with color and archived state.
- [ ] Create tag.
- [ ] Edit name, color, archived state.
- [ ] Use tags as flat labels, not hierarchical categories.

**Acceptance:**
- [ ] User can create and edit tags.
- [ ] Archived tags are visually distinct.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 2.5: Categorization rules page

**Files likely touched:**
- `web/app/t/[slug]/rules/page.tsx`
- `web/components/classification/*`

**Steps:**
- [ ] List rules ordered by priority.
- [ ] Create rules for merchant/raw description/account/amount conditions supported by backend.
- [ ] Edit rule priority, enabled state, conditions, and actions.
- [ ] Delete rule.
- [ ] Keep form constrained to backend-supported rule shape.

**Acceptance:**
- [ ] User can create a useful rule without reading JSON.
- [ ] Rule ordering is visible.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

---

## Phase 3: Transaction Detail, Editing, and Uncategorized Workflow

**Goal:** Make the ledger maintainable day to day.

**Primary ownership:** Frontend first, backend only for missing filters or update fields.

**Dependencies:** Phase 2 API helpers and selectors.

### Task 3.1: Transaction detail drawer or page

**Files likely touched:**
- `web/app/t/[slug]/transactions/page.tsx`
- `web/components/transactions/*`
- `web/lib/api/client.ts`

**Steps:**
- [ ] Add transaction row action to open detail.
- [ ] Show account, dates, status, amount, merchant, category, tags, description, counterparty, notes.
- [ ] Add edit mode for backend-supported mutable fields.
- [ ] Allow category and merchant assignment.
- [ ] Allow notes and description updates.
- [ ] Allow delete or void according to existing backend semantics.

**Acceptance:**
- [ ] User can correct a transaction after creation.
- [ ] The list updates after save/delete.
- [ ] Error states are explicit and recoverable.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 3.2: Transaction filters

**Files likely touched:**
- `web/app/t/[slug]/transactions/page.tsx`
- `web/components/transactions/*`
- `web/lib/api/client.ts`
- `backend/internal/transactions/*` if filters are missing

**Steps:**
- [ ] Add filters for account, status, date range, and uncategorized.
- [ ] Keep filter state in URL search params.
- [ ] Respect backend limit behavior.
- [ ] Add clear filters action.

**Acceptance:**
- [ ] Filtered URLs are shareable/reloadable.
- [ ] Existing backend filters are used before adding new ones.
- [ ] `go test ./...` if backend changes.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 3.3: Uncategorized transaction queue

**Files likely touched:**
- `web/app/t/[slug]/transactions/uncategorized/page.tsx` or a tab/filter in `transactions/page.tsx`
- `web/components/transactions/*`
- `web/components/classification/*`

**Steps:**
- [ ] Provide a focused view for transactions with no category and no split lines.
- [ ] Allow quick category assignment.
- [ ] Allow merchant assignment.
- [ ] Allow tag assignment.
- [ ] Add "apply categorization rules" action.
- [ ] Move completed items out of the queue after successful classification.

**Acceptance:**
- [ ] User can clear the uncategorized queue quickly.
- [ ] Dashboard uncategorized count links here.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 3.4: Transaction tags UI

**Files likely touched:**
- `web/components/transactions/*`
- `web/lib/api/client.ts`

**Steps:**
- [ ] Show assigned tags on transaction rows or detail.
- [ ] Add tag selector in transaction detail.
- [ ] Add/remove tags using existing transaction tag endpoints.

**Acceptance:**
- [ ] Tags can be assigned and removed without a full page reload.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

---

## Phase 4: Basic Insights

**Goal:** Give users feedback from account and categorized transaction data without building the full reporting engine yet.

**Primary ownership:** Backend and frontend can split by endpoint/view.

**Dependencies:** Transactions with categories and accounts with balances.

### Task 4.1: Decide derived-read strategy

**Files likely touched:**
- `backend/internal/reports/*` or `backend/internal/insights/*`
- `backend/internal/http/router.go`
- `openapi/openapi.yaml`
- `web/lib/api/client.ts`

**Steps:**
- [ ] Decide whether Phase 4 reads can be computed client-side from existing endpoints or need backend aggregate endpoints.
- [ ] Prefer backend endpoints once data size or semantics become non-trivial.
- [ ] Document the decision in the PR or plan notes.

**Acceptance:**
- [ ] The strategy can support current month/cycle totals and category spend.
- [ ] No report table is introduced as source of truth.

### Task 4.2: Backend summary endpoints, if needed

**Files likely touched:**
- `backend/internal/insights/*`
- `backend/internal/http/router.go`
- `openapi/openapi.yaml`

**Steps:**
- [ ] Add workspace-scoped summary endpoint for current period totals.
- [ ] Add category spend aggregate.
- [ ] Add merchant spend aggregate.
- [ ] Add tests with multiple workspaces to verify isolation.
- [ ] Update OpenAPI and regenerate clients.

**Acceptance:**
- [ ] Aggregates exclude transfers/refunds only if relationship tables and semantics are implemented enough to do so correctly.
- [ ] Multi-workspace isolation tests pass.
- [ ] `go test ./...` passes.

### Task 4.3: Dashboard insights

**Files likely touched:**
- `web/app/t/[slug]/page.tsx`
- `web/components/dashboard/*`

**Steps:**
- [ ] Show net worth or currency-grouped balances.
- [ ] Show income, expense, and net for current month or cycle.
- [ ] Show category spend list.
- [ ] Show top merchants.
- [ ] Show recent transactions and uncategorized count.

**Acceptance:**
- [ ] Dashboard is useful after 10 manually entered transactions.
- [ ] Every number can be traced back to transactions/accounts.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

---

## Phase 5: CSV Import MVP

**Goal:** Add the first real ingestion path before external bank sync.

**Primary ownership:** Backend import service plus frontend upload/mapping flow. Can be split into backend and frontend once contracts are set.

**Dependencies:** Accounts, transactions, source refs/import batches, classification rules.

### Task 5.1: Backend CSV parser and import service

**Files likely touched:**
- `backend/internal/imports/*`
- `backend/internal/http/router.go`
- `backend/db/migrations/*` only if existing schema is insufficient
- `openapi/openapi.yaml`

**Steps:**
- [ ] Implement CSV upload parsing.
- [ ] Create import batch.
- [ ] Support mapping for date, amount, description/counterparty, currency, and optional merchant/category columns.
- [ ] Validate target account and account currency.
- [ ] Preview parsed rows before commit.
- [ ] Commit accepted rows as transactions.
- [ ] Create source refs for dedupe.
- [ ] Run categorization rules after transaction creation.

**Acceptance:**
- [ ] Import preview catches invalid rows.
- [ ] Commit is transactional.
- [ ] Exact duplicate import does not create duplicate transactions.
- [ ] `go test ./...` passes.

### Task 5.2: CSV import UI

**Files likely touched:**
- `web/app/t/[slug]/imports/page.tsx`
- `web/components/imports/*`
- `web/lib/api/client.ts`

**Steps:**
- [ ] Add Imports navigation item.
- [ ] Upload CSV.
- [ ] Select account.
- [ ] Map columns.
- [ ] Preview rows with validation errors.
- [ ] Commit import.
- [ ] Link to imported transactions.

**Acceptance:**
- [ ] User can import a simple bank CSV without editing code.
- [ ] Invalid rows are explained before commit.
- [ ] `pnpm typecheck` and `pnpm lint` pass.

### Task 5.3: Saved import profiles

**Files likely touched:**
- `backend/internal/imports/*`
- `web/components/imports/*`

**Steps:**
- [ ] Save mapping profile per workspace.
- [ ] Reuse previous mapping for the same import profile kind/name.
- [ ] Allow profile rename/delete.

**Acceptance:**
- [ ] Second import from same bank is faster than first import.
- [ ] `go test ./...`, `pnpm typecheck`, and `pnpm lint` pass as applicable.

---

## Phase 6: Planning v0

**Goal:** Introduce Folio's differentiator after the ledger is trustworthy.

**Primary ownership:** Backend planning service plus frontend cycle planner.

**Dependencies:** Categorized transactions, accounts, workspace cycle anchor, categories.

### Task 6.1: Payment cycle generation

**Files likely touched:**
- `backend/internal/planning/*`
- `backend/internal/http/router.go`
- `openapi/openapi.yaml`

**Steps:**
- [ ] Generate payment cycles from workspace cycle anchor.
- [ ] Support current, previous, and next cycle lookup.
- [ ] Add tests for edge anchor days such as 29, 30, 31.
- [ ] Expose workspace-scoped cycle endpoints.

**Acceptance:**
- [ ] Current cycle is deterministic for a given date/timezone.
- [ ] Month length edge cases are tested.
- [ ] `go test ./...` passes.

### Task 6.2: Income sources and recurring templates

**Files likely touched:**
- `backend/internal/planning/*`
- `web/app/t/[slug]/planning/*`
- `web/components/planning/*`

**Steps:**
- [ ] Add backend CRUD for income sources.
- [ ] Add backend CRUD for recurring templates.
- [ ] Add UI for income sources.
- [ ] Add UI for recurring expenses and subscriptions.
- [ ] Keep percentage-of-income support if already modeled cleanly; otherwise defer with explicit TODO.

**Acceptance:**
- [ ] User can define salary and recurring rent.
- [ ] Templates are workspace-scoped and tested.
- [ ] `go test ./...`, `pnpm typecheck`, and `pnpm lint` pass.

### Task 6.3: Cycle plan view

**Files likely touched:**
- `backend/internal/planning/*`
- `web/app/t/[slug]/planning/page.tsx`
- `web/components/planning/*`

**Steps:**
- [ ] Create or fetch current cycle plan.
- [ ] Pre-fill expected income and recurring expenses from templates.
- [ ] Add flexible category budgets.
- [ ] Add one-off planned expenses.
- [ ] Compare planned vs actual using transactions in the cycle window.

**Acceptance:**
- [ ] User can create a plan for the current cycle.
- [ ] Actual spend updates as transactions are categorized.
- [ ] `go test ./...`, `pnpm typecheck`, and `pnpm lint` pass.

### Task 6.4: Action items

**Files likely touched:**
- `backend/internal/planning/*`
- `web/components/planning/*`

**Steps:**
- [ ] Generate action items from planned events where useful.
- [ ] Show pending/done/skipped states.
- [ ] Allow marking an item done.
- [ ] Link done action to a transaction when applicable.

**Acceptance:**
- [ ] Planning creates concrete next actions.
- [ ] Marking done is idempotent.
- [ ] `go test ./...`, `pnpm typecheck`, and `pnpm lint` pass.

---

## Recommended Multi-Agent Split

These can run in parallel once the base contracts are understood:

- **Agent A:** Phase 1 shell and dashboard.
- **Agent B:** Phase 2.1 API/OpenAPI cleanup for classification.
- **Agent C:** Phase 2.2 categories UI after Agent B exposes helpers.
- **Agent D:** Phase 2.3 and 2.4 merchants/tags UI after Agent B exposes helpers.
- **Agent E:** Phase 3 transaction detail/edit after classification selectors exist.

Avoid parallel edits to the same files:

- `web/lib/api/client.ts`
- `openapi/openapi.yaml`
- `web/app/t/[slug]/layout.tsx`
- `web/components/app/workspace-shell.tsx`

If multiple agents need those files, nominate one integration owner.

---

## Explicit Deferrals

Do not start these until the manual ledger MVP is usable:

- GoCardless bank sync.
- IBKR Flex sync.
- AI categorization or receipt OCR.
- Investments reporting.
- Travel/split bills.
- Full export/PDF reports.
- Web push notifications.
- Offline conflict resolution.

These are in scope for Folio, but they depend on clean account, transaction, classification, import, and planning workflows.
