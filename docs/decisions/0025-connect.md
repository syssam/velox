# 25. velox.Connect opens a database in one call

Status: accepted, 2026-11-01.

## Context

Starting velox took two handles, one to run statements and one to close:

    sqlDB, err := sql.Open("sqlite", dsn)
    ...
    defer sqlDB.Close()
    db := velox.Open(velox.SQLite, sqlDB)

The database was named twice, and the settings velox depends on (MySQL's
`parseTime` and `clientFoundRows`, one connection on an SQLite `:memory:`
database) were only in a doc comment. ent's `ent.Open(driver, dsn)` starts in
one handle.

## Decision

- `velox.Connect(driver, dsn, opts...) (*DB, error)` opens the pool and works
  out the dialect from the driver's name. An unknown name is an error: the
  dialect decides the SQL, so velox does not guess it.
- `DB.Close` closes the pool only if Connect opened it, so whoever opens a pool
  closes it. On a transaction's DB it returns an error.
- `velox.Pool(db)` returns the `*sql.DB` of a DB from Connect or Open, for pool
  settings and `migrate.Up`. It returns nil on a transaction's DB, whose
  statements must not leave the transaction, and on a DB with no single
  `*sql.DB`.
- On MySQL, Connect refuses a DSN without `parseTime=true` and
  `clientFoundRows=true`. Without them, reads and `Save` go wrong at run time.
- On SQLite, Connect sets one open connection. Each connection to `:memory:` is
  its own database. An application can raise it with `Pool`.
- velox imports no drivers: the application imports the one it names.
- `velox.Open` stays, for a pool the application configures or wraps.

## Alternatives

- `db.Pool()`, a method. It reads better, but a method or field of DB that names
  `*sql.DB` puts database/sql's types in the export data of every package using a
  DB. Measured on the entity package of `TestGenericTypesCompileSmall`: 731,382
  bytes before, about 952,000 with the method or the field, 731,600 with the
  function and an `io.Closer` field. 30% on every package is not worth a method.
- A generated `gen.Open`, as ent has. The generated package has no other reason
  to carry it, and it would add nothing that the library cannot do.
- Named `OpenDSN`. Connect reads better at the first line of a program. Like
  `sql.Open`, it does not reach the database; its documentation says so.
