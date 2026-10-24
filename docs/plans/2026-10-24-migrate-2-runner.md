# Migrate Plan 2: Directory, Runner, Inspect, CLI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A migrations directory that velox writes, checks and applies on PostgreSQL, MySQL and SQLite:
- `migrate` reads and writes the directory (`velox.json`, `velox.sum`, `schema.json`, the SQL files and their directives) and splits its statements;
- `Up`, `Status`, `Resolve` and `Verify` apply it under a lock, one transaction per file where the dialect allows it, with a dirty state, unit resume and ruling 54's version rules;
- `Inspect` reads a database into the model on three dialects, and `Drift` reads a database and compares it with a snapshot through the predicate normaliser;
- `velox-gen migrate diff|apply|status|drift|lint|hash|resolve`;
- itests on real databases for every change kind, with the three invariants, rows, the targeted itests and the binding round-trips.

**Architecture:** Everything but the CLI is in package `migrate`, which stays driver-free: the runner and `Inspect` take a `*sql.DB` and speak each dialect through `database/sql` only, reading driver codes through the root package's `velox.DriverCode`. The directory is plain files read through `fs.FS`; `Dir.Add` writes a plan's scripts with plan 1's `writeScript`, and `ParseFile` reads them back with the statement splitter, so the two are tested as one round trip. The runner holds one `*sql.Conn` for a run: the lock, the session checks, the history and every statement go through it. The model stays plain data: an unexported `inspect` returns the schema and, beside it, notes on what the model has no place for (a MySQL engine, an invalid PostgreSQL index). `Inspect` returns the schema alone; `Drift(ctx, db, snapshot)` inspects the snapshot's tables and passes schema and notes to the unexported `drift`, which turns the notes and the residual spellings into warnings and everything else into `Diff`'s changes. Comparing two schemas in memory is `Diff`'s job, so there is no schema-to-schema `Drift`. velox-gen links the three drivers already in the root module. The itests live in `migrate`'s own tests (package `migrate_test`, reusing plan 1's `cases`) and in `internal/itest` for the binding round-trips.

**Tech Stack:** Go 1.27; `database/sql`, `io/fs`, `testing/fstest`, `log/slog`, `crypto/sha256`. Drivers in tests and velox-gen only: pgx v5 (`pgx` and `pgxpool`), go-sql-driver/mysql, modernc SQLite. PostgreSQL 16 and MySQL 8.4 from `docker-compose.ci.yml`.

**Spec:** `docs/specs/2026-10-23-migrate-design.md`: §9, §10, §11, §13, §14 (the directory, Inspect, runner and Drift blocks; `Drift`'s signature changed by ruling 20), §16, §17 (Fuzz, the real-database itests, the targeted itests, the predicate goldens, binding, the runner, the CLI), Plans item 2, rulings 45–55, and "Rulings made in plan 1" (binding facts about the code this plan builds on). Follow-ups: `docs/plans/2026-10-14-core-1-followups.md`, "Left by migrate plan 1" (every bullet lands in a task, named in its step). Where this plan's rulings differ from the spec, the rulings stand (see "Rulings this plan makes").

## Global Constraints

- **Dependency discipline.** "The root module's dependencies stay pgx, the MySQL driver and SQLite. `migrate` adds none." `go.mod` does not change.
- **`migrate` imports** the standard library, `github.com/syssam/velox2` and `internal/ddl`, nothing else (`TestImports` pins it). No driver in a non-test file of `migrate`; `cmd/velox-gen` and test files import the drivers.
- **The model is a value.** "`Diff`, `Lint`, `Render`, `Drift` and `Inspect` never write through a `*Schema` they are given; planning copies." `drift` and `Drift` get `DoesNotMutate` tests.
- **Deterministic output.** "Every list in the model, the plan and the files is sorted by a stated key." The same schema and directory give the same bytes; `Add`, `Sum`, `Inspect`, `Drift` and the CLI's printed lists each have a test that runs them twice.
- **The core is untouched.** No file of the root package changes. `GeneratedCodeIsVersion4` stays 4; `go test -run Budget -v .` and the escape check of plan 1's Global Constraints print the same as before every task.
- **Errors.** `migrate`'s start with `migrate: `; each `*Error` carries a `Code` the package doc lists (each task adds its codes to `doc.go`). velox-gen prints `velox-gen: migrate <subcommand>: <text without "migrate: ">`. Messages carry no rule numbers.
- **Exit codes** are velox-gen's: "0 success, 1 error, 2 misuse, 3 stale or drift", as §13's table assigns them.
- **godoc.** `TestGodocCitesNoSpec` scans `migrate` and `cmd/velox-gen`: no comment or message cites a spec, a section or a ruling.
- **Databases in tests.** The itests' variables are:
  - `VELOX_TEST_POSTGRES`: PostgreSQL 16; tests make a database of their own beside it (`pgDatabase`);
  - `VELOX_TEST_MYSQL_ADMIN`: MySQL 8.4's root DSN; `migrate`'s and `cmd/velox-gen`'s tests make a database of their own through it;
  - `VELOX_TEST_MYSQL`: MySQL 8.4 as the `velox` user, read by `internal/itest` (the binding round-trips, Task 13);
  - `CI`: `true` on a CI runner.

  SQLite needs none: a file in `t.TempDir()`, or memory where one connection serves. Every database test skips cleanly, with `t.Skip` naming the variable, when its variable is unset. Under `CI=true` a missing variable fails the package instead: a `TestMain` in each package with database tests checks them before `m.Run` (`migrate`: Task 3; `cmd/velox-gen`: Task 10; `internal/itest` has its own). `scripts/ci-local.sh` and `.github/workflows/ci.yml` set all four today; every task keeps them set in both, and a task that adds a variable adds it to both. Database tests run serially (no `t.Parallel`): the machine is short of memory.
- **The full gate passes at every task's commit,** run on the committed tree: `bash scripts/ci-local.sh`, rc=0, with Docker. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` stay identical in what they run; a task that changes one changes both (Task 1: `FuzzSplit`; Task 13: `internal/bindmodel`'s `-check` and `verify`).
- **No machine-specific paths** in committed files.
- **Shell:** before any go command, export `GOCACHE` set to the shared cache the full gate uses, and `GOFLAGS=-p=2`. One heavy command at a time. Do not kill other processes.
- **Test packages** (ruling 36). Every test this plan adds is in package `migrate_test`. The unexported code a test needs is reached through a hook in `migrate/export_test.go`, which the task that owns the code adds: `Split` (Task 1), `FileBackslash` and `FileGenerated` (Task 1), `LockName`, `LockKey` and `CheckVersion` (Task 3), `NormalizePredicate` and `DriftOf` with its `Note` (Task 5), `InspectNotes` (Task 6), `ParseCreateTable` (Task 8). The `-update` flag stays declared once, in `migrate/lint_test.go` (package `migrate_test`); no file declares it again.
- **Goldens of this plan** are rewritten with `-update`: `go test ./migrate -run 'TestDirGolden|TestNormalizePredicate' -update`, and, with `VELOX_TEST_POSTGRES` set, `go test ./migrate -run TestPredicatesPostgres -update`. Read every rewritten file before committing.
- **Commits** are `Area: what changed`, end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, stage files by name, and are never pushed.

## Review Focus

1. **A directory checked out on Windows.** Git turns LF into CRLF; `velox.sum`, the statements and the directives must not change, and the runner must not call an applied file edited. Task 1 pins this in `TestParseFileCRLF`; Task 2 in `TestSumCRLF`.
2. **A run cancelled part-way.** A deploy's context is cancelled while an online index builds or a MySQL file runs. The version must stay dirty with the count of its completed units and the error that stopped it, though the context is gone, and `-resume` must finish it. Task 4 pins this in `TestUpPostgresOnlineResume` (the error column is written after the cancel).
3. **A rolling deploy.** An old binary meets a database a newer one migrated: `Up` applies nothing, reports `Ahead` and returns no error; `status` exits 0; `drift` refuses, naming the versions above the directory. Task 3 pins `TestUpAhead`; Task 10 `TestMigrateAhead`.
4. **A `;` where it is not a statement's end.** In a literal, a quoted name, a comment, a PostgreSQL dollar quote or `E''` string, a MySQL backslash-escaped quote, and in a `-- review:` line that `writeScript` wrote from a finding's message. Task 1 pins this in `TestSplit` and in `FuzzSplit`'s seeds.
5. **A database holding more than the snapshot names.** Tables the schema does not know, an extra index on a known table, the history table. Drift must never read or report an unknown table, must report the extra index as a change, and must leave the history table out. Task 5 pins `TestDriftUnknownTables`; Task 8 `TestInspectSQLiteFilter` (and Tasks 6–7 its dialect twins).

---

### Task 1: The statement splitter, directives and `ParseFile`

**Files:**
- Create: `migrate/split.go`, `migrate/file.go`, `migrate/split_test.go`, `migrate/file_test.go` (both package `migrate_test`).
- Modify: `migrate/export_test.go` (`WriteScript`, `Split`, `FileBackslash`, `FileGenerated`), `migrate/doc.go` (code `"file"`), `scripts/ci-local.sh` and `.github/workflows/ci.yml` (one fuzz line each: `go test -run XXX -fuzz FuzzSplit -fuzztime 10s ./migrate`).

**Interfaces:**
- Consumes: plan 1's `Directives`, `Script`, `writeScript`, `Allow`, the destructive codes of `lint.go`. Not `stringEnd` and `dollarEnd`: they cannot tell an unterminated literal from one closed at the end, so the splitter finds ends itself (ruling 37).
- Produces: §14's `File` and `ParseFile`, verbatim, and:

```go
// File's unexported fields:
	lines     []int // the line, from 1, of each statement's first token
	backslash bool  // MySQL: a string literal holds a backslash
	generated bool  // the first line is the header Dir.Add writes ("-- Generated by velox-gen "); LintFile reads it

// A piece is a statement or a directive line, in the order of the file.
type piece struct {
	SQL       string // a statement, from its first token to its last, without ";"; "" for a directive
	Directive string // a directive line's text after "-- velox:"; "" for a statement
	Line      int    // its first line, from 1
}

// split reads src as d reads SQL: ";" ends a statement outside quotes,
// comments and dollar quotes. Comments before a statement's first token and
// after its last are not part of it. A "-- velox:" line between statements
// is a directive; one inside a statement is an error.
func split(d velox.Dialect, src string) ([]piece, error)

// sum is a file's hash: "h1:" and the padded standard base64 of the SHA-256
// of data with each "\r\n" read as "\n".
func sum(data []byte) string

// export_test.go:
func WriteScript(s Script) string // writeScript's text (exists)
type Piece struct{ SQL, Directive string; Line int }
func Split(d velox.Dialect, src string) ([]Piece, error) // split
func FileBackslash(f File) bool                          // f.backslash
func FileGenerated(f File) bool                          // f.generated
```

- **The splitter** (ruling 1, ruling 3):
  - all dialects: `'…'` with `''`, `"…"` with `""`, `--` to the end of the line, `/* … */`;
  - PostgreSQL: `E'…'` with backslash escapes, `$tag$…$tag$`, nested `/* */`;
  - MySQL: `` `…` ``; a backslash escapes inside `'…'` and `"…"`; `#` to the end of the line; `--` is a comment only when white space or the end follows it;
  - SQLite: `` `…` `` and `[…]`;
  - an unterminated quote, comment or dollar quote is an error at its line; an empty statement (`;;`) is skipped; text after the last `;` is a statement.
