# Merchants & default-categorisation — design

**Status:** Design approved, ready for implementation plan
**Date:** 2026-04-29
**Owner:** David Batista

## 1. Goal

Make merchants a first-class navigation surface and the default driver of transaction categorisation. The user should be able to:

- See every merchant their transactions hit, with logo, default category, transaction count, and totals.
- Drill into one merchant and see all of its transactions.
- Set a default category on a merchant. New transactions inherit it; existing transactions can be retroactively re-categorised on demand.
- Clean up the long tail of bank-emitted strings ("MIGROSEXP-7711", "COOP-4382 ZUR") by **renaming** a merchant or **merging** it into another, with the old raw strings captured as aliases so future imports auto-link.
- Filter the transactions list by merchant.

## 2. Principles

- **One transaction → at most one merchant.** Auto-created at import from `counterparty_raw`. Optional when there is no counterparty (transfers, ATM, fees).
- **Manual user intent always wins** over automatic behavior. Category writes from import / merchant-default / merge cascade only happen when the field is empty *or* equals the value being replaced — never silently overwrite a user-chosen category.
- **Stateless cascade rule.** "Apply to all" means "update transactions whose category equals the merchant's previous default." No `category_source` audit column is needed.
- **Imports are deterministic.** Exact-string match on `counterparty_raw` looking up `merchants.canonical_name` ∪ `merchant_aliases.raw_pattern`. No fuzzy normalisation in v1.
- **Aliases capture user cleanup.** Renaming or merging a merchant stores the prior name(s) so future imports of the same raw bank string land on the right merchant without manual intervention.

## 3. Out of scope (v1)

- Internal-transfer auto-detection. Transfers between own accounts get `merchant_id = NULL` and will be handled by a future transfer-pair detector.
- Fuzzy/normalised import matching ("COOP-XXXX ZUR" → Coop without explicit alias).
- AI / LLM enrichment of merchant metadata.
- Logo lookup service.
- Merging more than two merchants in one operation.
- Restoring a merge (undo).

## 4. Data model

### 4.1 Existing — `merchants` (no new columns; one new constraint)

Columns already present and reused as-is:

```
id                   uuid pk
workspace_id         uuid not null
canonical_name       text not null
logo_url             text
default_category_id  uuid       -- references categories(id)
industry             text
website              text
notes                text
archived_at          timestamptz
created_at, updated_at
```

Constraint to add (if not already present): `UNIQUE (workspace_id, canonical_name) WHERE archived_at IS NULL`. This makes rename collisions explicit and lets imports rely on canonical-name lookup. Archived merchants are excluded from the unique index so an archived "Coop" doesn't block a new active "Coop".

### 4.2 New — `merchant_aliases`

```
id           uuid pk
workspace_id uuid not null
merchant_id  uuid not null  references merchants(id) on delete cascade
raw_pattern  text not null
is_regex     boolean not null default false
created_at   timestamptz default now()

UNIQUE (workspace_id, raw_pattern)
INDEX  (merchant_id)
```

Each `raw_pattern` value can map to at most one merchant in a workspace.

> **Naming note:** the column is `raw_pattern` (not `raw_alias`) because the table was created earlier as part of the classification migration (`backend/db/migrations/20260424000003_classification.sql`) with a forward-compatibility `is_regex` flag alongside it. In v1 `is_regex` is always `false` and is **not** exposed on the API (see §3 and §12) — the entire matching path is exact-string equality on `raw_pattern`. The column and flag are reserved for a possible v2 regex-aliases feature; we adopted the existing schema rather than rename it.

### 4.3 Transactions (no schema change)

`merchant_id` (nullable) and `counterparty_raw` (nullable text) already exist on the `transactions` table. No new columns.

## 5. Import behavior (a.k.a. "attach by raw")

Single helper `merchants.AttachByRaw(ctx, workspaceID, counterpartyRaw)` returns a `*Merchant` (or `nil` if `counterpartyRaw` is empty). Called by every importer (`bankimport`, GoCardless, IBKR cash, manual entry) immediately after the row is parsed and before insertion.

```
attach_by_raw(workspace_id, raw):
    if raw is empty: return null

    merchant := query:
        select m.* from merchants m
        where m.workspace_id = $w
          and m.canonical_name = $raw
          and m.archived_at is null
        union all
        select m.* from merchants m
        join merchant_aliases a on a.merchant_id = m.id
        where a.workspace_id = $w
          and a.raw_pattern = $raw
          and m.archived_at is null
        limit 1

    if merchant is null:
        merchant := insert into merchants (workspace_id, canonical_name)
                    values ($w, $raw) returning *

    return merchant
```

