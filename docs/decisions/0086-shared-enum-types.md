# 0086. Enum fields whose types get one name and the same values share the type

- Status: accepted, 2026-11-19.

## Context

velox-gen names an enum field's Go type `<Schema><Field>`, and velox-graphql
names the GraphQL enum after it. `Asset.depreciation_method` and
`AssetDepreciation.method` both make `AssetDepreciationMethod`, and
`Budget.period_type` and `BudgetPeriod.type` both make `BudgetPeriodType`.
velox-gen refused the second as a collision.

veloxe declared one enum per name, and typed both fields with it. The largest
consumer's published GraphQL API has one `AssetDepreciationMethod` and one
`BudgetPeriodType`, the latter with Budget's order of values. Renaming either
would change that API.

## Decision

- When two enum fields make one Go type name and hold the same values (by
  constant and stored value, in any order), they share one type. The first
  schema by name declares it, with its order of values. With other values,
  the names still collide and are refused.
- velox-graphql declares the GraphQL enum once, in the Go type's order. The
  second field's `sdl.Enum` is `Shared`, so its marshalers are not written
  again, and they match each constant to its stored value, not to the
  field's position. Two fields of one type whose `graphql.EnumValues` name a
  value differently are refused, since they are one GraphQL enum.

## Consequences

- Schemas like these port without a rename, and their GraphQL API keeps
  veloxe's enums.
- A field's `plugin.Field.EnumValues` stays in its own declared order, and
  `plugin.Enum.Values` gives the shared type's order.
