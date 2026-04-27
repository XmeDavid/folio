-- name: GetAccountGroup :one
SELECT id, workspace_id, name, sort_order, aggregate_balances, archived_at, created_at, updated_at
FROM account_groups
WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListAccountGroups :many
SELECT id, workspace_id, name, sort_order, aggregate_balances, archived_at, created_at, updated_at
FROM account_groups
WHERE workspace_id = @workspace_id
ORDER BY sort_order, created_at;

-- name: ListAccountGroupsActive :many
SELECT id, workspace_id, name, sort_order, aggregate_balances, archived_at, created_at, updated_at
FROM account_groups
WHERE workspace_id = @workspace_id AND archived_at IS NULL
ORDER BY sort_order, created_at;

-- name: InsertAccountGroup :one
INSERT INTO account_groups (id, workspace_id, name, aggregate_balances, sort_order)
VALUES (
  @id, @workspace_id, @name, @aggregate_balances,
  coalesce((SELECT max(sort_order) + 1000 FROM account_groups WHERE workspace_id = @workspace_id), 1000)
)
RETURNING id, workspace_id, name, sort_order, aggregate_balances, archived_at, created_at, updated_at;

-- name: ClearAccountGroupMembership :exec
UPDATE accounts
SET account_group_id = NULL
WHERE workspace_id = @workspace_id AND account_group_id = @group_id::uuid;

-- name: DeleteAccountGroup :execrows
DELETE FROM account_groups WHERE workspace_id = @workspace_id AND id = @id;

-- name: ReorderAccountGroup :execrows
UPDATE account_groups
SET sort_order = @sort_order
WHERE workspace_id = @workspace_id AND id = @id;

-- name: ReorderAccount :execrows
UPDATE accounts
SET account_group_id = @account_group_id, account_sort_order = @account_sort_order
WHERE workspace_id = @workspace_id AND id = @id;
