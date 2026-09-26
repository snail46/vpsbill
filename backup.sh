#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"
BACKUP_DIR=${1:-"$ROOT_DIR/backups"}

if [ ! -f "$ENV_FILE" ]; then echo "Missing .env; deploy the system first."; exit 1; fi
mkdir -p "$BACKUP_DIR"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
TARGET="$BACKUP_DIR/vpsbill-$STAMP.dump"
SUCCESS=0
cleanup_partial() {
  if [ "$SUCCESS" -ne 1 ]; then rm -f "$TARGET" "$TARGET.sha256"; fi
}
trap cleanup_partial EXIT INT TERM

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner' > "$TARGET"
if [ ! -s "$TARGET" ]; then
  echo "Backup failed: pg_dump produced an empty file."
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$(dirname "$TARGET")" && sha256sum "$(basename "$TARGET")" > "$(basename "$TARGET").sha256")
fi
chmod 600 "$TARGET" "$TARGET.sha256" 2>/dev/null || chmod 600 "$TARGET"
SUCCESS=1
trap - EXIT INT TERM
echo "Backup created: $TARGET"
