# velox2 API cleanup before 1.0 — design

Status: API plans 1–5 implemented · 2026-10-22

The user asked for a review of the whole public API: is it clean, correct, complete, modern and easy to maintain. Three reviews were done, one each for the core package, the schema DSL with velox-gen and the plugin model, and the contrib modules. They are in `docs/history/reviews/2026-10-20-api-review-{core,schema,contrib}.md`.

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

*Note, 2026-10-22 (API plan 4):* item 1 is `Config.OmitCallers bool`, not `Config.Callers`; §6e has the rulings.

## 6. Additions (not breaking)

- `First(ctx)` and `Exists(ctx)`.
- `Rel.Of(owners...) Pred[T]`, to query from loaded entities' edges.
- `Sum`/`Avg`/`Min`/`Max` as functions over a `Query` and a number handle. *(Note, 2026-10-22 (API plan 5): they stay functions, `velox.Sum(ctx, q, col)`; §6f ruling 21 gives the rule.)*
- Locking reads: `ForUpdate`/`ForShare`/`SkipLocked`/`NoWait`, refused outside a `Tx` and on SQLite.
- `Mutation.Conds()` and typed `Handle.ValueIn(m)`, for soft delete and audit.
- `m.Affected()`. *(Note, 2026-10-22 (API plan 5): `ValueIn` and `Affected` replace `Mutation.Value` and `Mutation.Result`, which are removed rather than kept beside them: the no-alias rule. A soft-delete hook does not call `next`; it reports its update's count with `Mutation.SetAffected`, which the delete's `Exec` returns, and without it the delete reports 0 rows. `Conds` is the conditions as given, before and after `next`.)*
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
6. A duplicate index means the same columns in order, the same `Unique` and the same `Where`. Rule 44 compares every index's effective name (the explicit `StorageKey` or the derived one) across the whole graph, among the tables of one database schema (`schema.TableIn`), because PostgreSQL index names share one namespace per schema. *(Wording corrected 2026-10-21 by the final review: names must be unique within one database schema, not across all of them.)* Derived names are `<entity>_<cols>`, as ent names them, with no `_unique` suffix; a unique field's index is `<table>_<col>_key` on PostgreSQL and SQLite, and the column's name on MySQL. A partial index adds `_<fnv32 of Where>` only when another index of its entity has the name without it. This changed the model hash lines of generated code, which compiles unchanged (the version stays 4). *(Updated 2026-10-29 by hardening wave 6: the names were `<table>_<cols>[_unique][_<fnv32 of Where>]`, with `_unique` "not `_key`"; commit 25e9e88 gave ent's index, unique and foreign-key names.)*
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

## 6e. Rulings made while implementing API plan 4

Added 2026-10-22. Each is a decision taken in the plan or while executing it, and it stands. Where one differs from §5, it is the rule.

