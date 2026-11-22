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
# A group is touched by the files its lanes read:
#
#   - its own trees and scripts, by path:
#       graphql  contrib/graphql/**, examples/graphql/**,
#                scripts/gqlgen-latest.sh, bench/scale/** and
#                scripts/scale-check.sh (scale_graphql runs them)
#       otel     contrib/veloxotel/**, examples/otel/**
#       bench    bench/**, scripts/bench-compare.sh
#   - the repository's packages its modules build from, derived with go list
#     -deps (deps, below): a non-test .go file in the directory of such a
#     package, or a file it embeds. graphql's are those of contrib/graphql's
#     and examples/graphql's packages and tests, and of what an application
#     velox-gen writes builds from (app, below), which its tests build in
#     temporary modules. otel's are those of contrib/veloxotel's and
#     examples/otel's. bench's are those of bench/compare's, and of app's,
#     which it builds to measure them.
#   - what every module reaches the root through: the root's go.mod and
#     go.sum (a replace), and examples/go.mod and go.sum for graphql and otel,
#     and a change to the line that declares GeneratedCodeIsVersionN, which
#     ties generated code to the runtime (graphql and otel).
#   - the CI itself, for all three: .github/workflows/ci.yml,
#     scripts/ci-local.sh, scripts/ci-changes.sh,
#     scripts/docker-compose.ci.yml.
#
# Any other file is the ORM's: the merge gate covers it, and no group runs.
#
#   scripts/ci-changes.sh --files
#
# reads the changed paths from stdin, one to a line, in place of git, and
# leaves out the version line's check. VELOX_CI_CHANGES_NODEPS=1 leaves out
# the derived files, for a test that runs a copy of this script outside the
# repository. A go list that fails fails the script.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD

[ $# = 1 ] || { echo "usage: scripts/ci-changes.sh BASE | --files" >&2; exit 2; }
base=$1

if [ "$base" = --files ]; then
  changed=$(sort -u)
else
  changed=$({ git diff --name-only "$base" --; git ls-files --others --exclude-standard; } | sort -u)
fi

# deps prints the repository's files that the packages named, in the module
# of directory $1, build from: "D dir" for a package's directory and "F file"
# for a file it embeds, relative to the root. -test adds the packages' tests.
deps() {
  local dir=$1
  shift
  (cd "$dir" && go list -deps -f '{{if not .Standard}}D {{.Dir}}{{range .EmbedFiles}}{{"\n"}}F {{$.Dir}}/{{.}}{{end}}{{end}}' "$@") |
    awk -v root="$root/" '
      $2 == substr(root, 1, length(root) - 1) { print $1 " ."; next }
      index($2, root) == 1 { print $1 " " substr($2, length(root) + 1) }'
}
graphql_deps= otel_deps= bench_deps=
if [ "${VELOX_CI_CHANGES_NODEPS:-}" != 1 ]; then
  # What an application velox-gen writes can build from: velox-gen, and the
  # root module's packages outside internal/, cmd/, bench/ and scripts/.
  # One assignment to a call, so that set -e sees each go list fail.
  app=$(go list ./... | grep -vE '/(internal|bench|scripts|cmd)(/|$)')
  graphql_deps+=$(deps contrib/graphql -test ./...)$'\n'
  graphql_deps+=$(deps examples -test ./graphql/...)$'\n'
  graphql_deps+=$(deps . ./cmd/velox-gen $app)$'\n'
  otel_deps+=$(deps contrib/veloxotel -test ./...)$'\n'
  otel_deps+=$(deps examples -test ./otel/...)$'\n'
  bench_deps+=$(deps bench/compare -test ./...)$'\n'
  bench_deps+=$(deps . ./cmd/velox-gen $app)$'\n'
  # A path go list spells otherwise than $root would leave each list empty.
  for d in "$graphql_deps" "$otel_deps" "$bench_deps"; do
    grep -qxF 'D .' <<<"$d" || { echo "ci-changes.sh: go list named no package of $root" >&2; exit 1; }
  done
fi
# builds reports whether file $2 is one the derived files $1 name. grep
# reads a here-string: grep -q in a pipe could stop its writer, which
# pipefail would read as no match.
builds() {
  local dir
  dir=$(dirname "$2")
  if grep -qxF "F $2" <<<"$1"; then return 0; fi
  case $2 in
    *_test.go) return 1 ;;
    *.go) grep -qxF "D $dir" <<<"$1" ;;
    *) return 1 ;;
  esac
}

graphql= otel= bench=
for f in $changed; do
  case $f in
    .claude/*) continue ;;
    contrib/graphql/*|examples/graphql/*|scripts/gqlgen-latest.sh) graphql=1 ;;
    contrib/veloxotel/*|examples/otel/*) otel=1 ;;
    bench/scale/*|scripts/scale-check.sh) graphql=1 bench=1 ;;
    bench/*|scripts/bench-compare.sh) bench=1 ;;
    go.mod|go.sum) graphql=1 otel=1 bench=1 ;;
    examples/go.mod|examples/go.sum) graphql=1 otel=1 ;;
    .github/workflows/ci.yml|scripts/ci-local.sh|scripts/ci-changes.sh|scripts/docker-compose.ci.yml) graphql=1 otel=1 bench=1 ;;
  esac
  if [ -z "$graphql" ] && builds "$graphql_deps" "$f"; then graphql=1; fi
  if [ -z "$otel" ] && builds "$otel_deps" "$f"; then otel=1; fi
  if [ -z "$bench" ] && builds "$bench_deps" "$f"; then bench=1; fi
done
# The version that ties generated code to the runtime, wherever it is
# declared. grep without -q reads all of git diff's output (see builds).
if [ "$base" != --files ] && git diff "$base" -- '*.go' ':!.claude' | grep -E '^[-+]const GeneratedCodeIsVersion[0-9]+ ' >/dev/null; then
  graphql=1 otel=1
fi

if [ -n "$graphql" ]; then echo graphql; fi
if [ -n "$otel" ]; then echo otel; fi
if [ -n "$bench" ]; then echo bench; fi
