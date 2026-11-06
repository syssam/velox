# 47. An edge's connection is the target's paginate of a gqlimpl.Edge

Status: accepted, 2026-11-07. Builds on 0046.

## Context

After 0046 a connection edge method read the DB from ctx, built the root
connection with `PaginateLabels(ctx, db.Query[Tag](), …)`, and re-wrapped
it with `gqlimpl.EdgeConnection(ctx, c.c, &postGQL, x, postEdgeTags,
"Post.tags", []any{after, first, before, last, orderBy, where})`. It
repeated its arguments as the batch key and spelt its GraphQL field, which
the type literal could say once. `EdgeOne` and `EdgeList` took the same
four values.

## Decision

- `(*gqlimpl.Type).Edge(x, rel)` is a `gqlimpl.Edge`: the owner's type, the
  row and the relation. `Type.Edges` is indexed by relation
  (`postEdgeTags: "tags"`), so the edge's field is the type's `Name` and
  that entry; the field check that keeps a call from another field from
  batching reads it.
- `gqlimpl.Source` is a query, `gqlimpl.Query(q)`, or an `Edge`.
  `gqlimpl.Paginate(ctx, from, &t, args, order)` pages either; an Edge's
  query is the target's, on the DB in ctx, and its connection batches.
  `EdgeOne(ctx, &target, e)` and `EdgeList(ctx, &target, e, maxList)` take
  an Edge. `EdgeConnection` is gone.
- Each type has an unexported `paginate<Plural>(ctx, from, …)` with the
  order and where code. `Paginate<Plural>` calls it with `gqlimpl.Query`
  of its query, and each edge method with its Edge: one line each.
- `gqlimpl.Args` carries the where input as given (`Where`) and its
  condition (`Pred`). A connection's batch key is the arguments Paginate
  got: the paging arguments, the order and `Where`, compared with
  `reflect.DeepEqual` as before. `Pred` is not compared: it may hold a
  function (a relation's `Has`), which DeepEqual never finds equal.
- `veloxgql.GeneratedCodeIsVersion17`.

## Consequences

- The key is not gqlgen's `FieldContext.Args`. Those are the client's
  arguments; a forceResolver that computes the edge's arguments per row
  passes others, and must read alone (`TestEdgePerRowArguments`). The
  arguments Paginate gets are the same values for a variable and the same
  literal, so those batch as before (`TestEdgeArgumentsBatch`).
- Generated code no longer calls `veloxgql.DBFromContext` in an edge
  method. With no DB in ctx and a where input that fails, the where's error
  comes first; before, the DB's did.
