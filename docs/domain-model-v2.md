# Folio Domain Model v2 - Working Notes

Source: `FEATURE-BIBLE.md`, created 2026-04-24.

This document translates the feature bible into modeling decisions from first
principles. It intentionally ignores the current scaffold/schema.

## Problems To Resolve First

### 1. Workspace vs user identity

The feature bible says Folio is single-user per workspace, with platform-level
isolation. Model that explicitly:

- `workspaces` own all financial data.
- `users` authenticate into a workspace.
- v1 can enforce one normal user per workspace, while still allowing future admin,
  recovery, or household/member support without rewriting every table.

Decision: model both `workspace_id` and `user_id`. Financial rows belong to
`workspace_id`; audit rows record `actor_user_id`.

### 2. Ledger facts vs plans

"Every posted, planned, or scheduled financial event is a transaction" is too
broad for the database. Bank facts, user plans, recurring templates, and action
instructions have different lifecycles.

Recommendation:

- `transactions`: actual ledger facts, imported or manually entered.
- `planned_events`: future or hypothetical money movements.
- `recurring_templates`: rules that generate planned events.
- `cycle_plan_lines`: per-cycle budget/income/expense plan.
- `action_items`: executable instructions derived from plans.
- Matching tables link planned/action items to actual transactions.

### 3. Accounts vs assets vs holdings

"Every balance lives on an account" works for UX, but not cleanly for storage.
Checking balances, credit-card liabilities, investment cash, securities,
physical assets, and mortgages behave differently.

Recommendation:

- `accounts`: containers that can have balances and transactions.
- `account_balance_snapshots`: observed or asserted balances over time.
- `investment_positions` / `investment_lots`: securities held inside brokerage
  accounts.
- `asset_valuations`: valuation history for physical assets, retirement
  accounts, crypto wallets, and other manually valued items.

Accounts can still be the user-facing umbrella.

### 4. Transaction category rule needs nuance

"Each transaction has exactly one leaf category" conflicts with transfers,
investment trades, refunds, split transactions, and uncategorised workflows.

Recommendation:

- Parent transaction category is nullable.
- Category is required on classification lines that count in reports.
- Split transactions use `transaction_lines`, each with its own category.
- Transfers, internal settlements, and investment mechanics are classified by
  relationship tables, not expense categories.

### 5. Transfers need their own relationship table

Transfers are not a separate financial fact, but the matched-pair layer still
needs persistence.

Recommendation: `transaction_links` or `transfer_matches` with type, leg A, leg
B, FX amount/rate, tolerance metadata, and manual/auto provenance.

### 6. Refund sign language is ambiguous

A refund is not reliably "negative amount"; signs are account-relative. A card
refund may be positive on a liability account, while a checking refund may also
be positive.

Recommendation: model refund/reimbursement semantics via links and report
treatment, not sign alone.

### 7. Provider source and import source are different concepts

`manual`, `gocardless`, `ibkr_flex`, `camt053_import`, `csv_import`,
`crypto_address` mixes connection providers, import methods, and manual data.

Recommendation:

- `provider_connections`: durable external connections.
- `import_batches`: uploaded/imported files and parser runs.
- `source_refs`: external IDs and raw payload references per entity.
- Accounts/transactions can point to source refs without encoding everything in
  an enum.

### 8. Offline PWA creates conflict resolution requirements

Offline transaction capture means client-created rows may later match bank sync
or server edits.

Recommendation: all user-created entities need stable client-generated IDs,
`created_at`, `updated_at`, and conflict-aware sync semantics. Actual bank data
should not be overwritten by stale offline edits.

### 9. Audit and attachments are cross-cutting

Audit logs and attachments appear everywhere. Avoid one attachment column per
domain table.

Recommendation:

- `attachments` plus polymorphic `attachment_links`.
- `audit_events` with entity type/id, actor, before/after JSON, and reason.

### 10. Derived views should not become source tables

Subscriptions, reports, alerts, net-worth history, savings rate, and plan-vs-
actual views are mostly derived. Some need snapshots for performance or history,
but the source of truth should remain transactions, plans, positions,
valuations, FX rates, and checkpoints.

## Core Model

## Modeling Principles

1. Separate **facts**, **intentions**, and **interpretations**.
   - Facts: bank transactions, balances, trades, imported statement rows.
   - Intentions: budgets, planned payments, goals, savings rules.
   - Interpretations: categories, merchant cleanup, transfer matching, reports.

2. Preserve source data.
   Imported records should keep enough raw/source metadata that Folio can
   re-parse, debug, dedupe, and explain its decisions later.

3. Prefer relationship tables over overloaded enums.
   Transfers, refunds, reimbursements, goal contributions, trip spending,
   receipt matches, and asset purchases are links between facts and contexts.

4. Keep user-facing flexibility without corrupting accounting facts.
   Users can rename merchants, reclassify lines, tag trips, and override report
   treatment. They should not have to mutate a bank-sourced fact to do it.

5. Make recomputation normal.
   Backfill, category edits, FX-rate changes, checkpoints, and corrected imports
   should trigger derived-view recomputation instead of forcing hand-maintained
   report tables.

6. Treat money as `(decimal amount, currency, date context)`.
   Base-currency values are derived through FX rates unless explicitly stored as
   a source-provided value.

### Identity

- `workspaces`
- `users`
- `user_preferences`
- `sessions`
- `webauthn_credentials`
- `totp_credentials`

Financial data is scoped by `workspace_id`.

### Accounts And Balances

- `accounts`
  - workspace, name, kind, currency, institution, open date, close/archive date
  - inclusion flags: net worth, liquid savings rate, reports
  - optional provider/import/source metadata
- `account_balance_snapshots`
  - account, date/time, balance, source
  - source examples: opening, bank_sync, manual_checkpoint, valuation, import
