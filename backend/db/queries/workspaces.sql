-- name: GetWorkspaceByID :one
SELECT id, name, slug, base_currency, cycle_anchor_day, locale, timezone,
       deleted_at, created_at
FROM workspaces
WHERE id = $1 AND deleted_at IS NULL;

-- name: InsertWorkspace :one
INSERT INTO workspaces (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, slug, base_currency, cycle_anchor_day, locale, timezone, deleted_at, created_at;

-- name: SoftDeleteWorkspace :execrows
UPDATE workspaces SET deleted_at = coalesce(deleted_at, now()) WHERE id = $1;

-- name: RestoreWorkspace :execrows
UPDATE workspaces SET deleted_at = NULL WHERE id = $1;

-- name: ListWorkspacesWithRoleByUser :many
SELECT t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day,
       t.locale, t.timezone, t.deleted_at, t.created_at, m.role
FROM workspace_memberships m
JOIN workspaces t ON t.id = m.workspace_id
WHERE m.user_id = $1 AND t.deleted_at IS NULL
ORDER BY t.name;
