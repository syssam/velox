# 0093. An INSERT leaves out what the database fills, and reads it back

- Status: accepted, 2026-11-20. Supersedes [0064](0064-insert-null-literal.md).
  Amended by [0124](0124-explicit-null-in-inserts.md): a nil column a setter
  stored (`SetNull`) is written as NULL, not left out.

## Context

An insert wrote every column. A nil nullable column went out as the literal
`NULL` (0064), and a zero value went out as itself. So:

- A database default never applied to a row velox wrote. A schema that
  declared `sqlschema.Default("CURRENT_TIMESTAMP")` or
  `field.SchemaDefault(...)` got the `DEFAULT` in its migration, but velox's
  insert overrode it. The default was only for rows written outside velox.
- A logged INSERT listed every column, most of them `NULL` or zero. It was
  harder to read than ent's or old velox's, which list only the columns set.

ent and old velox leave out every field the builder did not set. GORM leaves
out a zero field that has a `default` tag and reads it back with RETURNING.
velox has no "set" bit on a struct field, so "unset" has to be read from the
value itself.

## Decision

A column is left out of an INSERT when every row leaves it empty. A cell is
empty when it is one of these:

- a nil value in a nullable column (a nil pointer, a nil `Null*`, a nil slice
  or map whose column holds NULL);
- a value whose codec encodes it to nil;
- the zero value in a column with a database default the schema declares on
  this dialect.

A declared database default is `field.SchemaDefault`, or `sqlschema.Default`,
`DefaultExpr` or `DefaultExprs`. velox-gen writes its dialects into the
column's `ColumnDescriptor.DBDefault`. The following are not counted as
declared database defaults:

- A Go `Default`. It fills the struct before the hooks run (0082), so the
  value is already set.
- The implicit zero `DEFAULT` that the migration gives a NOT NULL column
  (0090). It equals the zero value, so writing the zero gives the same row.

A NOT NULL column without a declared default is written whatever it holds.
The conflict target's columns are always written, and so is every column an
upsert's update branch copies from the inserted row: MySQL's `new.<col>`
names only a column the INSERT lists, and PostgreSQL's `EXCLUDED.<col>`
would hold the column's DEFAULT, not the row's NULL.

Validation follows the same rule (amended 2026-11-20, A++ round D10). A
value an insert leaves to the database's default skips the field's
validators, since the database stores its default, not the zero: an enum
whose `SchemaDefault` is `'new'` inserts with its zero value, and the row holds
`new`. The test is the one that leaves the cell out (`leftToDB`), on the
insert's dialect, for `Insert`, `InsertAll` and an upsert's inserted rows.
A column the conflict clause writes is sent as the row holds it, and is
validated as before; so is a value a hook set, and every value an `Update`
sets, the zero included.

An insert whose every column is left empty lists none: `INSERT INTO t
DEFAULT VALUES` on PostgreSQL and SQLite, `INSERT INTO t () VALUES ()` on
MySQL, so every declared default applies. Writing one column as the row
held it would store its zero value over the default. `DEFAULT VALUES`
writes one row, so an InsertAll of several such rows keeps one column on
PostgreSQL, whose cells are then `DEFAULT`, and sends each row alone on
SQLite; MySQL writes `() VALUES (), ()`.

The database's values come back into the entity:

- **PostgreSQL and SQLite:** the INSERT gets `RETURNING` with the generated
  key and every column it left to a declared default.
- **MySQL:** there is no RETURNING. The key comes from `LastInsertId`, and
  the defaulted columns are read by key after the insert, without
  policies; inside the caller's transaction when there is one. For several
  rows with a one-column key, this is one `SELECT … WHERE id IN (…)`.
  - A single-row upsert reads them by its conflict values instead, with
    the generated key, as `LastInsertId` does not name a row the upsert
    updated: the entity gets the stored row's key, as RETURNING gives it
    on PostgreSQL and SQLite. A key the caller set is not read, as
    RETURNING names none then. A `DoNothing` upsert reads nothing back:
    under `clientFoundRows` its count is 1 whether it inserted or kept the
    row, and the kept row's values are what PostgreSQL's RETURNING, giving
    no row, never hands the entity. So a MySQL `DoNothing` that does insert
    gets neither its generated key nor its declared defaults, where
    PostgreSQL and SQLite return both. The database compares the values, as
    it did for the conflict. The upsert and the read run in one
    transaction, velox's own outside the caller's. A row whose conflict
    values cannot find it (a NULL, a generated key left zero) keeps its Go
    values. On a table with several unique keys, `ON DUPLICATE KEY UPDATE`
    fires on a conflict with any of them, so the read by the conflict
    target's values can then find another row than the one updated, or none.
  - A multi-row upsert does not read its defaulted columns back: its rows
    cannot be told apart by key without a read per row. They keep their Go
    values.
  - A key column whose value the database fills from a non-integer default
    is refused on MySQL before anything is sent, because it cannot be read
    back.
