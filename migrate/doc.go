// Package migrate models a database's schema as a value, so that a program
// can compare two of them, read or write one as JSON, and plan the SQL that
// turns one into the other.
//
// A [Schema] is the database as one dialect sees it: every name is the
// database's name, and every type is spelled as the dialect spells it. The
// model is plain data, and the functions that take a *Schema never write
// through it. A MySQL enum, and a PostgreSQL enum type, sort by the order
// of their values, not by their text.
//
// [Diff], [Lint], [Render] and [NewPlan] are pure: they read schemas and
// open no connection. [Up], [Status], [Resolve], [Inspect], [Drift] and
// [Baseline] take a *sql.DB and use one connection of it, with the session
// in UTC on PostgreSQL and MySQL, and close that connection after rather
// than return it to the pool; when a file run statement by statement fails
// and that connection is lost, Up writes the error to its dirty row, and
// reads the row's count, through another connection of the pool. [Verify]
// uses three such connections in turn: one to check the database is empty,
// then Up's and Drift's.
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
//   - "nil": a schema Diff, Render, Drift or Baseline needs is nil, or Up,
//     Status, Resolve, Inspect, Drift, Verify or Baseline was given no
//     database.
//   - "dialect": a schema's dialect is not PostgreSQL, MySQL or SQLite, or
//     two schemas' dialects differ, or Inspect or ParseFile was given
//     another dialect.
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
//     name, an unterminated quote, comment or dollar quote, a directive
//     that is unknown, malformed, repeated, misplaced or refused on its
//     dialect, or on SQLite or MySQL a trigger, a MySQL routine or event, or
//     a BEGIN … END block, or on PostgreSQL a BEGIN ATOMIC body or a psql
//     meta-command line; or LintFile found a statement that cannot run in
//     the file's transaction, or Up, before its run, one that begins or
//     ends a transaction in a file run in one.
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
//     dialect, is below its minimum version (PostgreSQL 14, MySQL 8.4,
//     SQLite 3.35; MariaDB is refused), names no MySQL database, or is a
//     PostgreSQL session with standard_conforming_strings off; or a MySQL
//     session whose sql_mode holds NO_BACKSLASH_ESCAPES or ANSI_QUOTES when
//     a file to apply holds a literal the mode would read otherwise.
//   - "history": the history table holds what the runner cannot read, a
//     file is pending below the highest applied version, a dirty version
//     to resume runs in a transaction or has versions above the
//     directory's last file beside it, or the version Resolve was given is
//     not dirty or not in the history.
//   - "resolution": Resolve was given neither ResolveApplied nor
//     ResolveUndone.
//   - "drift": Verify was given a database that is not empty, or the
//     database differs from schema.json after the directory's files ran.
//   - "baseline": Baseline found none of the schema's tables, a foreign key
//     to a table outside them, or a sequence default the column does not
//     own; or Up was given WithBaseline where it cannot record.
//
// On PostgreSQL 18, PostgreSQL names the NOT NULL constraints; migrate
// neither models nor renames them, and after a table rename they keep the
// old table's name.
//
// Each [Change] has a String, its kind and the object it acts on in one
// line. [Inspect] reads a database's tables as a Schema, and [Drift] reads
// the tables a snapshot names and returns a [DriftReport], what tells the
// database from the snapshot: the changes that would bring the database to
// it, and [Warning]s. A predicate or check the database prints its own way
// is no difference: Drift compares the two by their parsed form, where a
// cast the database adds itself without changing the value matches no
// cast, and a NOT over a comparison matches its negated operator. One whose
// meaning differs is a change, so a hand-edited check is found. MySQL's
// _utf8mb4 and _utf8mb3 before a literal, and _latin1 and _ascii before an
// ASCII one, are dropped, so two literals compared with each other can be
// called the same where their collations differ on trailing spaces. A
// MySQL binary or varbinary default is compared by its bytes. A Warning is
// what the comparison cannot settle: an expression default that differs, a
// predicate the normaliser cannot read whose text differs, a primary key
// that differs, or what the model has no place for, such as a MySQL table
// that is not InnoDB, an invalid PostgreSQL index or a NOT VALID
// constraint.
//
// [Baseline] adopts an existing database into a new directory: it reads
// the tables a schema names and returns them as the directory's first
// snapshot, each object in the schema's spelling where the comparison of
// Drift finds it equal and in the database's otherwise, with warnings for
// what a build from the snapshot loses. It refuses a database that holds
// none of the schema's tables, a foreign key to a table outside them, and
// a sequence default the column does not own.
//
// A directory holds velox.json, schema.json, the migration files and
// velox.sum, which hashes the others. Line endings never change a sum: each
// file, velox.sum too, is read with CRLF as LF. [ReadDir] reads one and
// returns a [*SumError] when velox.sum does not match; [Dir.Add] writes the
// next files of a plan. [LintFile] checks a file as velox-gen migrate lint
// does: what cannot run in its transaction, and, in a file not written by
// Dir.Add, the destructive statements it can read, with the codes
// delete-rows and update-rows beside Lint's for TRUNCATE, and DELETE or
// UPDATE without WHERE.
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
// check the failed statement before resuming or undoing: resuming runs it
// again. [Status] reads the directory against the history without
// applying. Files run with the session in UTC on PostgreSQL and MySQL, so
// a time literal means one instant on any server; each file starts with
// the session as Up was given it (on MySQL, in the database it was given
// too), and nothing a file sets on its session reaches the application's
// other connections. The history table is named by the schema (PostgreSQL)
// or database (MySQL) Up was given, so a file that changes search_path or
// USEs another database still records itself. A file resumed with
// WithResume (velox-gen apply -resume) starts again with the session as Up
// was given it, so its statements after a SET search_path or a USE run
// without them; such a file sets them again at the top of each unit. A trigger, a MySQL routine or event and
// a BEGIN … END block are refused on SQLite and MySQL, whose bodies the
// splitter would cut at their inner semicolons; a BEGIN ATOMIC body and a
// psql meta-command line, one that starts with a backslash, are refused on
// PostgreSQL. An applied file edited or
// gone is a [*ChecksumError], and a lock held past the wait
// [ErrLockWait]. [Verify] applies a directory to an empty database and
// fails when Drift then finds a change against schema.json: a test's check
// that the files make what the snapshot says. On SQLite, Verify, Up and
// Drift need a file database, since each run opens a connection of its own
// and a connection to :memory: is a new, empty database.
//
// # Adopting a database
//
// A database made before the directory, by ent, by old velox or by hand,
// is adopted in five steps. In a program, [Baseline] is step 1 and
// [WithBaseline] step 3:
//
//  1. Run velox-gen migrate baseline -dialect d -db <dsn> against one
//     environment. It writes a new directory whose 0001_baseline.sql builds
//     the schema's tables as that database holds them. Read its warnings:
//     an extension to add to 0001_baseline.sql by hand, a sequence's start
//     or increment it does not keep, a PostgreSQL domain type, which it does
//     not create. A file made with pg_dump instead needs its lines that start
//     with a backslash removed first (pg_dump 18's \restrict and
//     \unrestrict): they are psql meta-commands, not SQL, and ParseFile
//     refuses them.
//  2. 0001_baseline.sql may be edited, followed by velox-gen migrate hash,
//     only before any environment runs apply -baseline. After that, an edit
//     is a [*ChecksumError] on every baselined database.
//  3. Commit the directory, and run velox-gen migrate apply -baseline 1
//     once on each existing environment. It records the file as applied
//     without running it, and refuses a history that is not empty and a
//     database with no tables besides the history.
//  4. Run velox-gen migrate drift on each environment, since baseline read
//     only one.
//  5. Run velox-gen migrate diff as usual. An ent database's names come
//     back as renames, and its varchar enums as alter-type changes, which
//     are destructive and need -allow.
//
// # Atlas
//
// A team that uses Atlas can plan and apply with it instead: velox-gen
// migrate sql prints the schema package's CREATE statements, the first file
// migrate diff would write for a new directory, as plain DDL for Atlas's
// external_schema. Add velox-gen to the module with go get -tool
// github.com/syssam/velox2/cmd/velox-gen, and run atlas from the module root
// with this atlas.hcl:
//
//	data "external_schema" "velox" {
//	  program = ["go", "tool", "velox-gen", "migrate", "sql", "-dialect", "postgres", "./schema"]
//	}
//
//	env "velox" {
//	  src = data.external_schema.velox.url
//	  dev = "docker://postgres/16/dev?search_path=public"
//	  migration { dir = "file://migrations" }
//	}
//
// atlas migrate diff --env velox then writes a migration, and atlas migrate
// apply --env velox -u <url> applies it. For a schema that keeps tables in
// PostgreSQL schemas (schema.TableIn), use docker://postgres/16/dev, which
// is database-scoped. For MySQL use docker://mysql/8.4/dev with -dialect
// mysql, and for SQLite sqlite://dev?mode=memory with -dialect sqlite.
// velox's migrate and Atlas are alternatives for one directory: a directory
// is managed by one of them.
package migrate
