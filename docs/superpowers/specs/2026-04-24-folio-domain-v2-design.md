# Folio Domain v2 — Schema Design

**Status:** Approved for implementation
**Date:** 2026-04-24
**Source:** `FEATURE-BIBLE.md`, `docs/domain-model-v2.md`
**Supersedes:** `backend/db/migrations/20260424000000_init.sql` (scaffold) and `docs/domain.md` (scaffold-era)

## 1. Goal

Replace the scaffold schema with the full v2 domain model. The scaffold covers ~6 tables; v2 covers everything in the feature bible. Since there is no production data, the schema is rewritten in place (no migration history to preserve).

The deliverable is the data model — tables, constraints, enums, indexes. Handlers, sqlc queries, and OpenAPI paths grow later per feature. `docs/domain.md` is rewritten to describe the v2 model so docs don't drift.

## 2. Non-goals

- No Go code changes (handlers, services, sqlc queries).
- No OpenAPI schema expansion beyond the current scaffold (contracts grow with real endpoints).
- No seed data / fixtures.
- No row-level security (tenant isolation at the service layer in v1).
- No partitioning strategy (defer until a tenant has enough rows to need it).
- No performance benchmarking — indexes are baseline; tune on real queries.

## 3. Cross-cutting conventions

### 3.1 Common columns

Every tenant-owned row:

```sql
id          uuid primary key default uuidv7(),
tenant_id   uuid not null references tenants(id) on delete cascade,
created_at  timestamptz not null default now(),
updated_at  timestamptz not null default now()
```

`updated_at` is maintained by a single `set_updated_at()` trigger (already in scaffold) attached to every table that has the column.

### 3.2 UUIDv7

Enable the `pg_uuidv7` extension. `id uuid default uuidv7()` on every table. Client-created rows (offline PWA captures) may override the server default with a client-generated v7 — the column is `default`, not `generated always`, so a supplied value is honoured. Bank-sourced rows use the server default.

Rationale:
- Time-sorted: index locality is good for the transaction-heavy tables. Listing "latest N" matches native btree order.
- Single ID column (no `id` + `client_id` split): clients and server speak the same identifier.

### 3.3 Money

Every monetary column is `numeric(28,8) not null`, always paired with a `currency char(3) not null check (currency ~ '^[A-Z]{3}$')`.

- No separate `currencies` lookup table. ISO 4217 + common crypto tickers are validated by the regex; adding a new currency never requires a migration.
- No pre-computed base-currency columns on facts. Base values derive at read time from `fx_rates`. FX corrections automatically recompute reports.
- The only stored base-currency cache is `networth_snapshots.total_value`, which has an explicit recompute trigger list.

### 3.4 Tenant scoping

Every financial row carries `tenant_id`. Every unique constraint that could collide across tenants includes `tenant_id` in its key (e.g. `(tenant_id, name)` on categories).

Tenant isolation is enforced at the service layer — no row-level security in v1. RLS adds operational complexity (connection-pooling awareness, role management) that is not justified while every tenant has a single user.

### 3.5 Identity: tenants and users

Tenants and users are separate concepts even though v1 enforces 1:1:
- `tenants` own financial data.
- `users` authenticate into a tenant.
- `users.tenant_id` has a `UNIQUE` constraint, enforcing one user per tenant in v1. Future household / admin / read-only-advisor roles drop the unique with a single ALTER instead of rewriting every query.
- Audit rows record `actor_user_id`; it is distinct from the row's `tenant_id`.

### 3.6 Soft delete

Applied only to tables where "archive but may un-archive" matters or where historical preservation drives reports:

| Table | Column |
|---|---|
| accounts | archived_at |
| categories | archived_at |
| merchants | archived_at |
| tags | archived_at |
| recurring_templates | archived_at |
| income_sources | archived_at |
| goals | archived_at |
| wishlist_items | archived_at |
| trips | `status='cancelled'` (enum state, not soft delete) |
| people | archived_at |

Transactions are hard-deleted. The `audit_events` trigger captures the deletion (before/after JSON) so history is preserved. Sessions, tokens, notification events, notification deliveries are hard-deleted (housekeeping jobs prune).

### 3.7 Polymorphic references

`attachment_links`, `audit_events`, and `source_refs` use `entity_type text` + `entity_id uuid`. Text rather than enum: adding a new entity type shouldn't require a migration. The service layer enforces valid values with a constant set.

### 3.8 Relationship tables, not overloaded enums

Where v2 lists `transaction_links` as a grab-bag (transfer, refund, reimbursement, planned_match, receipt_match, asset_purchase, goal_contribution, trip, settlement), this design uses typed tables — one table per kind of relationship, each carrying only the fields relevant to that kind:

