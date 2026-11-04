#!/usr/bin/env bash
# Tests velox-graphql against the newest gqlgen release. It works in a copy of
# the tree, so the checkout and its go.mod and go.sum files are never touched.
# A failure means gqlgen moved: velox-graphql supports v0.17.86 and later.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)

tmp=$(mktemp -d)
# Remove the copy on exit. The module cache is not in it (GOMODCACHE is the
# user's); chmod only makes any read-only file the tree holds removable.
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT

# Tracked and untracked files, not ignored ones.
(cd "$root" && git ls-files -co --exclude-standard -z | tar --null -T - -cf -) | tar -xf - -C "$tmp"

(
  cd "$tmp/contrib/graphql"
  go get github.com/99designs/gqlgen@latest
  go mod tidy
  # The itests build applications offline that require velox and this
  # module, with velox-gen and velox-graphql as tools: TestThroughTheLoader
  # (annotation_test.go) and app (cmd/velox-graphql/e2e_test.go) write their
  # go.mod, which this one mirrors. Their module graph is not this module's
  # (velox-gen's SQLite driver reaches modules the upgrade moved), so tidy one
  # such application online first, to fill the module cache with what they
  # read. Its go line is the repository's.
  goline=$(sed -n 's/^go //p' "$tmp/go.mod")
  mkdir "$tmp/warm"
  printf 'module example.com/warm\n\ngo %s\n\ntool (\n\tgithub.com/syssam/velox/cmd/velox-gen\n\tgithub.com/syssam/velox/contrib/graphql/cmd/velox-graphql\n)\n\nrequire (\n\tgithub.com/syssam/velox v0.0.0\n\tgithub.com/syssam/velox/contrib/graphql v0.0.0\n)\n\nreplace (\n\tgithub.com/syssam/velox => %s\n\tgithub.com/syssam/velox/contrib/graphql => %s\n)\n' \
    "$goline" "$tmp" "$tmp/contrib/graphql" > "$tmp/warm/go.mod"
  (cd "$tmp/warm" && go mod tidy)
  go vet ./...
  # CI unset: this job has no databases, and the mutation integration test's TestMain
  # fails a CI run without them. It runs on SQLite here.
  # The timeout fits the lane on a loaded machine: TestLargeSchemaServed
  # alone takes about 105 s, and ran past go test's default 10m at load 92 to 120.
  env -u CI go test -count=1 -timeout 30m ./...
)

cd "$tmp/examples"
go get github.com/99designs/gqlgen@latest
go mod tidy
# The new gqlgen's output is built and tested, not compared with the checked-in one.
go -C graphql tool gqlgen generate
go vet ./graphql/...
go test -count=1 -timeout 30m ./graphql/...

echo "gqlgen $(go list -m -f '{{.Version}}' github.com/99designs/gqlgen): ok"
