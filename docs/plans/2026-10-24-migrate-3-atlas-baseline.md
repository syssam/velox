# Migrate Plan 3: The Atlas Provider and Baseline — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two ways into velox's migrations for an application that has a schema already:
- `velox-gen migrate sql` prints the schema's DDL for Atlas's `external_schema`;
- `migrate.Baseline` and `velox-gen migrate baseline` adopt an existing database (ent's, old velox's, a hand-made one) into a new directory, and `apply -baseline N` (`WithBaseline`) records the adopted files on each existing environment.

Plus the documents for both paths.

**Architecture:**
- `migrate sql` is `NewPlan` from an empty schema to `internal/gen.Tables`, printed as plain statements. It is the same path `migrate diff` takes for a new directory, so the provider needs no renderer of its own.
- Render's PostgreSQL `CreateTable` learns to create the sequence a `serial` column owns. With that, any snapshot `Inspect` can produce, rendered from empty, builds a fresh database.
- `migrate.Baseline` reuses what plan 2 built:
  - the unexported `inspectWith` reads the tables and enum types that `want` names;
  - the drift comparison runs in an *adopt* mode, so an object that is equal to the schema's takes the schema's spelling and everything else keeps the database's;
  - `inspect`'s notes become warnings.
- The CLI writes the result through `NewDir`, `NewPlan(empty, snapshot)`, `Dir.Add("baseline")` and `writeAll`.
- `WithBaseline` is one more step in `Up`, under the lock, before the pending files.

**Tech Stack:** Go 1.27; `database/sql`; pgx v5, go-sql-driver/mysql and modernc SQLite in tests and velox-gen only. PostgreSQL 16 and MySQL 8.4 come from `docker-compose.ci.yml`.

**Spec:** `docs/specs/2026-10-23-migrate-design.md`. This plan covers:
- §12 Baseline;
- §18 The Atlas provider;
- the block **"Rulings on plan 3's builder check"** (just before "What is left to the builder"), which is binding and overrides §12 and §18 where they differ;
- Plans item 3;
- "Rulings made in plan 1" and "Rulings made in plan 2", which are binding facts about the code this plan builds on.

The builder check's findings are in that block. In this plan, "builder ruling N" means item N of that block. Where this plan's own rulings (at the end) differ from the spec, they stand.

## Global Constraints

- **Lean.** Migrations are not the focus; the ORM and GraphQL are (the user, 2026-10-24). The property test, `internal/testmodel` and the examples on the planner, `diff -check` in CI, drift through a scratch database and an Atlas cross-check are out of scope; Task 5 lists them under "Left by migrate plan 3". No task adds a per-dialect itest that `TestMigrateCases` already covers (builder ruling 2).
- **Dependency discipline.** "The root module's dependencies stay pgx, the MySQL driver and SQLite. `migrate` adds none." `go.mod` does not change. velox does not import Atlas.
- **`migrate` imports** the standard library, `github.com/syssam/velox2` and `internal/ddl` only (`TestImports`).
- **The model is a value.** `Baseline` never writes through `want`; it gets a `DoesNotMutate` test.
- **Deterministic output.** The same schema gives the same bytes from `migrate sql`. The same database and schema give the same files and warnings from `baseline`. Warnings and refusals are sorted by object.
- **The core is untouched.** No file of the root package changes, and `GeneratedCodeIsVersion4` stays 4.
- **Errors.**
  - `migrate`'s errors start with `migrate: `.
  - A refusal is an `*Error` with a `Code` that `migrate/doc.go` lists. Each task adds its codes.
  - A driver error is wrapped as `migrate: <what>: %w`.
  - velox-gen prints `velox-gen: migrate <subcommand>: <text without "migrate: ">`.
  - Messages carry no rule numbers.
- **Never return a connection to the pool.** Every connection `Baseline` and the runner take is closed with `discard`, as plan 2's are.
- **godoc.** `TestGodocCitesNoSpec` scans `migrate` and `cmd/velox-gen`: no comment or message cites a spec, a section or a ruling.
- **Exit codes** are velox-gen's: 0 success, 1 error, 2 misuse, 3 stale or drift.
- **Databases in tests.**
  - The variables are `VELOX_TEST_POSTGRES`, `VELOX_TEST_MYSQL_ADMIN` and `VELOX_TEST_MYSQL`.
  - A test skips cleanly without its variable, with `t.Skip` naming it. Under `CI=true` a missing variable fails the package, through the existing `TestMain`s.
  - SQLite needs no variable: it uses a file in `t.TempDir()`.
  - Database tests run serially, with no `t.Parallel`, because the machine is short of memory.
  - No new variable is added, so `scripts/ci-local.sh` and `.github/workflows/ci.yml` do not change.
