# Working on velox

velox is a type-safe Go ORM with code generation, closely modelled on
[Ent](https://entgo.io/). It uses [Jennifer](https://github.com/dave/jennifer)
rather than templates.

This file is for people — and agents — changing velox itself. A separate
`AGENTS.md` is *generated* into each project's output directory for people
using a generated client; do not confuse the two.

## Pipeline

Schemas in `schema/` (or `testschema/` for tests) flow through
**load → graph → validate → generate**.

| Package | Role |
|---|---|
| `compiler/load` | reads user schemas |
| `compiler/gen` | graph, validation, feature flags, write orchestration |
| `compiler/gen/sql` | the SQL dialect's generators — nearly all codegen lives here |
| `runtime/` | non-generated runtime: query/mutation execution, stores, registries |
| `privacy/` | policy rules (`QueryRule`, `MutationRule`, `Allow`/`Deny`/`Skip`) |
| `dialect/sql/` | SQL builder, sqlgraph, dialects, migration |
| `contrib/graphql/` | opt-in GraphQL generation |

## Commands

```bash
make generate                                   # gitignored fixtures the root tests import; needed on a fresh clone
go test ./...                                   # all tests
make check                                      # what CI's test job runs: -race -cover, then lint
make lint                                       # golangci-lint at CI's pinned version; must print 0 issues
gofmt -s -w . && goimports -w .                 # format
go run tests/integration/generate.go            # regenerate the integration prototype
go test ./compiler/gen/sql/ -update-golden      # after intentional codegen changes
go test . -run TestPublicAPIGuard -update-api   # after intentional public-API changes
bash scripts/regen.sh                           # regenerate + build every example module
```

`scripts/regen.sh` is not optional after a codegen change. Example modules
are separate Go modules whose generated output is gitignored, so a root
`go test ./...` will not catch a break in them. The script exits non-zero
and prints `done (with failures: ...)` if any module fails.

**Bump a dependency with `go get`, never `go mod tidy`.** Every sub-module
carries `replace github.com/syssam/velox => ../..`, so a root bump enters
their module graph and `go build ./...` starts reporting `updates to go.mod
needed` — each sub-module has to be updated too. The obvious repair is the
destructive one: `generate.go` carries `//go:build ignore`, so `go mod tidy`
cannot see the generator's imports and prunes jennifer and atlas out of
go.sum. The module still builds; only `go run generate.go` breaks, with
`missing go.sum entry for ... jennifer/jen`. One tidy sweep silently broke
generation in 7 of 11 modules. After any bump:

```bash
for d in examples/* tests/external-module tests/parity; do
  (cd "$d" && go run generate.go >/dev/null && go build ./...) || echo "FAIL $d"
done
```

## Rules that are load-bearing

**One generator per output file.** Two generators writing the same path race
in the write errgroup and the loser's output is silently discarded. Parallel
generator paths also drift, which has caused real bugs. Do not create a
second path for an existing artifact.

**All generated artifacts go through `gen.WriteFileIfChanged`.** It
byte-compares then does an atomic temp+rename, so a no-op regeneration
rewrites zero files and leaves mtimes alone. A direct `os.WriteFile` breaks
that for every watcher and make rule downstream.

**Generated files are not re-parsed after Jennifer renders them.**
`gen.FormatGoBytes` regroups the import block textually (stdlib above
third-party, sorted by path) — the only thing goimports ever changed in
Jennifer output — and falls back to `x/tools/imports` in `FormatOnly`
mode for a block it does not recognize. Jennifer already tracks every
import and already runs gofmt; a resolving pass spawned one `go env`
subprocess per file and a parsing pass printed every file twice more. It
also means a missing or unused import in generator output is a compile
error in the generated project rather than something silently patched
over — which is what you want. Never run `goimports -w` over `testdata/`:
golden import paths do not resolve, so it strips them and corrupts the
pins (`scripts/regen.sh` prunes `testdata`).

**The schema loader cache (`.velox/`) persists on purpose.** The
loader source is named by content hash (the build cache keys
`go build file.go` on the file name), the directory is kept between runs,
and a rebuilt binary only replaces the old one when its bytes differ —
`go build -o` rewrites its output even on a cache hit, and macOS charges
0.4–0.7s to validate a freshly written executable on first exec. Do not
reintroduce `os.RemoveAll` of the directory or a per-run file name; both
turn a 0.3s load into a 1.4s one. Pinned by
`compiler/load/load_test.go::TestLoad_CacheDirPersistsAndReuses`.

**One generator, several source files is fine.** `genQueryPkg` is a
`queryGen` struct whose section methods live in `query_pkg.go`,
`query_pkg_terminals.go` and `query_pkg_select.go`. They all append to
the same `*jen.File` in a fixed order; the rule above is about output
paths, not source files.

**Generated content must not depend on the output path.** `velox generate
--check` renders into a temporary directory and compares, so anything
varying with `outDir` reads as drift.

**Invariants are guarded by tests, not types.** `compiler/gen/sql/wiring_test.go`
pins the generator footguns; `apiguard_test.go` golden-pins the public API of
the consumer-facing packages. Refactoring the generator to make illegal
states unrepresentable is risk for no measurable gain — the bug class
already cannot ship.

**A declared identifier with no production reader is a bug** — velox's most
repeated one. `deadapi_test.go` fails on unread feature flags, `graphql.Annotation`
fields, runtime registries and unreachable `runtime` exports; see
CONTRIBUTING.md § Dead-API Guard. Never allowlist something generated code should read.

**Dialect differences go through `dialect.Capability` flags**, never string
comparisons on the dialect name.

**Ent is the parity reference, not the spec.** Some deviations are
deliberate because Ent's behavior is wrong for users; do not "restore
parity" on them without reading why. Query-level traversals
(`sqlgraph.SetNeighbors`) are semi-joins, `WHERE key IN (SELECT … FROM
(<source>) AS t1)`, not Ent's JOIN + default `DISTINCT`: the DISTINCT makes
Postgres and MySQL reject a traversal ordered by an unselected expression
(`ByPostsCount`), which SQLite accepts, so only a live-database run shows
it. `client.Mutate` scopes an `OpDeleteOne` mutation to its ID (Ent's
identical path is unreachable only because its constructor is
unexported). Before claiming parity, read the Ent template line — a
misread `_node.<fk> = &nodes[0]` once shipped an ID-only edge stub as
"Ent parity".

## Authorization: the split that matters

Two mechanisms with different reach. The difference is measured in
`tests/integration/e2e_authz_surface_test.go`, not assumed:

- `Intercept()` is **per client** (`c.interStore`). It reaches every read
  terminal, eager loading, edge queries, `Noder` and reads inside a
  transaction — and **no mutations at all**.
- `Policy()` is **process-global** (`RuntimePolicy`, read at client
  construction). It reaches the same reads *and* writes.

Row-level authorization therefore belongs in `Policy()`; a filter built only
from interceptors leaves `Update().Where(...)` and `Delete().Where(...)`
unscoped. But because a Policy is global, it cannot express "this client is
scoped and that one is not" — which matters, because narrowing a read that
guards an invariant (a dependency `Exist()` before a delete) corrupts data
rather than leaking it. `examples/multitenant/` is the worked reference.

Edge predicates (`HasXxx()`, `HasXxxWith(...)`) compile to a subquery on a
`*sql.Selector` that never becomes a Query. The target's **Policy** is applied
there anyway (`runtime.ApplyEntityPolicy`, emitted for every edge whose target
declares one): a filtering policy narrows the subquery and a denying one fails
the whole read or write — pinned by
`tests/integration/e2e_edge_predicate_policy_test.go`. Interceptors do not
reach it; that remains pinned in `e2e_authz_known_gaps_test.go`, one more
reason row-level authorization belongs in `Policy()`. The subquery's errors
must survive rendering — `Builder.Wrap`, `UpdateBuilder.Query` and sqlgraph's
post-render `Err()` checks carry them; a write that ran past a denied edge
policy would do so with the subquery unscoped.

A write evaluates its mutation policy in `Save`/`Exec` before the hooks (a
denied request never reaches a hook with side effects) and — only when hooks
are registered — again after them, on the mutation the hooks produced: the
chain's core is then `sqlSaveAfterHooks`/`sqlExecAfterHooks`, and each
bulk-create row mutator checks its row (`genPolicyAfterHooks`). Without
hooks the mutation cannot change, so `Save` calls `sqlSave` directly and the
rules run once. Do not drop either check: without the first a denied request
runs hooks, without the second a hook writes past every rule. Do not move the
second into `sqlSave` either — the no-hook path would then run every rule
twice. Pinned by
`wiring_test.go::TestPolicyReevaluatedAfterHooks` and
`tests/integration/e2e_policy_after_hooks_test.go`.

## Test discipline

**A test of SQL that differs by dialect runs on every dialect.** Use
`forEachDialect` (tests/integration) for anything touching upserts and
conflicts, returned IDs, cursors and ordering, JSON, NULL placement, time
columns, row locking, or generated DISTINCT/subqueries; CI runs it against
PostgreSQL and MySQL. SQLite accepts things the others reject and hides
things they expose: in one review pass six fixes passed on SQLite and failed
on a real server — three MySQL upsert paths (no RETURNING), a DISTINCT that
Postgres and MySQL reject under ORDER BY, and two cursor bugs. Run locally
with `VELOX_TEST_POSTGRES` / `VELOX_TEST_MYSQL` set, or `make ci-docker-db`.

**testschema must cover every schema shape the generator branches on.**
`TestTestschemaCoversGeneratorShapes` fails when one disappears; when the
generator grows a branch, add the shape there and a test exercising it.

**A test that calls a generator without asserting on its output is worse
than no test.** It fills the coverage metric while hiding that the generated
code is dead. Four such tests once "covered" an interceptor generator whose
output nothing ever read. Assert on the rendered source.

**Code in documentation must be compile-verified.** The multi-tenant section
of `docs/privacy.md` once shipped a snippet that did not compile and, once
fixed, performed no isolation. Paste new snippets into a scratch package and
run `go vet`; for security-relevant guidance, also assert the behavior in a
test. `examples/` is the preferred home for anything longer than a few lines
— it is compiled and tested by CI.

**Reproduce a CI job with CI's version of the tool.** CI pins golangci-lint
to an exact version (`v2.13.2` in `.github/workflows/ci.yml`, bumped by hand)
but still runs `go-version: stable`, which moves without a commit. Three of
the four failures found on 2026-09-07 were pure version drift — red on every
nightly for weeks with `main` untouched — which is why the linter is now
pinned. A local linter that differs from CI's reports zero issues on code CI
rejects. Install the exact version CI pins and run it with
`GOLANGCI_LINT_CACHE=<tmpdir>` (the shared cache holds a global lock). Reach the `stable` leg with `GOTOOLCHAIN=go1.XX.Y`, and
run `govulncheck` under CI's Go — it reports standard-library advisories for
whatever toolchain built it.

**Report only results you observed.** A command that silently no-ops still
exits 0: a missing binary, an empty glob, an output filter that ate
everything. Confirm the positive evidence (`ok <pkg>`, `PASS`, `0 issues`)
rather than the absence of errors.

## Where to look next

- `docs/` — user-facing guides (privacy, hooks and interceptors,
  observability, migrating from Ent)
- `runtime/PROTOCOL.md` — the runtime contract between generated code and
  `runtime/`
- `COMPATIBILITY.md` — what the public-API guard covers and how to change it
- `.references/ent` — an upstream Ent checkout, read-only. velox uses Ent as
  its parity reference; check it before deviating from a generated signature.
