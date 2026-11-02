#!/bin/sh
# Tägliches Datenbank-Backup (z. B. per Cron). Schreibt ein komprimiertes Dump nach $BACKUP_DIR
# und löscht Dumps, die älter als $KEEP_DAYS Tage sind. Backups enthalten alle Inhalte inkl.
# persönlicher Bereiche: Zugriff entsprechend einschränken und Medium verschlüsseln.
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