- `transfer_matches`
- `refund_matches`
- `planned_event_matches`
- `goal_contributions`
- `trip_transaction_links`
- `split_bill_allocations`
- `receivables` + `settlements`
- `reimbursement_claims`
- `wishlist_purchase_links`

Rationale: v2 principle #3 — "prefer relationship tables over overloaded enums". A single mega-table with nine nullable field-groups is the anti-pattern that principle warns against.

### 3.9 Enums vs text

- **Enum** for closed domains that the app code switches on: `account_kind`, `account_source`, `transaction_status`, `goal_status`, `goal_type`, `notification_channel`, `recurring_template_kind`, `planned_event_status`, `allocation_method`, etc. `ALTER TYPE ADD VALUE` is fine when the domain grows.
- **Text** for open-set domains: provider names, `entity_type`, `event_kind` on notifications. Enforced by service layer.

### 3.10 Naming

- `snake_case` for table and column names.
- Plural table names (`transactions`, `accounts`).
- Foreign keys named `<referenced_singular>_id` (`account_id`, `tenant_id`).
- Timestamps named `<verb>_at` (`created_at`, `booked_at`, `archived_at`).

### 3.11 Triggers and constraints

- One `set_updated_at()` trigger function, attached per-table where needed.
- Leaf-category enforcement on transactions: trigger on `transactions.category_id` / `transaction_lines.category_id` rejects a category that has children.
- Account-currency enforcement: trigger on `transactions` rejects a row where `currency != accounts.currency`.
- Audit trigger on core tables (transactions, accounts, categories, goals, recurring_templates, merchants, rules) inserts to `audit_events` before/after update/delete.
- Constraint enforcement that needs cross-row state (line sums, settlement totals, allocation totals ≤ account balance) lives in the service layer; the DB protects per-row invariants.

## 4. File layout

Migrations are grouped by domain, loaded in order. File prefix is ordinal; timestamp suffix is `20260424000000` so Atlas sees one coherent initial apply.

```
backend/db/migrations/
  20260424000001_identity.sql
  20260424000002_accounts.sql
  20260424000003_transactions.sql
  20260424000004_classification.sql
  20260424000005_imports.sql
  20260424000006_planning.sql
  20260424000007_goals.sql
  20260424000008_investments.sql
  20260424000009_assets.sql
  20260424000010_travel_splits.sql
  20260424000011_wishlist.sql
  20260424000012_attachments_audit.sql
  20260424000013_fx_reports.sql
  20260424000014_notifications.sql
```

The existing `20260424000000_init.sql` is deleted.

Cross-file FKs reference earlier files. Where a later file needs to reference a table in the same or earlier domain, it does so directly. Back-references (e.g. `accounts.id` referenced by `transfer_matches` in a later file) are forward-legal because migrations run in order.

## 5. Domain sections

### 5.1 Identity (`001_identity.sql`)

- **`tenants`**: id, name, base_currency char(3), cycle_anchor_day smallint (1–31), locale text, timezone text, created_at, updated_at.
- **`users`**: id, tenant_id (**UNIQUE**), email citext unique, password_hash text nullable, display_name, last_login_at timestamptz nullable, created_at, updated_at.
- **`user_preferences`**: user_id PK, theme text, date_format text, number_format text, display_currency char(3) nullable, feature_flags jsonb not null default '{}', updated_at.
- **`sessions`**: id text PK (token hash), user_id FK cascade, created_at, expires_at, user_agent text, ip inet.
- **`webauthn_credentials`**: id, user_id FK cascade, credential_id bytea UNIQUE, public_key bytea, sign_count bigint, transports text[], label, created_at.
- **`totp_credentials`**: id, user_id FK cascade, secret_cipher text (AES-GCM ciphertext), verified_at, recovery_codes_cipher text, created_at.

### 5.2 Accounts & balances (`002_accounts.sql`)

Enums: `account_kind` (`checking, savings, cash, credit_card, brokerage, crypto_wallet, loan, mortgage, asset, pillar_2, pillar_3a, other`), `balance_snapshot_source` (`opening, bank_sync, manual_checkpoint, valuation, import, recompute`), `reconciliation_status` (`open, balanced, drift`).

- **`accounts`**: id, tenant_id, name, nickname, kind, currency, institution, open_date, close_date nullable, opening_balance numeric(28,8) not null default 0, opening_balance_date date, include_in_networth bool default true, include_in_savings_rate bool not null, archived_at, created_at, updated_at.
- **`account_balance_snapshots`**: id, tenant_id, account_id FK cascade, as_of timestamptz, balance, currency, source enum, note. Unique (account_id, as_of, source).
- **`reconciliation_checkpoints`**: id, tenant_id, account_id, statement_date, asserted_balance, status, drift_amount numeric(28,8) nullable, drift_currency char(3) nullable, resolved_at timestamptz nullable, notes.

