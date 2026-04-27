-- name: InsertAuthToken :exec
INSERT INTO auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetAuthTokenForConsume :one
SELECT id, user_id, coalesce(email::text, '')::text AS email, expires_at
FROM auth_tokens
WHERE token_hash = $1 AND purpose = $2 AND consumed_at IS NULL
FOR UPDATE;

-- name: ConsumeAuthToken :exec
UPDATE auth_tokens SET consumed_at = now() WHERE id = $1;

-- name: GetUserForPasswordReset :one
SELECT u.id, u.email, u.display_name
FROM auth_tokens t JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1 AND t.purpose = $2 AND t.consumed_at IS NULL;
