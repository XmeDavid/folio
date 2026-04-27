-- name: InsertAuditEvent :exec
INSERT INTO audit_events (
    id, workspace_id, actor_user_id, action,
    entity_type, entity_id, before_jsonb, after_jsonb, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW());

-- name: InsertAuditEventWithRequest :exec
INSERT INTO audit_events (
    id, workspace_id, actor_user_id, action, entity_type, entity_id,
    before_jsonb, after_jsonb, ip, user_agent
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);
