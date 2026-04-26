-- Folio v2 domain — transactions (the ledger core).
-- transactions, transaction_lines, transaction_tags, transfer_matches,
-- refund_matches, and categorization_suggestions (lives here because its FK
-- target is transactions). Enforces four invariants via triggers:
--   1. Transaction currency must match its account's currency.
--   2. category_id (on transactions or transaction_lines) must be a leaf.
--   3. A transaction cannot have both a category_id and lines (double
--      classification). Split transactions set category_id = NULL and
--      classify per-line; simple transactions set category_id and have no
--      lines. Enforced on both tables.
--   4. Currency-match guard runs on INSERT and on UPDATE of
--      currency/account_id (per spec §3.11).

-- Transaction lifecycle (spec §5.4). 'draft' = imported but not yet posted;
-- 'posted' = normal ledger entry; 'reconciled' = tied to a statement
-- checkpoint; 'voided' = reversed without deletion.
create type transaction_status as enum (
  'draft', 'posted', 'reconciled', 'voided'
);

-- Provenance of a transfer or refund match (spec §5.4). 'auto_detected' is
-- the matcher's first pass; 'user_confirmed_auto' tracks matches the user
-- accepted, useful for matcher quality metrics.
create type match_provenance as enum (
  'auto_detected', 'manual', 'user_confirmed_auto'
);

-- Transactions: the core ledger fact table. `amount` and `currency` are
-- always in the account's currency (enforced by trigger). `original_amount`
-- and `original_currency` capture the pre-FX amount when the transaction
-- was booked in a different currency (e.g. foreign card spend). `count_as_expense`
-- is a user override: NULL defers to the derived rule (see spec §5.4).
-- `category_id` is nullable because split transactions classify per-line
-- in `transaction_lines` — enforced by the no-double-classification trigger.
create table transactions (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  account_id          uuid not null,
  status              transaction_status not null,
  booked_at           date not null,
  value_at            date,
  posted_at           timestamptz,
  amount              numeric(28,8) not null,
  currency            money_currency not null,
  original_amount     numeric(28,8),
  original_currency   money_currency,
  merchant_id         uuid,
  category_id         uuid,
  counterparty_raw    text,
  description         text,
  notes               text,
  count_as_expense    bool,                           -- NULL = derive
  raw                 jsonb,
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  unique (workspace_id, id),
  constraint transactions_account_fk foreign key (workspace_id, account_id)
    references accounts(workspace_id, id) on delete cascade,
  constraint transactions_category_fk foreign key (workspace_id, category_id)
    references categories(workspace_id, id) on delete set null,
  constraint transactions_merchant_fk foreign key (workspace_id, merchant_id)
    references merchants(workspace_id, id) on delete set null,
  -- original_amount and original_currency must be NULL together: either both
  -- present (FX-denominated) or both absent (native currency).
  constraint transactions_original_pair_chk
    check ((original_amount is null) = (original_currency is null))
);

create trigger transactions_updated_at before update on transactions
  for each row execute function set_updated_at();

-- Spec §5.4 indexes. Composite (workspace, booked_at desc) supports the default
-- feed; (account, booked_at desc) supports account-detail view; partial
-- category/merchant indexes keep FK-side cascades cheap while skipping the
-- majority of unclassified rows.
create index transactions_workspace_booked_idx on transactions(workspace_id, booked_at desc);
create index transactions_account_booked_idx on transactions(account_id, booked_at desc);
create index transactions_category_idx on transactions(category_id) where category_id is not null;
create index transactions_merchant_idx on transactions(merchant_id) where merchant_id is not null;
create index transactions_status_idx on transactions(workspace_id, status);

-- Currency-match trigger (spec §3.11 P3): a transaction's currency must
-- equal its account's currency. Runs on INSERT and on UPDATE of currency
-- or account_id. Prevents silent cross-currency mixups in the ledger.
create or replace function assert_txn_currency_matches_account() returns trigger language plpgsql as $$
declare
  v_account_currency money_currency;
begin
  select currency into v_account_currency from accounts where id = new.account_id;
  if new.currency <> v_account_currency then
    raise exception 'transaction currency % does not match account currency %',
      new.currency, v_account_currency;
  end if;
  return new;
end;
$$;

