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
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
# The root DSN: the routing tests make databases beside the CI one (PostgreSQL's velox can; MySQL's cannot).
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# As on a CI runner: the itests fail, rather than skip, on a database whose variable is unset.
export CI=true

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
go vet ./...
go -C cmd/velox-gen vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
(cd cmd/velox-gen && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...)
go test -race -count=1 ./...
go -C cmd/velox-gen test -race -count=1 ./...
go test -run XXX -fuzz FuzzRender -fuzztime 20s .
go test -run XXX -fuzz FuzzQuote -fuzztime 10s .
go test -run XXX -fuzz FuzzScope -fuzztime 10s .

go -C cmd/velox-gen build -o "$PWD/bin/velox-gen" .
bin/velox-gen -check -out internal/testmodel -package testmodel ./internal/testmodel/schema
bin/velox-gen verify -out internal/testmodel ./internal/testmodel/schema

# The example is its own module: its own vet, lint, tests and generated files.
(
  cd example
  test -z "$(gofmt -l .)" || { echo "gofmt needed in example:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
  go test -race -count=1 ./...
  for x in start relations tenant policy hooks tx routing otel tour; do
    go tool velox-gen -check -out "./$x/gen" "./$x/schema"
    go tool velox-gen verify -out "./$x/gen" "./$x/schema"
  done
)

# contrib/otel is its own module: its own vet, lint and tests. Its PostgreSQL
# test runs under VELOX_TEST_POSTGRES, exported above.
(
  cd contrib/otel
  test -z "$(gofmt -l .)" || { echo "gofmt needed in contrib/otel:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
  go test -race -count=1 ./...
)

go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json
