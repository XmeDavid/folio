-- Folio v2 domain — identity.
-- Tenants own financial data; users authenticate into a tenant (1:1 in v1).

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

-- Tenants: root of the financial data graph. Not tenant-scoped itself; FKs to tenants always reference tenants(id), never composite.
create table tenants (
  id                uuid primary key,
  name              text not null,
  base_currency     money_currency not null,
  cycle_anchor_day  smallint not null check (cycle_anchor_day between 1 and 31),
  locale            text not null,
  timezone          text not null default 'UTC',
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create trigger tenants_updated_at before update on tenants
  for each row execute function set_updated_at();

-- Users: authenticate into a tenant. 1:1 with tenants in v1 (enforced by
-- UNIQUE(tenant_id)). The composite UNIQUE(tenant_id, id) is the target
-- for composite FKs from tenant-scoped child tables (audit_events.actor_user_id,
-- saved_searches.user_id, etc. — see spec §3.4).
create table users (
  id             uuid primary key,
  tenant_id      uuid not null unique references tenants(id) on delete cascade,
  email          citext not null unique,
  password_hash  text,
  display_name   text not null,
  last_login_at  timestamptz,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  unique (tenant_id, id)           -- composite-FK target
);

create trigger users_updated_at before update on users
  for each row execute function set_updated_at();

-- user_preferences has no tenant_id; it reaches the tenant via users.tenant_id. Not an FK target for tenant-scoped rows.
create table user_preferences (
  user_id           uuid primary key references users(id) on delete cascade,
  theme             text,
  date_format       text,
  number_format     text,
  display_currency  money_currency,
  feature_flags     jsonb not null default '{}'::jsonb,
  updated_at        timestamptz not null default now()
);

create trigger user_preferences_updated_at before update on user_preferences
  for each row execute function set_updated_at();

-- Sessions: opaque session tokens. `id` is the token hash (text PK).
create table sessions (
  id          text primary key,
  user_id     uuid not null references users(id) on delete cascade,
  created_at  timestamptz not null default now(),
  expires_at  timestamptz not null,
  user_agent  text,
  ip          inet
);

create index sessions_user_id_idx on sessions (user_id);
create index sessions_expires_at_idx on sessions (expires_at);

-- WebAuthn credentials: passkeys / hardware keys registered to a user.
create table webauthn_credentials (
  id             uuid primary key,
  user_id        uuid not null references users(id) on delete cascade,
  credential_id  bytea not null unique,
  public_key     bytea not null,
  sign_count     bigint not null default 0,
  transports     text[],
  label          text,
  created_at     timestamptz not null default now()
);

create index webauthn_credentials_user_id_idx on webauthn_credentials(user_id);

-- TOTP credentials: authenticator-app seeds (encrypted at rest).
create table totp_credentials (
  id                     uuid primary key,
  user_id                uuid not null references users(id) on delete cascade,
  secret_cipher          text not null,
  verified_at            timestamptz,
  recovery_codes_cipher  text,
  created_at             timestamptz not null default now()
);

create index totp_credentials_user_id_idx on totp_credentials(user_id);
