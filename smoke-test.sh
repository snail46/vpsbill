#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
if [ ! -f "$ENV_FILE" ]; then echo "Missing .env"; exit 1; fi
PUBLIC_URL=$(sed -n 's/^PUBLIC_URL=//p' "$ENV_FILE")
METRICS_TOKEN=$(sed -n 's/^METRICS_TOKEN=//p' "$ENV_FILE")
: "${PUBLIC_URL:?PUBLIC_URL is required}"

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
curl --fail --silent --show-error -H "Authorization: Bearer $METRICS_TOKEN" "$PUBLIC_URL/metrics" | grep -q clicd_billing_accounts
echo "Smoke test passed for $PUBLIC_URL"
