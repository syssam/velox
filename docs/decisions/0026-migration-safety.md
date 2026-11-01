# 26. Up refuses to lose what a file does not say it loses

Status: accepted, 2026-11-01.

## Context

A plan is made from two schemas and no database, so it writes only what the
snapshot holds. Several statements it writes replace a whole definition:

- A SQLite rebuild makes a new table from the snapshot, copies the rows and drops
  the old one. A column added by hand or by another branch, a `UNIQUE`, a check, a
  foreign key's action, `AUTOINCREMENT`, a default, a unique expression index, a
  trigger, `COLLATE NOCASE`, `STRICT` and `WITHOUT ROWID` went with it. The plan
  said only `sqlite-rebuild`, `Up` succeeded, and `Drift` then reported nothing,
  since the thing was gone. The rowids of a table keyed otherwise were renumbered,
  which corrupts external-content FTS5.
- A MySQL `MODIFY COLUMN` replaces the column, on a change of the comment too, and
  `ON UPDATE CURRENT_TIMESTAMP`, a collation other than the table's, `INVISIBLE`
  and a generation went with it.
- A PostgreSQL `ALTER COLUMN TYPE` takes the new type's collation, so widening a
  `COLLATE "C"` column lost it.
- A retype of a foreign key's column drops the key and adds it from the model, so
  `ON UPDATE` and `DEFERRABLE` went on PostgreSQL and MySQL.
- A SQLite `DROP TABLE` with foreign keys on deletes the rows of tables that
  reference it, modeled or not. Under `-allow drop-table` that took an unmodeled
  child's rows; a rebuild whose `foreign-keys off` was taken out took every child's,
  and `Up` returned nil.
- MySQL without a strict `sql_mode` cuts a string, turns a NULL into `''` and clamps
  a number, and reports success.

A view that uses a rebuilt table, or another table's trigger that does, made the
rebuild's `RENAME` fail: a plan that could never run.

## Decision

One idea, not a list of cases. Only the database knows what it holds, so the check
is at apply time, in `Up`, and so in `velox-gen migrate apply`. Before a file's
statements take effect `Up` reads what the database holds for the tables they
replace and compares it with the file's change list. What would go and is not
accounted for is a refusal, an `*Error` of Code `unmodeled`, that names each object
and says what to do.

The change list is what the file says: its statements, `-- velox:drops <target>`
lines, and `-- velox:allow unmodeled-loss [<object>]`. A plan writes `drops` for what
its SQLite rebuild takes away by design (`t.t_n`, `t.(s)`, `t.s:default`,
`t.n:not-null`, `t.c:type`, `t.id:identity`), so an ordinary migration needs no
directive and a hand-added one of the same kind is still seen. `-allow
unmodeled-loss=<object>` writes the directive at plan time, on every dialect.

- SQLite. A rebuild is recognised by its statements, so it survives a hand edit: a
  `CREATE TABLE` of a temporary name and `ALTER TABLE` of it `RENAME TO t`, the name
  being `ddl.Rebuild(t)` or the file also having `DROP TABLE t`. Ahead of it `Up`
  reads `t`: its columns, definition and notes, Inspect's "the model cannot hold"
  warnings, to which triggers and `ON CONFLICT` are added. After the file's last
  statement, still in the transaction, it reads `t` again. A column the `INSERT`'s
  `SELECT` list does not read is a loss; so is a column's type, `NOT NULL`, default
  or `AUTOINCREMENT`, an index, a foreign key, a check, the primary key or a note
  that is gone, with the `INSERT`'s renames followed. What the file makes again is no
  loss. What is over a dropped column goes with it. Refusing rolls the file back.
- A view or another table's trigger that uses `t` is refused ahead of the rebuild.
  SQLite says which itself: a rename of `t` in a savepoint rewrites its uses, and
  only them, so a column named `t` of another table is not one. If that rename fails,
  on a view already broken, the text is read, which errs towards refusing.
- A `DROP TABLE` of a table others reference is refused, naming each child the file
  neither drops nor rebuilds, unless a directive accepts that child. A rebuild's drop
  does the same only with foreign keys on, where every child counts. `LintFile` also
  flags a rebuild without `foreign-keys off`, as a plan always writes it.
