#!/usr/bin/env bash
# Runs the benchmarks of bench/baseline/'s three modules and compares each with
# its committed baseline using benchstat. Report-only: the comparison is
# printed and the script exits 0 whatever benchstat reports, because the
# baselines come from one machine and CI runs on other hardware. Allocation
# counts are enforced by the budget tests, not here. A benchmark that fails to
# build or run does fail the script.
#
#   scripts/bench-compare.sh           compare with the baselines
#   scripts/bench-compare.sh -update   write the baselines (see bench/baseline/README.md)
set -euo pipefail
cd "$(dirname "$0")/.."

benchstat=(go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e)

update=
case "${1:-}" in
  "") ;;
  -update) update=1 ;;
  *) echo "usage: scripts/bench-compare.sh [-update]" >&2; exit 2 ;;
esac

root=$PWD
# The load before the benchmarks start, not the load the benchmarks add.
load=$(uptime | sed 's/^.*load averages*: *//')
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# name, module directory, package: the baseline is bench/baseline/<name>.txt.
targets=(
  "root . ."
  "veloxotel contrib/veloxotel ."
  "example-graphql examples ./graphql"
)

for t in "${targets[@]}"; do
  read -r name dir pkg <<<"$t"
  out=$tmp/$name.txt
  echo "== $name ($dir $pkg)"
  (cd "$dir" && go test -run '^$' -bench . -benchmem -count 6 -benchtime 200ms "$pkg") >"$out" || { cat "$out" >&2; exit 1; }
  if [ -n "$update" ]; then
    {
      echo "# recorded: $(date -u +%Y-%m-%dT%H:%M:%SZ) by scripts/bench-compare.sh -update, $(git rev-parse --short HEAD)"
      echo "# machine: $(sysctl -n machdep.cpu.brand_string 2>/dev/null || /usr/bin/grep -m1 'model name' /proc/cpuinfo | cut -d: -f2- | sed 's/^ *//'), $(getconf _NPROCESSORS_ONLN) CPUs, $(go env GOVERSION)"
      echo "# load (1, 5, 15 min, before the run): $load"
      cat "$out"
    } >"$root/bench/baseline/$name.txt"
    echo "wrote bench/baseline/$name.txt"
  else
    "${benchstat[@]}" "baseline=$root/bench/baseline/$name.txt" "current=$out" || echo "bench-compare: benchstat failed for $name (report-only, ignored)" >&2
  fi
done
exit 0
