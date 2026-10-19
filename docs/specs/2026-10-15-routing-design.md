# velox2 Routing — design

Status: implemented (routing plan) · 2026-10-15 · Go 1.27+

This is sub-project 4 of five; the table of sub-projects is at the top of the Core design, `2026-10-14-core-design.md`. It fills the pipeline's route stage (Core §5): the choice of the connection a statement runs on. It covers:
- primary/replica splitting;
- read-your-writes through session pinning;
- failing a read over to the primary when its replica errors;
- choosing a shard per statement from the context.

It is built into the core module, because executors are core internals. It adds no dependency.

## Decisions

Made by the author during design (2026-10-15):

- **Read-your-writes by session pinning.** `velox.WithSession(ctx)` marks a session, typically one request. After a write in it, its reads go to the primary for a configured window. `velox.Primary(ctx)` forces the primary. There is no replication-position tracking.
- **Sharding routes by key, with no fan-out.** The shard comes from the context: the tenant mapped by an application function, or an explicitly named shard. A statement with neither fails closed. Nothing crosses shards, and a transaction stays on one shard.
- **Replica failure falls over to the primary.** A read that fails on a replica with a connection or send error, before any row is returned, is retried once on the primary. The replica is then skipped for a back-off. There is no lag polling.

## Success criteria

1. **Nothing configured:** a DB with no routing behaves and costs exactly as today. Core's budgets (Core §8) are unchanged.
2. **Writes go to the primary:**
   - writes, every statement in `db.Tx`, and every statement in a session's pin window go to the primary;
   - no statement is ever sent to a replica of another shard, or to another shard at all.
3. **No stale read of your own write:** within the pin window, a session never reads from a replica after its own write.
4. **Fail closed:** on a sharded DB, a statement whose shard cannot be determined is refused, and nothing is sent.
5. **Every dialect:** it holds on all three dialects. SQLite has no replicas, but its file-per-shard layout is supported.

## 1. Connections

A `velox.Conn` is an opaque connection pool that velox can run statements on:

```go
func PgxConn(pool *pgxpool.Pool) Conn
func SQLConn(db *sql.DB) Conn
```

- `Open(d, db, opts...)` and `OpenPgx(pool, opts...)` are unchanged. Their argument is the primary.
- Every `Conn` of one DB has the DB's dialect. A pgx `Conn` is valid only for PostgreSQL. `Open` panics on a mismatch, as it does for other misconfigurations.

## 2. Replicas and the route stage

`velox.WithReplicas(conns ...Conn)` adds read replicas to a DB's primary.

The route stage runs after render and before the `Around` hooks. It picks:

| Statement | Goes to |
|---|---|
| INSERT, UPDATE, DELETE, an upsert | the primary |
| any statement in `db.Tx` (and its savepoints) | the transaction's connection, on the primary |
| a read whose context says `velox.Primary(ctx)` | the primary |
| a read in a session within its pin window (§3) | the primary |
| any other read (SELECT, Count, a `With` load, `Iter`) | a replica, round-robin; the primary if none is available |

`velox.Primary(ctx)` marks a context so that every read on it goes to the primary.

`Statement.Route` records the choice before the `Around` hooks run:

```go
type Route struct {
    Shard    string // "" on an unsharded DB
    Replica  int    // index into the replicas; -1 for the primary
    Fallback bool   // a replica failed and the read was retried on the primary
}
```

## 3. Read-your-writes

`velox.WithSession(ctx)` returns a context carrying a new session. A session records the end of its pin, set by each write made through it to the time of the write plus the DB's window, and never lowered:
- any INSERT, UPDATE, DELETE or upsert that succeeded;
- the commit of a `db.Tx` that made one.

`velox.WithPinWindow(d)` sets how long after a write the session's reads go to the primary; `velox.PinUntil(ctx, t)` raises the pin's end to `t`, and `velox.PinnedUntil(ctx)` returns it. The default is 2 s, sized to typical replica lag, so a larger lag needs a larger window.

