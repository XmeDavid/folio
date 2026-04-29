-- name: GetAccountCurrency :one
SELECT currency FROM accounts WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListImportAccountMatches :many
-- Archived accounts are kept in the candidate set so re-importing the
-- same file matches the account the user already imported into instead
-- of silently creating a duplicate.
SELECT id, name, currency, institution, archived_at
FROM accounts
WHERE workspace_id = @workspace_id
ORDER BY name;

-- name: InsertImportBatch :exec
INSERT INTO import_batches (
  id, workspace_id, source_kind, file_name, file_hash, status,
  summary, created_by_user_id, started_at, finished_at
) VALUES (
  @id, @workspace_id, 'file_upload', @file_name, @file_hash, 'applied',
  @summary::jsonb, @created_by_user_id, @started_at, @started_at
);

-- name: InsertImportTransaction :exec
INSERT INTO transactions (
  id, workspace_id, account_id, status, booked_at, value_at, posted_at,
  amount, currency, counterparty_raw, description, raw
) VALUES (
  @id, @workspace_id, @account_id, 'posted', @booked_at, @value_at, @posted_at,
  @amount::numeric, @currency, @counterparty_raw, @description, @raw::jsonb
);

-- name: InsertSourceRef :exec
INSERT INTO source_refs (
  id, workspace_id, entity_type, entity_id, provider,
  import_batch_id, external_id, raw_payload, observed_at
) VALUES (
  @id, @workspace_id, 'transaction', @entity_id, @provider,
  @import_batch_id, @external_id, @raw_payload::jsonb, @observed_at
);

-- name: InsertImportAccount :exec
INSERT INTO accounts (
  id, workspace_id, name, kind, currency, institution,
  open_date, opening_balance, opening_balance_date,
  include_in_networth, include_in_savings_rate
) VALUES (
  @id, @workspace_id, @name, @kind::account_kind, @currency, @institution,
  @open_date, @opening_balance::numeric, @opening_balance_date, true, @include_in_savings_rate
);

-- name: UnarchiveAccount :exec
-- Opt-in reactivate: clear archived_at when the user explicitly
-- asked to resurface this account. No-op for non-archived rows.
UPDATE accounts
SET archived_at = null
WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NOT NULL;

-- name: LoadExistingTransactions :many
-- Load existing transactions in the date range for duplicate/conflict detection.
SELECT t.id, t.booked_at, t.posted_at, t.amount::text AS amount, t.currency,
       coalesce(t.description, t.counterparty_raw, '')::text AS description,
       sr.external_id AS source_id,
       coalesce(t.raw->>'synthetic' = 'balance_reconcile', false)::bool AS synthetic
FROM transactions t
LEFT JOIN source_refs sr
  ON sr.workspace_id = t.workspace_id
 AND sr.entity_type = 'transaction'
 AND sr.entity_id = t.id
 AND sr.provider = @provider
WHERE t.workspace_id = @workspace_id
  AND t.account_id = @account_id
  AND t.booked_at BETWEEN @date_from AND @date_to
  AND t.status <> 'voided'
  AND t.currency = @currency;

-- name: LoadSyntheticCandidates :many
-- Scan synthetic balance-reconcile rows for potential retirement.
-- gap_start_date is the consolidated row date that established the previous
-- balance for this synthetic; together with booked_at it bounds the interval
-- where the missing real transaction must have occurred. Older synthetics
-- (pre-#TBD) didn't store this and fall back to NULL.
SELECT t.id, t.booked_at, t.posted_at, t.amount::text AS amount, t.currency,
       coalesce(t.raw->>'synthetic_residual', t.amount::text)::text AS residual,
       coalesce(t.raw->>'gap_start_date', '')::text AS gap_start_date,
       sr.import_batch_id
FROM transactions t
LEFT JOIN source_refs sr
  ON sr.workspace_id = t.workspace_id
 AND sr.entity_type = 'transaction'
 AND sr.entity_id = t.id
WHERE t.workspace_id = @workspace_id
  AND t.account_id = @account_id
  AND t.status = 'posted'
  AND t.raw->>'synthetic' = 'balance_reconcile'
  AND t.booked_at BETWEEN @date_from::timestamptz AND @date_to::timestamptz;

-- name: LoadRealRowsForSynthetic :many
-- Load real (non-synthetic) rows near a synthetic for residual-explained check.
-- Only considers rows from a different import batch than the synthetic itself.
SELECT t.id, t.booked_at, t.posted_at, t.amount::text AS amount, t.currency,
       coalesce(t.description, t.counterparty_raw, '')::text AS description
FROM transactions t
JOIN source_refs sr
  ON sr.workspace_id = t.workspace_id
 AND sr.entity_type = 'transaction'
 AND sr.entity_id = t.id
WHERE t.workspace_id = @workspace_id
  AND t.account_id = @account_id
  AND t.status = 'posted'
  AND t.currency = @currency
  AND t.booked_at BETWEEN @date_from::timestamptz AND @date_to::timestamptz
  AND coalesce(t.raw->>'synthetic', '') <> 'balance_reconcile'
  AND t.id <> @exclude_id
  AND (sqlc.narg('exclude_batch_id')::uuid IS NULL OR sr.import_batch_id <> sqlc.narg('exclude_batch_id')::uuid);

-- name: VoidTransaction :exec
UPDATE transactions SET status = 'voided' WHERE id = @id AND workspace_id = @workspace_id;
