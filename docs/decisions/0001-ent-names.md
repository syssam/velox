# 0001. Names follow ent's

- Status: accepted, 2026-10-29

## Context

Teams adopt velox2 from ent with a live database. A table, column, index or
foreign key named apart from ent's turns the first `migrate diff` into renames
on production data. The triage found velox2's inflection and index names
differed (C21, C71, C72, C77).

## Decision

Tables, columns, indexes, unique and foreign-key constraints, and inflection
follow ent's. A table is the snake_case of `Plural` of the entity's Go name, as
ent's `Type.Table()`. A declared index is `<lower(type)>_<cols>`. Constraint
names are the dialect's for an ent column (PostgreSQL `<table>_<col>_key`), and
foreign keys are `<table>_<ref table>_<edge>`, capped as ent caps them. The
foreign-key indexes velox adds keep their own names.

## Consequences

An ent database baselines without renames. A name may differ from the one an
earlier velox2 wrote, so its next `migrate diff` must be read before it is
applied (CHANGELOG, "Schema and migrations"). The names are frozen: see 0002.

## Evidence

- `TestEntNames` (plugin/names_test.go:75) checks the helpers against
  plugin/testdata/ent.golden, names ent's own integration code generates.
- `TestMigrateBaselineEnt` (cmd/velox-gen/migrate_baseline_test.go:92):
  baseline, then the next diff renames nothing.
- Commits 1d216d8 (inflection) and 25e9e88 (index, unique and foreign-key
  names).
- docs/specs/2026-10-25-pre-1.0-hardening-design.md: rulings 1–2 (:33-34) and
  builder rulings 1 and 4 (:150, :153).
