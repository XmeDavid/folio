-- Folio v2 domain — identity, tenancy, auth, memberships.
-- Workspaces own financial data; users authenticate and can belong to many workspaces.

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

-- Shared: workspace_role enum. Owner and member are the only roles in v1.
create type workspace_role as enum ('owner', 'member');

-- Workspaces: root of the financial data graph. Not workspace-scoped itself.
-- FKs to workspaces always reference workspaces(id); never composite.
create table workspaces (
  id                uuid primary key,
  name              text not null,
  slug              citext not null unique
                    check (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  base_currency     money_currency not null,
  cycle_anchor_day  smallint not null check (cycle_anchor_day between 1 and 31),
  locale            text not null,
  timezone          text not null default 'UTC',
  deleted_at        timestamptz,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create trigger workspaces_updated_at before update on workspaces
  for each row execute function set_updated_at();

create index workspaces_deleted_at_idx
  on workspaces (deleted_at)
  where deleted_at is not null;

-- Users: authenticate into zero or more workspaces via workspace_memberships.
-- password_hash is NOT NULL; signup always sets it.
create table users (
  id                 uuid primary key,
  email              citext not null unique,
  password_hash      text not null,
  display_name       text not null,
  email_verified_at  timestamptz,
  last_workspace_id     uuid references workspaces(id) on delete set null,
  is_admin           boolean not null default false,
  last_login_at      timestamptz,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

create trigger users_updated_at before update on users
  for each row execute function set_updated_at();

-- user_preferences: per-user UI settings. No workspace_id; reaches workspace via
-- the user's active membership at read time.
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

-- workspace_memberships: a user can belong to many workspaces with a role each.
-- Primary key (workspace_id, user_id) — a user cannot have two roles in one workspace.
create table workspace_memberships (
  workspace_id   uuid not null references workspaces(id) on delete cascade,
  user_id     uuid not null references users(id)   on delete cascade,
  role        workspace_role not null,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  primary key (workspace_id, user_id)
);

create index workspace_memberships_user_id_idx
  on workspace_memberships (user_id);

-- Partial index for the "does this workspace still have an owner?" check.
create index workspace_memberships_owners
  on workspace_memberships (workspace_id)
  where role = 'owner';

create trigger workspace_memberships_updated_at before update on workspace_memberships
  for each row execute function set_updated_at();

-- workspace_invites: pending invitations to join a workspace.
-- token_hash is sha256(plaintext); plaintext ships only in the email.
create table workspace_invites (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  email               citext not null,
  role                workspace_role not null,
  token_hash          bytea not null unique,
  invited_by_user_id  uuid not null references users(id) on delete restrict,
  created_at          timestamptz not null default now(),
  expires_at          timestamptz not null,
  accepted_at         timestamptz,
  revoked_at          timestamptz
);

create index workspace_invites_workspace_id_idx on workspace_invites (workspace_id);
create index workspace_invites_pending_email_idx
  on workspace_invites (email)
  where accepted_at is null and revoked_at is null;

create index workspace_invites_invited_by_idx
  on workspace_invites (invited_by_user_id);

-- auth_tokens: unified single-use tokens for email verify / password reset /
-- email change. Plan 3 populates this.
create table auth_tokens (
  id           uuid primary key,
  user_id      uuid not null references users(id) on delete cascade,
  purpose      text not null
               check (purpose in ('email_verify', 'password_reset', 'email_change')),
  token_hash   bytea not null unique,
  email        citext,
  created_at   timestamptz not null default now(),
  expires_at   timestamptz not null,
  consumed_at  timestamptz
);

create index auth_tokens_user_id_idx on auth_tokens (user_id);
create index auth_tokens_live_idx
  on auth_tokens (purpose, expires_at)
  where consumed_at is null;

-- auth_recovery_codes: MFA recovery codes, one row per code, Argon2id-hashed.
-- Plan 4 populates this.
create table auth_recovery_codes (
  id           uuid primary key,
  user_id      uuid not null references users(id) on delete cascade,
  code_hash    text not null,
  created_at   timestamptz not null default now(),
  consumed_at  timestamptz
);

create index auth_recovery_codes_live_idx
  on auth_recovery_codes (user_id)
  where consumed_at is null;

-- sessions: opaque cookie tokens. id = sha256(plaintext_token) stored as text.
create table sessions (
  id            text primary key,
  user_id       uuid not null references users(id) on delete cascade,
  created_at    timestamptz not null default now(),
  expires_at    timestamptz not null,
  last_seen_at  timestamptz not null default now(),
  reauth_at     timestamptz,
  user_agent    text,
  ip            inet
);

create index sessions_user_id_idx on sessions (user_id);
create index sessions_expires_at_idx on sessions (expires_at);

-- webauthn_credentials: passkeys / hardware keys registered to a user.
-- Plan 4 populates this.
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

-- totp_credentials: authenticator-app seeds (encrypted at rest). Recovery
-- codes moved to auth_recovery_codes for per-code consumption tracking.
create table totp_credentials (
  id            uuid primary key,
  user_id       uuid not null references users(id) on delete cascade,
  secret_cipher text not null,
  verified_at   timestamptz,
  created_at    timestamptz not null default now()
);

create index totp_credentials_user_id_idx on totp_credentials(user_id);
