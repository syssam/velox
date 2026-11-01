# Changelog

All notable changes are listed here, in the shape of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

velox is before 1.0, and its API can still change. Two rules hold already:

- A change to the generated code replaces `velox.GeneratedCodeIsVersionN` (and
  `veloxgql.GeneratedCodeIsVersionN`) with the next number, in a minor release,
  with a line here. Regenerate with the new `velox-gen`: code that names the old
  constant does not compile against the new runtime.
- `schema.json` and `velox.json` are both version 1. A newer velox reads every
  older version and refuses a newer one, with an error that names both versions.

After 1.0, a deprecated identifier carries a `// Deprecated:` comment until the
next major version. Before 1.0, nothing is deprecated: it changes or goes.

## Unreleased

1.0.0 will be the first release from this repository.

<!-- TO ADD: entries from the migrate, contrib/graphql and internal/engine work.
     Put each under Added, Changed or Fixed below, then delete this comment. -->

### Added

- The runtime: typed queries, inserts, upserts, updates, deletes and transactions
  on PostgreSQL 14, MySQL 8.4 and SQLite 3.35 or newer. `velox.Connect` opens a
  database in one call, `velox.Open` takes a pool you opened, and `veloxpgx.Open`
  takes a pgx pool. An application on SQLite or MySQL does not link pgx.
- A schema DSL in the style of ent, and `velox-gen`, which generates the code.
  Bidirectional self edges work as ent's do.
- `With` eager loading, keyset pages with `After`, `Has` and `HasWhere`.
- Tenant scoping (`WithTenant`), policies, typed hooks, sharding and read replicas.
  `Iter`, `Count` and `Exists` work across shards.
- One-key grouped aggregates: `CountBy`, `SumBy`, `AvgBy`, `MinBy` and `MaxBy`.
- Validation: `MinRuneLen` and `MaxRuneLen` on a string field count characters, and
  `schema.Check` declares a CHECK constraint that migrations plan and drift compares.
- `velox.ErrLocked` for a lock that `NoWait` did not get, and `velox.IsNotFound`.
- `migrate` and `velox-gen migrate`: a planner, an applier, drift checks, and
  `migrate baseline` to adopt an existing database. `velox-gen migrate sql` feeds
  Atlas, for a directory that Atlas manages instead.
- `contrib/graphql`: a GraphQL schema and resolvers generated for gqlgen, with node
  IDs, keyset connections and mutations in savepoints. `veloxgql.Cost` reads `@skip`
  and `@include`.
- `contrib/veloxotel`: OpenTelemetry tracing and metrics.
- Runnable examples in `examples/`, up to a whole service in `examples/tracker`.

### Changed

- The module path `github.com/syssam/velox` used to hold an older, different project,
  whose `v0.x` versions stay in the Go module proxy. Code written for it does not
  build against this one.
- Generated code is version 6, and velox-graphql's is version 3. Regenerate.
- Schema and migrations: table, index, unique and foreign-key names follow ent's, so
  an ent database baselines without renames. A name may differ from one that the older
  project wrote, so read the first `migrate diff` before you apply it. Foreign keys take
  ent's defaults: a one-to-one key is unique, and a NULL-able key is ON DELETE SET NULL.
- On SQLite, every time argument is written as UTC text of one width, so times compare
  by the instant and read back in UTC. Rows already stored with another zone keep their
  old text: rewrite them, or they still compare as that text.
- A row kept from a to-many `With` or `LoadEach` keeps alive the rows it was loaded
  with, which share its arrays. Copy it to keep it alone.
- `WithTenant` converts predeclared integers and strings to the tenant column's type.
- `Open` and `OpenShards` panic on an unknown dialect. `Connect` returns an error.

### Fixed

- `velox-gen` writes all of its files or none, `-check` reports leftover files, and it
  refuses Go types it cannot write.
- A rollback that velox sends after a transaction function fails is bounded to 5
  seconds, and savepoints keep the transaction's deadline.
- `TxRetrySerializable` does not retry `ErrLocked`.
- A PostgreSQL error is recognized by its `SQLState` method, in any pgx version.
- MySQL `varchar` goes up to 16381, and a default that a decimal column would round is
  refused.
- `velox.WithTags` no longer panics on an odd number of strings: the last key takes the
  value "".
- `veloxgql.EncodeCursor` returns an error instead of panicking, and gqlgen reports a
  cursor that cannot be written on its field.
- In GraphQL, re-pointing a one-to-one edge checks the new child first, and is refused
  over a child the caller cannot see.
