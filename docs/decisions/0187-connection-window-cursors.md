# 187. A connection takes after and before together

Status: accepted, 2026-11-27. Extends 0059 and 0166: the rule that a page
is bounded on one side only is replaced.

## Context

A Relay connection takes `first`, `after`, `last` and `before`. The Relay
spec's algorithm applies both cursors to the list, which leaves the window
strictly between them, and then takes the first or the last rows of it.
entgql and veloxe both accept `after` with `before`, `first` with `before`
and `last` with `after`. velox-graphql refused all three with
`ErrBadPagination`, on the ground that keyset paging bounds a page on one
side only. A client that sent a window query, which worked under entgql or
veloxe, got an error after moving to velox.

`first` with `last` stays refused: entgql and veloxe refuse it, and the spec
discourages it, since which rows the page holds would not be plain.

## Decision

- `untyped.TableQuery.Before(row, vals)` limits the query to the rows before
  `row` in the query's order as it stands: After's condition on the order
  reversed. A term descends where it ascended, and NULLS FIRST and LAST swap,
  so the NULL that sorted last sorts first. `vals` are the row's values of
  the orderings by an expression, as `AfterValues` takes them. It is applied
  at the call, as a condition, so `After` and `AfterValues` hold beside it
  and every path that reads the statement (a page, a count, an edge's batch)
  sees it. The order must be set before it is called.
- A connection pages forward with `first`, and backward with `last`, as
  before. With neither, `after` pages forward, and `before` alone pages
  backward from the cursor. The cursor the page starts from is `After`'s
  row, and the other cursor bounds the window with `Before` on the order the
  page reads: for a backward page the order is reversed, so `after` is the
  far end.
- `hasNextPage` and `hasPreviousPage`: the probe row sets the flag of the
  page's own side. The flag of the other side is true when the page starts at
  a cursor, as before, and the cursor the page ends at, when given, makes the
  far side's flag true as well: the rows past the window exist. This is
  entgql's rule (`HasNextPage = before != nil`). It can be true when the
  cursor's row has since been deleted, which Relay allows.
- A count of the connection (`totalCount`) is the whole list, as before.

## Consequences

- Public API: `TableQuery.Before`, in the snapshot.
- No generated-code change.
- Every cursor of a window is read and checked as any cursor: another type,
  another order or a value of another type is `ErrBadCursor`.
- Tests: every pair of cursors of four orders on a nullable and tied column
  (`TestBeforeNullsExact`, SQLite, PostgreSQL and MySQL), expression
  orderings with NULLs first and last (`TestOrderValuesKeyset`), and every
  window of a connection at several page sizes (`TestPaginate`).
