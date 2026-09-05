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
go test ./...                                   # all tests
go test -race -cover ./...                      # what CI runs
golangci-lint run                               # must pass with zero warnings
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

**Dialect differences go through `dialect.Capability` flags**, never string
comparisons on the dialect name.

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

Neither mechanism reaches edge-predicate subqueries (`HasXxxWith(...)`);
they are built directly on a `*sql.Selector` and never become a Query. That
is pinned as a known gap in `tests/integration/e2e_authz_known_gaps_test.go`.

## Test discipline

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
