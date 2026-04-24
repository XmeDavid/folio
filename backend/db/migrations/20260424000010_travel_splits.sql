-- Folio v2 domain — travel, split bills, receivables (spec §5.10).
-- Introduces the "shared-economy" slice: trips (with multi-destination,
-- dated windows, and per-category budgets), free-text people (non-app-user
-- counterparties like friends/family/colleagues), split bills (either
-- trip-bound or standalone), receivables (money owed in either direction),
-- settlements (partial/full repayments), and reimbursement claims
-- (employer / third-party refunds). Also back-fills the deferred FK on
-- `cycle_plan_lines.trip_id` so `trip_budget`-kind plan lines actually
-- point at a real trip.
--
-- Shape rules threaded throughout:
--   * trip_participants XORs `is_self` and `person_id` — a row is either
--     the user themselves or a named non-user person, never both/neither.
--   * split_bill_allocations XORs `participant_trip_id` and `person_id` —
--     allocation targets a trip participant OR a free-standing person.
--   * trip_budgets / trip_transaction_links enforce the iff relationship
--     between `category='custom'` and a non-null `custom_label`.
--   * split_bill_events.state='settled' is biconditional with settled_at.
--   * receivables.status='settled' is biconditional with settled_at.
--   * reimbursement_claims.claim_status='paid' is biconditional with
--     BOTH paid_at AND paid_transaction_id (stronger than a single flag).

-- Trip lifecycle: `planned` is the default (ideas, early planning);
-- `active` covers the live window; `completed` is post-trip reconciliation;
-- `cancelled` preserves the record without treating it as real spend.
create type trip_status as enum ('planned', 'active', 'completed', 'cancelled');

-- Trip category taxonomy (spec §5.10). First-class categories cover the
-- common trip cost buckets; `custom` is the escape hatch and requires a
-- non-null `custom_label` (enforced on both trip_budgets and
-- trip_transaction_links).
create type trip_category as enum (
  'flights', 'accommodation', 'food', 'activities',
  'transport', 'shopping', 'other', 'custom'
);

-- Default share rule for a trip participant when a new split bill is
-- created. `equal` is the canonical case; `zero` excludes them from the
-- default allocation (e.g. partner joined mid-trip); `custom` forces the
-- UI to prompt.
create type trip_participant_share_default as enum ('equal', 'zero', 'custom');

-- Allocation method for a split bill (spec §5.10). `equal` divides
-- total_amount evenly; `fixed_amounts` lets each allocation set its own
-- amount_owed; `percentages` is a weighted share; `by_items` ties
-- allocations to individual transaction_lines.
create type split_allocation_method as enum ('equal', 'fixed_amounts', 'percentages', 'by_items');

-- Split bill lifecycle. `open` = outstanding; `settled` = all allocations
-- fully repaid (biconditional with settled_at).
create type split_bill_state as enum ('open', 'settled');

-- Direction of a receivable from the tenant's perspective. `i_am_owed`
-- means the counterparty owes the user; `i_owe` is the reverse.
create type receivable_direction as enum ('i_am_owed', 'i_owe');

-- How a receivable came to exist. `split_bill` is the dominant origin;
-- `reimbursement` wraps a reimbursement_claim; `manual_loan` is user-entered
-- IOUs; `refund_pending` covers merchandise returns awaiting credit.
create type receivable_origin as enum ('split_bill', 'reimbursement', 'manual_loan', 'refund_pending');

-- Receivable lifecycle. `partially_settled` means settlements exist but
-- sum < amount. `written_off` is a terminal non-payment state (bad debt).
create type receivable_status as enum ('open', 'partially_settled', 'settled', 'written_off');

-- Reimbursement claim lifecycle. `submitted` records the hand-off to the
-- payer; `approved` is intermediate; `paid` requires BOTH paid_at and
-- paid_transaction_id (enforced by rc_paid_chk); `rejected` is terminal.
create type reimbursement_claim_status as enum ('draft', 'submitted', 'approved', 'paid', 'rejected');

