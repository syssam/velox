# 0022: grouped aggregates take a key made by the handle's Key method

Date: 2026-10-31

The key is the bare handle since
[0158](0158-values-and-ids-are-query-methods.md), which supersedes the
choice of `Key()` below; the rest stands.

Several keys, `HAVING` and an ordering by an aggregate are `Query.Select` (0074); this record stands for the one-key forms.

## Context

`Sum`, `Avg`, `Min` and `Max` left `GROUP BY` to `Raw`, which skips the policy,
a scope included,. A typed form must infer the key's Go type from a column handle, refuse a
JSON column and another entity's column at compile time, and cost an entity's package
nothing it does not use (`TestGenericTypesCompileSmall`).

## Decision

One key:

    func CountBy[E, K any](ctx, q Query[E], key GroupKey[E, K]) ([]Group[K, int64], error)
    func SumBy[E, K, V any](ctx, q Query[E], key GroupKey[E, K], c NumberCol[E, V]) ([]Group[K, V], error)

`AvgBy` returns `[]Group[K, float64]`; `MinBy` and `MaxBy` as `SumBy`. The call is
`velox.SumBy(ctx, q, I.OrgID.Key(), I.Total)`.

- **The key is `handle.Key()`, a generic method, not the bare handle.** Inferring K from a
  bare handle needs K in the handle's method set or in its struct. The sizes below are
  the archive of `internal/testmodel`, which has 30 handle types:

  | How K reaches the call | Archive | Budget |
  | --- | --- | --- |
  | today, no grouping | 1,891,836 | |
  | an ordinary method `groupKey(*E, *K) int32` on each handle | about +11.5 KB per handle type | over: 26,262 against 19,456 for `NumberCol` |
  | a phantom field, `struct{ _ key[E, V]; I int32 }`, with an unnamed struct parameter | 1,967,218, +4.0% | within, by 2 KB |
  | the generic method `Key` | 1,894,502, +0.14% | within |

  The phantom field keeps the bare handle at the call and costs each entity's package 4%
  whether it groups or not. `Key` costs one word at the call and compiles only there.
  Like every other handle method, it goes through `Lazy`.
- **A JSON column has no `Key`.** Its values compare differently by dialect. A handle
  made by hand over an encoded column fails at rendering.
- **`Statement.GroupBy` is the key's column index plus one.** Column 0 is a real key, and
  0 must stay what a statement without grouping holds, as a hook that writes
  `*st = Statement{...}` leaves.
- **NULL keys.** A nullable handle's `Key` makes K a pointer, and NULL one group with a
  nil Key. It sorts where the database puts NULLs: first on SQLite and MySQL, last on
  PostgreSQL. velox does not normalize this, as `OrderBy` does not. *(Superseded by
  0032: `OrderBy` sorts NULL last ascending on every dialect, and so does the key.)*
- **NULL values.** A group with only NULLs sums to 0, as `Sum` is 0. `AvgBy`, `MinBy` and
  `MaxBy` give it no entry, as `Avg`, `Min` and `Max` return false: a pointer would make
  every caller check for a case that a `NumberCol`, NOT NULL by its schema, rarely has.
- **Order.** `ORDER BY` the key, ascending, always. Text keys follow the database's
  collation. velox sets it in `Statement.OrderBy`, so a Rewrite hook can see it and,
  as on a Count, one that changes it gets it rendered.
- **What is read of q.** As `Sum`: Where, the Read policy (a scope included) and After's
  condition apply; ordering, limit, offset, columns and relations are ignored; a lock is
  refused.
- **The limit guard applies.** A key may be unique, so the groups can be as many as the
  rows: q must say `AllowUnbounded()`, or the statement fails with `ErrUnbounded`. A `Limit` does
  not stand in for it, as it is ignored; the error says so.
- **The row cap applies.** Groups are rows: `WithMaxRows` and q's `MaxRows` send a LIMIT
  and fail with `ErrTooManyRows` past it, unlike `Sum`, which returns one row.
- **Rewrite hooks.** A hook that changes `GroupBy` is refused, as for `Aggregate`: the
  scan reads a key and a value. The render cache's shape holds the key.

## Rejected

- Honouring q's `Limit` and `Offset` over the groups: `Sum` ignores them, and a top-N
  wants an order by the value, which is not offered.
- More than one key, and `HAVING`: `Raw` still holds them.
