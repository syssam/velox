# 111. Upsert setters, hook conditions, and BeforeCommit and AfterRollback

Status: accepted, 2026-11-22.

## Context

Three ent features had no velox counterpart:

- An upsert's update branch could only copy inserted values
  (`col = EXCLUDED.col`). ent's upsert builder has `UpdateX`, `AddX`,
  `ResolveWith`, `UpdateWhere`, `ConflictWhere` and `ConflictConstraint`.
  `Add` in an insert's `Set` was refused, as it needs a row.
- A hook could read an update's assigned value by name, but could not ask
  which columns a write assigns, which it clears, or which edges it changes.
  ent has `m.Fields()`, `FieldCleared`, the edge lists, and the `hook`
  package's `If`, `Unless`, `HasFields`, `HasOp` and `Reject`.
- A transaction had `AfterCommit` (0100), but nothing before COMMIT and
  nothing on a rollback. ent has commit hooks and `tx.OnRollback`.

## Decision

### Upsert

- `InsertQuery.UpdateSet(setters...)` adds setters to the update branch. It
  is a new method rather than an overload of `Update(cols...)`, because
  `Update` takes column handles and keeps ent's `UpdateX` meaning of "copy
  the inserted value". Its setters are the ones an UPDATE takes:
  - `Set` and `SetNull` write a value.
  - `Add` and `Sub` add to, or subtract from, the existing row's value:
    `qty = "stock"."qty" + $1`. This is ent's `AddX` on the upsert builder,
    which adds the given value to the stored one.
  - `SetExpr` writes an expression.
- `velox.Excluded(c)` is a `Term` of the inserted row's column:
  `EXCLUDED.c` on PostgreSQL and SQLite, `new.c` on MySQL. It is a free
  function, as `SetExpr` is, so it costs no entity package anything (0050,
  0055). It fills a `?` of `SetExpr` and of `Expr` in `UpdateWhere`.
  Anywhere else the statement fails. Adding the inserted value is
  `SetExpr(c, "? + ?", c, Excluded(c))`, so `Add` keeps one meaning.
- In the update branch, a column handle names the existing row, qualified by
  its table. On PostgreSQL a bare name may be read as EXCLUDED's, and on
  MySQL a bare name is ambiguous beside the alias `new`.
- A column is assigned once, by `Update`, `UpdateAll` or `UpdateSet`.
  `UpdateAll` leaves out the columns `UpdateSet` assigns. `DoNothing` drops
  the setters. Immutable and Version columns, an `Add` or expression on an
  encoded column, and an edge setter are refused. The validators run on
  `Set`'s values. A skipped `SetPtr` assigns nothing; with nothing left the
  branch is `Ignore`'s no-op assignment.
- `UpdateWhere(preds...)` sets `ConflictClause.Where`, which Rewrite hooks
  could already set. The policy's Read and Write conditions are still ANDed
  in by the scope. So `ErrPolicyUpsert` on MySQL and the hidden-row check
  stay as they were. Without an update branch, `UpdateWhere` is refused.
- `OnConflictWhere(preds...)` is the conflict target's condition, a partial
  index's predicate: `ON CONFLICT (cols) WHERE …`, unqualified. It holds no
  relation or subquery. PostgreSQL proves the index from the condition's
  text, so a condition with a bound value may not match it. `IsNull`, or an
  `Expr` without arguments, does.
- `OnConstraint(name)` renders `ON CONFLICT ON CONSTRAINT name` on
  PostgreSQL. `OnConflict`'s columns stay required. velox reads rows back,
  places a multi-row upsert's returned rows, and records an upsert's rows by
  those columns.
- MySQL renders `col = new.col`, `col = tbl.col + ?` and expressions over
  `tbl.col` and `new.col`. It refuses `UpdateWhere`, `OnConflictWhere` and
  `OnConstraint`, and SQLite refuses `OnConstraint`, while rendering, before
  anything is sent. MySQL runs its assignments in order, so an expression
  after an assignment reads the assigned value; velox documents this and
  does not reorder.
