# 0094. A recording handle reports each row a write changed: db.Record

- Status: accepted, 2026-11-20. Replaces this ADR's first text,
  `Mutation.Track` and `Mutation.Changes` in a hook, which was never pushed.
  Amended the same day (A++ round): the sink gets the write's transaction
  and all of the write's Changes in one call, always before COMMIT; the
  context guard of rule 6 is gone; UpdateDefault columns are ignored by
  default, and `RecordIgnore` is typed.

## Context

Applications log what changed. nextapp's audit log stores `old_values`,
`new_values` and `changed_fields` at about 859 write sites. Each site reads
the row under `ForUpdate()`, writes it, and passes both rows to an audit
batch. That is code at every site, and a missed `ForUpdate()` logs wrong old
values.

ent offers `m.OldX(ctx)` per field, on a one-row update only, read by a
query outside the update's lock. GORM, Rails, Laravel, EF Core, Hibernate,
TypeORM and SQLAlchemy work from loaded objects and skip bulk writes.
PostgreSQL 18 returns the old and new rows from one statement.

This ADR first gave a hook `Mutation.Track(names...)` and
`Mutation.Changes()`. It worked, but every audit needed middleware,
column names as strings, and a hook placed before the others.

## Decision

Recording is a property of the `*velox.DB` a write goes through, as
`WithoutPolicy` is:

```go
// Once, at startup: every write's Changes become audit rows in the write's
// own transaction.
rdb := db.Record(velox.RecordFunc(func(ctx context.Context, tx *velox.DB, cs []velox.Change) error {
	rows := make([]veloxgen.AuditLog, len(cs))
	for i, c := range cs {
		rows[i] = auditRow(c) // c.Entity, c.Key, c.Op, c.OldRow(), c.NewRow(), c.Diff()
	}
	return tx.InsertAll(rows).Exec(ctx)
}))
rdb.Update[veloxgen.Invoice]().Where(I.TotalCents.LT(10000)).Set(I.TotalCents.Add(500)).Exec(ctx)
```

### API

- `db.Record(s Sink, opts ...RecordOption) *DB`. A nil sink panics.
  `RecordIgnore[E](cols...)` and `RecordKeep[E](cols...)` are the options,
  typed, one per entity.
- `Sink` has `Record(ctx, tx *DB, changes []Change) error`. `RecordFunc` is
  a function as a Sink. `Recorder` is a Sink that keeps what it receives
  and ignores tx: `All`, `Len`, `Reset`. Its zero value is ready, and it is
  safe for concurrent use.
- `Change{Entity, Op, Key, Soft}` and unexported rows. `Entity` is the Go
  type's name ("Invoice"). `Key` is the primary key after the write for an
  insert or an update, and before it for a delete; a `[]any` for a composite
  key. `Soft` marks a soft delete.
- Typed reads: `I.TotalCents.Old(c)` and `I.TotalCents.New(c)` on every handle kind,
  lazy generic methods as `Set` is. A `Null*` handle gives its element, the
  zero when NULL; its `OldPtr(c)` and `NewPtr(c)` give a copy, nil for NULL.
  Recorded times are in UTC. `c.Changed(cols...)` reports whether an update's values
  differ in any of cols. `velox.RowsOf[E](c)` gives both rows as `*E`.
- Any entity: `c.OldRow()` and `c.NewRow()` give the `*E` (nil for an
  insert's old row and a delete's new row); `c.Diff()` lists an update's
  columns that differ, as `FieldChange{Name, Old, New}`.
- `velox.Diff[E](before, after)` stays, for two rows the caller holds.
- Removed: `Mutation.Track`, `Mutation.Changes`, `Change.Fields`,
  `Change.Changed()`, `FieldChange.Changed`.

The getters are on the handles because a method set on `Change` cannot
satisfy an interface with a generic method. A `Valued` marker interface for
`c.Old(I.TotalCents)` cost about 9.7 KB per handle kind and 23 KB per entity
package. As handle methods they cost a few KB per entity package
(`compilecost_test.go`).

### Rules

1. **Which writes are recorded.** Every write through a recording handle:
   its `Tx` and savepoints, its `WithoutPolicy`, and a hook's `Mutation.DB()`
   on one of its writes. `Record` on a recording handle replaces the sink
   and options. Raw SQL, dry runs (`SQL(ctx)`) and a hook that skips next
   are not recorded. Nothing about recording is read from or stored in the
   context. `Record` on a transaction's handle replaces the sink for that
   handle; a rollback still drops a Recorder's rows.
