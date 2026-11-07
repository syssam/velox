# 52. The untyped surface is package untyped

Status: accepted, 2026-11-08. Revises 0035 (where the surface lives) and
0051.

## Context

0035 put the untyped surface in the root package: `Table`, `TableRows`,
`PredOp` and its 13 operators, `TableQuery`, `TableInsert`, `TableUpdate`,
`TableDelete`, `TableOf`, `RowsOf`, `Untyped`, `Typed`, `Assignments`,
`AndPredicates`, `OrPredicates`, `NotPredicate`, `Mutation.Table` and five
methods of `DB` (`QueryTable`, `InsertTable`, `InsertRows`, `UpdateTable`,
`DeleteTable`). That is 35 declarations, five of them methods of `DB`.
Each said "For generated code.", but `go doc velox.DB` listed them beside
`Query` and `Tx`.
Package softdelete used them too: an application's mixin may, so they are
not for generated code alone.

The surface reads the state of `velox.Query` and `velox.Meta`. Go puts a
generic type's fields and methods in its own package, so another package
could not reach that state without new exported methods on `Query[E]` or
`Meta[E]`. An exported method on a generic type is compiled into every
package that uses it (0022).

## Decision

- **Package `github.com/syssam/velox/untyped`.** It has `Table`, `Rows`,
  `PredOp` and its operators, `TableQuery`, `TableInsert`, `TableUpdate`,
  `TableDelete`, `TableOf`, `RowsOf`, `FromQuery`, `Assignments`, `And`, `Or`,
  `Not`, `MutationTable`, and `Query`, `Insert`, `InsertRows`, `Update` and
  `Delete`, each taking the `*velox.DB` first. The root keeps none of it.
- **The state is the engine's.** `velox.Meta[E]` holds an
  `engine.MetaState[E]` as its only field. `velox.Query[E]` holds an
  `engine.QueryState` first and its `*MetaState[E]` second. QueryState is
  not generic, and its After row is an unsafe.Pointer, so that a Query of
  each entity adds no type of its own. untyped reaches them by a pointer
  conversion, as it reaches `velox.DB`'s `engine.DB` and `velox.Mutation`'s
  `engine.MutationState`. A size assertion in untyped fails its build when
  one of those layouts changes.
- **`Typed` is gone.** Only two test assertions used it, and setting the
  Query's meta from outside would need an offset after QueryState. The
  engine's `TableQueryParts` and TableQuery's owner, kept for it, are gone
  too.
- **Generated GraphQL code imports untyped** for `untyped.TableOf[Post]` and
  `untyped.FromQuery(q)`. `veloxgql.GeneratedCodeIsVersion18` is
  `GeneratedCodeIsVersion19`.

## Consequences

- The root's public API is 35 declarations smaller. `go doc velox.DB` lists
  velox's own API only.
- With `TestGenericTypesCompileSmall`, `entityDescUse` is 768,566 bytes,
  from 767,890 (+676, +0.09%); `entityDescHooks` and `entityDescMixin` the
  same. A generic `engine.QueryState[E]` cost +5.1 KB, which is why it is
  not generic.
- A hook that writes rows itself names the package:
  `untyped.Update(m.DB(), untyped.MutationTable(m))`.
- untyped has a coverage floor and a unit test; its parity and refusal
  tests are still `internal/apitest`'s.
