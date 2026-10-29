#!/usr/bin/env bash
# Runs the comparison of velox2 with old velox and ent: the build measurements,
# then the runtime benchmarks on SQLite and on PostgreSQL, then the summary and
# its tables. Every command is one `compare step`, timed, logged to
# <results>/steps.jsonl with the load before and after, and, where it measures,
# held at a load gate first.
#
#   bench/compare/run.sh                      a real run, into a new results directory
#   COMPARE_SMOKE=1 bench/compare/run.sh      a small, ungated run, to check the script
#   COMPARE_RESULTS=<dir> bench/compare/run.sh   resume <dir>: completed outputs are skipped
#
# The knobs, each optional (see the defaults below): COMPARE_SIZES, COMPARE_RUNS,
# COMPARE_SERVICES, COMPARE_BENCH_COUNT, COMPARE_BENCHTIME, COMPARE_GATE,
# COMPARE_GATE_WAIT, COMPARE_BENCH_GATE_WAIT, COMPARE_RESULTS.
set -euo pipefail
cd "$(dirname "$0")/../.."

if [ "${COMPARE_SMOKE:-}" = 1 ]; then
  : "${COMPARE_SIZES:=10}" "${COMPARE_RUNS:=1}" "${COMPARE_SERVICES:=2}"
  : "${COMPARE_BENCH_COUNT:=1}" "${COMPARE_BENCHTIME:=10x}" "${COMPARE_GATE:=0}"
fi
: "${COMPARE_SIZES:=100 300}" "${COMPARE_RUNS:=3}" "${COMPARE_SERVICES:=20}"
: "${COMPARE_BENCH_COUNT:=6}" "${COMPARE_BENCHTIME:=1s}" "${COMPARE_GATE:=12}"
: "${COMPARE_GATE_WAIT:=60m}" "${COMPARE_BENCH_GATE_WAIT:=10m}"

case "$(go env GOVERSION)" in
  go1.27*) ;;
  *) echo "run.sh uses Go 1.27; found $(go env GOVERSION)" >&2; exit 1 ;;
esac
command -v docker >/dev/null || { echo "run.sh needs docker" >&2; exit 1; }

compose=(docker compose -f docker-compose.ci.yml -p velox2-ci)
if [ -n "$("${compose[@]}" ps -q)" ]; then
  echo "velox2-ci is running: run.sh starts and stops its own" >&2
  exit 1
fi

