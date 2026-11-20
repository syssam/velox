# 96. Handles are named by what the column holds

Status: accepted, 2026-11-20. Amends 0087 (the Other field's handle).

## Context

A time field's handle and an Other field's were both `velox.OrderedCol`
(`NullOrderedCol` when nullable). The user found the name unclear: it says
how the column sorts, not what it holds, and a reader of generated code
cannot tell a time from a decimal by it.

nextapp, the largest schema velox serves, shows what the kind holds in
practice: about 4,800 `OrderedCol` uses are time fields (2,438 plain, 2,350
nullable) and about 2,300 are `decimal.Decimal` Other fields (2,292 plain,
14 nullable), and nothing else. Every one of its decimals declares
`SchemaType{mysql: "decimal(p,s)", postgres: "numeric(p,s)"}`. A decimal is
a number, but as an `OrderedCol` it could not be summed or incremented.

## Decision

- A time field's handle is `TimeCol` (`NullTimeCol`).
- An Other field whose `SchemaType` names an exact-number type for every
  dialect it lists is a number: `NumberCol` (`NullNumberCol`), with `Sum`,
  `Avg`, `Add` and `Sub`, whatever its Go type. The exact-number types are
  `numeric` and `decimal`, with or without a precision, and on MySQL also
  `dec` and `fixed`: what velox-gen's default check already reads as a
  decimal (`decimalType`), after the same normalisation. velox has no
  registry of Go types; the column type is what the schema already says.
- Any other Other field (a range, a point, a uuid) has `OtherCol`
  (`NullOtherCol`), with the methods `OrderedCol` had. An Other field of a
  string kind outside a decimal column keeps `StringCol`, as before.
- `OrderedCol` and `NullOrderedCol` are removed, with no alias: nothing is
  released.
- velox's generated code is version 25.

## Consequences

- A decimal's `Add(v)` sends `col = col + ?` with v bound through its
  `driver.Valuer`, and `velox.Sum` scans the database's result through its
  `sql.Scanner`; `TestDecimalNumberCol` runs both on a text-backed decimal
  type.
- An Other field whose dialects disagree, a decimal on one and a float on
  another, is an `OtherCol`: arithmetic must work on every database the
  schema targets.
- `TimeCol` and `OtherCol` each cost what `OrderedCol` did in a package that
  uses them (14,578 and 14,674 bytes against 14,824; nullable 14,920 and
  15,002 against 15,166).