1. **One way to configure.** `veloxotel.Options(cfg)` is the only way in. `Around` and `TxAround` are not exported; an application that wants `WithCallerSkip` passes it after `Options`. The booleans are `OmitCallers`, `OmitQueryText`, `RecordTenant`, `RecordMetricFingerprint` and `RecordReturnedRows`: `Omit` turns off a default, `Record` turns on an extra.
2. **`OmitCallers bool` replaced the `Callers` type.** The plan made `Callers` a type with `CallersOn` as its zero value, to leave room for a mode that put call sites on spans only. Review found one name better than three and no second mode in sight; a second can be added when one is wanted. This supersedes the plan's ruling 3 and §5.1's `Config.Callers`.
3. **The module is `contrib/veloxotel`,** with no alias at the old path. Every caller, the example, CI and the scripts moved in the same commit; plans and reviews keep their history.
4. **The pinned release is semconv v1.43.0,** the newest that `go.opentelemetry.io/otel@v1.47.0` carries. The adapter writes its names out and imports no `semconv` package; a test-only import of `semconv/v1.43.0` and `dbconv` (`TestNamesMatchSemconv`) checks every name, unit, description and the schema URL. The adapter is brought to v1.43.0 where it differed: `db.query.summary` is added (not for raw SQL); `db.response.returned_rows` on spans is opt-in (`RecordReturnedRows`); the rows histogram has the advised boundaries and `dbconv`'s description.
5. **The scope's version** is read from `debug.ReadBuildInfo`, since velox tags no releases. `(devel)` and a module replaced by a directory give none, not a placeholder.
6. **Raw SQL's `db.query.text`** is recorded only when it has arguments: the conventions say text that is not parameterized should not be collected by default, and velox cannot see literals in raw text.
7. **The query summary is built once per statement,** in the Around hook, and passed to the attributes and the metric. There is no cache.
8. **The core gains `Result.LoopBody` and `velox.DriverCode`, and nothing else.** Both are additive and `GeneratedCodeIsVersion4` stays 4. `LoopBody` is the time `Iter`'s caller spent in the loop body, measured only when the DB has an Around hook, with no allocation; it is the only way to time `Iter` without its loop body, as no hook sees a row being yielded. The budgets did not move.
9. **`db.client.operation.duration` is the database's time,** the loop body subtracted, also when the loop breaks. The span keeps covering the loop body, as it shows how long the connection was held.
10. **`DriverCode(err) (string, bool)` is a function,** not a field of `ConstraintError`: serialization failures, deadlocks and timeouts carry codes and are no constraint error. The codes are PostgreSQL's SQLSTATE, MySQL's error number and SQLite's extended result code. Within an `errors.Join` of errors from more than one driver, PostgreSQL's wins, then MySQL's, then SQLite's.
11. **`error.type` keeps velox's own values** (`velox.denied`, `constraint.unique`, …) and becomes the driver's code where it would have been `_OTHER`. The code is also `db.response.status_code`. A retried serialization failure is therefore no longer `_OTHER`.
12. **`Config.Target` and `Config.TargetFor`** name `db.namespace`, `server.address` and `server.port`, as the adapter sees no connection string. The namespace is written in the system's form (PostgreSQL's `{database}|{schema}`); `server.port` is set whenever it is given with an address.
13. **TargetFor's attributes are set once, at the final route,** before the span ends and on the duration point. This replaces the plan's "at span start, and again after a fallback", which left a replica's field on a span the primary ran: OpenTelemetry cannot remove an attribute. A static `Target` stays a start attribute, as it cannot change. Samplers therefore see the sharding but not the target of a `TargetFor`.
14. **The transaction span has no namespace or server:** it is velox's own internal span, and `TxInfo` has no route.
15. **Left out:** `Op.Raw()` and `Op.Reads()` (to API plan 5); the review's `Config.Filter` and `Config.Attributes`; `network.peer.address`; a nested test module for the SDK and pgx; skipping the attribute set under a no-op `MeterProvider`.
16. **Documents.** The adapter's package is under a citation test (`TestGodocCitesNoSpec`), as the other public packages are. The observability design carries dated notes for the changes above.

## 6f. Rulings made while implementing API plan 5

Added 2026-10-22. Each is a decision taken in the plan or while executing it, and it stands. Where one differs from §6, it is the rule. The plan's numbering is kept; "changed" says what execution did to a call.

