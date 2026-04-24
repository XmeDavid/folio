package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/jobs"
)

func (s *Service) RetryJob(ctx context.Context, jobID int64, actorUserID uuid.UUID) error {
	if s.jobs == nil {
		return errors.New("admin retry job: jobs client is not configured")
	}
	if _, err := s.jobs.JobRetry(ctx, jobID); err != nil {
		return err
	}
	return s.writeAdminAuditRow(ctx, "admin.retried_job", actorUserID, "job", uuid.New(), nil, map[string]any{"jobId": jobID})
}

func (s *Service) ResendEmail(ctx context.Context, emailID uuid.UUID, actorUserID uuid.UUID) error {
	if s.jobs == nil {
		return errors.New("admin resend email: jobs client is not configured")
	}
	var args jobs.SendEmailArgs
	err := s.pool.QueryRow(ctx, `
		select template_name, to_address, idempotency_key, data
		from transactional_emails where id = $1
	`, emailID).Scan(&args.TemplateName, &args.ToAddress, &args.IdempotencyKey, &args.Data)
	if err != nil {
		return fmt.Errorf("load transactional email: %w", err)
	}
	args.IdempotencyKey = args.IdempotencyKey + ":admin-resend:" + emailID.String()
	if _, err := s.jobs.Insert(ctx, args, nil); err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `update transactional_emails set last_resent_at = now() where id = $1`, emailID)
	return s.writeAdminAuditRow(ctx, "admin.resent_email", actorUserID, "email", emailID, nil, map[string]any{"emailId": emailID})
}
