#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
# CI tests PostgreSQL 14 and 18; this runs 18, and VELOX_CI_MATRIX=1 then runs the tests again on 14.
# golangci-lint waits here for another run on the machine, which CI does not have.
#
# After a short serial start (the containers, the tidy checks, gofmt), the
# checks run in lanes, at once, as ci.yml's jobs do. Each lane writes a log
# of its own; the first lane to fail stops the others, and its log is
# printed. Each module's tests get a database of their own on the two
# servers, as each CI job gets its own containers. The coverage floors and
# the benchmarks run after the lanes, the benchmarks alone, so that their
# times mean something. VELOX_CI_JOBS=n runs at most n lanes at once
# (VELOX_CI_JOBS=1: one after another).
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

started=$(date +%s)
tmp=${TMPDIR:-/tmp}
logs=$(mktemp -d "${tmp%/}/velox-ci.XXXXXX")
compose=(docker compose -f scripts/docker-compose.ci.yml -p velox-ci)
pids=() # of the running lanes, by index into lanes; empty when not running
starts=()
# Stops the running lanes and everything they started.
stop_lanes() {
  local i=0
  while [ "$i" -lt "${#pids[@]}" ]; do
    if [ -n "${pids[$i]}" ]; then
      kill -TERM -- "-${pids[$i]}" 2>/dev/null || true
    fi
    i=$((i + 1))
  done
}
# Registered before "up" so a failed or interrupted start still tears down.
trap 'stop_lanes; "${compose[@]}" down -v >/dev/null 2>&1' EXIT
trap 'exit 130' INT TERM

# The DSNs of database db on the two servers. The root module's tests use velox,
# as CI's do; the other modules' lanes each use one of their own (lane_dbs).
use_db() {
  export VELOX_TEST_POSTGRES="postgres://velox:velox@127.0.0.1:55432/$1?sslmode=disable"
  export VELOX_TEST_MYSQL="velox:velox@tcp(127.0.0.1:53306)/$1?parseTime=true&clientFoundRows=true"
}
lane_dbs="velox_examples velox_veloxotel velox_graphql"
# Makes each of lane_dbs on both servers, owned or granted to velox.
make_lane_dbs() {
  local db
  for db in $lane_dbs; do
    "${compose[@]}" exec -T postgres psql -q -U velox -d velox -c "CREATE DATABASE $db"
    "${compose[@]}" exec -T -e MYSQL_PWD=velox mysql mysql -uroot -e "CREATE DATABASE $db; GRANT ALL ON $db.* TO 'velox'@'%'"
  done
}

"${compose[@]}" up -d --wait
make_lane_dbs

use_db velox
# The root DSN: the routing tests make databases beside the CI one (PostgreSQL's velox can; MySQL's cannot).
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# As in CI: the itests fail, rather than skip, when a database variable is unset.
export VELOX_ITEST_REQUIRE=1

# The serial start: what writes go.mod and go.sum, and what reads every file,
# runs before the lanes build anything.
# .claude/ holds agent worktrees, which CI does not have and go ignores (a dot directory).
unformatted=$(gofmt -l . | grep -v '^\.claude/' || true)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
# Each module's gofmt too, before velox-gen verify writes its .velox-verify-*
# directories there.
(cd examples && test -z "$(gofmt -l .)") || { echo "gofmt needed in examples:"; (cd examples && gofmt -l .); exit 1; }
(cd contrib/veloxotel && test -z "$(gofmt -l .)") || { echo "gofmt needed in contrib/veloxotel:"; (cd contrib/veloxotel && gofmt -l .); exit 1; }
(cd contrib/graphql && test -z "$(gofmt -l .)") || { echo "gofmt needed in contrib/graphql:"; (cd contrib/graphql && gofmt -l .); exit 1; }
# The tests build modules offline, with gqlgen as a tool: they need its dependencies too,
# which "go mod download" alone leaves out. The tidy check comes first: download all
# dirties go.sum, and the tidy after it restores it.
go mod tidy -diff
go mod download all
go mod tidy # download all adds to go.sum what tidy leaves out
(cd examples && go mod tidy -diff)
(cd contrib/veloxotel && go mod tidy -diff)
(cd contrib/graphql && go mod tidy -diff)
# The collection integration test builds an application offline, with gqlgen as a tool.
(cd contrib/graphql && go mod download all)
(cd contrib/graphql && go mod tidy) # download all adds to go.sum what tidy leaves out
(cd bench/compare && go mod tidy -diff)

