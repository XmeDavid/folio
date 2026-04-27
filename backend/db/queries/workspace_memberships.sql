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

-- name: ListMembersWithUser :many
SELECT m.workspace_id, m.user_id, m.role::text AS role, m.created_at,
       u.email, u.display_name
FROM workspace_memberships m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1
ORDER BY m.created_at;

-- name: InsertMembership :one
INSERT INTO workspace_memberships (workspace_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING workspace_id, user_id, role, created_at;

-- name: GetMembershipRoleForUpdate :one
SELECT role FROM workspace_memberships
WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE;

-- name: CountWorkspaceOwners :one
SELECT count(*) FROM workspace_memberships
WHERE workspace_id = $1 AND role = 'owner';

-- name: CountUserMemberships :one
SELECT count(*) FROM workspace_memberships WHERE user_id = $1;

-- name: UpdateMembershipRole :exec
UPDATE workspace_memberships SET role = $3, updated_at = now()
WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteMembership :exec
DELETE FROM workspace_memberships WHERE workspace_id = $1 AND user_id = $2;

-- name: AcquireWorkspaceMembershipLock :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: AcquireUserMembershipLock :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 1));
