#!/usr/bin/env bash
# Runs bench/scale with its arguments against bench/scale/thresholds.json. The
# thresholds were measured only on darwin/arm64, and binary size and link memory
# differ by platform, so a measurement over a threshold fails only there. Elsewhere,
# such as on CI's Linux, it is a warning until a run there recalibrates them. A
# build or a run that fails is a failure everywhere.
set -euo pipefail
cd "$(dirname "$0")/.."

# The thresholds were measured at GOFLAGS=-p=2, and the ratio depends on -p
# (2026-11-24 on main: cold 7.40 at -p=2, 8.61 at the default -p=14), so the
# check builds at -p=2 unless GOFLAGS sets -p itself.
case " ${GOFLAGS:-} " in
  *" -p="*) ;;
  *) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=2" ;;
esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# Built first because go run reports every failure as exit 1, and scale's codes
# differ: 1 is over a threshold, and 2 is a run that failed.
go build -o "$tmp/scale" ./bench/scale

rc=0
"$tmp/scale" "$@" -check bench/scale/thresholds.json || rc=$?
case $rc in
  0) ;;
  1)
    if [ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" = darwin/arm64 ]; then
      exit 1
    fi
    echo "::warning title=bench/scale::over a threshold measured on darwin/arm64 only; report-only here"
    ;;
  *) exit "$rc" ;;
esac
