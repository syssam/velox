# 0093. An INSERT leaves out what the database fills, and reads it back

- Status: accepted, 2026-11-20. Supersedes [0064](0064-insert-null-literal.md).

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
The conflict target's columns are always written, and so is at least one
column.

The database's values come back into the entity:

- **PostgreSQL and SQLite:** the INSERT gets `RETURNING` with the generated
  key and every column it left to a declared default.
- **MySQL:** there is no RETURNING. The key comes from `LastInsertId`, and
  the defaulted columns are read by key after the insert, in the same
  transaction, without policies. For several rows with a one-column key,
  this is one `SELECT … WHERE id IN (…)`.
  - A key column whose value the database fills from a non-integer default
    is refused on MySQL before anything is sent, because it cannot be read
    back.
- Only the leaving out of a nil nullable column needs no read: the
  entity's nil already holds the stored NULL.

InsertAll gives every row what it would get alone:

- It splits its rows into runs that leave the same declared-default columns
  to the database, and sends one statement per run, in one transaction when
  there is more than one statement.
- Every row gets its generated key back, which before needed a loop of
  `Insert`. RETURNING returns rows in the order of `VALUES` on all three
  databases. MySQL's keys are consecutive from `LastInsertId` for one
  multi-row INSERT under the default `innodb_autoinc_lock_mode`, as ent
  assumes too. They are taken only when the INSERT has no ON CONFLICT and
  wrote every row.
- An InsertAll with `DoNothing` and more than one row reads nothing back.
  RETURNING leaves out the rows kept, so the rows returned do not line up
  with the rows sent.

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
- `GeneratedCodeIsVersion24`: descriptors carry `DBDefault`.
- An insert with a declared default sends one statement more on MySQL. The
  other databases send none more.
- 0064's literal `NULL` stays in the renderer, for an INSERT whose Rewrite
  hook puts back a column every row holds nil in. velox's own inserts no
  longer have such a column.
