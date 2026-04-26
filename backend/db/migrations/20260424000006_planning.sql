-- Folio v2 domain — planning.
-- Introduces payment cycles (the user-configurable planning period), income
-- sources (salary & variable inflows), recurring templates (rent, utilities,
-- subscriptions, savings rules), per-cycle plans with budget lines, and
-- planned events (the materialised expectations matched against actual
-- transactions). Also: action items (the "do this now" todo list driven from
-- planned events) and rollover policies (how leftover or overspent category
-- budgets carry into the next cycle). Finally relocates
-- `planned_event_matches` here — it needs `planned_events` (this file) and
-- `transactions` (004), so it cannot live in either earlier migration.
--
-- Deferred FKs: `cycle_plan_lines.goal_id` (goals in 007) and
-- `cycle_plan_lines.trip_id` (trips in 010) are declared as plain uuid here;
-- their composite FKs get attached via `alter table` in those later
-- migrations.

-- Cycle anchor (spec §5.6): determines how the user slices time. Monthly on
-- a specific day is the common case ("my salary lands on the 25th"), but
-- biweekly and weekly cadences exist for hourly / contractor users. Custom
-- is an escape hatch for odd schedules.
create type cycle_anchor_kind as enum ('monthly_day', 'biweekly', 'weekly', 'custom');

-- Cycle lifecycle: upcoming cycles are visible but not yet the active plan;
-- exactly zero-or-one cycle is `active` at a time; `closed` cycles are
-- historical and drive retrospectives.
create type cycle_status as enum ('upcoming', 'active', 'closed');

-- Recurring template kinds (spec §5.6). `investment_contribution` is a
-- scheduled buy (e.g. DCA); `subscription` is broken out so the UI can show
-- a "cancel" list independent from regular expenses.
create type recurring_template_kind as enum (
  'income', 'expense', 'transfer', 'investment_contribution', 'subscription'
);

-- How a template's amount scales. `percentage_of_income` is the primary
-- mechanism for savings rules ("save 20% of net income").
create type recurring_amount_type as enum ('fixed', 'percentage_of_income');

-- Plan lifecycle mirrors cycle lifecycle but can lag: a cycle may be active
-- with its plan still in draft while the user is still tweaking lines.
create type cycle_plan_status as enum ('draft', 'active', 'closed');

-- Line taxonomy (spec §5.6). `flexible_budget` = variable category cap
-- ("Groceries: 600"); `one_off` = planned expense just for this cycle;
-- `savings_rule` = materialised from a percentage template; `trip_budget`
-- points at a future trip (Task 11).
create type cycle_plan_line_kind as enum (
  'expected_income', 'recurring_expense', 'flexible_budget',
  'one_off', 'savings_rule', 'planned_investment', 'trip_budget'
);

-- Planned-event lifecycle (spec §5.6). `planned` is the default; `scheduled`
-- means a concrete calendar date was picked (action item generated);
-- `executed` means matched to an actual transaction; `skipped` / `cancelled`
-- are final without matching.
create type planned_event_status as enum (
  'planned', 'scheduled', 'executed', 'skipped', 'cancelled'
);

-- Action item status. `dismissed` is the "I saw this and don't care" state,
-- distinct from `skipped` (acted, chose not to execute) and `done`.
create type action_item_status as enum ('pending', 'done', 'skipped', 'dismissed');

-- Rollover strategy per category. `reset` = fresh budget each cycle;
-- `rollover` = unused budget accumulates; `rollover_with_cap` = accumulates
-- up to a ceiling (prevents runaway "savings" in a category).
create type rollover_behavior as enum ('reset', 'rollover', 'rollover_with_cap');

-- What to do when a category overspends. `absorb_to_next_cycle` reduces
-- next cycle's budget; `zero_out` wipes the deficit (no carryover).
create type overspend_behavior as enum ('absorb_to_next_cycle', 'zero_out');

-- Income source behaviour. `fixed` = "salary of 6000 net each month";
-- `variable` = freelance / commission income where the expected amount is
-- a forecast.
create type income_amount_type as enum ('fixed', 'variable');

-- Whether an income_source's expected_amount is expressed pre-tax or
-- post-tax. Drives display and savings-rate math.
create type tax_hint as enum ('gross', 'net');

-- Payment cycles: the planning period. `period_start`/`period_end` are
-- inclusive/exclusive dates (end > start enforced). `anchor` records how
-- the cycle was generated so future cycles can extend the same cadence.
-- Unique on (workspace_id, period_start) prevents overlapping duplicates for
-- a single workspace.
create table payment_cycles (
  id            uuid primary key,
  workspace_id     uuid not null references workspaces(id) on delete cascade,
  period_start  date not null,
  period_end    date not null,
  anchor        cycle_anchor_kind not null,
  label         text not null,
  status        cycle_status not null default 'upcoming',
  closed_at     timestamptz,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, period_start),
  check (period_end > period_start)
);