results=${COMPARE_RESULTS:-bench/compare/results/$(date -u +%Y%m%dT%H%M%SZ)}
echo "results: $results"
mkdir -p "$results/raw"
results=$(cd "$results" && pwd)
raw=$results/raw
work=$(mktemp -d)
started=
cleanup() {
  if [ -n "$started" ]; then "${compose[@]}" down -v || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# The commit measured, kept from the first start of a resumed run.
if [ ! -s "$results/commit.txt" ]; then
  commit=$(git rev-parse --short HEAD)
  [ -z "$(git status --porcelain --untracked-files=no)" ] || commit=$commit-dirty
  echo "$commit" >"$results/commit.txt"
fi

orms=(velox2 oldvelox ent)
# rotated k: the ORMs, starting at the k'th (mod 3), so that the load's drift
# does not always land on the same ORM.
rotated() {
  local j
  for j in 0 1 2; do echo "${orms[$(((${1} + j) % 3))]}"; done
}

tools=$work/tools
mkdir -p "$tools" "$work/boot"
# The step runner of the tools step itself, built before any step; every later
# step runs under the tools step's compare.
go -C bench/compare build -o "$work/boot/compare" .
"$work/boot/compare" step -log "$results/steps.jsonl" -name tools -- sh -c '
  go -C bench/compare build -o "$1/compare" . &&
  go -C bench/compare build -o "$1/velox-gen" github.com/syssam/velox2/cmd/velox-gen &&
  go -C bench/compare build -o "$1/ent" entgo.io/ent/cmd/ent &&
  go -C bench/compare build -o "$1/velox" github.com/syssam/velox/cmd/velox' sh "$tools"
step=("$tools/compare" step -log "$results/steps.jsonl")

# Build: every ORM at every size, RUNS times.
for ((r = 1; r <= COMPARE_RUNS; r++)); do
  for n in $COMPARE_SIZES; do
    for orm in $(rotated $((r - 1))); do
      name=build-$orm-n$n-r$r
      out=$raw/$name.json
      if [ -e "$out" ]; then echo "skip $name"; continue; fi
      "${step[@]}" -name "$name" -gate "$COMPARE_GATE" -gate-wait "$COMPARE_GATE_WAIT" -timeout 180m -- \
        "$tools/compare" build -orm "$orm" -n "$n" -s "$COMPARE_SERVICES" -run "$r" -tools "$tools" -raw "$raw" -out "$out"
    done
  done
done

# The runtime apps and their test binaries, built offline as the harness builds its apps.
goflags="${GOFLAGS:+$GOFLAGS }-mod=mod"
for orm in "${orms[@]}"; do
  "${step[@]}" -name "runtime-app-$orm" -- "$tools/compare" runtime-app -orm "$orm" -tools "$tools" -dir "$work/rt-$orm"
  "${step[@]}" -name "test-binary-$orm" -- env GOFLAGS="$goflags" GOPROXY=off GOWORK=off \
    go -C "$work/rt-$orm" test -c -o "$work/rt-$orm.test"
done

# rounds_done d: every round file of dialect d exists.
rounds_done() {
  local i orm
  for ((i = 1; i <= COMPARE_BENCH_COUNT; i++)); do
    for orm in "${orms[@]}"; do [ -e "$raw/bench-$1-$orm-$i.txt" ] || return 1; done
  done
}

# bench d: the guards, then BENCH_COUNT rounds of each ORM's benchmarks on dialect d.
# A round is one -count 1 run of each ORM, after one gate, so that each round
# sees the three ORMs under the same load.
bench() {
  local d=$1 i orm out todo
  for orm in "${orms[@]}"; do
    "${step[@]}" -name "guard-$d-$orm" -- env COMPARE_DIALECT="$d" "$work/rt-$orm.test" -test.run '^TestGuard$'
  done
  for ((i = 1; i <= COMPARE_BENCH_COUNT; i++)); do
    todo=
    for orm in "${orms[@]}"; do [ -e "$raw/bench-$d-$orm-$i.txt" ] || todo=1; done
    if [ -z "$todo" ]; then
      echo "skip gate-$d-$i"
    else
      "${step[@]}" -name "gate-$d-$i" -gate "$COMPARE_GATE" -gate-wait "$COMPARE_BENCH_GATE_WAIT" -- true
    fi
    for orm in $(rotated $((i - 1))); do
      out=$raw/bench-$d-$orm-$i.txt
      if [ -e "$out" ]; then echo "skip bench-$d-$orm-$i"; continue; fi
      "${step[@]}" -name "bench-$d-$orm-$i" -timeout 60m -- env COMPARE_DIALECT="$d" sh -c \
        '"$1" -test.run "^\$" -test.bench . -test.benchmem -test.count 1 -test.benchtime "$3" > "$2.tmp" && mv "$2.tmp" "$2"' \
        sh "$work/rt-$orm.test" "$out" "$COMPARE_BENCHTIME"
    done
  done
}

bench sqlite

if rounds_done postgres; then
  echo "skip postgres: every round file exists"
  for ((i = 1; i <= COMPARE_BENCH_COUNT; i++)); do
    echo "skip gate-postgres-$i"
    for orm in $(rotated $((i - 1))); do echo "skip bench-postgres-$orm-$i"; done
  done
else
  started=1
  "${compose[@]}" up -d --wait postgres
  # As ci-local.sh exports its DSN: in the environment only, never in an argv or the log.
  export VELOX_COMPARE_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
  bench postgres
  unset VELOX_COMPARE_POSTGRES
  "${compose[@]}" down -v
  started=
fi

# Each ORM's rounds in one file per dialect, and benchstat over the three.
benchstat=(go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e)
for d in sqlite postgres; do
  for orm in "${orms[@]}"; do
    for ((i = 1; i <= COMPARE_BENCH_COUNT; i++)); do cat "$raw/bench-$d-$orm-$i.txt"; done >"$raw/bench-$d-$orm.txt"
  done
  "${step[@]}" -name "benchstat-$d" -- sh -c '
    out=$1; shift
    "$@" > "$out.tmp" && mv "$out.tmp" "$out"' sh "$results/benchstat-$d.txt" "${benchstat[@]}" \
    "velox2=$raw/bench-$d-velox2.txt" "oldvelox=$raw/bench-$d-oldvelox.txt" "ent=$raw/bench-$d-ent.txt"
done

"${step[@]}" -name summarize -- "$tools/compare" summarize -results "$results" -out "$results/summary.json" \
  -config runs="$COMPARE_RUNS" -config services="$COMPARE_SERVICES" -config bench_count="$COMPARE_BENCH_COUNT" \
  -config benchtime="$COMPARE_BENCHTIME" -config sizes="$COMPARE_SIZES"
"${step[@]}" -name report -- sh -c '"$1" report -summary "$2" > "$3.tmp" && mv "$3.tmp" "$3"' \
  sh "$tools/compare" "$results/summary.json" "$results/report-tables.md"

echo "results: $results"