Invariants:
- Balance is derived, not stored on `accounts`. Service reads: latest snapshot + sum(transactions since snapshot).
- An `opening` snapshot at `open_date` is seeded by the service on account insert.
- Default `include_in_savings_rate`: true for {checking, savings, cash}; false for {credit_card, brokerage, crypto_wallet, loan, mortgage, asset, pillar_2, pillar_3a, other}. Computed in the service on insert; column is bool so it can be overridden.

### 5.3 Transactions (`003_transactions.sql`)

Enums: `transaction_status` (`draft, posted, reconciled, voided`), `match_provenance` (`auto_detected, manual, user_confirmed_auto`).

- **`transactions`**: id, tenant_id, account_id, status, booked_at date, value_at date nullable, posted_at timestamptz nullable, amount numeric(28,8), currency, original_amount nullable, original_currency nullable, merchant_id nullable FK, category_id nullable FK (leaf only), counterparty_raw text nullable, description text nullable, notes text nullable, count_as_expense bool nullable (NULL = derive), raw jsonb, created_at, updated_at.
- **`transaction_lines`**: id, tenant_id, transaction_id FK cascade, amount, currency, category_id FK (leaf only), merchant_id nullable FK, note text, sort_order int.
- **`transaction_tags`**: (transaction_id, tag_id) composite PK, tenant_id.
- **`transfer_matches`**: id, tenant_id, source_transaction_id FK, destination_transaction_id nullable FK (null = outbound-to-external), fx_rate numeric(28,10) nullable, fee_amount numeric(28,8) nullable, fee_currency char(3) nullable, tolerance_note text, provenance, matched_at timestamptz default now(), matched_by_user_id nullable.
- **`refund_matches`**: id, tenant_id, original_transaction_id FK, refund_transaction_id FK, net_to_zero bool default true, provenance, matched_at, matched_by_user_id nullable.
- **`planned_event_matches`**: id, tenant_id, planned_event_id FK, transaction_id FK, provenance, matched_at.

Invariants:
- `transactions.currency = accounts.currency` (trigger).
- Exactly one of: (`transactions.category_id IS NOT NULL`) XOR (≥1 `transaction_lines` row). Enforced by trigger on insert/update.
- When lines exist, `sum(transaction_lines.amount) = transactions.amount` and all line currencies match parent currency (service guard).
- `amount` sign is account-relative: negative = outflow, positive = inflow. Credit-card refund on a credit_card account = positive.
- Paired transactions (transfer_matches, refund_matches) are excluded from income/expense reports regardless of sign.

Indexes: `(tenant_id, booked_at desc)`, `(account_id, booked_at desc)`, `(category_id)`, `(merchant_id)`, `(status)`.

### 5.4 Classification (`004_classification.sql`)

Enums: `categorization_source` (`ai, rule, merchant_default, similar_transaction`).

- **`categories`**: id, tenant_id, parent_id nullable self-FK on delete set null, name, color, archived_at, sort_order int, created_at, updated_at. Unique (tenant_id, parent_id, name).
- **`category_history`**: id, category_id, renamed_from text nullable, renamed_at, merged_into_category_id nullable FK, actor_user_id nullable.
- **`merchants`**: id, tenant_id, canonical_name, logo_url text, default_category_id nullable FK, industry text, website text, notes text, archived_at, created_at, updated_at. Unique (tenant_id, canonical_name).
- **`merchant_aliases`**: id, tenant_id, merchant_id FK cascade, raw_pattern text, is_regex bool default false, created_at. Unique (tenant_id, raw_pattern).
- **`tags`**: id, tenant_id, name, color, archived_at, created_at, updated_at. Unique (tenant_id, name).
- **`categorization_rules`**: id, tenant_id, priority int, when_jsonb jsonb, then_jsonb jsonb, enabled bool default true, last_matched_at timestamptz nullable, created_at, updated_at.
- **`categorization_suggestions`**: id, tenant_id, transaction_id FK cascade, suggested_category_id FK, confidence numeric(5,4), source enum, created_at, accepted_at nullable, dismissed_at nullable.

Invariants:
- Categories referenced by transactions are leaves (trigger rejects writes where the target category has children).
- Rule evaluation order by `priority ASC` (first match wins, per FEATURE-BIBLE §5).
- Merchant merge flow: update transactions / aliases / rules to point at survivor, then delete the merged merchant.

### 5.5 Imports & providers (`005_imports.sql`)

