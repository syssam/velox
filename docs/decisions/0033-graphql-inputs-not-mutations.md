# 33. velox-graphql generates inputs, not mutations

Status: accepted, 2026-11-03. Its two methods, generic runtime and explicit
nulls by type are superseded by [0045](0045-graphql-input-setters.md).

## Context

velox-graphql wrote the root `type Mutation { createX updateX deleteX }` and sealed
Go functions, `gen.CreateX(ctx, input)`, `gen.UpdateX(ctx, id, input)` and
`gen.DeleteX(ctx, id)`. An application could not rename a mutation, return a
payload, add an argument or a directive, or set a field the client must not
choose: in `examples/graphql`, `CreatePostInput.authorID` came from the client, and
the resolver could not force it to the viewer. entgql generates the inputs and
leaves the mutations to the application, and it generates no delete, which has no
input and whose meaning (soft delete, cascade, payload) is the application's. The
generated code was also heavy: about 210 lines per entity, the decoding, checks,
transaction, links, unlinks and read-back all inline, and two identical name
switches per entity.

## Decision

- **Inputs only in the SDL.** velox-graphql writes `Create<T>Input` and
  `Update<T>Input`, with the fields they had, and no `type Mutation`.
  `graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)` means
  "generate these inputs", as `entgql.Mutations` does. `graphql.MutationDelete`
  is removed: before 1.0 that is a compile error, and the CHANGELOG says what to
  write instead. The application declares its mutations in its own schema file,
  with any name, arguments, payload or directives.