create trigger transactions_currency_match
  before insert or update of currency, account_id on transactions
  for each row execute function assert_txn_currency_matches_account();

-- Transaction lines: split-transaction components. Each line carries its
-- own amount, currency, and leaf category. category_id is NOT NULL (every
-- line must be classified) and ON DELETE RESTRICT — deleting a category
-- that still has lines would silently orphan classifications; force the
-- user to re-classify first. merchant_id may override the transaction's
-- merchant (e.g. a grocery line at a megastore).
create table transaction_lines (
  id               uuid primary key,
  workspace_id        uuid not null references workspaces(id) on delete cascade,
  transaction_id   uuid not null,
  amount           numeric(28,8) not null,
  currency         money_currency not null,
  category_id      uuid not null,
  merchant_id      uuid,
  note             text,
  sort_order       int not null default 0,
  created_at       timestamptz not null default now(),
  unique (workspace_id, id),
  constraint tl_transaction_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tl_category_fk foreign key (workspace_id, category_id)
    references categories(workspace_id, id) on delete restrict,
  constraint tl_merchant_fk foreign key (workspace_id, merchant_id)
    references merchants(workspace_id, id) on delete set null
);

-- Per-transaction line lookup, ordered by sort_order (UI display order).
create index transaction_lines_tx_idx on transaction_lines(transaction_id, sort_order);
-- FK-side index for category cascades and per-category reporting.
create index transaction_lines_category_idx on transaction_lines(category_id);

-- Transaction tags: many-to-many between transactions and tags. workspace_id
-- carried for composite-FK safety. PK is (transaction_id, tag_id), so a tag
-- can only be applied once per transaction.
create table transaction_tags (
  transaction_id  uuid not null,
  tag_id          uuid not null,
  workspace_id       uuid not null references workspaces(id) on delete cascade,
  created_at      timestamptz not null default now(),
  primary key (transaction_id, tag_id),
  constraint tt_transaction_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tt_tag_fk foreign key (workspace_id, tag_id)
    references tags(workspace_id, id) on delete cascade
);

-- Reverse lookup: transactions by tag.
create index transaction_tags_tag_idx on transaction_tags(tag_id);

