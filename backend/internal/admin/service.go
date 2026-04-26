package admin

import (
	"context"
	"encoding/json"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/mailer"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type Service struct {
	pool   *pgxpool.Pool
	jobs   *jobs.Client
	mailer mailer.Mailer
	getEnv func(string) string
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, getEnv: os.Getenv}
}

func (s *Service) WithJobs(c *jobs.Client) *Service {
	s.jobs = c
	return s
}

func (s *Service) WithMailer(m mailer.Mailer) *Service {
	s.mailer = m
	return s
}

func writeAdminAudit(ctx context.Context, tx pgx.Tx, action string, actorUserID uuid.UUID, entityType string, entityID uuid.UUID, before, after any) error {
	var actor any
	if actorUserID != uuid.Nil {
		actor = actorUserID
	}
	beforeJSON, err := marshalNullableJSON(before)
	if err != nil {
		return err
	}
	afterJSON, err := marshalNullableJSON(after)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		insert into audit_events (id, workspace_id, actor_user_id, entity_type, entity_id, action, before_jsonb, after_jsonb, occurred_at)
		values ($1, null, $2, $3, $4, $5, $6, $7, now())
	`, uuidx.New(), actor, entityType, entityID, action, beforeJSON, afterJSON)
	return err
}

func (s *Service) writeAdminAuditRow(ctx context.Context, action string, actorUserID uuid.UUID, entityType string, entityID uuid.UUID, before, after any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := writeAdminAudit(ctx, tx, action, actorUserID, entityType, entityID, before, after); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func marshalNullableJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
