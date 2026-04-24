# Folio

Personal finance & planning app — self-hosted, multi-user.

**Stack**: Go 1.25 + Postgres 17 + Next.js 15 (PWA) + Caddy on a single VPS.

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
web/        Next.js 15 App Router (PWA via Serwist)
openapi/    OpenAPI 3.1 spec — source of truth for contracts
deploy/     Production docker-compose, Caddyfile, backup scripts
docs/       Architecture, domain model, runbooks
legacy/     Pre-rewrite code — ignored by new codebase
```

## Prerequisites

- Go 1.25+
- Node 20+ and a package manager (pnpm recommended; npm works too)
- Docker + Docker Compose
- [Atlas](https://atlasgo.io) (migrations)
- [sqlc](https://sqlc.dev) (typed queries)

Install pnpm if you don't have it: `npm install -g pnpm`

## Quick start (local dev)

```bash
# 1. Copy env
cp .env.example .env
# Edit .env — at minimum, generate SECRET_ENCRYPTION_KEY and SESSION_SECRET.

# 2. Start Postgres
docker compose -f docker-compose.dev.yml up -d

# 3. Backend
cd backend
go mod tidy
atlas migrate apply --env local       # apply migrations
sqlc generate                          # generate typed queries
go run ./cmd/server

# 4. Web (new terminal)
cd web
pnpm install
pnpm dev
```

Open http://localhost:3000.

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
