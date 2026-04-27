-- name: SweepDeletedWorkspaces :many
DELETE FROM workspaces
WHERE deleted_at IS NOT NULL
  AND deleted_at < now() - make_interval(secs => @grace_period_seconds::float8)
RETURNING id::text;
