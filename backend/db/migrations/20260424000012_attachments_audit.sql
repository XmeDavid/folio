-- Folio v2 domain — attachments, documents, audit (spec §5.12).
-- The cross-cutting "paper trail" slice: blob storage metadata for files
-- (attachments) with polymorphic linking to any entity (attachment_links),
-- OCR extraction output (ocr_documents, 1:1 with attachments), per-user
-- filter presets (saved_searches), and an append-only audit log
-- (audit_events) written by a trigger bound to every audited table.
--
-- Shape rules threaded throughout:
--   * attachments.sha256 is bytea (not text) with a length-32 check, and
--     (workspace_id, sha256) is UNIQUE: the same binary content can only be
--     stored once per workspace (workspace-level dedupe).
--   * attachment_links is polymorphic on (entity_type, entity_id) — any
--     entity can have attachments without explicit FKs. Referential
--     integrity is enforced only against attachments(workspace_id, id) on the
--     attachment side; the link's entity side is intentionally untyped.
--   * ocr_documents.attachment_id is bare UNIQUE (1:1) — each attachment
--     has at most one OCR document.
--   * audit_events is append-only at the DB level: reject triggers on
--     UPDATE and DELETE make the log tamper-evident.
--   * record_audit_event() reads folio.actor_user_id from the session via
--     current_setting(..., true) (missing_ok = true, so unset = NULL actor),
--     which keeps system/job writes auditable without requiring a logged-in
--     user.
--   * audit_events.workspace_id is derived from the row (OLD for DELETE, NEW
--     otherwise), so workspace scoping is preserved even for system events.

-- Requires PostgreSQL 13+ for gen_random_uuid() in core (see record_audit_event).
-- If targeting PG 12 or earlier, install pgcrypto via create extension if not exists.

-- Storage backend for an attachment blob. `local` = on-box filesystem
-- (default in dev / single-workspace deploys); `s3` = object storage. The
-- storage_key is backend-relative (path for local, object key for s3).
create type attachment_storage as enum ('local', 's3');

-- OCR pipeline state. `none` = OCR not applicable / not requested;
-- `pending` = queued for extraction (partial index targets this); `done` =
-- ocr_documents row populated; `failed` = extraction attempt failed (retry
-- is a state transition back to pending).
create type ocr_status as enum ('none', 'pending', 'done', 'failed');

-- Audit action taxonomy. `merged` is the merge-entities operation (e.g.
-- merchant merge); `restored` is undo of a soft delete.
create type audit_action as enum ('created', 'updated', 'deleted', 'restored', 'merged');

-- Attachments: blob metadata. The blob itself lives in storage_backend at
-- storage_key; this row is the referenceable handle. sha256 is bytea (not
-- hex text) because it's fixed-width 32 bytes — half the storage of the
-- hex form, and equality compares on raw bytes. uploaded_by_user_id is
-- nullable (system uploads have no actor) and references users(id).
-- Workspace scoping is enforced by the row's workspace_id FK to workspaces(id),
-- not a composite FK into users.
create table attachments (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  filename            text not null,
  content_type        text not null,
  size_bytes          bigint not null,
  storage_backend     attachment_storage not null default 'local',
  storage_key         text not null,
  sha256              bytea not null,
  uploaded_by_user_id uuid,
  uploaded_at         timestamptz not null default now(),
  ocr_status          ocr_status not null default 'none',
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  unique (workspace_id, id),
  -- Workspace-scoped content dedupe: uploading the same file twice within a
  -- workspace should collapse to one row. Cross-workspace dedupe is intentionally
  -- not attempted (workspace isolation > storage savings).
  unique (workspace_id, sha256),
  constraint attachments_uploader_fk foreign key (uploaded_by_user_id)
    references users(id) on delete set null,
  check (size_bytes >= 0),
  -- SHA-256 is exactly 32 bytes; guard against truncation or mis-encoding.
  check (length(sha256) = 32)
);

create trigger attachments_updated_at before update on attachments
  for each row execute function set_updated_at();

-- Partial index: the OCR worker polls for `pending` attachments per workspace.
-- Partial on pending keeps the index tiny (most rows are `done` or `none`).
create index attachments_ocr_pending_idx on attachments(workspace_id)
  where ocr_status = 'pending';

-- Attachment links: polymorphic M:N between attachments and any entity.
-- (entity_type, entity_id) is a loose pointer — no FK, because the target
-- entity type varies. UNIQUE(attachment_id, entity_type, entity_id)
-- prevents duplicate links. linked_by_user_id is optional (system links
-- have no actor) and references users(id). Workspace scoping is enforced by
-- the row's workspace_id FK to workspaces(id).
create table attachment_links (
  id                uuid primary key,
  workspace_id         uuid not null references workspaces(id) on delete cascade,
  attachment_id     uuid not null,
  entity_type       text not null,
  entity_id         uuid not null,
  linked_by_user_id uuid,
  linked_at         timestamptz not null default now(),
  created_at        timestamptz not null default now(),
  unique (workspace_id, id),
  unique (attachment_id, entity_type, entity_id),
  constraint al_attachment_fk foreign key (workspace_id, attachment_id)
    references attachments(workspace_id, id) on delete cascade,
  constraint al_linker_fk foreign key (linked_by_user_id)
    references users(id) on delete set null
);

-- Index for the dominant read: "show all attachments for entity X".
create index attachment_links_entity_idx on attachment_links(entity_type, entity_id);
-- Reverse lookup: "which entities does this attachment link to?"
create index attachment_links_attachment_idx on attachment_links(attachment_id);

