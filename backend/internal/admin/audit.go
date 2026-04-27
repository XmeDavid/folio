package admin

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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
	rows, err := dbq.New(s.pool).AdminListAuditEvents(ctx, dbq.AdminListAuditEventsParams{
		FilterActorUserID: filter.ActorUserID,
		FilterWorkspaceID: filter.WorkspaceID,
		FilterAction:      action,
		FilterSince:       filter.Since,
		FilterUntil:       filter.Until,
		CursorOccurredAt:  nullTimePtr(cur.OccurredAt),
		CursorID:          nullUUIDPtr(cur.ID),
		QueryLimit:        int32(filter.Limit + 1),
	})
	if err != nil {
		return nil, Pagination{}, err
	}
	events := make([]AuditEvent, 0, filter.Limit)
	for _, r := range rows {
		events = append(events, AuditEvent{
			ID:          r.ID,
			WorkspaceID: r.WorkspaceID,
			ActorUserID: r.ActorUserID,
			EntityType:  r.EntityType,
			EntityID:    r.EntityID,
			Action:      r.Action,
			BeforeJSONB: r.BeforeJsonb,
			AfterJSONB:  r.AfterJsonb,
			OccurredAt:  r.OccurredAt,
		})
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
