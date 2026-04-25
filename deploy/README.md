# Deploy

Single-VPS deployment: **host nginx → {web, backend} → Postgres**.

Folio does not bind public ports. Docker Compose publishes only localhost ports:

- `127.0.0.1:13000` → web
- `127.0.0.1:13080` → backend

The VPS-level nginx loads project configs from `/etc/nginx/sites-enabled/`.

## First-time setup

1. SSH to the VPS and create the deploy directory:
   ```bash
   sudo mkdir -p /opt/folio/deploy
   sudo chown -R "$USER":"$USER" /opt/folio
   ```
2. Copy `deploy/docker-compose.yml`, `deploy/backup.sh`, `deploy/nginx/folio.conf`, and `backend/db/migrations/` to the VPS.
   The GitHub Actions workflow will keep these files updated after the first successful deploy.
3. Create and fill `/opt/folio/.env`:
   ```bash
   # Generate secrets:
   openssl rand -base64 32          # SECRET_ENCRYPTION_KEY
   openssl rand -base64 64          # SESSION_SECRET
   ```
   Use `.env.production.example` as the template.
4. Point DNS A records for `APP_DOMAIN`, `API_DOMAIN`, and optionally `*.davidsbatista.com` at the VPS.
5. Install Docker and the Compose plugin on the VPS.
6. Make sure the shared nginx wildcard certificate exists.
7. Make sure the deploy user can write `/etc/nginx/sites-enabled/folio.conf` and run `nginx-reload`.
8. Run the GitHub Actions `Deploy` workflow from `main`.

The workflow builds and pushes:

- `ghcr.io/<owner>/folio-backend:<git-sha>`
- `ghcr.io/<owner>/folio-web:<git-sha>`

Then it SSHes into the VPS, uploads the deploy files, uploads `/etc/nginx/sites-enabled/folio.conf`, pulls the SHA-tagged images, applies Atlas and River migrations, restarts Docker Compose, and runs `nginx-reload`.

For a manual deploy, log in to GHCR and run:

```bash
cd /opt/folio
export GHCR_OWNER=<github-owner-lowercase>
export IMAGE_TAG=<git-sha-or-latest>
docker compose --env-file .env -f deploy/docker-compose.yml pull
docker compose --env-file .env -f deploy/docker-compose.yml up -d
```

## VPS nginx and TLS

The project nginx config expects a wildcard certificate at:

```text
/etc/letsencrypt/live/davidsbatista.com/fullchain.pem
/etc/letsencrypt/live/davidsbatista.com/privkey.pem
```

For a wildcard certificate, Let's Encrypt requires DNS-01 validation. The exact command depends on your DNS provider. With Cloudflare, the shape is:

```bash
sudo apt install certbot python3-certbot-dns-cloudflare
sudo certbot certonly \
  --dns-cloudflare \
  --dns-cloudflare-credentials /root/.secrets/cloudflare.ini \
  -d davidsbatista.com \
  -d '*.davidsbatista.com'
```

The deploy workflow runs:

```bash
nginx-reload
```

## GitHub Actions secrets

Set these repository secrets:

- `VPS_HOST`: VPS hostname or IP address.
- `VPS_USER`: SSH user on the VPS.
- `VPS_SSH_KEY`: private SSH key for that user.

Add the matching public key to the VPS user's `~/.ssh/authorized_keys`.

## SSH key setup

Generate a dedicated deploy key locally:

```bash
ssh-keygen -t ed25519 -C "github-actions-folio-deploy" -f ~/.ssh/folio_github_actions
```

On the VPS:

```bash
mkdir -p ~/.ssh
chmod 700 ~/.ssh
cat folio_github_actions.pub >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```

Put the private key contents in the GitHub `VPS_SSH_KEY` secret:

```bash
cat ~/.ssh/folio_github_actions
```

## Initial stack start

After the first workflow run has uploaded deploy files and pushed images:

```bash
cd /opt/folio
export GHCR_OWNER=<github-owner-lowercase>
export IMAGE_TAG=latest
docker compose --env-file .env -f deploy/docker-compose.yml up -d
```

If your GHCR packages are private, log in first with a GitHub token that has `read:packages`:

```bash
echo "<token>" | docker login ghcr.io -u "<github-user>" --password-stdin
```

Apply migrations once the backend image is available:

```bash
docker compose --env-file .env -f deploy/docker-compose.yml run --rm migrate
docker compose --env-file .env -f deploy/docker-compose.yml run --rm --entrypoint /app/folio-river-migrate backend -direction up
```

## Backups

`backup.sh` runs inside the db container and writes to `/opt/folio/deploy/backups`.

Add to the host's crontab:

```cron
0 3 * * * cd /opt/folio && docker compose --env-file .env -f deploy/docker-compose.yml exec -T db /usr/local/bin/backup.sh >> /var/log/folio-backup.log 2>&1
```

For off-site: run `rclone sync /opt/folio/deploy/backups remote:folio-backups` in a second cron entry. Keep encryption keys **off** the VPS so a compromised server can't decrypt archived data.

## Updating

Push to `main`, or run the `Deploy` workflow manually.

For an emergency manual restart:

```bash
cd /opt/folio
export GHCR_OWNER=<github-owner-lowercase>
export IMAGE_TAG=latest
docker compose --env-file .env -f deploy/docker-compose.yml pull backend web
docker compose --env-file .env -f deploy/docker-compose.yml up -d
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
