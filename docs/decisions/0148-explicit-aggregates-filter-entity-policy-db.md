# 0148. An aggregate Calc is marked, a Filter reads its entity, a policy gets the DB

- Status: accepted, 2026-11-24. Supersedes the "aggregate in Calc's text"
  answer of [0142](0142-nextapp-projection-recipes.md) and
  [0137](0137-nextapp-port-helpers.md)'s `velox.HasColumn`; amends
  [0132](0132-filter-reads-through-the-statements-db.md)'s Filter
  signature.

## Context

A review asked for no hidden state and no guessing. It found three places
that fell short.

1. 0142 made a `Calc` an aggregate when its text called a known aggregate
   function: a name such as SUM or MAX followed by `(`. The scan misfired on
   SQLite's two-argument `MAX(a, b)` and `MIN(a, b)`, which are values of
   each row. It missed every aggregate it did not list: `PERCENTILE_CONT`,
   `MEDIAN`, a user's aggregate.
2. 0137's `velox.HasColumn(entity, column)` read a process-wide registry,
   filled when an entity's metadata was first built, under an RWMutex. It
   was false for an entity not yet used, answered by name, so two
   packages' entities of one name were one, and took a lock per call. A
   Filter was given only the entity's name, so it had nothing better.

## Decision

### 1. `Term.Agg()` marks an aggregate Calc

`func (t Term[E]) Agg[_ Lazy[E]]() Term[E]` marks an expression as an
aggregate, as `Decimal()` marks one as holding decimals (0131).
`untyped.Agg(t)` is the same for an untyped term. A marked term is placed
and refused as an aggregate Term is (`hasAgg`): MySQL orders a grouped
projection by it as written, not as `ANY_VALUE` of it; it cannot be inside
an aggregate, a joined term or a Rollup. velox does not read the text, so
`exprCallsAgg` and its list are gone. An unmarked `Calc("SUM(?)", x)` is a
value of each row to velox; the database refuses it where SQL does.

The mark is in the term's shape and in `sameTerm`. `Agg` of a term that is
not an expression, or of a window function, fails the statement.
`GroupBy` now refuses an aggregate key, marked or not, before anything is
sent. `SetExpr` takes text, not a term, so it no longer refuses aggregate
text itself; the database does (`TestCalcAgg`).

### 2. A Filter gets a `FilterEntity`

```go
type Filter func(ctx context.Context, db *DB, entity FilterEntity, op Op) (Predicate, error)
type FilterEntity = engine.FilterEntity
func (e FilterEntity) Name() string
func (e FilterEntity) HasColumn(column string) bool
```

`FilterEntity` is a value holding the entity's metadata. `Name` is what
the Filter was given before, the `TypeX` constant's value. `HasColumn`
looks the column up in that entity's own name index (`ColumnNamed`), as a
condition by name does: one map read, no lock, exact for that entity.
The zero value names nothing. `velox.HasColumn`, `engine.HasColumn`,
`registerInfo` and the registry are removed. `privacy.Filter`'s
`HasColumn` reads the same index (`TestFilterEntityHasColumn`). The
generated `TypeX` comment says the value is `FilterEntity.Name`'s; a
comment only, so no version change.

The name `FilterEntity` was chosen over `EntityInfo` and `Meta`, which
name other things, and over `Entity`, taken.

## Consequences

- A `Calc` that aggregates needs `Agg()`. Forgetting it on MySQL writes
  `ANY_VALUE(SUM(x))` in a grouped ordering, which MySQL refuses: a loud
  failure, not a wrong answer.
- A Filter compares `e.Name()` where it compared `entity`.
