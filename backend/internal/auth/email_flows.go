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

	var email, displayName string
	var verifiedAt *time.Time
	if err := tx.QueryRow(ctx, `
		select email, display_name, email_verified_at from users where id = $1
	`, userID).Scan(&email, &displayName, &verifiedAt); err != nil {
		return err
	}
	if verifiedAt != nil {
		return tx.Commit(ctx)
	}
	plaintext, hash := GenerateSessionToken()
	tokenID := uuidx.New()
	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposeEmailVerify, hash, email, s.now().Add(verifyEmailTTL)); err != nil {
		return fmt.Errorf("insert verify token: %w", err)
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "verify_email",
		ToAddress:      email,
		IdempotencyKey: fmt.Sprintf("verify_email:%s", tokenID),
		Data: map[string]any{
			"DisplayName": displayName,
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
		ct, err := tx.Exec(ctx, `
			update users set email_verified_at = coalesce(email_verified_at, now())
			where id = $1 and email = $2
		`, userID, tokenEmail)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
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
	var userID uuid.UUID
	var displayName string
	err := s.pool.QueryRow(ctx, `select id, display_name from users where email = $1`, email).Scan(&userID, &displayName)
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
	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposePasswordReset, hash, email, s.now().Add(passwordResetTTL)); err != nil {
		return err
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "password_reset",
		ToAddress:      email,
		IdempotencyKey: fmt.Sprintf("password_reset:%s", tokenID),
		Data: map[string]any{
			"DisplayName": displayName,
			"ResetURL":    s.cfg.AppURL + "/reset/" + plaintext,
			"ExpiresIn":   passwordResetCopy,
		},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPassword(ctx context.Context, plaintext, newPassword string) error {
	var email, displayName string
	var userID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		select u.id, u.email, u.display_name
		from auth_tokens t join users u on u.id = t.user_id
		where t.token_hash = $1 and t.purpose = $2 and t.consumed_at is null
	`, HashToken(plaintext), purposePasswordReset).Scan(&userID, &email, &displayName)
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		return ErrTokenInvalid
	}
	if err != nil {
		return err
	}
	if err := CheckPasswordPolicy(newPassword, email, displayName); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword, s.cfg.SecretKey)
	if err != nil {
		return err
	}
	return s.consumeUserToken(ctx, plaintext, purposePasswordReset, func(ctx context.Context, tx pgx.Tx, tokenID, userID uuid.UUID, _ string) error {
		if _, err := tx.Exec(ctx, `update users set password_hash = $1 where id = $2`, hash, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from sessions where user_id = $1`, userID); err != nil {
			return err
		}
		// Kill pending MFA challenges too — otherwise an attacker who phished
		// the reset link could complete a challenge created before the reset.
		if _, err := tx.Exec(ctx, `update auth_mfa_challenges set consumed_at = now() where user_id = $1 and consumed_at is null`, userID); err != nil {
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
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from users where email = $1 and id <> $2)`, newEmail, userID).Scan(&exists); err != nil {
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
	var oldEmail, displayName string
	if err := tx.QueryRow(ctx, `select email, display_name from users where id = $1`, userID).Scan(&oldEmail, &displayName); err != nil {
		return err
	}
	plaintext, hash := GenerateSessionToken()
	tokenID := uuidx.New()
	if _, err := tx.Exec(ctx, `
		insert into auth_tokens (id, user_id, purpose, token_hash, email, expires_at)
		values ($1, $2, $3, $4, $5, $6)
	`, tokenID, userID, purposeEmailChange, hash, newEmail, s.now().Add(emailChangeTTL)); err != nil {
		return err
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "email_change_new",
		ToAddress:      newEmail,
		IdempotencyKey: fmt.Sprintf("email_change_new:%s", tokenID),
		Data: map[string]any{
			"DisplayName": displayName,
			"ConfirmURL":  s.cfg.AppURL + "/auth/email/confirm/" + plaintext,
			"OldEmail":    oldEmail,
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
		var oldEmail, displayName string
		if err := tx.QueryRow(ctx, `select email, display_name from users where id = $1`, userID).Scan(&oldEmail, &displayName); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update users set email = $1, email_verified_at = now() where id = $2`, newEmail, userID); err != nil {
			return err
		}
		if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
			TemplateName:   "email_change_old_notice",
			ToAddress:      oldEmail,
			IdempotencyKey: fmt.Sprintf("email_change_old_notice:%s", tokenID),
			Data: map[string]any{
				"DisplayName": displayName,
				"OldEmail":    oldEmail,
				"NewEmail":    newEmail,
			},
		}); err != nil {
			return err
		}
		return writeAuditTx(ctx, tx, nil, &userID, "user.email_change_confirmed", "user", userID, nil, map[string]any{"oldEmail": oldEmail, "newEmail": newEmail}, nil, "")
	})
}

func (s *Service) consumeUserToken(ctx context.Context, plaintext, purpose string, fn func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tokenID, userID uuid.UUID
	var email string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		select id, user_id, coalesce(email::text, ''), expires_at
		from auth_tokens
		where token_hash = $1 and purpose = $2 and consumed_at is null
		for update
	`, HashToken(plaintext), purpose).Scan(&tokenID, &userID, &email, &expiresAt)
	if err != nil && errors.Is(err, pgx.ErrNoRows) {
		return ErrTokenInvalid
	}
	if err != nil {
		return err
	}
	if expiresAt.Before(s.now()) {
		return ErrTokenExpired
	}
	if err := fn(ctx, tx, tokenID, userID, email); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update auth_tokens set consumed_at = now() where id = $1`, tokenID); err != nil {
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