- **Test packages.** `migrate`'s new tests are in package `migrate_test`. Unexported code is reached through hooks in `migrate/export_test.go`, added by the task that owns the code: `AdoptOf` in Task 2. The `-update` flag stays declared once per test binary: `migrate/lint_test.go` declares it for `migrate`, and Task 4 declares one for `cmd/velox-gen` in `migrate_baseline_test.go`.
- **The full gate** passes at every task's commit, run on the committed tree: `bash scripts/ci-local.sh` with Docker, rc=0. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **Shell.** Before any go command, `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2`. Run one heavy command at a time. The machine is memory-constrained: do not run the gate beside another `go test`, and do not kill other processes.
- **No machine-specific paths** in committed files.
- **Commits** are `Area: what changed`. They end with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Stage files by name and never stage `.DS_Store`. Never push.

## Review Focus

1. **Baseline pointed at the wrong database.** Examples: a fresh database, or another application's database that shares a table name. `baseline` must refuse when it finds none of the schema's tables, and `apply -baseline` must refuse on a database that holds no table but the history. Neither may write a directory or a row. Pins: Task 2 `TestBaselineNoTables`, Task 3 `TestUpBaselineEmptyDatabase`.
2. **`apply -baseline` run twice, or on a database velox already migrated.** The second run is refused and the history is unchanged, row for row. This is not a no-op that hides the mistake. Pins: Task 3 `TestUpBaselineTwice` and `TestUpBaselineMigrated`.
3. **Two environments' deploys running `apply -baseline` at once.** Example: two pods against one database. Exactly one records the rows; the other is refused, and no version is recorded twice. Pin: Task 3 `TestUpBaselineConcurrent`, on SQLite through a file, and on PostgreSQL and MySQL under their variables.
4. **A database holding more than the schema names.**
   - An unknown table that references a schema table (an `audit` table) is not read, warned about or refused.
   - A schema table's index the schema lacks is kept in the snapshot, so the next `diff` proposes dropping it as a review change.

   Pin: Task 2 `TestBaselineUnknownTables`.
5. **Anything but DDL on `migrate sql`'s stdout.** Atlas parses stdout as SQL, so a stray line fails the user's `atlas migrate diff` with a parse error far from the cause. Pins, in Task 1 `TestMigrateSQL`:
   - on success, stderr is empty and stdout splits into exactly the plan's statements;
   - on a refusal, stdout is empty.

---

### Task 1: `velox-gen migrate sql`, the Atlas provider

**Files:**
- Create: `cmd/velox-gen/migrate_sql.go`, `cmd/velox-gen/migrate_sql_test.go`.
- Modify:
  - `cmd/velox-gen/migrate.go`: `case "sql"` in `runMigrate`, and the "needs a subcommand" list.
  - `cmd/velox-gen/main.go`: the usage line in the package doc.
  - `cmd/velox-gen/testdata/usage.golden`.
  - `cmd/velox-gen/migrate_test.go`: `TestMigrateUsage`'s subcommand list and its `-h` loop.
  - `migrate/doc.go`: a paragraph "Atlas" with the `atlas.hcl` below.
  - `example/README.md`: replace the "Tables are created with plain DDL. Planning migrations … is a later velox2 sub-project." bullet with a "Migrations" section. It names the two paths and their exclusivity, and gives the `atlas.hcl`. Task 5 adds baseline to it.

**Interfaces:**
- Consumes:
  - `gen.Load`, `gen.Build` and `gen.Tables`;
  - `migrate.NewDir`, `migrate.NewPlan` and `migrate.MainScript`;
  - velox-gen's `newFlagSet`, `parseFlags`, `tooMany`, `schemaPackage`, `relErrors`, `relMigrate` and `dialectFlag`.
- Produces:

```go
// runMigrateSQL is velox-gen migrate sql: the CREATE statements of the
// schema package on stdout, each followed by ";\n", for Atlas.
func runMigrateSQL(args []string, dir string, env []string, stdout, stderr io.Writer) int
```

- **Usage:** `velox-gen migrate sql -dialect postgres|mysql|sqlite [package]`, described as "print the schema package's DDL, for Atlas's external_schema".
  - Its flag set has `-dialect` alone, with no `-dir`. It does not use `newMigrateCmd`; it prints errors as `velox-gen: migrate sql: …`.
  - A missing `-dialect` is exit 2: `velox-gen: migrate sql needs -dialect postgres, mysql or sqlite`. So are an unknown dialect, an unknown flag and two packages.
- **Body:**
  1. Load, build and `Tables` as `runMigrateDiff` does. A refusal prints `velox-gen: <relErrors>` and exits 1.
  2. `plan, err := migrate.NewPlan(empty, to, migrate.PlanOptions{})`, where `empty` is `NewDir(Config{Dialect})`'s `Snapshot`.
  3. Fail with exit 1 unless the plan has exactly one script, of `MainScript`, whose `Directives` is the zero value: `velox-gen: migrate sql: the plan from an empty database is not one plain script` (builder ruling 1).
  4. Build all of the output in a `strings.Builder` first, as `s.SQL + ";\n"` per statement, and write it to stdout in one call, only on success. `Review` lines and units are not printed.
- **`atlas.hcl`**, exactly as builder ruling 3 gives it, in `migrate/doc.go` (indented as code) and in `example/README.md`:

