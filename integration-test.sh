#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CONTAINER="vpsbill-test-$$"
PASSWORD="vpsbill-integration-test"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required for the PostgreSQL integration test."
  exit 1
fi

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker run --rm --detach \
  --name "$CONTAINER" \
  -e POSTGRES_DB=vpsbill_test \
  -e POSTGRES_USER=vpsbill \
  -e POSTGRES_PASSWORD="$PASSWORD" \
  -p 127.0.0.1::5432 \
  postgres:16-alpine >/dev/null

ATTEMPTS=0
until docker exec "$CONTAINER" pg_isready -U vpsbill -d vpsbill_test >/dev/null 2>&1; do
  ATTEMPTS=$((ATTEMPTS + 1))
  if [ "$ATTEMPTS" -ge 30 ]; then
    docker logs "$CONTAINER"
    echo "PostgreSQL did not become ready."
    exit 1
  fi
  sleep 1
done

PORT=$(docker port "$CONTAINER" 5432/tcp | sed 's/.*://')
TEST_DATABASE_URL="postgres://vpsbill:${PASSWORD}@127.0.0.1:${PORT}/vpsbill_test?sslmode=disable"
export TEST_DATABASE_URL

go test -count=1 -run TestBillingLifecycleIntegration ./internal/store/postgres
echo "PostgreSQL integration test passed."
