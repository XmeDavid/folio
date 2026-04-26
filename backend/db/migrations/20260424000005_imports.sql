-- Folio v2 domain — imports & provider integration (spec §5.5).
-- provider_connections, provider_accounts, import_profiles, import_batches,
-- source_refs. source_refs is polymorphic (entity_type text + entity_id uuid)
-- with a workspace-scoped dedupe key on (workspace_id, entity_type, provider,
-- external_id) so the same provider-supplied id can coexist across workspaces.

-- Provider connection lifecycle. 'active' = usable; 'error' = transient
-- failure, will retry; 'revoked' = user or provider terminated access;
-- 'consent_expired' = PSD2 90-day consent (or similar) lapsed, re-auth needed.
create type provider_connection_status as enum (
  'active', 'error', 'revoked', 'consent_expired'
);

-- Import profile kinds. File-based parsers (csv, camt053, ibkr_flex) use
-- user-defined mappings; preset_* profiles are shipped parsers for common
-- personal-finance exports. Service layer dispatches on this enum.
create type import_profile_kind as enum (
  'csv', 'camt053', 'ibkr_flex',
  'preset_mint', 'preset_ynab', 'preset_actual', 'preset_firefly'
);

-- Batch origin. file_upload = user uploaded a file;
-- provider_sync = automated pull from a provider_connection;
-- manual = user-created batch (e.g. ad-hoc bulk entry).
create type import_source_kind as enum (
  'file_upload', 'provider_sync', 'manual'
);

-- Batch lifecycle. pending -> parsing -> applied | failed.
create type import_status as enum (
  'pending', 'parsing', 'applied', 'failed'
);

-- Provider connections: credentials and sync state for a linked third-party
-- (bank aggregator, brokerage, etc.). `secrets_cipher` holds the encrypted
-- token bundle (envelope-encrypted at the service layer). `metadata` carries
-- non-sensitive provider context (institution id, account flags, etc.).
-- `next_scheduled_sync_at` drives the background sync scheduler.
create table provider_connections (
  id                        uuid primary key,
  workspace_id                 uuid not null references workspaces(id) on delete cascade,
  provider                  text not null,
  label                     text,
  status                    provider_connection_status not null default 'active',
  secrets_cipher            text not null,
  metadata                  jsonb not null default '{}'::jsonb,
  consent_expires_at        timestamptz,
  last_synced_at            timestamptz,
  next_scheduled_sync_at    timestamptz,
  last_error                text,
  created_at                timestamptz not null default now(),
  updated_at                timestamptz not null default now(),
  unique (workspace_id, id)
);

create trigger provider_connections_updated_at before update on provider_connections
  for each row execute function set_updated_at();

create index provider_connections_workspace_idx on provider_connections(workspace_id);
-- Job scheduler: find connections due for sync. Partial index keeps the
-- worker query tight (skips revoked/expired and connections without a
-- scheduled next run).
create index provider_connections_sync_due_idx
  on provider_connections(next_scheduled_sync_at)
  where status = 'active' and next_scheduled_sync_at is not null;

-- Provider accounts: the external-side accounts exposed by a connection.
-- `account_id` is nullable because the user maps a provider account to a
-- local account after discovery. `external_account_id` is the provider's
-- stable id; unique per connection. `external_payload` caches the last
-- known provider metadata (balance, name, etc.) for UI display.
create table provider_accounts (
  id                      uuid primary key,
  workspace_id               uuid not null references workspaces(id) on delete cascade,
  provider_connection_id  uuid not null,
  account_id              uuid,                  -- null until user maps
  external_account_id     text not null,
  external_payload        jsonb not null default '{}'::jsonb,
  linked_at               timestamptz,
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now(),
  unique (workspace_id, id),
  unique (provider_connection_id, external_account_id),
  constraint pa_conn_fk foreign key (workspace_id, provider_connection_id)
    references provider_connections(workspace_id, id) on delete cascade,
  constraint pa_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete set null
);

create trigger provider_accounts_updated_at before update on provider_accounts
  for each row execute function set_updated_at();

-- Reverse lookup: which provider accounts map to a given local account.
-- Partial index skips the many unmapped rows.
create index provider_accounts_account_idx on provider_accounts(account_id) where account_id is not null;
-- FK-side index for cascades and per-connection listing.
create index provider_accounts_conn_idx on provider_accounts(provider_connection_id);

