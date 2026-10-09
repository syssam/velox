#!/usr/bin/env bash
# Compile cost of the generated ORM code for the "realistic" fixture, for
# velox and for vx, on the same schema.
#
#	benchmarks/compile-bench.sh <velox|vx> [scenario...]
#
# Scenarios (default: cold noop hook field):
#
#	cold   everything the ORM generated, with an empty build cache
#	noop   the same build again (should compile nothing)
#	hook   the body of a mixin hook edited and the code regenerated
#	field  one field added to one entity and the code regenerated
#	app    the 312 mock services of services_<orm>/ (24 ORM calls and ~1,200
#	       lines each) on top of the already built ORM packages; the
#	       numbers are the services' alone, the ORM is built first, untimed
#
# Only the generated packages are timed; the Go packages they import (the
# runtime, the SQL builder) are built first and are not part of the numbers.
# Set GOGC=200 (or GOMAXPROCS, -p via BENCH_P) to see their effect; VX_FLAGS
# is passed to vxgen. With REALISTIC_LIST=1 the services also have a GraphQL
# list endpoint per entity; vx then needs VX_FLAGS="-graphql ./gqlschema_vx/velox.graphql".
# Output
# is a line per scenario: wall seconds, packages compiled, CPU seconds, and
# the largest compile's memory.
#
# Run it on an idle machine: the script says so if the load is high, and its
# CPU and memory numbers are far less sensitive to load than the wall time.
set -euo pipefail

orm="${1:?usage: compile-bench.sh <velox|vx> [scenario...]}"
shift || true
scenarios=("$@")
[ ${#scenarios[@]} -eq 0 ] && scenarios=(cold noop hook field)

root="$(cd "$(dirname "$0")/.." && pwd)"
fix="$root/benchmarks/fixtures/realistic"
timing="$root/benchmarks/tools/compile-timing.sh"
P="${BENCH_P:-4}"
cd "$fix"

case "$orm" in
velox) target=./velox/... ; warm=(github.com/syssam/velox/...) ;;
vx) target=./vx/... ; warm=(github.com/syssam/velox/vx/orm) ;;
*) echo "unknown ORM $orm" >&2; exit 2 ;;
esac

export GOFLAGS=-mod=mod GOMAXPROCS="$P"
GOCACHE="$(mktemp -d "${TMPDIR:-/tmp}/compile-bench.XXXXXX")"
export GOCACHE
log="$GOCACHE/timing.log"
backup="$(mktemp -d "${TMPDIR:-/tmp}/compile-bench-src.XXXXXX")"
cp schema/mixins.go "$backup/mixins.go"
cp schema/rec010.go "$backup/rec010.go"
restore() {
	cp "$backup/mixins.go" schema/mixins.go
	cp "$backup/rec010.go" schema/rec010.go
	rm -rf "$GOCACHE" "$backup"
}
trap restore EXIT

generate() {
	if [ "$orm" = velox ]; then
		go run generate_velox.go >/dev/null
	else
		go run github.com/syssam/velox/vx/cmd/vxgen -schema example.com/realistic/schema \
			-target ./vx -package example.com/realistic/vx ${VX_FLAGS:-} >/dev/null 2>&1
	fi
}

ncpu=$(sysctl -n hw.ncpu 2>/dev/null || nproc)
load=$(uptime | sed 's/.*load averages*: *//' | awk '{gsub(",","",$1); print $1}')
echo "== $orm on the realistic fixture: -p $P, GOGC=${GOGC:-default}, $ncpu CPUs, load $load"
if awk -v l="$load" -v n="$ncpu" 'BEGIN{exit !(l > n/2)}'; then
	echo "WARNING: the machine is busy (load $load); wall times will be inflated and CPU times somewhat" >&2
fi

generate
go build "${warm[@]}" ./schema/... ./types/... >/dev/null 2>&1

build() { # label [target]
	local tgt="${2:-$target}"
	: >"$log"
	local t0 t1
	t0=$(perl -MTime::HiRes=time -e 'print time')
	TXLOG="$log" go build -p "$P" -toolexec "$timing" "$tgt" 2>&1 | tail -3
	t1=$(perl -MTime::HiRes=time -e 'print time')
	awk -v L="$1" -v W="$(echo "$t1 - $t0" | bc)" '
		$1 == "compile" { n++; c += $4 + $5; if ($6 > m) { m = $6; big = $2 } }
		END { printf "%-6s wall %6.1fs  compiled %4d packages  cpu %7.1fs  largest %5.0f MB (%s)\n", L, W, n, c, m / 1048576, big }' "$log"
}

for s in "${scenarios[@]}"; do
	case "$s" in
	cold) build cold ;;
	noop) build noop ;;
	hook)
		perl -0pi -e 's/_ = m.SetField\("workspace_id", id\)/_ = m.SetField("workspace_id", id+0)/' schema/mixins.go
		generate
		build hook
		;;
	field)
		perl -0pi -e 's/(entityID\("rec010_id"\),\n)/$1\t\tfield.String("zz_probe").Optional(),\n/' schema/rec010.go
		generate
		build field
		;;
	app)
		go run gen_services.go >/dev/null
		go build -p "$P" "$target" >/dev/null 2>&1
		build app "./services_$orm/..."
		awk '$1 == "compile" { print $4 + $5, $6 / 1048576 }' "$log" | sort -n | awk '
			{ c[NR] = $1; m[NR] = $2 }
			END { if (NR) printf "       per service: median cpu %.2fs  p90 %.2fs  max %.2fs;  median rss %.0f MB  max %.0f MB\n", c[int((NR + 1) / 2)], c[int(NR * 0.9)], c[NR], m[int((NR + 1) / 2)], m[NR] }'
		;;
	*) echo "unknown scenario $s" >&2; exit 2 ;;
	esac
done
