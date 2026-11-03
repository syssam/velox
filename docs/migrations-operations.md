# Operating migrations

What to do when `migrate apply` refuses. Each recovery here is run by
`TestOperationsDoc` in `cmd/velox-gen`. The commands take `-dir` and `-db`, or
`$VELOX_DB`; they are left out below. None of them changes the schema: `resolve` only
writes the history table.

| Way out | When | What it does |
| --- | --- | --- |
| `resolve -version N -applied` | a dirty version you finished by hand, or a pending file the database already holds | checks the database, then records the file as applied, running nothing |
| `resolve -version N -undone` | a dirty version you reverted by hand | deletes the row, so the file runs again |
| `resolve -version N -statements K` | a dirty version whose count is behind what ran: MySQL, or a PostgreSQL no-transaction file | records that the first K statements ran; `apply -resume` runs K+1 on |
| `resolve -version N -adopt` | an applied version whose file changed, and the database holds what the file does | checks the database, then records the file's checksum and name, running nothing |
| `resolve -version N -forget` | an applied version whose file is gone, differs, or never ran | deletes the row and undoes nothing; prints what the database keeps |

## What -applied and -adopt check

`-applied` and `-adopt` say the database holds what the directory makes. They
compare before they record. With M the highest version the history would then
hold, the database must match what the directory says it holds at M:

- `schema.json`, when M is the directory's last file;
- else what the files up to M make on an empty database. On SQLite that is a
  temporary file. On PostgreSQL and MySQL, pass one with `-scratch-db`; the files
  are left applied there, so give a new one each time.

As `migrate drift` does, only the tables and enum types of that schema are read. A
table that schema does not name is no difference. A difference is refused, listed
as `migrate drift` lists it:

    version 2 is not marked applied: the database differs from what the files up to 0002_mine.sql make on an empty database; what would bring it there:
      change: create-table mine
    finish its statements by hand and resolve again; record how many ran with -statements k and run migrate apply -resume; or pass -force with the reason

Make the database match and run it again. If you know better, `-force` records it
all the same:

    migrate resolve -version 2 -applied -force "restored from the backup"

It prints the differences as a note. The row's error column keeps `forced: `, the
reason and the differences; `status` and `status -json` show it. A claim that
cannot be checked is refused the same way: a dirty version left, a version above
the directory's last file, no `-scratch-db` on PostgreSQL or MySQL, or files that
fail on it. A file whose rows a hand fix made good, as the hotfix below, may fail on
an empty database; then `-force` is the way.

`-undone`, `-statements` and `-forget` check nothing. `-undone` and `-statements`
leave the file to run again, which meets the real schema and Up's checks.
`-forget` takes the claim away.

## Two branches took one version

Both branches ran `migrate diff` from 0001, so each has a `0002_*.sql`, and `velox.sum`
and `schema.json` conflict in the merge.

1. Keep the main branch's `velox.sum`, `schema.json` and `0002_main.sql`, and delete
   your `0002_mine.sql`.
2. Run `migrate diff -name mine` on the merged schema. It writes `0003_mine.sql`. Do
   not rename a file by hand: the snapshot and sum follow from `diff`.

A database your branch ran on now holds version 2 as `0002_mine.sql`, and `apply` says:

    version 2 was applied as 0002_mine.sql, with checksum ..., and the directory's file for it is 0002_main.sql, with checksum ...

Undo what your file did by hand, then:

    migrate resolve -version 2 -forget
    migrate apply

That runs `0002_main.sql` and `0003_mine.sql`. Rebuilding a throwaway database is as good.

`migrate diff -db <dsn>` looks at a database's history first, and refuses a version
above the directory's last, which the next file would reuse. It is the only way
`diff` sees a database: without `-db` it reads the directory alone, and two branches
that have not met cannot be told apart. `$VELOX_DB` is not read here.

## An applied file was edited

    0001_a.sql was applied with checksum ... and is now ...

Restore the file if the edit was a slip. If the edit is meant, and the database holds
what the file now does, make it so by hand if it does not, then:

    migrate resolve -version 1 -adopt

Run that on every database the file applied to. What the file now says does not run.
A change meant to run belongs in a new migration.

A refusal that says to change a statement means on a file not yet applied anywhere.
A `-- velox:allow` or `-- velox:drops` line is outside the checksum, so adding one to
an applied file needs no `-adopt`.

## An applied file is gone

    version 2 is applied but the directory has no file for it

Restore the file if it should be there. If it was reverted on purpose, or its branch is
gone, undo what it did if that matters, then:

    migrate resolve -version 2 -forget

`-forget` refuses a version whose file is still there with the row's checksum and ran,
since `apply` would run it again: reverse that in a new migration. A version above the
directory's last is no refusal of `apply`, which reports it and applies nothing, but it
is one of `diff -db`; `-forget` takes it away as well.

## A pending file the database already holds

Someone ran it by hand, or it came with a restore:

    migrate resolve -version 2 -applied

The row counts no statements, as none ran, so `-forget` can take it back. It refuses
while a file below it is pending: the history is linear.

## Squashing

To replace old files with one baseline, on a directory whose every database is at the
last version:

1. Write the database's whole schema as `<next version>_baseline.sql`: `0003_baseline.sql`
   after 0002. The number must be above every squashed one, and the name must be
   `baseline`.
2. Delete the files it replaces, and run `migrate hash`.
3. A new database runs the baseline. Each existing one needs telling, once:

       migrate resolve -version 3 -applied

