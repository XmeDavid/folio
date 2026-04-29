-- name: InsertPlatformInvite :one
INSERT INTO platform_invites (id, email, token_hash, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetPlatformInviteByTokenHash :one
SELECT * FROM platform_invites WHERE token_hash = $1;

-- name: GetPlatformInviteForAccept :one
SELECT id, email, token_hash, expires_at, revoked_at, accepted_at
FROM platform_invites
WHERE token_hash = $1
FOR UPDATE;

-- name: GetPlatformInviteForRevoke :one
SELECT id, created_by, revoked_at, accepted_at
FROM platform_invites
WHERE id = $1
FOR UPDATE;

-- name: ListPlatformInvitesActive :many
SELECT * FROM platform_invites
WHERE accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC;

-- name: ListPlatformInvitesAll :many
SELECT * FROM platform_invites
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: MarkPlatformInviteRevoked :exec
UPDATE platform_invites
SET revoked_at = now(), revoked_by = $2
WHERE id = $1;

-- name: MarkPlatformInviteAccepted :exec
UPDATE platform_invites
SET accepted_at = now(), accepted_by = $2
WHERE id = $1;
