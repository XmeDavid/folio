-- Folio v2 domain — notifications (spec §5.14).
-- The final schema slice: event-driven user-facing notifications. A rules
-- engine (notification_rules) subscribes to domain event kinds; emitted
-- events are appended to notification_events; a delivery fanout writes one
-- notification_deliveries row per (event, channel, destination); users can
-- override per-event / per-channel routing via notification_preferences.
-- Webhook destinations (Slack, Discord, custom HTTP) are stored with a
-- cipher-wrapped secret for HMAC signing.
--
-- Shape rules threaded throughout:
--   * Every tenant-scoped table carries unique(tenant_id, id) so composite
--     FKs from sibling tables enforce tenant consistency.
--   * notification_events is APPEND-ONLY: there is no updated_at, no trigger,
--     no mutation path. Events are an immutable audit log; edits to the
--     live state happen on the delivery row.
--   * notification_deliveries is a state machine: queued -> sent | failed,
--     and sent -> read. Three CHECK constraints enforce the invariants
--     (sent iff delivered_at, failed iff error, read implies delivered_at).
--   * nd_destination_chk is a biconditional: channel='webhook' iff
--     destination_id is not null. In-app / email / web_push deliveries
--     route by (tenant, user) and must NOT carry a destination row.
--   * The fanout uniqueness key uses NULLS NOT DISTINCT so (event, channel)
--     can only be delivered once even when destination_id is null
--     (i.e. in_app / email / web_push channels). Without it, Postgres'
--     default "NULLs are distinct" semantics would let the fanout worker
--     double-write a delivery on retry and the user would see two copies.

-- Delivery channel. `in_app` = in-product notification bell; `email` =
-- transactional email; `web_push` = browser Push API; `webhook` = outbound
-- HTTP POST to a user-configured URL (Slack, Discord, Zapier, etc.).
create type notification_channel as enum ('in_app', 'email', 'web_push', 'webhook');

-- Digest mode. `realtime` = emit delivery immediately; `daily_digest` /
-- `weekly_digest` = collect events and emit a batched summary on the digest
-- worker's cadence. Set on the rule (default) and optionally overridden per
-- user / event / channel via notification_preferences.digest_override.
create type notification_digest_mode as enum ('realtime', 'daily_digest', 'weekly_digest');

-- Delivery state machine. `queued` = fanout emitted the row, worker has not
-- picked it up; `sent` = channel accepted (email queued with SMTP, webhook
-- returned 2xx, in_app persisted); `failed` = channel rejected (error
-- populated); `read` = user acknowledged (in_app click, email open beacon,
-- web_push tap). `read` is only reachable from `sent`.
create type notification_delivery_status as enum ('queued', 'sent', 'failed', 'read');

-- Notification rules: tenant-scoped subscriptions to a domain event_kind
-- (e.g. 'budget_overrun', 'large_inflow', 'goal_reached'). `config_jsonb`
-- carries rule-specific parameters (thresholds, target categories, etc.).
-- `enabled` is the kill switch; `digest_mode` sets the default emission
-- cadence (users can override per-channel via notification_preferences).
-- UNIQUE (tenant_id, name) prevents duplicate rule names per tenant.
create table notification_rules (
  id           uuid primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  name         text not null,
  event_kind   text not null,
  config_jsonb jsonb not null default '{}'::jsonb,
  enabled      bool not null default true,
  digest_mode  notification_digest_mode not null default 'realtime',
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, name)
);

create trigger notification_rules_updated_at before update on notification_rules
  for each row execute function set_updated_at();

-- Dispatch-side index: "for tenant T, which enabled rules listen to event
-- kind K?". Partial on enabled=true since disabled rules shouldn't be
-- considered by the dispatcher. Rebuilt automatically as rules are toggled.
create index notification_rules_event_idx on notification_rules(tenant_id, event_kind)
  where enabled = true;

