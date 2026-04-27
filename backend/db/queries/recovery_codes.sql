-- name: DeleteRecoveryCodesByUser :exec
DELETE FROM auth_recovery_codes WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO auth_recovery_codes (id, user_id, code_hash, created_at)
VALUES ($1, $2, $3, $4);

-- name: ListUnconsumedRecoveryCodes :many
SELECT id, code_hash FROM auth_recovery_codes
WHERE user_id = $1 AND consumed_at IS NULL
FOR UPDATE;

-- name: ConsumeRecoveryCode :exec
UPDATE auth_recovery_codes SET consumed_at = $2 WHERE id = $1;

-- name: CountUnconsumedRecoveryCodes :one
SELECT count(*) FROM auth_recovery_codes
WHERE user_id = $1 AND consumed_at IS NULL;