create trigger payment_cycles_updated_at before update on payment_cycles
  for each row execute function set_updated_at();

create index payment_cycles_workspace_status_idx on payment_cycles(workspace_id, status);

-- Income sources: salary and other recurring inflows the user wants to
-- forecast. `cadence` is a jsonb schedule definition (rrule-like). `tax_hint`
-- is optional because freelance sources may not map cleanly to gross/net.
create table income_sources (
  id                uuid primary key,
  workspace_id         uuid not null references workspaces(id) on delete cascade,
  name              text not null,
  account_id        uuid not null,
  amount_type       income_amount_type not null,
  expected_amount   numeric(28,8) not null,
  currency          money_currency not null,
  cadence           jsonb not null,
  tax_hint          tax_hint,
  notes             text,
  archived_at       timestamptz,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  unique (workspace_id, id),
  constraint income_sources_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade
);

create trigger income_sources_updated_at before update on income_sources
  for each row execute function set_updated_at();

-- Partial index: the UI mostly queries active (non-archived) sources.
create index income_sources_workspace_active_idx on income_sources(workspace_id)
  where archived_at is null;
create index income_sources_account_idx on income_sources(account_id);

-- Recurring templates: rent, utilities, subscriptions, savings rules. Mixed
-- amount_type / amount / percentage shape guarded by rt_amount_shape_chk —
-- exactly one of (amount, percentage) must be set based on amount_type.
-- `dest_account_id` is used by transfer-kind templates (e.g. monthly
-- automatic move to savings). `share_percentage` is for shared expenses
-- (e.g. 50% of rent is mine).
create table recurring_templates (
  id                   uuid primary key,
  workspace_id            uuid not null references workspaces(id) on delete cascade,
  kind                 recurring_template_kind not null,
  name                 text not null,
  account_id           uuid not null,
  dest_account_id      uuid,
  category_id          uuid,
  merchant_id          uuid,
  amount_type          recurring_amount_type not null,
  amount               numeric(28,8),
  percentage           numeric(5,2),
  currency             money_currency not null,
  cadence              jsonb not null,
  start_date           date not null,
  end_date             date,
  share_percentage     numeric(5,2),
  cancel_url           text,
  notes                text,
  archived_at          timestamptz,
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  unique (workspace_id, id),
  constraint rt_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade,
  constraint rt_dest_account_fk foreign key (workspace_id, dest_account_id)
    references accounts(workspace_id, id) on delete set null,
  constraint rt_category_fk foreign key (workspace_id, category_id)
    references categories(workspace_id, id) on delete set null,
  constraint rt_merchant_fk foreign key (workspace_id, merchant_id)
    references merchants(workspace_id, id) on delete set null,
  -- amount vs percentage pair matches amount_type
  constraint rt_amount_shape_chk check (
    (amount_type = 'fixed'                 and amount is not null     and percentage is null) or
    (amount_type = 'percentage_of_income'  and percentage is not null and amount is null)
  ),
  -- Percentage bounds. `percentage` is the share-of-income driver;
  -- `share_percentage` is the fraction the user personally owes of a shared
  -- expense. Both must be between 0 and 100 when present.
  constraint rt_percentage_range_chk check (
    percentage is null or (percentage >= 0 and percentage <= 100)
  ),
  constraint rt_share_percentage_range_chk check (
    share_percentage is null or (share_percentage >= 0 and share_percentage <= 100)
  ),
  -- start/end date sanity. Uses `>=` (not `>`) so same-day start/end is
  -- legal — a one-day template is a legitimate edge case (cancelled
  -- subscription, trial period). Contrast with `payment_cycles.check`
  -- which uses `>` because a zero-day cycle is nonsensical.
  constraint rt_date_order_chk check (end_date is null or end_date >= start_date)
);

create trigger recurring_templates_updated_at before update on recurring_templates
  for each row execute function set_updated_at();

create index recurring_templates_workspace_active_idx on recurring_templates(workspace_id)
  where archived_at is null;
create index recurring_templates_account_idx on recurring_templates(account_id);
create index recurring_templates_category_idx on recurring_templates(category_id) where category_id is not null;

-- Cycle plans: a plan is 1:1 with a payment_cycle. `summary` is a denormalised
-- rollup (totals, savings rate forecast) recomputed on line change. The
-- global unique on payment_cycle_id enforces the 1:1 relationship without
-- needing a workspace scope — cycles themselves are workspace-scoped so this is
-- safe.
create table cycle_plans (
  id               uuid primary key,
  workspace_id        uuid not null references workspaces(id) on delete cascade,
  payment_cycle_id uuid not null unique,
  status           cycle_plan_status not null default 'draft',
  summary          jsonb not null default '{}'::jsonb,
  closed_at        timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  unique (workspace_id, id),
  constraint cp_cycle_fk foreign key (workspace_id, payment_cycle_id)
    references payment_cycles(workspace_id, id) on delete cascade
);