1. **`Exists` is a `Statement` field,** rendered `SELECT 1 … LIMIT 1`, not a one-column `All`, so a hook and the adapter see what the statement is. `First` adds no ordering: an unordered `First` is any matching row, as ent's is.
2. **`One` checks its row count before it loads relations.** Its results do not change.
3. **`Rel.Of` resolves its relation and reads the owners' keys when called,** and holds its own copy, like `In`. Unlike `HasWhere` it reads metadata, so its doc says to use `HasWhere` in Policy and Hooks bodies. `r.Where` applies; `r.OrderBy` and `r.With` do not. A many-to-many `Of` is the node `opThrough`, keyed in the render cache by its join entity. A nil owner fails the statement, naming the entity; a NULL key is skipped; no owners match no row; each owner is a parameter, so too many owners are refused by the dialect's parameter limit.
4. **The aggregates take `NumberCol` only.** A Go function cannot take either of two struct types and still infer `V`. A nullable column's aggregate goes through `Raw` until one is asked for. `GroupBy` stays out; the tour keeps its `Raw` `GROUP BY`.
5. **`Sum` returns `(V, error)`, 0 on no row;** `Avg`, `Min` and `Max` return `(…, bool, error)`, false on no row. `Avg` is a `float64`, which holds integers exactly up to 2^53. NULL is handled in Go through a `**V` scan, not `COALESCE`, so the SQL is one shape on every dialect, and a named number type scans into itself.
6. **Locks are two fields,** `Lock` and `LockWait`, with typed constants, so an invalid combination cannot be written; the last call of each pair wins. A wait without a lock, SQLite, a read outside `Tx`, and `Count` or an aggregate are refused by the guard with plain errors. `With` is refused, since the loads would not be locked. A lock failure has no sentinel: `DriverCode` gives `55P03` and `3572`. MySQL's InnoDB locks every row a locking read scans, so `SkipLocked` and `ForUpdate` want an index on the filter, and a `One` with `ForUpdate` locks up to 2 rows; both are in the godoc.
7. **A Rewrite hook may not change `Count`, `Exists`, `Aggregate`, `Lock` or `LockWait`.** Changed: on a `Count`, `Exists` or aggregate statement it may not change `Columns` either, since the scan depends on them. Changed again (final review): `Count` was added, as it renders before `Exists` and an aggregate, so a hook that set it made `Exists` true on no row and an aggregate return a count, one that cleared it made `Count` return a column, and one that set it on a locking read slipped past the guard's refusal. The `Columns` check reads the fields as they were before the hooks.
8. **`Mutation.Value` and `Mutation.Result` are removed;** `ValueIn` and `Affected` replace them, and every caller moved in the same commit. Changed: `ValueIn` on a value of another type records an error on the mutation (the first wins, as with other misuse), naming the column and both types, and returns the zero value and false. `Conds` returns nil on an insert without recording a misuse.
9. **A soft-delete hook does not call `next`.** Changed: it reports its own update's count with `Mutation.SetAffected(n)`, which the delete's `Exec` returns; without it a skipped `next` still reports 0. Calling both `SetAffected` and `next`, or a negative count, is a misuse. This amends the plan's ruling 9, which left the delete reporting 0 rows, a trap.
10. **No `clientFoundRows` check at `Open`.** `Open` takes a `*sql.DB`, and neither database/sql nor go-sql-driver/mysql exposes the DSN or the client flags behind it. The pgx split reshapes `Open(d, Conn)`, so it can take the MySQL configuration and refuse a missing flag; the item moved there.
11. **`apidiff` is not wired now.** There is no tag to compare with, and a check against `main` would fail on the breaking changes still planned. After the first `v1.0.0` tag, CI runs `golang.org/x/exp/cmd/apidiff`, pinned, for the root module, `contrib/veloxotel` and `contrib/graphql` against the latest `v1` tag, and fails on an incompatible change; the generated-only names are reported, not failed.
12. **The follow-ups the plan took and left.** Taken: `Op.Raw()`/`Op.Reads()`, the `SQL(ctx)` dialect sweep, the read-path goldens, `One`'s loads and the root citation scan's alignment. Left to their plans, as the follow-ups say: GraphQL plan 4, the migration planner, the pgx split, velox-gen and otel. Changed: the plan left the LIKE escape's dependency on server settings "with no plan"; that is superseded by ruling 16.
13. **The four new read fields are the only `Statement` changes.** The exported types `Aggregate`, `Lock` and `LockWait` give hooks and adapters typed values. None is "For generated code", and none touches generated files, so `GeneratedCodeIsVersion4` stays 4.
14. **Only `First` and `Exists` get budget lines,** each held to the one-row `All`'s count, measured in the same test.
15. **A nested `Tx` refuses every option,** `TxRetrySerializable(0)` included.
16. **The LIKE escape is `!`** (added while executing). `HasPrefix`, `HasSuffix`, `Contains` and `Like` use `ESCAPE '!'` on every dialect, so neither MySQL's `NO_BACKSLASH_ESCAPES` nor PostgreSQL's `standard_conforming_strings` changes a match; `Like`'s doc says `!` escapes. A `\` in a pattern now matches a backslash, where it escaped the next character before. The core design carries a dated note.
17. **`Conds` returns the caller's conditions as given** (changed): a snapshot taken before `prepare`, the same before and after `next`, never the tenant's, the policy's or a Rewrite hook's; changing the returned slice changes nothing. It relies on `prepare` never editing a condition in place (`TestCondsAfterNext`).
18. **The compile-cost bounds keep their margin.** A measured size moved by a task gets a bound of the new size plus the same margin of about 1.2% that the old bounds had, not a looser one that stops guarding; the test's comment says so.
19. **The MySQL lock itests need `VELOX_TEST_MYSQL_ADMIN`,** the CI MySQL's root, which CI and the local mirror set: the check that a second `ForUpdate` is waiting reads `performance_schema` instead of sleeping a fixed time. PostgreSQL's check uses `pg_blocking_pids`.
20. **The `velox.aggregate` span attribute** is optional and not built; it is in the follow-ups.
21. **A method when it needs no type parameter beyond `E`; a function when it does** (final review). `Sum`, `Avg`, `Min` and `Max` stay functions, `velox.Sum(ctx, q, col)`, as §6 has them. The final review proposed `Query` methods, `q.Sum(ctx, col)`, for one shape across the terminal operations; measured, they cost about 17.5 KiB (2.3%) of every entity package that uses a `Query` (`entityDescUse`, 782738 → 800636 bytes), called or not, because a `Query` method whose signature names `NumberCol[E, V]` puts it in the export data of each such package. velox's scale rule keeps the binary flat in the number of entities, and a `Query` method with its own type parameter is paid for by every entity, so the methods were reverted and the bounds re-set from the restored sizes by ruling 18. `Query.With[T]` and `Rel.With[U]` stay methods, as the exception to this rule: on 2026-10-26, with both called, `entityDescUse` measured 769036 bytes with the methods and 767398 with functions `velox.With` and `velox.WithRel`, a difference of 1638, under the 1% (7670 bytes) the pre-1.0 hardening spec set (its ruling 11 and builder ruling 10).
22. **The godoc nits are done, not deferred** (final review): `Mutation`, `Option` and `TxOption` are structs with unexported fields, so their documentation names no unexported type, and `ColumnInfo.Codec`'s rules moved to the package documentation's `JSON fields` section.
23. **`SetAffected` is for updates and deletes** (final review). On an insert it is a recorded misuse, as `Set` and `Where` are: an insert's count is its rows, and its `Exec` returns none. A `Save` whose hook makes the write itself stores nothing in its entity, as velox wrote nothing; `Save`'s doc and `SetAffected`'s say so. The `Hook` doc's "next exactly once" allows not calling it to make the write itself, and `Conds` says that an outer hook whose `next` has returned also sees the inner hooks' conditions.

