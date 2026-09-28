#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
if [ ! -f "$ENV_FILE" ]; then echo "Missing .env"; exit 1; fi
MODE=$(sed -n 's/^ACCESS_MODE=//p' "$ENV_FILE")
DOMAIN=$(sed -n 's/^DOMAIN=//p' "$ENV_FILE")
PORT=$(sed -n 's/^PORTAL_PORT=//p' "$ENV_FILE")
[ -n "$PORT" ] || PORT=$(sed -n 's/^APP_PORT=//p' "$ENV_FILE")
PORT=${PORT:-8080}
# Behind Caddy the portal is reached through its domain; every other mode
# publishes the portal port on this host.
if [ "$MODE" = caddy ] && [ -n "$DOMAIN" ]; then PUBLIC_URL="https://$DOMAIN"; else PUBLIC_URL="http://127.0.0.1:${PORT##*:}"; fi

ATTEMPTS=0
until curl --fail --silent --show-error "$PUBLIC_URL/health/ready" >/dev/null 2>&1; do
  ATTEMPTS=$((ATTEMPTS + 1))
  if [ "$ATTEMPTS" -ge 30 ]; then
    echo "Service did not become ready at $PUBLIC_URL within 60 seconds."
    exit 1
  fi
  sleep 2
done

curl --fail --silent --show-error "$PUBLIC_URL/health/live" >/dev/null
curl --fail --silent --show-error "$PUBLIC_URL/api/v1/meta" >/dev/null
echo "Smoke test passed for $PUBLIC_URL"
