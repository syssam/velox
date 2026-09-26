# Contributing to Velox

Thank you for your interest in contributing to Velox! This guide will help you get started.

## Development Setup

### Prerequisites

- Go 1.25 or later (the root `go.mod` minimum; the example and test submodules under `examples/` and `tests/` need Go 1.26)
- golangci-lint (for linting)

### Getting Started

```bash
git clone https://github.com/syssam/velox.git
cd velox
make test
```

The root module's tests import generated code that is gitignored
(`tests/integration/…`, `examples/realworld/velox`), so a plain
`go test ./...` on a fresh clone fails with `no required module provides
package`. `make test` runs `make generate` first; after that, plain
`go test ./...` works until the generators change.

### Running Tests

```bash
# Generate the fixtures the tests import (once per clone, and after generator changes)
make generate

# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detection
go test -race ./...

# Run a specific package's tests
go test ./compiler/gen/sql/...
```

### Running CI Locally

`scripts/ci-local.sh` replays the jobs in `.github/workflows/ci.yml`; the git
pre-push hook runs its `--fast` tier. To include the live-database jobs with
the same database versions and Go release as CI, use Docker:

```bash
make ci-docker-db   # integration + parity on Postgres 16/MySQL 8.0 and 14/5.7 (~10 min)
make ci-docker      # every job, incl. fuzz, then the DB jobs on the second pair (~45 min)
```

Each database pair runs in fresh tmpfs-backed containers (`docker-compose.ci.yml`,
ports 55432/53306, so your own database containers are untouched) that are
removed afterwards. The Go toolchain is the newest patch of the minor version
`ci.yml` pins, downloaded via `GOTOOLCHAIN`; set `CI_GO=local` to use your
installed Go. On Apple silicon, MySQL 5.7 runs under amd64 emulation.

Reusing a long-lived local database for these tests is what this avoids:
tables left by another schema (the parity harness once shared `velox_test`)
make the next migration fail in ways CI never sees.

### Running Linter

```bash
golangci-lint run
```

### Formatting Code

```bash
gofmt -s -w .
goimports -w .
```

### Golden File Tests

Code generation output is verified against golden files. After changing any generator code in `compiler/gen/sql/`:

```bash
# Update golden files to match new output
go test ./compiler/gen/sql/ -update-golden

# Verify golden files match (CI runs this)
go test ./compiler/gen/sql/ -run TestGolden
```

Golden files live in `compiler/gen/sql/testdata/golden/`. Review diffs carefully before committing — they represent the public-facing generated API.

### Dead-API Guard

`deadapi_test.go` (root package) fails when something is declared but nothing
in production reads it — velox's most repeated bug, where a feature compiles,
is stored at init, and silently does nothing. It checks five rules:

| Rule | Fails when |
|---|---|
| (a) | a `gen.Feature` var is never consulted by a generator and has no `Deprecated:` doc line |
| (b) | a `contrib/graphql.Annotation` field is read by nothing outside `annotation.go` (an accessor counts only if the accessor has a caller; `Merge` never counts) |
| (c) | a package-level registry a `runtime` function writes is read by no function that has a caller |
| (d) | an exported `runtime` identifier is unreachable from generated code and from every other package |
| (e) | a field of a struct in generated code (`tests/integration`, `examples/realworld`) is written only inside `clone()` — copied between queries, never given a value |

Generated code counts as a reader, so the guard needs the gitignored fixtures;
it skips locally (and fails under `CI`) without them:

```bash
go run tests/integration/generate.go && (cd examples/realworld && go run generate.go)
go test . -run TestDeadAPIGuard
```

When it fails, in order of preference:

1. **Delete the identifier** if nothing should read it. Add a CHANGELOG
   `[Unreleased] → Removed` entry, and run
   `go test . -run TestPublicAPIGuard -update-api` if it was public API.
2. **Wire it up** if it stands for a feature that should work, with a test that
   asserts on generated output or behavior — not that the value was stored.
3. For a `gen.Feature` kept only so existing `generate.go` files still
   compile, add a `Deprecated:` line saying the flag has no effect.
4. **Allowlist it** only if it is deliberately application-facing API with no
   reader in this repository: add `<rule> <pkg>.<Name>  # reason` to
   `testdata/deadapi/allowlist.txt`. The guard also fails on an entry that is
   no longer needed, so the list cannot rot.

### Regenerating Examples

After changing generators, regenerate the example fixtures:

```bash
cd examples/basic && go run generate.go
```

### Adding a New Generator

1. Implement the generator function in `compiler/gen/sql/` (per-entity) or as a graph-level generator
2. Register it in the dialect's `EntityGenerator` or `GraphGenerator` interface
3. Add golden file test coverage
4. Update `compiler/gen/sql/testutil_test.go` if new mock helpers are needed

### Hot-path Benchmarks

Velox has four reverts on `main` around `UpdateOne` because a refactor
shipped without perf measurement. To catch that class of regression
locally, `make bench-hotpaths` runs the guarded set (UpdateOne_SQLite,
CreateBulk, Create_SingleRowLoop) at `-count=10`, and
`make bench-compare` runs benchstat against the committed baseline
in `testdata/bench-baseline.txt`.

**Before merging any change to `compiler/gen/sql/{create,update}.go`,
`runtime/` query execution, or `dialect/sql/sqlgraph/` entry points:**

```bash
make bench-install    # once, installs benchstat
make bench-hotpaths   # ~3 min on an M3; produces testdata/bench-current.txt
make bench-compare    # benchstat vs baseline — p-values, geomean delta
```

Paste the `bench-compare` output into the PR description (or the commit
message for trunk-based work). Intentional baseline bumps are a
separate, reviewable change: `make bench-baseline` freezes the current
run, commit `testdata/bench-baseline.txt` alone with the rationale.

There is no CI gate on bench (GitHub Actions shared runners are too
noisy for meaningful benchstat). The gate is social.

## Code Style

This project follows the [Google Go Style Guide](https://google.github.io/styleguide/go/) and [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md). See [`docs/reference.md`](docs/reference.md) for detailed project-specific conventions.

Key points:
- Use `slog` for structured logging (not `log`)
- Use `any` instead of `interface{}`
- Use modern octal syntax (`0o644`)
- Generated files must include `// Code generated by velox. DO NOT EDIT.` header
- Follow interface segregation: implement `MinimalDialect` for basic support, `DialectGenerator` for full support

## Pull Request Process

1. Fork the repository and create a feature branch from `main`.
2. Write tests for any new functionality.
3. Ensure all tests pass: `go test ./...`
4. Ensure the linter passes: `golangci-lint run`
5. Keep commits focused and write clear commit messages.
6. Open a pull request with a description of what changed and why.

## Reporting Issues

When reporting a bug, please include:
- Go version (`go version`)
- Velox version
- Steps to reproduce
- Expected vs actual behavior
- Relevant error messages or logs

## Project Structure

See [`docs/architecture.md`](docs/architecture.md) for a comprehensive overview of the project architecture and package layout.
