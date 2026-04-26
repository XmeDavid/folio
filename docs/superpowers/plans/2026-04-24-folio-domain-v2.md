# Folio Domain v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the scaffold schema with the full Folio v2 domain model — 14 domain-grouped migration files covering ~70 tables — and rewrite `docs/domain.md` to match.

**Architecture:** Schema-only work. No Go code changes, no sqlc queries, no OpenAPI edits. Migrations are grouped by domain (identity → accounts → classification → transactions → imports → planning → goals → investments → assets → travel/splits → wishlist → attachments/audit → FX/reports → notifications). UUIDv7 app-side, uniform `numeric(28,8)` money, composite foreign keys for workspace isolation, typed relationship tables.

**Tech Stack:** Go 1.25, Postgres 17 (stock image, no extensions), [Atlas](https://atlasgo.io) for migration apply, [sqlc](https://sqlc.dev) for generated queries (run to confirm schema parses).

**Spec:** `docs/superpowers/specs/2026-04-24-folio-domain-v2-design.md` — the column-level source of truth. Every task points at a section in the spec; the spec has the table/column/enum listings, the plan has the patterns, verification commands, and commit messages.

---

## 0. Setup and shared patterns

### 0.1 Working directory

All `atlas` and `sqlc` commands run in `backend/`:

```bash
cd /Users/xmedavid/dev/folio/backend
```

Postgres must be running:

```bash
cd /Users/xmedavid/dev/folio
docker compose -f docker-compose.dev.yml up -d
```

`DATABASE_URL` must be exported (see `.env.example`). Atlas reads it through `atlas.hcl`.

### 0.2 Naming and file order

- Migration filenames: `20260424000001_identity.sql` through `20260424000014_notifications.sql`. Atlas applies in name-sorted order.
- No migration preserves history from the scaffold. The scaffold file is deleted in Task 1.
- SQL style: lowercase keywords, `snake_case` identifiers, `create table ... (` with `primary key` inline, constraints named `<table>_<purpose>_{chk,fk,uq}` when a name is useful.

### 0.3 Reset-from-scratch apply

Because we're replacing the schema, every task applies against a **freshly reset** database. Reset command:

```bash
# From backend/
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
```

This is safe in dev only. Never run in production.

### 0.4 Shared SQL patterns

The following patterns are referenced by name from every migration task. Define each **once** in the migration where it is first used.

#### P1 — `set_updated_at` trigger function (defined in `001_identity.sql`)

```sql
create or replace function set_updated_at() returns trigger language plpgsql as $$
begin
  new.updated_at = now();
  return new;
end;
$$;
```

Bind to every table with an `updated_at` column:

```sql
create trigger <table>_updated_at before update on <table>
  for each row execute function set_updated_at();
```

#### P2 — `money_currency` domain (defined in `001_identity.sql`)

Centralises the ISO-4217-plus-crypto currency check:

```sql
create domain money_currency as varchar(10)
  check (value ~ '^[A-Z0-9]{3,10}$');
```

Every currency column elsewhere uses `money_currency not null` (or `money_currency nullable` where spec allows null).

#### P3 — Workspace-scoped table skeleton

Every table that carries `workspace_id` follows this skeleton:

```sql
create table <name> (
  id          uuid primary key,
  workspace_id   uuid not null references workspaces(id) on delete cascade,
  -- ...domain columns...
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  unique (workspace_id, id)                 -- target for composite FKs
);

create trigger <name>_updated_at before update on <name>
  for each row execute function set_updated_at();
```

Tables without `updated_at` (append-only: `goal_allocations`, `audit_events`, `notification_events`, `fx_rates`, `instrument_prices`, `wishlist_price_observations`, `category_history`, `settlements`, `investment_lot_consumptions`, `source_refs`, `event_markers`) skip the trigger bind but keep the `unique (workspace_id, id)` row.

#### P4 — Composite FK to a workspace-scoped parent

```sql
constraint <child>_<ref>_fk foreign key (workspace_id, <ref>_id)
  references <parent>(workspace_id, id)
  on delete cascade          -- or: set null, or: restrict, per spec
```

Examples (to be used verbatim, substituting table names):

```sql
constraint transactions_account_fk
  foreign key (workspace_id, account_id)
  references accounts(workspace_id, id) on delete cascade

-- self-referential:
constraint categories_parent_fk
  foreign key (workspace_id, parent_id)
  references categories(workspace_id, id) on delete set null

-- nullable user ref (MATCH SIMPLE = default; NULLs skip the check):
constraint audit_events_actor_fk
  foreign key (workspace_id, actor_user_id)
  references users(workspace_id, id) on delete set null
```

#### P5 — Leaf-category guard (defined in `003_classification.sql`)

```sql
create or replace function assert_leaf_category() returns trigger language plpgsql as $$
begin
  if new.category_id is null then return new; end if;
  if exists (
    select 1 from categories
    where parent_id = new.category_id and archived_at is null
  ) then
    raise exception 'category_id % is not a leaf', new.category_id;
  end if;
  return new;
end;
$$;
```

Bound from `004_transactions.sql` (since transactions doesn't exist yet in 003):

```sql
create trigger transactions_leaf_category
  before insert or update of category_id on transactions
  for each row when (new.category_id is not null)
  execute function assert_leaf_category();

create trigger transaction_lines_leaf_category
  before insert or update of category_id on transaction_lines
  for each row when (new.category_id is not null)
  execute function assert_leaf_category();
```

#### P6 — Transaction-classification guard (defined in `004_transactions.sql`)

Disallows the one forbidden state: `category_id` set AND `transaction_lines` exist.

```sql
create or replace function assert_no_double_classification() returns trigger language plpgsql as $$
begin
  -- On transaction update/insert: if category_id is set, confirm no lines exist.
  if tg_table_name = 'transactions' and new.category_id is not null then
    if exists (select 1 from transaction_lines where transaction_id = new.id) then
      raise exception 'transaction % has lines; category_id must be null', new.id;
    end if;
  end if;

  -- On transaction_lines insert: confirm parent has no category_id.
  if tg_table_name = 'transaction_lines' then
    if exists (
      select 1 from transactions
      where id = new.transaction_id and category_id is not null
    ) then
      raise exception 'transaction % has category_id; cannot add lines', new.transaction_id;
    end if;
  end if;

  return new;
end;
$$;

create trigger transactions_no_double_class
  before insert or update of category_id on transactions
  for each row execute function assert_no_double_classification();

create trigger transaction_lines_no_double_class
  before insert on transaction_lines
  for each row execute function assert_no_double_classification();
```

#### P7 — Audit event recorder (defined in `012_attachments_audit.sql`)

```sql
create or replace function record_audit_event() returns trigger language plpgsql as $$
declare
  v_entity_type text := tg_argv[0];
  v_actor uuid := nullif(current_setting('folio.actor_user_id', true), '')::uuid;
  v_workspace uuid;
  v_entity_id uuid;
  v_before jsonb;
  v_after jsonb;
  v_action audit_action;
begin
  if tg_op = 'DELETE' then
    v_action := 'deleted'; v_workspace := old.workspace_id; v_entity_id := old.id;
    v_before := to_jsonb(old); v_after := null;
  elsif tg_op = 'UPDATE' then
    v_action := 'updated'; v_workspace := new.workspace_id; v_entity_id := new.id;
    v_before := to_jsonb(old); v_after := to_jsonb(new);
  else
    v_action := 'created'; v_workspace := new.workspace_id; v_entity_id := new.id;
    v_before := null; v_after := to_jsonb(new);
  end if;

  insert into audit_events (
    workspace_id, entity_type, entity_id, action,
    actor_user_id, before_jsonb, after_jsonb, occurred_at
  ) values (
    v_workspace, v_entity_type, v_entity_id, v_action,
    v_actor, v_before, v_after, now()
  );

  return coalesce(new, old);
end;
$$;
```

Application code sets `set_config('folio.actor_user_id', <uuid>::text, true)` at the start of a request; the trigger reads it. If unset, the audit row has `actor_user_id = null` (system/job event).

Bind per audited table (list in Task 13):

```sql
create trigger <table>_audit
  after insert or update or delete on <table>
  for each row execute function record_audit_event('<entity_type>');
```

### 0.5 Per-task verification baseline

Every migration task ends with the same three checks:

```bash
# 1. Apply from scratch
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local

# 2. sqlc still parses the schema
sqlc generate

# 3. backend still compiles
go build ./...
```

Expected: all three succeed. `atlas migrate apply` prints `Migrated to version 2026042400XXXX_<name>` (or similar); `sqlc generate` produces nothing new (no queries yet) and exits 0; `go build` exits 0.

Note: sqlc reads DDL from `db/migrations/` and generates Go types into `internal/db/dbq/`. With no `db/queries/` entries yet, sqlc emits only the empty package. That is expected.

---

## Task 1: Remove scaffold and verify clean baseline

**Files:**
- Delete: `backend/db/migrations/20260424000000_init.sql`
- Modify: `docs/domain.md` — add a one-line "being rewritten" banner so anyone who peeks at it before the rewrite isn't misled. Final rewrite is Task 16.

- [ ] **Step 1: Confirm no code depends on the scaffold tables**

Run:

```bash
cd /Users/xmedavid/dev/folio
rg -g '!docs' -g '!*.md' -l 'from transactions|from accounts|from users|from categories|from sessions|from provider_connections' backend/ web/ openapi/ 2>/dev/null || true
```

Expected: no matches outside documentation. If any appear (handlers wired to old schema), stop and escalate — the scaffold assumption no longer holds.

- [ ] **Step 2: Delete the scaffold migration**

```bash
rm backend/db/migrations/20260424000000_init.sql
```

- [ ] **Step 3: Drop the dev DB schema**

```bash
cd backend
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
```

- [ ] **Step 4: Confirm Atlas sees zero migrations**

```bash
atlas migrate status --env local
```

Expected: `No migrations have been applied yet. No migration files found.`

- [ ] **Step 5: Mark `docs/domain.md` as superseded**

Replace the top of the file with:

```markdown
# Domain model

> **Being rewritten** to match `docs/superpowers/specs/2026-04-24-folio-domain-v2-design.md`. Until the rewrite (tracked at the end of this plan), consult the spec for the authoritative column listing.

## Entities
```

Keep the rest of the old text below this banner — Task 16 replaces it wholesale.

- [ ] **Step 6: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/db/migrations/ docs/domain.md
git commit -m "$(cat <<'EOF'
Remove scaffold migration ahead of v2 rewrite

Drop backend/db/migrations/20260424000000_init.sql and flag
docs/domain.md as being rewritten. Schema is re-created in 14
domain-grouped migrations in the following tasks.
EOF
)"
```

---

## Task 2: `001_identity.sql`

**Spec:** §5.1 (Identity) + §3.1–3.11 conventions.

**Files:**
- Create: `backend/db/migrations/20260424000001_identity.sql`

- [ ] **Step 1: Enable required extensions and define shared pieces**

At the top of the file:

```sql
-- Folio v2 domain — identity.
-- Workspaces own financial data; users authenticate into a workspace (1:1 in v1).

create extension if not exists citext;

-- Shared: updated_at trigger (P1). Used by every table with updated_at.
create or replace function set_updated_at() returns trigger language plpgsql as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

-- Shared: money_currency domain (P2). Used by every currency column.
create domain money_currency as varchar(10)
  check (value ~ '^[A-Z0-9]{3,10}$');
```

- [ ] **Step 2: `workspaces` — not workspace-scoped, plain id PK**

```sql
create table workspaces (
  id                uuid primary key,
  name              text not null,
  base_currency     money_currency not null,
  cycle_anchor_day  smallint not null check (cycle_anchor_day between 1 and 31),
  locale            text not null default 'en',
  timezone          text not null default 'UTC',
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create trigger workspaces_updated_at before update on workspaces
  for each row execute function set_updated_at();
```

- [ ] **Step 3: `users` — one-to-one with workspaces in v1**

```sql
create table users (
  id             uuid primary key,
  workspace_id      uuid not null unique references workspaces(id) on delete cascade,
  email          citext not null unique,
  password_hash  text,
  display_name   text not null,
  last_login_at  timestamptz,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  unique (workspace_id, id)           -- composite-FK target
);

create trigger users_updated_at before update on users
  for each row execute function set_updated_at();
```

- [ ] **Step 4: `user_preferences`, `sessions`, `webauthn_credentials`, `totp_credentials`**

Write each per spec §5.1. None carry `workspace_id` (they belong to a user; workspace is reached via join).

- `user_preferences`: `user_id uuid primary key references users(id) on delete cascade`, `theme text`, `date_format text`, `number_format text`, `display_currency money_currency`, `feature_flags jsonb not null default '{}'::jsonb`, `updated_at timestamptz not null default now()`. Bind P1 trigger.
- `sessions`: `id text primary key`, `user_id uuid not null references users(id) on delete cascade`, `created_at`, `expires_at timestamptz not null`, `user_agent text`, `ip inet`. Index `(user_id)`, `(expires_at)`. No trigger (no updated_at).
- `webauthn_credentials`: `id uuid primary key`, `user_id uuid not null references users(id) on delete cascade`, `credential_id bytea not null unique`, `public_key bytea not null`, `sign_count bigint not null default 0`, `transports text[]`, `label text`, `created_at`. No updated_at, no trigger.
- `totp_credentials`: `id uuid primary key`, `user_id uuid not null references users(id) on delete cascade`, `secret_cipher text not null`, `verified_at timestamptz`, `recovery_codes_cipher text`, `created_at`. No updated_at.

- [ ] **Step 5: Run the verification baseline (§0.5)**

Expected: `atlas migrate apply` succeeds; `\dt` in psql shows `workspaces, users, user_preferences, sessions, webauthn_credentials, totp_credentials`.

Spot-check:

```bash
psql "$DATABASE_URL" -c '\d users'
```

Expected: `workspace_id UNIQUE`, composite `UNIQUE (workspace_id, id)`, FK to `workspaces(id) ON DELETE CASCADE`.

- [ ] **Step 6: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add backend/db/migrations/20260424000001_identity.sql
git commit -m "$(cat <<'EOF'
feat(schema): add identity migration

Workspaces, users (1:1 with workspaces in v1), user_preferences, sessions,
webauthn_credentials, totp_credentials. Introduces the set_updated_at
trigger function and money_currency domain used by later migrations.
EOF
)"
```

---

## Task 3: `002_accounts.sql`

**Spec:** §5.2 (Accounts & balances).

**Files:**
- Create: `backend/db/migrations/20260424000002_accounts.sql`

- [ ] **Step 1: Define the enums**

```sql
create type account_kind as enum (
  'checking', 'savings', 'cash', 'credit_card',
  'brokerage', 'crypto_wallet', 'loan', 'mortgage',
  'asset', 'pillar_2', 'pillar_3a', 'other'
);

create type balance_snapshot_source as enum (
  'opening', 'bank_sync', 'manual_checkpoint',
  'valuation', 'import', 'recompute'
);

create type reconciliation_status as enum (
  'open', 'balanced', 'drift'
);
```

- [ ] **Step 2: `accounts`**

Use the P3 workspace-scoped skeleton. Columns per spec §5.2 — `name text not null`, `nickname text`, `kind account_kind not null`, `currency money_currency not null`, `institution text`, `open_date date not null`, `close_date date`, `opening_balance numeric(28,8) not null default 0`, `opening_balance_date date not null`, `include_in_networth bool not null default true`, `include_in_savings_rate bool not null`, `archived_at timestamptz`.

Index `(workspace_id, archived_at) where archived_at is null` to accelerate "active accounts" lookups.

- [ ] **Step 3: `account_balance_snapshots`**

Workspace-scoped skeleton minus the `updated_at`/trigger (append-only fact):

```sql
create table account_balance_snapshots (
  id          uuid primary key,
  workspace_id   uuid not null references workspaces(id) on delete cascade,
  account_id  uuid not null,
  as_of       timestamptz not null,
  balance     numeric(28,8) not null,
  currency    money_currency not null,
  source      balance_snapshot_source not null,
  note        text,
  created_at  timestamptz not null default now(),
  unique (workspace_id, id),
  unique (account_id, as_of, source),
  constraint abs_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade
);

create index abs_account_timeline_idx on account_balance_snapshots(account_id, as_of desc);
```

- [ ] **Step 4: `reconciliation_checkpoints`**

Per spec §5.2; workspace-scoped with updated_at and P1 trigger. Composite FK to `accounts(workspace_id, id)`.

- [ ] **Step 5: Run verification baseline**

Spot-check:

```bash
psql "$DATABASE_URL" -c "insert into workspaces (id, name, base_currency, cycle_anchor_day) values ('00000000-0000-7000-8000-000000000001', 't1', 'CHF', 25);"
psql "$DATABASE_URL" -c "insert into users (id, workspace_id, email, display_name) values ('00000000-0000-7000-8000-00000000000a', '00000000-0000-7000-8000-000000000001', 'a@example.com', 'Alice');"
psql "$DATABASE_URL" -c "insert into accounts (id, workspace_id, name, kind, currency, open_date, opening_balance_date, include_in_savings_rate) values ('00000000-0000-7000-8000-0000000000ff', '00000000-0000-7000-8000-000000000001', 'Checking', 'checking', 'CHF', '2026-01-01', '2026-01-01', true);"
```

Then attempt the cross-workspace violation:

```bash
psql "$DATABASE_URL" -c "insert into workspaces (id, name, base_currency, cycle_anchor_day) values ('00000000-0000-7000-8000-000000000002', 't2', 'CHF', 25);"
psql "$DATABASE_URL" -c "insert into account_balance_snapshots (id, workspace_id, account_id, as_of, balance, currency, source) values ('00000000-0000-7000-8000-00000000f000', '00000000-0000-7000-8000-000000000002', '00000000-0000-7000-8000-0000000000ff', now(), 0, 'CHF', 'opening');"
```

Expected: last insert fails with an FK violation on `abs_account_fk` (the snapshot row's workspace_id ≠ account's workspace_id).

Clean up:

```bash
psql "$DATABASE_URL" -c "drop schema public cascade; create schema public;"
atlas migrate apply --env local
```

- [ ] **Step 6: Commit**

```bash
git add backend/db/migrations/20260424000002_accounts.sql
git commit -m "feat(schema): add accounts and balance snapshots migration

Accounts, account_balance_snapshots, reconciliation_checkpoints.
Account balance is derived from the latest snapshot + post-snapshot
transactions; no cached balance column."
```

---

## Task 4: `003_classification.sql`

**Spec:** §5.3 (Classification). `categorization_suggestions` is declared in Task 5, not here.

**Files:**
- Create: `backend/db/migrations/20260424000003_classification.sql`

- [ ] **Step 1: Enums**

```sql
create type categorization_source as enum (
  'ai', 'rule', 'merchant_default', 'similar_transaction'
);
```

- [ ] **Step 2: Define `assert_leaf_category` function (P5)**

Include the full P5 body at the top of the file. The transaction-side triggers that USE it are defined in Task 5; defining the function here keeps the dependency clean.

- [ ] **Step 3: `categories`, `category_history`, `merchants`, `merchant_aliases`, `tags`, `categorization_rules`**

Per spec §5.3.

- Self-reference on `categories.parent_id` uses composite FK (P4):
  ```sql
  constraint categories_parent_fk
    foreign key (workspace_id, parent_id)
    references categories(workspace_id, id) on delete set null
  ```
- `merchants.default_category_id` → composite FK to `categories`.
- `merchant_aliases.merchant_id` → composite FK to `merchants` (on delete cascade).
- `category_history` carries `workspace_id`; composite FKs to `categories(workspace_id, id)` (both `category_id` and `merged_into_category_id`) and to `users(workspace_id, id)` for `actor_user_id` (on delete set null).
- `categorization_rules.workspace_id` + `priority int not null`; add partial index `where enabled = true` on `(workspace_id, priority)` for rule-engine scans.

- [ ] **Step 4: Run verification baseline**

Spot-check hierarchy constraint:

```bash
# seed workspace + user + two categories
psql "$DATABASE_URL" <<'SQL'
insert into workspaces (id, name, base_currency, cycle_anchor_day)
  values ('00000000-0000-7000-8000-000000000001', 't1', 'CHF', 25);
insert into users (id, workspace_id, email, display_name)
  values ('00000000-0000-7000-8000-00000000000a',
          '00000000-0000-7000-8000-000000000001', 'a@example.com', 'A');
insert into categories (id, workspace_id, name, sort_order)
  values ('00000000-0000-7000-8000-00000000c001',
          '00000000-0000-7000-8000-000000000001', 'Food', 0);
insert into categories (id, workspace_id, parent_id, name, sort_order)
  values ('00000000-0000-7000-8000-00000000c002',
          '00000000-0000-7000-8000-000000000001',
          '00000000-0000-7000-8000-00000000c001', 'Groceries', 0);
SQL
```

Expected: both inserts succeed. `parent_id` set → composite FK matched.

Reset and move on.

- [ ] **Step 5: Commit**

```bash
git add backend/db/migrations/20260424000003_classification.sql
git commit -m "feat(schema): add classification migration

Categories (hierarchical, leaf-only enforced in 004), category_history,
merchants, merchant_aliases, tags, categorization_rules. Defines the
assert_leaf_category() function used by transaction triggers."
```

---

## Task 5: `004_transactions.sql`

**Spec:** §5.4 (Transactions) + §3.11 (Triggers).

**Files:**
- Create: `backend/db/migrations/20260424000004_transactions.sql`

- [ ] **Step 1: Enums**

```sql
create type transaction_status as enum (
  'draft', 'posted', 'reconciled', 'voided'
);

create type match_provenance as enum (
  'auto_detected', 'manual', 'user_confirmed_auto'
);
```

- [ ] **Step 2: `transactions`**

Workspace-scoped skeleton. Columns per spec §5.4 — note `original_currency money_currency nullable`, `currency money_currency not null`. Composite FKs:

```sql
constraint transactions_account_fk   foreign key (workspace_id, account_id)   references accounts(workspace_id, id)   on delete cascade,
constraint transactions_category_fk  foreign key (workspace_id, category_id)  references categories(workspace_id, id) on delete set null,
constraint transactions_merchant_fk  foreign key (workspace_id, merchant_id)  references merchants(workspace_id, id)  on delete set null
```

Indexes from spec: `(workspace_id, booked_at desc)`, `(account_id, booked_at desc)`, `(category_id)`, `(merchant_id)`, `(status)`.

Currency-match trigger:

```sql
create or replace function assert_txn_currency_matches_account() returns trigger language plpgsql as $$
declare
  v_account_currency money_currency;
begin
  select currency into v_account_currency from accounts where id = new.account_id;
  if new.currency <> v_account_currency then
    raise exception 'transaction currency % does not match account currency %',
      new.currency, v_account_currency;
  end if;
  return new;
end;
$$;

create trigger transactions_currency_match
  before insert or update of currency, account_id on transactions
  for each row execute function assert_txn_currency_matches_account();
```

- [ ] **Step 3: `transaction_lines`, `transaction_tags`**

Per spec §5.4. Composite FKs to transactions, categories, merchants, tags. All amounts use `numeric(28,8)`; all currency columns use `money_currency`.

- [ ] **Step 4: `transfer_matches`, `refund_matches`**

Per spec §5.4. Composite FKs to the transaction pairs and to users for `matched_by_user_id`.

- [ ] **Step 5: `categorization_suggestions`** (relocated from classification)

```sql
create table categorization_suggestions (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  transaction_id      uuid not null,
  suggested_category_id uuid not null,
  confidence          numeric(5,4) not null,
  source              categorization_source not null,
  created_at          timestamptz not null default now(),
  accepted_at         timestamptz,
  dismissed_at        timestamptz,
  unique (workspace_id, id),
  constraint cs_txn_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint cs_cat_fk foreign key (workspace_id, suggested_category_id)
    references categories(workspace_id, id) on delete cascade
);
```

- [ ] **Step 6: Bind leaf-category triggers (using P5 from Task 4)**

```sql
create trigger transactions_leaf_category
  before insert or update of category_id on transactions
  for each row when (new.category_id is not null)
  execute function assert_leaf_category();

create trigger transaction_lines_leaf_category
  before insert or update of category_id on transaction_lines
  for each row execute function assert_leaf_category();
```

- [ ] **Step 7: Define and bind P6 (no-double-classification)**

Paste the full P6 function body, then the two `create trigger` calls.

- [ ] **Step 8: Run verification baseline**

Spot-check all three invariants with psql: (1) inserting a transaction with currency mismatching its account fails; (2) inserting a transaction with `category_id` set AND a line fails; (3) inserting a transaction with `category_id` pointing at a non-leaf category fails.

Seed script (reset first):

```bash
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
psql "$DATABASE_URL" <<'SQL'
insert into workspaces (id, name, base_currency, cycle_anchor_day)
  values ('00000000-0000-7000-8000-000000000001', 't1', 'CHF', 25);
insert into users (id, workspace_id, email, display_name)
  values ('00000000-0000-7000-8000-00000000000a',
          '00000000-0000-7000-8000-000000000001', 'a@example.com', 'A');
insert into accounts (id, workspace_id, name, kind, currency, open_date, opening_balance_date, include_in_savings_rate)
  values ('00000000-0000-7000-8000-0000000000ff',
          '00000000-0000-7000-8000-000000000001',
          'Checking', 'checking', 'CHF', '2026-01-01', '2026-01-01', true);
insert into categories (id, workspace_id, name, sort_order)
  values ('00000000-0000-7000-8000-00000000c001',
          '00000000-0000-7000-8000-000000000001', 'Food', 0);
insert into categories (id, workspace_id, parent_id, name, sort_order)
  values ('00000000-0000-7000-8000-00000000c002',
          '00000000-0000-7000-8000-000000000001',
          '00000000-0000-7000-8000-00000000c001', 'Groceries', 0);
SQL

# (1) currency mismatch
psql "$DATABASE_URL" -c "insert into transactions (id, workspace_id, account_id, status, booked_at, amount, currency)
  values ('00000000-0000-7000-8000-00000000t001',
          '00000000-0000-7000-8000-000000000001',
          '00000000-0000-7000-8000-0000000000ff',
          'posted', '2026-04-24', -10, 'USD');"
# Expected: ERROR: transaction currency USD does not match account currency CHF

# (2) parent category (non-leaf)
psql "$DATABASE_URL" -c "insert into transactions (id, workspace_id, account_id, status, booked_at, amount, currency, category_id)
  values ('00000000-0000-7000-8000-00000000t002',
          '00000000-0000-7000-8000-000000000001',
          '00000000-0000-7000-8000-0000000000ff',
          'posted', '2026-04-24', -10, 'CHF',
          '00000000-0000-7000-8000-00000000c001');"
# Expected: ERROR: category_id ... is not a leaf

# Valid insert
psql "$DATABASE_URL" -c "insert into transactions (id, workspace_id, account_id, status, booked_at, amount, currency, category_id)
  values ('00000000-0000-7000-8000-00000000t003',
          '00000000-0000-7000-8000-000000000001',
          '00000000-0000-7000-8000-0000000000ff',
          'posted', '2026-04-24', -10, 'CHF',
          '00000000-0000-7000-8000-00000000c002');"
# Expected: INSERT 0 1

# (3) double classification
psql "$DATABASE_URL" -c "insert into transaction_lines (id, workspace_id, transaction_id, amount, currency, category_id, sort_order)
  values ('00000000-0000-7000-8000-000000001ab0',
          '00000000-0000-7000-8000-000000000001',
          '00000000-0000-7000-8000-00000000t003',
          -10, 'CHF',
          '00000000-0000-7000-8000-00000000c002', 0);"
# Expected: ERROR: transaction ... has category_id; cannot add lines
```

All three expected errors must fire; the valid insert must succeed. Reset and continue.

- [ ] **Step 9: Commit**

```bash
git add backend/db/migrations/20260424000004_transactions.sql
git commit -m "feat(schema): add transactions migration

transactions, transaction_lines, transaction_tags, transfer_matches,
refund_matches, categorization_suggestions. Currency-match trigger,
leaf-category guard, no-double-classification guard."
```

---

## Task 6: `005_imports.sql`

**Spec:** §5.5 (Imports & providers).

**Files:**
- Create: `backend/db/migrations/20260424000005_imports.sql`

- [ ] **Step 1: Enums**

Per spec: `provider_connection_status`, `import_profile_kind`, `import_source_kind`, `import_status`. Full value lists are in the spec.

- [ ] **Step 2: Tables**

Create `provider_connections`, `provider_accounts`, `import_profiles`, `import_batches`, `source_refs` per spec §5.5.

- Composite FKs on `provider_accounts.account_id`, `import_batches.import_profile_id` and `import_batches.provider_connection_id` and `import_batches.created_by_user_id`, `source_refs.import_batch_id`.
- `source_refs` unique key: `unique (workspace_id, entity_type, provider, external_id) where provider is not null`. Note: Postgres unique indexes with predicates require the CREATE INDEX form, not a table-level `UNIQUE`:

  ```sql
  create unique index source_refs_dedupe
    on source_refs (workspace_id, entity_type, provider, external_id)
    where provider is not null;
  ```

- Index `(entity_type, entity_id)` on `source_refs` for reverse lookups.

- [ ] **Step 3: Verification baseline + spot-check**

Insert two workspaces, two provider_connections, same provider + external_id, same entity_type. Confirm the two rows coexist. Repeat within one workspace and confirm the second insert conflicts on `source_refs_dedupe`.

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260424000005_imports.sql
git commit -m "feat(schema): add imports and provider integration migration

provider_connections, provider_accounts, import_profiles, import_batches,
source_refs (polymorphic dedupe, workspace-scoped unique key)."
```

---

## Task 7: `006_planning.sql`

**Spec:** §5.6 (Planning).

**Files:**
- Create: `backend/db/migrations/20260424000006_planning.sql`

- [ ] **Step 1: Enums**

Per spec: `cycle_anchor_kind`, `cycle_status`, `recurring_template_kind`, `recurring_amount_type`, `cycle_plan_status`, `cycle_plan_line_kind`, `planned_event_status`, `action_item_status`, `rollover_behavior`, `overspend_behavior`, `income_amount_type`, `tax_hint`.

- [ ] **Step 2: Tables**

`payment_cycles`, `income_sources`, `recurring_templates`, `cycle_plans`, `cycle_plan_lines`, `planned_events`, `action_items`, `rollover_policies`, `planned_event_matches` (relocated here because it needs both `planned_events` from this file and `transactions` from 004).

Composite FKs on every cross-table reference (accounts, categories, merchants, transactions, users, goals in 007, trips in 010 — goals and trips come later; defer those FKs via `alter table ... add constraint` in Tasks 8 and 11).

Execute check constraint on `planned_events`:

```sql
alter table planned_events add constraint planned_events_executed_chk
  check ((status <> 'executed') or (executed_transaction_id is not null));
```

- [ ] **Step 3: Deferred FKs noted**

`cycle_plan_lines.goal_id` and `cycle_plan_lines.trip_id` have no targets yet. Two options:

- Option A (preferred): Leave the column uncommented and add the composite FK from the later migration (Task 8 for goals, Task 11 for trips) via `alter table cycle_plan_lines add constraint ...`.
- Option B: Use a check/trigger; don't use this.

Go with Option A. In Step 2, do not add FK constraints for `goal_id` or `trip_id`; record the deferred constraints in a comment.

- [ ] **Step 4: Verification baseline**

Reset, apply, confirm no errors. Spot-check: `insert into planned_events (..., status='executed', executed_transaction_id=null)` fails the check constraint.

- [ ] **Step 5: Commit**

```bash
git add backend/db/migrations/20260424000006_planning.sql
git commit -m "feat(schema): add planning migration

Payment cycles, income sources, recurring templates, cycle plans and
lines, planned events, action items, rollover policies,
planned_event_matches. goal_id / trip_id FKs deferred to 007 / 010."
```

---

## Task 8: `007_goals.sql`

**Spec:** §5.7 (Goals, buckets, savings rules).

**Files:**
- Create: `backend/db/migrations/20260424000007_goals.sql`

- [ ] **Step 1: Enums**

Per spec: `goal_status`, `goal_type`, `goal_allocation_source`, `savings_rule_trigger`, `savings_rule_action`.

- [ ] **Step 2: Tables**

`goals`, `goal_accounts`, `goal_allocations` (append-only; no trigger), `goal_contributions`, `savings_rules`.

Self-FKs on `goals`: `parent_goal_id` and `auto_redirect_on_reach_goal_id` both use composite FK per P4.

Composite FKs to `accounts` on `goal_accounts.account_id` and `goal_allocations.account_id`.

Composite FKs on `goal_contributions.transaction_id` (to transactions) and `goal_contributions.planned_event_id` (to planned_events).

- [ ] **Step 3: Back-fill deferred FK from Task 7**

```sql
alter table cycle_plan_lines add constraint cpl_goal_fk
  foreign key (workspace_id, goal_id) references goals(workspace_id, id)
  on delete set null;
```

- [ ] **Step 4: Verification baseline + spot-check**

Reset, apply. Insert `(goal, account)` pair. Insert a `goal_contribution` referencing a valid transaction.

- [ ] **Step 5: Commit**

```bash
git add backend/db/migrations/20260424000007_goals.sql
git commit -m "feat(schema): add goals, buckets, savings rules

goals (hierarchical + auto-redirect), goal_accounts, goal_allocations,
goal_contributions, savings_rules. Back-fills cycle_plan_lines.goal_id
FK."
```

---

## Task 9: `008_investments.sql`

**Spec:** §5.8 (Investments).

**Files:**
- Create: `backend/db/migrations/20260424000008_investments.sql`

- [ ] **Step 1: Enums**

Per spec: `asset_class`, `trade_side`, `cost_basis_method`, `corporate_action_kind`, `price_source`.

- [ ] **Step 2: Global tables (no `workspace_id`)**

`instruments` and `instrument_prices`. Plain `id uuid primary key`, no workspace_id, no composite unique. Indexes per spec.

Instruments:

```sql
create unique index instruments_isin_uq on instruments(isin) where isin is not null;
create unique index instruments_symbol_exchange_uq
  on instruments(symbol, exchange) where isin is null;
```

- [ ] **Step 3: Workspace-scoped tables**

`investment_accounts` (extends `accounts`; `account_id uuid primary key references accounts(id) on delete cascade` — this is the one exception to the "every PK is v7 uuid" rule since it's a 1:1 extension), `investment_trades`, `investment_lots`, `investment_lot_consumptions`, `dividend_events`, `corporate_actions`, `investment_positions` (composite PK `(account_id, instrument_id)`; cache), `allocation_buckets`, `position_bucket_allocations`, `target_allocations`.

`investment_accounts.workspace_id` sits alongside `account_id` for workspace-scoped queries; composite FK `(workspace_id, account_id)` to `accounts(workspace_id, id)`.

Self-FK on `allocation_buckets.parent_bucket_id` uses composite P4.

- [ ] **Step 4: Verification baseline**

Insert an instrument (global), an investment_account (tied to a brokerage account), a trade, and a lot. Confirm a cross-workspace insert of investment_trades against another workspace's investment_account fails.

- [ ] **Step 5: Commit**

```bash
git add backend/db/migrations/20260424000008_investments.sql
git commit -m "feat(schema): add investments migration

Instruments and instrument_prices (global), investment_accounts,
trades, lots, lot_consumptions, dividends, corporate_actions,
materialized investment_positions, allocation buckets and targets."
```

---

## Task 10: `009_assets.sql`

**Spec:** §5.9 (Physical assets & retirement).

**Files:**
- Create: `backend/db/migrations/20260424000009_assets.sql`

- [ ] **Step 1: Enums**

Per spec: `asset_category`, `asset_valuation_method`, `asset_valuation_source`, `asset_event_kind`, `depreciation_method`, `retirement_pillar`, `mortgage_payment_status`.

- [ ] **Step 2: Tables**

`assets` (1:1 with asset-kind accounts — `account_id uuid not null unique`, composite FK), `asset_valuations`, `asset_events`, `asset_depreciation_schedules` (1:1 with assets), `retirement_contribution_limits` (reference data, no workspace_id), `mortgage_schedules` (1:1 with mortgage-kind accounts), `mortgage_payments`.

`mortgage_schedules.account_id` is unique and references `accounts(workspace_id, id)` via composite FK.

`asset_events.linked_transaction_id` uses composite FK to `transactions(workspace_id, id)` (`on delete set null`).

- [ ] **Step 3: Verification baseline**

Insert an asset-kind account, then an asset row for it, then a valuation. Confirm a second asset row for the same account fails (UNIQUE).

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260424000009_assets.sql
git commit -m "feat(schema): add physical assets and retirement migration

Assets (1:1 with asset-kind accounts), valuations, events,
depreciation schedules, retirement contribution limits (reference
data), mortgage schedules and payments."
```

---

## Task 11: `010_travel_splits.sql`

**Spec:** §5.10 (Travel, split bills, receivables).

**Files:**
- Create: `backend/db/migrations/20260424000010_travel_splits.sql`

- [ ] **Step 1: Enums**

Per spec: `trip_status`, `trip_category`, `trip_participant_share_default`, `split_allocation_method`, `split_bill_state`, `receivable_direction`, `receivable_origin`, `receivable_status`, `reimbursement_claim_status`.

- [ ] **Step 2: Tables**

`people`, `trips`, `trip_budgets`, `trip_participants`, `trip_transaction_links`, `split_bill_events`, `split_bill_allocations`, `receivables`, `settlements`, `reimbursement_claims`.

Check constraints:

```sql
alter table trip_participants add constraint tp_self_or_person_chk
  check ((person_id is not null) <> is_self);

alter table split_bill_allocations add constraint sba_participant_or_person_chk
  check ((participant_trip_id is not null) <> (person_id is not null));
```

Composite FK back-fill for `cycle_plan_lines.trip_id`:

```sql
alter table cycle_plan_lines add constraint cpl_trip_fk
  foreign key (workspace_id, trip_id) references trips(workspace_id, id)
  on delete set null;
```

- [ ] **Step 3: Verification baseline + spot-checks**

Seed a trip, a person, a trip_participant with `is_self=true AND person_id=null`, a second trip_participant with `is_self=false AND person_id=<uuid>`. Confirm both insert. Attempt `is_self=true AND person_id=<uuid>` — must fail the check constraint.

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260424000010_travel_splits.sql
git commit -m "feat(schema): add travel, split bills, receivables

Trips, participants, budgets, transaction links; split_bill_events and
allocations; receivables, settlements, reimbursement_claims. Back-fills
cycle_plan_lines.trip_id FK."
```

---

## Task 12: `011_wishlist.sql`

**Spec:** §5.11 (Wishlist).

**Files:**
- Create: `backend/db/migrations/20260424000011_wishlist.sql`

- [ ] **Step 1: Enums**

`wishlist_status`, `wishlist_price_source`.

- [ ] **Step 2: Tables**

`wishlist_items`, `wishlist_price_observations` (append-only; no updated_at), `wishlist_purchase_links`.

Composite FKs: `wishlist_items.linked_goal_id` → `goals(workspace_id, id)`, `wishlist_purchase_links.transaction_id` → `transactions(workspace_id, id)`, `wishlist_purchase_links.wishlist_item_id` unique + composite FK to `wishlist_items(workspace_id, id)` cascade.

Optional trigger: on insert into `wishlist_purchase_links`, set `wishlist_items.status = 'bought'`.

```sql
create or replace function wishlist_mark_bought() returns trigger language plpgsql as $$
begin
  update wishlist_items set status = 'bought', updated_at = now()
  where id = new.wishlist_item_id;
  return new;
end;
$$;

create trigger wpl_mark_bought after insert on wishlist_purchase_links
  for each row execute function wishlist_mark_bought();
```

- [ ] **Step 3: Verification baseline**

Seed a wishlist_item, a purchase link. Confirm the item's `status` flips to `bought`.

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260424000011_wishlist.sql
git commit -m "feat(schema): add wishlist migration

wishlist_items, price_observations (price tracking), purchase_links
with trigger that flips wishlist_items.status to 'bought' on purchase."
```

---

## Task 13: `012_attachments_audit.sql`

**Spec:** §5.12 (Attachments, documents, audit).

**Files:**
- Create: `backend/db/migrations/20260424000012_attachments_audit.sql`

- [ ] **Step 1: Enums**

`attachment_storage`, `ocr_status`, `audit_action`.

- [ ] **Step 2: Tables**

`attachments` (unique `(workspace_id, sha256)` for dedupe), `attachment_links` (unique `(attachment_id, entity_type, entity_id)`), `ocr_documents`, `saved_searches`, `audit_events`.

Composite FKs for user references (`attachments.uploaded_by_user_id`, `attachment_links.linked_by_user_id`, `saved_searches.user_id`, `audit_events.actor_user_id`). Indexes:

```sql
create index audit_events_entity_idx
  on audit_events(workspace_id, entity_type, entity_id, occurred_at desc);
```

- [ ] **Step 3: Define P7 (`record_audit_event` function)**

Paste the full P7 body. Then bind to every audited table. Exact list:

```sql
create trigger transactions_audit           after insert or update or delete on transactions           for each row execute function record_audit_event('transaction');
create trigger transaction_lines_audit      after insert or update or delete on transaction_lines      for each row execute function record_audit_event('transaction_line');
create trigger accounts_audit               after insert or update or delete on accounts              for each row execute function record_audit_event('account');
create trigger categories_audit             after insert or update or delete on categories            for each row execute function record_audit_event('category');
create trigger merchants_audit              after insert or update or delete on merchants             for each row execute function record_audit_event('merchant');
create trigger tags_audit                   after insert or update or delete on tags                  for each row execute function record_audit_event('tag');
create trigger categorization_rules_audit   after insert or update or delete on categorization_rules  for each row execute function record_audit_event('categorization_rule');
create trigger recurring_templates_audit    after insert or update or delete on recurring_templates   for each row execute function record_audit_event('recurring_template');
create trigger goals_audit                  after insert or update or delete on goals                 for each row execute function record_audit_event('goal');
create trigger savings_rules_audit          after insert or update or delete on savings_rules         for each row execute function record_audit_event('savings_rule');
create trigger receivables_audit            after insert or update or delete on receivables           for each row execute function record_audit_event('receivable');
create trigger reimbursement_claims_audit   after insert or update or delete on reimbursement_claims  for each row execute function record_audit_event('reimbursement_claim');
create trigger wishlist_items_audit         after insert or update or delete on wishlist_items        for each row execute function record_audit_event('wishlist_item');
create trigger assets_audit                 after insert or update or delete on assets                for each row execute function record_audit_event('asset');
create trigger mortgage_schedules_audit     after insert or update or delete on mortgage_schedules    for each row execute function record_audit_event('mortgage_schedule');
```

- [ ] **Step 4: Prevent direct DELETE/UPDATE on `audit_events`**

Append-only:

```sql
create or replace function reject_audit_mutation() returns trigger language plpgsql as $$
begin
  raise exception 'audit_events is append-only';
end;
$$;

create trigger audit_events_no_update before update on audit_events
  for each row execute function reject_audit_mutation();
create trigger audit_events_no_delete before delete on audit_events
  for each row execute function reject_audit_mutation();
```

- [ ] **Step 5: Verification baseline + spot-check**

Seed one workspace/user. Insert a category; confirm `audit_events` has one `created` row for `entity_type='category'` with `actor_user_id = null` (no `folio.actor_user_id` set). Now:

```bash
psql "$DATABASE_URL" <<'SQL'
set local folio.actor_user_id = '00000000-0000-7000-8000-00000000000a';
update categories set name = 'Food & drink' where name = 'Food';
SQL
```

Confirm the resulting audit row's `actor_user_id` matches. Confirm `delete from audit_events where true` fails with `audit_events is append-only`.

- [ ] **Step 6: Commit**

```bash
git add backend/db/migrations/20260424000012_attachments_audit.sql
git commit -m "feat(schema): add attachments, documents, audit migration

Attachments (sha256 dedupe), attachment_links, ocr_documents,
saved_searches, audit_events + record_audit_event trigger bound to all
audited tables. audit_events enforced append-only."
```

---

## Task 14: `013_fx_reports.sql`

**Spec:** §5.13 (FX & reporting).

**Files:**
- Create: `backend/db/migrations/20260424000013_fx_reports.sql`

- [ ] **Step 1: Enums**

`fx_source`, `event_marker_kind`, `report_export_kind`, `report_export_status`.

- [ ] **Step 2: Tables**

`fx_rates` (global, no workspace_id), `networth_snapshots`, `event_markers`, `report_exports`, `export_templates`.

Composite FKs for user references (`report_exports.requested_by_user_id`). `report_exports.file_attachment_id` FK to `attachments` (single-column is OK here because attachments is workspace-scoped and the composite `(workspace_id, file_attachment_id)` is also needed — use composite).

- [ ] **Step 3: Verification baseline + spot-check**

Seed two FX rates (`(CHF,EUR,2026-04-24)`, `(CHF,USD,2026-04-24)`). Confirm inserting a duplicate on the same `(base_currency, quote_currency, as_of, source)` fails.

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260424000013_fx_reports.sql
git commit -m "feat(schema): add FX and reporting migration

fx_rates (global), networth_snapshots, event_markers, report_exports,
export_templates."
```

---

## Task 15: `014_notifications.sql`

**Spec:** §5.14 (Notifications).

**Files:**
- Create: `backend/db/migrations/20260424000014_notifications.sql`

- [ ] **Step 1: Enums**

`notification_channel`, `notification_digest_mode`, `notification_delivery_status`.

- [ ] **Step 2: Tables**

`notification_rules`, `notification_events`, `notification_deliveries`, `notification_preferences`, `webhook_destinations`.

Composite FKs for user references (`notification_preferences.user_id`). Unique keys per spec.

- [ ] **Step 3: Verification baseline + full rerun**

Reset and apply. Confirm the full 14-file migration chain runs clean:

```bash
cd /Users/xmedavid/dev/folio/backend
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
```

Expected: ends with `Migrated to version 20260424000014_notifications`.

`\dt` in psql should list every table from the spec (count them — ~70 including global ones).

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/20260424000014_notifications.sql
git commit -m "feat(schema): add notifications migration

notification_rules, events, deliveries, preferences, webhook_destinations.
Completes the v2 schema (14 migrations, ~70 tables)."
```

---

## Task 16: Rewrite `docs/domain.md`

**Files:**
- Modify: `docs/domain.md` (full rewrite)

The current file describes the scaffold model. Replace with a ≤2-page overview of v2 pointing at the spec for details.

- [ ] **Step 1: Replace the file's contents**

Write the new `docs/domain.md`:

```markdown
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
- Every amount carries a currency (`varchar(10)`, domain
  `money_currency`, ISO 4217 plus crypto tickers).
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
```

- [ ] **Step 2: Commit**

```bash
cd /Users/xmedavid/dev/folio
git add docs/domain.md
git commit -m "docs: rewrite domain model overview for v2 schema

Replaces the scaffold-era description with a high-level overview of
the v2 model. Spec at docs/superpowers/specs/2026-04-24-folio-domain-v2-design.md
remains the authoritative column-level reference."
```

---

## Task 17: Final verification

**Files:** none changed.

- [ ] **Step 1: Full reset + apply from scratch**

```bash
cd /Users/xmedavid/dev/folio/backend
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
```

Expected: 14 migrations apply cleanly. Output ends at `Migrated to version 20260424000014_notifications`.

- [ ] **Step 2: Confirm `sqlc generate` parses the schema**

```bash
sqlc generate
```

Expected: exit 0. No files produced in `internal/db/dbq/` because there are no queries (empty `db/queries/` directory), but sqlc reads `db/migrations/` to validate DDL.

- [ ] **Step 3: Confirm backend compiles**

```bash
go build ./...
```

Expected: exit 0.

- [ ] **Step 4: Spot-check table count**

```bash
psql "$DATABASE_URL" -tAc "
  select count(*) from information_schema.tables
  where table_schema = 'public' and table_type = 'BASE TABLE';
"
```

Expected: ~70 (exact count depends on how many "extends via 1:1" tables you created; anywhere from 68–72 is in range).

- [ ] **Step 5: Spot-check the composite-FK discipline**

```bash
psql "$DATABASE_URL" -tAc "
  -- Count composite FKs — should be large (> 50)
  select count(*)
    from pg_constraint c
    where contype = 'f' and cardinality(c.conkey) >= 2;
"
```

Expected: high count (≥ 50). This confirms composite FKs exist in bulk.

Spot-check one specific composite FK:

```bash
psql "$DATABASE_URL" -c "
  select conname, pg_get_constraintdef(oid)
  from pg_constraint
  where conname = 'transactions_account_fk';
"
```

Expected output includes `FOREIGN KEY (workspace_id, account_id) REFERENCES accounts(workspace_id, id)`.

- [ ] **Step 6: Spot-check triggers**

```bash
psql "$DATABASE_URL" -tAc "
  select trigger_name
  from information_schema.triggers
  where trigger_schema = 'public'
  order by trigger_name;
" | head -40
```

Expected: includes `transactions_audit`, `transactions_currency_match`, `transactions_leaf_category`, `transactions_no_double_class`, `transactions_updated_at`, the audit triggers on all audited tables, and trigger bindings for every `updated_at` column.

- [ ] **Step 7: No residual scaffold artifacts**

```bash
cd /Users/xmedavid/dev/folio
test ! -f backend/db/migrations/20260424000000_init.sql && echo "scaffold removed"
rg 'account_source.*csv_import|source account_source' backend/ docs/ 2>/dev/null || echo "no scaffold enum refs"
```

Expected: both echo their confirmation messages.

- [ ] **Step 8: Confirm no uncommitted changes**

```bash
cd /Users/xmedavid/dev/folio
git status
```

Expected: clean working tree (all migration files and doc rewrites committed in their respective tasks).

- [ ] **Step 9: Final commit (if any stray changes remain)**

If step 8 shows unexpected modified files, investigate and commit — this should be a no-op if every prior task committed its own work.

---

## Self-review checklist (run after writing all migrations, before declaring done)

- [ ] Every spec section §5.1–§5.14 has a corresponding migration task (Tasks 2–15).
- [ ] `docs/domain.md` rewritten (Task 16).
- [ ] No `pg_uuidv7` or `gen_random_uuid()` anywhere in the migrations — all IDs supplied by caller.
- [ ] Every `currency` column uses the `money_currency` domain.
- [ ] Every workspace-scoped table has `UNIQUE (workspace_id, id)`.
- [ ] Every cross-table FK to a workspace-scoped parent is composite.
- [ ] Self-FKs (`categories.parent_id`, `goals.parent_goal_id`, `goals.auto_redirect_on_reach_goal_id`, `allocation_buckets.parent_bucket_id`) are composite.
- [ ] User FKs on workspace-scoped tables are composite (audit_events, saved_searches, report_exports, attachments, attachment_links, notification_preferences, transfer/refund_matches.matched_by_user_id, import_batches.created_by_user_id, category_history.actor_user_id).
- [ ] `source_refs` unique index includes `workspace_id`.
- [ ] `categorization_suggestions` declared exactly once (in Task 5).
- [ ] `planned_event_matches` declared exactly once (in Task 7).
- [ ] No XOR on `(category_id, transaction_lines)` — only the "no double classification" trigger.
- [ ] Audit triggers bound to every audited table listed in §3.11 of the spec.
- [ ] `audit_events` is append-only (reject_audit_mutation trigger).
- [ ] Final `atlas migrate apply` on a fresh DB succeeds with zero extensions installed.
