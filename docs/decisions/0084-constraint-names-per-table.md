# 0084. Check and not-null names are compared per table

- Status: accepted, 2026-11-19. Narrows rule 44.

## Context

velox-gen names an enum field's check `<table>_<column>_check` and the
helper of a NOT NULL change `<table>_<column>_not_null`. Rule 44 compared
them with every constraint in the database. So table `asset`'s column
`depreciation_method` and table `asset_depreciation`'s column `method`, both
`asset_depreciation_method_check`, were refused, and the largest consumer
has several such pairs.

Databases do not keep these names that way. PostgreSQL keys `pg_constraint`
by table and name, so two tables may each have a constraint of one name. A
table's checks and foreign keys still share that space. SQLite does not
compare constraint names. MySQL keeps check and foreign-key names in one
space per database. Not-null checks are PostgreSQL's alone.

## Decision

- Build compares checks, declared or an enum's, and not-null checks with the
  other constraints of their own table, foreign keys included. Foreign keys
  are still compared with each other across the database, as MySQL needs.
- On MySQL, `Tables` refuses a check whose name another table of the database
  has as a check or a foreign key.

## Consequences

- A schema that PostgreSQL and SQLite accept is no longer refused for a name
  only MySQL would reject, and MySQL's refusal comes from MySQL's `Tables`.
- A MySQL enum without a string `SchemaType` is a native `ENUM`, which has no
  check, so it has no name to clash.
