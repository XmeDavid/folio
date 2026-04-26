-- User-defined account groups for organizing large account lists without
-- changing the ledger model. Accounts remain the accounting truth; groups are
-- a workspace-scoped presentation/reporting layer.

create table account_groups (
  id            uuid primary key,
  workspace_id     uuid not null references workspaces(id) on delete cascade,
  name          text not null,
  sort_order    integer not null default 0,
  archived_at   timestamptz,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),
  unique (workspace_id, id)
);

create trigger account_groups_updated_at before update on account_groups
  for each row execute function set_updated_at();

create unique index account_groups_workspace_name_active_idx
  on account_groups(workspace_id, lower(name))
  where archived_at is null;

create index account_groups_workspace_order_idx
  on account_groups(workspace_id, sort_order, created_at);

alter table accounts
  add column account_group_id uuid,
  add column account_sort_order integer not null default 0,
  add constraint accounts_group_fk foreign key (workspace_id, account_group_id)
    references account_groups(workspace_id, id);

create index accounts_group_order_idx
  on accounts(workspace_id, account_group_id, account_sort_order, created_at);