- `reconciliation_checkpoints`
  - account, statement date, asserted balance, status, notes

Important invariant: account currency is the currency for cash-like
transactions on that account. Investment positions can have instrument
currencies distinct from the account base currency.

### Transactions

- `transactions`
  - workspace, account, status, dates, amount, currency, original amount/currency
  - merchant, notes, raw description, source/import refs
  - reconciled source marker if matched to bank data
- `transaction_lines`
  - transaction, amount, category, merchant override, note
  - used for splits and report classification
- `transaction_tags`
- `transaction_links`
  - types: transfer, refund, reimbursement, planned_match, receipt_match,
    asset_purchase, goal_contribution, trip, settlement

Recommended status vocabulary:

- `planned` only on planned events, not actual transactions.
- Actual transactions use `draft`, `posted`, `reconciled`, `voided`.

### Classification

- `categories`
  - hierarchical, workspace scoped, merge/archive supported
- `category_aliases` or `category_history`
- `merchants`
- `merchant_aliases`
- `tags`
- `categorization_rules`
- `categorization_suggestions`

Categories answer "what kind of money movement was this?" Tags answer "what
slice or context does this belong to?"

### Imports And Providers

- `provider_connections`
  - provider, status, encrypted secrets, consent expiry, last/next sync, errors
- `provider_accounts`
  - provider connection to Folio account mapping
- `import_profiles`
  - CSV/CAMT/parser mapping profile
- `import_batches`
  - file/source, parser, status, created by, summary counts
- `source_refs`
  - entity type/id, provider/import batch, external id, raw payload pointer

Important invariant: dedupe keys live in source refs and are unique per source,
not in the main transaction table only.

### Planning

- `payment_cycles`
  - workspace, period start/end, anchor, status
- `income_sources`
- `recurring_templates`
  - income, expense, transfer, investment contribution, subscription
- `cycle_plans`
- `cycle_plan_lines`
  - expected income, recurring expense, flexible budget, one-off, savings rule
- `planned_events`
  - generated or manual planned money movement
- `action_items`
  - derived instruction with pending/done/skipped state
- `rollover_policies`
  - per category, reset/rollover/cap/overspend behavior

Plans are allowed to change without rewriting historical bank facts.

### Goals And Buckets

- `goals`
  - target amount/currency, deadline, priority, status, type
- `goal_accounts`
  - links goals to real accounts
- `goal_allocations`
  - virtual sub-balances inside accounts
- `goal_contributions`
  - link actual or planned transactions to goals
- `savings_rules`

Virtual buckets are Folio-side allocations; they must sum to no more than the
available balance of their linked account unless the user explicitly allows
negative buckets.

### Investments

- `instruments`
  - symbol, ISIN, name, asset class, currency
- `investment_accounts`
  - account-specific settings
- `investment_trades`
- `investment_lots`
- `investment_positions`
  - derived or materialized from lots/trades
- `dividend_events`
- `corporate_actions`
- `instrument_prices`
- `allocation_buckets`
- `position_bucket_allocations`
- `target_allocations`

Trades and dividends can create linked cash transactions, but investment
accounting should not be forced into ordinary expense categories.

### Physical Assets And Retirement

- `assets`
  - workspace, linked account, type, description, purchase metadata
- `asset_valuations`
- `asset_events`
  - purchase, sale, maintenance, depreciation, appraisal
- `retirement_contribution_limits`
  - country/year specific, e.g. Swiss Pillar 3a

Physical assets can be represented as account-like in the UI, but valuation
history is the source of truth for net worth.

### Trips, Split Bills, Receivables

- `people`
  - external participants, not app users
- `trips`
- `trip_budgets`
- `trip_participants`
- `trip_transaction_links`
- `split_bill_events`
- `split_bill_allocations`
- `receivables`
- `settlements`

Split transaction lines classify a purchase. Split bill allocations decide who
owes whom. They are related but not the same primitive.

### Wishlist

- `wishlist_items`
- `wishlist_price_observations`
- `wishlist_purchase_links`

### Attachments, Documents, Search

- `attachments`
- `attachment_links`
- `ocr_documents`
- `saved_searches`
- search indexes/materialized views as implementation detail

### FX And Reporting

- `fx_rates`
  - base currency, quote currency, date, rate, source
- `networth_snapshots`
  - optional materialized daily snapshots
- `report_exports`
- `export_templates`

Reports must be reproducible from source data. Snapshots are caches with
recompute rules.

### Notifications

- `notification_rules`
- `notification_events`
- `notification_deliveries`
- `notification_preferences`
- `webhook_destinations`

Alerts are generated from rules and source events; delivery is separate so one
event can fan out to in-app, email, push, or webhook.

## Suggested Build Slices

### Slice 1 - Trustworthy Ledger

Identity, accounts, balance snapshots, transactions, categories, merchants,
tags, rules, attachments, audit, CSV/CAMT imports, and export.

### Slice 2 - Planning

Payment cycles, income sources, recurring templates, cycle plans, planned
events, action items, rollover, plan-vs-actual.

### Slice 3 - External Sync

GoCardless, IBKR Flex, provider connections, import/source refs, dedupe,
reconciliation checkpoints.

### Slice 4 - Goals And Net Worth

Goals, virtual buckets, savings rules, FX rates, net-worth snapshots, retirement
manual balances, physical asset valuations.

### Slice 5 - Investments

Instruments, trades, lots, dividends, prices, corporate actions, allocation
targets, tax reports.

### Slice 6 - Collaboration Adjacent

Trips, split bills, receivables, settlements, reimbursements.

### Slice 7 - Intelligence And Automation

AI suggestions, receipt capture, anomaly detection, price scraping, external
alerts.
