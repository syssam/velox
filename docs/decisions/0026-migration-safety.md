# 26. Up refuses to destroy what the model cannot hold

Status: accepted, 2026-11-01.

## Context

A plan is made from two schemas and no database, so it writes only what the
model holds. Two statements it writes replace a whole definition:

- A SQLite rebuild makes a new table from the model, copies the rows and drops
  the old one. A unique expression index, a trigger, `COLLATE NOCASE`, a
  generated column, `ON UPDATE CASCADE`, `DEFERRABLE`, `ON CONFLICT`, `STRICT`
  and `WITHOUT ROWID` went with it. The plan said only `sqlite-rebuild`, `Up`
  succeeded, and `Drift` then reported nothing, since the thing was gone.
- A MySQL `MODIFY COLUMN` replaces the column. `ON UPDATE CURRENT_TIMESTAMP`, a
  collation other than the table's, `INVISIBLE` and a generation went with it,
  on a change of the comment too, which MySQL can write no other way.

A view that names a rebuilt table, or another table's trigger that does, made
the rebuild's `RENAME` fail: a plan that could never run.

## Decision

Only the database knows, so the check is at apply time, in `Up`, and so in
`velox-gen migrate apply`. The refusal is an `*Error` of Code `unmodeled` that
names each object and says what to do.

- SQLite. A rebuild is recognised by its statements: `CREATE TABLE` of
  `ddl.Rebuild(t)`, and `ALTER TABLE` of it `RENAME TO t`, so it survives a hand
  edit of the file. Ahead of the rebuild's first statement `Up` refuses a view or
  a trigger of another table that names `t`, and reads `t`'s notes, Inspect's
  "the model cannot hold" warnings, to which triggers and `ON CONFLICT` are
  added. After the file's last statement, still in the transaction, it reads
  them again: a note that is gone is a loss. So what the file makes again after
  the rebuild is no loss and needs no directive. Refusing rolls the file back.
- MySQL. DDL has no transaction, so `Up` reads the file before it starts, before
  any history row: each `MODIFY` or `CHANGE COLUMN` of a column with such an
  attribute must write it (`ON UPDATE`, `COLLATE`, `INVISIBLE` or `VISIBLE`, or
  `AS`). A rename earlier in the file is followed back to the name the database
  holds. So a comment-only change is refused until the file keeps the attribute.
- `-- velox:allow unmodeled-loss [<object>]` accepts a loss, as `-allow
  unmodeled-loss=<object>` writes it at plan time. It does not cover a view or
  a trigger that blocks the rename, as SQLite refuses that anyway: the file drops
  and remakes the view; a trigger, which a file cannot hold, is handled by hand.
- A column dropped under `-allow drop-column` takes what it has with it.

The checks of a pending file run when `Up` reaches it, as an earlier pending file
may add or remove the object. Files before it stay applied.

Beside this, in the same spirit, each found by running it:

- A SQLite type change is destructive by affinity. A rebuild copies through the
  new column's affinity, so `text` to `integer` turned `'007'` into `7`.
- PostgreSQL leaves narrowing to a string or bit type of limited length to the
  assignment cast. `USING c::varchar(5)` cut the row silently. Other changes keep
  `USING`: where an assignment cast also exists, both read a value alike.
- `ALTER SEQUENCE … AS` follows a serial's type. An identity's follows on its own.
- A column a foreign key references is renamed in place before a rebuild, as only
  `ALTER TABLE` rewrites the child's `REFERENCES`.
- An allow directive's target is one word, or in Go's double quotes when it has
  white space or a leading quote, so any legal identifier round-trips and a plain
  target's line, and so an applied file's checksum, does not change.
- `WithTarget(0)`, `-to 0` and `resolve -version 0` mean version 0.
- `Verify` lists the database's tables, which `Drift` by design does not.
- `foreign_key_check` covers the tables a file touches and their children, or the
  whole database when a statement cannot be read or a touched table has a trigger.
- `LintFile` reads files with the generated header, as a header proves nothing.

## Alternatives

- Modelling the attributes in `Column` and `Table`. The list has no end (`STRICT`,
  `ON CONFLICT`, DESC, every future clause), and an object in the schema DSL would
  be needed for each. A guard covers what is not listed yet in the same way, once
  Inspect notes it.
- `PRAGMA legacy_alter_table = ON` to let a rename run past a view. It would keep
  a view whose column the same plan renames, broken and silent.
- Detecting rebuilds by the generated header or the `-- review:` line. A hand edit
  keeps both or loses both; the statements are what runs.
- A hash of the body in the generated header, so that `LintFile` could tell a
  hand edit exactly. Files already written have none, and the edits the guard asks
  for (a directive) would void it. `LintFile` therefore skips `alter-type` in a
  file with the header, since Render writes a widening or a comment change with no
  directive to cover it; a type change added by hand there goes unseen.

## Limits

- A hand-written rebuild under another temporary name is not recognised.
- A column or index added to the database by hand, which the model could hold but
  the snapshot does not, is still lost by a rebuild. Without the older snapshot
  `Up` cannot tell it from a rename the plan made. `migrate drift` reports it
  before the rebuild runs.
- A MySQL refusal is checked on the statements as written, so `ON UPDATE` written
  with another expression counts as kept.
