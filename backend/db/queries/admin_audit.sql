-- name: AdminListAuditEvents :many
SELECT id, workspace_id, actor_user_id, entity_type, entity_id, action,
       before_jsonb, after_jsonb, occurred_at
FROM audit_events
WHERE (sqlc.narg(filter_actor_user_id)::uuid IS NULL OR actor_user_id = sqlc.narg(filter_actor_user_id))
  AND (sqlc.narg(filter_workspace_id)::uuid IS NULL OR workspace_id = sqlc.narg(filter_workspace_id))
  AND (sqlc.arg(filter_action)::text = '' OR action LIKE sqlc.arg(filter_action) || '%')
  AND (sqlc.narg(filter_since)::timestamptz IS NULL OR occurred_at >= sqlc.narg(filter_since))
  AND (sqlc.narg(filter_until)::timestamptz IS NULL OR occurred_at <= sqlc.narg(filter_until))
  AND (sqlc.narg(cursor_occurred_at)::timestamptz IS NULL OR (occurred_at, id) < (sqlc.narg(cursor_occurred_at), sqlc.narg(cursor_id)::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(query_limit);
