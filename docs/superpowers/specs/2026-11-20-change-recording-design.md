# Change recording: design

Status: built, 2026-11-20; ADR 0094 records the decision. Replaces the hook
API (`Mutation.Track`, `Mutation.Changes`) of ADR 0094's first text. This
file was brought in line with the code after it was built.

## Problem

Real applications log what changed: nextapp's audit log stores `old_values`,
`new_values` and `changed_fields` for about 859 write sites. Each site does it
by hand, in three steps:

1. Read the row under `ForUpdate()`.
2. Write it.
3. Pass both rows to the audit batch.

This costs code at every site, and a missed `ForUpdate()` records wrong old
values. No ORM we know records bulk updates or deletes:

- ent offers `m.OldX(ctx)` per field, for UpdateOne only.
- GORM, Rails, Laravel, EF Core, Hibernate, TypeORM and SQLAlchemy work from
  loaded objects and skip bulk writes.

PostgreSQL 18 returns old and new rows from one statement
(`RETURNING old.*, new.*`).

## Designs rejected

| Design | Why not |
|---|---|
| `Mutation.Track` / `Changes` in a hook (built) | middleware boilerplate, string names, order-dependent |
| `.Change(ctx)` / `.Changes(ctx)` on the query builders | widens the SQL API; misses writes in helpers, hooks, GraphQL |
| `velox.Record(ctx)`: recorder in the context | a context carries policy, user, tenant and middleware values; a new or derived context silently loses or leaks the recording; it is an optional parameter in a context value, which the context package's docs advise against |
| `TxRecord` option on `Tx` | not every write runs in a transaction |

## Design: a recording handle

Recording is a property of the `*velox.DB` a write goes through, as
`WithoutPolicy` already is:

```go
var rec velox.Recorder
rdb := db.Record(&rec, velox.RecordIgnore("updated_at"))
rdb.Update[Invoice]().Where(I.ID.EQ(id)).Set(I.Status.Set(paid)).Exec(ctx)
for c := range rec.All() {
    batch.Add(c.Entity, c.Key, c.Op, c.OldRow(), c.NewRow(), c.Diff()) // any entity
    if c.Changed(I.Status) {                                          // typed
        log.Printf("status %s -> %s", I.Status.Old(c), I.Status.New(c))
    }
}
```

### API

```go
type Sink interface{ Record(ctx context.Context, c Change) error }
type RecordFunc func(ctx context.Context, c Change) error // a Sink
type Recorder struct{ /* zero value ready, safe for concurrent use */ }
func (r *Recorder) Record(ctx context.Context, c Change) error
func (r *Recorder) All() iter.Seq[Change] // what was recorded when called, in order
func (r *Recorder) Len() int
func (r *Recorder) Reset()

func (db *DB) Record(s Sink, opts ...RecordOption) *DB // panics on a nil s
type RecordOption struct{ /* unexported */ }
func RecordIgnore(columns ...string) RecordOption

type Change struct {
    Entity string // the entity's name, "Invoice"
    Op     Op     // OpInsert, OpUpdate or OpDelete
    Key    any    // the primary key after the write (before it for a delete); []any for a composite key
    Soft   bool   // a soft delete: Op is OpDelete, the row was updated
    // unexported: the entity and the old and new rows
}
// Typed, on every column handle (lazy generic methods, as Set): the value in
// c's old or new row; zero for an insert's old row, a delete's new row, NULL
// in a Null* handle (its element type), or a Change of another entity.
func (h Col[E, V]) Old[_ Lazy[V]](c Change) V   // I.Total.Old(c)
func (h Col[E, V]) New[_ Lazy[V]](c Change) V   // I.Total.New(c)
func (c Change) Changed[E any](cols ...Column[E]) bool // updates: any of cols differs
func RowsOf[E any, PE Entity[E]](c Change) (old, cur *E, ok bool)
// Any entity:
func (c Change) OldRow() any  // the old *E; nil for an insert
func (c Change) NewRow() any  // the new *E; nil for a delete
func (c Change) Diff() []FieldChange // updates: the columns that differ; nil otherwise

type FieldChange struct{ Name string; Old, New any }
func Diff[E any, PE Entity[E]](before, after *E) []FieldChange // kept
```

The getters are handle methods because a generic method on `Change` cannot
satisfy an interface: a `Valued` marker for `c.Old(col)` cost about 23 KB per
entity package.

The following are removed:

- `Mutation.Track` and `Mutation.Changes`.
- The agent's `Change` (`Key []any`, `Fields`, `Changed()`).
- `FieldChange.Changed`.
- `OldValues`, `NewValues` and `ChangedFields` (string-keyed maps), replaced
  by the typed getters and `Diff` (2026-11-20).

### Rules

1. **Which writes are recorded.** A write is recorded when it goes through a
   recording handle. That includes the following:
   - a `Tx` or savepoint begun on one;
   - `WithoutPolicy` of one;
   - the `Mutation.DB()` of a mutation running on one.

   `Record` on a recording handle replaces its sink. Raw SQL (`Exec`, `Raw`,
   `Rows`) and dry runs (`SQL(ctx)`) are not recorded, and neither is a hook
   that skips next. Nothing about recording is read from the context.
2. **One Change per row.**
   - An update or delete reports its rows in key order.
   - An insert reports its rows in the order given.
   - An update reports a row only when a column outside `RecordIgnore`
     differs. Values are compared by `Equal(T) bool` if the type has one,
     `bytes.Equal` for bytes, and `reflect.DeepEqual` otherwise.