- Without a session in the context, nothing is pinned. The documentation says so, and the example shows request middleware that starts a session.
- A session is safe for concurrent use. Recording a write and reading the time use atomics.
- A session lives only in its context. Pinning across requests is out of scope: an application stores the pin's end, from `velox.PinnedUntil(ctx)`, and restarts the session's pin with `velox.PinUntil(ctx, t)`.

## 4. Failover

A read sent to a replica that fails before any row was returned falls over:
- **Which errors count:** a connection or send error, meaning `driver.ErrBadConn`, a `net.Error`, a pgx connect error, a MySQL error that is the server refusing or ending the connection (1040, 1045, 1053, 1129, 1130, 1203, 1226, 1927), or a closed connection.
- **What happens:** the read is retried once on the primary, and `Route.Fallback` is set. The replica is skipped for `WithReplicaBackoff(d)`, 5 s by default, and then tried again.

Nothing else falls over:
- a query error (syntax, a constraint, a permission);
- a context cancelled or past its deadline;
- an error after a row was returned. In particular, once `Iter` has yielded a row, nothing is retried.

When every replica is skipped, reads go to the primary.

An unreachable replica needs a connect timeout to fall over. Give each replica's pool one shorter than the request deadlines: pgx's `connect_timeout`, a MySQL DSN's `timeout=`. Neither driver sets one, so a replica that drops packets stalls the dial until the operating system gives up. A dial that times out is a dial error and falls over. A dial still hanging when the request's context ends is the context's error: it neither retries nor backs the replica off (Ruling 5).

## 5. Shards

```go
db := velox.OpenShards(velox.Postgres, map[string]velox.Shard{
        "eu": {Primary: velox.PgxConn(eu), Replicas: []velox.Conn{velox.PgxConn(euR)}},
        "us": {Primary: velox.PgxConn(us)},
    },
    velox.WithShardFor(func(ctx context.Context, tenant any) (string, error) { … }))
```

`OpenShards` returns a `*DB` whose statements each run on one shard. Each shard has a primary and optional replicas, and §2–§4 apply within it.

The shard is chosen from the context, in this order:
1. `velox.OnShard(ctx, name)` names a shard explicitly, for admin jobs and data on no tenant.
2. Otherwise, the context's tenant (`WithTenant`) is passed to the `WithShardFor` function. Its error refuses the statement.
3. Otherwise, the statement fails with `ErrNoShard`. That includes a context under `AllTenants` with no `OnShard`.

Because the shard comes from the context and not from the statement:
- a `db.Tx` call picks its shard at begin, and every statement in it runs there;
- an entity with no tenant column lives on the shard the context names.

Fail closed:
- An unknown shard name, from `OnShard` or returned by `WithShardFor`, fails with `ErrNoShard`, naming the shard.
- A statement inside a transaction whose context names another shard fails, with "statement for shard X in a transaction on shard Y". Nothing is sent.

`WithShardFor` is required with `OpenShards`; without it, `OpenShards` panics.

## 6. Errors

`ErrNoShard` = `"no shard for the statement"` is a new sentinel, wrapped as `velox: <Entity>: no shard for the statement: …`, or `velox: transaction: …` for a `db.Tx`. Failover is invisible to the caller except through `Route.Fallback`. The primary's error, if the retry fails, is returned as usual.

## 7. Observability

`contrib/otel` (observability design §2) adds three span attributes:
- `velox.route.shard`;
- `velox.route.replica`, a bool;
- `velox.route.fallback`.

Metrics keep their attributes; `velox.route.shard` is added to them because shards are few. A counter `velox.route.fallbacks` counts failovers by shard.

## 8. Performance

- **Nothing configured:** Core's budgets are unchanged. The route stage is one branch.
- **With replicas, a cached one-row read:** at most one more context lookup, for the session and `Primary` marks, and no allocation. A budget test pins this.

## 9. Testing

- **Unit tests,** with one `FakeExec` per connection, asserting which connection each statement reached:
  - every row of §2's table;
  - pinning in and out of the window, and with no session;
  - `Primary(ctx)`;
  - failover on each error class, and its absence on query errors and cancelled contexts;
  - the back-off and its expiry;
  - `Iter` after a yielded row;
  - shard selection, `ErrNoShard` and the transaction-shard mismatch;
  - concurrency under `-race`.
