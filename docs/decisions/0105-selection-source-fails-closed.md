# 105. The selection source, fail closed

Status: accepted, 2026-11-22. Amended by 0121: `veloxgql.Service` is the
explicit way in for a service outside a request.

## Context

Generated code reads only the columns a field's selection needs, and
batches an edge across the rows of a page. Both came from gqlgen's field
context, read in `gqlgraph`. A second engine (0104, 0106) needs the same
answers from its own context. Where the answer was missing, velox read
every column and batched nothing, silently: the parked graphql-go spec
measured 241 statements for 5, and veloxe's one-time log line is what hid
N-V2.

## Decision

- `gqlcore.SelectionSource` (internal, Q8) has `Field(ctx) (Field, bool)`
  and `Wait(ctx) func()`. A `Field` is `Object`, `Name`, `Type`, `Key` and
  `Sel`; `Sel.On(typ)` gives the `SelectedField`s on a type, `ok` false
  when the source cannot tell. gqlcore computes, once for every engine,
  `Selected` (a connection's `edges.node` fields, every column on an
  application's own field), its memo per `(Key, typ)` in the operation's
  table, `Add`'s "inside a field" test and the batch key
  (`g.Batch(f.Key, key, args)`).
- `Field.Type` is not in the spec's interface: it is the named type the
  field returns, wrappers off. `Selected` needs it to tell a connection
  field from an object field, which gqlgen's definition gave before; every
  engine's field context has it.
- An operation's state (the DB, the limits, the source) is one context
  value, made only by `gqlcore.Install(ctx, db, limits, src)`, which
  panics on a nil `src`. The spec wrote `Install(ctx, cfg, src)`: gqlcore
  cannot import `veloxgql` (veloxgql imports gqlcore), so it takes the DB
  and the adapter's `*veloxgql.Limits` as `any`, which
  `veloxgql.LimitsFromContext` reads back. `gqlcore.WithDB` replaces the
  DB for a mutation's transaction and keeps the rest.
- Every gqlgraph read (`Paginate`, an edge's connection, `EdgeOne`,
  `EdgeList`) fails without a source, before any statement, with
  `veloxgql.ErrNoSelectionSource` joined with `ErrNoDB`: the error names
  the adapters, `gqlgenx.NewExtension` or `graphqlgo.With` (0106), which
  put both in the context. Nothing logs and
  nothing falls back. `Present` shows it as "internal error".
- With a source, the rest is as before: outside a field (an application's
  own code), or in another field, a read is alone; a selection the source
  cannot read is every column. That reads more, never less.
- gqlgen's source keys a field occurrence by its `*ast.Field` and the
  selections gqlgen merged under its response key, interned per source as
  one `*selection` pointer that is both `Key` and `Sel`, so a call
  allocates nothing after the first. `On` is `graphql.CollectFields` with
  the schema's interfaces of the type. `Wait` does nothing: gqlgen runs
  each field in its own goroutine.
- `gqlgenx.Extension.NewContext` installs gqlgen's source too. Outside a
  gqlgen server it finds no field, so generated code there reads alone and
  every column, as documented, rather than failing.
- A caller that finds a batch's read started by another calls its source's
  `Wait` around the wait, so an engine that schedules fields in waves
  (graphql-go) can park the waiter.

## Consequences

- Generated code called under a context no adapter made now gets an
  error before any statement. `gqlgenx.Extension.NewContext` gives such
  code a context.
- The Nested request (examples/graphql `TestNestedRequestBudget`) sends 5
  statements before and after; its allocations at n=50 are 159.35k–159.38k
  before and 159.40k–159.42k after, within 0.05%.
- `TestNoSelectionSource` pins the failure for `Paginate`, an edge's
  connection, `EdgeOne` and `EdgeList`, and that no statement is sent.
