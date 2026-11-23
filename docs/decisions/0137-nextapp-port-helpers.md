# 137. Run-time names, IsZero, HasColumn, TxFrom and softdelete.By

- Status: accepted, 2026-11-23. `velox.HasColumn` is superseded by
  [0148](0148-explicit-aggregates-filter-entity-policy-db.md)'s
  `FilterEntity.HasColumn`.

## Context

The nextapp port reported six gaps. Each was checked against the code
before anything was added.

1. N-V10: nextapp's shared rules (`veloxschema/rule/`) take column and
   table names from configuration (`cfg.IDColumn`, `cfg.ShareTable`) and
   pass them to `velox.FieldIn`, `FieldEQ` and `FieldInSelect`.
2. About 96 nextapp helpers returned a nil predicate for "no condition".
   `velox.Pred[E]` holds a tree, so it has neither nil nor `==`. The port
   uses `reflect.ValueOf(p).IsZero()`.
3. W5: a `WithFilter` filter is given only the entity's name. nextapp
   generates a list of the 329 entities with `deleted_at` to know which to
   narrow.
4. W6: apps want to assert the SQL of their condition helpers without a
   database.
5. W3: nextapp keeps its own transaction key in the context, so that a
   nested `WithTx(ctx, db, fn)` joins the outer transaction. Under a
   transaction's context, `db.Tx` fails with `ErrOutsideTx`, and
   `velox.UseTx` takes a `*sql.Tx` the caller began, not one in a context.
6. N-V11: `softdelete.Mixin` sets only `deleted_at`. nextapp also records
   who deleted the row.

## Decision

1. Run-time names already work, and nothing changes. velox-gen checks a
   name that go/types gives a constant value. For any other name, it warns
   and accepts the schema, as for `Mutation.Set` with a run-time name. It
   does not see a call in another package, such as nextapp's `rule`. The
   statement resolves every name when it runs. An unknown column, table or
   key fails the statement with `velox: <Entity>: no column "x"` (or the
   subquery's error), and nothing is sent. No unchecked identifier is
   rendered. `TestRunTimeColumnNames` covers a rule configured by a struct.
   `TestCheckColumnsWarnsNonConstant` now includes a configuration field.
2. `velox.IsZero[E any](p Pred[E]) bool` reports the zero Pred. It holds
   for the zero `Predicate` too, and not for `Allow()`, `None()` or
   `And()`. It is a generic function because the compile-cost test
   measured the two method forms (darwin/arm64, Go 1.27):
   - an ordinary method `func (Pred[E]) IsZero() bool`: one `Pred[E]`
     variable went from 1136 to 13894 bytes, `entityDescriptor` grew by
     11.7 KB and `entityDescUse` by 21.8 KB;
   - a lazy generic method `IsZero[_ interface{ *E }]()`: about 290 bytes
     per entity package, even where it is not called;
   - the function: no bytes where it is not called.
3. `velox.HasColumn(entity, column string) bool` uses the names a Filter
   gets and `FieldEQ` takes. The engine records each entity's metadata by
   its schema name once it is built (`SetInfoKit`). The entity a Filter is
   called for is always built. An entity not used yet is unknown, so
   HasColumn is false for it. When two packages have an entity of one name,
   one of them having the column is enough. A condition on a column the
   other entity lacks fails its statement (fail closed). It is a function,
   not a `DB` method, because metadata belongs to the process, not to a DB.
   No change to the Filter's signature.
4. No new helper. `velox.Open(d, nil)` opens a DB with no connection, and
   `Query.SQL(ctx)` renders a query and sends nothing. So
   `db.WithoutPolicy(reason).Query[E]().Where(p).Limit(1).SQL(ctx)` shows a
   condition's text and arguments. `velox.Pred[E](q)` converts an untyped
   Predicate. `ExamplePredicate_sql` shows it, and the guide says so. A
   helper that returned only the WHERE text would have to cut the SELECT
   apart, and an interceptor's condition is part of that WHERE anyway.
5. `velox.TxFrom(ctx context.Context, db *DB) (*DB, bool)` returns a DB in
   the transaction ctx is marked with. A helper that is handed `db` and
   `ctx` joins with it, or makes a savepoint with `tx.Tx`. The DB it
   returns carries `db`'s WithoutPolicy reason, or none, never the outer
   transaction's. So joining gives no helper a bypass it was not given
   (0054: a bypass belongs to a DB, never to a context). ok is false in
   these cases:
   - ctx has no transaction, or its transaction has ended;
   - the transaction belongs to another Open or to a DB with other options
     (another config);
   - one of `db` and the transaction's DB is a recording handle and the
     other is not.
   If `db` is the transaction's own DB, TxFrom returns it unchanged.
   `db.Tx` still fails under a foreign mark. Making it join without being
   asked would hide the misuse that the transaction's mark exists to catch.
6. `softdelete.Mixin.Assign` already sets more columns in the soft delete's
   UPDATE. Two helpers are added on top of it:
   - `softdelete.By[V comparable](column string, k *scope.Key[V])` returns
     an Assign that sets `column` to the key's value, and sets nothing when
     the context has no value;
   - `softdelete.Assigns(fs...)` joins several Assigns in order and stops
     at the first error, for a row that records one of several kinds of
     actor (user, employee, member).

## Consequences

- Nothing in generated code changes, so `GeneratedCodeIsVersion28` stays.
  The root's generic types change only by the new function, which costs
  nothing where it is not called.
- HasColumn depends on the order of first use. Inside a Filter it is
  exact. Elsewhere, it is false for an entity no statement has used yet.
