# Changelog

All notable changes are listed here, in the shape of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

velox is before 1.0, and its API can still change. Two rules hold already:

- A change to the generated code replaces `velox.GeneratedCodeIsVersionN` (and
  `veloxgql.GeneratedCodeIsVersionN`) with the next number, in a minor release,
  with a line here. Regenerate with the new `velox-gen`: code that names the old
  constant does not compile against the new runtime.
- `schema.json` and `velox.json` are both version 1. A newer velox reads every
  older version and refuses a newer one, with an error that names both versions.

After 1.0, a deprecated identifier carries a `// Deprecated:` comment until the
next major version. Before 1.0, nothing is deprecated: it changes or goes.

## Unreleased

1.0.0 will be the first release from this repository.

### Added

- A policy function, privacy rule or interceptor Filter is not applied
  to a statement made under its own ctx, so one that reads its own
  entity ends instead of calling itself without end. One that reads
  joins the statement's transaction with `velox.TxFrom(ctx, db)`, as an
  ent rule uses `ent.TxFromContext` (ADR 0135).
- `velox.IsZero(p)` reports the zero `Pred` or `Predicate`: a helper with no
  condition returns it, and its caller tests it. A function, not a method,
  so a package pays only where it calls it (ADR 0137).
- `velox.HasColumn(entity, column)` lets a `WithFilter` filter narrow every
  entity having a column, such as `deleted_at`, without a list (ADR 0137).
- `velox.TxFrom(ctx, db)` returns the transaction ctx belongs to, with db's
  own policy bypass, so a helper given db and ctx joins it (ADR 0137).
- `softdelete.By(column, key)` sets a column from a `scope.Key` in a soft
  delete's UPDATE, and `softdelete.Assigns` joins several (ADR 0137).

- `Term[E].Equal` reports whether two terms are the same: written alike,
  with equal bound values, their names aside. A report compares an
  ordering's `Term()` with its bucket by it (ADR 0127).
- `Term[E].Decimal` marks an expression as holding decimals: on SQLite
  its `Sum()` and `Avg()` are rounded to 15 significant digits as a
  decimal column's are, so `Calc("? * ?", X.Qty, X.Price).Decimal().Sum()`
  is exact there (ADR 0131).
- A `WithFilter` filter is given the DB its statement runs on, as its
  second parameter, `func(ctx, db *velox.DB, entity string, op velox.Op)`,
  the transaction's inside `Tx`, so a filter that reads gets the same
  answer inside a transaction and outside. A filter is not applied to
  its own reads, which ends the recursion
  ([ADR 0132](docs/decisions/0132-filter-reads-through-the-statements-db.md)).
- `velox.WithTxDefaults(opts...)` gives every outermost `Tx` on the DB
  options that the call's own override, such as a retry and a timeout. A
  savepoint applies none, and gqlgenx's mutation transaction never retries
  ([ADR 0133](docs/decisions/0133-tx-defaults-and-none.md)).
- `velox.None()`, the condition that matches no row, `1 = 0` on every
  dialect, beside `velox.Allow()`.
- `velox.ErrCommitUnknown` is matched by a `Tx`'s error when its COMMIT
  failed and the error does not say the transaction rolled back, so a
  caller that retries on its own can stop before applying a committed
  transaction twice. PostgreSQL's ROLLBACK answer to an aborted
  transaction's COMMIT (pgx's `ErrTxCommitRollback`), a deferred
  constraint, a serialization failure or deadlock, MySQL's 3101 and
  SQLite's BUSY and LOCKED are rollbacks and do not match; MySQL's lock
  wait timeout at COMMIT matches, and `TxRetrySerializable` no longer
  retries it
  ([ADR 0134](docs/decisions/0134-commit-unknown.md)).

- velox-graphql: `sdlfiles=entity` writes the SDL as `velox.graphql`, with
  Query and the vocabulary, and one `velox_<label>.graphql` per entity.
  gqlc makes a package of each file, so `Query.salesOrderItems` and
  `SalesOrder.items` no longer both declare `SalesOrderItemsArgs`. It is
  the default under compat=veloxe, with veloxe's names
  (`velox_salesorder.graphql`); `sdlfiles=one` keeps one file.
  `entities=A+B` writes those entities' SDL alone, with each where input
  theirs nest, for an application that serves part of its schema (ADR
  0125).
- velox-graphql: `apptypes=A+B` names the application's types the SDL of
  `entities=` may name; a field naming another type velox-graphql does not
  declare is left out, and an input naming one is refused. Under
  `sdlfiles=entity` the inputs written of an entity not chosen go in
  `velox.graphql`, so no gqlc group holds inputs alone, whose `Bindings()`
  gqlc's `NewSchema` registers itself (ADR 0130).
- reports: `velox.ColumnByName[E](name)` is a column's Term by its name;
  `velox.DateTrunc[E](unit, t, opts...)` truncates a time to a year,
  quarter, month, ISO week, day or hour, `InZone(name)` and
  `FiscalYear(start)`, as an instant on every dialect;
  `velox.Rollup` and `velox.Cube` are keys of `GroupBy` (PostgreSQL; MySQL
  `WITH ROLLUP` only); `Order.Term()`, `Order.Descending()`,
  `Term.Column()` and `RawRow.Columns()` read an ordering and a row back. A
  Term that is a column orders as its handle does, so After follows it
  (ADR 0127).
- veloxgql: `Service(ctx, db)` and `ServiceWithLimits(ctx, db, l)` let a
  service page through generated code outside a GraphQL request:
  `Paginate`, edge connections, `EdgeOne` and `EdgeList` read every column,
  each edge alone. Without it, or an adapter, they still fail with
  `ErrNoSelectionSource`, whose text now names `Service` (ADR 0121).
- graphqlgo: `ScalarsOnly(names...)` binds only the named scalars of Time,
  UUID, Bytes and Uint64, for a schema that does not declare all four;
  graphql-go's `NewSchema` refuses `Scalars()` there.
- velox-graphql: under compat=veloxe, a `graphql.OrderField` or
  `graphql.OrderUnguarded` dropped from a field of a type veloxe did not
  order (a decimal, a JSON field, a `Text`) is a warning naming the entity,
  the field and the reason.
- delete: `DeleteQuery.One(ctx)` removes exactly one row, ent's
  `DeleteOneID(id).Exec` as `Delete[E]().Where(X.ID.EQ(id)).One(ctx)`: no
  row removed wraps `ErrNotFound`, a hidden or soft-deleted row included;
  by key it is one DELETE, otherwise a transaction that rolls back with
  `ErrNotSingular` on more than one row (ADR 0122).
- sqlite: the option `WithSQLiteLockingReads()` runs `ForUpdate` and
  `ForShare` on SQLite without the lock clause, where they are refused by
  default; the other locking-read checks still hold. It does not change
  how `Tx` begins: `_txlock=immediate` in the DSN gives a row lock's wait
  (ADR 0123).
- insert: `UpdateSet(setters...)` gives an upsert's update branch `Set`,
  `SetNull`, `Add`, `Sub` and `SetExpr`, and `velox.Excluded(c)` names the
  inserted row in it; `UpdateWhere`, `OnConflictWhere` (a partial index) and
  `OnConstraint` (PostgreSQL). MySQL refuses the last three before sending
  (ADR 0111).
- hooks: `Mutation.Columns`, `Cleared` and `Edges`; `If`, `Unless`, `HasOp`,
  `HasColumns`, `HasCleared` and `Reject`, for schema and run-time hooks
  (ADR 0111).
- tx: `velox.BeforeCommit(tx, fn)` runs before COMMIT, and its error rolls
  back; `velox.AfterRollback(tx, fn)` runs after a rollback, a savepoint's
  included (ADR 0111).

- A JSON handle's `Append(v)` appends a slice's elements to the column's
  array, as ent's `AppendX` does (`velox.AssignAppend`); `Len(keys...)` is
  the length of an array, for conditions and orderings. `JSONText` has
  `Contains`, `HasPrefix` and `HasSuffix`, with case on every dialect;
  `JSONNumber` and `JSONText` have `Asc` and `Desc`; the three path kinds
  have `Term()` for `Select` (ADR 0115).
- `velox.Scan[T](ctx, q)` and `velox.ScanOne[T]` read a projection into a
  struct by a `velox:"name"` tag or the field's name, decoding JSON columns,
  and `Term.As(name)` names a result column. A JSON column may be a result
  column of `Select` (ADR 0116).
- migrate: `Create(ctx, db, schema)` makes a schema's tables in a new
  database, for tests and development: it plans from empty, writes no
  history, does nothing when the database already holds the schema, and
  refuses part of it or a difference with Code `create` (ADR 0113).
- veloxtest: `Open(t, driver, dsn, schemas, opts...)` gives a test a
  database with the schema's tables, as ent's `enttest.Open` does, and
  `Schema(dialect, schemas...)` builds velox-gen's tables in the process
  (ADR 0113). `Budget` and `NoNPlusOne` fail a test whose context ran a
  read of one shape too often (ADR 0117).
- migrate: an `unindexed-fk` review finding names a foreign key that no
  index leads once a plan has run, on PostgreSQL and SQLite;
  `-allow unindexed-fk=t.c` takes it out (ADR 0114).
- velox: `WithStatementCounting` and `CountStatements` count a context's
  statements by shape; `StatementCounter.Check` with a `StatementBudget`
  reports an N+1 as a `*BudgetError` (ADR 0117).
- velox: a relation's `Count()`, `Sum`, `Avg`, `Min` and `Max` are terms of
  each row, ent's `ByPostsCount` and `ByPosts(sql.OrderBySum(...))`: order by
  one, select it or compare it in an `Expr`. The target's interceptors,
  filters and Read policy apply inside, so a row the reader may not see is
  not counted, and `After` pages over them (decision 0112). Untyped:
  `Table.RelAgg`.
- velox-graphql: `graphql.OrderField("POSTS_COUNT")` on a to-many edge orders
  the owner's connections by the edge's count, as entgql's, counting only
  the nodes the reader may see; cursors page over it, and a request that
  orders by it costs what a `has<Edge>With` costs (decision 0112).
  `gqlgraph.Column.Count` is new; `veloxgql.GeneratedCodeIsVersion27` stays,
  as code generated before uses nothing that changed.
- velox-gen: `migrate rebase <file>...` moves a branch's migration files to
  the versions after the directory's others, as they are, writes the schema
  package's tables as `schema.json` and rebuilds `velox.sum`, after checking
  that the files make it (`migrate.Rebase`). It replaces deleting the file
  and running `diff` again when two branches took one version.
- migrate: `WithDryRun` makes `Up` check the pending files and change
  nothing: on SQLite it applies them to a copy of the database, on
  PostgreSQL and MySQL it runs the apply-time walk of each file read-only.
  `velox-gen migrate apply -dry-run` uses it, so a dry run refuses what the
  apply would; it printed the files and checked nothing before.
- migrate: `SchemaAt` is what the files up to a version make, read from an
  empty scratch database. `velox-gen migrate drift` with files pending
  compares the database with it, on a temporary SQLite file or
  `-scratch-db`; it refused before.
- migrate: `-- velox:statement begin` and `-- velox:statement end` lines
  mark one statement the split does not cut, so a SQLite or MySQL trigger,
  a MySQL routine, or a PostgreSQL `BEGIN ATOMIC` body can be in a file.
  docs/migrations-operations.md, "A trigger or a routine in a file".
- migrate: `diff`'s refusal of a dropped column, when a column of its type
  is added to the same table, ends with the `-rename-column table.old=new`
  that plans the two as a rename.
- migrate: two Review findings. `drop-identity`: a column loses its
  identity, so an insert that leaves it out gets no value. `zero-default`:
  a NOT NULL column added to a table that exists with `''`, `0` or `false`
  as its default, as velox-gen gives a string, bool or number field with no
  Default (ADR 0090), says what the existing rows get.
- Typed hook accessors on every column handle: `Value(m)`, `Assign(m, v)`
  and `Fill(m, v)` on a `*Mutation`, and `Get(r)` and `Put(r, v)` on a
  `Row`, so a run-time hook writes `T.Title.Value(m)` and
  `T.Title.Assign(m, v)` where it wrote `m.Value("title")` and
  `m.Set("title", v)`, and a value of the wrong type does not compile. The
  Null handles add `ValuePtr`, `GetPtr`, `AssignNull` and `PutNull`. A
  handle of another entity is a misuse, which fails the write. They are
  methods of the shared handle types, not generated per entity; an entity
  package's export data grows by about 1 KB.
- A test fails when the golden generated code changes shape and
  `GeneratedCodeIsVersionN` does not. `internal/codegen/testdata/golden/shape.txt`
  records the version with a hash of the code without its comments, and
  `-update` rewrites it only with a new version.
- Edge schemas, as ent's `edge.Through`: a many-to-many edge names a schema
  whose rows are its links, with an ID, fields and two Required to-one key
  edges. velox-gen generates it as any entity, adds the through edge and a
  unique index over the pair of keys, and writes no join table. `Rel.Add`
  inserts its rows with their defaults, keeps a link that exists, and fails
  before any statement when a field is required and has no default. An edge
  schema cannot have a Read or Query policy, Interceptors or
  `softdelete.Mixin` (rule 70). velox-gen warns of an edge
  schema whose keys declare no `OnDelete`. Regenerate:
  `velox.GeneratedCodeIsVersion26` replaces Version25, as `EdgeThrough`
  takes the columns Add cannot fill. `loadjson.Version` is 9, for
  `Edge.Through`.
- The plugin model has `Entity.EdgeSchema`, `Edge.Through` and
  `Edge.Unfilled`; an M2M edge's `Join` names its edge schema. velox-graphql
  gives an edge schema a type like any schema's. An M2M edge whose edge
  schema has a column Add cannot fill has no create or add input, and
  velox-graphql warns of it.
- velox-graphql takes `engine=graphql-go` (ADR 0107), and so does
  `graphqlgen.Plugin(opts)`: velox.graphql and the Go are for
  github.com/syssam/graphql-go's generator gqlc, which binds every field but
  the root fields to the generated code by autobind. Every type of the SDL
  but Query carries `@goModel` naming its Go type, each scalar the Go type
  entities hold; an input field whose name graphql-go would not derive has
  a `graphql` tag, and an optional list in a mutation input is a slice. A
  field gqlgen reads through a getter, a JSON field or one of the
  application's Go type, carries `@goField(forceResolver: true)`, and its
  resolver returns the getter's value or the field. A field or edge renamed
  by `graphql.FieldName`, a non-null bytes field and a key not named ID are
  refused, all of them in one run, naming the schema and the field, as gqlc
  does not read `@goField(name:)` yet. `engine=gqlgen` is the default, and
  its output is unchanged. `graphqlgo.Scalars()` writes a nil `[]byte` as
  null, as gqlgen does, and binds Int to int8, int16, uint8, uint16 and
  uint32, and Uint64 to uint, as gqlgen.yml's models do. `veloxgql.GeneratedCodeIsVersion27` stays: code generated
  before runs as it did, and the new output needs nothing new of the
  runtime.
- Under velox-graphql's `compat=veloxe` (`gqlgraph.Type.Veloxe`), a
  connection behaves for clients as veloxe's did (ADR 0108). It takes a
  cursor veloxe wrote, so a list a client paged before the deploy pages
  on. The cursor's key is the key, and its values are the current order's;
  a cursor with another number of values fails as one taken under another
  order. Connections still write velox's cursors. An order of one term
  breaks ties by the key in that term's direction. A page after a cursor
  has `hasPreviousPage`, and a page before a cursor `hasNextPage`. A
  foreign key's predicates (`customerId`, `customerIdIn`,
  `customerIdIsNil`, …) are the column's alone, with no EXISTS on the
  target. Without compat, a veloxe cursor fails with the same error as
  before, when the connection is built rather than when the argument is
  decoded. Generated code does not change.
