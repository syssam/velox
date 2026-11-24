# 171. A loaded entity carries the DB that read it, for its edges

Status: accepted, 2026-11-24. Supersedes the "an entity is a plain struct,
with no client in it" rule of `docs/coming-from-ent.md`, and amends 0121
(a resolver no longer needs a DB in its context to read an edge). Amended
by 0173: a query's connection needs no adapter or `Service` either.

## Context

ent and veloxe put the client in every entity, so `m.QueryAuthor()` and
`post.Author(ctx)` work anywhere and a GraphQL server needs no setup but its
resolver's client. velox kept entities plain: an edge read took its DB from a
GraphQL request's context (`veloxgql.DB`, `graphqlgo.With`), and code outside
GraphQL wrote `db.Query[User]().Where(Users.ID.EQ(m.AuthorID))`. The user
found that harder to use, and ports from veloxe rewrote about 200 `m.QueryX()`
lines by hand. gRPC and REST code has no request context of velox's at all.

ent's model has two known traps, which velox must not keep:

- An entity read in a transaction holds the transaction: an edge read after
  commit fails ("transaction has already been committed"); ent's answer is
  `Unwrap()`, which users forget. nextapp's `ErrOutsideTx` bugs were this.
- An entity read through a privileged client keeps the privilege: handed to
  other code, its edges are read with it.

## Decision

- Every entity velox reads or writes holds, in one unexported field, a
  pointer to the source it came from: `All`, `First`, `Only`, `Iter`, `With`
  (nested and per owner), joined targets (`Join`, `LeftJoin`), `Insert`,
  `InsertAll`, `InsertBulk` and upserts that return rows, `UpdateQuery.One`
  and `Returning`, `Paginate`, `db.Rows` scanned into an entity. Storing it is
  one pointer store per row: no allocation per row.
- Generated code gives each edge a read: `m.QueryX()` returns a
  `velox.Query[T]` of the edge's rows, ent's name and shape. It is read only:
  a write names its DB (`db.Update`, `db.Insert`, `db.Delete`).
- The source is the DB the statement ran on, so its filters, interceptors,
  scope, limits and routing carry over, with two exceptions:
  - It never keeps `WithoutPolicy`: an edge read is under the entities'
    policies, with the viewer in its context. Code that needs an edge without
    them queries it on its own `WithoutPolicy` DB.
  - A transaction's source falls back to the transaction's parent DB once the
    transaction ends, commit or rollback, so an edge read after commit runs
    outside it. The source holds the transaction through one object per
    transaction, cleared at its end, so a long-lived entity does not keep the
    `*sql.Tx` alive. After a rollback such a read can see rows of other
    transactions, never the rolled-back ones.
- An entity no statement made (built by hand, `velox.LoadedOne`, decoded from
  JSON) has no source: its `QueryX()` fails each statement with an error that
  says so and names `db.Query`, never a nil-pointer panic.
- GraphQL: a generated edge method (`post.Author(ctx)`) reads through the
  entity's source, batched with its siblings when the request has velox's
  state, one statement otherwise. A resolver uses its own DB
  (`r.DB.Query[veloxgen.User]()`). The adapters (`gqlgenx.Setup`,
  `graphqlgo.With`) keep the per-request batching, selection and limits, and
  keep their signatures.
- `reflect.DeepEqual` of two loads from different DBs, and `go-cmp` without
  `cmpopts.IgnoreUnexported`, now see the field, as with ent. `veloxtest`
  gets a comparison that ignores it, and `coming-from-ent.md` says so.

## Consequences

`GeneratedCodeIsVersion` is bumped and every example regenerated (nextapp-ce
cleared it). The cost of the per-edge methods is measured with
`internal/buildcost` and the scale lane before merge; the per-row cost with
the HundredRows benchmark.

## As built

- `velox.Source` is one pointer word, to engine's `Source`. A plain DB
  makes its own at `Open`, and its copies (`WithoutPolicy`, `Record`)
  share it, so a `WithoutPolicy` DB gives its entities the policy-on DB it
  was copied from. Filters, interceptors and limits are the DB's
  configuration and a scope is in the context, so both carry over. A transaction or savepoint makes one,
  pointing at its handle and at the parent's, cleared at its end: one
  allocation per transaction (two for one begun `WithoutPolicy`, combined
  into one object). `UseTx` points at the adopted transaction.
- The field is written only on an entity with edges: one without has no
  `QueryX` to read it, so it stays a plain struct.
- `velox.QueryEdge[T](src, owner, Handles.X.I)` is the body of every
  `QueryX`, one line. It is generic in the target alone and takes the
  owner as an `any`, whose metadata the engine finds by its type (every
  entity with a Source had its metadata built first), so it is compiled
  once per target type, not once per edge: a `QueryEdge[E, T]` with the
  `Rel` handle cost an entity package about 27 KB per edge (the relations
  example's 12 edges: +327 KB, then +167 KB).
- A `velox.Query[T]` has no write, and `untyped.FromQuery` none either,
  so read only needs no check of its own.
- `velox.Scan[T]` and a recorded `Change` give no source. `db.Rows`
  yields untyped rows, not entities; `db.Raw`, which scans into an
  entity, gives its rows the source.
- The DB that began a transaction joins it. Under the context of a live
  transaction, a statement on the DB that began it (same Source, same
  WithoutPolicy reason, same recording) runs on the transaction's handle,
  or on the savepoint's whose fn got the context, and its `Tx` makes a
  savepoint. Any other DB still fails with `ErrOutsideTx`, as before
  (amends the rule 0132, 0135 and 0137 rely on: a filter or policy that
  captured the DB now reads in the transaction too). The engine redirects
  at its entry points (`Plain`, `Run`, `DryRun`, `Tx`, the write and load
  entries, raw SQL); the context carries the innermost frame beside the
  root (`txCtx.self`). It costs a statement on a DB outside a transaction
  one more context lookup. `TxFrom` stays for a helper whose DB differs from
  the beginner's.
- velox-graphql: an edge's DB is its row's (`untyped.SourceDB`), else the
  request's. A connection of a query still fails without an adapter or
  `veloxgql.Service` (0105, 0121). A resolver reads and writes on the one
  DB the server serves (`r.DB`): in a gqlgen mutation, which runs in a
  transaction gqlgenx begins on that DB, its statements under the
  resolver's ctx run in that transaction, serialised by the extension's
  per-field lock as `veloxgql.DB(ctx)`'s were. `veloxgql.DB(ctx)` is for
  code outside a resolver.
- Compile cost, bench/scale at 500 entities and 100 services, base
  1461075f against this branch, three alternating runs each
  (GOFLAGS=-p=2, Apple M3 Max): cold build CPU 167 s to 175 s (+4.6%),
  schema edit and rebuild 149 s to 155 s (+4.1%), link peak 219 MB to
  237 MB; with velox-graphql, cold 207 s to 211 s (+2.3%), edit 189 s to
  194 s (+2.6%), link peak 286 MB to 299 MB, service 7.0 MB to 7.1 MB.
  The runtime is unchanged: HundredRows 106 allocations and about
  21.5 KB either way, a statement in a transaction 6; a transaction
  makes one object more.
