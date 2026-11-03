# 33. velox-graphql generates inputs, not mutations

Status: accepted, 2026-11-03.

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
  `DecodeJSON`, `NotZero`, `Cleared`, `ClearEdge`, `Disjoint`, `Exists`, `Join`,
  `Repoint`, `Children`, `Child`, `Through`), then `Insert` or `Exec`. Each call
  records the first error, decode errors before the others as before, and registers
  the steps that run inside the transaction. Column handles are all
  `struct{ I int32 }`, so a step that needs many operations of a column takes the
  handle as a `~struct{ I int32 }` type parameter and converts it, rather than
  method values, which generated code would otherwise store and Go 1.27 fails to
  compile. Every semantic of the generated mutations is kept, and
  `cmd/velox-graphql`'s mutation tests, ported to the methods, are the contract.
- **Explicit nulls by type.** An explicit null was found in the field's argument
  named `input`, which the application now names. Create and Update find it by the
  input's GraphQL type instead: in the running field's arguments, literals and
  variables, through input objects and lists. One occurrence behaves as before; none, as when Go
  code builds the input, is no null; more than one refuses a null on a clearable
  field with BAD_USER_INPUT naming `clear<Field>`, since it cannot tell which one
  is this input's.
- **One name function.** `<entity>InputGQL` names both inputs' fields in errors,
  and `<entity>CreateInputGQL` and `<entity>UpdateInputGQL` are written only when
  the two inputs have different fields, so that an error on a field one input
  leaves out names none. `String` and `GoString`, which hide a
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