Then, in the calling importer, after the transaction row is materialised:

```
if merchant != null:
    txn.merchant_id = merchant.id
    if txn.category_id is null and merchant.default_category_id is not null:
        txn.category_id = merchant.default_category_id
```

**Properties:**

- One round-trip for lookup, one for create-on-miss.
- Archived merchants do not match. Archiving a merchant and re-importing creates a new active row.
- The same code path runs for every import source — single source of truth.
- Existing `category_id` is never overwritten (manual override wins).
- Concurrent imports inserting the same raw simultaneously: rely on a `(workspace_id, canonical_name)` unique constraint plus `INSERT … ON CONFLICT DO NOTHING RETURNING` followed by a re-select. Avoids racing duplicate creates.

## 6. Lifecycle operations

### 6.1 Rename — `PATCH /api/v1/merchants/{id}` with `{ canonicalName }`

In one transaction:

1. Read current `canonical_name`. No-op if unchanged.
2. Insert old `canonical_name` into `merchant_aliases` with `ON CONFLICT (workspace_id, raw_pattern) DO NOTHING`.
3. Update `merchants.canonical_name`.
4. On unique-violation against another active merchant: return **409** with `{ code: "merchant_name_conflict", existingMerchantId }`. Frontend can offer "Merge into that merchant instead?"

### 6.2 Default-category change — same `PATCH /api/v1/merchants/{id}` with `{ defaultCategoryId, cascade }`

Behavior:

- `cascade = false` (default) → update merchant only. Existing transactions untouched. Future imports use the new default.
- `cascade = true` → in one DB transaction:
  1. Read current `default_category_id` as `old_default` (may be null).
  2. Update merchants row to new default.
  3. ```sql
     UPDATE transactions
     SET category_id = $new_default
     WHERE workspace_id = $w
       AND merchant_id  = $id
       AND category_id IS NOT DISTINCT FROM $old_default
     RETURNING id
     ```
  4. Return `{ merchant, cascadedTransactionCount }`.

`IS NOT DISTINCT FROM` handles the null case (first time a default is set, fill nulls). Manually-categorised transactions (category not equal to old default) are not touched.

### 6.3 Merge — `POST /api/v1/merchants/{sourceId}/merge` with `{ targetId, applyTargetDefault }`

Validation:

- `sourceId != targetId`
- both belong to the calling workspace
- target not archived

Lock both merchant rows `FOR UPDATE` ordered by `id` to prevent concurrent-merge races.

In one DB transaction:

1. Read source `canonical_name` and `default_category_id` as `source_old_default`.
2. Reparent aliases:
   ```sql
   INSERT INTO merchant_aliases (workspace_id, merchant_id, raw_pattern)
   SELECT workspace_id, $target, raw_pattern
   FROM merchant_aliases
   WHERE merchant_id = $source
   ON CONFLICT (workspace_id, raw_pattern) DO NOTHING;
   DELETE FROM merchant_aliases WHERE merchant_id = $source;
   ```
3. Capture source canonical name as alias of target:
   ```sql
   INSERT INTO merchant_aliases (workspace_id, merchant_id, raw_pattern)
   VALUES ($w, $target, $source_canonical_name)
   ON CONFLICT (workspace_id, raw_pattern) DO NOTHING;
   ```
4. Reassign transactions, capturing IDs into application memory (the Go service holds `movedIDs []uuid.UUID` from `RETURNING id`):
   ```sql
   UPDATE transactions SET merchant_id = $target
   WHERE merchant_id = $source
   RETURNING id;
   ```
5. Fill blanks on target metadata for `logo_url, industry, website, notes`:
   ```sql
   UPDATE merchants t SET
     logo_url = COALESCE(t.logo_url, s.logo_url),
     industry = COALESCE(t.industry, s.industry),
     website  = COALESCE(t.website,  s.website),
     notes    = COALESCE(t.notes,    s.notes)
   FROM merchants s
   WHERE t.id = $target AND s.id = $source;
   ```
   `default_category_id` is **not** filled — target wins on category policy.
6. If `applyTargetDefault = true` (using the `movedIDs` slice from step 4):
   ```sql
   UPDATE transactions
   SET category_id = $target_default
   WHERE id = ANY($moved_ids)
     AND category_id IS NOT DISTINCT FROM $source_old_default;
   ```