```hcl
data "external_schema" "velox" {
  program = ["go", "tool", "velox-gen", "migrate", "sql", "-dialect", "postgres", "./schema"]
}

env "velox" {
  src = data.external_schema.velox.url
  dev = "docker://postgres/16/dev?search_path=public"
  migration { dir = "file://migrations" }
}
```

  With it, the documents say:
  - run `go get -tool github.com/syssam/velox2/cmd/velox-gen` first, and run `atlas` from the module root;
  - for a schema that uses PostgreSQL schemas (`schema.TableIn`), use `docker://postgres/16/dev`, which is database-scoped;
  - MySQL is `docker://mysql/8.4/dev` with `-dialect mysql`, and SQLite is `sqlite://dev?mode=memory` with `-dialect sqlite`;
  - the commands are `atlas migrate diff --env velox` and `atlas migrate apply --env velox -u <url>`;
  - velox's own `migrate` and Atlas are alternatives for one directory, and a directory is managed by one of them.

- [ ] **Step 1: Write the failing tests** in `migrate_sql_test.go`. Each builds a module, so each skips under `-short`.
  - `TestMigrateSQL`:
    - Write a `tempModule` with this schema:
      - `User`: a `field.Enum("role").Values("admin", "member")`, a unique `field.String("email")`, a `Comment`, and an `index.Fields("role")`;
      - `Post`: an edge `From("author", User.Type)`.
    - For each dialect:
      - `migrate sql -dialect <d>` exits 0 with empty stderr (Review Focus 5).
      - Run `migrate diff -dialect <d> -dry-run -dir fresh`. Take the text after its first `== fresh/0001_` line, up to the next `== `, and `ParseFile` it.
      - The `sql` output equals `strings.Join(file.Statements, ";\n") + ";\n"`.
    - On PostgreSQL the output starts with `CREATE TYPE` and holds `COMMENT ON`.
    - Two runs give identical bytes.
  - `TestMigrateSQLRefusals`:
    - no `-dialect`, `-dialect oracle`, `-dir x` and two packages each exit 2 with nothing on stdout;
    - a MySQL schema with `index.Fields("email").Where("email <> ''")` exits 1 with the file:line refusal on stderr and nothing on stdout (Review Focus 5).
  - `TestMigrateUsage` (`migrate_test.go`): add `sql` to the "needs a subcommand" text, and leave `sql` out of the `-h` loop that asserts `-dir string`. Instead, `migrate sql -h` exits 0 and shows `Flags of migrate sql:` and `-dialect`.
- [ ] **Step 2: Run them.** Run `go test ./cmd/velox-gen/ -run 'MigrateSQL|MigrateUsage'`. Expected: FAIL with `unknown subcommand "sql"`.
- [ ] **Step 3: Implement** `runMigrateSQL`, the `runMigrate` case and the usage lines. Write the two documents' Atlas text.
- [ ] **Step 4: Run** `go test ./cmd/velox-gen/ && go test -run TestGodocCitesNoSpec .`. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `velox-gen: migrate sql, the schema's DDL for Atlas`.

### Task 2: Render's owned sequences, and `migrate.Baseline`

**Files:**
- Create: `migrate/baseline.go`, `migrate/baseline_test.go` (package `migrate_test`).
- Modify:
  - `migrate/render_postgres.go` (`pgCreate`);
  - `migrate/render_postgres_test.go`;
  - `migrate/drift.go`: the drifter's adopt mode;
  - `migrate/inspect.go`: `note.baseline`;
  - `migrate/inspect_postgres.go`: three baseline notes;
  - `migrate/export_test.go`: `AdoptOf`, and `Note.Baseline`;
  - `migrate/itest_test.go` and `migrate/inspect_postgres_test.go`: drop the hand files `ownedSequences` and `predicateDir` write, since Render now writes them;
  - `migrate/doc.go`: `Baseline` in the overview, and code `"baseline"`.

**Interfaces:**
- Consumes:
  - plan 2's `inspectWith`, `note`, `drifter`, `drift`, `Schema.validate`, `Schema.clone`, `Schema.sort` and `discard`;
  - plan 1's `renderer`.
- Produces:

```go
// Baseline reads the tables and enum types want names from db, a database
// of want's dialect, and returns them as a snapshot for a new migration
// directory, with warnings.
func Baseline(ctx context.Context, db *sql.DB, want *Schema) (*Schema, []Warning, error)

// adopt is Baseline after inspecting: inspected's tables that want names,
// each object in want's spelling where drift's comparison finds it equal,
// in inspected's otherwise; the notes on those tables as warnings; and the
// refusals, joined.
func adopt(inspected *Schema, notes []note, want *Schema) (*Schema, []Warning, error)

// note gains:
	baseline bool // only Baseline reports it: what a build from the snapshot loses, not a difference; drift skips it

// export_test.go:
type Note struct{ Table TableRef; Object, Message string; Baseline bool } // Baseline added
func AdoptOf(inspected, want *Schema, notes ...Note) (*Schema, []Warning, error) // adopt
```

