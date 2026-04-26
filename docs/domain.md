# Domain model

Folio's data model is documented in full at
`docs/superpowers/specs/2026-04-24-folio-domain-v2-design.md`. This
document is the high-level narrative; the spec is the authoritative
column listing.

## Identity and workspace

- **Workspaces** own all financial data. Financial rows carry `workspace_id`.
- **Users** authenticate into a workspace. v1 enforces one user per workspace
  via `users.workspace_id UNIQUE`.
- Workspace isolation is enforced by **composite foreign keys** (every
  workspace-scoped table has `UNIQUE (workspace_id, id)`; every FK from
  another workspace-scoped table is composite).

## Money and currency

- Every amount is `numeric(28,8)` — a single precision for fiat,
  crypto, and FX.
- Every amount carries a currency (`money_currency` domain over
  `varchar(10)`, ISO 4217 plus crypto tickers).
- Base-currency values are **derived** from `fx_rates` at read time.
  The only stored base-currency cache is `networth_snapshots.total_value`.
- UUIDv7 primary keys, generated app-side (backend: `uuid.NewV7()`;
  web PWA: any JS UUIDv7 lib).

## Facts, intentions, interpretations

The schema separates three layers:

| Layer | Tables |
|---|---|
| **Facts** (what happened) | `transactions`, `transaction_lines`, `account_balance_snapshots`, `investment_trades`, `investment_lots`, `dividend_events`, `asset_valuations`, `fx_rates` |
| **Intentions** (what you plan) | `recurring_templates`, `cycle_plans`, `cycle_plan_lines`, `planned_events`, `action_items`, `goals`, `savings_rules`, `rollover_policies` |
| **Interpretations** (how Folio reads the data) | `categories`, `merchants`, `tags`, `categorization_rules`, `transfer_matches`, `refund_matches`, `planned_event_matches`, `goal_contributions`, `split_bill_allocations`, `trip_transaction_links` |

## Accounts and balances

- An **account** is anything with a balance: bank account, brokerage,
  cash pot, credit card, loan, mortgage, physical asset, retirement
  pillar.
- Balances are derived from `account_balance_snapshots` + post-snapshot
  transactions. No cached balance column on `accounts`.
- Reconciliation checkpoints assert "at date D, balance was B" and
  surface drift.

## Transactions and classification

- `transactions` carries `status` (`draft/posted/reconciled/voided`)
  and optional `category_id`.
- Splits create `transaction_lines`; each line has its own category
  and amount. When lines exist, `category_id` on the parent must be
  null (enforced by trigger).
- Uncategorised transactions (no `category_id`, no lines) are valid —
  they surface in the "Uncategorised" bucket or are classified by
  relationship (`transfer_matches`, `investment_trades.linked_cash_transaction_id`,
  `dividend_events.linked_cash_transaction_id`,
  `mortgage_payments.linked_transaction_id`,
  `asset_events.linked_transaction_id`).
- Transfers, refunds, reimbursements, goal contributions, trip spend,
  and split-bill shares all live in **typed relationship tables**, not
  in transaction fields.

## Imports and providers

- `provider_connections` holds encrypted tokens for external sources
  (GoCardless, IBKR Flex, crypto addresses).
- `import_batches` records each file or sync run.
- Dedupe keys live in `source_refs` (polymorphic, workspace-scoped).

## Planning, goals, investments, assets, travel, wishlist

- **Planning**: payment cycles, recurring templates, cycle plans with
  per-line kinds (expected_income / recurring_expense /
  flexible_budget / one_off / savings_rule / planned_investment /
  trip_budget), planned_events + action_items for execution,
  rollover_policies per category.
- **Goals**: hierarchical goals, multi-account allocations, savings
  rules with priority-ordered evaluation.
- **Investments**: global `instruments` + `instrument_prices`;
  workspace-scoped trades, lots, lot_consumptions, dividends, corporate
  actions, and a materialized `investment_positions` cache.
- **Physical assets & retirement**: `assets` are 1:1 with asset-kind
  accounts; valuations drive networth. `mortgage_schedules` and
  `mortgage_payments` split each payment into principal/interest.
  `retirement_contribution_limits` is country/year reference data.
- **Travel & split bills**: trips with per-category budgets,
  participants (app users or external `people`), split-bill events
  with per-allocation amounts, receivables ledger + settlements.
- **Wishlist**: items with price-observation history; purchase_links
  flip the item to `bought` and connect it to the real transaction.

## Cross-cutting

- **Attachments**: deduped by `(workspace_id, sha256)`; linked to any
  entity via the polymorphic `attachment_links`.
- **Audit**: `audit_events` is append-only; triggers on every audited
  table record create/update/delete with before/after JSON. Actor is
  read from `current_setting('folio.actor_user_id')`.
- **FX**: `fx_rates` is global. Base-currency reports derive from
  rates at read time; retroactive rate corrections automatically
  recompute.
- **Notifications**: rules → events → deliveries; per-user channel
  preferences; webhook destinations for external integrations.

## Soft invariants

- `transactions.currency = accounts.currency` (trigger).
- A transaction's `category_id` must reference a leaf category
  (trigger).
- A transaction cannot have both `category_id` and lines (trigger).
- Workspace isolation is composite-FK-enforced at the DB layer.
- Archived accounts are excluded from sums by default (query helper).
