# routing

Routing on `gen.Note`: two shards chosen by organization, one read replica for the
`eu` shard, and a request that reads its own write.

```sh
cd examples
go run ./routing
```

```go
db := velox.OpenShards(velox.SQLite, map[string]velox.Shard{
	"eu": {Primary: velox.SQLConn(eu), Replicas: []velox.Conn{velox.SQLConn(euReplica)}},
	"us": {Primary: velox.SQLConn(us)},
},
	velox.WithShardFor(shardFor),                   // context -> shard name
	velox.WithPinWindow(250*time.Millisecond),      // default 2 s
)

ctx = velox.WithSession(ctx)                           // per request, in middleware
db.Update[gen.Note]()…Exec(ctx)                        // a write: pins this session
db.Query[gen.Note]()…One(ctx)                          // in the window: the primary; after: a replica
db.Query[gen.Note]()…One(velox.WithPrimaryReads(ctx))  // always the primary
velox.WithShard(ctx, "us")                             // name a shard without an organization
```

- `WithShardFor` gets the context (here it reads the organization that
  `schema.OrgKey.With` put there) and returns a shard name. A statement is sent to that shard's primary, or for a read, to one of
  its replicas in turn. Nothing runs on two shards.
- A statement whose shard cannot be found fails with `ErrNoShard` and is not
  sent. `WithShardFor` returns "" for a context that names none. On a scoped
  entity such as `Note`, a context with no organization would fail with
  `scope.ErrNoScope`; `velox.WithoutPolicy` lifts that check and shows
  `ErrNoShard`. `WithShard` names the shard for work that has no organization.
- A write pins its session's reads to the primary for the pin window, so the
  request reads what it wrote. `velox.PinnedUntil(ctx)` gives the time to keep
  across requests, and `velox.PinUntil(ctx, t)` restores it on the next one.
- The example never copies a write to the replica, so the read after the
  window is stale. Real replication is out of scope: the replica here is an
  old copy of the file. If a replica fails to answer, the read is retried once
  on the primary (`Route.Fallback`); in production, give each replica's pool
  a connect timeout shorter than your request deadlines (pgx `connect_timeout`,
  MySQL DSN `timeout=`), or a replica that drops packets stalls reads until
  their deadline. The example's `Around` hook records each
  statement's `Statement.Route`, and the output says where each one ran.
- The three files at the end of the output show where the writes went: the
  `eu` primary has the update, the replica has not, and `us` has only its own
  row.

The output is in `testdata/output.golden`.