## 6g. Notes made while implementing GraphQL plan 4

*Note, 2026-10-22 (GraphQL plan 4):* additions to the core, all additive, with `GeneratedCodeIsVersion4` unchanged and the budgets and the escape check as they were. The rest of the plan's rulings are in the GraphQL design, "Rulings made in plan 4".

- **`Statement.Each`** (`*EachClause`). The per-owner shape is a field of the statement, so hooks and adapters see what it is, and the scope's conditions (the tenant, the Read policy) render inside the window: a row they hide takes no rank. A Rewrite hook may not change it, as with `Lock` (§6f ruling 7). It is set for a `With` load with `LimitEach`, for `LoadEach` and for `CountEach`.
- **`Rel.LimitEach(n)`.** At most `n` related entities for each owner, the first in the relation's order, in one statement for every owner: `ROW_NUMBER() OVER (PARTITION BY …)` in a derived table on PostgreSQL, MySQL 8 and SQLite 3.25 or later. Ties break by the target's primary key. A to-one relation read by the target's one-column primary key needs no window. A many-to-many page joins the join table in the target's one statement.
- **`Rel.MaxRows(n)`.** A cap of its own for any load, in place of the owner query's `MaxRows` and the DB's cap. A load with `LimitEach(n)` is bounded by its limit, and one read by the target's primary key by one row per key; any other load shares the owner's cap.
- **`LoadEach(ctx, q, r, owners)` and `CountEach(ctx, q, r, owners)`.** The per-owner page and its count for owners already in hand, with a `Query` of the target that gives the conditions, the order, the columns and `After`. One statement per chunk of owners, under the dialect's parameter limit.
- **`Query.Selects(cols...)`.** Whether the query reads every one of the columns; true for a query with no `Columns` or `Omit`. It is what lets generated code narrow a query without adding a column the application's query left out.
- **`RowScope` and `RowScopeOf(ctx)`.** A method-free struct and a function, read-only: the tenant, the reasons of `AllTenants` and `SkipPolicy`, and `OnShard`'s shard. It is for code that caches or batches by scope, which compares scopes with `==`. A tenant that cannot be compared panics there, and the doc says how to guard.

