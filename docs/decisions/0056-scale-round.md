# 56. Optimistic locking, and what large deployments get elsewhere

Status: accepted, 2026-11-08.

## Context

A review asked what velox lacks for a large company's production use: keys
other than int64, optimistic locking, overload control, bulk loading,
changing shard layouts, CockroachDB, online DDL on large MySQL tables and a
transactional outbox. Each was checked against the code before deciding.

## Decision

**Keys of any type were already supported, and are now tested.** A schema
that declares its own `id` keeps it: `field.UUID("id", T{})` with a
`Default`, or `field.String("id")`. Only the implicit ID is int64.
`TestNonIntegerKeys` runs a UUID key, a string key, an int64 key with a UUID
foreign key and a join of the two through inserts, With in both directions,
Of, HasWith, Save, an upsert, After and a cascading delete, on PostgreSQL
(pgx and database/sql), MySQL and SQLite.

**Optimistic locking is a field option.** `field.Int64("version").Version()`
marks the column (`ColumnDescriptor.Version`). velox adds 1 to it in every
UPDATE it sends, a hook's and its own updates of a bidirectional key
included, and in an upsert's update branch, named by its table
(`"papers"."version" + 1`). Save adds `version = *x's` to its key and fails
with `ErrStale`, which also matches `ErrNotFound`, when no row matches; on
success it stores the new version in `*x`. A write that assigns the column,
a hook's included, fails with `ErrVersionSet`. An update of edges only sends
no UPDATE of the row and leaves it. One field per schema, an integer that is
not the ID, Nillable, Immutable or given an UpdateDefault. velox-graphql
leaves it out of create and update inputs; a GraphQL update does not carry
the version the client read. Neither ent nor old velox has it; GORM's
`optimisticlock` and JPA's `@Version` are the model.

**No overload control in velox.** A statement waits for a connection only
until its deadline, and `WithDeadlines` gives every statement and
transaction one (`MaxStatement`, `MaxTx`). Shedding load belongs before the
request does any work: at the RPC server or proxy (adaptive concurrency
limits). The pool's metrics are the driver's (veloxotel's doc).

**No COPY in velox.** COPY returns no keys, and a bulk load through velox
would have to run every row's policy and hooks, which is what InsertAll's
multi-row INSERT does. A backfill that may skip them uses pgx's `CopyFrom`
on the pool velox was opened on (`veloxpgx.Open(pool)`).

**Shards change outside velox.** `WithShardFor` is a function, so it can read
a live tenant directory: moving a tenant is a change to that directory.
Adding a shard is a new `OpenShards`, swapped in for the old `*DB` (an
`atomic.Pointer`), which in-flight statements keep using.

**CockroachDB is not supported.** Run against CockroachDB 25.2, velox's
runtime works for reads, writes, RETURNING, ON CONFLICT, savepoints, locks
and 40001 retries (`TxRetrySerializable`). Two behaviours differ:
CockroachDB sorts NULL first ascending where PostgreSQL sorts it last, so
velox's PostgreSQL rendering of a nullable order, and After over it, drop
rows; and `FOR UPDATE ... SKIP LOCKED` with an EXISTS filter locks scanned
rows it does not return. Support needs a dialect, or detecting the server,
and a CI lane; migrate refuses the server (it reports PostgreSQL 13).

**Online DDL on large MySQL tables is Atlas's or gh-ost's.** velox's migrate
refuses what MySQL refuses partway and sets `lock_timeout` (0026); it does
not copy tables in the background. Migrations are not velox's focus (0006).

**An outbox is the application's.** A hook that writes an outbox row through
`Mutation.DB` inside a Tx commits with the write; velox adds no API for it.

## Consequences

- `ColumnDescriptor`, `ColumnInfo` and the plugin model's `Field` gain
  `Version`; code generated without a Version field is unchanged, so
  `GeneratedCodeIsVersion19` stays.
- `TestOptimisticLocking` runs on every dialect; `TestVersionSQL` pins the SQL.
- CockroachDB support, if wanted, starts from the NULL order.