Enums: `provider_connection_status` (`active, error, revoked, consent_expired`), `import_profile_kind` (`csv, camt053, ibkr_flex, preset_mint, preset_ynab, preset_actual, preset_firefly`), `import_source_kind` (`file_upload, provider_sync, manual`), `import_status` (`pending, parsing, applied, failed`).

- **`provider_connections`**: id, tenant_id, provider text, label, status, secrets_cipher text, metadata jsonb default '{}', consent_expires_at nullable, last_synced_at nullable, next_scheduled_sync_at nullable, last_error text, created_at, updated_at.
- **`provider_accounts`**: id, tenant_id, provider_connection_id FK, account_id nullable FK, external_account_id text, external_payload jsonb, linked_at nullable. Unique (provider_connection_id, external_account_id).
- **`import_profiles`**: id, tenant_id, name, kind, mapping jsonb, options jsonb, created_at, updated_at.
- **`import_batches`**: id, tenant_id, import_profile_id nullable FK, provider_connection_id nullable FK, source_kind, file_name text nullable, file_hash text nullable, status, summary jsonb, created_by_user_id, started_at, finished_at nullable, error text.
- **`source_refs`**: id, tenant_id, entity_type text, entity_id uuid, provider text nullable, import_batch_id nullable FK on delete set null, external_id text, raw_payload jsonb, observed_at. Unique (entity_type, provider, external_id) where provider is not null.

Invariants:
- Dedupe keys live in `source_refs`, not on the entity tables. A transaction can have multiple source_refs (re-import updates payload; unique key on `(entity_type, provider, external_id)` still idempotent).
- `source_refs` is the single polymorphic table — it covers transactions, accounts, balance snapshots, investment trades, dividend events, instrument prices.

### 5.6 Planning (`006_planning.sql`)

Enums: `cycle_anchor_kind` (`monthly_day, biweekly, weekly, custom`), `cycle_status` (`upcoming, active, closed`), `recurring_template_kind` (`income, expense, transfer, investment_contribution, subscription`), `recurring_amount_type` (`fixed, percentage_of_income`), `cycle_plan_status` (`draft, active, closed`), `cycle_plan_line_kind` (`expected_income, recurring_expense, flexible_budget, one_off, savings_rule, planned_investment, trip_budget`), `planned_event_status` (`planned, scheduled, executed, skipped, cancelled`), `action_item_status` (`pending, done, skipped, dismissed`), `rollover_behavior` (`reset, rollover, rollover_with_cap`), `overspend_behavior` (`absorb_to_next_cycle, zero_out`), `income_amount_type` (`fixed, variable`), `tax_hint` (`gross, net`).

- **`payment_cycles`**: id, tenant_id, period_start date, period_end date, anchor, label text, status, closed_at nullable, created_at, updated_at. Unique (tenant_id, period_start).
- **`income_sources`**: id, tenant_id, name, account_id FK, amount_type, expected_amount numeric(28,8), currency, cadence jsonb, tax_hint nullable, notes text, archived_at, created_at, updated_at.
- **`recurring_templates`**: id, tenant_id, kind, name, account_id FK, dest_account_id nullable FK, category_id nullable FK, merchant_id nullable FK, amount_type, amount numeric(28,8) nullable, percentage numeric(5,2) nullable, currency, cadence jsonb, start_date, end_date nullable, share_percentage numeric(5,2) nullable, cancel_url text, notes text, archived_at, created_at, updated_at.
- **`cycle_plans`**: id, tenant_id, payment_cycle_id FK, status, summary jsonb, closed_at nullable, created_at, updated_at. Unique (payment_cycle_id).
- **`cycle_plan_lines`**: id, tenant_id, cycle_plan_id FK cascade, kind, category_id nullable FK, recurring_template_id nullable FK, income_source_id nullable FK, goal_id nullable FK, trip_id nullable FK, planned_amount numeric(28,8), currency, note, sort_order int.
- **`planned_events`**: id, tenant_id, cycle_plan_line_id nullable FK, recurring_template_id nullable FK, kind (shared enum with recurring), account_id, dest_account_id nullable, category_id nullable, merchant_id nullable, planned_for date, amount, currency, status, executed_transaction_id nullable FK, created_at, updated_at.
- **`action_items`**: id, tenant_id, planned_event_id FK cascade, instruction text, due_at date, status, done_at nullable, notes text, created_at, updated_at.
- **`rollover_policies`**: id, tenant_id, category_id FK, behavior, cap_amount numeric(28,8) nullable, cap_currency char(3) nullable, overspend, created_at, updated_at. Unique (tenant_id, category_id).

Invariants:
- `planned_events.status='executed'` requires `executed_transaction_id IS NOT NULL` (check constraint).
- `cycle_plans.status='closed'` freezes `summary`; plan lines may still be edited (audit-logged) but no longer affect reports.
- `rollover_policies` is one-per-category; categories without a policy default to `reset`.

