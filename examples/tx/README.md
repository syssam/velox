# tx

Transactions on `veloxgen.Account`, through a `transfer` function that takes a
`*velox.DB` and so runs unchanged on `db` or on a transaction's `tx`.

```sh
cd examples
go run ./tx
```

```go
err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { // nil commits, an error rolls back
	return transfer(ctx, tx, "alice", "bob", 30)                // ctx is the transaction's
})

tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { … })  // nested: a savepoint

ce, ok := errors.AsType[*velox.ConstraintError](err)            // the same on every dialect
ok && ce.Kind == velox.ConstraintUnique

db.Tx(ctx, fn, velox.TxRetrySerializable(3))                     // re-run on serialization failure or deadlock
db.Tx(ctx, fn, velox.TxTimeout(10*time.Millisecond))
velox.Open(velox.SQLite, sqlDB, velox.WithDeadlines(velox.Deadlines{…}))
```

- `fn`'s `ctx` is the transaction's: it carries its deadline. A statement on
  `db` under it runs in the transaction, as on `tx`, and `db.Tx` makes a
  savepoint. One on any other `*velox.DB` fails with `velox.ErrOutsideTx`
  before anything is sent, until the transaction ends. `velox.WithoutTx(ctx)` lifts that for work that must not
  join the transaction, such as an audit row that survives a rollback.
- A locking read, `tx.Query[veloxgen.Account]().Where(…).ForUpdate().One(ctx)`,
  runs inside a transaction only, on PostgreSQL and MySQL. MySQL's
  InnoDB locks every row the read scans, not only those it returns, so
  `SkipLocked` and `ForUpdate` want an index on the filter; a `One` with
  `ForUpdate` locks up to 2 rows.
- A nested `Tx` that fails undoes only its own work; here a duplicate
  account name is caught and the outer transaction opens `bob2` instead. On
  SQLite `ConstraintError.Constraint` holds the columns (`accounts.name`);
  PostgreSQL and MySQL give the constraint's name.
- `TxRetrySerializable` runs `fn` again on PostgreSQL 40001/40P01, MySQL
  1213/1205 and SQLite BUSY/LOCKED, so `fn` must be safe to repeat. This
  example's one-connection database never conflicts, so it runs once.
- A transaction past its deadline is rolled back, never committed; the error
  matches `velox.ErrTimeout` (velox's deadline) and `context.DeadlineExceeded`.

The output is in `testdata/output.golden`.
