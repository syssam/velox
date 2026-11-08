# 35. Generated GraphQL code runs on an untyped runtime over the same engine

Status: accepted, 2026-11-03. Builds on 0020 and 0033. Its generated read
surface (`XsConnectionOf`, `PredicateGQL`) is revised by 0046. Its rows are a
*E in an any since 0051, and it is package untyped since 0052. Its Node
lookups are gone since [0061](0061-graphql-plain-ids.md).

## Context

An application of 100 entities with GraphQL took about 31 s of user CPU and
2.1–2.2 GB of memory to compile its `gen` package (main at ab6304b3). The same
package without the velox-graphql plugin, velox-gen's ORM output alone, took
2.4 s and 335 MB. The plugin's own code is not large. The cost was generic
instantiation: gqlimpl's runtime was generic over the entity (`Connection[T]`,
`Update[T]`, the where helpers), so each entity compiled its own copy of it and
of the typed velox API it called (`Query[E]`, `NumberCol[E, V]`, `Rel[…]`,
`UpdateQuery[E]`). `go tool nm -size` put 10.94 MB of text in that package,
led by `velox.Query[…]`, `gqlimpl.(*Connection[…])` and the where inputs'
closures. The typed API's own build cost per entity is flat (0020), and is not
what grew.

## Decision

- **An untyped surface, for generated code.** The root package gains `Table`
  (an entity's columns, relations and row layout, by column index), `TableOf`,
  `TableRows`, `RowsOf`, `PredOp` and its operators, `TableQuery`
  (`DB.QueryTable`, `Untyped`, `Typed`), `TableInsert`, `TableUpdate` and
  `TableDelete` (`DB.InsertTable`, `InsertRows`, `UpdateTable`, `DeleteTable`)
  and `Assignments`. Each says "For generated code." and is outside semantic
  versioning, as `Descriptor` is.
- **The same engine path.** The untyped builders are aliases of
  `internal/engine` types. They build the `Statement`s the typed builders
  build and send them through the same core (`Run`, `Mutate`, `Write`,
  `InsertRow`, `ExecWrite`), so hooks, policies, scopes, caps and errors are
  the same code, not a copy.
- **gqlimpl is non-generic.** Connections, cursors, Node lookups, where
  filters, edge resolvers and the create and update inputs take a
  `velox.Table` and column indexes. What stays generic is only the decoded key
  type K (`Key`, `Keys`, `NewUpdate`) and the typed signatures the generated
  code exposes (`XsConnectionOf`, `Create(ctx, x)`, `Update(ctx, id, set...)`).
- **Checked at the boundary.** Nothing types an index or a condition, so the
  untyped entry points check them before anything runs. A condition made by
  another entity's Table, an EXISTS on another entity's relation, a column
  index out of range, rows of another entity, an assignment whose value is not
  of its column's Go type and a number its column's type cannot hold
  (`ErrOutOfRange`) are errors, never a panic and never a statement on the
  wrong column.

## Consequences

- At N=100, with the application's mutations (two runs each, against
  ab6304b3), the `gen` package compiles in 1.7–1.8 s and about 617 MB, from
  7.1–7.3 s and 2.3–2.4 GB. Its archive went from 208 MB to 49 MB, and its
  text from 10.94 MB to 2.31 MB. What remains is per-entity code, not
  generics.
- The typed API is unchanged. The typed `Query` now shares `PrepareStmt`,
  `CountStmt` and `ExistsStmt` with `TableQuery`. Its benchmarks allocate the
  same.
- Parity tests in `internal/apitest` (`tquery_test.go`, `twrite_test.go`) hold
  the two surfaces to the same SQL, arguments and run logs. They cover
  SQLite, PostgreSQL and MySQL, plain and with hooks, under scope and policy.
  `tharden_test.go` covers the refusals.
- Some checks have gaps:
  - An `Assignment` carries no entity, so another entity's assignment of a
    column of the same Go type is not told apart. Tagging the typed setters
    would cost every entity's package code.
  - A typed `Pred[E]` converted to a `Predicate` carries no entity either. It
    is checked only for its column range.
  - `TableQuery.After` takes a bare pointer, which nothing checks.
- The `graph` package, gqlgen's own generated code, is unchanged: about 6 s
  and 2 GB at N=100. This decision does not touch it.
- GraphQL will support both gqlgen and github.com/syssam/graphql-go, with
  graphql-go the default; a later record decides it. gqlimpl is meant to be
  the runtime both share. Its queries, filters and writes take velox's
  untyped types, not a server's. Three files still read gqlgen's request
  context: `edge.go`, `mutation.go` and `select.go` use `GetFieldContext`,
  `GetOperationContext` and `CollectFields` for the selected fields and the
  input's variables. graphql-go support needs them behind an interface.
