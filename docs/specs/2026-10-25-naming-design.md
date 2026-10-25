# API naming pass (before 1.0)

## Intent

velox2's exported names are frozen at 1.0. A naming audit on 2026-10-25 ran revive, staticcheck's ST checks and errname over every module, including the generated code. It found the mechanical rules held: there were no initialism errors, no stutter, and error types and sentinels were named correctly. It also found that names nobody had checked by hand are inconsistent with each other and with the Go ecosystem's idioms, and that many godoc comments are not in Go's form. The user asked for every API name to "match golang standard". This pass makes the names consistent before they freeze, and adds the linters so the rules hold afterwards.

The controller made every call below (the user said "do it"). Each call follows the standard library first, then the Go ecosystem's dominant libraries (OpenTelemetry, gRPC, ent).

## Goals

1. One form for each kind of name, across velox, migrate, plugin, schema/*, contrib/graphql (with veloxgql and gqlimpl), contrib/veloxotel, velox-gen's generated code, and the examples.
2. Godoc in Go's form for every exported identifier.
3. The naming linters in `.golangci.yml`, so `scripts/ci-local.sh` fails on a regression in any module, the generated code included.

## Non-goals

- No behaviour change. Renames, godoc and lint fixes only. Every test passes with only its names changed.
- No compatibility shims. There are no deprecated aliases: velox2 is unreleased, so old names are deleted.
- No rewriting of history documents. Specs and plans earlier than this one keep the names they were written with. Living documents (godoc, READMEs, examples, `migrate/doc.go`, and the followups' open items) use the new names.

## Anti-goals

- Renaming for taste. A name that follows a recognised Go idiom stays, even where another idiom was possible.
- Any change to `GeneratedCodeIsVersion4`'s meaning, other than what the renames below require. Generated code is regenerated in the same change; see ruling 9.

## Rulings

1. **Context setters use the standard library's `With`/`Without` form.** That form is `context.WithValue`, `context.WithoutCancel`. A function that returns a derived `context.Context` is named `WithX` or `WithoutX`. These already conform and stay: `WithTenant`, `WithTags`, `WithSession`, `WithoutTx`. These are renamed:
   - `AllTenants(ctx, reason)` → `WithAllTenants(ctx, reason)`
   - `SkipPolicy(ctx, reason)` → `WithoutPolicy(ctx, reason)`
   - `ReadPrimary(ctx)` → `WithPrimaryReads(ctx)`
   - `OnShard(ctx, name)` → `WithShard(ctx, name)`

   `PinUntil(ctx, t)` stays, because it changes the session and returns nothing; it is a verb, not a setter. The field names `Statement.TenantBypass` and `Statement.PolicyBypass` stay, and their godoc names the new functions.
2. **Context getters are `XFromContext(ctx)`.** This is the form of `trace.SpanFromContext`, `metadata.FromIncomingContext`, `peer.FromContext` and ent's `FromContext`.
   - `velox.RowScopeOf` → `velox.RowScopeFromContext`
   - `veloxgql.DBFrom` → `veloxgql.DBFromContext`
   - `veloxgql.LimitsFrom` → `veloxgql.LimitsFromContext`
   - Internal: `shared.TableFrom` → `shared.TableFromContext`. `shared.OperationTx` keeps its name, since it is not a plain getter. Rename it to `OperationTxFromContext` only if it does nothing but read ctx.

   These stay: predicates and computed queries that read ctx, `IsDryRun(ctx)` and `PinnedUntil(ctx)`, because they are not stored values handed back.
3. **Options.**
   - Package options stay `WithX` constructors returning an opaque `Option` struct. That is velox's `Option`, migrate's `Option`, and the functional-options idiom of gRPC and OpenTelemetry.
   - `veloxgql.Option`, today `func(*Extension)`, becomes an opaque struct like the other two. Its constructors keep their names.
   - velox's `TxOption` constructors keep the `Tx` prefix (`TxIsolation`, `TxReadOnly`, `TxTimeout`, `TxRetrySerializable`). This is the grouped-constructor idiom, as in gRPC's `grpc.MaxCallRecvMsgSize`. It keeps them apart from `Open`'s options in the same package, where a `With` prefix would collide (`WithTimeout`, `WithReadOnly`).
   - `TxRetries(opts ...TxOption) int` stays, because contrib/graphql needs it.
   - The rule is written in velox's package doc: "Open's options are `WithX`; a transaction's are `TxX`; a context is changed by `WithX`/`WithoutX` and read by `XFromContext`".
4. **Shard option.** `WithShardFor(f)` stays. Its parameter is a function, and the context setter `WithShard(ctx, name)` takes a context, so a call site cannot confuse them. Each godoc links the other.
5. **Descriptor naming.** `Relation`, the generated-code descriptor next to `Desc[E]` and `ColumnDesc`, becomes `EdgeDesc`, which matches `ColumnDesc` and its constructors `EdgeTo` and `EdgeThrough`. That keeps the user-facing `Rel[E, T]` from having a near-twin. The field `Desc.Relations` becomes `Desc.Edges`. `Pred[E]` and `Predicate` stay: they pair the way `Order[E]` and `OrderTerm` do (typed and untyped), and ent pairs `predicate.X` with `sql.Predicate` the same way. Their godoc says so.
6. **Small renames.** `gqlimpl.Exist` → `gqlimpl.Exists`, since it reports a fact, like `velox.Exists`.
7. **Kept after review.** These were looked at and judged idiomatic, so they stay:
   - `GoType(ColumnInfo)` stays a function. Its godoc gives the binary-size reason.
   - `field.Postgres` and the other `field` dialect strings stay; they are ent's form.
   - `migrate.ResolveApplied` and `ResolveRolledBack` stay; they read as the action taken.
   - `migrate.ColumnType` and the other `ColumnChange` flags stay.
   - `plugin.O2O` and the other edge kinds stay.
   - The generated `gen.Users` handle tables stay.
8. **Godoc form.**
   - Every exported identifier has a doc comment that begins with its name, or with "A"/"An"/"The" followed by its name for a type.
   - "For generated code." moves after the first sentence. For example: "Validate runs … For generated code."
   - Every exported method gets a one-line doc, including `Kind` and `Target` on every migrate change type and the `Error` methods.
   - Every exported constant block gets a comment.
   - Package comments are present and in Go's form.
   - Unexported identifiers that shadow a built-in (`comparable`, `max`, `clear`) are renamed.
9. **Generated code and examples.**
   - velox-gen's templates and contrib/graphql's generator emit the new names. Every generated file in the repo is regenerated (`go generate`, or the repo's generate step), and the CI's "generated files are up to date" check passes.
   - The hand-written example helpers follow rulings 1 and 2: `RoleOf` → `RoleFromContext`; `AsUser` → `WithUser` in hooks and in policy; `userOf` → `userFromContext`.
   - Because the generated code changes the names it references, `GeneratedCodeIsVersion4` stays at 4 if no generated file references a renamed identifier. Otherwise it becomes `GeneratedCodeIsVersion5`, with the old constant deleted. The plan states which, after checking.
10. **Linters.**
    - `.golangci.yml` keeps `default: standard` and enables `revive`, `errname`, and staticcheck's ST1003, ST1005, ST1006, ST1012, ST1016, ST1017, ST1019, ST1020, ST1021 and ST1022.
    - revive gets these rules: `var-naming`, `exported` (with `sayRepetitiveInsteadOfStutters`), `receiver-naming`, `error-naming`, `error-strings`, `errorf`, `context-as-argument` (allowing `*testing.T` and `testing.TB` before ctx), `context-keys-type`, `unexported-return`, `redefines-builtin-id`, `package-comments`, `time-naming`, `increment-decrement` and `blank-imports`.
    - Generated files are linted: `exclusions.generated: disable`.
    - Each module's lint in `scripts/ci-local.sh` uses this one config, and the plan checks that it does.
    - Test files are linted too. Their findings are fixed rather than excluded, except where a rule has no meaning in a test, and then a path exclusion names the rule.
11. **The `gen.X` handle tables and every generated method name** (`VeloxMeta`, `Hooks`, `Policy`, `Values`, `String`, `GoString`) stay, except where ruling 5 or 6 renames a referenced runtime identifier.

## Constraints

- Go 1.27. Everything is from the existing toolchain: `golangci-lint@v2.14.0`, as `scripts/ci-local.sh` pins it.
- The gate is `bash scripts/ci-local.sh`. It must pass at the end with the new lint config.
- No machine-specific paths. Commits end with the repository's trailer.

## Left to the builder

- The order of the renames, and how the godoc work is split up.
- The exact wording of each doc comment, in the voice of the surrounding godoc.
- Whether a rule from ruling 10 needs a per-path exclusion. Each one is listed in the plan's report.
