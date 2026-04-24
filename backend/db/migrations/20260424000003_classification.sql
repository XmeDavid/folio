-- Folio v2 domain — classification.
-- Categories (hierarchical, leaf-only enforced by transaction triggers in 004),
-- category_history, merchants, merchant_aliases, tags, categorization_rules.
-- Defines assert_leaf_category() here so 004_transactions.sql can bind triggers
-- without re-defining it.

-- Provenance of a suggested categorization (spec §5.3). Used by
-- categorization_suggestions (declared in 004_transactions.sql — its FK target
-- (transactions) doesn't exist until that migration runs).
create type categorization_source as enum (
  'ai', 'rule', 'merchant_default', 'similar_transaction'
);

-- Shared: leaf-category assertion (Plan §0.4 P5). Defined here so transaction
-- triggers in 004 can bind it without re-declaring. Rejects assignments of
-- category_id to rows when the target category still has active children.
-- Known v1 gap: this function treats a parent as a leaf if all its children
-- are archived. Flow that can break the "exactly one leaf per transaction"
-- invariant: archive every child of P, assign a transaction to P, then
-- unarchive one of the children. Fix deferred — options are (a) a trigger
-- on categories.archived_at that rejects unarchive when sibling transactions
-- exist on the parent, or (b) tighten the check below to reject any parent
-- with children regardless of archive state. See FEATURE-BIBLE §5.
create or replace function assert_leaf_category() returns trigger language plpgsql as $$
begin
  if new.category_id is null then return new; end if;
  if exists (
    select 1 from categories
    where parent_id = new.category_id and archived_at is null
  ) then
    raise exception 'category_id % is not a leaf', new.category_id;
  end if;
  return new;
end;
$$;

-- Categories: hierarchical, tenant-scoped. Self-FK is composite so a child
-- cannot point at a parent in a different tenant.
create table categories (
  id           uuid primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  parent_id    uuid,
  name         text not null,
  color        text,
  sort_order   int not null default 0,
  archived_at  timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, parent_id, name),
  constraint categories_parent_fk foreign key (tenant_id, parent_id)
    references categories(tenant_id, id) on delete set null
);

create trigger categories_updated_at before update on categories
  for each row execute function set_updated_at();

-- FK-side index on the self-reference; partial because most rows are roots.
create index categories_parent_idx on categories(parent_id) where parent_id is not null;
-- Active-only filter: most lookups exclude archived categories.
create index categories_active_idx on categories(tenant_id) where archived_at is null;

-- Category history: append-only audit of renames and merges. No updated_at.
-- tenant_id carried for composite FK consistency.
create table category_history (
  id                        uuid primary key,
  tenant_id                 uuid not null references tenants(id) on delete cascade,
  category_id               uuid not null,
  renamed_from              text,
  renamed_at                timestamptz not null default now(),
  merged_into_category_id   uuid,
  actor_user_id             uuid,
  created_at                timestamptz not null default now(),
  unique (tenant_id, id),
  constraint ch_category_fk foreign key (tenant_id, category_id)
    references categories(tenant_id, id) on delete cascade,
  constraint ch_merged_into_fk foreign key (tenant_id, merged_into_category_id)
    references categories(tenant_id, id) on delete set null,
  constraint ch_actor_fk foreign key (tenant_id, actor_user_id)
    references users(tenant_id, id) on delete set null
);

-- Per-category timeline lookup (also serves FK-side index for ch_category_fk).
create index category_history_category_idx on category_history(category_id, renamed_at desc);

-- Merchants: canonical payee records. default_category_id is a composite FK
-- and clears on category delete (merchants outlive their default category).
create table merchants (
  id                    uuid primary key,
  tenant_id             uuid not null references tenants(id) on delete cascade,
  canonical_name        text not null,
  logo_url              text,
  default_category_id   uuid,
  industry              text,
  website               text,
  notes                 text,
  archived_at           timestamptz,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, canonical_name),
  constraint merchants_default_category_fk foreign key (tenant_id, default_category_id)
    references categories(tenant_id, id) on delete set null
);

create trigger merchants_updated_at before update on merchants
  for each row execute function set_updated_at();

create index merchants_active_idx on merchants(tenant_id) where archived_at is null;

-- Merchant aliases: raw-string patterns that resolve to a canonical merchant.
-- Unique per (tenant, raw_pattern) so the resolver can't have ambiguous rules.
create table merchant_aliases (
  id           uuid primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  merchant_id  uuid not null,
  raw_pattern  text not null,
  is_regex     bool not null default false,
  created_at   timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, raw_pattern),
  constraint merchant_aliases_merchant_fk foreign key (tenant_id, merchant_id)
    references merchants(tenant_id, id) on delete cascade
);

-- FK-side index for cascades and per-merchant alias lookups.
create index merchant_aliases_merchant_idx on merchant_aliases(merchant_id);

-- Tags: free-form labels, tenant-scoped, unique name per tenant.
create table tags (
  id           uuid primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  name         text not null,
  color        text,
  archived_at  timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, name)
);

create trigger tags_updated_at before update on tags
  for each row execute function set_updated_at();

create index tags_active_idx on tags(tenant_id) where archived_at is null;

-- Categorization rules: user-defined when→then JSON rules evaluated in
-- priority order by the rule engine. `last_matched_at` is maintained by the
-- engine for diagnostics.
create table categorization_rules (
  id                uuid primary key,
  tenant_id         uuid not null references tenants(id) on delete cascade,
  priority          int not null,
  when_jsonb        jsonb not null,
  then_jsonb        jsonb not null,
  enabled           bool not null default true,
  last_matched_at   timestamptz,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  unique (tenant_id, id)
);

create trigger categorization_rules_updated_at before update on categorization_rules
  for each row execute function set_updated_at();

-- Rule-engine scan: enabled rules, priority-ordered.
create index categorization_rules_priority_idx
  on categorization_rules(tenant_id, priority) where enabled = true;

-- categorization_suggestions lives in 004_transactions.sql: its FK target
-- (transactions) doesn't exist until that migration runs.
