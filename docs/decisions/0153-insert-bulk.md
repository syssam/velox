# 0153. InsertBulk is a bulk of single-row inserts, as ent's CreateBulk

- Status: accepted, 2026-11-24. Builds on [0093](0093-lean-inserts.md),
  [0082](0082-defaults-before-setters.md),
  [0136](0136-explicit-zero-and-empty-patch.md) and
  [0151](0151-explicit-aggregates-filter-entity-policy-db.md).

## Context

An application ported from ent has 179 calls of the shape

```go
builders[i] = tx.X.Create().SetA(a).SetCanEdit(false)
...
tx.X.CreateBulk(builders...).Save(ctx)
```

velox had two ways to write them, and neither fits:

- `db.InsertAll(xs)` takes values. A struct has no "set" bit, so a zero
  field is unset (0136): it gets its `Default` (0082) or its declared
  database default (0093). A deliberate `SetCanEdit(false)` on a column
  whose default is `true` stores `true`. The port compiles and stores the
  wrong value, with no error.
- `db.Insert(&x).Set(ss...)` stores a setter's zero, but it is one row and
  one statement.

## Decision

```go
func (db *DB) InsertBulk[E any, PE Entity[E]](qs ...InsertQuery[E]) InsertQuery[E]
func InsertBulk(db *velox.DB, t Table, qs ...TableInsert) TableInsert // package untyped
```

`InsertBulk` takes one `Insert` a row, as ent's `CreateBulk` takes one
create builder a row. It returns an `InsertQuery`, so `Exec`, `SQL` and the
upsert options are the ones an insert has.

### A row is its Insert alone

- Each row's columns are what its `Insert` would write: a column its
  setters (`Set`, `SetInput`) or its hooks' `Mutation.Set` name is written
  as the row holds it, its zero included; a column none names gets its
  `Default` or default function, filled before the setters (0082), or is
  left to its declared database default and read back (0093).
- Each row's hooks run on a mutation of that one row. They nest: row 0's
  hooks are the outermost and the statements are sent inside the last
  row's, as ent's `CreateBulk` chains its builders' mutators. A hook's
  `Mutation.Affected` is the whole bulk's count, as no driver reports a
  count per row of a multi-row INSERT.
- The validators run and the policy's `Insert` is asked for every row,
  inside the transaction velox begins when the bulk needs more than one
  statement (0151).
- Each row's generated key, and what the database filled, are read back
  into the entity its `Insert` points to.

### Rows are grouped by the columns they name

Rows whose setters and hooks name the same set of columns, whose key is
set or left to the database alike, and whose skipped `SetPtr` setters
leave an upsert's `Update` the same columns, are one group. A group is an
`InsertAll` of its rows (`engine.InsertAllRows`'s path, `TableRows` with a
list of row pointers): one multi-row INSERT, chunked by the same
parameter limits, with 0093's `DEFAULT` cells on PostgreSQL and MySQL and
its runs on SQLite. Groups go in the order their first row comes in `qs`,
so the database's keys follow `qs` within a group, not across groups.

- A uniform bulk, every row given the same setters, is one INSERT (or one
  a `WithMaxParams` chunk): `TestInsertBulkChunks` sends 5 rows as
  `[2 2 1]` under ten parameters.
- A mixed bulk is one INSERT a column set. `TestInsertBulk` sends five
  rows naming three column sets as three INSERTs of 3, 1 and 1 rows.
- More than one statement runs in one transaction, a savepoint inside the
  caller's, so the bulk is all or nothing (`TestInsertBulkAtomic`).
- A recording DB (0094) reports a Change for every row: each group goes
  through `InsertAll`'s recorded path (`TestInsertBulkRecord`).

### What InsertBulk refuses

`Exec` and `SQL` fail before anything is sent for a query of `qs` that
carries an upsert option of its own (`OnConflict` and the rest, or
`UpdateAll`), that was built on another DB (or, untyped, Table), that is an
`InsertAll`, `InsertRows` or `InsertBulk`, that has edge setters, or that
inserts a nil pointer, and for `Set` on the bulk. A query's `SetInput`
error is returned as it is, as its own `Exec` returns it. An `InsertBulk`
of no queries sends nothing and returns nil, and its `SQL` is "", as an
`InsertAll` of no entities.

An upsert option on the bulk applies to every row, as an `InsertAll`'s
does, with `InsertAll`'s limits: a multi-row upsert on MySQL reads nothing
back, and a group of several rows with `DoNothing` reads nothing back.
A row that sets a bidirectional one-to-one key is refused, as in an
`InsertAll`.

`SQL(ctx)` runs the hooks and asks the policy for every row, as `Exec`
would, and returns the first statement: the first row's group's first
chunk, with `InTx` set when the bulk sends more than one statement.

### Compile cost

The rows ride in `InsertQuery`'s `Setters` (`engine.BulkSetters`), and
`Exec` reaches them through `engine.Insert`, which it already calls: the
bulk takes the first row's pointer as `Insert`'s, so `Exec` passes it on.
A field of InsertQuery's own and a branch in `Exec` and `SQL` were
measured first: a branch cost an entity's package 1.5 KB in `Exec` and
0.8 KB in `SQL`, which an entity package compiles though it never calls
it. `SQL`'s work moved into the engine (`engine.InsertSQL`), which took
about 9 KB off: `entityDescUse` went from 795072 to 787420 bytes,
`entityDescHooks` from 880840 to 873342 and `entityDescMixin` from 926312
to 918822, the method's declaration (about 1.4 KB in every package that
names DB) included. The engine's bulk helpers are functions, not methods
of its `table`, which every package that describes a DB describes too.

## Consequences

- `CreateBulk(builders...)` ports as `InsertBulk(qs...)` with each builder
  an `Insert(&rows[i]).Set(...)`. A setter's zero is stored per row.
- `InsertAll` stays for values: rows built as structs, whose zero fields
  mean unset.
- A row's edges are written with `Insert`: `InsertBulk` refuses an edge
  setter where ent's `CreateBulk` takes one.
- No generated-code change; `GeneratedCodeIsVersion` stays.