`apply` refuses to run it over rows below it, saying that, and never skips it unasked.
The rows below stay in the history, which no longer asks for their files. `-applied`
compares the database with `schema.json` first, so one that was behind is refused.

## A dirty MySQL version

MySQL undoes nothing, so a failure part-way leaves the statements before it in place.
`apply` lists the file's statements and what the history counts. The count is written
after each unit. If the first K ran and the history counts otherwise:

    migrate resolve -version 2 -statements K
    migrate apply -resume

A run that died with a statement on the server is the next section.

A statement that fails with nothing changed needs none of this. A lock wait timeout or
deadlock (1205, 1213), or the server's refusal of a file's first DDL statement, leaves no
dirty row: run `apply` again. A lock at the head of a later unit leaves a dirty row that
`apply` goes on from without `-resume`. A failing statement inside a unit, after earlier
statements of it ran, or a first statement that is not DDL, stays dirty for you to judge.

## A run that died with a statement under way

A run that is killed, loses its connection or is cancelled while a statement runs
leaves the statement on the server, which goes on and may finish it after the history's
count. MySQL commits each statement, and so does PostgreSQL in a no-transaction file's
`CONCURRENTLY` or a unit of more than one statement. A backfill like
`UPDATE a SET n = n + 1` may then have run once, though the history counts none of it.
`apply` and `status` lead with checking:

    version 2 is dirty, and the run stopped with statement 1 under way: it may have run on the server after the history's count of 0, or be running still. ...

1. Wait for it to end: `SHOW FULL PROCESSLIST` on MySQL, `pg_stat_activity` on
   PostgreSQL, shows it until then. The next `apply` waits for its lock up to
   `-lock-wait`.
2. See whether it took effect: `SHOW CREATE TABLE` or `\d` for a schema change; its
   rows for an `INSERT`, `UPDATE` or `DELETE`, as whether `n` moved above.
3. If the first J statements ran:

       migrate resolve -version 2 -statements J
       migrate apply -resume

   If none past the count did, `migrate apply -resume`.

Do not run `apply -resume` first: it runs the statement again, and the `UPDATE` above
would add 2.

On PostgreSQL a unit of one statement in a no-transaction file, which is every
statement when the file has no `unit` line, runs in a transaction with its count, unless
it is `CONCURRENTLY` or a statement PostgreSQL refuses in a transaction (`VACUUM`,
`CREATE DATABASE` and the like) or one that may end a transaction (`CALL`, `DO`), or
the file begins or ends one. The server rolls the statement back with the session, the
count is exact, and the refusal says `fix the cause and run migrate apply -resume`:
that is all it needs.

## A dirty PostgreSQL online file

A PostgreSQL file in a transaction leaves nothing to clear: a failure says `the file
was rolled back`, and `apply` runs it again once the cause is fixed. A hand hotfix
that the next file repeats fails so, with `already exists`: that is
`resolve -version N -applied`, above.

An `_online` file runs outside a transaction a unit at a time, and the history counts
the units done. A statement that fails leaves the version dirty at its unit, and what
the unit had done stays: a failed `CREATE INDEX CONCURRENTLY` leaves the index
`INVALID` (`\d` shows it), which serves no query but may still be kept up by writes;
a failed `VALIDATE CONSTRAINT` leaves the constraint `NOT VALID`, which checks new
rows. Fix the cause the error names (duplicates for a unique index, rows with no
match for a foreign key, a NULL for `NOT NULL`), then:

    migrate apply -resume

That runs the unit again from its first statement, which drops what the failed run
left (`DROP INDEX CONCURRENTLY IF EXISTS`, `DROP CONSTRAINT IF EXISTS`). To give the
file up instead, drop what its units made, as `DROP INDEX CONCURRENTLY a_x_key`, then:

    migrate resolve -version N -undone

A plan that changes an index `x` builds the new one as `x_velox_new` first, then drops
`x` and renames `x_velox_new` to `x`, each a unit. A failed build leaves `x` as it was,
with `x_velox_new` `INVALID` beside it; to give the file up, drop `x_velox_new`.
Where `x` is or becomes a unique constraint, a unit between makes `x_velox_new` a
constraint, and the last renames it with `RENAME CONSTRAINT`; to give up after
that unit, `ALTER TABLE t DROP CONSTRAINT x_velox_new`.

A statement that waits for a table's lock waits for `lock_timeout` from `velox.json`,
in an online file too; only `CONCURRENTLY` and `VALIDATE CONSTRAINT`, which block no
query while they wait for older transactions, wait for as long as those take. A lock
timeout at a unit's first statement changed nothing: run `apply` again, with no
`-resume`.

velox sets no `statement_timeout`. One the role or database sets cancels a long
`CREATE INDEX CONCURRENTLY` or `VALIDATE CONSTRAINT` and leaves the file dirty. Set it
to 0, or past the longest build, for the role or the connection that runs the online
file, as `ALTER ROLE migrator SET statement_timeout = 0` or `options=-c
statement_timeout=0` in the DSN.

A run killed during a unit's statement may leave the statement running on the server,
as `pg_stat_activity` shows, with the run's lock: the next `apply` waits for it up to
`-lock-wait`. Check what it did as in the section above. A unit `migrate diff` wrote
starts by dropping what it makes, so `apply -resume` is right whatever ran, though a
finished build is built again; `resolve -statements` spares it.

## Notes

`apply` and `resolve` print `velox-gen: note:` lines for what stops nothing: a SQLite
orphan row from before the file, a plain MySQL index a `DROP COLUMN` leaves narrower, what
`-forget` leaves behind. Through the library they come to `migrate.WithNotes`.
