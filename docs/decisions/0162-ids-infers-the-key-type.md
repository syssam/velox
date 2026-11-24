# 0162. IDs infers the key's type from a generated marker

- Status: accepted, 2026-11-24. Supersedes 0158's `q.IDs[K](ctx)`: the
  method stays, and its type argument goes.

## Context

0158 made `IDs` a method of `Query`, `q.IDs[int64](ctx)`. `Query[E]`
knows the entity and not its key's type, so the caller named K, and a
wrong K compiled and failed when the statement ran ("IDs[int] of a key
whose field is int64"). An entity with a composite key also compiled and
failed at run time. ent's `client.User.Query().IDs(ctx)` is typed because
ent generates a query type per entity, which velox does not (0020, 0055).

`q.Values(ctx, U.ID)` is checked when the program compiles, but `IDs`
reads better and is ent's name, so the user kept `IDs` and asked for a
better design.

Go infers a type parameter by unifying a constraint's methods: with
`P interface{ *E; VeloxKey(K) }`, the core type `*E` gives P, as `Lazy`
does, and `*E`'s method `VeloxKey` then gives K.

## Decision

- velox-gen writes, for an entity whose primary key has one column,
  `func (*User) VeloxKey(int64) {}`, beside `VeloxMeta`. Its parameter
  is the key field's Go type. It does nothing.
- `func (q Query[E]) IDs[P keyed[E, K], K any](ctx) ([]K, error)`, where
  the unexported `keyed[E, K]` is `interface{ *E; VeloxKey(K) }`:
  `db.Query[veloxgen.User]().Limit(n).IDs(ctx)` is `[]int64`, a UUID key's
  `[]bindtype.UUID`, a string key's `[]string`.
- A composite key gets no marker, so `IDs` on it does not compile. Nor
  does a type argument, `IDs[int64]`, or a result of another type.
- The run-time key-type check and `engine.KeyTypeError` go.
- `velox.GeneratedCodeIsVersion31`.

## Consequences

- No type argument to write or get wrong, and errors move from run time
  to compile time. `repocheck`'s `TestValuesAtCompileTime` pins the three
  refusals.
- Every generated entity has one more method, empty. bench/scale at 500
  entities and 100 services, base (8c3da6ba) and branch alternated twice
  on darwin/arm64: cold_build_x 6.59, 6.76 against 6.48, 6.76;
  schema_edit_rebuild_x 5.96, 6.06 against 6.02, 6.32; service binary
  5.699 MB against 5.683 MB (16 KB smaller: a caller of `IDs` no longer
  compiles its run-time key check); link peak 220, 239 MB against 225,
  221 MB. Every difference is within the runs' own spread.
- Callers change `.IDs[K](ctx)` to `.IDs(ctx)`, and every schema
  regenerates.

## Rejected

- Removing `IDs` for `q.Values(ctx, U.ID)`: typed, but longer, and not
  ent's name.
- `Query[E, K]`: every signature that names a query changes, for one
  method.
- A marker that returns the key, `Key() K`: a schema field named `key`
  would collide with it, and a body costs more than an empty method.
- An unexported marker method: a constraint in package velox could only
  be met by types in package velox.