-- People: free-text counterparties (friends, family, colleagues) who are
-- NOT app users. Distinct from `users` which is the auth principal table.
-- Unique on (tenant_id, name) prevents duplicate Alices; archived_at is
-- the soft-delete marker so historical receivables keep their target.
create table people (
  id           uuid primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  name         text not null,
  email        citext,
  phone        text,
  notes        text,
  archived_at  timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, name)
);

create trigger people_updated_at before update on people
  for each row execute function set_updated_at();

-- Partial index: the UI mostly queries active (non-archived) people.
create index people_tenant_active_idx on people(tenant_id) where archived_at is null;

-- Trips: the top-level travel record. `destinations` is a text[] so a
-- single trip can span multiple cities without forcing a child table.
-- `overall_budget` is a convenience ceiling; per-category breakdowns live
-- in trip_budgets. Date range is half-inclusive/inclusive (>=) since a
-- single-day trip is legitimate.
create table trips (
  id               uuid primary key,
  tenant_id        uuid not null references tenants(id) on delete cascade,
  name             text not null,
  destinations     text[] not null default '{}',
  start_date       date not null,
  end_date         date not null,
  overall_budget   numeric(28,8),
  currency         money_currency not null,
  status           trip_status not null default 'planned',
  notes            text,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  unique (tenant_id, id),
  check (end_date >= start_date),
  check (overall_budget is null or overall_budget >= 0)
);

create trigger trips_updated_at before update on trips
  for each row execute function set_updated_at();

create index trips_tenant_date_idx on trips(tenant_id, start_date desc);
create index trips_tenant_status_idx on trips(tenant_id, status);

-- Back-fill the deferred composite FK on cycle_plan_lines.trip_id. The
-- column was declared in 006_planning.sql without a FK because trips did
-- not yet exist. `on delete set null` preserves the plan line if the trip
-- is deleted — the line becomes a plain flexible budget.
alter table cycle_plan_lines add constraint cpl_trip_fk
  foreign key (tenant_id, trip_id) references trips(tenant_id, id)
  on delete set null;

-- Trip budgets: per-category caps within a trip. Unique on
-- (trip_id, category, custom_label) so each category appears at most
-- once per trip (custom gets a second dimension via custom_label).
-- tb_custom_label_chk enforces the iff relationship between
-- `category='custom'` and a non-null custom_label.
create table trip_budgets (
  id              uuid primary key,
  tenant_id       uuid not null references tenants(id) on delete cascade,
  trip_id         uuid not null,
  category        trip_category not null,
  custom_label    text,
  budget_amount   numeric(28,8) not null,
  currency        money_currency not null,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  unique (tenant_id, id),
  -- Per-trip uniqueness: one row per (trip, category, custom_label).
  -- NULLS NOT DISTINCT (Postgres 15+) is required so two rows with
  -- custom_label=NULL on the same (trip, category) collide. The default
  -- NULLS-are-distinct semantics would silently allow duplicate non-custom
  -- budgets (both `food` rows with NULL label would pass), defeating the
  -- intended "one budget per (trip, category)" invariant.
  unique nulls not distinct (trip_id, category, custom_label),
  constraint tb_trip_fk foreign key (tenant_id, trip_id)
    references trips(tenant_id, id) on delete cascade,
  check (budget_amount >= 0),
  -- custom_label required iff category='custom'
  constraint tb_custom_label_chk check (
    (category = 'custom' and custom_label is not null) or
    (category <> 'custom' and custom_label is null)
  )
);

create trigger trip_budgets_updated_at before update on trip_budgets
  for each row execute function set_updated_at();

