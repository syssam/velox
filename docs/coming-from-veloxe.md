# Coming from veloxe

veloxe is a fork of ent, so most of what a port changes is what an ent
port changes. [coming-from-ent.md](coming-from-ent.md) lists that: the
schema rules, the query and write calls, policies, hooks and the
generator's Go API. This page lists only what is particular to veloxe: its
generator options, its auto-migration, its own names, and the behaviour a
veloxe application sees change.

## The generator

### velox-gen has no veloxe option

velox-gen reads a veloxe schema package as veloxe did, with no option to
ask for it ([ADR 0090](decisions/0090-veloxe-defaults.md)). These rules are
its defaults:

- An unset `Optional` field is written as its zero value, and its column
  stays NOT NULL. A NOT NULL string, bool or number column gets its zero as
  its column `DEFAULT`, as `FeatureAutoDefault` gave it.
- Index names are prefixed with the table, and long names are capped with
  veloxe's FNV hash ([ADR 0083](decisions/0083-package-index-names.md)).
  A codegen hook that prefixed index names is not needed.
- `sqlschema.NonUniqueFK` drops the key's `UNIQUE`, as in veloxe.
- A check's or a not-null helper's name only has to be unique within its
  table ([ADR 0084](decisions/0084-constraint-names-per-table.md)).
- A one-to-one pair to its own schema may keep its key on the assoc edge
  ([ADR 0085](decisions/0085-self-one-to-one-key-on-assoc.md)).
- Two enum fields whose Go types get one name and hold the same values
  share the type
  ([ADR 0086](decisions/0086-shared-enum-types.md)).
- `Policy()` may return veloxe's `privacy.Policy{Query: …, Mutation: …}`.
  When a schema's policy file imports velox's `privacy`, velox-gen writes
  `<out>/privacy` beside the entities, as veloxe did
  ([ADR 0089](decisions/0089-veloxe-privacy-shape.md)).

### velox-graphql's options

veloxe's GraphQL extension took Go options. velox-graphql is a plugin, and
takes `key=value` options after a colon:

```sh
go tool velox-gen -out ./velox \
  -plugin velox-graphql:compat=veloxe,where=whitelist,engine=graphql-go \
  ./veloxschema
```

| veloxe | velox-graphql |
| --- | --- |
| the API `graphqlgen` made | `compat=veloxe` ([ADR 0087](decisions/0087-graphql-veloxe-compat.md), [0088](decisions/0088-graphql-veloxe-node-and-filters.md), [0108](decisions/0108-veloxe-compat-paging-and-fk-filters.md)) |
| a hook that adds `SkipWhereInput` to every field without `WithWhereInputs(true)` | `where=whitelist` |
| `graphqlgen.WithoutGQLGen()`, served by graphql-go | `engine=graphql-go` |
| a hook that sets `OpsString` on filterable string fields | not needed: `OpsString` is a string field's default |
| `WithSchemaSplitMode(SchemaSplitPerEntity)` | the default under `compat=veloxe`: `velox.graphql` and `velox_<entity>.graphql`, veloxe's names; `sdlfiles=one` writes one file ([ADR 0125](decisions/0125-graphql-sdl-per-entity-and-subset.md)) |
| a slicer over the SDL for an application that serves part of the schema | `entities=SalesOrder+SalesOrderItem` ([ADR 0125](decisions/0125-graphql-sdl-per-entity-and-subset.md)) |
| `WithSchemaPath` | not offered: the SDL is in the generated package |

`compat=veloxe` makes the API veloxe's generator made:

- Fields and edges take veloxe's camel case of the schema name:
  `customer_id` is `customerId`.
- A foreign key bound to an edge stays in the type as an `ID`, and its
  predicates are named from the field (`customerId`, `customerIdIn`).
- An `Optional` field is nullable in the type, and a `Required` unique
  edge is non-null.
- The order enum lists the fields veloxe's listed. A `graphql.OrderField`
  on a field of a type veloxe did not order, such as a decimal, a JSON
  field or a `Text`, is dropped, as veloxe dropped it, and velox-graphql
  warns of it, naming the entity and the field.
- Every type with an ID implements `Node`, and the root query has
  `node(id:)` and `nodes(ids:)`. The application answers them with the
  generated `ResolveNode` and `ResolveNodes`.
- Where inputs, cursors, ties, page flags and foreign-key predicates
  behave as veloxe's did. [What stays the same](#what-stays-the-same-under-compatveloxe)
  lists them.

