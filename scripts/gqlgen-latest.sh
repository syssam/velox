#!/usr/bin/env bash
# Tests velox-graphql against the newest gqlgen release. It works in a copy of
# the tree, so the checkout and its go.mod and go.sum files are never touched.
# A failure means gqlgen moved: velox-graphql supports v0.17.86 and later.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)

tmp=$(mktemp -d)
# The module cache is read-only; make the copy writable before removing it.
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT

# Tracked and untracked files, not ignored ones.
(cd "$root" && git ls-files -co --exclude-standard -z | tar --null -T - -cf -) | tar -xf - -C "$tmp"

(
  cd "$tmp/contrib/graphql"
  go get github.com/99designs/gqlgen@latest
  go mod tidy
  go vet ./...
  go test -count=1 ./...
)

cd "$tmp/example"
go get github.com/99designs/gqlgen@latest
go mod tidy
# The new gqlgen's output is built and tested, not compared with the checked-in one.
go -C graphql tool gqlgen generate
go vet ./graphql/...
go test -count=1 ./graphql/...

echo "gqlgen $(go list -m -f '{{.Version}}' github.com/99designs/gqlgen): ok"