### 5.7 Goals, buckets, savings rules (`007_goals.sql`)

Enums: `goal_status` (`active, paused, reached, archived`), `goal_type` (`emergency, travel, house, retirement, sabbatical, car, wedding, sinking_fund, other`), `goal_allocation_source` (`contribution, savings_rule, manual_adjustment, recompute`), `savings_rule_trigger` (`cycle_close, income_received, goal_reached, manual`), `savings_rule_action` (`percentage_of_leftover, fixed_from_income, top_up_to_balance, redirect_on_reach`).

- **`goals`**: id, tenant_id, name, target_amount numeric(28,8), currency, deadline date nullable, priority int, status, type, parent_goal_id nullable self-FK, auto_redirect_on_reach_goal_id nullable self-FK, notes text, archived_at, created_at, updated_at.
- **`goal_accounts`**: (goal_id, account_id) composite PK, tenant_id, share_percentage numeric(5,2) nullable.
- **`goal_allocations`** (append-only): id, tenant_id, goal_id FK, account_id FK, balance numeric(28,8), currency, as_of timestamptz, source.
- **`goal_contributions`**: id, tenant_id, goal_id FK, transaction_id nullable FK, planned_event_id nullable FK, amount numeric(28,8), currency, applied_at timestamptz.
- **`savings_rules`**: id, tenant_id, name, priority int, trigger, trigger_config jsonb, action, action_config jsonb, enabled bool default true, last_fired_at nullable, created_at, updated_at.

Invariants:
- Current allocation per (goal, account) = latest row in `goal_allocations`. Sum of current allocations per account ≤ account balance (service guard).
- `savings_rules` evaluation order by `priority ASC`.
- Sinking funds are `goals` with `type='sinking_fund'` and an associated `recurring_template` of kind `expense`; no separate table.

### 5.8 Investments (`008_investments.sql`)

Enums: `asset_class` (`equity, etf, bond, fund, reit, option, future, crypto, commodity, cash_equivalent`), `trade_side` (`buy, sell`), `cost_basis_method` (`fifo, lifo, average, specific_lot`), `corporate_action_kind` (`split, reverse_split, merger, spinoff, delisting, symbol_change, cash_distribution, stock_distribution`), `price_source` (`broker, provider_primary, provider_fallback, manual`).

- **`instruments`** (**global, no tenant_id**): id, symbol text, isin text nullable, name, asset_class, currency, exchange text nullable, metadata jsonb, active bool default true, created_at, updated_at. Unique (isin) where isin is not null; unique (symbol, exchange) where isin is null.
- **`investment_accounts`**: account_id PK references accounts(id) on delete cascade, tenant_id, cost_basis_method default `fifo`, default_tax_lot_strategy text nullable.
- **`investment_trades`**: id, tenant_id, account_id FK, instrument_id FK, side, quantity numeric(28,8), price numeric(28,8), currency, fee_amount numeric(28,8) default 0, fee_currency char(3), trade_date, settle_date nullable, linked_cash_transaction_id nullable FK, created_at, updated_at.
- **`investment_lots`**: id, tenant_id, account_id FK, instrument_id FK, acquired_at date, quantity_opening numeric(28,8), quantity_remaining numeric(28,8), cost_basis_per_unit numeric(28,8), currency, source_trade_id FK, closed_at nullable, created_at.
- **`investment_lot_consumptions`**: id, tenant_id, lot_id FK, sell_trade_id FK, quantity_consumed numeric(28,8), realised_gain numeric(28,8), currency, consumed_at date.
- **`dividend_events`**: id, tenant_id, account_id FK, instrument_id FK, ex_date date, pay_date date, amount_per_unit numeric(28,8), currency, total_amount numeric(28,8), tax_withheld numeric(28,8) default 0, linked_cash_transaction_id nullable FK, created_at.
- **`corporate_actions`**: id, tenant_id nullable, account_id nullable FK, instrument_id FK, kind, effective_date date, payload jsonb, applied_at timestamptz nullable, created_at.
- **`instrument_prices`** (**global**): id, instrument_id FK, as_of timestamptz, price numeric(28,8), currency, source, provider_ref text nullable, created_at. Unique (instrument_id, as_of, source).
- **`investment_positions`** (materialized cache): account_id, instrument_id, quantity numeric(28,8), average_cost numeric(28,8), currency, last_price numeric(28,8) nullable, last_price_at nullable, unrealised_gain numeric(28,8) nullable, refreshed_at. Composite PK (account_id, instrument_id).
- **`allocation_buckets`**: id, tenant_id, name, parent_bucket_id nullable self-FK, target_percentage numeric(5,2) nullable, created_at, updated_at.
- **`position_bucket_allocations`**: (account_id, instrument_id, bucket_id) composite PK, tenant_id, share_percentage numeric(5,2) default 100.
- **`target_allocations`**: id, tenant_id, bucket_id FK, target_percentage numeric(5,2), effective_from date, created_at.

