-- Prevent multiple transfer_matches rows referencing the same source or
-- destination in the same role. Application inserts also take deterministic
-- row locks on every participant before checking the cross-role invariant.
create unique index transfer_matches_source_uq
  on transfer_matches(workspace_id, source_transaction_id);

create unique index transfer_matches_destination_uq
  on transfer_matches(workspace_id, destination_transaction_id)
  where destination_transaction_id is not null;

-- Drop the existing non-unique source index — superseded by the unique one.
drop index if exists transfer_matches_source_idx;

-- Guard non-application writes too: a transaction may participate in at most
-- one transfer match, regardless of whether it appears as source or
-- destination. App code still locks participants first so concurrent writes
-- serialize before this check runs.
create or replace function transfer_matches_participant_guard()
returns trigger
language plpgsql
as $$
begin
  if new.destination_transaction_id is not null
     and new.source_transaction_id = new.destination_transaction_id then
    raise exception 'source and destination must differ'
      using errcode = '23514',
            constraint = 'transfer_matches_distinct_participants_chk';
  end if;

  if exists (
    select 1
    from transfer_matches tm
    where tm.workspace_id = new.workspace_id
      and tm.id <> new.id
      and (
        tm.source_transaction_id = new.source_transaction_id
        or tm.destination_transaction_id = new.source_transaction_id
        or (
          new.destination_transaction_id is not null
          and (
            tm.source_transaction_id = new.destination_transaction_id
            or tm.destination_transaction_id = new.destination_transaction_id
          )
        )
      )
  ) then
    raise exception 'transaction already participates in transfer match'
      using errcode = '23505',
            constraint = 'transfer_matches_participant_uq';
  end if;

  return new;
end;
$$;

create trigger transfer_matches_participant_guard_trg
before insert or update of workspace_id, source_transaction_id, destination_transaction_id
on transfer_matches
for each row
execute function transfer_matches_participant_guard();
