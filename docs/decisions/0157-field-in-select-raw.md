# 0157. FieldInSelectRaw: a subquery by name with no policy inside

- Status: accepted, 2026-11-24. Amends 0079 and 0091.
- Amended 2026-11-24 by [0161](0161-text-column-integer-key.md): a text
  column against an integer key keeps the key bare and casts the column,
  behind a guard, so the key's index serves; the rows are the same.

## Context

An application's activity trail scopes its rows by the entity they name:
`activity_log.entity_id IN (SELECT CAST(lead_id AS TEXT) FROM lead WHERE
<conditions>)`. The conditions are `velox.Predicate` values built by name
(`FieldIn`, `FieldEQ`, `untyped.Or`) in code that does not know the inner
entity's type. Nothing else may apply inside the subquery: no Read policy,
filter, interceptor or soft delete, so a deleted lead's history stays
readable. `entity_id` is text and `lead_id` an integer key.

`FieldInSelect` (0079) names the entity, but applies its policies inside, as
it must for every other use. It refused a text column against an integer
key: 0091 allowed only the other way round. `ExprRaw` (0155) can say the
subquery, but takes SQL text and typed handles, not by-name predicates whose
entity is not known.

## Decision

- `velox.FieldInSelectRaw(reason, column, entity, key string, ps
  ...Predicate) Predicate`, beside `FieldInSelect`, named as `ExprRaw` pairs
  with `Expr`, the reason first.
- It resolves as `FieldInSelect` does: the column on the statement's
  entity, the entity in the package's entity table, the key on it, and `ps`
  against it, when the statement is prepared. An unknown name or two
  columns that do not compare fail the statement.
- Inside its `EXISTS` only `ps` apply: the scope adds none of the target's
  Read conditions, so no Read policy, `WithFilter` filter or interceptor,
  soft delete included. A relation or `FieldInSelect` among `ps` keeps its
  own target's conditions, which fails closed. The statement around it is
  scoped as always. It works in `Or`, `And` and `Not`, and in an update's
  and a delete's `Where`.
- A blank reason fails the statement. `Statement.RawReasons()` reports the
  reason, before the condition is resolved and after, in the order written
  among `ExprRaw`'s. The resolved link carries it (`Link.Raw`); the render
  cache keys neither it nor the values, and the SQL of a raw and a plain
  subquery differ only by the conditions the scope adds.
- `FieldInSelect` and `FieldInSelectRaw` compare a text column with an
  integer key as text, the key cast inside the subquery: `CAST(k AS TEXT)`
  on PostgreSQL and SQLite, `CONCAT(k)` on MySQL, whose text takes the
  column's collation where `CAST(k AS CHAR)` can fail with error 1267
  (0155's finding). 0091's direction, an integer column against a text key,
  still casts the outer column. The correlation is written as
  `CAST("vx1"."lead_id" AS TEXT) = "activity_log"."entity_id"`, so an index
  on the key does not serve the correlation.
- velox-gen reads `FieldInSelectRaw` after its reason as it reads
  `FieldInSelect`, and accepts both directions of integer and text.

## Rejected

- An option on `FieldInSelect` to drop the policies: the reason would be
  optional, and a reader of a policy would not see the bypass in the name.
- `ExprRaw` with by-name predicates: its text is SQL of the outer entity's
  handles; mixing in conditions on an unknown entity needs the resolution
  `FieldInSelect` already does.
- Dropping the policies of nested relations among `ps` too: `ps` are the
  caller's conditions, and a relation in them asks for its target's rows as
  any relation does.

## Consequences

- A `FieldInSelectRaw` can match rows of the inner entity the caller may not
  read. The reason is required, and an Around hook can record it.
- `PolicyBypass` is not set: the outer statement's policies still apply.

## Evidence

`internal/apitest/fieldinselectraw_test.go`: `TestFieldInSelectRawSkipsInner`
(no Read policy, scope or filter inside; Or, And, Not; update and delete),
`TestFieldInSelectRawRows` (INote's interceptor and Read policy on SQLite),
`TestFieldInSelectRawReasons` (an Around hook, a blank reason),
`TestFieldInSelectRawRenderCache`. `internal/engine/named_sub_test.go`:
`TestFieldInSelectTextColumn` (the cast per dialect),
`TestFieldInSelectRefusals`. `internal/codegen/behaviourcheck_sub_test.go`.
`internal/integration/fieldinselectraw_test.go`: `TestFieldInSelectRaw`, an
interceptor, a Read policy, soft delete and another org on SQLite, and on
PostgreSQL and MySQL when their variables are set.