-- Import profiles: reusable parser configurations. `mapping` is the column-
-- to-field map (CSV) or XPath/selector map (CAMT053); `options` carries
-- parser switches (date format, thousands separator, etc.). name is unique
-- per workspace for UX.
create table import_profiles (
  id          uuid primary key,
  workspace_id   uuid not null references workspaces(id) on delete cascade,
  name        text not null,
  kind        import_profile_kind not null,
  mapping     jsonb not null,
  options     jsonb not null default '{}'::jsonb,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, name)
);

create trigger import_profiles_updated_at before update on import_profiles
  for each row execute function set_updated_at();

-- Import batches: one row per import attempt. `import_profile_id` is set
-- for file_upload and (optionally) manual; `provider_connection_id` is set
-- for provider_sync; both are nullable since the batch survives deletion
-- of either parent. `file_name`/`file_hash` are only populated for
-- file_upload (hash enables dedupe of re-uploaded files at the service
-- layer). `summary` is a free-form jsonb blob with counts, warnings, etc.
create table import_batches (
  id                       uuid primary key,
  workspace_id                uuid not null references workspaces(id) on delete cascade,
  import_profile_id        uuid,
  provider_connection_id   uuid,
  source_kind              import_source_kind not null,
  file_name                text,
  file_hash                text,
  status                   import_status not null default 'pending',
  summary                  jsonb not null default '{}'::jsonb,
  created_by_user_id       uuid,
  started_at               timestamptz not null default now(),
  finished_at              timestamptz,
  error                    text,
  updated_at               timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ib_profile_fk foreign key (workspace_id, import_profile_id)
    references import_profiles(workspace_id, id) on delete set null,
  constraint ib_conn_fk foreign key (workspace_id, provider_connection_id)
    references provider_connections(workspace_id, id) on delete set null,
  constraint ib_actor_fk foreign key (created_by_user_id)
    references users(id) on delete set null
);

-- Row-freshness signal for stuck-parse detection and parity with every
-- other table. started_at/finished_at track lifecycle; updated_at tracks
-- any row mutation (status transitions, summary updates, error writes).
create trigger import_batches_updated_at before update on import_batches
  for each row execute function set_updated_at();

-- Default listing: batches for a workspace, newest first.
create index import_batches_workspace_started_idx on import_batches(workspace_id, started_at desc);
-- Status filters (failed-batch dashboards, pending-queue drain).
create index import_batches_status_idx on import_batches(workspace_id, status);

-- Source refs: polymorphic provenance records. `entity_type` is plain text
-- (not an enum) so adding new entity types doesn't require a migration;
-- the service layer enforces valid values. Dedupe key is workspace-scoped
-- so the same provider external_id can coexist across workspaces (common
-- when two workspaces use the same aggregator).
create table source_refs (
  id                 uuid primary key,
  workspace_id          uuid not null references workspaces(id) on delete cascade,
  entity_type        text not null,
  entity_id          uuid not null,
  provider           text,
  import_batch_id    uuid,
  external_id        text,
  raw_payload        jsonb not null default '{}'::jsonb,
  observed_at        timestamptz not null default now(),
  created_at         timestamptz not null default now(),
  unique (workspace_id, id),
  constraint sr_batch_fk foreign key (workspace_id, import_batch_id)
    references import_batches(workspace_id, id) on delete set null
);

-- Dedupe: a (workspace, entity_type, provider, external_id) tuple is unique.
-- Both provider and external_id must be present for dedupe to apply — rows
-- with either column null are unique-index-exempt (Postgres treats nulls as
-- distinct anyway; the explicit predicate just shrinks the index).
create unique index source_refs_dedupe_idx
  on source_refs (workspace_id, entity_type, provider, external_id)
  where provider is not null and external_id is not null;

-- Reverse lookup: "show me source refs for this entity" (e.g. on a
-- transaction detail page, display the provider payload that produced it).
create index source_refs_entity_idx on source_refs(entity_type, entity_id);
-- Batch dropdown / filtering: "show rows imported in this batch".
create index source_refs_batch_idx on source_refs(import_batch_id) where import_batch_id is not null;
