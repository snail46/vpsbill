#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"
MODE=${1:-deploy}

case "$MODE" in
  deploy|--init) ;;
  *) echo "Usage: ./deploy.sh [--init]"; exit 1 ;;
esac

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
  else
    POSTGRES_PASSWORD=$(date +%s | sha256sum | cut -c1-48)
    SESSION_SECRET=$(date +%s%N | sha256sum | cut -c1-64)
    ENCRYPTION_KEY=$(date +%s%N%N | sha256sum | cut -c1-64)
  fi
  sed \
    -e "s|CHANGE_ME_POSTGRES|$POSTGRES_PASSWORD|" \
    -e "s|CHANGE_ME_SESSION|$SESSION_SECRET|" \
    -e "s|CHANGE_ME_ENCRYPTION|$ENCRYPTION_KEY|" \
    "$ROOT_DIR/.env.example" > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  echo "Created .env with generated secrets."
fi

chmod 600 "$ENV_FILE"

if [ "$MODE" = "--init" ]; then
  echo "Initialization completed. Edit .env, then run ./deploy.sh."
  exit 0
fi

DOMAIN=$(sed -n 's/^DOMAIN=//p' "$ENV_FILE")
if [ -n "$DOMAIN" ]; then
  APP_PORT=$(sed -n 's/^APP_PORT=//p' "$ENV_FILE")
  case "$APP_PORT" in
    127.0.0.1:*|\[::1\]:*) ;;
    *)
      echo "When DOMAIN is set, APP_PORT must bind to loopback, for example 127.0.0.1:8080"
      exit 1
      ;;
  esac
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" --profile tls up -d --build
else
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --build
fi
echo "CLICD Billing is starting. Open the server address and complete the Web installer."