7. Delete source merchant row.
8. Return `{ target, movedCount, cascadedCount, capturedAliasCount }`.

### 6.4 Merge preview — `POST /api/v1/merchants/{sourceId}/merge/preview` with `{ targetId }`

Read-only. Returns the same counts the real merge would produce so the dialog can render "2 transactions will move; 1 currently matches source's default and would be re-categorised if you check that box". Computed by inspecting source/target rows and running the same predicates as the merge SQL without writing.

### 6.5 Archive — `PATCH /api/v1/merchants/{id}` with `{ archived: true }`

Already implemented. Behavior:

- Hidden from default merchant list (frontend `?includeArchived=true` to show).
- Excluded from import lookup (Section 5) and merge target search.
- Their transactions stay attached and visible.

## 7. API surface

```
GET    /api/v1/merchants                                list
POST   /api/v1/merchants                                create
GET    /api/v1/merchants/{id}                           get
PATCH  /api/v1/merchants/{id}                           update (canonicalName/defaultCategoryId/cascade/etc.)
DELETE /api/v1/merchants/{id}                           archive

GET    /api/v1/merchants/{id}/aliases                   list aliases
POST   /api/v1/merchants/{id}/aliases                   { rawPattern }
DELETE /api/v1/merchants/{id}/aliases/{aliasId}         remove

POST   /api/v1/merchants/{id}/merge/preview             { targetId } → counts
POST   /api/v1/merchants/{id}/merge                     { targetId, applyTargetDefault }

GET    /api/v1/transactions?merchantId=...              filter (extend existing)
```

### Error contract

| Case | Status | Code |
|---|---|---|
| Rename collision with another active merchant | 409 | `merchant_name_conflict` (returns `existingMerchantId`) |
| Merge target archived | 422 | `merge_target_archived` |
| Merge source == target | 400 | `merge_source_equals_target` |
| Merge target/source not found in workspace | 404 | `merchant_not_found` |
| Default category does not exist or is archived | 422 | `default_category_invalid` |
| Alias already exists pointing at another merchant | 409 | `alias_conflict` (manual `POST .../aliases`) |

## 8. Frontend

### 8.1 Routes

- `web/app/w/[slug]/merchants/page.tsx` — list (replaces the 20-line placeholder).
- `web/app/w/[slug]/merchants/[merchantId]/page.tsx` — new detail page.

### 8.2 Layouts

- **List:** dense table — name + logo, default category, txn count, last seen, total spend (workspace base currency). Search box, "Show archived" toggle, "New merchant" button. Click a row → detail page.
- **Detail:** persistent left sidebar (logo, editable canonical name, editable default category, industry/website/notes, aliases list with remove-X, action buttons) + right pane with that merchant's transactions table.

### 8.3 New components (`web/components/classification/`)

- `merchants-table.tsx`
- `merchant-detail-sidebar.tsx`
- `merchant-aliases.tsx` (read + remove + manual add)
- `merchant-default-category-dialog.tsx` — fires when `defaultCategoryId` changes and merchant has > 0 transactions; sends `cascade: true|false` based on user choice.
- `merchant-merge-dialog.tsx` — single-modal merge UX. Async-search target → calls `/merge/preview` → renders preview + "apply target default" checkbox → calls `/merge` on confirm.

### 8.4 Reuse

- The transactions table on the detail page is the **same component** used on `/transactions`. Extract `transactions-table.tsx` if not already, parameterised by filter (`{ merchantId }`).
- The default-category cascade dialog is reused by **any** UI that changes a merchant's default — sidebar editor, future bulk-edit, etc.

### 8.5 Cross-page integration

- `/transactions`: extend filter chip set with **Merchant**. Render the merchant column as a link to `/merchants/{id}`.
- Transaction edit form (wherever it surfaces): the merchant picker becomes "type to search; if no match, create new from this raw string." Server-side, when a transaction's `merchant_id` is changed via PATCH, run the same "if txn.category_id is null and new merchant has default, apply default" logic. (One-line change in the transactions service's update path.)

### 8.6 API client / hooks

