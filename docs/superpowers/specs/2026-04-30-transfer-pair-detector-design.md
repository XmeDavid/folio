# Transfer-pair detector — design

**Status:** Design approved, ready for implementation plan
**Date:** 2026-04-30
**Owner:** David Batista

## 1. Goal

Recognise when two transactions in tracked accounts are the two legs of a single transfer between the user's own accounts, and treat them as a matched pair: hidden from the default transaction list, excluded from income/expense stats, but always discoverable through a "show internal moves" toggle and a counterpart link in the detail pane.

The current state shows the cost of not having this: a single Revolut CSV produces hundreds of transactions with descriptions like *"Transfer to Revolut Digital Assets Europe Ltd"*, *"Buy BTC"*, *"Conversão cambial para EUR"*, each treated as an "expense" attached to a noise merchant. After this feature lands, the same import auto-pairs the matching legs and silences the noise.

## 2. Principles

- **Detection happens automatically on import**, with a manual review surface for ambiguous cases. The user never has to fight a misclassification — auto pairs are reversible (`Unpair`), and Tier-3 *suggestions* require explicit user confirmation before becoming pairs.
- **Pairing is data-only.** A `transfer_matches` row marks the pair; nothing about the underlying transactions changes. `merchant_id` and `category_id` stay as they are. Un-pairing fully restores the prior state.
- **Hidden by default in the transaction list**, but always discoverable via a filter toggle and via the linked counterpart in the detail pane.
- **Stats hard-exclude paired transactions** regardless of whether the user has them visible in the list. Spend totals, savings rate, runway, and category trends never count internal moves.
- **Cross-currency is first-class** in v1, but only via reliable signals (original-amount equality, shared import batch). FX-rate-equivalent guessing is rejected as too noisy.
- **Reusable affordance for review queues.** The "things you should look at" surface lives as a generic "dossier tab" container on the right edge of the workspace; transfer-review is the first tenant. Future review types (anomaly detection, uncategorised cleanup) will register additional tabs.

## 3. Out of scope (v1)

- FX-rate-equivalent matching (convert source via Frankfurter daily rate, match within ±1%). Rejected: too noisy, false-positive-prone.
- Background-job re-detection. The auto-detector runs at import time only; missed pairs are caught by the manual-pair UI or by the user re-triggering detection via `POST /transfers/detect`.
- A dedicated `/transfers` global page (one row per pair, side-by-side legs). The transactions list with the toggle covers v1 needs.
- Bulk pair / bulk unpair operations.
- Per-account "show internal moves" defaults (one global toggle per session is enough).
- Hiding transfer-pair rows from accounts overview / portfolio views (separate query paths; revisit when those views materialise).

## 4. Data model

### 4.1 Existing — `transfer_matches` (no schema change)

Already in place from migration `20260424000004_transactions.sql`:

```
id                           uuid pk
workspace_id                 uuid not null
source_transaction_id        uuid not null  references transactions
destination_transaction_id   uuid           references transactions, NULL = outbound-to-external
fx_rate                      numeric(28,10)
fee_amount                   numeric(28,8)
fee_currency                 money_currency
tolerance_note               text
provenance                   match_provenance not null   -- enum: 'auto_detected' | 'manual' | 'imported_external' | ...
matched_by_user_id           uuid
matched_at, created_at       timestamptz
```

Plus indexes on `source_transaction_id` and `destination_transaction_id` (partial, where dest is non-null).

### 4.2 New — `transfer_match_candidates`

Added in this feature's migration. Holds Tier-3 suggestions awaiting user confirmation.

```sql
create table transfer_match_candidates (
  id                          uuid primary key,
  workspace_id                uuid not null references workspaces(id) on delete cascade,
  source_transaction_id       uuid not null,
  candidate_destination_ids   uuid[] not null,
  status                      text not null default 'pending',  -- pending | paired | declined
  suggested_at                timestamptz not null default now(),
  resolved_at                 timestamptz,
  resolved_by_user_id         uuid,
  unique (workspace_id, source_transaction_id),
  constraint tmc_source_fk foreign key (workspace_id, source_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tmc_actor_fk foreign key (resolved_by_user_id)
    references users(id) on delete set null
);

create index transfer_match_candidates_pending_idx
  on transfer_match_candidates(workspace_id) where status = 'pending';
```

