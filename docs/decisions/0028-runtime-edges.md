# 28. The runtime's edges fail, or say so

Status: accepted, 2026-11-01. The foreign-key check and the join-row scoping below
(`TenantRefs`, `ErrTenantRef`) were removed by [0029](0029-scope-replaces-tenant.md); the
rest stands.

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
  the transaction is broken. No answer counts as gone. Failures pay, and raw SQL.
- **Raw SQL that ends the transaction.** `tx.Exec("COMMIT")`, then an insert, then an
  error left the insert durable and Tx returning an error; after a raw `ROLLBACK` a
  nested Tx committed alone, as SAVEPOINT opens a transaction and RELEASE commits it.
  In a transaction, Raw and Exec refuse, unsent (`ErrTxControl`), a statement whose
  first keyword after comments is COMMIT, END, ROLLBACK but not ROLLBACK TO, ABORT,
  BEGIN, START or PREPARE TRANSACTION. MySQL's DDL commits too, and the same keyword
  finds it: CREATE, ALTER, DROP, TRUNCATE and RENAME, unless of a TEMPORARY table, are
  refused there. Velox reads the first statement only, since a trigger's or a
  function's body holds semicolons. So on SQLite, which runs all of a text, the probe
  above also runs after each raw statement that succeeds in a transaction, and breaks
  it if it is gone: one failed BEGIN per raw statement in a transaction, on SQLite.
  **Not caught:** text that ends the transaction and begins another, on SQLite;
  anything past the first statement where a driver runs multi-statement text (MySQL's
  `multiStatements`, lib/pq without arguments); and MySQL's other implicit commits,
  such as GRANT, LOCK TABLES or SET autocommit. The Tx docs say so.
- **Unsent means at the send.** A broken transaction is checked where a statement
  is sent as well as before its hooks, so an Around hook that calls next again
  after the statement that broke it gets the error and sends nothing.
- **SQLite's integer overflow.** SQLite stores an integer that overflows as a REAL,
  which no later Scan reads. `Add` and `Sub` on an integer column render
  `CASE WHEN typeof(x) = 'real' THEN abs(-9223372036854775808) ELSE x END`, abs of
  the least integer being SQLite's error, and `x NOT BETWEEN lo AND hi` for a field
  narrower than int64, whose read would fail the same way. It costs a second
  parameter. Other dialects raise by themselves.
- **A foreign key to another tenant's row.** The tenant scope reads and writes the
  tenant's rows, but a foreign key names a row by its key alone. Under tenant A, an
  insert naming B's project succeeded: B could then not delete its own project, for a
  row it cannot see, and success against a foreign key failure told A which of B's IDs
  exist. `velox-gen` now writes `Descriptor.TenantRefs`, plain strings so that a relation's
  metadata still names no other entity's, for each foreign key of a tenant-scoped
  entity whose target is tenant-scoped and each such side of a join entity. A write
  under a tenant that sets one, non-NULL, sends
  `SELECT key FROM target WHERE key IN (...) AND tenant = ?`, one per column, of
  distinct values, in the pipeline's send on the write's connection, after hooks. A row
  of another tenant and no row get one error, `ErrTenantRef` in a `ConstraintError`, so
  that there is no oracle; the write is not sent. `WithAllTenants` skips it. A row may
  name a key its own insert sets, unless it is an upsert, which may not insert it, and
  whose key may be another tenant's (`TestTenantRefSelfExcuseOnlyWhenInserted`), so a
  new row naming itself in an upsert is refused. Arithmetic on such a column under a
  tenant is refused: its value is not known. A Rewrite that moves the statement to another
  schema moves the check of a reference in the entity's own schema with it
  (`TestTenantRefFollowsARewrite`); one in a named other schema stays. A join entity has
  no tenant column, so its write reads the tenant itself, converted as a scoped entity's
  is, to the reflect kind each TenantRef states (`TenantKind`, a plain string), and fails
  with `ErrNoTenant` with none. This is not fully sound: a named id of that kind passes, as
  the column's own type is not known there, so on a join row a UserID serves an OrgID
  column of the same kind. Its value is compared as the column's kind, so nothing is left
  to the database's coercion.

  This is a check, not a constraint. What it does not do: a target deleted after it,
  which is the database foreign key's job; a composite foreign key is the stronger
  guarantee. Around hooks do not see its SELECT, one round trip per foreign key column a
  write sets, which nothing turns off under a tenant, and one that changes Args or the
  context passes it. A Rewrite that moves the table moves neither it nor a join row's
  condition, which name the target's table. It ignores the
  target's Read policy. Under snapshot isolation it may refuse a target committed after
  the snapshot, and MySQL's case-insensitive string keys and a deferred key to a row
  inserted later may refuse falsely, all failing closed. A DoNothing upsert is checked
  too. A non-join entity with no tenant may still name a tenant-scoped row.
