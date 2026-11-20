# Change recording: design

Status: proposed, 2026-11-20. Replaces the hook API (`Mutation.Track`,
`Mutation.Changes`) of ADR 0094 as built on branch `lean-inserts` (not pushed).

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
    batch.Add(c.Entity, c.Key, c.Op, c.OldValues(), c.NewValues(), c.ChangedFields())
}
if old, cur, ok := velox.RowsOf[Invoice](c); ok && old.Status != cur.Status { … }
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

func (db *DB) Record(s Sink, opts ...RecordOption) *DB
type RecordOption struct{ /* unexported */ }
func RecordIgnore(columns ...string) RecordOption

type Change struct {
    Entity string // the entity's name, "Invoice"
    Op     Op     // OpInsert, OpUpdate or OpDelete
    Key    any    // the primary key's value; []any for a composite key
    Soft   bool   // a soft delete: Op is OpDelete, the row was updated
    // unexported: the entity and the old and new rows
}
func (c Change) OldValues() map[string]any   // nil for an insert
func (c Change) NewValues() map[string]any   // nil for a delete
func (c Change) ChangedFields() []FieldChange // updates only; nil otherwise
func RowsOf[E any, PE Entity[E]](c Change) (old, cur *E, ok bool)

type FieldChange struct{ Name string; Old, New any }
func Diff[E any, PE Entity[E]](before, after *E) []FieldChange // kept
```

The following are removed:

- `Mutation.Track` and `Mutation.Changes`.
- The agent's `Change` (`Key []any`, `Fields`, `Changed()`).
- `FieldChange.Changed`.

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
3. **Values.** Map keys are column names. Values are plain Go values, as
   `Mutation.Value` gives them; a JSON column holds its decoded field value.
   - A `Sensitive` column's value is `"***"` in the three methods. It is
     real in `RowsOf`.
   - `RecordIgnore` columns are left out of `ChangedFields` only; the
     snapshots keep every column.
   - `NewValues` of an insert includes what the database filled (ADR 0093).
4. **When the sink runs.** It runs after the write's statements succeed and
   before the write method returns, once per Change, in order. A sink error
   is the write's error.
   - Inside a transaction, the sink runs in it. An audit row written on the
     transaction commits or rolls back with the change.
   - Outside one, the write is committed when the sink runs, and an error
     undoes nothing. This holds on every database and version.
5. **Rollback.** For a `*Recorder`, velox drops the Changes recorded since a
   transaction or savepoint began when that transaction or savepoint rolls
   back, and before each retry of `TxRetrySerializable`. A `RecordFunc`
   sees every Change as it happens. Its writes on the same transaction roll
   back with it; for side effects outside the database, collect with a
   `Recorder` and act after `Tx` returns.
6. **No recursion.** A write a sink makes through the recording handle, while
   that sink runs, is not recorded. The audit row for a change is not itself
   audited.
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
   - An update or delete that does not pin one row by key runs in a
     transaction of its own when it is outside one. Its row count is checked
     against `MaxRows` (`ErrTooManyRows`, nothing kept).
   - The server version is read once per pool (`SHOW server_version_num`).
8. **Upsert.** A row that conflicted and was updated is an update with its
   real old values. A row that was inserted is an insert. A row kept by
   `DoNothing` is not reported. Below PostgreSQL 18, the conflicting rows
   are read first, locked, by the conflict columns.
9. **Soft delete.** softdelete's update reports `Op: OpDelete, Soft: true`,
   with old and new rows.
10. **Edges.**
    - A bidirectional one-to-one re-point reports each row it changes.
    - A many-to-many `Add` or `Remove` reports the join entity's inserts and
      deletes.
    - Rolling these up into owner-level edge changes is phase 2.
11. **Cost.**
    - A handle that does not record sends exactly what it sends today.
    - `Change` is not generic, and the recording code lives in the engine,
      so no entity package grows.
    - `RowsOf[E]` is instantiated only where it is called.

## Phase 2 (a separate plan)

- Owner-level many-to-many edge changes (`Change.Edges`).
- `AfterCommit`.
- Recording only some columns of a bulk write.
