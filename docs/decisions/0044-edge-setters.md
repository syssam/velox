# 44. A relation handle's Add, Remove and Clear are setters

Status: accepted, 2026-11-06.

## Context

velox wrote the edges whose rows are not the owner's own only in
velox-graphql's runtime: gqlimpl's `Join`, `Repoint`, `Children`, `Child`
and `Through` checked the keys, re-pointed children and wrote join rows
for a GraphQL input. Go code had no way to say "link this post to tags 1
and 2" but to write the join rows itself. ent writes edges as part of a
mutation (`AddTagIDs`, `RemoveTagIDs`, `ClearTags`, then `Save`).

## Decision

- `velox.Rel[E, T]` has `Add(keys ...any)`, `Remove(keys ...any)` and
  `Clear()`, each a `Setter[E]`, as a column handle's `Set` is. A setter
  holds the edge in its `Assignment`, Column -1, so `Assignments` keeps its
  layout.
- `InsertQuery.Set` takes setters. A column setter is stored in each
  entity before the insert; Add and Sub fail. `UpdateQuery.Set` takes edge
  setters beside column ones, and an update of only edge setters sends no
  UPDATE.
- A write with edge setters runs in one transaction, `db.Tx`, a savepoint
  inside one. An update first reads the keys of the rows it matches, under
  the Read and Write policies, locked where the dialect can, as ent reads
  its nodes' IDs. For each owner an edge is cleared, then unlinked, then
  linked.
- The semantics are gqlimpl's, moved into `internal/engine` (`edgeset.go`),
  unchanged: a many-to-many Add checks its targets under the Read policy
  and inserts ON CONFLICT DO NOTHING; Remove and Clear delete only links to
  targets the caller may read; an edge whose foreign key the target holds
  re-points children no owner holds and checks each by a read after the
  UPDATE; a one-to-one Add lets go of the held child first. A failure on
  keys is a `*velox.EdgeError` whose Err is `ErrNotFound` or the new
  `ErrLinked`.
- Every statement goes through the untyped Table API, so the target's and
  the join entity's policies and hooks run.
- An edge whose key is on the owner's row, and a bidirectional one-to-one
  edge, are set by `Rel.Set(key)`, or the key's column handle: Add, Remove
  and Clear of them fail the statement. `Set` first checks, in the write's
  transaction and once for an update, that the key names a row the caller
  may read, under the target's Read policy (an `*EdgeError` of
  `ErrNotFound` otherwise, with nothing written), then assigns the column
  as its handle's `Set` does. Set of another edge, or a nil key, fails the
  statement; the column's `SetNull` clears the edge. So do a foreign key that is not nullable, Add of more than one
  key to a one-to-one edge, Add for several owners of an edge whose key is
  on the target, an upsert, and an InsertAll whose keys the database
  assigns.
- `SQL(ctx)` renders the main statement alone, as for EdgeBidi re-pointing;
  an update of only edge setters renders "".

## Consequences

- An application links rows with the handle it already has:
  `db.Insert(&p).Set(gen.Posts.Tags.Add(1, 2)).Exec(ctx)`.
- An `EdgeError` names the keys that failed, which gqlimpl's errors did
  not; its text names none.
- A Clear of a many-to-many edge reads `velox.EdgePage` (500) links per
  statement, where gqlimpl read the GraphQL MaxPage.
- velox-graphql moves onto these setters in a second phase; gqlimpl's write
  helpers stay until then.
- No generated code changes: `GeneratedCodeIsVersion18` stays.