- Package `contrib/graphql/veloxgql/graphqlgo` serves velox-graphql's
  generated code under github.com/syssam/graphql-go (ADR 0106), with
  graphql-go's own option types: `graphqlgo.With(db)` is the executor
  option that puts the DB and graphql-go's selection source in each
  operation's context, so generated code reads only the selected columns
  and batches its edges, as under gqlgen; a mutation runs in no
  transaction, and a resolver opens one where it needs it; `Scalars`
  binds `Time`, `UUID`, `Bytes` and `Uint64`; `ErrorPresenter` shows
  `veloxgql.Present`'s codes. The
  limits of a request are graphql-go's own options; velox's page, list
  and filter bounds are fixed at `veloxgql.DefaultLimits()`. The error a
  subscription's source function returns reaches a client as its text, as
  graphql-go drops the error before any presenter sees it.
  contrib/graphql requires graphql-go v0.0.0-20261029022150-3e0050be7462.
- Package `generate` runs velox-gen's generate from Go, as ent's
  `entc.Generate` and veloxe's `compiler.Generate` did (ADR 0097):
  `generate.Run(ctx, dir, generate.Config{Schema, Out, Package, Plugins,
  Check, Env, Stdout, Stderr})`, with `ErrStale` for a stale
  check and `ErrInterrupted` for a signal during the write. velox-gen calls
  it, and its output is unchanged. A `Plugin` with `Run` answers in the
  process; `contrib/graphql/graphqlgen.Plugin(opts)` is velox-graphql so,
  with the tool's output byte for byte.
- `Query.Distinct()` (ADR 0102): `SELECT DISTINCT` for a projection and for
  `velox.Values` and `velox.IDs`. A read of entities, `Count`, `Exists` or
  an aggregate with it fails before anything is sent, and so does an
  ordering by a term it does not read.
- `InsertQuery.UpdateAll()` (ADR 0102), ent's `UpdateNewValues`: an upsert
  that updates every column but the key, the conflict columns, `Immutable`
  and `Version` columns and skipped `SetPtr` columns. `UpdateDefault` still
  does not fire (ADR 0017).
- Window functions in `Calc` and `SelectRaw` (ADR 0101): `COUNT(*) OVER ()`,
  `ROW_NUMBER() OVER (PARTITION BY ? ORDER BY ?)` and the like, as a result
  column or an ordering. Beside `GroupBy` and `Limit`, `COUNT(*) OVER ()`
  counts every group. A subquery or a refused function inside `OVER` stays
  refused, and a window function in an `Expr` (`Where`, `Having`), in
  `GroupBy`, under an aggregate or in `SetExpr` fails the statement before
  anything is sent.
- `softdelete.Mixin{Assign: ...}` (ADR 0101) sets more columns, such as
  `deleted_by_id`, from the delete's context, in the soft delete's one
  UPDATE; an error from it fails the delete. `Reads: softdelete.Unfiltered`
  leaves deleted rows visible to reads and updates, while a delete stays
  soft and marks live rows only. The zero `Mixin` behaves as before.
- `velox.AfterCommit(tx, fn)` (ADR 0100), as veloxe's `tx.OnCommit`:
  queues `fn` on the transaction, to run in order once the outermost
  transaction has committed, on the `Tx` context without its cancellation.
  A rollback, a failed COMMIT or a retried attempt runs none; a nested `Tx`
  that rolls back drops what was queued while it ran. Outside a transaction
  and on a `UseTx` DB it returns an error.
- `velox.Values(ctx, q, I.Number.Key())` and `velox.IDs[int64](ctx, q)`
  (ADR 0100) read one column, or the key, of the rows `q` reads, under the
  Read policy, the scope, the interceptors, `WithFilter` and the Limit rule.
- `velox.WithLog(logger, velox.LogArgs())` (ADR 0100) logs each statement
  at Debug with its SQL, duration, rows and error, and its arguments with
  `LogArgs`. A Sensitive column's argument is always `"<sensitive>"`.
  velox cannot tie some arguments to a column, such as an `Expr`'s or raw
  SQL's, and those are `"<hidden>"`. A driver's error text, which can echo a
  value, is printed only with `LogArgs` and every argument shown; otherwise
  the error is redacted to its constraint, velox's own error by its fixed
  text (`ErrNotFound`…, a `ValidationError`'s field), or its type, and the
  driver's code.
- Hooks and row filters for every entity (ADR 0098), as ent's `client.Use`
  and `client.Intercept`: `velox.WithHooksAll(hooks...)` runs hooks on every
  entity's writes, before the schema's and `WithHooks[E]`'s, and
  `Mutation.Entity()` names the entity. `velox.WithFilter(f)` adds a row
  filter, `func(ctx, entity string, op velox.Op) (velox.Predicate, error)`,
  given the statement's op (`OpSelect` for related entities, `OpDelete` for
  a soft delete), with an
  interceptor's reach: reads, writes, loads, joins, `Has`/`HasWith`, counts
  and aggregates. `WithoutPolicy` does not lift it; an error, the zero
  `Predicate` or an unknown column fails the statement, and
  `velox.Allow()` restricts nothing.
- `SetPtr(p)` on every column handle with `Set` (ADR 0099), as ent's
  `SetNillableX`: a nil `p` gives a setter the builders skip, a non-nil one
  sets `*p`; on a nullable column a nil `p` skips too, and `SetNull()`
  writes NULL. In an upsert, a column `Update` names whose only setter was
  skipped is not updated; with none left, `ConflictClause.Ignore` keeps
  the row by a no-op update of the conflict key and reads its key back. An
  update whose every setter was skipped sends nothing and returns 0.
  `UpdateQuery.KeepDefaults(cols...)`,
  `SaveQuery.KeepDefaults(cols...)` and `untyped.TableUpdate.KeepDefaults`
  stop the named columns' `UpdateDefault` from firing, as veloxe's
  `SkipDefault`; a column without one fails the statement.
  `velox.IsConstraintError`, `IsValidationError` and `IsNotSingular`, as
  ent's, safe with a typed nil.
- A `field.Other` field in a decimal column (its `SchemaType` names
  `numeric` or `decimal`, on MySQL also `dec` or `fixed`, for every dialect
  it lists), such as a `decimal.Decimal`, has a `NumberCol` (`NullNumberCol`
  when Nillable) and gains `Sum`, `Avg`, `Add` and `Sub`, and `velox.Sum`,
  `Avg`, `SumBy`… take it (ADR 0096). The value binds and scans through the
  type's own `driver.Valuer` and `sql.Scanner`.
- Lean inserts (ADR 0093): an INSERT leaves out a column that every row
  leaves empty: a nil nullable column, or the zero value in a column whose
  database default the schema declares (`field.SchemaDefault`,
  `sqlschema.Default`, `DefaultExpr`, `DefaultExprs`). The database fills it,
  and velox reads it back into the entity: `RETURNING` on PostgreSQL and
  SQLite, a SELECT by key on MySQL. InsertAll returns every row's generated
  key. On PostgreSQL and MySQL it stays one statement (per WithMaxParams
  chunk): a cell a row leaves to its default is the `DEFAULT` keyword; on
  SQLite, which has no `DEFAULT` in `VALUES`, it splits its rows into runs
  that leave the same columns out. An insert that leaves every column to
  the database sends `DEFAULT VALUES` (MySQL: `() VALUES ()`). An upsert of
  several rows whose update branch has a condition (a policy's, an
  interceptor's or a Rewrite hook's) places the rows `RETURNING` gives back
  by their conflict columns, never by position, and fails, rolled back, on
  a row it cannot place; on a DB with a Rewrite hook such an InsertAll runs
  in a transaction. On MySQL a single-row upsert reads the columns it left
  to the database by its conflict values, in one transaction with it.
  Regenerate: `velox.GeneratedCodeIsVersion24` replaces Version23, and
  descriptors carry `ColumnDescriptor.DBDefault`. The nil nullable column
  left out is a change of behaviour, listed under Changed.
- An insert's zero value left to a declared database default skips the
  field's validators (ADR 0093), so an enum with `SchemaDefault` inserts
  with its zero and stores the default; a column an upsert's conflict
  clause writes is validated as before. velox-gen warns of a field that is
  not Nillable whose `SchemaDefault` or `sqlschema.Default` is a non-zero
  literal (`true`, `5`, `'x'`): the zero value cannot be stored through
  Insert.
- `velox.SetExpr(col, text, args...)` (ADR 0095): an UPDATE sets a column
  to an SQL expression, `SET views = (views * 2)`, with `Expr`'s guarded
  text and arguments, and keeps the Write policy, the scope and the
  interceptors. `AssignExpr` is its `AssignOp`; `untyped.SetExpr` is its
  untyped form. `docs/sql.md` lists every way to write SQL in velox and
  which keep the policies.
- Change recording (ADR 0094): `rdb := db.Record(sink)` returns a handle
  whose writes report each row they change, bulk updates and deletes,
  upserts, soft deletes and join rows included, as a `velox.Change` with the
  row before and after the write. `velox.Recorder` collects them and drops
  what a rolled-back transaction recorded. Read a column typed with
  `I.Total.Old(c)` and `I.Total.New(c)`, or any entity's rows with
  `c.OldRow()`, `c.NewRow()` and `c.Diff()`. Old values are read locked in
  the write's transaction, or on PostgreSQL 18 returned by the write itself.
  `velox.Diff` compares two rows of an entity. The sink,
  `Record(ctx, tx *DB, changes []Change)`, gets all of a write's Changes in
  one call, inside the write's transaction before its COMMIT (velox opens
  one outside a transaction, a single statement's too), so its error rolls
  the write back; `tx` is a handle on that transaction that does not
  record, so audit rows written with `tx.InsertAll` commit with the write.
  A column an `UpdateDefault` fills (`updated_at`) is ignored by default;
  `velox.RecordIgnore(cols...)` and `velox.RecordKeep(cols...)` take typed
  columns, one call per entity. A `Null*` handle's `OldPtr(c)` and
  `NewPtr(c)` tell NULL (nil) from an empty value. The times in recorded
  rows are in UTC, so an insert's new row and a later read marshal alike;
  the caller's entity keeps its zone. `Diff` counts a NaN that stays NaN
  as unchanged. On MySQL an update that assigns the key fails, rolled
  back, when its read by the new keys finds fewer rows than it locked.
- veloxe's privacy policies (ADR 0089): `velox.Policy` has `Query` and
  `Mutation` rule lists, and package `privacy` has veloxe's names, with
  `privacy.Policy` an alias of `velox.Policy`, so a veloxe schema's
  `return privacy.Policy{Query: privacy.QueryPolicy{...}}` compiles. Skip goes
  on, Allow ends a list, any other error denies; Query's conditions apply to
  reads only, Mutation's rules to inserted rows, updates and deletes;
  `privacy.DecisionContext` lifts the lists. velox-gen accepts
  `Policy() privacy.Policy`, and for such a schema writes
  `<out>/privacy/privacy.go`, veloxe's generated privacy package, as aliases
  of velox's.
- A `field.Other` field's handle is `velox.OtherCol` (`NumberCol` in a
  decimal column, ADR 0096): GT, GTE, LT, LTE, Between, Min and Max,
  compared by its SQL type. velox-graphql's WhereOps
  gives such a field equality and comparison predicates (ADR 0087).
- velox-graphql's option `compat=veloxe` makes the API veloxe's generator
  made: names from the schema name (`customerId`, `imageUrl`), foreign keys in
  the type as IDs beside their edges, and their where predicates named from
  the field. `graphql.Type` on a field is the field's GraphQL type, as veloxe
  took it: an enum field's enum name, or a scalar or list the application
  declares (`Map`, `[ID!]`) (ADR 0087).
- Under `compat=veloxe` the rest of veloxe's surface follows: every orderable
  field in the order enum, Optional fields nullable in the type, Required
  unique edges non-null, a root Query field for every type (a list for a
  `QueryField` without `RelayConnection`, which `graphql.RelayConnection()`
  now records), nullable connection edges and nodes, only `Cursor`, `Time`
  and `Bytes` declared, equality-only defaults on keys and Other fields,
  explicit WhereOps over WhereInput, no directives on inputs, a json field of
  a named Go type typed by its name, and silence where veloxe ignored an
  annotation. The option `where=whitelist` filters only fields with
  `WhereInput` (ADR 0087).
- Under velox-graphql's `compat=veloxe`, every type with an ID implements
  `Node`, the root query has `node(id:)` and `nodes(ids:)`, and the generated
  `ResolveNode` and `ResolveNodes` answer them through each type's Read
  policy; a where input means what veloxe's did: an empty `In`, a false
  `IsNil` and an empty `or` add nothing, `not: {}` is an error, and no width
  or node cap applies (`gqlgraph.Type.Veloxe`) (ADR 0088).
- Two enum fields whose types get one name (`Asset.depreciation_method`,
  `AssetDepreciation.method`) and hold the same values share one Go type and
  one GraphQL enum, as veloxe did (ADR 0086).
- A one-to-one pair to its own schema may bind its key field on the assoc
  edge (`edge.To("reversed_by", T.Type).Field("reversed_by_id").Unique()` with
  its `From` inverse), as veloxe schemas do; each edge reads from its own end
  (ADR 0085).
- Two tables may share a check or not-null constraint name, as PostgreSQL
  allows; MySQL, which keeps check names per database, refuses it in its
  `Tables` (ADR 0084).
- Each index is named `<table>_<name>` unless its name starts with
  `<table>_`, so a mixin's StorageKey is one name per table, and a long name
  is capped with veloxe's FNV-1 128 suffix, not ent's MD5. A veloxe
  application keeps its database's index names (ADR 0083, 0090).
- `sqlschema.NonUniqueFK` on an edge: its key is not unique, and a to-one
  edge to its own schema with no inverse is many-to-one (ADR 0090).
- `field.JSON` of an unnamed struct, written inline (ADR 0090).
- `field.Bool("active").Default(true)`, and any `Default` or `DefaultFunc` on a
  bool or number, is accepted as in ent, with ent's `NOT NULL DEFAULT` column.
  An insert now fills defaults before its setters and hooks, so
  `Insert(&x).Set(Xs.Active.Set(false))`, a GraphQL create input's
  `active: false` and a hook's `Mutation.Set` store the zero; a struct's zero
  field with no setter still takes the default. Hooks see defaults filled, so
  `Mutation.Fill` of a defaulted column does nothing. `ColumnDescriptor.ZeroInvalid`
  is removed. Regenerate: `velox.GeneratedCodeIsVersion23` replaces Version22
  (decision 0082).
- `HasEdge`, `HasEdgeWith` and `FieldInSelect` find their target in the
  generated package's `velox.EntityTable` when the statement runs, so a rule in
  a shared package can name an entity or a relation by a value from
  configuration, and `FieldInSelect` accepts an entity's table as well as its
  name. velox-gen checks the names it can read and warns about the others,
  where it refused them. `velox.Linked` and `Descriptor.Links` are removed and
  `velox.LinkEntity` takes the table. Regenerate:
  `velox.GeneratedCodeIsVersion22` replaces Version21 (decision 0081).
- `Has`, `HasSome`, `HasEvery` and `IsEmpty` on a JSON handle test the elements
  of a column that is an array, alike on PostgreSQL, MySQL and SQLite
  (`untyped.PredJSONHas` and `PredJSONEmpty` are the untyped forms).
  velox-graphql ports the old velox's `OpsJSONArray`: a `[]string`, `[]int`,
  `[]int64`, `[]float64` or `[]bool` field offers `<field>Has`, `HasSome`,
  `HasEvery` and `IsEmpty`, each charged as a scan. Regenerate:
  `veloxgql.GeneratedCodeIsVersion24` replaces Version23 (decision 0080).
