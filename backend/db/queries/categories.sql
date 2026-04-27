-- name: InsertCategory :one
INSERT INTO categories (id, workspace_id, parent_id, name, color, sort_order)
VALUES (@id, @workspace_id, @parent_id, @name, @color, coalesce(sqlc.narg('sort_order')::int, 0))
RETURNING id, workspace_id, parent_id, name, color, sort_order, archived_at, created_at, updated_at;

-- name: CategoryExists :one
SELECT true AS ok FROM categories WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetCategory :one
SELECT id, workspace_id, parent_id, name, color, sort_order, archived_at, created_at, updated_at
FROM categories
WHERE workspace_id = @workspace_id AND id = @id;

-- name: ArchiveCategory :execrows
UPDATE categories
SET archived_at = coalesce(archived_at, @now)
WHERE workspace_id = @workspace_id AND id = @id;