Under `engine=graphql-go`, the generated code serves graphql-go through
`veloxgql/graphqlgo`: `graphqlgo.With(db)` as an executor option,
`graphqlgo.Scalars()` and `graphqlgo.ErrorPresenter`. A schema that does
not declare all of Time, UUID, Bytes and Uint64 binds the ones it declares
with `graphqlgo.ScalarsOnly`. veloxe's `Paginate` ran in any context;
velox's runs outside a request only under `veloxgql.Service(ctx, db)`.

### generate.go

A `generate.go` that calls `compiler.Generate` with `gen.NewConfig` becomes
one that calls `generate.Run` with a `generate.Config`
([ADR 0097](decisions/0097-generate-package.md)).
[Running the generator from Go](coming-from-ent.md#running-the-generator-from-go)
maps each `gen` option. veloxe's codegen hooks map as follows:

| veloxe hook | velox |
| --- | --- |
| a hook that sorts each entity's fields | none: `CREATE TABLE` follows the schema's declaration order, so declare the fields in the order wanted |
| a hook that prefixes index names with the table | the default |
| a where-input whitelist hook | `where=whitelist` |
| a hook that widens string where operators | the default |
| `compiler.BuildFlags("-p=" + n)` | `Env: append(os.Environ(), "GOFLAGS=-p="+n)` |
| `compiler.Extensions(graphqlgen.NewExtension(...))` | `Plugins: []generate.Plugin{graphqlgen.Plugin(opts)}`, from `contrib/graphql/graphqlgen` |

A program that calls `generate.Run` with a plugin's `Run` is its own check:
run it with `Check: true` in CI, since `velox-gen -check` does not know it.

`gen.FeatureBidiEdgeRefs` has no velox form; see
[Behaviour that changes](#behaviour-that-changes).

## Moving from auto-migration

A veloxe application often migrates with a program like this:

- `client.Schema.Create(ctx, opts...)` on every deploy;
- `migrate.WithDropColumn` and `migrate.WithDropIndex` behind flags;
- `migrate.WithForeignKeys(false)`;
- `client.Schema.WriteTo(ctx, os.Stdout)` for a dry run;
- hand-written SQL before and after, for what `Create` cannot do.

velox has no auto-migration at deploy ([ADR 0006](decisions/0006-own-migrate-and-atlas.md)).
A test or a development database gets its tables from `veloxtest.Open` or
`migrate.Create`, which make them in an empty database and never alter one
([ADR 0113](decisions/0113-test-schema-create.md)).
It writes numbered SQL files from the schema, which you read and commit,
and applies them in order. The database is adopted once, then each schema
change is one file.

**Adopt the database once.**
[Adopting an existing database](../examples/README.md#adopting-an-existing-database)
gives the five steps. In short:

1. `go tool velox-gen migrate baseline -dialect postgres -db <dsn>` writes
   `./migrations/0001_baseline.sql`, which builds the tables as that
   database holds them.
2. `go tool velox-gen migrate apply -baseline 1 -db <dsn>` records it as
   applied, running nothing, on each existing database.
3. `go tool velox-gen migrate drift -db <dsn>` checks each one.

**Then each change is a file.**

| veloxe | velox |
| --- | --- |
| `client.Schema.Create(ctx)` at deploy | `migrate diff` when the schema changes, then `migrate apply -db <dsn>`, or `migrate.Up(ctx, velox.Pool(db), dir)` in the program |
| `client.Schema.WriteTo(ctx, os.Stdout)` | `migrate diff -dry-run` prints the file it would write; `migrate apply -dry-run` prints the files it would apply |
| `migrate.WithDropColumn(true)` | `migrate diff -allow drop-column=users.nickname` |
| `migrate.WithDropIndex(true)` | none: `diff` plans a dropped index, as a finding to review |
| a hand-written table rename before `Create` | `migrate diff -rename-table old=new`, or `-rename-column table.old=new` |
| hand-written SQL around `Create` | statements you add to the file `diff` wrote, then `migrate hash` |

`diff` compares the schema with the directory's `schema.json`, not with a
database. `migrate drift -db <dsn>` compares a database with it.

`migrate diff` refuses a destructive change until `-allow` names it, such
as a dropped table or column. It also refuses a NOT NULL column with no
default, which would fail on a table that has rows. A string, bool or
number column gets its zero as its default, so only another type, such as
a JSON column, meets this. Give the field a `Default` or make it
`Nillable`, or add the statements that fill it to the file by hand.
[A join table becomes an edge schema](migrations-operations.md#a-join-table-becomes-an-edge-schema)
shows the edit and `migrate hash`.

velox declares a foreign key for every edge, and has no option to leave
them out. A database made with `WithForeignKeys(false)` has none, and
baseline records it as it is. So the first `diff` adds every foreign key.
Each fails on rows whose key matches no row of its target: find and fix
those first.

A field made `Nillable` gets `DROP NOT NULL` from `diff`, so a hand step
that relaxed such columns is not needed.

When `apply` refuses or stops,
[migrations-operations.md](migrations-operations.md) says what to do.

## Names

Calls ent and veloxe share are in
[Queries and writes](coming-from-ent.md#queries-and-writes). These are
veloxe's own, or differ in a veloxe port:

| veloxe | velox |
| --- | --- |
| `velox.Open(dialect.Postgres, dsn)` | `velox.Connect("postgres", dsn, opts...)` |
| `Modify(func(s *sql.Selector){…})`, `func(*sql.Selector)` predicates, `WhereP` | `velox.Expr` for a condition, `velox.Calc` for a term or an order, `Select` or `SelectRaw` with `Rows` for a projection, `velox.SetExpr` for an update ([sql.md](sql.md#coming-from-ent)) |
| `COUNT(*) OVER ()` in a `Modify` | `velox.Calc[T]("COUNT(*) OVER ()")` or `SelectRaw`; a window function is refused in `Expr`, `GroupBy`, an aggregate and `SetExpr` ([ADR 0101](decisions/0101-window-terms-soft-delete-options.md)) |
| a report's table from field names to columns, `sql.As(f, name)` by name | `velox.ColumnByName[T](column)`, a Term that selects, groups and orders ([guide](guide.md#date-buckets-subtotals-and-columns-by-name), [ADR 0127](decisions/0127-report-terms.md)) |
| a date bucket in a `Modify`: `TO_CHAR(? AT TIME ZONE 'z', 'YYYY-MM')`, `date_trunc` | `velox.DateTrunc[T](velox.UnitMonth, col, velox.InZone(z))`, the bucket's first instant on every dialect; `FiscalYear(m)` for fiscal years and quarters; a date part (`EXTRACT`) stays a `Calc` |
| `GroupBy(f…).Aggregate(velox.As(velox.Sum(col), n))` with a run-time `col` | `t, _ := velox.ColumnByName[T](col)`; `Select(key, t.Sum().As(n)).GroupBy(key)` ([ADR 0142](decisions/0142-nextapp-projection-recipes.md)) |
| `GroupBy(a, b).Scan` with no aggregate | `Select(a, b).Distinct()` or `Select(a, b).GroupBy(a, b)`, read with `velox.Scan` |
| `COUNT(DISTINCT CASE WHEN c <> 0 THEN c END)` in a `Modify` | `velox.Calc[T]("CASE WHEN ? <> 0 THEN ? END", c, c).CountDistinct()` |
| `AppendSelectExprAs(expr, alias)` beside entity rows, read with `Value(alias)` | `Select` of every column (`ColumnByName`) and the `Calc` `.As(alias)`, scanned into a struct embedding the entity; the keyset an `Expr` over the same Term ([guide](guide.md#date-buckets-subtotals-and-columns-by-name)) |
| a derived table in `Modify` so the parent's columns group the child | `Join(rel)` and `velox.Joined(rel, col)` in `Select` and `GroupBy`, the parent's policy in ON ([ADR 0143](decisions/0143-joined-terms-in-projections.md)) |
| a hand join to a dated table with no edge (`store_group_member` on the document's date) | read the memberships first, then a `CASE` term over the document's own columns; a correlated outer reference is not built ([ADR 0142](decisions/0142-nextapp-projection-recipes.md)) |
| `sqljson.ValueContains(f, v, sqljson.Path(k))` | `T.F.Array(k).Has(v)` ([ADR 0141](decisions/0141-json-array-at-a-path.md)) |
| `GROUP BY ROLLUP(…)`, `CUBE(…)` in a `Modify` | `GroupBy(velox.Rollup[T](keys...))`, `velox.Cube`; MySQL `WITH ROLLUP` as the only key, SQLite refuses |
| field names kept beside each `Order` | `o.Term().Column()` and `o.Descending()`; `RawRow.Columns()` for a row's names |
| `client.Intercept(...)`, `intercept.Func` | `velox.WithFilter(func(ctx, db *velox.DB, entity string, op velox.Op) (velox.Predicate, error))`, which returns `velox.Allow()` for no condition ([guide](guide.md#hooks-and-filters-for-every-entity)) |
| `client.Use(hooks...)`, `m.Type()` | `velox.WithHooksAll(hooks...)`, `m.Entity()` |
| the generated package's `velox.TypeUser` | `veloxgen.TypeUser`, the same value: the entity a filter receives, `m.Entity()` and `velox.HasColumn`'s first argument ([ADR 0139](decisions/0139-entity-type-constants.md)) |
| a privacy rule's `WhereP(func(*sql.Selector))` | `Filter.Where(velox.Predicate)` |
| `client.User.Get(ctx, id)`, `UpdateOneID(id)…Save(ctx)`, `DeleteOneID(id).Exec(ctx)` | `Where(U.ID.EQ(id))` and `One(ctx)` on `db.Query`, `db.Update` and `db.Delete`, or `db.Save(&u, cols...)` for an entity in hand ([guide](guide.md#one-row-by-key), [ADR 0121](decisions/0122-one-row-by-key.md)) |
| `ForUpdate()` on SQLite, which veloxe ran without the clause | the same, under the option `velox.WithSQLiteLockingReads()`; without it the read fails ([guide](guide.md#row-locks), [ADR 0123](decisions/0123-sqlite-locking-reads-opt-in.md)) |
| `SkipDefault(user.FieldUpdatedAt)` | `KeepDefaults(U.UpdatedAt)` |
| `SetNillableNickname(p)` | `Set(U.Nickname.SetPtr(p))` |
| the generated package's `IsConstraintError(err)`, `IsValidationError`, `IsNotSingular`, `IsNotFound` | `velox.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular`, `IsNotFound` |
| `ValidationError.Name`, the schema field's name, from which an error presenter derives the GraphQL input key | `velox.ValidationError.Name`, the same; `Field` is the Go name ([ADR 0144](decisions/0144-validation-error-name.md)) |
| `OpCreate` | `OpInsert` |
| `OpUpdate \| OpUpdateOne` | `OpUpdate` |
| `OpDelete \| OpDeleteOne` | `OpDelete` |
| an op tested as a bit set | an op compared with `==`; `velox.On(h, ops...)` runs `h` for the ops listed |
| `tx.OnCommit(func(next velox.Committer) velox.Committer {…})` | `velox.AfterCommit(tx, fn)`: `fn` runs once the outermost transaction has committed |
| `IDs(ctx)` | `velox.IDs[int64](ctx, q.AllowUnbounded())`, or with a `Limit` |
| `Each(ctx, fn)` | `for x, err := range q.Iter(ctx)` |
| a filter's lookup on the client inside `WithTx` | a read through the filter's `db` parameter, the statement's DB ([guide](guide.md#hooks-and-filters-for-every-entity), [ADR 0132](decisions/0132-filter-reads-through-the-statements-db.md)) |
| a client read inside `WithTx` that must not join it | a read on `db` under `velox.WithoutTx(ctx)`; under fn's `ctx` it fails with `ErrOutsideTx` |
| a test's `tx, _ := c.Tx(ctx); defer tx.Rollback()` that never commits | `veloxtest.RolledBack(t, ctx, db, func(ctx context.Context, tx *velox.DB) { … })`: fn's writes roll back, a `t.Fatal` in fn included |
| a failed COMMIT, told apart by its message | `errors.Is(err, velox.ErrCommitUnknown)`: the outcome is unknown, so retry nothing. A COMMIT that says it rolled back does not match, such as PostgreSQL's answer to an aborted transaction, `pgx.ErrTxCommitRollback` ([ADR 0134](decisions/0134-commit-unknown.md)) |
| a helper returning a `nil` predicate for "no condition", checked with `p == nil` | the zero `velox.Pred[E]{}`, checked with `reflect.ValueOf(p).IsZero()`: a `Pred` holds a slice, so it has no `nil` and no `==`. A zero `Pred` that reaches a query fails the statement, as a `nil` predicate panicked |
| a retry and a timeout in every `WithTx` | `velox.WithTxDefaults(velox.TxRetrySerializable(3), velox.TxTimeout(30*time.Second))` at `Connect` ([ADR 0133](decisions/0133-tx-defaults-and-none.md)) |
| `Select(f).Strings(ctx)`, `Ints(ctx)` | `velox.Values(ctx, q, T.F.Key())` |
| the client option `velox.Debug()` | the option `velox.WithLog(logger)`, which writes Debug records, so the logger must be enabled at Debug; `velox.LogArgs()` shows arguments |
| `e.Unwrap()` | none: an entity is a plain struct, with no client in it |
| `edge.Through` | an edge schema, as in ent ([Edges](coming-from-ent.md#edges), [ADR 0109](decisions/0109-edge-schemas.md)) |
| `softdelete` mixin of your own, with `deleted_by_*` | `softdelete.Mixin{Assign: …}`, and `Reads: softdelete.Unfiltered` for unfiltered reads ([guide](guide.md#soft-delete)) |
| `gqlrelay.Cursor{ID, Value, Values}` | `veloxgql.Cursor{Type, Terms, Key}`, bound to a type and an order |
| `gqlrelay.PageInfo` | `veloxgql.PageInfo` |
| `gqlrelay.MaxPaginationLimit` (1000) | `veloxgql.Limits.MaxPage` (100) |
| `gqlrelay`'s paging, where and collection helpers | `gqlgraph`, which only generated code calls |
| `where.P()`, `where.Filter()` | `where.Pred(ctx)`, or the Paginate option `veloxgen.WithXFilter(where)` |
| `where.AddPredicates(p)` | none: `q.Where(p)` before `Paginate` |
| `w.UnmarshalGQL(doc)` of a kept filter (an access policy's `{"ownerId": "123"}`) | `veloxgql.DecodeWhere(doc, &w)`, then `w.Pred(ctx)`. An unknown key or a value of the wrong type is an error naming its path, such as `not.idIn[0]` ([ADR 0146](decisions/0146-decode-where.md)) |

## Behaviour that changes

**`hasX` and `hasXWith` see soft delete and row filters.** veloxe applied
the target's policy inside `hasX` and `hasXWith`, but not the
interceptors registered on the client. A velox `WithFilter` filter, an
interceptor and `softdelete.Mixin` reach every `Has` and `HasWith`, the
GraphQL `hasX` and `hasXWith` included. So a parent whose only matching
children are soft-deleted, or hidden by a row filter, no longer matches.
This is kept: under veloxe, rows a row-scope interceptor hid still made
a parent match. Check each `hasXWith` a client sends, and each
`HasWith` in service code. To match deleted children too, run the query
under the context that shows deleted rows, such as `softdelete.Skip(ctx)`.
That context shows deleted parents as well.

**A filter must not restrict a join entity.** A `WithFilter` filter that
returns a condition for the join entity of a many-to-many edge, or for an
edge schema, fails `Has`, `With`, `LoadEach` and `CountEach` through it.
Return `velox.Allow()` for those names. A filter for a tenant column
on an edge schema is covered in
[Edges](coming-from-ent.md#edges)
([ADR 0098](decisions/0098-hooks-and-filters-for-every-entity.md)).

**A filter is asked about writes.** veloxe's interceptors wrapped queries.
A velox filter is called with the op, `OpSelect` for reads and the write's
op for updates and deletes. A filter that narrows reads only returns
`velox.Allow()` when `op != velox.OpSelect`. Otherwise a restore that
clears `deleted_at` under the plain context updates no row.

**A read on the client inside a transaction fails.** Inside `db.Tx`, a
statement on `db` under fn's `ctx` fails with `ErrOutsideTx`, where
veloxe ran it outside the transaction. Run it on `tx`, or on `db` under
`velox.WithoutTx(ctx)` when it must not join the transaction; on SQLite's
pool of one connection that fails too, rather than wait for the
transaction. A `WithTx(ctx, db, fn)` helper that kept its own transaction
key in the context, so that a nested call joined the outer transaction,
asks `velox.TxFrom(ctx, db)` instead: it returns the transaction ctx
belongs to, with `db`'s own `WithoutPolicy` reason, and the helper runs fn
on it, or begins `db.Tx` when it is false
([ADR 0137](decisions/0137-nextapp-port-helpers.md)).
A row-scope filter that looks up teams or an organization
tree reads through the filter's `db` parameter, which is `tx` inside the
transaction. A lookup on `db` that swallows its error narrows the
statement inside a transaction and not outside.

**A nil predicate is the zero Predicate.** A veloxe helper that returned
a nil predicate for "no condition" returns `velox.Predicate{}` or
`Pred[E]{}`. A statement that holds it fails, so the caller tests it with
`velox.IsZero(p)`, not `p == nil` or reflection.

**A filter finds columns by name.** A `WithFilter` filter that narrows the
entities with `deleted_at` asks `velox.HasColumn(entity, "deleted_at")`,
in place of a generated list of entity names.

**A rule may name columns from configuration.** `velox.FieldIn`,
`FieldEQ` and `FieldInSelect` take any string. velox-gen checks a constant
name and warns on any other, and the statement checks it when it runs.
An unknown name fails the statement, naming the entity and the column.
To check a rule's SQL in a test, render a query that holds it with
`velox.Open(d, nil)` and `Query.SQL`, which needs no database
([guide](guide.md#policies)).

**softdelete records who deleted.** `softdelete.Mixin{Assign: ...}` sets
more columns in the soft delete's UPDATE. `softdelete.By("deleted_by_id",
key)` reads a `scope.Key`'s value, and `softdelete.Assigns` joins several,
for a row that records the user, employee or member. `Reads:
softdelete.Unfiltered` leaves reads unfiltered, as nextapp's services
filter `deleted_at` themselves.

**A transaction retries nothing by default.** nextapp's `WithTx` retried
3 times with a 30-second timeout and turned a panic into an error. A velox
`Tx` runs once, under `WithDeadlines`' `Tx` limit, and panics again after
the rollback. `velox.WithTxDefaults` at `Connect` restores the retry and
the timeout for every `Tx`, and a call's own options override them. A
nested `Tx` does not retry on its own, and `gqlgenx`'s mutation
transaction never retries. A panic stays a panic: recover it in fn to make
it an error.

**Back-references are not filled.** veloxe's `FeatureBidiEdgeRefs` set
each loaded child's parent edge. velox sets a child's to-one edge only
when that edge is loaded, with `With` on the child's query
([Refused by design](coming-from-ent.md#refused-by-design),
[ADR 0101](decisions/0101-window-terms-soft-delete-options.md)).

**`AddPredicates` is gone.** A generated where input has no
`AddPredicates`. Add the condition to the query: `q.Where(p).Paginate(...)`.
`Paginate` takes a query with conditions of its own, but not one with an
`OrderBy`: the connection orders it.

**Three paging pairs are refused.** `first` with `before`, `last` with
`after`, and `after` with `before` are a `BAD_USER_INPUT` error, under
`compat=veloxe` too. veloxe read them as windows. Send `first` with
`after`, or `last` with `before`.

**A zero a setter gives is stored.** A field such as `exchange_rate`, with
a Go `Default` and `sqlschema.Default("1")`, stores 0 from
`Set(X.ExchangeRate.Set(0))`, as veloxe's `SetExchangeRate(0)` did: every
column a setter names is written
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)). A zero the
struct holds with no setter is unset and takes the default. A
`decimal.Decimal` that holds a non-nil `*big.Int`, such as `decimal.Zero`,
is not the Go zero, and is written from the struct too
([guide](guide.md#fields)).

**An empty patch still runs its hooks.** An update whose setters are all
`SetPtr(nil)` runs its hooks and its `UpdateDefault`s, as veloxe's
`UpdateOne` with nothing set did, so `updated_at` moves. When they assign
nothing, no statement is sent and `Exec` returns 0
([ADR 0136](decisions/0136-explicit-zero-and-empty-patch.md)).

**A self one-to-one reads from the right end.** With the key on the assoc
edge, as in `reversed_by`/`reversal_of`, the inverse edge now returns the
row whose key holds its ID. veloxe returned the same row as the assoc edge
([ADR 0085](decisions/0085-self-one-to-one-key-on-assoc.md)).

**Two Optional fields are refused.** An `Optional` enum needs a `Default`
or `Nillable`, as `""` is none of its values. An `Optional` `Unique`
field needs `Nillable` or a `DefaultFunc`, or every unset row would hold
the same value. Any other unset `Optional` field is written as its zero
value, as veloxe wrote it.

**GraphQL limits apply, with or without compat.** An application that
caps pages and masks errors itself, before velox runs, sees none of
these:

- A connection with no `first` or `last` reads `MaxPage` rows, 100 by
  default, where veloxe read every row.
- A `first` or `last` above `MaxPage` fails with `ErrPageTooLarge`. veloxe
  allowed up to 1000. Under graphql-go, `MaxPage` is fixed at 100.
- A list edge, one with no connection, reads at most `MaxList` rows, 1000
  by default. More fails with `velox.ErrTooManyRows`.
- A velox cursor taken under another order is refused, where veloxe read
  its value as the new order's.
- `after: ""` is refused, where veloxe read it as a zero cursor.
- `totalCount` runs its COUNT only when the field is resolved.
- `xContainsFold` and `xEqualFold` compare `LOWER(x)` with `LOWER(v)`,
  where veloxe wrote `ILIKE`.
- An error from velox carries a code, such as `BAD_USER_INPUT`, in
  `extensions.code`.

**An Other field with no SQLite `SchemaType` is a typeless column.** veloxe
made the column with no type name on SQLite, and velox-gen once refused it.
It now makes it as veloxe did, so a `decimal.Decimal` field whose
`SchemaType` names MySQL and PostgreSQL only opens on SQLite. The value is
kept as bound, so `"1.10"` stays `"1.10"`, and `ORDER BY` and `>` compare it
as text. Name `numeric` for SQLite to compare as numbers. MySQL and
PostgreSQL still need a `SchemaType` (ADR 0145).

## What stays the same under compat=veloxe

A client written against veloxe's API sees these as it did:

- **Cursors veloxe wrote are accepted.** A connection reads a veloxe
  cursor's key, and its values as the current order's, as veloxe did. A
  cursor whose number of values differs from the order's terms is refused.
  Pages velox returns carry velox's cursors.
- **Ties follow the order.** With one order term, ties are broken by the
  key in that term's direction: `NAME DESC` puts ties in key DESC order.
  With two or more terms, the key stays ASC.
- **Page flags.** A page after a cursor has `hasPreviousPage: true`, and a
  page before one has `hasNextPage: true`.
- **Foreign-key predicates are plain.** `customerId: 7`, `customerIdIn`
  and `customerIdIsNil` test the column alone. Without compat they read a
  key whose target the reader cannot see as null.
- **Where inputs.** An empty `xIn` or `xNotIn`, a false `xIsNil` or
  `xNotNil`, and an empty `or` add nothing. A null entry is skipped. `not:
  {}` and `or: [{}]` are an error. A list has no width cap; the depth
  stays at 5. Unlike veloxe, a where argument holds at most 10 times
  `MaxFilterNodes` (10000) inputs, predicates and list values.
- **Node.** `node` of an ID no type has, or one the policy hides, is
  `NOT_FOUND`. `nodes` gives null for it. Unlike veloxe, `nodes` reads
  each type with one `IN` query, and takes at most `MaxPage` (100) IDs.
- **The order argument.** It is one `XOrder`, and `direction` defaults to
  ASC.
- **A hidden required edge.** A `Required` to-one edge whose target is
  hidden nulls the parent, as in veloxe.

These hold with or without compat:

- `first: 0` gives an empty page, and `hasNextPage` is true when rows
  exist.
- `totalCount` counts the whole connection, with its `where`, and ignores
  `first` and `last`.
- A nested connection's `first` is per parent.
- An empty result has no edges, null cursors and both flags false.
- The privacy rule lists keep veloxe's meaning: a `Query` list applies to
  reads, a `Mutation` list to writes, and `privacy.DecisionContext` lifts
  them ([ADR 0089](decisions/0089-veloxe-privacy-shape.md)). A rule that
  read through `gen.FromContext(ctx)` or `TxFromContext(ctx)` keeps the DB
  it was given and joins the transaction: `rdb, ok := velox.TxFrom(ctx,
  db); if !ok { rdb = db }`. A read made under a rule's ctx is not
  judged by that rule's list, so a rule that reads its own entity ends
  ([ADR 0135](decisions/0135-policies-read-through-the-transaction.md)).
- `Optional` stays NOT NULL, and `Nillable` makes a column NULL-able
  ([ADR 0003](decisions/0003-nullability.md)).

## Removed schema options

velox keeps one spelling of each schema option, `entsql`'s where ent has
one. A port that names one of these does not compile; write the
replacement.

| Removed | Write instead |
|---|---|
| `schema.Table(name)` | `sqlschema.Table(name)` |
| `schema.TableIn(schema, table)` | `sqlschema.SchemaTable(schema, table)` |
| `schema.Collation(c)` | `sqlschema.Collation(c)` |
| `schema.Check(name, expr)` | `sqlschema.Checks(map[string]string{name: expr})` |
| `index.Fields(...).Where(pred)` | `index.Fields(...).Annotations(sqlschema.IndexWhere(pred))` |
| `field.X(...).SchemaDefault(m)` | `field.X(...).Annotations(sqlschema.DefaultExprs(m))` |
| `field.Postgres`, `field.MySQL`, `field.SQLite` | `dialect.Postgres`, `dialect.MySQL`, `dialect.SQLite` |
| `sqlschema.Check(expr)`, `Annotation.Check` | `sqlschema.Checks`, which names the constraint |
| `sqlschema.Size(n)`, `Annotation.Size` | `field.MaxLen(n)` |
| `sqlschema.ColumnType(t)`, `Annotation.ColumnType` | `field.SchemaType`, by dialect |
| `sqlschema.OnDelete(a)`, `Annotation.OnDelete` | `edge.OnDelete(a)` |
| `sqlschema.CascadeAction`, `Cascade`, `SetNull`, `Restrict`, `NoAction` | `edge.Action`, `edge.Cascade`, `edge.SetNull`, `edge.Restrict`, `edge.NoAction` |
| `sqlschema.SetDefault` | not supported |
| `sqlschema.OnUpdate(a)`, `Annotation.OnUpdate` | not supported: velox writes no `ON UPDATE` |
| `sqlschema.Charset(c)`, `Annotation.Charset` | not supported |
| `sqlschema.Skip()`, `Annotation.Skip` | not supported |
| `sqlschema.IncrementStart(n)`, `Annotation.IncrementStart` | not supported |
| `sqlschema.WithComments(b)`, `Annotation.WithComments` | not needed: velox writes a comment where the dialect has them |
| `sqlschema.StorageParams(p)`, `IndexAnnotation.StorageParams` | not supported |
| `sqlschema.Prefix(n)`, `sqlschema.PrefixColumn(name, n)`, `IndexAnnotation.Prefix`, `PrefixColumns` | not supported: velox does not model a MySQL index prefix |
| `sqlschema.IncludeColumns(...)`, `IndexAnnotation.IncludeColumns` | not supported: velox does not model a covering index |

## Not supported

Generator:

- **Templates, graph hooks, `WithGenerator`, `WithHeader`, feature flags and
  dependency injection.** The generated package is velox-gen's alone, so
  `velox-gen -check` proves it current from the schema
  ([ADR 0034](decisions/0034-schema-declares-behaviour.md),
  [0097](decisions/0097-generate-package.md)). A hook that sorts each
  entity's fields has no form: declare the fields in the order wanted.
- **veloxe's `velox init`, `watch` and `velox.yaml`.** velox-gen has `generate`,
  `verify`, `describe`, `migrate` and `version`.
- **An `AGENTS.md` written into the generated package.**
  [for-agents.md](for-agents.md) and `skills/velox` are in the repository.

Schema:

- **`field.Any`, `field.Deprecated`, `field.ValueScanner`, an edge's
  `StructTag` and `sqlschema.View`.** Not built; nothing asked for them.
- **`FeatureCheckBounds`.** `Min`, `Max` and `Range` make no `CHECK`.
  Declare one with `sqlschema.Checks`.
- **`mixin.AuditHook`, `AnnotateFields` and `AnnotateEdges`.** `db.Record`
  reports each change with the row before and after
  ([ADR 0094](decisions/0094-change-recording.md)).
- **Composite keys and `field.ID`.** An edge schema is keyed by its own
  `id`. A bidirectional edge through an edge schema, and a Read policy,
  interceptors or soft delete on an edge schema, are refused too
  ([ADR 0109](decisions/0109-edge-schemas.md)).

Queries and runtime:

- **An exported SQL builder.** `sql.Selector` and `sqlgraph` are not
  public, so no code can drop a policy from a statement
  ([ADR 0069](decisions/0069-expr.md)).
- **Back-references on load** ([ADR 0101](decisions/0101-window-terms-soft-delete-options.md)).
- **Named eager loads (`WithNamedX`).** One `With` per relation.
- **`OpCreate`, `OpUpdateOne` and `OpDeleteOne`.** velox keeps one name per
  op ([ADR 0099](decisions/0099-keep-defaults-and-set-ptr.md)).
- **`AllX` and the other panicking variants, and `Clone`.** Builders are
  values, and every call returns its error.
- **`querylanguage.P` (entql).** `velox.FieldEQ` and its kin, and package
  `untyped`, name columns by string.
- **A per-client table schema at run time.** `sqlschema.SchemaTable` and
  `sqlschema.Schema` are fixed at generation.
- **veloxe's viewer, role and tenant rules** (`WithViewer`, `HasRole`,
  `TenantRule`, `DenyIfNoViewer`). Tenancy is a mixin over package `scope`
  ([ADR 0038](decisions/0038-tenancy-is-a-mixin.md)).
- **`privacy.NewPolicies` and `Policies`.** velox composes a schema's and
  its mixins' policies itself.
- **Auto-migration at deploy** ([ADR 0006](decisions/0006-own-migrate-and-atlas.md)).
  Tests and development databases have `veloxtest.Open` and
  `migrate.Create` ([ADR 0113](decisions/0113-test-schema-create.md)).

GraphQL:

- **A chosen SDL path.** velox-graphql writes the SDL into the generated
  package.
- **Ordering by a to-many edge's count**
  ([ADR 0087](decisions/0087-graphql-veloxe-compat.md)).
- **`AddPredicates`**, and **`first`+`before`, `last`+`after`,
  `after`+`before`**, as above.
- **Federation, subscriptions, interfaces and unions.** Not built; no
  current application needs them.
- **Input validation annotations** (`CreateInputValidate` and the like),
  `WhereInputFields`, the smaller annotations (`Unbind`, `MapsTo`,
  `SkipField` and the rest) and the generator's hooks (`WithSchemaHook`,
  `WithTemplates`, `WithGQLGenConfig`). Generated inputs run the schema's
  validators.
- **`FeatureWhereInputAll`.** Where inputs are opt in, field by field,
  with `graphql.WhereOps` or `graphql.WhereInput`.
