# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

velox is a Go 1.27 ORM: an ent-style schema DSL, a generator (`velox-gen`) that writes
typed entities and column handles into a `gen` package, a generic runtime in the root,
and its own migration planner. `CONTRIBUTING.md` is the source of truth for layout,
checks, generated code, coverage floors, fuzz targets, benchmarks and releases. Read it
before running checks or regenerating anything. `docs/coming-from-ent.md` lists where
velox differs from ent.

## Modules

Five Go modules, each checked on its own: the root, `examples`, `contrib/graphql`,
`contrib/veloxotel`, `bench/compare` (`bench/compare/veloxe` only pins a tool and is
never tidied). Run `go` commands from the module that owns the file. A change that
touches several is checked in each.

`.claude/worktrees/` holds agent worktrees with their own copies of every module. Exclude
it from repo-wide `find`/`grep`/`gofmt`.

## Checks

- Inner loop: `scripts/quick.sh` (changed modules, SQLite, no race, cached; `-all`,
  `-race`, `-long`).
- Merge gate (needs Docker): `bash scripts/ci-local.sh` runs the ORM's checks plus
  the graphql/otel/bench groups the change touches (`scripts/ci-changes.sh`;
  `VELOX_CI_GROUPS=` forces none). Run it once before a local merge.
  `bash scripts/ci-local.sh --full` runs everything, coverage floors included; run it
  before every push. Lanes run concurrently; `VELOX_CI_JOBS=1` runs them one at a time.
  It stops at the first failure and uses the compose project `velox-ci`, so never run
  two at once or alongside a manual compose.
- Quick loop, per module: `go vet ./...`,
  `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...`,
  `go test -race -count=1 ./...`.
- One test: `go test -race -count=1 -run '^TestName$' ./internal/engine`.
- PostgreSQL and MySQL tests skip without `VELOX_TEST_POSTGRES` / `VELOX_TEST_MYSQL` /
  `VELOX_TEST_MYSQL_ADMIN`; SQLite always runs. CONTRIBUTING.md has the compose command
  and DSNs.

## Workflow

Trunk-based: small commits straight to main, each one building and passing on
its own. Verification scales with risk, as CI tiers do (fast pre-commit, merge
gate, full suite before release):

- Small change (a few files, no change to generated code's shape): edit on
  main yourself, `scripts/quick.sh`, then one `bash scripts/ci-local.sh`.
  No subagent, no worktree, no `--full`, no compile-cost measurement.
- Large change (generator output shape, regenerating every example, a
  multi-phase design): a worktree branch, deleted after merge. A subagent
  only when the work is long or can run beside other work. One merge gate
  per branch, not one per phase.
- Measure compile cost once, and only when generated code's shape or the
  root's generic types change.
- `bash scripts/ci-local.sh --full` runs once on main before a push, not per
  task or per branch.
- Batch related small fixes into one gate run.
- A flaky lane is rerun alone once; a second failure is a bug to fix.
- Debugging starts from a reproduction; a fix needs a test that failed
  before it.
- Report what was done and what was verified, briefly.

## Architecture

- **Root package** (`github.com/syssam/velox`) is only the generic API: `DB`,
  `Query[E]`, `Update[E]`, `Insert`, `Pred[E]`, aggregates, `Tx`. Go requires a generic
  type's methods in its own package, so builders stay generic here instead of being
  generated per entity; that keeps a schema's build cost flat.
- **`internal/engine`** is everything non-generic: statements, rendering and its
  cache, policies, scope, loading, routing, transactions, errors. Reading a
  driver's error is `internal/dberr` (0053). The root's `DB` holds
  an `engine.DB` by value; most root public names are aliases of engine names
  (decision 0020). Engine behaviour tests live in `internal/engine` (often
  `package engine_test`); other public-API tests in `internal/apitest`; repository
  invariants in `internal/repocheck`.
- **`veloxtype`** holds the plain public data types the root re-exports (0021).
- **Generation**: `cmd/velox-gen` → `schema/load` loads the user's schema package →
  `internal/codegen` builds the model and renders `*_velox.go` files. The generated code
  is mostly a `Descriptor` (metadata) plus column handles that the generic runtime reads
  on first use. a `Descriptor` names the schema's value, `Schema: schema.E{}`,
  when the schema or a mixin has behaviour, and velox reads the mixins' then
  its own Policy, Hooks and Interceptors (0040); there is no gen/runtime, and velox-gen
  refuses a schema that imports gen (0039). velox-gen type-checks the
  column and relation names, and constant values, that behaviour gives to
  `velox.FieldEQ`…, `velox.HasEdge`… and the Mutation and Row methods. Plugins (e.g. `velox-graphql`) are separate programs speaking JSON over
  stdin/stdout per `plugin/doc.go`; `velox-graphql` writes `*_gql.go`.
- **`migrate`** is velox's own planner/differ/applier with per-dialect inspect and
  render files; `internal/ddl` writes the DDL. It also emits SQL for Atlas (0006).
- **`contrib/graphql`**: the annotation package (stdlib + `velox/schema` imports only),
  the `velox-graphql` plugin (`internal/gogen`, `internal/sdl`) and the `veloxgql`
  runtime used with gqlgen. **`contrib/veloxotel`**: OpenTelemetry tracing/metrics.
- `docs/decisions/` (ADRs, indexed in its README) records why the code is as it is.
  Check the relevant record before changing a behaviour it covers. `docs/history/` is a
  frozen record; leave it as is.

## Invariants that fail CI

- Public API is snapshotted in `internal/repocheck/testdata/api.txt`. After an intended
  change: `go test ./internal/repocheck -run TestPublicAPISnapshot -update-api`, then
  read the diff.
- Generated files are committed and checked for staleness: `go generate .` for the
  root's `handles.go`; regenerate `internal/testmodel`, `internal/testbind` and every
  example after a generator or schema change (commands in CONTRIBUTING.md).
- Golden files update with the package's `-update` flag (`internal/codegen`,
  `internal/ddl`, `migrate`, `cmd/velox-gen` (incl. `testdata/wrongcolumn`), `contrib/graphql/internal/{gogen,sdl}`,
  each example). Read the diff before committing.
