-- Folio v2 domain — physical assets and retirement (spec §5.9).
-- Assets are a 1:1 extension of asset-kind accounts (workspace-scoped).
-- Valuations and events are workspace-scoped and cascade from the asset.
-- Depreciation schedules are 1:1 with assets. Retirement contribution
-- limits are GLOBAL reference data (no workspace_id). Mortgage schedules
-- are 1:1 extensions of mortgage-kind accounts, with a payment stream
-- that can link to the materialized transaction when a payment lands.

-- Asset category taxonomy (spec §5.9). `pension` covers retirement
-- wrappers held as assets (e.g. Swiss pillar 3a wrapper account);
-- `other` is the escape hatch for idiosyncratic holdings.
create type asset_category as enum (
  'car', 'property', 'art', 'watch', 'collectible',
  'crypto_physical', 'pension', 'other'
);

-- Valuation method pluggability. `manual` is the baseline; `plugin_*`
-- variants point to external pricers (Eurotax for cars, Zestimate for
-- property, crypto oracle for physically-held crypto wallets).
create type asset_valuation_method as enum (
  'manual', 'plugin_eurotax', 'plugin_zestimate', 'plugin_crypto_oracle'
);

-- Provenance for each valuation row. `purchase`/`sale` anchor the
-- endpoints; `appraisal` records a professional valuation; `plugin`
-- stamps automated external pricings; `manual` is user-entered.
create type asset_valuation_source as enum (
  'manual', 'plugin', 'appraisal', 'purchase', 'sale'
);

-- Lifecycle events. `depreciation` records periodic book-value writedowns
-- (from the depreciation schedule); `improvement` is a capitalisable
-- upgrade; `transfer_of_ownership` handles gifts/inheritance etc.
create type asset_event_kind as enum (
  'purchase', 'sale', 'maintenance', 'depreciation',
  'appraisal', 'improvement', 'transfer_of_ownership'
);

-- Depreciation methods. `manual` leaves schedule_jsonb authoritative so
-- the user can encode arbitrary curves without us modelling each variant.
create type depreciation_method as enum ('straight_line', 'declining_balance', 'manual');

-- Retirement pillar taxonomy. Starts with Swiss pillars (3a/2) as the
-- primary supported jurisdiction; `other` is the escape hatch for
-- foreign equivalents until they graduate into first-class enums.
create type retirement_pillar as enum ('ch_pillar_3a', 'ch_pillar_2', 'other');

-- Mortgage payment lifecycle. `upcoming` is future-dated; `due` is
-- past-dated but not yet paid; `paid` requires paid_at; `skipped` is
-- an explicit non-payment (forbearance, error, payoff overlap).
create type mortgage_payment_status as enum ('upcoming', 'due', 'paid', 'skipped');

-- Assets: 1:1 extension of asset-kind accounts. The account carries the
-- ledger-facing balance stream; the asset carries the physical-object
-- metadata (acquired_cost/date, disposal triple, valuation method).
-- Disposal fields move together to prevent partial disposal records.
create table assets (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  account_id          uuid not null,
  category            asset_category not null,
  description         text not null,
  acquired_date       date not null,
  acquired_cost       numeric(28,8) not null,
  currency            money_currency not null,
  disposal_date       date,
  disposal_amount     numeric(28,8),
  disposal_currency   money_currency,
  valuation_method    asset_valuation_method not null default 'manual',
  notes               text,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, account_id),     -- 1:1 with its asset-kind account
  constraint assets_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade,
  check (acquired_cost >= 0),
  -- disposal amount/currency/date move together
  constraint assets_disposal_pair_chk check (
    (disposal_date is null and disposal_amount is null and disposal_currency is null) or
    (disposal_date is not null and disposal_amount is not null and disposal_currency is not null)
  ),
  constraint assets_disposal_order_chk check (
    disposal_date is null or disposal_date >= acquired_date
  ),
  check (disposal_amount is null or disposal_amount >= 0)
);

create trigger assets_updated_at before update on assets
  for each row execute function set_updated_at();

create index assets_account_idx on assets(account_id);
create index assets_workspace_category_idx on assets(workspace_id, category);

-- Asset valuations: time-series of values over the life of the asset.
-- Rows with source='manual' are user-entered; 'plugin' rows come from
-- automated pricers. `as_of` is the business-date the value applies to;
-- query "latest valuation" via ORDER BY as_of DESC LIMIT 1.
create table asset_valuations (
  id          uuid primary key,
  workspace_id   uuid not null references workspaces(id) on delete cascade,
  asset_id    uuid not null,
  as_of       date not null,
  value       numeric(28,8) not null,
  currency    money_currency not null,
  source      asset_valuation_source not null,
  note        text,
  created_at  timestamptz not null default now(),
  unique (workspace_id, id),
  constraint av_asset_fk foreign key (workspace_id, asset_id)
    references assets(workspace_id, id) on delete cascade,
  check (value >= 0)
);

create index asset_valuations_asset_asof_idx on asset_valuations(asset_id, as_of desc);

