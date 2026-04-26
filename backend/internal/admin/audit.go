package admin

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

type auditCursor struct {
	OccurredAt time.Time `json:"occurredAt"`
	ID         uuid.UUID `json:"id"`
}

type AuditEvent struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID    *uuid.UUID      `json:"workspaceId,omitempty"`
	ActorUserID *uuid.UUID      `json:"actorUserId,omitempty"`
	EntityType  string          `json:"entityType"`
	EntityID    uuid.UUID       `json:"entityId"`
	Action      string          `json:"action"`
	BeforeJSONB json.RawMessage `json:"before,omitempty"`
	AfterJSONB  json.RawMessage `json:"after,omitempty"`
	OccurredAt  time.Time       `json:"occurredAt"`
}

func (s *Service) ListAudit(ctx context.Context, filter AuditFilter, actorUserID uuid.UUID) ([]AuditEvent, Pagination, error) {
	filter.AdminListFilter = filter.Normalize()
	var cur auditCursor
	if filter.Cursor != "" {
		if err := decodeCursor(filter.Cursor, &cur); err != nil {
			return nil, Pagination{}, httpx.NewValidationError("invalid cursor")
		}
	}
	action := strings.TrimSpace(filter.Action)
	rows, err := s.pool.Query(ctx, `
		select id, workspace_id, actor_user_id, entity_type, entity_id, action,
		       before_jsonb, after_jsonb, occurred_at
		from audit_events
		where ($1::uuid is null or actor_user_id = $1)
		  and ($2::uuid is null or workspace_id = $2)
		  and ($3::text = '' or action like $3 || '%')
		  and ($4::timestamptz is null or occurred_at >= $4)
		  and ($5::timestamptz is null or occurred_at <= $5)
		  and ($6::timestamptz is null or (occurred_at, id) < ($6, $7))
		order by occurred_at desc, id desc
		limit $8
	`, filter.ActorUserID, filter.WorkspaceID, action, filter.Since, filter.Until, nullTime(cur.OccurredAt), nullUUID(cur.ID), filter.Limit+1)
	if err != nil {
		return nil, Pagination{}, err
	}
	defer rows.Close()
	events := make([]AuditEvent, 0, filter.Limit)
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.ActorUserID, &e.EntityType, &e.EntityID, &e.Action, &e.BeforeJSONB, &e.AfterJSONB, &e.OccurredAt); err != nil {
			return nil, Pagination{}, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, Pagination{}, err
	}
	events, p, err := pageAudit(events, filter.Limit)
	if err != nil {
		return nil, Pagination{}, err
	}
	_ = s.writeAdminAuditRow(ctx, "admin.viewed_audit", actorUserID, "audit", uuid.New(), nil, map[string]any{
		"actorUserId": filter.ActorUserID,
		"workspaceId":    filter.WorkspaceID,
		"action":      action,
		"since":       filter.Since,
		"until":       filter.Until,
	})
	return events, p, nil
}

func pageAudit(rows []AuditEvent, limit int) ([]AuditEvent, Pagination, error) {
	p := Pagination{Limit: limit}
	if len(rows) <= limit {
		return rows, p, nil
	}
	spill := rows[limit]
	c, err := encodeCursor(auditCursor{OccurredAt: spill.OccurredAt, ID: spill.ID})
	if err != nil {
		return nil, Pagination{}, err
	}
	p.NextCursor = &c
	return rows[:limit], p, nil
}
