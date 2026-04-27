-- name: ListWebAuthnCredentials :many
SELECT credential_id, public_key, sign_count, coalesce(transports, '{}') AS transports
FROM webauthn_credentials WHERE user_id = $1;

-- name: InsertWebAuthnCredential :exec
INSERT INTO webauthn_credentials (id, user_id, credential_id, public_key, sign_count, transports, label, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: UpdateWebAuthnSignCount :exec
UPDATE webauthn_credentials SET sign_count = $2
WHERE user_id = $1 AND credential_id = $3;
