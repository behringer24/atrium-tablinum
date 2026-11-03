#!/bin/sh
# Daily database backup (e.g. via cron). Writes a compressed dump to $BACKUP_DIR
# and deletes dumps older than $KEEP_DAYS days. Backups contain all content including
# personal spaces: restrict access accordingly and encrypt the medium.
set -eu
cd "$(dirname "$0")/.."
BACKUP_DIR="${BACKUP_DIR:-./backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"

mkdir -p "$BACKUP_DIR"
umask 077
out="$BACKUP_DIR/atrium-tablinum-$(date +%Y%m%d-%H%M%S).sql.gz"
docker compose exec -T postgres pg_dump -U knowledge -d knowledge | gzip > "$out"
find "$BACKUP_DIR" -name 'atrium-tablinum-*.sql.gz' -mtime +"$KEEP_DAYS" -delete
echo "Backup: $out"
