# Pre-1.0 Hardening, Wave 4: migrate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0 freezes the snapshot format and the generated DDL, migrate fixes what the triage found wrong on adopted and dumped databases:
- a PostgreSQL unique constraint is held as one (`Index.Constraint`), so dropping it on an ent-adopted database works, and a rename names the constraint (M22);
- inspection sees an extension's type behind an array or a domain (M23), and a primary key's index attributes (M1);
- one object gets one warning (M2);
- the history table is qualified, so a pg_dump file that empties `search_path` records itself (M14);
- `BEGIN ATOMIC`, and a psql meta-command line such as pg_dump 18's `\restrict`, get refusals that name them (M13's message; check finding 10);
- MySQL binary defaults are read exactly, from `SHOW CREATE TABLE`, and ASCII introducers stop drifting (M3, M4);
- a DSN naming an unreadable file says so (M7).

**Architecture:** Eight tasks, each one commit that passes its check.
- Tasks 1–3 are the inspect_postgres.go items, in sequence (builder ruling 20): M23 (the columns query), M1 (the indexes query), then M22 (the indexes query again, the model, Render and Dir.Add).
- Task 4 merges notes per object in `compare`, which Task 3 makes easier to see.
- Tasks 5–8 are independent of each other: the runner (M14), the splitter (M13 and psql meta-commands), MySQL defaults and predicates (M3, M4), and the CLI (M7).
- Only the root module changes, in `migrate/` and `cmd/velox-gen/`. No velox-gen output changes, and no generated-code version moves.

**Tech Stack:** Go 1.27. pgx v5.11.0, go-sql-driver/mysql v1.10.1, modernc sqlite v1.60.1 (go.mod). golangci-lint v2.14.0, revive v1.17.0 (`scripts/ci-local.sh`). Docker for PostgreSQL and MySQL (docker-compose.ci.yml).

**Spec:** `docs/history/specs/2026-10-25-pre-1.0-hardening-design.md`. Its final block, **"Rulings on the builder check"**, is binding and overrides the rulings above it.
- "Spec ruling N" is item N of the spec's Rulings, and "builder ruling N" is item N of the final block.
- The evidence for each id is in `docs/history/reviews/2026-10-25-triage-M.md`.
- The migrate design is `docs/history/specs/2026-10-23-migrate-design.md`, with plans `docs/history/plans/2026-10-2{3,4}-migrate-*.md`.
- Every file:line below was read at c33cf95.
- The builder check of this plan (at 7a27429, `scratchpad/hardening-4-plan-check.md`) ran the [D] probes on PostgreSQL 18.6 and MySQL 8.4.13. Its findings are cited as "check finding N", and the controller's rulings on them are under "Controller rulings". Where a [D] value below was measured by the check, it says "measured".

**Scope.** The spec's wave split names Wave 4 "migrate". Its open migrate items are:
- **This wave:** M1, M2, M3, M4, M7, M13 (the message), M14, M22, M23.
- **Done earlier:**
  - M5, M8 and M10 landed in d139204 (Wave 1).
  - MS1 and the floors (builder ruling 16) landed in 9c25cef (Wave 0).
  - N1 and N2 landed in Waves 0–1.
  - M28 landed in a21f54a (Wave 2).
  - Wave 2's edge.NoAction, and SET NULL on a Required self edge's NULL-able key, regenerated 66 migrate goldens. This plan doesn't redo them.
- **Wave 5 (tests):** M15, M16, M17 and M20.
- **Wave 6 (docs):** M21, the documented limits of M9 (builder ruling 6), and Wave 1's leftovers in the migrate spec (:182, :294-295, :432-509, :544, :1091, :1144, :1519).
- **Deferred or blocked by the spec:** M6, M9's typed normaliser, M11, M12, M13's full support, M18 and M19 are deferred. M24, M25 and M26 are blocked on the user.
- **Earlier plans' leftovers.** Neither Wave 2 nor Wave 3 left a migrate item. Wave 1 left one (plan 1:653): "The PostgreSQL warning about `users_email_key` stays until wave 4's M22". Task 3 closes it.

## Global Constraints

- **Shell.** Run `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2` before any go command.
  - The machine is memory-constrained and shared. Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test. Do not kill other processes.
  - If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **The per-task check (C).** Every task ends with it, on its committed tree.
  - Lint: `go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` at the root.
  - Tests: `go test -race -count=1 -timeout 20m ./...` at the root, which is the only module the wave touches. It runs under D where the task says so.
  - G and the goldens' `-update` run only where the task names them.
  - The full gate, `bash scripts/ci-local.sh`, runs once, in Task 9.
  - On a failure, fix the cause, stage by name, `git commit --amend --no-edit`, then run C again.
- **Commits.**
  - The message is `Area: what changed`, with the ids in parentheses. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage files by name with `git add <paths>`, never with `git add -A` or `.`, and never stage `.DS_Store`.
  - `git status --short` lists what a task changed, and every path in it but `.DS_Store` is staged.
  - Never push.
- **No deprecated aliases.** A replaced name is deleted.
- **Lint and revive stay at 0.**
- **Greps.** Apple's git greps with BSD regex, where `-E` never matches `\b`. Every grep here is `git grep -P`.
- **Test names.** Before adding a test function, run `git grep -nP 'func <Name>\(' -- '*.go'`. Expected: nothing. Every name below was checked at c33cf95, and a name added by an earlier task of this plan counts.
- **Goldens.** A golden changes only through its `-update` run under D, or a hand edit that a step names. Read every changed golden line: only what the task names may differ.
- **Historical documents.** Nothing under `docs/` changes but this plan. Godoc and `example/README.md` change where a task names them.
- **The followups file** (`docs/history/plans/2026-10-14-core-1-followups.md`) is not touched. Wave 6 marks its items, citing these commits.
- **Compile cost.** No task touches the root package, so compilecost_test.go's bounds hold untouched: entityDescUse 757760 (measured 748016), entityDescHooks 1049600 (measured 1037136).
- **How the expected values were found.** Every "today" value below came from a run at c33cf95: a throwaway `zz*_test.go` in the package, deleted after its run, or a grep.
  - The runs needed no database. A value marked **[D]** needs PostgreSQL or MySQL, and Docker was not run while planning. The implementer runs that step under D and records the result in its commit message. If it differs from the plan's expectation, stop and report.
  - Values for new code (messages, statements) are this plan's decisions, written as exact copy.

### R and D

- **RE** (under D): `go test -count=1 -run TestMigrateBaselineEnt ./cmd/velox-gen -update`. It rewrites `cmd/velox-gen/testdata/ent/want/{postgres,mysql}/*`. Only Task 3 runs it. The MySQL files must not change.
- **D: the databases.** Never run D while a gate runs: both use the compose project `velox2-ci`.

```bash
docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# … the step's command …
docker compose -f docker-compose.ci.yml -p velox2-ci down -v
```

- **G** is CI's "generated files are up to date". Wave 3's plan has it verbatim (docs/history/plans/2026-10-27-hardening-3-graphql.md, "The generated check (G)"). Expected: every command exits 0, and the `git diff` is empty. No task here changes generated code, so G runs only in Task 3, whose goldens change, and in the gate.

## Review Focus

1. **Dropping a field-Unique on an ent-adopted PostgreSQL database.** The plan must drop the constraint, and the apply must succeed both on the adopted database and on a fresh one built from `0001_baseline.sql`. Today the first fails: PostgreSQL refuses `DROP INDEX` of a constraint's index. Pin: Task 3 `TestDriftPostgresAdoptedUnique`.
2. **Redefining an adopted unique, for example adding a column.** The constraint is dropped and a plain unique index made. The next `schema.json` must not mark the new index a constraint, or a later drop renders `DROP CONSTRAINT` of an index. Pin: Task 3 `TestCarryConstraints` and `TestAddCarriesConstraint`, their redefined cases.
3. **A pg_dump file that empties `search_path`, followed by a plain file.** Both must be recorded, and the second file's unqualified table must land in the schema the session started in. Pin: Task 5 `TestUpPostgresSearchPath`.
4. **A binary default holding a NUL, and a varchar default whose text is `0x61`.** information_schema cuts a binary default at its first NUL (`0x6100ff` prints `0x61`, measured), so a decoder fed by it would call `'a'` and `0x6100ff` equal. The literal is read from SHOW CREATE TABLE, which is exact, and only printable ASCII is decoded: `0x6100FF` and `'a\0b'` stay as printed, and differ from `'a'`. A varchar keeps `'0x61'`. Pin: Task 7 `TestMySQLBinaryDefault`'s rows and `TestInspectMySQLBinaryDefaults`' differing case.
5. **Renaming a table that holds an adopted constraint.** The rename of the derived name must be `RENAME CONSTRAINT`, on the table's new name. Pin: Task 3 `TestRenderPostgresConstraintIndex`, its table-rename case.

