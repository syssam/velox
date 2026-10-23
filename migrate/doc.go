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
//   - "nil": a schema Diff or Render needs is nil.
//   - "dialect": a schema's dialect is not PostgreSQL, MySQL or SQLite, or
//     two schemas' dialects differ.
//   - "schema": a schema lists a table, an enum type, or a table's column,
//     index or constraint twice.
//   - "primary-key": a table's primary key changes its columns, or gains or
//     loses its name, which Diff leaves to a hand-written migration.
//   - "rename": a rename names a table or column that is not there, or one
//     that is there already.
//   - "render": Render was given a change it cannot write: nil, not one Diff
//     gives for the dialect, about what to lacks, or an identity change
//     whose sequence from does not name.
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
//     PostgreSQL session with standard_conforming_strings off; or a file
//     the runner cannot run yet.
//   - "history": the history table holds what the runner cannot read, or
//     a file is pending below the highest applied version.
//
// A directory holds velox.json, schema.json, the migration files and
// velox.sum, which hashes the others. [ReadDir] reads one and returns a
// [*SumError] when velox.sum does not match; [Dir.Add] writes the next files
// of a plan.
//
// [Up] applies a directory's pending files to a database, under a lock, each
// in a transaction with its row in the history table; [Status] reads the
// directory against the history without applying. An applied file edited or
// gone is a [*ChecksumError], a version left part-way a [*DirtyError], and a
// lock held past the wait [ErrLockWait].
package migrate
