#!/usr/bin/env sh
set -eu

if [ "${2:-}" != "--confirm" ]; then echo "Usage: ./restore.sh BACKUP.dump --confirm"; exit 1; fi
ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"
BACKUP_FILE=$1
if [ ! -f "$ENV_FILE" ]; then echo "Missing .env; deploy the system first."; exit 1; fi
if [ ! -f "$BACKUP_FILE" ]; then echo "Backup file not found: $BACKUP_FILE"; exit 1; fi
if [ -f "$BACKUP_FILE.sha256" ] && command -v sha256sum >/dev/null 2>&1; then (cd "$(dirname "$BACKUP_FILE")" && sha256sum -c "$(basename "$BACKUP_FILE").sha256"); fi

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" stop api
trap 'docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" start api >/dev/null 2>&1 || true' EXIT
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres sh -c 'exec pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --exit-on-error --no-owner' < "$BACKUP_FILE"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" start api
trap - EXIT
echo "Restore completed. Verify /health/ready before accepting traffic."
