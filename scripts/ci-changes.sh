#!/usr/bin/env bash
# Prints the groups of checks beyond the ORM's that a change touches, one to a
# line: graphql, otel, bench. scripts/ci-local.sh runs a group's lanes only
# when it is printed, and ci.yml's changes job runs its jobs so on a pull
# request.
#
#   scripts/ci-changes.sh BASE
#
# The change is every file that differs from BASE, a commit, in the working
# tree or the index, and every untracked file that is not ignored: the files
# committed since BASE and the ones not committed yet.
#
# A group is touched by its own files, and by what its module compiles or
# reads from the root:
#
#   graphql  contrib/graphql/**, examples/graphql/**, scripts/gqlgen-latest.sh,
#            bench/scale/**, scripts/scale-check.sh (scale_graphql runs it),
#            schema/** (the annotations' package and its loader, which fill
#            the plugin model), internal/tempmod/** (its itests use it)
#   otel     contrib/veloxotel/**, examples/otel/**, internal/testmodel/**
#            (its tests use it)
#   bench    bench/**, scripts/bench-compare.sh
#   both graphql and otel: the root's API and what generated code is:
#            internal/repocheck/testdata/api.txt (an exported name changed),
#            plugin/** (the plugin protocol velox-graphql speaks),
#            internal/codegen/** (the plugin model and the generated code
#            both modules build), scope/**, veloxpgx/**, the root's go.mod and
#            go.sum (the modules reach the root through a replace), and a
#            change to the line that declares GeneratedCodeIsVersionN.
#   all three: the CI itself: .github/workflows/ci.yml, scripts/ci-local.sh,
#            scripts/ci-changes.sh, scripts/docker-compose.ci.yml.
#
# Any other file is the ORM's: the merge gate covers it, and no group runs.
set -euo pipefail
cd "$(dirname "$0")/.."

[ $# = 1 ] || { echo "usage: scripts/ci-changes.sh BASE" >&2; exit 2; }
base=$1

changed=$({ git diff --name-only "$base" --; git ls-files --others --exclude-standard; } | sort -u)

graphql= otel= bench=
for f in $changed; do
  case $f in
    .claude/*) ;;
    contrib/graphql/*|examples/graphql/*|scripts/gqlgen-latest.sh|schema/*|internal/tempmod/*) graphql=1 ;;
    contrib/veloxotel/*|examples/otel/*|internal/testmodel/*) otel=1 ;;
    bench/scale/*|scripts/scale-check.sh) graphql=1 bench=1 ;;
    bench/*|scripts/bench-compare.sh) bench=1 ;;
    internal/repocheck/testdata/api.txt|plugin/*|internal/codegen/*|scope/*|veloxpgx/*|go.mod|go.sum) graphql=1 otel=1 ;;
    .github/workflows/ci.yml|scripts/ci-local.sh|scripts/ci-changes.sh|scripts/docker-compose.ci.yml) graphql=1 otel=1 bench=1 ;;
  esac
done
# The version that ties generated code to the runtime, wherever it is declared.
if git diff "$base" -- '*.go' ':!.claude' | grep -qE '^[-+]const GeneratedCodeIsVersion[0-9]+ '; then
  graphql=1 otel=1
fi

if [ -n "$graphql" ]; then echo graphql; fi
if [ -n "$otel" ]; then echo otel; fi
if [ -n "$bench" ]; then echo bench; fi
