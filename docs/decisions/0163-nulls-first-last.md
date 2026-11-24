# 0163. An ordering says where NULL goes: NullsFirst and NullsLast

- Status: accepted, 2026-11-24. Adds to OrderBy and After; amends
  nothing.

## Context

velox sorts NULL after every value ascending and before them descending,
as PostgreSQL does, on every dialect: MySQL and SQLite sort NULL first
ascending, so velox writes `x IS NULL` before `x` there. After's keyset
condition counts on that place. ent has `sql.OrderNullsFirst()` and
`sql.OrderNullsLast()`, and a list of tasks by due date wants the undated
ones last in either direction, which the one place cannot say.

## Decision

- `Order[E]` has `NullsFirst()` and `NullsLast()`, methods with a `Lazy`
  type parameter (0055), so `T.DueAt.Desc().NullsLast()` works with no
  change to the handles or the generated code. The later call wins.
- `OrderTerm` has `Nulls uint8`: 0 is the default place, `NullsFirst` (1)
  and `NullsLast` (2) the constants in `veloxtype`, re-exported by velox.
  Any other value fails the statement. The field sits in the padding after
  `Desc`, so `OrderTerm` stays 32 bytes.
- PostgreSQL and SQLite write `x [DESC] NULLS FIRST|LAST`. SQLite has the
  clause since 3.30.0; the modernc driver ships a newer one. MySQL writes
  `x IS NULL DESC, x [DESC]` for NULLs first and `x IS NULL, x [DESC]` for
  NULLs last. An expression is written twice there, each copy binding its
  own values, as the default's `IS NULL` term already does, ANY_VALUE
  under GROUP BY and DISTINCT included.
- An ordering without Nulls writes the SQL it wrote before, byte for byte;
  SQLite keeps its `IS NULL` term for it.
- A column that cannot be NULL takes no clause. A left join's column, an
  expression and a relation's nullable aggregate can be NULL and take it;
  a relation's count cannot.
- The render cache keys on Nulls.
- After pages a Nulls ordering exactly. Where NULL sorts last, past a value
  is `> v OR IS NULL` and past NULL is nothing; where it sorts first, past
  a value is `> v` and past NULL is `IS NOT NULL` (`<` descending). A
  relation's aggregate is compared the same way with its bound copy.
- A GraphQL connection builds its own order and refuses an ordered query,
  so it never meets a Nulls term; its order keeps the one place.

## Rejected

- A type `NullOrder` for the field: about 90 bytes more in every entity
  package that names `Order` (`TestGenericTypesCompileSmall`).
- `OrderTerm.NullsFirst()` and `NullsLast()` methods: about 200 bytes more
  in every such package, over the bound. An untyped caller sets `Nulls`.
- Refusing a Nulls ordering in After: the condition is a small change of
  the default's.
- The emulation on SQLite as on MySQL: the native clause is shorter, and
  the default's SQL does not change either way.

## Consequences

- Entity packages grow by about 616 bytes, the two methods' declarations
  in `Order[E]`'s method set: entityDescMixin from 927064 to 927682, under
  its bound of 927744. `Order` grows by 6 bytes, `Query` not at all.
- No generated-code change.