- `velox.FieldInSelect(column, entity, key, ps...)` is a subquery on another
  entity by name, for a schema's Policy, Hooks and Interceptors: the entity's
  `column` is among the `key` of the `entity` rows that meet `ps`. The target's
  Read policy applies inside it; a wrong name or type is an error, not a dropped
  arm. velox-gen links the entity (`Descriptor.Links`). Regenerate:
  `velox.GeneratedCodeIsVersion21` replaces Version20 (decision 0079).
- A number whose checks refuse 0 may have a non-zero `Default` or
  `DefaultFunc`: `field.Int("seats").Positive().Default(100)`. `Positive`,
  `Negative`, a `Min` above 0, a `Max` below 0 and a `Range` that excludes 0
  count; a bool is still refused. velox-gen writes the new
  `ColumnDescriptor.ZeroInvalid` for it. Regenerate:
  `velox.GeneratedCodeIsVersion20` replaces Version19 (decision 0032).
- `velox.Expr[E](text, args...)` is SQL text as a condition in `Where`: a
  function, an expression or a `CASE` the handles do not offer. It is ANDed
  with the Read policy, the scope and the interceptors, which `Raw` skips.
  Each `?` is a column handle of E or a bound value; text that holds a
  semicolon, a comment, a quote other than a plain string literal, or a
  subquery's `SELECT` is refused (ADR 0069).
- `field.Int("seats").Annotations(sqlschema.Default("100"))` sets a column
  `DEFAULT` to one SQL text on every dialect, where `SchemaDefault` takes a
  map; `DefaultExpr` and `DefaultExprs` are the old velox's too. velox-gen also refuses a MySQL `SchemaDefault` that is a literal on a
  text or blob column, which MySQL takes only as an expression
  (decision 0073).
- `graphql.Resolvers(graphql.Map("organization", "PublicOrganization"), ...)`
  on a schema adds fields to its GraphQL type that the application's resolvers
  answer, written with `@goField(forceResolver: true)`: a type of the
  application's own SDL, or a value computed per row. A selection that holds
  one reads every column, as one of an extend type's does (decision 0071).
- `index.Fields(...).Annotations(sqlschema.IndexType("GIN"))` sets an index's PostgreSQL access method
  (btree, hash, gin, gist, spgist, brin), and a GIN index may cover a JSON
  field. `migrate.Index` has a `Method`, and `Inspect` reads it instead of noting
  it (decision 0070).
- `sqlschema.DescColumns("created_at")` sorts an index key descending and
  `sqlschema.OpClassColumn("title", "gin_trgm_ops")` sets its operator class,
  both annotations of an index and PostgreSQL's. `migrate.Index` has `Desc` and `OpClass`, and `Inspect` reads
  them instead of noting them (decision 0072).
- Package `dialect/sqlschema` holds the options only a SQL database reads, with
  the old velox's names (`IndexType`, `Desc`, `OpClass`, `Default`, ...), so one
  schema can feed SQL, GraphQL and gRPC without SQL words in `index` and
  `field`. `sqlschema.Annotation{Table: "users"}`
  and `sqlschema.Table` name a schema's table as `schema.Table` does, and
  `Collation` and `Checks` lower likewise. What velox does not hold
  (`StorageParams`, `Prefix`, `IncludeColumns`, `Size`, `ColumnType`, `OnDelete`
  and the like) compiles and velox-gen refuses it, as it does an annotation on
  the wrong kind of element (decision 0075). A column default or index
  predicate that holds `;`, `--` or `/*` outside a string literal is refused.

- `velox.Schema`, `Field`, `Edge`, `Index`, `Mixin` and `Annotation` are aliases of
  package `schema`'s, as in ent and the old velox, so a schema can import one
  package for them, and package `dialect` names `Postgres`, `MySQL` and `SQLite`
  for `SchemaType` (decision 0076).

- Text predicates that ignore case: a string handle's `ContainsFold` (new,
  `LOWER(column) LIKE LOWER(pattern)`) beside `EqualFold`, `velox.FieldContainsFold`
  and `untyped.PredEqualFold` and `PredContainsFold`. In velox-graphql,
  `graphql.OpEqualFold`, `OpContainsFold`, `OpsCaseFold` and `OpsSubstring` are new,
  and `OpsString` and `OpsComparison` mean what the old velox's did: `OpsString` is
  equality, contains, has prefix, has suffix and the two fold predicates, and is
  the default of a string field's `WhereOps()`; `OpsComparison` includes equality.
  Regenerate: `veloxgql.GeneratedCodeIsVersion23` replaces Version22
  (decision 0078).

- `graphql.QueryField()` with no name is the old velox's name for the entity
  (`terms`, `graphQlLogs`, `performanceGoals`),
  `graphql.RelayConnection()` is accepted and says nothing (connections are always
  written), and `graphql.OpsNone` is no predicate. `graphql.WhereInput()`,
  `EnableWhereInputs(true)` and `WhereInputEdges(...)` make fields and edges
  filterable as `WhereOps()` and `WhereEdge()` do, and `NewDirective` and
  `Deprecated` build a `Directive` (decision 0077).

- `Query.Select`, `SelectRaw`, `GroupBy`, `Having` and `Rows` read terms in
  place of an entity: column handles as they are, SQL expressions
  (`velox.Calc`, `SelectRaw`) and aggregates (a handle's `Sum`, `Avg`, `Min`,
  `Max`, `Count` and `CountDistinct`, and `velox.CountAll`), with several
  group keys, `HAVING` and an ordering by an aggregate or an expression
  (`Term.Asc`, `Term.Desc`). It is a read of the entity, so the Read policy,
  scope and interceptors apply, which `Raw` skips. `Expr` also takes a term
  for a `?`. `OrderTerm` gained `Expr`, and `Statement` gained `Project`.
  `untyped.TableQuery` has the same methods, with `untyped.Col`, `Calc`,
  `Expr`, `Sum` and the other term makers (ADR 0074).
- `Query.Selects` is now `Query.Reads`, so that it does not sit beside
  `Select`. `untyped.TableQuery.Selects` keeps its name.

- `velox-gen -plugin velox-graphql:only=enums` writes only the GraphQL
  marshalers of velox-gen's enum types, for an application that writes its
  own SDL and binds it with gqlgen's autobind: `open` goes out as `OPEN` and
  `OPEN` comes back as `open` (decision 0068).
- `velox.UseTx(db, tx)` returns a DB whose statements run on a `*sql.Tx` the
  caller began, next to raw SQL on the same transaction. The caller commits or
  rolls back; the DB does neither (decision 0066).
- `UpdateQuery.SetInput(in)` and `InsertQuery.SetInput(in)` take a
  `velox.Input[E]`, such as a generated GraphQL input, as ent's
  `SetInput` does: a resolver is
  `db.Update[gen.Post]().Where(gen.Posts.ID.EQ(id)).SetInput(input).One(ctx)`.
  An error from `Setters()` is returned unchanged by `Exec`, `One` or
  `SQL`, before anything is sent. The generated `Setters` has a value
  receiver. Regenerate: `veloxgql.GeneratedCodeIsVersion22` replaces
  Version21 (decision 0062).
- `veloxgql.MarshalInt64ID` and `UnmarshalInt64ID`, the `ID` model
  `veloxgql.Int64ID` of an int64 key: a JSON string out, a string of digits
  or an integer in, anything else BAD_USER_INPUT (decision 0061).

- `db.Rows(ctx, reason, sql, args...)` ranges over raw SQL whose rows are no
  entity's, scanned in column order, on the DB's connection, a
  transaction's included (decision 0060). `go.mod` retracts `v0.1.0` to
  `v0.3.0`, another project's code at this path.
- A connection is a query's `Paginate`, as in ent:
  `db.Query[gen.Post]().Paginate(ctx, after, first, before, last,
  gen.WithPostOrder(orderBy), gen.WithPostFilter(where))` replaces
  `gen.NewPostConnection(ctx, q, …)`. Regenerate:
  `veloxgql.GeneratedCodeIsVersion20` replaces Version19 (decision 0059).
- A JSON field's handle tests a value inside the document:
  `Attrs.Text("plan", "tier").EQ("pro")`, `Attrs.Number("seats").GT(10)`,
  `Attrs.Bool("trial").EQ(true)` and `Attrs.HasKey("trial")`. A value of another kind is NULL, so the same rows
  match on PostgreSQL, MySQL and SQLite (ADR 0058).
- A column handle's `Select(ps...)` makes a `velox.Subquery[V]`, and another
  handle's `InSelect(s)` and `NotInSelect(s)` test a column of the same type
  against it: `I.ID.InSelect(It.InvoiceID.Select(It.Qty.GTE(100)))`. The
  subquery's entity gets its own Read policy and interceptors. `NotInSelect`
  is `NOT EXISTS`, so a NULL in the subquery does not empty it (ADR 0057).
- `field.Int64("version").Version()` makes a field an entity's optimistic-locking
  version. Every update adds 1 to it, an upsert's update branch included. `Save`
  writes only where the row still holds the version `*x` read, and fails with
  `velox.ErrStale`, which also matches `ErrNotFound`, when another write moved it.
  A write that assigns it fails with `velox.ErrVersionSet`. velox-graphql leaves
  it out of create and update inputs (ADR 0056).
- `UpdateQuery.One(ctx)` writes the one row its conditions match and returns it
  as written, as ent's `UpdateOne`: `ErrNotFound` for no row the Read and Write
  policies let the caller change, `ErrNotSingular` for more than one, read back
  by its key without the Read policy (ADR 0048).
- `veloxgql.DB(ctx)` returns the request's DB for a resolver, and panics where
  `DBFromContext` fails, a schema served without `NewExtension` (ADR 0048).
- A relation handle's `Add(keys...)`, `Remove(keys...)` and `Clear()` are setters:
  `db.Insert(&p).Set(gen.Posts.Tags.Add(1, 2))` and `db.Update[gen.Post]().Set(...)`
  write a many-to-many edge's join rows, or re-point the children of an edge whose key
  the target holds, after the row and in one transaction. `InsertQuery.Set` also takes
  column setters. A key that names no row the caller may read or change fails with a
  `*velox.EdgeError` of `ErrNotFound`, and a child another owner holds with `ErrLinked`
  (ADR 0044).
- velox-gen checks the names a schema's behaviour gives: each constant name passed to
  a `velox.Field*` function, `velox.HasEdge`, `velox.HasEdgeWith`, `m.Set`, `m.Fill`, `m.Value`, `r.Value`, `r.IsZero` and
  `r.Set`, in a Policy, Hooks or Interceptors method or a mixin's, or in a function of
  the same package they reach, must name a column or relation of each entity the method
  serves, and a constant value must fit the column. It refuses each at its line, with the
  entity and the method; a name it cannot read is a warning, checked when the statement
  runs (ADR 0039).
- Package `schema/mixin/softdelete`: `softdelete.Mixin{}` in a schema's `Mixin()` adds
  `deleted_at`, an interceptor that ANDs `deleted_at IS NULL` into every statement on the
  entity, and a hook that turns a delete into an update that sets `deleted_at`.
  `softdelete.Skip(ctx)` lifts only the mixin: reads see deleted rows, and a delete is a
  hard delete. `velox.WithoutPolicy` does not lift it. Uniqueness over live rows is a
  partial index, `.Unique().Where("deleted_at IS NULL")`, on PostgreSQL and SQLite.
- Package `scope/scopetest`: `scopetest.Isolates[E](t, ctx, db, key, column, a, b, row)`
  checks that a scope confines entity E. It fails an entity that lacks the scope's hook or
  policy, or lets a read or an insert through without a value or into another scope.
- A mixin may declare behaviour: `func (M) Policy[E any]() velox.Policy[E]`,
  `Hooks[E any]() []velox.Hook[E]` and `Interceptors[E any]() []velox.Interceptor[E]`.
  velox-gen binds each for every entity that lists the mixin, mixins first in `Mixin()`
  order, then the entity's own methods. It refuses another shape at its line, and a
  literal name in a `velox.Field` call that an entity lacks or types differently; a name
  that is not a literal is a warning.
- `velox.Field[E, V]("org_id")` returns `velox.Named[E, V]`, the handle of entity E's column
  found by its name, for code that cannot name the generated handles, such as a mixin's
  behaviour. V must be the field's Go type exactly. A wrong name or type panics, which,
  called from a Policy, Hooks or Interceptors function, is E's `*SchemaError` at its
  first use. velox records E's columns before it calls those functions, so a call from
  one does not wait on the build in progress. A `Named` is a `Column[E]`, and has `EQ`,
  `NEQ`, `In`, `IsNull`, `NotNull`, `Set`, `Ptr`, `Immutable` and `Nullable`; for a
  pointer V, `EQ(nil)` is IS NULL and `Set(nil)` sets NULL.
- `velox.Interceptor[E]`, a row filter an entity declares with an `Interceptors()` method
  on its schema, which velox-gen binds. Its `Filter`'s condition is ANDed into every
  SELECT, Count, aggregate, With load, EXISTS, join ON, UPDATE, DELETE, Save and upsert
  update branch on the entity, before the Read policy. `WithoutPolicy` does not lift it,
  and its error is not a denial. A join entity cannot have one, and on MySQL an upsert
  that updates an entity with one is refused with `ErrPolicyUpsert`.
- `Query.Join` and `Query.LeftJoin` read a to-one relation in the query's own statement and
  fill its `One` field from the same row. `Rel.Is` and `Rel.By` are a condition and an
  ordering on the joined row's columns; the target's Read policy is in the join's ON
  clause, so a join shows nothing a query on the target would not. A to-many relation is
  refused (use `With` or `HasWhere`), as are an aggregate, a group, a per-owner read through a
  join table and a locking read. `LoadEach` and `CountEach` read a joined query's rows for each
  owner, in one statement, and `LimitEach` ranks the rows the join keeps. `After` pages over a joined column. `Join(rel.With(…))` loads relations of the
  joined entity. `Statement.Joins` lists the joins; a
  Rewrite hook may not change them. See `docs/decisions/0031-to-one-joins.md`.
- `graphql.OrderVia(field, value)`, an annotation of a to-one edge: connections to the edge's
  entity can be ordered by a column of its target (`orderBy: {field: AUTHOR_NAME}`), joined in
  the page query and the count, and paged by cursors that hold the value. It is an inner join: a
  row whose target is missing or hidden by the target's Read policy is not in the connection.
  The plugin refuses it on a to-many edge and for a nullable, sensitive, json or bytes field, or a
  field or edge with an application directive. The connection of an edge (`User.feed`), read for
  every parent in one statement, orders by it too. `gqlimpl.JoinedTerm` is the term;
  `Rel.Fill` and `Rel.Filled` set and read a joined entity. See
  `docs/decisions/0031-to-one-joins.md`.
- `schema.Scoped(key)`, a field annotation that marks the column an entity is scoped by.
  velox-gen writes the key (`OrgKey` for `"org"`) and each entity's scope into
  `scope_velox.go` and binds the scope around the entity's own `Policy` and `Hooks`, so a
  hand-written `scope.go` is no longer needed. The column must be `Immutable`, not
  `Optional` or `Nillable`, and an integer, string or UUID. See
  `docs/decisions/0030-generated-scope.md`. `velox.BindHooksAfter` is for that generated
  code: it binds the scope's hook before the entity's `Hooks`, and a nil hook is reported
  at its index in `Hooks()`. A malformed `Scoped` annotation is refused with its own message,
  and two UUID types of one name from different packages are two types under one key.
