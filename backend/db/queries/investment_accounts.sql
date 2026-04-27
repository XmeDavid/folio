-- name: InvestmentAccountExists :one
SELECT EXISTS(SELECT 1 FROM investment_accounts WHERE workspace_id = @workspace_id AND account_id = @account_id);

-- name: GetAccountKind :one
SELECT kind::text FROM accounts WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertInvestmentAccount :exec
INSERT INTO investment_accounts (account_id, workspace_id)
VALUES (@account_id, @workspace_id)
ON CONFLICT DO NOTHING;

-- name: FindBrokerageAccount :one
SELECT a.id, a.name
FROM accounts a
WHERE a.workspace_id = @workspace_id AND a.kind = 'brokerage'
  AND a.archived_at IS NULL
  AND (
    a.currency = @currency
    OR coalesce(a.institution, '') ILIKE '%' || @source_label || '%'
    OR coalesce(a.nickname, '') ILIKE '%' || @source_label || '%'
    OR a.name ILIKE '%' || @source_label || '%'
  )
ORDER BY CASE WHEN a.currency = @currency THEN 0 ELSE 1 END,
         a.created_at ASC
LIMIT 1;

-- name: InsertBrokerageAccount :exec
INSERT INTO accounts (
  id, workspace_id, name, kind, currency, institution,
  open_date, opening_balance, opening_balance_date,
  include_in_networth, include_in_savings_rate
) VALUES (
  @id, @workspace_id, @name, 'brokerage'::account_kind, @currency, @institution,
  @open_date, 0, @open_date,
  true, false
);

-- name: InsertBrokerageOpeningSnapshot :exec
INSERT INTO account_balance_snapshots (
  id, workspace_id, account_id, as_of, balance, currency, source
) VALUES (
  @id, @workspace_id, @account_id, @as_of, 0, @currency, 'opening'
);

-- name: GetWorkspaceBaseCurrency :one
SELECT base_currency FROM workspaces WHERE id = @id;
