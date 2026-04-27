-- Folio v2 domain — investments.
-- Instruments and instrument_prices are GLOBAL (no workspace_id); deleting a
-- workspace never cascades into shared market-data rows. Investment accounts
-- are a 1:1 extension of accounts (workspace-scoped). Trades, lots, lot
-- consumptions, dividends, corporate actions, materialized positions, and
-- allocation bucketing live here.

-- Asset classification (spec §5.8). `cash_equivalent` is for money-market
-- funds and similar instruments that track cash but trade like securities.
create type asset_class as enum (
  'equity', 'etf', 'bond', 'fund', 'reit', 'option',
  'future', 'crypto', 'commodity', 'cash_equivalent'
);

-- Trade direction. Shorts/covers are modelled as sell/buy of a
-- short-position instrument; the DB only cares about the signed side.
create type trade_side as enum ('buy', 'sell');

-- Cost-basis accounting method per account (spec §5.8). `specific_lot`
-- lets the user pick which lots to consume per sell trade.
create type cost_basis_method as enum ('fifo', 'lifo', 'average', 'specific_lot');

-- Corporate-action taxonomy (spec §5.8). Action-specific parameters live
-- in `corporate_actions.payload` jsonb.
create type corporate_action_kind as enum (
  'split', 'reverse_split', 'merger', 'spinoff',
  'delisting', 'symbol_change', 'cash_distribution', 'stock_distribution'
);

-- Price provenance. `provider_primary` / `provider_fallback` model a
-- two-tier market-data pipeline; `broker` is the broker-reported price;
-- `manual` is a user override (illiquid private holdings).
create type price_source as enum ('broker', 'provider_primary', 'provider_fallback', 'manual');

