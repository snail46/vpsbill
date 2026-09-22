#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required. Install Docker Engine and the Compose plugin first."
  exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "The Docker Compose plugin is required."
  exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
  if command -v openssl >/dev/null 2>&1; then
    POSTGRES_PASSWORD=$(openssl rand -hex 24)
    SESSION_SECRET=$(openssl rand -hex 32)
    ENCRYPTION_KEY=$(openssl rand -hex 32)
    PAYMENT_WEBHOOK_SECRET=$(openssl rand -hex 32)
    NOTIFICATION_WEBHOOK_SECRET=$(openssl rand -hex 32)
    METRICS_TOKEN=$(openssl rand -hex 32)
  else
    POSTGRES_PASSWORD=$(date +%s | sha256sum | cut -c1-48)
    SESSION_SECRET=$(date +%s%N | sha256sum | cut -c1-64)
    ENCRYPTION_KEY=$(date +%s%N%N | sha256sum | cut -c1-64)
    PAYMENT_WEBHOOK_SECRET=$(date +%s%N%N%N | sha256sum | cut -c1-64)
    NOTIFICATION_WEBHOOK_SECRET=$(date +%s%N%N%N%N | sha256sum | cut -c1-64)
    METRICS_TOKEN=$(date +%s%N%N%N%N%N | sha256sum | cut -c1-64)
  fi
  sed \
    -e "s|CHANGE_ME_POSTGRES|$POSTGRES_PASSWORD|" \
    -e "s|CHANGE_ME_SESSION|$SESSION_SECRET|" \
    -e "s|CHANGE_ME_ENCRYPTION|$ENCRYPTION_KEY|" \
    -e "s|CHANGE_ME_WEBHOOK|$PAYMENT_WEBHOOK_SECRET|" \
    -e "s|CHANGE_ME_NOTIFICATION|$NOTIFICATION_WEBHOOK_SECRET|" \
    -e "s|CHANGE_ME_METRICS|$METRICS_TOKEN|" \
    "$ROOT_DIR/.env.example" > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  echo "Created .env with generated secrets."
fi

ensure_secret() {
  KEY=$1
  if ! grep -q "^${KEY}=" "$ENV_FILE"; then
    if command -v openssl >/dev/null 2>&1; then VALUE=$(openssl rand -hex 32); else VALUE=$(date +%s%N | sha256sum | cut -c1-64); fi
    printf '%s=%s\n' "$KEY" "$VALUE" >> "$ENV_FILE"
    echo "Added missing $KEY to .env."
  fi
}
ensure_secret METRICS_TOKEN
chmod 600 "$ENV_FILE"

DOMAIN=$(sed -n 's/^DOMAIN=//p' "$ENV_FILE")
if [ -n "$DOMAIN" ]; then
  PUBLIC_URL=$(sed -n 's/^PUBLIC_URL=//p' "$ENV_FILE")
  if [ "$PUBLIC_URL" != "https://$DOMAIN" ]; then
    echo "When DOMAIN is set, PUBLIC_URL must equal https://$DOMAIN"
    exit 1
  fi
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" --profile tls up -d --build
else
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --build
fi
echo "CLICD Billing is starting. Open the PUBLIC_URL configured in .env."
