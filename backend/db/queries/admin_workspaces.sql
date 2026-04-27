-- name: AdminListWorkspaces :many
SELECT id, name, slug::text, base_currency::text, cycle_anchor_day, locale, timezone, deleted_at, created_at
FROM workspaces
WHERE (sqlc.arg(include_deleted)::bool OR deleted_at IS NULL)
  AND (sqlc.arg(search)::text = '' OR name ILIKE sqlc.arg(search_pattern) OR slug::text ILIKE sqlc.arg(search_pattern) OR id::text ILIKE sqlc.arg(search_pattern))
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(cursor_created_at), sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit);

-- name: AdminGetWorkspaceByID :one
SELECT id, name, slug::text, base_currency::text, cycle_anchor_day, locale, timezone, deleted_at, created_at
FROM workspaces WHERE id = $1;

-- name: AdminCountWorkspaceMembers :one
SELECT count(*) FROM workspace_memberships WHERE workspace_id = $1;