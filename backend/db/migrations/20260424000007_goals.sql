-- Folio v2 domain — goals, buckets, and savings rules.
-- Introduces hierarchical goals with auto-redirect-on-reach, goal-to-account
-- bucketing with optional share splits (multi-goal savings accounts),
-- append-only goal allocation snapshots, goal contributions (linked to
-- either an actual transaction or a planned event — never both), and
-- priority-ordered savings rules. Also back-fills the deferred composite FK
-- on cycle_plan_lines.goal_id now that goals exists.

-- Goal lifecycle (spec §5.7). `active` goals accrue allocations and appear
-- in savings recompute; `paused` goals skip rule-driven allocation but keep
-- their balances; `reached` triggers auto-redirect logic; `archived` hides
-- the goal from the default UI.
create type goal_status as enum ('active', 'paused', 'reached', 'archived');

-- Goal taxonomy (spec §5.7). `sinking_fund` is for recurring-but-unsmooth
-- expenses (car maintenance, gifts); `other` is the catch-all escape hatch.
create type goal_type as enum (
  'emergency', 'travel', 'house', 'retirement',
  'sabbatical', 'car', 'wedding', 'sinking_fund', 'other'
);

-- Provenance of a goal_allocations row. `contribution` = user directed a
-- specific transaction at this goal; `savings_rule` = rule engine applied a
-- cycle-close allocation; `manual_adjustment` = hand correction;
-- `recompute` = idempotent re-derivation from ledger (e.g. after currency
-- conversion rate update).
create type goal_allocation_source as enum (
  'contribution', 'savings_rule', 'manual_adjustment', 'recompute'
);

-- When a savings rule fires. `cycle_close` is the end-of-cycle sweep;
-- `income_received` fires on a matched income transaction; `goal_reached`
-- triggers follow-on rules (pairs with `redirect_on_reach`); `manual` is
-- user-triggered.
create type savings_rule_trigger as enum (
  'cycle_close', 'income_received', 'goal_reached', 'manual'
);

-- What a savings rule does. `percentage_of_leftover` is the classic
-- end-of-cycle sweep; `fixed_from_income` deducts a fixed amount from each
-- paycheque; `top_up_to_balance` fills a goal to a target; `redirect_on_reach`
-- sends future contributions to another goal once this one is full.
create type savings_rule_action as enum (
  'percentage_of_leftover', 'fixed_from_income',
  'top_up_to_balance', 'redirect_on_reach'
);

-- Goals: user-facing savings targets. Hierarchical via `parent_goal_id`
-- (e.g. "Vacation 2027" under "Travel"); auto-redirect via
-- `auto_redirect_on_reach_goal_id` (e.g. once "Emergency fund" is reached,
-- contributions flow into "House deposit"). Both self-FKs are composite
-- (workspace_id, id) to keep the workspace-scoped invariant. Cheap self-reference
-- checks prevent `parent = id` and `redirect = id` — multi-hop cycles
-- (A->B->A) are not DB-enforced; the service layer handles that.
create table goals (
  id                               uuid primary key,
  workspace_id                        uuid not null references workspaces(id) on delete cascade,
  name                             text not null,
  target_amount                    numeric(28,8) not null,
  currency                         money_currency not null,
  deadline                         date,
  priority                         int not null default 0,
  status                           goal_status not null default 'active',
  type                             goal_type not null,
  parent_goal_id                   uuid,
  auto_redirect_on_reach_goal_id   uuid,
  notes                            text,
  archived_at                      timestamptz,
  created_at                       timestamptz not null default now(),
  updated_at                       timestamptz not null default now(),
  unique (workspace_id, id),
  constraint goals_parent_fk foreign key (workspace_id, parent_goal_id)
    references goals(workspace_id, id) on delete set null,
  constraint goals_redirect_fk foreign key (workspace_id, auto_redirect_on_reach_goal_id)
    references goals(workspace_id, id) on delete set null,
  -- Prevent self-reference through parent or redirect (cheap sanity)
  check (parent_goal_id is null or parent_goal_id <> id),
  check (auto_redirect_on_reach_goal_id is null or auto_redirect_on_reach_goal_id <> id),
  check (target_amount > 0)
);

create trigger goals_updated_at before update on goals
  for each row execute function set_updated_at();

-- Partial index: the UI mostly queries active, non-archived goals.
create index goals_workspace_active_idx on goals(workspace_id)
  where archived_at is null and status = 'active';
-- FK-side indexes for `on delete set null` cascades and hierarchy walks.
create index goals_parent_idx on goals(parent_goal_id) where parent_goal_id is not null;
create index goals_redirect_idx on goals(auto_redirect_on_reach_goal_id) where auto_redirect_on_reach_goal_id is not null;

