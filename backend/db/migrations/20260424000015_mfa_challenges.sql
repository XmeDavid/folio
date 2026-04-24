-- +goose Up
-- +goose StatementBegin

create table auth_mfa_challenges (
  id              uuid primary key,
  user_id         uuid not null references users(id) on delete cascade,
  ip              inet not null,
  user_agent      text not null,
  created_at      timestamptz not null default now(),
  expires_at      timestamptz not null,
  consumed_at     timestamptz,
  attempts        int not null default 0,
  webauthn_state  jsonb
);

create index auth_mfa_challenges_user_id_idx
  on auth_mfa_challenges (user_id);

create index auth_mfa_challenges_live_idx
  on auth_mfa_challenges (expires_at)
  where consumed_at is null;

create unique index if not exists totp_credentials_user_id_unique
  on totp_credentials (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists auth_mfa_challenges;
drop index if exists totp_credentials_user_id_unique;
-- +goose StatementEnd
