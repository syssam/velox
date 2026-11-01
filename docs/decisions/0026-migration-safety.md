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
- A MySQL `MODIFY COLUMN` replaces the column from the snapshot, on a change of the
  comment too. `ON UPDATE CURRENT_TIMESTAMP`, a collation other than the table's,
  `INVISIBLE` and a generation went with it; so did what the model does hold, when
  the database differed: a comment-only plan turned a hand `decimal(12,4)` 12.3456
  into 12.35 and a hand `NOT NULL` into `NULL`, a widening lost a hand default and
  comment, and an id's retype lost `AUTO_INCREMENT`.
- A PostgreSQL `ALTER COLUMN TYPE` takes the new type's collation, storage and
  compression, so widening a `COLLATE "C"` column lost it; and the plan's retype
  drops the snapshot's default and sets it after, which replaced a hand 42 with 1.
- An index plan drops and makes an index from the model. The `DROP INDEX
  CONCURRENTLY IF EXISTS` of an add-index plan, there to clear a failed build,
  removed a hand `UNIQUE (lower(a)) WHERE`, and a change lost an operator class,
  `INCLUDE`, fillfactor, or on MySQL `UNIQUE`, a prefix or `DESC`.
- MySQL's `RENAME COLUMN` and `RENAME TABLE` run past a view, which then fails 1356,
  and a trigger, whose `INSERT` then fails 1054. A SQLite `-allow drop-table` left
  another table's trigger and a view naming the table, and so broke later writes and
  the next rebuild. MySQL's `DROP COLUMN` shrank a hand `UNIQUE (a, b)` to `UNIQUE (a)`.
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
lines, and `-- velox:allow unmodeled-loss [<object>]`. A plan writes `drops`, on every
dialect, for what it changes by design, by the attribute where it can (`t.t_n`,
`t.(s)`, `t.s:default`, `t.n:not-null`, `t.c:type`, `t.c:comment`, `t.id:identity`,
`c.c_fk:on-delete`, `t.x:columns`), so an ordinary migration needs no directive and a
hand-added thing of another attribute is still seen. `t.c` accepts each of `t.c:…`.
`-allow unmodeled-loss=<object>` writes the directive at plan time. The history's
checksum leaves `allow` and `drops` lines out, so adding one strands no environment
that already applied the file; `velox.sum` covers the whole file, so `migrate hash`
follows the edit.

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
  has not dropped at that point, unless a directive accepts that child. With foreign
  keys on the drop deletes the rows at once, so a child rebuilt later copies nothing
  and does not count; with them off, a rebuild that lets go of the key accounts for
  it. `LintFile` also flags a rebuild without `foreign-keys off`, as a plan always
  writes it.
- A SQLite `DROP TABLE` that leaves a view or another table's trigger naming the table
  is refused, naming each, unless the file drops it, makes the table again or a
  directive accepts it. So is a primary key `DESC`: `INTEGER PRIMARY KEY DESC` is no
  rowid alias, so a rebuild renumbers, and `PRIMARY KEY (id DESC)` loses the order.
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
  Every pending file counts as later, past `-to` too: a key dropped under `-to` and
  added again beyond it is refused at the drop. An unqualified PostgreSQL name is
  resolved as the server does, by `to_regclass`, so with `search_path=app,public` a
  `t` in `public` is read there.
- What a statement writes is compared with what the database holds, attribute by
  attribute. A MySQL `MODIFY` or `CHANGE` states the whole column, so its type,
  nullability, default, comment and `AUTO_INCREMENT` are each compared, spellings of
  one value apart, as drift reads them; what differs needs `t.c:type`, `t.c:not-null`,
  `t.c:default`, `t.c:comment` or `t.c:identity`. A hand `MODIFY` that means to change
  the type says so with the same line, which is the price of telling it from a stale
  plan. A PostgreSQL `ALTER COLUMN TYPE` states the type and nothing else, so the type
  needs no line; a default that its statement drops or sets is compared, under
  `t.c:default`. A storage or compression must be set again by a statement of the file.
- An index dropped and made again under one name, here or in a later pending file, is
  compared with the statement that makes it: `unique`, `columns`, `where`, `method`,
  `key-options` (operator class, collation, `DESC`, prefix), `include`, `with`,
  `tablespace`, `comment`, `nulls`, `invisible`, as `t.x:<attr>`. So an add-index
  plan's `DROP INDEX CONCURRENTLY IF EXISTS` goes through for an invalid index or one
  of the plan's shape, and a valid other one is refused.
