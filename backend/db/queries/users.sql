-- name: GetUserIDByEmail :one
SELECT id FROM users WHERE email = $1;

-- name: ListAdminUsers :many
SELECT email::text AS email, id, updated_at
FROM users
WHERE is_admin = true
ORDER BY email;

-- name: GetUserEmailAndName :one
SELECT email, display_name, email_verified_at FROM users WHERE id = $1;

-- name: GetUserIDAndNameByEmail :one
SELECT id, display_name FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, display_name, email_verified_at, is_admin, last_workspace_id, created_at
FROM users WHERE id = $1;

-- name: GetUserByEmailWithPassword :one
SELECT id, email, display_name, email_verified_at, is_admin, last_workspace_id, created_at, password_hash
FROM users WHERE email = $1;

-- name: InsertUserReturning :one
INSERT INTO users (id, email, password_hash, display_name)
VALUES ($1, $2, $3, $4)
RETURNING id, email, display_name, email_verified_at, is_admin, last_workspace_id, created_at;

-- name: UpdateUserLastWorkspace :exec
UPDATE users SET last_workspace_id = $1 WHERE id = $2;

-- name: UpdateUserLastLoginAt :exec
UPDATE users SET last_login_at = $1 WHERE id = $2;

-- name: VerifyUserEmail :execrows
UPDATE users SET email_verified_at = coalesce(email_verified_at, now())
WHERE id = $1 AND email = $2;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = $1 WHERE id = $2;

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: UpdateUserEmail :exec
UPDATE users SET email = $1, email_verified_at = now() WHERE id = $2;

-- name: CheckEmailExistsExcludingUser :one
SELECT exists(SELECT 1 FROM users WHERE email = $1 AND id <> $2) AS email_exists;

-- name: GetUserEmailAndDisplayName :one
SELECT email, display_name FROM users WHERE id = $1;

-- name: UserExists :one
SELECT exists(SELECT 1 FROM users) AS user_exists;

-- name: GetUserIsAdmin :one
SELECT is_admin FROM users WHERE id = $1;

-- name: GetUserPasswordHash :one
SELECT password_hash FROM users WHERE id = $1;

-- name: AcquireFirstRunLock :exec
SELECT pg_advisory_xact_lock($1);

-- name: InsertLoginFailedAudit :exec
INSERT INTO audit_events (id, workspace_id, actor_user_id, action, entity_type, entity_id, after_jsonb, ip, user_agent)
VALUES ($1, null, null, 'user.login_failed', 'email', $2, jsonb_build_object('email', $3::text), $4, $5);

-- name: InsertAuditDirect :exec
INSERT INTO audit_events (id, workspace_id, actor_user_id, action, entity_type, entity_id, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: InsertInvitedMembership :exec
INSERT INTO workspace_memberships (workspace_id, user_id, role)
VALUES ($1, $2, $3::workspace_role);

-- name: AcceptWorkspaceInvite :exec
UPDATE workspace_invites SET accepted_at = now() WHERE id = $1;

-- name: GetWorkspaceInviteByTokenHash :one
SELECT id, workspace_id, email, role::text AS role, expires_at, revoked_at, accepted_at
FROM workspace_invites WHERE token_hash = $1 FOR UPDATE;
