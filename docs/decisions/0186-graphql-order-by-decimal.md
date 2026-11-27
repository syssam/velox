# 186. velox-graphql orders and pages by a decimal

Status: accepted, 2026-11-27.

## Context

nextapp keeps money in `decimal.Decimal`, an Other field with a numeric
`SchemaType`. It asked to sort a connection by such a field, such as
`orderBy: {field: AMOUNT}`. velox-graphql refused it twice: `orderfields=all`
dropped `graphql.OrderField` on an Other field with a warning, and gogen's
`termBase` refused type `other`.

velox itself already orders and pages by a decimal. `NumberCol` orders it,
and `Query.After` binds the value as the column's type. This is tested by
`TestDecimalKeysetPaging` on SQLite, PostgreSQL and MySQL: 9 sorts before 100
and -3 before both.

## Decision

- A cursor term of a struct type holds the value as its text. A struct
  qualifies when it implements `encoding.TextMarshaler` and `driver.Valuer`
  and its pointer implements `encoding.TextUnmarshaler`.
  `gqlcore.JSONValue` writes the text and `gqlcore.SetValue` reads it back
  through `UnmarshalText`. `Value` is what `After` binds to compare. A struct
  without all three is refused when the connection is made, and the error
  names what it lacks.
- gogen accepts an Other field by its Go kind: a struct as itself, a string,
  bool, integer or float kind as that kind. Any other kind is refused at
  generation.
- `orderfields=all` does not list an Other field by its type, since its
  order is the database's and velox cannot know it. It lists one when
  `graphql.OrderField` asks, and keeps the field when it does.
- The column must be numeric. On a text column the database orders `"100"`
  before `"9"`. velox cannot see the column type of a hand-written
  `SchemaType`, so this is documented on `graphql.OrderField` and not
  checked. SQLite is numeric when the decimal names no SchemaType for it
  (0148). SQLite keeps a numeric column as a float, so a decimal of more
  than about 15 digits is rounded when written (12345678901234.5678 and
  12345678901234.5679 both read back as 12345678901234.568): the cursor
  holds the stored value and the key breaks the tie, so paging stays exact
  over what is stored. Money belongs on PostgreSQL or MySQL.

## Consequences

- No generated code changes shape; generated code names a column's index
  only, and the runtime reads the Go type. An app that has `OrderField` on a
  decimal gets new order values in its SDL and `*_gql.go` when it
  regenerates.