-- Notification events: append-only log of domain events that matched a rule
-- (or were emitted directly without a rule, hence rule_id is nullable).
-- `subject_entity_type` + `subject_entity_id` identify the domain object the
-- event is about (e.g. 'category' + <uuid> for a budget overrun, 'account' +
-- <uuid> for a balance jump). `payload_jsonb` carries the rendered content
-- (title, body, variables) the delivery worker uses to compose per-channel
-- output. `occurred_at` is when the domain condition was detected; `created_at`
-- is when the row was written (typically within seconds).
--
-- No updated_at, no trigger, no mutations — this is an audit log. Lifecycle
-- on the delivery row.
create table notification_events (
  id                   uuid primary key,
  tenant_id            uuid not null references tenants(id) on delete cascade,
  rule_id              uuid,
  event_kind           text not null,
  subject_entity_type  text not null,
  subject_entity_id    uuid not null,
  payload_jsonb        jsonb not null default '{}'::jsonb,
  occurred_at          timestamptz not null default now(),
  created_at           timestamptz not null default now(),
  unique (tenant_id, id),
  -- Composite FK to keep the rule's tenant and the event's tenant aligned.
  -- on delete set null: deleting the rule (e.g. user turning off an alert)
  -- must not cascade-delete the historical event log.
  constraint ne_rule_fk foreign key (tenant_id, rule_id)
    references notification_rules(tenant_id, id) on delete set null
);

-- Timeline query: "load recent events for tenant T, newest first".
create index notification_events_tenant_occurred_idx on notification_events(tenant_id, occurred_at desc);
-- FK-side index so rule deletes can null-out dependent events without a scan.
-- Partial on non-null to stay small (most events retain their rule_id).
create index notification_events_rule_idx on notification_events(rule_id) where rule_id is not null;
-- Subject lookup: "show me all notifications about category X / account Y".
create index notification_events_subject_idx on notification_events(subject_entity_type, subject_entity_id);

-- Webhook destinations: outbound HTTP targets for channel='webhook' deliveries.
-- `secret_cipher` is the HMAC-signing secret, stored as an application-layer
-- cipher (envelope-encrypted by KMS, decrypted in-process before signing).
-- `last_success_at` / `last_error` are rolling status fields updated by the
-- delivery worker for UI visibility (health indicator in the webhook list).
-- UNIQUE (tenant_id, name) gives each destination a human-readable handle.
create table webhook_destinations (
  id              uuid primary key,
  tenant_id       uuid not null references tenants(id) on delete cascade,
  name            text not null,
  url             text not null,
  secret_cipher   text not null,
  enabled         bool not null default true,
  last_success_at timestamptz,
  last_error      text,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, name)
);

create trigger webhook_destinations_updated_at before update on webhook_destinations
  for each row execute function set_updated_at();