3. **Values.** Values are plain Go values, as `Mutation.Value` gives them; a
   JSON column holds its decoded field value. `FieldChange.Name` is the
   column name.
   - `Sensitive` keeps ent's meaning, not printable and not serializable:
     the `Old`/`New` and `RowsOf` give real values (it is the caller's
     data, as a struct field is); `OldRow`/`NewRow` marshal without it
     (`json:"-"`); `Diff` leaves Sensitive columns out. Nothing is masked.
   - `RecordIgnore` columns are left out of `Diff` and do not make a row
     count as changed; `Old`, `New`, `OldRow` and `NewRow` still give them.
   - The new row of an insert includes what the database filled (ADR 0093).
   - `Key` of an insert or an update is the row's key after the write; of a
     delete, before it.
4. **When the sink runs.** It runs after the write's statements succeed and
   before the write method returns, once per Change, in order. A sink error
   is the write's error.
   - Inside a transaction, the write and the sink run under a savepoint; an
     error from either rolls back to it, so a write that returns an error
     took no effect. An audit row written on the transaction commits or
     rolls back with the change.
   - In a transaction velox opens for the write, the Changes wait for its
     COMMIT.
   - Outside one, the write is committed when the sink runs, and an error
     undoes nothing. This holds on every database and version.
5. **Rollback.** For a `*Recorder`, velox drops the Changes recorded since a
   transaction or savepoint began when that transaction or savepoint rolls
   back, and before each retry of `TxRetrySerializable`. It drops them by id,
   so a Recorder shared by concurrent transactions loses only the rows of
   the one that rolled back. A `RecordFunc`
   sees every Change as it happens. Its writes on the same transaction roll
   back with it; for side effects outside the database, collect with a
   `Recorder` and act after `Tx` returns.
6. **No recursion.** A write a sink makes through the recording handle with
   the ctx it is given is not recorded (an unexported context key on that
   ctx only). The audit row for a change is not itself audited. A write under
   another context is recorded and comes back to the sink.
7. **Old values are consistent with the write.**
   - **PostgreSQL ≥ 18:** a single statement,
     `UPDATE … RETURNING old.<cols>, new.<cols>`, and
     `DELETE … RETURNING old.<cols>`. An upsert uses `INSERT … ON CONFLICT
     … RETURNING old.<cols>, new.<cols>`, where old is NULL for an inserted
     row.
   - **PostgreSQL < 18, SQLite and MySQL:**
     - A locked read of the rows under the write's own scope (no
       `FOR UPDATE` on SQLite).
     - The write, narrowed to those keys, a chunk at a time.
     - New values by RETURNING, or by a SELECT by key on MySQL.
   - Outside a transaction a recorded write runs in a transaction of its
     own, but for one statement that commits by itself: an insert of one
     statement, and on PostgreSQL 18 an update or delete that pins one row
     by key, or an upsert of one statement. Its row count is checked against
     `MaxRows` (`ErrTooManyRows`, nothing kept).
   - The server version is read once per pool (`SHOW server_version_num`),
     only by recording handles, never seen by hooks, and not cached on
     failure. In a caller's transaction it is read inside the write's
     savepoint; for velox's own transaction, before BEGIN. A failed read
     never aborts a transaction; that write takes the locked read.
   - On PostgreSQL 18, Changes come in key order as Go compares keys (a text
     key under a collation other than C may differ), and an upsert's in
     RETURNING order.
8. **Upsert.** A row that conflicted and was updated is an update with its
   real old values. A row that was inserted is an insert. A row kept by
   `DoNothing` is not reported. Below PostgreSQL 18, the conflicting rows
   are read first, locked, by the conflict columns, without the scope or the
   Read policy.
   - PostgreSQL and SQLite: `RETURNING` every column; the rows returned are
     the rows touched.
   - MySQL: a read by the conflict values, checked against `RowsAffected`
     (`clientFoundRows=true`, which Connect requires). A row with NULL in a
     conflict column or a generated key left zero in the target is refused;
     a conflict on another unique key, or two rows of one InsertAll on one
     key, fails the write.
   - Below PostgreSQL 18 a concurrent insert of a new key can be reported as
     an insert (phantom); PostgreSQL 18 closes it. MySQL's gap locks can
     deadlock two recorded upserts of new keys (retryable).
9. **Soft delete.** softdelete's update reports `Op: OpDelete, Soft: true`,
   with old and new rows; `Changed` is false and `Diff` nil. The mark is
   engine-internal, so another soft-delete mixin's update is an update.
10. **Edges.**
    - A bidirectional one-to-one re-point reports each row it changes.
    - A many-to-many `Add` or `Remove` reports the join entity's inserts and
      deletes, both rows of a bidirectional link: a plain insert reports a
      link's row then its mirror, a `DoNothing` upsert in RETURNING order.
    - Rolling these up into owner-level edge changes is phase 2.
11. **Fail closed.** A recorded write that cannot account for every row it
    touched returns an error and is rolled back. A Rewrite hook may not
    change a recorded write's `RETURNING`.

### Cost

- A handle that does not record sends exactly what it sends today.
- Below PostgreSQL 18, one locked read per recorded update or delete (BEGIN
  and COMMIT for a pinned update outside a transaction); a SAVEPOINT and
  RELEASE per recorded write in a caller's transaction; a read per step of a
  bidirectional re-point.
- `Change` is not generic, and the recording code lives in the engine. The
  handles' `Old` and `New` add about 4 KB to each entity package.
- `RowsOf[E]` is instantiated only where it is called.

## Phase 2 (a separate plan)

- Owner-level many-to-many edge changes (`Change.Edges`).
- `AfterCommit`.
- Recording only some columns of a bulk write.
