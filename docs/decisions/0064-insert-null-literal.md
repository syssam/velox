# 0064. An INSERT writes a NULL that every row holds as the literal NULL

- Status: accepted, 2026-11-10.

## Context

An insert writes every column but a zero generated key (`engine.InsertColumns`).
A nullable column that a row leaves nil was bound as a parameter holding nil. A
batch of 100 rows of an entity of 25 columns, 13 of them nil, sent 2,500
parameters; old velox lists only the columns some row sets, 12 here, and sends
1,200 (`dialect/sql/sqlgraph` `batchCreator.nodes`).

The parameters are the cost. A scratch benchmark of `database/sql` with pgx on
PostgreSQL 18, 100 rows of 25 columns, 12 samples of each:

| Statement | Median | Bytes per op |
| --- | --- | --- |
| 25 parameters per row | 1.03 ms | 523 KB |
| 12 columns, 12 parameters per row | 0.63 ms | 262 KB |
| 25 columns, 13 literal `NULL`s | 0.72 ms | 262 KB |

## Decision

A column that holds NULL in every row of an INSERT is written as the literal
`NULL`, with no parameter. A cell is NULL when its column is nullable and has
no codec, and its value is a nil pointer, a nil interface or a nil `[]byte`
(`nullCell`). A column with a value in any row keeps a parameter in every row.

The column is still written, so a column `DEFAULT` does not apply: an unset
`Nillable` field stores NULL, as the guide says, and `SchemaDefault` is still
for rows written outside velox. Omitting the column, as old velox and ent do,
would store the default instead, and would need `SchemaDefault` in the
descriptor.

The statement's shape key holds which columns are literal (`nullsKey`), so a
cached text is never used for another set. Hooks see `Statement.Rows` as
before, with the nil values in them.

## Consequences

An insert sends fewer arguments, and a statement's arguments are no longer one
per cell. `PerStatement` still counts every column, so a batch splits at the
rows it did before and a split statement is below the dialect's parameter
limit by the literal columns. Counting them would fit more rows per statement
in a batch of thousands; it is not done.

## Evidence

- `TestRenderInsertNullLiteral` (internal/engine/render_test.go): the text and
  argument count on each dialect, a value in one row only, and a nil in a
  column that is not nullable.
- `TestRenderCacheInsertNullShape`: three shapes that differ only in the
  literal columns, each rendered and then read from the cache.
- `TestJSONNullableNoPointer` (internal/engine/json_test.go): a nil `[]byte`
  is the literal alone in a row, and an untyped nil beside a value.
