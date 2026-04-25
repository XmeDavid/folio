package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

var ErrRecoveryCodeInvalid = errors.New("recovery code invalid")

const (
	recoveryCodeCount = 10
	// hmacRecoveryPrefix marks rows hashed via HMAC-SHA256(pepper, code).
	// Older rows lacked the prefix and were Argon2id-hashed; consumeRecoveryCode
	// falls back to VerifyPassword for that legacy format.
	hmacRecoveryPrefix = "hmac:"
)

func generateRecoveryCode() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	enc = strings.ToUpper(enc)[:10]
	return enc[:5] + "-" + enc[5:], nil
}

func normalizeRecoveryCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}

// hashRecoveryCode hashes a recovery code with the configured pepper. We use
// a plain HMAC instead of Argon2id because recovery codes are 50 bits of
// random entropy — there's nothing to "stretch", and Argon2id was burning
// hundreds of milliseconds of CPU per check.
func hashRecoveryCode(code string, pepper []byte) string {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(normalizeRecoveryCode(code)))
	return hmacRecoveryPrefix + base64.RawStdEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) generateAndStoreRecoveryCodes(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time) ([]string, error) {
	if _, err := tx.Exec(ctx, `delete from auth_recovery_codes where user_id = $1`, userID); err != nil {
		return nil, err
	}
	plain := make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		hash := hashRecoveryCode(code, s.cfg.SecretKey)
		if _, err := tx.Exec(ctx, `
			insert into auth_recovery_codes (id, user_id, code_hash, created_at)
			values ($1, $2, $3, $4)
		`, uuidx.New(), userID, hash, now); err != nil {
			return nil, err
		}
		plain = append(plain, code)
	}
	return plain, nil
}

func (s *Service) consumeRecoveryCode(ctx context.Context, tx pgx.Tx, userID uuid.UUID, code string, now time.Time) error {
	rows, err := tx.Query(ctx, `
		select id, code_hash from auth_recovery_codes
		where user_id = $1 and consumed_at is null
		for update
	`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	normalized := normalizeRecoveryCode(code)
	hmacCandidate := hashRecoveryCode(code, s.cfg.SecretKey)
	var matchID *uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return err
		}
		if strings.HasPrefix(hash, hmacRecoveryPrefix) {
			if subtle.ConstantTimeCompare([]byte(hash), []byte(hmacCandidate)) == 1 {
				matchID = &id
				break
			}
			continue
		}
		// Legacy Argon2id-hashed row — keep verifying so codes stored before
		// the HMAC switch still work until the user regenerates.
		ok, _ := VerifyPassword(normalized, hash, s.cfg.SecretKey)
		if ok {
			matchID = &id
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if matchID == nil {
		return ErrRecoveryCodeInvalid
	}
	_, err = tx.Exec(ctx, `update auth_recovery_codes set consumed_at = $2 where id = $1`, *matchID, now)
	return err
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	st, err := s.MFAStatus(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Recovery codes only pay off during MFA challenges; without any second
	// factor enrolled they'd just be dangling rows.
	if !st.TOTPEnrolled && st.PasskeyCount == 0 {
		return nil, ErrTOTPNotEnrolled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	now := s.now().UTC()
	codes, err := s.generateAndStoreRecoveryCodes(ctx, tx, userID, now)
	if err != nil {
		return nil, err
	}
	_ = writeAuditTx(ctx, tx, nil, &userID, "mfa.recovery_codes_regenerated", "user", userID, nil, nil, nil, "")
	return codes, tx.Commit(ctx)
}

func (s *Service) RemainingRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from auth_recovery_codes
		where user_id = $1 and consumed_at is null
	`, userID).Scan(&n)
	return n, err
}
