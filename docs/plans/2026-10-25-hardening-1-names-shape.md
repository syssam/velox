# Pre-1.0 Hardening, Waves 0 and 1: CI, Frozen Names and Generated Shape — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0 freezes them:
- CI runs once per change with a read-only token, pinned images, and the supported versions: PostgreSQL 14 and 18, MySQL 8.4;
- every name velox-gen puts in a database or in Go code is ent's: tables, join columns, Go plurals, index, unique and foreign-key names;
- the generated code has its final shape (no model hash, `omitzero` where `omitempty` does nothing, enum `String` and `IsValid`), behind one bump to `GeneratedCodeIsVersion6`;
- velox-gen refuses at generation time what would fail later, explains its failures, and writes all or nothing.

**Architecture:** Ten tasks, each building and passing the gate at its commit.
- Wave 0 is Tasks 1–2: CI files, the itest guard variable, the C7 re-measure, then the version floors and the matrix.
- Wave 1 is Tasks 3–10, in the spec's order. R2 lands first (Task 3), so that later regenerations stop rewriting every `velox.go`. Then names (Task 4) and DDL names (Task 5), which both touch `internal/gen/build.go` and every golden. Then the shape and the one version bump (Task 6), MySQL's plan-time checks (Task 7), and the velox-gen items (Tasks 8–10).

There are no templates. velox-gen writes code from format strings in `internal/gen/render.go` and `lower.go`, velox-graphql from format strings in `contrib/graphql/internal/gogen/*.go`. A change to emitted code is an edit to a format string, followed by the regeneration steps in R below.

**Tech Stack:** Go 1.27; golangci-lint v2.14.0 and revive v1.17.0, as `scripts/ci-local.sh` pins them; gqlgen v0.17.86 in example/graphql; Docker for PostgreSQL and MySQL; go-openapi/inflect v0.19.0's rule tables, copied (not a dependency).

**Spec:** `docs/specs/2026-10-25-pre-1.0-hardening-design.md`. Its final block, **"Rulings on the builder check"**, is binding and overrides the rulings above it where they differ. In this plan, "spec ruling N" is item N of the spec's Rulings, and "builder ruling N" is item N of that final block. The evidence for each id (C…, G…, M…, R…, N…) is in `docs/reviews/2026-10-25-triage-{C,G,M}.md`. Every file:line below was taken at 3841a08.

## Global Constraints

- **Shell.** Run `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2` before any go command.
  - The machine is memory-constrained. Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test. Do not kill other processes.
  - The gate is `bash scripts/ci-local.sh` with Docker. Only one runs at a time. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **Commit, then check.** Every task commits first, then runs G (where it says) and the gate on the committed tree. On a failure, the task fixes the cause and amends its own commit (`git commit --amend --no-edit`, staging by name), then runs both again. The gate passes at every task's commit: rc=0.
- **Commits** are `Area: what changed`. They end with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Stage files by name with `git add <paths>`, never `git add -A` or `.`, and never stage `.DS_Store`. `git status --short` lists what a task changed; every path in it but `.DS_Store` is staged. Never push.
- **No deprecated aliases.** A replaced name or constant is deleted.
- **Lint and revive stay at 0** in every module, generated code included.
- **Goldens.** A golden changes only by its `-update` run or by the hand edit a step names. Read every changed golden line: only what the task changes may differ.
- **Historical documents.** Nothing under `docs/reviews/` or `docs/plans/` changes but this plan. Under `docs/specs/`, only the lines a task names change: core-design §2's line count (C7), the migrate spec's §4 and its ruling 41 and line 36 (M10, MS1). The hardening spec's builder ruling 3 had its licence corrected to MIT with this plan.
- **The followups file** (`docs/plans/2026-10-14-core-1-followups.md`) is not touched. Wave 6 marks its items, citing these commits.
- **No machine-specific paths** in committed files.

### R: the regeneration steps

Each task runs the steps it names, in this order, from the repo root.

- **R1** `go generate . && git diff --exit-code handles.go`. Expected: no diff.
- **R2** `go build -o bin/velox-gen ./cmd/velox-gen && bin/velox-gen -out internal/testmodel -package testmodel ./internal/testmodel/schema && bin/velox-gen -out internal/bindmodel -package bindmodel ./internal/bindmodel/schema`.
- **R3** `go test ./internal/gen -update`. This rewrites `internal/gen/testdata/golden/*/want`, `internal/gen/testdata/golden/*/plugin.json` and `internal/gen/testdata/tables/*.json`.
- **R4** `(cd contrib/graphql && go test ./internal/sdl ./internal/gogen -update)`. This rewrites `internal/sdl/testdata/velox.graphqls` and `internal/gogen/testdata/golden`.
- **R5** `(cd example && for x in start relations tenant policy hooks tx routing otel tour; do go tool velox-gen -out ./$x/gen ./$x/schema; done && go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema)`.
- **R6** `(cd example && go -C graphql tool gqlgen generate)`.
- **R7** `(cd example && go test ./start ./relations ./tenant ./policy ./hooks ./tx ./routing ./otel ./tour -update)`. This rewrites each `testdata/output.golden`.
- **R8** `go test ./migrate -update`. This rewrites `migrate/testdata/dir`, whose models `migrate/cases_test.go` builds with `gen.Tables`, and migrate's lint goldens.
- **R9** (needs D) `go test -count=1 ./cmd/velox-gen -run TestMigrateBaselineEnt -update`. This rewrites `cmd/velox-gen/testdata/ent/want`.

**D: the databases, for R9 and for a step that names them.** Never while a gate runs; both use the compose project `velox2-ci`.

```bash
docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# … the step's command …
docker compose -f docker-compose.ci.yml -p velox2-ci down -v
```

**The generated check (G)** is CI's "generated files are up to date", with all of ci-local.sh's commands. It runs on the committed tree, since it diffs against HEAD. Expected: every command exits 0 with no output, and the `git diff` is empty.

```bash
go build -o bin/velox-gen ./cmd/velox-gen
for m in testmodel bindmodel; do bin/velox-gen -check -out internal/$m -package $m ./internal/$m/schema && bin/velox-gen verify -out internal/$m ./internal/$m/schema; done
(cd example && for x in start relations tenant policy hooks tx routing otel tour; do go tool velox-gen -check -out ./$x/gen ./$x/schema && go tool velox-gen verify -out ./$x/gen ./$x/schema; done \
  && go tool velox-gen -check -out ./graphql/gen -plugin velox-graphql ./graphql/schema \
  && go tool velox-gen verify -out ./graphql/gen -plugin velox-graphql ./graphql/schema \
  && go -C graphql tool gqlgen generate && git diff --exit-code HEAD -- graphql/graph \
  && test -z "$(git ls-files --others --exclude-standard -- graphql/graph)")
```

`verify` prints `velox-gen verify: N entities, 0 with errors` per package; that line is its expected output.

## Review Focus

1. **An entity name inflect leaves unchanged.** `Status`, `Settings`, `Data`, `Media` and `Equipment` must still generate: table `status`, handles `StatusSlice`, no rule 44 clash with the type. Pin: Task 4 `TestEntityNamedStatus`.
2. **A self many-to-many edge whose singular is the owner's label.** `Person` with `edge.To("people", Person.Type)` now gives `person_id` twice. It must be refused with rule 35's hint, not generate a broken join table. Pin: Task 4, a `TestBuildRules` case.
3. **Two declared indexes on the same columns.** Under ent's names they share `lower(Type)_<cols>`. A partial one gets its Where hash; two full ones (one `Unique`) are refused by rule 44, naming StorageKey as the fix. Pin: Task 5 `TestIndexNames` and its rule 44 case.
4. **MySQL's per-table index names.** A `Unique` field's index is named after its column on MySQL, so it can meet a declared index's name inside one table. Tables must refuse it with file:line, not leave MySQL to fail at apply. Pin: Task 5 `TestTablesMySQLIndexNameClash`.
5. **A generate that fails part-way, or an output whose file names change only in case.** `-out` must be exactly as it was after a failed write, stale files included. A case-only rename must work on a case-insensitive file system. Pins: Task 10 `TestWriteAllRemoves`, `TestWriteAllCaseRename` and `TestGenerateIsAllOrNothing`.

---

## Wave 0: CI hygiene

### Task 1: One CI run per change, a read-only token, `VELOX_ITEST_REQUIRE`, and C7's re-measure

Covers C11, C12's docker guard, C100 and C7. C12's pinned digests land in Task 2, with the new images.

**Files:**
- Modify: `.github/workflows/ci.yml`, `scripts/ci-local.sh`.
- Modify, the guard: `internal/itest/itest_test.go:1-46`, `cmd/velox-gen/main_test.go:10-27`, `migrate/main_test.go:10-27`, `contrib/graphql/cmd/velox-graphql/collect_e2e_test.go:220-235` and `contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go:200-215`.
- Modify, C7: `docs/specs/2026-10-14-core-design.md:224-225`.

**Interfaces:**
- Consumes: nothing.
- Produces: the environment variable `VELOX_ITEST_REQUIRE=1`. With it, a test binary whose database variables are unset exits 1 instead of skipping. `CI` is no longer read.

- **ci.yml** (C11):
  - the trigger is `on: {push: {branches: [main]}, pull_request: {}}`, written in block form;
  - a top-level `permissions: {contents: read}`;
  - both `actions/setup-go@v5` steps take `go-version-file: go.mod` in place of `go-version: "1.27"`;
  - each gofmt check is `test -z "$(gofmt -l . | tee /dev/stderr)"`: the root step's `run:`, and the first line of the example, contrib/veloxotel and contrib/graphql steps;
  - the job's `CI: "true"` and its comment become `VELOX_ITEST_REQUIRE: "1"`, with the comment `# The itests fail, rather than skip, when a database variable is unset.`.
- **ci-local.sh:**
  - after the Go-version check, `command -v docker >/dev/null || { echo "ci-local.sh needs docker" >&2; exit 1; }`;
  - `export CI=true` and its comment become `export VELOX_ITEST_REQUIRE=1`, with the comment `# As in CI: the itests fail, rather than skip, when a database variable is unset.`.
- **The five TestMains** test `os.Getenv("VELOX_ITEST_REQUIRE") == "1"`. Each message replaces `CI=true but` with `VELOX_ITEST_REQUIRE=1 but`. Each doc comment says `when VELOX_ITEST_REQUIRE=1, as CI and scripts/ci-local.sh set it` in place of `on a CI runner (CI=true)`, and itest's package comment says `With VELOX_ITEST_REQUIRE=1` in place of `With CI=true`.
- **C7.** Spec text: the sentence at core-design:224-225 becomes `That is about 8 lines per field plus 53 per entity (98 lines per entity on average, re-measured on 2026-10-25 for the schema DSL on the 25 generated entities of internal/testmodel, internal/bindmodel and example).`