- **`ParseFile`** turns `\r\n` into `\n` first. Its errors are `*Error` with `Pos` `<path>:<line>` and `Code` `"file"`:
  - the path: `^([0-9]{4,})_([a-z0-9_]+)\.sql$`, the version a `uint64`;
  - directives: `no-transaction`, `foreign-keys off`, `lock-timeout <duration>` (`time.ParseDuration`, not negative; `0` is off), `allow <code> [<target>]` (a destructive code, as `ParseAllow` checks one), `unit`. Unknown, malformed or given twice (but `allow`) is an error, and so is a directive but `unit` after the first statement;
  - by dialect (ruling 2): `no-transaction` refused on SQLite (`no-transaction is refused on SQLite: a file outside a transaction would run unlocked`), accepted on MySQL, whose files run statement by statement anyway; `foreign-keys off` on SQLite only; `lock-timeout` on PostgreSQL and MySQL only; `unit` only in a file run statement by statement (a MySQL file, or a PostgreSQL `no-transaction` one);
  - `Units`: in a file run statement by statement, the index of each unit's first statement; a statement before the first `unit` line, or in a file with none, is a unit alone. `nil` for a file run in a transaction;
  - `Sum` is `sum(data)`; `backslash` is set on MySQL when a `'…'` or `"…"` literal holds `\`; `generated` is set when the first line starts with `-- Generated by velox-gen `.

- [ ] **Step 1: Write the failing tests.**
  - `TestSplit` (Review Focus 4), a table of `(dialect, src, want []piece or error)`:
    - `SELECT 'a;b'; SELECT 2` → two; `SELECT "x;y"` → one; MySQL `` SELECT `a;b` ``, `SELECT 'it\'s;'`, `SELECT 1 # c;\n;` → one each; PostgreSQL `SELECT $$a;b$$`, `SELECT $t$ $x$ ; $t$`, `SELECT E'\';'`, `SELECT /* a /* b; */ ; */ 1` → one each; SQLite `SELECT [a;b]` → one;
    - `-- review: drop-column: drops column legacy; it's "gone"\nSELECT 1;` → one statement `SELECT 1` at line 2;
    - `SELECT 1 -- c;\n;` → `SELECT 1`; `;;SELECT 1` → one; `SELECT 1` (no `;`) → one;
    - `-- velox:no-transaction\n\nSELECT 1;\n-- velox:unit\nSELECT 2;` → directive, statement, directive, statement with lines 1, 3, 4, 5;
    - errors: `SELECT 'abc` (`unterminated string literal` at line 1), `SELECT $$x` , `/* x`, and `SELECT\n-- velox:unit\n1;` (`a directive inside a statement`, line 2).
  - `FuzzSplit` (`split_test.go`, through `Split`): for a dialect byte (mod 3) and a source, `split` never panics; when it gives no error, each statement is non-empty with no white space at either end, `split(d, s.SQL + ";")` gives that statement alone, and `split` of the statements joined by `";\n"` gives the same statements. Seeds: every `TestSplit` source, and three `writeScript` outputs whose `-- review:` lines hold `;`, `'` and `"` (carry 1).
  - `file_test.go`:
    - `TestParseFileName`: `0002_add_users_email.sql` is version 2, name `add_users_email`; `00012_x.sql` is 12; refused: `001_x.sql`, `0001-x.sql`, `0001_X.sql`, `0001_x.txt`, `99999999999999999999_x.sql`.
    - `TestParseFileDirectives`: each directive sets its field (`lock-timeout 5s` sets `LockTimeout` and `LockTimeoutSet`; `lock-timeout 0` sets `LockTimeoutSet` with 0; `allow drop-column users.legacy` and `allow drop-table` append `Allow`s); each refusal of the list above on its dialect, its message and `Pos` asserted.
    - `TestParseFileUnits`: a PostgreSQL `no-transaction` file `S1; unit; S2; S3; unit; S4` has `Units` `[0 1 3]`; a MySQL file of three statements without marks `[0 1 2]`; a PostgreSQL file in a transaction `nil`.
    - `TestParseFileCRLF` (Review Focus 1): a file and its CRLF copy give equal `File`s, `Sum` included.
    - `TestParseFileBackslash` (through `FileBackslash`): MySQL `INSERT INTO t VALUES ('a\\b')` sets it; `` `a\b` `` and a comment holding `\` do not.
    - `TestParseFileGenerated` (through `FileGenerated`): a file whose first line is `-- Generated by velox-gen v0.1.0 migrate diff. …` is generated; one with the header on line 2, or none, is not.
    - `TestWriteScriptParses` (carry 3): for every case of `cases` on each of its dialects and each script `Render` gives (`RenderOptions{LockTimeout: 5 * time.Second, Allow: c.allow}`), `ParseFile(d, "0001_x.sql", header + WriteScript(s))` gives `Statements` equal to the scripts' `SQL`, `Directives` equal to the script's (allows compared sorted), and, for a script run statement by statement, `Units` equal to the indexes where `Statement.Unit` changes. `header` is the first line `Dir.Add` writes (Task 2), written here as a literal comment.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'Split|ParseFile|WriteScriptParses'`. Expected: FAIL (`split` and `ParseFile` undefined).
- [ ] **Step 3: Implement** `split`, `sum` and `ParseFile` as the Interfaces say.
- [ ] **Step 4: Run them, and the fuzz test.** Run `go test ./migrate/` and `go test -run XXX -fuzz FuzzSplit -fuzztime 30s ./migrate`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0, with the new fuzz line.
- [ ] **Step 6: Commit** `Migrate: the statement splitter, directives and ParseFile`.

### Task 2: The directory: `velox.json`, `velox.sum`, `ReadDir` and `Add`

**Files:**
- Create: `migrate/dir.go`, `migrate/dir_test.go`, `migrate/dirs_test.go` (package `migrate_test`: the helpers later tasks use), `migrate/testdata/dir/<case>.<dialect>/…`.
- Modify: `migrate/errors.go` (`SumError`), `migrate/doc.go` (codes `"config"` and `"directory"`).

**Interfaces:**
- Consumes: Task 1's `ParseFile` and `sum`; plan 1's `Plan`, `writeScript` and `Schema.MarshalJSON`.
- Produces: §14's `Config`, `Dir`, `NewDir`, `ReadDir`, `Sum` and `(*Dir).Add`, verbatim, and:

```go
type SumError struct{ Path string } // "velox.sum", as fsys names it

// Error is "migrate: <Path> is out of date; run velox-gen migrate hash if
// the edits are intended".
func (e *SumError) Error() string

// Dir's unexported fields:
	sums  map[string]string // path → h1 of each file ReadDir read: the SQL files, schema.json, velox.json
	fresh bool              // made by NewDir: Add writes velox.json too

// generator is "velox-gen" and the version of the velox2 module the program
// is built from, "(devel)" when the build does not say, as velox-gen
// version prints it.
func generator() string

// defaultName is a plan's file name when Add is given none (ruling 7).
func defaultName(changes []Change) string

// dirs_test.go (package migrate_test):
type step struct {
	to    *migrate.Schema     // nil: no plan, only files
	opts  migrate.PlanOptions // LockTimeout 0 takes the Config's
	name  string
	files map[string]string // hand-written files added after the plan's, by path
}
// extend is a copy of fsys (nil: a new directory of dialect d) with s's plan
// and files added and velox.sum rewritten. A step whose plan has no change
// writes no plan file (Add is not called). A step's hand files are written
// with schema.json of the current model (the last plan's to, or the empty
// schema), so the next ReadDir accepts the directory (ruling 39).
func extend(t *testing.T, fsys fstest.MapFS, d velox.Dialect, s step) fstest.MapFS
// dirOf is extend from nil, once per step.
func dirOf(t *testing.T, d velox.Dialect, steps ...step) fstest.MapFS
```

