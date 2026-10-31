#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
# CI tests PostgreSQL 14 and 18; this runs 18, and VELOX_CI_MATRIX=1 then runs the tests again on 14.
# golangci-lint waits here for another run on the machine, which CI does not have.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD

case "$(go env GOVERSION)" in
  go1.27*) ;;
  *) echo "CI uses Go 1.27; found $(go env GOVERSION)" >&2; exit 1 ;;
esac

command -v docker >/dev/null || { echo "ci-local.sh needs docker" >&2; exit 1; }

# VELOX_CI_MATRIX=1's PostgreSQL 14 is the image of CI's matrix, read from
# ci.yml, so that it is pinned in one place.
if [ "${VELOX_CI_MATRIX:-}" = 1 ]; then
  pg14=$(sed -n 's/^ *postgres-image: *\(postgres:14@sha256:[0-9a-f]\{64\}\) *$/\1/p' .github/workflows/ci.yml)
  [ "$(printf '%s\n' "$pg14" | grep -c .)" = 1 ] || { echo "ci-local.sh: no single postgres:14 image in .github/workflows/ci.yml's matrix" >&2; exit 1; }
fi

compose=(docker compose -f scripts/docker-compose.ci.yml -p velox2-ci)
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
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
# Per-package coverage floors (scripts/coverage-floors.txt) are checked over the four modules' profiles
# below, which are measured as CI measures them: with -race, and with the database tests as run.
go test -race -count=1 -timeout 20m -coverpkg=./... -coverprofile="$root/cover-root.out" ./...
go test -run XXX -fuzz '^FuzzRender$' -fuzztime 20s ./internal/engine
go test -run XXX -fuzz '^FuzzQuote$' -fuzztime 10s ./internal/engine
go test -run XXX -fuzz '^FuzzScope$' -fuzztime 10s ./internal/engine
go test -run XXX -fuzz '^FuzzLiteral$' -fuzztime 10s ./internal/ddl
go test -run XXX -fuzz '^FuzzSplit$' -fuzztime 10s ./migrate
go test -run XXX -fuzz '^FuzzRenderWrite$' -fuzztime 10s ./internal/engine
go test -run XXX -fuzz '^FuzzConstraint$' -fuzztime 10s ./internal/engine
go test -run XXX -fuzz '^FuzzDiff$' -fuzztime 10s ./migrate
go test -run XXX -fuzz '^FuzzColumnScan$' -fuzztime 10s ./internal/conn

go build -o bin/velox-gen ./cmd/velox-gen
bin/velox-gen -check -out internal/testmodel -package testmodel ./internal/testmodel/schema
bin/velox-gen verify -out internal/testmodel ./internal/testmodel/schema
bin/velox-gen -check -out internal/testbind -package testbind ./internal/testbind/schema
bin/velox-gen verify -out internal/testbind ./internal/testbind/schema
# golangci-lint's revive skips generated files; revive itself lints velox-gen's.
go run github.com/mgechev/revive@v1.17.0 -config scripts/revive.toml -set_exit_status ./internal/testmodel ./internal/testbind

# The example is its own module: its own vet, lint, tests and generated files.
(
  cd examples
  test -z "$(gofmt -l .)" || { echo "gofmt needed in examples:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  # golangci-lint's revive skips generated files; revive itself lints velox-gen's.
  go run github.com/mgechev/revive@v1.17.0 -config ../scripts/revive.toml -set_exit_status ./*/gen
  go test -race -count=1 -coverprofile="$root/cover-example.out" ./...
  for d in */schema; do
    x=${d%/schema}
    [ "$x" = graphql ] && continue # its own lines below, with the plugin
    go tool velox-gen -check -out "./$x/gen" "./$x/schema"
    go tool velox-gen verify -out "./$x/gen" "./$x/schema"
  done
  # Each example's tables come from its committed migrations: fail on drift from the
  # schema package, and on SQL that does not replay to its schema.json (applied to a
  # temporary SQLite file, then compared with drift).
  migdb=$(mktemp -d)
  for d in */migrations; do
    x=${d%/migrations}
    go tool velox-gen migrate diff -check -dir "./$x/migrations" "./$x/schema"
    go tool velox-gen migrate apply -dir "./$x/migrations" -db "$migdb/$x.db"
    go tool velox-gen migrate drift -dir "./$x/migrations" -db "$migdb/$x.db"
  done
  rm -rf "$migdb"
  # tracker's PostgreSQL migrations: its tests apply them, under VELOX_TEST_POSTGRES.
  go tool velox-gen migrate diff -check -dir ./tracker/migrations-postgres ./tracker/schema
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
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -count=1 -coverprofile="$root/cover-veloxotel.out" ./...
)

# contrib/graphql is its own module too: the annotations package, the
# velox-graphql plugin and the runtime. Its mutation and collection itests
# (TestMutationsRun, TestCollectRun) run on SQLite, and on PostgreSQL and MySQL
# under VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL, exported above.
(
  cd contrib/graphql
  # The collection integration test builds an application offline, with gqlgen as a tool.
  go mod download
  test -z "$(gofmt -l .)" || { echo "gofmt needed in contrib/graphql:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -count=1 -coverprofile="$root/cover-graphql.out" ./...
  go test -run XXX -fuzz '^FuzzCost$' -fuzztime 10s ./veloxgql
)

# bench/compare is its own module: the comparison harness. CI checks it; the
# measurements run only from bench/compare/run.sh.
(
  cd bench/compare
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -short -count=1 ./...
)

# No package may fall below its floor, or lack one (scripts/coverage-floors.txt).
go run ./scripts/covfloor -floors scripts/coverage-floors.txt cover-root.out cover-example.out cover-veloxotel.out cover-graphql.out

go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json
go run ./bench/scale -n 500 -s 100 -graphql -check bench/scale/thresholds.json

# Benchmarks against the committed baselines (bench/baseline). Report-only: the
# baselines come from other hardware, so the deltas inform and never fail.
scripts/bench-compare.sh

# velox-graphql against the newest gqlgen, in a copy of the tree.
scripts/gqlgen-latest.sh

# VELOX_CI_MATRIX=1 runs the tests again on PostgreSQL 14, CI's other entry,
# in every module CI's 14 leg tests.
if [ "${VELOX_CI_MATRIX:-}" = 1 ]; then
  "${compose[@]}" down -v
  VELOX_CI_POSTGRES_IMAGE=$pg14 "${compose[@]}" up -d --wait
  go test -race -count=1 -timeout 20m ./...
  (cd examples && go test -race -count=1 ./...)
  (cd contrib/veloxotel && go test -race -count=1 ./...)
  (cd contrib/graphql && go test -race -count=1 ./...)
fi
