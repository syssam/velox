# velox2 Core — design

Status: draft for review · 2026-10-14 · Go 1.27+

velox2 is an open-source ORM for Go. Its author is its first production user;
it is built to hold up at the scale of a large company: thousands of tables,
hundreds of services, many teams. This document specifies **Core**, the first of
five sub-projects. The others plug into what Core defines and get specs of
their own.

| # | Sub-project | Depends on |
|---|---|---|
| 1 | **Core** (this document): entity model, generator, query and mutation API, transactions, relations and eager loading, SQL rendering for three dialects, the execution pipeline | — |
| 2 | Policy and guardrails: tenant scoping, row-level policy, hooks, required limits, deadlines | Core's pipeline |
| 3 | Observability: OpenTelemetry spans and metrics, query fingerprints, call-site SQL comments (`2026-10-15-observability-design.md`) | Core's pipeline |
| 4 | Routing: primary/replica, read-your-writes, shard-key hook | Core's pipeline and executor |
| 5 | Migration planner: diff structs against a live database, emit reviewable SQL with safety lints | Core's entity metadata |

All five ship in 1.0.

## Decisions

Made by the author during design (2026-10-14):

- A new design; it does not derive from, stay compatible with, or migrate from
  the earlier `velox` project.
- Open source; the author's own services are the first consumer.
- **Plain Go structs are the source of truth**, each in the package of the team
  that owns it.
- **Metadata goes in a typed `Schema(s *velox.Schema)` method** that refers to
  fields by pointer.
- **PostgreSQL, MySQL and SQLite** are all supported in 1.0. PostgreSQL is the
  reference dialect.
- 1.0 ships guardrails, observability, the migration planner and routing.

Taken on the designer's recommendation, open to change at review:

- Updates are set-based or name their columns; there is no write-back of a
  whole struct (§4.4).
- No `Find(ctx, id)` shortcut in 1.0; a single row is `Where(X.ID.EQ(id)).One(ctx)` (§4.3).
- A query with no `Limit` fails unless it says `Unbounded()` (§4.3, enforced by sub-project 2).
- GraphQL is not part of 1.0.

## Success criteria

1. Every statement velox sends is predictable from the code that sent it, and
   can be printed without a database (`SQL(ctx)`; the policy design changed it
   from `SQL()` so that it includes tenant and policy scope).
2. Compile cost follows what a package uses, not how many entities the
   application has (§3, measured).
3. Nothing proportional to the number of entities runs at program start.
4. Per-query overhead over hand-written `database/sql` stays within the budget
   in §8.
5. Every feature passes on all three dialects, or is documented as
   dialect-specific and refused on the others.

## 1. Entity model

```go
package billing

type Invoice struct {
    common.Timestamps                  // embedded: a mixin, its columns flatten in
    ID      int64                      // primary key by convention
    OrgID   int64
    Number  string
    Total   decimal.Decimal
    PaidAt  *time.Time                 // pointer: the column allows NULL
    Lines   velox.Many[Line]           // relation, never a column
    Org     velox.One[identity.Org]
    Display string `velox:"-"`         // exported, not a column
    cache   string                     // unexported: never a column
}

func (x *Invoice) Schema(s *velox.Schema) {
    s.Table("invoices")
    s.Column(&x.Total).SQLType("numeric(18,2)")
    s.Unique(&x.OrgID, &x.Number)
    s.Tenant(&x.OrgID)
    s.Many(&x.Lines).ForeignKey(func(l *Line) *int64 { return &l.InvoiceID })
}
```

Rules:

1. **Entity.** A struct type with a `Schema(*velox.Schema)` method declared on
   its own pointer type (not one promoted from an embedded field).
2. **Columns.** Every exported field is a column, except relations and fields
   tagged `velox:"-"`. Unexported fields never are. `velox:"-"` is the only
   struct tag velox reads: it is the one fact the static generator needs that
   the struct's shape cannot express.
3. **Names.** Column names are the snake_case of field names
   (`OrgID` → `org_id`); table names are the snake_case of the type name.
   `s.Table` and `s.Column(&x.F).Name("…")` override.
4. **NULL is in the type.** `*T` allows NULL; `T` is NOT NULL. velox does not
   use `sql.NullString` and similar types.
5. **Primary key.** A field named `ID`, or `s.PrimaryKey(&x.A, &x.B)` for a
   composite key.
6. **Mixins.** A struct embedded in an entity contributes its fields as columns.
   If it declares `Mixin(*velox.Schema)`, velox calls that before the entity's
   `Schema`, for every embedded mixin, automatically. A mixin is never asked to
   be called by hand, so it cannot be forgotten.
7. **Relations.** `velox.One[E]` and `velox.Many[E]` fields.
   - They are never loaded lazily. The zero value means "not loaded";
     `Get()` returns `(value, loaded bool)`. Tag relation fields
     `json:",omitzero"` to leave not-loaded relations out of JSON; without the
     tag they marshal as null.
   - Keys, by convention:
     - **To-one:** a One relation named R uses the owner's field `RID` when
       there is one: the owner belongs to the target, whose primary key RID
       holds.
     - **Otherwise:** the target holds the key in a field named after the
       owner: `InvoiceID` on `Line` for a relation of `Invoice`.
   - **Declared keys:**
     `s.Many(&x.Lines).ForeignKey(func(l *Line) *int64 { return &l.InvoiceID })`
     names the target's column, and `s.One(&x.Org).References(&x.OrgID)` the
     owner's. Declaring a relation's key twice is a `SchemaError`.
     `ForeignKey`'s function runs once, while the schema is built.
   - **Key types:** a relation joins one column to a single-column primary key
     of the same Go type (ignoring one level of pointer).
   - **When a mistake surfaces:** at the relation's first use, and in CI through
     `velox-gen verify`.
   - Many-to-many goes through an explicit join entity.
   - Relations follow Go's import graph, so a cross-package relation points one
     way only. See §3 rule 6 for what a cross-package relation costs.