-- Trip participants: who's on the trip. XOR via tp_self_or_person_chk —
-- a row represents either the user themselves (`is_self=true`,
-- `person_id` null) OR a named non-user (`is_self=false`, `person_id`
-- not null). Both-set or neither-set are rejected.
-- `display_name` lets the UI show "Me" or a nickname without modifying
-- the underlying `people.name`.
create table trip_participants (
  id              uuid primary key,
  tenant_id       uuid not null references tenants(id) on delete cascade,
  trip_id         uuid not null,
  person_id       uuid,                            -- null when is_self
  is_self         bool not null default false,
  display_name    text not null,
  share_default   trip_participant_share_default not null default 'equal',
  created_at      timestamptz not null default now(),
  unique (tenant_id, id),
  constraint tp_trip_fk foreign key (tenant_id, trip_id)
    references trips(tenant_id, id) on delete cascade,
  constraint tp_person_fk foreign key (tenant_id, person_id)
    references people(tenant_id, id) on delete cascade,
  -- XOR: either is_self or person_id, not both, not neither
  constraint tp_self_or_person_chk check (
    (person_id is not null) <> is_self
  )
);

create index trip_participants_trip_idx on trip_participants(trip_id);
-- Uniqueness guards against duplicate participants per trip:
--   self_idx: at most one `is_self=true` row per trip (no "me, twice").
--   person_idx: at most one row per (trip, person_id) — no duplicate
--   Alices. Serves double-duty as the FK-side index for cascades from
--   people (and supersedes the non-unique form).
create unique index trip_participants_self_idx
  on trip_participants(trip_id) where is_self;
create unique index trip_participants_person_idx
  on trip_participants(trip_id, person_id) where person_id is not null;

-- Trip transaction links: tags an existing transaction as part of a trip
-- under a specific trip_category. Composite PK (trip_id, transaction_id)
-- prevents duplicate tags for the same pair. ttl_custom_label_chk mirrors
-- trip_budgets — custom_label required iff trip_category='custom'.
create table trip_transaction_links (
  trip_id         uuid not null,
  transaction_id  uuid not null,
  tenant_id       uuid not null references tenants(id) on delete cascade,
  trip_category   trip_category not null,
  custom_label    text,
  created_at      timestamptz not null default now(),
  primary key (trip_id, transaction_id),
  constraint ttl_trip_fk foreign key (tenant_id, trip_id)
    references trips(tenant_id, id) on delete cascade,
  constraint ttl_txn_fk foreign key (tenant_id, transaction_id)
    references transactions(tenant_id, id) on delete cascade,
  constraint ttl_custom_label_chk check (
    (trip_category = 'custom' and custom_label is not null) or
    (trip_category <> 'custom' and custom_label is null)
  )
);

-- FK-side index: the PK covers (trip_id, transaction_id) lookups; this
-- index supports the reverse lookup ("what trip is this txn on?") and
-- cascades from transactions.
create index trip_transaction_links_txn_idx on trip_transaction_links(transaction_id);

-- Split bill events: the "who paid what" record. Either trip-bound
-- (`trip_id` set) or standalone (both refs null). `transaction_id` links
-- to the real payment once it materializes; until then the split is
-- conceptual. sbe_settled_chk enforces the biconditional between
-- state='settled' and settled_at.
create table split_bill_events (
  id                   uuid primary key,
  tenant_id            uuid not null references tenants(id) on delete cascade,
  transaction_id       uuid,                -- null if not yet linked to real txn
  trip_id              uuid,                -- null if standalone
  total_amount         numeric(28,8) not null,
  currency             money_currency not null,
  allocation_method    split_allocation_method not null,
  note                 text,
  state                split_bill_state not null default 'open',
  settled_at           timestamptz,
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  unique (tenant_id, id),
  constraint sbe_txn_fk foreign key (tenant_id, transaction_id)
    references transactions(tenant_id, id) on delete set null,
  constraint sbe_trip_fk foreign key (tenant_id, trip_id)
    references trips(tenant_id, id) on delete set null,
  check (total_amount > 0),
  -- settled state requires settled_at
  constraint sbe_settled_chk check (
    (state = 'settled') = (settled_at is not null)
  )
);

