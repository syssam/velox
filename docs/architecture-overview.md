# Architecture Overview

User-facing onboarding doc for velox. Read this after `README.md` if you are
evaluating velox or starting your first project. The audience here is the
human integrator: where the generated code lives, why the layout looks like
that, and what bites first-time users.

> Maintainer / AI-assistant reference is `CLAUDE.md` at the repo root.
> This doc is intentionally narrower.

---

## 1. 30-Second Elevator Pitch

Velox is a type-safe Go ORM with code generation, GraphQL integration, and
schema-as-Go. The closest sibling is [Ent](https://entgo.io/) — velox borrows
the schema DSL, hook/interceptor model, and privacy layer almost verbatim.
The main divergence is _generated layout_: where Ent emits a single
flat package, velox emits a graph of small per-entity sub-packages. This
keeps incremental rebuilds fast and memory-light at large schema sizes —
the headline number from the post-cycle-break scale report is roughly
**3.4× faster incremental rebuild and ~75% lower peak RSS than Ent at 100
entities**, and the curve stays linear out to 328 entities. See
[`docs/scale-performance-2026-04-25.md`](scale-performance-2026-04-25.md)
for the methodology and numbers.

If your schema has fewer than ~50 entities and you are not building a
GraphQL API, plain Ent is probably a better fit — its single-package
ergonomics outweigh the build-time cost at that scale. Velox's design
breakeven is around 50+ entities, a non-trivial GraphQL surface, or a
project where build/test feedback time is the dominant developer-pain cost.
Velox is **pre-1.0** and breaking changes still happen on `main`; pin to a
commit and read `docs/migrations/` before upgrading.

---

## 2. Package Topology

The generator emits one root package and seven (eight with feature gates)
sub-packages per project. Use the integration prototype at
`tests/integration/` as the canonical reference — it is regenerated from
`testschema/` on every build and exercises every feature.

| Path                           | Holds                                                                 | Does NOT hold                                                                            | Look at                                                  |
| ------------------------------ | --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| Root (`velox/`, project-named) | `Client`, `Open`, `WithTx`, `Use`, `Intercept`                        | No CRUD code, no entity structs                                                          | `tests/integration/client.go`                            |
| `entity/`                      | Concrete entity structs, edge methods, `HookStore`, `InterceptorStore`, GraphQL pagination types | Per-entity client methods, predicates, schema constants                                  | `tests/integration/entity/user.go`                       |
| `client/{entity}/`             | Heavy per-entity client + builders: `NewXxxClient`, `Create`, `Update`, `Delete`, `Query`, `Mutation`, GraphQL input types | Schema metadata, predicates, edge interfaces                                             | `tests/integration/client/user/client.go`                |
| `{entity}/`                    | True leaf: field constants (`FieldID`, `FieldName`), edge name constants (`EdgePosts`), typed predicates (`HasPosts`, `IDField.EQ`), enum types | NO references back to `query/`, `client/{entity}/`, or `entity/`                         | `tests/integration/user/user.go`, `tests/integration/user/where.go` |
| `query/`                       | Query builders (`*XxxQuery`), where `Where`, `WithXxx`, `Order`, `Limit`, `Offset`, `Paginate` live | No mutation builders, no `Filter` GraphQL types                                          | `tests/integration/query/user.go`                        |
| `predicate/`                   | One generic predicate type per entity: `type User func(*sql.Selector)` | No predicate _functions_; those live in `{entity}/where.go`                              | `tests/integration/predicate/predicate.go`               |
| `filter/`                      | GraphQL `XxxWhereInput` types and `Filter() (predicate.X, error)`     | NO `query/` import. `Filter()` returns a predicate, not a `*XxxQuery` mutator            | `tests/integration/filter/user.go`                       |
| `migrate/`                     | Atlas-backed schema migrator                                          | No diff/snapshot data; that's runtime-only                                               | `tests/integration/migrate/migrate.go`                   |
| `hook/`                        | Hook helpers (`On`, `If`, `Reject`, `Unless`)                         | No actual hook bodies — those are user code                                              | `tests/integration/hook/hook.go`                         |
| `intercept/`                   | Interceptor helpers (`TraverseUser`, function helpers)                | No actual interceptors                                                                   | `tests/integration/intercept/intercept.go`               |
| `privacy/`                     | Per-entity policy adapters (`QueryRuleFunc`, `MutationRuleFunc`)      | The policy types themselves (live in top-level `velox/privacy/`)                         | `tests/integration/privacy/privacy.go`                   |

### Layer interactions in one picture

```
            (root package)
                 │
                 ▼
            entity/                  ◄── concrete structs, edge methods,
              ▲      ▲                   HookStore, InterceptorStore,
              │      │                   GraphQL pagination types
              │      └─── filter/    ◄── XxxWhereInput → predicate.X
              │              │
   client/{entity}/          ▼
   (per-entity heavy)   predicate/   ◄── leaf type defs only
              │              ▲
              ▼              │
          query/  ───────────┘
              │
              ▼
         {entity}/           ◄── TRUE LEAF: schema consts + predicates
```

Every arrow goes downward; no arrow returns. This is the invariant that
the cycle-break refactor (Section 3) bought.

### Why heavy code lives in `client/{entity}/`

User code rarely imports `client/{entity}/` directly — the root client
re-exports everything as `client.User.Create()`, `client.User.Query()`, etc.
The package is split out from `entity/` so each per-entity build unit stays
small: changing `Post`'s hooks does not invalidate `User`'s compile cache.
That is what unlocks the incremental-rebuild numbers.

---

## 3. Why This Layout Exists

A sub-package layout is the only way velox holds linear build/RSS scaling
at 100+ entities. A flat Ent-style layout was measured and rejected; see
[`docs/scale-performance-2026-04-25.md`](scale-performance-2026-04-25.md)
for cold-build, peak-RSS, and incremental-rebuild numbers across
stress-100, stress-200, and stress-328 fixtures.

The current layout is the result of two recent refactors:

1. **Cycle-break (2026-04-24, [migrations/2026-04-24-cycle-break.md](migrations/2026-04-24-cycle-break.md))** —
   pre-cycle-break velox had `entity/ → filter/ → query/ → entity/`,
   blocking entity-level edge methods from carrying
   `where *filter.XxxWhereInput`. The fix split each per-entity package
   into a true leaf `{entity}/` plus a sibling heavy client
   `client/{entity}/`, and dropped `query/` from `filter/`'s imports.
2. **Edge-method `where` autobind (2026-04-25, [migrations/2026-04-25-edge-method-where.md](migrations/2026-04-25-edge-method-where.md))** —
   spent the new freedom: edge methods like `(*User).Posts(ctx, after,
   first, before, last, orderBy, where *filter.PostWhereInput)` are now
   real methods on `*entity.User` with full gqlgen autobind, **no
   user-written resolver code**. See Section 5.

Read the migration docs if you want the full motivation. The rest of this
doc takes the current layout as given.

---

## 4. Gotchas (Curated)

CLAUDE.md catalogs every gotcha. The seven below are the ones that bite
first-time users — schema-authoring traps and runtime contracts that look
fine until they don't.

### 4.1. `Optional()` does not add a DB DEFAULT

`Optional()` marks the field optional in the schema graph; it does **not**
emit `DEFAULT NULL` or any other DEFAULT clause. Inserts that omit the
column fail unless the column is nullable or has a `Default(...)`.

```go
// Wrong — insert without `age` fails
field.Int("age").Optional()

// Right — nullable column, Go *int
field.Int("age").Nillable()

// Right — nullable column with DEFAULT
field.Int("age").Optional().Default(0)
```

For non-standard types (enum, bytes), `compiler/gen/graph_validate.go`
enforces this at codegen time: `Optional()` without `Default()` or
`Nillable()` is a build error.

### 4.2. `Nullable()` was removed; use `Nillable()`

The field-builder method is `Nillable()` (equivalent to
`Optional().Nillable()`): DB `NULL`, Go `*T`.

```go
field.String("middle_name").Nillable()    // VARCHAR NULL, Go *string
```

### 4.3. Mutations are opt-in via `graphql.Mutations()`

By default, `contrib/graphql` only generates GraphQL **queries** for an
entity — no `createUser`, `updateUser`, `deleteUser`. To opt into mutations,
add the annotation explicitly:

```go
func (User) Annotations() []schema.Annotation {
    return []schema.Annotation{
        graphql.QueryField(),
        graphql.RelayConnection(),
        graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate),
    }
}
```

`WhereInput` is the same shape — whitelist per field via `graphql.WhereInput()`
on the field, or `graphql.WhereInputEdges("posts", "tags")` for edges. See
`examples/fullgql/schema/todo.go` for a complete worked example.

### 4.4. SQLite driver name is `"sqlite"`, not `"sqlite3"`

Velox uses pure-Go SQLite (`modernc.org/sqlite`), so the driver name is
`"sqlite"` and the foreign-keys pragma syntax is different from
`mattn/go-sqlite3`:

```go
import _ "modernc.org/sqlite"

client, err := velox.Open(dialect.SQLite, "file:app.db?_pragma=foreign_keys(1)")
```

For in-memory test databases:

```go
velox.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
```

See `examples/basic/e2e_test.go:27` for the canonical test setup.

### 4.5. Inside a transaction, call `entity.Unwrap()` before returning entities

Entities created inside `client.WithTx(...)` carry the transaction's driver.
After commit, any read through that entity — including `u.QueryPosts()` and
GraphQL edge resolvers walking `u.Edges` — fails with `sql: transaction has
already been committed`.

Call `Unwrap()` before handing the entity to a caller that outlives the tx:

```go
err := velox.WithTx(ctx, client, func(tx *velox.Tx) error {
    u, err := tx.User.Create().SetName("alice").Save(ctx)
    if err != nil {
        return err
    }
    user = u.Unwrap()    // detach from tx-driver
    return nil
})
// user.QueryPosts() works here; without Unwrap() it panics post-commit.
```

`Unwrap()` panics on non-transactional entities by design, matching Ent's
contract. See `runtime/unwrap.go:5` for the interface and
`examples/basic/e2e_test.go` for post-commit edge-read tests.

### 4.6. Velox does NOT generate `(*Entity).Update()` / `.Delete()`

A deliberate departure from Ent. Velox's pattern is:

```go
client.User.UpdateOne(u).SetName("bob").Save(ctx)   // outside a tx
tx.User.UpdateOne(u).SetName("bob").Save(ctx)       // inside  a tx
```

Reasons: (1) entity types live in the neutral `entity/` package, which
cannot import per-entity sub-packages where `*UserUpdateOne` is defined;
(2) `client.User.UpdateOne(u)` uses `client.config.Driver`, NOT
`u.Config().Driver` — so mutations are immune to the tx-driver-on-entity
leak that `Unwrap()` patches for reads. Ent's `u.Update()` does not have
this property.

If you take `*entity.User` as a function argument and want to update it,
the function should also take a `*velox.Client` or `*velox.Tx`.

### 4.7. `CreateBulk(...).OnConflict(sql.DoNothing())` returns unreliable IDs

For bulk inserts with mixed-duplicate input, `sql.DoNothing()` produces a
`RETURNING` clause with fewer rows than inputs. The ID slice returned by
`Save(ctx)` cannot be aligned back to input slots. Use `sql.ResolveWithIgnore()`
instead — it emits `DO UPDATE SET col=col`, preserving the no-op semantic
at the DB level while producing one `RETURNING` row per input:

```go
// Avoid for mixed-duplicate input — IDs of skipped rows are unreliable
client.User.CreateBulk(builders...).
    OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
    Save(ctx)

// Prefer
client.User.CreateBulk(builders...).
    OnConflict(sql.ConflictColumns("name"), sql.ResolveWithIgnore()).
    Save(ctx)
```

Pinned by `tests/integration/e2e_bulk_create_test.go:566` (DoNothing failure
case) and `:646` (ResolveWithIgnore correct case).

---

## 5. GraphQL Extension Contract

velox + gqlgen interaction is structured around five rules. Get these
right and almost no resolver code is needed.

### 5.1. `XxxWhereInput.Filter()` returns a predicate

```go
where := &filter.UserWhereInput{
    Name: ptr("alice"),
    HasPosts: ptr(true),
}
p, err := where.Filter()
if err != nil { return err }
users, err := client.User.Query().Where(p).All(ctx)
```

`Filter()`'s signature is `(predicate.X, error)`. It does **not** take a
`*XxxQuery` (that pre-cycle-break shape is gone). Apply the returned
predicate via `q.Where(p)`.

Source: `tests/integration/filter/user.go:53`.

### 5.2. Edge method autobind — no resolver code needed

For every edge connection (with or without `where`), velox generates a
method on the entity type with the exact signature gqlgen autobind expects:

```go
func (m *User) Posts(
    ctx context.Context,
    after *gqlrelay.Cursor, first *int,
    before *gqlrelay.Cursor, last *int,
    orderBy *PostOrder,
    where *filter.PostWhereInput,
) (*PostConnection, error)
```

gqlgen sees this method during codegen and binds the SDL field directly to
it. Your gqlgen package needs **zero** resolver code for edge connections —
even ones with a `where:` arg. Source:
`tests/integration/entity/gql_edge_user.go:12`.

Pre-2026-04-25 this required a `@goField(forceResolver: true)` directive
plus a hand-written ~5-line resolver per where-carrying edge. Both are
gone. If you are upgrading from a pre-2026-04-25 velox, follow
[migrations/2026-04-25-edge-method-where.md](migrations/2026-04-25-edge-method-where.md)
to remove now-dead resolver stubs.

### 5.3. Eager-load fast path — zero round-trip

When the parent query eager-loads the edge AND the edge call has no `where`
/ `after` / `before`, the generated edge method reuses the loaded slice via
`entity.BuildXxxConnection`:

```go
// Single DB round trip for the entire user.posts response:
users, _ := client.User.Query().
    WithPosts(func(q *query.PostQuery) { q.Order(post.ByCreatedAt()) }).
    All(ctx)

// gqlgen autobind invokes (*User).Posts(...) per user; with no where/after/before,
// it returns the eager-loaded slice — no second round trip.
```

`TotalCount` on the connection reflects the eager-loaded slice (matches
Ent). Conditions for the fast path: `where == nil && after == nil &&
before == nil` and the edge actually loaded
(`Edges.PostsOrErr() == nil err`). Source:
`tests/integration/entity/gql_edge_user.go:13`.

### 5.4. Pagination boundaries: `WithXxxOrder`, `WithXxxFilter`

Inside `Paginate`, two extension points are available:

- `WithXxxOrder(orderBy *XxxOrder)` — applies the GraphQL `orderBy:` arg.
- `WithXxxFilter(filter func() (predicate.X, error))` — applies a closure
  that returns a predicate. The closure shape matches `*XxxWhereInput.Filter`'s
  method-value, so the generated edge method passes
  `WithPostFilter(where.Filter)` directly.

Source: `contrib/graphql/gen_entity_pagination.go:285` (`genModelPaginationDefs`).

### 5.5. Where to put resolver code

Anything gqlgen can't autobind to: custom queries, mutations, scalar
resolvers, fields not in the generated schema. **Edge connections (with or
without `where`) need zero resolver code.** If you find yourself writing
a 5-line resolver that delegates to `Paginate`, you are duplicating the
generated entity method — delete it.

---

## 6. Recommended Startup Pattern

```go
import (
    "github.com/syssam/velox/dialect"
    _ "modernc.org/sqlite"
    "example.com/myapp/velox"
)

func main() {
    ctx := context.Background()

    client, err := velox.Open(dialect.SQLite,
        "file:app.db?cache=shared&_pragma=foreign_keys(1)")
    if err != nil {
        slog.Error("open", "err", err); os.Exit(1)
    }
    defer client.Close()

    if err := client.Schema.Create(ctx); err != nil {
        slog.Error("migrate", "err", err); os.Exit(1)
    }

    // ... start HTTP server, gqlgen handler, etc.
}
```

The generated root client transitively imports every per-entity
sub-package, so the per-entity `init()` blocks (which register query
factories, mutators, node resolvers, and policies) run as a compile-time
guarantee. A missing entity sub-package is therefore a Go build error,
not a runtime concern.

---

## 7. Where to Go Next

| You want to                                  | Look at                                                                     |
| -------------------------------------------- | --------------------------------------------------------------------------- |
| See a minimal CRUD project                   | [`examples/basic/`](../examples/basic/) — 4-entity SQLite                   |
| See a full GraphQL stack                     | [`examples/fullgql/`](../examples/fullgql/) — gqlgen + 10 entities + hooks  |
| See production-shape code                    | [`examples/realworld/`](../examples/realworld/) — Unwrap() tx contract demo |
| Read the schema DSL reference                | [`docs/reference.md`](reference.md)                                         |
| Get started step-by-step                     | [`docs/getting-started.md`](getting-started.md)                             |
| Migrate from Ent                             | [`docs/migrating-from-ent.md`](migrating-from-ent.md)                       |
| Read past breaking-change records            | [`docs/migrations/`](migrations/)                                           |
| Read benchmarks                              | [`docs/benchmarks.md`](benchmarks.md), [`docs/scale-performance-2026-04-25.md`](scale-performance-2026-04-25.md) |
| See full feature list and project README     | [`README.md`](../README.md)                                                 |

`CLAUDE.md` at the repo root is the AI-assistant / maintainer reference.
Skim it if you are contributing to velox itself; otherwise the docs above
are the user-facing surface.
