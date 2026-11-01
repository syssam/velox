# 28. The runtime's edges fail, or say so

Status: accepted, 2026-11-01.

## Context

A review found runtime paths that returned success, or an error, for work that
then did the opposite: a nested Tx that failed and committed, a read-only SQLite
transaction that wrote, a loop that hung. Each decision below prefers an error
at the call to a silent outcome.

## Decision

- **After and Columns.** `After` fails, naming the column, when an order column
  is not selected: the row holds its zero value there. Count, Exists and the
  aggregates ignore Columns, so they do not ask.
- **A failed RELEASE.** A nested Tx that returns an error has undone its work.
  After a failed RELEASE it sends ROLLBACK TO on a context without the caller's
  cancellation. If that fails too, the transaction is broken: statements fail
  unsent, and the outer fn's nil rolls back with an error.
- **Upserts of a bidirectional key.** An upsert's update branch that writes the
  key of a bidirectional one-to-one edge is refused, as it writes one row of the
  pair. `Save` re-points both.
- **SQLite's transaction options.** `TxReadOnly` runs under `PRAGMA query_only`.
  The transaction holds a `*sql.Conn`, as database/sql hands a transaction's
  connection back once its context ends, and a setting left on would make that
  connection read-only for good. velox reads the setting first, and it returns to
  that value after the transaction ends, on a detached context; if that fails the
  connection is discarded. So a DSN's `_pragma=query_only(1)` stays on. The setting goes
  on after BEGIN, off for it if it was on, as it refuses the BEGIN IMMEDIATE of a DSN's
  `_txlock=immediate`, whose read-only transaction therefore takes the write lock.
  `TxIsolation(LevelSerializable)` is what SQLite gives; other levels are refused.
- **Raw in a transaction is a write.** velox cannot read the SQL, and a
  transaction is where `Raw`'s documentation sends a write that returns rows. So
  its commit pins the session. A read-only `Raw` in a transaction pins needlessly,
  which costs a few reads on the primary. `DB.Exec` already counted.
- **SQLite's time form.** `mixin.Time`'s SQLite default is
  `(strftime('%Y-%m-%d %H:%M:%f000000+00:00','now'))`, velox's bound form, since
  SQLite compares text. No renderer changes: the model holds the default verbatim.
  Other writers must use the form (package doc, `# SQLite`). A time field scans
  SQLite's text forms, trying them only after the driver's scan fails, so a row of
  declared columns pays nothing.
- **Connect keeps one connection on a SQLite file.** A default pool was measured:
  159 of 160 concurrent writes failed with SQLITE_BUSY, and 147 of 160 in WAL mode.
  `busy_timeout` fixes it (0 of 160), but its spelling is the driver's. So a
  statement or Tx on the DB in the body of a loop over Iter, or in a Tx's fn under a
  context without the transaction's mark, on a pool of one, fails rather than hang.
- **How that holder is recognised.** By goroutine ID, read from `runtime.Stack`.
  Nothing else tells a nested statement from a concurrent one: the body's context
  is the caller's, and a timeout would fail a concurrent goroutine behind a long
  export. Only an Iter or a Tx on a pool of one pays for it, and a statement made
  while one is open; other statements pay one atomic load. It costs about 3 µs at an
  empty stack, 12 µs at 20 frames and 29 µs at 60 (`BenchmarkGoid`, Apple M3 Max):
  `runtime.Stack` walks the whole stack however small its buffer, and nothing short
  of a link to the runtime's internals gives the ID. A goroutine the body starts, and
  `iter.Pull`, are not recognised and wait as before: a missed hang, never a false
  failure. So is another DB over the same pool (a second `Open`, or `OpenConn` with
  `SQLConn`): the holder is the DB's, as keying it on the pool would cost those
  DBs a process-wide lookup per statement. On a DB with replicas the check is off, as
  the loop may hold a replica's connection.
- **Mutation.DB outside db.Tx.** The docs change, not the behaviour. velox learns
  that a hook wants the DB only after the hook has started, too late to begin a
  transaction around it, and opening one for every hooked write costs every
  entity with a hook a BEGIN and a COMMIT.
- **Retry backoff.** `TxRetrySerializable` waits before each retry: half of a
  ceiling that doubles from 10 ms to 1 s, plus a random part up to the other half.
  ctx and the deadline end the wait. The error then matches why, ErrTimeout for
  velox's deadline and the context's error otherwise, and the last attempt's cause.