create trigger split_bill_events_updated_at before update on split_bill_events
  for each row execute function set_updated_at();

create index split_bill_events_txn_idx on split_bill_events(transaction_id) where transaction_id is not null;
create index split_bill_events_trip_idx on split_bill_events(trip_id) where trip_id is not null;
create index split_bill_events_state_idx on split_bill_events(tenant_id, state);

-- Split bill allocations: each participant's share of a split bill. XOR
-- via sba_participant_or_person_chk — the allocation targets either a
-- trip participant (trip scenario) or a free-standing person (standalone
-- split), never both. `item_description` and `transaction_line_id` support
-- allocation_method='by_items' where each allocation pairs with a line.
create table split_bill_allocations (
  id                        uuid primary key,
  tenant_id                 uuid not null references tenants(id) on delete cascade,
  split_bill_event_id       uuid not null,
  participant_trip_id       uuid,                    -- trip_participants.id (trip scenario)
  person_id                 uuid,                    -- people.id (free-standing scenario)
  amount_owed               numeric(28,8) not null,
  currency                  money_currency not null,
  item_description          text,
  transaction_line_id       uuid,
  created_at                timestamptz not null default now(),
  unique (tenant_id, id),
  constraint sba_event_fk foreign key (tenant_id, split_bill_event_id)
    references split_bill_events(tenant_id, id) on delete cascade,
  constraint sba_participant_fk foreign key (tenant_id, participant_trip_id)
    references trip_participants(tenant_id, id) on delete cascade,
  constraint sba_person_fk foreign key (tenant_id, person_id)
    references people(tenant_id, id) on delete cascade,
  constraint sba_txn_line_fk foreign key (tenant_id, transaction_line_id)
    references transaction_lines(tenant_id, id) on delete set null,
  check (amount_owed > 0),
  -- Exactly one of participant_trip_id / person_id
  constraint sba_participant_or_person_chk check (
    (participant_trip_id is not null) <> (person_id is not null)
  )
);

create index split_bill_allocations_event_idx on split_bill_allocations(split_bill_event_id);
create index split_bill_allocations_participant_idx on split_bill_allocations(participant_trip_id) where participant_trip_id is not null;
create index split_bill_allocations_person_idx on split_bill_allocations(person_id) where person_id is not null;
create index split_bill_allocations_line_idx on split_bill_allocations(transaction_line_id) where transaction_line_id is not null;

-- Receivables: the unified "money owed" ledger. `counterparty_person_id`
-- is optional (on_delete_set_null) so archiving a person leaves the
-- receivable intact with `counterparty_label` as the human-readable fallback.
-- `direction` captures which way the money flows. `origin` and
-- `origin_event_id` pin the receivable to its source (a split bill, a
-- reimbursement claim, or free-form). rcv_settled_chk enforces biconditional.
create table receivables (
  id                       uuid primary key,
  tenant_id                uuid not null references tenants(id) on delete cascade,
  counterparty_person_id   uuid,
  counterparty_label       text not null,
  direction                receivable_direction not null,
  amount                   numeric(28,8) not null,
  currency                 money_currency not null,
  origin                   receivable_origin not null,
  origin_event_id          uuid,
  due_date                 date,
  status                   receivable_status not null default 'open',
  notes                    text,
  settled_at               timestamptz,
  created_at               timestamptz not null default now(),
  updated_at               timestamptz not null default now(),
  unique (tenant_id, id),
  constraint rcv_person_fk foreign key (tenant_id, counterparty_person_id)
    references people(tenant_id, id) on delete set null,
  check (amount > 0),
  -- settled status requires settled_at
  constraint rcv_settled_chk check (
    (status = 'settled') = (settled_at is not null)
  )
);

