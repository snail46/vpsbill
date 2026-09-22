#!/usr/bin/env sh
set -eu

if [ "${1:-}" != "--confirm" ]; then echo "Usage: ./rotate-key.sh --confirm"; exit 1; fi
ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"
if [ ! -f "$ENV_FILE" ]; then echo "Missing .env"; exit 1; fi
OLD_KEY=$(sed -n 's/^ENCRYPTION_KEY=//p' "$ENV_FILE")
if [ ${#OLD_KEY} -ne 64 ]; then echo "Invalid current ENCRYPTION_KEY"; exit 1; fi
if command -v openssl >/dev/null 2>&1; then NEW_KEY=$(openssl rand -hex 32); else NEW_KEY=$(date +%s%N | sha256sum | cut -c1-64); fi
TEMP_ENV="$ENV_FILE.next"
awk -v key="$NEW_KEY" 'BEGIN{done=0} /^ENCRYPTION_KEY=/{print "ENCRYPTION_KEY=" key;done=1;next} {print} END{if(!done) print "ENCRYPTION_KEY=" key}' "$ENV_FILE" > "$TEMP_ENV"
chmod 600 "$TEMP_ENV"

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" stop api
ROTATED=0
cleanup() {
  if [ "$ROTATED" = "1" ] && [ -f "$TEMP_ENV" ]; then mv "$TEMP_ENV" "$ENV_FILE"; fi
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" start api >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" run --rm --no-deps --entrypoint /app/keyrotate -e OLD_ENCRYPTION_KEY="$OLD_KEY" -e NEW_ENCRYPTION_KEY="$NEW_KEY" api
ROTATED=1
mv "$TEMP_ENV" "$ENV_FILE"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --no-deps --force-recreate api
trap - EXIT
echo "Encryption key rotation completed. Store an encrypted backup off-host."