-- Notification deliveries: fanout row per (event, channel, destination). The
-- delivery worker reads queued rows, attempts the channel-specific transport,
-- and transitions the row to sent/failed. For in_app channel, `read_at` is
-- populated when the user clicks the notification; other channels may or
-- may not support read tracking.
--
-- State-machine invariants (enforced by the nd_* check constraints):
--   * nd_destination_chk (biconditional): channel='webhook' iff destination_id
--     is not null. Non-webhook channels route by (tenant, user) and must not
--     carry a destination row; webhook channels require one.
--   * nd_sent_chk: status='sent' iff delivered_at is populated.
--   * nd_failed_chk: status='failed' iff error is populated.
--   * nd_read_chk: read_at is null, OR delivered_at is not null. read implies
--     the row was delivered first (no "read without sent" orphan).
--
-- Uniqueness note: (notification_event_id, channel, destination_id) is
-- UNIQUE NULLS NOT DISTINCT so the fanout worker cannot double-write a
-- delivery on retry when destination_id is null (in_app / email / web_push).
-- Without NULLS NOT DISTINCT, Postgres' default semantics would treat each
-- null destination as unique and allow duplicates.
create table notification_deliveries (
  id                     uuid primary key,
  tenant_id              uuid not null references tenants(id) on delete cascade,
  notification_event_id  uuid not null,
  channel                notification_channel not null,
  -- destination_id points to webhook_destinations.id when channel='webhook';
  -- null for all other channels (enforced by nd_destination_chk).
  destination_id         uuid,
  status                 notification_delivery_status not null default 'queued',
  attempted_at           timestamptz,
  delivered_at           timestamptz,
  read_at                timestamptz,
  error                  text,
  created_at             timestamptz not null default now(),
  unique (tenant_id, id),
  -- A given event delivers at most once per (channel, destination). Uses
  -- NULLS NOT DISTINCT so that null destination_id (in_app/email/web_push)
  -- still collides on retry; see the block comment above for rationale.
  unique nulls not distinct (notification_event_id, channel, destination_id),
  -- Composite FK keeps event and delivery in the same tenant.
  constraint nd_event_fk foreign key (tenant_id, notification_event_id)
    references notification_events(tenant_id, id) on delete cascade,
  -- Composite FK to webhook_destinations (nullable). on delete set null so a
  -- destination delete leaves the historical delivery row intact but unbound.
  constraint nd_webhook_fk foreign key (tenant_id, destination_id)
    references webhook_destinations(tenant_id, id) on delete set null,
  -- destination_id is required iff channel='webhook' (biconditional).
  constraint nd_destination_chk check (
    (channel = 'webhook') = (destination_id is not null)
  ),
  -- delivered_at is set iff the row reached the channel. Both 'sent' (delivered
  -- but not yet read) and 'read' (delivered and acknowledged) imply
  -- delivered_at is populated; 'queued' and 'failed' imply it is null.
  -- The earlier `(status='sent') = (delivered_at is not null)` form made the
  -- documented sent -> read transition unreachable: a read row carries
  -- delivered_at but status='read', which violated the biconditional.
  constraint nd_sent_chk check (
    (delivered_at is not null) = (status in ('sent', 'read'))
  ),
  -- Error-column rules per status. failed requires an error message; sent and
  -- read forbid it (terminal success states must be clean); queued permits an
  -- optional error to support transient-retry tracking (e.g. last attempt
  -- returned 502 Bad Gateway, worker will retry). The earlier biconditional
  -- `(status='failed') = (error is not null)` forbade that operational pattern.
  constraint nd_failed_chk check (
    case status
      when 'failed' then error is not null
      when 'sent'   then error is null
      when 'read'   then error is null
      else true   -- queued: error is optional (carries last transient error during retry)
    end
  ),
  -- read implies the row was delivered first.
  constraint nd_read_chk check (
    read_at is null or delivered_at is not null
  )
);

-- Event -> deliveries join index, used when the UI expands "what channels
-- fired for this event?" and when cascading deletes traverse the FK.
create index notification_deliveries_event_idx on notification_deliveries(notification_event_id);
-- FK-side index on destination_id so webhook deletes don't require a scan.
-- Partial on non-null to stay small (only webhook rows populate it).
create index notification_deliveries_destination_idx on notification_deliveries(destination_id) where destination_id is not null;
-- Worker queue index: "give me queued or failed deliveries for tenant T".
-- Partial on in-flight statuses keeps it narrow — sent/read rows are terminal
-- from the worker's perspective and shouldn't be re-examined.
create index notification_deliveries_status_idx on notification_deliveries(tenant_id, status) where status in ('queued', 'failed');

-- Notification preferences: per-user per-event per-channel routing override.
-- UNIQUE (tenant_id, user_id, event_kind, channel) so a user has at most one
-- preference row for a given (event, channel) pair. Uniqueness leads with
-- tenant_id to keep per-tenant scoping explicit. The user FK is plain
-- users(id); tenant scoping is enforced by the row's own tenant_id FK to
-- tenants(id). `digest_override` lets a user set e.g. daily digest for
-- 'budget_overrun' email while the rule default is realtime. `enabled=false`
-- opts out of that specific (event, channel) combination.
create table notification_preferences (
  id              uuid primary key,
  tenant_id       uuid not null references tenants(id) on delete cascade,
  user_id         uuid not null,
  event_kind      text not null,
  channel         notification_channel not null,
  enabled         bool not null default true,
  digest_override notification_digest_mode,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, user_id, event_kind, channel),
  constraint np_user_fk foreign key (user_id)
    references users(id) on delete cascade
);

create index notification_preferences_user_id_idx on notification_preferences (user_id);

create trigger notification_preferences_updated_at before update on notification_preferences
  for each row execute function set_updated_at();
