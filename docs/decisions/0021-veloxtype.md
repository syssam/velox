# 0021: the plain data types are in the public package veloxtype

Date: 2026-10-31

- Status: accepted, 2026-10-31.

## Context

Decision 0020 made the root's public types aliases of `internal/engine`. Its cost:
`go doc velox.Result` and pkg.go.dev show `type Result = engine.Result`, for a package
a reader cannot open, with no fields. The alias's comment listed them by hand, and
`TestAliasDocsListMembers` kept the list in step. A list has no links and no field
documentation of its own.

Go documents a type where it is declared. Only a public declaration has real documents,
and an alias of a public type links to them.

## Decision

- The public package `veloxtype` holds 25 types: `Aggregate`, `AssignOp`, `Assignment`,
  `Caller`, `ColumnDescriptor`, `ConstraintError`, `ConstraintKind`, `Deadlines`, `Dialect`,
  `IndexInfo`, `Lock`, `LockWait`, `Op`, `OrderTerm`, `PolicyError`, `Result`, `Route`,
  `RowScope`, `SchemaError`, `Tag`, `TxAround`, `TxInfo`, `TxOutcome`, `TxRetry` and
  `ValidationError`, with their constants and exported methods.
- They moved because they can: none has an unexported field, and each reaches only the
  others and the standard library. `veloxtype` compiles without the engine, which is the
  proof, and `TestVeloxTypeIsReExported` keeps its imports to the standard library.
- `ErrDenied` moved with them, as `PolicyError.Is` compares against it. The other
  sentinels stay in the engine: no method of these types needs them.
- `Dialect`'s unexported methods, `quote`, `placeholder` and `noLimit`, became functions
  of the engine. An unexported method cannot be called from another package. No other
  moved type had one.
- The root's aliases name `veloxtype` directly, so godoc links through. Each comment is
  one sentence, and the prose and the field comments are in `veloxtype`. The engine
  aliases the same names in `veloxtype.go`, so the rest of it did not change.
- The root re-exports every name of `veloxtype` under the same name, and
  `TestVeloxTypeIsReExported` fails on one it misses. Application code writes
  `velox.Result` and need not import `veloxtype`.

## What could not move

`Statement`, `Option`, `Conn`, `EntityInfo` and `Predicate` stay in the engine. They hold
private fields: `Statement` the state of an upsert and a bidirectional write,
`EntityInfo` the metadata's compiled parts, `Predicate` its condition tree, `Option` a
function over the engine's `Config`, `Conn` its executor. Another package cannot read a
private field, so the engine could not run on them from `veloxtype`. Making the fields
public, or moving what they reach, would make the engine public, which is what 0020
decided against.

`ColumnInfo`, `BoundPolicy`, `BoundHooks`, `EdgeDescriptor`, `TxOption` and `Pred` hold private
fields too. `ConflictClause`, `EachClause`, `Shard`, `Rewrite` and `Around` have none of
their own, but reach `Predicate`, `EntityInfo`, `Conn` or `Statement`.

## Cost

- Those aliases keep their hand-written member lists, and `TestAliasDocsListMembers` now
  covers only them.
- A reader of `velox.Result` takes one link to reach its fields.
- `veloxtype` is public, so its names are API twice over, here and on the root. Both
  follow decision 0004.
- A generated entity's package is slightly smaller (`entityDesc`, 130,182 to 129,598
  bytes). One variable of a generic type costs up to 120 bytes more (`Query`, 6,598 to
  6,718). Every budget holds, unchanged.
- `Tag` is now imported into the engine, so vet's composites check requires keyed
  literals of it there.