- Package `scope`: `scope.NewKey`, `Key.With` and `Key.From`, and `scope.By`, which gives a
  `Policy`, a `Wrap` of one, and a `Fill` hook. It confines the rows of an entity to a value
  in the context (an organization, a region, an owner), with `scope.ErrNoScope` and
  `scope.ErrMismatch`. `velox.WithScopeValue` records such a value for `RowScope`. See
  `examples/tenant` and `docs/decisions/0029-scope-replaces-tenant.md`.
- `graphql.MaxList(n)`, a per-edge bound on rows and on what they are charged.
- `veloxgql.Limits`: `MaxResponseBytes` (32 MiB) and `Timeout` (30 s), with `CodeTimeout`.
  `Setup` also caps resolver errors and websocket messages.
- `migrate resolve -statements`, `-adopt` and `-forget`; `-applied` works on a pending file;
  `diff -db`; `schema.Collation`. `docs/migrations-operations.md` covers merges and recovery.
- `ErrTxControl`.
- `migrate resolve -force reason` and `-scratch-db dsn`; `migrate.WithForce`, `migrate.WithScratch`
  and the `Error` Code `unverified`.
- `veloxgql.Limits`: `MaxFragments`, `MaxDepth`, `MaxNameBytes`, `MaxLiteralBytes`,
  `MaxVariableNodes` and `MaxErrors`. The `Server` interface `Setup` takes gains
  `SetValidationRulesFn`.
- `migrate.LintFile` codes `drop-domain`, `drop-type`, `drop-collation` and
  `drop-partition`; `TRUNCATE PARTITION` is `delete-rows`.
- `veloxgql.Setup` and `Limits.MaxTokens`, `MaxFields`, `MaxBodyBytes` and
  `MaxIntrospectionCost`, with `ErrRequestTooLarge`: an oversized document is refused
  before it is parsed. A public server should call `Setup`.
- `-- velox:drops`, the plan's list of what a generated file removes.
- `velox-gen` refuses `Unique` with `Optional`, two edges on one foreign key, non-ASCII
  names, and a `Default` that fails the field's built-in checks.
- `migrate.LintFile` reports `DROP SCHEMA`, `DROP DATABASE`, MySQL `CHANGE COLUMN` and
  an always-true `WHERE`, and also reads generated files.
- The runtime: typed queries, inserts, upserts, updates, deletes and transactions
  on PostgreSQL 14, MySQL 8.4 and SQLite 3.35 or newer. `velox.Connect` opens a
  database in one call, `velox.Open` takes a pool you opened, and `veloxpgx.Open`
  takes a pgx pool. An application on SQLite or MySQL does not link pgx.
- A schema DSL in the style of ent, and `velox-gen`, which generates the code.
  Bidirectional self edges work as ent's do.
- `With` eager loading, keyset pages with `After`, `Has` and `HasWhere`.
- Policies, typed hooks, row scoping (package `scope`), sharding and read replicas.
  `Iter`, `Count` and `Exists` work across shards.
- One-key grouped aggregates: `CountBy`, `SumBy`, `AvgBy`, `MinBy` and `MaxBy`.
- Validation: `MinRuneLen` and `MaxRuneLen` on a string field count characters, and
  `schema.Check` declares a CHECK constraint that migrations plan and drift compares.
- `velox.ErrLocked` for a lock that `NoWait` did not get, and `velox.IsNotFound`.
- `migrate` and `velox-gen migrate`: a planner, an applier, drift checks, and
  `migrate baseline` to adopt an existing database. `velox-gen migrate sql` feeds
  Atlas, for a directory that Atlas manages instead.
- `contrib/graphql`: a GraphQL schema and resolvers generated for gqlgen, with node
  IDs, keyset connections and mutations in savepoints. `gqlgenx.Cost` reads `@skip`
  and `@include`.
- `contrib/veloxotel`: OpenTelemetry tracing and metrics.
- Runnable examples in `examples/`, up to a whole service in `examples/tracker`.
- `graphql.CreateDefault(v)`: a default for a field in the GraphQL create input.
- `SumNull`, `AvgNull`, `MinNull`, `MaxNull` and their `...ByNull` grouped forms, for
  nullable number columns.
- `docs/coming-from-ent.md`; `examples/relations` links a many-to-many edge.
- `OrderField` on a nullable field, and `After` over nullable columns, with
  `gqlimpl.NullTerm` carrying NULL in the cursor (decision 0032).
- An untyped surface over the same engine path as the typed API, for generated code
  (decision 0035): `Table`, `TableOf`, `TableRows`, `RowsOf`, `PredOp` and its operators,
  `TableQuery` (`DB.QueryTable`, `Untyped`, `Typed`), `TableInsert`, `TableUpdate`,
  `TableDelete` (`DB.InsertTable`, `InsertRows`, `UpdateTable`, `DeleteTable`),
  `AndPredicates`, `OrPredicates`, `NotPredicate` and `Assignments`. It refuses a
  condition of another entity's Table, an EXISTS on another entity's relation, an
  assignment whose value is not of its column's Go type, rows of another entity and a
  column index out of range, with an error before anything is sent.
- `ErrOutOfRange`: `Table.Convert` refuses a number its column's Go type cannot hold
  (300 for an int8, a negative number for a uint), where it converted it with wrap-round.
  gqlimpl's where filters and update inputs report it as `BAD_USER_INPUT` naming the field.

### Changed

- A value an insert's setter gives is stored, its zero included, as ent's
  `SetX` is: `Set(X.Rate.Set(0))` writes 0 over the column's declared
  database default and over a `Default`, in `Insert`, `InsertAll` and an
  upsert's inserted rows, and is validated. A zero field no setter names
  still takes its default. velox-gen no longer warns "the zero value
  cannot be stored through Insert" (ADR 0136).
- An update whose setters were all skipped, a `Set()` of none or an empty
  `SetInput`, runs its hooks and its `UpdateDefault`s, as ent's does, so
  `updated_at` moves; so do `UpdateQuery.One` with no setter and an update
  of only edge setters. A hook's `Mutation.Set` on an insert names its
  column, so its zero is stored. Nothing is
  sent only when they assign nothing: `Exec` returns 0, `SQL` "", `One`
  the row (ADR 0136).
- `TxTimeout(d)` with `d <= 0` sets no bound of its own: it lifts a
  `WithTxDefaults` timeout, leaving `WithDeadlines`' limits.

- velox-gen is faster, and writes the same bytes. One run lists the schema
  package once and reads and parses each of its files once, where each pass
  did both again. The packages a mixin's file imports are found with one
  go list, and only the one an embedded type names is parsed whole. The
  generated files, and velox-graphql's, are gofmt'd in parallel, and the
  plugins run beside velox-gen's own work. A run makes 9 go commands, where
  it made 21. On a 496-entity schema with velox-graphql, a run takes
  2.8 s, where it took 6.0 s; velox-graphql alone takes 0.6 s, where it
  took 1.2 s.
- On PostgreSQL an In list of integers or strings is `col = ANY($1)`, and a
  NotIn `col <> ALL($1)`, with one argument: the list's array literal, such
  as `{1,2,3}`. PostgreSQL's generic plan cannot fold `IN ($1, …, $n)` into a
  constant array and tests each parameter on every row; bench/compare's
  EagerO2M100 on database/sql ran 6% behind ent's. Every length of a list is
  now one statement. Other values, and MySQL and SQLite, keep the IN list
  (ADR 0120).
- A query's LIMIT is a literal in every dialect, as SQLite's already was and
  as ent writes it; OFFSET stays a parameter. PostgreSQL's generic plan,
  which pgx's statement cache reaches after five runs, guessed a tenth of the
  table for a LIMIT parameter and picked a slower join: bench/compare's
  WhereHasEdge ran 30% behind ent's. Each page size is a statement of its own
  (ADR 0118).
- velox-gen: an enum type that one entity uses is declared in that
  entity's file, and only the enum types several entities share stay in
  `velox.go`. An enum field of an application's Go type is checked by one
  `veloxCheck<Type>` function per type, where every Check held a copy of
  its `switch`. A field whose default replaces its zero value in an insert
  says so on its struct field and handle (decision 0119). Regenerate:
  `velox.GeneratedCodeIsVersion28` replaces Version27.
- `All` allocates the rows of a `Limit` up to 128 at once, a page's size,
  where it allocated 16 and grew: a `Limit(20)` read of 20 rows is one
  allocation, and the runtime benchmarks of 20-row pages allocate about 40%
  fewer bytes. A larger `Limit` is a bound, and still starts at 16 rows.
- A bool column compared with a constant true or false binds no argument.
  PostgreSQL gets the column alone or `NOT` the column, as ent writes it: a
  bound bool led PostgreSQL's generic plan, which pgx's statement cache
  reaches after five runs, to misjudge the selectivity of the condition
  inside an EXISTS. MySQL and SQLite get `= 1` or `= 0`, the value their
  drivers bind, which SQLite's planner serves from an index and a bare
  column it does not. The constant is part of the render cache's shape key.
- Generated code reads the descriptors of an entity's closure defaults and
  validators with one `schema.FieldDescriptors(&S{}, "a", "b")` call, which
  calls the schema's Mixin and Fields once, where a `schema.FieldDescriptor`
  call per field called them once per field. `schema.FieldDescriptor` is
  gone. Regenerate: `velox.GeneratedCodeIsVersion27` replaces Version26.
- velox-gen: `migrate diff -check` applies the directory's files to an empty
  database and fails unless they make `schema.json`, as `migrate.Verify`
  does: a file deleted, or edited and hashed, passed before. A SQLite
  directory uses a temporary file; a PostgreSQL or MySQL one needs
  `-scratch-db <dsn>`, and is exit 2 without it. `-replay=false` compares
  the schema package with `schema.json` alone, as before.
- migrate: a MySQL `varchar(n)` to `text`, `mediumtext` or `longtext` is a
  Review `widen-type`, not a destructive `alter-type`: each holds what any
  varchar can. The finding names an index over the column, which MODIFY
  fails on without a prefix length.
- An IN or NOT IN list is written padded to the next power of two up to
  1024 values, then to the next multiple of 1024 up to 16384, its last
  value repeated, so lists of many lengths share a few shapes in the
  render cache. Loads and Each plan their chunks so that a padded list
  stays under the parameter limit. A full render cache now evicts shapes at
  random to hold a new one; it stopped adding shapes, so every later shape
  rendered each time.
- `Statement`, `ConflictClause`, `EachClause`, `JoinClause`, `Projection`,
  `EntityInfo`, `ColumnInfo`, `Rewrite`, `Around`, package `untyped`,
  `Rel.Fill`, `Rel.Filled` and the handles' `I` field are documented as for
  generated code and extensions, outside the 1.0 compatibility promise
  (`doc.go`, `docs/stability.md`).
- `db.WithoutPolicy("")`, or a reason of only white space, returns a DB
  whose every statement and `Tx` fail with "WithoutPolicy needs a reason",
  as `Exec` without a reason fails. It returned `db`, so the policies stayed
  on without a word.
- `Query.Limit(n)` and `Rel.LimitEach(n)` with n <= 0 fail the query with
  "Limit must be positive" ("LimitEach must be positive"), before anything
  is sent. `Limit(0)` reported "query has no Limit", and `LimitEach(0)`
  removed an earlier `LimitEach`. `MaxRows(0)` still restores the DB's cap,
  and `untyped.TableQuery.Limit(0)` still removes the limit.