- **Render (builder ruling 5).** In `pgCreate`, for each column with `!c.Identity && c.Sequence != ""`, in column order:
  - before the `CREATE TABLE`: `CREATE SEQUENCE <seq> AS <c.Type>`, for a `smallint`, `integer` or `bigint` column; a column of another type gets no `AS`;
  - after the `CREATE TABLE` and before its indexes: `ALTER SEQUENCE <seq> OWNED BY <table>.<col>`.

  `<seq>` is the sequence in the table's schema, quoted as `r.table` quotes a table. The column's `DEFAULT nextval('s')` is unchanged. MySQL and SQLite never have such a column.
- **`Baseline`:**
  - A nil `want` or `db` is Code `"nil"`.
  - It reads with `inspectWith(ctx, db, want.Dialect, <want's table refs, not nil>, <want's enum refs>)` and returns `adopt`.
- **`adopt`** (builder rulings 4 and 6):
  - **Missing tables.** A table of `want` that is not in `inspected` gives a warning: `Object` is the table, and `Message` is `the database has no table <t>; the next migrate diff creates it`. The table is left out.
  - **No tables found.** When none of want's tables is found, it returns an `*Error` with Code `"baseline"`: `the database holds none of the schema's tables; use migrate diff to start a directory`.
  - **The comparison.** It runs the drift comparison with `drifter.adopt = true`, on the copy that drift makes. In adopt mode:
    - Every place where `drifter.table` now forces the snapshot's form over a difference keeps the database's form, and still warns. These places are: the primary key and its name, a default that is not the same, and a predicate or check that cannot be parsed. Only forms the comparison calls the same take want's text.
    - An inspected index with no index of its name in `want` takes the `Where` text of the `want` index that has its columns, its uniqueness and the same normalised predicate. It keeps its own name, so the next `diff` renames it by definition.
    - The MySQL implicit foreign-key index stays in the snapshot: it is in the database.
  - **The result.** It is a clone holding only want's found tables and the enum types they use. `Dialect` is want's, the result is sorted, and it is checked with `validate("the baseline")`.
  - **Refusals.** Each is an `*Error` with Code `"baseline"` and `Object` set. All of them are collected and returned with `errors.Join`, sorted by object, so one run names them all:
    - a foreign key whose referenced table is not in the result: `<t>.<fk>: references <ref>, which the schema does not name; add the table to the schema, or drop the key first`;
    - on PostgreSQL, a column whose default is `nextval(…)` with `Sequence == ""`: `<t>.<c>: the default uses the sequence <s>, which the column does not own; run ALTER SEQUENCE <s> OWNED BY <t>.<c>, then baseline again`.
  - **Warnings.** The warnings are every note on a result table, baseline notes included, plus the comparison's warnings, sorted.
- **Baseline notes** (`baseline: true`; `drift` skips them):
  - **A unique constraint read as an index:** `the unique constraint <name> is read as its index; to drop it later, replace it with a unique index by hand first`. This note is added on every dialect where `Inspect` folds a constraint into its index.
  - **An owned sequence with other options (PostgreSQL):** `the sequence <s> starts at <n> and increments by <m>; 0001_baseline.sql creates it with the defaults`. It applies to an identity's or a serial's sequence whose `pg_sequence.seqstart` or `seqincrement` is not 1.
  - **A column type from an extension (PostgreSQL):** `the type <t> comes from the extension <e>; add CREATE EXTENSION <e> to 0001_baseline.sql before the tables, then run migrate hash`. It is found through `pg_depend` with `deptype = 'e'` on the column's type, and given once per extension per table.

- [ ] **Step 1: Write the failing tests.**
  - `render_postgres_test.go`, `TestRenderOwnedSequence`:
    - From empty to a table `g` with columns `id integer` (`Default: "nextval('g_id_seq')"`, `Sequence: "g_id_seq"`) and `n bigint`, Render gives these statements in order:
      1. `CREATE SEQUENCE "g_id_seq" AS integer`;
      2. the `CREATE TABLE` with `DEFAULT nextval('g_id_seq')`;
      3. `ALTER SEQUENCE "g_id_seq" OWNED BY "g"."id"`.
    - The same table in schema `billing` qualifies all three.
    - An identity column with a `Sequence` gives no `CREATE SEQUENCE`.
    - Rewrite the render goldens with `-update`. Every changed golden file must hold such a column; read each one.
  - `itest_test.go` and `inspect_postgres_test.go`: `TestMigrateCases` and `TestInspectPostgresCases` still pass once their hand sequence files are removed. That proves the rendered pair builds what `Inspect` reads back.
  - `baseline_test.go`, through `AdoptOf`, with no database:
    - `TestAdoptSpellings`: an inspected PostgreSQL index named `user_active`, with `Where "(active = true)"`, against a `want` index `users_active` with `Where "active = true"` on the same columns. The snapshot index is named `user_active` and has `Where "active = true"`. The same holds for an equal check and an equal number default (`0.00` against `0`).
    - `TestAdoptKeepsDifferences`:
      - a predicate that differs after normalising keeps the database's text and gives the comparison's warning;
      - a differing primary key keeps the database's columns and warns;
      - a varchar column where want has an enum keeps `character varying`, with no warning (it is a change, which the next `diff` plans).
    - `TestAdoptRefusals`: a foreign key to `orgs` and a not-owned `nextval` both appear in one joined error. `errors.As` finds an `*Error` with Code `"baseline"`, and each message is the text above.
    - `TestBaselineNoTables` (Review Focus 1): want names `users`, and inspected holds only `audit`, so the result is Code `"baseline"`, with no snapshot. A missing second table gives the warning and is left out.
    - `TestAdoptNotes`: a `Baseline: true` note becomes a warning from `AdoptOf`, and is absent from `DriftOf`'s report for the same inputs.
    - `TestBaselineDoesNotMutate`: `want` is deep-equal before and after `AdoptOf`.
  - `baseline_test.go`, on SQLite (always) through `Baseline`:
    - `TestBaselineUnknownTables` (Review Focus 4): create `users`, an extra index on `users`, and `audit` with a foreign key to `users`. `Baseline` with want `{users}` gives a snapshot with `users` and its extra index, no `audit`, and no warning that names `audit`.
    - `TestBaselineSQLiteVerify`: a hand-made database that matches a two-table schema gives a snapshot. Verify then holds: `NewPlan(empty, snap)`, then `Dir.Add`, then `Verify` on a fresh file, passes.
  - `baseline_test.go`, under `VELOX_TEST_POSTGRES`, `TestBaselinePostgres`:
    - a `serial` column, an identity with `START WITH 1000`, a `citext` column (if `CREATE EXTENSION citext` fails, the extension sub-test skips), a `UNIQUE` constraint, a `TableIn("billing")` table and a not-owned `nextval` in a second database;
    - the first database gives the three baseline-note warnings;
    - its snapshot, planned from empty, applied to a fresh database and then passed through `Drift`, has no change;
    - the second database is refused, naming the column.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ -run 'RenderOwnedSequence|Adopt|Baseline'`. Expected: FAIL (`Baseline` and `AdoptOf` undefined; no `CREATE SEQUENCE` rendered).
