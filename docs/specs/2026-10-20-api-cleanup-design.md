# velox2 API cleanup before 1.0 — design

Status: API plans 1–3 implemented · 2026-10-21

The user asked for a review of the whole public API: is it clean, correct, complete, modern and easy to maintain. Three reviews were done, one each for the core package, the schema DSL with velox-gen and the plugin model, and the contrib modules. They are in `docs/reviews/2026-10-20-api-review-{core,schema,contrib}.md`.

This document records what changes, decided by the author (**call**), and in what order. Every change here is cheap now and breaking after 1.0. Additions that break nothing are listed last.

The reviews also agreed on what stays:
- `*DB` is the only handle, transactions included, and builders are values.
- Guards fail closed.
- The typed handles and the `Lazy` convention stay.
- `One`/`Many` with `Get() (…, bool)`; `Iter` as `iter.Seq2`; `SQL(ctx)` on every builder.
- The error model: sentinels wrapped as `velox: <Entity>: …`, plus typed errors.
- `Policy()`/`Hooks()` stay out of schemas.
- ent parity in the DSL.
- velox-gen's file ownership.
- The protoc-style plugin protocol.
- Typed opaque IDs and bound cursors.
- One transaction per mutation.
- otel's API-only dependency.

## 1. Core semantics (breaking)

1. **`Tx` passes its context (call).**
   - The signature becomes `db.Tx(ctx, func(ctx context.Context, tx *DB) error, opts...)`.
   - The ctx it passes carries the transaction's deadline and what the `TxAround` hooks added.
   - A statement run on a `*DB` that is not this transaction, under a ctx marked as belonging to a transaction, fails with `ErrOutsideTx`. This catches the classic mistake of using `db` instead of `tx` inside the function.
2. **No restriction is said, never implied (call).**
   - A zero `Pred` returned by a policy's `Read` or `Write` is an error naming the entity.
   - `velox.Allow[E]()` says "no restriction".
   - The policy signatures stay `(ctx)` / `(ctx, *E)`: a policy states its rule as a predicate (parent checks through `Has`/`HasWhere`), so it needs no `*DB`.
3. **A dry run sends no write (call).**
   - Under a context marked by `SQL(ctx)`, `Exec`, `Save` and `Tx` on any `*DB` fail with `ErrDryRun` before sending, so a hook's audit write does not happen during a dry run.
   - Reads still run.
   - `InsertQuery.SQL` documents that it fills defaults into the caller's structs.
4. **Names (call).**
   - `ConstraintUnique`, `ConstraintForeignKey`, `ConstraintNotNull` and `ConstraintCheck` replace `Unique`, `ForeignKey`, `NotNull` and `Check`.
   - `TxTimeout`, `TxReadOnly` and `TxIsolation` replace `Timeout`, `ReadOnly` and `Isolation`.
   - `ReadPrimary(ctx)` replaces `Primary(ctx)`.
   - The godoc groups options (`With*` on `Open`) apart from context decorators.
5. **`Save` is a builder (call).**
   - The signature is `db.Save(&x, col, more...) SaveQuery[E]`, with `.Where(...)`, `.SQL(ctx)` and `.Exec(ctx)`.
   - Naming no column is a compile error.
   - `Where` gives optimistic locking: `Where(U.Version.EQ(v))`.
6. **Raw SQL with a reason (call).**
   - `db.Exec(ctx, reason, sql, args...) (int64, error)`.
   - `db.Raw[E](reason, sql, args...)`, run with `All`, `One` or `Iter`, scanned by column name.
   - Both go through the Around hooks, deadlines and routing. `Raw` runs as `OpRawQuery`, a read; `Exec` runs as `OpRawExec`, a write that goes to the primary, pins the session and fails under a dry run. Two Ops let a hook, the router and otel tell a raw write from a raw read before it runs.
   - A reason that is only white space is refused, as an empty one is, by `Raw`, `Exec`, `AllTenants` and `SkipPolicy` alike.
   - Both skip the tenant scope and the policy: `Statement.TenantBypass` and `Statement.PolicyBypass` hold the reason.
   - `RawQuery.MaxRows` sets the row cap for a raw read.
   - Neither applies tenant scoping or policy, which is why a non-empty reason is required, as `AllTenants` and `SkipPolicy` require one.
   - They are the escape hatch for locking, CTEs and anything the builder cannot express.