- Only the leaving out of a nil nullable column needs no read: the
  entity's nil already holds the stored NULL.

InsertAll gives every row what it would get alone:

- On PostgreSQL and MySQL it lists every column some row writes, and a
  cell a row leaves to its declared default is the `DEFAULT` keyword, which
  takes no parameter. The rows are one statement, or one per
  `WithMaxParams` chunk, in one transaction when there is more than one.
  Every column with a `DEFAULT` cell is read back, for every row. A
  conflict column, and a column the update branch copies, is written as the
  rows hold it, as above. Where the `DEFAULT` cells fall is data, so such a
  statement is rendered each time rather than cached (a 4-row InsertAll:
  about 0.7 µs and 11 allocations more than a cached one, against a
  statement per run before).
- On SQLite, which has no `DEFAULT` in `VALUES`, it splits its rows into
  runs that leave the same declared-default columns to the database, and
  sends one statement per run, in one transaction when there is more than
  one statement.
- Every row gets its generated key back, which before needed a loop of
  `Insert`. RETURNING returns rows in the order of `VALUES` on all three
  databases. MySQL's keys are `@@auto_increment_increment` apart from
  `LastInsertId` for one multi-row INSERT under the default
  `innodb_autoinc_lock_mode`, as ent assumes too (with a step of 1). velox
  reads the step once per DB, before the first such INSERT, and refuses the
  insert unsent when it cannot. The keys are taken only when the INSERT has
  no ON CONFLICT and wrote every row.
- An InsertAll with `DoNothing` and more than one row reads nothing back.
  RETURNING leaves out the rows kept, so the rows returned do not line up
  with the rows sent.
- An upsert of several rows whose update branch has a condition (the
  policy's, an interceptor's, or one a Rewrite hook added) returns only the
  rows it inserted or updated. Its RETURNING then also names the conflict
  columns (added after the Rewrite hooks and the scope), and when fewer rows
  come back than were sent, each is given to the one entity whose conflict
  columns hold exactly the values the row holds. A row that matches no
  entity, or more than one, fails the write: the database may store a value
  in another form (a collation's `alice@x` for `Alice@x`, a padded CHAR),
  and a guess could give an entity another row's key. So that the failure
  rolls back, such an InsertAll runs in a transaction when the entity has
  an interceptor or a Read or Write policy, or the DB has a Rewrite hook,
  and the rows read anything back. A recorded upsert, which returns every
  column, places its rows the same way. Without a condition every row comes
  back, in order, and is placed by position.

Columns are left out in `engine.PlanInsert`, after the hooks and before the
Rewrite hooks. A Rewrite hook sees the statement as it will be sent. Its
shape key, the columns, keeps the render cache right without 0064's literal
mask.

Typed InsertAll uses the untyped API's batch insert (`engine.InsertAllRows`)
instead of compiling one for each entity. An entity package is about 45 KB
smaller (`compilecost_test.go`).

## Consequences

- A nil nullable column without a declared default is left out, as ent and
  old velox leave it out. If the table has a `DEFAULT` the schema does not
  declare, that default applies, where before NULL was stored. Declare the
  default with `sqlschema.Default`, or give the field a value.
- A zero value in a column with a declared default cannot be stored through
  `Insert`: the default replaces it. This matches GORM's `default` tag. To
  store the zero, make the field Nillable and set a pointer to the zero
  (a non-nil value is always written), or `Update` the row after the insert.
  velox-gen warns of a field that is not Nillable whose declared default is
  a non-zero literal of its type (`true`, a number other than 0, a
  non-empty string): "the zero value cannot be stored through Insert; make
  it Nillable to store it". A Go `Default` beside it does not help: a
  setter's zero is still left to the database. An enum, whose zero is none
  of its values, a zero literal and an expression (`now()`) get no warning.
- `GeneratedCodeIsVersion24`: descriptors carry `DBDefault`.
- An insert with a declared default sends one statement more on MySQL, and
  a single-row upsert that leaves a column to one, or whose key is
  generated, also opens a transaction for it outside the caller's. The
  other databases send none more.
- 0064's literal `NULL` stays in the renderer, for an INSERT whose Rewrite
  hook puts back a column every row holds nil in. velox's own inserts no
  longer have such a column.
