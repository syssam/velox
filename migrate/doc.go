// Package migrate models a database's schema as a value, so that a program
// can compare two of them, read or write one as JSON, and plan the SQL that
// turns one into the other.
//
// A [Schema] is the database as one dialect sees it: every name is the
// database's name, and every type is spelled as the dialect spells it. The
// model is plain data. Nothing in this package opens a connection, and the
// functions that take a *Schema never write through it.
//
// Planning has four parts. [Diff] lists the changes from one schema to
// another, in the order they run. [Lint] says which of them need care: a
// review, or an allowance for a destructive change. [Blocked] is the
// destructive findings no [Allow] covers. [Render] writes the changes as
// scripts: on PostgreSQL, values added to enum types in a script of their
// own, the main script, and an online script of units for what must not
// hold a lock for long. [NewPlan] runs the four in one call, and refuses a
// plan with a destructive change not allowed with a [*BlockedError].
//
// The other refusals are [*Error] values, whose Code names the failure:
//
//   - "nil": a schema Diff, Render or Drift needs is nil, or Inspect was
//     given no database.
//   - "dialect": a schema's dialect is not PostgreSQL, MySQL or SQLite, or
//     two schemas' dialects differ, or Inspect was given a dialect it cannot
//     read yet.
//   - "schema": a schema lists a table, an enum type, or a table's column,
//     or a named index or constraint, twice.
//   - "primary-key": a table's primary key changes its columns, or gains or
//     loses its name, which Diff leaves to a hand-written migration.
//   - "rename": a rename names a table or column that is not there, or one
//     that is there already.
//   - "render": Render was given a change it cannot write: nil, not one Diff
//     gives for the dialect, about what to lacks, an identity change
//     whose sequence from does not name, or on MySQL a rename of a primary
//     key, which MySQL always names PRIMARY.
//   - "allow": ParseAllow was given an item it cannot read.
//   - "json": UnmarshalJSON was given malformed JSON.
//   - "version": UnmarshalJSON was given another version of the snapshot.
//   - "file": ParseFile was given a migration file it cannot read: a bad
//     name, an unterminated quote, comment or dollar quote, or a directive
//     that is unknown, malformed, repeated, misplaced or refused on its
//     dialect.
//   - "config": velox.json, or the Config NewDir was given, is missing or
//     invalid: another version, an unknown dialect or key, a key given twice
//     or null, a table that is not an identifier of at most 63 bytes, a lock
//     timeout that is not a duration or is negative.
//   - "directory": ReadDir was given a directory that does not hang
//     together: two files of one version, schema.json missing beside files,
//     present beside none, or of another dialect, or a file that cannot be
//     read; or Dir.Add was given a plan with no script, a schema of another
//     dialect, or a name outside a-z, 0-9 and _ or over 40 bytes.
//   - "session": the runner's connection does not answer as velox.json's
//     dialect, is below its minimum version (PostgreSQL 12, MySQL 8.0.19,
//     SQLite 3.35; MariaDB is refused), names no MySQL database, or is a
//     PostgreSQL session with standard_conforming_strings off; or a MySQL
//     session whose sql_mode holds NO_BACKSLASH_ESCAPES or ANSI_QUOTES when
//     a file to apply holds a literal the mode would read otherwise.
//   - "history": the history table holds what the runner cannot read, a
//     file is pending below the highest applied version, a dirty version
//     to resume runs in a transaction, or the version Resolve was given is
//     not dirty or not in the history.
//   - "resolution": Resolve was given neither ResolveApplied nor
//     ResolveUndone.
//
// Each [Change] has a String, its kind and the object it acts on in one
// line. [Inspect] reads a database's tables as a Schema, and [Drift] reads
// the tables a snapshot names and returns a [DriftReport], what tells the
// database from the snapshot: the changes that would bring the database to
// it, and [Warning]s for what differs only in spelling or what the model has
// no place for. Both read on one connection of the database and close it
// after. Up and Resolve run on one connection too, with the session in UTC
// on PostgreSQL and MySQL, so files run with the session in UTC: a time
// literal means one instant on any server, and Inspect reads it in UTC.
//
// A directory holds velox.json, schema.json, the migration files and
// velox.sum, which hashes the others. [ReadDir] reads one and returns a
// [*SumError] when velox.sum does not match; [Dir.Add] writes the next files
// of a plan.
//
// [Up] applies a directory's pending files to a database, under a lock. On
// PostgreSQL and SQLite each runs in a transaction with its row in the
// history table. A MySQL file, or a PostgreSQL no-transaction one, runs
// statement by statement, a unit at a time, and its row counts the
// statements of its completed units: one that fails leaves its version
// dirty, a [*DirtyError] that stops every later run until [WithResume]
// continues it from its first incomplete unit or [Resolve] marks it applied
// or undone. A run killed while a statement ran may have applied it, and on
// MySQL a cancelled statement may still have finished on the server, so
// check the failed statement before resuming or undoing. [Status] reads the
// directory against the history without applying. Each uses one connection
// of the database and closes it after, so nothing a file sets on its
// session reaches the application's other connections. An applied file
// edited or gone is a [*ChecksumError], and a lock held past the wait
// [ErrLockWait].
package migrate