-- Asset events: lifecycle log. Amount+currency move together (biconditional
-- via NULL-equality). `linked_transaction_id` wires events to the ledger
-- when they materialize (e.g. a maintenance receipt creates an expense
-- transaction); on-delete-set-null preserves the event if the txn is purged.
create table asset_events (
  id                    uuid primary key,
  workspace_id             uuid not null references workspaces(id) on delete cascade,
  asset_id              uuid not null,
  kind                  asset_event_kind not null,
  occurred_at           date not null,
  amount                numeric(28,8),
  currency              money_currency,
  linked_transaction_id uuid,
  note                  text,
  created_at            timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ae_asset_fk foreign key (workspace_id, asset_id)
    references assets(workspace_id, id) on delete cascade,
  constraint ae_transaction_fk foreign key (workspace_id, linked_transaction_id)
    references transactions(workspace_id, id) on delete set null,
  -- amount and currency move together
  constraint ae_amount_pair_chk check (
    (amount is null) = (currency is null)
  )
);

create index asset_events_asset_occurred_idx on asset_events(asset_id, occurred_at desc);
create index asset_events_transaction_idx on asset_events(linked_transaction_id) where linked_transaction_id is not null;

-- Depreciation schedule: 1:1 with an asset. `schedule_jsonb` is the
-- materialized per-period plan (for 'manual' method or pre-computed
-- straight-line / declining-balance runs); `rate` is 0..1 for declining
-- balance. `salvage_value` is the residual book value floor.
create table asset_depreciation_schedules (
  id              uuid primary key,
  workspace_id       uuid not null references workspaces(id) on delete cascade,
  asset_id        uuid not null unique,
  method          depreciation_method not null,
  start_date      date not null,
  end_date        date,
  salvage_value   numeric(28,8),
  rate            numeric(7,4),
  schedule_jsonb  jsonb,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ads_asset_fk foreign key (workspace_id, asset_id)
    references assets(workspace_id, id) on delete cascade,
  check (end_date is null or end_date >= start_date),
  check (salvage_value is null or salvage_value >= 0),
  check (rate is null or (rate >= 0 and rate <= 1))
);

create trigger asset_depreciation_schedules_updated_at
  before update on asset_depreciation_schedules
  for each row execute function set_updated_at();

-- Retirement contribution limits: GLOBAL reference data. No workspace_id —
-- every workspace sees the same (country, year, pillar, amount) row. This
-- table is maintained by platform ops, not per-workspace.
create table retirement_contribution_limits (
  id         uuid primary key,
  country    char(2) not null,
  year       int not null check (year between 1900 and 2200),
  pillar     retirement_pillar not null,
  amount     numeric(28,8) not null,
  currency   money_currency not null,
  created_at timestamptz not null default now(),
  unique (country, year, pillar),
  check (amount >= 0)
);

create index retirement_contribution_limits_country_year_idx
  on retirement_contribution_limits(country, year);

-- Mortgage schedules: 1:1 extension of mortgage-kind accounts. Carries
-- the amortization parameters (original_principal, interest_rate, term).
-- The payment stream (below) materializes the per-period breakdown.
create table mortgage_schedules (
  id                 uuid primary key,
  workspace_id          uuid not null references workspaces(id) on delete cascade,
  account_id         uuid not null,
  original_principal numeric(28,8) not null,
  currency           money_currency not null,
  interest_rate      numeric(7,4) not null,
  term_months        int not null,
  start_date         date not null,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, account_id),
  constraint ms_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade,
  check (original_principal > 0),
  check (interest_rate >= 0 and interest_rate <= 1),
  check (term_months > 0)
);

create trigger mortgage_schedules_updated_at before update on mortgage_schedules
  for each row execute function set_updated_at();

-- Mortgage payments: the per-period amortization stream. `due_on` is the
-- scheduled date; one row per (schedule, due_on). When the payment
-- materializes in the ledger, `linked_transaction_id` wires it up and
-- status transitions to 'paid' (biconditional with paid_at non-null).
create table mortgage_payments (
  id                     uuid primary key,
  workspace_id              uuid not null references workspaces(id) on delete cascade,
  mortgage_schedule_id   uuid not null,
  due_on                 date not null,
  principal_amount       numeric(28,8) not null,
  interest_amount        numeric(28,8) not null,
  currency               money_currency not null,
  linked_transaction_id  uuid,
  status                 mortgage_payment_status not null default 'upcoming',
  paid_at                timestamptz,
  created_at             timestamptz not null default now(),
  updated_at             timestamptz not null default now(),
  unique (workspace_id, id),
  unique (mortgage_schedule_id, due_on),
  constraint mp_schedule_fk foreign key (workspace_id, mortgage_schedule_id)
    references mortgage_schedules(workspace_id, id) on delete cascade,
  constraint mp_transaction_fk foreign key (workspace_id, linked_transaction_id)
    references transactions(workspace_id, id) on delete set null,
  check (principal_amount >= 0),
  check (interest_amount >= 0),
  -- paid status requires paid_at; other statuses forbid it
  constraint mp_paid_chk check (
    (status = 'paid') = (paid_at is not null)
  )
);

create trigger mortgage_payments_updated_at before update on mortgage_payments
  for each row execute function set_updated_at();

create index mortgage_payments_schedule_due_idx on mortgage_payments(mortgage_schedule_id, due_on);
create index mortgage_payments_status_idx on mortgage_payments(workspace_id, status) where status in ('upcoming', 'due');
create index mortgage_payments_transaction_idx on mortgage_payments(linked_transaction_id) where linked_transaction_id is not null;