-- Transfer matches: links the two sides of a cross-account transfer.
-- destination_transaction_id is NULL for outbound-to-external (money leaving
-- the tracked accounts to an unlinked destination). fx_rate is the observed
-- rate when source and destination currencies differ; fee_amount/fee_currency
-- capture wire/FX fees attributed to the transfer.
create table transfer_matches (
  id                           uuid primary key,
  workspace_id                    uuid not null references workspaces(id) on delete cascade,
  source_transaction_id        uuid not null,
  destination_transaction_id   uuid,                    -- null = outbound-to-external
  fx_rate                      numeric(28,10),
  fee_amount                   numeric(28,8),
  fee_currency                 money_currency,
  tolerance_note               text,
  provenance                   match_provenance not null,
  matched_by_user_id           uuid,
  matched_at                   timestamptz not null default now(),
  created_at                   timestamptz not null default now(),
  unique (workspace_id, id),
  constraint tm_source_fk foreign key (workspace_id, source_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tm_dest_fk foreign key (workspace_id, destination_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tm_actor_fk foreign key (matched_by_user_id)
    references users(id) on delete set null,
  -- fee_amount and fee_currency must be NULL together.
  constraint tm_fee_pair_chk
    check ((fee_amount is null) = (fee_currency is null))
);

-- Per-side lookups; dest is partial because many transfers have no dest row.
create index transfer_matches_source_idx on transfer_matches(source_transaction_id);
create index transfer_matches_dest_idx on transfer_matches(destination_transaction_id) where destination_transaction_id is not null;

-- Refund matches: links a refund transaction to the original charge.
-- net_to_zero indicates whether refund + original sum to zero (the common
-- case); a false value flags partial refunds for reporting.
create table refund_matches (
  id                        uuid primary key,
  workspace_id                 uuid not null references workspaces(id) on delete cascade,
  original_transaction_id   uuid not null,
  refund_transaction_id     uuid not null,
  net_to_zero               bool not null default true,
  provenance                match_provenance not null,
  matched_by_user_id        uuid,
  matched_at                timestamptz not null default now(),
  created_at                timestamptz not null default now(),
  unique (workspace_id, id),
  constraint rm_original_fk foreign key (workspace_id, original_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint rm_refund_fk foreign key (workspace_id, refund_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint rm_actor_fk foreign key (matched_by_user_id)
    references users(id) on delete set null
);

-- FK-side indexes for cascades and per-side lookup.
create index refund_matches_original_idx on refund_matches(original_transaction_id);
create index refund_matches_refund_idx on refund_matches(refund_transaction_id);

-- Categorization suggestions: the categorizer's proposed classifications.
-- Lives here (not in 003_classification.sql) because its FK target
-- (transactions) doesn't exist until this migration runs. accepted_at and
-- dismissed_at form the lifecycle; partial index on open (both NULL) is
-- the hot query.
create table categorization_suggestions (
  id                     uuid primary key,
  workspace_id              uuid not null references workspaces(id) on delete cascade,
  transaction_id         uuid not null,
  suggested_category_id  uuid not null,
  confidence             numeric(5,4) not null,
  source                 categorization_source not null,
  created_at             timestamptz not null default now(),
  accepted_at            timestamptz,
  dismissed_at           timestamptz,
  unique (workspace_id, id),
  constraint cs_txn_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint cs_cat_fk foreign key (workspace_id, suggested_category_id)
    references categories(workspace_id, id) on delete cascade
);

-- Per-transaction lookup (also covers FK-side for cs_txn_fk cascade).
create index categorization_suggestions_txn_idx on categorization_suggestions(transaction_id);
-- Open suggestions (not yet accepted or dismissed): the primary worklist
-- query. Partial index keeps it tight as the historical log grows.
create index categorization_suggestions_open_idx
  on categorization_suggestions(workspace_id, created_at desc)
  where accepted_at is null and dismissed_at is null;

-- Leaf-category triggers. Uses assert_leaf_category() defined in 003. On
-- transactions, only fires when a category_id is assigned (split rows are
-- NULL and don't need the check). On transaction_lines, category_id is NOT
-- NULL, so fire unconditionally.
--
-- Trigger firing order on transactions is alphabetical by name:
-- currency_match -> leaf_category -> no_double_class -> updated_at.
-- This means a non-leaf category on a transaction that already has lines
-- surfaces the leaf error before the double-classification error.
-- Acceptable for v1 -- both errors are valid and the caller has to fix the
-- category either way. Rename a trigger if you need a different order.
create trigger transactions_leaf_category
  before insert or update of category_id on transactions
  for each row when (new.category_id is not null)
  execute function assert_leaf_category();

create trigger transaction_lines_leaf_category
  before insert or update of category_id on transaction_lines
  for each row execute function assert_leaf_category();

-- No-double-classification guard (plan §0.4 P6). Enforces the invariant
-- that a transaction is classified *either* by its own category_id *or* by
-- its lines, never both. Checks both directions:
--   * Setting category_id on a transaction with existing lines: reject.
--   * Inserting a line onto a transaction with a non-NULL category_id: reject.
--
-- Known v1 gap: under READ COMMITTED, two concurrent writes (one setting
-- category_id on the transaction, one inserting the first line) can both
-- see "sibling doesn't exist yet" and commit, violating the invariant.
-- Fix when needed: take a transaction-level advisory lock keyed on the
-- transaction_id (pg_advisory_xact_lock(hashtext(transaction_id::text)))
-- at the top of both triggers -- SELECT ... FOR UPDATE on the sibling
-- is insufficient because the sibling row may not exist yet.
create or replace function assert_no_double_classification() returns trigger language plpgsql as $$
begin
  if tg_table_name = 'transactions' and new.category_id is not null then
    if exists (select 1 from transaction_lines where transaction_id = new.id) then
      raise exception 'transaction % has lines; category_id must be null', new.id;
    end if;
  end if;

  if tg_table_name = 'transaction_lines' then
    if exists (
      select 1 from transactions
      where id = new.transaction_id and category_id is not null
    ) then
      raise exception 'transaction % has category_id; cannot add lines', new.transaction_id;
    end if;
  end if;

  return new;
end;
$$;

create trigger transactions_no_double_class
  before insert or update of category_id on transactions
  for each row execute function assert_no_double_classification();

create trigger transaction_lines_no_double_class
  before insert on transaction_lines
  for each row execute function assert_no_double_classification();
