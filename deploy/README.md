# Deploy

Single-VPS deployment: **Caddy → {web, backend} → Postgres**, all Docker Compose.

## First-time setup

1. SSH to the VPS, clone the repo:
   ```bash
   git clone <repo> /opt/folio && cd /opt/folio/deploy
   ```
2. Copy and fill env:
   ```bash
   cp .env.production.example ../.env
   # Generate secrets:
   openssl rand -base64 32          # SECRET_ENCRYPTION_KEY
   openssl rand -base64 64          # SESSION_SECRET
   ```
3. Point DNS A records for `APP_DOMAIN` and `API_DOMAIN` at the VPS.
4. Bring up the stack:
   ```bash
   docker compose --env-file ../.env up -d --build
   ```
   Caddy fetches TLS certs automatically on first start.
5. Apply migrations (one-off):
   ```bash
   docker compose run --rm backend /app/server migrate up
   ```
   *(Or run Atlas from the host against `DATABASE_URL`.)*

## Backups

`backup.sh` runs inside the db container and writes to `./backups`.

Add to the host's crontab:

```cron
0 3 * * * cd /opt/folio/deploy && docker compose exec -T db /backups/backup.sh >> /var/log/folio-backup.log 2>&1
```

For off-site: run `rclone sync ./backups remote:folio-backups` in a second cron entry. Keep encryption keys **off** the VPS so a compromised server can't decrypt archived data.

## Updating

```bash
cd /opt/folio && git pull
cd deploy && docker compose --env-file ../.env up -d --build
```

For zero-downtime: blue-green later. This is fine for 10–20 users.

## Checklist before first real user

- [ ] TLS working for both domains (`curl -I https://…`)
- [ ] HSTS header present
- [ ] Backup cron entry installed and tested (restore into a throwaway db)
- [ ] `SECRET_ENCRYPTION_KEY` backed up **somewhere else** — losing it means all stored provider tokens are unrecoverable
- [ ] Sentry DSN set (optional but recommended)
- [ ] `ufw` or cloud firewall allows only 22 / 80 / 443
- [ ] Postgres port 5432 NOT exposed to the internet (only on the internal compose network)
