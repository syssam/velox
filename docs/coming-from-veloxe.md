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
| `WithSchemaPath`, `WithSchemaSplitMode(SchemaSplitPerEntity)` | not offered: one `velox.graphql` in the generated package |

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
| `client.Intercept(...)`, `intercept.Func` | `velox.WithFilter(func(ctx, entity string, op velox.Op) (velox.Predicate, error))`, which returns `velox.Allow()` for no condition ([guide](guide.md#hooks-and-filters-for-every-entity)) |
| `client.Use(hooks...)`, `m.Type()` | `velox.WithHooksAll(hooks...)`, `m.Entity()` |
| a privacy rule's `WhereP(func(*sql.Selector))` | `Filter.Where(velox.Predicate)` |
| `SkipDefault(user.FieldUpdatedAt)` | `KeepDefaults(U.UpdatedAt)` |
| `SetNillableNickname(p)` | `Set(U.Nickname.SetPtr(p))` |
| the generated package's `IsConstraintError(err)`, `IsValidationError`, `IsNotSingular`, `IsNotFound` | `velox.IsConstraintError(err)`, `IsValidationError`, `IsNotSingular`, `IsNotFound` |
| `OpCreate` | `OpInsert` |
| `OpUpdate \| OpUpdateOne` | `OpUpdate` |
| `OpDelete \| OpDeleteOne` | `OpDelete` |
| an op tested as a bit set | an op compared with `==`; `velox.On(h, ops...)` runs `h` for the ops listed |
| `tx.OnCommit(func(next velox.Committer) velox.Committer {…})` | `velox.AfterCommit(tx, fn)`: `fn` runs once the outermost transaction has committed |
| `IDs(ctx)` | `velox.IDs[int64](ctx, q)` |
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

**An empty patch writes nothing.** An update whose setters are all
`SetPtr(nil)` sends no statement and returns 0. No hook runs, and no
`UpdateDefault` fires, so `updated_at` stays. veloxe's `UpdateOne` with
nothing set still ran its hooks and set `updated_at`
([ADR 0099](decisions/0099-keep-defaults-and-set-ptr.md)).

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
  them ([ADR 0089](decisions/0089-veloxe-privacy-shape.md)).
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

- **An SDL split per entity, and a chosen SDL path.** velox-graphql writes
  one `velox.graphql` into the generated package.
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
