# Runbook

## Common operations

### Rotate `SECRET_ENCRYPTION_KEY`

Losing or rotating this key invalidates all stored provider tokens.

1. Generate new key: `openssl rand -base64 32`.
2. Write a one-off job that re-encrypts every row in `provider_connections.secrets_cipher` with the new key.
3. Swap `.env`, restart backend.
4. If rotation happened because of compromise: revoke every provider connection and re-auth from scratch.

### Restore from backup

```bash
cd /opt/folio/deploy
docker compose stop backend web
gunzip -c ./backups/folio-20260424T030000Z.sql.gz | \
  docker compose exec -T db psql -U folio -d folio
docker compose start backend web
```

### Adding a new user

Until an invite flow exists, users self-register at `/register`. Optionally:

```sql
-- Disable self-registration once your circle is onboarded:
-- add an env flag REGISTRATION_OPEN=false and gate the handler.
```

### Running a one-off GoCardless sync

```bash
docker compose exec backend /app/server jobs run gocardless-sync --user <user-id>
```

*(CLI wiring not yet implemented — to build via `spf13/cobra` when needed.)*

## Incident checklist

- [ ] Check `docker compose logs --tail 200 backend`
- [ ] Check `docker compose logs --tail 200 caddy`
- [ ] Confirm DB is healthy: `docker compose exec db pg_isready`
- [ ] Check Sentry for unhandled errors
- [ ] Check disk: `df -h` (Postgres + backups can fill up)
