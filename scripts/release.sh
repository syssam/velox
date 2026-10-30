#!/usr/bin/env bash
# Tags a release of the three published modules: the root, contrib/graphql and
# contrib/veloxotel. In the tree the contrib modules reach the root through a
# replace, which Go ignores in a dependency, so each tag's go.mod must require a
# real version of the root. That takes three steps, with a push between them, as
# Go must be able to download the root's tag before contrib can require it. This
# script never pushes; it prints what to push.
#
#   scripts/release.sh root v1.2.3     tags the root
#   scripts/release.sh contrib v1.2.3  commits contrib's requires, tags contrib
#   scripts/release.sh dev v1.2.3      puts the replaces back for development
#
# example and bench/compare are not published, and keep their replaces.
set -euo pipefail
cd "$(dirname "$0")/.."

root=github.com/syssam/velox2
contribs=(contrib/graphql contrib/veloxotel)

usage() { echo "usage: $0 root|contrib|dev vMAJOR.MINOR.PATCH" >&2; exit 2; }
[ $# = 2 ] || usage
step=$1 version=$2
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || usage

clean() {
  [ -z "$(git status --porcelain)" ] || { echo "release: the tree is not clean" >&2; exit 1; }
}

case $step in
root)
  clean
  git tag -a "$version" -m "$version"
  echo "Next: git push origin $version, then $0 contrib $version"
  ;;
contrib)
  clean
  git rev-parse -q --verify "refs/tags/$version" >/dev/null || { echo "release: no tag $version; run $0 root $version" >&2; exit 1; }
  tags=()
  for m in "${contribs[@]}"; do
    (
      cd "$m"
      go mod edit -dropreplace="$root" -require="$root@$version"
      GOWORK=off go mod tidy
    )
    git add "$m/go.mod" "$m/go.sum"
    tags+=("$m/$version")
  done
  git commit -q -m "release: contrib requires the root's $version"
  for t in "${tags[@]}"; do git tag -a "$t" -m "$t"; done
  echo "Next: git push origin HEAD ${tags[*]}, then $0 dev $version"
  ;;
dev)
  clean
  for m in "${contribs[@]}"; do
    (cd "$m" && go mod edit -replace="$root=../.." && GOWORK=off go mod tidy)
    git add "$m/go.mod" "$m/go.sum"
  done
  git commit -q -m "release: contrib builds against the tree again, after $version"
  echo "Next: git push origin HEAD"
  ;;
*) usage ;;
esac
