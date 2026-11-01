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

### Added

- `graphql.MaxList(n)`, a per-edge bound on rows and on what they are charged.
- `veloxgql.Limits`: `MaxResponseBytes` (32 MiB) and `Timeout` (30 s), with `CodeTimeout`.
  `Setup` also caps resolver errors and websocket messages.
- `migrate resolve -statements`, `-adopt` and `-forget`; `-applied` works on a pending file;
  `diff -db`; `schema.Collation`. `docs/migrations-operations.md` covers merges and recovery.
- `ErrTenantRef`, `TenantRef` and `Desc.TenantRefs`; `ErrTxControl`.
- `veloxgql.Limits`: `MaxFragments`, `MaxDepth`, `MaxNameBytes`, `MaxLiteralBytes`,
  `MaxVariableNodes` and `MaxErrors`. The `Server` interface `Setup` takes gains
  `SetValidationRulesFn`.
- `migrate.LintFile` codes `drop-domain`, `drop-type`, `drop-collation` and
  `drop-partition`; `TRUNCATE PARTITION` is `delete-rows`.
- `veloxgql.Setup` and `Limits.MaxTokens`, `MaxFields`, `MaxBodyBytes` and
  `MaxIntrospectionCost`, with `ErrRequestTooLarge`: an oversized document is refused
  before it is parsed. A public server should call `Setup`.
- `-- velox:drops`, the plan's list of what a generated file removes.
- `velox-gen` refuses `Unique` with `Optional`, two edges on one foreign key, non-ASCII
  names, and a `Default` that fails the field's built-in checks.
- `migrate.LintFile` reports `DROP SCHEMA`, `DROP DATABASE`, MySQL `CHANGE COLUMN` and
  an always-true `WHERE`, and also reads generated files.
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

- Join entities with `TenantRefs` are tenant-scoped on every read and write, and fail with
  `ErrNoTenant` without a tenant. `TenantRef.TenantKind` is new: regenerate.
- GraphQL: an explicit `null` on a Nillable field in an update clears it. `null` on an
  `<edge>ID` stays refused and names `clear<Edge>`. A JSON input number is `float64` when
  that is exact, and `json.Number` only when it would not be. `MaxFields` is 5000 and
  `MaxVariableNodes` 100000.
- A plan's attribute line records the value it expects, and `Up` refuses a mismatch.
- A MySQL table whose model states no collation keeps what the database holds.
- MySQL: a DDL statement refused with nothing changed leaves no dirty version; the new
  `Error` code `retry` replaces `*DirtyError` there.
- A plain index narrowed by a MySQL `DROP COLUMN` is a note, not a refusal.
- A write under a tenant checks each foreign key to a tenant-scoped row, with one more
  statement per column set, and fails with `ErrTenantRef` for another tenant's row or a
  missing one alike. `WithAllTenants` skips it. A join row needs a tenant or
  `WithAllTenants`.
- In a transaction, `Raw` and `Exec` refuse what would end it, `COMMIT`, `ROLLBACK` and the
  like (`ErrTxControl`), and MySQL DDL, which commits implicitly.
- `Up`'s history checksum ignores `velox:allow` and `velox:drops` lines, so accepting a loss
  for one environment does not strand another that already applied the file.
- `velox:drops` names the attribute it accounts for, on every dialect. `Up` compares MySQL
  `MODIFY`/`CHANGE`, PostgreSQL retype defaults and remade indexes with the database.
- `Up` refuses an in-memory SQLite database, whose tables would be gone with its connection.
- GraphQL: `MaxCost` is 150000. Under `Setup`, validation offers no suggestions and returns
  at most 100 errors. Null on a Nillable update field is refused, naming `clear<Field>`.
  `FromJSON` is strict, and a hook sees `json.Number`. `Uint64` input stops at 2^63-1.
- `All` allocates at most 16 rows ahead of `Limit`, then eightfold as they fill.
- `GeneratedCodeIsVersion7` and `veloxgql.GeneratedCodeIsVersion6`: regenerate.
- GraphQL: explicit `null` on a Nillable field with a `Default` is refused on create.
  `@skip` no longer lowers the rows charged, and a mutation's cost counts its statements.
  GraphiQL has its own budget, enough for 150 rich entities: raise
  `MaxIntrospectionCost` for a larger schema.
- MySQL `Up` runs in strict mode, in its own session only.
- GraphQL: a Required unique edge is `T`, not `T!`, since a policy or tenant can hide its
  target. `or: []` matches nothing. A tenant key is never an input. `Create<T>` refuses an
  explicit zero on a field with a `Default`. Tenant errors present as `FORBIDDEN`.