- [ ] **Step 3: Implement** the Render change, the notes, adopt mode and `Baseline`, as the Interfaces say. Add `"baseline"` to `doc.go`'s codes: "Baseline found none of the schema's tables, a foreign key to a table outside them, or a sequence default the column does not own; or Up was given WithBaseline where it cannot record".
- [ ] **Step 4: Run** `go test ./migrate/` with the database variables. Expected: PASS, with `TestMigrateCases`, `TestInspectPostgresCases` and every drift test unchanged.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: Render creates owned sequences; Baseline adopts a database`.

### Task 3: `WithBaseline` and `apply -baseline`

**Files:**
- Modify:
  - `migrate/runner.go`: `WithBaseline`, the options, and the baseline step in `Up`;
  - `migrate/history.go`: `hasTables`;
  - `migrate/runner_test.go` and `migrate/runner_db_test.go`: the tests;
  - `cmd/velox-gen/migrate_apply.go`: `-baseline`;
  - `cmd/velox-gen/main.go` and `testdata/usage.golden`: the `apply` line gains `[-baseline version]`;
  - `cmd/velox-gen/migrate_test.go`: `TestMigrateApplyBaseline`.

**Interfaces:**
- Consumes: plan 2's `session`, including `prepare`, `history`, `record`, `begin`, `rollback`, `lock` and `reading`; Task 2's `"baseline"` code.
- Produces:

```go
// WithBaseline makes Up record the files up to version as applied, without
// running them, before it applies the files above it ...
func WithBaseline(version uint64) Option

// options gains:
	baseline    uint64
	baselineSet bool // 0 is a version: 0000 is a valid file