Status flow: `pending → paired` (user confirmed via dossier tab) or `pending → declined` (user marked as external credit). Once `declined`, Tier-3 will not re-suggest the same source — the unique constraint on `(workspace_id, source_transaction_id)` enforces one candidate row per source.

### 4.3 Transactions (no schema change)

`merchant_id` and `category_id` stay as they are. Pairing is data-only via `transfer_matches`.

## 5. Detection algorithm — `transfers.DetectAndPair`

Single helper, three tiers, run in order. First match wins per source row. A match never replaces an existing `transfer_matches` row.

### 5.1 Tier 1 — original-amount exact match

For each unpaired transaction `t1` (in a tracked account, with `original_amount` and `original_currency` populated), search for an unpaired `t2`:

```
t2.workspace_id  = t1.workspace_id
t2.account_id   != t1.account_id
abs(epoch(t2.booked_at - t1.booked_at)) <= 86400         -- ±1 day
t1.original_amount + t2.amount = 0                       -- exact, no tolerance
t1.original_currency = t2.currency
sign(t1.amount) != sign(t2.amount)
NOT EXISTS (transfer_matches WHERE source = t2.id OR destination = t2.id)
```

**Exactly one** candidate → insert `transfer_matches` with `source = t1.id`, `destination = t2.id`, `fx_rate = abs(t1.amount / t1.original_amount)`, `provenance = 'auto_detected'`. **Multiple candidates** → skip; Tier 3 may surface it for manual review.

### 5.2 Tier 2 — same import_batch + opposite sign + same currency

For each unpaired `t1`, look for unpaired `t2`:

```
t2.workspace_id  = t1.workspace_id
t2.account_id   != t1.account_id
EXISTS (source_refs sr1, source_refs sr2
         WHERE sr1.entity_id = t1.id AND sr2.entity_id = t2.id
           AND sr1.import_batch_id = sr2.import_batch_id
           AND sr1.import_batch_id IS NOT NULL)
t1.currency = t2.currency
abs(t1.amount + t2.amount) <= max(2.00, 0.005 * abs(t1.amount))   -- fee tolerance
sign(t1.amount) != sign(t2.amount)
abs(epoch(t2.booked_at - t1.booked_at)) <= 86400
NOT EXISTS (transfer_matches WHERE source = t2.id OR destination = t2.id)
```

**Exactly one** match → insert `transfer_matches` with `provenance = 'auto_detected'`, `fee_amount = abs(t1.amount + t2.amount)` if non-zero (and `fee_currency` = `t1.currency`).

### 5.3 Tier 3 — heuristic suggest, NOT auto-pair

For each unpaired credit (positive amount) in a tracked account whose `counterparty_raw` is non-empty AND fuzzy-matches one of:

- a tracked account name in this workspace (case-insensitive substring),
- the workspace owner's display name,
- a known transfer phrase from a small built-in list (English + Portuguese + German): `"transfer"`, `"pocket"`, `"between accounts"`, `"transferência"`, `"carregamento"`, `"levantamento"`, `"überweisung"`, `"ueberweisung"` (umlaut-stripped variant), `"umbuchung"`, `"einzahlung"`, `"abhebung"`, `"zwischen konten"`,

…find unpaired debits in **other** tracked accounts within ±5 days, ranked by date proximity then amount closeness. Up to 5 candidates.

Insert/update a `transfer_match_candidates(workspace_id, source_transaction_id, candidate_destination_ids[], status='pending')` row. The dossier-tab review queue reads from this table.

User actions in the drawer:
- **Pair with selected** → insert `transfer_matches` with `provenance = 'manual'`, mark candidate `status = 'paired'`, set `resolved_at`, `resolved_by_user_id`.
- **External credit** → mark candidate `status = 'declined'`. Transaction stays unpaired and visible normally; Tier 3 will not re-surface it.

