package auth

import (
	"context"
	"crypto/subtle"
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

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

const (
	totpPeriodSeconds int64 = 30
	totpSkewSteps     int64 = 1
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
	ErrTOTPReplay          = errors.New("totp code already used")
	ErrMFARequired         = errors.New("mfa required")
)

func (s *Service) GetUserByID(ctx context.Context, userID uuid.UUID) (identity.User, error) {
	row, err := dbq.New(s.pool).GetUserByID(ctx, userID)
	if err != nil {
		return identity.User{}, err
	}
	return identity.User{
		ID: row.ID, Email: row.Email, DisplayName: row.DisplayName,
		EmailVerifiedAt: row.EmailVerifiedAt, IsAdmin: row.IsAdmin,
		LastWorkspaceID: row.LastWorkspaceID, CreatedAt: row.CreatedAt,
	}, nil
}

func (s *Service) MFAStatus(ctx context.Context, userID uuid.UUID) (MFAStatus, error) {
	row, err := dbq.New(s.pool).GetMFAStatus(ctx, userID)
	if err != nil {
		return MFAStatus{}, err
	}
	return MFAStatus{
		TOTPEnrolled:           row.TotpEnrolled,
		PasskeyCount:           int(row.PasskeyCount),
		RemainingRecoveryCodes: int(row.RemainingRecoveryCodes),
	}, nil
}

func (s *Service) EnrollTOTP(ctx context.Context, userID uuid.UUID) (TOTPSetup, error) {
	user, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return TOTPSetup{}, err
	}
	q := dbq.New(s.pool)
	verifiedAt, err := q.GetTOTPVerifiedAt(ctx, userID)
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
	n, err := q.UpsertTOTPCredential(ctx, dbq.UpsertTOTPCredentialParams{
		ID:           uuidx.New(),
		UserID:       userID,
		SecretCipher: base64.StdEncoding.EncodeToString(sealed),
		CreatedAt:    s.now().UTC(),
	})
	if err != nil {
		return TOTPSetup{}, err
	}
	if n == 0 {
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
	if err := dbq.New(tx).ConfirmTOTPCredential(ctx, dbq.ConfirmTOTPCredentialParams{
		UserID: userID, VerifiedAt: &now,
	}); err != nil {
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
	q := dbq.New(tx)
	n, err := q.DeleteTOTPCredential(ctx, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTOTPNotEnrolled
	}
	if err := q.DeleteOtherSessionsByUser(ctx, dbq.DeleteOtherSessionsByUserParams{
		UserID: userID, ID: currentSessionID,
	}); err != nil {
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
	step, ok, err := matchTOTPStep(secret, code, s.now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return ErrTOTPInvalidCode
	}
	// Replay guard: only accept this code if its time-step is strictly
	// greater than the last consumed step. The conditional UPDATE makes the
	// check + commit atomic — concurrent callers cannot both win.
	n, err := dbq.New(s.pool).BumpTOTPLastUsedStep(ctx, dbq.BumpTOTPLastUsedStepParams{
		UserID: userID, LastUsedStep: &step,
	})
	if err != nil {
		return fmt.Errorf("totp step bump: %w", err)
	}
	if n == 0 {
		return ErrTOTPReplay
	}
	return nil
}

// matchTOTPStep validates code against secret and returns the matching
// time-step (Unix-seconds / period). Tries every step in [-skew, +skew] so
// callers know exactly which window matched and can record it.
func matchTOTPStep(secret, code string, now time.Time) (int64, bool, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0, false, nil
	}
	opts := totp.ValidateOpts{
		Period:    uint(totpPeriodSeconds),
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
	nowStep := now.Unix() / totpPeriodSeconds
	for offset := -totpSkewSteps; offset <= totpSkewSteps; offset++ {
		step := nowStep + offset
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriodSeconds, 0), opts)
		if err != nil {
			return 0, false, err
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return step, true, nil
		}
	}
	return 0, false, nil
}

func (s *Service) loadTOTPSecret(ctx context.Context, userID uuid.UUID, requireVerified bool) (string, error) {
	q := dbq.New(s.pool)
	var encoded string
	var err error
	if requireVerified {
		encoded, err = q.GetTOTPSecretCipher(ctx, userID)
	} else {
		encoded, err = q.GetTOTPSecretCipherAny(ctx, userID)
	}
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
