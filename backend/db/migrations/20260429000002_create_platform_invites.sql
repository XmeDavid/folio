-- platform_invites: platform-level invites issued by an admin to onboard a new user.
-- distinct from workspace_invites (which bind to a specific workspace).
create table platform_invites (
  id            uuid primary key,
  email         citext,                       -- nullable: admin can mint open tokens
  token_hash    bytea not null unique,
  created_by    uuid not null references users(id) on delete restrict,
  created_at    timestamptz not null default now(),
  expires_at    timestamptz not null,
  accepted_at   timestamptz,
  accepted_by   uuid references users(id) on delete set null,
  revoked_at    timestamptz,
  revoked_by    uuid references users(id) on delete set null
);

create index platform_invites_pending_idx
  on platform_invites (expires_at)
  where accepted_at is null and revoked_at is null;

create index platform_invites_created_by_idx on platform_invites (created_by);
