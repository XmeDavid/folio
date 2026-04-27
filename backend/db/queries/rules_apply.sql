-- name: LoadTransactionSnapshot :one
SELECT id, workspace_id, account_id, amount::text AS amount,
       counterparty_raw, description, merchant_id, category_id, count_as_expense
FROM transactions
WHERE workspace_id = @workspace_id AND id = @id;

-- name: LoadEnabledRules :many
SELECT id, workspace_id, priority, when_jsonb, then_jsonb, enabled,
       last_matched_at, created_at, updated_at
FROM categorization_rules
WHERE workspace_id = @workspace_id AND enabled = true
ORDER BY priority ASC, created_at ASC;

-- name: StampRuleLastMatchedAt :exec
UPDATE categorization_rules SET last_matched_at = now()
WHERE workspace_id = @workspace_id AND id = @id;
