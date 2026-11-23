# 148. A decimal Other field with no SQLite SchemaType is numeric on SQLite

- Status: accepted, 2026-11-24. Supersedes 0145.

## Context

Decision 0145 made an Other field with no SQLite SchemaType a typeless
column on SQLite, with BLOB affinity, so a decimal's text stayed as written.
It said that such a column orders and compares as text and told a test to
name `numeric` for SQLite.

That is a bug, not a trade. A `decimal.Decimal` is a Valuer of text. On a
typeless column the values are stored as TEXT, and SQLite compares TEXT with
TEXT. With 9.5, 10.25 and 100, `ORDER BY amount` gave 10.25, 100, 9.5 and
`amount > 20` (bound as text `'20'`) matched 9.5 only. Range predicates,
ordering, `Min` and `Max` of a decimal field were wrong, with no error. The
nextapp port has 1,153 such fields and no way to rename them.

## Decision

- On SQLite, an Other field with no SQLite SchemaType whose Go type is held
  in a decimal column has the column type `numeric`, the affinity ADR 0126
  gives a decimal column. A decimal column is the one `decimalField` reads:
  the field names a SchemaType for MySQL or PostgreSQL (or both), and each
  one it names is an exact-number type (`numeric`, `decimal`, and on MySQL
  `dec` and `fixed`). No Go type is looked at. The field's handle is a
  NumberCol already, from the same test.
- Any other Other field with no SQLite SchemaType is refused again: "an
  Other field needs a SchemaType for SQLite". So is one that names a
  non-decimal type for MySQL or PostgreSQL only.
- A SchemaType for SQLite still wins.
- The typeless column goes: the SQLite renderer and the linter return to
  writing and naming `c.Type` as is.

## Consequences

- A decimal orders, compares and ranges as a number on SQLite. The text of
  `"1.10"` is stored as the number 1.1, as ADR 0126 says of any decimal
  column on SQLite.
- Generated code does not change.
- Tests: `TestOpenOtherDecimalWithoutSQLiteType` (veloxtest) failed before
  this change; `TestTablesOtherWithoutSQLiteType` (internal/codegen) and
  `TestMigrateSQLOtherWithoutSQLiteType` (cmd/velox-gen) check the column
  type and the refusal.
