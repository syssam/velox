# 0121. One row by key is the key's EQ and One; DeleteQuery.One

- Status: accepted, 2026-11-23. Builds on [0048](0048-update-one.md) and
  [0092](0092-update-one-by-key.md).

## Context

A port of nextapp's sales-order service from old velox has 14 `Get(ctx, id)`,
26 `UpdateOneID(id)` and 7 `DeleteOneID(id)` sites. velox had two of the
three:

- A read of one row: `db.Query[E]().Where(X.ID.EQ(id)).One(ctx)`, which
  wraps `ErrNotFound` and `ErrNotSingular`.
- An update of one row: `db.Update[E]().Where(X.ID.EQ(id)).Set(...).One(ctx)`
  (0048), one UPDATE and a read back when the conditions pin the key
  (0092), and `db.Save(&x, cols...)` for an entity already held.

A delete of one row had no form. The port wrote
`n, err := Delete[E]().Where(X.ID.EQ(id)).Exec(ctx)` and then `n != 1` by
hand at each site.

The candidates for a key form were:

- `db.Get[E](ctx, id any)`, `Query.Key(id any)` or `Delete.Key(id any)`.
  A key typed `any` gives up the check the handle gives: `X.ID.EQ(id)`
  refuses a string for an int64 key, or an int64 for a UUID one, at compile
  time. It must also refuse a composite key at run time.
- A key typed by a second type argument, `db.Get[E, K](ctx, id K)`. Go
  cannot tie `K` to `E`'s key type, so the check is still at run time.
- A `Get` generated per entity. It is typed, but it adds code to every
  entity package and grows an application's build. The root stays generic
  so that a schema's build cost stays flat.

## Decision

- A row's key is named by its key column's handle, `X.ID.EQ(id)`, an
  int64's or a UUID's alike. One row is `One` on each of the three builders:
  `Query.One`, `UpdateQuery.One` and the new `DeleteQuery.One`. ent's
  `Get`, `UpdateOneID(id)` and `DeleteOneID(id)` are these three with
  `Where(X.ID.EQ(id))`.
- `func (d DeleteQuery[E]) One(ctx context.Context) error` removes the one
  row the conditions match. No row removed wraps `ErrNotFound`, so
  `IsNotFound` holds. That covers no row, a row the Write policy or an
  Interceptor hides (softdelete's included, whose delete is an UPDATE of the
  live row), and a hook that skips the delete without `SetAffected`, as in
  ent, whose `DeleteOne` is not found when its count is 0. A count of -1,
  which the driver could not report, is taken as one row.
- When the conditions pin every primary-key column (0092's test, at their
  top level), One is the DELETE alone, sent as `Exec` sends it: it can
  match one row at most, so it needs no transaction. Otherwise it runs in a
  transaction, a savepoint inside one, and a count over one rolls it back
  with `ErrNotSingular`.
- The hooks see the caller's conditions, as `Exec`'s do. The guard runs
  first: no `Where` is `ErrNoWhere`, sent nothing.

## Consequences

- `DeleteOneID(id).Exec(ctx)` ports as
  `db.Delete[E]().Where(X.ID.EQ(id)).One(ctx)`: one statement, as ent's.
- A policy-hidden row is not found by all three forms, and neither is a
  soft-deleted one, since each runs the statement `Where(X.ID.EQ(id))`
  with `Exec` or `All` would run.
- No generated code changes, and the root gains one method.
- By key, a count over one is taken as the one row. It can come only from
  a hook's `SetAffected`, or from the mirror row a delete on the join
  entity of a bidirectional edge also removes. A delete there that does not
  name its key can count both rows of the link, and One then refuses it with
  `ErrNotSingular`.
- A composite key works when every key column is pinned, but has no
  shorter form.
- Tests (`internal/apitest/deleteone_test.go`): `TestByKeyHonoursPolicies`
  (a hidden row and a hook's condition, for read, update and delete),
  `TestByKeySoftDelete`, `TestByKeyUUID` (with a Version column, which a
  delete does not make `ErrStale`), `TestDeleteOneNotSingular` (rolls back
  its savepoint only) and `TestDeleteOneStatements` (one DELETE by key, a
  transaction otherwise, the guard first).
