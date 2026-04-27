package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// nullUUID returns nil for the zero UUID (so pgx writes SQL NULL) or a pointer
// to the UUID otherwise. Lets WriteAudit support user-scoped events where
// workspace_id is NULL without the caller reaching for a *uuid.UUID.
func nullUUID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}

// netIPToAddr converts a net.IP to *netip.Addr for nullable inet columns.
// Returns nil for a nil IP so pgx writes SQL NULL — matching the prior
// ipString helper behaviour.
func netIPToAddr(ip net.IP) *netip.Addr {
	if ip == nil {
		return nil
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return nil
	}
	return &addr
}

// WriteAudit is the non-transactional audit-write helper for steady-state
// events (workspace admin actions, member changes). Best-effort — errors are
// logged, not surfaced, so an audit-table blip doesn't bring down the
// foreground request. Use writeAuditTx inside transactions that atomically
// mutate the entity being audited; this method is for handlers that write
// the audit after their primary mutation has committed.
func (s *Service) WriteAudit(
	ctx context.Context,
	workspaceID, actorUserID uuid.UUID,
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
	err := dbq.New(s.pool).InsertAuditEvent(ctx, dbq.InsertAuditEventParams{
		ID:          uuidx.New(),
		WorkspaceID: nullUUID(workspaceID),
		ActorUserID: nullUUID(actorUserID),
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		BeforeJsonb: beforeJSON,
		AfterJsonb:  afterJSON,
	})
	if err != nil {
		slog.Default().Warn("audit.write_failed", "action", action, "err", err)
	}
}

// writeAuditTx inserts an audit_events row inside the provided tx.
// entity_type and entity_id are NOT NULL in the schema; use the user/workspace
// id as entity_id when the event is user-scoped.
func writeAuditTx(ctx context.Context, tx pgx.Tx, workspaceID, actorUserID *uuid.UUID,
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
	if err := dbq.New(tx).InsertAuditEventWithRequest(ctx, dbq.InsertAuditEventWithRequestParams{
		ID:          uuidx.New(),
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		BeforeJsonb: beforeJSON,
		AfterJsonb:  afterJSON,
		Ip:          netIPToAddr(ip),
		UserAgent:   &ua,
	}); err != nil {
		return fmt.Errorf("audit insert: %w", err)
	}
	return nil
}
