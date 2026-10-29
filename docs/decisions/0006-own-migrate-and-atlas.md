# 0006. velox's own migrate, and an Atlas provider

- Status: accepted, 2026-10-29

## Context

Old velox forked ent's `sql/schema` on Atlas. Three dialects and one model are
small enough to plan and apply in velox, which keeps its dependency rules,
deterministic output and safety checks. Teams that already run Atlas want to
keep it, and migrations are not velox2's focus.

## Decision

velox keeps its own planner and runner (`migrate`, `velox-gen migrate`). It
also ships an Atlas provider: `velox-gen migrate sql` prints the schema's
`CREATE` statements, the first file `migrate diff` would write for a new
directory, for Atlas's `external_schema`. velox's migrate and Atlas are
alternatives for one directory: a directory is managed by one of them.

## Consequences

velox owns the planner's code and its correctness. A team on Atlas plans,
lints and applies with Atlas, and does not mix in `velox-gen migrate apply`.

## Evidence

- migrate/doc.go, "# Atlas": the atlas.hcl and the one-directory rule.
- `TestMigrateSQL`, `TestMigrateSQLRefusals` and
  `TestMigrateSQLPositionsAreRelative` (cmd/velox-gen/migrate_sql_test.go).
- docs/specs/2026-10-23-migrate-design.md: decision 1 (:1276) and §18
  (:1179, :1208).
