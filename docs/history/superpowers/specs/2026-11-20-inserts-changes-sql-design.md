# Lean inserts with database defaults, change tracking, SQL terms

2026-11-20. Four requests from the user, after the CRUD comparison with
old velox:

1. velox2 must honour database defaults, by the schema's declaration.
2. An INSERT should list fewer columns: easier to read in a log.
3. A hook needs old values, and a way to log which fields changed.
4. SQL that the builders cannot say must be easy to write.

## 1+2. An INSERT lists the columns that carry a value (ADR 0093)

Today an INSERT writes every column but a zero generated key (ADR 0064):
a nil column as the literal `NULL`, a column with a database default as
its Go value. A default declared only in the database never applies to a
row velox writes.

Decision:

- The descriptor learns which columns have a database default the schema
  declared: `ColumnDescriptor.DBDefault`, set by velox-gen from
  `SchemaDefault` and from `sqlschema.Default`, `DefaultExpr` and
  `DefaultExprs`. A constant Go `Default` and ADR 0090's implicit zero
  `DEFAULT` do not set it: the first is filled in Go before the insert,
  and a database that velox did not migrate may lack the second.
- A column is left out of an INSERT when every row of the statement
  holds nothing for it:
  - a nullable column whose value is nil: the row stores NULL, or the
    column's default;
  - a `DBDefault` column whose value is the Go zero (nil, for a pointer):
    the row stores the database's default.
  Every other column is written. A zero in a NOT NULL column without a
  declared default is still written, as nothing else would fill it.
- The values the database filled are read back into the entity:
  `RETURNING` on PostgreSQL and SQLite, in the INSERT itself. MySQL has
  no `RETURNING`: one `SELECT` of those columns by key follows, only when
  such a column was left out. A key the database generates that is not
  an auto-increment integer cannot be found again on MySQL: such an
  insert is refused there, before anything is sent.
- `InsertAll` splits a batch into runs of consecutive rows that leave out
  the same `DBDefault` columns, so every row gets the same defaults as
  it would alone, on every dialect. Row order, and keys, are kept.
- The choice is made when the statement renders, as ADR 0064's literal
  `NULL` was: hooks and Rewrite hooks see every column, with the Go
  values; the shape key holds the set of columns left out.
- The rule is the one a Go `Default` already has: a zero value in a
  column with a default stores the default. A zero that must be stored
  in such a column goes in a nullable field, where a non-nil pointer to
  zero is a value.

Precedent: ent and old velox list only the fields a builder set (seen in
the CRUD round's PostgreSQL log). GORM leaves out a zero field that has a
`default` tag and reads it back with `RETURNING`.

Supersedes ADR 0064.

## 3. Old values and changes (ADR 0094)

Superseded by docs/history/superpowers/specs/2026-11-20-change-recording-design.md.

ent gives `OldX(ctx)` on a one-row update only, loaded by a query of its
own outside the update's lock. velox's hooks see bulk updates too.

Decision: a hook asks for the rows' state before the write with
`m.Track(names...)` (none: the columns the update assigns; on a delete,
every column) before it calls next. velox then, in one transaction (the
caller's, or its own):

1. reads the key and the tracked columns of the rows the statement will
   change, under the same scope, locked where the dialect can;
2. sends the UPDATE or DELETE narrowed to those keys;
3. for an update, reads the new values: `RETURNING` on PostgreSQL and
   SQLite, a `SELECT` by key on MySQL.

After next, `m.Changes()` yields one `velox.Change` per row: its key, and
for each tracked column the old and new values and whether they differ.
Equality is by value: an `Equal` method when the type has one
(`time.Time`, decimals), bytes for `[]byte` and JSON, else
`reflect.DeepEqual`. `Change.Changed()` lists only the columns that
differ, which is what an audit log writes. An insert's Changes have no
old values.

`velox.Diff[E](old, new *E)` compares two entities column by column, for
application code that holds both.

Track costs a statement, so nothing is read unless a hook asks.

## 4. SQL where the builders end (ADR 0095)

velox has `Expr` conditions, `SelectRaw`, `db.Raw[E]`, `db.Rows` and
`db.Exec`. Missing, for real applications:

- `velox.SQL[E](text, args...) Term[E]`: an expression as a term, for
  `OrderBy(velox.SQL[E]("similarity(?, ?)", Name, q).Desc())`, `Select`
  and `GroupBy`. Same text rules as `Expr`.
- `Col.SetSQL(text, args...) Setter[E]`: an assignment of an expression,
  `SET search = to_tsvector(?)`, `SET views = views * 2`.
- `docs/sql.md`: which tool fits which need, from a condition to a raw
  query, and which apply the policies.
</content>
</invoke>