create trigger receivables_updated_at before update on receivables
  for each row execute function set_updated_at();

-- Partial indexes: the UI asks "what's still outstanding?" (open +
-- partially_settled) far more often than it asks for historical totals.
create index receivables_tenant_open_idx on receivables(tenant_id, status) where status in ('open', 'partially_settled');
create index receivables_person_idx on receivables(counterparty_person_id) where counterparty_person_id is not null;

-- Settlements: partial/full repayments against a receivable. Append-only
-- log (no updated_at). `settling_transaction_id` links to a real txn when
-- the repayment lands in the ledger; null means off-ledger (cash, etc).
-- The receivable.status transitions to partially_settled / settled based
-- on sum(settlements.amount); that rollup is service-layer logic.
create table settlements (
  id                       uuid primary key,
  tenant_id                uuid not null references tenants(id) on delete cascade,
  receivable_id            uuid not null,
  settling_transaction_id  uuid,
  amount                   numeric(28,8) not null,
  currency                 money_currency not null,
  settled_at               timestamptz not null default now(),
  note                     text,
  created_at               timestamptz not null default now(),
  unique (tenant_id, id),
  constraint settlements_receivable_fk foreign key (tenant_id, receivable_id)
    references receivables(tenant_id, id) on delete cascade,
  constraint settlements_txn_fk foreign key (tenant_id, settling_transaction_id)
    references transactions(tenant_id, id) on delete set null,
  check (amount > 0)
);

create index settlements_receivable_idx on settlements(receivable_id, settled_at desc);
create index settlements_txn_idx on settlements(settling_transaction_id) where settling_transaction_id is not null;

-- Reimbursement claims: employer/third-party refunds. `transaction_id` is
-- the original expense being claimed; `paid_transaction_id` is the inbound
-- refund transaction once received. rc_paid_chk enforces a stronger
-- biconditional: claim_status='paid' iff BOTH paid_at and
-- paid_transaction_id are set (either one alone is insufficient — paid
-- with no backing txn or a refund txn without a status update are both
-- rejected).
create table reimbursement_claims (
  id                          uuid primary key,
  tenant_id                   uuid not null references tenants(id) on delete cascade,
  transaction_id              uuid not null,
  employer_or_counterparty    text not null,
  claim_status                reimbursement_claim_status not null default 'draft',
  submitted_at                timestamptz,
  paid_at                     timestamptz,
  paid_transaction_id         uuid,
  notes                       text,
  created_at                  timestamptz not null default now(),
  updated_at                  timestamptz not null default now(),
  unique (tenant_id, id),
  constraint rc_txn_fk foreign key (tenant_id, transaction_id)
    references transactions(tenant_id, id) on delete cascade,
  constraint rc_paid_txn_fk foreign key (tenant_id, paid_transaction_id)
    references transactions(tenant_id, id) on delete set null,
  -- paid status requires paid_at AND paid_transaction_id
  constraint rc_paid_chk check (
    (claim_status = 'paid') = (paid_at is not null and paid_transaction_id is not null)
  ),
  -- Non-paid statuses must have both paid columns null. Without this,
  -- the biconditional above is satisfied by paid_at+paid_transaction_id
  -- being fully set OR fully absent — but "approved with paid_at only"
  -- would leak through because it doesn't satisfy the RHS (which requires
  -- BOTH). This second constraint forbids stale paid_at / paid_transaction_id
  -- on any non-paid status (approved/rejected/submitted/draft).
  constraint rc_paid_columns_chk check (
    claim_status = 'paid' or (paid_at is null and paid_transaction_id is null)
  )
);

create trigger reimbursement_claims_updated_at before update on reimbursement_claims
  for each row execute function set_updated_at();

create index reimbursement_claims_txn_idx on reimbursement_claims(transaction_id);
create index reimbursement_claims_status_idx on reimbursement_claims(tenant_id, claim_status);