- [ ] **Step 1: Check the guard's current behaviour.** Run `VELOX_ITEST_REQUIRE=1 VELOX_TEST_POSTGRES= VELOX_TEST_MYSQL= VELOX_TEST_MYSQL_ADMIN= go test -count=1 ./internal/itest`. Expected: `ok` (the guard is not read yet, and the tests skip).
- [ ] **Step 2: Edit** ci.yml, ci-local.sh and the five TestMains as above.
- [ ] **Step 3: Run the guard.**
  - The Step 1 command again. Expected: FAIL, with stderr `itest: VELOX_ITEST_REQUIRE=1 but VELOX_TEST_POSTGRES, VELOX_TEST_MYSQL, VELOX_TEST_MYSQL_ADMIN unset: the PostgreSQL and MySQL tests would be skipped`.
  - `CI=true VELOX_TEST_POSTGRES= VELOX_TEST_MYSQL= VELOX_TEST_MYSQL_ADMIN= go test -count=1 ./internal/itest ./migrate`. Expected: `ok` twice.
- [ ] **Step 4: Measure C7.** The script is not committed.

  ```bash
  for f in internal/testmodel/*_velox.go internal/bindmodel/*_velox.go example/*/gen/*_velox.go; do echo "$(wc -l < $f) $(grep -c '{Field: "' $f)"; done | awk '{n++; x+=$2; y+=$1; xx+=$2*$2; xy+=$1*$2} END {b=(n*xy-x*y)/(n*xx-x*x); a=(y-b*x)/n; printf "n=%d mean_lines=%.0f mean_fields=%.1f per_field=%.1f per_entity=%.0f\n", n, y/n, x/n, b, a}'
  ```

  Expected: `n=25 mean_lines=98 mean_fields=5.5 per_field=8.0 per_entity=53`. Write the spec sentence above. If the numbers differ, write the measured ones and say so in the report.
- [ ] **Step 5: Leftover grep.** `git grep -nE 'Getenv\("CI"\)|CI=true|CI: "true"|go-version: "1.27"' -- ':!docs'`. Expected: nothing.
- [ ] **Step 6: Commit** `CI: one run per change, a read-only token, go.mod's Go; VELOX_ITEST_REQUIRE; C7 re-measured`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 2: Floors PostgreSQL 14 and MySQL 8.4; the matrix and pinned images

Covers the version part of MS1 and C32 (builder ruling 16), and C12's digests.

**Files:**
- Modify: `migrate/session.go:313-347`, `migrate/session_test.go:12-52`, `migrate/doc.go:67-69`.
- Modify: `.github/workflows/ci.yml`, `docker-compose.ci.yml`, `scripts/ci-local.sh`.
- Modify: `docs/specs/2026-10-23-migrate-design.md:36` and `:1317` (ruling 41).

**Interfaces:**
- Consumes: Task 1's files.
- Produces:

```go
// migrate/session.go
var (
	minPostgres = 140000
	minMySQL    = [3]int{8, 4, 0}
	minSQLite   = [3]int{3, 35, 0}
)
```

- **Refusal text:**
  - `the server is PostgreSQL %s; migrate needs 14 or later`;
  - `the server is MariaDB %s; migrate needs MySQL 8.4 or later`;
  - `the server is MySQL %s; migrate needs 8.4 or later`.
  - checkVersion's comment and doc.go:68 name `PostgreSQL 14, MySQL 8.4, SQLite 3.35`.
- **The images**, pinned by the digests resolved on 2026-10-25 (`docker buildx imagetools inspect <tag>`):
  - `postgres:14@sha256:8ab94f61668dc73bbab4dc13273ff2fc37e635536af62aef5306a49a9682a446`
  - `postgres:18@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6`
  - `mysql:8.4@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69`
- **ci.yml.** The `test` job gets:

  ```yaml
      name: test (PostgreSQL ${{ matrix.pg }})
      strategy:
        fail-fast: false
        matrix:
          include:
            - pg: "14"
              postgres-image: postgres:14@sha256:8ab94f61668dc73bbab4dc13273ff2fc37e635536af62aef5306a49a9682a446
            - pg: "18"
              postgres-image: postgres:18@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6
  ```

  The postgres service's `image:` is `${{ matrix.postgres-image }}`, and mysql's is the pinned `mysql:8.4@sha256:…`. Every step runs in both entries.
- **docker-compose.ci.yml.**
  - postgres: `image: ${VELOX_CI_POSTGRES_IMAGE:-postgres:18@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6}`, and `tmpfs: [/var/lib/postgresql]`. PostgreSQL 18 keeps PGDATA in `/var/lib/postgresql/18/docker` and 14 in `/var/lib/postgresql/data`, and both are below it.
  - mysql: the pinned `mysql:8.4@sha256:…`.
  - The header comment says the default is PostgreSQL 18 and that `VELOX_CI_POSTGRES_IMAGE` names another.
- **ci-local.sh.** At the end, after `scripts/gqlgen-latest.sh`:

  ```bash
  # VELOX_CI_MATRIX=1 runs the tests again on PostgreSQL 14, CI's other entry.
  if [ "${VELOX_CI_MATRIX:-}" = 1 ]; then
    "${compose[@]}" down -v
    VELOX_CI_POSTGRES_IMAGE='postgres:14@sha256:8ab94f61668dc73bbab4dc13273ff2fc37e635536af62aef5306a49a9682a446' "${compose[@]}" up -d --wait
    go test -race -count=1 -timeout 20m ./...
    (cd contrib/veloxotel && go test -race -count=1 ./...)
    (cd contrib/graphql && go test -race -count=1 ./...)
  fi
  ```

  The header comment names `VELOX_CI_MATRIX=1`.
- **The migrate spec.**
  - Line 36 becomes `8. **Every dialect.** PostgreSQL 14+, MySQL 8.4+ and SQLite 3.35+, tested on PostgreSQL 14 and 18, MySQL 8.4 and SQLite.`
  - Ruling 41 becomes `41. **Minimum versions:** PostgreSQL 14, MySQL 8.4 and SQLite 3.35 (\`DROP COLUMN\`). PostgreSQL 12 and 13 and MySQL 8.0 are past their end of life; the normaliser still strips MySQL's display widths (ruling 52). CI tests PostgreSQL 14 and 18, MySQL 8.4 and SQLite **(changed by the pre-1.0 hardening spec, builder ruling 16: this ruling named PostgreSQL 12 and MySQL 8.0.19)**.`

- [ ] **Step 1: Check PostgreSQL 18 before any edit.** Edit only `docker-compose.ci.yml`'s postgres image and tmpfs, as above. With D, run `docker compose -f docker-compose.ci.yml -p velox2-ci exec postgres psql -U velox -tAc 'show server_version_num'`. Expected: a number from `180000` up. Then run `go test -count=1 ./migrate ./internal/itest ./cmd/velox-gen`. Expected: `ok` three times. **If a test fails only on 18, stop and report it, with the test and its output.** It is a defect outside Wave 0. Then run D's `down -v`.
- [ ] **Step 2: Write the failing test.** `TestCheckVersion`'s table:
  - `{Postgres, "130000", false}`, `{Postgres, "140000", true}`, `{Postgres, "180001", true}` replace the `110000` and `120000` rows; `160004` and `sixteen` stay;
  - `{MySQL, "8.0.19", false}`, `{MySQL, "8.0.36-log", false}`, `{MySQL, "8.3.0", false}`, `{MySQL, "8.4.0", true}`, `{MySQL, "8.4.3", true}`, `{MySQL, "9.1.0", true}` replace the `8.0.18` to `8.0.36-log` rows; `5.7.44`, MariaDB and `eight` stay.
  - `TestCheckVersionText` gets `"130000": "PostgreSQL 13;"`. A new case checks that `CheckVersion(MySQL, "8.0.36")`'s text holds `needs 8.4 or later`, and that `CheckVersion(Postgres, "130000")`'s holds `needs 14 or later`.
- [ ] **Step 3: Run** `go test -run 'TestCheckVersion' ./migrate`. Expected: FAIL on `130000`, `8.0.19` and `8.0.36-log`.
- [ ] **Step 4: Edit** session.go, doc.go, ci.yml, the rest of docker-compose.ci.yml, ci-local.sh and the migrate spec, as the Interfaces say.
- [ ] **Step 5: Run** `go test -run 'TestCheckVersion' ./migrate`. Expected: PASS.
- [ ] **Step 6: Leftover grep.**

  ```bash
  git grep -nE 'postgres:1[0-9]\b|mysql:8\.4\b' -- .github docker-compose.ci.yml scripts | grep -v '@sha256:'
  git grep -nE '8\.0\.19|120000|PostgreSQL 12' -- migrate ':!migrate/*_test.go'
  ```

  Expected: nothing from either.
- [ ] **Step 7: Commit** `CI: PostgreSQL 14 and 18, MySQL 8.4, pinned by digest; migrate's floors are PostgreSQL 14 and MySQL 8.4`.
- [ ] **Step 8: Run the gate, then the matrix.** Run `bash scripts/ci-local.sh`, then `VELOX_CI_MATRIX=1 bash scripts/ci-local.sh`, one after the other. Expected: rc=0 from both.

---

## Wave 1: frozen names and generated shape

### Task 3: No model hash in velox.go (R2)

**Files:**
- Modify: `internal/gen/render.go:99`, `internal/gen/out.go:21`, `internal/gen/lower.go:6-8,34-41`, `internal/gen/graph.go:42`.
- Test: `internal/gen/lower_test.go:146-149` and `:1145-1189`, `internal/gen/build_test.go:1217-1260`.
- Regenerated: R2, R3, R5. Every `velox.go` loses its `// Model: sha256:` line.

**Interfaces:**
- Consumes: nothing.
- Produces: `gen.Out` has no `Hash` field. `Lower` no longer marshals the Graph, and lower.go drops its `crypto/sha256` and `encoding/hex` imports if nothing else uses them. velox.go's lines are the header, the package clause, the import, then `const _ = velox.GeneratedCodeIsVersion5`.
- graph.go:42's comment says `so that describe -json and the generated files do not depend on the checkout's path`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSchemaDefaultChangesOnlyTheHash` becomes `TestSchemaDefaultChangesNoCode`: a SchemaDefault is for the planner only, so `Generate` gives byte-identical files with and without it. The `hashes` exception and the "did not change" check go.
  - `TestOutputIgnoresPositionsInGraph` compares the two `Generate` results file by file for equality, in place of `out1.Hash != out2.Hash`.
  - lower_test.go:148 drops `len(o.Hash) != 64` and `o.Hash` from its message.
  - A new `TestPackageFileHasNoHash`: no file `Generate` writes for `User{}, Post{}` contains `sha256`.
