-- name: InsertAccount :one
INSERT INTO accounts (
  id, workspace_id, name, nickname, kind, currency, institution,
  account_group_id, account_sort_order,
  open_date, opening_balance, opening_balance_date,
  include_in_networth, include_in_savings_rate
) VALUES (
  @id, @workspace_id, @name, @nickname, @kind::account_kind, @currency, @institution,
  @account_group_id,
  coalesce((
    SELECT max(account_sort_order) + 1000
    FROM accounts
    WHERE workspace_id = @workspace_id
      AND account_group_id IS NOT DISTINCT FROM @account_group_id::uuid
  ), 1000),
  @open_date, @opening_balance::numeric, @opening_balance_date,
  @include_in_networth, @include_in_savings_rate
)
RETURNING id, workspace_id, name, nickname, kind::text AS kind, currency, institution,
          account_group_id, account_sort_order,
          open_date, close_date, opening_balance::text AS opening_balance, opening_balance_date,
          include_in_networth, include_in_savings_rate, archived_at,
          created_at, updated_at;

-- name: InsertOpeningSnapshot :exec
INSERT INTO account_balance_snapshots (
  id, workspace_id, account_id, as_of, balance, currency, source
) VALUES (
  @id, @workspace_id, @account_id, @as_of, @balance::numeric, @currency, 'opening'
);

-- Derived balance rule (spec §5.2):
--   balance = coalesce(latest_snapshot.balance, opening_balance)
--           + sum(transactions.amount where status in ('posted','reconciled')
--                 and booked_at >= snapshot.as_of projected to UTC date)
--
-- Every account has an "opening" snapshot at create time, so post-opening-day
-- transactions are included correctly.

-- name: GetAccountWithBalance :one
SELECT
  a.id, a.workspace_id, a.name, a.nickname, a.kind::text AS kind, a.currency, a.institution,
  a.account_group_id, a.account_sort_order,
  a.open_date, a.close_date, a.opening_balance::text AS opening_balance, a.opening_balance_date,
  a.include_in_networth, a.include_in_savings_rate, a.archived_at,
  a.created_at, a.updated_at,
  CASE
    WHEN t.max_booked_at IS NULL THEN s.as_of
    WHEN s.as_of IS NULL THEN t.max_booked_at::timestamp AT TIME ZONE 'UTC'
    WHEN t.max_booked_at::timestamp AT TIME ZONE 'UTC' > s.as_of THEN t.max_booked_at::timestamp AT TIME ZONE 'UTC'
    ELSE s.as_of
  END AS balance_as_of,
  (coalesce(s.balance, a.opening_balance) + coalesce(t.post_sum, 0))::text AS balance
FROM accounts a
LEFT JOIN LATERAL (
  SELECT balance, as_of
  FROM account_balance_snapshots
  WHERE account_id = a.id
  ORDER BY as_of DESC
  LIMIT 1
) s ON true
LEFT JOIN LATERAL (
  SELECT coalesce(sum(amount), 0) AS post_sum, max(booked_at) AS max_booked_at
  FROM transactions
  WHERE account_id = a.id
    AND status IN ('posted', 'reconciled')
    AND booked_at >= (coalesce(s.as_of, a.opening_balance_date::timestamptz)
                        AT TIME ZONE 'UTC')::date
) t ON true
WHERE a.workspace_id = @workspace_id AND a.id = @id;

-- name: ListAccountsWithBalance :many
SELECT
  a.id, a.workspace_id, a.name, a.nickname, a.kind::text AS kind, a.currency, a.institution,
  a.account_group_id, a.account_sort_order,
  a.open_date, a.close_date, a.opening_balance::text AS opening_balance, a.opening_balance_date,
  a.include_in_networth, a.include_in_savings_rate, a.archived_at,
  a.created_at, a.updated_at,
  CASE
    WHEN t.max_booked_at IS NULL THEN s.as_of
    WHEN s.as_of IS NULL THEN t.max_booked_at::timestamp AT TIME ZONE 'UTC'
    WHEN t.max_booked_at::timestamp AT TIME ZONE 'UTC' > s.as_of THEN t.max_booked_at::timestamp AT TIME ZONE 'UTC'
    ELSE s.as_of
  END AS balance_as_of,
  (coalesce(s.balance, a.opening_balance) + coalesce(t.post_sum, 0))::text AS balance
FROM accounts a
LEFT JOIN LATERAL (
  SELECT balance, as_of
  FROM account_balance_snapshots
  WHERE account_id = a.id
  ORDER BY as_of DESC
  LIMIT 1
) s ON true
LEFT JOIN LATERAL (
  SELECT coalesce(sum(amount), 0) AS post_sum, max(booked_at) AS max_booked_at
  FROM transactions
  WHERE account_id = a.id
    AND status IN ('posted', 'reconciled')
    AND booked_at >= (coalesce(s.as_of, a.opening_balance_date::timestamptz)
                        AT TIME ZONE 'UTC')::date
) t ON true
WHERE a.workspace_id = @workspace_id
ORDER BY a.account_group_id NULLS FIRST, a.account_sort_order, a.created_at;

-- name: ListAccountsWithBalanceActive :many
SELECT
  a.id, a.workspace_id, a.name, a.nickname, a.kind::text AS kind, a.currency, a.institution,
  a.account_group_id, a.account_sort_order,
  a.open_date, a.close_date, a.opening_balance::text AS opening_balance, a.opening_balance_date,
  a.include_in_networth, a.include_in_savings_rate, a.archived_at,
  a.created_at, a.updated_at,
  CASE
    WHEN t.max_booked_at IS NULL THEN s.as_of
    WHEN s.as_of IS NULL THEN t.max_booked_at::timestamp AT TIME ZONE 'UTC'
    WHEN t.max_booked_at::timestamp AT TIME ZONE 'UTC' > s.as_of THEN t.max_booked_at::timestamp AT TIME ZONE 'UTC'
    ELSE s.as_of
  END AS balance_as_of,
  (coalesce(s.balance, a.opening_balance) + coalesce(t.post_sum, 0))::text AS balance
FROM accounts a
LEFT JOIN LATERAL (
  SELECT balance, as_of
  FROM account_balance_snapshots
  WHERE account_id = a.id
  ORDER BY as_of DESC
  LIMIT 1
) s ON true
LEFT JOIN LATERAL (
  SELECT coalesce(sum(amount), 0) AS post_sum, max(booked_at) AS max_booked_at
  FROM transactions
  WHERE account_id = a.id
    AND status IN ('posted', 'reconciled')
    AND booked_at >= (coalesce(s.as_of, a.opening_balance_date::timestamptz)
                        AT TIME ZONE 'UTC')::date
) t ON true
WHERE a.workspace_id = @workspace_id AND a.archived_at IS NULL
ORDER BY a.account_group_id NULLS FIRST, a.account_sort_order, a.created_at;

-- name: DeleteAccountSourceRefs :exec
DELETE FROM source_refs sr
WHERE sr.workspace_id = @workspace_id
  AND sr.entity_type = 'transaction'
  AND sr.entity_id IN (
    SELECT tx.id FROM transactions tx WHERE tx.workspace_id = @workspace_id AND tx.account_id = @account_id
  );

-- name: DeleteAccount :execrows
DELETE FROM accounts WHERE workspace_id = @workspace_id AND id = @id;
