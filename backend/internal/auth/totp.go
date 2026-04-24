package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/skip2/go-qrcode"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type TOTPSetup struct {
	Secret        string   `json:"secret"`
	URI           string   `json:"uri"`
	QRCodeBase64  string   `json:"qrCodeBase64"`
	RecoveryCodes []string `json:"recoveryCodes,omitempty"`
}

type MFAStatus struct {
	TOTPEnrolled           bool `json:"totpEnrolled"`
	PasskeyCount           int  `json:"passkeyCount"`
	RemainingRecoveryCodes int  `json:"remainingRecoveryCodes"`
}

var (
	ErrTOTPAlreadyEnrolled = errors.New("totp already enrolled")
	ErrTOTPNotEnrolled     = errors.New("totp not enrolled")
	ErrTOTPInvalidCode     = errors.New("invalid totp code")
	ErrMFARequired         = errors.New("mfa required")
)

func (s *Service) GetUserByID(ctx context.Context, userID uuid.UUID) (identity.User, error) {
	var user identity.User
	err := s.pool.QueryRow(ctx, `
		select id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at
		from users where id = $1
	`, userID).Scan(&user.ID, &user.Email, &user.DisplayName, &user.EmailVerifiedAt, &user.IsAdmin, &user.LastTenantID, &user.CreatedAt)
	return user, err
}

func (s *Service) MFAStatus(ctx context.Context, userID uuid.UUID) (MFAStatus, error) {
	var st MFAStatus
	err := s.pool.QueryRow(ctx, `
		select exists(select 1 from totp_credentials where user_id = $1 and verified_at is not null),
		       (select count(*) from webauthn_credentials where user_id = $1),
		       (select count(*) from auth_recovery_codes where user_id = $1 and consumed_at is null)
	`, userID).Scan(&st.TOTPEnrolled, &st.PasskeyCount, &st.RemainingRecoveryCodes)
	return st, err
}

func (s *Service) EnrollTOTP(ctx context.Context, userID uuid.UUID) (TOTPSetup, error) {
	user, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return TOTPSetup{}, err
	}
	var verifiedAt *time.Time
	err = s.pool.QueryRow(ctx, `select verified_at from totp_credentials where user_id = $1`, userID).Scan(&verifiedAt)
	if err == nil && verifiedAt != nil {
		return TOTPSetup{}, ErrTOTPAlreadyEnrolled
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TOTPSetup{}, err
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Folio",
		AccountName: user.Email,
		Period:      30,
		Digits:      otp.DigitsSix,
	})
	if err != nil {
		return TOTPSetup{}, err
	}
	sealed, err := SealSecret(s.cfg.SecretKey, []byte(key.Secret()))
	if err != nil {
		return TOTPSetup{}, err
	}
	// The `WHERE verified_at IS NULL` on DO UPDATE closes the check-then-act
	// gap between the select above and this upsert: a row that was verified
	// by a concurrent ConfirmTOTP will not be silently reset to null here.
	ct, err := s.pool.Exec(ctx, `
		insert into totp_credentials (id, user_id, secret_cipher, created_at)
		values ($1, $2, $3, $4)
		on conflict (user_id) do update
		set secret_cipher = excluded.secret_cipher, created_at = excluded.created_at, verified_at = null
		where totp_credentials.verified_at is null
	`, uuidx.New(), userID, base64.StdEncoding.EncodeToString(sealed), s.now().UTC())
	if err != nil {
		return TOTPSetup{}, err
	}
	if ct.RowsAffected() == 0 {
		return TOTPSetup{}, ErrTOTPAlreadyEnrolled
	}
	png, err := qrcode.Encode(key.URL(), qrcode.Medium, 256)
	if err != nil {
		return TOTPSetup{}, err
	}
	return TOTPSetup{Secret: key.Secret(), URI: key.URL(), QRCodeBase64: base64.StdEncoding.EncodeToString(png)}, nil
}

func (s *Service) ConfirmTOTP(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	if err := s.verifyTOTPCodeWithEnrollment(ctx, userID, code, false); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	now := s.now().UTC()
	if _, err := tx.Exec(ctx, `update totp_credentials set verified_at = $2 where user_id = $1`, userID, now); err != nil {
		return nil, err
	}
	codes, err := s.generateAndStoreRecoveryCodes(ctx, tx, userID, now)
	if err != nil {
		return nil, err
	}
	_ = writeAuditTx(ctx, tx, nil, &userID, "mfa.totp_enabled", "user", userID, nil, nil, nil, "")
	return codes, tx.Commit(ctx)
}

func (s *Service) DisableTOTP(ctx context.Context, userID uuid.UUID, currentSessionID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `delete from totp_credentials where user_id = $1`, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrTOTPNotEnrolled
	}
	if _, err := tx.Exec(ctx, `delete from sessions where user_id = $1 and id <> $2`, userID, currentSessionID); err != nil {
		return err
	}
	_ = writeAuditTx(ctx, tx, nil, &userID, "mfa.totp_disabled", "user", userID, nil, nil, nil, "")
	return tx.Commit(ctx)
}

func (s *Service) verifyTOTPCode(ctx context.Context, userID uuid.UUID, code string) error {
	return s.verifyTOTPCodeWithEnrollment(ctx, userID, code, true)
}

func (s *Service) verifyTOTPCodeWithEnrollment(ctx context.Context, userID uuid.UUID, code string, requireVerified bool) error {
	secret, err := s.loadTOTPSecret(ctx, userID, requireVerified)
	if err != nil {
		return err
	}
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, s.now().UTC(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return err
	}
	if !ok {
		return ErrTOTPInvalidCode
	}
	return nil
}

func (s *Service) loadTOTPSecret(ctx context.Context, userID uuid.UUID, requireVerified bool) (string, error) {
	var encoded string
	q := `select secret_cipher from totp_credentials where user_id = $1`
	if requireVerified {
		q += ` and verified_at is not null`
	}
	err := s.pool.QueryRow(ctx, q, userID).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTOTPNotEnrolled
	}
	if err != nil {
		return "", err
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode totp secret: %w", err)
	}
	plain, err := OpenSecret(s.cfg.SecretKey, sealed)
	if err != nil {
		return "", fmt.Errorf("decrypt totp secret: %w", err)
	}
	return strings.TrimSpace(string(plain)), nil
}
