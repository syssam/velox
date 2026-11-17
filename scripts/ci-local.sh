#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
#
#   scripts/ci-local.sh          the merge gate: the ORM's checks, and the groups
#                                the change touches (scripts/ci-changes.sh)
#   scripts/ci-local.sh --full   everything, as CI runs on a push to main
#
# Run the merge gate before a local merge, and --full before any push.
#
# Each lane has a level (lanes, below). gate lanes run in both runs. merge
# lanes run in the merge gate only: --full runs their full form instead.
# full lanes run in --full only. graphql, otel and bench lanes run in --full,
# and in the merge gate when the change touches their group: the files that
# differ from the merge base with main, committed or not, as
# scripts/ci-changes.sh reads them. ci.yml's jobs and steps carry the same
# levels: none for gate, a push for full, a pull request for merge, and the
# changes job's output for a group.
#
# CI tests PostgreSQL 14 and 18; this runs 18, and VELOX_CI_MATRIX=1 then runs the tests again on 14.
# golangci-lint waits here for another run on the machine, which CI does not have.
#
# After a short serial start (the containers, the tidy checks, gofmt), the
# checks run in lanes, at once, as ci.yml's jobs do. Each lane writes a log
# of its own; the first lane to fail stops the others, and its log is
# printed. Each module's tests get a database of their own on the two
# servers, as each CI job gets its own containers. The finals (the coverage
# floors and the benchmarks) run after the lanes, the benchmarks alone, so
# that their times mean something. VELOX_CI_JOBS=n runs at most n lanes at
# once (VELOX_CI_JOBS=1: one after another).
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD

full=
case "${1:-}" in
  "") ;;
  --full) full=1 ;;
  *) echo "usage: scripts/ci-local.sh [--full]" >&2; exit 2 ;;
esac

case "$(go env GOVERSION)" in
  go1.27*) ;;
  *) echo "CI uses Go 1.27; found $(go env GOVERSION)" >&2; exit 1 ;;
esac

command -v docker >/dev/null || { echo "ci-local.sh needs docker" >&2; exit 1; }

# The groups beyond the ORM's that this run checks. VELOX_CI_GROUPS, when
# set, names them in place of scripts/ci-changes.sh: empty for the ORM's
# checks alone.
if [ -n "$full" ]; then
  groups=" graphql otel bench "
elif [ -n "${VELOX_CI_GROUPS+set}" ]; then
  for g in $VELOX_CI_GROUPS; do
    case $g in graphql|otel|bench) ;; *) echo "VELOX_CI_GROUPS: no group $g (graphql, otel, bench)" >&2; exit 2 ;; esac
  done
  groups=" $VELOX_CI_GROUPS "
else
  base=$(git merge-base main HEAD)
  groups=" $(scripts/ci-changes.sh "$base" | tr '\n' ' ')"
fi
# selected reports whether this run runs the lanes of level $1.
selected() {
  case $1 in
    gate) return 0 ;;
    merge) [ -z "$full" ] ;;
    full) [ -n "$full" ] ;;
    *) case $groups in *" $1 "*) return 0 ;; *) return 1 ;; esac ;;
  esac
}

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
pids=() # of the running lanes, by index into run; empty when not running
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
lane_dbs="velox_examples velox_examples_graphql velox_examples_otel velox_veloxotel velox_graphql"
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
# gofmt skips what is under a dot directory, which go ignores too: .claude/
# holds agent worktrees, which CI does not have, and a velox-gen that a
# stopped lane killed can leave its .velox-gen-* or .velox-verify-* directory
# behind.
undotted() { grep -vE '(^|/)\.' || true; }
unformatted=$(gofmt -l . | undotted)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
# Each module's gofmt too. The root's covers the contrib modules' files.
unformatted=$(cd examples && gofmt -l . | undotted)
[ -z "$unformatted" ] || { echo "gofmt needed in examples:"; echo "$unformatted"; exit 1; }
# The tests build modules offline, with gqlgen as a tool: they need its dependencies too,
# which "go mod download" alone leaves out. The tidy check comes first: download all
# dirties go.sum, and the tidy after it restores it.
go mod tidy -diff
go mod download all
go mod tidy # download all adds to go.sum what tidy leaves out
(cd examples && go mod tidy -diff)

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

