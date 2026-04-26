-- Folio v2 domain — wishlist (spec §5.11).
-- The "want-to-buy" slice: wishlist items (name, estimated price, priority,
-- optional link to a savings goal), price observations (append-only price
-- history, manual or scraper-sourced), and purchase links (1:1 mapping from
-- wishlist item to the transaction that realized it, with variance vs the
-- estimate).
--
-- Shape rules threaded throughout:
--   * wishlist_purchase_links.wishlist_item_id is bare UNIQUE (1:1) so a
--     wishlist item can only be purchased once. Re-buying the same item
--     requires a new wishlist row.
--   * wpl_mark_bought trigger flips wishlist_items.status to 'bought' on
--     purchase link insert — the wishlist is the source of truth, the
--     trigger keeps it in sync with the purchase event.
--   * wishlist_price_observations is append-only (no updated_at) — price
--     history should not be mutable.

-- Wishlist lifecycle: `wanted` is the default (open desire); `bought` is
-- flipped by the wpl_mark_bought trigger on purchase link insert;
-- `abandoned` is a user-driven terminal state (no longer interested).
create type wishlist_status as enum ('wanted', 'bought', 'abandoned');

-- Source of a price observation. `manual` = user-entered; `scraper` = the
-- scraper pipeline populated it (scraper_run_id then references the run).
create type wishlist_price_source as enum ('manual', 'scraper');

-- Wishlist items: the user's want-list. `estimated_price` is optional
-- because early-stage wishes may lack a concrete price. `linked_goal_id`
-- optionally ties the item to a savings goal (so progress toward the goal
-- doubles as progress toward the purchase); set-null on goal delete so the
-- wish persists. Partial index on (workspace_id, priority desc) targets the
-- dominant UI query: "what do I still want, most important first?"
create table wishlist_items (
  id                uuid primary key,
  workspace_id         uuid not null references workspaces(id) on delete cascade,
  name              text not null,
  estimated_price   numeric(28,8),
  currency          money_currency not null,
  url               text,
  notes             text,
  priority          int not null default 0,
  status            wishlist_status not null default 'wanted',
  linked_goal_id    uuid,
  archived_at       timestamptz,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  unique (workspace_id, id),
  constraint wi_goal_fk foreign key (workspace_id, linked_goal_id)
    references goals(workspace_id, id) on delete set null,
  check (estimated_price is null or estimated_price >= 0)
);

create trigger wishlist_items_updated_at before update on wishlist_items
  for each row execute function set_updated_at();

-- Partial indexes: the active-wishlist view is overwhelmingly "unarchived
-- wanted items by priority", and the goal rollup needs a fast reverse
-- lookup from goal -> linked wishlist items.
create index wishlist_items_workspace_active_idx on wishlist_items(workspace_id, priority desc)
  where archived_at is null and status = 'wanted';
create index wishlist_items_goal_idx on wishlist_items(linked_goal_id) where linked_goal_id is not null;

-- Wishlist price observations: append-only price history. `source`
-- distinguishes manual entries from scraper-populated rows; `scraper_run_id`
-- is populated iff source='scraper' (enforced via wpo_scraper_run_chk).
-- No updated_at: history is immutable.
create table wishlist_price_observations (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  wishlist_item_id    uuid not null,
  observed_at         timestamptz not null,
  price               numeric(28,8) not null,
  currency            money_currency not null,
  source              wishlist_price_source not null,
  scraper_run_id      uuid,
  note                text,
  created_at          timestamptz not null default now(),
  unique (workspace_id, id),
  constraint wpo_item_fk foreign key (workspace_id, wishlist_item_id)
    references wishlist_items(workspace_id, id) on delete cascade,
  check (price >= 0),
  -- scraper_run_id is set iff source='scraper'. Manual entries must not
  -- carry a run id (would imply scraper provenance); scraper entries must
  -- carry one (the run is the audit trail). DB-level guarantee independent
  -- of the scraper pipeline's own invariants.
  constraint wpo_scraper_run_chk check (
    (source = 'scraper') = (scraper_run_id is not null)
  )
);

-- Time-series index: "show me this item's price trend" is the dominant
-- read pattern; DESC matches the typical "latest first" query shape.
create index wishlist_price_observations_item_time_idx
  on wishlist_price_observations(wishlist_item_id, observed_at desc);

-- Wishlist purchase links: 1:1 binding from wishlist item to the real
-- transaction that realized the purchase. `variance` is actual - estimated,
-- nullable because items without an estimate have no variance to compute.
-- Bare UNIQUE on wishlist_item_id enforces 1:1 (one purchase per item);
-- re-buying requires a new wishlist row. Bare UNIQUE is cleaner than the
-- composite form — the composite FK already scopes the item to the workspace,
-- so a bare UNIQUE is sufficient and more explicit about the 1:1 intent.
create table wishlist_purchase_links (
  id                  uuid primary key,
  workspace_id           uuid not null references workspaces(id) on delete cascade,
  wishlist_item_id    uuid not null,
  transaction_id      uuid not null,
  purchased_at        date not null,
  actual_amount       numeric(28,8) not null,
  currency            money_currency not null,
  variance            numeric(28,8),                  -- actual - estimated (null if no estimate)
  created_at          timestamptz not null default now(),
  unique (workspace_id, id),
  unique (wishlist_item_id),                          -- 1:1 — one purchase per item
  constraint wpl_item_fk foreign key (workspace_id, wishlist_item_id)
    references wishlist_items(workspace_id, id) on delete cascade,
  constraint wpl_txn_fk foreign key (workspace_id, transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  check (actual_amount >= 0)
);

-- FK-side index: supports the reverse lookup ("is this transaction a
-- wishlist purchase?") and cascades from transactions.
create index wishlist_purchase_links_txn_idx on wishlist_purchase_links(transaction_id);

-- Mark-bought trigger: on insert of a purchase link, promote the wishlist item
-- to `status='bought'` and clear archived_at. Applies even if the item was
-- previously `abandoned` — the purchase is the stronger signal. Re-buying the
-- same item (e.g. buying a second one) requires a new wishlist item row
-- because of the bare UNIQUE on wishlist_purchase_links.wishlist_item_id.
create or replace function wishlist_mark_bought() returns trigger language plpgsql as $$
begin
  update wishlist_items
    set status = 'bought',
        archived_at = null,
        updated_at = now()
    where id = new.wishlist_item_id;
  return new;
end;
$$;

create trigger wpl_mark_bought after insert on wishlist_purchase_links
  for each row execute function wishlist_mark_bought();