// hasTables reports whether the database holds a table besides the
// history: PostgreSQL, an ordinary or partitioned table outside pg_catalog,
// information_schema and pg_toast; MySQL, a base table of DATABASE();
// SQLite, a table of main not named sqlite_%. The history table is left out.
func (s *session) hasTables(ctx context.Context) (bool, error)
```

- **In `Up`**, when `o.baselineSet`, these checks come before `open`. Each is an `*Error` with Code `"baseline"`:
  - `o.resume` is set: `WithBaseline cannot resume a dirty version`;
  - `o.target != 0 && o.target < o.baseline`: `the target <t> is below the baseline <n>`;
  - no file of the directory has version `o.baseline`: `the directory has no file of version <n>`.
- **Under the lock**, after `prepare` (which creates the history table):
  1. If the history has any row: `the history already has versions; WithBaseline adopts a database once, run Up without it`.
  2. If `!hasTables`: `the database is empty; run Up without WithBaseline`.
  3. Record the rows in one transaction. PostgreSQL uses `BEGIN`, MySQL `START TRANSACTION`, and SQLite `s.begin` (`BEGIN IMMEDIATE`), after which it re-reads the history and refuses as in item 1 if any row is there.
     - It inserts one `Applied` per file with `Version <= o.baseline`: `Name`, `Sum` (the file's current sum), `AppliedAt` now, `Duration 0`, `Dirty false`, `Statements 0` and `Error ""`.
     - Then it commits. A failure rolls back and returns `migrate: baselining: %w`.
  4. Set `Report.Baselined` to those versions, in order, and log each through `WithLogger`.
  5. Continue as `Up` does, with `reading` over the new rows, so the files above the baseline are pending, up to `WithTarget`.
- **`Status` and `Resolve` ignore `WithBaseline`.**
- **CLI.** `apply -baseline version` uses a flag type that records whether it was given (`flag.Func` into a `uint64` and a `bool`).
  - Exit 2, before any connection: `-baseline` with `-resume` (`-baseline and -resume cannot be combined`), and `-to` given and below `-baseline` (`-to <t> is below -baseline <n>`).
  - Output: `baselined <path>` for each baselined version (`c.path(filePath(d, …))`), then the `applied …` lines.
  - `-dry-run` reads `Status`:
    - a history with any row is exit 1 with the library's text;
    - otherwise it prints `would baseline <path>` per file up to N, then the `== <path>` blocks of the files above N, up to `-to`.
    - A SQLite file that does not exist is exit 1 with `the database is empty; run apply without -baseline`.

- [ ] **Step 1: Write the failing tests.**
  - `runner_test.go`, on SQLite:
    - `TestUpBaseline`:
      - The directory has files 1, 2 and 3. A database holds a table `t`, created by hand, and no history.
      - `Up(WithBaseline(2))` gives `Baselined [1 2]` and `Applied` holding version 3.
      - The history rows 1 and 2 have the files' sums, `duration_ms 0`, `statements 0`, and are not dirty.
      - `Status` is clean.
      - `WithBaseline(0)` on a directory whose first file is `0000_x.sql` baselines version 0.
    - `TestUpBaselineRefusals`: Code `"baseline"` and the exact text for `WithResume`, for `WithTarget(1)` with `WithBaseline(2)`, and for a version with no file. Each is checked before `open`: afterwards the SQLite database has no history table.
    - `TestUpBaselineEmptyDatabase` (Review Focus 1): a database with no table is refused. The history table exists afterwards and has no row.
    - `TestUpBaselineTwice` and `TestUpBaselineMigrated` (Review Focus 2):
      - a second `Up(WithBaseline(1))` is refused, and the rows are deep-equal to before;
      - a database migrated by `Up` alone is refused the same way.
    - `TestUpBaselineConcurrent` (Review Focus 3): two goroutines run `Up(WithBaseline(1))` on one SQLite file through two `*sql.DB`s. Exactly one returns `Baselined [1]`. The other returns Code `"baseline"`, or a lock-wait error. The history has one row.
  - `runner_db_test.go`: `TestUpBaselineConcurrent` and `TestUpBaseline` on PostgreSQL and MySQL under their variables.
  - `migrate_test.go`, `TestMigrateApplyBaseline` (SQLite, in a built module):
    - `apply -baseline 1` prints `baselined migrations/0001_….sql` and the `applied` line of 0002;
    - a second run exits 1;
    - `-baseline 1 -resume` and `-to 1 -baseline 2` exit 2;
    - `-dry-run -baseline 1` prints `would baseline …` and file 2's block.
- [ ] **Step 2: Run them.** Run `go test ./migrate/ ./cmd/velox-gen/ -run 'Baseline'`. Expected: FAIL (`WithBaseline` undefined).
- [ ] **Step 3: Implement** `WithBaseline`, `hasTables`, the baseline step and the CLI flag, as the Interfaces say. Write `WithBaseline`'s godoc with the refusals listed above. It also says to run it once per existing environment, and that an edit to a baselined file is then a `*ChecksumError`, as for any applied file.
- [ ] **Step 4: Run** `go test ./migrate/ ./cmd/velox-gen/` with the database variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Migrate: WithBaseline and apply -baseline`.

### Task 4: `velox-gen migrate baseline`, and the ent fixture

**Files:**
- Create:
  - `cmd/velox-gen/migrate_baseline.go`;
  - `cmd/velox-gen/migrate_baseline_test.go`, which declares `var update = flag.Bool("update", false, "write the golden files")`;
  - `cmd/velox-gen/testdata/ent/schema/{user,post}.go`;
  - `cmd/velox-gen/testdata/ent/{postgres,mysql}.sql`;
  - `cmd/velox-gen/testdata/ent/want/{postgres,mysql}/{0001_baseline.sql,schema.json,diff.sql,warnings.txt}`.
- Modify:
  - `cmd/velox-gen/migrate.go`: `case "baseline"`, and the subcommand list;
  - `cmd/velox-gen/main.go` and `testdata/usage.golden`: `velox-gen migrate baseline [-dir ./migrations] -dialect d [-db dsn] [package]`, described as "adopt an existing database into a new migration directory";
  - `cmd/velox-gen/migrate_test.go`: `TestMigrateUsage` loses its `"baseline"`-is-unknown case, and `baseline` joins the `-h` loop;
  - `migrate/dir.go`: the header.

