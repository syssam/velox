# 144. A ValidationError names the schema field

- Status: accepted, 2026-11-23.

## Context

ent's `ValidationError` has `Name`, the schema field's name, such as
`"address_city"`. veloxe's had it too. nextapp's GraphQL error presenter
derives the input key from it: `customer_id` becomes `customerId`.

velox's `ValidationError` held `Entity`, `Field` (the Go name) and `Err`.
The Go name cannot be mapped back to the schema name: Go names apply
initialisms (`customer_id` is `CustomerID`), and schemas spell them
differently (`webURL`, `logoUrl`), so the mapping back is a guess.

The schema name is the column name unless the field has a `StorageKey`.
The metadata held the Go name and the column name only.

## Decision

- `ValidationError` gains `Name string`, the schema field's name, ent's
  name for it. `Error()` is unchanged: it still prints the Go name.
- `ColumnDescriptor` gains `SchemaName`. velox-gen writes it only for a
  field whose schema name is not its column, so generated output changes
  only for schemas that use `StorageKey`. An edge's implicit key field
  has its column as its name, and gets none.
- `ColumnInfo.SchemaName` is `ColumnDescriptor.SchemaName`, or the
  column name when that is `""`.
- Every `ValidationError` velox builds fills `Name`: a validator's
  refusal, a Required edge's nil key, which names its key field as the
  schema does, and a bidirectional edge's self-link.
- `TestValidationErrorName` checks a plain field, a field with a
  `StorageKey` and a Required edge's key; `TestRequiredEdge` and the
  bidi `refused` test check the other two paths.
- Generated code changes shape: `GeneratedCodeIsVersion30`.

## Consequences

- A presenter reads `ve.Name` as it read ent's, with no table of Go names.
- For a Required edge ent's `Name` is the edge's name; velox's is the key
  field's, which is the input key a mutation sends.
- `ColumnDescriptor` and `ColumnInfo` grow by one string each.
