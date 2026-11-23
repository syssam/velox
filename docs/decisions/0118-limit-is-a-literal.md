# 118. A query's LIMIT is a literal

Status: accepted, 2026-11-23.

## Context

velox bound every value as a parameter, and SQLite's LIMIT was the one
exception (9a7a117d): SQLite prepares a statement whose LIMIT is a
parameter again at each run, about eight times slower.

On PostgreSQL, bench/compare's WhereHasEdge, an EXISTS read ordered by key
with `LIMIT 20`, ran 30% behind ent's and veloxe's after the bool constant
was inlined. EXPLAIN on the benchmark's data showed why. pgx's statement
cache prepares each text once, and PostgreSQL moves a prepared statement to
its generic plan after five runs. A generic plan cannot see a parameter's
value, so the planner guesses a tenth of the table's rows for `LIMIT $1`.
It then picked an index scan of the child table that filtered 342 rows,
0.091 ms against 0.056 ms for the custom plan's sort of the 60 matching
rows. ent writes LIMIT as a literal in every dialect, so its statement had
no parameter to guess.

## Decision

- LIMIT is in the statement's text in every dialect. The render cache's
  shape key holds its value (`ShapeKey`), so each limit is a shape and a
  prepared statement of its own.
- OFFSET stays a parameter. Its values are unbounded, one per page number,
  and a literal would grow the render cache and pgx's statement cache
  without bound; the generic plan already sees OFFSET as a cost to skip.
- A per-owner limit (`Each`'s `vx_rn <= ?`) stays a parameter: it filters
  a window, and no measurement showed a plan change.

## Consequences

- An application uses a handful of page sizes, so the extra shapes are few.
  A Limit taken from a client is capped by the caller (GraphQL's `first`
  is at most `Limits.MaxPage`, 100 by default), which bounds them too.
- MySQL prepares each statement on its own and has no generic plan; the
  literal costs it nothing and keeps one rule for every dialect.
- `TestPostgresLimitLiteral` prepares a read under
  `plan_cache_mode = force_generic_plan` and checks that the plan's Limit
  node estimates the limit's rows; `TestSQLiteLimitLiteral` covers SQLite.
