# Changelog

All notable changes are listed here, in the shape of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

velox2 is before 1.0, and its API can still change. Two rules hold already:

- A change to the generated code replaces `velox.GeneratedCodeIsVersionN` (and
  `veloxgql.GeneratedCodeIsVersionN`) with the next number, in a minor release,
  with a line here. Regenerate with the new `velox-gen`: code that names the old
  constant does not compile against the new runtime.
- `schema.json` and `velox.json` are both version 1. A newer velox reads every
  older version and refuses a newer one, with an error that names both versions.

After 1.0, a deprecated identifier carries a `// Deprecated:` comment until the
next major version. Before 1.0, nothing is deprecated: it changes or goes.

Each entry names the finding or the commit it comes from.

## Unreleased (1.0.0)

This summarizes what a user of the tree before the hardening work must know.

### Added

- `field.String(...).MinRuneLen` and `MaxRuneLen`: lengths in characters.
- `schema.Check(name, expr)`: a CHECK constraint on a schema's table, which migrations
  plan and drift compares.
- `velox.CountBy`, `SumBy`, `AvgBy`, `MinBy` and `MaxBy`: one-key `GROUP BY`, typed and
  scoped as `Sum` is, with a column handle's `Key` method and `Group[K, V]` results.
  `Statement.GroupBy` holds the key. See decision 0022.
- `dbtype`: the plain data types, such as `Result`, `ConstraintError` and `ColumnDesc`,
  with real documentation. `velox.Result` and the rest are aliases of them, and code
  that uses them does not change.

### Changed

- On SQLite, velox writes every time argument as UTC text of one width, so times store
  and compare by the instant whatever their zone. A time then reads back in UTC. Rows a
  tree before this wrote with another zone keep their old text: rewrite them, or they
  still compare as that text.
- `veloxgql.EncodeCursor` returns an error instead of panicking, and `veloxgql.Cursor` is a
  context scalar (`MarshalGQLContext`, `UnmarshalGQLContext`), so gqlgen reports a cursor
  that cannot be written on its field. Run `gqlgen generate` again.
- `velox.WithTags` no longer panics on an odd number of strings: the last key takes
  the value "".
- A PostgreSQL error is recognized by its `SQLState` method, in any pgx version, and
  no longer only by the type `*pgconn.PgError` of pgx v5.

### Removed

- `velox.DriverConn`, whose parameter no code outside the module could name.

### Added

Examples
- `examples/tracker`, a whole service: an issue tracker's JSON API with tenants,
  logins, policies, relations, transactions, keyset pages and migrations, on SQLite
  or PostgreSQL.

Runtime
- The new package `veloxpgx` holds pgx's native path: `veloxpgx.Open(pool)` is
  what `velox.OpenPgx(pool)` was and `veloxpgx.Conn(pool)` what `velox.PgxConn(pool)`
  was; both of those are gone. `velox` itself no longer imports pgx, so an
  application on SQLite or MySQL does not link it. `velox.Open` with pgx's stdlib
  driver is unchanged, and its errors are classified as before. `velox.OpenConn`
  opens a DB on a `Conn` (C53, decision 0019).
- `velox.IsNotFound` (G50). `velox.ErrLocked` matches PostgreSQL's 55P03 and
  MySQL's 3572; `TxRetrySerializable` does not retry it (4b3ae1c).
- `TxInfo.Route` says where a transaction runs (00b804b). veloxotel's
  transaction spans name their shard and server.
- Bidirectional self edges, as ent's. A self one-to-one edge with no inverse
  keeps both rows' keys pointing at each other (`velox.EdgeBidi`, 1e5519c). A
  self many-to-many edge writes and removes both join rows (`Desc.Bidi`,
  e84aeef). Both are written by velox-gen, and velox-graphql handles
  self edges end to end (8b009ae).
- `edge.NoAction` spells out ON DELETE NO ACTION (e29e619).
- Sharded `Iter`, `Count` and `Exists` (C93).
- Insert, InsertAll, upserts, Update and Save refuse a nil key of a Required
  edge to its own type with a `*ValidationError`, before anything is sent
  (`ColumnDesc.RequiredEdge`, e29e619).

velox-gen and migrate
- `VELOX_PLUGIN_TRACE=1` adds a plugin panic's stack to velox-gen's message
  (82b4728).
- `migrate baseline` adopts the tables of an ent SQLite database (M20), and an
  ent database baselines without renames, because index, unique and foreign-key
  names and PostgreSQL's key and sequence names are ent's (25e9e88).
- `migrate.Index.Constraint` marks a PostgreSQL UNIQUE constraint. Drift warns
  when one side has a constraint and the other a plain index; a drop or rename
  names the constraint (M22, F3).
- Notes on one object are one warning (M2). MySQL binary defaults are read from
  `SHOW CREATE TABLE` and compared by their bytes (M3, M4). A `BEGIN ATOMIC` body
  and a psql meta-command line are refused by name (M13). A PostgreSQL domain an
  extension creates gets no domain note (F7).
- `velox-gen -check` reports leftover files (738da1c). `velox-gen verify`
  sweeps what it leaves, stops on a signal with exit 130 and explains a failed
  build (82b4728, 05db2ee).

GraphQL
- `veloxgql.ErrorPresenter` takes options, with `OnCanceled` (G37).
- `veloxgql.Cost` reads `@skip` and `@include` (G24).
- `where` predicates on the node key (G1). `@goModel` binds an application
  enum (G11, G12, G13).
