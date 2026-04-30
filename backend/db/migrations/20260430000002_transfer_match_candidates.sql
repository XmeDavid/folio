-- Tier-3 review queue for the transfer-pair detector.
-- Heuristic candidate suggestions awaiting user confirmation. One pending
-- row per source_transaction_id (unique). On user action the row
-- transitions to 'paired' or 'declined' and is never re-suggested by
-- Tier 3 — the unique constraint enforces this.
create table transfer_match_candidates (
  id                          uuid primary key,
  workspace_id                uuid not null references workspaces(id) on delete cascade,
  source_transaction_id       uuid not null,
  candidate_destination_ids   uuid[] not null,
  status                      text not null default 'pending',
  suggested_at                timestamptz not null default now(),
  resolved_at                 timestamptz,
  resolved_by_user_id         uuid,
  unique (workspace_id, source_transaction_id),
  constraint tmc_source_fk foreign key (workspace_id, source_transaction_id)
    references transactions(workspace_id, id) on delete cascade,
  constraint tmc_actor_fk foreign key (resolved_by_user_id)
    references users(id) on delete set null,
  constraint tmc_status_chk
    check (status in ('pending', 'paired', 'declined'))
);

create index transfer_match_candidates_pending_idx
  on transfer_match_candidates(workspace_id) where status = 'pending';
