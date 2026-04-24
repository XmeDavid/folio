package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

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