- A rebuild copies the rowid of a table whose key is not its rowid, under whichever
  of `rowid`, `_rowid_`, `oid` no column takes. A `WITHOUT ROWID` table has none, and
  `Up` refuses until the name leaves the `INSERT`.
- MySQL and PostgreSQL. DDL does not always roll back, so `Up` walks the file before
  it starts, before any history row, following a statement's case, qualified names,
  `USE`, `RENAME`, `CHANGE` and `SET SCHEMA` back to what the database holds, and
  skipping columns and tables the file made. A `MODIFY`, `CHANGE`, `CONVERT TO` or
  `ALTER COLUMN TYPE` of a column with such an attribute must write it (`ON UPDATE`,
  `COLLATE`, `INVISIBLE` or `VISIBLE`, `AS`). A constraint with one (`ON UPDATE`,
  `DEFERRABLE`, `MATCH`, `EXCLUDE`, `NOT ENFORCED`) that is dropped and added again,
  in the file or a later pending one, must write it where it is added; dropped and
  not added again is the file's own change. A statement or action it cannot read
  (`EXECUTE`, `CALL`, `DO`, `SET search_path`, an unknown `ALTER` action) is refused
  on a table, or for `EXECUTE` and the like a database, with such an attribute.
- A plan cannot read a collation, so widening a collated PostgreSQL column stays
  written without it, and this guard refuses it until the file writes it.
- MySQL's `Up` adds `STRICT_ALL_TABLES` to its own session's `sql_mode` when it is
  missing, and gives the connection its mode back. A server setting does not change.
- `-- velox:allow unmodeled-loss` does not cover a view or a trigger that blocks the
  rename, as SQLite refuses that anyway: the file drops and remakes the view; a
  trigger, which a file cannot hold, is handled by hand.
- A column dropped under `-allow drop-column` takes what it has with it.

The checks of a pending file run when `Up` reaches it, as an earlier pending file
may add or remove the object. Files before it stay applied.

`migrate drift` cannot do this job: it refuses while a file is pending, and the
directory holds no snapshot per version to compare an applied state with.

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
- `foreign_key_check` also covers the whole database when a table the file touched,
  and dropped, had a trigger, which may have written anywhere first.
- `LintFile` reads files with the generated header, as a header proves nothing, and
  `WITH` bodies, `MERGE` and `UPDATE OR`.
- A plan refuses what can never run or would cut a value whatever it says: a `USING`
  cast to a limited type becomes the assignment cast, a truncating target under
  `alter-type` is a finding, an enum reorder over an array column and a `CONVERT TO`
  a collation of another character set are refused.

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

- A rebuild written rename-first (`ALTER TABLE t RENAME TO old`, `CREATE TABLE t`) is
  not recognised, nor a MySQL shape swap done with `RENAME TABLE` around a new table.
- Statements are read as written. `ON UPDATE` with another expression, or `NOT
  DEFERRABLE`, counts as kept. Any name in a rebuild's `SELECT` list counts as copied.
- A PostgreSQL or MySQL index dropped and created again under the plan loses what
  its note names unseen; only columns and constraints are walked. A constraint renamed
  and then added again in one file is not matched by its old name.
- `SELECT somefunc()` that alters a table is not seen. PostgreSQL `DO` and function
  bodies are not parsed: `DO` is refused when any such attribute exists, and a false
  refusal of that kind needs the directive.
- A PostgreSQL domain target keeps its `USING` with only a finding, so it may cut a
  value if the finding is allowed.
- A pending generated SQLite file from before `velox:drops` is refused until it gets
  the directive or an allow: the safe side, and a hash re-run is needed.
- A hand-written `velox:drops` line is an allow by another name.
- SQLite check text is compared by the normaliser, so a check spelled otherwise in a
  rewritten rebuild, which it cannot parse as the same, is refused.
- `LintFile` judges `MERGE` by its `WHEN` clauses, not by what they reach.
- `migrate drift` cannot report a hand-added column while a file is pending.
