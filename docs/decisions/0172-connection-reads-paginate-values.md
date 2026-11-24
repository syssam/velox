# 0172. A connection reads with the values of Paginate's context

- Status: accepted, 2026-11-24. Builds on 0046 (Paginate) and 0059
  (`Query.Paginate`).

## Context

gqlgraph's `Paginate` returns a lazy `*Connection`. Its page query and its
COUNT ran under the context of the first `Page` or `TotalCount` call: the
resolver context of the `edges`, `pageInfo` or `totalCount` field. An
application sets values before it paginates:

```go
ctx = requestscope.Internal(ctx)
return gen.PaginatePosts(ctx, q, after, first, before, last, orderBy, where)
```

A Policy, an Interceptor or a Filter reads such a value. It never reached
the read. A value that widened a read dropped rows, which fails closed. A
value that narrowed a read stopped narrowing it, silently. The page and
the count could also be read under different contexts.

## Decision

- `newConnection` keeps `context.WithoutCancel(ctx)`, Paginate's context
  without its cancellation. A resolver often cancels a `WithTimeout`
  context with `defer cancel()` before the connection's fields resolve.
- `Page` and `TotalCount` read under a merged context:
  - `Deadline`, `Done` and `Err` are the reading context's.
  - `Value(k)` is Paginate's value, or, when it has none, the reading
    context's. The field context of the reading field, which Paginate's
    context lacks, comes from the reading context.
  - The reading context's transaction mark is dropped (`velox.WithoutTx`).
    A transaction is a value, so it is Paginate's alone: a Paginate in
    `Tx`'s fn joins or refuses as the mark says, and one under
    `WithoutTx` reads outside, whatever the reading context carries.
- The merged context is one two-field struct, one allocation per read. The
  context package's cancellation lookup finds nothing in the captured
  half, which `WithoutCancel` made, so a context derived from the merged
  one is a child of the reading context's cancellation, with no goroutine.
  velox keeps nothing keyed by a context, and its deadline handling reads
  `Deadline`, the reading context's.
- An Edge's batch is unchanged. It reads every owner's page and count in
  one statement, under the context the first caller gave Paginate, its
  cancellation included. A batch's key holds the row scope
  (`velox.RowScopeFromContext`), the read velox would render under that
  context (after the Read policy, the Filters, the interceptors and the
  Rewrite hooks) and `gqlgenx.WithBatchScope`'s value. A per-owner value
  separates the batch only through one of those. A value that only an
  Around hook reads, or that changes nothing velox renders, is the first
  caller's for every owner.

## Consequences

- A value set before Paginate holds for the page and for `totalCount`
  alike, which therefore agree.
- Cancellation stays the request's: a cancelled reading context fails the
  read, and a cancelled Paginate context does not.
- An application whose per-owner value must separate an edge's batch
  records it with `velox.WithScopeValue`, which is in the row scope, or
  returns it from `gqlgenx.WithBatchScope`. Without either, it may page
  each owner with the root `Paginate` over a query filtered by the owner,
  which reads alone.
- Rejected: splitting an edge's batch by the identity of the captured
  context. gqlgen and graphql-go give each owner's field its own context,
  so every batch would split, back to one statement per owner. Context
  values cannot be compared either.
- Rejected: an option. The old behaviour was a bug.