- **A join row's boundary.** `Query[ProjectLabel]` returned another tenant's links, with
  or without a tenant, `Delete` removed them and `Update` rewrote them onto the caller's
  rows. A join entity with TenantRefs is now scoped through them. Under a tenant a read,
  an update and a delete hold `fk IN (SELECT key FROM target WHERE tenant = ?)` per side,
  in the schema a Rewrite moved it to, added after the hooks, in Count, Exists, the
  aggregates and Iter too. With no tenant they fail with `ErrNoTenant`; `WithAllTenants`
  adds nothing. A link that crosses tenants, written before the check or under
  `WithAllTenants`, is hidden from both sides by With and Has; Of, LoadEach and
  CountEach on a scoped target take the caller's owners as given, so an owner of another
  tenant that the caller holds still reads its links. With's read of the join rows, and its
  LimitEach, pays nothing, as its owners were read under the scope and its targets get
  it; it still needs a tenant. Has is bound to rows that are scoped. Of, LoadEach and
  CountEach take the caller's owners: on a target with no tenant, where nothing else
  stands between a tenant and another's links, the owner side is conditioned, and on a
  scoped target they pay nothing, as what they return is the tenant's. GraphQL's link and
  unlink go through the public API and pay. An upsert that updates a join row must name
  every side in its conflict columns (on MySQL, every unique key must hold them), or it
  fails with `ErrTenantUpsert`.

  **Not covered:** a join entity without TenantRefs, such as one written by hand. A
  non-join entity with no tenant column is not scoped, even when each of its rows is
  reachable only through a tenant's (a log with a foreign key to a project): every tenant
  reads and changes all of it. It needs a tenant column or a policy. Raw SQL is the
  caller's.

## Consequences

An application on the old default gets a schema diff: `velox-gen migrate diff`
writes a SQLite table rebuild for each table using `mixin.Time`. The examples hold
theirs. Rebuilds keep the rows' text, so rows written by `CURRENT_TIMESTAMP` still
sort below parameters until rewritten.

A nested Tx's failed RELEASE costs one or two more statements. A 1205 in a MySQL
transaction costs one query. A failed statement in a SQLite transaction costs one
BEGIN, and so does a raw statement in one. A MySQL migration that runs DDL through
`tx.Exec` now fails, where it committed part way.

A write under a tenant costs one SELECT per foreign key it sets to a tenant-scoped
row, and generated code is version 7: rerun `velox-gen`, as a TenantRef without a
`TenantKind` fails when the entity loads. Reading, updating or deleting a join row with no
tenant, as writing one, now fails with `ErrNoTenant`: an application that crosses tenants
says `WithAllTenants`. Deleting a shared row with no tenant of its own deletes it for
every tenant, and the `ON DELETE CASCADE` that velox-gen writes on join rows removes
every tenant's links to it, as a review showed: an earlier version of this record said
the opposite. Guard such a delete with a Policy.

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
  `TestTxEndedContextIsNotWaitingForever`, `TestLimitIsNotASize`,
  `TestRawSQLCannotEndTheTransaction`, `TestSQLiteRawTextEndingTheTransaction`,
  `TestMySQLDDLInATransaction`, `TestTenantRefRefusesAnotherTenantsRow`,
  `TestTenantRefAllowsTheTenantsOwn`, `TestTenantRefBypass`, `TestTenantRefStatement`,
  `TestTenantRefJoinRows`, `TestCheckRefsCostsNothingUnset`, `TestLowerTenantRefs`,
  `TestTenantRefSelfExcuseOnlyWhenInserted`, `TestTenantRefJoinTenantKind`,
  `TestTenantKind`, `TestTenantRefJoinStatements`, `TestJoinRowsAreScoped`,
  `TestJoinLoadsPayNothing`, `TestJoinOwnersGivenAreScoped`.
- `TestMySQLDeadlockSwallowedDoesNotCommit` and
  `TestMySQLDeadlockRetriedByAHookDoesNotCommit` run only against a real MySQL
  (`VELOX_TEST_MYSQL`).
