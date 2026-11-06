# 45. A GraphQL input gives setters; the resolver writes

Status: accepted, 2026-11-06. Supersedes 0033's two methods, generic runtime
and explicit nulls by type, and 0010; moves 0009's read-back to the
application.

## Context

0033 gave each generated input a method that wrote the row,
`Create(ctx, x)` and `Update(ctx, id, set...)`, run by gqlimpl: decoding,
key checks, the savepoint, the edges, the read-back. 0044 moved the edge
writes into the core as setters (`gen.Posts.Tags.Add(1, 2)`), so what was
left of gqlimpl's write path was a second copy of velox's own Insert and
Update. entgql's input has `Mutate(builder)`, plain generated Go that sets
the builder's fields, and the resolver saves. Finding an explicit null in
the request by the input's GraphQL type needed the running field's
arguments and, for some variables, the server's schema, and still could
not tell two inputs of a type apart.

## Decision

- Each `Create<T>Input` and `Update<T>Input` has one method,
  `Setters() ([]velox.Setter[<T>], error)`, with no context and no I/O. Its
  body is plain Go: a column's `Set` or `SetNull` for each field given, a
  relation handle's `Add`, `Remove` or `Clear` for each edge field given,
  and `gqlimpl.Checked` last, which runs the columns' validators as an
  insert (`Table.Validate`) or an update (`Table.ValidateSet`) does.
- The resolver writes: `db.Insert(x).Set(set...)` and
  `db.Update[T]().Where(...).Set(set...)`, then reads the row back.
  `examples/graphql`'s `update` helper is the pattern.
- An edge whose key is on the row is that key column's setter. Setters
  does not check that the key names a row the caller may read; the
  database's foreign key checks that it names a row.
- A create input's required fields are always set; an Immutable field is
  only in the create input; a JSON field is decoded with an error return;
  a zero value given to a field with a Default is refused with an error
  return (`gqlimpl.NotZero`).
- Explicit null is a field not given, as in entgql. Only `clear<Field>`
  and `clear<Edge>` clear, and a clear field wins over its field.
- Setters' errors are BAD_USER_INPUT naming the input field: a validator's,
  an ID that does not decode or is of another type, and a list of more
  than `gqlimpl.MaxIDs` (1000) IDs, wrapping `veloxgql.ErrPageTooLarge`.
  The bound is fixed, as Setters has no context to read `Limits.MaxPage`
  from, and the document limits bound lists only loosely.
- The write's errors are velox's. `veloxgql.ErrorPresenter` presents a
  `*velox.EdgeError` of `ErrNotFound` as NOT_FOUND and of `ErrLinked` as
  BAD_USER_INPUT, "already linked to another owner", each naming the edge.
- gqlimpl keeps what Setters calls: `DecodeID`, `DecodeIDs` (and their
  `As` forms), `FromJSON`, `NotZero` and `Checked`. `Atomic`,
  `NewCreate`, `NewUpdate`, the per-edge descriptors, `LockRow`,
  `CheckExists`, `CountUnknown` and the null lookup are gone, with the
  generated per-edge functions.

## Consequences

- `veloxgql.GeneratedCodeIsVersion15`: generated GraphQL code must be
  regenerated, and resolvers that called Create or Update rewritten.
- A client that sent `null` to clear a field sends `clear<Field>: true`.
- An ID given both to add and to remove is no longer refused: the core
  removes, then adds. A field given with its clear is no longer refused.
- A write of columns alone is one statement with no savepoint; one with
  edge setters runs in the core's `db.Tx` (0044). 0010's savepoint per
  mutation is gone.
- 0009's "returned as written" is the resolver's: the example reads the
  row back with `velox.WithoutPolicy`.
- An application's setter of a column the input sets fails the UPDATE,
  "assigned twice": the resolver drops the input's setter first (0033's
  review item 1 is the resolver's now).
- veloxgql's cost charges an update's edges as the core writes them: the
  rows' keys read, and each edge's clear, lists and re-point.

## Evidence

`TestCheckedCreate`, `TestCheckedUpdate`, `TestDecodeIDsCap`,
`TestDecodeIDsRepeats`, `TestNotZero` in gqlimpl; `TestEdgeErrorPresented`
in veloxgql; `TestCreateInputs`, `TestUpdateInputs` and the goldens in
gogen; `TestMutationsRun` in `cmd/velox-graphql`; in `examples/graphql`,
`TestCreatePost`, `TestUpdatePost`, `TestLinkOtherOrg`,
`TestUnlinkOtherOrg` and `TestMutationBudget`.