## 2. Generated-code contract

1. **One statement of what generated code may rely on (call).**
   - The core's generated-only names keep their place: `Desc`, `ColumnDesc`, `Relation`, `EdgeTo`, `EdgeThrough`, `Meta`, `MetaOnce`, `Bind*`/`Bound*`.
   - The package doc gains a section saying these change only together with `GeneratedCodeIsVersionN` and are outside semver.
   - Each of their docs opens with "For generated code".
   - godoc cites no internal spec ("design §3 rule 7" goes).
   - A separate `veloximpl` package was considered. It is not taken, because of the alias layer's compile cost and the core's §3 rule 2.
2. **The version guard moves to 4** for the renames in §1 and the nullability change in §3.
3. **GraphQL gets the same guard (call).**
   - `veloxgql.GeneratedCodeIsVersion1` is referenced by every `gql_*.go`.
   - The helpers that only generated code calls move to `contrib/graphql/veloxgql/gqlimpl`, covered by the guard rather than semver, as protobuf's `protoimpl` is.
   - `Paginate`, `CheckDepth` and `CheckWidth` stop being public.

## 3. Schema DSL and velox-gen

1. **One nullability rule, old velox's (call, corrected 2026-10-20).** It follows old velox (`compiler/gen/type_field.go`, `Column`):
   - `Nillable()` means a pointer Go field, a NULL-able column and a `Null*` handle. It is valid on its own.
   - `Optional()` means the field need not be given on create. The column stays NOT NULL and the Go field a value type. An unset field writes its `Default`, or its zero value. Validators skip the zero value, which means unset.
   - The exception is an `Optional` JSON field: its column is NULL-able, because a slice or map has no natural zero value. NULL decodes to nil.
   - `Optional().Nillable()` means both.
   - A `Default` never changes the Go type.
   - These are refused:
     - a `Default` or `DefaultFunc` on a bool or number field that is not `Nillable` (an insert fills either into a zero field, so false or 0 could never be stored);
     - an `Optional` enum, bytes or time field that has neither a `Default` nor `Nillable` (MySQL's strict mode refuses the zero time);
     - an `Optional` edge field that is not `Nillable`.
   - A field that is unique only when present needs `Nillable`.
   - In the plugin model, `Pointer` is Nillable, unless the Go type can already be nil (a slice, a map), and `Nullable` is Nillable || (Optional && JSON).
   - Two earlier versions were withdrawn:
     - the first tied Nillable to Optional;
     - the second made every Optional column NULL-able, which is ent's rule but not old velox's.
2. **velox-gen joins the root module (call).** It imports `internal/gen`, so a separate module breaks when velox2 is upgraded alone. `go get -tool github.com/syssam/velox2/cmd/velox-gen` keeps working.
3. **Plugin protocol 2 and model 2, one bump, before gRPC (call).**
   - The request carries `Generator` and `Options` (`-plugin velox-grpc:go_package=x/pb`).
   - The response carries `Warnings`.
   - File paths may contain directories below `-out` (no `..`), and `.proto` is allowed.
   - `plugin.Main(supports, gen)` decodes the request, checks the version literals the plugin was written for, and encodes the response, so the check is no longer vacuous.
   - The model:
     - `Field.Imports` replaces `Entity.Imports`;
     - `Entity.TableSchema` and `Indexes` are added;
     - `Field.Size`, `Default` (literal), `Checks` and `SchemaType` are added;
     - `type EdgeKind string` gets constants;
     - only `Pointer` and `Nullable` remain of the four nullability flags;
     - annotations get lower-case JSON tags.
   - Naming moves into the `plugin` package as one frozen, standard-library-only set: `Snake`, `Plural`, `Singular`, `GoName` (acronyms). velox-gen and every plugin import it, with golden name tables, and the copy in gogen goes.
4. **CLI (call).**
   - An explicit `generate` subcommand is accepted.
   - `version` is added.
   - Each subcommand has its own flags.
   - Stale files exit 3, not 1.
   - `describe -json` prints `plugin.Model`.
5. **`schema/load` exports only `Marshal`.** Its model types move into `internal/gen`.
6. **The DSL itself.**
   - Builder types are exported (`field.StringBuilder`, `NumberBuilder[T]`, `edge.AssocBuilder`, …) so godoc lists their options.
   - Typed constructors: `field.Number[T]`, `field.EnumOf[T]`, `field.UUIDOf[T]`, and a typed `Time.Default`.
   - Contradictions are refused: `Min` above `Max`, `Immutable` with `UpdateDefault`, `Unique` or an index on a JSON column, duplicate indexes and storage keys.
   - `Bytes.MaxLen` sets `Size`.
   - Errors carry `file:line` (`runtime.Caller` in each constructor), and all builder errors are reported together.
   - `Float64` goes; ent has only `Float`.
   - `edge.OnDelete(Cascade|SetNull|Restrict)` is added.
   - Dialect keys are exported and checked (`field.Postgres`, …).
   - The index `Where` doc is fixed: SQLite has partial indexes too, and MySQL does not.

## 4. GraphQL runtime

1. **Limits come from the context (call).** A `veloxgql.Limits` struct and `LimitsFrom(ctx)`, set by the extension. Generated code reads them from the context. `LoadedConnection` takes a ctx.
2. **The extension (call).**
   - `veloxgql.NewExtension(db, opts...)` replaces `Handler(db)`.
   - Options:
     - `WithLimits`;
     - `WithTxOptions`, which refuses `TxRetrySerializable`: gqlgen builds the response once and cannot run it again;
     - `WithTxFilter(func(op) bool)`, so mutations that only call other services hold no pooled connection.
3. **Errors safe for clients (call).**
   - veloxgql's errors implement gqlgen's `ExtendedError` with a `code`.
   - `veloxgql.ErrorPresenter(logf)` passes coded errors through and replaces everything else with "internal error", which it logs.
   - The example uses it. This lands before mutations, because queries leak driver and tenant text today.
4. **Composable connections.** `XConnectionOf(ctx, q, …)` is exported, so an application can serve `myPosts` from its own query.
5. **Directives reach where inputs.**
   - A field's `graphql.Directives` are copied onto its where-input fields.
   - `OrderField` on such a field is refused unless the annotation opts in. Otherwise a protected field's value could be bisected.
6. **`Pred(ctx)`.** The depth travels in the context, as the node budget already does.
7. **CI:** a job against `gqlgen@latest`.

## 5. otel

1. **One way to configure (call).**
   - `veloxotel.Options(cfg)`, with `Config.Callers`.
   - `Around`/`TxAround` stop being exported.
   - Config booleans share one naming style.
2. **Instrumentation scope:** the version and the semconv schema URL are set.
3. **Timing:** `db.client.operation.duration` under `Iter` excludes the loop body.
4. **Attributes:** `db.namespace`, `server.address`/`server.port`, and `db.response.status_code` (the core exposes the driver's code on its errors).
5. **The module path becomes `contrib/veloxotel`,** matching the package name.

## 6. Additions (not breaking)

- `First(ctx)` and `Exists(ctx)`.
- `Rel.Of(owners...) Pred[T]`, to query from loaded entities' edges.
- `Sum`/`Avg`/`Min`/`Max` as functions over a `Query` and a number handle.
- Locking reads: `ForUpdate`/`ForShare`/`SkipLocked`/`NoWait`, refused outside a `Tx` and on SQLite.
- `Mutation.Conds()` and typed `Handle.ValueIn(m)`, for soft delete and audit.
- `m.Affected()`.
- A nested `Tx` given options returns an error.
- Docs: `DB` is safe for concurrent use; `Tx` re-panics; sentinel docs follow the Go style.
- `apidiff` in CI once 1.0 is cut.

## 6a. Rulings made while implementing API plan 1

Added 2026-10-20. Each is a decision taken during the plan, and it stands.

1. `TxInfo.Context` is removed. A statement's span is parented on the statement's own context, which inside `Tx` is the transaction's.
2. `db.Tx` on a non-transaction `*DB` under a context marked by a transaction fails with `ErrOutsideTx`: a nested transaction is `tx.Tx`. The check compares transactions, so a statement on transaction B's `*DB` under transaction A's context fails too. A savepoint shares its transaction's identity. A mark whose transaction has ended counts as no mark.
3. `velox.WithoutTx(ctx)` returns a context without the mark, keeping tenant, tags and spans. It is the explicit way out for work that must not join the transaction, such as an audit row that survives a rollback.
4. A zero `Pred` from a policy is a plain error naming the entity and the policy (`Read` or `Write`), not a `*PolicyError`, so it does not match `ErrDenied`: it is a programming error, not a denial. A policy that returns an error with it denies the statement with that error.
5. `SQL(ctx)` treats an `ErrDryRun` returned by a hook as success once the statement was rendered (an audit write after `next`), and returns it when nothing was rendered (a hook that wrote before `next`). An error that joins another error to `ErrDryRun` is returned. A write that is both under a dry run and outside its transaction fails with `ErrDryRun`, with hooks or without.
6. Nullability follows old velox exactly (§3.1). Two earlier rulings that went the other way (a pointer for every `Optional`, then a NULL-able column for every `Optional`) were withdrawn.
7. `GeneratedCodeIsVersion4` stays at 4: version 4 is not released, and the nullability rules are additive.
8. GraphQL create inputs are not required when the field is `Optional` or `Nillable`. Update inputs offer `clear<Field>` for a *nullable* field, and the `IsNil`/`NotNil` filters follow the same flag, because a value handle cannot write NULL. *(Corrected 2026-10-21 by API plan 2: the first wording said `Nillable` only. `clear<Field>` follows the model's `Nullable`, so an `Optional` JSON field, whose column is NULL-able, gets it too; its handle is `NullJSONCol`, which has `SetNull`. `IsNil` and `NotNil` follow `Nullable` too, but a JSON field has no predicates at all, so in practice they appear on `Nillable` fields only. See `contrib/graphql/internal/sdl/where.go` and `input.go`.)* *(Corrected 2026-10-22 by GraphQL plan 3, Task 4: an `Optional` JSON field's handle is not `NullJSONCol`. Only `Nillable` makes a pointer and a `Null…` handle; an `Optional` JSON slice or map keeps `JSONCol`, which has no `SetNull`. Its column is NULL-able all the same: velox-gen marks its `ColumnDesc` `Nullable`, so a nil slice or map binds SQL NULL, and `clear<Field>` writes it with `Set(nil)`. A NULL-able column must be representable in Go, so velox-gen refuses an `Optional` JSON field of a struct or scalar type that is not also `Nillable` (rule 48: "an Optional JSON field of a non-nil-able type needs Nillable"). The plugin model's `Nullable` is `Nillable || (Optional && a JSON field whose Go type holds nil)`, nil-holding meaning a slice, map, pointer, interface, func or chan, as velox-gen's `nilKinds` has it. A `Nillable` bytes field's `ColumnDesc` is `Nullable` too, and its nil binds SQL NULL.)*
9. The documents' history is kept. Plans and reviews record what was decided then; older specs get a dated note where a rule changed.
10. `Or(Allow(), x)` is `Allow` only when no other kid is or holds the zero `Pred`, an invalid condition or a `Has`; otherwise the `Or` keeps every kid, so the statement still fails on a bad one (a `Has`'s relation resolves only when the statement runs). It matches every row either way.

## 6b. Rulings made while implementing API plan 2

Added 2026-10-21. Each is a decision taken in the plan or while executing it, and it stands.

1. Model 2 drops `Nillable` and keeps `Optional`. Since §3.1, `Optional` is no nullability flag: it says a field need not be given on create, which GraphQL's create input and gRPC's create request need. `Pointer` and `Nullable` are the nullability flags. This departs from §3.3's "only `Pointer` and `Nullable` remain of the four".
2. The load model's types live in `internal/gen/loadjson` (version 2); `schema/load` exports only `Marshal`.
3. Builder misuse travels in the model's `Errors`, all of it, not the first, and prints in the format of rule errors, with positions relative to the working directory. Positions stay out of the Graph's JSON, so the model hash, every generated file and `describe -json` never depend on the checkout path.
4. `DefaultFunc` and `UpdateDefault` keep taking `any`. The typed forms are `Number[T]`, `EnumOf[T]`, `UUIDOf[T]` and `Time.Default`. A time GoType's default must convert from `time.Time`, and the generated code converts it.
5. Contradictions inside one builder are builder errors; contradictions across elements are numbered rules 41–47, and rules 37, 39 and 40 are one table.
6. A duplicate index means the same columns in order, the same `Unique` and the same `Where`. Rule 44 compares every index's effective name (the explicit `StorageKey` or the derived one) across the whole graph, among the tables of one database schema (`schema.TableIn`), because PostgreSQL index names share one namespace per schema. *(Wording corrected 2026-10-21 by the final review: names must be unique within one database schema, not across all of them.)* Derived names are `<table>_<cols>[_unique][_<fnv32 of Where>]`: `_unique`, not `_key`, which PostgreSQL uses for a column's UNIQUE constraint. This changed the model hash lines of generated code, which compiles unchanged (the version stays 4).
7. `OnDelete` is a method on both edge builders (`Cascade`, `SetNull`, `Restrict`), refused on M2M; `SetNull` needs a NULL-able key; both sides of a pair must agree. It is recorded for the migration planner and stays out of the plugin model.
8. Dialect keys equal the core's names (`postgres`, `mysql`, `sqlite`); an unknown key is a builder error. Refusing `Where` on MySQL waits for the migration planner.
9. `clear<Field>` follows `Nullable` (see §6a ruling 8).
10. Model 2's `Field.Default` is set only for bool, number, string and enum fields (the JSON of the base value). Bytes, JSON, other, and any type with its own `MarshalJSON` or `MarshalText` get none, and a plugin treats the database default as unknown.
11. Model convention: named elements (`Entity`, `Field`, `Edge`, `Index`) are pointers; small leaf records (`Check`) are values.
12. A response echoes `plugin_protocol`, which is how velox-gen tells a protocol-1 plugin apart and names it in the error. `plugin.Run` is exported beside `Main`, so plugins test without a subprocess; `GenerateFunc` returns files, warnings and an error; a panic in it becomes the response's error, without the stack. `Request.Generator` is `velox-gen <velox2 module version>`.
13. Plugin options are `-plugin name:key=value,key=value`; a malformed option is a usage error (exit 2).
14. Plugin output: paths fold case over the whole path; the stale-file sweep is recursive, skips `.`- and `_`-prefixed directories, and removes only the directories it emptied. *(Added 2026-10-21 by the final review: it also skips a directory that holds a `go.mod` or velox-gen's own `velox.go` with the header, which is another module or another run's output, such as `-out ./gen/b` beside `-out ./gen`; a plugin path named `velox.go` below the root is refused, so that mark stays unambiguous.)* velox-gen never renames a directory: a case-only clash with a directory it does not empty stops the run and names both spellings. Symbolic links are refused. Warnings print on one line with control characters escaped and never fail a run. A write that fails part-way leaves `-out` partly updated, and running again restores it.
15. Exit codes: 0 success, 1 error, 2 misuse, 3 stale (verify's stale plugin file included). No subcommand means `generate`. `version` prints `velox-gen <v> (generated code 4, plugin protocol 2, model 2)`.
16. A flag after the package is accepted (velox-gen re-parses the remaining arguments, as `go test` does); a second positional exits 2. A bare first word that is no subcommand exits 2, and the message says to write `./word` when a directory of that name exists.
17. The naming golden has no `-update` flag: editing it by hand is the deliberate act a frozen table needs.
18. Left out, as §3.3–3.6 do not ask for them: "did you mean" for names; `Through`, `Deprecated` and `SchemaDefault`; `schema.Handles`; dropping the model hash; enum `String()`/`Valid()`. The review's Could list keeps them.

## 6c. Rulings made while implementing API plan 3

Added 2026-10-21. Each is a decision taken in the plan or while executing it, and it stands. Where one differs from §2.3 or §4, it is the rule; §4 above is the proposal, as §6a's rule 9 says of older documents.

1. The generated-code guard is `veloxgql.GeneratedCodeIsVersion1`, not a `gqlimpl` constant. Generated files reference it as `const _ = veloxgql.GeneratedCodeIsVersion1`, as the core's files do; it stays 1 until the first release. The core's `GeneratedCodeIsVersion4` stays 4.
2. `gqlimpl` holds every name only generated code calls. `Paginate`, `CheckDepth` and `CheckWidth` are unexported there, as are the cursor's `Check`, `CursorOrder` and `CursorValue`. `Cursor`, `CursorTerm`, `EncodeCursor` and `DecodeCursor` stay in `veloxgql`: the cursor is a scalar the application sees. Base values are unwrapped by the callers: `internal/shared` converts base types only, and `veloxgql` and `gqlimpl` each unwrap their own handles with a compile-checked switch, with no reflection and no path string.
3. `WithDB` is removed. `(*Extension).Context(ctx)` puts the DB and the limits in a context for generated code called outside a gqlgen server, and panics with the error `Validate` returns.
4. A zero `Limits` field means the default; a negative one is refused by `Validate` ("must not be negative"). `DefaultLimits()` is a function, so no mutable package state is left. `LimitsFrom` gives the defaults for a context without limits, never zeros.
5. gqlgen v0.17.86 has no `ExtendedError`. veloxgql's coded errors have an `Extensions() map[string]any` method in its shape, and `ErrorPresenter` reads it; gqlgen's default presenter does not, so an application gets codes only with `ErrorPresenter`. The codes are Apollo's (`BAD_USER_INPUT`, `FORBIDDEN`, `UNAUTHENTICATED`, `INTERNAL_SERVER_ERROR`) and `NOT_FOUND`, which is defined now because GraphQL plan 3 adds no public API to `veloxgql`. A core error gets a fixed message, so no entity, constraint, tenant or driver name reaches a client.
6. The presenter presents a `*gqlerror.Error` that wraps an error as that error, keeping its path and locations; an `errors.Join` of a coded error and an internal one shows the coded message only, and is logged for the second. A nil `log` logs with `slog.ErrorContext`, with the field's path and the operation's name, so an internal error is never silent. A panic reaches the presenter only through the server's recover function (`SetRecoverFunc`).
7. A list edge over `MaxList`, a timeout and `ErrNoDB` are internal errors: the client cannot fix them, and the log names the edge for the operator. The node budget has its own sentinel, `ErrFilterTooLarge` ("filter is too large"); `ErrFilterTooWide` keeps lists over `MaxFilterWidth`.
8. The core gains `velox.TxRetries(opts ...TxOption) int`, because a `TxOption` is an opaque function and veloxgql cannot otherwise see a retry. `WithTxOptions(velox.TxTimeout(t), velox.TxRetrySerializable(3))` is refused naming `TxRetrySerializable`; `TxRetrySerializable(3), TxRetrySerializable(0)` passes, since the last wins and asks for no retry.
9. `WithTxFilter`'s function takes the context and the operation definition (`func(ctx, *ast.OperationDefinition) bool`), not §4.2's `func(op) bool`, which cannot see the request's caller. A nil function means every mutation runs in a transaction. A mutation the filter leaves out runs under `velox.WithSession(ctx)`, so its reads after its own writes go to the primary (read-your-writes); its statements are not atomic and its resolvers are not serialized.
10. A where input's `Pred(ctx)` takes the context and nothing else (§4.6). The depth and the node budget travel in an unexported context key; a context without them starts a where argument at depth 0 with a full budget, so two calls of `Pred(ctx)` on one context each have their own budget.
11. `<Plural>ConnectionOf` is written for every Node entity, not only those with a `QueryField`; root functions and edge methods call it. The connection clears the base query's Offset and replaces its Limit. **This supersedes the plan's ruling 12:** `velox.Query.Ordered() bool` (public, additive; the core's version stays 4) exists, and an ordered base query is refused with an internal (uncoded) error naming the connection: the connection orders the query, and a base `OrderBy` would corrupt pages 2 and later. A `Columns` or `Omit` that drops the key or an order column breaks the cursors; it is documented on the function, not refused.
12. Directives are copied onto every where-input field that filters on the element: a field's predicates, an edge's `has`/`hasWith`, and a foreign key's ID predicates (the key field's, then the edge's, each name once). A directive on a filterable field or edge must therefore also be declared on `INPUT_FIELD_DEFINITION`: **breaking** for an application that already has one.
13. An application directive is refused on a foreign-key field ("put it on the edge") and on a Node's primary key (its id is readable through `node` and the cursors): a guard that guards nothing is a false promise. A non-Node single key is an ordinary field and may carry one. GraphQL's own directives (`@deprecated`) guard nothing and are always allowed.
14. The `OrderField` refusal counts application directives only. The opt-in is `graphql.OrderUnguarded()`.
15. The `gqlgen@latest` job is `scripts/gqlgen-latest.sh`, which works in a copy of the tree; CI runs it as its own job and `ci-local.sh` runs it last. It fails red rather than `continue-on-error`. The supported range is "v0.17.86 and later". The example's tests ignore the `locations` of an error, which gqlgen v0.17.97 adds: `ErrorPresenter` keeps them, since a client can use them.
16. Left out: mutations (GraphQL plan 3); the first/last-aware cost limit and `LoadedConnection`'s argument rule (GraphQL plan 4); the dialect sweep of `SQL(ctx)` with a policy, which moves to API plan 5; renaming the module to `contrib/veloxgql`.
17. `contrib/graphql`'s public packages (`graphql`, `veloxgql`, `gqlimpl`) are under the citation test (`TestGodocCitesNoSpec`), as the root module's are. `contrib/otel` stays open for API plan 4.
18. An application directive is refused on an entity too ("put it on the fields and edges it guards, or use a policy for row access"): gqlgen runs a type's directives on the fields that return the type only, so the rows' values would stay readable through the cursors, the where inputs, the order and `totalCount`. GraphQL's own directives stay allowed. Type-level directives (federation's `@key`) would need an explicit opt-in, which is a followup.
19. `ErrorPresenter` presents gqlgen's integer range errors (a `*graphql.IntegerError`, which `NumberOverflowError` and `UintSignError` wrap) as `BAD_USER_INPUT` with gqlgen's text, and does not log them. A coded error's own tree (a join of its sentinels) is not searched for a hidden internal error, so a cursor of another type is not logged either.
20. An error that is only the cancellation of the request (the client went away) is not logged at ERROR: the request's context is canceled and every internal leaf of the error's tree, through wraps and joins, is `context.Canceled`. It is not passed to the application's `log`, and the default records it with `slog` at level debug. A driver's error, a panic or any other internal error at the same moment, joined with the cancellation or not, is logged at ERROR as before. The client sees what it would otherwise. An application's coded error that wraps an internal one shows its own text and is logged; what veloxgql's coded errors wrap (their sentinels) is not searched.

## 6d. Notes made while implementing GraphQL plan 3

*Note, 2026-10-22 (GraphQL plan 3):* additions to the core, all additive, with `GeneratedCodeIsVersion4` unchanged, a guard, and one rule.

- **`Query.Writable()`.** It limits a query to the rows an Update or a Delete of the entity may change: the tenant and the Read policy, then the entity's Write policy. A `Count` of it checks a row before writes that do not go through the entity's own `UPDATE` (an edge-only update's join rows and children, a delete's join rows). Without it such an update would check the row under Read alone, and change edges of a row the caller may not write. It stays a read, and does not reach a `With` load.
- **`ColumnDesc.Nullable`.** The column holds NULL though the field is no pointer: velox-gen sets it on the JSON column of an `Optional` or `Nillable` field that is a slice or a map, and on a `Nillable` bytes field. Such a nil binds SQL NULL, not the JSON text `null`, and NULL reads back as nil; `clear<Field>` stores SQL NULL. On a field of any other type the flag is refused. This corrects §3.1's plugin rule: `Nullable` is `Nillable || (Optional && a JSON field whose Go type holds nil)`, nil-holding meaning a slice, map, pointer, interface, func or chan, as velox-gen's `nilKinds` has it; there is no `NullJSONCol`. *(2026-10-22: for a JSON field of an interface, func or chan type the plugin says `Nullable`, and its nil binds SQL NULL, but its `ColumnDesc` is not `Nullable`, which the core refuses on such a field; the migrations plan reconciles the two before it reads `ColumnInfo.Nullable` for DDL.)*
- **`velox.Validate` and `velox.ValidateSet`** *(the final review, 2026-10-22)*. For generated code: `Validate(x, cols...)` runs the validators of the given columns of a row, as `Insert` would, and `ValidateSet(set...)` those of an update's `Set` values (refusing an `Immutable` column), as `Update` would, with no DB and no statement. The generated `Check` is reachable only through `Desc`, so velox-graphql's `CreateX` and `UpdateX` need them to refuse an invalid field before the ID checks. Additive; `GeneratedCodeIsVersion4` stays.
- **`Statement.Writable` is guarded** *(the final review)*. `prepare` refuses a Rewrite hook that changes it, as it refuses one that changes `Op`, `Entity` or the policy: the scope reads it, so a hook could otherwise drop the Write policy from a `Writable` count.
- **Rule 48.** An `Optional` JSON field of a type that cannot be nil (a struct or a scalar) needs `Nillable`: its column would be NULL-able and Go could not hold the NULL. The message is "an Optional JSON field of a non-nil-able type needs Nillable". Rules 41–47 and this one are the numbered cross-element rules (`internal/gen/build.go`).

## 7. Order and plans

1. **API plan 1, core:** §1, §2.1–2.2, velox-gen into the root (§3.2), the nullability rule (§3.1), and the Go 1.27 doc pass. Generated code moves to version 4, and every example and golden is regenerated.
2. **API plan 2, schema and plugins:** §3.3–3.6, plugin protocol 2.
3. **API plan 3, GraphQL runtime:** §2.3, §4. Then GraphQL plan 3 (mutations) builds on it.
4. **API plan 4, otel:** §5.
5. **API plan 5, additions:** §6, and the parts of the follow-ups' query-API list that §6 does not cover.
6. **Later:** the pgx split reshapes `Open(d, Conn)` (core review #7), with `veloxpgx`.