2. **One Change per row, one sink call per write.** An update or delete
   reports its rows in key order, an insert in the order given. An update
   reports a row only when a column it does not ignore differs (rule 3).
   Values compare by an `Equal` method, `bytes.Equal` for bytes, else
   `reflect.DeepEqual`; a float NaN equals NaN, by value and through a
   pointer. A decimal column's values (its descriptor's `Decimal`) compare
   by number, read from their `Value` or `String` text, so `"1.50"` equals
   `"1.5"` in a decimal type held as text. All the Changes of one write method call (`Exec`,
   `One`, `Insert`, `InsertAll`, a `Save`, the edge writes of `Rel.Add`,
   `Remove`, `Set`, `Clear`, a soft delete's `Exec`) go to the sink in one
   call, across its chunks, re-point steps, mirror rows and default runs
   (0093); a write that changed nothing calls no sink. A hook runs before
   its write's frame opens, so a write it makes through `Mutation.DB()`,
   before or after next, is a recorded write of its own: its own sink call
   (and outside the caller's transaction its own transaction), in the
   order the hook makes the two writes. A write with edge setters is the
   exception: its frame opens first and its hooks run inside it, so a
   hook's write joins the frame's one sink call, in the order it is made.
3. **Values.** Plain Go values, as `Mutation.Value` gives them; a JSON
   column's decoded value. An insert's new row is the row as stored:
   PostgreSQL and SQLite return every column with the INSERT, and MySQL
   reads the rows by key after it, in the write's transaction. A value the
   column rounds or pads (a decimal's scale, a time's precision, a `CHAR`),
   the key and the defaults the database filled are as stored, so the row
   equals a later update's old row. The caller's entity still gets only the
   key and the declared defaults (0093), as without recording.
   - `Sensitive` keeps ent's meaning: not printable and not serializable.
     The getters and `RowsOf` give real values. `OldRow` and `NewRow`
     marshal without the column (`json:"-"`), and `Diff` leaves it out.
     Nothing is masked. An update that changes only a Sensitive column is
     reported with an empty `Diff`; `c.Changed(col)` tells it changed.
   - Ignored columns are left out of `Diff` and do not make a row changed.
     `Old`, `New`, `Changed`, `OldRow` and `NewRow` still read them. A
     column an `UpdateDefault` fills (`updated_at`) is ignored without being
     named, as every update changes it; `RecordKeep` takes it back in.
     `RecordIgnore` names more, and wins over `RecordKeep`.
4. **When the sink runs.** After the write's statements, inside the write's
   transaction, before its COMMIT or the RELEASE of its savepoint. A sink
   error is the write's error, and rolls the write back, everywhere.
   - In a caller's transaction the write and the sink run under a
     savepoint. An error from either rolls back to it, so a recorded write
     that returns an error took no effect. A write is one savepoint: the
     recorded statements inside it (an edge write's join rows, a
     re-point's steps, `UpdateQuery.One`'s edges) run in it, with none of
     their own. One of them that fails fails the write, with its error
     wrapped, even when a hook swallowed it, as it may have stored part of
     its rows unreported; a hook that tolerates the failure runs the write
     in `tx.Tx`, whose savepoint undoes it.
   - Outside a transaction velox opens one for every recorded write, a
     single statement's too, and the sink runs before its COMMIT.
   - Code that acts after the commit reads a `Recorder` once `Tx` returns.
5. **Rollback.** A `Recorder` drops what a transaction, a savepoint or a
   `TxRetrySerializable` attempt recorded when it rolls back. It drops by
   id, so a Recorder shared by concurrent transactions loses only the rows
   of the one that rolled back. Until the others commit or roll back, its
   `All` and `Len` also show their rows. A `RecordFunc` sees each Change as
   it happens, including ones later rolled back.
   - velox never sees a transaction the caller began on its own `*sql.Tx`
     (`UseTx`) end. There every Change goes to the sink, a Recorder's too,
     and none is dropped when that transaction rolls back: a Recorder used
     through `UseTx` holds every Change delivered. The sink runs inside the
     caller's `*sql.Tx`, so what it writes through tx rolls back with it.
6. **No recursion: the sink's tx does not record.** The sink's `tx` is a
   handle on the write's transaction (the caller's transaction or
   savepoint frame, or the one velox opened): a copy of the frame's handle,
   its policy unchanged, with recording off. What the sink writes through
   it joins the write's transaction and is not recorded. A write through
   any other handle runs outside the write's transaction (and on a pool of
   one connection waits for it forever); one through the recording handle
   is recorded again. This replaces the first
   text's mark on the sink's context, which made a sink set up at startup
   capture a transaction variable to write in it.
