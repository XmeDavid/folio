#!/usr/bin/env bash
# Nightly Postgres backup.
#
# Runs inside the db container via:
#   docker compose exec db /backups/backup.sh
# or scheduled on the host via cron/systemd-timer:
#   0 3 * * *  cd /opt/folio/deploy && docker compose exec -T db /backups/backup.sh
#
# Backups are written to /backups (mounted from ./backups on the host).
# Rotate to object storage (e.g. Backblaze B2, Cloudflare R2) via rclone in a
# second step. Keep 7 daily + 4 weekly locally.

set -euo pipefail

: "${POSTGRES_DB:?}"
: "${POSTGRES_USER:?}"

OUT_DIR=/backups
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
OUT="${OUT_DIR}/folio-${STAMP}.sql.gz"

pg_dump --format=plain --no-owner --no-privileges \
  --dbname="${POSTGRES_DB}" --username="${POSTGRES_USER}" \
  | gzip -9 > "${OUT}"

echo "Wrote ${OUT} ($(du -h "${OUT}" | cut -f1))"

# Retention: 7 daily
find "${OUT_DIR}" -maxdepth 1 -type f -name "folio-*.sql.gz" -mtime +7 -delete
