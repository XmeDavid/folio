-- name: GetSessionByID :one
SELECT id, user_id, created_at, expires_at, last_seen_at, reauth_at
FROM sessions WHERE id = $1;

-- name: InsertSession :exec
INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $3, $5, $6);

-- name: DeleteSessionByID :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteSessionByIDReturningUserID :one
DELETE FROM sessions WHERE id = $1 RETURNING user_id;

-- name: DeleteOtherSessionsByUser :exec
DELETE FROM sessions WHERE user_id = $1 AND id <> $2;

-- name: UpdateSessionLastSeen :exec
UPDATE sessions SET last_seen_at = $1 WHERE id = $2;

-- name: UpdateSessionReauthAt :exec
UPDATE sessions SET reauth_at = $3 WHERE id = $1 AND user_id = $2;