-- Goal-to-account bucketing (spec §5.7). Composite PK (goal_id, account_id)
-- is the natural key — one row per (goal, account) pair. `share_percentage`
-- optionally splits a multi-goal savings account: if a single account backs
-- three goals, each row's share defines what fraction belongs to which
-- goal. NULL share means "all of this account" (single-goal account).
create table goal_accounts (
  goal_id           uuid not null,
  account_id        uuid not null,
  workspace_id         uuid not null references workspaces(id) on delete cascade,
  share_percentage  numeric(5,2),
  created_at        timestamptz not null default now(),
  primary key (goal_id, account_id),
  constraint ga_goal_fk foreign key (workspace_id, goal_id)
    references goals(workspace_id, id) on delete cascade,
  constraint ga_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade,
  constraint ga_share_range_chk check (
    share_percentage is null or (share_percentage >= 0 and share_percentage <= 100)
  )
);

-- FK-side indexes: cascades from goals and accounts scan these.
create index goal_accounts_account_idx on goal_accounts(account_id);
create index goal_accounts_goal_idx on goal_accounts(goal_id);

-- Goal allocations: append-only per-(goal, account) balance snapshots.
-- Used for charting progress over time and for idempotent recompute (given
-- the ledger at time T, the allocations table is a projection that can be
-- rebuilt). `as_of` is the logical time; `created_at` is physical. Index on
-- (goal_id, account_id, as_of desc) gives cheap "latest snapshot" lookup.
create table goal_allocations (
  id          uuid primary key,
  workspace_id   uuid not null references workspaces(id) on delete cascade,
  goal_id     uuid not null,
  account_id  uuid not null,
  balance     numeric(28,8) not null,
  currency    money_currency not null,
  as_of       timestamptz not null,
  source      goal_allocation_source not null,
  created_at  timestamptz not null default now(),
  unique (workspace_id, id),
  constraint gal_goal_fk foreign key (workspace_id, goal_id)
    references goals(workspace_id, id) on delete cascade,
  constraint gal_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade
);

create index goal_allocations_goal_account_idx
  on goal_allocations(goal_id, account_id, as_of desc);

-- Goal contributions: links either an actual transaction OR a planned event
-- to a goal. `gc_source_chk` enforces XOR (exactly one of the two refs must
-- be set) using the SQL boolean-XOR idiom `(a is not null) <> (b is not
-- null)`. Why both? An executed contribution points to a transaction; a
-- pending pledge (inside a draft plan) points to a planned_event. Once the
-- planned event is executed and matched to a transaction, a new contribution
-- row with the transaction ref replaces the pledge (append-only).
create table goal_contributions (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  goal_id             uuid not null,
  transaction_id      uuid,
  planned_event_id    uuid,
  amount              numeric(28,8) not null,
  currency            money_currency not null,
  applied_at          timestamptz not null default now(),
  created_at          timestamptz not null default now(),
  unique (workspace_id, id),
  constraint gc_goal_fk foreign key (workspace_id, goal_id)
    references goals(workspace_id, id) on delete cascade,
  constraint gc_txn_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint gc_planned_fk foreign key (workspace_id, planned_event_id)
    references planned_events(workspace_id, id) on delete cascade,
  -- XOR: exactly one of (transaction_id, planned_event_id) must be set.
  constraint gc_source_chk check (
    (transaction_id is not null) <> (planned_event_id is not null)
  )
);

create index goal_contributions_goal_applied_idx on goal_contributions(goal_id, applied_at desc);
-- Partial FK-side indexes: each contribution has exactly one of these set.
create index goal_contributions_txn_idx on goal_contributions(transaction_id) where transaction_id is not null;
create index goal_contributions_planned_idx on goal_contributions(planned_event_id) where planned_event_id is not null;

-- Savings rules: priority-ordered, trigger-driven rules that produce
-- goal_allocations and goal_contributions. `trigger_config` / `action_config`
-- are jsonb escape hatches so new trigger/action variants can ship without a
-- schema migration (e.g. `percentage_of_leftover` stores `{percentage: 25}`;
-- `redirect_on_reach` stores `{from_goal_id, to_goal_id}`). `last_fired_at`
-- is set by the rule engine to power "when did this last run?" UX and dedup
-- protection.
create table savings_rules (
  id               uuid primary key,
  workspace_id        uuid not null references workspaces(id) on delete cascade,
  name             text not null,
  priority         int not null default 0,
  trigger          savings_rule_trigger not null,
  trigger_config   jsonb not null default '{}'::jsonb,
  action           savings_rule_action not null,
  action_config    jsonb not null default '{}'::jsonb,
  enabled          bool not null default true,
  last_fired_at    timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  unique (workspace_id, id)
);

create trigger savings_rules_updated_at before update on savings_rules
  for each row execute function set_updated_at();

-- Rule-engine scan path: enabled rules for a workspace, ordered by priority.
create index savings_rules_priority_idx on savings_rules(workspace_id, priority)
  where enabled = true;

-- Back-fill the deferred composite FK on cycle_plan_lines.goal_id.
-- `cycle_plan_lines` was created in 006_planning.sql without this FK because
-- `goals` did not yet exist. Now that it does, attach the composite
-- (workspace_id, goal_id) -> (workspace_id, id) FK with on delete set null so a
-- deleted goal leaves the plan line intact (but unlinked) for audit.
alter table cycle_plan_lines add constraint cpl_goal_fk
  foreign key (workspace_id, goal_id) references goals(workspace_id, id)
  on delete set null;
