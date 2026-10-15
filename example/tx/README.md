# tx

Transactions on `model.Account`, through a `transfer` function that takes a
`*velox.DB` and so runs unchanged on `db` or on a transaction's `tx`.

```sh
cd example
go run ./tx
```

```go
err := db.Tx(ctx, func(tx *velox.DB) error {   // nil commits, an error rolls back
	return transfer(ctx, tx, "alice", "bob", 30) // fn gets no context: use Tx's
})

tx.Tx(ctx, func(tx *velox.DB) error { … })      // nested: a savepoint

var ce *velox.ConstraintError                   // the same on every dialect
errors.As(err, &ce) && ce.Kind == velox.Unique

db.Tx(ctx, fn, velox.RetrySerializable(3))       // re-run on serialization failure or deadlock
db.Tx(ctx, fn, velox.Timeout(10*time.Millisecond))
velox.Open(velox.SQLite, sqlDB, velox.WithDeadlines(velox.Deadlines{…}))
```

- A nested `Tx` that fails undoes only its own work; here a duplicate
  account name is caught and the outer transaction opens `bob2` instead. On
  SQLite `ConstraintError.Constraint` holds the columns (`accounts.name`);
  PostgreSQL and MySQL give the constraint's name.
- `RetrySerializable` runs `fn` again on PostgreSQL 40001/40P01, MySQL
  1213/1205 and SQLite BUSY/LOCKED, so `fn` must be safe to repeat. This
  example's one-connection database never conflicts, so it runs once.
- A transaction past its deadline is rolled back, never committed; the error
  matches `velox.ErrTimeout` (velox's deadline) and `context.DeadlineExceeded`.

The output is in `testdata/output.golden`.
