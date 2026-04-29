-- Allow archived merchants to share a canonical_name with an active one,
-- so users can archive "MIGROSEXP-7711" and later create a clean "Migros"
-- without the archived row blocking the namespace.
--
-- Prerequisite: there must be no duplicate active canonical_name within a
-- workspace before this runs, or the partial unique CREATE INDEX fails. The
-- prior unconditional UNIQUE (workspace_id, canonical_name) guaranteed this
-- by construction, so no data reconciliation pass is required for any DB
-- created from migration 20260424000003 onward. Spec §11 contemplated a
-- one-shot reconciler for the hypothetical case where this constraint had
-- been relaxed earlier; that case does not apply to this codebase.

-- Drop the unconditional unique constraint added by 20260424000003.
alter table merchants drop constraint merchants_workspace_id_canonical_name_key;

-- Replace it with a partial unique that only constrains active rows.
create unique index merchants_active_canonical_name_uniq
  on merchants(workspace_id, canonical_name)
  where archived_at is null;
