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

setting() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1; }

ACCESS_MODE=$(setting ACCESS_MODE)
DOMAIN=$(setting DOMAIN)
ADMIN_DOMAIN=$(setting ADMIN_DOMAIN)
PORTAL_PORT=$(setting PORTAL_PORT)
ADMIN_PORT=$(setting ADMIN_PORT)
# Older .env files used APP_PORT (optionally with a bind address) and
# enabled Caddy whenever DOMAIN was set.
if [ -z "$PORTAL_PORT" ]; then
  PORTAL_PORT=$(setting APP_PORT | sed 's/.*://')
fi
PORTAL_PORT=${PORTAL_PORT:-8080}
ADMIN_PORT=${ADMIN_PORT:-8081}
if [ -z "$ACCESS_MODE" ]; then
  if [ -n "$DOMAIN" ]; then ACCESS_MODE=caddy; else ACCESS_MODE=direct; fi
fi

PROFILES=""
case "$ACCESS_MODE" in
  direct)
    PORTAL_BIND=0.0.0.0; ADMIN_BIND=0.0.0.0; PORTAL_TARGET=80; ADMIN_TARGET=81
    ;;
  caddy)
    if [ -z "$DOMAIN" ] || [ -z "$ADMIN_DOMAIN" ]; then
      echo "ACCESS_MODE=caddy needs DOMAIN (portal) and ADMIN_DOMAIN (admin console) in .env"
      exit 1
    fi
    PORTAL_BIND=127.0.0.1; ADMIN_BIND=127.0.0.1; PORTAL_TARGET=7080; ADMIN_TARGET=7081
    PROFILES="--profile tls"
    ;;
  cloudflare)
    if [ -z "$(setting CLOUDFLARE_TUNNEL_TOKEN)" ]; then
      echo "ACCESS_MODE=cloudflare needs CLOUDFLARE_TUNNEL_TOKEN in .env"
      exit 1
    fi
    PORTAL_BIND=127.0.0.1; ADMIN_BIND=127.0.0.1; PORTAL_TARGET=7080; ADMIN_TARGET=7081
    PROFILES="--profile tunnel"
    ;;
  proxy)
    PORTAL_BIND=127.0.0.1; ADMIN_BIND=127.0.0.1; PORTAL_TARGET=7080; ADMIN_TARGET=7081
    ;;
  *)
    echo "ACCESS_MODE must be direct, caddy, cloudflare or proxy"
    exit 1
    ;;
esac
export PORTAL_BIND ADMIN_BIND PORTAL_TARGET ADMIN_TARGET PORTAL_PORT ADMIN_PORT

# shellcheck disable=SC2086
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" $PROFILES up -d --build

echo "VPSBill is starting ($ACCESS_MODE)."
case "$ACCESS_MODE" in
  direct) echo "Customer portal: http://<server-ip>:$PORTAL_PORT   Admin console: http://<server-ip>:$ADMIN_PORT/admin" ;;
  caddy) echo "Customer portal: https://$DOMAIN   Admin console: https://$ADMIN_DOMAIN/admin" ;;
  cloudflare) echo "Point your tunnel hostnames at http://web:7080 (portal) and http://web:7081 (admin)." ;;
  proxy) echo "Proxy your portal domain to http://127.0.0.1:$PORTAL_PORT and your admin domain to http://127.0.0.1:$ADMIN_PORT." ;;
esac
echo "Finish the Web installer on the admin console, then set the public and admin addresses in 站点设置."
