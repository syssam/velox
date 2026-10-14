#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(go env GOVERSION)" in
  go1.27*) ;;
  *) echo "CI uses Go 1.27; found $(go env GOVERSION)" >&2; exit 1 ;;
esac

compose=(docker compose -f docker-compose.ci.yml -p velox2-ci)
# Registered before "up" so a failed or interrupted start still tears down.
trap '"${compose[@]}" down -v' EXIT
"${compose[@]}" up -d --wait

export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true'

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
go test -race -count=1 ./...
