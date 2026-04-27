-- name: GetUserIDByEmail :one
SELECT id FROM users WHERE email = $1;

-- name: ListAdminUsers :many
SELECT email::text AS email, id, updated_at
FROM users
WHERE is_admin = true
ORDER BY email;