- [ ] **Step 2: Run** `go test -run 'TestSchemaDefaultChangesNoCode|TestOutputIgnoresPositionsInGraph|TestPackageFileHasNoHash' ./internal/gen`. Expected: FAIL to compile on `o.Hash`, or `TestSchemaDefaultChangesNoCode` and `TestPackageFileHasNoHash` FAIL.
- [ ] **Step 3: Drop** the line, the field and the hashing.
- [ ] **Step 4: Regenerate:** R2, R3, R5.
- [ ] **Step 5: Leftover grep.** `git grep -nE 'Model: sha256|model hash|\bo\.Hash\b|\.Hash !=' -- internal cmd example contrib`. Expected: nothing.
- [ ] **Step 6: Run** `go test ./internal/gen ./cmd/velox-gen`. Expected: PASS.
- [ ] **Step 7: Commit** `velox-gen: no model hash in velox.go`.
- [ ] **Step 8: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 4: ent's inflection, copied; ent's plurals for tables and Go names (builder rulings 1–3)

Covers C21, C71, C72 and G10 as builder rulings 1–3 settle them.

**Files:**
- Create: `plugin/inflect.go`, the copied rule tables and their notice; `plugin/testdata/ent.golden`.
- Modify: `plugin/names.go` (`Singular`, `Plural`, new `GoPlural`), `plugin/doc.go:126-132`, `plugin/plugin.go:59`.
- Modify: `internal/gen/build.go:133` and `:596`, `internal/gen/dbnames.go` (new `tableName`), `internal/gen/lower.go:126`, `:751`, `:761` and `:842`.
- Modify, contrib/graphql: `internal/gogen/conn.go:21,63`, `internal/sdl/input.go:274-277`, `veloxgql/gqlimpl/paginate.go:125`, `doc.go:133-135`.
- Test: `plugin/names_test.go`, `plugin/testdata/names.golden`, `internal/gen/build_test.go`, `internal/gen/lower_test.go`, `contrib/graphql/internal/sdl/*_test.go`.
- Test, the tables that change: `migrate/itest_test.go:314-354`, `migrate/diff_test.go:522-538` and `migrate/runner_db_test.go:79`, where the case schema's `Beta` is now table `beta`.
- Regenerated: R2, R3, R4, R5, R6, R7, R8.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// plugin
func Plural(s string) string     // ent's rules.Pluralize: go-openapi/inflect v0.19.0's default ruleset, case-sensitive
func Singular(s string) string   // ent's rules.Singularize, the same ruleset
func GoPlural(name string) string // ent's plural() (entc/gen/func.go:153): Plural(name), plus "Slice" when Plural leaves name unchanged
// internal/gen/dbnames.go
func tableName(name string) string // plugin.Snake(plugin.Plural(name)): ent's Type.Table() (entc/gen/type.go:319)
```

- **The copied rules** (builder ruling 3). `plugin/inflect.go`:
  - It opens with a comment that says the tables are copied from github.com/go-openapi/inflect v0.19.0's `NewDefaultRuleset`, the version ent pins. Then the full text of v0.19.0's LICENCE, which is MIT: `Copyright (c) 2011 Chris Farmiloe` and its permission notice.
  - The rules are a package-level array of `{kind, a, b string; exact bool}` literals, one per `Add*` call of `NewDefaultRuleset` (inflect.go:46-233), in that order: 83 `AddPlural`/`AddPluralExact`, 87 `AddSingular`/`AddSingularExact`, 6 `AddIrregular` and 10 `AddUncountable`. It is static data, so plugin's init does no inflect work.
  - `var defaultRules = sync.OnceValue(newRuleset)` builds the lookup lists on first use, with inflect's semantics:
    - each add puts its rule *first*;
    - an add of plural or singular deletes its suffix from the uncountables;
    - `AddIrregular(s, p)` deletes s and p from the uncountables, then adds plural s→p, plural p→p and singular p→s;
    - `AddUncountable` stores the lower-cased word.
  - Lookup is inflect's `Pluralize` and `Singularize` (inflect.go:330-374):
    - "" is returned as is;
    - a word whose last space-separated word, lower-cased, is uncountable is returned as is;
    - otherwise the first rule that matches wins: `exact` needs `word == suffix`, and the others `strings.HasSuffix(word, suffix)`, case-sensitive. The suffix is then replaced.
    - With no match, Pluralize appends "s" and Singularize returns the word.
  - No regexps. `gqlimpl`, which calls `GoPlural` only on a paging error, pays nothing at start-up.
- **Godoc.**
  - `Plural` and `Singular` say: ent's rules, copied from go-openapi/inflect v0.19.0; case-sensitive, so `Plural("Person")` is `Persons` while `Plural("person")` is `people`; `Singular("children")` is `child`; their outputs never change.
  - `GoPlural` says: the plural velox-gen names Go identifiers with, ent's `plural()`; `Users` for `User`, `StatusSlice` for `Status`.
  - plugin/doc.go's Naming section lists `[GoPlural]` and says the rules are ent's.
  - `Entity.Handles`'s comment says `GoPlural of Name, e.g. "Users", "StatusSlice"`.
- **The call sites:**
  - build.go:133 `e.Table = tableName(e.Name)`;
  - build.go:596 `if e.Table == tableName(e.Name)`;
  - build.go:297 and :333 stay `plugin.Singular(le.Name) + "_id"`, which is now ent's (entc/gen/graph.go:480,499);
  - lower.go:126 and :761 `Handles: plugin.GoPlural(…)`;
  - lower.go:751 stays `plugin.GoName(plugin.Singular(ed.Rel.Table))`, so `user_children` gives `UserChild`;
  - lower.go:842 `add(plugin.GoPlural(e.Name), …)`;
  - conn.go:21 and :63, and paginate.go:125, use `plugin.GoPlural`.
- **GraphQL's edge singulars** (G10, entgql's form). In input.go, when the GraphQL name was made from the edge's Go name (`camel(ed.GoName) == name`):
  - `goOne := plugin.GoName(plugin.Singular(ed.Name))`;
  - `many := camel(goOne)`;
  - `one := upper(many)`.
  
  So `children` gives `childIDs`, `addChildIDs`, `ChildIDs` and `AddChildIDs`. A renamed edge keeps `Singular(name)` and `upper(Singular(name))`, as now.
- **contrib/graphql/doc.go:133-135:** `the root connection function <GoPlural>Connection, named after ent's plural of the GraphQL type (UsersConnection for User, StatusSliceConnection for Status, AccountsConnection for a User exposed as Account)`.

- [ ] **Step 1: Write the failing tests.**
  - `plugin/names_test.go`:
    - `TestSingular`'s map: `"children": "child"`, `"quizzes": "quiz"`, `"s": ""`; the other entries stay.
    - A new `TestGoPlural`: `User`→`Users`, `Status`→`StatusSlice`, `Settings`→`SettingsSlice`, `Media`→`MediaSlice`, `Person`→`Persons`, `Category`→`Categories`, `Index`→`Indices`.
    - `TestNamesGolden` reads 6 columns (Snake, Plural, Singular, GoName, GoPlural), and `rows = 266`.
    - A new `TestEntNames` reads `testdata/ent.golden`, skipping lines that start with `#`. Each `table` row checks `Snake(Plural(Name)) == table` and `GoPlural(Name) == slice`. Each `edge` row checks `Singular(name) == singular` and `GoName(Singular(name)) == goName`. It pins the row count at 105.
  - `plugin/testdata/names.golden`: every row gets a sixth column, `GoPlural(in)`, which is column 3 plus `Slice` when column 3 equals column 1. Columns 3 and 4 change in exactly these 37 rows (input: Plural, Singular):

    ```
    "": "", ""                     Fizz: Fizzs, Fizz             JSONData: JSONData, JSONDatum
    OrgURLs: OrgURLs, OrgURL       UserIDs: UserIDs, UserID      Users: Users, User
    aws: aws, aw                   base64_data: base64_data, base64_datum
    boxes: boxes, box              buses: buses, bus             buzz: buzzs, buzz
    categories: categories, category                             child: children, child
    children: children, child      dns: dns, dn                  fizz: fizzs, fizz
    https: https, http             ies: ies, ie                  lhs: lhs, lh
    men: men, man                  person: people, person        qps: qps, qp
    quiz: quizzes, quiz            quizzes: quizzes, quiz        rhs: rhs, rh
    s: s, ""                       tls: tls, tl                  wishes: wishes, wish
    x_aws: x_aws, x_aw             x_dns: x_dns, x_dn            x_https: x_https, x_http
    x_lhs: x_lhs, x_lh             x_qps: x_qps, x_qp            x_rhs: x_rhs, x_rh
    x_tls: x_tls, x_tl             x_xsrf: x_xsrves, x_xsrf      xsrf: xsrves, xsrf
    ```

    23 rows are added, in sorted position (Snake, Plural, Singular, GoName, GoPlural):

    ```
    B2y b2y B2ys B2y B2y B2ys                  Bus bus Bus Bu Bus BusSlice
    Child child Childs Child Child Childs      Data data Data Datum Data DataSlice
    Equipment equipment Equipment Equipment Equipment EquipmentSlice
    Goods goods Goods Good Goods GoodsSlice    Media media Media Medium Media MediaSlice
    Metadata metadata Metadata Metadatum Metadata MetadataSlice
    Person person Persons Person Person Persons
    Settings settings Settings Setting Settings SettingsSlice
    Status status Status Statu Status StatusSlice
    data data data datum Data dataSlice        equipment equipment equipment equipment Equipment equipmentSlice
    index index indices index Index indices    indices indices indices index Indices indicesSlice
    leaf leaf leafs leaf Leaf leafs            media media media medium Media mediaSlice
    mouse mouse mice mouse Mouse mice          news news news news News newsSlice
    people people people person People peopleSlice
    series series series series Series seriesSlice
    settings settings settings setting Settings settingsSlice
    wife wife wives wife Wife wives
    ```

    Write the file with a throwaway program over the new functions, then check it against these two lists. No other cell may change.
  - `plugin/testdata/ent.golden`, as given at the end of this task.
  - `internal/gen/build_test.go`:
    - `TestEntityNamedStatus` (Review Focus 1): `mustBuild` of schemas `Status`, `Settings`, `Data`, `Media` and `Equipment`. Then `Lower` gives tables `status`, `settings`, `data`, `media` and `equipment`, handles `StatusSlice`, `SettingsSlice`, `DataSlice`, `MediaSlice` and `EquipmentSlice`, and no error.
    - A `TestBuildRules` case `{"35 singular is the owner's label", []schema.Interface{Person{}}, `schema Person: edge "people": join table person_people has two columns named person_id; name them with edge.Columns`}` (Review Focus 2). `Person` (label `person`, table `persons`) declares `edge.To("people", Person.Type)`; ent's `Singular("people")` is `person`, so both columns are `person_id`.
    - A test that a self-M2M edge `children` on `Category` has join columns `category_id` and `child_id`.
  - `contrib/graphql/internal/sdl`: a test on a Category model with an O2M `children` edge. The create input has `childIDs` with Go name `ChildIDs`. The update input has `addChildIDs`/`AddChildIDs` and `removeChildIDs`/`RemoveChildIDs`.