- A `WithFilter` filter that restricts the join entity of a many-to-many
  edge, plain or an edge schema, fails `Has`, `Of`, a `With` load,
  `LoadEach` and `CountEach` through it (ADR 0098's amendment). It was
  ignored inside `Has` and applied to a `With` load, so the two disagreed.
  Return `Allow()` for a join entity.
- `veloxgql` is an engine-free core, and gqlgen's adapter is the new
  package `veloxgql/gqlgenx` (ADR 0104): `NewExtension`, `Extension`,
  `Setup`, `Server`, `ErrorPresenter`, `OnCanceled`, `PresenterOption` and
  the function marshalers (`MarshalTime`, `MarshalInt64ID` and the rest)
  moved there, and so did the options and the cost (ADR 0106):
  `veloxgql.WithLimits`, `WithTxOptions`, `WithTxFilter`, `WithBatchScope`
  and `Cost` are `gqlgenx.WithLimits` and the rest, of type
  `gqlgenx.Option`; `veloxgql.Config`, `NewConfig` and `Option` are gone.
  The core keeps `Limits`, `DefaultLimits` and `LimitsFromContext`, and
  imports no gqlparser. `veloxgql.Present` is the code and message a
  client sees. In `gqlgen.yml`, `Int64ID`, `Float32`, `Uint64`,
  `Uint`, `Time`, `UUID` and `Bytes` name `veloxgql/gqlgenx`; `JSON`,
  `Cursor`, `OrderDirection` and `PageInfo` stay on `veloxgql`. Generated
  code is unchanged; gqlgen's `generated.go` names `gqlgenx.MarshalTime`
  and `gqlgenx.MarshalInt64ID`. `Cursor` gains `MarshalGQL`, which
  panics on a cursor that does not encode, and `UnmarshalGQL`; a nil
  `gqlgraph.Connection` is the empty connection.
- A read of generated GraphQL code (a connection, an edge) in a context no
  adapter made fails with the new `veloxgql.ErrNoSelectionSource`, joined
  with `ErrNoDB`, and sends no statement (ADR 0105). It never falls back to
  reading every column. `gqlgenx.Extension.NewContext` gives code outside a
  server a context.
- Handles are named by what the column holds (ADR 0096): a time field's is
  `velox.TimeCol` (`NullTimeCol`), and an Other field's that is no decimal
  `velox.OtherCol` (`NullOtherCol`), each with the methods `OrderedCol` had.
  `OrderedCol` and `NullOrderedCol` are removed. Regenerate:
  `velox.GeneratedCodeIsVersion25` replaces Version24.
- An insert that leaves a nullable column nil leaves the column out of the
  INSERT (ADR 0093), as ent and old velox do, where it wrote NULL. A
  `DEFAULT` the table has but the schema does not declare now applies to
  such a row, where NULL was stored. Declare the default with
  `sqlschema.Default`, or give the field a value.
- `UpdateQuery.One` whose conditions pin the whole primary key
  (`Where(X.ID.EQ(id))`) sends the UPDATE with those conditions and reads
  the row back, without the `SELECT key ... FOR UPDATE` it sent first: two
  statements in its transaction, not three (ADR 0092). It also reads the
  row back by the key the update assigned, where an update of a key column
  read it by the old key and failed with `ErrNotFound`; an `Add` or `Sub`
  on a key column is refused before anything is sent.
- On SQLite a query's `LIMIT` is a literal in its SQL, not a parameter:
  SQLite ran a statement with `LIMIT ?` about eight times slower per
  execution (86 µs against 10 µs for a 90-column read, prepared once). A
  create-read-update-delete round over nextapp's 496 entities takes 122 ms
  instead of 151 ms, its reads 31 ms instead of 48 ms. OFFSET
  and every other value stay parameters. SQLite's fingerprints of limited
  queries change, and each limit is its own render-cache shape.

- velox-gen writes the empty interface as `any`, as Go has since 1.18: a
  JSON field of `map[string]any` is `map[string]any` in the entity, its
  handle and its default, not `map[string]interface{}`.

- velox-graphql: a where input's `Predicate` is one call, to
  `gqlgraph.Predicate`, which reads the input's fields as the type's
  `WhereFields` describe them and adds each set predicate with the same
  helpers, errors and limits as before. The where inputs' own code, a third
  of velox-graphql's output, is gone: at 478 types the generated package
  builds in 11.3 s instead of 12.9 (the ORM alone takes 6.9), in 4.3 GB
  instead of 4.8. A where
  input's untyped condition is the exported method `Predicate`, which
  `gqlgraph.Predicate` calls on a nested input (`gqlgraph.Input`).
  `veloxgql.GeneratedCodeIsVersion26` replaces Version25.

- velox-graphql: a connection reads the column of an order field only when
  its orderBy names it. `AlwaysRead` holds the key and the foreign keys, so
  a selection reads its own fields' columns and no more; before, every
  orderable column was read, which under `compat=veloxe` (every field
  orderable) was every column. An `XOrderField`'s `IsValid` and
  `UnmarshalGQL` are one call each to `gqlgraph.Type.IsOrderField` and
  `gqlgraph.UnmarshalOrderField`, with the same values and error text.
  `veloxgql.GeneratedCodeIsVersion25` replaces Version24.

- A policy, Query rule or interceptor whose subquery reaches its own entity
  again, as nextapp's conversation rule reads participants through the
  participants' own policy, is cut there and no longer refused: the inner
  subquery gets none of that entity's conditions, the outer gets them all,
  as veloxe's raw subquery applied none. `FieldInSelect` compares an integer
  column with a text key as text (`CAST(id AS TEXT)`, on MySQL
  `CONCAT(id)`), at run time and in velox-gen (ADR 0091).
- An index's type (`sqlschema.IndexType`) is ignored on SQLite, as ent's and
  veloxe's SQLite migration ignored it, where velox-gen refused it; MySQL
  still refuses it (ADR 0090).
- velox-gen refuses an `Optional` field whose only default is a
  `SchemaDefault`, and no longer lets one excuse a `Unique` `Optional` field
  (rules 37 and 54). velox's insert writes every column, so the field stored
  its Go zero, never the column's `DEFAULT`. Add a `Default` or `Nillable`.
- velox-gen warns of each unique key over every row of an entity with
  `softdelete.Mixin`: a deleted row keeps its value and blocks a new one.

- `Query.Unbounded()` is `Query.AllowUnbounded()`. It only waives the check that
  fails a read with no `Limit`; a `Limit` you set still applies. The error
  says so. `ErrUnbounded` keeps its name (decision 0067).

- velox-gen's defaults are `./veloxschema` for the schema package and
  `./veloxgen` for the generated package, which was `./schema` and `./gen`:
  `go tool velox-gen` with no flags reads and writes the new folders, the
  generated package is named `veloxgen`, and the examples use them. `-out`
  and `-package` still name any other folder and package. The generated
  code's shape is the same, so `GeneratedCodeIsVersionN` stays. A runtime
  error that names an entity says `veloxgen.Task`, not `gen.Task`. To keep the
  old layout, pass `-out ./gen ./schema`. Decision 0065.
- veloxe's defaults are velox-gen's (ADR 0090, superseding 0063): an unset
  `Optional` field is its Go zero, and only an `Optional` enum needs a
  `Default` or `Nillable` (rule 37; rules 39, 40 and 48 are gone). An
  `Optional` JSON field of any type is accepted; unset, a slice or map is
  written `[]` or `{}`. A NOT NULL string, bool or number column with no
  `Default` gets its zero as its column `DEFAULT`. An unsized string is
  `character varying` on PostgreSQL. An enum is stored as ent and veloxe
  store it: varchar on PostgreSQL, `enum(...)` on MySQL, text on SQLite,
  with no enum type unless a Postgres `SchemaType` names one and no `CHECK`.
  An `Immutable` key makes its edge `Immutable` (rule 34 refuses only the
  converse). Existing databases need a migration; `loadjson.Version` is 8.
- velox-gen declares no index of a foreign key, as ent and veloxe declare
  none (ADR 0090's amendment): on PostgreSQL and SQLite it added
  `<table>_<column>` for each key no other index led, so an ent or veloxe
  database's first diff added them. MySQL keeps the index InnoDB makes,
  named as the key. A database velox created earlier has the old indexes:
  the next `migrate diff` drops them, and each example has a
  `drop_fk_indexes` migration.
- An unset `Optional` JSON field of pointer type is written as its
  element's zero value's JSON, as veloxe wrote it, not NULL (ADR 0090's
  amendment).
- A GraphQL ID is the entity's key in its own Go type, which gqlgen decodes:
  a resolver takes `id int64`, an input's `<edge>ID` and `<edge>IDs` and a
  where input's ID predicates are keys, and velox-graphql refuses a schema
  whose GraphQL types have keys of different Go types. Bind `ID` in
  `gqlgen.yml` to `veloxgql.Int64ID` (int64), `veloxgql.UUID` (UUID) or
  gqlgen's `graphql.ID` (string). Without a `QueryField`, `velox.graphql` has
  no `type Query`. Regenerate: `veloxgql.GeneratedCodeIsVersion21` replaces
  Version20 (decision 0061).

- `velox.WithoutPolicy(ctx, reason)` is `db.WithoutPolicy(reason)`, which
  returns a DB whose statements, transactions and write hooks skip the
  policies; a context no longer carries a bypass. `DB.PolicyBypass()` reads
  it, and `RowScope.PolicyBypass` is gone (ADR 0054).
- A GraphQL edge batch is keyed by the SQL each caller's context reads, so a
  policy's own context values never share a read (ADR 0054).
- On a pool of one connection, a statement is refused when it runs under the
  context its Iter or Tx was given, from any goroutine, not when it runs on
  that goroutine (ADR 0054).

- The untyped surface is package `github.com/syssam/velox/untyped`:
  `velox.TableOf` is `untyped.TableOf`, `velox.Untyped` is `untyped.FromQuery`,
  `db.QueryTable(t)` is `untyped.Query(db, t)` (and `InsertTable`,
  `InsertRows`, `UpdateTable`, `DeleteTable` are `untyped.Insert`,
  `InsertRows`, `Update`, `Delete`), `m.Table()` is `untyped.MutationTable(m)`,
  `velox.TableRows` is `untyped.Rows`, `velox.AndPredicates` is `untyped.And`,
  and `PredOp`, its operators and the `Table*` types moved as they are.
  `velox.Typed` is gone. Generated GraphQL code imports untyped:
  `veloxgql.GeneratedCodeIsVersion18` is `GeneratedCodeIsVersion19`
  (ADR 0052).

- The untyped surface for generated code takes and returns a row as a *E in
  an any, checked against the Table's entity, where it took an
  `unsafe.Pointer`: `Table.Field`, `Value`, `New`, `Fill`, `Filled`, `Of`,
  `SetField`, `Validate`, `TableRows.At`, `TableQuery.After`, `One`, `First`,
  `LoadEach`, `CountEach` and `DB.InsertTable`. A row of another entity is an
  error. `Table.Ptr` is gone (ADR 0051).
- velox-gen tags a required time, UUID, struct or array field `json:"name"`,
  as it tags a required number or string, where it was `omitzero`: a zero
  value is a value. An optional one stays `omitzero`.

- `Rel.HasWhere` is `Rel.HasWith`, as ent's `HasXWith` and velox's own
  `HasEdgeWith`. Generated code: the handles' type is exported (`PostHandles`),
  and an enum field's handle is `velox.Col`, without `Contains`, `HasPrefix` or
  `Like`. Regenerate: `velox.GeneratedCodeIsVersion19` replaces Version18
  (ADR 0050).
- velox-graphql: a type's connection is `gen.NewPostConnection(ctx, q, …)`, which
  was `gen.PaginatePosts`; it sends nothing until gqlgen resolves its fields. The
  package generated code imports is `veloxgql/gqlgraph`, which was
  `veloxgql/gqlimpl`. Generated receivers are named after their type, `p *Post`.
  Regenerate: `veloxgql.GeneratedCodeIsVersion18` replaces Version17 (ADR 0049).
- **Breaking.** velox-graphql's edge methods are one line: a connection edge
  calls the target's unexported `paginate<Plural>(ctx, from, …)` with
  `postGQL.Edge(x, postEdgeTags)`, and `Paginate<Plural>` calls it with
  `gqlimpl.Query(q)`; a to-one or list edge passes the Edge to `EdgeOne` or
  `EdgeList`. The type literal's `Edges` is indexed by relation and names the
  edge's GraphQL field. In gqlimpl, `Paginate` takes a `Source`,
  `EdgeConnection` is gone, and `Args` holds the where input and its
  condition, so a batch keeps the same arguments as before. Edge methods no
  longer read the DB themselves. `veloxgql.GeneratedCodeIsVersion16` is now
  `GeneratedCodeIsVersion17` (ADR 0047).

- **Breaking.** velox-graphql writes one connection function per type,
  `Paginate<Plural>(ctx, q, after, first, before, last, orderBy, where)`, as
  entgql's `Query().Paginate`: a query resolver passes
  `db.Query[T]()`, with `db` from `veloxgql.DBFromContext(ctx)`, or its own
  query. `<Plural>Connection` and `<Plural>ConnectionOf` are gone, and so is
  a where input's exported `PredicateGQL`; `Pred(ctx)` stays. In gqlimpl,
  `Conn` is `Type` (its field `Type` is `Name`), `Paginate` replaces
  `ConnectionBase` and `NewConnection`, and `Not`, `And`, `Or` and `HasWith`
  take the nested input's condition as a `Cond`. `veloxgql.GeneratedCodeIsVersion15`
  is now `GeneratedCodeIsVersion16` (ADR 0046).

- A velox-graphql input has one method, `Setters() ([]velox.Setter[T],
  error)`, plain Go that returns its fields and edges as setters and does no
  I/O; the resolver writes them with `db.Insert(x).Set(set...)` or
  `db.Update[T]().Where(...).Set(set...)` and reads the row back. `Create`,
  `Update`, the per-edge functions and gqlimpl's write helpers (`Atomic`,
  `NewCreate`, `NewUpdate`, `LockRow`, …) are gone. An explicit null is a
  field not given, as in entgql: send `clear<Field>: true` to clear. A list
  of more than `gqlimpl.MaxIDs` (1000) IDs is refused. `ErrorPresenter`
  presents a `*velox.EdgeError`: NOT_FOUND, or BAD_USER_INPUT "already
  linked to another owner". `veloxgql.GeneratedCodeIsVersion14` is now
  `GeneratedCodeIsVersion15` (ADR 0045).

- A GraphQL type's columns and input names are data in its `gqlimpl.Conn`:
  `Read`, `Fields` and `Edges` give the columns a selection reads, and
  `Inputs` (or `CreateInputs` and `UpdateInputs`) the input field that sets
  each column. The generated `<entity>ColumnsGQL` and `<entity>InputGQL`
  switch functions are gone, and `Conn.Columns` is a method.
  `veloxgql.GeneratedCodeIsVersion13` is now `GeneratedCodeIsVersion14`
  (ADR 0033).

- velox-graphql names an edge's method after the edge, `Post.Author(ctx)` and
  `Post.Tags(ctx, …)`, as entgql does, not `AuthorGQL` and `TagsGQL`, and the
  SDL drops the `@goField` gqlgen no longer needs; a Node's ID method is
  `GlobalID`, was `IDGQL`, and `veloxgql.Noder` has it. The per-type `<entity>NodeGQL`
  and the unused `<entity>KeyGQL` are gone: `node_gql.go` builds each Node type
  itself. `veloxgql.GeneratedCodeIsVersion12` is now `GeneratedCodeIsVersion13`
  (ADR 0043).

- velox-gen writes an entity to `gen/<entity>.go`, as ent does, not
  `gen/<entity>_velox.go`; a name go build would leave out of a build, such as
  `ab_test.go`, or `velox.go`, keeps `_velox.go`. The next run removes the old
  files. A hand-written `gen/<entity>.go` must be renamed (ADR 0042).

- On SQLite a time column is `datetime(9)`, was `datetime`. The modernc.org/sqlite
  driver hands its text over, and velox parses velox's form itself with no allocation,
  where the driver's parse of a `datetime` cell failed once and allocated on every
  read. bench/compare's Query100 and EagerO2M100 on SQLite allocate about 30% less.
  `migrate diff` plans the change of an existing `datetime` column, a table rebuild
  whose file drops `type=datetime`; until then the column reads as before. A
  `sql.Scanner` or `*any` destination of a `datetime(9)` column receives a `time.Time`,
  as from a `datetime` one; a `*string` receives the stored text.
- **Breaking.** `GeneratedCodeIsVersion18`: regenerate. An entity's relations are in
  an ent-style struct, `Edges IssueEdges`, after its columns: `x.Project.Get()` is
  `x.Edges.Project.Get()`, and JSON nests the loaded relations under `"edges"`, left
  out when none was loaded (ADR 0041). An entity without relations has no `Edges`. A
  schema named `IssueEdges` beside an `Issue` with relations is refused.
- **Breaking.** `GeneratedCodeIsVersion17`: regenerate. A descriptor names its schema's
  value, `Schema: schema.Issue{}`, and velox reads the behaviour from it when it builds
  the metadata: each `Mixin()` value's Policy, Hooks and Interceptors, in order, then the
  schema's own (ADR 0040). The lists `Descriptor.Policy`, `Hooks` and `Interceptors` are
  gone; the database schema's name is `Descriptor.TableSchema`. gen imports the schema
  package by its own name, and `velox/schema` as `veloxschema` when the two collide. A
  hand-written entity names itself, `Schema: (*E)(nil)`. A method named Policy, Hooks or
  Interceptors whose signature velox does not call is the entity's `*SchemaError`.
- **Breaking.** The untyped behaviour API takes ent's names. `velox.FieldEQ("org_id", v)`,
  `FieldNEQ`, `FieldGT`, `FieldGTE`, `FieldLT`, `FieldLTE`, `FieldIn`, `FieldNotIn`,
  `FieldIsNull`, `FieldNotNull`, `FieldContains`, `FieldHasPrefix`, `FieldHasSuffix` and
  `FieldEqualFold` replace `velox.C(name).EQ(v)` and the rest; `velox.HasEdge("doc")` and
  `HasEdgeWith("doc", ps...)` replace `velox.Edge("doc").Has()` and `HasWhere`. `Cond` is
  `velox.Predicate`, an alias of `Pred` that `And`, `Or` and `Not` combine; `C`, `Named`,
  `Edge`, `NamedEdge`, `Cond` and `velox.Predicates` are gone, and `Mutation.Conds` is
  `Mutation.Predicates`. `schema.MixinAt` is gone.
- `veloxgql.GeneratedCodeIsVersion12`: regenerate. velox-graphql's `*_gql.go` files
  import no `unsafe` and convert no pointer: gqlimpl takes and returns rows as `*E` in an
  `any`, which the generated code type-asserts. Each type's `<entity>ConnGQL`
  (`gqlimpl.Conn`) also holds its `Table` and its inputs' field names (`CreateField`,
  `UpdateField`), and `NewNode`, `NewFilter`, `NewCreate`, `NewUpdate`, `EdgeOne`,
  `EdgeList` and `EdgeConnection` take it in place of the type's names, its table and its
  columns. `EdgeOne` and `EdgeList` read the DB from the context and narrow to the
  selection themselves. A where input's `PredicateGQL` adds a field's predicates with
  one call, `gqlimpl.Field`, `IDField` or `FKField`, whose `gqlimpl.Ops[V]` holds the
  input's values of each operator; `Value`, `List`, `Is`, `ID`, `IDList`, `FKID`,
  `FKIDList` and `FKIs` are gone. The GraphQL schema and every behaviour are unchanged.
