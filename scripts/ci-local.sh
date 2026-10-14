#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -f docker-compose.ci.yml -p velox2-ci)
"${compose[@]}" up -d --wait
trap '"${compose[@]}" down -v' EXIT

export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true'

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
go vet ./...
golangci-lint run ./...
go test -race -count=1 ./...
