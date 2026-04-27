-- name: InsertCorporateAction :one
INSERT INTO corporate_actions (
  id, workspace_id, account_id, instrument_id,
  kind, effective_date, payload
) VALUES (
  @id, @workspace_id, @account_id, @instrument_id,
  @kind::corporate_action_kind, @effective_date, @payload::jsonb
)
RETURNING
  id, workspace_id, account_id, instrument_id,
  kind::text, effective_date, payload, applied_at, created_at;

-- name: GetCorporateActionForDelete :one
SELECT instrument_id, account_id FROM corporate_actions
WHERE id = @id AND (workspace_id = @workspace_id OR workspace_id IS NULL);

-- name: DeleteCorporateAction :execrows
DELETE FROM corporate_actions
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListCorporateActions :many
SELECT
  ca.id, ca.workspace_id, ca.account_id, ca.instrument_id, i.symbol,
  ca.kind::text AS kind, ca.effective_date, ca.payload, ca.applied_at, ca.created_at
FROM corporate_actions ca
JOIN instruments i ON i.id = ca.instrument_id
WHERE ca.instrument_id = @instrument_id
  AND (ca.workspace_id IS NULL OR ca.workspace_id = @workspace_id)
ORDER BY ca.effective_date DESC, ca.created_at DESC;

-- name: LoadCorporateActionEvents :many
SELECT kind::text AS kind, effective_date, payload
FROM corporate_actions
WHERE (workspace_id IS NULL OR workspace_id = @workspace_id)
  AND (account_id IS NULL OR account_id = @account_id)
  AND instrument_id = @instrument_id
ORDER BY effective_date ASC, id ASC;

-- name: ListPositionAccountsForInstrument :many
SELECT DISTINCT account_id FROM investment_positions
WHERE workspace_id = @workspace_id AND instrument_id = @instrument_id;
