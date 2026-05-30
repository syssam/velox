# Parity Harness (velox ⟷ ent differential testing)

Model-based differential testing for the velox ORM. Each test program is a
typed `op.Program` (a replayable list of operations: creates, edge mutations,
JSON set/append, queries, aggregates, Relay pagination). The same program is run
three ways and compared:

- **reference** — an ORM-free in-memory oracle (`model.Run`) that defines the
  correct observable outcome.
- **velox** — the generated velox client (`runner.RunVelox`).
- **ent** — the generated Ent client (`runner.RunEnt`), the parity baseline.

`runner.RunParity(t, backend, prog)` runs all three on fresh, isolated
in-memory SQLite clients, normalizes every result back to ORM-neutral
`model.Result` (db ids → creation handles, deterministic parity clock for
timestamps), and classifies each op with a three-way verdict:

| velox vs ref | ent vs ref | verdict             | meaning                          |
|--------------|------------|---------------------|----------------------------------|
| match        | match      | `Pass`              | no divergence                    |
| differ       | match      | `VeloxBug`          | velox is wrong (the target)      |
| differ       | differ     | `ReferenceSuspect`  | oracle/program likely wrong      |
| match        | differ     | `EntDivergent`      | ent is the outlier               |

## Running

```bash
cd tests/parity
go test ./...                          # full SQLite suite + executor tests
go test -run TestCuratedSuite_SQLite -v
```

Postgres/MySQL backends and the op/field/edge coverage matrix are forthcoming
in A3b (env-gated `VELOX_TEST_*`); only SQLite is wired today.

## Reading a failure

On any non-`Pass` op the `Report.String()` prints the failing op, its verdict,
the structured mismatches against the reference for both ORMs, and the SQL each
ORM emitted for that op (captured via the debug driver). A `VeloxBug` therefore
shows the failing op, the three values, and the two SQL statements side by side.

### Known finding (SQLite): ent JSON-array append

The curated `json_append` case is an `EntDivergent`: ent's generated SQLite SQL
for appending to a JSON array runs `JSON_INSERT(labels, '$[#]', ?)` against a
`labels` column that SQLite stores as a BLOB, which SQLite rejects as "malformed
JSON". velox CASTs the value to TEXT first (`json_each(CAST(... AS TEXT))`) and
succeeds. The harness surfaces this (it is not silenced); the case asserts only
that velox matches the reference (zero `VeloxBug`s) while recording ent's
divergence. The same JSON-append class is exercised against Postgres in A3b.

## Layout

- `op/` — typed operation model and `Program`.
- `model/` — the reference oracle and normalized result types (`Value`, `Row`,
  `Ref`, `Result`, `PageInfo`).
- `compare/` — structured `Diff`, error taxonomy, and verdict `Classify`.
- `runner/` — executors and driver. `run_velox*.go` / `run_ent*.go` are the only
  files allowed to import the ORMs (pinned by `architecture_test.go`); everything
  else stays ORM-free.
- `suite_test.go` — the curated three-way suite.

Spec: `docs/superpowers/plans/2026-05-30-parity-harness-A3a-executors-driver.md`.
