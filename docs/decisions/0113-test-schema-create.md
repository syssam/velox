# 113. A test's or a development database gets its tables without migrations

Status: accepted, 2026-11-22.

Amends [0006](0006-own-migrate-and-atlas.md), and the lists of what velox
does not offer in coming-from-ent.md and coming-from-veloxe.md, which
named auto-migration.

## Context

ent's tests open a database with `enttest.Open(t, "sqlite3", dsn)`, which
runs `client.Schema.Create`, the auto-migration, so a test has the tables
of the schema without a migration directory. velox had no such path. A
test applied the migration directory with `migrate.Up`, which works only
when the directory is of the test's dialect: an application that migrates
PostgreSQL and tests on SQLite had no SQLite tables. velox refused
auto-migration (0006), and the refusal was read as covering tests too.

The user ruled that a schema bootstrap is allowed when it is named and
documented as for tests and development, and production keeps versioned
migrations.

Where the tables come from at run time was the question. The generated
package holds each entity's `Descriptor`: its columns, primary key,
indexes and edges, but no column types, defaults, checks, enum types or
actions of foreign keys, so a `CREATE TABLE` cannot be written from it.
velox-gen could embed the schema snapshot it computes for `migrate diff`
in the generated package, but that changes the generated code's shape,
and every application then links a snapshot per dialect it never reads.
velox-gen's loader runs a program that calls `schema/load.Marshal` on the
schema package's values; the same call works in the test's process, given
the values.

## Decision

- **`migrate.Create(ctx, db, schema)`** plans from an empty schema, as the
  first file of `migrate diff` would, and runs the statements: in one
  transaction on PostgreSQL and SQLite, one after another on MySQL. It
  writes no history row. It reads the schema's tables and enum types
  first. A database that holds none of them gets them all. One that holds
  them as the schema says, as `Drift` finds, is left alone, so a
  development server may call `Create` on each start. Anything else, part
  of the schema or a table that differs, is refused with Code `create`,
  listing what differs, and nothing changes. `Create` makes a new
  database; it never alters one. An additive diff was not chosen: it is
  the auto-migration that production must not run, a database it left
  behind would differ from the one the versioned files make, and a
  refusal tells a developer to drop a stale database rather than leave
  them guessing which of its columns are current. A SQLite database in
  memory, which ends with its connection, is refused as `Up` refuses it.
- **Package `veloxtest`** has `Schema(d, schemas...)`, the tables
  velox-gen plans for the schema values on dialect d, built in the
  process by `load.Marshal`, `codegen.Build` and `codegen.Tables`, the
  calls `migrate diff` makes; and `Open(t, driver, dsn, schemas, opts...)`,
  which connects, makes the tables with `Create`, closes at the end of the
  test and fails it on an error. On SQLite an empty dsn is a new file in
  `t.TempDir()`. The schemas are listed by the caller, since Go cannot
  list a package's types at run time; one left out that a relation names
  is an error. Open builds the tables once per dialect and list in a test
  binary.
- The generated code does not change, and nothing is compiled per entity.
  A test binary that imports `veloxtest` compiles velox's generator
  packages once.

## Consequences

- `migrate diff` and `migrate.Up` stay the production path. The docs name
  `Create` and `veloxtest` as for tests and development wherever they
  appear.
- coming-from-ent.md maps `enttest.Open` and `client.Schema.Create` to
  `veloxtest.Open` and `migrate.Create`. coming-from-veloxe.md no longer
  lists auto-migration as refused: deploy-time auto-migration still is.
- `schema/load` is for the program velox-gen writes and for `veloxtest`.

## Evidence

- `TestCreate`, `TestCreatePartial`, `TestCreateRefusals` and
  `TestCreateFails` (migrate/create_test.go), on PostgreSQL and MySQL when
  their variables are set.
- `TestSchemaIsVeloxGens` (veloxtest/veloxtest_test.go): `Schema` equals
  what velox-gen's loader gives for internal/testmodel/schema on each
  dialect. `TestOpen` and `TestOpenSameFile`.
- examples/relations/main_test.go opens its schema with `veloxtest.Open`.