- **Two methods in Go.** `func (in *Create<T>Input) Create(ctx, x *<T>) (*<T>, error)`
  sets the input's fields on x, a nil x being a new row, and inserts it: what the
  resolver set on x beforehand, such as the author, is inserted as it is. This
  replaces `CreateX`'s `set func(*T)` for a required field the input leaves out,
  which Create's godoc now names. `func (in *Update<T>Input) Update(ctx, id string,
  set ...velox.Setter[<T>]) (*<T>, error)` applies the input's fields and then
  the application's own setters, validated with the input's.
- **No delete.** A delete is one `db.Delete[T]()` that the application writes with
  the checks it wants; `examples/graphql`'s `deletePost` is the pattern: decode the
  ID, delete under the scope, `NOT_FOUND` when no row went, then the join rows
  (velox-gen's join tables are ON DELETE CASCADE wherever the database enforces
  foreign keys). veloxgql's cost still charges a field named `delete<T>` for the
  links of its type, as an upper bound.
- **A generic runtime.** The per-entity code is a description that `gqlimpl`
  runs: `NewCreate` and `NewUpdate`, one call per field or edge (`Key`, `Keys`,
  `AddRemove`, `DecodeJSON`, `NotZero`, `Cleared`, `ClearEdge`, `Exists`, `Join`,
  `Repoint`, `Children`, `Child`, `Through`), then `Insert` or `Exec`. Each call
  records the first error, decode errors before the others as before, and registers
  the steps that run inside the transaction. Column handles are all
  `struct{ I int32 }`, so a step that needs many operations of a column takes the
  handle as a `gqlimpl.Handle[T]` type parameter, `interface{ ~struct{ I int32 };
  velox.Column[T] }`, and converts it, rather than method values, which generated
  code would otherwise store and Go 1.27 fails to compile. An edge written by the
  target's rows is one descriptor, from one generated function per edge
  (`gqlimpl.M2M` from `NewM2M`, `gqlimpl.Owned` from `NewOwned`), which Create and
  Update both take. Every semantic of the generated mutations is kept, and
  `cmd/velox-graphql`'s mutation tests, ported to the methods, are the contract.
- **Explicit nulls by type.** Superseded by 0045: an explicit null is a field
  not given, as in entgql. An explicit null was found in the field's argument
  named `input`, which the application now names. Create and Update find it by the
  input's GraphQL type instead: in the running field's arguments, literals and
  variables, through input objects and lists. One occurrence behaves as before; none, as when Go
  code builds the input, is no null; more than one refuses a null on a clearable
  field with BAD_USER_INPUT naming `clear<Field>`, since it cannot tell which one
  is this input's.
- **Input names are data.** The type's `gqlimpl.Conn` names, by column, the
  input field that sets each column: `Inputs` for both inputs, and
  `CreateInputs` and `UpdateInputs` only when the two inputs have different
  fields, so that an error on a field one input leaves out names none. Its
  `Read`, `Fields` and `Edges` give the columns a selection reads in the same
  way. Both were generated switch functions before `GeneratedCodeIsVersion14`. `String` and `GoString`, which hide a
  sensitive field, are written only for an input that has one.

## Consequences

- `veloxgql.GeneratedCodeIsVersion9`: generated GraphQL code must be regenerated.
  The core guard stays at 7.
- SDL breaking change: the generated schema has no `type Mutation`. An
  application adds one to its own schema file, which its gqlgen.yml must list,
  and writes the resolvers, `input.Create(ctx, nil)` and `input.Update(ctx, id)`
  for what the generated mutations did. A delete it served it writes itself.
- The errors name the input type, not the mutation: a row an update cannot find
  is `Update<T>Input: no <T> has this ID`, and a bad ID of the row is named `id`.
  Field errors are as they were (`CreatePostInput.authorID`).
- Create changes x even when it fails: a resolver that retries passes a new one.
- `examples/graphql`'s `createPost` takes its author from the viewer (`X-User`,
  for the demo only) and `CreatePostInput` has no `authorID`.
- Generated lines in `examples/graphql/gen`: `post_gql.go` 724 to 543, `tag_gql.go`
  544 to 407, `user_gql.go` 551 to 482, `node_gql.go` 53, unchanged.

## Review

An independent review of the first implementation (commits bbf9e4d..2b5f09c)
found nine issues; the rulings, all applied:

1. An application's setter on Update of a column the input sets too made the core
   refuse the UPDATE, "column assigned twice", as an internal error. The
   application's setter wins: Exec drops the input's setter of any column an
   application's setter assigns (`velox.Setter.Column`), before validation. The
   application's setters are validated apart, and their error is velox's as it is,
   not BAD_USER_INPUT: it is the server's bug (`TestUpdateAppSetters`, in gqlimpl
   and in `TestMutationsRun`).
2. Create overwrites a field prefilled on x with the client's value for any field
   the input has. Kept, and said in Create's godoc and in "Mutations": to own a
   field server-side, leave it out of the input with
   `graphql.Skip(graphql.SkipMutationCreateInput)` (or `SkipMutationUpdateInput`),
   or set it in a hook.
3. `examples/graphql`'s `UpdatePostInput` had `authorID`, so a client could give a
   post to another author. The author edge skips the update input too, and
   `updatePost` and `deletePost` let only the post's author, the viewer, change it:
   FORBIDDEN otherwise, UNAUTHENTICATED without `X-User` (`TestOnlyAuthorWrites`).
4. Keys prefilled on x are not checked against what the caller may read. Create's
   godoc says x's own fields, foreign keys included, are trusted and unchecked;
   the example's `createPost` checks the viewer is a user first, and its SQLite
   DSN turns foreign keys on, so `deletePost` relies on `post_tags`' ON DELETE
   CASCADE and sends no DELETE of the join rows (`TestDeletePost` counts them).
5. `deletePost` took a count of -1 for success. Now 0 is NOT_FOUND and below 0 an
   internal error that rolls back; the delete recipe in "Mutations" says both,
   and that a self-referencing many-to-many edge's join rows hold the key in
   either column.
6. The generic helpers took handles as `~struct{ I int32 }`, so any entity's handle
   compiled in any slot. They take `gqlimpl.Handle[T]`.
7. NewCreate and NewUpdate guessed the GraphQL type by trimming the input's name.
   The generated code passes it.
8. The lookup of an explicit null by type: a sentence in "Mutations" and on
   `Cleared` for what it leaves, an input Go code builds inside a field whose
   arguments hold one of its type, and a value nested in a variable of another
   input type without the Extension's schema.
9. `Children`'s doc named a parameter it has not: fixed.

And a cleanup: one descriptor function per edge for both inputs; one key
function per Node entity (`<entity>KeyGQL`, the order of the key renamed
`<entity>KeyOrderGQL`), which NewNode and the descriptors take; and `AddRemove`
for the two decodes and the check that no ID is in both, which replaces
`Disjoint`. Generated lines in `examples/graphql/gen`: `post_gql.go` 543 to 531,
`tag_gql.go` 407 to 421, `user_gql.go` 482 to 490, `node_gql.go` 53: the
descriptor functions and key functions add lines, and the calls are shorter.

## Build cost

To be measured at 100 entities (the scale schema of
`cmd/velox-graphql/scale_e2e_test.go`, the application declaring create and update
for each), against the figures in the design: gen 32.7 s CPU, 2.33 GB RSS, 64.4k
lines, and gqlgen's graph 28.7 s CPU, 2.13 GB, 172k lines, with create, update and
delete generated; gen 25.8 s, 1.83 GB, 43.6k lines with none.

## Evidence

`TestMutationsRun` (every mutation test of before, but the delete ones, on SQLite,
and on PostgreSQL and MySQL with `scripts/ci-local.sh`), `TestUnknownRowCount`,
`TestMutationSchemaServes`, `TestLimitsServed`, `TestCreateInputs`,
`TestUpdateInputs`, `TestCreateSetsSkipped`, `TestInputStringHidesSensitive`,
`TestCleared`, `TestClearEdge`, `TestNotNull`, `TestErrorOrder`; in
`examples/graphql`, `TestCreatePost`, `TestDeletePost` and `TestDeleteOtherOrg`.
