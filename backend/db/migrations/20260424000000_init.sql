-- Initial schema: users, sessions, accounts, transactions.
-- Money uses numeric(19,4) for fiat. Adjust to (28,8) if you add crypto.
--
-- Naming conventions:
--   id           uuid primary key, generated v7 (time-ordered) via gen_random_uuid() for now.
--                Swap to pg_uuidv7 extension later if you want sortable IDs.
--   created_at   timestamptz not null default now()
--   updated_at   timestamptz not null default now()  (trigger below)
--   deleted_at   timestamptz null — soft delete where relevant.

create extension if not exists "pgcrypto";

create or replace function set_updated_at() returns trigger language plpgsql as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

-- ─── users ──────────────────────────────────────────────────────────────────
create table users (
  id              uuid primary key default gen_random_uuid(),
  email           text not null unique,
  password_hash   text,                         -- null if passkey-only
  display_name    text not null,
  base_currency   text not null default 'CHF',  -- for reporting
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create trigger users_updated_at before update on users
  for each row execute function set_updated_at();

-- ─── sessions ───────────────────────────────────────────────────────────────
create table sessions (
  id              text primary key,                       -- random 32-byte hex
  user_id         uuid not null references users(id) on delete cascade,
  created_at      timestamptz not null default now(),
  expires_at      timestamptz not null,
  user_agent      text,
  ip              inet
);

create index sessions_user_idx on sessions(user_id);
create index sessions_expires_idx on sessions(expires_at);

-- ─── accounts ───────────────────────────────────────────────────────────────
-- An "account" is anything with a balance: a bank account, brokerage account,
-- cash wallet, manual pot. Source indicates where data comes from.
create type account_source as enum (
  'manual',
  'gocardless',
  'ibkr_flex',
  'camt053_import',
  'csv_import'
);

create type account_kind as enum (
  'checking',
  'savings',
  'credit_card',
  'brokerage',
  'cash',
  'loan',
  'other'
);

create table accounts (
  id               uuid primary key default gen_random_uuid(),
  user_id          uuid not null references users(id) on delete cascade,
  name             text not null,
  kind             account_kind not null,
  source           account_source not null,
  currency         text not null,
  institution      text,                          -- "Revolut", "PostFinance", "IBKR"
  external_id      text,                          -- provider's account id (iban, ibkr account id, ...)
  balance          numeric(19,4) not null default 0,
  balance_as_of    timestamptz,
  archived_at      timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  unique (user_id, source, external_id)
);

create index accounts_user_idx on accounts(user_id);

create trigger accounts_updated_at before update on accounts
  for each row execute function set_updated_at();

-- ─── categories ─────────────────────────────────────────────────────────────
create table categories (
  id           uuid primary key default gen_random_uuid(),
  user_id      uuid not null references users(id) on delete cascade,
  name         text not null,
  parent_id    uuid references categories(id) on delete set null,
  color        text,
  created_at   timestamptz not null default now(),
  unique (user_id, parent_id, name)
);

-- ─── transactions ───────────────────────────────────────────────────────────
-- One row per posted transaction. Amount is signed (negative = outflow) in the
-- account's currency. FX / original currency captured in original_* columns.
create table transactions (
  id                 uuid primary key default gen_random_uuid(),
  user_id            uuid not null references users(id) on delete cascade,
  account_id         uuid not null references accounts(id) on delete cascade,
  category_id        uuid references categories(id) on delete set null,
  external_id        text,                          -- provider tx id; null for manual
  booked_at          date not null,
  value_at           date,
  amount             numeric(19,4) not null,
  currency           text not null,
  original_amount    numeric(19,4),
  original_currency  text,
  description        text,
  counterparty       text,
  notes              text,
  raw                jsonb,                         -- original provider payload for debugging
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now(),
  unique (account_id, external_id)                  -- idempotent sync
);

create index transactions_user_booked_idx  on transactions(user_id, booked_at desc);
create index transactions_account_idx      on transactions(account_id, booked_at desc);
create index transactions_category_idx     on transactions(category_id);

create trigger transactions_updated_at before update on transactions
  for each row execute function set_updated_at();

-- ─── provider_connections ───────────────────────────────────────────────────
-- Stores encrypted tokens for provider integrations (GoCardless requisitions,
-- IBKR Flex tokens, etc.). Actual secret bytes are AES-GCM encrypted.
create table provider_connections (
  id               uuid primary key default gen_random_uuid(),
  user_id          uuid not null references users(id) on delete cascade,
  provider         account_source not null,        -- reuses enum
  label            text,                           -- user-provided ("My Revolut", "IBKR taxable")
  status           text not null default 'active', -- active | revoked | error
  secrets_cipher   text not null,                  -- base64(nonce||ct||tag)
  metadata         jsonb not null default '{}'::jsonb,
  last_synced_at   timestamptz,
  last_error       text,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create index provider_connections_user_idx on provider_connections(user_id);

create trigger provider_connections_updated_at before update on provider_connections
  for each row execute function set_updated_at();