- **`velox.json`** (ruling 5): `version` (1, required), `dialect` (`velox.Dialect`'s string, required), `table` (default `velox_migrations`; `^[A-Za-z_][A-Za-z0-9_]*$`, at most 63 bytes), `lock_timeout` (default `5s`; `time.ParseDuration`, not negative). An unknown key is an error. `Add` writes every key, indented by two spaces, with a final newline.
- **`velox.sum`** (rulings 4 and 38): the first line `h1:` and the base64 SHA-256 of the lines below, each with its `\n`; then `<path> <sum>` for each top-level `*.sql`, `schema.json` and `velox.json`, sorted by path. Other files and directories are not covered and not read. Each file's hash is over its content with CRLF read as LF, and `ReadDir` and `hash -check` parse `velox.sum` itself with CRLF read as LF, so a checkout with `core.autocrlf` gives the same sums.
- **`ReadDir`** checks the sum first: a missing or different `velox.sum` is `*SumError`, whatever else is wrong. Then `velox.json`, `schema.json` (required when there are files, refused when there are none, and of the Config's dialect) and every `*.sql` through `ParseFile`; two files of one version are an error. `Snapshot` is the empty schema of the dialect when there are no files.
- **`Add`** (rulings 6–9) does not change `d`. It refuses (`*Error`, `Code` `"directory"`): a nil plan or one with no script, `to` of another dialect, a name not `[a-z0-9_]+` or over 40 bytes. It returns:
  - one file per script, at the last version plus one, two, three, zero-padded to four: `<v>_<name>_enum.sql`, `<v>_<name>.sql`, `<v>_<name>_online.sql`;
  - each file is `-- Generated by ` + `generator()` + ` migrate diff. Review before applying; velox.sum checks it.\n`, then `writeScript`'s text;
  - **an `EnumScript` without `LockTimeoutSet` takes `lock-timeout <Config.LockTimeout>`** (carry 4, ruling 6);
  - `schema.json`: `to`'s `MarshalJSON`, indented by two spaces, with a final newline;
  - `velox.json` when `d.fresh`; `velox.sum` over the files `d` read and the new ones.

- [ ] **Step 1: Write the failing tests** (`dir_test.go`).
  - `TestConfig`: `NewDir(Config{Dialect: velox.Postgres})` fills `velox_migrations` and 5s; the `velox.json` `Add` writes reads back equal; refusals: version 2, dialect `oracle`, a key `tabel`, table `a-b` and one of 64 bytes, `lock_timeout` `-1s` and `x`.
  - `TestSum`: the format above on a three-file directory (lines sorted, the first line's hash recomputed in the test); a `README.md` and a `sub/0009_x.sql` change nothing.
  - `TestSumCRLF` (Review Focus 1): a directory and its CRLF copy (SQL files, `schema.json`, `velox.json` and `velox.sum` itself all CRLF) have the same `Sum`; `ReadDir` of the CRLF copy succeeds; and `ReadDir` of the LF files with a CRLF `velox.sum` succeeds.
  - `TestReadDirStale`: an edited, added, removed or renamed SQL file, an edited `schema.json` or `velox.json`, and a missing `velox.sum` each give `*SumError` with `Path` `velox.sum` and the text above; a stale sum wins over a malformed file.
  - `TestReadDir`: files sorted by version; the empty directory's snapshot is `&Schema{Dialect: d}`; errors: two version-2 files, `velox.json` missing, `schema.json` missing beside files, `schema.json` of another dialect; a file's own `ParseFile` error comes through.
  - `TestDirAdd`:
    - a hand-built PostgreSQL plan of three scripts on a directory at version 3 gives `0004_x_enum.sql`, `0005_x.sql`, `0006_x_online.sql`, `schema.json` and `velox.sum`, no `velox.json`; on a `NewDir` directory, `velox.json` too, and versions from `0001`;
    - each SQL file's first line is the header, and `ReadDir` of the result succeeds;
    - `d` is `reflect.DeepEqual` to a copy taken before;
    - refusals: an empty plan, `to` of MySQL on a PostgreSQL directory, the name `Add_X`;
    - carry 4: a plan made with `LockTimeout` 0 on a directory whose `lock_timeout` is `7s` gives an `_enum` file with `-- velox:lock-timeout 7s` and a main file with no lock-timeout line.
  - `TestDefaultName`: `create-tables` gives `create_<first table>`, `add-column-optional` `add_users_<col>`, `drop-column` `drop_users_legacy`, `schema-qualified` `create_billing_invoices`, a change on a 60-byte table name is cut to 40 bytes with no trailing `_`, and a table `Users-X` gives `create_users_x`.
  - `TestDirGolden`: `create-tables`, `add-index`, `enum-insert` and `drop-column` (with its allow), on each of their dialects, as `dirOf(t, d, step{to: from}, step{to: to, opts: …})`, equal `testdata/dir/<case>.<dialect>/<path>` byte for byte (the header's version reads `(devel)` in tests).
  - `TestDirReadsWhatAddWrites`: for every case on each dialect, `ReadDir(dirOf(from, to))` has a `Snapshot` `Equal` to `to`, and its last plan's files' statements equal `Render`'s.
  - `TestDirDeterministic`: `dirOf` twice gives equal maps.
  - `TestDirOfSteps`: a step whose plan is empty (`create-tables`' empty `from`) adds no file; a files-only first step writes its file and the empty schema's `schema.json`, and the next step's `ReadDir` succeeds; `Add` of an empty plan is still refused.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'Config|Sum|ReadDir|DirAdd|DefaultName|DirGolden|DirReads|DirDeterministic'`. Expected: FAIL.
- [ ] **Step 3: Implement** as the Interfaces say. Write the goldens with `-update`, and read them.
- [ ] **Step 4: Run** `go test ./migrate/`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: the directory, velox.sum and Dir.Add`.

### Task 3: The runner: sessions, locks, history and files in a transaction

**Files:**
- Create: `migrate/runner.go` (options, `Applied`, `Report`, `State`, `Up`, `Status`), `migrate/session.go` (the connection, version and session checks, locks), `migrate/history.go`, `migrate/session_test.go`, `migrate/runner_test.go` (SQLite), `migrate/runner_db_test.go` (PostgreSQL, MySQL), `migrate/dbs_test.go`, `migrate/main_test.go`.
- Modify: `migrate/export_test.go` (`LockName`, `LockKey`, `CheckVersion`), `migrate/errors.go` (`ChecksumError`, `ErrLockWait`, and §14's `DirtyError` type, which `reading` returns), `migrate/render_postgres_test.go` (`pgDatabase`, `dsnWithDatabase` and `pgExec` move to `dbs_test.go`), `migrate/doc.go` (codes `"session"` and `"history"`).

**Interfaces:**
- Consumes: Task 2's `ReadDir` and `Dir`; Task 1's `File`; `velox.DriverCode`.
- Produces: from §14's runner block, verbatim: `Option`, `WithTarget`, `WithLockWait`, `WithLogger`, `Applied`, `Report`, `State`, `Up`, `Status`, `ChecksumError`, `ErrLockWait` (`WithResume`, `Resolve` and `DirtyError` are Task 4's; `WithBaseline` is plan 3's, ruling 17). And:

```go
// Error is "migrate: <Path> was applied with checksum <Applied> and is now
// <Now>", or, when Now is "", "migrate: version <Version> is applied but the
// directory has no file for it".
func (e *ChecksumError) Error() string

type options struct {
	target   uint64 // 0: every file
	resume   bool
	lockWait time.Duration // default 5m
	logger   *slog.Logger  // nil: none
}

type session struct {
	conn *sql.Conn
	d    velox.Dialect
	cfg  Config
	db   string // MySQL: DATABASE()
}

// open takes one connection and checks it: it answers as cfg.Dialect, at its
// minimum version, and on PostgreSQL with standard_conforming_strings on.
// Its refusals are *Error with Code "session".
func open(ctx context.Context, db *sql.DB, cfg Config) (*session, error)
func (s *session) close()

// checkVersion refuses a server below PostgreSQL 12 (server_version_num),
// MySQL 8.0.19 (@@version; MariaDB refused) or SQLite 3.35 (sqlite_version()).
func checkVersion(d velox.Dialect, v string) error

func lockName(database, table string) string // "velox_migrate_" + the first 16 hex digits of SHA-256 of database + "." + table
func lockKey(table string) int64             // the first 8 bytes of SHA-256 of table, big-endian

// lock takes the migration lock, waiting at most wait and while ctx lives;
// a timeout is ErrLockWait. On SQLite it sets busy_timeout to wait for the
// run, and each file's BEGIN IMMEDIATE is the lock.
func (s *session) lock(ctx context.Context, wait time.Duration) (unlock func(), err error)

func (s *session) createHistory(ctx context.Context) error
// history is the table's rows by version; exists is false when there is no table.
func (s *session) history(ctx context.Context) (rows []Applied, exists bool, err error)

// reading is dir against the history, by ruling 54's rules (ruling 40): a
// dirty row first, as the state with Dirty set and a *DirtyError, together;
// else the state, or the first refusal (*ChecksumError, or *Error with Code
// "history"), with a nil state.
func reading(dir *Dir, rows []Applied) (*State, error)

// applyTx runs a file in one transaction, its history row included.
func (s *session) applyTx(ctx context.Context, f File, o options) (Applied, error)

// dbs_test.go (package migrate_test):
// freshDB is a new, empty database of dialect d, closed and dropped when t
// ends: a file in t.TempDir() on SQLite; on PostgreSQL a database beside
// VELOX_TEST_POSTGRES's; on MySQL one made through VELOX_TEST_MYSQL_ADMIN.
// It skips t when the dialect's variable is unset.
func freshDB(t *testing.T, d velox.Dialect) (db *sql.DB, dsn string)
```

- **Steps of `Up`** are §10's 1–7. The history table is §10's, created with `CREATE TABLE IF NOT EXISTS` after the lock (on SQLite inside a `BEGIN IMMEDIATE` that also reads the history and commits, so creating it waits on the lock too, ruling 41), in the connection's default schema, all columns `NOT NULL`, MySQL's `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`. `applied_at` is written in UTC as text (`2006-01-02 15:04:05.000000`, with `+00:00` on PostgreSQL) and read back as text in that layout (ruling 12): PostgreSQL `to_char(applied_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')`, MySQL `CAST(applied_at AS CHAR)`; on SQLite the column is declared `text` and holds the layout, so the driver never turns it into a `time.Time` (ruling 42).
- **Locks** (rulings 10–11): PostgreSQL `SET lock_timeout = '<wait>ms'`, `SELECT pg_advisory_lock($1)` with `lockKey(cfg.Table)`, `RESET lock_timeout`; `55P03` is `ErrLockWait`; `pg_advisory_unlock` after. MySQL `GET_LOCK(lockName(DATABASE(), cfg.Table), <wait, whole seconds rounded up>)`: 0 is `ErrLockWait`, NULL an error; `RELEASE_LOCK` after; a DSN with no database is a `"session"` refusal. SQLite: `PRAGMA busy_timeout` set to the wait and restored; `SQLITE_BUSY` (`DriverCode`'s code & 0xff == 5) at `BEGIN IMMEDIATE` is `ErrLockWait`.
- **Ruling 54** in `reading` (ruling 40): a dirty row is reported first, whatever its file's checksum and wherever its version is: `State.Dirty` and a `*DirtyError` (`Statement` 0) together; a dirty row above the last file is that too, never in `State.Ahead`. Then, for applied (clean) rows only: within the files' range every applied version has its file, with the same `Sum` (else `*ChecksumError`); clean versions above the last file go to `State.Ahead`, and then `Up` applies nothing and sets `Report.Ahead`; a pending file below the highest applied version is `*Error{Code: "history"}` (`migrate: 0003_x.sql is pending below version 5, the highest applied; the history is linear`); `Up` refuses the `*DirtyError` (Task 4 adds `WithResume` and the error's full text).
- **A file in a transaction** is §10's six steps: `BEGIN` (`BEGIN IMMEDIATE` on SQLite, run on the connection), `SET LOCAL lock_timeout = '<ms>ms'` on PostgreSQL (the file's directive, else the Config's), the statements, on SQLite with `foreign-keys off` `PRAGMA foreign_key_check` (a row reported rolls back), the history row (`dirty = false`, `statements` = the file's count), `COMMIT`. On SQLite `foreign_keys` is set off before `BEGIN` when the file asks and restored to what it was after. SQLite reads the history again after `BEGIN IMMEDIATE` and skips a file another runner applied meanwhile.
- **A failure in a transaction** returns `fmt.Errorf("migrate: %s: statement %d of %d: %w; the file was rolled back", f.Path, i+1, n, err)` (ruling 13).
- **`Status`** reads the directory and the history without the lock and without creating the table, and returns `reading`'s result: with a dirty version, the state and the `*DirtyError` both (ruling 19).
- **`Up` on MySQL, or on a PostgreSQL `no-transaction` file,** returns `*Error{Code: "session", Reason: "files run statement by statement are not built yet"}` until Task 4.
- **`TestMain`** (`main_test.go`): with `CI=true`, a missing `VELOX_TEST_POSTGRES` or `VELOX_TEST_MYSQL_ADMIN` fails the package, as `internal/itest`'s does.

- [ ] **Step 1: Write the failing tests.**
  - `session_test.go` (through the hooks): `TestCheckVersion` (PostgreSQL `110000` refused, `120000` and `160004` pass; MySQL `8.0.18` refused, `8.0.19`, `8.4.3` and `8.0.36-log` pass, `10.11.2-MariaDB` refused; SQLite `3.34.1` refused, `3.35.0` passes); `TestLockName`: `lockName("velox", "velox_migrations")` is `velox_migrate_28873223e55a53b9`; `TestLockKey`: `lockKey("velox_migrations")` is `-6634131438283035561`.
  - `runner_test.go` (SQLite, with `dirOf`):
    - `TestUpAppliesInOrder`: two steps apply as versions 1 and 2 in `Report.Applied`, with their names and sums; the history rows say so (`dirty` false, `statements` the count, `applied_at` within the test's span); a second `Up` applies nothing.
    - `TestUpWithTarget`: three files and `WithTarget(2)` apply two.
    - `TestUpRollsBack`: a hand file `0002_bad.sql` of `CREATE TABLE a (x integer); INSERT INTO missing VALUES (1)` gives an error whose text starts `migrate: 0002_bad.sql: statement 2 of 2: ` and ends `; the file was rolled back`, and `errors.As` finds the driver's error; table `a` and the history row are absent.
    - `TestUpChecksum`: version 1 applied, then its file edited and the sum rewritten, gives `*ChecksumError` with §16's text; the file deleted gives `Now` "" and `migrate: version 1 is applied but the directory has no file for it`.
    - `TestUpAhead` (Review Focus 3): two files applied, then `Up` with the first only: no error, `Report.Ahead`, nothing applied; `Status` lists version 2 in `Ahead` and nothing pending.
    - `TestUpPendingBelow`: versions 1 and 3 applied, 2 pending: `*Error` `"history"`.
    - `TestUpStaleSum`: `*SumError`; `TestUpNoTransactionOnSQLite`: a hand file with `-- velox:no-transaction` is `ParseFile`'s refusal, through `Up`.
    - `TestUpDialectMismatch`: a PostgreSQL directory on a SQLite database is `*Error` `"session"`.
    - `TestUpSQLiteRebuild`: `rebuild-parent` through the runner keeps the children and the `AUTOINCREMENT` counter, as `TestRenderSQLiteRebuildKeepsRows` checks, and `PRAGMA foreign_keys` reads 1 after; a hand `foreign-keys off` file that leaves an orphan row is rolled back with an error naming `foreign_key_check`.
    - `TestUpSQLiteConcurrent`: two `*sql.DB` on one file, two goroutines' `Up`: each file applied once (one history row each, a hand file's `INSERT INTO log` row once).
    - `TestUpSQLiteLockWait`: on a fresh file, a second connection holds `BEGIN IMMEDIATE`; `Up` with `WithLockWait(200 * time.Millisecond)` is `ErrLockWait` (the history table's creation waits inside the lock), and no history table exists after.
    - `TestStatusDirty` (a history row set dirty by hand): `Status` returns the state with `Dirty` and a `*DirtyError`, also when the dirty version's file was edited, and also when the dirty version is above the last file (`Ahead` empty).
    - `TestStatus`: before any `Up` every file is pending and no history table exists after; after `Up`, every file is applied.
    - `TestHistoryTableSQLite`: `pragma table_info` gives §10's columns and types, but `applied_at` `text` (ruling 42).
  - `runner_db_test.go` (each under its variable):
    - `TestUpPostgres`: `create-tables`'s `to` applies; the history table's columns have §10's types (`information_schema.columns`); a hand file `DO $$ BEGIN IF current_setting('lock_timeout') <> '5s' THEN RAISE EXCEPTION 'lock_timeout is %', current_setting('lock_timeout'); END IF; END $$` applies.
    - `TestUpPostgresConcurrent`: two `Up`s at once apply each file once.
    - `TestUpPostgresLockWait`: another session holding `pg_advisory_lock(lockKey)` makes `Up` with a 300ms wait `ErrLockWait`.
    - `TestUpPostgresStandardConformingStrings`: after `ALTER DATABASE … SET standard_conforming_strings = off`, `Up` is `*Error` `"session"`.
    - `TestMySQLLock`: another session holding `GET_LOCK(<the lock name>)` makes `Up` of a directory with no files, with a 1s wait, `ErrLockWait`; released, `Up` succeeds and creates the history table with §10's MySQL types.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'CheckVersion|LockName|LockKey|Up|Status|History|MySQLLock'`, then with the CI variables of `scripts/ci-local.sh` against its containers (`docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait`). Expected: FAIL.
- [ ] **Step 3: Implement** as the Interfaces say.
- [ ] **Step 4: Run** `go test ./migrate/` with and without the variables. Expected: PASS; without them the database tests skip.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Up and Status, with locks, history and files in a transaction`.

### Task 4: Files run statement by statement, the dirty state, resume and `Resolve`

**Files:**
- Create: `migrate/stepwise.go`, `migrate/resolve.go`, `migrate/stepwise_db_test.go` (PostgreSQL, MySQL), `migrate/resolve_test.go`.
- Modify: `migrate/runner.go` (`WithResume`; the dispatch), `migrate/session.go` (MySQL's `sql_mode` check), `migrate/errors.go` (`DirtyError`), `migrate/doc.go`.

**Interfaces:**
- Consumes: Task 3's session, `reading`, `options`.
- Produces: §14's `WithResume`, `Resolution`, `ResolveApplied`, `ResolveUndone`, `Resolve`, verbatim, `DirtyError`'s `Error` text (Task 3 declares the type), and:

```go
// Error, when Statement > 0: "migrate: <Path>: statement <Statement> of
// <Statements>: <Err>", then "\n  version <V> is dirty; fix the cause and run
// migrate apply -resume, or " + undo + "run migrate resolve -version <V>
// -undone", where undo is "", "undo statement 1 and " or "undo statements 1
// to <k> and " for k = Statement-1. When Statement is 0 (found dirty, not
// failed now): "migrate: <Path>: version <V> is dirty: <Err>", then "\n  fix
// the cause and run migrate apply -resume, or undo its applied statements
// and run migrate resolve -version <V> -undone".
func (e *DirtyError) Error() string
func (e *DirtyError) Unwrap() error

// applyStepwise runs a file statement by statement (§10), from the first
// statement of its first incomplete unit when resuming.
func (s *session) applyStepwise(ctx context.Context, f File, row *Applied, o options) (Applied, error)
```

- **Statement by statement** is §10's five steps: the history row `dirty = true, statements = 0, error = ''` (on resume, the row is kept); the lock timeout (the file's directive; else on MySQL the Config's; else on PostgreSQL `SET lock_timeout = 0`); each unit, then `UPDATE … SET statements = <first statement of the next unit>`; on success `dirty = false`, the duration, `applied_at` now, and `RESET lock_timeout` on PostgreSQL.
- **MySQL's lock timeout** (ruling 15): `SET SESSION lock_wait_timeout = <seconds, rounded up>`; `0` sets back the value the session had when the run began, which is also restored after each file.
- **A failure** writes `error` and returns `*DirtyError{Version, Path, Statement: i+1, Statements: n, Err}`. It writes through `context.WithoutCancel(ctx)` with a 10-second bound, and through a new connection from `db` when its own no longer answers (ruling 14): a cancelled run leaves its error on the dirty row.
- **Before applying,** `reading`'s `*DirtyError` stops the run unless `WithResume`; with it, the dirty file must be in the directory with the row's checksum unchanged (else `*ChecksumError`), and it resumes from `statements`. A dirty version above the directory's last file has no file, and is refused with or without resume.
- **Ruling 53 on MySQL:** when `@@SESSION.sql_mode` holds `NO_BACKSLASH_ESCAPES`, a pending file with `backslash` set is refused before any file runs (`*Error` `"session"`, naming the file: `0003_x.sql holds a backslash in a literal, which NO_BACKSLASH_ESCAPES would store as two; turn the mode off for migrating`) (ruling 33).
- **`Resolve`** (ruling 18) takes the lock and reads the history itself, not through `reading`, so an edited file does not stop it; the version must be dirty (`*Error` `"history"`: `version 4 is not dirty`). `ResolveApplied` needs the file with the row's checksum (else `*ChecksumError`) and sets `dirty = false`, `statements` the file's count, `error = ''`; `ResolveUndone` deletes the row.

- [ ] **Step 1: Write the failing tests.**
  - `TestDirtyErrorText` (`resolve_test.go`, no database): `{4, "0004_add_users_org_id.sql", 2, 3, errors.New("Error 1205: Lock wait timeout exceeded")}` prints §16's two lines exactly; `Statement` 1 and 3 give no undo and `undo statements 1 to 2 and `; `Statement` 0 gives the found-dirty form; `errors.Unwrap` is `Err`.
  - `stepwise_db_test.go`, MySQL:
    - `TestUpMySQLDirty`: a file `CREATE TABLE a (x int); INSERT INTO b VALUES (1); CREATE TABLE c (x int)` gives `*DirtyError` with `Statement` 2, `Statements` 3; the row is dirty with `statements` 1 and the driver's error; `Up` again is `*DirtyError` with `Statement` 0; `Status.Dirty` is that row; after `CREATE TABLE b (x int)`, `Up(WithResume())` succeeds without running `CREATE TABLE a` again (it would fail) and leaves the row clean with `statements` 3.
    - `TestResolveMySQL`: on a dirty version, `ResolveUndone` deletes the row; after the file is fixed and rehashed, `Up` applies it; `ResolveApplied` on a dirty version cleans it; on a clean version it is `*Error` `"history"`; `ResolveApplied` after the file was edited is `*ChecksumError`, `ResolveUndone` is not refused.
    - `TestUpMySQLLockTimeout`: another session holds a transaction that read table `t`; a file `-- velox:lock-timeout 1s` with `ALTER TABLE t ADD COLUMN y int` fails dirty within 5 seconds with error 1205.
    - `TestUpMySQLNoBackslashEscapes`: on a database whose DSN sets `sql_mode='NO_BACKSLASH_ESCAPES'`, a directory whose second file inserts `'a\\b'` is refused before its first file runs (no history row).
    - `TestUpMySQLConcurrent`: two `Up`s at once apply each file once.
  - PostgreSQL:
    - `TestUpPostgresOnlineResume` (Review Focus 2; follow-up "the online units' resume … with rows"): `users` holds 1000 rows; another connection runs `BEGIN ISOLATION LEVEL REPEATABLE READ; SELECT count(*) FROM users` and stays open (a REPEATABLE READ transaction keeps its snapshot after the `SELECT`, and `CREATE INDEX CONCURRENTLY` waits for every older snapshot; a READ COMMITTED one holds no snapshot between statements and would not block it); `Up` of `add-index`'s plan with a 2-second context returns `*DirtyError`, the row is dirty with `statements` 0 and a non-empty `error`, and `pg_index.indisvalid` of `users_created_at` is false; the transaction ends; `Up(WithResume())` succeeds and the index is valid.
    - `TestUpPostgresSetNotNullResume` (follow-up "validated checks of `SET NOT NULL` … with rows"): `set-not-null` on a table with a row holding NULL fails at `VALIDATE`, dirty; after `UPDATE users SET bio = '' WHERE bio IS NULL`, resume succeeds; the column is `NOT NULL` and `users_bio_not_null` is gone.
    - `TestUpPostgresNoTransactionLockTimeout`: a `no-transaction` hand file whose `DO` block raises unless `current_setting('lock_timeout')` is `0` applies; with `-- velox:lock-timeout 3s`, unless it is `3s`; the `_enum` file `Add` wrote for `enum-insert` with `LockTimeout` 0 applies with the Config's `5s`, through the directive `Add` wrote (`TestDirAdd` pins it). Whether `RESET` ran is not observed: every later file sets its own `lock_timeout` first.
- [ ] **Step 2: Run them** against the CI containers. Expected: FAIL.
- [ ] **Step 3: Implement** as the Interfaces say; the Task 3 stub goes.
- [ ] **Step 4: Run** `go test ./migrate/` with and without the variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: files run statement by statement, dirty versions, resume and Resolve`.

### Task 5: The predicate normaliser and drift's comparison

**Files:**
- Create: `migrate/predicate.go`, `migrate/drift.go`, `migrate/predicate_test.go`, `migrate/drift_test.go`, `migrate/testdata/predicates/postgres.txt`.
- Modify: `migrate/change.go` (`String` methods), `migrate/export_test.go` (`Note`, `DriftOf`, `NormalizePredicate`), `migrate/doc.go`.

**Interfaces:**
- Consumes: plan 1's `Diff`, `clone`, `validate`, `literalDefault` (`lint.go`), `ddl.ParseEnumCheck`.
- Produces: §14's `Warning` and `DriftReport`, verbatim (the exported `Drift` is Task 6's, with ruling 20's signature), and:

```go
func (w Warning) String() string // "<Object>: <Message>"

// Every change type has String (ruling 27): "<kind> <object>", the object
// being the table, "<table>.<column>", "<table>.<index or constraint>" or the
// enum type; a rename adds " → <new name>", an alter-column " (<What>)",
// an alter-enum " (<from values> → <to values>)".
func (c AddColumn) String() string // "add-column users.email"; one per change type

// A note is what inspect saw that the model has no place for (ruling 20):
// a MySQL engine other than InnoDB, an invalid PostgreSQL index, a NOT VALID
// constraint, a GENERATED ALWAYS identity. The model holds no notes.
type note struct {
	table   TableRef
	object  string // as a Warning's Object: "users", "users.users_x"
	message string // as a Warning's Message
}

// drift compares an inspected schema and its notes with snapshot: the
// changes that would bring the database to snapshot, and the warnings (each
// note on a table of snapshot among them). Drift calls it after inspecting.
func drift(inspected *Schema, notes []note, snapshot *Schema) (*DriftReport, error)

// export_test.go:
type Note struct{ Table TableRef; Object, Message string } // a note, as tests write and read it
func DriftOf(inspected, snapshot *Schema, notes ...Note) (*DriftReport, error) // drift
func NormalizePredicate(d velox.Dialect, expr string) string                  // normalizePredicate

// normalizePredicate is expr in one canonical spelling, for comparing what a
// database prints with what was written (ruling 23).
func normalizePredicate(d velox.Dialect, expr string) string
```

- **The normaliser** parses `or`, `and`, `not`, comparisons (`=`, `<>`/`!=`, `<`, `<=`, `>`, `>=`), `IS [NOT] NULL`, `[NOT] IN (…)`, `[NOT] LIKE`/`ILIKE` (PostgreSQL's `~~`, `!~~`, `~~*`, `!~~*`), `BETWEEN a AND b` (rewritten `>= a AND <= b`), `x = ANY (ARRAY[…])` and `x <> ALL (ARRAY[…])` (rewritten `IN` and `NOT IN`), `+ - * /`, unary `-`, function calls, literals, numbers, `true`/`false`/`NULL`, identifiers and casts; it drops every `::type` (a `'<number>'` cast to a numeric type becomes the number), unquotes identifiers PostgreSQL would not quote, lower-cases keywords, function names and unquoted identifiers, and prints with one space around binary operators and the parentheses precedence needs. Text it cannot parse comes back with its white space collapsed.
- **`drift(inspected, notes, snapshot)`** refuses a nil schema (`"nil"`), two dialects (`"dialect"`) and `validate`'s refusals. On a copy of `inspected`, for each table both have (ruling 22):
  - an index of one name, columns and `Unique` whose predicates differ: `snapshot`'s `Where` is copied in; when they differ after `normalizePredicate`, a warning names both spellings;
  - a check of one name whose `Expr`s differ: when both are enum checks (`ddl.ParseEnumCheck`), nothing is copied (a real change); else, when they are equal after `normalizePredicate`, `snapshot`'s is copied with a warning naming both spellings; a check whose normalised forms differ is a real change, left to `Diff` (ruling 22);
  - a column default where both are expressions (`literalDefault` false for both): copied, with a warning naming both;
  - an index, foreign key or check named "" (SQLite's unparsed) whose definition equals one of `snapshot`'s: the name is copied;
  - MySQL: `OnDelete` "" and `RESTRICT` are one action, copied;
  - primary-key columns, or a name present on one side only, that differ: `snapshot`'s copied, with a warning (Diff would refuse them);
  - each note on a table of `snapshot`: a warning (`table users uses engine MyISAM, not InnoDB`; `index users_x is invalid: a CREATE INDEX CONCURRENTLY failed; run migrate apply -resume, or drop it`; `constraint … is NOT VALID`; `column … is GENERATED ALWAYS, the model has BY DEFAULT`).
  
  Then `Changes` is `Diff(copy, snapshot, DiffOptions{})`; `Warnings` sorted by `(Object, Message)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestNormalizePredicate` (`predicate_test.go`, through `NormalizePredicate`): `testdata/predicates/postgres.txt` holds blocks `input: …`, `printed: …` (may be empty until Task 6 fills it), `normal: …`. For each, `normalizePredicate(Postgres, input)` and, when present, of `printed`, equal `normal`. The inputs (written here; the normal forms with `-update`, read line by line): `status = 'active'`; `"status" = 'it''s'`; `qty > 0`; `qty >= -1`; `price < 1.5`; `qty <> 3`; `qty != 3`; `deleted_at IS NULL`; `deleted_at IS NOT NULL`; `active`; `NOT active`; `active = true`; `status IN ('a', 'b')`; `status NOT IN ('a', 'b')`; `qty > 0 AND status = 'a'`; `qty > 0 OR status = 'a'`; `(qty > 0 OR qty < -5) AND deleted_at IS NULL`; `qty > 0 AND (status = 'a' OR status = 'b')`; `status = 'a'::text`; `created_on > '2020-01-01'::date`; `lower(email) = 'x@y'`; `length(name) > 3`; `coalesce(qty, 0) > 1`; `name LIKE 'a%'`; `name NOT LIKE 'a%'`; `name ILIKE 'a%'`; `qty BETWEEN 1 AND 5`; `code = 'x'` (a `varchar` column).
  - `TestNormalizePredicateKeepsMeaning`: `a AND (b OR c)` and `a AND b OR c` differ; `(a)` and `a` are equal; a literal `'A  B'` keeps its case and spaces; `"Foo" = 1` and `foo = 1` differ.
  - `drift_test.go`, on hand-made schemas, through `DriftOf`:
    - `TestDriftSelf`: `DriftOf(x, x)` has no change and no warning for every case's `from` and `to`.
    - `TestDriftPredicateSpelling`: `(status = 'active'::text)` against `status = 'active'` gives nothing; `(status = 'banned'::text)` gives no change and one warning on `users.users_active` holding both texts.
    - `TestDriftDefaultExpression`: `CURRENT_TIMESTAMP` against `now()` gives a warning and no change; `'x'` against `'y'` gives an `AlterColumn` of default.
    - `TestDriftEnumCheck`: a SQLite enum check losing a value is a change.
    - `TestDriftCheckSpelling`: PostgreSQL's printed `(qty > 0)` against `qty > 0` is a spelling: a warning and no change; `qty > 5` against `qty > 0` is a drop and an add of the check.
    - `TestDriftMySQLRestrict`, `TestDriftUnnamedConstraints` (SQLite, by definition), `TestDriftPrimaryKey` (a warning, no error).
    - `TestDriftNotes` (through `DriftOf` with `Note`s): each kind of note on a snapshot table gives its warning and no change; a note on a table the snapshot lacks gives none; the same schemas with no notes give no warning.
    - `TestDriftUnknownTables` (Review Focus 5): an inspected schema holding a table the snapshot lacks gives no change about it; a snapshot table missing from it gives `CreateTable`; an extra index on a known table gives `DropIndex`.
    - `TestDriftOfDoesNotMutate`, `TestDriftDeterministic`, `TestDriftErrors`.
    - `TestChangeStrings`: one line per change type, e.g. `rename-table users → accounts`, `alter-column users.name (type|nullable)`.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'Predicate|Drift|ChangeStrings'`. Expected: FAIL.
- [ ] **Step 3: Implement** as the Interfaces say.
- [ ] **Step 4: Run** `go test ./migrate/`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: drift's comparison, with the predicate normaliser`.

### Task 6: `Inspect` and `Drift` on PostgreSQL

**Files:**
- Create: `migrate/inspect.go` (`Inspect`, `Drift`, the dispatch, sorting, the default normaliser), `migrate/inspect_postgres.go`, `migrate/inspect_test.go`, `migrate/inspect_postgres_test.go`.
- Modify: `migrate/export_test.go` (`InspectNotes`, `NormalizeDefault`), `migrate/testdata/predicates/postgres.txt` (the `printed` lines), `migrate/doc.go`.

**Interfaces:**
- Consumes: Task 5's `note`, `drift` and `normalizePredicate`; Task 3's `freshDB`; `ddl.EnumCheck`, `ddl.EnumType`, `ddl.NormalizeType`.
- Produces: §14's `Inspect`, verbatim; `Drift` with ruling 20's signature; and:

```go
// Drift reads db's tables and enum types that snapshot names, and reports
// the changes that would bring them to snapshot, and warnings: spellings the
// normaliser cannot match, and what the model cannot hold (a MySQL engine
// other than InnoDB, an invalid index, a NOT VALID constraint, a GENERATED
// ALWAYS identity). It never reads a table snapshot does not name.
func Drift(ctx context.Context, db *sql.DB, snapshot *Schema) (*DriftReport, error)

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// inspect is Inspect with its notes; Inspect returns its schema alone.
func inspect(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, []note, error)

func inspectPostgres(ctx context.Context, q querier, tables []TableRef) (*Schema, []note, error)

// export_test.go:
func InspectNotes(ctx context.Context, db *sql.DB, d velox.Dialect, tables []TableRef) (*Schema, []Note, error) // inspect
func NormalizeDefault(d velox.Dialect, s string) string                                                       // normalizeDefault

// normalizeDefault is a default as the database prints it, in the model's
// spelling: casts dropped from literals, a quoted number cast to a numeric
// type unquoted, nextval('s'::regclass) as nextval('s').
func normalizeDefault(d velox.Dialect, s string) string
```

- **`Drift`** is `inspect(ctx, db, snapshot.Dialect, <snapshot's table refs>)`, then `drift` with its notes; MySQL and SQLite return `*Error{Code: "dialect", Reason: "… is not built yet"}` until Tasks 7 and 8.
- **`Inspect`** refuses an unknown dialect (`"dialect"`); wraps database errors as `migrate: inspect: %w`; returns the tables sorted as §2 says. `tables` nil means every table of the default schema but `velox_migrations` (ruling 21); a named table that does not exist is absent. A table of the default schema has `Schema` "", and so does a foreign key's `RefTable` there.
- **PostgreSQL** (§11), through `pg_catalog`, the default schema being `current_schema()`:
  - tables: `relkind` `r` and `p`; comments by `obj_description` and `col_description`;
  - columns in `attnum` order: `format_type` through `ddl.NormalizeType`; a column of an enum type (`typtype = 'e'`) has `ddl.EnumType` of its type's schema (qualified when not the default) and name; `attnotnull`; `pg_get_expr(adbin, adrelid)` through `normalizeDefault`; `attidentity` `d` and `a` are `Identity` (`a` gives a note);
  - **`Sequence`** (carry 5): an identity's from `pg_get_serial_sequence`; a column whose default is `nextval` of a sequence it owns (`pg_depend` `deptype = 'a'`) has `Identity` false, `Default` `nextval('s')` and `Sequence` `s`; the name is unqualified when the sequence is in the table's schema;
  - the enum types the inspected columns use, values by `enumsortorder` (ruling 26);
  - indexes from `pg_index` but the primary key: columns by `indkey`, `indisunique`, `pg_get_expr(indpred, indrelid)` as printed, an index with `indisvalid` false is left out of the schema and reported only as a note (ruling 43); an index over an expression is left out (ruling 25);
  - constraints from `pg_constraint`: `p` gives `PrimaryKey` (by `conkey`) and `PrimaryKeyName`; `f` gives a foreign key (`confdeltype` `c`, `n`, `r`, `a` as `CASCADE`, `SET NULL`, `RESTRICT`, ""; `d` as `SET DEFAULT`), `c` a check: an enum check (`normalizePredicate` gives `<col> IN (<literals>)`) as `ddl.EnumCheck`, any other with `pg_get_expr(conbin, conrelid)` as printed; `convalidated` false gives a note; `u` is left out, its index modelled (ruling 25).

- [ ] **Step 1: Write the failing tests.**
  - `inspect_test.go` (no database): `TestNormalizeDefault` (through `NormalizeDefault`): PostgreSQL `'x'::text` → `'x'`, `'it''s'::character varying` → `'it''s'`, `'-1'::integer` → `-1`, `'0'::numeric` → `0`, `'viewer'::users_role` → `'viewer'`, `nextval('users_id_seq'::regclass)` → `nextval('users_id_seq')`, `now()`, `CURRENT_TIMESTAMP`, `true` and `1.5` unchanged.
  - `inspect_postgres_test.go` (under `VELOX_TEST_POSTGRES`):
    - `TestInspectPostgresCases`: for every case's `from` (but the hand-made ones) and `to` on PostgreSQL, created fresh through `Up` of `dirOf`, `Drift(ctx, db, model)` has no change and no warning.
    - `TestInspectPostgresSequence` (carry 5): an identity named `users_id_seq` has it; an identity created without `SEQUENCE NAME` has PostgreSQL's name; a `serial` column has `Identity` false, `nextval('t_id_seq')` and `Sequence` `t_id_seq`; a `nextval` default of a sequence no column owns has `Sequence` "".
    - `TestInspectPostgresEnums`: `enum-shared`'s type appears once, values in order; a table in `billing` using a type there has `"billing"."status"`-form type.
    - `TestInspectPostgresNotes`: an invalid index (a failed `CREATE UNIQUE INDEX CONCURRENTLY` over duplicates), a `NOT VALID` foreign key and a `GENERATED ALWAYS` column each give a note through `InspectNotes`, the invalid index absent from the schema; `Drift` against a model with the index warns on the invalid index and reports its `AddIndex` (the model's index is not there in usable form), and warns on the other two with no change.
    - `TestInspectPostgresFilter` (Review Focus 5): `nil` leaves out `velox_migrations` and reads every other table; a named missing table is absent; a table only another schema holds is not read.
    - `TestPredicatesPostgres` (ruling 52's goldens): for each input of `postgres.txt`, a table with the columns the inputs name gets `CREATE INDEX … WHERE <input>`; `Inspect`'s `Where` equals the block's `printed` (written with `-update`), and `NormalizePredicate` of it equals `normal`.
    - `TestInspectDoesNotMutate`: the `tables` slice is unchanged; `TestDriftDoesNotMutate`: the snapshot is unchanged; `TestInspectDeterministic`.
- [ ] **Step 2: Run them** against the CI PostgreSQL. Expected: FAIL.
- [ ] **Step 3: Implement.** Fill `printed` with `-update` and read each line: a `normal` that the printed form misses is a normaliser bug, not a golden to rewrite.
- [ ] **Step 4: Run** `go test ./migrate/` with and without the variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Inspect and Drift on PostgreSQL, with owned sequences and predicate goldens`.

### Task 7: `Inspect` on MySQL, and the primary key's name

**Files:**
- Create: `migrate/inspect_mysql.go`, `migrate/inspect_mysql_test.go`.
- Modify: `migrate/inspect.go` (the dispatch), `migrate/inspect_test.go`, `migrate/render_mysql.go` and `migrate/render_test.go` (carry 2).

**Interfaces:**
- Consumes: Task 6's `inspect`, `Drift`, `querier`, `normalizeDefault`; `ddl.MySQLEnum`, `ddl.ParseMySQLEnum`, `ddl.Literal`.
- Produces:

```go
func inspectMySQL(ctx context.Context, q querier, tables []TableRef) (*Schema, []note, error)
```

- **MySQL** (§11), through `information_schema`, the default schema being `DATABASE()`:
  - `TABLES`: `TABLE_COLLATION` as `Collation`, `TABLE_COMMENT`, `ENGINE` not `InnoDB` gives a note;
  - `COLUMNS` by `ORDINAL_POSITION`: `COLUMN_TYPE` through `ddl.NormalizeType`; `IS_NULLABLE`; `EXTRA` `auto_increment` is `Identity`; `COLUMN_COMMENT`;
  - `COLUMN_DEFAULT`: NULL is ""; with `DEFAULT_GENERATED` in `EXTRA`, an expression: `CURRENT_TIMESTAMP(6)` and the like as printed, a `_utf8mb4'…'` (MySQL's `\'` and `\\` read) as the model's `('<ddl.Literal>')`; otherwise a literal: unquoted text re-quoted with `ddl.Literal` for string, enum, date and time types, a number as printed;
  - `STATISTICS` but `PRIMARY`: `NON_UNIQUE`, columns by `SEQ_IN_INDEX`; an index with an expression (`COLUMN_NAME` NULL) or a prefix (`SUB_PART`) is left out;
  - `TABLE_CONSTRAINTS`, `KEY_COLUMN_USAGE` and `REFERENTIAL_CONSTRAINTS`: the primary key's columns, and **`PrimaryKeyName` always `PRIMARY`** when there is one (carry 2); foreign keys with `DELETE_RULE` (`NO ACTION` as ""); `RefTable.Schema` "" when it is `DATABASE()`;
  - `CHECK_CONSTRAINTS`: an enum check ``(`c` in (_utf8mb4'a',_utf8mb4'b'))`` as `ddl.EnumCheck`, any other as printed.
- **`Render` on MySQL** refuses a `RenameConstraint` whose `From` or `To` is the table's primary key name in `from` or `to`, or `PRIMARY` (`*Error`, `Code` `"render"`, `MySQL's primary key is always named PRIMARY`) (carry 2).

- [ ] **Step 1: Write the failing tests.**
  - `TestRenderMySQLRefusesPrimaryKeyRename` (`render_test.go`, no database): a `RenameConstraint{From: "PRIMARY", To: "users_pkey"}` on a MySQL `users` is that `*Error`.
  - `inspect_mysql_test.go` (under `VELOX_TEST_MYSQL_ADMIN`):
    - `TestInspectMySQLCases`: as `TestInspectPostgresCases`, on MySQL; `schema-qualified` creates and drops its `billing` database around itself (ruling 30).
    - `TestInspectMySQLPrimaryKeyName` (carry 2): every table with a primary key has `PRIMARY`; `Diff(Inspect, a snapshot of the same tables)` has no `RenameConstraint`.
    - `TestInspectMySQLDefaults`: `literals`'s columns (a `'` and a `\` in a default, a comment and an enum value) inspect equal to the model; `CURRENT_TIMESTAMP(6)`, `('x')` on a `text` column, `0`, `1.5` and `1` on `tinyint(1)` read back as the model writes them.
    - `TestInspectMySQLRestrict`: `ON DELETE RESTRICT` and no action both give no drift against either model spelling.
    - `TestInspectMySQLEngine`: a `MyISAM` table gives a note through `InspectNotes`, and `Drift` an engine warning and no change.
    - `TestInspectMySQLFilter` (Review Focus 5): as on PostgreSQL.
- [ ] **Step 2: Run them** against the CI MySQL. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./migrate/` with and without the variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Inspect on MySQL; the primary key is always PRIMARY`.

### Task 8: `Inspect` on SQLite, and `Verify`

**Files:**
- Create: `migrate/inspect_sqlite.go`, `migrate/verify.go`, `migrate/inspect_sqlite_test.go`, `migrate/verify_test.go`.
- Modify: `migrate/inspect.go`, `migrate/export_test.go` (`ParseCreateTable`), `migrate/doc.go` (code `"drift"`).

**Interfaces:**
- Consumes: Tasks 3–7.
- Produces: §14's `Verify`, verbatim, and:

```go
func inspectSQLite(ctx context.Context, q querier, tables []TableRef) (*Schema, []note, error)

// createTable is what the CREATE TABLE text of sqlite_schema names that the
// pragmas do not: constraint names, CHECK texts and AUTOINCREMENT.
type createTable struct {
	autoincrement bool
	checks        []Check                       // in order; Name "" for an unnamed one
	foreignKeys   map[string]string             // the key's columns joined by "," → its name
}
func parseCreateTable(sql string) (createTable, bool) // false: not a form it reads

// export_test.go:
func ParseCreateTable(sql string) (autoincrement bool, checks []Check, foreignKeys map[string]string, ok bool)
```

- **SQLite** (§11): tables from `sqlite_schema` (`type = 'table'`, not `sqlite_%`, not `velox_migrations` when `tables` is nil); `pragma table_xinfo` (hidden columns left out): the declared type lower-cased through `ddl.NormalizeType`, `notnull`, `dflt_value` as written, `pk` order as `PrimaryKey`; `Identity` only for an `integer` one-column key with `AUTOINCREMENT` (ruling 24); `index_list` (origin `c` and `u`, not `pk`) and `index_info`, a partial index's `Where` the text after `WHERE` in its `CREATE INDEX`; `foreign_key_list` grouped by `id` (`NO ACTION` as ""); names and checks from `parseCreateTable`, which reads `CONSTRAINT <name> FOREIGN KEY|CHECK`, unnamed `CHECK (…)`, column-level `REFERENCES` and `CHECK`, all four quote forms and comments; a form it cannot read leaves names "" (§11's call). `PrimaryKeyName`, `Comment` and `Collation` are "".
- **`Verify`** reads the directory; refuses a database that is not empty (a table but the history table, or a history row: `*Error` `"drift"`, `Verify needs an empty database`); runs `Up`; and returns `*Error{Code: "drift", Reason: "the database differs from schema.json after applying the directory:\n  " + each change's String, one per line}` when `Drift` against the snapshot finds a change (warnings do not fail it, ruling 32).

- [ ] **Step 1: Write the failing tests.**
  - `TestParseCreateTable` (through `ParseCreateTable`): velox's own `CREATE TABLE` of `create-tables` on SQLite; `CREATE TABLE t (id integer PRIMARY KEY AUTOINCREMENT, p integer REFERENCES u(id), s text CHECK (s IN ('a')), [x y] text, CONSTRAINT "k" FOREIGN KEY (p) REFERENCES u (id), CONSTRAINT c2 CHECK (length(s) > 0) /* ; */)`: `autoincrement`, the column-level check unnamed, `c2`'s text, `k` for `p`; an unreadable text gives false.
  - `TestInspectSQLiteCases`: as on PostgreSQL, always run (a file database).
  - `TestInspectSQLiteAutoincrement`: `integer PRIMARY KEY` without it is not `Identity`.
  - `TestInspectSQLiteFilter` (Review Focus 5): `sqlite_sequence` and `velox_migrations` are never read; a missing named table is absent.
  - `TestDriftSQLite`: `Drift` of a database made from a case's `to` has no change and no warning against it; a column added by hand gives `drop-column`; a table the snapshot does not name is never read.
  - `TestVerify` (SQLite): `dirOf` of a case's `from` then `to` passes on an empty database; a directory whose last file has a hand `ALTER TABLE users ADD COLUMN extra text` (rehashed) fails, the error naming `drop-column users.extra`; a database holding a table fails as not empty. `TestVerifyPostgres` and `TestVerifyMySQL`: the passing case, under their variables.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'CreateTable|InspectSQLite|Verify'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./migrate/` with and without the variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Inspect on SQLite, and Verify`.

### Task 9: `velox-gen migrate diff`, `hash` and `lint`

**Files:**
- Create: `cmd/velox-gen/migrate.go` (the dispatch, shared flags and output), `cmd/velox-gen/migrate_diff.go`, `cmd/velox-gen/migrate_test.go`, `migrate/lintfile.go`, `migrate/lintfile_test.go`.
- Modify: `cmd/velox-gen/main.go` (`case "migrate"`; `printUsage`; the package doc's `migrate` paragraphs), `migrate/split.go` (`words`), `migrate/lint.go` (the codes `delete-rows`, `update-rows` in the destructive list), `migrate/doc.go`.

**Interfaces:**
- Consumes: Tasks 1–2; plan 1's `NewPlan`, `ParseAllow`, `Blocked`, `BlockedError`; `gen.Load`, `gen.Build`, `gen.Tables`; the CLI's `newFlagSet`, `parseFlags`, `schemaPackage`, `relPos`.
- Produces:

```go
// cmd/velox-gen:
// runMigrate is velox-gen migrate: diff, apply, status, drift, lint, hash and
// resolve (Task 10 adds the four that open a database).
func runMigrate(args []string, dir string, env []string, stdout, stderr io.Writer) int

// migrate (ruling 28):
// LintFile checks f as velox-gen migrate lint does. Its error, *Error values
// with Code "file" joined, is for CONCURRENTLY or ALTER TYPE … ADD VALUE in
// a PostgreSQL file run in a transaction, and for BEGIN, START, COMMIT,
// ROLLBACK or END leading a statement of a PostgreSQL or SQLite file run in
// one. Its findings, for a file that is not generated (f.generated), are the
// destructive statements it reads: DROP TABLE t (drop-table t), ALTER TABLE t
// DROP COLUMN c (drop-column t.c), ALTER TABLE t ALTER COLUMN c [SET DATA]
// TYPE and MODIFY COLUMN c (alter-type t.c), ALTER COLUMN c SET NOT NULL
// (set-not-null t.c), TRUNCATE t and DELETE FROM t without WHERE
// (delete-rows t), UPDATE t without WHERE (update-rows t); Pos is
// "<path>:<line>", Change the statement's index.
func LintFile(d velox.Dialect, f File) ([]Finding, error)

// words is sql's tokens outside literals and comments, keywords upper-cased,
// quoted names unquoted.
func words(d velox.Dialect, sql string) []string
```

- **`migrate diff`** (§13): `-dir` (`./migrations`), `-dialect`, `-name`, `-allow`, `-rename-table old=new` and `-rename-column table.old=new` (repeatable; a table may be `schema.name`; the last dot splits the column), `-lock-timeout` (default: `velox.json`'s; ruling 29), `-check`, `-dry-run`, the package (`./schema`):
  - the directory: `ReadDir(os.DirFS(dir))`; a missing directory needs `-dialect` (else exit 2) and is `NewDir`; `-dialect` differing from `velox.json` is exit 2; a stale sum is exit 1 (`velox-gen: migrate diff: migrations/velox.sum is out of date; …`, the path joined to `-dir`), exit 3 under `-check`;
  - a malformed `-allow` or `-rename-*` is exit 2; a rename naming nothing in the snapshot is exit 1 with §16's text (`-rename-column users.nme=full_name: table users in schema.json has no column nme`; `-rename-table x=y: schema.json has no table x`);
  - `gen.Tables` errors print as `generate`'s do, positions relative (exit 1);
  - `-check`: `Diff` alone; any change is exit 3;
  - `NewPlan`; a `*BlockedError` prints `velox-gen: migrate diff: ` and its text without `migrate: `, positions relative (exit 1); no change prints `no changes` (exit 0);
  - `-dry-run` prints each file `Add` returns as `== <path>` and its content, and writes nothing; otherwise each is written (0o644, the directory made 0o755) and its path printed.
- **`migrate hash`**: writes `Sum(os.DirFS(dir))` to `velox.sum`; `-check` writes nothing and exits 3 when it differs.
- **`migrate lint`**: `ReadDir` (stale: exit 1); for each file from `-since` (default all), `LintFile`; errors and the findings `Blocked(findings, f.Directives.Allow)` keeps print as `<pos>: <code>: <message>` (exit 1); none: exit 0.
- An unknown `migrate` subcommand, `baseline` included until plan 3, is exit 2.

- [ ] **Step 1: Write the failing tests.**
  - `lintfile_test.go`: `TestLintFile`: each classified form gives its code and target on its dialect, at its line; `DROP TABLE` in a string literal or comment gives none; a file with the header (`generated`) gives no finding; `CREATE INDEX CONCURRENTLY` in a PostgreSQL transaction file and `BEGIN` in a SQLite file are errors; `ParseAllow("delete-rows,update-rows")` succeeds.
  - `migrate_test.go`, `TestMigrateDiff`, in one temp module (`tempModule`, one schema `User` with `name`), subtests in order, each asserting exit code, stdout and stderr:
    - `diff -dialect sqlite` writes `migrations/{0001_create_users.sql,schema.json,velox.json,velox.sum}` and prints the four paths; again: `no changes`, exit 0; `diff -check`: exit 0;
    - with a field `email` added: `diff -check` exits 3; `diff -dry-run` prints `== 0002_add_users_email.sql` and writes nothing; `diff` writes it;
    - with `name` removed: exit 1, the `BlockedError` lines with `-allow drop-column=users.name`; with `-allow drop-column=users.name` the file carries `-- velox:allow drop-column users.name`;
    - `-dialect postgres`: exit 2; `-allow nope`: exit 2; `-rename-column users.nme=x`: exit 1 with §16's text;
    - a missing directory without `-dialect`: exit 2.
  - `TestMigrateHashAndLint` (no module needed: files written by the test, `velox.sum` by `migrate.Sum`): an edited file makes `hash -check` exit 3 and `lint` exit 1; `hash` then makes both exit 0; a hand file `0002_x.sql` with `DROP TABLE users;` makes `lint` exit 1 with `0002_x.sql:1: drop-table: …`; with `-- velox:allow drop-table users` exit 0; `lint -since 3` skips it.
  - `TestMigrateUsage`: `migrate`, `migrate nope` and `migrate baseline` exit 2; `-h` exits 0.
- [ ] **Step 2: Run them.** Run `go test ./cmd/velox-gen/ ./migrate/ -run 'Migrate|LintFile'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./cmd/velox-gen/ ./migrate/ && go test -run TestGodocCitesNoSpec .`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `velox-gen: migrate diff, hash and lint`.

### Task 10: `velox-gen migrate apply`, `status`, `resolve` and `drift`

**Files:**
- Create: `cmd/velox-gen/migrate_db.go` (the drivers, opening a database), `cmd/velox-gen/migrate_apply.go`, `cmd/velox-gen/main_test.go` (`TestMain`: with `CI=true`, a missing `VELOX_TEST_MYSQL_ADMIN` fails the package, as `migrate`'s does).
- Modify: `cmd/velox-gen/migrate.go`, `cmd/velox-gen/main.go` (usage and doc), `cmd/velox-gen/migrate_test.go`.

**Interfaces:**
- Consumes: Tasks 3–8; Task 9's `runMigrate`.
- Produces:

```go
// openDB opens dsn with the driver of d: "pgx", "mysql" or "sqlite", whose
// packages this file imports. dsn is -db, else $VELOX_DB from env.
func openDB(d velox.Dialect, dsn string) (*sql.DB, error)
```

- **`apply`** `[-dir] [-db] [-to] [-resume] [-lock-wait 5m] [-dry-run]`: no DSN is exit 2 (`apply needs -db or $VELOX_DB`). Prints `applied <path> in <duration>` per file; `Ahead` prints `the database is ahead of the directory (versions 5, 6); nothing applied` (exit 0); `-dry-run` prints each pending file as `== <path>` and its content, from `Status`. A `*DirtyError`, `*ChecksumError`, `*SumError`, `ErrLockWait` or other error is exit 1, printed as `velox-gen: migrate apply: …`.
- **`status`** `[-dir] [-db] [-json]`: lines `applied <version> <path> <applied_at RFC 3339 UTC>`, `pending <version> <path>`, `dirty <version> <path>: <statements> statements done: <error>`, `ahead <version> <name>`. Exit 0 when up to date or ahead, 3 with pending files, 1 with a dirty version (ruling 29). `-json` prints, indented:
  `{"applied":[{"version":1,"name":"create_users","sum":"h1:…","applied_at":"2026-10-24T10:00:00.123456Z","duration_ms":12,"dirty":false,"statements":3,"error":""}],"pending":[{"version":2,"path":"0002_add_users_email.sql"}],"ahead":[],"dirty":null}`.
- **`drift`** `[-dir] [-db] [-json]`: `Status` first; a pending, dirty or ahead state is exit 1 naming it (`the database is not at the directory's last version: 0002_x.sql is pending`); then `Drift` against the snapshot. Each change prints `change: <String>`, each warning `warning: <String>`; exit 3 with a change, else 0. `-json`: `{"changes":[{"kind":"add-column","target":"users","text":"add-column users.email"}],"warnings":[{"object":"…","message":"…"}]}`.
- **`resolve`** `[-dir] [-db] -version n (-applied | -undone)`: neither or both, or no `-version`, is exit 2.

- [ ] **Step 1: Write the failing tests** (`migrate_test.go`, on a SQLite file DSN in `t.TempDir()`, the directory written by the test with `migrate.NewDir`, `Add` and plans from hand-made schemas):
  - `TestMigrateApply`: `status` exits 3 listing two pending; `apply -dry-run` prints both and applies nothing; `apply -to 1` applies one; `apply` the other; `status` exits 0; `status -json` decodes into the shape above.
  - `TestMigrateDrift`: after `apply`, `drift` exits 0 with no output; after `ALTER TABLE users ADD COLUMN extra text` by hand, exit 3 printing `change: drop-column users.extra`; with a pending file, exit 1.
  - `TestMigrateAhead` (Review Focus 3): a database applied from a three-file directory, the CLI on its two-file copy: `apply` exit 0 with the ahead line, `status` exit 0 with `ahead 3 …`, `drift` exit 1.
  - `TestMigrateResolve`: `resolve -version 1 -applied` on a clean version exits 1 (`version 1 is not dirty`); `resolve -version 1` exits 2; no `-db` and no `VELOX_DB` exits 2; `VELOX_DB` in the env serves.
  - `TestMigrateDirtyMySQL` (under `VELOX_TEST_MYSQL_ADMIN`): a failing second statement makes `apply` exit 1 with §16's two lines, `status` exit 1 with the `dirty` line, `resolve -undone` exit 0 and `status` exit 3.
- [ ] **Step 2: Run them.** Run `go test ./cmd/velox-gen/ -run Migrate`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./cmd/velox-gen/`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `velox-gen: migrate apply, status, resolve and drift`.

### Task 11: MySQL's row limit over every column

**Files:**
- Modify: `internal/gen/tables_check.go` (the row count; its constant `mysqlRowBytes = 65535` is renamed `mysqlRowLimit`, so the new function takes the name), `internal/gen/tables_test.go` (`TestTablesRowLimit`, `TestTablesRowSize`).

**Interfaces:**
- Consumes: plan 1's `Tables` and its row-limit refusal.
- Produces:

```go
// mysqlRowBytes is what a column of MySQL type t (normalised) counts toward
// the 65535-byte row limit, as MySQL 8.4 counts it with utf8mb4; ok is false
// for a type it does not know, which counts 0.
func mysqlRowBytes(t string) (n int, ok bool)
```

- **The count** (ruling 35, spec defect in ruling 47): the sum over every column of the table, primary key and join columns included, plus the NULL bitmap, at most 65535:
  - `tinyint` 1, `smallint` 2, `mediumint` 3, `int` 4, `bigint` 8 (`unsigned` alike); `float` 4, `double` 8;
  - `decimal(M,D)`: the integer digits (M−D) and the fraction digits (D) each count 4 bytes per 9 digits, and their leftover digits 0, 1, 1, 2, 2, 3, 3, 4, 4 bytes for 0 to 8;
  - `date` 3; `datetime(p)` 5, `timestamp(p)` 4 and `time(p)` 3, each plus 0, 1, 1, 2, 2, 3, 3 bytes for p 0 to 6; `year` 1;
  - `char(n)` 4n; `binary(n)` n; `varchar(n)` 4n and `varbinary(n)` n, each plus 1 length byte when that is at most 255, else 2 (ruling 47's count, unchanged);
  - `enum(…)` 1, or 2 over 255 values; `bit(M)` ⌈M/8⌉;
  - `tinytext` and `tinyblob` 9, `text` and `blob` 10, `mediumtext` and `mediumblob` 11, `longtext`, `longblob` and `json` 12 (the in-row pointer);
  - the NULL bitmap: ⌈(NULL-able columns)/8⌉.
- **The error** keeps §16's form: `MySQL rows hold at most 65535 bytes; these need <n>:`, one line per `varchar` and `varbinary` field as today (they are what `Text()` or a smaller `MaxLen` shrinks), then `  the other columns and the NULL bitmap: <m> bytes`, then `  use Text() or a smaller MaxLen`.

- [ ] **Step 1: Write the failing tests.**
  - `TestMySQLRowBytes`: a table of `(type, bytes)` over every row above, with the edges: `varchar(63)` 253 (252 + 1), `varchar(64)` 258 (256 + 2), `varbinary(255)` 256, `varbinary(256)` 258, `decimal(20,0)` 9, `decimal(10,2)` 5, `datetime(6)` 8, `enum` of 256 values 2; an unknown type is `0, false`.
  - `TestTablesRowLimit` (rewritten): a schema of an `int64` ID (8), `Int32("n").Optional().Nillable()` (4, and a 1-byte bitmap), `String("a").MaxLen(8165)` (32662), `String("b").MaxLen(8164)` (32658) and `Bytes("c").MaxLen(201)` (`varbinary(201)`, 202) comes to exactly 65535 and builds on MySQL; with `Bytes("c").MaxLen(202)` (65536) it is refused with the error above, naming `a`, `b` and `c` and `the other columns and the NULL bitmap: 13 bytes`; plan 1's case of 65535 bytes of `varchar` alone is now refused (its ID counts).
  - `TestTablesRowSize`: a join table and a table with a `Text()` field and a `json` field count their pointers, not their contents.
- [ ] **Step 2: Run them.** Run `go test ./internal/gen/ -run 'RowBytes|RowLimit|RowSize'`. Expected: FAIL.
- [ ] **Step 3: Implement** as the Interfaces say.
- [ ] **Step 4: Run** `go test ./internal/gen/ ./migrate/`. Expected: PASS; `TestTablesGolden` and the render goldens unchanged (no golden table is near the limit).
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Generator: MySQL's row limit counts every column`.

### Task 12: Every change kind on real databases, with rows and the three invariants

**Files:**
- Create: `migrate/itest_test.go`, `migrate/rows_test.go` (rows on three dialects).
- Modify: `migrate/render_test.go` (`insertRows` and `rowValue` move to `rows_test.go`, taking a dialect).

**Interfaces:**
- Consumes: everything above; `cases`, `models`, `dirOf`, `extend`, `freshDB`.
- Produces (test code):

```go
// insertRows puts one row in each table of s on d, with foreign keys off
// while it does (PostgreSQL: session_replication_role = replica; MySQL:
// FOREIGN_KEY_CHECKS = 0; SQLite: the pragma), each column a value its type
// and checks take that reads the same as text in every type the case gives
// the column (rowValue).
func insertRows(t *testing.T, db *sql.DB, d velox.Dialect, s *migrate.Schema)
// rowsOf is each table's row as text by column, cast per dialect.
func rowsOf(t *testing.T, db *sql.DB, d velox.Dialect, s *migrate.Schema) map[migrate.TableRef]map[string]string
// invariants checks §17's three after an apply of fsys.
func invariants(t *testing.T, db *sql.DB, d velox.Dialect, fsys fs.FS, model *migrate.Schema)
```

- **`TestMigrateCases`**: for each case on each of its dialects (SQLite always, PostgreSQL and MySQL under their variables), serially, on `freshDB`:
  1. `dirOf(from)`; `Up`; `invariants(from)`. A hand-made `from` with a `serial` column gets a hand file `CREATE SEQUENCE` before its plan and one with `ALTER SEQUENCE … OWNED BY` after, as baseline will (ruling 51);
  2. `insertRows(from)` unless the case's `Lint` has `add-required-column`;
  3. `extend` with the case's plan (`opts.Diff` and `allow`); `Up`; `invariants(to)`;
  4. each table of `from` that `to` keeps (renames followed) still has its row, and each column it keeps (renamed or not) reads the same text.
  
  `invariants`: (1) `Drift(ctx, db, model)` has no change; (2) `Diff(ReadDir(fsys).Snapshot, model)` is empty; (3) `Drift(ctx, db, snapshot)` has no change and no warning, the notes' warnings included. A failure prints the scripts and the drift report.
- **Targeted itests** (each naming the follow-up or §17 line it covers):
  - `TestMigrateMySQLCombined` (follow-up "MySQL's combined `ALTER TABLE`s"): asserts the goldens still hold the combined statement (`literals.mysql.sql`: an `ADD COLUMN` with a `MODIFY COLUMN`; `reuse-dropped-name.mysql.sql`: an index dropped and added under one name; `rename-table.mysql.sql` and `drop-column-in-unique-key.mysql.sql`: a foreign key's step-1 drop and step-7 add), then applies each on MySQL with rows, without error.
  - `TestMigrateFKIndexReplacedMySQL` (ruling 46): `fk-index-replaced` then `fk-index-restored` on MySQL with rows, no error 1553.
  - `TestMigrateSQLiteDropCycle` (follow-up): `create-tables`'s `to` with rows in the two tables that reference each other, then a plan dropping both, through `Up`: applied, `PRAGMA foreign_keys` 1 after; the same plan with a hand-added orphan insert fails `foreign_key_check` and rolls back.
  - `TestMigrateRowsSurvive` (follow-up "the rows"), on PostgreSQL with 3 rows per table: `key-type-change` and `widen-int` keep the values through their casts; `enum-remove`, `enum-reorder` and `enum-shared` keep the labels the rows hold through the swap; after `add-identity`, an insert without `id` gets the old maximum plus one (`setval`); `set-not-null` validates over rows with no NULL.
  - `TestMigrateLiterals` (ruling 53): after `literals`, on each dialect, a row inserted with `DEFAULT` reads back `it's a \ test`, the enum value `o'neil` and the comments read back unchanged through `Inspect`.
  - `TestMigrateMySQLRowLimit` (rulings 47 and 35): `TestTablesRowLimit`'s schema at exactly 65535 bytes builds, is created on MySQL 8.4 through `Up`, and holds a row of full-length values; its 65536-byte twin (`MaxLen(202)`) is refused by `Tables` at plan time. If MySQL 8.4 refuses the row at the limit, stop and report: Task 11's count is then short of MySQL's (InnoDB's own row limit under `innodb_strict_mode` would be the first suspect).
- [ ] **Step 1: Write the tests.** Run them on SQLite first: `go test ./migrate/ -run 'TestMigrate'`. Expected: FAIL where a dialect's `Inspect`, `Drift` or `Render` and the database disagree; each failure is fixed in the code it names (a renderer bug in `render_*.go` with its golden rewritten and read; an inspect bug in `inspect_*.go`), never by loosening an invariant.
- [ ] **Step 2: Run them on PostgreSQL and MySQL** against the CI containers. Expected: as Step 1.
- [ ] **Step 3: Fix** what they find; a fix that changes a plan-1 behaviour the spec's rulings fix is stopped and reported instead.
- [ ] **Step 4: Run** `go test ./migrate/` with the variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: every change kind on three databases, with rows and the three invariants`.

### Task 13: Binding round-trips, and the documents

**Files:**
- Create: `internal/bindmodel/schema/bind.go`, `internal/bindmodel/bindtype/bindtype.go` (a UUID type and an enum type), the generated `internal/bindmodel/*.go`, `internal/itest/binding_test.go`.
- Modify: `scripts/ci-local.sh` and `.github/workflows/ci.yml` (`bin/velox-gen -check -out internal/bindmodel -package bindmodel ./internal/bindmodel/schema` and its `verify` line, after testmodel's); `docs/plans/2026-10-14-core-1-followups.md` (mark each "Left by migrate plan 1" bullet resolved with its task; add "Left by migrate plan 2"); `docs/specs/2026-10-23-migrate-design.md` (a section "Rulings made in plan 2": this plan's rulings and those found while executing it; §14's `Drift` line marked as changed by ruling 20; §9's `velox.sum` paragraph notes that `velox.sum` itself is read with CRLF as LF, ruling 38); `migrate/doc.go` (the runner, the directory and the codes, checked against the code).

**Interfaces:**
- Consumes: Tasks 2–3 (the table is made by `Up` of a directory `dirOf`-style in memory); Task 11's row count (the `binds` table must fit); `internal/itest`'s `targets`.
- Produces: the entity `Bind` (table `binds`), one field per row of §4, as the DSL spells them: `field.Bool`, `Int8`, `Int16`, `Int32`, `Int64`, `Uint8`, `Uint16`, `Uint32`, `Uint64`, `Float32`, `Float`, `String`, `String(…).MaxLen(20)`, `Text`, `Enum(…).Values("a", "b")`, `EnumOf[bindtype.Level]`, `Bytes`, `Bytes(…).MaxLen(16)`, `Time`, `UUID(…, bindtype.UUID{})`, `Strings(…).Optional()` (NULL-able), `JSON(…, bindtype.Doc{})`, and a `Nillable` string. `bindtype.UUID` binds and scans its canonical 36-character text (§4).

- **`TestBindingRoundTrip`** (`internal/itest`): on each target (SQLite, PostgreSQL through pgx and through `database/sql`, MySQL), the `binds` table is created by `migrate.Up` of a directory planned from `gen.Tables` of the `bindmodel` schema (loaded in-process with `load.Marshal`), then a row with every field set is written through velox and read back equal; and:
  - `uint64` at 2⁶³−1 round-trips everywhere; at 2⁶⁴−1 it round-trips on PostgreSQL and MySQL and is refused at bind on SQLite, never stored as another value;
  - the enum fields round-trip native (a PostgreSQL enum type, a MySQL `enum`) and through SQLite's check;
  - a nil `Strings` field stores SQL NULL (`IS NULL` true) and reads back nil; an empty one stores `[]`.

- [ ] **Step 1: Write the schema and generate.** `go build -o bin/velox-gen ./cmd/velox-gen && bin/velox-gen -out internal/bindmodel -package bindmodel ./internal/bindmodel/schema`; then `-check` and `verify` pass.
- [ ] **Step 2: Write the failing test.** Run `go test ./internal/itest/ -run TestBindingRoundTrip` with the variables. Expected: FAIL until the test's setup is written; then PASS, or a binding bug, which is fixed in the planner's type (§4) or reported when it is the core's.
- [ ] **Step 3: Edit the documents** (the Files list). "Left by migrate plan 2" names what plan 3 owns: baseline and `apply -baseline` (`WithBaseline`, `Report.Baselined`), the property test, the examples and `internal/testmodel` on the planner, `diff -check` in CI, and the documents.
- [ ] **Step 4: Run** `go test ./internal/itest/ ./migrate/ ./cmd/velox-gen/ && go test -run TestGodocCitesNoSpec .`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0, with the two `bindmodel` lines.
- [ ] **Step 6: Commit** `Itest: binding round-trips through the planner; plan 2's rulings recorded`.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call) A statement runs from its first token to its last.** Comments before and after it are not part of it, so `-- review:` lines and trailing comments never reach the database, and a statement split again gives itself, which `FuzzSplit` checks.
2. **(call) Directives are refused where they mean nothing:** `foreign-keys off` outside SQLite, `lock-timeout` on SQLite, `unit` in a file run in a transaction, any directive but `allow` given twice. `no-transaction` is accepted on MySQL, whose files run statement by statement anyway. `unit` groups a MySQL file's statements as it does PostgreSQL's (ruling 34).
3. **(call) The splitter reads each dialect as the dialect does:** MySQL's `#` comments and its `--` only before white space, PostgreSQL's nested block comments and `E''` strings, SQLite's `[…]` names. §9 names only the common forms; a file valid for its database must split as that database splits it.
4. **(call) `velox.sum` lists its files sorted by path; the first line hashes the lines below, each with its newline.** It covers the top-level `*.sql`, `schema.json` and `velox.json` only, so a `README.md` beside them is free. `ReadDir` checks the sum before it parses anything, so a stale sum is reported first, whatever else is wrong.
5. **(call) `velox.json` refuses unknown keys,** so `"tabel"` is caught. The table is an identifier of at most 63 bytes, the planner's cap.
6. **(call) The `_enum` file always carries its lock timeout:** `Add` writes the Config's into an enum script `Render` gave none (carry 4). The runner then applies a file's directive, else the Config's in a transaction or on MySQL, else none in a PostgreSQL `no-transaction` file, as §10 says, and never reads a file's name to decide. So a hand-written `_enum` file without the directive runs with no lock timeout, as any `no-transaction` file does. `ADD VALUE` takes a short exclusive lock on the type (plan 1's ruling 15); the online script, which must run without one, has no directive and gets none.
7. **(call) The default file name** is the first change's kind up to its first `-`, then its table (`<schema>_<name>` when qualified), then the column for a column change: `create_users`, `add_users_email`, `drop_posts_legacy`; lower-cased, other bytes `_`, cut to 40 bytes, no trailing `_`.
8. **(call) The header's version is the velox2 module's, read from the build info,** as `velox-gen version` reads it. §14's `Add` has no parameter for it, and both describe the same module.
9. **(call) `Add` does not change the `Dir`;** it returns the files. A second plan reads the directory again. It refuses an empty plan, another dialect, and a name outside `[a-z0-9_]+` or over 40 bytes.
10. **(call) PostgreSQL's lock key is the first 8 bytes of SHA-256 of the history table's name, as a big-endian `int64`,** fixed once shipped, as MySQL's name is (ruling 48). The wait is `lock_timeout` around `pg_advisory_lock`, so the lock is taken in the order asked; `55P03` is `ErrLockWait`.
11. **(call) SQLite's wait is `busy_timeout`,** set to the lock wait for the run and restored after; `SQLITE_BUSY` at `BEGIN IMMEDIATE` is `ErrLockWait`.
12. **(call) `applied_at` is written in UTC and read as text,** in a fixed layout per dialect, so no DSN needs `parseTime` and no session time zone changes it.
13. **(call) A failure in a file run in a transaction is a wrapped error,** `migrate: <path>: statement i of n: <driver error>; the file was rolled back`. §14 names no type for it, nothing is dirty, and `%w` keeps the driver's code for `errors.As`.
14. **(call) The runner records a failure through `context.WithoutCancel`, bounded to 10 seconds, and through a new connection when its own is gone.** A cancelled deploy is the commonest way an online build fails, and the dirty row must say why.
15. **(call) MySQL's `lock_wait_timeout` is whole seconds, rounded up; `0` sets back the session's own value,** which is also restored after each file. MySQL's minimum is 1 second, so "off" can only mean the server's default.
16. **(call) MariaDB is refused at the version check.** Its `@@version` passes 8.0.19 by number, but its DDL, `CHECK` and information schema differ from what the planner writes and reads.
17. **(call) `WithBaseline` and `apply -baseline` are plan 3's,** with baseline (Plans item 3); `Report.Baselined` is declared now and stays empty.
18. **(call) `Resolve` acts on a dirty version only.** Resolving a clean version would rewrite history; adopting a database is baseline's. `ResolveApplied` needs the file's checksum unchanged, so the history never records a file other than the one on disk; `ResolveUndone` does not, since editing the file is why one undoes.
19. **(call) `Status` returns the state with its ahead versions, ruling 54's refusals as errors with no state, and a dirty version as the state and a `*DirtyError` together,** so `status` shows a dirty version, its count and its error, even when its file was edited.
20. **(spec defect) `Drift` reads the database: `Drift(ctx context.Context, db *sql.DB, snapshot *Schema) (*DriftReport, error)`, replacing §14's `Drift(db, snapshot *Schema)`.** A drift check that cannot see the database cannot report what only the database knows: §11 asks it to warn on a non-InnoDB table, which a `Schema` cannot hold, and comparing two schemas in memory is `Diff`'s job already. So there is one exported drift function. It inspects the snapshot's tables through the unexported `inspect`, which returns the schema and notes on what the model has no place for (a MySQL engine, an invalid PostgreSQL index, a `NOT VALID` constraint, `GENERATED ALWAYS`), and compares through the unexported `drift(inspected, notes, snapshot)`, which tests call through `DriftOf`. The model stays plain data: unexported fields on it would be invisible to `Equal` and the JSON, and would surprise a user who builds or compares schemas. `Inspect` returns the schema alone. `velox-gen migrate drift`, `Verify` and the itests' invariants call `Drift`.
21. **(call) `Inspect` with `nil` leaves out `velox_migrations`** (spec defect: §11's "the history table is always left out", but `Inspect` is not given the Config). `drift`, `Verify` and the itests name their tables; `Verify`'s emptiness check leaves out the Config's table itself.
22. **(call, amended while executing) `Drift` reports definitions and is silent on spellings:** an index predicate or a non-enum check that differs from the snapshot's only in what the normaliser removes (a cast the database adds itself without changing the value, parentheses, quoting, white space, a `NOT` folded into its operator) is no difference, neither a change nor a warning; one whose normalised form differs is a change, so `Verify` fails on a hand-edited check; text the normaliser cannot parse is compared with its white space collapsed, and a difference there is a warning. An expression default that differs is a warning, since the normaliser cannot read every expression (§5). MySQL's `RESTRICT` and no action are one; SQLite's unnamed constraints match by definition; a primary-key difference is a warning, since `Diff` refuses it. (As first written, a spelling-only difference was a warning; Task 5 made it silent, which invariant 3 needs.)
23. **(call) The predicate normaliser parses the predicates velox users write and prints them with the parentheses precedence needs.** Stripping every parenthesis would make `a AND (b OR c)` equal `a AND b OR c`; text it cannot parse is compared with its white space collapsed, so it warns rather than passes.
24. **(call) A SQLite `integer PRIMARY KEY` without `AUTOINCREMENT` is not `Identity`:** it reuses a deleted top ID, which the model's identity never does (§4), so drift reports it.
25. **(call) A `UNIQUE` constraint is modelled as its unique index, and an index over an expression or a MySQL prefix is left out.** The model has unique indexes only (§8.2) and no expressions.
26. **(call) `Inspect` reads the enum types the inspected columns use.** A snapshot holds no unused type (`Diff` drops one), and reading every type would report other applications' types.
27. **(call) Every change type has `String`** (§14 addition): the CLI's `drift` and `Verify`'s error print changes, and one spelling serves both.
28. **(call) `migrate.LintFile`, and the destructive codes `delete-rows` and `update-rows`** (§14 addition; spec defect: §13 lints `TRUNCATE` and `DELETE`/`UPDATE` without `WHERE`, which no §8.1 code names, so no `allow` could cover them). The checks need the splitter, which only `migrate` has. Files with velox-gen's header are not classified: `diff` gated them, and their swaps and rebuilds would read as `alter-type` and `drop-table`.
29. **(call) CLI exits:** a rename naming nothing is 1 (well-formed, but refused); `status` with a dirty version is 1; `drift` on a database not at the directory's last version is 1; `-lock-timeout 0` writes no directive, so the files take `velox.json`'s at run time (a `"0s"` there turns it off).
30. **(call) PostgreSQL itests make a database per test, not a schema,** as plan 1's do: `schema-qualified` needs a `billing` schema beside the default one. MySQL's `billing` is a server-wide database, so its case creates and drops it around itself, and the database tests run serially.
31. **(call) The binding round-trips use a new generated package, `internal/bindmodel`,** not `internal/testmodel`: plan 3 moves `testmodel` onto the planner, and an entity of every type would change every test that counts its entities.
32. **(call) `Verify` fails on changes only,** as §10 says; warnings are spellings, which `drift` prints.
33. **(call) The `NO_BACKSLASH_ESCAPES` refusal checks every pending file before the first runs,** so a run never stops part-way over a mode it could have seen.
34. **(call) A MySQL file's `unit` lines group statements for resume as on PostgreSQL;** without them each statement is a unit, as §10 says.
35. **(spec defect) MySQL's row limit counts every column (ruling 47).** Ruling 47 counts only `varchar` and `varbinary`, but MySQL 8.4 counts every column toward its 65535 bytes: fixed-size types by their size, `text`, `blob` and `json` by their in-row pointer (9 to 12 bytes), `decimal` by its digits, and the NULL bitmap. A schema at ruling 47's limit, with any ID, would pass plan time and fail at `CREATE TABLE`. Task 11 counts as MySQL does, and Task 12's itest confirms the boundary on MySQL 8.4.
36. **(call) Every test this plan adds is in package `migrate_test`, reaching unexported code through `export_test.go` hooks,** listed in Global Constraints. Tests here need `cases`, `dirOf` and `freshDB`, which are `migrate_test`'s; two test packages in one binary would also declare `-update` twice, which panics at init.
37. **(call) The splitter finds the ends of literals, quoted names and dollar quotes itself.** Plan 1's `stringEnd` returns the end of the text both for a literal closed there and for an unterminated one, and `dollarEnd` returns 0 both for "not a dollar quote" and for an unclosed one; the splitter must report the unterminated ones.
38. **(call) Line endings never change a sum:** each file's hash is over its content with CRLF read as LF, and `velox.sum` itself is parsed with CRLF read as LF, so a checkout with `core.autocrlf` gives the same sums and a fresh `velox.sum`.
39. **(call) The test helper `extend` skips an empty plan and writes `schema.json` with hand files:** a step with no change writes no file, and a files-only step writes the current model's `schema.json`, so `ReadDir` accepts every directory the helpers make. `Dir.Add` still refuses an empty plan.
40. **(call) `reading` reports a dirty row first,** as the state and a `*DirtyError` together, whatever its file's checksum and also above the last file (never in `Ahead`); checksums are compared for clean rows only. Resuming needs the dirty file's checksum unchanged. `Resolve` reads the history itself, so `ResolveUndone` works after the file was edited, the usual recovery (edit, `hash`, `resolve -undone`, `apply`).
41. **(call) On SQLite the history table is created inside `BEGIN IMMEDIATE`,** so creating it waits on the lock and a busy database gives `ErrLockWait`, not a plain `SQLITE_BUSY`.
42. **(call) SQLite's `applied_at` column is `text`,** holding ruling 12's layout: modernc's driver turns a `datetime` column's text into a `time.Time`, which would scan back as RFC 3339, not the layout. PostgreSQL and MySQL keep §10's types.
43. **(call) An invalid PostgreSQL index is left out of `Inspect`'s schema and reported only as a note.** It serves no query and is dropped by the next resume, so the database does not have the index the snapshot names: `Drift` reports its `AddIndex` and warns why.
44. **(call) `LintFile` knows a generated file by `File`'s unexported `generated`,** set by `ParseFile` when the first line is `Dir.Add`'s header, since `ParseFile` drops comments.
