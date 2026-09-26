#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT_DIR"
go test ./...
go vet ./...
go build ./cmd/server
go build ./cmd/keyrotate
(cd web && corepack pnpm install --frozen-lockfile && corepack pnpm run build)
if command -v docker >/dev/null 2>&1; then
  COMPOSE_ENV=.env.example
  if [ -f .env ]; then COMPOSE_ENV=.env; fi
  docker compose --env-file "$COMPOSE_ENV" -f deploy/docker-compose.yml config >/dev/null
  docker compose --env-file "$COMPOSE_ENV" -f deploy/docker-compose.yml --profile tls config >/dev/null
  "$ROOT_DIR/integration-test.sh"
  docker build -t vpsbill-api:verify .
  docker build -t vpsbill-web:verify web
fi
echo "All available verification checks passed."
