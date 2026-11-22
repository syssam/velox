# GraphQL runtime without per-entity generics

Date: 2026-11-03. Controller's design; the user asked to "improve all to modern, best,
performance, clean" after raising build time and memory.

## Evidence

100 entities, the scale schema with create and update, one package compiled via
`-toolexec`, Apple M3 Max, load ~10, two runs each:

| package | lines | CPU | peak RSS |
|---|---|---|---|
| gen, velox-gen only (ORM) | 11.6k | 2.4 s | 335 MB |
| gen, with velox-graphql | 52.7k | 30.6 s | 2.1–2.2 GB |
| graph (gqlgen) | 165k | 24.4 s | 1.9–2.0 GB |

The inputs redesign (ADR 0033) cut generated lines 14% and changed neither CPU nor
memory. `go tool nm -size` of gen.a (10.9 MB text): the bulk is generic code
instantiated once per entity, because every entity is a distinct struct shape:
velox.Query[E] methods (2290 symbols, 920 KB), NumberCol/StringCol methods (2190,
616 KB, the method values the where inputs pass), Rel, UpdateQuery, InsertQuery,
And/Or, LoadEach; gqlimpl's Connection, NewConnection, Term, batchOf, loadBatch,
EdgeConnection, EdgeOne, NodeOf, Narrow, Update/Create; plus large generated bodies
(XsConnectionOf 2.3 KB each, WhereInput.Pred 1.9 KB, Update 1.5 KB). The ORM alone
instantiates none of it. So 92% of gen's cost is the GraphQL layer pulling velox's
whole typed query engine in per entity.

## Decision

velox's root is already a typed shell over `internal/engine`, which works on
`*EntityInfo`, `engine.Predicate`, `Statement` and `unsafe.Pointer` rows. The
GraphQL runtime moves onto an untyped surface of that engine, so it is compiled
once, not once per entity. Per entity, generated code keeps only what gqlgen must
bind (named Go types and methods), each a one-line call into non-generic code, and
small literal tables (column indexes, names, edge indexes) that velox-gen already
emits as constants (`postColTitle`, `postEdgeAuthor`).

### Core: an untyped query surface for generated code

In the root package, marked "For generated code" like the other generated-only
API (decision 0024's public-surface rules apply; pin it in the API snapshot):

- `velox.Table`: an entity's metadata as a non-generic value, from
  `velox.TableOf[E, PE]()` (one tiny instantiation per entity, called once).
- `db.QueryTable(t)` → an untyped query with the operations the GraphQL runtime
  uses: Where(Predicate...), OrderBy(OrderTerm...), Columns(col ints), Limit,
  After/cursor support as the typed Query has it, Count, Exists, ForUpdate,
  WithoutPolicy context, Selects, LoadEach/CountEach/LimitEach by edge index, Join
  by edge index; All returns rows as a `velox.Rows` (a slab: base pointer, stride,
  len) and `velox.RowsOf[E](r) []E` / pointer view, the only per-entity generic.
- Untyped predicates and setters by column index: EQ/NEQ/In/NotIn/GT/GTE/LT/LTE/
  Contains/HasPrefix/HasSuffix/IsNull/NotNull/Has/HasWhere, Set/SetNull, built as
  engine nodes, with the same value checks the typed handles do (the value's Go
  type must be the column's: check it at build time against ColumnDescriptor, so
  a wrong value is an error, never a wrong query).
- Untyped Insert (of a row pointer), Update (setters + where), Delete, InsertAll of
  join rows (built from two key values by the join table's descriptor).
- Same policies, hooks, scope, routing, savepoints and errors as the typed API:
  the typed API and this one build the same `Statement` and go through the same
  engine path. The typed `Query[E]` keeps its API and may be re-expressed on top of
  the untyped one if that removes duplication; it must not get slower.

### gqlimpl on the untyped surface

- `Connection`, `NewConnection`, `Term`/order, cursors, `Narrow`, `Selected`,
  `Node`/`Nodes`, edge loading and batching (`EdgeOne`, `EdgeList`,
  `EdgeConnection`, `batchOf`, `loadBatch`), the where `Filter` and its helpers,
  and the mutation inputs (`NewCreate`/`NewUpdate`/`Join`/`Through`/`Repoint`/...)
  become non-generic, keyed by `velox.Table` and column/edge indexes. Keep exported
  names stable where the shape allows; this is generated-code API under
  `veloxgql.GeneratedCodeIsVersionN` — bump it.
- Where inputs: one call per predicate stays, but passes the column index and the
  op instead of a method value (`gqlimpl.Value(f, "name", w.Name, postColName,
  gqlimpl.OpEQ)`), so no column method is instantiated. ID and FK predicates likewise.
- Generated per-entity Go keeps: entity enums' Marshal/Unmarshal, `XConnection`,
  `XEdge`, `XOrder`, `XWhereInput`, inputs, `XGQL` edge methods, `XsConnection(Of)`
  — each body a few lines delegating to gqlimpl, plus the literal tables.
  `XsConnectionOf(ctx, q velox.Query[X], ...)` keeps its typed signature for the
  application (it converts the typed query to the untyped one once).

### Runtime performance

Must not regress: `VELOX_GQL_BENCH=1 go test -run TestGeneratedBench
./cmd/velox-graphql` (list, nested list, where, node lookups) within +5% time and
+5% allocs (benchstat, 10 runs, GOMAXPROCS=1), and `bench/` ORM benchmarks
unchanged for the typed API. A slab view avoids per-row boxing; predicates are
built once per request as today.

## Plan shape (one plan, phases with a measured gate each)

1. Core untyped surface + tests (parity tests: for each op, the typed and untyped
   statements are byte-identical SQL and args on SQLite/PG/MySQL; policies/hooks
   identical).
2. Connections, orders, cursors, node, narrowing on it; regenerate; **gate**:
   gen at 100 entities measured; continue only if CPU and RSS fall by ≥40% for
   this phase's share, else stop and report.
3. Where inputs. 4. Edges and batching. 5. Mutation inputs.
6. Docs (ADR 0035, CHANGELOG, package docs), API snapshot, final measurement
   table in the ADR, full `scripts/ci-local.sh`.

Target at the end: gen at 100 entities ≤ 8 s CPU and ≤ 700 MB RSS, runtime
benchmarks within 5%. gqlgen's own `graph` package is out of scope (only a runtime
executor replacing gqlgen would shrink it; the user decides that after 1.0).

## Kept

Every behaviour and error of the current runtime, the e2e suites as the contract,
the no-method-value-in-struct-field rule for generated code (Go 1.27 ICE).
