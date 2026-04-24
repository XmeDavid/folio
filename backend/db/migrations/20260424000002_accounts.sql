-- Folio v2 domain — accounts and balance snapshots.
-- Account balance is derived from the latest snapshot + post-snapshot
-- transactions; there is no cached balance column on accounts.

-- Account classification (spec §5.2). Enumerates the asset/liability kinds
-- Folio supports; 'pillar_2' and 'pillar_3a' are Swiss retirement vehicles.
create type account_kind as enum (
  'checking', 'savings', 'cash', 'credit_card',
  'brokerage', 'crypto_wallet', 'loan', 'mortgage',
  'asset', 'pillar_2', 'pillar_3a', 'other'
);

-- Provenance of a balance snapshot (spec §5.2). 'opening' is the seed row;
-- 'recompute' is the engine's periodic re-materialization.
create type balance_snapshot_source as enum (
  'opening', 'bank_sync', 'manual_checkpoint',
  'valuation', 'import', 'recompute'
);

-- Reconciliation lifecycle (spec §5.2). 'drift' records a variance against
-- the user-asserted statement balance.
create type reconciliation_status as enum (
  'open', 'balanced', 'drift'
);

-- Accounts: tenant-scoped ledger containers. No cached balance column —
-- balance is derived from the latest snapshot + post-snapshot transactions.
create table accounts (
  id                        uuid primary key,
  tenant_id                 uuid not null references tenants(id) on delete cascade,
  name                      text not null,
  nickname                  text,
  kind                      account_kind not null,
  currency                  money_currency not null,
  institution               text,
  open_date                 date not null,
  close_date                date,
  opening_balance           numeric(28,8) not null default 0,
  opening_balance_date      date not null,
  include_in_networth       bool not null default true,
  include_in_savings_rate   bool not null,
  archived_at               timestamptz,
  created_at                timestamptz not null default now(),
  updated_at                timestamptz not null default now(),
  unique (tenant_id, id)           -- composite-FK target
);

create trigger accounts_updated_at before update on accounts
  for each row execute function set_updated_at();

-- Active-accounts index (most queries filter on non-archived).
create index accounts_tenant_active_idx on accounts(tenant_id) where archived_at is null;

-- Balance snapshots: append-only fact table. No updated_at, no trigger.
-- Composite FK to accounts enforces tenant consistency.
create table account_balance_snapshots (
  id          uuid primary key,
  tenant_id   uuid not null references tenants(id) on delete cascade,
  account_id  uuid not null,
  as_of       timestamptz not null,
  balance     numeric(28,8) not null,
  currency    money_currency not null,
  source      balance_snapshot_source not null,
  note        text,
  created_at  timestamptz not null default now(),
  unique (tenant_id, id),
  unique (account_id, as_of, source),
  constraint abs_account_fk foreign key (tenant_id, account_id)
    references accounts(tenant_id, id) on delete cascade
);

-- Timeline lookup: latest snapshots per account.
create index abs_account_timeline_idx
  on account_balance_snapshots(account_id, as_of desc);

-- Reconciliation checkpoints: user-asserted statement balance vs. derived
-- balance at a given date. Drift rows record the variance for resolution.
-- `asserted_currency` mirrors the account currency but is stored for
-- immutability (historical statements stay valid if the account currency
-- is ever reclassified).
create table reconciliation_checkpoints (
  id                  uuid primary key,
  tenant_id           uuid not null references tenants(id) on delete cascade,
  account_id          uuid not null,
  statement_date      date not null,
  asserted_balance    numeric(28,8) not null,
  asserted_currency   money_currency not null,
  status              reconciliation_status not null default 'open',
  drift_amount        numeric(28,8),
  drift_currency      money_currency,
  resolved_at         timestamptz,
  notes               text,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  unique (tenant_id, id),
  constraint rc_account_fk foreign key (tenant_id, account_id)
    references accounts(tenant_id, id) on delete cascade
);

create trigger reconciliation_checkpoints_updated_at before update on reconciliation_checkpoints
  for each row execute function set_updated_at();

-- FK-side index for cascades and per-account statement lookups.
create index rc_account_idx on reconciliation_checkpoints(account_id, statement_date desc);
