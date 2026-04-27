-- name: InsertWorkspaceInvite :one
INSERT INTO workspace_invites (id, workspace_id, email, role, token_hash,
                               invited_by_user_id, expires_at)
VALUES ($1, $2, $3, $4::workspace_role, $5, $6, $7)
RETURNING id, workspace_id, email, role::text AS role, invited_by_user_id,
          created_at, expires_at;

-- name: GetInvitePreview :one
SELECT i.workspace_id, t.name AS workspace_name, t.slug AS workspace_slug,
       u.display_name AS inviter_display_name,
       i.email, i.role::text AS role, i.expires_at, i.revoked_at, i.accepted_at
FROM workspace_invites i
JOIN workspaces t ON t.id = i.workspace_id
JOIN users u ON u.id = i.invited_by_user_id
WHERE i.token_hash = $1 AND t.deleted_at IS NULL;

-- name: GetInviteForAccept :one
SELECT id, workspace_id, email, role::text AS role, expires_at, revoked_at, accepted_at
FROM workspace_invites
WHERE token_hash = $1
FOR UPDATE;

-- name: GetInviteForRevoke :one
SELECT invited_by_user_id, revoked_at, accepted_at
FROM workspace_invites
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: MarkInviteAccepted :exec
UPDATE workspace_invites SET accepted_at = now() WHERE id = $1;

-- name: MarkInviteRevoked :exec
UPDATE workspace_invites SET revoked_at = now() WHERE id = $1;

-- name: ListPendingInvites :many
SELECT id, email, role::text AS role, invited_by_user_id, created_at, expires_at
FROM workspace_invites
WHERE workspace_id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
ORDER BY created_at DESC;

-- name: CheckIsWorkspaceOwner :one
SELECT exists(
    SELECT 1 FROM workspace_memberships
    WHERE workspace_id = $1 AND user_id = $2 AND role = 'owner'
) AS is_owner;

-- name: UpsertMembershipOnInvite :exec
INSERT INTO workspace_memberships (workspace_id, user_id, role)
VALUES ($1, $2, $3::workspace_role)
ON CONFLICT (workspace_id, user_id) DO NOTHING;

-- name: GetUserEmailAndVerification :one
SELECT email, email_verified_at FROM users WHERE id = $1;
