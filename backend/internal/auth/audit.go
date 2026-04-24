package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// nullUUID returns nil for the zero UUID (so pgx writes SQL NULL) or the
// UUID itself otherwise. Lets WriteAudit support user-scoped events where
// tenant_id is NULL without the caller reaching for a *uuid.UUID.
func nullUUID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

// WriteAudit is the non-transactional audit-write helper for steady-state
// events (tenant admin actions, member changes). Best-effort — errors are
// logged, not surfaced, so an audit-table blip doesn't bring down the
// foreground request. Use writeAuditTx inside transactions that atomically
// mutate the entity being audited; this method is for handlers that write
// the audit after their primary mutation has committed.
func (s *Service) WriteAudit(
	ctx context.Context,
	tenantID, actorUserID uuid.UUID,
	action, entityType string, entityID uuid.UUID,
	before, after any,
) {
	var beforeJSON, afterJSON []byte
	if before != nil {
		beforeJSON, _ = json.Marshal(before)
	}
	if after != nil {
		afterJSON, _ = json.Marshal(after)
	}
	_, err := s.pool.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action,
		                          entity_type, entity_id, before_jsonb, after_jsonb, occurred_at)
		values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, now())
	`, uuidx.New(), nullUUID(tenantID), nullUUID(actorUserID),
		action, entityType, entityID, beforeJSON, afterJSON)
	if err != nil {
		slog.Default().Warn("audit.write_failed", "action", action, "err", err)
	}
}

// writeAuditTx inserts an audit_events row inside the provided tx.
// entity_type and entity_id are NOT NULL in the schema; use the user/tenant
// id as entity_id when the event is user-scoped.
func writeAuditTx(ctx context.Context, tx pgx.Tx, tenantID, actorUserID *uuid.UUID,
	action, entityType string, entityID uuid.UUID, before, after any, ip net.IP, ua string) error {

	var beforeJSON, afterJSON []byte
	if before != nil {
		b, _ := json.Marshal(before)
		beforeJSON = b
	}
	if after != nil {
		b, _ := json.Marshal(after)
		afterJSON = b
	}
	_, err := tx.Exec(ctx, `
		insert into audit_events (id, tenant_id, actor_user_id, action, entity_type, entity_id, before_jsonb, after_jsonb, ip, user_agent)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, uuidx.New(), tenantID, actorUserID, action, entityType, entityID, beforeJSON, afterJSON, ipString(ip), ua)
	if err != nil {
		return fmt.Errorf("audit insert: %w", err)
	}
	return nil
}
