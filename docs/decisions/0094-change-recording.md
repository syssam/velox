# 0094. A recording handle reports each row a write changed: db.Record

- Status: accepted, 2026-11-20. Replaces this ADR's first text,
  `Mutation.Track` and `Mutation.Changes` in a hook, which was never pushed.

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
var rec velox.Recorder
rdb := db.Record(&rec, velox.RecordIgnore("updated_at"))
rdb.Update[Invoice]().Where(I.Total.LT(100)).Set(I.Total.Add(5)).Exec(ctx)
for c := range rec.All() {
	batch.Add(c.Entity, c.Key, c.Op, c.OldRow(), c.NewRow(), c.Diff())
	if c.Changed(I.Total) {
		log.Printf("total %v -> %v", I.Total.Old(c), I.Total.New(c))
	}
}
```

### API

- `db.Record(s Sink, opts ...RecordOption) *DB`. A nil sink panics.
  `RecordIgnore(columns...)` is the one option.
- `Sink` has `Record(ctx, Change) error`. `RecordFunc` is a function as a
  Sink. `Recorder` is a Sink that keeps what it receives: `All`, `Len`,
  `Reset`. Its zero value is ready, and it is safe for concurrent use.
- `Change{Entity, Op, Key, Soft}` and unexported rows. `Entity` is the Go
  type's name ("Invoice"). `Key` is the primary key after the write for an
  insert or an update, and before it for a delete; a `[]any` for a composite
  key. `Soft` marks a soft delete.
- Typed reads: `I.Total.Old(c)` and `I.Total.New(c)` on every handle kind,
  lazy generic methods as `Set` is. A `Null*` handle gives its element, the
  zero when NULL. `c.Changed(cols...)` reports whether an update's values
  differ in any of cols. `velox.RowsOf[E](c)` gives both rows as `*E`.
- Any entity: `c.OldRow()` and `c.NewRow()` give the `*E` (nil for an
  insert's old row and a delete's new row); `c.Diff()` lists an update's
  columns that differ, as `FieldChange{Name, Old, New}`.
- `velox.Diff[E](before, after)` stays, for two rows the caller holds.
- Removed: `Mutation.Track`, `Mutation.Changes`, `Change.Fields`,
  `Change.Changed()`, `FieldChange.Changed`.

The getters are on the handles because a method set on `Change` cannot
satisfy an interface with a generic method. A `Valued` marker interface for
`c.Old(I.Total)` cost about 9.7 KB per handle kind and 23 KB per entity
package. As handle methods they cost a few KB per entity package
(`compilecost_test.go`).

### Rules

1. **Which writes are recorded.** Every write through a recording handle:
   its `Tx` and savepoints, its `WithoutPolicy`, and a hook's `Mutation.DB()`
   on one of its writes. `Record` on a recording handle replaces the sink
   and options. Raw SQL, dry runs (`SQL(ctx)`) and a hook that skips next
   are not recorded. Nothing is read from the context, except the mark on
   the sink's own ctx (rule 6). `Record` on a transaction's handle replaces
   the sink for that handle; a rollback still drops a Recorder's rows.
2. **One Change per row.** An update or delete reports its rows in key
   order, an insert in the order given. An update reports a row only when a
   column outside `RecordIgnore` differs. Values compare by an `Equal`
   method, `bytes.Equal` for bytes, else `reflect.DeepEqual`.
3. **Values.** Plain Go values, as `Mutation.Value` gives them; a JSON
   column's decoded value. An insert's new row holds what the database
   filled (0093).
   - `Sensitive` keeps ent's meaning: not printable and not serializable.
     The getters and `RowsOf` give real values. `OldRow` and `NewRow`
     marshal without the column (`json:"-"`), and `Diff` leaves it out.
     Nothing is masked. An update that changes only a Sensitive column is
     reported with an empty `Diff`; `c.Changed(col)` tells it changed.
   - `RecordIgnore` columns are left out of `Diff` and do not make a row
     changed. `Old`, `New`, `Changed`, `OldRow` and `NewRow` still read them.
4. **When the sink runs.** After the write's statements, before the write
   returns, once per Change, in order. A sink error is the write's error.
   - In a caller's transaction the write and the sink run under a
     savepoint. An error from either rolls back to it, so a recorded write
     that returns an error took no effect.
   - In a transaction velox opens for the write, the Changes wait for its
     COMMIT.
   - Outside a transaction the sink runs after COMMIT, and its error undoes
     nothing.
5. **Rollback.** A `Recorder` drops what a transaction, a savepoint or a
   `TxRetrySerializable` attempt recorded when it rolls back. It drops by
   id, so a Recorder shared by concurrent transactions loses only the rows
   of the one that rolled back. Until the others commit or roll back, its
   `All` and `Len` also show their rows. A `RecordFunc` sees each Change as
   it happens, including ones later rolled back.
6. **No recursion.** A sink's write through the recording handle with the
   ctx it was given is not recorded. A write under another ctx is, and comes
   back to the sink. A sink can also write through a handle that does not
   record.
7. **Old values match the write.**
   - PostgreSQL 18 and later: one statement, `UPDATE … RETURNING old.*,
     new.*`, `DELETE … RETURNING old.*`, and an upsert's `INSERT … ON
     CONFLICT … RETURNING old.*, new.*`.
   - Below PostgreSQL 18, SQLite and MySQL: a locked read of the rows under
     the write's own scope (no `FOR UPDATE` on SQLite), the write narrowed to
     their keys a chunk at a time, and the new values by RETURNING, or a
     SELECT by key on MySQL.
   - Outside a transaction a recorded write runs in a transaction of its
     own, but for one statement that commits by itself: an insert of one
     statement, and on PostgreSQL 18 an update or delete that names one row
     by its key when the DB has no Rewrite hooks, or an upsert of one
     statement whose update branch gets no policy or interceptor condition.
     With a Rewrite hook that update or delete gets BEGIN and COMMIT. More
     rows than `MaxRows` fail with `ErrTooManyRows`, nothing kept.
   - The PostgreSQL version is read once per pool
     (`SHOW server_version_num`), only by a recording handle, and no hook
     sees it. In a caller's transaction it is read inside the write's
     savepoint, and for velox's own transaction before BEGIN, so a failed
     read never aborts a transaction. That write takes the locked read, and
     a later one asks again.
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

## Consequences

- A handle that does not record sends exactly what it sent before.
- Below PostgreSQL 18 a recorded update or delete adds one locked read. A
  recorded update that pins one row by key runs in a transaction of its own
  there (BEGIN and COMMIT). In a caller's transaction every recorded write
  adds SAVEPOINT and RELEASE. A bidirectional re-point adds a read per
  step.
- Below PostgreSQL 18 the first read cannot lock a row that is not there:
  a row another session inserts under the same key at once is reported as an
  insert although the upsert updated it. PostgreSQL 18 closes this.
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