8. **`Schema` is declarative.** It runs once per entity, on first use, against a
   zero value, and must not read runtime state. Field pointers are resolved to
   columns by offset from the zero value, disambiguated by type (a pointer to an
   embedded struct and one to its first field share an address). Its builder
   methods are generic, so `s.Column(&x.Paid).Default("no")` does not compile
   when `Paid` is a `bool` (verified on Go 1.27.1).

## 2. Generated code

`go tool velox-gen ./...` runs once from the module root (installed as a tool
dependency in `go.mod`). It loads struct shapes with `go/packages` and never
builds or runs the application's code.

It writes one file per entity, beside the file declaring it:

```go
// Code generated by velox-gen. DO NOT EDIT.

package billing

import (
    "time"

    "example.com/identity"
    "github.com/shopspring/decimal"
    "github.com/syssam/velox2" // module path to be confirmed, see §11
)

// Fails to compile if this file and the velox library disagree.
const _ = velox.GeneratedCodeIsVersion2

// Invoices holds the typed handles of Invoice's columns and relations.
var Invoices = struct {
    CreatedAt velox.OrderedCol[Invoice, time.Time]
    UpdatedAt velox.OrderedCol[Invoice, time.Time]
    ID        velox.NumberCol[Invoice, int64]
    OrgID     velox.NumberCol[Invoice, int64]
    Number    velox.StringCol[Invoice, string]
    Total     velox.NumberCol[Invoice, decimal.Decimal]
    PaidAt    velox.NullOrderedCol[Invoice, time.Time]
    Lines     velox.Rel[Invoice, Line]
    Org       velox.Rel[Invoice, identity.Org]
}{
    CreatedAt: velox.OrderedCol[Invoice, time.Time]{I: 0},
    // … one line per handle: an index, static data, no code at init
}

var invoiceMeta velox.MetaOnce[Invoice] // zero value: nothing runs at program start

// VeloxMeta is called by velox, not by applications.
func (*Invoice) VeloxMeta() *velox.Meta[Invoice] { return invoiceMeta.Get(invoiceShape) }

// Fails to compile when a field's type changed and this file was not regenerated.
func _(x *Invoice) {
    var _ *time.Time = &x.CreatedAt
    // … one line per column
}

func invoiceShape() velox.Shape[Invoice] {
    return velox.Shape[Invoice]{
        Columns:   []string{"CreatedAt", "UpdatedAt", "ID", "OrgID", "Number", "Total", "PaidAt"},
        Relations: []velox.Relation{velox.RelationTo[Line]("Lines"), velox.RelationTo[identity.Org]("Org")},
        Addr: func(x *Invoice, i int) any { // scan targets: no reflection per row
            switch i {
            case 0:
                return &x.CreatedAt
            // …
            }
            return nil
        },
    }
}
```

That is about 3 lines per field plus 35 per entity (98 lines per entity
measured on the simulated schema). The only addition to the entity's own type is
the method `VeloxMeta`.

### Handle types

The handle type follows the field's type, by its underlying type
(`type Cents int64` is a number):

| Field type | Handle | Adds |
|---|---|---|
| `bool`, `[]byte`, `velox.JSON[T]`, other `Scanner`/`Valuer` types | `Col` | `EQ` `NEQ` `In` `NotIn` `Asc` `Desc` `Set` |
| `time.Time`; types registered as ordered | `OrderedCol` | `GT` `GTE` `LT` `LTE` `Between` |
| integers, floats, `decimal.Decimal`; types registered as numbers | `NumberCol` | `Add` `Sub`, `Sum` `Avg` |
| `~string` | `StringCol` | `Like` `Contains` `HasPrefix` `HasSuffix` `EqualFold` |
| `*T` of any of these | `Null…Col` | `IsNull` `NotNull` `SetNull` |
| `velox.One[T]` / `velox.Many[T]` | `Rel` (one type for both kinds) | `Has` `HasWhere` `Where` `OrderBy` `With` (§4.3) |

Each column handle produces values typed by its entity (`velox.Pred[Invoice]`,
`velox.Order[Invoice]`, `velox.Setter[Invoice]`), so a condition on `Line`
passed to a query on `Invoice` does not compile, and neither does
`Invoices.Paid.GT(x)` on a `bool`. Types are registered once on the generator's
command line (`-ordered=…`, `-number=…`).

### Generator rules

- **Names.** The handle variable is the entity's plural, by fixed rules (`y` →
  `ies`; `s`/`x`/`z`/`ch`/`sh` → `es`; else `s`). A collision with an existing
  identifier is a generate-time error; `//velox:handle Name` on the struct
  overrides the name.
- **Imports.** The file imports velox and only packages the struct's own file
  already imports. Generation never adds an edge to the import graph.
- **Determinism.** Output is byte-identical for the same input, already
  gofmt'd. Files with velox-gen's header whose entity is gone are deleted.
  `velox-gen -check` exits non-zero when any file would change; CI runs it.
- **Stale code.** If a struct is edited and not regenerated, the first
  operation on that entity returns an error naming the mismatched field and the
  command to run. No row is ever scanned into the wrong field.
  A field whose type changed is caught at compile time: each generated file
  holds a function, never called, that assigns `&x.Field` to a variable of the
  field's generated type, so the build fails naming the field.
