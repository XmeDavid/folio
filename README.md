# Folio

Personal finance & planning app — self-hosted, multi-user.

**Stack**: Go 1.26 + Postgres 17 + Next.js 16 (PWA) + Caddy on a single VPS.

## Integrations

| Source | Via | Coverage |
|---|---|---|
| Revolut + EU banks | [GoCardless Bank Account Data](https://developer.gocardless.com/bank-account-data) | ~2,500 banks, EEA/UK |
| Interactive Brokers | IBKR Flex Web Service | all IBKR activity, per-user token |
| PostFinance + long-tail | `camt.053` XML / CSV import | manual or email-to-import |
| Anything else | Manual entry | always available |

## Repo layout

```
backend/    Go API server (chi, pgx, sqlc, River)
web/        Next.js 16 App Router (PWA via Serwist)
openapi/    OpenAPI 3.1 spec — source of truth for contracts
deploy/     Production docker-compose, Caddyfile, backup scripts
docs/       Architecture, domain model, runbooks
legacy/     Pre-rewrite code — ignored by new codebase
```

## Prerequisites

- Go 1.26+
- Node 22+ and pnpm, if running the web app outside Docker
- Docker + Docker Compose
- [Atlas](https://atlasgo.io) (migrations)
- [sqlc](https://sqlc.dev) (typed queries)

Install pnpm if you don't have it: `corepack enable && corepack prepare pnpm@9.12.0 --activate`

## Quick start (local dev)

```bash
# One-command full stack with live reload.
make dev
```

Open http://localhost:3000.

The dev backend is exposed on http://localhost:8081 by default to avoid
colliding with other projects on port 8080. The web app talks to it over the
Compose network.

If you prefer running backend/web on the host:

```bash
cp .env.example .env
docker compose -f docker-compose.dev.yml up -d db

cd backend
go run ./cmd/server

cd ../web
pnpm install
pnpm dev
```

For host backend development, make sure `.env` contains `DATABASE_URL`,
`SESSION_SECRET`, and a valid base64 `SECRET_ENCRYPTION_KEY`.

### Database migrations

Folio's application schema is managed by Atlas:

```bash
cd backend
atlas migrate apply --env local
```

River's queue tables are managed by River's own migrator:

```bash
cd backend
go run ./cmd/folio-river-migrate -direction up
```

Run Atlas first, then the River migrator. A full local reset is:

```bash
psql "$DATABASE_URL" -c 'drop schema public cascade; create schema public;'
atlas migrate apply --env local
go run ./cmd/folio-river-migrate -direction up
```

## Make targets

```bash
make dev            # start db + backend + web
make migrate        # apply migrations
make sqlc           # regenerate sqlc queries
make openapi        # regenerate Go + TS clients from spec
make test           # run all tests
make lint           # run linters
```

## Deployment

See [`deploy/README.md`](./deploy/README.md). TL;DR: one `docker-compose.yml`, Caddy for TLS, nightly `pg_dump` to object storage.

## Security notes

- Provider tokens (GoCardless, IBKR) are AES-GCM encrypted at rest with `SECRET_ENCRYPTION_KEY`.
- Auth is session-cookie based with Argon2id passwords and WebAuthn/passkeys.
- Money is stored as `numeric(19,4)` (fiat) or `numeric(28,8)` (crypto/FX) — never floats.
- TLS is enforced in production via Caddy auto-HTTPS.

## License

Private — not yet licensed for public use.
