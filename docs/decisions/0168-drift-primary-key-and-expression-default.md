# 0168. Drift reports a primary key and an expression default as changes

- Status: accepted, 2026-11-24. Amends 0012.

## Context

Drift warned of a primary key that differed from the snapshot's and copied
the snapshot's into the comparison, as Diff refuses to change a key. It did
the same for two expression defaults that differed, though both parsed. So
`migrate drift` exited 0, and `migrate.Create`, hence `veloxtest.Open` on a
file, accepted a database whose key was dropped by hand, or whose
`DEFAULT CURRENT_TIMESTAMP` became `CURRENT_DATE`. Both differences are
fully known: 0012 makes such a difference a change.

## Decision

- A primary key that differs in its columns, or in having a name, is an
  `alter-primary-key` change, listed after Diff's. Diff never gives it, and
  Render refuses it: a key is changed by hand. Two names are a rename, as
  before. Baseline keeps the database's key, with a warning, as before.
- Two expression defaults that both parse and differ are left to Diff: an
  `alter-column (default)` change. One that cannot be parsed, with another
  text, stays a warning, and Baseline keeps the database's.
- A literal default and an expression that parse to one tree are one
  default: SQLite keeps `DEFAULT ('active')` as written.
- SQLite inspection reads a one-column INTEGER primary key, a rowid alias,
  as NOT NULL, as it holds no NULL without the words.

## Consequences

`now()` and `CURRENT_TIMESTAMP` on PostgreSQL are a change, though they
read alike; velox writes a default as the server prints it (0026), so only
a hand edit meets this. Inspection reads no primary key that it cannot
compare, so drift has no primary-key warning left.

## Evidence

- `TestDriftPrimaryKey`, `TestDriftDefaultExpression` (migrate/drift_test.go).
- `TestDriftSQLitePrimaryKey`, `TestDriftSQLiteExpressionDefault`,
  `TestDriftSQLiteSpelling` (migrate/drift_db_test.go).
- `TestMigrateDriftPrimaryKey` (cmd/velox-gen/migrate_test.go): exit 3.