Extend `web/lib/api/client.ts` with the endpoints in Section 7. Add React Query hooks: `useMerchants`, `useMerchant`, `useMerchantAliases`, `useMergePreview`. Mutations: `useCreateMerchant`, `useUpdateMerchant`, `useArchiveMerchant`, `useAddMerchantAlias`, `useRemoveMerchantAlias`, `useMergeMerchants`. Standard invalidation patterns: any merchant mutation invalidates `["merchants", workspaceId, ...]` and `["transactions", workspaceId, ...]`.

## 9. Concurrency & consistency

- All multi-step operations (rename, default cascade, merge, import attach-by-raw create) run inside a single `BEGIN…COMMIT`. No partial states are observable.
- Merge acquires `SELECT … FOR UPDATE` on both merchants ordered by `id` to avoid deadlocks.
- The `(workspace_id, canonical_name) WHERE archived_at IS NULL` unique constraint plus `INSERT … ON CONFLICT DO NOTHING RETURNING` makes import auto-create idempotent under concurrent imports of the same raw string. Lookup-after-conflict resolves to the row another transaction created.
- Default-category cascade and merge cascade are bounded `UPDATE … WHERE merchant_id = $id`; with the existing `idx_transactions_merchant` index, cost is linear in matched rows.

## 10. Testing

### 10.1 Backend (Go)

**Unit (pure logic, no DB):**

- `attach_by_raw` decision tree: empty raw → nil; raw matches canonical → reuse; raw matches alias → reuse; archived merchant ignored.
- Cascade query predicate: `IS NOT DISTINCT FROM` handles null-old-default and value-old-default branches correctly.

**Integration (`testdb`):**

- Rename → captures alias; subsequent import of the old name attaches to the renamed merchant.
- Rename collision against another active merchant → 409, no DB mutation.
- Default-category change `cascade=false` → metadata changes, transactions unchanged.
- Default-category change `cascade=true` → transactions matching old default updated; manually-categorised transactions untouched; returned `cascadedTransactionCount` matches actual rows updated.
- Merge end-to-end happy path: 2 source transactions + 1 source alias + source canonical name → all attached/captured on target after merge; source row gone; logo backfilled when target was empty; `applyTargetDefault=true` recategorises only just-moved transactions whose category equals source's old default.
- Merge with overlapping aliases → no duplicate-alias error, single alias row per raw.
- Merge preview produces counts equal to real-merge results (run preview, run merge, assert equality).
- Concurrent merges of the same source → second returns 404 (source already deleted).
- Import after merge: raw string that previously pointed at the deleted source resolves to target.

### 10.2 Frontend (Vitest)

- API client: each new endpoint asserts request shape and parses response correctly.
- `merchant-merge-dialog`: preview is fetched on target select; "apply default" toggles `applyTargetDefault` in confirm payload; cancel does not call merge.
- `merchant-default-category-dialog`: with txn count = 0 the dialog is skipped (PATCH sends `cascade: false`); with txn count > 0 and "Yes" the PATCH sends `cascade: true`.
- Cross-page link: clicking the merchant cell on `/transactions` navigates to `/merchants/{id}`.

### 10.3 Manual smoke

1. Import a real CAMT.053 with mixed Coop variants → reasonable merchant rows; drill-down works.
2. Rename a merchant → re-import the same file → transactions still attach to the renamed merchant.
3. Merge two merchants with overlapping aliases → no duplicate-alias error.
4. Set a default on a merchant with ~50 transactions, click "Apply to all" → only previously-uncategorised ones change; manually-categorised ones unchanged.

## 11. Migration

- Add `merchant_aliases` table.
- Add `UNIQUE (workspace_id, canonical_name) WHERE archived_at IS NULL` partial index on `merchants` (if not already present). If duplicates exist in current data, a one-shot data migration must reconcile them — auto-merge duplicates by oldest-first, capturing each duplicate's name as alias on the survivor. (Run as a separate, idempotent job; don't block the schema change on the data step.)
- No backfill of `merchant_aliases` from existing canonical names is required — Section 5 query reads canonical_name directly.

## 12. Open questions / future

- Manual alias add (`POST /merchants/{id}/aliases`) lets a user pre-empt an upcoming bank format change. Useful but not strictly required for v1; ship it because the underlying table already supports it.
- Bulk merge (select N source merchants → merge into one target) is post-v1.
- Merge undo / soft-delete the source row (instead of hard delete) so a 24-hour reverse is possible — defer; YAGNI.
- Auto-suggest merge targets ("MIGROSEXP-7711 looks like Migros") via prefix/Levenshtein heuristics — defer to a separate "merchant suggestions" feature.
