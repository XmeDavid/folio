-- Groups can opt into being counted as a single reporting balance while the
-- underlying accounts remain unchanged ledger containers.

alter table account_groups
  add column aggregate_balances boolean not null default false;