The rules they follow:
- **A method when it needs no type parameter beyond the receiver's; a function when it does** (§6f ruling 21). `LimitEach`, `MaxRows` and `Selects` are methods (`[_ Lazy[…]]` is allowed); `LoadEach` and `CountEach` take both `E` and `T`, so they are functions. `Query.With[T]` stays as it is: it is the "Before 1.0" follow-up's.
- **Every statement goes through the pipeline.** The windowed and grouped statements are `Statement`s on the target entity: the tenant scope, the Read policy, the Rewrite and Around hooks and the guards see them.
- **A link held twice counts once.** In a non-unique join table, a many-to-many page and `CountEach` dedupe (owner, target) before ranking or counting.
- **No statement for the join entity.** A per-owner load through a join reads the join table inside the target's statement, so the join entity's hooks and policy do not see it (the target's do). A load without `LimitEach` still reads the join rows apart.
- **Reserved aliases.** `vx_rn` and `vx_owner` name the rank and the owner's key of a per-owner page, and a target column of either name is refused.
- **`Query.Count` keeps ignoring `Limit` and `Offset`,** which `totalCount` relies on. `CountEach` refuses them, and its doc says so. A nil owner fails naming the entity; an owner with a NULL key gets no rows, or 0.
- **`After` left the generic prepare path,** which took about 26.8 KB off every entity package, and the compile-cost bounds were lowered by §6f ruling 18's rule. `With(rel.Where(x.Has()))` no longer panics.

## 7. Order and plans

1. **API plan 1, core:** §1, §2.1–2.2, velox-gen into the root (§3.2), the nullability rule (§3.1), and the Go 1.27 doc pass. Generated code moves to version 4, and every example and golden is regenerated.
2. **API plan 2, schema and plugins:** §3.3–3.6, plugin protocol 2.
3. **API plan 3, GraphQL runtime:** §2.3, §4. Then GraphQL plan 3 (mutations) builds on it.
4. **API plan 4, otel:** §5.
5. **API plan 5, additions:** §6, and the parts of the follow-ups' query-API list that §6 does not cover.
6. **Later:** the pgx split reshapes `Open(d, Conn)` (core review #7), with `veloxpgx`.
