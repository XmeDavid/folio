-- Prevent multiple transfer_matches rows referencing the same source or
-- destination. The application layer checks this before insert, but the
-- check is racy under concurrent imports / manual pairs. With these unique
-- indexes, ON CONFLICT DO NOTHING in the detector becomes a real concurrent
-- safety net, and ManualPair's race window collapses to a friendly 23505
-- → ConflictError translation.
create unique index transfer_matches_source_uq
  on transfer_matches(workspace_id, source_transaction_id);

create unique index transfer_matches_destination_uq
  on transfer_matches(workspace_id, destination_transaction_id)
  where destination_transaction_id is not null;

-- Drop the existing non-unique source index — superseded by the unique one.
drop index if exists transfer_matches_source_idx;