- **Verifying `Schema` methods.** The generator reads only struct shapes, so a
  mistake inside a `Schema` method (a pointer to a non-column, a conflicting
  option) would otherwise surface at the entity's first use, possibly in
  production. `velox-gen verify ./...` closes that gap: it writes a temporary
  program that imports every entity package, builds every entity's metadata,
  and reports all `*SchemaError`s. CI runs it beside `-check`. Nothing about it
  exists at run time: no registry is added to applications.
- **Speed.** Measured on the 2,000-entity simulation: loading all 100 entity
  packages from source takes 1.4–2.0 s (2.4 CPU-s, 450 MB); one package takes
  0.16 s. `velox-gen <package>` regenerates one package for editors and
  `go:generate`; `./...` is for CI.

## 3. Compile- and link-cost rules

These rules are part of the contract, enforced by tests (§9). Each was found
or confirmed by measurement (Appendix A).

1. **Every method of a generic velox type is a generic method**, with a type
   parameter callers never write (`func (c NumberCol[E, V]) GT[_ Lazy[V]](v V) Pred[E]`).
   An ordinary method of a generic type is compiled into every package that
   instantiates the type, whether called or not; a generic method only where
   it is called. Measured: a type with 30 methods, one called, compiles 60
   function bodies into the caller's package as ordinary methods and 1 as
   generic methods. One exception: each column handle type has the
   unexported ordinary method `column(*E) int32`, which makes it a
   `velox.Column[E]` so that `Columns(…)` and `Omit(…)` accept handles of every
   level. An interface is the only way Go lets one parameter accept them, and an
   interface needs an ordinary method. It is one small method, and it takes
   `*E`, so a handle of another entity does not satisfy `Column[E]`.
   `One[T]` and `Many[T]` also have `MarshalJSON`, an ordinary method because
   `encoding/json` finds it by name; its body is one call. Tag relation fields
   `json:",omitzero"` to leave unloaded ones out.
2. **No generic velox type's descriptor reaches another entity.** Generic
   instantiations have no home package, so every package that needs one emits
   its runtime type descriptor, and everything that descriptor reaches. The
   relation holders `One[T]` and `Many[T]` therefore store their value as
   `any`; `T` appears only in method signatures. Measured: holding `*T`/`[]T`
   (plus one marker method) made a service touching 5 entities emit descriptors
   for 180 entities; the fix cut cold-build CPU from 304 to 134 seconds and
   monolith link memory from 2.6 to 1.1 GB. The same holds for relation
   metadata: a generated entity's Desc never names its relation targets'
   metadata (no `VeloxMeta`, no getter; the join entity of a many-to-many
   relation is the one exception, and it has no relations). The use site supplies the target: the
   `Rel[E, T]` handle puts it in the load or condition that uses the relation.
   A Desc that named its targets would link every entity reachable through
   relations, which is the whole schema when every edge has an inverse; this
   keeps a service's binary independent of the size of the schema (A.6).
3. **No ordinary method on a velox generic type** where avoidable. Relation
   fields are recognized by type name, not by a marker method; a method set
   adds descriptors for each method's type to every instantiation.
4. **Nothing at init.** Generated code declares only static data and zero
   values; metadata is built on first use. Measured: 0.47 ms total package init
   for a binary linking 2,000 entities.
5. **No package depends on every entity.** There is no generated root package,
   client or registry. Each entity is reached through its own package.
   Measured: a one-service binary is 3.2 MB in an application of 2,000
   entities, and linking one service's test binary peaks at 129 MB against
   771 MB when all entities share one package. (With the schema DSL all
   entities are one generated package by choice; the service binary stays
   independent of N by rule 2, and the linker's memory does not: see the
   2026-10-19 note in A.6.)
6. **Cross-package relations are a cost the application chooses.** Go
   recompiles every package whose exported types mention a changed type,
   transitively. A typed relation from package A to package B puts B's entity
   in A's exported API. Measured: editing an entity at the root of a tree of
   domain packages linked by relations recompiled all 402 packages; editing a
   leaf recompiled 11. velox documents this and recommends referring to
   entities in other domains by key (`OrgID int64`) rather than by relation
   when the target is widely depended on.
