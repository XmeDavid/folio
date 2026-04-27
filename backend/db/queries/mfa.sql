-- name: InsertMFAChallenge :exec
INSERT INTO auth_mfa_challenges
    (id, user_id, ip, user_agent, created_at, expires_at, webauthn_state)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetMFAChallengeByID :one
SELECT id, user_id, ip::text AS ip, user_agent, created_at, expires_at,
       consumed_at, attempts, coalesce(webauthn_state, '{}'::jsonb) AS webauthn_state
FROM auth_mfa_challenges
WHERE id = $1;

-- name: ConsumeMFAChallenge :execrows
UPDATE auth_mfa_challenges SET consumed_at = $2
WHERE id = $1 AND consumed_at IS NULL;

-- name: BumpMFAChallengeAttempts :one
UPDATE auth_mfa_challenges
SET attempts = attempts + 1,
    consumed_at = CASE WHEN attempts + 1 >= 10 THEN $2 ELSE consumed_at END
WHERE id = $1
RETURNING attempts;

-- name: UpdateMFAChallengeWebAuthnState :exec
UPDATE auth_mfa_challenges SET webauthn_state = $2 WHERE id = $1;

-- name: ConsumeOpenMFAChallengesByUser :exec
UPDATE auth_mfa_challenges SET consumed_at = now()
WHERE user_id = $1 AND consumed_at IS NULL;

-- name: GetMFAStatus :one
SELECT
    exists(SELECT 1 FROM totp_credentials tc WHERE tc.user_id = $1 AND tc.verified_at IS NOT NULL) AS totp_enrolled,
    (SELECT count(*) FROM webauthn_credentials wc WHERE wc.user_id = $1) AS passkey_count,
    (SELECT count(*) FROM auth_recovery_codes rc WHERE rc.user_id = $1 AND rc.consumed_at IS NULL) AS remaining_recovery_codes;

-- name: HasMFAEnrolled :one
SELECT exists(SELECT 1 FROM totp_credentials tc WHERE tc.user_id = $1 AND tc.verified_at IS NOT NULL)
    OR exists(SELECT 1 FROM webauthn_credentials wc WHERE wc.user_id = $1) AS has_mfa;
