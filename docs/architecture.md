# Architecture

## High level

```
┌──────────────────────────────┐           ┌──────────────────────┐
│ Browser / PWA (Next.js 15)   │ ───HTTPS─▶│  Caddy (TLS)         │
│  - React 19 + TanStack Query │           │  - HSTS, compression │
│  - Serwist service worker    │           └──────┬───────┬───────┘
└──────────────────────────────┘                  │       │
                                                  ▼       ▼
                                       ┌──────────────┐ ┌────────────┐
                                       │ Next.js SSR  │ │  Go API    │
                                       │ (web)        │ │  (backend) │
                                       └──────┬───────┘ └──────┬─────┘
                                              │                │
                                              └────────┬───────┘
                                                       ▼
                                                 ┌───────────┐
                                                 │ Postgres  │──┐
                                                 └───────────┘  │  River jobs
                                                                │  (same DB)
                                                                ▼
                                                ┌────────────────────────┐
                                                │ External (outbound):   │
                                                │  • GoCardless BAD API  │
                                                │  • IBKR Flex Web Svc   │
                                                │  • SMTP (notifications)│
                                                └────────────────────────┘
```

## Key decisions

| Area | Choice | Why |
|---|---|---|
| Backend | Go + chi + pgx + sqlc | Typed queries, no ORM surprises, fast, one binary |
| DB | Postgres 17 | Decimal math, strong indexing, River needs it anyway |
| Queue | River (Postgres-backed) | No Redis, transactional enqueue with business writes |
| Frontend | Next.js 15 App Router + PWA | One codebase, installable, shareable URLs |
| Auth | Session cookies + Argon2id + Passkeys | Trusted-circle app; passkeys are ideal for friends/family |
| Money | `numeric(19,4)` in DB, `decimal.Decimal` in Go, `string` on the wire | Never trust floats |
| Integrations | Adapter-per-source into unified domain model | Swap providers without app-wide changes |

## Secrets

- `SECRET_ENCRYPTION_KEY` (32 bytes, base64) encrypts provider tokens at rest via AES-GCM.
- `SESSION_SECRET` signs session cookies.
- Both live in `.env` on the VPS only; never in the repo.

## Data flow: a GoCardless sync

1. User clicks "Connect Revolut" → backend creates a GoCardless requisition, returns consent URL.
2. User authorises at bank; GoCardless redirects back to `/callback?ref=…`.
3. Backend stores the encrypted requisition id in `provider_connections`.
4. River job `GoCardlessSyncWorker` runs hourly:
   - For each connection: fetch accounts → balances → transactions.
   - Upsert into `accounts` + `transactions` keyed on `(account_id, external_id)`.
   - Update `last_synced_at`; surface errors on `last_error`.
5. Web UI polls or uses Server-Sent Events for fresh data.

## Where things will live (backend)

```
internal/
  auth/         sessions, password, webauthn
  accounts/     domain + service + handlers
  transactions/ domain + service + handlers
  categories/   domain + service + handlers
  providers/
    gocardless/ API client + sync worker
    ibkr/       Flex client + sync worker
    camt053/    ISO-20022 parser + importer
    csv/        generic CSV importer with mapping profiles
  jobs/         River queue config + worker registry
  money/        decimal + currency helpers
  crypto/       AES-GCM AEAD for secrets at rest
  http/         router, middleware, shared helpers
  db/           pgx pool, migrations, sqlc-generated dbq/
  config/       env loading + validation
```
