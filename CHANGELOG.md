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
  IDs, keyset connections and mutations in savepoints. `veloxgql.Cost` reads `@skip`
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

### Fixed

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