- Read-back: the columns `UpdateSet` assigns are added to RETURNING on
  PostgreSQL and SQLite, and to the read after a one-row MySQL upsert. The
  entity then holds what the database stored. A column `Excluded` names is
  always sent, never left to its database default, which EXCLUDED would
  then hold. An InsertAll whose `UpdateWhere` may skip rows reads back by
  the conflict columns, in a transaction, as with the policy's conditions.
- `ConflictClause` gains `Set`, `TargetWhere` and `Constraint`. The render
  cache keys all three.

### Hook introspection and conditions

- `Mutation.Columns()` names the assigned columns in the entity's order. For
  an update, these are its assignments of any kind. For an insert, they are
  the columns some row holds a non-zero value in, Defaults included: velox
  counts a zero field as unset, as `fillDefaults` does, and ent fills its
  defaults before its hooks too.
- `Mutation.Cleared(name)` reports an update's assignment of NULL.
- `Mutation.Edges()` returns `[]EdgeChange{Name, Op, Keys}`, with `EdgeOp`
  `EdgeAdd`, `EdgeRemove`, `EdgeClear` or `EdgeSet`. `MutationState` holds
  the write's edge setters. Inserts had them at hook time. An update with
  edges now passes them through the write (`writeWith`). The edges are
  written after `next`, so a hook sees what will change. An update whose
  setters are all edge setters sends no UPDATE and runs no hook, as before.
- `velox.Condition` is `func(ctx, *Mutation) bool`. `If`, `Unless`, `HasOp`,
  `HasColumns`, `HasCleared` and `Reject` mirror ent's `hook` package. They
  are plain functions over `Mutation`, which is untyped, so a schema's hooks
  and `WithHooks[E]` hooks use the same ones, at no per-entity cost.
  `Reject`'s error matches `ErrDenied`. A nil hook or condition gives a nil
  hook, which the schema's `Hooks` and `WithHooks` refuse. `And`, `Or` and
  `Not` are left out: those names are taken by the predicate combinators,
  and a closure says them.

### BeforeCommit and AfterRollback

- `velox.BeforeCommit(tx, func(ctx) error)` and
  `velox.AfterRollback(tx, func(ctx))` are functions of the transaction's DB,
  for `AfterCommit`'s reasons (0100). They refuse what `AfterCommit`
  refuses, and they share its mutex and closed flag.
- BeforeCommit functions run after the outermost `fn` returned nil, in
  order, on `fn`'s context, so their statements join the transaction. One
  queued while they run runs too. An error rolls back and is `Tx`'s error.
  A panic rolls back and panics on.
- The order in a transaction that commits:
  1. `fn`'s recorded writes, each calling its sink before its savepoint's
     RELEASE (0094);
  2. the BeforeCommit functions, whose recorded writes call their sinks the
     same way;
  3. COMMIT;
  4. the AfterRollback functions that a rolled back savepoint made due;
  5. the AfterCommit functions.
  Steps 4 and 5 run once the connection is back in the pool.
- AfterRollback functions run when the work done through their DB is undone.
  That is when the outermost transaction does not commit, for any reason:
  `fn`'s error, a BeforeCommit error, a context that ended, a broken
  transaction or a failed COMMIT. It is also when the savepoint they were
  queued in rolls back, even if the outer transaction then commits. A
  savepoint's rollback marks its functions due. It does not run them then,
  because the transaction still holds the connection, and a function that
  queries the database would wait for it on a pool of one. Each retried
  attempt's functions run, as that attempt rolled back. A panic or
  `runtime.Goexit` in `fn` runs none.
- A savepoint that rolls back drops the BeforeCommit and AfterCommit
  functions queued while it ran, as before. One savepoint mark now covers all
  three queues.

## Consequences

- Counters, high-water marks and "newer version wins" upserts are one
  statement that keeps the policies, where they needed a read and an update
  or `db.Exec`.
- Generated code does not change. The root gains generic methods on
  `InsertQuery` (with `[_ Lazy[E]]`) and plain functions.
- Left out: `UpdateSet` on the untyped `TableInsert`; reading back
  `UpdateSet`'s columns after a MySQL InsertAll, which has no row identity to
  read by; and `Mutation.Edges` on an update of only edge setters, which runs
  no hook.
