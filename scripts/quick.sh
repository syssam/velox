#!/usr/bin/env bash
# The inner loop: gofmt, go vet and go test -short of the modules a change
# touches, on SQLite only, without -race, and with go test's cache, so that
# what did not change is not tested again. -short skips the tests that build
# whole modules. It is not CI: run scripts/ci-local.sh before a push.
#
#   scripts/quick.sh            the modules changed since main, committed or not
#   scripts/quick.sh -all       every module
#   scripts/quick.sh -race      with the race detector
#   scripts/quick.sh -long      without -short
#
# A change to the root reaches the other modules through their replace lines:
# after one to the root's API, run it with -all.
set -euo pipefail
cd "$(dirname "$0")/.."

all=
short=-short
race=
for a in "$@"; do
  case $a in
    -all) all=1 ;;
    -race) race=-race ;;
    -long) short= ;;
    *) echo "usage: scripts/quick.sh [-all] [-race] [-long]" >&2; exit 2 ;;
  esac
done

# SQLite only: the PostgreSQL and MySQL tests skip without their variables.
unset VELOX_TEST_POSTGRES VELOX_TEST_MYSQL VELOX_TEST_MYSQL_ADMIN VELOX_ITEST_REQUIRE

# The modules, the root last: a file belongs to the first whose directory
# holds it. bench/compare/veloxe only pins a tool.
modules=(examples contrib/veloxotel contrib/graphql bench/compare .)
selected=()
if [ -n "$all" ]; then
  selected=("${modules[@]}")
else
  base=$(git merge-base HEAD main 2>/dev/null || echo HEAD)
  changed=$({ git diff --name-only "$base" --; git ls-files --others --exclude-standard; } | sort -u)
  for m in "${modules[@]}"; do
    for f in $changed; do
      case $f in
        .claude/*|bench/compare/veloxe/*) continue ;;
      esac
      owner=.
      for o in "${modules[@]}"; do
        if [ "$o" != . ] && [ "${f#"$o"/}" != "$f" ]; then owner=$o; break; fi
      done
      if [ "$owner" = "$m" ]; then selected+=("$m"); break; fi
    done
  done
  if [ "${#selected[@]}" = 0 ]; then
    echo "quick: nothing changed since main; scripts/quick.sh -all checks every module"
    exit 0
  fi
fi

started=$(date +%s)
for m in "${selected[@]}"; do
  echo "== $m"
  (
    cd "$m"
    unformatted=$(gofmt -l . | grep -v '^\.claude/' || true)
    [ -z "$unformatted" ] || { echo "gofmt needed:"; echo "$unformatted"; exit 1; }
    go vet ./...
    go test $race $short ./...
  )
done
echo "quick: ok in $(($(date +%s) - started))s (${selected[*]})"