Invariants:
- A sell's total `investment_lot_consumptions.quantity_consumed` equals the sell trade's quantity (service guard).
- Sum of `quantity_remaining` across open lots for (account, instrument) = `investment_positions.quantity` (refresh job reconciles).
- Trades/dividends with `linked_cash_transaction_id` are excluded from income/expense reports (investment mechanics, not spending).
- `instruments` and `instrument_prices` are global; tenant deletion does not cascade into them.

### 5.9 Physical assets & retirement (`009_assets.sql`)

Enums: `asset_category` (`car, property, art, watch, collectible, crypto_physical, pension, other`), `asset_valuation_method` (`manual, plugin_eurotax, plugin_zestimate, plugin_crypto_oracle`), `asset_valuation_source` (`manual, plugin, appraisal, purchase, sale`), `asset_event_kind` (`purchase, sale, maintenance, depreciation, appraisal, improvement, transfer_of_ownership`), `depreciation_method` (`straight_line, declining_balance, manual`), `retirement_pillar` (`ch_pillar_3a, ch_pillar_2, other`), `mortgage_payment_status` (`upcoming, due, paid, skipped`).

- **`assets`**: id, tenant_id, account_id FK **UNIQUE**, category, description text, acquired_date, acquired_cost numeric(28,8), currency, disposal_date nullable, disposal_amount nullable, disposal_currency char(3) nullable, valuation_method, notes text, created_at, updated_at.
- **`asset_valuations`**: id, tenant_id, asset_id FK cascade, as_of date, value numeric(28,8), currency, source, note.
- **`asset_events`**: id, tenant_id, asset_id FK cascade, kind, occurred_at date, amount numeric(28,8) nullable, currency char(3) nullable, linked_transaction_id nullable FK, note, created_at.
- **`asset_depreciation_schedules`**: id, tenant_id, asset_id FK cascade UNIQUE, method, start_date, end_date nullable, salvage_value numeric(28,8) nullable, rate numeric(7,4) nullable, schedule_jsonb jsonb nullable.
- **`retirement_contribution_limits`** (**reference data, no tenant_id**): id, country char(2), year int, pillar, amount numeric(28,8), currency. Unique (country, year, pillar).
- **`mortgage_schedules`**: id, tenant_id, account_id FK **UNIQUE** (account of kind `mortgage`), original_principal numeric(28,8), currency, interest_rate numeric(7,4), term_months int, start_date, created_at, updated_at.
- **`mortgage_payments`**: id, tenant_id, mortgage_schedule_id FK cascade, due_on date, principal_amount numeric(28,8), interest_amount numeric(28,8), currency, linked_transaction_id nullable FK, status, paid_at timestamptz nullable, created_at, updated_at.

Invariants:
- Networth for asset-kind accounts is driven by `asset_valuations` (latest per asset_id), not by summing transactions.
- `assets.account_id` is UNIQUE — one asset row per asset-kind account.
- Mortgage payments generate transfer_matches (checking → mortgage) when marked paid.

### 5.10 Travel, split bills, receivables (`010_travel_splits.sql`)

Enums: `trip_status` (`planned, active, completed, cancelled`), `trip_category` (`flights, accommodation, food, activities, transport, shopping, other, custom`), `trip_participant_share_default` (`equal, zero, custom`), `split_allocation_method` (`equal, fixed_amounts, percentages, by_items`), `split_bill_state` (`open, settled`), `receivable_direction` (`i_am_owed, i_owe`), `receivable_origin` (`split_bill, reimbursement, manual_loan, refund_pending`), `receivable_status` (`open, partially_settled, settled, written_off`), `reimbursement_claim_status` (`draft, submitted, approved, paid, rejected`).

