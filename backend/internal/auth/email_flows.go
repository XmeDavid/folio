package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

const (
	purposeEmailVerify   = "email_verify"
	purposePasswordReset = "password_reset"
	purposeEmailChange   = "email_change"

	verifyEmailTTL    = 24 * time.Hour
	passwordResetTTL  = 30 * time.Minute
	emailChangeTTL    = 24 * time.Hour
	passwordResetCopy = "30 minutes"
)

var (
	ErrTokenInvalid = errors.New("auth token invalid")
	ErrTokenExpired = errors.New("auth token expired")
)

func (s *Service) SendEmailVerification(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	row, err := q.GetUserEmailAndName(ctx, userID)
	if err != nil {
		return err
	}
	if row.EmailVerifiedAt != nil {
		return tx.Commit(ctx)
	}
	plaintext, hash := GenerateSessionToken()
	tokenID := uuidx.New()
	if err := q.InsertAuthToken(ctx, dbq.InsertAuthTokenParams{
		ID: tokenID, UserID: userID, Purpose: purposeEmailVerify,
		TokenHash: hash, Email: &row.Email,
		ExpiresAt: s.now().Add(verifyEmailTTL),
	}); err != nil {
		return fmt.Errorf("insert verify token: %w", err)
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "verify_email",
		ToAddress:      row.Email,
		IdempotencyKey: fmt.Sprintf("verify_email:%s", tokenID),
		Data: map[string]any{
			"DisplayName": row.DisplayName,
			"VerifyURL":   s.cfg.AppURL + "/auth/verify/" + plaintext,
		},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) VerifyEmail(ctx context.Context, plaintext string) error {
	return s.consumeUserToken(ctx, plaintext, purposeEmailVerify, func(ctx context.Context, tx pgx.Tx, tokenID, userID uuid.UUID, tokenEmail string) error {
		// Bind to the email the token was issued for — if the user has since
		// changed addresses, an old verify link must not re-verify the new one.
		n, err := dbq.New(tx).VerifyUserEmail(ctx, dbq.VerifyUserEmailParams{
			ID: userID, Email: tokenEmail,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrTokenInvalid
		}
		return writeAuditTx(ctx, tx, nil, &userID, "user.email_verified", "user", userID, nil, nil, nil, "")
	})
}

func (s *Service) RequestPasswordReset(ctx context.Context, email string, ip net.IP, ua string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil
	}
	row, err := dbq.New(s.pool).GetUserIDAndNameByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	plaintext, hash := GenerateSessionToken()
	tokenID := uuidx.New()
	if err := dbq.New(tx).InsertAuthToken(ctx, dbq.InsertAuthTokenParams{
		ID: tokenID, UserID: row.ID, Purpose: purposePasswordReset,
		TokenHash: hash, Email: &email,
		ExpiresAt: s.now().Add(passwordResetTTL),
	}); err != nil {
		return err
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "password_reset",
		ToAddress:      email,
		IdempotencyKey: fmt.Sprintf("password_reset:%s", tokenID),
		Data: map[string]any{
			"DisplayName": row.DisplayName,
			"ResetURL":    s.cfg.AppURL + "/reset/" + plaintext,
			"ExpiresIn":   passwordResetCopy,
		},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPassword(ctx context.Context, plaintext, newPassword string) error {
	row, err := dbq.New(s.pool).GetUserForPasswordReset(ctx, dbq.GetUserForPasswordResetParams{
		TokenHash: HashToken(plaintext),
		Purpose:   purposePasswordReset,
	})
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		return ErrTokenInvalid
	}
	if err != nil {
		return err
	}
	if err := CheckPasswordPolicy(newPassword, row.Email, row.DisplayName); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword, s.cfg.SecretKey)
	if err != nil {
		return err
	}
	return s.consumeUserToken(ctx, plaintext, purposePasswordReset, func(ctx context.Context, tx pgx.Tx, tokenID, userID uuid.UUID, _ string) error {
		q := dbq.New(tx)
		if err := q.UpdateUserPassword(ctx, dbq.UpdateUserPasswordParams{PasswordHash: hash, ID: userID}); err != nil {
			return err
		}
		if err := q.DeleteSessionsByUser(ctx, userID); err != nil {
			return err
		}
		// Kill pending MFA challenges too — otherwise an attacker who phished
		// the reset link could complete a challenge created before the reset.
		if err := q.ConsumeOpenMFAChallengesByUser(ctx, userID); err != nil {
			return err
		}
		return writeAuditTx(ctx, tx, nil, &userID, "user.password_reset_completed", "user", userID, nil, nil, nil, "")
	})
}

func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	newEmail = strings.ToLower(strings.TrimSpace(newEmail))
	if newEmail == "" || !strings.Contains(newEmail, "@") {
		return httpx.NewValidationError("email is required")
	}
	exists, err := dbq.New(s.pool).CheckEmailExistsExcludingUser(ctx, dbq.CheckEmailExistsExcludingUserParams{
		Email: newEmail, ID: userID,
	})
	if err != nil {
		return err
	}
	if exists {
		// Silent success — surfacing "already in use" to an authenticated
		// caller leaks account existence. No confirmation email is enqueued.
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbq.New(tx)
	urow, err := q.GetUserEmailAndDisplayName(ctx, userID)
	if err != nil {
		return err
	}
	plaintext, hash := GenerateSessionToken()
	tokenID := uuidx.New()
	if err := q.InsertAuthToken(ctx, dbq.InsertAuthTokenParams{
		ID: tokenID, UserID: userID, Purpose: purposeEmailChange,
		TokenHash: hash, Email: &newEmail,
		ExpiresAt: s.now().Add(emailChangeTTL),
	}); err != nil {
		return err
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "email_change_new",
		ToAddress:      newEmail,
		IdempotencyKey: fmt.Sprintf("email_change_new:%s", tokenID),
		Data: map[string]any{
			"DisplayName": urow.DisplayName,
			"ConfirmURL":  s.cfg.AppURL + "/auth/email/confirm/" + plaintext,
			"OldEmail":    urow.Email,
			"NewEmail":    newEmail,
		},
	}); err != nil {
		return err
	}
	if err := writeAuditTx(ctx, tx, nil, &userID, "user.email_change_requested", "user", userID, nil, map[string]any{"newEmail": newEmail}, nil, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ConfirmEmailChange(ctx context.Context, plaintext string) error {
	return s.consumeUserToken(ctx, plaintext, purposeEmailChange, func(ctx context.Context, tx pgx.Tx, tokenID, userID uuid.UUID, newEmail string) error {
		q := dbq.New(tx)
		urow, err := q.GetUserEmailAndDisplayName(ctx, userID)
		if err != nil {
			return err
		}
		if err := q.UpdateUserEmail(ctx, dbq.UpdateUserEmailParams{Email: newEmail, ID: userID}); err != nil {
			return err
		}
		if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
			TemplateName:   "email_change_old_notice",
			ToAddress:      urow.Email,
			IdempotencyKey: fmt.Sprintf("email_change_old_notice:%s", tokenID),
			Data: map[string]any{
				"DisplayName": urow.DisplayName,
				"OldEmail":    urow.Email,
				"NewEmail":    newEmail,
			},
		}); err != nil {
			return err
		}
		return writeAuditTx(ctx, tx, nil, &userID, "user.email_change_confirmed", "user", userID, nil, map[string]any{"oldEmail": urow.Email, "newEmail": newEmail}, nil, "")
	})
}

func (s *Service) consumeUserToken(ctx context.Context, plaintext, purpose string, fn func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbq.New(tx)
	row, err := q.GetAuthTokenForConsume(ctx, dbq.GetAuthTokenForConsumeParams{
		TokenHash: HashToken(plaintext),
		Purpose:   purpose,
	})
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		return ErrTokenInvalid
	}
	if err != nil {
		return err
	}
	email := row.Email
	if row.ExpiresAt.Before(s.now()) {
		return ErrTokenExpired
	}
	if err := fn(ctx, tx, row.ID, row.UserID, email); err != nil {
		return err
	}
	if err := q.ConsumeAuthToken(ctx, row.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) enqueueEmailTx(ctx context.Context, tx pgx.Tx, args jobs.SendEmailArgs) error {
	if s.cfg.Jobs == nil {
		return nil
	}
	opts := &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
	if _, err := s.cfg.Jobs.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("enqueue email: %w", err)
	}
	return nil
}