- **Breaking.** `GeneratedCodeIsVersion16`: regenerate. Behaviour declared in the schema
  is untyped and names columns by name; gen imports the schema; gen/runtime is gone;
  remove `import _ ".../gen/runtime"`. `velox.Policy`, `Hook`, `Mutator`, `MutateFunc`,
  `Mutation` and `Interceptor` are not generic: a policy's `Read` returns a `velox.Cond`
  built with `velox.C("org_id").EQ(v)`, a hook reads and sets columns with `m.Set`,
  `m.Fill`, `m.Value` and `r.Value`, and a mixin's methods are plain. `velox.Before` runs
  a function before the write, `velox.WithHooks[E]` adds a hook that names gen's types,
  and `velox.Edge("doc").Has()` names a relation. `velox.Field`, `Named[E, V]`, the
  `Bind*` functions and `Bound*` types, `Allow[E]` and the handles' `ValueIn` are gone.
  `scope.By` and `scope.Scope` are gone: a mixin's policy calls `Key.Require` and
  `Key.Match`. A schema package that imports gen is refused (ADR 0039).
- **Breaking.** `scope.By[E](key, column)` finds the scope's column by its name, with
  `velox.Field`, so that a mixin's generic `Policy` and `Hooks` can call it. It refuses a
  column that is mutable or nullable, as the entity's `*SchemaError` at its first use.
  `Scope.Wrap` is gone: velox ANDs a scope's `Policy()` with the entity's own.
- **Breaking.** `GeneratedCodeIsVersion15`: regenerate. Tenancy is a mixin (ADR 0038):
  `schema.Scoped`, `scope_velox.go` and its key variables, `Descriptor.Scope`,
  `velox.Scoper` and `velox.ScopeKit` are gone. Declare the key in the schema package,
  `var OrgKey = scope.NewKey[int64]("org")`, and a mixin whose `Fields` holds the
  Immutable column, whose `Policy[E]` returns `scope.By[E](OrgKey, "org_id").Policy()` and
  whose `Hooks[E]` returns its `Fill()`. `gen.OrgKey` becomes `schema.OrgKey`.
- velox-graphql: Create's doc no longer names a required field that the schema leaves out
  of the create input explicitly, with `Skip(SkipMutationCreateInput)` or
  `CreateInput(false)`: the application or a hook sets it, as a tenancy mixin's hook does.
- `GeneratedCodeIsVersion14`: regenerate. `velox.BindPolicy`, `BindHooks` and
  `BindInterceptors` take the functions to bind, `BindPolicy(fs ...func() Policy[E])`, and
  return the Descriptor's list. Package runtime binds each entity's sources there,
  `velox.BindPolicy[gen.Comment](sch.Comment{}.Policy)`, so gen's `VeloxRuntime` holds the
  bound lists and `veloxBound` is gone. Package runtime's comment lists where each
  entity's Policy, Hooks and Interceptors come from. A hand-written entity binds its
  method as `Policy: velox.BindPolicy((*E)(nil).Policy)`.
- `GeneratedCodeIsVersion13`: regenerate. `Descriptor.Policy`, `Descriptor.Hooks` and
  the new `Descriptor.Interceptors` are lists of erased bindings
  (`[]func() velox.BoundPolicy` and so on), which velox composes: the policies ANDed in
  order, with no short-circuit, the hooks in order, the first outermost, and the
  interceptors' conditions ANDed in order. A hand-written entity wraps its binding in a
  one-element list. `Meta.HasReadPolicy` and `Table.HasReadPolicy` are `HasRowFilter`,
  true for a Read policy or an interceptor.
- `GeneratedCodeIsVersion12`: regenerate. A descriptor names each part of an entity's
  behaviour by one line: `Scope: commentScope` when it is `schema.Scoped`,
  `Policy: rt.CommentPolicy` when the schema declares `Policy`, and `Hooks: rt.CommentHooks`
  when it declares `Hooks`. velox composes them, as the generated code did: the policy is
  the scope's `Wrap` around the entity's own, or around an empty one, and the hooks are the
  scope's `Fill`, then the entity's. `Descriptor.Scope` is a `velox.Scoper`; `scope.Scope`
  is one by embedding `velox.ScopeKit`. A Scoper without `Wrap` or `Fill`, of another
  entity, or whose `Fill` is nil is refused with a `*SchemaError`. `velox.BindHooksAfter`
  is gone.
- `GeneratedCodeIsVersion11`: regenerate. The layout is ent's (decision 0037), and
  replaces the split of 0036. Every entity is in `gen`, always; there is no `gen/entity`
  and no re-export. A schema with `Policy` or `Hooks` imports `gen` and types them with
  `gen.User`. gen then does not import the schema: velox-gen writes `gen/runtime`, which
  binds the schema's methods, defaults and validators by name in its `init`, and only
  when something needs binding. A program imports it once, in its main package and in
  each test package, `import _ "<module>/gen/runtime"`; without it, a statement on an
  entity fails with `velox.NotLinkedError`, which names that line. velox-gen warns when
  no file of the module imports it, and refuses a schema that imports it. Plugins write
  into `gen`. Regenerating removes an old `gen/entity`'s generated files, the old
  facade's content and a plugin's re-export file; a file of yours there stays, named.
- `GeneratedCodeIsVersion10`: regenerate. In split mode gen is one file, `gen/velox.go`,
  which re-exports every entity, enum, scope key and handle; a plugin's re-exports are one
  `gen/<plugin>_velox.go`, and velox-gen removes the old per-entity files. An entity's
  handles have a named type (`var Users = userHandles{…}`), `String` prints a pointer field
  with `velox.Show`, and `VeloxMeta` is one line in split mode too: an entity used before
  gen's init panics with `velox.NotLinkedError`, which `MetaOnce` does not keep. A SIGINT
  or SIGTERM while velox-gen loads the schema removes its `.velox-gen-` directory.
- The generated descriptor calls a default or a validator by its name when it is an
  exported function declared at package level in a package gen can import: `mixin.Time`'s
  `time.Now` is `func() any { return time.Now() }`. The schema package's own such function
  is called from the facade, `gen/velox.go`, in split mode. A closure, a method value, an
  instance of a generic function, an unexported function and one of a package gen cannot
  import (package main, an internal package of another tree, one that imports gen) is read
  from its field's descriptor by the field's name, with `schema.FieldDescriptor`, which is
  new. In split mode `entity.VeloxRuntime` holds typed fields, set by name in `gen/velox.go`:
  `CommentPolicy: sch.Comment{}.Policy`, `GroupTokenDefault func() string`. The compiler
  checks each at its line there. Generated code reads no field by its index.
- `GeneratedCodeIsVersion9`: regenerate. `entity.VeloxRuntime` has a typed field per bound
  method, default and validator in place of a schema value per entity, and `veloxPolicy` and
  `veloxHooks` are gone.
- velox-gen finds schemas by ent's rule: an exported, non-generic struct type of the schema
  package that implements `schema.Interface`, by value or through a pointer, directly or
  through embedding at any depth. A schema that embeds an unexported base (`type Release
  struct{ base }`) was skipped without a word, and is now a schema; a method with a pointer
  receiver failed in velox-gen's internal program, and now works. What looks like a schema and
  is not one is refused at its line: a schema that embeds another schema (both would be
  entities), `*schema.Schema` embedded at any depth, a generic type, an alias, a method of
  another signature, a lone unexported struct that embeds `schema.Schema`, and a schema file
  that only some builds include. A package that the schema's structure reaches and that does
  not compile is reported with the chain of imports. See `docs/coming-from-ent.md`.
- A schema that declares `Policy` or `Hooks` splits the generated package. `gen/entity`
  holds every entity, handle and descriptor, and does not import the schema package; the
  schema imports it and writes `velox.Policy[entity.User]`. `gen` imports both, re-exports
  every name of `gen/entity` (types as aliases, functions as wrappers) and binds the schema
  in its `init`. `gen/runtime` is gone, and so is the blank import: a program imports `gen`.
  A program that links `gen/entity` alone fails at an entity's first use, naming the import.
  A schema that imports `gen`, directly or through another package, is refused at the import
  with what to write. Plugins write into `gen/entity` then, and velox-gen re-exports their Go
  files in `gen`. Without `Policy` or `Hooks`, `gen` is one package, as before. Errors and
  telemetry name `gen.User` in both layouts once the entity's metadata is
  built; an error while it is built, such as the not-linked one or a
  relation's target, names `entity.User`. See `docs/decisions/0036-split-gen-for-behaviour.md`.
- `GeneratedCodeIsVersion8`: regenerate. The generated descriptor reads an entity's
  `Policy` and `Hooks` from its schema value by an interface assertion, and the runtime holder
  has one schema value per entity. `Descriptor.Name`, new, names the entity in errors and
  telemetry; velox-gen sets it to `gen.User` when the type is `entity.User`.
- `migrate resolve -applied` and `-adopt` (`migrate.Resolve`'s `ResolveApplied` and
  `ResolveAdopted`) compare the database with what the directory says it holds after the
  version, and refuse a difference, listed as `migrate drift` lists it, unless `-force`
  gives a reason. That schema is `schema.json` at the directory's last file, else the files
  up to the version applied to a scratch database. A forced row keeps the reason in its
  error column, which `status` shows. `docs/migrations-operations.md` has the rule.
- velox-graphql's connections, cursors and Node lookups run on one non-generic runtime,
  `gqlimpl.Conn` and `gqlimpl.NewConnection` on velox's untyped `TableQuery` (`velox.Table`,
  `TableOf`, `Untyped`, `Typed`, for generated code), and the generated code only names the
  columns: a `<entity>ConnGQL` table of the key and the order fields' columns replaces the
  `<entity>KeyOrderGQL` and `<entity>TermGQL` functions, and `<entity>ColumnsGQL` returns
  column indexes. `gqlimpl.Term`, `NullTerm`, `JoinedTerm` and `OrderTerm` are gone, and
  `Connection` and `Page` are not generic. `XsConnectionOf` keeps its typed signature and every
  behaviour and error. `veloxgql.GeneratedCodeIsVersion10`: regenerate.
- velox-graphql's where inputs, edge resolvers and mutation inputs run on the same
  non-generic runtime, on velox's untyped writes (`DB.InsertTable`, `InsertRows`,
  `UpdateTable`, `DeleteTable`, `Assignments`): `gqlimpl.Filter`, `Create`, `Update`,
  `M2M`, `Owned` and `Edit` are not generic, and their helpers take column indexes.
  `Create(ctx, x)` and `Update(ctx, id, set...)` keep their signatures, and an
  application's setter still wins over the input's. `veloxgql.GeneratedCodeIsVersion11`:
  regenerate.
- velox-graphql's enum marshalers switch on velox-gen's constants and write each known
  value as a literal, with no `encoding/json`; `gqlimpl.WriteString` writes any other
  value as a JSON string. An unknown value's error names it: "PostStatus must be DRAFT
  or PUBLISHED, not ARCHIVED". `plugin.Enum.Consts` gives the plugins the constants;
  velox-graphql refuses a model without them and says velox-gen is too old.
- velox-graphql's generated doc comments say what each thing is in a sentence or two;
  how the generated code uses the runtime is documented once, in gqlimpl's godoc.
- velox-graphql writes its schema to `gen/velox.graphql`, which was `gen/velox.graphqls`, as
  entgql writes `ent.graphql`: the extension more GraphQL tooling reads. velox-gen's plugins
  write `.go`, `.graphql` and `.proto` files. gqlgen.yml's `schema` globs must change to match,
  `gen/*.graphql` (and `graph/*.graphql` for the application's own files, which the examples
  rename to `app.graphql`); delete the old `gen/velox.graphqls`, which velox-gen no longer owns
  and so leaves in place.
- velox-graphql generates inputs, not mutations, as entgql does (decision 0033). The
  generated schema has `Create<T>Input` and `Update<T>Input` and no `type Mutation`; the
  application declares its mutations in its own schema file and writes their resolvers.
  `gen.Create<T>(ctx, input[, set])` and `gen.Update<T>(ctx, id, input)` are now methods of
  the inputs, `input.Create(ctx, x)` and `input.Update(ctx, id, setters...)`: x is the row to
  insert (nil for a new one), with what the client must not choose already set on it, which
  replaces the `set func(*T)` of an entity whose create input leaves out a required field;
  the setters are the resolver's own, validated apart from the input's. The generated code is a
  description that `gqlimpl` runs, 12 to 25% shorter per entity
  (`examples/graphql/gen/post_gql.go` 724 lines to 543). An explicit null is found by the
  input's GraphQL type, not the argument named `input`; where the type occurs more than
  once in the field's arguments, a null on a clearable field is refused, naming `clear<Field>`.
  The errors name the input: `Update<T>Input: no <T> has this ID`, and `id` for a bad ID
  of the row. `String` and `GoString` are written only for an input with a sensitive
  field, and one `<entity>InputGQL` names both inputs' fields unless they differ.
  `veloxgql.GeneratedCodeIsVersion9`: regenerate. To migrate, add to a schema file of
  your own, which gqlgen.yml lists (`graph/*.graphql`),

  ```graphql
  type Mutation {
    createPost(input: CreatePostInput!): Post!
    updatePost(id: ID!, input: UpdatePostInput!): Post!
  }
  ```

  run `go tool gqlgen generate`, and write the resolvers:

  ```go
  func (r *mutationResolver) CreatePost(ctx context.Context, input gen.CreatePostInput) (*gen.Post, error) {
  	return input.Create(ctx, nil) // was gen.CreatePost(ctx, input)
  }

  func (r *mutationResolver) UpdatePost(ctx context.Context, id string, input gen.UpdatePostInput) (*gen.Post, error) {
  	return input.Update(ctx, id) // was gen.UpdatePost(ctx, id, input)
  }
  ```

  A `gen.CreatePost(ctx, input, func(p *gen.Post) { p.Slug = s })` is
  `input.Create(ctx, &gen.Post{Slug: s})`.
