-- name: InsertTransactionTag :exec
INSERT INTO transaction_tags (transaction_id, tag_id, workspace_id)
VALUES (@transaction_id, @tag_id, @workspace_id)
ON CONFLICT (transaction_id, tag_id) DO NOTHING;

-- name: DeleteTransactionTag :exec
DELETE FROM transaction_tags
WHERE workspace_id = @workspace_id AND transaction_id = @transaction_id AND tag_id = @tag_id;

-- name: TransactionExists :one
SELECT true AS ok FROM transactions WHERE workspace_id = @workspace_id AND id = @id;
