-- +goose Up
-- +goose StatementBegin

-- Replace the non-unique pending-invite index with a partial UNIQUE index so
-- two concurrent Create calls for the same (tenant, email) pair can no
-- longer race through the application-level check.
drop index if exists tenant_invites_pending_email_idx;

create unique index tenant_invites_pending_email_unique
  on tenant_invites (tenant_id, email)
  where accepted_at is null and revoked_at is null;

-- Track the last-consumed TOTP time-step so a code can't be replayed inside
-- the verifier's skew window.
alter table totp_credentials
  add column if not exists last_used_step bigint;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table totp_credentials drop column if exists last_used_step;

drop index if exists tenant_invites_pending_email_unique;

create index if not exists tenant_invites_pending_email_idx
  on tenant_invites (email)
  where accepted_at is null and revoked_at is null;

-- +goose StatementEnd