**Interfaces:**
- Consumes:
  - Task 2's `migrate.Baseline`;
  - plan 2's `newDBCmd`, `dsn`, `open` (with `existing` true), `dirState`, `makeDirs`, `writeAll`, `relErrors`, `relMigrate` and `signalContext`;
  - `NewDir`, `NewPlan` and `Dir.Add`.
- Produces:

```go
// runMigrateBaseline is velox-gen migrate baseline: it reads the schema
// package's tables from the database and writes a new directory whose
// 0001_baseline.sql builds them.
func runMigrateBaseline(args []string, dir string, env []string, stdout, stderr io.Writer) int
```

- **Steps of the command:**
  1. A missing `-dialect` is exit 2, as is a malformed `-db`.
  2. `dirState(abs) == migrationDir` is exit 1: `<dir> is a migration directory already; baseline starts a new one`. A missing directory, or one with only other files, is accepted.
  3. Load, build and `Tables` for `-dialect`. Refusals are exit 1, through `relErrors`.
  4. Open the database with `existing` true, then call `migrate.Baseline`. An error is exit 1, with each joined line printed as `velox-gen: migrate baseline: <line>`.
  5. Run `NewDir(Config{Dialect})`, then `NewPlan(d.Snapshot, snap, PlanOptions{LockTimeout: d.Config.LockTimeout})`, then `d.Add("baseline", plan, snap)`. Write the result with `makeDirs` and `writeAll`, and print the paths as `diff` prints them.
  6. Print each warning to stderr as `velox-gen: migrate baseline: warning: <Object>: <Message>`, then this line: `velox-gen: migrate baseline: run velox-gen migrate apply -baseline 1 once on each existing database, then migrate drift there`. Exit 0.
- **The header** (builder ruling 9): `Dir.Add` writes `migrate baseline` in place of `migrate diff` in the header when it adds the first file of a directory with no files under the name `baseline`. This is the plan's ruling 4.
- **The fixture** (builder rulings 8 and 11). It is hand-written ent-shaped DDL with no ent dependency, and a schema package that declares the same columns:
  - `users`:
    - `id`: `bigserial` on PostgreSQL, `bigint AUTO_INCREMENT` on MySQL;
    - `name varchar(255) NOT NULL`;
    - `email varchar(255) NOT NULL UNIQUE`;
    - `role`: `character varying NOT NULL` with no check on PostgreSQL, `enum('admin','member') NOT NULL` on MySQL;
    - an index `user_name` on `name`.
  - `posts`:
    - `id`: ent's identity on PostgreSQL, `bigint AUTO_INCREMENT` on MySQL;
    - `title`;
    - `user_posts bigint NULL`, with the foreign key `posts_users_posts` to `users(id) ON DELETE SET NULL`.
  - The MySQL tables use ent's defaults: `utf8mb4` and `utf8mb4_bin`.
  - The schema uses `field.Enum("role").Values("admin", "member")`, `field.String("email").Unique()`, `index.Fields("name")`, and `edge.To("posts", Post.Type)` with `StorageKey(edge.Column("user_posts"))`.

- [ ] **Step 1: Write the failing tests** in `migrate_baseline_test.go`.
  - `TestMigrateBaselineSQLite` runs with no variable:
    - in a module with `userSchema(field.String("name"))` and a SQLite file holding `users` as velox makes it, `baseline -dialect sqlite -db <file>` writes `migrations/0001_baseline.sql`, `schema.json`, `velox.json` and `velox.sum`, and exits 0;
    - the header of `0001` names `migrate baseline`;
    - `migrate diff` then prints `no changes`;
    - a second `baseline` exits 1 with the "already" text;
    - an empty database exits 1 with "none of the schema's tables";
    - no `-dialect` exits 2;
    - a missing file exits 1 with `no such database`.
  - `TestMigrateBaselineEnt`, for PostgreSQL and MySQL under their variables. For each dialect, `testDSN` gives the original database, which gets `ent/<d>.sql`. Then:
    1. `baseline` writes the directory, and its `0001_baseline.sql`, its `schema.json` and its stderr warnings equal `want/<d>/…`. The PostgreSQL warnings include the `users_email_key` unique-constraint note.
    2. `migrate.Verify` on a second `testDSN` database, with the written directory, returns nil.
    3. `apply -baseline 1 -db <original>` prints `baselined migrations/0001_baseline.sql`, and `status` exits 0 with nothing pending.
    4. `migrate diff -dry-run -allow alter-type=users.role` against the written directory (`-allow` names the code `Lint` gives the PostgreSQL varchar-to-enum change) gives output equal to `want/<d>/diff.sql`.
    5. Every change in it is one of these: a rename of an index or foreign key; on PostgreSQL, `CreateEnum` with the `role` alter-type and its check, and `serial-to-identity` on `users.id`. On MySQL there is no enum change. The test asserts that the change kinds are only these.

    If a change of another kind appears, stop and report it as a finding against Task 2's adoption. Do not change the golden to accept it.
  - Goldens are written by `go test ./cmd/velox-gen/ -run TestMigrateBaselineEnt -update` with the variables set. Read every file before committing.