7. **Old values match the write.**
   - PostgreSQL 18 and later: one statement, `UPDATE … RETURNING old.*,
     new.*`, `DELETE … RETURNING old.*`, and an upsert's `INSERT … ON
     CONFLICT … RETURNING old.*, new.*`.
   - Below PostgreSQL 18, SQLite and MySQL: a locked read of the rows under
     the write's own scope (no `FOR UPDATE` on SQLite), the write narrowed to
     their keys a chunk at a time, and the new values by RETURNING, or a
     SELECT by key on MySQL. A delete there returns the keys it deleted
     (`RETURNING <key>`), and only those rows are reported, so a row a
     Rewrite hook or a trigger kept is not; on MySQL its count of rows must
     match the locked read's, or the write fails, rolled back.
   - An update that assigns the key finds its rows by the key assigned. A
     row the database returns with the key in another form (a truncated
     time, a padded `CHAR`) takes the place of the chunk's one row, or fails
     the write, rolled back; on MySQL a re-read that finds fewer rows than
     the locked read fails it too.
   - Outside a transaction a recorded write runs in a transaction of its
     own (rule 4). More rows than `MaxRows` fail with `ErrTooManyRows`,
     nothing kept.
   - The PostgreSQL version is read once per pool
     (`SHOW server_version_num`), only by a recording handle, and no hook
     sees it. In a caller's transaction it is read inside the write's
     savepoint, or, for a write of several recorded statements (an edge
     write, a re-point), in a savepoint of its own before the write's; for
     velox's own transaction it is read before BEGIN, so a failed read never
     aborts a transaction. That write takes the locked read, and a later one
     asks again.
8. **Upserts.** A row that conflicted and was updated is an update with its
   real old values; an inserted row is an insert; a row `DoNothing` kept is
   not reported. Below PostgreSQL 18 the conflicting rows are read first,
   locked, by the conflict columns, without the scope or the Read policy.
   - PostgreSQL and SQLite: a recorded upsert gets `RETURNING` of every
     column, and the rows it returns are the rows touched. Rows of one
     InsertAll that share a conflict key give one Change per stored row.
   - MySQL: a read by the conflict values follows, checked against
     `RowsAffected` (which needs the `clientFoundRows=true` Connect
     requires). Rows that share a conflict key in one InsertAll, and a row
     that conflicts on another unique key, fail the write. A row with NULL in
     a conflict column, or a generated key left zero in the conflict target,
     is refused before anything is sent.
9. **Soft delete.** softdelete's update reports `Op: OpDelete, Soft: true`,
   with both rows; `Changed` is false and `Diff` nil. The mark is internal:
   another soft-delete mixin's update is recorded as an update.
10. **Edges.** A bidirectional one-to-one re-point reports each row it
    changes, one Change per row. A many-to-many `Add` or `Remove` reports
    the join rows it writes, both rows of a bidirectional link. A plain
    join insert reports each link's row then its mirror; a `DoNothing`
    upsert reports them in the order the database returns them.
11. **Fail closed.** A recorded write that cannot account for every row it
    touched returns an error and is rolled back. A Rewrite hook may not
    change a recorded write's `RETURNING`.

## Designs rejected

| Design | Why not |
|---|---|
| `Mutation.Track` / `Changes` in a hook (this ADR's first text) | middleware at every app, column names as strings, depends on hook order |
| `.Change(ctx)` / `.Changes(ctx)` on the query builders | widens the SQL API; misses writes in helpers, hooks and GraphQL |
| `velox.Record(ctx)`: the recorder in the context | a new or derived context silently loses or leaks it; an optional parameter in a context value, which package context advises against |
| `TxRecord` option on `Tx` | not every write runs in a transaction |
| `c.Old(col)` on Change, through a `Valued` marker | about 23 KB per entity package |
| `OldValues`/`NewValues`/`ChangedFields` maps | string keys; replaced by the getters and `Diff` |
| `"***"` for Sensitive values | ent's Sensitive means not serialized, not masked |
| A sink call per Change, after COMMIT outside a transaction (the first text) | a sink error left a committed write with no audit row; a 10k-row update made 10k audit INSERTs |
| A mark on the sink's ctx to stop recursion (the first text's rule 6) | a sink set up once could not write in the caller's transaction without capturing a `*DB` variable, racy and nil outside a transaction |
| `RecordIgnore(names ...string)` | untyped; a renamed column fails silently |

## Consequences

- A handle that does not record sends exactly what it sent before.
- Below PostgreSQL 18 a recorded update or delete adds one locked read.
  Outside a transaction every recorded write adds BEGIN and COMMIT, a
  single statement's too. In a caller's transaction every recorded write
  adds one SAVEPOINT and RELEASE, however many statements it sends. A
  bidirectional re-point adds a read per step.
- Below PostgreSQL 18 the first read cannot lock a row that is not there:
  a row another session inserts under the same key at once is reported as an
  insert although the upsert updated it. PostgreSQL 18 closes this.
- Below PostgreSQL 18 that read finds conflicts by `=`, which NULL never
  matches: on a unique index with PostgreSQL's `NULLS NOT DISTINCT`, a row
  with NULL in a conflict column that conflicts is reported as an insert
  although the upsert updated it.
- On MySQL that read takes gap locks: two recorded upserts of new keys in
  one gap, at once, can deadlock. `ER_LOCK_DEADLOCK` is retryable.
- An upsert's old rows, and on MySQL its new rows, are read without the
  scope or the Read policy, so a sink can see values the caller's policy
  hides.
- On PostgreSQL 18, Changes come in key order as Go compares the keys:
  numeric and UUID keys as the database orders them, but a text key under a
  collation other than C may order otherwise. An upsert's Changes come in
  RETURNING order.
- `Change` is not generic and the work is in the engine. `RowsOf[E]` is
  instantiated only where it is called.
- Phase 2, not built: owner-level many-to-many edge changes, `AfterCommit`,
  and recording only some columns of a bulk write.
