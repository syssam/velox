# Parity Harness (velox ⟷ ent differential testing)

Model-based differential testing for the velox ORM. Each test program is a
typed `op.Program` (a replayable list of operations: creates, edge mutations,
JSON set/append, queries, aggregates, Relay pagination). The same program is run
three ways and compared:

- **reference** — an ORM-free in-memory oracle (`model.Run`) that defines the
  correct observable outcome.
- **velox** — the generated velox client (`runner.RunVelox`).
- **ent** — the generated Ent client (`runner.RunEnt`), the parity baseline.

`runner.RunParity(t, backend, prog)` runs all three on isolated clients for the
chosen backend, normalizes every result back to ORM-neutral `model.Result`
(db ids → creation handles, deterministic parity clock for timestamps), and
classifies each op with a three-way verdict:

| velox vs ref | ent vs ref | verdict             | meaning                          |
|--------------|------------|---------------------|----------------------------------|
| match        | match      | `Pass`              | no divergence                    |
| differ       | match      | `VeloxBug`          | velox is wrong (the target)      |
| differ       | differ     | `ReferenceSuspect`  | oracle/program likely wrong      |
| match        | differ     | `EntDivergent`      | ent is the outlier               |

## Running

```bash
cd tests/parity
go test ./...                          # SQLite suite + executor tests; PG/MySQL skip
go test -run TestCuratedSuite -v       # the curated three-way suite, all backends
```

With no `VELOX_TEST_*` env the suite runs SQLite only and **skips** Postgres /
MySQL cleanly, so a machine without those databases stays green.

### Dialect matrix (Postgres + MySQL)

The same curated suite runs across **SQLite + Postgres + MySQL**. Point the
harness at real servers via env vars (local OrbStack DSNs shown):

```bash
cd tests/parity
VELOX_TEST_POSTGRES="host=localhost port=5433 user=postgres password=test dbname=parity sslmode=disable" \
VELOX_TEST_MYSQL="root:test@tcp(localhost:3306)/parity?parseTime=true&multiStatements=true" \
go test ./... -count=1
```

- `VELOX_TEST_POSTGRES` / `VELOX_TEST_MYSQL` hold the **velox** DSN. Unset or
  unreachable → that backend skips.
- **Separate databases per ORM.** velox and ent migrate identical table names,
  so on one shared server they live in different databases: velox owns the base
  database (`parity`), ent owns the sibling `parity_ent`. The harness derives the
  ent DSN by switching the database name and creates `parity_ent` if missing
  (catalog-checked `CREATE DATABASE` on Postgres, `CREATE DATABASE IF NOT EXISTS`
  on MySQL). If the test user lacks the privilege to create it, the backend
  **skips** with a clear message rather than failing.
- **Isolation by truncate.** Each backend migrates once per test; before every
  program both ORMs' tables are truncated (FK checks off → `TRUNCATE`/restart
  identity → on) so programs never bleed into one another.

### Coverage matrix

`runner.CoverProgramSet(progs).MissingOpKinds()` measures which op kinds the
curated suite exercises. `coverage_test.go::TestCoverage_AllOpKindsExercised`
fails if any of the 14 op kinds is never exercised — so the suite must grow when
a new op is added (don't weaken the assertion; add a case). It also pins that the
JSON-label, O2M (`author.posts`), and M2M (`post.tags`) surfaces are reached.

### CI gating

The `parity` job in `.github/workflows/ci.yml` runs the full SQLite + Postgres +
MySQL matrix (with service containers) on every push/PR and **blocks merges** —
a `VeloxBug` verdict on any dialect fails the build.

## Reading a failure

On any non-`Pass` op the `Report.String()` prints the failing op, its verdict,
the structured mismatches against the reference for both ORMs, and the SQL each
ORM emitted for that op (captured via the debug driver). A `VeloxBug` therefore
shows the failing op, the three values, and the two SQL statements side by side.

### Documented finding: ent's JSON-array append defect is SQLite-specific

The curated `json_append` case has a **different verdict per backend**, and the
matrix is what surfaces that:

| backend  | verdict        | why                                                        |
|----------|----------------|------------------------------------------------------------|
| SQLite   | `EntDivergent` | ent emits `JSON_INSERT(labels, '$[#]', ?)`; SQLite rejects it as "malformed JSON" on the blob-stored value. velox CASTs to TEXT first (`json_each(CAST(... AS TEXT))`) and succeeds. |
| Postgres | all-`Pass`     | ent's jsonb append works; velox also correct.              |
| MySQL    | all-`Pass`     | ent's `JSON_ARRAY_APPEND` works; velox also correct.       |

velox matches the reference on **all three** dialects. The `json_append` case
therefore asserts only that velox is correct (zero `VeloxBug`s, no
`ReferenceSuspect`) and tolerates **either** all-`Pass` **or** `EntDivergent` —
it does not require the divergence, because Ent's defect is SQLite-only. This is
itself a finding the matrix documents: a JSON-append bug that a SQLite-only
harness would have wrongly attributed as a general "ent is broken" signal is
revealed to be engine-specific.

A `VeloxBug` verdict on **any** dialect (velox ≠ reference while ent = reference)
is a real velox bug the harness is designed to catch — it fails the suite and CI
rather than being silenced.

## Layout

- `op/` — typed operation model and `Program`.
- `model/` — the reference oracle and normalized result types (`Value`, `Row`,
  `Ref`, `Result`, `PageInfo`).
- `compare/` — structured `Diff`, error taxonomy, and verdict `Classify`.
- `runner/` — executors, driver, dialect harness, and coverage matrix.
  `run_velox*.go` / `run_ent*.go` are the only files allowed to import the ORMs
  (pinned by `architecture_test.go`); everything else stays ORM-free. The
  Postgres/MySQL client wiring lives in `run_velox_dialects.go` (ORM-importing,
  hence the `run_velox` prefix); the ORM-free DSN derivation, database creation,
  and truncation live in `db_postgres.go` / `db_mysql.go`. `coverage.go` is the
  op/field/edge coverage recorder.
- `suite_test.go` — the curated three-way suite, parametrized over all backends.
- `coverage_test.go` — the coverage-matrix assertions.

Specs:
- `docs/superpowers/plans/2026-05-30-parity-harness-A3a-executors-driver.md`
- `docs/superpowers/plans/2026-05-30-parity-harness-A3b-dialects-ci.md`
