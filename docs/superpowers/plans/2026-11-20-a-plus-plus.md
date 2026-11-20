# A++ round: recording sink, lean-insert fixes, defaults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix every confirmed finding of the 2026-11-20 engineering review (7.3/10) and usability review (8/10) of ADR 0093/0094/0095, so the features are correct, safe and easy in real use.

**Architecture:** One public API change (the Sink gets the write's transaction handle and the write's Changes in one call, always inside the transaction); the rest are engine fixes in lean inserts and recording, a generator diagnostic, and a cleanup of the recording engine code.

**Tech Stack:** Go 1.27, velox engine (`internal/engine`), velox-gen (`internal/codegen`), PostgreSQL 18 / 16, MySQL 8.4, SQLite.

**Spec:** this plan's Decisions section (controller design calls on the review findings) plus `docs/decisions/0093-lean-inserts.md` and `docs/decisions/0094-change-recording.md`, which Task 1 and Task 2 amend.

## Decisions (binding)

D1. **Sink signature.** `type Sink interface{ Record(ctx context.Context, tx *DB, changes []Change) error }`; `RecordFunc func(ctx context.Context, tx *DB, changes []Change) error`. One call per write method call (Exec, One, Insert, InsertAll, Rel.Add…), with every Change of that call in order. `tx` is the write's own transaction handle with recording off: writes through it join the transaction and are not recorded (this replaces R6's context guard, which is deleted).
D2. **The sink always runs inside the write's transaction, before COMMIT.** Outside a caller's transaction velox opens one for a recorded write. A sink error rolls the write back, everywhere. In a caller's transaction the write and the sink run under a savepoint (as now). The post-COMMIT delivery path and the velox-frame `pend` queue are removed. Code that acts after commit uses a `Recorder` read after `Tx` returns (it already drops rolled-back rows). `Recorder` ignores `tx`.
D3. **Ignored columns.** A column filled by an `UpdateDefault` (e.g. `updated_at`) is ignored by default: a change to only such columns is not reported, and they are left out of `Diff`. `RecordIgnore` becomes typed and generic: `func RecordIgnore[E any](cols ...Column[E]) RecordOption`; the string form is removed. `RecordKeep[E any](cols ...Column[E])` re-includes an UpdateDefault column.
D4. **Null-aware getters.** Null* handle kinds gain lazy generic `OldPtr(c Change) *V` and `NewPtr(c Change) *V` (nil for NULL, for a missing row, or another entity's Change). `Old`/`New` keep returning V.
D5. **Snapshots normalise time.** `time.Time` values in recorded old/new rows (and so in OldRow/NewRow JSON and Diff) are in UTC.
D6. **All-empty insert.** An INSERT whose every column is left empty sends `INSERT INTO t DEFAULT VALUES` (PG, SQLite) / `INSERT INTO t () VALUES ()` (MySQL), with the usual read-back; declared defaults apply.
D7. **InsertAll stays one statement on PostgreSQL and MySQL.** A cell left to a declared default is written as the `DEFAULT` keyword in a multi-row VALUES; runs are split only on SQLite (no DEFAULT keyword in VALUES). Read-back as today (RETURNING / MySQL select by key).
D8. **Upsert fill never misaligns.** When a statement's `Conflict.Where` is non-empty for any reason (policy, interceptor, Rewrite hook), RETURNING rows are not copied by position: they are matched to entities by exact conflict-column values (RETURNING includes the conflict columns); a returned row that matches no entity, or matches more than one, is an error (rolled back). Without `Conflict.Where` positional fill stays.
D9. **MySQL single-row upsert reads back what it left to the database**, by conflict values after the upsert (the database compares), same transaction; multi-row MySQL upserts document that defaulted columns are not read back.
D10. **Defaults and validation.** A zero value in a column the schema gives a declared DB default skips the field's validators (the database fills it), so enums with `SchemaDefault` work. velox-gen warns (not refuses) for a non-zero `SchemaDefault`/`sqlschema.Default` on a field that is not Nillable: "the zero value cannot be stored through Insert; make it Nillable to store it".
D11. **MySQL key-assigning update fails closed.** After a key-assigning recorded update on MySQL, the re-read row count must equal the rows the locked read found; otherwise error, rolled back.
D12. **Engine cleanup.** One helper scans RETURNING rows for update, delete, upsert and PG18 old/new; recording fields on `Statement` move into one embedded unexported struct; `EqualValues` caches its per-type method lookup and treats NaN as equal to NaN; `DB.Record`'s godoc is cut to the essentials with a pointer to the guide.

## Global Constraints

- A non-recording handle sends exactly what it sends at 4e5a5304, except D6 (an all-empty insert), D7 (InsertAll on PG/MySQL), D8 (conflict-where fill) and D9 (MySQL single-row upsert read-back), which change non-recording SQL on purpose; their goldens are updated and listed in the commit.
- Nothing about recording is read from or stored in a `context.Context` (after D1 there is no exception).
- `TestGenericTypesCompileSmall` bounds are not raised.
- Public API changes go through the API snapshot with the diff read.
- Every fix has a test that failed before it.
- Commit style `area: what`, staged by name, ending `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push. Subagents never run `scripts/ci-local.sh` or docker.

## Review Focus

1. A sink that writes many audit rows with `tx.InsertAll` inside a caller's transaction that later rolls back: audit rows gone, Recorder empty.
2. A recorded write outside any transaction whose sink errors: nothing committed.
3. InsertAll of 10k rows alternating defaulted/set cells: one statement per MySQL/PG chunk (WithMaxParams), keys and defaults right.
4. An upsert with a Rewrite-added `Conflict.Where` that rejects some rows: no entity gets another row's key.
5. An enum with `SchemaDefault("new")` inserted with its zero value: stored "new", no validation error.

---

### Task 1: Sink gets the transaction and the write's Changes, always inside the transaction (D1, D2, D3)

**Files:** root `record.go`, `change.go`; `internal/engine/record.go`, `change.go`, `record_upsert.go`, `bidi.go`, `tx.go`, `adopt.go`, `twrite.go`; tests in `internal/apitest/record_*_test.go`, `internal/integration/record_test.go`, `record_internal_test.go`; `example_record_test.go`; docs `docs/decisions/0094-change-recording.md`, `docs/guide.md` "Recording changes", `CHANGELOG.md`.

- [ ] Step 1: failing tests — `TestSinkGetsTx` (audit rows written with `tx.InsertAll` commit with the write and roll back with the caller's tx; not recorded themselves), `TestSinkErrorOutsideTxRollsBack` (real SQLite: nothing committed), `TestSinkOneCallPerWrite` (bulk update of 3 rows → one call with 3 Changes in key order), `TestUpdateDefaultIgnored` (a no-op update that only bumps updated_at reports nothing; `RecordKeep(I.UpdatedAt)` reports it), `TestRecordIgnoreTyped`.
- [ ] Step 2: run, expect FAIL.
- [ ] Step 3: implement; delete the R6 ctx guard, the post-COMMIT path and `pend`; update every existing recording test and golden (outside-tx recorded writes now carry BEGIN/COMMIT).
- [ ] Step 4: `go test -race . ./internal/engine ./internal/apitest ./internal/integration ./schema/...`, API snapshot, compile cost, `scripts/quick.sh` → `quick: ok`.
- [ ] Step 5: commit `velox: a sink gets the write's transaction and its changes, before commit`.

### Task 2: Lean-insert correctness (D6, D7, D8, D9)

**Files:** `internal/engine/lean.go`, `twrite.go`, `insert.go`, `render.go`, `record_upsert.go`; tests `internal/apitest/dbdefault_test.go`, `internal/integration/dbdefault_test.go`; `docs/decisions/0093-lean-inserts.md`.

- [ ] Step 1: failing tests — `TestInsertAllEmptyUsesDefaults` (PG/SQLite `DEFAULT VALUES`, MySQL `() VALUES ()`; real SQLite stores 'new'), `TestInsertAllOneStatementWithDefaultCells` (PG/MySQL goldens with `DEFAULT`; SQLite still runs), `TestUpsertConflictWhereFillsByKey` (Rewrite-added WHERE rejecting row 1: rows[0] unfilled, rows[1] id 42), `TestMySQLUpsertReadsBackDefaults` (single row).
- [ ] Step 2: run, expect FAIL.
- [ ] Step 3: implement; recorded paths keep working (Task 1's tests stay green).
- [ ] Step 4: same verification as Task 1.
- [ ] Step 5: commit `engine: lean inserts keep one statement and never misalign read-back`.

### Task 3: Defaults and validation (D10)

**Files:** generated validation path (find where field validators run for inserts: `internal/engine` or codegen output), `internal/codegen/lower.go` (warning), codegen goldens if output changes; tests in `internal/apitest` and `internal/codegen`.

- [ ] Step 1: failing tests — `TestEnumSchemaDefaultZero` (real SQLite: stored "new", no error), `TestNonZeroDefaultWarning` (velox-gen warns for `field.Bool("a").SchemaDefault(true)` and `field.Int("n").SchemaDefault(5)`; no warning for Nillable or zero defaults).
- [ ] Step 2–4: run FAIL, implement, verify (plus `go generate` checks if codegen output changes).
- [ ] Step 5: commit `velox: a zero value left to a database default skips validation; velox-gen warns on non-zero defaults`.

### Task 4: Recording polish (D4, D5, D11)

**Files:** `internal/handlegen/handlegen.go` (+ regenerate `handles.go`), root `change.go`, `internal/engine/record.go`, `change.go`; tests.

- [ ] Step 1: failing tests — `TestNullHandlePtrGetters`, `TestSnapshotTimesUTC` (insert's NewRow JSON and a later update's OldRow JSON use the same `Z` form), `TestMySQLKeyNormaliseFailsClosed` (fake: re-read finds fewer rows → error + ROLLBACK).
- [ ] Step 2–4: run FAIL, implement, verify; compile cost unchanged for handle kinds.
- [ ] Step 5: commit `velox: Null handles give OldPtr/NewPtr; recorded times are UTC; MySQL key re-read fails closed`.

### Task 5: Engine cleanup and docs (D12)

**Files:** `internal/engine/change.go`, `record_upsert.go`, `lean.go`, `statement.go`, `rewrite.go`, `render_cache.go`, root `record.go`, `CHANGELOG.md` (nil-nullable behaviour under "Changed"; this round's changes), ADR 0093/0094.

- [ ] Step 1: no behaviour change — every existing test and golden stays green unchanged; add `TestEqualValuesNaN` and a benchmark showing the cached lookup.
- [ ] Step 2: refactor; `go test -race` everything touched; compile cost; quick.sh.
- [ ] Step 3: commit `engine: one RETURNING scan and one recording state on Statement`.

### Task 6: Handle kinds named by what the column holds (D13)

D13 (user-chosen 2026-11-20): `OrderedCol`/`NullOrderedCol` are replaced. `time.Time` → `TimeCol`/`NullTimeCol`. A `field.Other` whose Go type is a registered numeric type (shopspring `decimal.Decimal` registered by default; others via the existing type-registration mechanism) → `NumberCol`/`NullNumberCol`, gaining Sum/Avg/Add/Sub. Any other `field.Other` type → `OtherCol`/`NullOtherCol` (EQ/NEQ/In/GT/GTE/LT/LTE/Between/Min/Max, Set, Old/New). `OrderedCol` is removed (unreleased API). Evidence: nextapp has 2,438+2,350 time and 2,292+14 decimal OrderedCol uses, nothing else.

**Files:** `internal/handlegen/handlegen.go` (+ `go generate .` → `handles.go`), `internal/codegen/lower.go` (kind mapping ~383-400), codegen goldens, every examples/ generated package (regenerate), `docs/guide.md` handle table, CHANGELOG, an ADR (next number) for the naming rule.

- [ ] Step 1: failing tests — codegen maps time→TimeCol, decimal→NumberCol, other→OtherCol (+Null variants); `TestDecimalNumberCol` (real SQLite: `Sum`, `Add` on a decimal column via a registered numeric type).
- [ ] Step 2–4: run FAIL, implement, regenerate everything (`go generate ./...` where the repo does; examples `velox-gen`), API snapshot, compile cost (TimeCol/OtherCol must cost no more than OrderedCol did), quick.sh.
- [ ] Step 5: commit `velox: handles are named by what the column holds (TimeCol, OtherCol; decimals are numbers)`.
