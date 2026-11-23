# 147. SQLite in memory, and a test database as a copy

- Status: accepted, 2026-11-24.

## Context

The nextapp port (496 entities) opens about 1,300 test databases with
`veloxtest.Open` on SQLite. Two findings came back.

1. The guidance contradicted itself. `velox.Connect` gives SQLite a pool
   of one connection. `migrate.Create` refused a database in memory in a
   pool of one, and its message recommended "a shared-cache database in a
   pool of two or more connections", which a Connect'd DB cannot be.
   Every enttest DSN, `file:ent?mode=memory&cache=shared&_fk=1`, failed.
2. An Open took about 6 s on a temporary file, 2.2 s with
   `journal_mode = MEMORY` and `synchronous = OFF`.

Facts behind the decision:

- A database in memory is its connection's. `database/sql` keeps a pool's
  idle connection open unless told otherwise (`SetMaxIdleConns(0)`,
  `SetConnMaxIdleTime`, `SetConnMaxLifetime`); Connect sets none of them.
- The migrate session closed its connection after a run, never returning
  it, because a migration file may change any setting. `Inspect` and
  `Drift` did the same. In a pool of one that ends a database in memory.
- modernc.org/sqlite ignores `_fk`; Connect took it as the DSN's own
  foreign-key setting and left foreign keys off.
- `BenchmarkCreateSQLite` (`internal/benchschema`: bench/scale's 500
  schemas, and a wide shape of 33 columns, six indexes and two checks a
  table, near nextapp's 33 and seven) measures Create at 500 wide tables:
  about 0.8 s on a file, 0.73 s in memory, on an idle M3 Max; 3 to 4.4 s
  on the same machine under heavy load. All of it is SQLite running the
  DDL (one transaction already). The CPU profile shows page writes, or
  with `cache_spill` off, first touches of fresh memory in
  modernc.org/sqlite's allocator, in equal measure. Turning `cache_spill`
  off changed nothing measurable, and was dropped.

## Decision

- `migrate.Create` accepts a SQLite database in memory in a pool of one
  connection. Its session holds the connection (`held`) and hands it back
  at close, after a `ROLLBACK` and with `busy_timeout` as open found it.
  Create runs only its own statements, so nothing else is left on it.
  After the run Create reads the first table back through the pool; when
  the pool closed the connection it refuses with Code `session`:
  "the SQLite database is in memory, and its pool closed its one
  connection after Create made the tables, which went with it; keep the
  connection open: no SetMaxIdleConns(0), SetConnMaxIdleTime or
  SetConnMaxLifetime".
- In a larger pool Create still needs a shared-cache database. The
  refusal says what works: "… name a file, use a shared-cache database
  (file:x?mode=memory&cache=shared), or give the pool one connection:
  velox.Connect's SQLite pool has one, or call db.SetMaxOpenConns(1)".
- Up still refuses a pool of one: it runs files that may change the
  connection. Its message names `sql.Open` with `velox.Open` for the
  shared-cache pool, says Connect's pool has one, and points at Create.
- `Inspect` and `Drift` hand a SQLite connection back with `busy_timeout`
  as it was, and discard it only when that cannot be set back.
- `veloxtest.Open` on SQLite with an empty DSN or one of a database in
  memory (`:memory:`, `file::memory:…`, `mode=memory`, `vfs=memdb`) makes
  the tables once per test binary, per driver and schema values, with
  Create in a scratch file, keeps the file's bytes, and gives each call a
  copy in `t.TempDir()`, opened with the DSN's parameters but `mode`,
  `cache` and `vfs`, and set to `journal_mode = MEMORY` and
  `synchronous = OFF`. Each call has a database of its own, as an empty
  DSN always gave, and two calls with one enttest DSN never share one. A
  DSN naming a file is opened as it is, and Create runs on it.
- On the "sqlite" driver Connect counts only `_pragma=foreign_keys(…)` as
  the DSN's own setting; mattn's `_foreign_keys` and `_fk` count on any
  other name, as before.

`BenchmarkOpenSQLite` at 500 wide tables, idle machine: an Open of a
copy costs about 27 ms (opening the file and parsing its schema); the
first Open of a binary also makes the image, about 0.8 s. An Open that
runs Create on a file costs about 0.8 s, as every Open did before.

## Consequences

- A test that opens 1,300 databases pays the DDL once.
- A test of an in-memory DSN gets a file, not memory. It is private and
  thrown away with `t.TempDir()`; a test that inspects
  `pragma_database_list` sees a path.
- The image is kept in the process's memory for the binary's life: the
  size of the database file, 15 MB at 500 wide tables.
- A pool of one whose owner later sets an idle limit loses its database
  in memory; Create detects it only at its own end, and velox.Connect's
  documentation says so.
- Production `Create` semantics are unchanged: one transaction on SQLite
  and PostgreSQL, statement by statement on MySQL.