- [ ] **Step 2: Run** `go test -run 'TestSingular|TestGoPlural|TestNamesGolden|TestEntNames' ./plugin && go test -run 'TestEntityNamedStatus|TestBuildRules' ./internal/gen`. Expected: FAIL. `GoPlural` is undefined, so the tests fail to compile.
- [ ] **Step 3: Implement** `plugin/inflect.go`, the three functions and the call sites.
- [ ] **Step 4: Check parity with inflect itself.** This is never committed. In a scratch module under the scratchpad that requires `github.com/go-openapi/inflect v0.19.0` (from the proxy) and replaces `github.com/syssam/velox2` with the checkout, compare `plugin.Plural`/`Singular` with `inflect.Pluralize`/`Singularize`. The inputs are every word of `/usr/share/dict/words`, each also with its first letter raised, every input of names.golden and every name of ent.golden. Expected: `0 differences` over about 472,000 inputs.
- [ ] **Step 5: Run** `go test ./plugin && go test -run 'TestEntityNamedStatus|TestBuildRules' ./internal/gen`. Expected: PASS.
- [ ] **Step 6: Regenerate:** R2, R3, R4, R5, R6, R7, R8. In the gogen goldens, `ChildrenIDs` becomes `ChildIDs` and `childrenIDs` becomes `childIDs`, and nothing else in them differs. The examples' generated code does not change (no example entity or edge is irregular), and R6 and R7 give no diff. In `migrate/testdata/dir`, `betas` becomes `beta`, and its sequence, key and index names follow it.
- [ ] **Step 7: Fix the tests that name changed tables**, by name: migrate's `betas` (the Files list), and whatever `go test` reports in `internal/gen` for the test schemas `Beta`, `ClBoxes`, `ClImports`, `LwDefaults`, `LwNumbers`, `LwSchemaA`–`D` and `PlDefaults`. Their tables become `beta`, `cl_boxes`, `cl_imports`, `lw_defaults`, `lw_numbers`, `lw_schema_as`–`lw_schema_ds` and `pl_defaults`, and their handles `BetaSlice`, `ClBoxesSlice`, `ClImportsSlice`, `LwDefaultsSlice`, `LwNumbersSlice`, `LwSchemaAs`… and `PlDefaultsSlice`.
- [ ] **Step 8: Leftover grep.**

  ```bash
  git grep -nE 'plugin\.Plural\((e\.Label|e\.Name|name|t\.Name|n|typ)\)' -- '*.go'
  git grep -nE '\bbetas\b|boxeses|importses|defaultses|numberses|ChildrenIDs|childrenIDs' -- '*.go' '*.json' '*.sql' '*.graphqls'
  ```

  Expected: nothing from either. `plugin.Plural` stays only inside `tableName` and the plugin package.
- [ ] **Step 9: Run** `go vet ./... && go test ./...` in the root, `contrib/graphql` and `example`, one at a time. Expected: PASS.
- [ ] **Step 10: Commit** `Names: ent's inflection, copied from go-openapi/inflect v0.19.0; tables from the Go name, Slice plurals`.
- [ ] **Step 11: Run G, then the gate.** Expected: G clean, gate rc=0.

`plugin/testdata/ent.golden` (tab-separated; the names ent's own integration code generates, at ent 2b829a033, entc/integration: each entity's `Table` constant and slice type, and each edge's `Add<X>IDs` builder method; the five entities with a custom table are left out):

```
table	Account	accounts	Accounts
table	Api	apis	Apis
table	AttachedFile	attached_files	AttachedFiles
table	Blob	blobs	Blobs
table	BlobLink	blob_links	BlobLinks
table	Blog	blogs	Blogs
table	Builder	builders	Builders
table	Car	cars	Cars
table	Card	cards	Cards
table	CleanUser	clean_users	CleanUsers
table	Comment	comments	Comments
table	Conversion	conversions	Conversions
table	CustomType	custom_types	CustomTypes
table	Device	devices	Devices
table	Doc	docs	Docs
table	Document	documents	Documents
table	ExValueScan	ex_value_scans	ExValueScans
table	FieldType	field_types	FieldTypes
table	File	files	Files
table	FileType	file_types	FileTypes
table	Friendship	friendships	Friendships
table	Goods	goods	GoodsSlice
table	Group	groups	Groups
table	GroupInfo	group_infos	GroupInfos
table	GroupTag	group_tags	GroupTags
table	Info	infos	Infos
table	IntSID	int_si_ds	IntSIDs
table	Item	items	Items
table	License	licenses	Licenses
table	Link	links	Links
table	Media	media	MediaSlice
table	Metadata	metadata	MetadataSlice
table	MixinID	mixin_ids	MixinIDs
table	Node	nodes	Nodes
table	Note	notes	Notes
table	Other	others	Others
table	Parent	parents	Parents
table	PC	pcs	PCs
table	Pet	pets	Pets
table	Post	posts	Posts
table	Process	processes	Processes
table	Relationship	relationships	Relationships
table	RelationshipInfo	relationship_infos	RelationshipInfos
table	Rental	rentals	Rentals
table	Revision	revisions	Revisions
table	Role	roles	Roles
table	RoleUser	role_users	RoleUsers
table	Session	sessions	Sessions
table	Spec	specs	Specs
table	Tag	tags	Tags
table	Task	tasks	Tasks
table	Team	teams	Teams
table	Token	tokens	Tokens
table	Tweet	tweets	Tweets
table	TweetLike	tweet_likes	TweetLikes
table	TweetTag	tweet_tags	TweetTags
table	User	users	Users
table	UserGroup	user_groups	UserGroups
table	UserTweet	user_tweets	UserTweets
table	ValueScan	value_scans	ValueScans
table	Zoo	zoos	Zoos
edge	admins	admin	Admin
edge	attached_files	attached_file	AttachedFile
edge	blocked	blocked	Blocked
edge	car	car	Car
edge	card	card	Card
edge	cards	card	Card
edge	cars	car	Car
edge	children	child	Child
edge	comments	comment	Comment
edge	field	field	Field
edge	files	file	File
edge	followers	follower	Follower
edge	following	following	Following
edge	friends	friend	Friend
edge	friendships	friendship	Friendship
edge	group_tags	group_tag	GroupTag
edge	groups	group	Group
edge	info	info	Info
edge	joined_groups	joined_group	JoinedGroup
edge	joined_users	joined_user	JoinedUser
edge	liked_tweets	liked_tweet	LikedTweet
edge	liked_users	liked_user	LikedUser
edge	links	link	Link
edge	parent_hood	parent_hood	ParentHood
edge	parents	parent	Parent
edge	pets	pet	Pet
edge	posts	post	Post
edge	processes	process	Process
edge	related	related	Related
edge	relatives	relative	Relative
edge	rentals	rental	Rental
edge	roles	role	Role
edge	sessions	session	Session
edge	spec	spec	Spec
edge	tags	tag	Tag
edge	tasks	task	Task
edge	teams	team	Team
edge	token	token	Token
edge	tweet_tags	tweet_tag	TweetTag
edge	tweet_user	tweet_user	TweetUser
edge	tweets	tweet	Tweet
edge	user	user	User
edge	user_tweets	user_tweet	UserTweet
edge	users	user	User
```

### Task 5: ent's DDL names, and the ent fixture's acceptance (builder ruling 4)

Covers C77 and builder ruling 4, with the coordinator's rulings on its open points (this plan's rulings 5–7): SQLite's unique name, MySQL's foreign-key index, and ent's cap on long names.

