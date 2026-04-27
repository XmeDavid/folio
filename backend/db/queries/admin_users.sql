-- name: AdminListUsers :many
SELECT id, email::text, display_name, email_verified_at, is_admin, last_workspace_id, created_at
FROM users
WHERE (sqlc.arg(search)::text = '' OR email::text ILIKE sqlc.arg(search_pattern) OR display_name ILIKE sqlc.arg(search_pattern) OR id::text ILIKE sqlc.arg(search_pattern))
  AND (NOT sqlc.arg(is_admin_only)::bool OR is_admin = true)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(cursor_created_at), sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit);

-- name: AdminGetUserDetail :one
SELECT id, email::text, display_name, email_verified_at, is_admin, last_workspace_id, created_at, last_login_at
FROM users WHERE id = $1;

-- name: AdminListUserMemberships :many
SELECT t.id AS workspace_id, t.name AS workspace_name, t.slug::text AS workspace_slug, tm.role::text AS role, tm.created_at AS joined_at
FROM workspace_memberships tm JOIN workspaces t ON t.id = tm.workspace_id
WHERE tm.user_id = $1
ORDER BY tm.created_at DESC;

-- name: AdminListUserActiveSessions :many
SELECT id, created_at, last_seen_at, coalesce(user_agent, '') AS user_agent, coalesce(host(ip), '')::text AS ip
FROM sessions
WHERE user_id = $1 AND expires_at > now()
ORDER BY last_seen_at DESC;

-- name: AdminListUserPasskeys :many
SELECT id, coalesce(label, '') AS label, created_at
FROM webauthn_credentials
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: AdminUserHasTOTP :one
SELECT exists(SELECT 1 FROM totp_credentials WHERE user_id = $1 AND verified_at IS NOT NULL) AS has_totp;

-- name: AdminCountUnusedRecoveryCodes :one
SELECT count(*) FROM auth_recovery_codes WHERE user_id = $1 AND consumed_at IS NULL;
