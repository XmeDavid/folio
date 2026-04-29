-- name: GetAccountCurrency :one
SELECT currency FROM accounts WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListImportAccountMatches :many
-- Archived accounts are kept in the candidate set so re-importing the
-- same file matches the account the user already imported into instead
-- of silently creating a duplicate. Kind is exposed so the import wizard
-- can avoid auto-suggesting a brokerage import target for a cash group
-- (e.g. Flexible Cash Funds USD interest rows landing in the Conta
-- Pessoal USD checking account because no other USD account exists).
SELECT id, name, currency, kind, institution, archived_at
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
  amount, currency, counterparty_raw, description, raw,
  merchant_id, category_id
) VALUES (
  @id, @workspace_id, @account_id, 'posted', @booked_at, @value_at, @posted_at,
  @amount::numeric, @currency, @counterparty_raw, @description, @raw::jsonb,
  @merchant_id, @category_id
);

-- name: InsertSourceRef :exec
INSERT INTO source_refs (
  id, workspace_id, entity_type, entity_id, provider,
  import_batch_id, external_id, raw_payload, observed_at
) VALUES (
  @id, @workspace_id, 'transaction', @entity_id, @provider,
  @import_batch_id, @external_id, @raw_payload::jsonb, @observed_at
);

-- name: FindAccountByNameKindCurrency :one
-- Lookup an account by exact (name, kind, currency) within a workspace.
-- Used at apply time to merge create_account requests into an account
-- that another file in the same multi-apply batch already created — the
-- preview step ran before any apply committed, so the wizard couldn't
-- see same-batch siblings as candidates and the user's plan defaults to
-- create. Without this divert the second create attempt produces a
-- duplicate account with the same name. Archived rows are included so
-- a re-import resurfaces the prior account instead of cloning around it.
SELECT id
FROM accounts
WHERE workspace_id = @workspace_id
  AND lower(name) = lower(@name)
  AND kind = @kind::account_kind
  AND currency = @currency
ORDER BY archived_at NULLS FIRST, created_at
LIMIT 1;

-- name: ListWorkspaceExternalIDs :many
-- Surface every (provider, external_id) tuple already present in the
-- workspace so classify can dedup re-imports across accounts. The
-- source_refs unique index is workspace-scoped, but the per-account
-- existing-row check used at classify time misses rows attached to a
-- different account (e.g. an earlier import that targeted a different
-- account, or a rerun that picked "create_account" instead of merging
-- into the prior target). Without this lookup the apply would attempt
-- to insert a duplicate source_ref and the whole file would 23505 out.
SELECT external_id::text AS external_id
FROM source_refs
WHERE workspace_id = @workspace_id
  AND entity_type = 'transaction'
  AND provider = @provider
  AND external_id IS NOT NULL;

-- name: InsertImportAccount :exec
-- close_date and archived_at are nullable; when both are NULL the new
-- account behaves identically to pre-archived-import rows. They get
-- populated when the consolidated export's metadata says the upstream
-- sub-account is already closed (e.g. an old `Dollar (USD)` pocket
-- closed in 2021), so the imported row lands in Folio already
-- archived and stays out of the active list.
INSERT INTO accounts (
  id, workspace_id, name, kind, currency, institution,
  open_date, close_date, opening_balance, opening_balance_date,
  include_in_networth, include_in_savings_rate, archived_at
) VALUES (
  @id, @workspace_id, @name, @kind::account_kind, @currency, @institution,
  @open_date, @close_date, @opening_balance::numeric, @opening_balance_date,
  true, @include_in_savings_rate, @archived_at
);

-- name: ArchiveImportAccount :exec
-- Apply-time archive for already-closed Revolut sub-accounts when we're
-- merging into a pre-existing account that didn't have its close_date
-- recorded yet. Idempotent — leaves rows alone if either field is
-- already populated.
UPDATE accounts
SET close_date = coalesce(close_date, @close_date::date),
    archived_at = coalesce(archived_at, @archived_at::timestamptz)
WHERE workspace_id = @workspace_id AND id = @id;

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

-- name: AccountHasSavingsStatementRows :one
-- Detects whether an account already contains higher-fidelity savings-
-- statement events. Used to make MMF summary retirement order-independent:
-- a consolidated import that follows a savings-statement import should
-- still void its newly-emitted summary rows, just like the reverse order.
SELECT EXISTS (
  SELECT 1 FROM transactions t
  WHERE t.workspace_id = @workspace_id
    AND t.account_id = @account_id
    AND t.status = 'posted'
    AND t.raw->>'section' = 'Flexible Cash Funds'
    AND t.raw->>'op' IN ('buy', 'sell', 'interest_paid', 'service_fee', 'interest_reinvested', 'interest_withdrawn')
  LIMIT 1
) AS exists_;

-- name: LoadMMFSummaryCandidates :many
-- Find consolidated-MMF "net interest" rows in this account that fall
-- within a date range, so a higher-fidelity savings-statement import can
-- void them. The granular savings-statement export breaks the same daily
-- interest into separate Interest PAID + Service Fee rows; without this
-- voiding step a user importing both files double-counts the interest.
SELECT t.id, t.booked_at, t.currency
FROM transactions t
WHERE t.workspace_id = @workspace_id
  AND t.account_id = @account_id
  AND t.status = 'posted'
  AND t.raw->>'mmf_summary' = 'true'
  AND t.booked_at BETWEEN @date_from::timestamptz AND @date_to::timestamptz;

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
