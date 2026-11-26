# 179. A read returns pointers, as in ent

Status: accepted, 2026-11-26.

## Context

`Query.All` returned a `[]E`, `Many.Get` a `[]T`. ent's `All` returns a
`[]*T`, and so does veloxe's. A caller that moves from either, or hands
the rows to code that expects pointers (gqlgen's lists, ent's own helpers,
a function that takes `*E` for `Save` or `After`), converted each slice.
nextapp does it with a `Ptrs` helper at thousands of call sites.

A `[]*E` costs one allocation a row where the rows are made one by one, as
ent makes them. velox scans a read into one array, so it need not.

## Decision

`Query.All`, `RawQuery.All`, `Many.Get`, `LoadedMany` and `LoadEach`
use pointers: `[]*E`, `[]*T`, `[]*T` and `[][]*T`.

- A read still scans its rows into one `[]E`; `All` returns a `[]*E` whose
  elements point into it. Two allocations a query, however many rows.
- A to-many edge is loaded as before, into each owner's own rows (in place
  when an owner's rows are a run of the scan, copied otherwise, so owners
  that have one target do not share it); the kit's `Ptrs` then makes each
  owner's `[]*T` over them, all of the owners' pointers in one array.
  Untyped code (`untyped.RowsOf`, `TableQuery.LoadEach`) still reads the
  rows as values, as it does not know `E`.
- `One`, `First`, `InsertAll(xs []E)` and the write
  methods keep their types: they took or returned a `*E`, or take values
  to insert.

## Consequences

A caller that held the result as `[]E`, or passed `&rows[i]`, changes:
`rows[i]` is the pointer. Code that ranges over the rows and reads their
fields compiles as it is.

Holding one pointer keeps the whole array of its read alive, as holding a
row of a `[]E` did.

Each read allocates one more than before (the pointer slice), and a package
that reads relations compiles about 9 KB more (`entityDescUse` 808042 to
817276 bytes, 1.1%); the allocation budgets and the compile-cost limits in
`internal/apitest` and `internal/repocheck` take both. The change is in the
types a generic read returns, not in the pointer helpers: `engine.PtrRows` and
`engine.PtrParts` are not generic, so they are compiled once.