-- OCR documents: 1:1 with attachments (bare UNIQUE on attachment_id).
-- `text_content` is the full extracted plaintext; `extracted_jsonb` is the
-- structured output (fields, bounding boxes, etc.) as returned by the
-- engine. `confidence` is the engine's self-reported 0..1 score.
create table ocr_documents (
  id              uuid primary key,
  workspace_id       uuid not null references workspaces(id) on delete cascade,
  attachment_id   uuid not null unique,
  text_content    text,
  extracted_jsonb jsonb not null default '{}'::jsonb,
  processed_at    timestamptz not null default now(),
  engine          text not null,
  confidence      numeric(5,4),
  created_at      timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ocr_attachment_fk foreign key (workspace_id, attachment_id)
    references attachments(workspace_id, id) on delete cascade,
  check (confidence is null or (confidence >= 0 and confidence <= 1))
);

-- Saved searches: per-user named filter presets. filter_jsonb is the
-- serialized filter state; `pinned` floats favorites in the UI. UNIQUE
-- (workspace_id, user_id, name) prevents duplicate preset names per user.
create table saved_searches (
  id           uuid primary key,
  workspace_id    uuid not null references workspaces(id) on delete cascade,
  user_id      uuid not null,
  name         text not null,
  filter_jsonb jsonb not null,
  pinned       bool not null default false,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, user_id, name),
  constraint ss_user_fk foreign key (user_id)
    references users(id) on delete cascade
);

create trigger saved_searches_updated_at before update on saved_searches
  for each row execute function set_updated_at();

-- Index order matches the sidebar query: by user, pinned-first, alpha.
create index saved_searches_user_pinned_idx on saved_searches(user_id, pinned desc, name);

-- Audit events: append-only log of entity mutations. Polymorphic via
-- (entity_type, entity_id). actor_user_id is nullable (system/job events
-- have no user). before_jsonb/after_jsonb carry the full row snapshots so
-- downstream diff UIs don't need to reconstruct state. ip/user_agent are
-- populated by the application (when available) for forensic context.
create table audit_events (
  id              uuid primary key,
  workspace_id       uuid references workspaces(id) on delete cascade,
  entity_type     text not null,
  entity_id       uuid not null,
  action          text not null,
  actor_user_id   uuid,
  before_jsonb    jsonb,
  after_jsonb     jsonb,
  reason          text,
  ip              inet,
  user_agent      text,
  occurred_at     timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ae_actor_fk foreign key (actor_user_id)
    references users(id) on delete set null
);

-- Entity timeline: "history of entity X, newest first" — the dominant
-- audit-UI query. Composite ordered by occurred_at desc.
create index audit_events_entity_idx
  on audit_events(workspace_id, entity_type, entity_id, occurred_at desc);
-- Actor timeline: "what has user Y done lately?" Partial on non-null
-- actor_user_id keeps it tight (system events skip the index).
create index audit_events_actor_idx
  on audit_events(actor_user_id, occurred_at desc) where actor_user_id is not null;

-- Append-only enforcement: reject UPDATE and DELETE at the trigger level.
-- This is a tamper-evidence guarantee, not a privilege check — superuser
-- could still drop the triggers, but routine application code cannot
-- mutate the log.
create or replace function reject_audit_mutation() returns trigger language plpgsql as $$
begin
  raise exception 'audit_events is append-only';
end;
$$;

create trigger audit_events_no_update before update on audit_events
  for each row execute function reject_audit_mutation();
create trigger audit_events_no_delete before delete on audit_events
  for each row execute function reject_audit_mutation();

-- Audit recorder: a single generic trigger function that records the
-- insert/update/delete of the source row into audit_events. The entity_type
-- is passed as a trigger argument (tg_argv[0]) so one function serves every
-- audited table. The actor is read from the session setting
-- folio.actor_user_id via current_setting(..., true) — the `true` means
-- missing_ok, so unset (system/job context) yields NULL rather than error.
-- workspace_id and entity_id come from the row itself (OLD for DELETE, NEW
-- otherwise), which preserves workspace scoping for system events too.
create or replace function record_audit_event() returns trigger language plpgsql as $$
declare
  v_entity_type text := tg_argv[0];
  v_actor uuid := nullif(current_setting('folio.actor_user_id', true), '')::uuid;
  v_workspace uuid;
  v_entity_id uuid;
  v_before jsonb;
  v_after jsonb;
  v_action text;
begin
  if tg_op = 'DELETE' then
    v_action := 'deleted';
    v_workspace := old.workspace_id;
    v_entity_id := old.id;
    v_before := to_jsonb(old);
    v_after := null;
  elsif tg_op = 'UPDATE' then
    v_action := 'updated';
    v_workspace := new.workspace_id;
    v_entity_id := new.id;
    v_before := to_jsonb(old);
    v_after := to_jsonb(new);
  else
    v_action := 'created';
    v_workspace := new.workspace_id;
    v_entity_id := new.id;
    v_before := null;
    v_after := to_jsonb(new);
  end if;

  insert into audit_events (
    id, workspace_id, entity_type, entity_id, action,
    actor_user_id, before_jsonb, after_jsonb, occurred_at
  ) values (
    gen_random_uuid(), v_workspace, v_entity_type, v_entity_id, v_action,
    v_actor, v_before, v_after, now()
  );

  return coalesce(new, old);
end;
$$;

-- Audit trigger bindings (spec §3.11). One trigger per audited table,
-- passing the singular entity_type as the trigger argument. AFTER-row so
-- the source row is fully materialized (constraints etc. have settled)
-- before the audit row is written.
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
