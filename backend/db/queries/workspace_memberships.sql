-- name: GetWorkspaceWithMembership :one
SELECT t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day,
       t.locale, t.timezone, t.deleted_at, t.created_at, m.role
FROM workspaces t
JOIN workspace_memberships m ON m.workspace_id = t.id
WHERE t.id = $1 AND m.user_id = $2 AND t.deleted_at IS NULL;

-- name: GetWorkspaceWithOwnership :one
SELECT t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day,
       t.locale, t.timezone, t.deleted_at, t.created_at, m.role
FROM workspaces t
JOIN workspace_memberships m ON m.workspace_id = t.id
WHERE t.id = $1 AND m.user_id = $2 AND m.role = 'owner';