### 5.4 Properties

- Idempotent: re-running over already-paired transactions writes nothing.
- Scope-limited at import time: `DetectAndPair(scope = {TransactionIDs: insertedIDs})` runs only with the just-imported rows as the "left side" of each search, but the candidate-search range covers all unpaired transactions in the workspace (the counterpart may have been imported earlier).
- A separate `DetectAndPair(scope = {All: true})` re-scans the entire workspace; surfaced via `POST /api/v1/transfers/detect` and useful after algorithm changes or after a wave of declines.

## 6. Lifecycle hooks

### 6.1 Auto-run on import

At the end of `bankimport.Apply` / `ApplyPlan` / `ApplyMultiPlan`, after the import transaction commits, call:

```go
transfersSvc.DetectAndPair(ctx, workspaceID, transfers.Scope{TransactionIDs: insertedIDs})
```

Failures here log warnings but do not roll back the import — pairing is best-effort and can be retried.

### 6.2 Manual pair

`POST /api/v1/transfers/manual-pair`

```json
{ "sourceId": "<uuid>", "destinationId": "<uuid|null>", "feeAmount": "0.50", "feeCurrency": "CHF" }
```

- `destinationId: null` → outbound-to-external (records the row, marks the source as paired but with no counterpart).
- Both source and destination must be unpaired (else 409).
- Server computes `fx_rate` from amounts when currencies differ and `original_amount` is null.
- Sets `provenance = 'manual'`, `matched_by_user_id` from auth context.
- Closes any pending `transfer_match_candidates` row for the source as `paired`.

### 6.3 Decline candidate

`POST /api/v1/transfers/candidates/{candidateId}/decline` — marks the candidate `declined`, sets `resolved_at`/`resolved_by_user_id`. No `transfer_matches` row is written.

### 6.4 Unpair

`DELETE /api/v1/transfers/{matchId}` — removes the `transfer_matches` row. Does NOT restore any `transfer_match_candidates` row. The two transactions become normal again, visible in stats and lists.

### 6.5 Manual re-detect

`POST /api/v1/transfers/detect` — runs `DetectAndPair(scope: All)`. Returns `{ tier1Paired, tier2Paired, tier3Suggested }`.

## 7. Visibility & stats

### 7.1 Transactions list filter

The list endpoint gains a `hideInternalMoves` query parameter (default `true`). SQL effect:

```sql
SELECT t.*
FROM transactions t
WHERE t.workspace_id = $1
  ...other filters...
  AND ($N IS FALSE OR NOT EXISTS (
        SELECT 1 FROM transfer_matches tm
        WHERE tm.workspace_id = t.workspace_id
          AND (tm.source_transaction_id = t.id
               OR tm.destination_transaction_id = t.id)
      ))
```

Frontend filter panel adds a "Show internal moves" checkbox. When toggled on, paired transactions render with a `↔ Transfer` badge (or `↗ External` for outbound-to-external).

### 7.2 Stats

Every aggregate query (income/expense, category rollup, merchant trends, savings rate, runway, calendar heatmap, etc.) gains the same `NOT EXISTS (transfer_matches WHERE ...)` predicate. Hard rule: paired transactions never count, regardless of list visibility.

Net-worth and per-account balance computations are unaffected — balances flow from snapshots + per-account transactions; transfer pairs net to zero per account naturally.

### 7.3 Detail pane

When a transaction has a `transfer_matches` row, the right-pane detail view renders a **Linked transfer** section with:

- The counterpart's date, account name, signed amount.
- A clickable link to focus the counterpart in the list.
- An **Unpair** button (calls `DELETE /transfers/{matchId}`, invalidates list + stats queries).

For outbound-to-external (destination null), the section reads "Outbound to external — no linked counterpart" with the same Unpair button.

## 8. Frontend

### 8.1 Routes

- `web/app/w/[slug]/transfers/review/page.tsx` — full-page review queue. Same data as the drawer, fuller layout.
- The dossier-tab affordance opens a slide-over drawer; the drawer has an "Open full page" link.