- **MySQL's rollback.** A deadlock (1213) rolls the transaction back and leaves the
  connection in autocommit, so a swallowed one would commit later statements alone.
  1213 breaks the transaction. A lock wait timeout (1205) rolls back only its
  statement, unless `innodb_rollback_on_timeout` is on: velox asks the server on
  the transaction's connection after a 1205, and no answer counts as on. The
  broken transaction's error wraps the deadlock, so `TxRetrySerializable` retries it.
- **SQLite's rollback.** An interrupted statement (its context ended), `INSERT OR
  ROLLBACK` and some errors roll the whole transaction back, with the same result as
  MySQL's deadlock. No error says so, so after any failed statement in a transaction
  velox sends `BEGIN` on a detached context. `ROLLBACK TO` an unknown savepoint
  cannot ask: on modernc.org/sqlite it says "no such savepoint" either way. In a
  transaction BEGIN fails, "within a transaction", and changes nothing. Without one
  it opens an empty transaction, which the broken transaction's ROLLBACK ends, and
  the transaction is broken. No answer counts as gone. Only failures pay. A `ROLLBACK`
  sent as raw SQL that succeeds is not noticed.
- **Unsent means at the send.** A broken transaction is checked where a statement
  is sent as well as before its hooks, so an Around hook that calls next again
  after the statement that broke it gets the error and sends nothing.
- **SQLite's integer overflow.** SQLite stores an integer that overflows as a REAL,
  which no later Scan reads. `Add` and `Sub` on an integer column render
  `CASE WHEN typeof(x) = 'real' THEN abs(-9223372036854775808) ELSE x END`, abs of
  the least integer being SQLite's error, and `x NOT BETWEEN lo AND hi` for a field
  narrower than int64, whose read would fail the same way. It costs a second
  parameter. Other dialects raise by themselves.

## Consequences

An application on the old default gets a schema diff: `velox-gen migrate diff`
writes a SQLite table rebuild for each table using `mixin.Time`. The examples hold
theirs. Rebuilds keep the rows' text, so rows written by `CURRENT_TIMESTAMP` still
sort below parameters until rewritten.

A nested Tx's failed RELEASE costs one or two more statements. A 1205 in a MySQL
transaction costs one query. A failed statement in a SQLite transaction costs one
BEGIN.

## Evidence

- `TestAfterRefusesAnOrderColumnNotSelected`, `TestAfterWithColumnsOnWhatIgnoresThem`,
  `TestNestedTxReleaseFails`,
  `TestNestedTxReleaseAndRollbackFail`,
  `TestNestedTxCanceledBeforeReleaseDoesNotCommit`,
  `TestBidiUpsertRefusesToUpdateTheKey`, `TestSQLiteTxReadOnly`,
  `TestSQLiteReadOnlyRestores`, `TestTxRawSQLPins`,
  `TestSQLiteDefaultTimePagesWithAfter`, `TestSQLiteRawScansTextTimes`,
  `TestIterNestedStatementFails`, `TestIterConcurrentStatementWaits`,
  `TestMutationDBAtomicOnlyInTx`, `TestRetryBacksOff`,
  `TestMySQLDeadlockBreaksTheTransaction`, `TestMySQLLockTimeout`,
  `TestBrokenTransactionSendsNothing`, `TestSQLiteServerRollbackBreaksTheTransaction`,
  `TestSQLiteProbe`, `TestRetryEndedMatchesTheContext`, `TestTxNestedStatementFails`,
  `TestTxConcurrentStatementWaits`, `TestSQLiteReadOnlyKeepsTheDSNsSetting`,
  `TestSQLiteReadOnlyWithTxLock`, `TestSQLiteTxReadOnlyUnderTheDSN`,
  `TestSQLiteArithmeticOverflowFails`, `TestSQLiteArithmeticRendering`, `TestIntRange`,
  `TestTxEndedContextIsNotWaitingForever`, `TestLimitIsNotASize`.
- `TestMySQLDeadlockSwallowedDoesNotCommit` and
  `TestMySQLDeadlockRetriedByAHookDoesNotCommit` run only against a real MySQL
  (`VELOX_TEST_MYSQL`).
