# 112. A foreign key no index leads is a review finding

Status: accepted, 2026-11-22.

Amends [0090](0090-veloxe-defaults.md).

## Context

velox-gen declares no index of a foreign key on PostgreSQL and SQLite, as
ent and veloxe declare none (0090's amendment of 2026-11-19). Every foreign
key column is read by a key: `With` loads a relation with
`WHERE fk IN (...)`, `Has` and `HasWith` are `EXISTS` on it, a join matches
on it, and a delete of a referenced row makes the database find the rows
that point at it, for `ON DELETE` and for the check. Without an index each
of those scans the table. MySQL makes an index for a key that has none.
ent leaves this to the developer to notice.

A velox-gen warning on each generate was weighed: it repeats for every key
on every run, with no way to say that a key is meant to stay unindexed,
and an adopted veloxe schema has hundreds. `migrate diff` already sorts
what a change needs into findings, writes the review ones above the
statement in the file a person reads, and has `-allow`.

## Decision

- `migrate`'s lint gives a **review** finding of code `unindexed-fk`,
  target `<table>.<column>`, when a change adds a foreign key, by a new
  table or by adding one to a table, or drops the last index that led one,
  and once the plan has run no full index and no primary key of the
  table has the key's first column first. A partial index does not count:
  it does not serve the database's check on delete. A key added again in
  place of one on the same column, as for a new `ON DELETE`, is not new.
  MySQL gets none.
- `-allow unindexed-fk=<table>.<column>`, or `-allow unindexed-fk` for
  every key, takes the finding out of the plan and its review line. It is
  the one review code `ParseAllow` takes; it writes no allow directive,
  since `Up` has nothing to check.
- Each change gets one finding of each code but `unindexed-fk`, which gets
  one per column.
- velox-gen still declares no index (0090 stands): the fix is the
  schema's `index.Fields("author_id")`, which the next `diff` plans.
- `migrate.Lint`, which has no target schema, finds the keys of tables the
  plan creates only. `NewPlan` and `Render` find them all.

## Consequences

- A new directory's first file names each key with no index. The examples'
  files were written before; their next `diff` names the keys it adds.
- A join table of a plain many-to-many relation is keyed by its two
  columns, so its second column is named: deleting a target scans it.

## Evidence

- `TestUnindexedForeignKey`, `TestUnindexedForeignKeyLint` and
  `TestParseAllowUnindexedFK` (migrate/unindexed_test.go).
- The lint and render goldens in migrate/testdata: `create-tables` on
  PostgreSQL and SQLite, `fk-index-restored`, `drop-column-in-index`,
  `add-foreign-key`; no MySQL golden changed.
