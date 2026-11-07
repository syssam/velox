# 51. The untyped surface passes rows as a checked *E in an any

Status: accepted, 2026-11-07. Revises 0035 (its untyped row type). Keeps
0022's aggregates as functions.

## Context

A review of the public API found sixteen signatures with `unsafe.Pointer`,
all on 0035's untyped surface: `Table.Field`, `Value`, `Ptr`, `New`,
`Fill`, `Filled`, `Of`, `SetField`, `Validate`, `TableRows.At`,
`TableQuery.After`, `One`, `First`, `LoadEach`, `CountEach` and
`DB.InsertTable`. They say "For generated code.", but a pointer of the
wrong entity reached memory unchecked: 0035 lists `TableQuery.After` as a
gap. gqlgraph erased each *E to an unsafe.Pointer at its boundary and back,
and keyed its batches by address alone.

The review also asked whether the aggregates (`velox.Sum(ctx, q, c)` and
the rest) should be methods of `Query`, as `Count` is, now that Go has
generic methods.

## Decision

- **A row is a *E in an any.** Every untyped method that takes or returns a
  row takes or returns one. A method that takes one checks its type word
  against the Table's *E: one comparison, no reflection, no allocation (a
  pointer in an interface is not boxed). Another value is an error (`a row is
  a non-nil *E, not T`), or nil from `Field` and `Value`, never a panic and
  never a write through the wrong type. A nil `*E` is no row.
- **`Table.Ptr` is gone.** It converted an unsafe.Pointer to a *E, which no
  caller holds now.
- **The engine keeps unsafe.Pointer inside.** Its loaders, writers and scan
  plans are unchanged; the conversion is at the exported methods.
- **gqlgraph and gqlcore hold rows as any.** A batch is keyed by the *E in
  an any, so by pointer and type, and gqlgraph's own erasure (`rowOf`'s
  type-word probe, `nodeOf`) is gone. `veloxgql.Batch` copies its `[]*E` into
  a `[]any`.
- **The aggregates stay functions.** Measured with
  `TestGenericTypesCompileSmall`, making the 17 aggregates `Query` methods
  added about 30 KB to each entity package (`entityDescUse` 767,890 to
  797,860 bytes, +3.9%; `entityDescHooks` and `entityDescMixin` the same):
  a generic method's signature is part of every instantiation of `Query[E]`.
  The `any` rows cost 350 bytes on the same packages.

## Consequences

- `go doc github.com/syssam/velox` has no `unsafe.Pointer`. The untyped
  surface is still in the root package, for 0035's reasons.
- 0035's `TableQuery.After` gap is closed: After checks its row.
- Tests in `internal/apitest` (`TestTableRows`) cover each method's refusal
  of a row of another entity, a value, a nil *E and nil;
  `gqlgraph`'s `TestEdgeErrors` covers an edge whose owner is of another
  entity.
- A row passed as a raw unsafe.Pointer still compiles, as any accepts it,
  and is refused at run time, as any other wrong type is.