# The lanes. Each is a function, run in the background with its output in
# its own log, under set -e.

# wait_lane waits until the lane named has passed; were it to fail, the run
# would stop this lane too.
wait_lane() {
  until [ -e "$logs/$1.ok" ]; do sleep 1; done
}

lane_root_lint() {
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
}

# Per-package coverage floors (scripts/coverage-floors.txt) are checked over the four modules' profiles
# below, which are measured as CI measures them: with -race, and with the database tests as run.
lane_root_test() {
  go test -race -count=1 -timeout 20m -coverpkg=./... -coverprofile="$root/cover-root.out" ./...
}

# The fuzz targets, with CI's 4 workers each: the lanes share the machine.
lane_fuzz() {
  export GOMAXPROCS=4
  go test -run XXX -fuzz '^FuzzRender$' -fuzztime 20s ./internal/engine
  go test -run XXX -fuzz '^FuzzQuote$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzScope$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzLiteral$' -fuzztime 10s ./internal/ddl
  go test -run XXX -fuzz '^FuzzSplit$' -fuzztime 10s ./migrate
  go test -run XXX -fuzz '^FuzzRenderWrite$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzConstraint$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzDiff$' -fuzztime 10s ./migrate
  go test -run XXX -fuzz '^FuzzColumnScan$' -fuzztime 10s ./internal/conn
  (cd contrib/graphql && go test -run XXX -fuzz '^FuzzCost$' -fuzztime 10s ./veloxgql)
}

lane_generated() {
  go build -o bin/velox-gen ./cmd/velox-gen
  bin/velox-gen -check -out internal/testmodel -package testmodel ./internal/testmodel/schema
  bin/velox-gen verify -out internal/testmodel ./internal/testmodel/schema
  bin/velox-gen -check -out internal/testbind -package testbind ./internal/testbind/schema
  bin/velox-gen verify -out internal/testbind ./internal/testbind/schema
  # golangci-lint's revive skips generated files; revive itself lints velox-gen's.
  go run github.com/mgechev/revive@v1.17.0 -config scripts/revive.toml -set_exit_status ./internal/testmodel ./internal/testbind
}