- **Real databases:** PostgreSQL (pgx and database/sql) and MySQL, with a second database standing in as the "replica" and a third as a second shard. The tests prove where statements were sent by reading each database. They do not test replication.
- **Example:** `example/routing`, on SQLite files: two shards, a "replica" file, and a session showing pinning.

## 10. Out of scope

- Polling replica lag.
- Tracking replication positions (LSN, GTID).
- Cross-shard queries and fan-out.
- Resharding.
- Consistent hashing.
- Connection-pool management: an application builds the pools.

## Rulings made in implementation

Made while building it (plan `2026-10-19-routing.md`, Rulings 1–11, as revised during review), and what they changed in the sections above.

From the plan:
1. `PgxConn` lives in the core beside `OpenPgx`; `Conn` is opaque, so the later dialect/pgx split can add constructors without changing a routing API.
2. `WithSession`, `Primary` and `OnShard` share one context key, so a read on a DB with replicas makes one `ctx.Value` lookup for all three; `WithShardFor`'s tenant lookup is a second, made only on a sharded DB.
3. A query's `With` loads run on the connection the query ran on (the parent's replica, or the primary after a fallback), so parents and children never come from replicas with different lag.
4. (Revised.) A connection-class error from the query call or from the rows, on a replica, is retried once on the primary unless the reader has already handed a row to the caller; the replica always goes into back-off; a net timeout falls over only from a dial, so a slow query is not moved to the primary.
5. A context cancelled or past its deadline never puts a replica in back-off and is never retried (`ctx.Err()` is checked before the error is classified).
6. After a fallback, `Route.Replica` is `-1` and `Route.Fallback` is `true`.
7. `PinnedUntil(ctx)` is added, so an application can store the time to give `PinUntil` on a user's next request; a session stores its pin end, a write sets it to now plus the window and never lowers it, and `PinUntil` on a context without a session does nothing.
8. In a transaction, a statement naming no shard runs on the transaction's shard; one naming another fails with `statement for shard "X" in a transaction on shard "Y"` (not `ErrNoShard`); a `WithShardFor` error fails the statement; a commit that made a write marks the session of the `Tx` context, and a rolled-back savepoint does not clear the mark.
9. `SQL(ctx)` runs the route stage: it returns `ErrNoShard` and the transaction-shard error as a run would, sets `Statement.Route`, and never fails over.
10. `db.Tx(ctx, fn, ReadOnly())` runs on the primary like every transaction; read-only transactions on replicas stay out of scope.
11. Every DB sets `Route{Replica: -1}`, with `Shard` on a sharded DB, so a hook never reads `Replica: 0` as replica 0 on a DB without replicas.

From the review rounds, where they change behaviour:
- A pinned `With` load falls over like any replica read, and one whose replica is in back-off goes to its shard's primary with `Fallback` set.
- The classes that fall over, beyond §4's: `io.EOF`, `io.ErrUnexpectedEOF`, `pgconn.SafeToRetry`, a `*pgconn.PgError` with code 57P01, 57P02 or 57P03, and any pgx `ConnectError`.
- MySQL numbers that occur only when connecting or when the server is going away fall over like pgx's `ConnectError`: 1040, 1045, 1053, 1129, 1130, 1203, 1226 and 1927.
- MySQL's `ErrInvalidConn` is matched by type and text, without importing the driver, which would run its `init` in every program; a driver error reading "invalid connection" from another driver also falls over, once.
- `OpenShards` also panics on a shard named `""`, a zero replica `Conn` and a `PgxConn` primary on a non-PostgreSQL DB; `OnShard(ctx, "")` fails closed.
- An error from the rows counts for failover only when the reader itself returned no error: pgx reports a failed `Scan` again through `rows.Err()`, so a `Scanner` failing with `io.ErrUnexpectedEOF` would otherwise look like a broken connection and evict a healthy replica.
- A `WithShardFor` error matches both `ErrNoShard` and the application's error.
- On a tenant-scoped entity, a context with no tenant fails with `ErrNoTenant` before routing; `AllTenants` without `OnShard` is what shows `ErrNoShard`.