---

### Task 1: An extension's type behind an array or a domain; a domain is a baseline note (M23)

**Files:**
- Modify: `migrate/inspect_postgres.go:199-224` (`pgColumnsQuery`: the extension subquery at :206-208), `:17-25` (`pgTable` gains `domains map[string]bool`), `:226-297` (`columns`).
- Test: `migrate/inspect_postgres_test.go`, new `TestInspectPostgresExtensionThrough`.
- Pinned, and must still pass: `migrate/baseline_test.go:429` (TestBaselinePostgres's citext warning, whose text does not change).

**Interfaces:** Consumes nothing. Produces nothing later tasks use.

- [ ] **Step 1: Write the failing test** (needs citext; skip as TestBaselinePostgres does, `migrate/baseline_test.go:422-425`). On a fresh PostgreSQL database:

```sql
CREATE EXTENSION citext;
CREATE TABLE tag_sets (id integer PRIMARY KEY, labels citext[]);
CREATE DOMAIN tag_name AS citext;
CREATE TABLE tag_names (id integer PRIMARY KEY, name tag_name);
CREATE TABLE tag_lists (id integer PRIMARY KEY, names tag_name[]);
```

Then `migrate.InspectNotes(ctx, db, velox.Postgres, []migrate.TableRef{{Name: "tag_sets"}, {Name: "tag_names"}, {Name: "tag_lists"}})`. The notes with `Baseline` set must equal, in this order:

```go
add := "; add CREATE EXTENSION citext to 0001_baseline.sql before the tables, then run migrate hash"
domain := " is a domain, which 0001_baseline.sql does not create; add its CREATE DOMAIN before the tables, then run migrate hash"
want := []migrate.Note{
	{Table: migrate.TableRef{Name: "tag_sets"}, Object: "tag_sets", Baseline: true, Message: "the type citext[] comes from the extension citext" + add},
	{Table: migrate.TableRef{Name: "tag_names"}, Object: "tag_names", Baseline: true, Message: "the type tag_name comes from the extension citext" + add},
	{Table: migrate.TableRef{Name: "tag_names"}, Object: "tag_names", Baseline: true, Message: "the type tag_name" + domain},
	{Table: migrate.TableRef{Name: "tag_lists"}, Object: "tag_lists", Baseline: true, Message: "the type tag_name[] comes from the extension citext" + add},
	{Table: migrate.TableRef{Name: "tag_lists"}, Object: "tag_lists", Baseline: true, Message: "the type tag_name" + domain},
}
```

The domain note names the nearest domain the walk passed (`tag_name`, not the column's `tag_name[]`; check finding 7). `ddl.NormalizeType(velox.Postgres, …)` keeps `citext[]`, `tag_name` and `tag_name[]` as written (checked by a run).
- [ ] **Step 2: Run it under D.** `go test -count=1 -run TestInspectPostgresExtensionThrough ./migrate`. Expected **[D]**, as measured on PostgreSQL 18.6 (check finding 3): FAIL. tag_sets' note is already there, since PostgreSQL 18 makes the array type `_citext` itself a member of the extension (`pg_depend` holds a `deptype = 'e'` row for it). tag_names and tag_lists have neither the extension note nor the domain note.
  - Record PostgreSQL 14's result in the commit message: run the test once with `VELOX_CI_MATRIX=1`'s PostgreSQL 14 image if that is cheap (scripts/ci-local.sh reads it from ci.yml), and otherwise at Task 9's gate, which runs the matrix. If PostgreSQL 14 has no member row for `_citext`, tag_sets' note is missing there today, and the walk adds it.
- [ ] **Step 3: Implement.**
  - In `pgColumnsQuery`, the extension subquery resolves the column's type first: through `typbasetype` while `typtype = 'd'`, and through `typelem` while the type is an array (`typelem <> 0 AND typlen = -1`). Use a `WITH RECURSIVE` walk with a depth bound of 16, and take the nearest type with a `pg_depend` row of `deptype = 'e'`, as today.
  - The query also returns the nearest domain the same walk passes, as `format_type(oid, NULL)`, or ''.
  - In `columns`, when that domain is not "", add the domain baseline note once per domain per table (`pt.domains`), after the extension note.
- [ ] **Step 4: Run** the test under D, then TestBaselinePostgres under D. Expected: PASS.
- [ ] **Step 5: Commit.** `migrate: an extension's type behind an array or a domain, and a domain, are baseline notes (M23)`. Then run C under D.

---

### Task 2: A primary key's index is read for what the model cannot hold (M1)

**Files:**
- Modify: `migrate/inspect_postgres.go:299-311` (`pgIndexesQuery`: drop `AND NOT i.indisprimary` at :309, and select `i.indisprimary`), and `:313-384` (`indexes`).
- Test: `migrate/inspect_postgres_test.go:405-502` (TestInspectPostgresUnheld gains a table and a `want` entry).

**Interfaces:**
- Produces, in inspect_postgres.go, for Task 3 (which adds a column to the same query and reads it in the same function):

```go
// pgUnheld is what an index has that the model cannot hold, in the words
// of the note: the method but btree, DESC, NULLS FIRST or LAST, an
// operator class or collation of its own, INCLUDE columns, NULLS NOT
// DISTINCT. option is pg_index.indoption as text.
func pgUnheld(method, option string, opclass, collat, notDistinct bool, keys, atts int) ([]string, error)
```

- [ ] **Step 1: Write the failing test.** In TestInspectPostgresUnheld's statement list, add:
  - `CREATE TABLE k (id integer, a integer, CONSTRAINT k_pkey PRIMARY KEY (id) INCLUDE (a))`;
  - `want["k.k_pkey"] = []string{"primary key", "INCLUDE"}`.

  The test's own counts (`len(got) != len(want)`, and Drift's `len(r.Warnings) != len(want)`) then cover the new note.
- [ ] **Step 2: Check what PostgreSQL allows on a key.** Under D, on a scratch database, run:

  ```sql
  CREATE TABLE k2 (id integer NOT NULL);
  CREATE UNIQUE INDEX k2_desc ON k2 (id DESC);
  ALTER TABLE k2 ADD CONSTRAINT k2_pkey PRIMARY KEY USING INDEX k2_desc;
  ```

  Expected **[D]**, measured on PostgreSQL 18.6: `index "k2_desc" column number 1 does not have default sorting behavior (SQLSTATE 42809)`, the refusal of `transformIndexConstraint` in parse_utilcmd.c. The check found the same for NULLS FIRST, an operator class and a collation. Record PostgreSQL 14's text in the commit message if the matrix run is cheap; otherwise Task 9 records it.
  - If PostgreSQL accepts the key instead, add the three statements to the test and `want["k2.k2_pkey"] = []string{"primary key", "DESC"}`.
- [ ] **Step 3: Run the test under D.** `go test -count=1 -run TestInspectPostgresUnheld ./migrate`. Expected **[D]**: FAIL with `k.k_pkey: note "", want it to name primary key`. An INCLUDE key has no note today (measured).
- [ ] **Step 4: Implement.**
  - Factor the `has` list (:351-377) into `pgUnheld`.
  - A primary row gets no Index. When `pgUnheld` returns something for it, it gets the note `the primary key's index has <has, joined by ", ">, which the model cannot hold; it is compared without`, on `pt.object(ix.Name)`, and `indexes` returns before reading columns or appending.
  - The doc comment of `indexes` and of `pgIndexesQuery` says so.
- [ ] **Step 5: Run** the test, then TestInspectPostgresCases, under D. Expected: PASS.
- [ ] **Step 6: Commit.** `migrate: a PostgreSQL primary key's INCLUDE columns are a note (M1)`. Then run C under D.

---

### Task 3: A PostgreSQL unique constraint is held as one: `Index.Constraint` (M22; spec ruling M22, builder ruling 5)

**Files:**
- Modify `migrate/model.go`:
  - `:64-70`: Index gains `Constraint bool`, with the json tag `constraint,omitempty`, after `Where`. Its comment: `// PostgreSQL: the index is a UNIQUE constraint's, as Inspect and Baseline read it; the desired model never sets it, and Diff, Drift and Equal ignore it`.
  - `:204-257` (`validate`): the refusals below.
  - `Equal` (:121-157) is unchanged, since it already lists the fields it compares.
- Modify `migrate/inspect_postgres.go`:
  - `pgIndexesQuery` gains `EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = i.indexrelid AND k.conrelid = i.indrelid AND k.contype = 'u')`, which `indexes` reads into `ix.Constraint`;
  - `constraints` (:404-465) loses the `case "u"` baseline note at :460-461, and its doc comment loses "and is a baseline note".
- Modify `migrate/render_postgres.go`:
  - `:150-155` AddIndex, `:156-161` DropIndex and `:162-163` RenameIndex;
  - `:447-481` pgCreate.
- Modify `migrate/dir.go:419-464` (`Add`, and its doc's "schema.json, which is to"), with the new `carryConstraints` in dir.go.
- Docs: in each list of baseline warnings, replace "a (PostgreSQL) unique constraint read as its index" with "a PostgreSQL domain type, which it does not create" (Task 1's note; check finding 8):
  - `migrate/baseline.go:26-28`;
  - `migrate/doc.go:155-158`;
  - `example/README.md:84-87`.
- Tests:
  - `migrate/model_test.go`: new `TestEqualIgnoresConstraint` and `TestSchemaRefusesConstraint`;
  - `migrate/json_test.go`: new `TestSnapshotConstraint`;
  - `migrate/model_test.go` (package migrate): new `TestCarryConstraints`, for dir.go's unexported `carryConstraints`;
  - `migrate/dir_test.go`: new `TestAddCarriesConstraint`;
  - `migrate/render_postgres_test.go`: new `TestRenderPostgresConstraintIndex`;
  - `migrate/baseline_test.go`: new `TestDriftPostgresAdoptedUnique` (D).
- Pinned tests and goldens this changes:
  - `migrate/inspect_postgres_test.go:567-585`, TestInspectPostgresBaselineNotes: the u_k_key note goes, and only the sequence note stays.
  - `cmd/velox-gen/migrate_baseline_test.go:117-119`: the check becomes the opposite. `strings.Contains(errs, "read as its index")` is an error, `baseline warns of a unique constraint:\n%s`.
  - The goldens, through RE: `cmd/velox-gen/testdata/ent/want/postgres/0001_baseline.sql`, `schema.json`, `warnings.txt`, `diff.sql`.
- Regenerate: RE (under D).

**Interfaces:**
- Consumes: Task 2's `pgUnheld`, and the primary-row early return in `indexes`.
- Produces:
  - `Index.Constraint bool`, the field Task 4's tests may build;
  - in dir.go:

```go
// carryConstraints is to with Constraint set on each index that has it in
// to already (a baseline's, read from the database), or whose index in
// from, the snapshot, is a constraint: the same table, after changes'
// RenameTable, and the same name, after changes' RenameIndex, with the
// same Unique and Columns, from's columns mapped through changes'
// RenameColumn of that table, and to's Where empty. A redefined index is
// a new one, and plain. It returns a clone; from and to are not changed.
func carryConstraints(from, to *Schema, changes []Change) *Schema
```

- [ ] **Step 1: Write the failing unit tests.**
  - **`TestEqualIgnoresConstraint`.** Take `full()` (model_test.go), clone it, and set `Constraint` on one index of the clone. `Equal` holds both ways.
  - **`TestSchemaRefusesConstraint`.** `validate("x")` returns `*Error` Code "schema", Object the table's ref, and these Reasons:
    - on MySQL and on SQLite: `x marks the index "k" a constraint, which only PostgreSQL holds`;
    - on PostgreSQL, with Unique false or Where set: `x marks the index "k" a constraint, which a non-unique or partial index cannot be`.

    A Constraint index that is unique and whole on PostgreSQL passes.
  - **`TestSnapshotConstraint`.** MarshalJSON of a PostgreSQL schema whose index `{Name: "users_email_key", Columns: ["email"], Unique: true, Constraint: true}` gives `…"indexes":[{"name":"users_email_key","columns":["email"],"unique":true,"constraint":true}]…`. UnmarshalJSON reads it back to an equal Schema whose index has Constraint. TestSnapshotShape's bytes are unchanged, since false is omitted.
  - **`TestCarryConstraints`.** From is `users` with `users_email_key` as a constraint. The cases are:

    | case | to | changes | carried |
    |---|---|---|---|
    | same | the index without the flag | none | yes |
    | renamed | the index renamed `users_mail_key` | `RenameIndex{Table: users, From: "users_email_key", To: "users_mail_key"}` | yes |
    | table renamed | table `accounts`, index `accounts_email_key` | `RenameTable` users→accounts, then `RenameIndex{Table: accounts, …}` | yes |
    | column renamed | the index over `mail` | `RenameColumn{Table: users, From: "email", To: "mail"}` (Diff emits no index change; PostgreSQL keeps the constraint over `mail`, measured, check finding 1) | yes |
    | redefined | columns `email, name` | the drop and the add | no |
    | another index | a plain index of the same columns under another name | none | no |
    | partial | to's index has a Where | none | no |
    | baseline | from empty; to's index has the flag itself | the CreateTable | yes, kept |

    From and to are unchanged after the call (reflect.DeepEqual against clones).
  - **`TestAddCarriesConstraint`** (dir_test.go). `dirOf(t, velox.Postgres, step{to: <users with the flagged index>, name: "baseline"})` writes a `schema.json` holding `"constraint": true`, as `migrate baseline` does. Then `ReadDir`, `NewPlan` and `Dir.Add` against a desired schema, without the flag, that adds a column:
    - with the unique unchanged, `out["schema.json"]` contains `"constraint": true`;
    - with the unique redefined to `email, name` (allowing `add-unique`), it does not.
  - **`TestRenderPostgresConstraintIndex`.** NewPlan from a PostgreSQL `users`. The table has columns `id bigint` identity with sequence `users_id_seq`, `email text` and `name text`, and the key `users_pkey`. Its `users_email_key` has `Constraint: true`, and the desired side never has the flag. Expected statements by script:

    | case | today (run at c33cf95) | expected |
    |---|---|---|
    | drop | online `DROP INDEX CONCURRENTLY IF EXISTS "users_email_key"` | main `ALTER TABLE "users" DROP CONSTRAINT "users_email_key"`; no online script |
    | rename to `users_mail_key` | main `ALTER INDEX "users_email_key" RENAME TO "users_mail_key"` | main `ALTER TABLE "users" RENAME CONSTRAINT "users_email_key" TO "users_mail_key"` |
    | redefine to `email, name`, `add-unique` allowed | online `DROP INDEX CONCURRENTLY IF EXISTS "users_email_key"`, then the add's unit `DROP INDEX CONCURRENTLY IF EXISTS "users_email_key"`, `CREATE UNIQUE INDEX CONCURRENTLY "users_email_key" ON "users" ("email", "name")` | main `ALTER TABLE "users" DROP CONSTRAINT "users_email_key"`; online the add's unit, as today |
    | table rename users→accounts, index `accounts_email_key` | main `ALTER TABLE "users" RENAME TO "accounts"`, `ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq"`, `ALTER INDEX "users_email_key" RENAME TO "accounts_email_key"`, `ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey"` | the same, with the third `ALTER TABLE "accounts" RENAME CONSTRAINT "users_email_key" TO "accounts_email_key"` |

    Two Render calls follow. Their from schema holds the flag, which only Inspect and Baseline do.
    - **Create** (from empty, to holding the flag, as a baseline is). Today it gives the CREATE TABLE, then `CREATE UNIQUE INDEX "users_email_key" ON "users" ("email")`. Expected: the one statement `CREATE TABLE "users" (\n  "id" bigint GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME "users_id_seq") NOT NULL,\n  "email" text NOT NULL,\n  "name" text NOT NULL,\n  CONSTRAINT "users_pkey" PRIMARY KEY ("id"),\n  CONSTRAINT "users_email_key" UNIQUE ("email")\n)`, and no CREATE INDEX.
    - **Add on an existing table** (to holding the flag, `add-unique` allowed: NewPlan refuses it otherwise, check finding 5). Today it gives online `DROP INDEX CONCURRENTLY IF EXISTS "users_email_key"` and `CREATE UNIQUE INDEX CONCURRENTLY "users_email_key" ON "users" ("email")`. Expected: main `ALTER TABLE "users" ADD CONSTRAINT "users_email_key" UNIQUE ("email")`, and no online script.
- [ ] **Step 2: Run them.** `go test -count=1 -run 'TestEqualIgnoresConstraint|TestSchemaRefusesConstraint|TestSnapshotConstraint|TestCarryConstraints|TestAddCarriesConstraint|TestRenderPostgresConstraintIndex' ./migrate`. Expected: a build failure, `unknown field Constraint in struct literal`.
- [ ] **Step 3: Implement.**
  - **Model.** The field and validate's two refusals, in the exact Reasons above.
  - **Render** (PostgreSQL only):
    - a DropIndex whose `c.Index.Constraint` is set is `ALTER TABLE <c.Table> DROP CONSTRAINT <name>`, in the main script, never online;
    - a RenameIndex whose from index is a constraint is `ALTER TABLE <c.Table> RENAME CONSTRAINT <from> TO <to>`. Find the from index in `r.from.table(r.l.canon(c.Table))` by `c.From`;
    - an AddIndex with Constraint is `ALTER TABLE <table> ADD CONSTRAINT <name> UNIQUE (<cols>)`, in main;
    - pgCreate writes a Constraint index as `CONSTRAINT <name> UNIQUE (<cols>)` after the checks, as SQLite's unnamed UNIQUE goes (render_test.go:755-777), and leaves it out of the CREATE INDEX loop.
  - **Inspect.** The query column and the dropped note.
  - **Add.** `snapshot` is `json.MarshalIndent(carryConstraints(d.Snapshot, to, p.Changes), …)`. The doc says "schema.json, which is to, with the constraints of the snapshot's indexes that it keeps".
  - **The docs.** The three clauses are replaced, as Files says.
- [ ] **Step 4: Run** the Step 2 command. Expected: PASS.
- [ ] **Step 5: Write the database test, `TestDriftPostgresAdoptedUnique`** (baseline_test.go; `needPostgres`).
  - On a fresh database, run `CREATE TABLE users (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, email text NOT NULL CONSTRAINT users_email_key UNIQUE)`.
  - `want` is that table as velox-gen would make it: Index `users_email_key` Unique, no flag.
  - `snap, ws, err := migrate.Baseline(ctx, db, want)`. Then `ws` is empty, and snap's users_email_key has Constraint.
  - `fsys := dirOf(t, velox.Postgres, step{to: snap, name: "baseline"}, step{to: <want without the index>, name: "drop_unique"})`. Its `0002_drop_unique.sql` holds `ALTER TABLE "users" DROP CONSTRAINT "users_email_key";`.
  - `mustUp(t, db, fsys, migrate.WithBaseline(1))`, and on a second fresh database `mustUp(t, fresh, fsys)`.
  - On both, `SELECT count(*) FROM pg_indexes WHERE indexname = 'users_email_key'` is 0, and Drift against the directory's snapshot has no change.
  - Expected today **[D]**, measured on PostgreSQL 18.6: Baseline warns `users.users_email_key: the unique constraint users_email_key is read as its index; …`, and the drop step writes `DROP INDEX CONCURRENTLY IF EXISTS "users_email_key"` in its online file, as TestRenderPostgresConstraintIndex's "today" column shows. The adopted database's Up then fails with `cannot drop index users_email_key because constraint users_email_key on table users requires it (SQLSTATE 2BP01)`, and the fresh database's passes.
- [ ] **Step 6: Update the pinned tests.** TestInspectPostgresBaselineNotes and migrate_baseline_test.go:117-119, as Files says.
- [ ] **Step 7: Regenerate and read.** RE under D. Expected diff, and nothing else:
  - `want/postgres/0001_baseline.sql`: `CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");` is gone. `CREATE TABLE "users"` ends `CONSTRAINT "users_pkey" PRIMARY KEY ("id"),\n  CONSTRAINT "users_email_key" UNIQUE ("email")\n);`.
  - `want/postgres/schema.json`, and the schema.json inside `want/postgres/diff.sql`: users_email_key gains `"constraint": true` after `"unique": true`.
  - `want/postgres/warnings.txt`: its first line goes, and the hint line stays.
  - `want/mysql/*`: unchanged.
- [ ] **Step 8: Commit.** `migrate: a PostgreSQL unique constraint is Index.Constraint; drops and renames name the constraint (M22)`. Then run G, and C under D.

---

### Task 4: One warning per object (M2)

**Files:**
- Modify: `migrate/drift.go:117-121` (`compare`'s loop over notes).
- Test: `migrate/drift_test.go`, new `TestDriftNotesMerged`.
- Pinned, and changing: `migrate/drift_test.go:343-357`, TestDriftDeterministic's notes part.

**Interfaces:** Consumes nothing. Produces nothing later tasks use.

- [ ] **Step 1: Write the failing test.** On `s := schemaOf(velox.Postgres, usersOf(velox.Postgres, nil))`:
  - `driftOf(t, s, s, {users.id "a"}, {users.id "b"})` gives exactly one warning, `users.id: a; b`.
  - `adoptOf(t, s, s, …)` with these two notes gives one warning, the first note's text, then `; `, then the second's:
    - `{users.id, "the column is GENERATED ALWAYS AS IDENTITY, which the model holds as BY DEFAULT: a value given on insert is refused"}`;
    - `{users.id, Baseline, "the sequence users_id_seq starts at 5 and increments by 1; 0001_baseline.sql creates it with the defaults"}`.

    Today it gives two warnings on `users.id` (checked by a run).
  - `driftOf` with the same two notes gives only the first: drift skips baseline notes.
  - A drift warning of its own on an object that a note also names stays separate. With `spelled()`, "users: the database has the primary key …" and "users: z" stay two lines.
- [ ] **Step 2: Run it.** `go test -count=1 -run TestDriftNotesMerged ./migrate`. Expected: FAIL on the first case, with two warnings.
- [ ] **Step 3: Implement.** In `compare`, a note that passes the filter joins the earlier note warning of its object, if there is one: `w.Message += "; " + n.message`. Otherwise it is a new warning. The notes keep their order. compare's doc says one object gets one note warning.
- [ ] **Step 4: Update TestDriftDeterministic.** Its notes `users.users_x "b"`, `users.users_x "a"` and `users "z"` gave 5 warnings at c33cf95 (run): two of spelled()'s own, and three notes. Now they give 4. `users.users_x`'s message is `b; a`. Change `len(a.Warnings) != 5` (:355) to `4`, and add the message check.
- [ ] **Step 5: Run** `go test -count=1 ./migrate`. Expected: PASS.
- [ ] **Step 6: Commit.** `migrate: the notes on one object are one warning (M2)`. Then run C under D: the database tests read warnings too.

---

### Task 5: The history table is qualified; a file that changes `search_path` or `USE`s a database records itself (M14; builder ruling 14)

**Files:**
- Modify `migrate/session.go`:
  - `:24-35`: the session gains `schema string // PostgreSQL: current_schema() at open`;
  - `:240-270` (`check`): PostgreSQL also reads `coalesce(current_schema(), '')`, and refuses "" with `refuseSession("the connection has no current schema; set search_path")`, Inspect's wording (inspect_postgres.go:42);
  - `:152-182` (`reset`, MySQL): when `SELECT DATABASE()` differs from `s.db`, run `USE <s.db>` (quoted) first.
- Modify `migrate/history.go`:
  - :46, :82-84, :104, :113 and :155 use the qualified name. The column qualifier at :104 stays the table name alone;
  - `hasHistory` (:72-89): PostgreSQL's `to_regclass` gets the qualified name, and MySQL's query gets `table_schema = ?` with `s.db`;
  - `hasTables` (:167-183): `current_schema()` becomes a `$2` of `s.schema`, and `DATABASE()` a `?` of `s.db`.
- Modify `migrate/resolve.go:113` and `migrate/stepwise.go:53`, `:208`, `:276`: the qualified name.
- Tests: `migrate/runner_db_test.go`, new `TestUpPostgresSearchPath`, `TestUpPostgresNoCurrentSchema` and `TestUpMySQLUse`.
- Grepped for SQL text this changes in tests: none. Every test pin found is a test's own `SELECT … FROM velox_migrations` (runner_test.go:39, runner_db_test.go:89, :226, :263, :333, cmd/velox-gen/migrate_test.go:891), and those connections keep the default schema. The error texts name `s.cfg.Table` and do not change.

**Interfaces:**
- Produces, in history.go:

```go
// historyTable is the history table as the session's statements name it:
// qualified by the schema (PostgreSQL) or database (MySQL) open read, so a
// file that changes search_path or USEs another database still records in
// it; unqualified on SQLite.
func (s *session) historyTable() string
```

- [ ] **Step 1: Write the failing tests.**
  - **`TestUpPostgresSearchPath`.** Two files, by `files(…)`:
    - `0001_dump.sql`: `SELECT pg_catalog.set_config('search_path', '', false);\nCREATE TABLE public.dumped (id integer);\n`;
    - `0002_plain.sql`: `CREATE TABLE plain (id integer);\n`.

    `mustUp` applies both. `historyOf` has versions 1 and 2, and `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('dumped', 'plain')` is 2.
  - **`TestUpPostgresNoCurrentSchema`.** On a fresh database, run `ALTER DATABASE <its name> SET search_path = ''`, with the name read by `SELECT current_database()`. Then `migrate.Up` on a new `openDB(t, "pgx", dsn)` returns `*Error` Code "session" and text `migrate: the connection has no current schema; set search_path`.
  - **`TestUpMySQLUse`.** Run `mysqlDatabase(t, "velox_use_other")`, then two files:
    - `0001_a.sql`: `CREATE TABLE a (id int);\nUSE velox_use_other;\nCREATE TABLE b (id int);\n`;
    - `0002_c.sql`: `CREATE TABLE c (id int);\n`.

    `mustUp` applies both, and `historyOf(t, db)` has versions 1 and 2. Table `c` is in the fresh database, and `b` is in `velox_use_other`, read through information_schema.TABLES.
- [ ] **Step 2: Run them under D.** `go test -count=1 -run 'TestUpPostgresSearchPath|TestUpPostgresNoCurrentSchema|TestUpMySQLUse' ./migrate`. Expected **[D]**, measured on PostgreSQL 18.6 and MySQL 8.4.13:
  - TestUpPostgresSearchPath fails with `recording version 1 in velox_migrations: ERROR: relation "velox_migrations" does not exist`.
  - TestUpPostgresNoCurrentSchema fails with `creating the history table velox_migrations: ERROR: no schema has been selected to create in (SQLSTATE 3F000)`, not the session refusal. With search_path `''`, `current_schema()` is NULL.
  - TestUpMySQLUse fails with `0001_a.sql: statements 1 to 2 ran, but recording them in velox_migrations: Error 1146 … 'velox_use_other.velox_migrations' doesn't exist`, the row left dirty.
- [ ] **Step 3: Implement** `historyTable`, the qualified sites, the check and the reset.
  - doc.go:136's "each file starts with the session as Up was given it" now holds for MySQL's database too.
  - `reset`'s doc comment says it restores the database.
  - One sentence beside doc.go:136 (check finding 11): a file resumed with `apply -resume` starts again with the session as Up was given it, so its statements after a `SET search_path` or a `USE` run without them; such a file sets them again at the top of each unit.
- [ ] **Step 4: Run** the tests, then `go test -count=1 ./migrate`, under D. Expected: PASS.
- [ ] **Step 5: Commit.** `migrate: the history table is qualified by the session's schema or database; reset USEs it again (M14)`. Then run C under D.

---

### Task 6: `BEGIN ATOMIC` and psql meta-commands are refused by name (M13's message; spec M13; check finding 10, controller ruling 3)

**Files:**
- Modify: `migrate/split.go:45-58` (`flush`), `:115-160` (`body` becomes `refusal(sql string) string`, the reason or ""), and `run`'s token case (:98-110), for the meta-command line.
- Modify the docs:
  - `migrate/file.go:48-55`, and `migrate/doc.go:51-57` and `:137-139`, each add "or on PostgreSQL a BEGIN ATOMIC body or a psql meta-command line";
  - doc.go's Baseline/adoption section (:150-170) says a pg_dump file needs its `\` lines (pg_dump 18's `\restrict` and `\unrestrict`) removed before it is a migration file.
- Test: `migrate/split_test.go:22-104` (`splitCases`, which FuzzSplit seeds from too).

**Interfaces:** Consumes nothing. Produces nothing later tasks use.

- [ ] **Step 1: Write the failing cases.** Let `atomic := "BEGIN ATOMIC bodies are not supported in migration files; write the body as a string, AS $$ … $$"`. Add to splitCases:

```go
{d: velox.Postgres, src: "CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END;", err: atomic, line: 1},
{d: velox.Postgres, src: "SELECT 1;\nCREATE OR REPLACE PROCEDURE p() LANGUAGE sql\nBEGIN ATOMIC INSERT INTO a VALUES (1); INSERT INTO a VALUES (2); END;", err: atomic, line: 2},
{d: velox.Postgres, src: "CREATE FUNCTION f() RETURNS int LANGUAGE sql begin atomic end", err: atomic, line: 1},
{d: velox.Postgres, src: "SELECT 'BEGIN ATOMIC'; CREATE FUNCTION f() RETURNS int AS $$ BEGIN ATOMIC $$ LANGUAGE sql",
	want: []migrate.Piece{stmt("SELECT 'BEGIN ATOMIC'", 1), stmt("CREATE FUNCTION f() RETURNS int AS $$ BEGIN ATOMIC $$ LANGUAGE sql", 1)}},
{d: velox.Postgres, src: "\\restrict abc123\n\nSET statement_timeout = 0;\nCREATE TABLE a (id integer);\n\\unrestrict abc123\n",
	err: `a psql meta-command (\restrict) is not SQL; remove it`, line: 1},
{d: velox.Postgres, src: "SELECT 1;\n  \\connect other\nSELECT 2;", err: `a psql meta-command (\connect) is not SQL; remove it`, line: 2},
{d: velox.Postgres, src: "SELECT 1\n\\gset\n;", err: `a psql meta-command (\gset) is not SQL; remove it`, line: 2},
{d: velox.Postgres, src: "SELECT '\n\\x';", want: []migrate.Piece{stmt("SELECT '\n\\x'", 1)}},
{d: velox.MySQL, src: "SELECT '\n\\x';", want: []migrate.Piece{stmt("SELECT '\n\\x'", 1)}},
```

The results at c33cf95 (run):
- The first splits into `CREATE FUNCTION … BEGIN ATOMIC SELECT 1` and `END`. ParseFile accepts it, and LintFile refuses `END in a file run in a transaction; …` at line 1.
- The second splits into four pieces, and LintFile refuses `END` at line 3 (check finding 9; the new refusal's line is 2, where the CREATE starts).
- The third is one piece, accepted.
- The fourth gives the two pieces above, and must keep giving them.
- The dump splits into three pieces, `\restrict abc123\n\nSET statement_timeout = 0`, `CREATE TABLE a (id integer)` and `\unrestrict abc123`, and ParseFile accepts it. The check measured the real pg_dump 18.6 output failing at Up with `0001_dump.sql: statement 1 of 16: ERROR: syntax error at or near "\"`.
- The `\connect` source gives `SELECT 1` and `\connect other\nSELECT 2`; the `\gset` source gives one piece `SELECT 1\n\gset`. ParseFile accepts both.
- A `\` inside a literal at a line's start stays one piece, on both dialects, and must keep doing so.
- [ ] **Step 2: Run it.** `go test -count=1 -run TestSplit ./migrate`. Expected: FAIL on the three BEGIN ATOMIC rows and the three meta-command rows.
- [ ] **Step 3: Implement.**
  - `refusal` returns today's trigger reason where `body` returned true.
  - On PostgreSQL, it returns `atomic` when two adjacent unquoted words of `lex(d, sql)` are BEGIN and ATOMIC, case-insensitively.
  - `flush` returns `fileErr(startLine, reason)` for a non-empty reason.
  - In `run`, on PostgreSQL, a token that starts with `\` where `lineStart(src, i)` holds is refused at its line with `a psql meta-command (<cmd>) is not SQL; remove it`, `<cmd>` being the `\` and the letters after it. Literals, quoted names and comments are skipped before tokens, so a `\` inside them is not read.
- [ ] **Step 4: Run** `go test -count=1 ./migrate`, then `go test -run XXX -fuzz '^FuzzSplit$' -fuzztime 10s ./migrate`. Expected: PASS.
- [ ] **Step 5: Commit.** `migrate: a BEGIN ATOMIC body and a psql meta-command line are refused by name (M13)`. Then run C.

---

### Task 7: MySQL binary defaults are read exactly, from SHOW CREATE TABLE; ASCII literals lose `_latin1` and `_ascii` (M3, M4; controller ruling 2)

**Files:**
- Modify: `migrate/inspect_mysql.go:140-192` (`columns`: the SHOW CREATE read), `:194-240` (`mysqlDefault`'s expression path, and its doc), and two new functions below.
- Modify: `migrate/predicate.go:340-355` (the introducer case of the predicate lexer, and its comment).
- Tests:
  - `migrate/inspect_test.go:201-228` (TestMySQLDefault gains rows), and new `TestMySQLBinaryDefault` and `TestMyShownDefault` there, through `migrate/export_test.go` (`MySQLBinaryDefault = mysqlBinaryDefault`, `MyShownDefault = myShownDefault`);
  - `migrate/predicate_test.go`. **Pinned and changing:** the not-same row `{velox.MySQL, "name = _latin1'abc'", "name = 'abc'"}` at :351 moves to the same list (:392-397), and new rows are added;
  - `migrate/inspect_mysql_test.go`: new `TestInspectMySQLBinaryDefaults` (D).

**Why SHOW CREATE TABLE** (check finding 2, measured on MySQL 8.4.13). information_schema's COLUMN_DEFAULT cuts a `binary` or `varbinary` literal default at its first NUL byte, and SHOW CREATE TABLE prints it exactly:

| column default as written | COLUMN_DEFAULT | SHOW CREATE TABLE |
|---|---|---|
| `varbinary DEFAULT 0x00` | `0x` | `DEFAULT '\0'` |
| `binary(3) DEFAULT 0x0061` | `0x` | `DEFAULT '\0a\0'` |
| `DEFAULT 0x6100ff` | `0x61` | `DEFAULT 0x6100FF` |
| `DEFAULT 'a\0b'` | `0x61` | `DEFAULT 'a\0b'` |
| `varbinary DEFAULT 0x00ff` | `0x` | not measured |
| `binary(2) DEFAULT 'a'` | `0x61` | not measured |

A decoder fed by COLUMN_DEFAULT would call `'a'`, `0x6100ff` and `'a\0b'` one default, which builder ruling 6 forbids: M9 is its one exception. The expression path (blob, `DEFAULT_GENERATED`) is exact in COLUMN_DEFAULT (`blob DEFAULT (0x00ff)` prints `0x00ff`, lower case), so it stays there. Only a table with a `binary` or `varbinary` column that has a literal default pays the one extra query, `SHOW CREATE TABLE`.

**Interfaces:** Consumes nothing. Produces, in inspect_mysql.go:

```go
// myShownDefault is the literal of column col's DEFAULT in create, SHOW
// CREATE TABLE's text: a quoted string as MySQL prints it, or 0x…; false
// when the column has no DEFAULT. It reads backquoted names and quoted
// literals, so a name or literal holding " DEFAULT " is not misread.
func myShownDefault(create, col string) (string, bool)

// mysqlBinaryDefault is a binary or varbinary column's literal default, as
// SHOW CREATE TABLE prints it, in the model's spelling. It decodes the
// literal ('…' with MySQL's escapes and doubled quotes, or 0x… in either
// case) to its bytes; on binary(n) it drops the trailing 0x00 bytes that
// pad the value. When every byte left is printable ASCII (0x20–0x7e), it is
// ddl.Literal(velox.MySQL, …) of them; otherwise it is shown as printed.
func mysqlBinaryDefault(typ, shown string) string
```

- [ ] **Step 1: Write the failing rows.**

  **`TestMySQLBinaryDefault`** (`typ, shown, want`). The rows marked "measured" take SHOW CREATE TABLE's output above:

  | typ | shown (Go raw) | want | |
  |---|---|---|---|
  | `varbinary(8)` | `'a'` | `'a'` | |
  | `varbinary(8)` | `0x6100FF` | `0x6100FF` | measured: not `'a'` |
  | `varbinary(8)` | `'a\0b'` | `'a\0b'` | measured: not `'a'` |
  | `varbinary(8)` | `'\0'` | `'\0'` | measured |
  | `binary(3)` | `'\0a\0'` | `'\0a\0'` | measured |
  | `binary(2)` | `'a\0'` | `'a'` | [D]: Step 4 records binary(2) DEFAULT 'a' |
  | `varbinary(8)` | `0x4A4B` | `'JK'` | upper-case hex, as SHOW CREATE prints it |
  | `varbinary(8)` | `0x5C` | `'\\'` | |
  | `varbinary(8)` | `'it''s'` | `'it''s'` | |
  | `varbinary(8)` | `'it\'s'` | `'it''s'` | |
  | `varbinary(8)` | `0x00FF` | `0x00FF` | [D]: Step 4 records 0x00ff |

  **`TestMyShownDefault`.** With `create` =

  ```go
  "CREATE TABLE `bins` (\n  `id` bigint NOT NULL AUTO_INCREMENT,\n  `code` varbinary(8) NOT NULL DEFAULT 'a',\n" +
  	"  `raw` varbinary(8) NOT NULL DEFAULT 0x6100FF,\n  `nul` varbinary(8) DEFAULT 'a\\0b',\n" +
  	"  `c DEFAULT d` varbinary(8) NOT NULL DEFAULT 'x'' DEFAULT y',\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB"
  ```

  `code` gives `'a'`, `raw` gives `0x6100FF`, `nul` gives `'a\0b'`, `c DEFAULT d` gives `'x'' DEFAULT y'`, and `id` gives false.

  **TestMySQLDefault** (`typ, expr, def, want`): the expression path and the guards. "Today" was run at c33cf95, and the MySQL spellings were measured by the check (finding 6):

  | typ | expr | def (Go raw) | today | want |
  |---|---|---|---|---|
  | `blob` | true | `_binary\'a\'` | `_binary'a'` | `('a')` |
  | `blob` | true | `0x61` | `0x61` | `('a')` |
  | `tinyblob` | true | `0x61` | `0x61` | `('a')` |
  | `blob` | true | `0x00ff` | `0x00ff` | `0x00ff` |
  | `varbinary(8)` | true | `_binary\'a\'` | `_binary'a'` | `('a')` |
  | `text` | true | `_latin1\'a\'` | `_latin1'a'` | `('a')` |
  | `text` | true | `_ascii\'a\'` | `_ascii'a'` | `('a')` |
  | `text` | true | `_latin1\'Ã©\'` (MySQL's print of `_latin1'é'`) | `_latin1'Ã©'` | `_latin1'Ã©'` |
  | `text` | true | `_binary\'a\'` | `_binary'a'` | `_binary'a'` |
  | `varchar(8)` | false | `0x61` | `'0x61'` | `'0x61'` |
  | `bit(8)` | false | `b'101'` | `b'101'` | `b'101'` |

  The last four are guards: a non-ASCII latin1 literal, `_binary` on a text column, a varchar's text, and bit are not touched.

  In predicate_test.go's same list:
  - `{velox.MySQL, "name = _latin1'abc'", "name = 'abc'"}`;
  - `{velox.MySQL, "name = _ASCII'abc'", "name = 'abc'"}`;
  - `{velox.MySQL, "(name = _ascii'a''b')", "name = 'a''b'"}`.

  In the not-same list:
  - `{velox.MySQL, "name = _latin1'é'", "name = 'é'"}` (Go string);
  - the existing `_binary` row (:350) stays.

  The four new pairs give `same = false, parsed = false` today (run).
- [ ] **Step 2: Run them.** `go test -count=1 -run 'TestMySQLDefault|TestMySQLBinaryDefault|TestMyShownDefault|TestNormalizePredicateKeepsMeaning' ./migrate`. Both predicate lists are in TestNormalizePredicateKeepsMeaning (predicate_test.go:235). Expected: a build failure on the two new exports. Once they are stubs, the new rows FAIL.
- [ ] **Step 3: Implement.**
  - **M3, literal path.** In `columns`, mark each `binary` or `varbinary` column with a non-NULL COLUMN_DEFAULT and no `DEFAULT_GENERATED`. After the loop, when any is marked, run `SHOW CREATE TABLE <db>.<table>` once, quoted with `ddl.Ident(velox.MySQL, …)`, since it takes no placeholders. Replace each marked column's Default with `mysqlBinaryDefault(typ, shown)`. A marked column that `myShownDefault` cannot find is an error naming the table and column.
  - **M3, expression path.** On a column whose type word is `binary`, `varbinary`, `tinyblob`, `blob`, `mediumblob` or `longblob`:
    - an expression `0x<hex>` decodes as `mysqlBinaryDefault` does, with the result in parentheses when it is a literal;
    - `_binary'…'` is read as the `_utf8mb4'…'` form is.

    `bit` is left as printed.
  - **M4.** `_latin1` and `_ascii` join the UTF-8 introducers. Both match case-insensitively, and only when every byte of the literal is below 0x80:
    - in `mysqlDefault`'s expression path, check the unquoted value;
    - in the predicate lexer, find the literal's end with `stringEnd(expr, j, true)` and check its bytes before dropping the introducer.
- [ ] **Step 4: Write the database test, `TestInspectMySQLBinaryDefaults`** (`needMySQL`). A table `bins` in mysqlSchema's style: `id bigint` identity, PrimaryKeyName `PRIMARY`, Collation `utf8mb4_bin`. Its columns:
  - `code varbinary(8)` Default `'a'`;
  - `pad binary(2)` Default `'a'`;
  - `quote varbinary(8)` Default `''''`;
  - `data blob` Default `('a')`;
  - `latin text` Default `(_latin1'a')`.

  Then:
  - `noDriftMySQL(t, dirOf(t, velox.MySQL, step{to: s, name: "bins"}), s)`.
  - The differing case: on a fresh database with the same directory applied, run `ALTER TABLE bins ALTER COLUMN code SET DEFAULT 0x6100FF`. Drift against `s` gives exactly the change `alter-column bins.code (default)`. That change's text was run through DriftOf at c33cf95.
  - Log SHOW CREATE TABLE's lines for `pad`, and for a scratch column `varbinary(8) DEFAULT 0x00ff`, and record them in the commit message. If they differ from Step 1's two [D] rows, set those rows' `shown` to what MySQL prints, keeping their wants.
  - Expected **[D]** before Step 3: changes or warnings on code, pad and quote. `data` already passes today (`_utf8mb4\'a\'` reads as `('a')`, measured). After Step 3: PASS.
  - If MySQL prints `latin`'s default in a form Step 3 does not read, record the printed form in the commit message and stop.
- [ ] **Step 5: Run** the unit tests, then `go test -count=1 -run 'TestInspectMySQL|TestDriftMySQL' ./migrate` under D. Expected: PASS.
- [ ] **Step 6: Commit.** `migrate: MySQL binary defaults are read from SHOW CREATE TABLE; _latin1 and _ascii drop on ASCII literals (M3, M4)`. Then run C under D.

---

### Task 8: A PostgreSQL DSN that names an unreadable file says which (M7)

**Files:**
- Modify: `cmd/velox-gen/migrate_db.go:171-212` (`dsnForm`'s PostgreSQL case, and its doc).
- Test: `cmd/velox-gen/migrate_test.go:1019-1050`, TestMigrateDSNForm, gains a row.

**Interfaces:** Consumes nothing. Produces nothing later tasks use.

- [ ] **Step 1: Write the failing row.** Add:

```go
{velox2.Postgres, "postgres://appuser:hunter22@dbhost.example/appdb?sslmode=verify-full&sslrootcert=/nonexistent/root.crt",
	"-db names a file that cannot be read: open /nonexistent/root.crt: no such file or directory"},
```

The test's loop then checks every subcommand, and the `$VELOX_DB` spelling (`$VELOX_DB names a file …`). It also checks that no part of the DSN but the path shows.

A run of `pgconn.ParseConfig` at c33cf95 gave this error for the DSN, for its keyword form, and for `sslmode=disable` too: `failed to configure TLS (unable to read CA file: open /nonexistent/root.crt: no such file or directory)`, wrapping a `*fs.PathError` with Op `open` and Path `/nonexistent/root.crt`. A directory given as the file is `read /etc: is a directory`, and a missing `sslkey` is `open …/c.key`. So today's message for the row is `-db is not a PostgreSQL connection string (velox.json's dialect is postgres)`.
- [ ] **Step 2: Run it.** `go test -count=1 -run TestMigrateDSNForm ./cmd/velox-gen`. Expected: FAIL, the new row giving today's message.
- [ ] **Step 3: Implement.** In the PostgreSQL case, when `errors.As(err, &pe)` with `pe *fs.PathError`, return `fmt.Errorf("%s names a file that cannot be read: %v", src, pe)`. Exit 2, as for a form error. Any other ParseConfig error keeps the form message. The doc says a file error names the file and nothing else of the DSN.
- [ ] **Step 4: Run** the test. Expected: PASS.
- [ ] **Step 5: Commit.** `velox-gen: a PostgreSQL DSN naming an unreadable file says which (M7)`. Then run C.

---

### Task 9: The wave's gate

- [ ] **Step 1:** Run `bash scripts/ci-local.sh` with nothing else running. Expected: rc=0. If only a bench/scale time threshold fails, run it once more. Never raise a threshold.
- [ ] **Step 2:** Run the leftover greps below. Expected: each prints nothing, but for grep 5's two exempt lines.
- [ ] **Step 3:** Record PostgreSQL 14's results for Task 1 Step 2 and Task 2 Step 2, from the gate's matrix run (`VELOX_CI_MATRIX=1`), if those tasks did not.

## Leftover greps

Each was run at c33cf95, and again after the plan-check fixes, with the same output. After the task named, each prints nothing, but for grep 5's two exempt lines.

1. **Task 3.** `git grep -nP 'is read as its index|unique constraint read as' -- ':!docs'` prints:
   - `cmd/velox-gen/testdata/ent/want/postgres/warnings.txt:1`;
   - `example/README.md:87`;
   - `migrate/baseline.go:28`;
   - `migrate/doc.go:157`;
   - `migrate/inspect_postgres.go:461`;
   - `migrate/inspect_postgres_test.go:567`, `:581`.
2. **Task 3.** `git grep -nP 'no unique-constraint warning' -- cmd` prints `cmd/velox-gen/migrate_baseline_test.go:118`.
3. **Task 2.** `git grep -nP 'NOT i\.indisprimary' -- migrate` prints `migrate/inspect_postgres.go:309`.
4. **Task 1.** `git grep -nP 'de\.objid = t\.oid' -- migrate` prints `migrate/inspect_postgres.go:207`.
5. **Task 5.** `git grep -nP 's\.q\(s\.cfg\.Table\)|name := s\.cfg\.Table|nspname = current_schema\(\) AND c\.relname' -- migrate` prints:
   - `migrate/history.go:46`, `:82`, `:104`, `:113`, `:155`, `:174`;
   - `migrate/resolve.go:113`;
   - `migrate/stepwise.go:53`, `:208`, `:276`.

   After Task 5 it prints at most the two lines that stay on purpose (check finding 4, controller ruling 6), which Task 9 Step 2 exempts:
   - `history.go:104`, the column qualifier `s.q(s.cfg.Table) + "." + s.q(name)`, since a column is qualified by the table's name alone even when FROM names the schema;
   - `history.go:82`, `name := s.cfg.Table`, which stays for `hasHistory`'s MySQL and SQLite queries, whose table name is a plain parameter. Its PostgreSQL branch takes `historyTable()`.
6. **Task 5.** `git grep -nP 'table_schema = DATABASE\(\)' -- migrate/history.go` prints `migrate/history.go:78`, `:177`.
7. **Task 4.** `git grep -nP 'len\(a\.Warnings\) != 5' -- migrate` prints `migrate/drift_test.go:355`.

## Rulings this plan makes

1. **(call) Task order.** The inspect_postgres.go items go in sequence (builder ruling 20): the columns query (M23), the indexes query (M1), then M22, which extends the indexes query that M1 reshaped. M2 follows M22 because M22 removes the duplicate the triage cited, the DEFERRABLE note beside the unique-constraint note on `u.u_k_key`. The duplicates M2 still has to merge are a GENERATED ALWAYS identity with a sequence start, and a column's collation with its identity.
2. **(call) A baseline creates a constraint as a constraint (Task 3).** The snapshot holds the flag for every database of the directory. A fresh database built from `0001_baseline.sql` must hold a constraint too, or a later `DROP CONSTRAINT` fails there. So `pgCreate` writes `CONSTRAINT … UNIQUE` for a flagged index. The desired model never sets the flag (builder ruling 5), so only Inspect and Baseline reach this.
3. **(call) The flag is carried in `Dir.Add`, the one writer of `schema.json`.** It is kept where `to` has it (a baseline's schema, read from the database), and carried from the snapshot only for an index whose definition is unchanged. A redefined index is created plain, so carrying would mark an index a constraint. `Diff`, `Drift` and `Equal` ignore the flag, so `migrate diff -check` is unaffected.
4. **(call) A constraint drop is never online.** `DROP INDEX CONCURRENTLY` cannot drop a constraint's index, so the drop goes in the main script. An add with the flag, only reachable from a hand-built schema, is a plain `ADD CONSTRAINT` in main.
5. **(call) Notes merge in `compare`, for all dialects (Task 4), not in `pgReader.note` as the triage proposed.** compare is where Baseline puts a baseline note beside a regular one. Only notes merge. A warning drift writes itself stays its own line.
6. **(call) M1's reachable case is INCLUDE.** PostgreSQL refuses a primary key whose index sorts, or has an operator class or collation, other than the default. Task 2 Step 2 checks this on the database. The same `pgUnheld` reads the key's index, so whatever PostgreSQL does allow is noted.
7. **(call) M3 decodes only printable ASCII, and leaves bit alone.** Decoding other bytes would put control bytes or invalid UTF-8 into a model literal, and into `0001_baseline.sql`. Leaving them as printed keeps a true difference visible. A bit default round-trips when the model spells it as MySQL prints it, `b'101'`.
8. **(call) M4's introducers are `_latin1` and `_ascii`, on ASCII bytes only, in both readers.** A fixed-width set such as `_utf16` reads the same bytes as other characters, so it is never stripped.
9. **(call) Every `BEGIN ATOMIC` is refused, the empty `BEGIN ATOMIC END` too.** The empty one splits correctly today, but one rule is simpler to document.
10. **(call) M7 names the file through `*fs.PathError`.** A missing service file is not a PathError, and it keeps the form message.
11. **(known model limit) One idea, two forms.** A PostgreSQL unique constraint is an Index with `Constraint` set. A SQLite UNIQUE constraint is an unnamed Index (`Name: ""`), which SQLite keeps no name for. The frozen model holds both, deliberately: each is what its database can drop and rename. Atlas's model makes the same PostgreSQL split (`postgres.Constraint{T: "u"}`, check finding 12). Wave 6 documents it.
12. **(call) The SHOW CREATE parser is narrow.** `myShownDefault` reads one column's DEFAULT literal, not the statement. It reads backquoted names and quoted literals, so neither can fake a `DEFAULT`.

## Controller rulings

The controller ruled on the two decisions of 7a27429 and on the builder check's findings.

1. **Both decisions are accepted.** M14 qualifies with the database read at open, and `reset` runs `USE` (Task 5). M23 gets the domain baseline note (Task 1).
2. **M3 reads SHOW CREATE TABLE (finding 2).** information_schema cuts a binary default at its first NUL, so it is not trusted for `binary` and `varbinary` literals. Their default is read from SHOW CREATE TABLE, which is exact, and decoded there. A second documented exception beside M9 would break builder ruling 6. Only a table with such a column and default pays the extra query. The test rows take MySQL's real output (finding 6), and the measured SHOW CREATE values are recorded in Task 7.
3. **psql meta-commands (finding 10).** ParseFile refuses a PostgreSQL line that starts with `\`, by name and line: `a psql meta-command (\restrict) is not SQL; remove it`. It goes in Task 6, beside BEGIN ATOMIC. The docs say a pg_dump file needs its `\` lines removed.
4. **carryConstraints maps columns through RenameColumn (finding 1).** Task 3 has the "column renamed" row.
5. **Task 1 Step 2's [D] expectation is corrected (finding 3).** PostgreSQL 18's `_citext` is an extension member, and only the domain lacks a note. PostgreSQL 14 is recorded under D with `VELOX_CI_MATRIX=1` if cheap, else at the gate (Task 9 Step 3).
6. **Leftover grep 5 exempts history.go:104 and :82 (finding 4),** with the reason.
7. **Findings 5, 7, 8 and 9.**
   - Task 3's add case allows `add-unique`.
   - The domain walk notes the nearest domain, through an array too (Task 1).
   - Task 3 puts the domain note into the baseline warning lists.
   - Task 6's "today" line is 3.
8. **Finding 11:** Task 5 adds one doc sentence on `apply -resume` after a `SET search_path` or a `USE`.
9. **Finding 12:** the model limit is recorded as plan ruling 11, and is a Wave 6 docs leftover.

## Decisions for the controller

None open. The two of 7a27429 are ruled above.

## Left by this plan

- **Wave 5 (tests).** M15, M16, M17 and M20, as the spec rules.
- **Wave 6 (docs).**
  - M21.
  - Plan ruling 11's model limit: PostgreSQL's unique constraint is `Index.Constraint`, and SQLite's is an unnamed Index.
  - M9's documented limits.
  - The migrate spec's stale lines (Wave 1's list).
  - The migrate spec's §§ on baseline warnings lose the unique-constraint warning and gain the domain note.
- **After 1.0, additive.**
  - A PostgreSQL DSN whose `servicefile` is missing still gives the form message.
  - An online `ADD CONSTRAINT … USING INDEX` for a flagged add on an existing table.

## Rulings made during execution

1. **The per-task check ran instead of the full gate.** It was lint, `go test -race` of the root module, G where goldens changed, and D where SQL, DDL or inspection changed. `scripts/ci-local.sh` ran once at the wave gate, with `VELOX_CI_MATRIX=1`.
2. **Task 1's PG 14 record.** PostgreSQL 14.24 has no extension member row for `_citext`. So before the walk, a PG 14 database gave no baseline notes at all; on 18.6 only the domain cases lacked one. The walk notes both.
3. **Task 3's carryConstraints** skips every table that the changes create. A table renamed away and a new one under its old name otherwise carried the flag, and the next drop failed with 42704.
4. **Task 6.** The psql meta-command refusal also covers a backslash that starts a statement after a `;` on the same line (`SELECT 1; \dt`). FuzzSplit's round-trip needs it, and no PostgreSQL token starts with `\`.
5. **Task 7's binary defaults.** On MySQL, a `binary` or `varbinary` default is compared by its bytes, decoding both sides alike, when both sides are literals: empty, `'…'`, `0x…`, `_binary'…'` or `X'…'`.
   - Equal bytes are the same; different bytes are a change.
   - A side that can't be decoded is compared as written.
   - An expression default takes the expression path, which warns when the two differ.
   - SHOW CREATE TABLE names are read backquoted, double-quoted, or bare when unambiguous.
   - Measured on MySQL 8.4: NO_BACKSLASH_ESCAPES doesn't change SHOW CREATE's print.
6. **Task 8 (M7)** was missed in dispatch, because the controller counted seven tasks. It ran in the final fix wave from its own brief.
7. **The pre-Constraint upgrade advice** is in doc.go, not the frozen `Index` godoc. To drop such a constraint, take the file migrate diff writes and replace its statement with `ALTER TABLE … DROP CONSTRAINT IF EXISTS` and `DROP INDEX IF EXISTS`, both names schema-qualified, then run migrate hash. That works on databases built either way.
8. **Drift on PostgreSQL warns** when an index is a UNIQUE constraint on one side and a plain index on the other. Changes and Equal still ignore the flag. Builder ruling 6 then holds without a second exception.
9. **Notes merged on one object** join with "; also, ", and every note names its subject.
10. **A PostgreSQL domain an extension creates**, or one in information_schema or pg_catalog, gets no domain note.
11. **Leftover grep 5** exempts history.go's `historyTable()` body (`:49-53`) and the column qualifier (`:117`). Both are meant to be there.
12. **5056f66 fails TestDriftDeterministic on its own;** c5d0147 fixes the pin. The local history was kept rather than rewritten, so a bisect needs both.

## Left by this plan (execution)

- **Wave 5 (tests):**
  - the PostgreSQL stepwise path (recordError, the persist fallback, resolve) under a changed search_path;
  - a quoted or dotted schema name;
  - a test pinning that a domain over a user domain names only the nearest domain.
- **Wave 6 (docs and limits):**
  - PG 18 `UNIQUE … WITHOUT OVERLAPS` is read with a method note and rebuilt as a btree UNIQUE;
  - SQLite's unique constraint is an unnamed Index while PostgreSQL's is `Index.Constraint`, two forms of one idea (ruling 11 of the plan);
  - for nested domains, only the nearest is named in the baseline note;
  - a `"$user"` schema created by a migration moves current_schema() between runs;
  - a unique constraint that a foreign key references still can't be dropped alone.
- **Left as is:**
  - PostgreSQL's advisory-lock key hashes the history table name alone. It over-serialises two schemas in one database, and changing it would let an old and a new runner overlap.
  - verify.go reads current_schema() and DATABASE() on the session's own connection, right after open's check.
