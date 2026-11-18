# 0073. DBDefault is SchemaDefault in one text

- Status: accepted, 2026-11-18.

## Context

A constant `Default` is already the column's `DEFAULT` (`Default("")` gives
`DEFAULT ''`, `Default(0)` gives `DEFAULT 0`), so most of the old velox's
`sqlschema.Default("...")` calls port as they are. What is left is a column
default with no Go default beside it. velox-gen refuses a `Default` on a number
that is not Nillable unless it is 0, as an unset struct field could not store 0;
so a column that is `NOT NULL DEFAULT 100` in a database has only
`SchemaDefault`, whose map takes the same text three times:
`map[string]string{field.Postgres: "100", field.MySQL: "100", field.SQLite: "100"}`.
In the three consumers of the old velox that is about six sites of thirty-seven,
all a number, `'[]'` or a time.

## Decision

`DBDefault(sql)` on a field sets `SchemaDefault` to `sql` for the three
dialects. It replaces what `SchemaDefault` set.

- It is for a number and a string only. Every other type has a default that is
  not one text: a bool is `true` or `1`, a time's `now()` is
  `CURRENT_TIMESTAMP(6)` on MySQL and velox's own form on SQLite, a JSON default
  is an expression on MySQL. The builder refuses those, naming `SchemaDefault`.
- A string field can be a text column on MySQL, whose default MySQL takes only
  as an expression: `DEFAULT ('x')`, not `DEFAULT 'x'`. velox-gen refuses a
  MySQL `SchemaDefault` that is a literal on such a column, whether it came
  from `DBDefault` or the map, and says to write it in parentheses. Without
  the refusal the migration fails when it is applied.

## Consequences

- It is the column's default only, as `SchemaDefault` is: velox's inserts
  write every column, and an Optional field is still not satisfied by it.
- An unrelated check is new: a hand-written MySQL `SchemaDefault` on a text,
  blob or JSON column that is not in parentheses is now refused in velox-gen,
  where MySQL refused it at apply.
- The generated code's shape does not change, so `GeneratedCodeIsVersionN`
  stays.

Enforced by `TestDBDefault` (`schema/field`) and
`internal/codegen/dbdefault_test.go`.