- velox-graphql's inputs, after review (decision 0033, "Review"): an application's setter
  given to `Update` of a column the input sets too wins, the input's dropped, where the
  UPDATE failed with "column assigned twice"; the application's setters are validated
  apart, and a refusal of theirs is velox's `*velox.ValidationError` as it is, not
  BAD_USER_INPUT. `Create`'s godoc says that the input's fields overwrite x's and that x's
  own fields, foreign keys included, are trusted, not checked. The generated code passes
  the GraphQL type to `gqlimpl.NewCreate` and `NewUpdate`, writes one function per edge that
  both inputs write by the target's rows (`<entity><Edge>EdgeGQL`) and one key function per
  Node entity (`<entity>KeyGQL`; the key's order is `<entity>KeyOrderGQL`), and decodes an
  edge's add and remove IDs with one `gqlimpl.AddRemove`; the helpers take column handles
  as `gqlimpl.Handle[T]`, so another entity's handle does not compile in their place.
  `velox.Setter.Column` is the column a setter assigns, for generated code. Regenerate.
  `examples/graphql`: `UpdatePostInput` has no `authorID`; only a post's author may update
  or delete it (FORBIDDEN otherwise); `createPost` checks the viewer is a user; SQLite opens
  with foreign keys on, so `deletePost` relies on `post_tags`' ON DELETE CASCADE, and a
  delete count the driver does not report is an internal error.
- velox-gen: the generated entity code names what it used to number. A column's and an edge's
  position is a constant (`postColTitle`, `postEdgeAuthor`) that the handles, the descriptor's
  `PrimaryKey`, `Indexes`, `Edges`, `FieldPtr` and `Check` cases share, including the columns of
  another entity that `EdgeTo` and `EdgeThrough` name, where there were bare `0`, `1` and `7`.
  Each handle and the descriptor function has a doc comment, and `String` names the local of a
  pointer field after it (`noteVal`), not `p5`. The runtime is unchanged and the new names are
  unexported, so no application code changes; regenerate to get them.
- velox-graphql: the generated Go is smaller, about 12% per entity and no longer growing in one
  function. `Node` and `Nodes` are two calls into `gqlimpl.NodeByID` and `NodesByID`, and each
  Node type writes only its own `<entity>NodeGQL`, so `node_gql.go` is a few lines per entity
  (26,000 lines for 500 entities before, 1,000 now). `UpdateX` and `DeleteX` check their row with
  `gqlimpl.LockRow`, an input's `String` uses `gqlimpl.Show`, and `Pred`'s comment points to
  `gqlimpl.NewFilter` for its errors. Locals are named after their input field (`writerIDKey`,
  `tagsJSON`, `unlinkRelated`), not by index (`k7`, `j4`). A page makes its edges and its cursors'
  terms in one slab each, and `EncodeCursor` writes the usual cursor (integer, string, boolean or nil
  values) without reflection, byte for byte as before: a list of 50 rows with cursors allocates 9%
  fewer objects and 15% fewer bytes. Behaviour and the SDL are as they were; regenerate.
  `VELOX_GQL_BENCH=1 go test -run TestGeneratedBench ./cmd/velox-graphql` (in `contrib/graphql`)
  measures a generated application's queries.
- Generated code: a required bool, number, string or enum field has no `omitempty` in its JSON
  tag, so a stored `0`, `false` or `""` is no longer dropped from the JSON of an entity (an
  Optional or Nillable field keeps it). A slice-of-scalars default (`field.Strings("tags").Default(...)`)
  is a Go literal, not JSON decoded on each insert. `Check` calls `IsValid` for an enum, names its
  values in the error, and compares an Optional value with `0`, `false` or `""`; a schema's
  `Fields` is called once per field that has several defaults and validators. The
  `GeneratedCodeIsVersion7` guard is in `velox.go` only, and `String` of an entity with no
  sensitive field says so. The foreign key velox adds for an edge (`UserSpouse`, `CategoryChildren`)
  is `json:"-"` and not in `String`, as ent keeps it unexported; its column name is ent's, and the
  Go field stays exported for the typed handle. The runtime is unchanged: regenerate to get them.
- **Breaking.** Tenant scoping is gone from the core; a `Policy` and a hook through
  `velox/scope` do it. Removed: `WithTenant`, `WithAllTenants`, `field.Tenant()`,
  `Descriptor.Tenant` and `TenantRefs`, `TenantRef`, `EntityInfo.Tenant`,
  `Statement.Tenant` and `TenantBypass`, `ErrNoTenant`, `ErrTenantMismatch`,
  `ErrTenantImmutable`, `ErrTenantRef` and `ErrTenantUpsert`. Mark the scope column
  `Immutable()`: the engine refuses to change it (`ErrImmutable`).
- **Breaking.** `WithoutPolicy(ctx, reason)` lifts every policy, a scope included, and a
  later call replaces the reason. `WithShardFor` takes `func(ctx) (string, error)`, reads
  the context itself, and returns "" for a context that names no shard. `RowScope` is
  `{Values, PolicyBypass, Shard, HasShard}`. `veloxotel.Config.RecordTenant` is
  `RecordScope`.
- **Breaking.** `plugin.ModelVersion` is 3 and the model JSON is version 4: rebuild
  plugins, and regenerate with `velox-gen`. `velox-graphql` declares `Model: 3`.
- On MySQL, an upsert that updates an entity with a Read or Write policy, a scope
  included, fails with `ErrPolicyUpsert`: any unique key can conflict there, and the
  update branch cannot be confined to the caller's rows.
- A Rewrite hook that edits a row after the policy's `Insert` check is no longer checked
  against the scope again; the policy's `Insert` and the scope's `Fill` run before it.
- A join entity of a many-to-many edge may not have a Read policy of its own: it is read
  through its two sides, whose Read policies apply. A bare join table is unscoped.
- GraphQL: an explicit `null` on a Nillable field in an update clears it. `null` on an
  `<edge>ID` stays refused and names `clear<Edge>`. A JSON input number is `float64` when
  that is exact, and `json.Number` only when it would not be. `MaxFields` is 5000 and
  `MaxVariableNodes` 100000.
- A plan's attribute line records the value it expects, and `Up` refuses a mismatch.
- A MySQL table whose model states no collation keeps what the database holds.
- MySQL: a DDL statement refused with nothing changed leaves no dirty version; the new
  `Error` code `retry` replaces `*DirtyError` there.
- A plain index narrowed by a MySQL `DROP COLUMN` is a note, not a refusal.
- In a transaction, `Raw` and `Exec` refuse what would end it, `COMMIT`, `ROLLBACK` and the
  like (`ErrTxControl`), and MySQL DDL, which commits implicitly.
- `Up`'s history checksum ignores `velox:allow` and `velox:drops` lines, so accepting a loss
  for one environment does not strand another that already applied the file.
- `velox:drops` names the attribute it accounts for, on every dialect. `Up` compares MySQL
  `MODIFY`/`CHANGE`, PostgreSQL retype defaults and remade indexes with the database.
- `Up` refuses an in-memory SQLite database, whose tables would be gone with its connection.
- GraphQL: `MaxCost` is 150000. Under `Setup`, validation offers no suggestions and returns
  at most 100 errors. Null on a Nillable update field is refused, naming `clear<Field>`.
  `FromJSON` is strict, and a hook sees `json.Number`. `Uint64` input stops at 2^63-1.
- `All` allocates at most 16 rows ahead of `Limit`, then eightfold as they fill.
- `GeneratedCodeIsVersion7` and `veloxgql.GeneratedCodeIsVersion8`: regenerate.
- `velox.Desc`, `ColumnDesc` and `EdgeDesc` are `Descriptor`, `ColumnDescriptor` and
  `EdgeDescriptor`; velox-gen writes `<entity>Descriptor()`.
- A zero `Default` (`false`, `0`) on a non-Nillable bool or number is allowed. A non-zero
  one stays refused, and the message names the ways out.
- NULL sorts as the largest value on every dialect: last ascending, first descending.
  On MySQL and SQLite this changes the order of NULL keys in `Order` and `GroupBy`.
- GraphQL: `field.Strings`, `Ints`, `Floats` and `Bools` are lists (`[String!]` and so
  on), not `JSON`, in output and input. Filters are unchanged.
- A `Uint64` input over 2^63-1 says database/sql cannot send it.
- velox-graphql: `Create<T>` takes `set func(*T)` when its create input leaves out a
  required field with no Default, so the resolver sets it (before, its zero was stored).
  Errors name only the input's own fields. Update such resolvers when you regenerate.
- veloxgql: each `Contains`, `HasPrefix` or `HasSuffix` predicate and each `has<Edge>With`
  costs 2000, so floods of table scans are refused by the default `MaxCost`.
- `velox.Connect` turns SQLite foreign keys on for every connection unless the DSN sets them.
- On MySQL, a tenant-scoped upsert that leaves a generated integer key at zero is no longer
  refused with `ErrTenantUpsert` when every unique index holds the tenant column.
- migrate: `add-required-column` suggests a Default or Optional.
- migrate: a dirty version left with a statement under way (MySQL, CONCURRENTLY,
  multi-statement units) leads its refusal and `status` with checking whether the statement
  took effect, then `resolve -statements`, then `-resume`.
- migrate: PostgreSQL's migration lock is keyed by the history table's schema and name.
  Upgrade together all instances that migrate one database.
- migrate: a MySQL rename blocked by a trigger says to drop and recreate it by hand; a
  set-not-null review comment sits over the online unit that sets NOT NULL.
- migrate: a PostgreSQL online file's statements other than `CONCURRENTLY` and
  `VALIDATE CONSTRAINT` wait for the configured `lock_timeout`; a lock timeout or deadlock
  at a unit's head is retried by the next `apply` without `-resume`.
- migrate: a foreign key or check a plan replaces is added back `NOT VALID` in the main
  file; only `VALIDATE` runs online.
- GraphQL: explicit `null` on a Nillable field with a `Default` is refused on create.
  `@skip` no longer lowers the rows charged, and a mutation's cost counts its statements.
  GraphiQL has its own budget, enough for 150 rich entities: raise
  `MaxIntrospectionCost` for a larger schema.
- MySQL `Up` runs in strict mode, in its own session only.
- GraphQL: a Required unique edge is `T`, not `T!`, since a policy can hide its
  target. `or: []` matches nothing. `Create<T>` refuses an explicit zero on a field with a
  `Default`. `scope.ErrNoScope` presents as `UNAUTHENTICATED` and `scope.ErrMismatch` as
  `FORBIDDEN`.
- GraphQL: `Cost` charges each statement, the size of inputs and what introspection
  returns, so costs change. A very large schema may need a higher `MaxCost` for GraphiQL.
- `mixin.Time`'s SQLite default now writes velox's time format: run
  `velox-gen migrate diff`. Rows already written keep their text.
- `TxRetrySerializable` backs off between attempts.
- The module path `github.com/syssam/velox` used to hold an older, different project,
  whose `v0.x` versions stay in the Go module proxy. Code written for it does not
  build against this one.
- Generated code is version 6, and velox-graphql's is version 3. Regenerate.
- Schema and migrations: table, index, unique and foreign-key names follow ent's, so
  an ent database baselines without renames. A name may differ from one that the older
  project wrote, so read the first `migrate diff` before you apply it. Foreign keys take
  ent's defaults: a one-to-one key is unique, and a NULL-able key is ON DELETE SET NULL.
- On SQLite, every time argument is written as UTC text of one width, so times compare
  by the instant and read back in UTC. Rows already stored with another zone keep their
  old text: rewrite them, or they still compare as that text.
- A row kept from a to-many `With` or `LoadEach` keeps alive the rows it was loaded
  with, which share its arrays. Copy it to keep it alone.
- `Open` and `OpenShards` panic on an unknown dialect. `Connect` returns an error.

### Removed

- `velox.NoFilter()`, which was `velox.Allow()` under a second name. A
  `WithFilter` filter returns `velox.Allow()` for an entity it does not
  restrict.
- One spelling of each schema option is kept, `entsql`'s where ent has
  one; a port that names a removed one does not compile, and
  `docs/coming-from-veloxe.md` and `docs/coming-from-ent.md` name each
  one's replacement. Gone: `schema.Table`, `TableIn`, `Collation`, `Check`
  and their annotation types (write `sqlschema.Table`, `SchemaTable`,
  `Collation`, `Checks`); `index.Builder.Where` (write
  `Annotations(sqlschema.IndexWhere(...))`); `field.SchemaDefault` (write
  `Annotations(sqlschema.DefaultExprs(...))`); `field.Postgres`, `MySQL`
  and `SQLite` (write package `dialect`'s). The `sqlschema` options
  velox-gen refused are gone with their refusals: `Check`, `Size`,
  `ColumnType`, `OnDelete`, `OnUpdate`, `Charset`, `Skip`,
  `IncrementStart`, `WithComments`, `CascadeAction` and its constants,
  and `IndexAnnotation`'s `StorageParams`, `Prefix`, `PrefixColumns` and
  `IncludeColumns` with their constructors.
- velox-gen's warning about a file of yours in `-out/entity` or
  `-out/runtime`, where a velox-gen before 1.0 wrote. A run still removes
  the generated files it no longer writes, there as anywhere below `-out`.
- `schema.Package`, `schema.VeloxeNames`, `load.MarshalPackage` and
  `ddl.VeloxeSymbol`: their index naming is velox-gen's default (ADR 0090).

- `gqlgraph.AsString`, `AsInt`, `AsFloat`, `AsBool`, `AsUUID` and the
  `Integer` constraint. Generated code stopped calling them when its inputs
  took typed receivers, and nothing else did.
- velox-graphql's Node: the `Node` interface, the `node` and `nodes` queries,
  `gen.Node`, `gen.Nodes`, `IsNode`, `GlobalID` and `node_gql.go`; and
  `veloxgql.Noder`, `EncodeID`, `DecodeID`, `IDType`, `ErrBadID` and
  `ErrWrongIDType`. Drop the `Node` model and the `Node` and `Nodes`
  resolvers (decision 0061).

- `velox.NotLinkedError` and `MetaOnce`'s retry of a build that panicked with one: no
  package binds gen late (ADR 0039). A failed build is the entity's error on every use.
- `Descriptor.Name`: an entity's name in errors is its Go type, `gen.User`, as no alias
  stands between them now (decision 0037).
- `graphql.MutationDelete` and the generated `deleteX` mutation and `gen.Delete<T>`
  (decision 0033). A delete is the application's: declare `deletePost(id: ID!): ID!` and
  write its resolver, as `examples/graphql` does:

  ```go
  key, err := veloxgql.DecodeID[int64](id, "Post")
  if err != nil {
  	return "", err
  }
  db, err := veloxgql.DBFromContext(ctx)
  if err != nil {
  	return "", err
  }
  n, err := db.Delete[gen.Post]().Where(gen.Posts.ID.EQ(key)).Exec(ctx)
  if err != nil {
  	return "", err
  }
  if n == 0 {
  	return "", &gqlerror.Error{Message: "no Post has this ID", Extensions: map[string]any{"code": veloxgql.CodeNotFound}}
  }
  return id, nil
  ```

  velox-gen's join tables delete their rows with the row (ON DELETE CASCADE) where the
  database enforces foreign keys; elsewhere delete them in the same transaction.
- `velox.Explain`, `privacy.And`, `privacy.Or`, `privacy.Not`,
  `privacy.WithTrace`, `privacy.TraceEntry` and `generate.Config.FieldOrder`
  (ADR 0097, 0102): none has a caller, and ent has none. For a plan, run
  `EXPLAIN` on `q.SQL(ctx)` through `db.Rows`. `CREATE TABLE` follows the
  schema's declaration order, so declare the fields in the order wanted.

### Fixed

- A HAVING that tests a GROUP BY key written as an expression runs: on
  PostgreSQL a key with a bound value is written with the key's
  placeholders, where a copy failed with 42803; on MySQL the key is
  written as `MIN` of it, where it failed with error 1054, bound or not.
  On MySQL, SELECT DISTINCT ordered by a selected expression runs,
  written as `ANY_VALUE` of it, where it failed with error 3065 or 3066
  (ADR 0138).
- mysql: a term with a bound value that is selected or ordered by and
  repeats a GROUP BY key is written as `ANY_VALUE` of it, so
  only_full_group_by takes it; it failed with error 1055. Under
  `WITH ROLLUP` such a statement fails before it is sent (ADR 0129).
- sqlite: the sum and average of a decimal column are rounded to 15
  significant digits, the digits SQLite stores of a decimal, so 0.1 + 0.2
  is 0.3 in `Sum`, `SumBy`, `Avg` and a `Sum()` term, not
  0.30000000000000004; a sum of integers stays exact (ADR 0126).
- sqlite: a value of a decimal column's Go type, such as a
  `decimal.Decimal`, bound in an `Expr`, `Calc` or `SetExpr` is a number,
  so `Expr("? > ?", X.Amount.Sum(), decimal.NewFromInt(1))` compares as
  one; it was text, false for every group (ADR 0126).
- postgres: a term with a bound value that is selected, grouped and ordered
  by reuses its placeholders, so PostgreSQL sees one expression; it failed
  with 42803 "must appear in the GROUP BY clause" (ADR 0128).
- An insert's `SetNull()` of a nullable column with a default stores
  NULL. Before, the insert left the nil column out and the column's
  `DEFAULT` (a constant `Default`'s, or a declared one) was stored. A nil
  field no setter names, or only a nil `SetPtr`, still gets its default, as
  in ent. velox-gen's sentence for such a field names `SetNull` (ADR
  0124).
- A statement on a Tx while another goroutine's statement on it is being
  sent fails, and the Tx then rolls back with an error that says a Tx is not
  safe for concurrent use. Before, both ran and raced on the transaction's
  state.
- On MySQL an InsertAll's keys are `@@auto_increment_increment` apart, read
  once per DB before the first multi-row INSERT. They were taken as
  consecutive, which a step other than 1 made wrong.
- On PostgreSQL a raw text in a Tx that holds more than one statement is
  followed by a savepoint probe, and one that ended the transaction breaks
  the Tx. Before, pgx and lib/pq ran a trailing COMMIT unseen, and later
  statements committed alone.
- GraphQL: a connection ordered by `graphql.OrderVia` reads the edge with a
  LEFT JOIN, so the order no longer drops a row whose target is missing or
  hidden, nor changes `totalCount`, which told a caller how many rows a
  policy hid. Such a row sorts as NULL: last ascending, first descending.
  `After` of a `LeftJoin` pages from a row with no joined entity as NULL,
  where it failed.
- GraphQL, compat=veloxe: a where argument has a budget of 10 times
  `Limits.MaxFilterNodes` (10000 by default), where it had none; and any
  where argument's budget is held to 30000, so a huge `idIn` is
  `ErrFilterTooLarge` (BAD_USER_INPUT), not the driver's bind-parameter
  limit as an internal error.
- GraphQL, compat=veloxe: `nodes(ids:)` reads each type of Node with one
  `IN` query, as entgql does, not one query per ID per type; each type's
  rows batch their edges; more IDs than `Limits.MaxPage` is
  `ErrPageTooLarge`; and the cost model charges it by the number of IDs,
  and `node`/`nodes` 100 for each type of Node.
- GraphQL, compat=veloxe: `node(id:)` of an ID that more than one type has
  (`gqlgraph.ErrAmbiguousNodeID`) reaches the client as BAD_USER_INPUT, not
  INTERNAL_SERVER_ERROR.
- veloxotel: a failed statement's or transaction's span no longer holds the
  error's text, which can quote a row's values (MySQL's "Duplicate entry
  'alice@example.com'"). Its status description is error.type, and its
  exception event has exception.type and no exception.message.
- A column handle made by hand with an index its entity lacks, such as
  `NumberCol{I: 42}`, fails the statement with an error naming the column.
  It panicked in the renderer.
- `IDs[K]` of a key whose field is not a K names the field's type
  (`int64`), not a pointer to it (`*int64`).
- `Expr` and `SetExpr` refuse PostgreSQL's `ts_rewrite` and `ts_stat`,
  which run SQL from a string, and an `@` right before a name, a MySQL
  variable such as `@@version`. PostgreSQL's `@@` and `@>` operators pass
  when a space follows them.
- `velox-gen -check` says why each file is stale: it is out of date, it is
  missing, or it is no longer generated and a run removes it. It said "is
  out of date" for all three.
- velox-gen's check of a behaviour's column and relation names adds "did
  you mean" with the nearest name, as its other unknown-name errors do.
- Rule 38, an Optional foreign key of an edge that is not Required, says
  the fix: make the field `.Optional().Nillable()`, or mark the edge
  `Required()` and drop the field's `Optional()`.
- A field that holds the column of the foreign key velox adds for an edge
  is reported at that edge's line, names the edge and says how to bind
  the field: `.Field("x")` on the edge, or an inverse edge on the field's
  schema when the edge is the other schema's to-many edge.
- Every problem velox-gen reports has its file:line: a builder misuse or
  a panic at the line of the method that returned it, an error of the
  schema as a whole at the line of its type, of an implicit foreign key at
  its edge's, and of a join entity at the edge that makes it. About fifty
  errors had none.
- velox-gen runs in a module that vendors it: velox-gen imports
  `schema/load`, which its loader's program needs, so `go mod vendor`
  copies it.
- A GraphQL filter on a foreign key (`xIDIsNil`, `xIDNotNil`, `xID`,
  `xIDIn` and the rest) reads a key whose target a `velox.WithFilter`
  filter hides as null, as `hasX` does; before, only an interceptor or a
  Read policy on the target counted. The statement decides, on the DB that
  runs it, with the new `untyped.Table.ScopedKey` (ADR 0103). A target with
  no row condition keeps the column's predicate alone, with no EXISTS.
- `Having` with a relation (`Has`, `HasWith`, `HasEdge`) or a column
  subquery no longer panics while rendering: the statement fails with an
  error that points to `Where`. A condition by name in `Having`
  (`velox.FieldEQ`) is now resolved, as one in `Where` is.
- velox-gen refuses a field named `edges` on an entity with relations, and
  two schemas whose unexported constants share a name (`Ab` with `col_x`
  and `AbCol` with `x`), instead of writing code that does not compile.
- A GraphQL create input stores a zero value given to a field with a
  Default (`active: false`, `plan: ""`), as decision 0082 says, instead of
  refusing it with BAD_USER_INPUT. `gqlgraph.NotZero` is gone, and
  `veloxgql.GeneratedCodeIsVersion27` replaces Version26: regenerate with
  the new velox-graphql.
- `migrate.Up` refuses a hand-edited SQLite rebuild whose INSERT copies a
  column only by an alias (`NULL AS s`, `'x' s`), fills one column from
  another (two swapped, by name or by position), or copies only some rows
  (`WHERE`, `LIMIT`, a join), unless the file allows the loss.
- `migrate.Resolve` refuses a claim whose file drops a table the database
  still holds, though the schema the claim leads to does not name it.
- An `Expr` refuses the functions that read a table, a file or server
  state without a subquery, such as PostgreSQL's `table_to_xml`,
  `query_to_xml` and `pg_read_file`, and MySQL's `LOAD_FILE`.

- A Rewrite hook that replaces the whole statement (`*st =
  velox.Statement{…}`) no longer drops what velox reads back: an insert
  still gets its generated key and declared defaults (RETURNING on
  PostgreSQL and SQLite, the read by key on MySQL), and an InsertAll still
  sends `DEFAULT` for a cell left to a default, where the Go zero was
  stored.
- `UpdateQuery.One` that moves its row out of an interceptor's view, such as
  softdelete's when it sets `deleted_at`, returns the row it wrote, not
  `ErrNotFound`: its read-back adds no row condition. So does the MySQL
  read-back of the columns an insert left to the database (ADR 0093).

- velox reads `github.com/mattn/go-sqlite3`'s errors, as it reads
  modernc.org/sqlite's: a violated constraint is a `*velox.ConstraintError`,
  `velox.DriverCode` gives SQLite's extended code, and `SQLITE_BUSY` or
  `SQLITE_LOCKED` retries a transaction. Before, the `"sqlite3"` driver's
  errors had no code and were none of these.
- `veloxgql.Limits.MaxResponseBytes` counts a list of nullable strings
  (`[]*string`), a pointer to a list, and lists of `Bytes` or `JSON`. Before,
  each counted 0 bytes.
  `json.RawMessage` counts as written, not as base64, and a type that holds
  itself (`type L []L`) counts 0 instead of overflowing the stack.
- A transaction's own deadline (`velox.TxTimeout`) fails a GraphQL mutation
  with its fields' errors. Before, it was reported as `Limits.Timeout`'s
  `TIMEOUT`, naming the Timeout's duration.
- An operation with `@defer` lets go of its `Limits.Timeout` timer when its
  last response is out. Before, the timer and its callback stayed until the
  deadline, on a websocket as long as the connection.
- `gqlgenx.Cost` counts a mutation's list of input objects, as a bulk
  mutation of the application's takes, as one write for each element. Before,
  the list added no write.
- velox-graphql refuses a `graphql.Type` that does not start with an upper-case
  letter, and a `graphql.FieldName` that starts with `_`. Each names Go types
  or fields too: `Type("error")` hid Go's `error` in the generated package,
  and `FieldName("_title")` made an input field gqlgen cannot set.
- A plugin refuses a model whose references name nothing (a key index past
  the fields, an edge's missing target), with an error naming it, before
  generating. Before, velox-graphql panicked on one.
- The GraphQL description of an `OrderVia` order value says which nodes the
  order leaves out: those without the edge's target, or whose target the
  reader may not see, which the inner join drops (0031).
- velox-gen refuses a mixin whose own `Mixin()` lists mixins. velox reads
  mixins one level deep, so their fields and behaviour, soft delete's
  included, were dropped without a word.
- `veloxgql.Setup`'s websocket limit no longer drops a client whose first frame the
  server read with the upgrade's request. With gqlgen's default websocket library at
  v0.17.95 and v0.17.98, which answers the upgrade before it hijacks, a client that sent
  `connection_init` at once was cut off without a close frame, more often under load.
- A statement, `BEGIN`, `SAVEPOINT`, `RELEASE` or `COMMIT` that fails once its context
  has ended matches the context's error (`context.DeadlineExceeded` or
  `context.Canceled`), or `ErrTimeout` for velox's deadline, whatever the driver
  returned; the driver's error is kept. pgx's database/sql driver reports a statement
  whose context ended before it was sent as `driver.ErrBadConn`, which a transaction
  returned as is, and a socket can time out at the deadline before the context's
  `Done` closes.
- A constraint checked at commit (a deferred one) is a `*ConstraintError`, with no `Entity`,
  as one a statement violates is; it was the driver's error alone. Its message is
  `velox: commit: <kind> constraint <name> violated: …`.
- An Around hook costs two fewer allocations per statement: the outermost hook is called
  directly, and what the innermost `next` records lives in one struct.
- veloxotel: each statement shape's table, summary and success metric set are built once and
  cached (up to 2,048 shapes), as are tag keys' attribute names, and an unsampled span builds
  no end attributes; with an SDK, a one-row read allocates 35 times instead of 42.
  `TestAdapterAllocs` holds the adapter to 4 allocations per statement over a bare hook.
- veloxotel: `Config.TargetFor` is called once per statement and once per transaction (a raw
  SQL span and a transaction called it twice), so it need not be deterministic for a route.
  With `TargetFor` set, `Config.Target` is ignored everywhere, as documented: it no longer
  names a raw SQL span at its start. A failed transaction's span carries the driver's
  code as `db.response.status_code`, as a failed statement's does.
- migrate: on PostgreSQL, a no-transaction unit of one statement commits with its history
  count, so `apply -resume` after a killed run never applies it twice.
- A PostgreSQL `Up` waiting for the migration lock polls outside a transaction, so it no
  longer deadlocks a running `CREATE INDEX CONCURRENTLY` (two replicas migrating at start).
- `docs/migrations-operations.md` covers recovering from a dirty PostgreSQL online file.
- A `NULL` scans into a nil-able Go type, such as `GoType(net.IP{})`, instead of failing
  every page that selects it.
- GraphQL: a foreign key predicate holds the target's policy, and a
  JSON array of the wrong length is refused. The documented worst case is now measured:
  844 MB allocated and a 59 MB response under the defaults with introspection on, as its
  budget adds to `MaxCost`'s, and 583 MB and 47 MB with it off.
- GraphQL: a query or mutation stopped by a limit over websocket or SSE ends its stream,
  where it sent error frames without end and held the handler.
- SQLite: only foreign key orphans a file made fail its migration. Drift reads `timestamp`,
  `time` and similar types in one form.
- Refused before running: `CONCURRENTLY` on a PostgreSQL partitioned table, `INPLACE` on
  MyISAM, and a rename or drop that a trigger function names.
- A request's shape is bounded before validation: spreads, value depth, name length,
  literal size and variable nodes. Attacks that took 2 to 3.5 GB are refused under 21 MB.
- GraphQL: `nodes` and deletes count their real statements; a foreign key predicate holds
  the target's Read policy; introspection switched off is a client error.
- Migrations: MySQL refuses a rename or drop that a view or trigger uses, and a `DROP COLUMN`
  that would shrink an index; SQLite `DROP TABLE` refuses broken dependents and counts
  children still present; `PRIMARY KEY (id DESC)` is refused in a rebuild; the guard resolves
  `search_path` and reads past `-to`.
- SQLite integer `Add` and `Sub` raise on overflow rather than turn the column to REAL.
- Migrations: `Up` refuses what a file would lose unaccounted, on every dialect, including
  columns and constraints added outside the snapshot, collations, and foreign key options.
  A pending generated SQLite file from before this change needs `velox:drops` or an allow.
- Migrations: SQLite rebuilds keep rowids and refuse an unaccounted child on `DROP TABLE`.
  Plans refuse truncating casts, enum arrays and a foreign `CONVERT TO`. `LintFile` reads
  `WITH`, `MERGE` and `UPDATE OR`, and flags a rebuild without `foreign-keys off`.
- SQLite: a transaction rolled back by an interrupt or `INSERT OR ROLLBACK` is broken, so
  later writes fail unsent instead of committing alone. A broken transaction refuses at the
  send, including an Around hook's retry.
- A statement on `db` inside `Tx`'s fn, on a one-connection pool, fails rather than hanging.
- `TxReadOnly` restores `query_only` and works under `_txlock=immediate`. A retry stopped by
  its context matches the context's error. `After` with `Columns` is allowed on `Count`,
  `Exists` and aggregates.
- GraphQL: an empty `Bytes` reads as `""` in `Bytes!`, and a literal `[]` stays `[]`.
- Migrations: `Up` refuses a SQLite rebuild or MySQL `MODIFY COLUMN` that would lose what
  the model cannot hold (`*Error` of Code `unmodeled`); `-- velox:allow unmodeled-loss`
  accepts it. A SQLite rebuild with a dependent view is refused.
- Migrations: SQLite type changes are destructive by affinity. PostgreSQL narrowing fails
  on a long row rather than truncating. Widening a serial widens its sequence. Renaming a
  referenced column in a SQLite rebuild works, as does dropping a foreign key's only MySQL
  index. `WithTarget(0)`, `-to 0` and `resolve -version 0` mean version 0. Allow targets
  round-trip any identifier. `Verify` fails on a stray table. SQLite's `foreign_key_check`
  covers only the touched tables and their children.
- GraphQL: introspection counts toward `MaxCost`. Entities named like introspection types
  build.
- `After` fails when an order column is not selected, rather than losing rows.
- A nested `Tx` that returns an error has undone its work, or has broken the transaction.
- An upsert cannot update a bidirectional one-to-one key.
- SQLite enforces `TxReadOnly` and refuses isolation levels other than Serializable.
- `Raw` in a transaction pins the session. A MySQL deadlock breaks its transaction, so
  later statements cannot autocommit.
- Time fields scan SQLite's text forms.
- A statement inside `Iter` on a one-connection pool fails rather than hanging. A goroutine
  the loop body starts can still block.
- `velox-gen` writes all of its files or none, `-check` reports leftover files, and it
  refuses Go types it cannot write.
- A rollback that velox sends after a transaction function fails is bounded to 5
  seconds, and savepoints keep the transaction's deadline.
- `TxRetrySerializable` does not retry `ErrLocked`.
- A PostgreSQL error is recognized by its `SQLState` method, in any pgx version.
- MySQL `varchar` goes up to 16381, and a default that a decimal column would round is
  refused.
- `velox.WithTags` no longer panics on an odd number of strings: the last key takes the
  value "".
- `veloxgql.EncodeCursor` returns an error instead of panicking, and gqlgen reports a
  cursor that cannot be written on its field.
- In GraphQL, re-pointing a one-to-one edge checks the new child first, and is refused
  over a child the caller cannot see.