# The root's tests with the race detector, against both databases, but for
# bench/scale's two GraphQL tests, which root_graphql runs.
lane_root_test() {
  go test -race -count=1 -timeout 20m -skip '^(TestGraphQLServiceLinksOnlyItsEntities|TestScaleHarnessSmallGraphQL)$' ./...
}

# The same, with the coverage profile the floors read. Per-package coverage
# floors (scripts/coverage-floors.txt) are checked over the modules' profiles,
# which are measured as CI measures them: with -race, and with the database
# tests as run.
lane_root_cover() {
  go test -race -count=1 -timeout 20m -coverpkg=./... -coverprofile="$root/cover-root.out" -skip '^(TestGraphQLServiceLinksOnlyItsEntities|TestScaleHarnessSmallGraphQL)$' ./...
}

# bench/scale's GraphQL tests: the link graph and the harness of the GraphQL
# variant, which build velox-graphql's output cold. With a coverage profile,
# as root_cover's leaves them out.
lane_root_graphql() {
  go test -race -count=1 -timeout 20m -coverpkg=./... -coverprofile="$root/cover-root-graphql.out" -run '^(TestGraphQLServiceLinksOnlyItsEntities|TestScaleHarnessSmallGraphQL)$' ./bench/scale
}

# The root's fuzz targets, with CI's 4 workers each: the lanes share the machine.
lane_fuzz() {
  export GOMAXPROCS=4
  go test -run XXX -fuzz '^FuzzRender$' -fuzztime 20s ./internal/engine
  go test -run XXX -fuzz '^FuzzQuote$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzScope$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzLiteral$' -fuzztime 10s ./internal/ddl
  go test -run XXX -fuzz '^FuzzSplit$' -fuzztime 10s ./migrate
  go test -run XXX -fuzz '^FuzzRenderWrite$' -fuzztime 10s ./internal/engine
  go test -run XXX -fuzz '^FuzzConstraint$' -fuzztime 10s ./internal/dberr
  go test -run XXX -fuzz '^FuzzDiff$' -fuzztime 10s ./migrate
  go test -run XXX -fuzz '^FuzzColumnScan$' -fuzztime 10s ./internal/conn
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

# The examples are their own module: its own vet, lint, tests and generated
# files. Its vet and lint cover every example; the tests of examples/graphql
# and examples/otel run with their groups.
lane_examples() (
  cd examples
  use_db velox_examples
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  # golangci-lint's revive skips generated files; revive itself lints velox-gen's.
  go run github.com/mgechev/revive@v1.17.0 -config ../scripts/revive.toml -set_exit_status ./*/veloxgen
  go test -race -count=1 -coverprofile="$root/cover-examples.out" $(go list ./... | grep -vE '/examples/(graphql|otel)(/|$)')
)

# The examples' generated files and migrations, as CI's generated job checks them.
lane_examples_generated() (
  cd examples
  for d in */veloxschema; do
    x=${d%/veloxschema}
    [ "$x" = graphql ] && continue # its own lane, with the plugin
    go tool velox-gen -check -out "./$x/veloxgen" "./$x/veloxschema"
    go tool velox-gen verify -out "./$x/veloxgen" "./$x/veloxschema"
  done
  # Each example's tables come from its committed migrations: fail on drift from the
  # schema package, and on SQL that does not replay to its schema.json (applied to a
  # temporary SQLite file, then compared with drift).
  migdb=$(mktemp -d)
  for f in */migrations/schema.json */migrations/sqlite/schema.json; do
    d=${f%/schema.json}
    x=${d%%/*}
    go tool velox-gen migrate diff -check -dir "./$d" "./$x/veloxschema"
    go tool velox-gen migrate apply -dir "./$d" -db "$migdb/$x.db"
    go tool velox-gen migrate drift -dir "./$d" -db "$migdb/$x.db"
  done
  rm -rf "$migdb"
  # tracker's PostgreSQL migrations: its tests apply them, under VELOX_TEST_POSTGRES.
  go tool velox-gen migrate diff -check -dir ./tracker/migrations/postgres ./tracker/veloxschema
)

# velox's own build cost. It fails over a threshold only where they were
# measured, darwin/arm64; CI only reports. It checks CPU-time ratios, which a
# busy machine leaves about the same.
lane_scale() {
  scripts/scale-check.sh -n 500 -s 100
}

# contrib/graphql is its own module: the annotations package, the
# velox-graphql plugin and the runtime. Its mutation and collection itests
# (TestMutationsRun, TestCollectRun) run on SQLite, and on PostgreSQL and MySQL
# under VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL. The collection integration
# test builds an application offline, with gqlgen as a tool: go mod download
# all fetches its dependencies, after the tidy check, which it would fail.
lane_graphql() (
  cd contrib/graphql
  use_db velox_graphql
  go mod tidy -diff
  go mod download all
  go mod tidy # download all adds to go.sum what tidy leaves out
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -count=1 -coverprofile="$root/cover-graphql.out" ./...
  export GOMAXPROCS=4 # CI's 4 workers, as the fuzz lane's
  go test -run XXX -fuzz '^FuzzCost$' -fuzztime 10s ./veloxgql/internal/cost
)

# examples/graphql: its generated files, with the plugin, its tests, and
# gqlgen's output, which is checked in. gqlgen rewrites files the examples
# lane builds, so it waits for that lane.
lane_examples_graphql() (
  cd examples
  use_db velox_examples_graphql
  go tool velox-gen -check -out ./graphql/veloxgen -plugin velox-graphql ./graphql/veloxschema
  go tool velox-gen verify -out ./graphql/veloxgen -plugin velox-graphql ./graphql/veloxschema
  go test -race -count=1 -timeout 20m -coverprofile="$root/cover-examples-graphql.out" ./graphql/...
  wait_lane examples
  # Regenerate gqlgen's output, and fail on any difference.
  go -C graphql tool gqlgen generate
  git diff --exit-code HEAD -- graphql/graph
  test -z "$(git ls-files --others --exclude-standard -- graphql/graph)"
)

lane_scale_graphql() {
  scripts/scale-check.sh -n 500 -s 100 -graphql
}

# velox-graphql against the newest gqlgen, in a copy of the tree. As CI's job,
# it has no databases: its tests would share velox with the root's.
lane_gqlgen_latest() {
  unset VELOX_TEST_POSTGRES VELOX_TEST_MYSQL VELOX_TEST_MYSQL_ADMIN VELOX_ITEST_REQUIRE
  scripts/gqlgen-latest.sh
}

# contrib/veloxotel is its own module: its own vet, lint and tests. Its
# PostgreSQL test runs under VELOX_TEST_POSTGRES.
lane_veloxotel() (
  cd contrib/veloxotel
  use_db velox_veloxotel
  go mod tidy -diff
  go vet ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -count=1 -coverprofile="$root/cover-veloxotel.out" ./...
)

# examples/otel's tests.
lane_examples_otel() (
  cd examples
  use_db velox_examples_otel
  go test -race -count=1 -coverprofile="$root/cover-examples-otel.out" ./otel/...
)

# bench/compare is its own module: the comparison harness. CI checks it; the
# measurements run only from bench/compare/run.sh.
lane_bench_compare() (
  cd bench/compare
  go mod tidy -diff
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners ./...
  go test -race -short -count=1 ./...
)

# The finals run after the lanes, one after another.

# No package may fall below its floor, or lack one (scripts/coverage-floors.txt).
final_covfloor() {
  go run ./scripts/covfloor -floors scripts/coverage-floors.txt cover-root.out cover-root-graphql.out cover-examples.out cover-examples-graphql.out cover-examples-otel.out cover-veloxotel.out cover-graphql.out
}

# Benchmarks against the committed baselines (bench/baseline). Report-only: the
# baselines come from other hardware, so the deltas inform and never fail.
final_bench() {
  scripts/bench-compare.sh
}

# The lanes and their levels, the slowest first. The root's tests are the
# longest; the lanes in niced run at a lower priority, so that they take the
# cores the others leave: fuzzing runs for its fixed time, and the scale
# checks measure CPU time.
lanes=(root_test:merge root_cover:full scale_graphql:graphql root_graphql:graphql examples:gate gqlgen_latest:graphql fuzz:gate scale:gate graphql:graphql examples_generated:gate examples_graphql:graphql root_lint:gate generated:gate veloxotel:otel examples_otel:otel bench_compare:bench)
niced=" scale_graphql scale gqlgen_latest fuzz "
finals=(covfloor:full bench:bench)

run=()
for l in "${lanes[@]}"; do
  if selected "${l#*:}"; then run+=("${l%%:*}"); fi
done

# Each lane runs in a process group of its own (set -m), so that stop_lanes
# reaches the go commands it started.
set -m
max=${VELOX_CI_JOBS:-${#run[@]}}
next=0 running=0 done_lanes=0
summary=""
named=$(echo $groups)
if [ -n "$full" ]; then level=--full; else level="merge gate, groups: ${named:-none}"; fi
echo "ci-local ($level): ${#run[@]} lanes, at most $max at once; logs in $logs"
while [ "$done_lanes" -lt "${#run[@]}" ]; do
  while [ "$next" -lt "${#run[@]}" ] && [ "$running" -lt "$max" ]; do
    lane=${run[$next]}
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
    lane=${run[$i]}
    took=$(($(date +%s) - starts[i]))
    if [ "$rc" != 0 ]; then
      stop_lanes
      echo "FAIL $lane (${took}s, exit $rc); its log, $logs/$lane.log:"
      cat "$logs/$lane.log"
      exit 1
    fi
    touch "$logs/$lane.ok"
    summary+=$(printf '%5ss  %s' "$took" "$lane")$'\n'
    printf 'ok   %-18s %5ss\n' "$lane" "$took"
  done
done
set +m

for f in "${finals[@]}"; do
  if selected "${f#*:}"; then
    s=$(date +%s)
    "final_${f%%:*}"
    summary+=$(printf '%5ss  %s' "$(($(date +%s) - s))" "${f%%:*}")$'\n'
  fi
done

# VELOX_CI_MATRIX=1 runs the tests again on PostgreSQL 14, CI's other entry,
# in every module CI's 14 leg tests in a run of this level.
if [ "${VELOX_CI_MATRIX:-}" = 1 ]; then
  "${compose[@]}" down -v
  VELOX_CI_POSTGRES_IMAGE=$pg14 "${compose[@]}" up -d --wait
  # One module at a time, all on velox, as CI's 14 leg runs them.
  go test -race -count=1 -timeout 40m -skip '^(TestGraphQLServiceLinksOnlyItsEntities|TestScaleHarnessSmallGraphQL)$' ./...
  (cd examples && go test -race -count=1 $(go list ./... | grep -vE '/examples/(graphql|otel)(/|$)'))
  if selected otel; then
    (cd contrib/veloxotel && go test -race -count=1 ./...)
    (cd examples && go test -race -count=1 ./otel/...)
  fi
  if selected graphql; then
    (cd contrib/graphql && go test -race -count=1 ./...)
    (cd examples && go test -race -count=1 -timeout 20m ./graphql/...)
  fi
fi

printf '%s' "$summary"
echo "ci-local ($level): ok in $(($(date +%s) - started))s"
rm -rf "$logs"