- MySQL's `RENAME COLUMN`, `CHANGE` to another name, `DROP COLUMN`, `RENAME TABLE` and
  `DROP TABLE` read the database's views and triggers as words. One that names the
  table and the column, or a trigger of the table that names the column, is refused
  by its name unless the file drops or remakes it or a directive accepts it. A table's
  own trigger moves and goes with it. `DROP COLUMN` of a multi-column index's column
  is refused as `t.x`, which `-allow drop-column` does not cover, unless the file drops
  the index or all its columns go.
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
  `WITH` bodies, `MERGE` and `UPDATE OR`. `DROP DOMAIN`, `DROP TYPE`, `DROP COLLATION`
  and `DROP PARTITION` are `drop-domain`, `drop-type`, `drop-collation` and
  `drop-partition`, `TRUNCATE PARTITION` is `delete-rows`. Render's own `DROP TYPE`
  without `CASCADE`, of a type no column uses, is not flagged.
- `Up` refuses a SQLite database in memory that ends with its connection, where it
  returned nil and left no table, as `:memory:`, `file::memory:`, `mode=memory` and an
  empty name. It writes a probe table and looks for it from a second connection of the
  pool, so a shared-cache database that another connection keeps passes.
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
- A line for an attribute accepts whatever the database holds of it: a plan that
  widens `decimal(12,2)` to `(14,2)` over a hand `(12,4)` rounds unseen, on MySQL behind
  `t.c:type`, on PostgreSQL because a retype's type is not compared at all. The same
  goes for `:default`, and for an index's `:columns`.
- A file from before these lines is refused on MySQL at its first `MODIFY` that
  changes something, and on PostgreSQL and MySQL at an index it changes, until it gets
  the line: the safe side. `migrate hash` follows; the history's checksum stays.
- A constraint renamed and then added again in one file is not matched by its old
  name. `DROP CONSTRAINT IF EXISTS` and `ADD` of a key or check plan, and a `DROP` and
  `ADD` in general, count as the file's own for what the model holds; so does `COLLATE
  "default"` as a statement's collation.
- An index made again in a later file is compared only for what the model has no
  place for, as `unique`, `columns` and `where` may be another plan's work. So a hand
  `UNIQUE` is not seen when the plan drops in main and builds in the online file, which
  it does only when the name is taken or a dropped column is on the index. SQLite's
  `DROP INDEX` and `CREATE INDEX` outside a rebuild are not compared, and an expression
  key is compared as written, so a hand remake of one PostgreSQL prints otherwise is
  refused until `t.x:columns` says it.
- Views and triggers are read as words: an alias of a column's name refuses falsely,
  and a routine, an event, another database's view, or a view whose text the user may
  not read (no `SHOW VIEW`) is not seen. PostgreSQL follows a rename in views itself,
  but a PL/pgSQL body naming the old name breaks unseen.
- The walk resolves a name when it reads it. A `CREATE TABLE` without a schema is taken
  as made in `Up`'s, the first of the path.
- A shared-cache database whose only other connection belongs to another pool is
  refused falsely under `SetMaxOpenConns(1)`, and one in a pool with `SetMaxIdleConns(0)`
  and nothing holding a connection passes and ends with `Up`.
- `SELECT somefunc()` that alters a table is not seen. PostgreSQL `DO` and function
  bodies are not parsed: `DO` is refused when any such attribute exists, and a false
  refusal of that kind needs the directive.
- A PostgreSQL domain target keeps its `USING` with only a finding, so it may cut a
  value if the finding is allowed.
- A pending generated SQLite file from before `velox:drops` is refused until it gets
  the directive or an allow: the safe side.
- A hand-written `velox:drops` line is an allow by another name.
- Whether a MySQL `DROP COLUMN` of an index's column would break the index's uniqueness
  is not read from the rows; it is refused in any case.
- SQLite check text is compared by the normaliser, so a check spelled otherwise in a
  rewritten rebuild, which it cannot parse as the same, is refused.
- `LintFile` judges `MERGE` by its `WHEN` clauses, not by what they reach.
- `migrate drift` cannot report a hand-added column while a file is pending.