### 8.2 Dossier-tab framework (reusable)

Mounted once at the workspace layout (`web/app/w/[slug]/layout.tsx`), pinned to the right edge of the viewport. v1 has one tab. Components:

- `web/components/dossier/dossier-tabs.tsx` — container. Reads tab registrations and renders zero-or-more tabs vertically. Renders nothing when all tabs report `count = 0`.
- `web/components/dossier/dossier-tab.tsx` — single tab affordance: a small paper-flap protruding ~24px from the viewport edge, count badge, click → opens its drawer.
- `web/components/dossier/dossier-drawer.tsx` — right-side slide-over (~420px wide). Header + content slot.
- Each review type ships its own `*-tab.tsx` (e.g. `transfers-review-tab.tsx`) that registers a `DossierTab` with: id, label, count (via React Query), drawer content. Adding a new review type later = adding a new `*-tab.tsx` under `web/components/<area>/`.

### 8.3 Transfer-review components

- `web/components/transfers/transfers-review-tab.tsx` — registers the dossier tab. Polls `fetchPendingTransferCandidateCount(workspaceId)` (cheap GET).
- `web/components/transfers/transfers-review-queue.tsx` — drawer/page-shared queue UI. Each row: source transaction summary (date, account, amount, counterparty raw), radio list of candidate destinations, "Pair with selected" / "External credit" buttons.
- `web/components/transfers/manual-pair-dialog.tsx` — modal opened from the transactions list ("Pair…" affordance on a transaction row's overflow menu) or from the detail pane. Pre-seeded with one transaction; an async-search field finds the counterpart by amount/date/account.
- `web/components/transfers/transfer-badge.tsx` — `↔ Transfer` / `↗ External` badge used in transaction rows + detail pane.

### 8.4 Cross-page wiring

- `web/app/w/[slug]/transactions/page.tsx`: extend `TransactionFilters` with `hideInternalMoves` (default `true`). Add a checkbox "Show internal moves" near the existing "Show archived" toggles. Pass through to the API call. Render `<TransferBadge>` on transaction rows where `transferMatchId` is set.
- `web/components/transactions/transaction-detail.tsx` (or wherever the detail pane lives): add the **Linked transfer** section.
- The transactions API response gains an optional `transferMatchId: string | null` and `transferCounterpartId: string | null` field per transaction so the frontend can render the badge without a follow-up query.

### 8.5 API client extensions

```ts
// web/lib/api/client.ts
export type TransferMatch = { ... };
export type TransferCandidate = { ... };

export async function fetchPendingTransferCandidates(workspaceId): Promise<TransferCandidate[]>;
export async function fetchPendingTransferCandidateCount(workspaceId): Promise<{ count: number }>;
export async function manualPairTransfer(workspaceId, body): Promise<TransferMatch>;
export async function unpairTransfer(workspaceId, matchId): Promise<void>;
export async function declineTransferCandidate(workspaceId, candidateId): Promise<void>;
export async function runTransferDetector(workspaceId): Promise<{ tier1Paired: number; tier2Paired: number; tier3Suggested: number }>;
```

The existing `fetchTransactions` gains a `hideInternalMoves?: boolean` option (default true).

## 9. API surface

```
POST   /api/v1/transfers/detect                          run DetectAndPair(All)
POST   /api/v1/transfers/manual-pair                     {sourceId, destinationId|null, feeAmount?, feeCurrency?}
DELETE /api/v1/transfers/{matchId}                       unpair

GET    /api/v1/transfers/candidates                      list pending candidates
GET    /api/v1/transfers/candidates/count                cheap count for the dossier tab
POST   /api/v1/transfers/candidates/{id}/decline         mark external credit

GET    /api/v1/transactions?hideInternalMoves=true|false (extend existing)
```

### Error contract

| Case | Status | Code |
|---|---|---|
| Manual-pair: source already paired | 409 | `transfer_source_already_paired` |
| Manual-pair: destination already paired | 409 | `transfer_destination_already_paired` |
| Manual-pair: source/destination not in workspace | 404 | `transaction_not_found` |
| Manual-pair: source == destination | 400 | `transfer_self_pair` |
| Decline candidate: candidate not pending | 422 | `transfer_candidate_not_pending` |
| Unpair: match not in workspace | 404 | `transfer_match_not_found` |

## 10. Testing

### 10.1 Backend

**Pure-logic unit tests** for matcher predicates:
- Same-currency fee tolerance.
- Cross-currency original-amount equality.
- Sign / account / date-window guards.

**Integration tests** (`testdb`) — `backend/internal/transfers/`:
- `Tier1_OriginalAmountPair_CrossCurrency` (CHF→EUR via Revolut-style original_amount).
- `Tier1_OriginalAmountPair_SameCurrency` (matches in same currency too).
- `Tier1_AmbiguousMultipleCandidatesSkips` and surfaces a Tier-3 candidate.
- `Tier2_SameBatchPair` (joined via `source_refs.import_batch_id`, fee tolerance applied).
- `Tier3_SuggestsForSelfTransferLikeCounterparty`.
- `Tier3_DeclineDoesntResurface`.
- `ManualPair_HappyPath`.
- `ManualPair_AlreadyPairedReturns409`.
- `ManualPair_OutboundToExternal` (destinationId null).
- `Unpair_RestoresVisibility`.
- `StatsExcludePaired` — direct SQL aggregate over a known fixture.

The bankimport `e2e_merchants_test.go` (or a sibling) gains an assertion: after the import, a `transfer_matches` row exists for the obvious pair embedded in the test data.

### 10.2 Frontend (Vitest)

- `dossier-tabs.test.tsx` — zero registered tabs renders nothing; one with count > 0 renders; click opens drawer; close hides.
- `transfers-review-queue.test.tsx` — pending candidates render; "Pair" button calls `manualPairTransfer` with the selected destination; "External credit" calls `declineTransferCandidate`.
- `transactions-page-toggle.test.tsx` — toggling "Show internal moves" mutates the React Query key; assert the mocked `fetchTransactions` call args.
- `manual-pair-dialog.test.tsx` — search → select → confirm calls `manualPairTransfer` with the right payload; cancel doesn't.

### 10.3 Manual smoke

1. Re-import a Revolut CSV with a known FX conversion → `transfer_matches` row created automatically; both legs hidden from list with toggle off.
2. Toggle "Show internal moves" on → both legs reappear with `↔ Transfer` badge. Click badge → detail panel shows linked counterpart + Unpair button.
3. Tier-3 review: insert a credit with counterparty containing a tracked account name plus a corresponding debit in another account → dossier tab appears with count 1; drawer shows the suggestion; pair from drawer; tab disappears.
4. Decline a candidate → it stays in the list, no tab; re-running detector doesn't re-surface it.
5. Stats page: spend total excludes the paired transactions both before and after the toggle.

## 11. Migration

Single migration `<date>_transfer_match_candidates.sql` adds the new table + index per §4.2. No backfill required — Tier-3 only runs forward; existing un-paired data won't have candidate rows until the next detector run, which the user can trigger via `POST /api/v1/transfers/detect`.

The existing `transfer_matches` table is unchanged.

## 12. Open questions / future

- A dedicated `/transfers` page that views one row per pair (left/right legs side-by-side) for serious reconciliation work. Defer until v1 ships and the user has a real use case.
- FX-rate-equivalent matching as a Tier-2.5 (Frankfurter day rate within ±0.5%). Defer; user-confirmed pairing via the dossier tab is the v1 fallback.
- Background re-detection job. Defer; the manual-detect endpoint covers the rare case of needing to retry.
- Bulk operations: select multiple candidates → pair-all / decline-all in one click. Defer.
- Smarter Tier-3 ranking: amount-relative vs exact-currency-match, learn from past confirmations. Defer.
- Auto-detect when an outbound-to-external is in fact internal (e.g. user added the counterpart account later). Manual unpair + re-detect is fine for v1.
