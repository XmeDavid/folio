-- name: InsertTag :one
INSERT INTO tags (id, workspace_id, name, color)
VALUES (@id, @workspace_id, @name, @color)
RETURNING id, workspace_id, name, color, archived_at, created_at, updated_at;

-- name: GetTag :one
SELECT id, workspace_id, name, color, archived_at, created_at, updated_at
FROM tags
WHERE workspace_id = @workspace_id AND id = @id;

-- name: TagExists :one
SELECT true AS ok FROM tags WHERE workspace_id = @workspace_id AND id = @id;

-- name: ArchiveTag :execrows
UPDATE tags
SET archived_at = coalesce(archived_at, @now)
WHERE workspace_id = @workspace_id AND id = @id;
