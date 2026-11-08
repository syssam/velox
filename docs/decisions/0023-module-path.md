# 0023: the module is github.com/syssam/velox

Date: 2026-10-31

## Context

The module was `github.com/syssam/velox2` and its package `velox`: the last element of the
path did not name the package, a `v1.0.0` tag of "velox2" read as a contradiction, and a
real second major version would have been `velox2/v2`. The plain path belonged to the
earlier project, which its owner has renamed on GitHub to `syssam/veloxe`.

## Decision

- The module path is `github.com/syssam/velox`. The project and the package are `velox`.
  The earlier project is called veloxe.
- This repository's first tag is `v1.0.0`. It must never tag `v0.1.0`, `v0.2.0`, `v0.2.1`
  or `v0.3.0`: the Go checksum database already holds veloxe's contents for those versions
  at this path.
- `bench/compare` still measures veloxe, whose `go.mod` declares this same path. One
  `go.mod` cannot hold both, so `bench/compare/veloxe` is a small module that pins it. The
  harness tests keep the versions the two share equal, so the comparison stays fair.
- `go.mod` retracts `[v0.1.0, v0.3.0]` (added 2026-11-08). It takes effect once a
  version that carries it is published: `go get` and `go list -m -u` then warn a
  module that requires one, and no version query resolves to one. A build that
  names one explicitly still gets it; `bench/compare/veloxe` pins a
  pseudo-version after `v0.3.0`, outside the range.

## Consequences

This repository has to be published as `syssam/velox` for the path to resolve, which ends
GitHub's redirect from the old name to `syssam/veloxe`. The plans, specs and reviews keep the
old names, as written.