- GraphQL: `Cost` charges each statement, the size of inputs and what introspection
  returns, so costs change. A very large schema may need a higher `MaxCost` for GraphiQL.
- `mixin.Time`'s SQLite default now writes velox's time format: run
  `velox-gen migrate diff`. Rows already written keep their text.
- `TxRetrySerializable` backs off between attempts.
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

- A `NULL` scans into a nil-able Go type, such as `GoType(net.IP{})`, instead of failing
  every page that selects it.
- GraphQL: a foreign key predicate holds the target's tenant as well as its policy, and a
  JSON array of the wrong length is refused. The documented worst case is now measured:
  583 MB allocated and a 47 MB response under the defaults.
- An upsert's own key no longer excuses a tenant foreign key.
- SQLite: only foreign key orphans a file made fail its migration. Drift reads `timestamp`,
  `time` and similar types in one form.
- Refused before running: `CONCURRENTLY` on a PostgreSQL partitioned table, `INPLACE` on
  MyISAM, and a rename or drop that a trigger function names.
- A request's shape is bounded before validation: spreads, value depth, name length,
  literal size and variable nodes. Attacks that took 2 to 3.5 GB are refused under 21 MB.
- GraphQL: `nodes` and deletes count their real statements; a foreign key predicate holds
  the target's Read policy; introspection switched off is a client error.
- Migrations: MySQL refuses a rename or drop that a view or trigger uses, and a `DROP COLUMN`
  that would shrink an index; SQLite `DROP TABLE` refuses broken dependents and counts
  children still present; `PRIMARY KEY (id DESC)` is refused in a rebuild; the guard resolves
  `search_path` and reads past `-to`.
- SQLite integer `Add` and `Sub` raise on overflow rather than turn the column to REAL.
- Migrations: `Up` refuses what a file would lose unaccounted, on every dialect, including
  columns and constraints added outside the snapshot, collations, and foreign key options.
  A pending generated SQLite file from before this change needs `velox:drops` or an allow.
- Migrations: SQLite rebuilds keep rowids and refuse an unaccounted child on `DROP TABLE`.
  Plans refuse truncating casts, enum arrays and a foreign `CONVERT TO`. `LintFile` reads
  `WITH`, `MERGE` and `UPDATE OR`, and flags a rebuild without `foreign-keys off`.
- SQLite: a transaction rolled back by an interrupt or `INSERT OR ROLLBACK` is broken, so
  later writes fail unsent instead of committing alone. A broken transaction refuses at the
  send, including an Around hook's retry.
- A statement on `db` inside `Tx`'s fn, on a one-connection pool, fails rather than hanging.
- `TxReadOnly` restores `query_only` and works under `_txlock=immediate`. A retry stopped by
  its context matches the context's error. `After` with `Columns` is allowed on `Count`,
  `Exists` and aggregates.
- GraphQL: an empty `Bytes` reads as `""` in `Bytes!`, and a literal `[]` stays `[]`.
- Migrations: `Up` refuses a SQLite rebuild or MySQL `MODIFY COLUMN` that would lose what
  the model cannot hold (`*Error` of Code `unmodeled`); `-- velox:allow unmodeled-loss`
  accepts it. A SQLite rebuild with a dependent view is refused.
- Migrations: SQLite type changes are destructive by affinity. PostgreSQL narrowing fails
  on a long row rather than truncating. Widening a serial widens its sequence. Renaming a
  referenced column in a SQLite rebuild works, as does dropping a foreign key's only MySQL
  index. `WithTarget(0)`, `-to 0` and `resolve -version 0` mean version 0. Allow targets
  round-trip any identifier. `Verify` fails on a stray table. SQLite's `foreign_key_check`
  covers only the touched tables and their children.
- GraphQL: introspection counts toward `MaxCost`. Entities named like introspection types
  build.
- `After` fails when an order column is not selected, rather than losing rows.
- A nested `Tx` that returns an error has undone its work, or has broken the transaction.
- An upsert cannot update a bidirectional one-to-one key.
- SQLite enforces `TxReadOnly` and refuses isolation levels other than Serializable.
- `Raw` in a transaction pins the session. A MySQL deadlock breaks its transaction, so
  later statements cannot autocommit.
- Time fields scan SQLite's text forms.
- A statement inside `Iter` on a one-connection pool fails rather than hanging. A goroutine
  the loop body starts can still block.
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