# The examples are their own module: its own vet, lint, tests and generated files.
lane_examples() (
  cd examples
  use_db velox_examples
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  # golangci-lint's revive skips generated files; revive itself lints velox-gen's.
  go run github.com/mgechev/revive@v1.17.0 -config ../scripts/revive.toml -set_exit_status ./*/gen
  go test -race -count=1 -coverprofile="$root/cover-examples.out" ./...
)

# The examples' generated files and migrations, as CI's generated job checks them.
lane_examples_generated() (
  cd examples
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
  for f in */migrations/schema.json */migrations/sqlite/schema.json; do
    d=${f%/schema.json}
    x=${d%%/*}
    go tool velox-gen migrate diff -check -dir "./$d" "./$x/schema"
    go tool velox-gen migrate apply -dir "./$d" -db "$migdb/$x.db"
    go tool velox-gen migrate drift -dir "./$d" -db "$migdb/$x.db"
  done
  rm -rf "$migdb"
  # tracker's PostgreSQL migrations: its tests apply them, under VELOX_TEST_POSTGRES.
  go tool velox-gen migrate diff -check -dir ./tracker/migrations/postgres ./tracker/schema
  go tool velox-gen -check -out ./graphql/gen -plugin velox-graphql ./graphql/schema
  go tool velox-gen verify -out ./graphql/gen -plugin velox-graphql ./graphql/schema
  # gqlgen's output is checked in: regenerate it, and fail on any difference.
  # It rewrites files the examples lane builds, so it waits for that lane.
  wait_lane examples
  go -C graphql tool gqlgen generate
  git diff --exit-code HEAD -- graphql/graph
  test -z "$(git ls-files --others --exclude-standard -- graphql/graph)"
)

# contrib/veloxotel is its own module: its own vet, lint and tests. Its PostgreSQL
# test runs under VELOX_TEST_POSTGRES.
lane_veloxotel() (
  cd contrib/veloxotel
  use_db velox_veloxotel
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -count=1 -coverprofile="$root/cover-veloxotel.out" ./...
)

# contrib/graphql is its own module too: the annotations package, the
# velox-graphql plugin and the runtime. Its mutation and collection itests
# (TestMutationsRun, TestCollectRun) run on SQLite, and on PostgreSQL and MySQL
# under VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL.
lane_graphql() (
  cd contrib/graphql
  use_db velox_graphql
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -count=1 -coverprofile="$root/cover-graphql.out" ./...
)

# bench/compare is its own module: the comparison harness. CI checks it; the
# measurements run only from bench/compare/run.sh.
lane_bench_compare() (
  cd bench/compare
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -short -count=1 ./...
)

# It fails over a threshold only where they were measured, darwin/arm64; CI only reports.
# It checks CPU-time ratios, which a busy machine leaves about the same.
lane_scale() {
  scripts/scale-check.sh -n 500 -s 100
}

lane_scale_graphql() {
  scripts/scale-check.sh -n 500 -s 100 -graphql
}

# velox-graphql against the newest gqlgen, in a copy of the tree. As CI's job,
# it has no databases: its tests would share velox with the root's.
lane_gqlgen_latest() {
  unset VELOX_TEST_POSTGRES VELOX_TEST_MYSQL VELOX_TEST_MYSQL_ADMIN VELOX_ITEST_REQUIRE
  scripts/gqlgen-latest.sh
}

# The lanes, the slowest first. The root's tests are the longest; the lanes in
# niced run at a lower priority, so that they take the cores the others leave:
# fuzzing runs for its fixed time, and the scale checks measure CPU time.
lanes=(root_test scale_graphql examples gqlgen_latest fuzz scale graphql examples_generated root_lint generated veloxotel bench_compare)
niced=" scale_graphql scale gqlgen_latest fuzz "

# Each lane runs in a process group of its own (set -m), so that stop_lanes
# reaches the go commands it started.
set -m
max=${VELOX_CI_JOBS:-${#lanes[@]}}
next=0 running=0 done_lanes=0
summary=""
echo "ci-local: ${#lanes[@]} lanes, at most $max at once; logs in $logs"
while [ "$done_lanes" -lt "${#lanes[@]}" ]; do
  while [ "$next" -lt "${#lanes[@]}" ] && [ "$running" -lt "$max" ]; do
    lane=${lanes[$next]}
    ( set -euo pipefail; "lane_$lane" ) >"$logs/$lane.log" 2>&1 &
    pids[$next]=$!
    case $niced in *" $lane "*) renice -n 10 -g "$!" >/dev/null 2>&1 || true ;; esac
    starts[$next]=$(date +%s)
    next=$((next + 1)) running=$((running + 1))
  done
  sleep 1
  for ((i = 0; i < next; i++)); do
    pid=${pids[$i]}
    [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null || continue
    rc=0
    wait "$pid" || rc=$?
    pids[$i]=""
    running=$((running - 1)) done_lanes=$((done_lanes + 1))
    lane=${lanes[$i]}
    took=$(($(date +%s) - starts[i]))
    if [ "$rc" != 0 ]; then
      stop_lanes
      echo "FAIL $lane (${took}s, exit $rc); its log, $logs/$lane.log:"
      cat "$logs/$lane.log"
      exit 1
    fi
    touch "$logs/$lane.ok"
    summary+=$(printf '%5ss  %s' "$took" "$lane")$'\n'
    printf 'ok   %-14s %5ss\n' "$lane" "$took"
  done
done
set +m

# No package may fall below its floor, or lack one (scripts/coverage-floors.txt).
go run ./scripts/covfloor -floors scripts/coverage-floors.txt cover-root.out cover-examples.out cover-veloxotel.out cover-graphql.out

# Benchmarks against the committed baselines (bench/baseline). Report-only: the
# baselines come from other hardware, so the deltas inform and never fail.
scripts/bench-compare.sh

# VELOX_CI_MATRIX=1 runs the tests again on PostgreSQL 14, CI's other entry,
# in every module CI's 14 leg tests.
if [ "${VELOX_CI_MATRIX:-}" = 1 ]; then
  "${compose[@]}" down -v
  VELOX_CI_POSTGRES_IMAGE=$pg14 "${compose[@]}" up -d --wait
  # One module at a time, all on velox, as CI's 14 leg runs them.
  go test -race -count=1 -timeout 40m ./...
  (cd examples && go test -race -count=1 ./...)
  (cd contrib/veloxotel && go test -race -count=1 ./...)
  (cd contrib/graphql && go test -race -count=1 ./...)
fi

printf '%s' "$summary"
echo "ci-local: ok in $(($(date +%s) - started))s"
rm -rf "$logs"
