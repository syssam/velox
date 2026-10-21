# velox2 API cleanup before 1.0 — design

Status: API plan 1 implemented · 2026-10-20

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
8. GraphQL create inputs are not required when the field is `Optional` or `Nillable`. Update inputs offer `clear<Field>` and `IsNil` filters only for `Nillable` fields, because a value handle cannot write NULL.
9. The documents' history is kept. Plans and reviews record what was decided then; older specs get a dated note where a rule changed.
10. `Or(Allow(), x)` is `Allow` only when no other kid is or holds the zero `Pred`, an invalid condition or a `Has`; otherwise the `Or` keeps every kid, so the statement still fails on a bad one (a `Has`'s relation resolves only when the statement runs). It matches every row either way.

## 7. Order and plans

1. **API plan 1, core:** §1, §2.1–2.2, velox-gen into the root (§3.2), the nullability rule (§3.1), and the Go 1.27 doc pass. Generated code moves to version 4, and every example and golden is regenerated.
2. **API plan 2, schema and plugins:** §3.3–3.6, plugin protocol 2.
3. **API plan 3, GraphQL runtime:** §2.3, §4. Then GraphQL plan 3 (mutations) builds on it.
4. **API plan 4, otel:** §5.
5. **API plan 5, additions:** §6, and the parts of the follow-ups' query-API list that §6 does not cover.
6. **Later:** the pgx split reshapes `Open(d, Conn)` (core review #7), with `veloxpgx`.
