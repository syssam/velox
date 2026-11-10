# 0067. AllowUnbounded allows a read with no Limit

- Status: accepted, 2026-11-10. Amends [0022](0022-group-by.md), which names
  the call `Unbounded`.

## Context

A read needs a `Limit`, or it fails with `ErrUnbounded`. The call that waives
the check was `Unbounded()`. Two things were wrong with the name:

- It reads as "remove the limit". It does not: it only waives the check. A
  `Limit` the query sets still applies, and so does `WithMaxLimit`'s maximum
  (`internal/engine/guard.go`). A porter's report took it to undo pagination.
- It is nobody's name. The sources of gorm v1.31.2, ent v0.14.6, bun v1.3.0,
  sqlboiler v4.19.7 and goqu v9.25.0 do not hold the word in non-test code, and
  none of them guards a read against a missing limit. gorm cancels a limit with
  `Limit(-1)`. gorm's guard on writes, `ErrMissingWhereClause`, is opted out of
  with `AllowGlobalUpdate`: an `Allow` name for a waived guard.

## Decision

`Query.AllowUnbounded()` replaces `Query.Unbounded()`, with the same effect.
Its doc comment says what it waives and that a `Limit` and `WithMaxLimit` still
apply. The errors say `add Limit, or AllowUnbounded to read every row`.

`ErrUnbounded` and the `Statement.Unbounded` field keep their names: they name
the condition and the state, not the call. The write-side `All()` is not
renamed here.

## Consequences

- Pre-1.0, nothing is kept: every use in the repository, the examples and
  their goldens, and the guide changed. `docs/history` is a frozen record and
  keeps the old name.
- The guard itself is unchanged. A change to its rule, such as skipping it for
  a read pinned to a unique key, is a separate decision.