create trigger cycle_plans_updated_at before update on cycle_plans
  for each row execute function set_updated_at();

create index cycle_plans_workspace_status_idx on cycle_plans(workspace_id, status);

-- Cycle plan lines: individual rows inside a plan. Each line is one of
-- several kinds and may reference a category, a recurring template, an
-- income source, a goal, or a trip. Append-mostly (no updated_at trigger);
-- edits typically produce a new line or a revised plan snapshot.
--
-- NOTE: goal_id and trip_id are declared here as plain uuid; composite FKs
-- are added by later migrations (007_goals.sql, 010_travel_splits.sql) via
-- `alter table`. Until those migrations run, writes to these columns are
-- not structurally prevented — the service layer must not populate them
-- before the referenced migrations are in place. This is a v1
-- development-phase concern, not a production one.
--
-- Kind-to-reference-column coherence (which ref column must be set for each
-- `kind`) is enforced by the service layer. A per-kind CHECK could be
-- added but the spec taxonomy leaves room for multiple references per kind
-- (e.g. `savings_rule` can reference both goal and recurring_template).
create table cycle_plan_lines (
  id                    uuid primary key,
  workspace_id             uuid not null references workspaces(id) on delete cascade,
  cycle_plan_id         uuid not null,
  kind                  cycle_plan_line_kind not null,
  category_id           uuid,
  recurring_template_id uuid,
  income_source_id      uuid,
  -- goal_id / trip_id carry NO FK at this migration; composite FKs are
  -- added by Tasks 8 (007_goals.sql) and 11 (010_travel_splits.sql) via
  -- ALTER TABLE. Service layer must not populate until those land.
  goal_id               uuid,   -- FK added in 007_goals.sql
  trip_id               uuid,   -- FK added in 010_travel_splits.sql
  planned_amount        numeric(28,8) not null,
  currency              money_currency not null,
  note                  text,
  sort_order            int not null default 0,
  created_at            timestamptz not null default now(),
  unique (workspace_id, id),
  constraint cpl_plan_fk foreign key (workspace_id, cycle_plan_id)
    references cycle_plans(workspace_id, id) on delete cascade,
  constraint cpl_category_fk foreign key (workspace_id, category_id)
    references categories(workspace_id, id) on delete set null,
  constraint cpl_template_fk foreign key (workspace_id, recurring_template_id)
    references recurring_templates(workspace_id, id) on delete set null,
  constraint cpl_income_fk foreign key (workspace_id, income_source_id)
    references income_sources(workspace_id, id) on delete set null
);

create index cycle_plan_lines_plan_idx on cycle_plan_lines(cycle_plan_id, sort_order);
create index cycle_plan_lines_template_idx on cycle_plan_lines(recurring_template_id) where recurring_template_id is not null;
create index cycle_plan_lines_goal_idx on cycle_plan_lines(goal_id) where goal_id is not null;
create index cycle_plan_lines_trip_idx on cycle_plan_lines(trip_id) where trip_id is not null;

-- Planned events: the materialised expectations. A template + cadence
-- produces many planned events (one per expected occurrence in the cycle).
-- `executed_transaction_id` links to the matched actual transaction;
-- planned_events_executed_chk enforces that status='executed' always has
-- a backing transaction.
create table planned_events (
  id                        uuid primary key,
  workspace_id                 uuid not null references workspaces(id) on delete cascade,
  cycle_plan_line_id        uuid,
  recurring_template_id     uuid,
  kind                      recurring_template_kind not null,
  account_id                uuid not null,
  dest_account_id           uuid,
  category_id               uuid,
  merchant_id               uuid,
  planned_for               date not null,
  amount                    numeric(28,8) not null,
  currency                  money_currency not null,
  status                    planned_event_status not null default 'planned',
  executed_transaction_id   uuid,
  created_at                timestamptz not null default now(),
  updated_at                timestamptz not null default now(),
  unique (workspace_id, id),
  constraint pe_cpl_fk foreign key (workspace_id, cycle_plan_line_id)
    references cycle_plan_lines(workspace_id, id) on delete set null,
  constraint pe_template_fk foreign key (workspace_id, recurring_template_id)
    references recurring_templates(workspace_id, id) on delete set null,
  constraint pe_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade,
  constraint pe_dest_account_fk foreign key (workspace_id, dest_account_id)
    references accounts(workspace_id, id) on delete set null,
  constraint pe_category_fk foreign key (workspace_id, category_id)
    references categories(workspace_id, id) on delete set null,
  constraint pe_merchant_fk foreign key (workspace_id, merchant_id)
    references merchants(workspace_id, id) on delete set null,
  constraint pe_executed_fk foreign key (workspace_id, executed_transaction_id)
    references transactions(workspace_id, id) on delete set null,
  -- Biconditional: executed_transaction_id is set iff status='executed'.
  -- Rejects both missing-txn-on-executed and spurious-txn-on-cancelled/skipped.
  constraint planned_events_executed_chk check (
    (status = 'executed') = (executed_transaction_id is not null)
  )
);