**Files:**
- Modify: `internal/gen/dbnames.go:16-20,105-160,192,353` (the name functions, `foreignKeys`, `fieldIndexes`, `objects`), `internal/gen/build.go:563,705-722` (declared index names; `indexName` moves to dbnames.go), `internal/gen/tables.go:120-160,187-206` (the per-dialect names), `internal/gen/tables_check.go:77-103` (MySQL's clash check).
- Modify: `internal/ddl/name.go` (new `Symbol`, ent's cap; `Cap` stays for the names ent never makes).
- Modify, the ent fixture: `cmd/velox-gen/testdata/ent/schema/{user,post}.go`, `postgres.sql`, `mysql.sql` (one long index name and one long foreign-key name, below).
- Modify, docs: `plugin/plugin.go:77` (`Index.Name`), `schema/index/index.go:44-47` (`StorageKey`), `schema/edge/edge.go:91-96` (`Symbol`, `Symbols`).
- Test: `internal/ddl/name_test.go` and a new `internal/ddl/testdata/symbol.golden`; `internal/gen/build_test.go` (`TestIndexNames`, `TestIndexNamesAcrossGraph`, `TestIndexNamesCapped`, `TestNamespace`), `internal/gen/dbnames_test.go`, `internal/gen/tables_test.go`, `internal/gen/plugin_test.go`; `cmd/velox-gen/migrate_baseline_test.go:83-200`; the migrate tests whose models come from `gen.Tables` (`migrate/cases_test.go`, `itest_test.go`, `baseline_test.go`). Tests that build `migrate.Schema` by hand keep their names.
- Regenerated: R3, R8, R9 (with D). Every file of `cmd/velox-gen/testdata/ent/want/{postgres,mysql}` changes: the fixture gains two objects, and the diffs lose their renames. The examples' generated Go code does not change: index and key names are not in it.

**Interfaces:**
- Consumes: Task 4's `tableName`.
- Produces, in `internal/ddl/name.go`:

```go
// Symbol is ent's cap on an index or foreign-key name (dialect/sql/schema/atlas.go:1110):
// name when it is at most limit bytes, else name[:limit-33], cut back to a UTF-8
// boundary, then "_", then the 32 lower-case hex digits of md5(name). limit is 63
// on PostgreSQL and 64 on MySQL and SQLite.
func Symbol(d velox.Dialect, name string) string
```

  ent cuts bytes without the UTF-8 step. The step changes only names ent would make invalid UTF-8, which no ent database holds.

- Produces, in dbnames.go. These return the **uncapped** name; the Graph keeps it, and each dialect caps it as the next list says:

```go
func indexName(entity string, cols []string) string                // strings.ToLower(entity) + "_" + cols joined by "_": ent's (entc/gen/type.go:727)
func partialIndexName(entity string, cols []string, where string) string // indexName's text + "_" + the 8 hex digits of FNV-1a 32 of where
func uniqueName(table, column string) string                       // table + "_" + column + "_key": a field Unique on PostgreSQL and SQLite
func fkIndexName(table, column string) string                      // ddl.Cap(table + "_" + column): velox's foreign-key index on PostgreSQL and SQLite, as now
func fkName(table, refTable, edge string) string                   // table + "_" + refTable + "_" + edge: ent's fkSymbol (entc/gen/graph.go:890)
func joinFKName(table, column string) string                       // table + "_" + column: ent's fkSymbols (graph.go:898)
```

- **Capping, per dialect** (this plan's ruling 7). `Tables` caps for its dialect; `PluginModel` and describe give the PostgreSQL form.
  - Declared, partial and derived index names, and every derived foreign-key name (relation, join table, and MySQL's foreign-key index): `ddl.Symbol(d, name)`, as ent caps every index and foreign key. A given StorageKey or Symbol is not capped: rule 50 limits it to 63 bytes, as now.
  - A field `Unique`'s name: ent does not cap it. PostgreSQL cuts the name ent sends at 63 bytes, on a character boundary, so velox gives `uniqueName` cut the same way. SQLite keeps the whole name. MySQL's is the column, which rule 50 already limits.
  - **`ddl.Cap` stays for the names ent never makes**, at these sites: `pkName`, `seqName` and `checkName` (dbnames.go:16-22), `enumType`'s two derived names (dbnames.go:41,43; ent stores an enum as varchar), `ddl.NotNull`, `ddl.Rebuild`, `ddl.OldEnum`, and `fkIndexName` on PostgreSQL and SQLite (ent makes no foreign-key index there).
  - Rule 44 compares the uncapped names. md5 is over the whole name, so two names that differ uncapped differ capped. Tables' MySQL clash check below compares the capped names.
  - The Graph field holding a derived name gets a `Derived bool` beside it (in JSON as `derived`, omitted when false) so that Tables knows which names to cap. Name it as the Graph's style has it.

- **Each call site:**
  - **Declared index** (build.go:563): `cmp.Or(li.StorageKey, indexName(e.Name, ix.Columns))`. Then, once all of an entity's declared indexes are named, an index with a Where and no StorageKey whose name another declared index of the entity also has is renamed `partialIndexName(…)`. A full index never gets the hash. Two full indexes of one name (one `Unique`) are rule 44's, as any shared name is.
  - **Field `Unique`** (`fieldIndexes`, dbnames.go:192): `uniqueName(e.Table, f.Column)`. This name is the one `objects` puts in the relations namespace. `Tables` gives it per dialect: PostgreSQL and SQLite `uniqueName`, MySQL the column's name (ent's, mysql.go:224-235).
  - **Foreign key** of an O2O, O2M or M2O relation (`foreignKeys`, dbnames.go:132): a Symbol if given, else `fkName(ed.Rel.Table, ref.Table, assoc)`. `assoc` is the assoc edge's name: `ed.Name` when `!ed.Inverse`, else `ed.Ref`.
  - **A join table's two foreign keys** (dbnames.go:144): `jt.Symbols[i]` if given, else `joinFKName(jt.Table, jt.Columns[i])`. Its primary key stays `<table>_pkey`.
  - **Foreign-key index** (tables.go:204, dbnames.go:353): `fkIndexName(fk.Table, fk.Column)` on PostgreSQL and SQLite. On MySQL it is the foreign key's name, which is what MySQL names the index it makes for an ent foreign key, so an ent database already has it.
- **MySQL's clash check** (Review Focus 4). In `check`, on MySQL, two indexes of one table whose names are equal case-insensitively fail at the second one's place: `index name %q is also index %s of table %s on MySQL, which names a Unique field's index after its column; give one a StorageKey`. `%s` is the first one's element, as the errors name it.
- **Docs:**
  - `plugin.Index.Name`: `the StorageKey, else ent's: lower(entity)_<columns>, with _<hash of Where> for a partial index whose name another index has. A derived name over 63 bytes is capped as ent caps it on PostgreSQL: its first 30 bytes, "_", and the md5 of the whole name. MySQL and SQLite cap at 64 bytes, so a derived name of exactly 64 bytes is capped only on PostgreSQL.`
  - `ddl.Cap`'s doc: `Cap caps the names velox makes and ent does not: keys, sequences, checks, enum types and velox's own foreign-key indexes. ent's index and foreign-key names are capped with Symbol.`
  - `StorageKey`: `Without one, velox-gen derives ent's name: the entity's name in lower case and the columns, as user_name; a partial index that would share it with another gets a hash of Where.`
  - `Symbol`: `Without it, the name is ent's: <table>_<referenced table>_<assoc edge>, as posts_users_posts.`
  - `Symbols`: `Without them, <join table>_<column>.`
  - The comment of `indexName`'s old home at build.go:705-711 goes.

- [ ] **Step 1: Write the failing tests.**
  - `TestSymbol` in `internal/ddl/name_test.go` reads `testdata/symbol.golden` (tab-separated: dialect, input, output; `-update` rewrites it) and checks `Symbol` against each row. Inputs: `posts_` padded with `x` to 62, 63, 64, 65 and 80 bytes, on each dialect; and `posts_x` followed by 30 `é` (67 bytes), on PostgreSQL and MySQL. The outputs are ent's formula, computed for this plan; after `-update`, the golden must hold exactly these:

    ```
    62 and 63 bytes, every dialect: the input, unchanged
    64  postgres  posts_xxxxxxxxxxxxxxxxxxxxxxxx_9792aa9ca5f26c5e08ed0b4f5ff63bf6
    64  mysql, sqlite: the input, unchanged
    65  postgres  posts_xxxxxxxxxxxxxxxxxxxxxxxx_2527dc711c103d45bf62572f215e893f
    65  mysql, sqlite  posts_xxxxxxxxxxxxxxxxxxxxxxxxx_2527dc711c103d45bf62572f215e893f
    80  postgres  posts_xxxxxxxxxxxxxxxxxxxxxxxx_9bbcb17c83ce03113784bc81e75f70a8
    80  mysql, sqlite  posts_xxxxxxxxxxxxxxxxxxxxxxxxx_9bbcb17c83ce03113784bc81e75f70a8
    é×30 postgres  posts_xééééééééééé_b4719d6da6b69f00823f753d622c57ee
    é×30 mysql     posts_xéééééééééééé_b4719d6da6b69f00823f753d622c57ee
    ```

    The test also checks that every output is at most the dialect's limit and is valid UTF-8.
  - `TestIndexNamesCapped` checks `ddl.Symbol` in place of `ddl.Cap`: on PostgreSQL the 70-byte names become 63 bytes, and on MySQL 64.
  - `TestIndexNames` (Review Focus 3). The fixture `indexNames` keeps `schema.Table("names")` and declares index(a), index(a).Where(X), index(a).Where(Y) and index(a).StorageKey("names_a_key"). Its want is `indexnames_a`, `indexnames_a_fd0c5087`, `indexnames_a_fc0c4ef4`, `names_a_key`. Use the hashes the current test has for X and Y.
  - A `TestBuildRules` case: index(name) and index(name).Unique() on `dupNamed` give `schema dupNamed: index 1: name "dupnamed_name" is also index 0`.
  - `TestDDLNames` in tables_test.go, on each dialect, for `User` (field `name`, field `email` Unique, index(name), edge.To("posts", Post).StorageKey(edge.Column("user_posts"))), `Post` (edge.From("author", User).Ref("posts").Unique(), edge.To("tags", Tag)) and `Tag`:
    - users' indexes: `user_name`, plus `users_email_key` on PostgreSQL and SQLite and `email` on MySQL;
    - posts' foreign key `posts_users_posts`; its index `posts_user_posts` on PostgreSQL and SQLite, `posts_users_posts` on MySQL;
    - post_tags' foreign keys `post_tags_post_id` and `post_tags_tag_id`, and its index on `tag_id` named `post_tags_tag_id`;
    - an entity `UserProfile` with index(bio) gives `userprofile_bio`.
  - `TestTablesMySQLIndexNameClash`: `User` with field `user_name` Unique and index(name) is refused on MySQL. The error carries the field's file:line and the text above, with `"user_name"`. The same schema passes on PostgreSQL and SQLite.
  - `migrate_baseline_test.go`:
    - `entChange` no longer accepts `RenameIndex` or `RenameConstraint`.
    - The fixture gains a long index name and a long foreign-key name, so the cap is pinned against ent's own names:
      - `Post` gets `field.Time("reviewed_by_an_editor_before_the_post_was_published_on").Optional()` and `index.Fields("title", "reviewed_by_an_editor_before_the_post_was_published_on")`. ent's index name has 65 bytes: on PostgreSQL it is `post_title_reviewed_by_an_edit_610afc49e3060dc9162738c5b5b2de3d`, on MySQL `post_title_reviewed_by_an_edito_610afc49e3060dc9162738c5b5b2de3d`.
      - `User` gets `edge.To("drafts_written_but_never_published_by_this_user_so_far", Post.Type)`. Its column in posts is `user_drafts_written_but_never_published_by_this_user_so_far` (59 bytes). ent's foreign key has 66 bytes: on PostgreSQL it is `posts_users_drafts_written_but_f5ecc73789cbf6208f1deb12fd406c67`, on MySQL `posts_users_drafts_written_but__f5ecc73789cbf6208f1deb12fd406c67`, which also names MySQL's index for it.
      - `postgres.sql` and `mysql.sql` get the column, the index and the foreign key (`ON DELETE SET NULL`, as `posts_users_posts`) with exactly those names, written as each file already writes ent's objects.
    - On PostgreSQL, the next diff holds exactly five changes: `CreateEnum users_role`, the role `AlterColumn` (`ColumnType`), the id serial-to-identity `AlterColumn`, `AddIndex posts_user_posts`, and `AddIndex` of the new foreign key's index, `ddl.Cap("posts_user_drafts_written_but_never_published_by_this_user_so_far")` (velox's own index; ent makes none on PostgreSQL).
    - On MySQL it holds none: `len(changes) == 0` replaces the "has no changes" error for MySQL, and step 4's output is `no changes\n`.
- [ ] **Step 2: Run** `go test -run 'TestIndexNames|TestBuildRules|TestDDLNames|TestTablesMySQLIndexNameClash' ./internal/gen`. Expected: FAIL.
- [ ] **Step 3: Implement** the name functions, the call sites, the clash check and the docs.
- [ ] **Step 4: Regenerate:** R3, R8, then R9 with D. Expected golden changes:
  - `want/postgres/diff.sql` loses its three rename lines (`ALTER INDEX "user_name" …`, `ALTER INDEX "users_email_key" …`, `ALTER TABLE "posts" RENAME CONSTRAINT …`), and gains the second online index. The rest is unchanged: `CREATE TYPE`, the identity lines, the alter-type, and 0003's online index.
  - `want/mysql/diff.sql` becomes `no changes\n`.
  - `schema.json` and `0001_baseline.sql` gain the new column, index and foreign key, under the names above, on both dialects; nothing else in them changes. `warnings.txt` does not change. The PostgreSQL warning about `users_email_key` stays until wave 4's M22.
- [ ] **Step 5: Fix the gen-derived migrate and gen tests** that `go test ./internal/gen ./migrate ./cmd/velox-gen` reports, to the new names.
- [ ] **Step 6: Leftover grep.**

  ```bash
  git grep -nE '_unique\b|_fkey"' -- internal/gen plugin schema cmd/velox-gen/testdata migrate/testdata ':!*_test.go'
  git grep -nE 'indexName\(e\.Table' -- internal/gen
  ```

  Expected: nothing from either. Primary keys keep `<table>_pkey`.
- [ ] **Step 7: Run** `go test ./internal/gen ./migrate ./cmd/velox-gen ./internal/itest`, with D for the database tests. Expected: PASS, with `TestMigrateBaselineEnt/postgres` and `/mysql` run, not skipped.
- [ ] **Step 8: Commit** `DDL: ent's index, unique and foreign-key names, capped as ent caps them; an ent database baselines without renames`.
- [ ] **Step 9: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 6: `omitzero` for struct and array fields, enum `String` and `IsValid`, and `GeneratedCodeIsVersion6`

Covers R1 (spec ruling 4, builder ruling 11), R3 (spec ruling 6) and spec ruling 10's velox bump. R9 is withdrawn (builder ruling 7), so the bump lands here, once.

**Files:**
- Modify: `internal/gen/lower.go:252-259` (the tag), `internal/gen/render.go:84-114` (`renderPackage`), `internal/gen/out.go:90-92`, `doc.go:218-236`.
- Test: `internal/gen/lower_test.go`, `internal/gen/render_test.go`, `oldapi_test.go:25,39-40`, `godoc_test.go:84`, `cmd/velox-gen/cli_test.go:309`. Create `internal/bindmodel/enum_test.go`.
- Regenerated: R2, R3, R5, R7.

**Interfaces:**
- Consumes: Tasks 3–5's output.
- Produces:

```go
const GeneratedCodeIsVersion6 = true // velox; GeneratedCodeIsVersion5 deleted
// internal/gen: const GeneratedCodeVersion = 6
```

and, for each enum velox-gen declares (shown for `BindKind`):

```go
// Values returns the values of BindKind in declaration order.
func (e BindKind) Values() []string { return []string{"a", "b"} }

// String returns the stored value of e.
func (e BindKind) String() string { return string(e) }

// IsValid reports whether e is one of the values of BindKind.
func (e BindKind) IsValid() bool {
	switch e {
	case BindKindA, BindKindB:
		return true
	}
	return false
}
```

The three methods share the receiver name `e`, old velox's (compiler/gen/sql/package.go:252-268). `Values` gains the name, so that ST1016 and revive's receiver-naming see one.
- **The tag** (lower.go's default case): `json:"<name>,omitzero"` when `!o.Pointer` and the element kind is `struct` or `array`. This covers `time.Time`, a struct GoType, a UUID's `[16]byte`, and a JSON field of struct or array type. Every other field keeps `omitempty`. A StructTag or Sensitive field is unchanged.
- **doc.go:** the Generated code section names `GeneratedCodeIsVersion6`. The constant's doc says it is replaced by `GeneratedCodeIsVersion7`. velox-gen's `version` prints `generated code 6`.

- [ ] **Step 1: Write the failing tests.**
  - `TestJSONTags` in lower_test.go, on an entity with these fields: `field.Time("at")` → `omitzero`; `field.Time("seen").Optional().Nillable()` → `omitempty`; `field.UUID("u", uuid.UUID{})` → `omitzero`; `field.JSON("p", point{})` (a struct) → `omitzero`; `field.JSON("m", map[string]int{})` → `omitempty`; `field.String("s")` → `omitempty`; `field.String("pw").Sensitive()` → `json:"-"`.
  - In render_test.go, the blog golden's velox.go holds the three methods exactly as above for its enum.
  - `internal/bindmodel/enum_test.go`, `TestEnumMethods`: `BindKindA.String() == "a"`; `BindKindA.IsValid()` and `BindKindB.IsValid()`; `!BindKind("").IsValid()`; `!BindKind("c").IsValid()`.
  - `oldapi_test.go`: the kept list gets `^const GeneratedCodeIsVersion6 `, and the old list gains `^const GeneratedCodeIsVersion5\b`. `godoc_test.go:84` names `GeneratedCodeIsVersion6`, and `cli_test.go:309` `generated code 6`.
- [ ] **Step 2: Run** `go test -run 'TestJSONTags|TestGolden' ./internal/gen && go test -run 'TestOldAPIIsGone|TestGeneratedOnlyAPIIsMarked' . && go test -run TestVersion ./cmd/velox-gen`. Expected: FAIL.
- [ ] **Step 3: Implement** the tag, the methods and the bump.
- [ ] **Step 4: Regenerate:** R2, R3, R5, R7. The examples' entity files change their time and UUID tags, and each `velox.go` with an enum gains the two methods.
- [ ] **Step 5: Re-run C7's script** (Task 1 Step 4). Expected: the same line, `n=25 mean_lines=98 mean_fields=5.5 per_field=8.0 per_entity=53`; no task of this wave changes an entity file's line count.
- [ ] **Step 6: Leftover grep.** `git grep -n 'GeneratedCodeIsVersion5' -- ':!docs'`. Expected: only `oldapi_test.go`'s old list.
- [ ] **Step 7: Run** `go test ./...` in the root, then `(cd contrib/graphql && go test ./...)`, then `(cd example && go test ./...)`, one at a time. Expected: PASS.
- [ ] **Step 8: Commit** `velox-gen: omitzero on struct and array fields, enum String and IsValid; generated code 6`.
- [ ] **Step 9: Run G, then the gate.** Expected: G clean, gate rc=0, revive at 0 on `./internal/testmodel`, `./internal/bindmodel` and `./*/gen`.

### Task 7: MySQL's varchar cut-off, row bytes, and defaults a decimal rounds (M10, M8, M5)

**Files:**
- Modify: `internal/gen/tables.go:388`, `internal/gen/tables_check.go:133-180` (`checkColumn`, M5) and `:222-268` (`mysqlRowBytes`, M8), `internal/ddl/enum.go` (`ParseMySQLSet`).
- Modify, the spec (spec ruling 8): `docs/specs/2026-10-23-migrate-design.md:235` and `:259`.
- Test: `internal/gen/tables_test.go:280-345`, `internal/gen/tables_check_test.go` (create if absent, package gen), `internal/ddl/enum_test.go`.
- Regenerated: R3, R8. A golden changes only if it holds a MaxLen of 16382 or 16383.

**Interfaces:**
- Consumes: nothing from Tasks 3–6.
- Produces:

```go
// internal/ddl
func ParseMySQLSet(t string) ([]string, bool) // the members of set('a',…), as ParseMySQLEnum reads enum(…)
```

- **M10.** `case size <= 16381: return "varchar(…)"`; 16382 and 16383 become `text`. 16381 is the widest varchar that fits a row beside a bigint ID: 16381 × 4 + 2 + 8 = 65534 bytes.
- **M8.** `mysqlRowBytes` knows:
  - `set(…)`: 1, 2, 3, 4 or 8 bytes for 1–8, 9–16, 17–24, 25–32 and 33–64 members;
  - `float(p)`: 4 for p ≤ 24, 8 for 25–53;
  - `geometry`, `point`, `linestring`, `polygon`, `multipoint`, `multilinestring`, `multipolygon` and `geometrycollection`: 12, a blob's in-row pointer, added to `mysqlRowFixed`.
- **M5.** In `checkColumn`, on PostgreSQL and MySQL, a field with a literal numeric Default and a column type `numeric(p,s)`, `numeric(p)`, `decimal(p,s)` or `decimal(p)` (normalised; scale 0 when absent) is refused when the Default does not fit:
  - Read the Default's JSON number with `new(big.Rat).SetString`. It does not fit when rounding it to s places changes it, or when its absolute value is 10^(p−s) or more.
  - The text: `its Default %s does not fit %s, which keeps %d digits before the point and %d after it; the database would round it or refuse it`, with the JSON text, the column type, p−s and s.
- **The spec.**
  - §4's table cell at :235 becomes `Size ≤ 16381: \`varchar(Size)\`; ≤ 65535: \`text\`; …`.
  - :259 says `` `varchar` stops at 16381, the widest that fits a row beside a bigint ID (16381 × 4 + 2 + 8 = 65534 bytes), because utf8mb4 uses up to 4 bytes per character``. The rest of the sentence stays.

- [ ] **Step 1: Write the failing tests.**
  - `TestTablesTypes`: `tbTypesWide` declares `field.String("s16381").MaxLen(16381)` with the default ID, and its comment says a bigint ID. The want rows: `"s16381": {"character varying(16381)", "varchar(16381)", "text"}`, and `tbTypes` gets `"s16382": {"character varying(16382)", "text", "text"}` and `"s16383": {"character varying(16383)", "text", "text"}`.
  - `TestMySQLRowBytes` (package gen): `set('a')`→1; a set of 9 members→2; of 17→3; of 25→4; of 33→8; `float(24)`→4; `float(25)`→8; `float(53)`→8; `geometry`→12; `point`→12; `multipolygon`→12. Each has ok true.
  - `TestParseMySQLSet`: `set('a','b')` gives `[a b]`, true; `enum('a')` gives false.
  - `TestTablesRoundedDefault`: `field.Float64("price").SchemaType(map[string]string{field.Postgres: "numeric(5,2)", field.MySQL: "decimal(5,2)"})`:
    - Default 1.555 is refused on PostgreSQL and MySQL, with its file:line and `its Default 1.555 does not fit`;
    - 1234.5 is refused;
    - 1.55, -999.99 and 0 pass;
    - SQLite passes every one.
- [ ] **Step 2: Run** `go test -run 'TestTablesTypes|TestMySQLRowBytes|TestTablesRoundedDefault' ./internal/gen && go test -run TestParseMySQLSet ./internal/ddl`. Expected: FAIL.
- [ ] **Step 3: Implement** M10, M8, M5 and the spec lines.
- [ ] **Step 4: Regenerate:** R3, R8.
- [ ] **Step 5: Run** `go test ./internal/gen ./internal/ddl ./migrate ./cmd/velox-gen`. Expected: PASS.
- [ ] **Step 6: Commit** `velox-gen: MySQL varchar up to 16381; row bytes of set, float(p) and spatial types; refuse a default the decimal rounds`.
- [ ] **Step 7: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 8: velox-gen refuses what would fail later (C74, C75, N1, G3, G31)

**Files:**
- Modify: `schema/internal/desc/desc.go` (new `Unwritable`), `schema/field/builders.go:56-62,346-355`, `schema/field/field.go:182-194`; `internal/gen/build.go:508-520` (`sameType`); `internal/gen/methods.go:35-38`; `cmd/velox-gen/plugin.go:205-245` (`checkPluginPath`), `plugin/plugin.go:38-43` (`File.Path`'s doc).
- Test: `schema/internal/desc/desc_test.go`, `schema/field/field_test.go`, `internal/gen/build_test.go`, `internal/gen/methods_test.go`, `cmd/velox-gen/plugin_test.go:166-200`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// schema/internal/desc
// Unwritable is why velox-gen cannot write t as Go source, or "": t
// reaches, through pointers, slices, arrays, maps and channels, an
// instance of a generic type (reflect prints its type arguments with
// their full package paths), or an unnamed func, an unnamed interface
// with methods, or an unnamed struct with fields (their packages would
// go unimported, and an unexported field would make another type).
// interface{} and struct{} are writable.
func Unwritable(t reflect.Type) string
// cmd/velox-gen
// goIgnored reports whether go build leaves out a .go file named base, or
// builds it only for some systems: a name that starts with _ or ., a
// _test.go file, or a _GOOS, _GOARCH or _GOOS_GOARCH suffix.
func goIgnored(base string) bool
```

- **C74.** `withType`, `setGoType` and `newNumber` call `desc.Unwritable(t)` after `GoTypeOf`. A non-empty reason fails the field: `GoType %v %s, which velox-gen cannot write; declare a named type for it`. The reason is `is an instance of generic type %s`, or `holds an unnamed %s type` with struct, func or interface.
- **C75.** `sameType` returns `f.Type == id.Type && typeName(f) == typeName(id)` for every kind. The message of rule 12 is unchanged.
- **N1.** `Methods` parses with `parser.ParseComments`, and its callback skips a file `buildIgnored(f)` reports. A `//go:build ignore` file's methods and types are not read.
- **G3.** `goIgnored` uses `build.Context{GOOS: "velox", GOARCH: "velox", Compiler: "gc"}`, whose `OpenFile` returns `package p\n` for any path. It is `strings.HasSuffix(base, "_test.go")`, or `MatchFile("/", base)` false or failing. MatchFile refuses a `_` or `.` prefix and any known GOOS or GOARCH suffix. `checkPluginPath` refuses a `.go` base it reports, with `a Go file's name is not one go build ignores or builds for some systems only`.
- **G31.** `checkPluginPath` refuses a path with a byte ≥ 0x80, first among the checks after the empty one, with `a path is ASCII`.
- **`File.Path`'s doc** adds: `it is ASCII, and a .go file's name is one go build always builds (not _x.go, .x.go, x_test.go or x_linux.go)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestUnwritable` in desc_test.go:
    - "" for `int`, `[]string`, `map[string]any`, `map[string]struct{}`, `*url.URL`, `[16]byte` and a named struct;
    - a reason that holds `generic` for `box[int]`, `[]box[int]` and `map[string]*box[int]`, where `type box[T any] struct{ V T }` is in the test file;
    - a reason that holds `unnamed struct` for `struct{ A int }` and `[]struct{ A int }`;
    - `unnamed func` for `func()`;
    - `unnamed interface` for `*interface{ M() }`.
  - field_test.go: `field.JSON("b", box[int]{})` has one Err that holds `cannot write`; `field.JSON("m", map[string]any{})` has none.
  - A `TestBuildRules` case for rule 12. `fkNamedType` declares `field.Int64("parent_id").GoType(cents(0))` and binds it to its own int64 ID. The want is `schema fkNamedType: edge "parent": field "parent_id" is gen.cents; fkNamedType's ID is int64`. Use the Ident `typeName` prints.
  - `TestMethodsSkipsIgnoredFiles` in methods_test.go. A file `//go:build ignore` with `func (*User) Policy() int { return 0 }` gives `Methods` an empty result. One that declares `type User struct{}` gives no error.
  - `TestPluginPathsAreChecked` gets these cases:
    - refused, with `invalid path "<p>": a Go file's name`: `_x.go`, `.x.go`, `pb/_x.go`, `x_test.go`, `x_linux.go`, `x_amd64.go`, `pb/x_linux_arm64.go`;
    - refused, with `invalid path "<p>": a path is ASCII`: `é.go`, `pb/ü/x.go`;
    - accepted (exit 0, the file written): `linux.go`, `x_unix.go`, `_x.proto`.
- [ ] **Step 2: Run** `go test -run TestUnwritable ./schema/internal/desc && go test ./schema/field && go test -run 'TestBuildRules|TestMethodsSkipsIgnoredFiles' ./internal/gen && go test -run TestPluginPathsAreChecked ./cmd/velox-gen`. Expected: FAIL.
- [ ] **Step 3: Implement** the five items.
- [ ] **Step 4: Run** `go test ./schema/... ./internal/gen ./cmd/velox-gen`. Expected: PASS. No golden changes: R3 gives no diff (`git diff --exit-code internal/gen/testdata`).
- [ ] **Step 5: Commit** `velox-gen: refuse Go types it cannot write, a foreign key of another Go type, ignored files' methods, and Go-ignored or non-ASCII plugin paths`.
- [ ] **Step 6: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 9: velox-gen's messages: `go get -tool`, the user's Mixin line, plugin stacks, "did you mean", and verify (G4, G29, G30, R4, C19, C22)

**Files:**
- Modify: `cmd/velox-gen/plugin.go:149-187` (G4, and G30's printing), `plugin/main.go:24-31,77-85` (G30), `schema/internal/desc/desc.go` (new `FuncOf`), `schema/load/marshal.go` (G29), `internal/gen/build.go:260-263,536-553` (R4), new `internal/gen/suggest.go`, `cmd/velox-gen/verify.go` (C19, C22) and its caller, `cmd/velox-gen/main.go:96-101` (verify's doc), `cmd/velox-gen/testdata/fakeplugin/main.go` (mode `panic`).
- Test: `cmd/velox-gen/plugin_test.go:308-332`, `plugin/main_test.go:163-170`, `schema/load/marshal_test.go:360-374`, `internal/gen/build_test.go`, new `internal/gen/suggest_test.go`, `cmd/velox-gen/verify_test.go` (create if absent).

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// schema/internal/desc
func FuncOf(pc uintptr) string // the function of a counter Caller returned, as runtime.Frame.Function; "" for 0
// internal/gen
// suggest is "; did you mean %q?" with the name of names closest to name by
// edit distance, when that distance is 1 or 2 and less than len(name); the
// first in names' order on a tie; "" otherwise.
func suggest(name string, names []string) string
// cmd/velox-gen
func runProgram(ctx context.Context, modDir string, src []byte, env []string, stdout, stderr io.Writer) int
func sweepVerifyDirs(modDir string, now time.Time) // removes .velox-verify-* directories of modDir last modified over an hour before now
```

- **G4.** When the plugin's stderr holds `no such tool`, runPlugin's error gets one more line: `the module has no tool <name>: add it with go get -tool <its package path>`.
- **G29.** `loader.build` computes `mixinPos := methodPos(vt, "Mixin")`.
  - Every position the loader records goes through `l.pos(pc uintptr) string`. That is `l.mixinPos` when `desc.FuncOf(pc)` starts with `github.com/syssam/velox2/schema/mixin.` and `mixinPos` is not "", else `desc.PosOf(pc)`.
  - It covers fields, edges and indexes, and the misuse errors at marshal.go:271-400.
  - The prefix is a constant, pinned by a test to `reflect.TypeFor[mixin.Time]().PkgPath() + "."`.
- **G30.** When `os.Getenv("VELOX_PLUGIN_TRACE") == "1"`, `call` gives `fmt.Errorf("the plugin panicked: %v\n%s", v, debug.Stack())`; otherwise the error is as now.
  - `Run`'s doc says so.
  - velox-gen's runPlugin prints `resp.Error` line by line, each through `oneLine`, as it prints a plugin's stderr. A one-line error prints as now.
  - velox-gen's package doc names `VELOX_PLUGIN_TRACE=1` in its plugin section.
- **R4.** `suggest` is appended to:
  - rule 7: candidates are the target's assoc edges' names, in declaration order;
  - rule 23's unknown field: the entity's field names;
  - rule 23's unknown edge: the entity's edge names.
- **C19 and C22.**
  - verify makes `ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` and passes it to `runProgram`.
  - `runProgram` first calls `sweepVerifyDirs(modDir, time.Now())`, then makes the directory and writes main.go.
  - It runs `go build -o <tmp>/check[.exe] ./<tmp>`, then the binary, both with `exec.CommandContext(ctx, …)` and Dir modDir. The deferred `os.RemoveAll(tmp)` stays.
  - When `ctx.Err() != nil` after either command, it prints `velox-gen verify: interrupted` and returns 1.
  - When the build exits non-zero, it prints `velox-gen verify: the check program did not build; the generated package may be stale or edited by hand: run velox-gen, then verify again` after go's own output, and returns 1.
  - The binary's exit and output are as now.
  - main.go:96-101's doc: `a killed process leaves it behind, and the next verify removes one older than an hour; ignore .velox-verify-*/ in git.`

- [ ] **Step 1: Write the failing tests.**
  - `TestPluginErrors`:
    - the `unknown` case also wants `the module has no tool nosuchplugin: add it with go get -tool`;
    - a new case `{"panic", "fakeplugin", {"FAKEPLUGIN_MODE=panic"}, "the plugin panicked: the fake plugin panics"}`, whose stderr has no `goroutine `;
    - a case `{"panic traced", …, {"FAKEPLUGIN_MODE=panic", "VELOX_PLUGIN_TRACE=1"}, "the plugin panicked: the fake plugin panics\n"}`, whose stderr holds `goroutine ` on a later line.
    - The fake plugin's mode `panic` calls `panic("the fake plugin panics")` inside its GenerateFunc, and its doc comment lists the mode.
  - `TestRunRecoversPanic` keeps its exact text without the variable. With `t.Setenv("VELOX_PLUGIN_TRACE", "1")`, the Error starts `the plugin panicked: index out of range\n` and holds `main_test.go`.
  - `TestMarshalPositions`: `u.Fields[0].Pos` (mixin.Time's field) ends with `marshal_test.go:<line>`. `<line>` is the line of `User`'s `Mixin` method, from `runtime.FuncForPC(reflect.ValueOf(User.Mixin).Pointer()).FileLine(…)`. `u.Fields[3]` is unchanged. A new test pins the prefix constant.
  - `TestSuggest`: `suggest("nmae", {"name", "email"})` is `; did you mean "name"?`; `suggest("x", {"name"})` is ""; `suggest("post", {"posts", "post_tags"})` is `; did you mean "posts"?`; `suggest("abc", nil)` is "".
  - `TestBuildRules`:
    - the "7 bad ref" want gets no suggestion (`nope` is far from every name);
    - a case `badRefNear` with Ref `"post"` beside an edge `"posts"` wants `…has no edge "post" to refer to; did you mean "posts"?`;
    - a case for rule 23 with index field `"nmae"` wants `names unknown field "nmae"; did you mean "name"?`.
  - `verify_test.go`:
    - `TestVerifySweepsOldDirs`: in `genModule`, make `.velox-verify-old` with mtime two hours ago (`os.Chtimes`) and `.velox-verify-new` with mtime now. After `verify -out gen ./schema`, the old one is gone and the new one is still there.
    - `TestVerifyExplainsBuildFailure`: append `var _ = undefinedName` to the generated `gen/velox.go`, run verify. Expected: exit 1, and stderr holds the explanation line.
    - `TestRunProgramCanceled`: a canceled ctx returns 1 and prints `velox-gen verify: interrupted`. No `.velox-verify-*` is left in the module directory.
- [ ] **Step 2: Run** `go test -run 'TestPluginErrors|TestVerify|TestRunProgramCanceled' ./cmd/velox-gen && go test -run TestRunRecoversPanic ./plugin && go test -run 'TestMarshalPositions' ./schema/load && go test -run 'TestSuggest|TestBuildRules' ./internal/gen`. Expected: FAIL.
- [ ] **Step 3: Implement** the six items.
- [ ] **Step 4: Run** `go test ./plugin ./schema/... ./internal/gen ./cmd/velox-gen`. Expected: PASS. R3 gives no diff.
- [ ] **Step 5: Commit** `velox-gen: go get -tool hint, the user's Mixin line, plugin stacks under VELOX_PLUGIN_TRACE, did you mean; verify sweeps, stops on a signal and explains a failed build`.
- [ ] **Step 6: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 10: generate writes all or nothing (C20)

**Files:**
- Create: `cmd/velox-gen/writeall.go`, holding `writeAll`, `writeTemp`, `makeDirs`, `renameFile` and `linkFile`, moved from `migrate_diff.go:300-460`.
- Modify: `cmd/velox-gen/generate.go:229-262`, `cmd/velox-gen/migrate_diff.go:255`, `migrate_baseline.go:107`, `migrate.go:236`, and `cmd/velox-gen/main.go:90-94` (the doc).
- Test: `cmd/velox-gen/writeall_test.go`, `cmd/velox-gen/generate_test.go`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// writeAll writes files and removes remove, all or nothing. Both are slash
// paths relative to dir, in any directory below it. It first sets aside each
// file it removes, by renaming it within its directory, so that a file whose
// new name differs only in case can take its place. Then it writes each file
// under a temporary name in its own directory, making the directories it
// lacks, and renames it into place, velox.sum last. A file it replaces is
// kept aside first, as now. Once every file is in place it deletes what it
// set aside. On a failure it removes what it wrote and the directories it
// made, and renames back what it set aside. The error names what it could not
// undo.
func writeAll(dir string, files map[string][]byte, remove []string) error
```

- **The migrate callers** pass `nil` for remove; their behaviour is unchanged.
- **generate.go** passes `out`, then `stale` and `remove` as paths relative to `out` (`filepath.Rel`, then `filepath.ToSlash`), to `writeAll`. The emptied directories are removed after it succeeds. Its old comment ("A write that fails part-way leaves the output partly updated") goes.
- **The doc** at main.go:90-94: `A write that fails part-way, such as on a full disk, is undone: -out is left as it was.`

- [ ] **Step 1: Write the failing tests.**
  - A test helper `treeFiles(t, dir) map[string]string` walks dir, keyed by slash path, with no directories.
  - `TestWriteAllRemoves`. The dir holds `old.go`=`old` and `gone.go`=`gone`. `writeAll(dir, {"a.go": "A", "pb/x.proto": "X", "old.go": "new"}, ["gone.go"])` succeeds. `treeFiles` is then exactly `{a.go: A, old.go: new, pb/x.proto: X}`.
  - The same in a fresh dir, with `renameFile` failing on the rename into `pb/x.proto`. The error is the injected one. `treeFiles` equals its value before, so `gone.go` is back, and `pb/` does not exist.
  - `TestWriteAllCaseRename`: the dir holds `user.go`=`a`. `writeAll(dir, {"User.go": "b"}, ["user.go"])` leaves exactly `{User.go: b}`, as `os.ReadDir` lists it.
  - `TestGenerateIsAllOrNothing`. In `genModule`, generate once, change the schema so that two files change and one goes away, and inject a failure of `renameFile` on the second file's rename. The run exits 1, and `treeFiles(gen)` equals its value before.
  - The existing `TestWriteAllRollback` and `TestWriteAllKeepsTarget` pass `nil` and keep their assertions.
- [ ] **Step 2: Run** `go test -run 'TestWriteAll|TestGenerateIsAllOrNothing' ./cmd/velox-gen`. Expected: FAIL to compile (writeAll's arity).
- [ ] **Step 3: Implement** the move, the generalisation and the callers.
- [ ] **Step 4: Run** `go test ./cmd/velox-gen`. Expected: PASS, the migrate tests included.
- [ ] **Step 5: Leftover grep.** `git grep -n 'partly updated' -- cmd`. Expected: nothing.
- [ ] **Step 6: Commit** `velox-gen: generate writes all or nothing`.
- [ ] **Step 7: Run G, then the gate.** Expected: G clean, gate rc=0.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call) v0.19.0, fetched from the proxy, is the source of the copied rules.** ent pins it, and so builder ruling 3 holds without a fallback. Its licence is MIT (`LICENCE`, Chris Farmiloe); Apache-2.0 is v0.21.5's. The copied file carries MIT's notice. The spec's builder ruling 3 said Apache-2.0, and this plan's commit corrects it.
2. **(call) v0.19.0's Pluralize and Singularize use no regexps.** "Compile lazily" is met by building the rule lists in a `sync.OnceValue` from a static array. Neither plugin's init nor gqlimpl's start-up does inflect work.
3. **(call) `GoPlural` is the exported name of ent's `plural()`.** It pairs with `GoName`: both make Go identifiers. `Plural` stays inflect's raw plural, which tables use through `tableName`.
4. **(call) A GraphQL edge input's singular comes from the schema edge's name**, as entgql's does, when the GraphQL name was made from the edge. inflect is case-sensitive, so `Singular("Children")` is `Children`, and the Go name would otherwise disagree with `childIDs`.
5. **(coordinator's ruling, overrides builder ruling 4's "SQLite unnamed") A field `Unique` on SQLite is named `<table>_<col>_key`.** ent's Atlas migrate names that index so on SQLite (dialect/sql/schema/sqlite.go:151-161: "Ent uses the PostgreSQL approach in its migration"); "unnamed" was ent's old, non-Atlas writer. velox2 already names it so, and a named index is one SQLite can drop without rebuilding the table.
6. **(coordinator's ruling) On MySQL, the foreign-key index takes the foreign key's name**, which is what MySQL names the index it makes for an ent foreign key. With `<table>_<col>`, the ent fixture's MySQL diff would hold a rename. PostgreSQL and SQLite keep their current names (`<table>_<col>`), as builder ruling 4 says: ent makes no foreign-key index there.
7. **(coordinator's ruling) Long names are capped as ent caps them.** ent caps every index and foreign-key name with Atlas's `symbol`: at 63 bytes on PostgreSQL and 64 on MySQL and SQLite, `name[:limit-33]_<md5 hex>`. `ddl.Symbol` is that, pinned by `internal/ddl/testdata/symbol.golden` at the boundary and by the ent fixture's long index and foreign key, so that an ent-adopted database with long names plans no renames. A field-Unique name, which ent does not cap, is PostgreSQL's own 63-byte cut there and whole on SQLite. `ddl.Cap` (54 bytes and FNV-1a) stays for the names ent never makes: primary keys, sequences, checks, enum types, migrate's NOT NULL checks, SQLite's rebuild tables, PostgreSQL's swapped-out enum types, and velox's foreign-key indexes on PostgreSQL and SQLite. **Left open:** PostgreSQL, not ent, names an ent table's primary key and serial sequence; for a table name over 58 bytes, PostgreSQL's own cut differs from `ddl.Cap`'s, so such an adoption would plan a rename. No task changes this.
8. **(call) The Where hash goes only on a partial index whose name another declared index of the same entity has.** Two full indexes of one name are rule 44's, whose message already says to name one.
9. **(call) Local CI runs postgres:18, and `VELOX_CI_MATRIX=1` runs the tests again on postgres:14.** That is one image per database, as builder ruling 16 says, plus the opt-in. An incompatibility found on 18 is reported, not fixed in Wave 0.
10. **(call) CI's Go comes from go.mod's `go 1.27.0`**, the module's floor (C11's `go-version-file`). ci-local still requires a go1.27 toolchain.
11. **(call) C7 is measured in Wave 0 and checked again in Task 6.** No Wave 1 task changes the line count of an entity file: tags change in place, and enum methods are in velox.go.
12. **(call) The velox bump lands with R1 and R3 (Task 6).** R9 is withdrawn (builder ruling 7). Tasks 4, 5 and 7 change generated values and DDL, but not the runtime contract. veloxgql's bump is wave 3's.
13. **(call) C74 refuses rather than walks.** Writing a generic instance needs a printer reflect does not have. An unnamed struct with an unexported field of another package would be another type. `interface{}` and `struct{}` stay allowed: `map[string]any` and `map[string]struct{}` are common JSON field types.
14. **(call) A traced plugin stack travels in `Response.Error`'s later lines.** velox-gen prints each line through `oneLine`, as it already prints a failed plugin's stderr. No protocol field is added.
15. **(call) `writeAll` moves to `writeall.go` and takes the files to remove.** Removals are set aside before any write. That is what makes a case-only rename work on a case-insensitive file system, which today's remove-then-write order handles.
16. **(call) Bookkeeping is wave 6's:** G10's resolution, the graphql design spec's superseded note on `childrenIDs`, C13's Snake doc, and the followups file.
