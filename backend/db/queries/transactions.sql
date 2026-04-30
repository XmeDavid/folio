-- name: DeleteTransaction :execrows
DELETE FROM transactions WHERE workspace_id = @workspace_id AND id = @id;
