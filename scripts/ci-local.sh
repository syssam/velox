#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
# CI tests PostgreSQL 14 and 18; this runs 18, and VELOX_CI_MATRIX=1 then runs the tests again on 14.
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(go env GOVERSION)" in
  go1.27*) ;;
  *) echo "CI uses Go 1.27; found $(go env GOVERSION)" >&2; exit 1 ;;
esac

command -v docker >/dev/null || { echo "ci-local.sh needs docker" >&2; exit 1; }

compose=(docker compose -f docker-compose.ci.yml -p velox2-ci)
# Registered before "up" so a failed or interrupted start still tears down.
trap '"${compose[@]}" down -v' EXIT
"${compose[@]}" up -d --wait

export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
# The root DSN: the routing tests make databases beside the CI one (PostgreSQL's velox can; MySQL's cannot).
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# As in CI: the itests fail, rather than skip, when a database variable is unset.
export VELOX_ITEST_REQUIRE=1

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
go test -race -count=1 -timeout 20m ./...
go test -run XXX -fuzz '^FuzzRender$' -fuzztime 20s .
go test -run XXX -fuzz '^FuzzQuote$' -fuzztime 10s .
go test -run XXX -fuzz '^FuzzScope$' -fuzztime 10s .
go test -run XXX -fuzz '^FuzzLiteral$' -fuzztime 10s ./internal/ddl
go test -run XXX -fuzz '^FuzzSplit$' -fuzztime 10s ./migrate

go build -o bin/velox-gen ./cmd/velox-gen
bin/velox-gen -check -out internal/testmodel -package testmodel ./internal/testmodel/schema
bin/velox-gen verify -out internal/testmodel ./internal/testmodel/schema
bin/velox-gen -check -out internal/bindmodel -package bindmodel ./internal/bindmodel/schema
bin/velox-gen verify -out internal/bindmodel ./internal/bindmodel/schema
# golangci-lint's revive skips generated files; revive itself lints velox-gen's.
go run github.com/mgechev/revive@v1.17.0 -config revive.toml -set_exit_status ./internal/testmodel ./internal/bindmodel

# The example is its own module: its own vet, lint, tests and generated files.
(
  cd example
  test -z "$(gofmt -l .)" || { echo "gofmt needed in example:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
  # golangci-lint's revive skips generated files; revive itself lints velox-gen's.
  go run github.com/mgechev/revive@v1.17.0 -config ../revive.toml -set_exit_status ./*/gen
  go test -race -count=1 ./...
  for d in */schema; do
    x=${d%/schema}
    [ "$x" = graphql ] && continue # its own lines below, with the plugin
    go tool velox-gen -check -out "./$x/gen" "./$x/schema"
    go tool velox-gen verify -out "./$x/gen" "./$x/schema"
  done
  go tool velox-gen -check -out ./graphql/gen -plugin velox-graphql ./graphql/schema
  go tool velox-gen verify -out ./graphql/gen -plugin velox-graphql ./graphql/schema
  # gqlgen's output is checked in: regenerate it, and fail on any difference.
  go -C graphql tool gqlgen generate
  git diff --exit-code HEAD -- graphql/graph
  test -z "$(git ls-files --others --exclude-standard -- graphql/graph)"
)

# contrib/veloxotel is its own module: its own vet, lint and tests. Its PostgreSQL
# test runs under VELOX_TEST_POSTGRES, exported above.
(
  cd contrib/veloxotel
  test -z "$(gofmt -l .)" || { echo "gofmt needed in contrib/veloxotel:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
  go test -race -count=1 ./...
)

# contrib/graphql is its own module too: the annotations package, the
# velox-graphql plugin and the runtime. Its mutation and collection itests
# (TestMutationsRun, TestCollectRun) run on SQLite, and on PostgreSQL and MySQL
# under VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL, exported above.
(
  cd contrib/graphql
  # The collection itest builds an application offline, with gqlgen as a tool.
  go mod download
  test -z "$(gofmt -l .)" || { echo "gofmt needed in contrib/graphql:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
  go test -race -count=1 ./...
)

go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json
go run ./bench/scale -n 500 -s 100 -graphql -check bench/scale/thresholds.json

# velox-graphql against the newest gqlgen, in a copy of the tree.
scripts/gqlgen-latest.sh

# VELOX_CI_MATRIX=1 runs the tests again on PostgreSQL 14, CI's other entry.
if [ "${VELOX_CI_MATRIX:-}" = 1 ]; then
  "${compose[@]}" down -v
  VELOX_CI_POSTGRES_IMAGE='postgres:14@sha256:8ab94f61668dc73bbab4dc13273ff2fc37e635536af62aef5306a49a9682a446' "${compose[@]}" up -d --wait
  go test -race -count=1 -timeout 20m ./...
  (cd contrib/veloxotel && go test -race -count=1 ./...)
  (cd contrib/graphql && go test -race -count=1 ./...)
fi