- A change to generated code's shape bumps `GeneratedCodeIsVersionN` to `N+1` with a
  `CHANGELOG.md` line.
- Per-package coverage floors in `scripts/coverage-floors.txt`; a new package needs a
  line.
- `scripts/ci-local.sh` must run the same commands as `.github/workflows/ci.yml`
  (`TestCILocalMirrorsCI`); a new fuzz target goes in both, anchored `'^FuzzX$'`.
- Docs tests: every exported method has a doc comment; godoc cites no spec, plan or
  wave; README's quick start is executed by `TestReadmeQuickStart`.

## Semantics that differ from intuition

- `Optional` stays NOT NULL; `Nillable` makes a column nullable (0003). IDs are int64.
- A read needs `Limit` or `AllowUnbounded()`, else `ErrUnbounded`.
- A Write policy filters: a write to hidden rows affects 0 rows, not an error (0008).
- The core has no tenant; scoping is a mixin whose plain, untyped `Policy()`
  and `Hooks()` use `scope.Key`'s `Require`/`Match`/`From` and name the column
  with `velox.FieldEQ("org_id", v)`; velox reads it before the entity's own
  (0029, 0038–0040). Soft delete is `schema/mixin/softdelete`. An Interceptor filters
  rows and is not lifted by `WithoutPolicy`.
- Schema behaviour uses ent's names: `velox.FieldEQ`…, `HasEdge`,
  `HasEdgeWith`, of type `velox.Predicate` (an instance of `Pred`); the zero
  `Predicate` fails the statement. Names are strings, checked by velox-gen
  and again when the statement runs; a hook that needs gen's types is a run-time
  `velox.WithHooks[gen.X]`.
- Nothing is released. Never tag `v0.1.0`, `v0.2.0`, `v0.2.1` or `v0.3.0` (another
  project's contents sit in the checksum DB at those versions); the first tag is
  `v1.0.0`. Before 1.0 nothing is kept for compatibility.

## Style

- Comments and docs: short declarative sentences, one claim each, wrapped at about 78
  columns. A behavioural claim has a test, run or grep behind it.
- Commit messages: `area: what changed`, e.g. `migrate: a PostgreSQL unique constraint
  is Index.Constraint`. Stage files by name.
