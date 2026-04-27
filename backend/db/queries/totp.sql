-- name: GetTOTPVerifiedAt :one
SELECT verified_at FROM totp_credentials WHERE user_id = $1;

-- name: UpsertTOTPCredential :execrows
INSERT INTO totp_credentials (id, user_id, secret_cipher, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
SET secret_cipher = excluded.secret_cipher, created_at = excluded.created_at, verified_at = null
WHERE totp_credentials.verified_at IS NULL;

-- name: ConfirmTOTPCredential :exec
UPDATE totp_credentials SET verified_at = $2 WHERE user_id = $1;

-- name: DeleteTOTPCredential :execrows
DELETE FROM totp_credentials WHERE user_id = $1;

-- name: BumpTOTPLastUsedStep :execrows
UPDATE totp_credentials
SET last_used_step = $2
WHERE user_id = $1 AND (last_used_step IS NULL OR last_used_step < $2);

-- name: GetTOTPSecretCipher :one
SELECT secret_cipher FROM totp_credentials WHERE user_id = $1 AND verified_at IS NOT NULL;

-- name: GetTOTPSecretCipherAny :one
SELECT secret_cipher FROM totp_credentials WHERE user_id = $1;