- **`people`**: id, tenant_id, name, email citext nullable, phone text nullable, notes text, archived_at, created_at, updated_at.
- **`trips`**: id, tenant_id, name, destinations text[], start_date, end_date, overall_budget numeric(28,8) nullable, currency, status, notes text, created_at, updated_at.
- **`trip_budgets`**: id, tenant_id, trip_id FK cascade, category, custom_label text nullable, budget_amount numeric(28,8), currency. Unique (trip_id, category, custom_label).
- **`trip_participants`**: id, tenant_id, trip_id FK cascade, person_id nullable FK, is_self bool default false, display_name, share_default, created_at.
- **`trip_transaction_links`**: (trip_id, transaction_id) composite PK, tenant_id, trip_category, created_at.
- **`split_bill_events`**: id, tenant_id, transaction_id nullable FK, trip_id nullable FK, total_amount numeric(28,8), currency, allocation_method, note text, state enum, created_at, settled_at nullable.
- **`split_bill_allocations`**: id, tenant_id, split_bill_event_id FK cascade, participant_trip_id nullable FK (trip_participants.id), person_id nullable FK, amount_owed numeric(28,8), currency, item_description text nullable, transaction_line_id nullable FK.
- **`receivables`**: id, tenant_id, counterparty_person_id nullable FK, counterparty_label text, direction, amount numeric(28,8), currency, origin, origin_event_id uuid nullable, due_date date nullable, status, notes text, created_at, updated_at, settled_at nullable.
- **`settlements`**: id, tenant_id, receivable_id FK cascade, settling_transaction_id nullable FK, amount numeric(28,8), currency, settled_at timestamptz default now(), note.
- **`reimbursement_claims`**: id, tenant_id, transaction_id FK, employer_or_counterparty text, claim_status, submitted_at nullable, paid_at nullable, paid_transaction_id nullable FK, notes text, created_at, updated_at.

Invariants:
- `trip_participants`: `person_id IS NOT NULL` XOR `is_self = true` (check constraint).
- `split_bill_allocations`: exactly one of `participant_trip_id` or `person_id` is set (check).
- Sum of `split_bill_allocations.amount_owed` per event = `split_bill_events.total_amount` (service guard; currencies must match).
- Sum of `settlements.amount` per receivable ≤ `receivables.amount`; receivable flips to `settled` when equal.

### 5.11 Wishlist (`011_wishlist.sql`)

Enums: `wishlist_status` (`wanted, bought, abandoned`), `wishlist_price_source` (`manual, scraper`).

- **`wishlist_items`**: id, tenant_id, name, estimated_price numeric(28,8) nullable, currency, url text nullable, notes text, priority int, status, linked_goal_id nullable FK, archived_at, created_at, updated_at.
- **`wishlist_price_observations`**: id, tenant_id, wishlist_item_id FK cascade, observed_at timestamptz, price numeric(28,8), currency, source, scraper_run_id uuid nullable, note.
- **`wishlist_purchase_links`**: id, tenant_id, wishlist_item_id FK **UNIQUE**, transaction_id FK, purchased_at date, actual_amount numeric(28,8), currency, variance numeric(28,8).

Invariants:
- Creating a `wishlist_purchase_links` row sets `wishlist_items.status = 'bought'` (trigger or service).

### 5.12 Attachments, documents, audit (`012_attachments_audit.sql`)

Enums: `attachment_storage` (`local, s3`), `ocr_status` (`none, pending, done, failed`), `audit_action` (`created, updated, deleted, restored, merged`).

- **`attachments`**: id, tenant_id, filename, content_type, size_bytes bigint, storage_backend, storage_key text, sha256 bytea, uploaded_by_user_id, uploaded_at timestamptz default now(), ocr_status default `none`, created_at. Unique (tenant_id, sha256) — dedupe same file.
- **`attachment_links`**: id, tenant_id, attachment_id FK cascade, entity_type text, entity_id uuid, linked_at timestamptz default now(), linked_by_user_id. Unique (attachment_id, entity_type, entity_id).
- **`ocr_documents`**: id, tenant_id, attachment_id FK cascade UNIQUE, text_content text, extracted_jsonb jsonb, processed_at, engine text, confidence numeric(5,4).
- **`saved_searches`**: id, tenant_id, user_id FK, name, filter_jsonb jsonb, pinned bool default false, created_at, updated_at.
- **`audit_events`**: id, tenant_id, entity_type text, entity_id uuid, action, actor_user_id nullable FK, before_jsonb jsonb nullable, after_jsonb jsonb nullable, reason text nullable, ip inet nullable, user_agent text nullable, occurred_at timestamptz default now().

Invariants:
- Attachments are physically stored once per `(tenant_id, sha256)` tuple; `attachment_links` is the m-to-n to entities.
- `audit_events` is append-only (no UPDATE/DELETE — enforced by a revoke-grant pattern or trigger).
- Audit triggers on: `transactions, transaction_lines, accounts, categories, merchants, tags, categorization_rules, recurring_templates, goals, savings_rules, receivables, reimbursement_claims, wishlist_items, assets, mortgage_schedules`.

Indexes: `(tenant_id, entity_type, entity_id, occurred_at desc)` on `audit_events`.

### 5.13 FX & reporting (`013_fx_reports.sql`)

