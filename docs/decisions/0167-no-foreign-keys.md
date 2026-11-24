# 0167. A migration directory without foreign keys

- Status: accepted, 2026-11-24. Amends 0006 (velox.json's keys) and 0113
  (`veloxtest.Open`'s options); builds on 0090 and 0114.

## Context

An application's databases have never had a FOREIGN KEY constraint: its
previous ORM ran ent's `Schema.Create(ctx, migrate.WithForeignKeys(false))`.
velox-gen declares one for every edge, so `migrate diff` wrote about 900
constraints for it, each of which fails on rows whose key matches no row,
and each of which the application never asked for. velox had no way to
leave them out.

## Decision

- velox.json takes an optional key, `"foreign_keys"`, a JSON boolean named
  after ent's option. Absent or `true` is the behaviour before: velox.json
  is written without the key, byte for byte as before. `false` is
  `migrate.Config.NoForeignKeys`, and velox.json is then written with
  `"foreign_keys": false` last. Any other value is a `config` error.
  velox.json stays version 1; a velox older than this one refuses the key
  as unknown, which only a directory that opts in meets.
- `(*migrate.Schema).WithoutForeignKeys()` is a copy with no foreign key on
  any table. It is the one switch, on migrate's model: every DDL path
  (`migrate.Create`, `NewPlan` and `Render`, `Dir.Add`) renders from a
  `Schema`, so each obeys it with no flag of its own. A field on `Schema`
  was weighed and rejected: it would be serialized into schema.json,
  compared by `Equal` and `Diff`, and need every renderer to check it,
  where a schema without keys is already what each of them handles.
- With `"foreign_keys": false`, velox-gen applies it to the schema
  package's tables before anything compares them: `migrate diff` plans and
  writes none (no `ADD CONSTRAINT … FOREIGN KEY`, no inline `REFERENCES`,
  on every dialect), schema.json records none, `diff -check` compares the
  tables without them with schema.json, and its replay applies files that
  have none. `migrate rebase` and `migrate.Rebase` leave them out too.
  `Dir.Add` refuses a schema with a foreign key in such a directory, so a
  Go caller cannot write one by mistake.
- `migrate drift` is unchanged: it compares the database with schema.json,
  which has no key, so a database without keys has no drift and a database
  with them reports each as `drop-foreign-key`. velox does not hide them:
  a directory that says it has no keys and a database that has them
  differ, and the keys are dropped by hand to settle it.
- `-foreign-keys=false` on `migrate diff` and `migrate baseline` starts a
  directory with the setting. On an existing directory the flag must agree
  with velox.json, or `diff` exits 2. Baseline only starts directories.
  `baseline -foreign-keys=false` also leaves the database's own keys out of
  schema.json and `0001_baseline.sql`, with a warning naming each: the
  directory says it has none, and drift then reports them on that
  database. `migrate sql -foreign-keys=false` prints DDL without them for
  Atlas.
- Changing the setting of an existing directory is supported by hand, not
  by the flag: edit `"foreign_keys"` in velox.json, run `migrate hash`, and
  the next `diff` plans the `drop-foreign-key` or `add-foreign-key`
  changes, as for any other change of the model. A flag that rewrote
  velox.json was weighed and rejected: the edit is one line, it shows in
  review beside the file it causes, and the flag would be a second way to
  change a setting that already has a file. The refusal says how.
- Indexes are unchanged. PostgreSQL and SQLite get no index of an edge
  column, as before (0090). On MySQL, velox-gen declares the index MySQL
  makes for a key that no index leads, named as the key; it keeps it, so
  an edge column is indexed alike with and without the setting. A
  database ent made on MySQL without keys has no such index, and the first
  `diff` after baseline adds it.
- No `unindexed-fk` review line is written in such a directory: the
  finding (0114) is raised by a change that adds a foreign key, and there
  is none. It is about the constraint's checks and `ON DELETE` as much as
  reads; the reads of an unindexed edge column are the schema's
  `index.Fields` to declare.
- For tests, `veloxtest.WithoutForeignKeys()` is ent's
  `enttest.WithMigrateOptions(migrate.WithForeignKeys(false))`: `Open`
  makes `WithoutForeignKeys()` of the tables, on every dialect. The tables
  `Open` builds once per binary, and SQLite's template file copied for
  each in-memory Open, are keyed on it, so tests with and without keys
  never share one. `Open`'s variadic parameter changes from `velox.Option`
  to a veloxtest `Option`, and `veloxtest.WithOptions(opts ...velox.Option)`
  passes the DB's options, as enttest's `Option`, `WithOptions` and
  `WithMigrateOptions` do. `velox.Option` cannot carry the flag: it is a
  struct with an unexported function that only velox reads, and a marker
  among a DB's options would be a test setting in the runtime's type. No
  caller passed a `velox.Option` to `Open`. Nothing checks that a test's
  option and a directory's velox.json agree, as ent has no such check.
  Elsewhere, `migrate.Create(ctx, db, s.WithoutForeignKeys())` does the
  same. No option is added to `PlanOptions` or `DiffOptions`: they plan
  from a schema, and the schema carries the switch.

## Consequences

- The runtime is untouched. It reads the generated descriptors, never a
  migration directory, and does no cascade itself: `With`, `Has`,
  `HasWith`, joins and the edge setters work as before. What the database
  did for a key is the application's now: an edge's `OnDelete`
  (`Cascade`, `SetNull`, `Restrict`) does nothing, a delete leaves rows
  that point at the deleted one, and a write may store a key that matches
  no row, which a load then finds no relation for. `velox.ConstraintForeignKey`
  errors are not raised.
- The setting is per directory, as ent's is per `Create` call. A schema
  cannot leave out some keys and keep others.
- `TestConfigForeignKeys`, `TestWithoutForeignKeys`,
  `TestDirAddRefusesForeignKeys` and `TestDriftWithoutForeignKeys` in
  `migrate`; `TestMigrateDiffNoForeignKeys` (goldens in
  `cmd/velox-gen/testdata/nofk`, one per dialect),
  `TestMigrateForeignKeysChanged` and `TestMigrateBaselineNoForeignKeys`
  in `cmd/velox-gen`; `TestOpenWithoutForeignKeys` (SQLite, the template
  kept apart, `WithOptions`) and `TestOpenWithoutForeignKeysServers`
  (PostgreSQL and MySQL when their variables are set) in `veloxtest`
  cover it. The goldens of every other directory are
  unchanged.