- Each mutation runs in a savepoint (G45).

Documentation and tests
- Runnable Examples for `Query.SQL`, `Query.With`, `DB.Tx`, `WithTenant`,
  `InsertQuery.OnConflict`, `migrate.NewPlan` and `migrate.Up`, `veloxgql.Cost`
  and `veloxgql.DecodeID` (2236976). Every example's tables come from its
  committed migrations (M16, M17).
- README.md, CONTRIBUTING.md, SECURITY.md and this file (9a5a474, 1cc5645).
- CI runs on PostgreSQL 14 and 18 and MySQL 8.4, pinned by digest (9c25cef),
  with fuzz targets, per-package coverage floors and benchmark baselines.
- `bench/compare` measures velox2's build and runtime cost against old velox and
  ent (`docs/reviews/2026-10-29-perf-comparison.md`; 591c054, b7603fd..4ebd51a).
  `COMPARE_STEPS=runtime`, and a `veloxpgx` column (da2ee65, c57da87).

### Changed

Generated code
- Generated code is version 6 (was 5), and velox-graphql's is version 3 (was
  2). Regenerate (020d2f1, 8bcb28f).
- velox-gen refuses Go types it cannot write: an unnamed struct, a generic
  instance and an unnamed interface (C74; `TestUnwritable` and
  `TestJSONUnwritableType`, 3396a6b).
- In the gen package's hand-written files, a file `go build` leaves out binds no
  methods, and a file only some systems build (a build constraint or a `_GOOS`
  name) is refused at the line that says so (`TestMethodsSkipsIgnoredFiles` and
  `TestMethodsRefusesSystemFiles`, 3396a6b).
- A plugin file path that is not ASCII, or a plugin `.go` file that `go build`
  leaves out, is refused (3396a6b).
- `velox-gen` writes all or nothing (4036977).
- `omitzero` is the JSON tag of struct and array fields. The enums in `velox.go`
  have `String` and `IsValid` (020d2f1).
- A join entity's handles take its table's Go name: `CategoryChildren` for
  `category_children` (13c5cf9).

Schema and migrations
- Names follow ent: its inflection (1d216d8), and the index, unique and
  foreign-key names, capped as ent caps them (25e9e88). A table's name comes
  from the entity's Go name. A name may differ from the one an earlier velox2
  wrote: read the next `migrate diff` before you apply it.
- ent's foreign-key defaults: a one-to-one key is unique, and a NULL-able key
  is ON DELETE SET NULL (e29e619).
- MySQL `varchar` goes up to 16381, row bytes count `set`, `float(p)` and
  spatial types, and a default that the decimal rounds is refused (d139204).
- The migrate floors are PostgreSQL 14 and MySQL 8.4 (9c25cef).
- The history table is qualified by the session's schema or database (M14).

Runtime
- On database/sql with pgx's stdlib, velox scans through an `sql.Scanner`, so
  pgx's per-column type check passes at its first step for each column of each
  row. Query100 on PostgreSQL went from 961 µs to 415 µs and EagerO2M100 from
  4.32 ms to 1.42 ms, in two runs at different loads
  (`docs/reviews/2026-10-29-pg-scan-path.md`; 399d7fe, 00be03b, f023d4b,
  f293a06).
- `With` and `LoadEach` read a relation's rows into arrays that never move,
  and give the owners of a to-many relation one array between them, each
  owner's slice capped at its length. An owner whose rows were read one after
  another takes them where they were read, without a copy, and each owner's
  rows are grouped in one array of indexes. EagerO2M100 on SQLite went from
  261,168 to 151,600 B/op and from 4,699 to 4,204 allocs/op (`BenchmarkScan`);
  in the three-ORM comparison it is 209,188 B/op on SQLite and 202,040 on
  PostgreSQL, under old velox's 259,516 and 251,707
  (`docs/reviews/2026-10-30-eager-memory.md`).
  A row kept from a to-many load keeps the rows of every owner loaded with it,
  in the arrays they were read into (up to 1,024 rows each) and in the one the
  rest were copied to; copy it to keep it alone (4e4e85a, 9fbf19e).
- `WithTenant` converts predeclared integers and strings to the tenant column's
  type, entity by entity (b04ac97).
- `Open` and `OpenShards` panic on an unknown dialect (06d3b4d). A rollback
  velox sends after fn fails is bounded to 5 seconds (on a DB from `Open`, a
  savepoint's ROLLBACK TO only), and savepoints keep the transaction's
  deadline (1642c74).
- Hooks cannot drop the bidi marker, the load pin or a bidi key (0785b33). The
  upsert policy flag moves to the Statement, and hooks cannot unguard an
  upsert (013fed4).
- A savepoint's name is unique in its transaction (f4fbd6b).
- veloxotel's `TargetFor` is deterministic for a Route (59a967b).

GraphQL
- A single owner key's existence check is EQ (G50).
- Unlinking a bidirectional key writes the old partner with no scope (684fa6e).
- An O2O re-point checks the new child first, and is refused over a hidden
  child (G47).
- A Required edge with a NULL-able key is nullable (200200c).

### Removed

- `velox.GeneratedCodeIsVersion5` and `veloxgql.GeneratedCodeIsVersion2`, each
  replaced by the next number (020d2f1, 8bcb28f).
- The model hash in `velox.go`. Generated files no longer hold it (20edb85).
