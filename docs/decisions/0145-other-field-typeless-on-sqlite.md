# 145. An Other field with no SQLite SchemaType is a typeless column

- Status: accepted, 2026-11-23.

## Context

The nextapp port has 1,153 `decimal.Decimal` fields declared with
`field.Other`. Their `SchemaType` names MySQL and PostgreSQL only, and some
sit in schema files that may not change. veloxe made such a field on SQLite
anyway: it wrote the column with no type name. velox-gen refused it with
"an Other field needs a SchemaType for SQLite", so the port's tests on
SQLite could not open.

SQLite takes a column with no type name. Its affinity is BLOB (also called
NONE): the value is stored as bound, with no conversion. The choices:

- `NUMERIC`: a decimal's text `"1.10"` is stored as the REAL 1.1, and the
  trailing zero is lost. ADR 0126 accepts that for a field that names
  `numeric` itself. It is not a default for a field that named nothing.
- `TEXT`: a number bound as a number would be stored as text.
- No type: the text stays as written, and a number stays a number.

## Decision

- On SQLite, an Other field with no SchemaType for SQLite has the column
  type `""`. velox-gen's `Tables` no longer refuses it. MySQL and
  PostgreSQL keep the refusal, naming the dialect.
- The SQLite renderer writes such a column as `"col" [NOT NULL ...]`, with
  no type and no extra space, in `CREATE TABLE` and `ADD COLUMN` alike.
- Inspect already reads it back as `""` (`PRAGMA table_info` gives an empty
  type), so a database made by `Create` or by a plan has no drift and the
  next plan is empty. A change between `""` and a named type is an
  `alter-type` finding, which SQLite rebuilds the table for; its message
  says "from no type", not "from  to".
- `migrate sql`, `migrate diff` and `veloxtest.Open` need no change: they
  all go through `Tables`.
- A SchemaType for SQLite still wins: `numeric`, `text` or any other name.

## Consequences

- A decimal's text round-trips exactly on SQLite: `"1.10"` reads back as
  `"1.10"`.
- A typeless column has no affinity, so SQLite compares and orders its
  values as they are stored, and a decimal bound as text is TEXT. `ORDER BY`
  and `>` on such a column compare as text (`"10.00"` sorts before
  `"9.00"`). `SUM` and `AVG` convert text to numbers, so aggregates work and
  keep ADR 0126's rounding. A test that orders or range-filters decimals on
  SQLite should name `numeric` for SQLite in the field's SchemaType.
- MySQL and PostgreSQL behaviour is unchanged.
- Generated code does not change.
