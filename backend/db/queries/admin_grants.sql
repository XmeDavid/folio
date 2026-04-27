-- name: AdminGetUserIsAdminForUpdate :one
SELECT is_admin FROM users WHERE id = $1 FOR UPDATE;

-- name: AdminSetUserAdmin :exec
UPDATE users SET is_admin = $1, updated_at = now() WHERE id = $2;

-- name: AdminCountAdmins :one
SELECT count(*) FROM users WHERE is_admin = true;
