# Operating migrations

What to do when `migrate apply` refuses. Each recovery here is run by
`TestOperationsDoc` in `cmd/velox-gen`. The commands take `-dir` and `-db`, or
`$VELOX_DB`; they are left out below. None of them changes the schema: `resolve` only
writes the history table.

| Way out | When | What it does |
| --- | --- | --- |
| `resolve -version N -applied` | a dirty version you finished by hand, or a pending file the database already holds | records the file as applied, running nothing |
| `resolve -version N -undone` | a dirty version you reverted by hand | deletes the row, so the file runs again |
| `resolve -version N -statements K` | a dirty MySQL version whose count is wrong | records that the first K statements ran; `apply -resume` runs K+1 on |
| `resolve -version N -adopt` | an applied version whose file changed, and the database holds what the file does | records the file's checksum and name, running nothing |
| `resolve -version N -forget` | an applied version whose file is gone, differs, or never ran | deletes the row and undoes nothing; prints what the database keeps |

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
The rows below stay in the history, which no longer asks for their files. Nothing checks
that a database was at the last version before you ran `-applied`; one that was behind
stays behind, and `migrate drift` is how to see that.

## A dirty MySQL version

MySQL undoes nothing, so a failure part-way leaves the statements before it in place.
`apply` lists the file's statements and what the history counts. The count is written
after each unit. A run killed with a statement on the server may leave it running, and
finishing, after the count: check `SHOW FULL PROCESSLIST`, then `SHOW CREATE TABLE`
against the list. If the first K ran:

    migrate resolve -version 2 -statements K
    migrate apply -resume

A statement that fails with nothing changed needs none of this. A lock wait timeout or
deadlock (1205, 1213), or the server's refusal of a file's first DDL statement, leaves no
dirty row: run `apply` again. A lock at the head of a later unit leaves a dirty row that
`apply` goes on from without `-resume`. A failing statement inside a unit, after earlier
statements of it ran, or a first statement that is not DDL, stays dirty for you to judge.

## Notes

`apply` and `resolve` print `velox-gen: note:` lines for what stops nothing: a SQLite
orphan row from before the file, a plain MySQL index a `DROP COLUMN` leaves narrower, what
`-forget` leaves behind. Through the library they come to `migrate.WithNotes`.