Enums: `fx_source` (`ecb, openexchangerates, manual`), `event_marker_kind` (`auto_large_inflow, auto_balance_jump, user_pinned`), `report_export_kind` (`csv_transactions, pdf_monthly, pdf_yearly, tax_year_gains, wealth_tax, full_data_bundle, custom_template`), `report_export_status` (`queued, running, ready, failed, expired`).

- **`fx_rates`** (**global, no tenant_id**): id, base_currency char(3), quote_currency char(3), as_of date, rate numeric(28,10), source, fetched_at timestamptz default now(). Unique (base_currency, quote_currency, as_of, source).
- **`networth_snapshots`**: id, tenant_id, as_of date, total_value numeric(28,8), base_currency char(3), breakdown_jsonb jsonb, computed_at timestamptz, created_at. Unique (tenant_id, as_of).
- **`event_markers`**: id, tenant_id, as_of date, kind, label text, note text, created_at.
- **`report_exports`**: id, tenant_id, requested_by_user_id FK, kind, params_jsonb, status, file_attachment_id nullable FK, requested_at, completed_at nullable, expires_at nullable, error text.
- **`export_templates`**: id, tenant_id, name, kind, definition_jsonb, created_at, updated_at.

Invariants:
- FX rates are global; recompute triggers on: FX rate inserts within a window, backfill, account creation, reconciliation checkpoint, transaction date earlier than any existing snapshot.
- `networth_snapshots` is a cache; `total_value` derivable from accounts × valuations × FX.

### 5.14 Notifications (`014_notifications.sql`)

Enums: `notification_channel` (`in_app, email, web_push, webhook`), `notification_digest_mode` (`realtime, daily_digest, weekly_digest`), `notification_delivery_status` (`queued, sent, failed, read`).

- **`notification_rules`**: id, tenant_id, name, event_kind text, config_jsonb, enabled bool default true, digest_mode default `realtime`, created_at, updated_at.
- **`notification_events`**: id, tenant_id, rule_id nullable FK, event_kind text, subject_entity_type text, subject_entity_id uuid, payload_jsonb, occurred_at timestamptz default now().
- **`notification_deliveries`**: id, tenant_id, notification_event_id FK cascade, channel, destination_id uuid nullable, status default `queued`, attempted_at timestamptz nullable, delivered_at nullable, read_at nullable, error text. Unique (notification_event_id, channel, destination_id).
- **`notification_preferences`**: id, tenant_id, user_id FK, event_kind text, channel, enabled bool default true, digest_override notification_digest_mode nullable. Unique (user_id, event_kind, channel).
- **`webhook_destinations`**: id, tenant_id, name, url text, secret_cipher text, enabled bool default true, last_success_at nullable, last_error text, created_at, updated_at.

Invariants:
- `notification_events` is immutable; state lives on `notification_deliveries`.
- `event_kind` is open-set text; adding a new kind doesn't require a migration.
- Retry policy (River job layer) updates `notification_deliveries.attempted_at` and `error`; a delivery has at most one row per (event, channel, destination).

## 6. `docs/domain.md` rewrite

The current `docs/domain.md` describes the scaffold model (5 entities, cached account balance, single-user scoping). After the migrations land, rewrite it to summarise the v2 model:

- Identity & tenancy (tenants vs users).
- Money, FX, and base-currency rules.
- Accounts and derived balances (snapshots + transactions).
- Transactions, lines, and relationship-table matches.
- Classification vs tagging.
- Imports and source refs.
- Planning, goals, investments, assets, travel/splits, wishlist (one paragraph each).
- Cross-cutting concerns (attachments, audit, notifications, FX).

Keep it a high-level overview (≤2 pages). The spec (this document) is the reference.

## 7. Out-of-scope / future

- Row-level security (defer until multi-user-per-tenant arrives).
- Table partitioning (defer until a tenant has enough rows).
- Materialized view refresh policy (`investment_positions`, `networth_snapshots` — job layer, not schema).
- Search indexes (`saved_searches` persists the query spec; tsvector columns and GIN indexes come with search features).
- Seed data / category defaults (handled by onboarding, not schema).
- Data retention / pruning (housekeeping jobs, not schema).

## 8. Acceptance criteria

- Existing `backend/db/migrations/20260424000000_init.sql` deleted.
- 14 new migration files in load order, each under ~250 lines.
- `atlas migrate apply --env local` against a fresh Postgres 17 database succeeds with no errors.
- `sqlc generate` succeeds (no queries yet; schema is valid).
- `backend/go build ./...` succeeds (only schema changed; no handler code to break).
- `pg_uuidv7` extension available in the dev/prod images.
- `docs/domain.md` rewritten; no references to scaffold-era shape remain.