create trigger planned_events_updated_at before update on planned_events
  for each row execute function set_updated_at();

create index planned_events_workspace_date_idx on planned_events(workspace_id, planned_for);
create index planned_events_status_idx on planned_events(workspace_id, status);
create index planned_events_account_idx on planned_events(account_id, planned_for);
-- FK-side indexes: without these, `on delete set null` cascades from
-- cycle_plan_lines and recurring_templates do sequential scans on
-- planned_events.
create index planned_events_cpl_idx on planned_events(cycle_plan_line_id)
  where cycle_plan_line_id is not null;
create index planned_events_template_idx on planned_events(recurring_template_id)
  where recurring_template_id is not null;

-- Action items: actionable reminders derived from planned events ("pay rent
-- on the 1st"). Partial index on (workspace_id, due_at) WHERE pending makes
-- the "what's due today" query cheap and tight.
create table action_items (
  id                 uuid primary key,
  workspace_id          uuid not null references workspaces(id) on delete cascade,
  planned_event_id   uuid not null,
  instruction        text not null,
  due_at             date not null,
  status             action_item_status not null default 'pending',
  done_at            timestamptz,
  notes              text,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now(),
  unique (workspace_id, id),
  constraint ai_planned_event_fk foreign key (workspace_id, planned_event_id)
    references planned_events(workspace_id, id) on delete cascade
);

create trigger action_items_updated_at before update on action_items
  for each row execute function set_updated_at();

create index action_items_workspace_due_idx on action_items(workspace_id, due_at)
  where status = 'pending';
create index action_items_planned_event_idx on action_items(planned_event_id);

-- Rollover policies: one per category per workspace (enforced by unique
-- constraint). Two guards:
--   rp_cap_pair_chk: cap_amount and cap_currency must be NULL together or
--   both set (a cap without a currency or vice-versa is malformed).
--   rp_cap_requires_behavior_chk: a cap is only meaningful with
--   `rollover_with_cap`; other behaviors must leave cap NULL.
create table rollover_policies (
  id            uuid primary key,
  workspace_id     uuid not null references workspaces(id) on delete cascade,
  category_id   uuid not null,
  behavior      rollover_behavior not null default 'reset',
  cap_amount    numeric(28,8),
  cap_currency  money_currency,
  overspend     overspend_behavior not null default 'absorb_to_next_cycle',
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, category_id),
  constraint rp_category_fk foreign key (workspace_id, category_id)
    references categories(workspace_id, id) on delete cascade,
  -- cap_amount and cap_currency must be NULL together
  constraint rp_cap_pair_chk check (
    (cap_amount is null) = (cap_currency is null)
  ),
  -- cap only makes sense with rollover_with_cap
  constraint rp_cap_requires_behavior_chk check (
    (behavior = 'rollover_with_cap' and cap_amount is not null) or
    (behavior <> 'rollover_with_cap' and cap_amount is null)
  )
);

create trigger rollover_policies_updated_at before update on rollover_policies
  for each row execute function set_updated_at();

-- planned_event_matches: links a planned event to the actual transaction
-- that realised it. Relocated here from 004 because it depends on
-- planned_events (this file) and transactions (004). Append-only (no
-- updated_at) — if the match is wrong you delete and create a new one.
-- Unique on (workspace_id, planned_event_id, transaction_id) prevents
-- duplicate match records for the same pair.
create table planned_event_matches (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  planned_event_id    uuid not null,
  transaction_id      uuid not null,
  provenance          match_provenance not null,
  matched_at          timestamptz not null default now(),
  matched_by_user_id  uuid,
  created_at          timestamptz not null default now(),
  unique (workspace_id, id),
  unique (workspace_id, planned_event_id, transaction_id),
  constraint pem_event_fk foreign key (workspace_id, planned_event_id)
    references planned_events(workspace_id, id) on delete cascade,
  constraint pem_txn_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint pem_actor_fk foreign key (matched_by_user_id)
    references users(id) on delete set null
);

create index planned_event_matches_event_idx on planned_event_matches(planned_event_id);
create index planned_event_matches_txn_idx on planned_event_matches(transaction_id);