7. **No velox type that generated code, an entity's methods, a Statement or a
   Query reaches holds `reflect.Type` or `reflect.Value` in a field or method
   signature.** `reflect`'s iterator types (`iter.Seq[reflect.Type]`…) have no
   home package, so every package that reaches `reflect.Type` compiles about
   134 KiB of their wrappers and descriptors. Measured: `var x *velox.EntityInfo`
   cost 134 KiB of object before, 0.3 KiB after. Go types are held as `any`;
   `velox.GoType(c)` reads a column's. The same holds for the bodies of the
   generic functions and methods compiled where they are called
   (`MetaOnce.Get`, `Schema.Column`, `Schema.One`/`Many` and their keys, a
   Query's run, Insert, Save, Update, Delete): they pass pointers and types as
   `any` to non-generic helpers that use `reflect`. Measured: a one-column
   entity package cost 283 KiB before, 97 KiB after; one with two relations
   that also queries, inserts, saves, updates and deletes its entity 884 KiB
   before, 658 KiB after.

## 4. Query and mutation API

### 4.1 One handle type

`*velox.DB` is the only type that runs statements, inside or outside a
transaction (`tx` in `db.Tx` is also a `*velox.DB`). Generic methods cannot
appear in interfaces, so a single concrete type is required anyway, and it means
every function taking `*velox.DB` works in a transaction unchanged.

```go
db := velox.OpenPgx(pool)                    // PostgreSQL: a *pgxpool.Pool the application opened
db := velox.Open(velox.MySQL, sqlDB)         // MySQL, SQLite (and PostgreSQL if a *sql.DB is required)
```

### 4.2 Create

```go
err := db.Insert(&inv).Exec(ctx)                 // type parameters inferred from &inv
err = db.InsertAll(lines).Exec(ctx)              // multi-row INSERT, batched per dialect limits
err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx)
```

Inserts are builders because Go infers a generic function's type parameters from
its arguments but not from an interface a column handle implements:
`velox.OnConflict(I.OrgID)` could not infer the entity,
`db.Insert(&inv).OnConflict(I.OrgID)` can.

Generated keys and database defaults are read back into the struct: `RETURNING`
on PostgreSQL and SQLite, `LastInsertId` on MySQL for an insert without
OnConflict (MySQL's insert id does not name the row after
`ON DUPLICATE KEY UPDATE`). Upsert renders `ON CONFLICT … DO UPDATE` /
`DO NOTHING`, or on MySQL `AS new ON DUPLICATE KEY UPDATE` (8.0.19 and later).
A static `Default` on a bool or numeric field must use a pointer field, so an
explicit false or 0 can be inserted; `DefaultFunc` applies to any type and
fills a zero field.

### 4.3 Read

```go
I, L := billing.Invoices, billing.Lines

one, err := db.Query[billing.Invoice]().Where(I.ID.EQ(id)).One(ctx)
// ErrNotFound for no row, ErrNotSingular for more than one (LIMIT 2 is sent).

page, err := db.Query[billing.Invoice]().
    Where(I.PaidAt.IsNull(), I.Total.GT(hundred)).
    OrderBy(I.CreatedAt.Desc()).
    With(I.Lines.Where(L.Qty.GT(0)).OrderBy(L.SKU.Asc())).
    Limit(50).
    All(ctx)

for inv, err := range db.Query[billing.Invoice]().Unbounded().Iter(ctx) { … }

n, err := db.Query[billing.Invoice]().Where(I.PaidAt.IsNull()).Count(ctx)
text, args, err := db.Query[billing.Invoice]().Where(…).SQL(ctx) // no database needed
```

- `With` loads a relation with one extra query per relation,
  `WHERE key IN (…)`, chunked under each dialect's parameter limit.
  - Chain `With` for several relations; a relation's own `With` loads nested
    ones.
  - Related entities come in the order given by the relation's `OrderBy`, by
    default the target's primary key.
  - A to-one entity can be shared by several owners.
  - `Iter` does not load relations.
  - Nothing is ever loaded lazily.
  - Errors in a `With`'s options are reported before any query runs. A query
    whose `Columns`/`Omit` leave out a loaded relation's key column is an
    error.
  - Each relation chunk leaves 64 parameters for conditions that rewrite hooks
    add.
- `Iter` returns `iter.Seq2[E, error]` and holds one row at a time.
- `Columns(I.ID, I.Total)` selects only those columns and `Omit(I.Payload)`
  all but those; the unselected fields keep their zero values. Both take
  handles of the query's own entity, so a column of another entity does not
  compile. The generated scan plan addresses columns by index, so a partial
  select costs nothing extra.
- `Has` / `HasWhere` render `EXISTS (…)`. On MySQL an `UPDATE` or `DELETE`
  whose `EXISTS` reads the table being changed fails with error 1093.
- Predicates combine with `velox.And`, `velox.Or`, `velox.Not`; a `Where` with
  several predicates is their `AND`.
- Keyset pagination: `After(lastRow)` continues after the previous page's last
  row. The `OrderBy` must include the primary key or a unique index, and no
  nullable column.
  - `x` must be a row a query returned.
  - Ordering by a nullable column, or by a slice or map column (nil is NULL),
    is refused.
  - `Count` applies `After`'s condition.
- Offset pagination is `Offset(n)`.
- A statement with more parameters than the dialect allows is refused before it
  is sent.
- `SQL(ctx)`, on `Query`, `InsertQuery`, `UpdateQuery` and `DeleteQuery`,
  returns the statement and arguments execution would send for that context,
  or the error execution would fail with before sending it (the parameter
  limit and the `With` key-column check included); it sends nothing. It runs
  every stage before execute: the guard, entity hooks, `Rewrite` hooks, tenant
  and policy scope, the row cap and render, with `Statement.DryRun` set. An
  insert's `SQL(ctx)` fills defaults and the tenant into the caller's structs
  and runs `BeforeInsert`, as `Exec` does; an `InsertAll` larger than one
  statement returns the first. The policy design (§1.1) gives the details.
- `EQ(nil)` and `NEQ(nil)` with an untyped nil or a nil `[]byte` render `IS NULL` and `IS NOT NULL`; a nil of another type is compared with `=` and matches nothing.

### 4.4 Update

```go
n, err := db.Update[billing.Invoice]().
    Set(I.PaidAt.Set(now), I.Total.Add(five)).
    Where(I.ID.EQ(id)).
    Exec(ctx)                                   // rows affected

err = db.Save(ctx, &inv, I.Number, I.Total)     // a loaded struct; columns named
```

On MySQL, `Save` needs `clientFoundRows=true` in the DSN.

There is no `Save(&x)` that writes every column: writing back a stale struct
silently loses concurrent updates. `UpdatedAt`-style columns are declared by a
mixin (`s.Column(&t.UpdatedAt).OnUpdate(time.Now)` in its `Mixin` method), not
recognized by name.

### 4.5 Delete

```go
n, err := db.Delete[billing.Line]().Where(L.InvoiceID.EQ(id)).Exec(ctx)
```

### 4.6 Transactions

```go
err := db.Tx(ctx, func(tx *velox.DB) error { … }, velox.RetrySerializable(3))
```

The function's error rolls back; `nil` commits; a panic rolls back and
re-panics. `RetrySerializable` re-runs the function on serialization failures
and deadlocks, as classified per dialect. Nested `Tx` uses savepoints.

## 5. Execution pipeline

Every operation becomes one non-generic `*velox.Statement` and passes through
the same stages (the policy design, `2026-10-14-policy-design.md` §1, adds
guard, entity-hook and scope stages and the statement fields `Tenant`,
`TenantBypass`, `PolicyBypass` and `DryRun`). Sub-projects 2–4 are written against these stages and nothing
else.

```
build ─▶ rewrite ─▶ render ─▶ route ─▶ execute ─▶ scan
          (hooks)              (hook)   (around)
```

| Stage | What happens | Who plugs in |
|---|---|---|
| build | The generic API fills a `Statement`: operation, entity info, predicates, assignments, order, limit, flags (`Unbounded`) | — |
| rewrite | Ordered hooks may add predicates, set assignments, or refuse the statement | Tenant scoping, row-level policy, guardrails (sub-project 2) |
| render | The dialect turns the statement into SQL and arguments, and the fingerprint, stored in the render cache; observability appends the SQL comment after rendering | Dialects; observability (sub-project 3) |
| route | Picks the executor: primary, replica, shard | Routing (sub-project 4); default: the `DB`'s own connection |
| execute | Ordered `around` hooks wrap running the statement and reading its rows: the innermost `next` sends it through the executor (`database/sql` or pgx), scans every row (the scan stage) and closes the rows before it returns, so a span or a deadline covers the whole query; with `Iter`, the caller's loop body runs inside it too | Spans and metrics (`contrib/otel`), retries |
| scan | Rows into entities through the generated scan plan, inside execute's `next` | — |

The pipeline is configured on `velox.Open` and immutable afterwards. Core
ships the rewrite and around stages (`velox.WithRewrite`, `velox.WithAround`),
and `velox.WithTxAround`, which wraps each `db.Tx` call the way an around hook
wraps a statement: it runs once per call, and its `next` runs every
`RetrySerializable` attempt and the commit (observability design §2.2). The
route stage's option arrives with sub-project 4; adding an option breaks no
caller.

```go
type Statement struct {
    Op           Op              // Select, Insert, Update, Delete
    Entity       *EntityInfo     // table, columns, keys, tenant column, policies
    Count        bool            // SELECT COUNT(*)
    Columns      []int           // SELECT: selected columns, nil selects all; INSERT: written columns
    Where        []Predicate
    Set          []Assignment    // UPDATE
    Rows         [][]any         // INSERT: one value per Columns entry
    Conflict     *ConflictClause // INSERT: upsert
    Returning    []int           // INSERT: read back (PostgreSQL, SQLite)
    AllRows      bool            // UPDATE, DELETE: said All
    OrderBy      []OrderTerm
    Limit        int
    Offset       int
    Unbounded    bool
    MaxRows      int             // SELECT: the most rows it may return; 0: the DB's WithMaxRows
    Tenant       any             // the tenant scope applied; nil when unscoped
    TenantBypass string          // the AllTenants reason, when it applied
    PolicyBypass string          // the SkipPolicy reason, when it applied
    DryRun       bool            // built for SQL(ctx), not sent
    InTx         bool
    Tx           *TxInfo         // the transaction it runs in, when the DB has hooks; nil otherwise
    Dialect      Dialect         // the DB's: an Around hook has only the statement
    Tags         []Tag           // the context's WithTags tags; a Rewrite hook gets its own copy
    Caller       Caller          // the call site, with WithCallers; zero otherwise
    SQL          string          // set by render, after the rewrite hooks; WithSQLComment's comment included
    Fingerprint  string          // set by render: the shape's ID, the hash of SQL without the comment
    Args         []any           // set by render
    Result       Result          // set by each execution: Rows read, rows Affected (-1 for a read)
}
```

`MaxRows`, `Tenant`, `TenantBypass`, `PolicyBypass` and `DryRun` come from the
policy design (§2.3, §4), and so does `ConflictClause.Where`, the condition of
an upsert's update branch, which PostgreSQL and SQLite render as
`DO UPDATE … WHERE` and velox refuses on MySQL. `Dialect`, `Tags`, `Caller`,
`Tx`, `Fingerprint` and `Result` come from the observability design (§1, §2).

Rendering caches SQL text by statement shape (operation, entity, predicate
structure, order, presence of a limit), so a statement that differs only in its
values reuses the text. The simulation measured rendering at 0.7 µs and 12
allocations per query without a cache.

## 6. Dialects

PostgreSQL is the reference; every feature is defined there first.

| | PostgreSQL | MySQL 8.0.19+ | SQLite 3.35+ |
|---|---|---|---|
| Executor | native pgx (`pgx/v5`, a `*pgxpool.Pool`) | `database/sql` + `go-sql-driver/mysql` | `database/sql` + `modernc.org/sqlite` (no cgo) |
| Placeholders | `$1` | `?` | `?` |
| Read back on insert | `RETURNING` | `LastInsertId` | `RETURNING` |
| Upsert | `ON CONFLICT … DO UPDATE` | `ON DUPLICATE KEY UPDATE` | `ON CONFLICT … DO UPDATE` |
| Serialization retry codes | `40001`, `40P01` | 1213, 1205 | `SQLITE_BUSY` |

MySQL DSNs need `parseTime=true` and, for `Save`, `clientFoundRows=true`. CI
tests MySQL 8.4.
SQLite enforces foreign keys only when the DSN asks for it.

Statements go through an executor: an internal interface whose rows type has
`Next`, `Scan(dst ...any)`, `Err` and `Close`, which both `*sql.Rows` and
`pgx.Rows` provide. PostgreSQL uses native pgx; MySQL and SQLite use
`database/sql`. Measured against PostgreSQL 16 (Appendix A.5), `database/sql`
over pgx costs 2.6 times the time and 2.5 times the allocations of native
pgx for 1,000 rows, far more than velox's own overhead. An application that
must hand velox a `*sql.DB` for PostgreSQL can, through
`velox.Open(velox.Postgres, sqlDB)`, and pays that cost knowingly.
`velox.OpenPgx(pool)` is the recommended form.
Constraint violations are normalized into `*velox.ConstraintError` with a kind
(unique, foreign key, check, not null) and the constraint name, on all three.

## 7. Errors

Sentinels for `errors.Is`: `ErrNotFound`, `ErrNotSingular`, `ErrUnbounded`,
`ErrNoWhere`, `ErrLimitTooLarge`, `ErrTooManyRows`, `ErrTimeout`, `ErrStale`
(generated code out of date). Typed errors for
`errors.As`: `*ConstraintError{Entity, Kind, Constraint, Err}` with `Kind` one
of `Unique`, `ForeignKey`, `NotNull`, `Check`; `Constraint` is the
constraint's name, except that SQLite reports the columns and a NOT NULL error
the column. `*SchemaError` (an invalid `Schema` method,
reported with the entity and field). Sentinels are bare phrases, always
returned wrapped (policy design §6). Every error message starts with `velox:`
and names the entity.

`ErrUnbounded` (a query with no `Limit` and no `Unbounded()`) and `ErrNoWhere`
(an update or delete with no `Where` and no `All()`) are raised
by velox (policy design §4). `ErrLimitTooLarge` (a `Limit` over `WithMaxLimit`),
`ErrTooManyRows` (a read past `WithMaxRows` or the query's `MaxRows`) and
`ErrTimeout` (a deadline velox set passed; it also matches
`context.DeadlineExceeded`) are raised under the same rules.

## 8. Performance budget

Measured on the simulation against a driver that returns canned rows, so only
velox's own cost counts. Core's benchmarks must stay within:

| Case | Measured (simulation, no SQL cache) | Budget for Core |
|---|---|---|
| One-row query | +2.0 µs, +13 allocations (4.8 vs 2.8 µs; 25 vs 12) | at most +1 µs and +8 allocations |
| 100-row query | within noise of hand-written (120 vs 131 µs median; +14 allocations) | at most +5% time, +16 allocations |
| Metadata, first use of an entity | 7.8 µs, 36 allocations, once per process | at most 20 µs |

Measured on 2026-10-14, darwin/arm64 (Apple M3 Max), after Core, against the
same fake driver, with the shape cache built:

| Case | velox | hand-written |
|---|---|---|
| One-row query | 607 ns, 6 allocations | 4 allocations |
| 100-row query | 11.7 µs, 105 allocations | 202 allocations |
| Metadata, first use of an entity | 2.7 µs, 30 allocations | |
| One-row query, `WithCallers` and `WithSQLComment` | 1.03 µs (minimum) to 1.38 µs (median), 7 allocations (observability design §6) | |

The one-row row with callers and comments was measured on 2026-10-15 against
`BenchmarkOneRow`'s 0.69 µs, 6 allocations: the minimum of 600 alternating
batches of 2,000 reads, because the machine was busy (load average above 20)
and its medians moved by a third. The added 340 ns are the comment's join (80
ns) and one `runtime.Callers` (260 ns; unwinding costs about 60 ns a frame, the
skipped ones included): 40 ns over the observability design's 300 ns aim.
Re-measured the same day at a load average of 15 to 26, the medians were
0.87 µs plain and 1.38 µs observed (`-count 10`, +518 ns), and 10
interleaved pairs differed by a median of +478 ns (300 to 652): over the aim
by about 200 ns. The allocations (6 and 7) were the same in every run. The
observability design's §6 has the table; runs at a load average above 39
moved by up to 4x and are left out.

The allocation budgets are tests (`budget_test.go`, `meta_bench_test.go`);
the time budgets are read from the benchmarks, not asserted, because times
vary between machines. The "Measured" column of the first table is the
simulation's, kept for what it showed before the cache.

Most of the one-row gap was rendering (1.6 µs and 12 allocations measured
alone), which the shape cache in §5 removes. For scale: a database round trip
is typically 100 µs or more, so the budget is about keeping velox out of
profiles, not about what users wait on.

## 9. Testing

- **Dialect matrix.** Every integration test runs on PostgreSQL, MySQL and
  SQLite. The CI services are mirrored by a committed local script
  (`scripts/ci-local.sh`, docker compose), so CI can be reproduced before
  pushing.
- **Golden SQL.** Each operation's rendered SQL is pinned per dialect.
- **Generated code.** Golden files per entity shape; `velox-gen -check` in CI;
  a test that the generator never adds an import.
- **Compile-cost guards** for §3, and where each lives:
  - every method of every exported generic type is generic, and has a
    `[_ Lazy[...]]` type parameter: `methods_test.go`;
  - each handle kind has exactly the methods of the handle table in §2, so a
    method granted to the wrong kind, or dropped, fails: the method-set table
    in `handles_test.go` (`TestHandleMethodSets`);
  - a package of one variable per exported generic type, and the entity
    packages (`entity`, `entityUse` rows), must stay under an object-size
    threshold, which also guards the `reflect.Type` rule (§3 rule 7):
    `compilecost_test.go`;
  - generated code contains no `init` and no package-level function call:
    velox-gen's `TestGeneratedCodeRunsNothingAtStart`.
- **Scale harness.** The simulation used for this design is `bench/scale`: it
  generates an application of N entities and S services and reports cold
  build, incremental rebuilds, the service binary's size and the linker's peak
  memory (since 2026-10-16 the entities are one generated package and the
  rebuild is the one after a schema edit; see A.6). The thresholds are in `bench/scale/thresholds.json`; CI runs it at
  N=500 and fails when one is exceeded. The full N=2,000 run is a release
  check.
- **Fuzzing** of the SQL builder (`fuzz_test.go`: placeholders match
  arguments, no argument appears in the SQL text, quotes stay balanced, the
  cached path equals the uncached one), run briefly in CI; `-race`
  everywhere.

## 10. Toolchain requirements

Generic methods need Go 1.27, and tools built for it. Observed on 2026-10-14:
gopls v0.22.0 reports generic methods as a syntax error and staticcheck
2025.1.1 does not run under Go 1.27; gopls v0.23.0, staticcheck 2026.2.2
(v0.8.2) and golangci-lint v2.14.0 handle them. velox documents these as minimum
tool versions.

## 11. Open and deferred

Open, for the author before implementation starts:

- The module path. This document uses `github.com/syssam/velox2`.
- The three choices taken on recommendation (see Decisions).

Deferred past Core:

- Typed `Find(ctx, id)` (costs one more generated method per entity).
- Optimistic locking with a version column.
- JSON column operators beyond equality.
- GraphQL.

## 12. Risks and unverified assumptions

What the simulation established is in Appendix A. The following has not been
established:

1. **Go 1.27 as the minimum.** Generic methods arrived in Go 1.27 (August
   2026). Large companies often run one or two releases behind, and support in
   debuggers, mock generators and documentation tools has not been checked.
   The alternative, ordinary methods, measured 60 compiled bodies instead of
   1 per package. The bet is that 1.27 is an ordinary minimum by the time velox2
   reaches 1.0.
2. **Runtime times are from a shared machine**, with PostgreSQL in Docker on
   macOS, whose network adds about 1 ms per round trip. Allocation counts are
   exact; times are indicative. The shape cache is built; the one-row and 100-row
   allocation budgets are tests. MySQL and SQLite have not been benchmarked.
3. **velox over native pgx has not been measured.** Appendix A.5 measures
   velox over `database/sql` and hand-written native pgx; the claim that velox
   adds as little over pgx as over `database/sql` is an expectation, to be
   confirmed when the pgx executor exists.
4. **The simulated application is synthetic**: uniform entities, domain
   packages in a tree, 5 entities per service. The direction of every result
   should hold for real applications; the magnitudes may not.
5. **No direct comparison with existing ORMs** (Ent, sqlc, Bun, GORM) on the
   same schema. For runtime, A.5 gives proxies: sqlc generates code equivalent
   to the hand-written native pgx column, and Ent, Bun and GORM read through
   `database/sql`, so they cost at least the hand-written `database/sql`
   column. Build cost against Ent has not been measured.
6. **Field pointers are resolved with `unsafe`** (offset from the entity's
   address). The use is confined to one function and to `Schema` evaluation,
   but some organizations audit every use of `unsafe`.
7. **Not simulated:** mutations, eager loading, mixins applied through
   reflection, and MySQL and SQLite rendering.

## Appendix A — measurements

Simulation: 2,000 entities of 15 columns and 2 relations, in 100 domain
packages of 20 (domain packages form a tree through relations); 300 services,
each using 5 entities from 3 domain packages, with one test each; one monolith
binary importing every service. Compared with the same code placing all 2,000
entities in one central package. Apple M3 Max, 14 cores, Go 1.27.1; another
workload shared the machine during some runs, so CPU-seconds are the reliable
figure, wall time is indicative.

### A.1 Relation holders: `*T` versus `any` (per-domain layout)

| | Holders store `*T`/`[]T`, marker method | Holders store `any` (§3 rules 2–3) |
|---|---|---|
| Cold build | 304 CPU-s | 134 CPU-s |
| Monolith link, peak memory | 2.6 GB | 1.1 GB |
| Archives read by the monolith link | 3.5 GB | 1.4 GB |
| One service's archive | 6.8 MB | 2.9 MB |
| Velox type descriptors in one service | 5,545, naming 180 entities | — |

### A.2 Per-domain packages versus one central package (holders store `any`)

| Scenario | Per-domain: CPU-s / packages compiled | Central: CPU-s / packages compiled |
|---|---|---|
| Cold build | 134 / 405 | 123 / 306 |
| Peak memory, cold build | 1.1 GB | 1.9 GB |
| No-op rebuild | 2.4 / 0 | 2.2 / 0 |
| Edit a struct in a leaf domain package | **7 / 11** | 124 / 303 |
| Edit a struct at the root of the domain tree | 135 / 402 | 123 / 303 |
| Edit only a `Schema` method body | **5.6 / 12** | 121 / 301 |
| Edit a service | 3.9 / 3 | 4.2 / 3 |
| Run all 300 services' tests, no edit | **38** | 60 |
| Run all 300 services' tests after a root edit | **162** | 225 |
| Peak memory linking one test binary | **129 MB** | 771 MB |
| Monolith binary / one-service binary | 29.7 MB / 3.2 MB | 28.9 MB / 3.2 MB |
| Package init, monolith | 0.3 ms | 1.0 ms |

Reading: per-domain packages cost the same as a central package for a cold
build or for an edit that everything depends on, and 15–20 times less for the
common case of an edit that only part of the application depends on. Test
binaries link with a sixth of the memory. The root-edit row is §3 rule 6.

### A.3 Method style on generic types

A library type with 30 methods, one of them called from a package: 60 function
bodies are compiled into the calling package when the methods are ordinary, 1
when they are generic methods.

### A.4 Runtime (fake driver; median of 6 one-second runs)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| 1 row, velox | 4,784 | 2,528 | 25 |
| 1 row, hand-written `database/sql` | 2,796 | 1,008 | 12 |
| 1 row, reflection-mapped | 2,748 | 1,008 | 12 |
| 100 rows, velox | 119,787 | 40,666 | 572 |
| 100 rows, hand-written | 131,270 | 39,138 | 558 |
| 100 rows, reflection-mapped | 142,523 | 39,137 | 558 |
| Build and render SQL only | 1,630 | 1,280 | 12 |
| Entity metadata, first use | 7,778 | 3,064 | 36 |

The machine was shared with another workload during these runs (ranges up to
±25%); differences under that are not meaningful.

### A.5 Runtime against PostgreSQL 16 (median of 6 one-second runs)

PostgreSQL 16 in Docker on macOS (data on tmpfs, `fsync=off`), 20,000 rows of
the 15-column entity, one connection, the same SQL for all three. pgx v5.11.0.

| Rows | velox (simulated runtime, `database/sql` + pgx stdlib) | hand-written `database/sql` + pgx stdlib | hand-written native pgx |
|---|---|---|---|
| 1 | 1,475 µs · 70 allocs | 1,153 µs · 65 allocs | 1,509 µs · 21 allocs |
| 100 | 4,883 µs · 1,815 allocs | 3,312 µs · 1,801 allocs | 1,629 µs · 732 allocs |
| 1,000 | 17,584 µs · 18,372 allocs | 18,335 µs · 18,357 allocs | 6,837 µs · 7,210 allocs |

Docker's network on macOS adds about 1 ms per round trip, so one-row times are
mostly network and their differences are noise. velox adds 5 to 15
allocations over hand-written `database/sql`. Native pgx takes 0.4 times the
time and allocations of `database/sql` at 1,000 rows; that is why §6 makes it
PostgreSQL's executor.

### A.6 Scale harness after Core

`bench/scale` on darwin/arm64 (Apple M3 Max), 10 entities per domain package,
5 entities per service, measured on 2026-10-14 after Core was complete:

| | N=500, S=100 | N=2,000, S=400 |
|---|---|---|
| Cold build | 14.3 s | 32.4 s |
| Rebuild after editing a leaf entity | 1.05 s | 2.2 s |
| Rebuild after editing a root entity | 5.9 s | 14.1 s |
| One service's binary | 5.47 MB | 5.47 MB |
| Linker peak memory | 187 MB | 202 MB |

The service binary does not grow with the application and the link memory
barely does (§3 rules 2 and 5); the root-edit rebuild is §3 rule 6. Times
include the harness's `-toolexec` wrapper. The recorded CI thresholds for
N=500 are in `bench/scale/thresholds.json`.

**Note, 2026-10-16: the scale figures now measure one generated package.**
The table above is the history of the per-domain layout and stays as it was
measured. `bench/scale` no longer writes a tree of domain packages with
struct-and-`Schema` entities. It writes N schemas (the schema DSL, `schema/e<i>.go`),
generates them with velox-gen into one `gen` package, and builds S services
that each query five entities of `gen`. One package has no leaves and roots, so
the two rebuild rows became one, `schema_edit_rebuild_s`: add a field to one
schema, regenerate, and rebuild every service. The relation shape is the old
one (`bench/scale/main.go`, `generate`). Measured on the same machine (M3 Max)
at N=500, S=100, three runs at loads of 3.25 to 3.94 (the largest of each):

| | Per-domain packages (2026-10-14) | One generated package (2026-10-16) |
|---|---|---|
| Cold build | 14.3 s | 29.4 s |
| Rebuild after a schema edit | 1.05 s (leaf), 5.9 s (root) | 27.1 s (edit, regenerate, rebuild every service) |
| One service's binary | 5.47 MB | 23.6 MB |
| Linker peak memory | 187 MB | 404 MB |

The service binary and the link memory now grow with N (23.6 MB at N=500, 14.5
MB at N=250, 9.1 MB at N=100), which the per-domain layout did not; §3 rules 2
and 5 describe the old layout. The CI thresholds for N=500 were re-baselined
from these runs.

**Note, 2026-10-19: a service's binary is independent of N again.** The growth
above had one cause. A Desc listed each relation with `EdgeTo[T]`, which kept
a closure that called T's `VeloxMeta`, so linking an entity linked every
entity reachable through relations; every schema has an inverse edge, so the
500 schemas are one connected component. Since this date `EdgeTo` carries no
target (it keeps the holder types, which are type descriptors only). The
target comes from where the relation is used: `Rel[E, T]`'s `spec`, `Where`,
`Has` and `HasWhere` put `T`'s metadata getter in the load or the condition,
and `velox.CheckRelation(gen.Users.Posts)` resolves one relation, which
`velox-gen verify` calls for every relation (`CheckEntity` no longer resolves
the relations of generated code). `EdgeThrough` keeps its join entity's
getter; a join entity has no relations of its own, so that is one hop and does
not depend on N. Measured the same way, N=500, S=100, three runs (the largest
of each), at 1-minute loads of 2.57, 6.00 and 11.37 (only the first under 4;
the harness waited 30 minutes for the others):

| | One generated package, before (2026-10-16) | After |
|---|---|---|
| Cold build | 29.4 s | 25.3 s |
| Rebuild after a schema edit | 27.1 s | 22.4 s |
| One service's binary | 23.6 MB (9.1 MB at N=100) | 5.66 MB (5.66 MB at N=100) |
| Linker peak memory | 404 MB | 279 MB (185 MB at N=100) |

The binary is flat. The linker's peak still grows with N (185 MB at N=100,
279 MB at N=500): it reads the whole `gen` package's object file, though it
keeps only what the service reaches. The CI thresholds for N=500 were
re-baselined from the 2026-10-19 runs: times 4x the largest run, sizes and
memory 1.5x (they were identical across loads; a 2x margin would pass the
404 MB link peak of the build before the fix).