- [ ] **Step 2: Run them.** Run `go test ./cmd/velox-gen/ -run 'MigrateBaseline|MigrateUsage'`. Expected: FAIL with `unknown subcommand "baseline"`.
- [ ] **Step 3: Implement** `runMigrateBaseline`, the header rule, the usage lines and the fixture.
- [ ] **Step 4: Run** `go test ./cmd/velox-gen/ ./migrate/` with the variables. Expected: PASS.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `velox-gen: migrate baseline, with the ent fixture`.

### Task 5: The documents

**Files:**
- Modify:
  - `migrate/doc.go`: a paragraph "Adopting a database": `Baseline`, `WithBaseline`, and the order of the steps;
  - `example/README.md`: the "Migrations" section of Task 1 gains "Adopting an existing database";
  - `docs/plans/2026-10-14-core-1-followups.md`: mark "Left by migrate plan 2"'s baseline bullet and documents bullet resolved, with their tasks, and add "Left by migrate plan 3";
  - `docs/specs/2026-10-23-migrate-design.md`: add a section "Rulings made in plan 3", with this plan's rulings and those found while executing it, after "Rulings made in plan 2".

**Interfaces:** none; the documents must match the code of Tasks 1–4.

- The adoption text, in both documents, says these things in order:
  1. Run `velox-gen migrate baseline -dialect d -db <one environment>`. Read its warnings: an extension to add by hand, sequence options it does not keep, unique constraints read as indexes.
  2. `0001_baseline.sql` may be edited, followed by `velox-gen migrate hash`, only before any environment runs `apply -baseline`. After that, an edit is a checksum error on every baselined database.
  3. Commit the directory, and run `velox-gen migrate apply -baseline 1` once on each existing environment. It refuses a history that is not empty and a database with no tables.
  4. Run `velox-gen migrate drift` on each environment, because baseline read only one.
  5. Run `velox-gen migrate diff` as usual. An ent database's names come back as renames, and its varchar enums as destructive changes that need `-allow`.
- "Left by migrate plan 3" holds:
  - the items the user's ruling of 2026-10-24 moved out of plan 3: the property test, `internal/testmodel` and the examples on the planner, `diff -check` in CI, drift through a scratch database, and an Atlas cross-check;
  - a SQLite ent fixture;
  - MySQL `AUTO_INCREMENT` start values, which baseline does not see (the plan's ruling 6);
  - what executing this plan found.

- [ ] **Step 1: Write the documents.** Check each command, flag and message against the code.
- [ ] **Step 2: Run** `go vet ./migrate/ && go test -run TestGodocCitesNoSpec .`. Expected: PASS.
- [ ] **Step 3: Run the full gate.** Expected: rc=0.
- [ ] **Step 4: Commit** `Docs: the Atlas provider and adopting a database; plan 3's rulings recorded`.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call) `migrate sql` takes no `-dir`.** It reads no directory, and a flag that does nothing would mislead an `atlas.hcl` author. So it has its own flag set, not `newMigrateCmd`'s.
2. **(call) `migrate sql` writes stdout once, only on success.** Atlas reads stdout as SQL, so a half-written output is worse than none.
3. **(call) Baseline's comparison is drift's, in an adopt mode, not a second comparer.** Builder ruling 4 says the snapshot takes the schema's form "wherever drift() finds an object equal", and one comparer keeps the two from disagreeing. Adopt mode inverts drift's forced alignments: where drift takes the snapshot's form so that `Diff` can report the rest, baseline keeps the database's form, because the snapshot must describe the database.
4. **(call) `Dir.Add` names `migrate baseline` in the header when it adds the first file of an empty directory under the name `baseline`.** §14's `Add` has no parameter for the subcommand, and the sum must cover the header as written, so the CLI cannot rewrite it afterwards.
5. **(call) Baseline's refusals are all collected and joined.** One run of `baseline` against an ent database then names every foreign key to an unknown table and every unowned sequence, instead of one per run.
6. **(call) What 0001 loses is a baseline note, which drift skips.** These are an owned sequence's start and increment, an extension's type, and a unique constraint folded into its index. They describe what a build from the snapshot loses, not how the database differs from it, and drift would repeat them on every run. They are PostgreSQL's, except the unique constraint note. A MySQL table's `AUTO_INCREMENT` is its next value, not its start, so it is not reported.
7. **(call) Every `WithBaseline` refusal is Code `"baseline"`.** This holds before the run and under the lock alike. The checks that need no database run before `open`, so a misuse never takes the lock.
8. **(call) "Empty" for `apply -baseline` means no table in the connection's database besides the history.** On PostgreSQL, any user schema counts, so an application that keeps its tables in `schema.TableIn` schemas is not refused. On MySQL, only `DATABASE()` counts, since the other databases on the server are not the application's.
9. **(call) Baseline reads only `want`'s tables and enum types, never `nil`.** An unknown table, even one that references a schema table, is not read, so it can never be refused or warned about.
10. **(call) The ent fixture is in `cmd/velox-gen`, through the CLI.** The proof is the command a user runs. `migrate.Baseline`'s own tests in `migrate` cover the library without a module build.