-- Instruments: GLOBAL reference table. No workspace_id — every workspace shares
-- the same AAPL row. Dedup strategy: unique ISIN when present, otherwise
-- unique (symbol, exchange) for instruments without an ISIN (e.g. crypto).
create table instruments (
  id          uuid primary key,
  symbol      text not null,
  isin        text,
  name        text not null,
  asset_class asset_class not null,
  currency    money_currency not null,
  exchange    text,
  metadata    jsonb not null default '{}'::jsonb,
  active      bool not null default true,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create trigger instruments_updated_at before update on instruments
  for each row execute function set_updated_at();

-- ISIN-based dedupe when present (partial unique).
create unique index instruments_isin_uq on instruments(isin) where isin is not null;
-- Symbol+exchange dedupe when no ISIN (e.g. crypto, OTC).
create unique index instruments_symbol_exchange_uq
  on instruments(symbol, exchange) where isin is null;

-- Hot-path: active instrument lookup by symbol.
create index instruments_active_idx on instruments(symbol) where active;

-- Investment accounts: 1:1 extension of accounts. `account_id` is both PK
-- and (composite) FK target. Workspace_id is stored denormalized for
-- composite-FK joins against child tables; the `ia_account_fk` constraint
-- pins it to the parent account's workspace.
create table investment_accounts (
  account_id                 uuid primary key,
  workspace_id                  uuid not null references workspaces(id) on delete cascade,
  cost_basis_method          cost_basis_method not null default 'fifo',
  default_tax_lot_strategy   text,
  created_at                 timestamptz not null default now(),
  updated_at                 timestamptz not null default now(),
  unique (workspace_id, account_id),
  constraint ia_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade
);

create trigger investment_accounts_updated_at before update on investment_accounts
  for each row execute function set_updated_at();

-- Investment trades: buy/sell events. `linked_cash_transaction_id` pairs a
-- trade with the cash leg in the transactions ledger (nullable because
-- some brokerages settle net-of-fees internally). FK to instruments is
-- single-column (global parent); FK to investment_accounts is composite
-- (workspace-scoped parent). on delete restrict on instrument_id to prevent
-- accidental mass deletion of market-data rows.
create table investment_trades (
  id                         uuid primary key,
  workspace_id                  uuid not null references workspaces(id) on delete cascade,
  account_id                 uuid not null,
  instrument_id              uuid not null references instruments(id) on delete restrict,
  side                       trade_side not null,
  quantity                   numeric(28,8) not null,
  price                      numeric(28,8) not null,
  currency                   money_currency not null,
  fee_amount                 numeric(28,8) not null default 0,
  fee_currency               money_currency not null,
  trade_date                 date not null,
  settle_date                date,
  linked_cash_transaction_id uuid,
  created_at                 timestamptz not null default now(),
  updated_at                 timestamptz not null default now(),
  unique (workspace_id, id),
  constraint it_account_fk foreign key (workspace_id, account_id)
    references investment_accounts(workspace_id, account_id) on delete cascade,
  constraint it_cash_fk foreign key (workspace_id, linked_cash_transaction_id)
    references transactions(workspace_id, id) on delete set null,
  check (quantity > 0),
  check (price >= 0),
  check (fee_amount >= 0),
  -- IBKR and most brokers book fees in the trade currency; enforcing equality
  -- keeps cost-basis math honest. If a v2 broker books fees in a distinct
  -- currency we'll drop this check and add an FX-normalization column.
  check (fee_currency = currency)
);

create trigger investment_trades_updated_at before update on investment_trades
  for each row execute function set_updated_at();

-- Per-account trade timeline (most common query) and per-instrument
-- trade history (for corporate-action replay).
create index investment_trades_account_trade_idx on investment_trades(account_id, trade_date desc);
create index investment_trades_instrument_idx on investment_trades(instrument_id, trade_date desc);
-- FK-side index for the `on delete set null` cascade from transactions.
create index investment_trades_cash_link_idx on investment_trades(linked_cash_transaction_id) where linked_cash_transaction_id is not null;

-- Investment lots: open tax lots per (account, instrument). `quantity_opening`
-- is the original acquired quantity; `quantity_remaining` shrinks as lot
-- consumptions are recorded on sells. `closed_at` is set when remaining hits
-- zero. `source_trade_id` points to the buy trade that created the lot
-- (nullable because lots can also be created by corporate actions). Lots
-- are opened by buys or corporate actions; the service layer ensures
-- `source_trade_id` points to a buy-side trade. The schema can't express
-- that without a trigger and the invariant rarely matters — a bad
-- reference is caught at position-refresh time.
create table investment_lots (
  id                      uuid primary key,
  workspace_id               uuid not null references workspaces(id) on delete cascade,
  account_id              uuid not null,
  instrument_id           uuid not null references instruments(id) on delete restrict,
  acquired_at             date not null,
  quantity_opening        numeric(28,8) not null,
  quantity_remaining      numeric(28,8) not null,
  cost_basis_per_unit     numeric(28,8) not null,
  currency                money_currency not null,
  source_trade_id         uuid,
  closed_at               timestamptz,
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now(),
  unique (workspace_id, id),
  constraint il_account_fk foreign key (workspace_id, account_id)
    references investment_accounts(workspace_id, account_id) on delete cascade,
  constraint il_trade_fk foreign key (workspace_id, source_trade_id)
    references investment_trades(workspace_id, id) on delete set null,
  check (quantity_opening > 0),
  check (quantity_remaining >= 0),
  check (quantity_remaining <= quantity_opening),
  check (cost_basis_per_unit >= 0),
  -- Biconditional: a lot is "closed" iff all of it has been consumed.
  -- closed_at IS NULL  <=>  quantity_remaining > 0.
  -- Prevents both incoherent states (closed with remaining > 0; exhausted
  -- without closed_at set).
  check ((closed_at is null) = (quantity_remaining > 0))
);

create trigger investment_lots_updated_at before update on investment_lots
  for each row execute function set_updated_at();

-- Lot selection for FIFO/LIFO: order by acquired_at.
create index investment_lots_account_instrument_idx on investment_lots(account_id, instrument_id, acquired_at);
-- Hot path: open lots only (quantity_remaining > 0) for sell allocation.
create index investment_lots_open_idx on investment_lots(account_id, instrument_id, acquired_at)
  where quantity_remaining > 0;
-- FK-side index for `on delete set null` from trades.
create index investment_lots_source_trade_idx on investment_lots(source_trade_id) where source_trade_id is not null;

-- Lot consumptions: append-only audit of how a sell trade drew down one or
-- more lots. `realised_gain` is (sell_proceeds - consumed_cost_basis) at
-- consumption time, denominated in `currency`. No updated_at — immutable.
create table investment_lot_consumptions (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  lot_id              uuid not null,
  sell_trade_id       uuid not null,
  quantity_consumed   numeric(28,8) not null,
  realised_gain       numeric(28,8) not null,
  currency            money_currency not null,
  consumed_at         date not null,
  created_at          timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ilc_lot_fk foreign key (workspace_id, lot_id)
    references investment_lots(workspace_id, id) on delete cascade,
  constraint ilc_trade_fk foreign key (workspace_id, sell_trade_id)
    references investment_trades(workspace_id, id) on delete cascade,
  check (quantity_consumed > 0)
);

-- FK-side indexes: walk by lot (lot detail view) or by sell trade (sell
-- reconstruction view).
create index investment_lot_consumptions_lot_idx on investment_lot_consumptions(lot_id);
create index investment_lot_consumptions_trade_idx on investment_lot_consumptions(sell_trade_id);

-- Dividend events. `total_amount` is gross; `tax_withheld` tracks foreign
-- withholding (e.g. 15% US-CH treaty rate). `linked_cash_transaction_id`
-- pairs the dividend with the cash credit in the transactions ledger.
create table dividend_events (
  id                          uuid primary key,
  workspace_id                   uuid not null references workspaces(id) on delete cascade,
  account_id                  uuid not null,
  instrument_id               uuid not null references instruments(id) on delete restrict,
  ex_date                     date not null,
  pay_date                    date not null,
  amount_per_unit             numeric(28,8) not null,
  currency                    money_currency not null,
  total_amount                numeric(28,8) not null,
  tax_withheld                numeric(28,8) not null default 0,
  linked_cash_transaction_id  uuid,
  created_at                  timestamptz not null default now(),
  unique (workspace_id, id),
  constraint de_account_fk foreign key (workspace_id, account_id)
    references investment_accounts(workspace_id, account_id) on delete cascade,
  constraint de_cash_fk foreign key (workspace_id, linked_cash_transaction_id)
    references transactions(workspace_id, id) on delete set null,
  check (amount_per_unit >= 0),
  check (total_amount >= 0),
  check (tax_withheld >= 0),
  check (tax_withheld <= total_amount)
);

-- Per-account dividend timeline and per-instrument dividend history.
create index dividend_events_account_paydate_idx on dividend_events(account_id, pay_date desc);
create index dividend_events_instrument_idx on dividend_events(instrument_id, pay_date desc);
-- FK-side index for `on delete set null` from transactions.
create index dividend_events_cash_link_idx on dividend_events(linked_cash_transaction_id) where linked_cash_transaction_id is not null;

-- Corporate actions. `workspace_id` is NULLABLE: some actions are global
-- market events (e.g. an AAPL split applies to every workspace holding AAPL).
-- When `account_id` is set, `workspace_id` must also be set (enforced via
-- check). When both are null, the row is a global market event that the
-- application layer replays against all affected positions. Composite FK
-- to investment_accounts is null-tolerant: (null, null) skips the check.
create table corporate_actions (
  id              uuid primary key,
  workspace_id       uuid,
  account_id      uuid,
  instrument_id   uuid not null references instruments(id) on delete restrict,
  kind            corporate_action_kind not null,
  effective_date  date not null,
  payload         jsonb not null default '{}'::jsonb,
  applied_at      timestamptz,
  created_at      timestamptz not null default now(),
  constraint ca_workspace_fk foreign key (workspace_id) references workspaces(id) on delete cascade,
  constraint ca_account_fk foreign key (workspace_id, account_id)
    references investment_accounts(workspace_id, account_id) on delete cascade,
  -- account_id requires workspace_id (can't have a scoped account without scope).
  check ((account_id is null) or (workspace_id is not null))
);

-- Hot path: replay actions for an instrument in effective-date order.
create index corporate_actions_instrument_effective_idx
  on corporate_actions(instrument_id, effective_date desc);
-- Partial index for workspace-scoped lookups (global rows excluded).
create index corporate_actions_workspace_account_idx
  on corporate_actions(workspace_id, account_id) where workspace_id is not null;

-- Instrument prices: GLOBAL time-series of quotes. No workspace_id — a
-- single AAPL close is shared by all workspaces. (source) is part of the
-- uniqueness key so provider_primary and provider_fallback can both
-- hold the same (instrument, as_of) without conflict; the read path
-- picks by source-preference order.
create table instrument_prices (
  id             uuid primary key,
  instrument_id  uuid not null references instruments(id) on delete cascade,
  as_of          timestamptz not null,
  price          numeric(28,8) not null,
  currency       money_currency not null,
  source         price_source not null,
  provider_ref   text,
  created_at     timestamptz not null default now(),
  unique (instrument_id, as_of, source),
  check (price >= 0)
);

-- Time-series lookup: latest price for an instrument.
create index instrument_prices_instrument_asof_idx
  on instrument_prices(instrument_id, as_of desc);

-- Investment positions: materialized cache — refreshed by a job from
-- trades + dividends + corporate_actions + prices. Composite PK
-- (account_id, instrument_id); no surrogate id (there is exactly one
-- position row per (account, instrument) pair). `last_price_at` /
-- `unrealised_gain` are nullable because the price service may not yet
-- have populated them. Composite FK to investment_accounts enforces
-- workspace consistency.
create table investment_positions (
  account_id         uuid not null,
  instrument_id      uuid not null references instruments(id) on delete cascade,
  workspace_id          uuid not null references workspaces(id) on delete cascade,
  quantity           numeric(28,8) not null,
  average_cost       numeric(28,8) not null,
  realised_pnl       numeric(28,8) not null default 0,
  currency           money_currency not null,
  last_price         numeric(28,8),
  last_price_at      timestamptz,
  unrealised_gain    numeric(28,8),
  refreshed_at       timestamptz not null default now(),
  primary key (account_id, instrument_id),
  constraint ip_account_fk foreign key (workspace_id, account_id)
    references investment_accounts(workspace_id, account_id) on delete cascade,
  check (quantity >= 0),
  check (average_cost >= 0)
);

-- Workspace-wide rollup queries and instrument-wide exposure queries.
create index investment_positions_workspace_idx on investment_positions(workspace_id);
create index investment_positions_instrument_idx on investment_positions(instrument_id);

-- Allocation buckets: user-defined classification buckets (e.g. "US
-- Equities", "EM Bonds"). Hierarchical via `parent_bucket_id` (composite
-- self-FK scoped to workspace). `target_percentage` is the aspirational
-- allocation (nullable — some buckets are reporting-only).
create table allocation_buckets (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  name                text not null,
  parent_bucket_id    uuid,
  target_percentage   numeric(5,2),
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, name),
  constraint ab_parent_fk foreign key (workspace_id, parent_bucket_id)
    references allocation_buckets(workspace_id, id) on delete set null,
  check (parent_bucket_id is null or parent_bucket_id <> id),
  check (target_percentage is null or (target_percentage >= 0 and target_percentage <= 100))
);

create trigger allocation_buckets_updated_at before update on allocation_buckets
  for each row execute function set_updated_at();

-- FK-side index for hierarchy walks and `on delete set null` cascades.
create index allocation_buckets_parent_idx on allocation_buckets(parent_bucket_id) where parent_bucket_id is not null;

-- Position -> bucket allocations. Composite PK (account_id, instrument_id,
-- bucket_id): a single position can be split across multiple buckets (e.g.
-- 70% "US Equities" / 30% "Tech"). Bucket FK is composite (workspace-scoped);
-- position FK is a two-column reference to investment_positions' composite
-- PK. `workspace_id` is stored for the composite bucket FK. Per-position
-- sum(share_percentage) <= 100 is enforced by the service layer on write;
-- a CHECK can't express aggregation across rows and a trigger is overkill
-- for a UX invariant.
create table position_bucket_allocations (
  account_id        uuid not null,
  instrument_id     uuid not null,
  bucket_id         uuid not null,
  workspace_id         uuid not null references workspaces(id) on delete cascade,
  share_percentage  numeric(5,2) not null default 100,
  created_at        timestamptz not null default now(),
  primary key (account_id, instrument_id, bucket_id),
  constraint pba_position_fk foreign key (account_id, instrument_id)
    references investment_positions(account_id, instrument_id) on delete cascade,
  constraint pba_bucket_fk foreign key (workspace_id, bucket_id)
    references allocation_buckets(workspace_id, id) on delete cascade,
  check (share_percentage >= 0 and share_percentage <= 100)
);

-- FK-side index for bucket-driven rollups.
create index position_bucket_allocations_bucket_idx on position_bucket_allocations(bucket_id);

-- Target allocations: time-series of target percentages per bucket.
-- `effective_from` lets us model rebalancing drift over time ("as of
-- 2026-01-01 the target was 60%; as of 2026-06-01 it's 50%").
create table target_allocations (
  id                 uuid primary key,
  workspace_id          uuid not null references workspaces(id) on delete cascade,
  bucket_id          uuid not null,
  target_percentage  numeric(5,2) not null,
  effective_from     date not null,
  created_at         timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, bucket_id, effective_from),
  constraint ta_bucket_fk foreign key (workspace_id, bucket_id)
    references allocation_buckets(workspace_id, id) on delete cascade,
  check (target_percentage >= 0 and target_percentage <= 100)
);

-- Time-series lookup: latest target for a bucket.
create index target_allocations_bucket_effective_idx
  on target_allocations(bucket_id, effective_from desc);
