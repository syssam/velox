# Change Recording Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Writes through a recording handle (`db.Record(sink)`) report each changed row's old and new values to a `Sink`, replacing the hook API `Mutation.Track` / `Changes`.

**Architecture:**
- The handle is an `engine.DB` copy carrying a `*recording` (sink, ignore set, a re-entry guard), derived the way `WithoutPolicy` derives handles.
- `writeCore` sends a recorded update or delete through the existing `trackedWrite`, which now reads every column; inserts emit after their send.
- PostgreSQL ≥ 18 uses `RETURNING old.*, new.*` in one statement.
- The root package wraps it all in non-generic types; only `RowsOf[E]` is generic.

**Tech Stack:** Go 1.27, velox engine (`internal/engine`), PostgreSQL 18 / MySQL 8.4 / SQLite in CI.

**Spec:** `docs/superpowers/specs/2026-11-20-change-recording-design.md`. The rule numbers below (R1–R11) are that spec's numbered Rules.

## Global Constraints

- Nothing about recording is read from or stored in a `context.Context`. The one exception is the re-entry guard of R6, which is internal and never exported.
- A non-recording handle sends exactly the statements it sends today. Existing SQL goldens must not change.
- No new module dependencies.
- No entity package grows: `TestGenericTypesCompileSmall` bounds stay as they are, except `entityDescHooks` and `entityDescMixin` may only shrink, since Track/Changes leave Mutation.
- Public API changes go through `go test ./internal/repocheck -run TestPublicAPISnapshot -update-api`, with the diff read.
- One ADR: rewrite `docs/decisions/0094-track-changes.md` as `0094-change-recording.md` (unpushed, so rewritten in place), with the rejected designs. Add a CHANGELOG line under Unreleased/Added and update the README index row.
- Commit style is `area: what`, staged by name, ending `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.
- Subagents do not run `scripts/ci-local.sh`. The controller runs the merge gate once at the end.

## Review Focus

1. **A sink that writes its audit row through the recording handle** must not recurse (R6). Test in Task 3: `TestRecordSinkWritesAreNotRecorded`.
2. **The outer `db` used with a transaction's ctx** is refused (`ErrOutsideTx`) and records nothing. Test in Task 2: `TestRecordOuterHandleInTxIsRefused`.
3. **An update that sets a column to its current value**, or changes only `RecordIgnore` columns, reports nothing, including MySQL's "matched but unchanged" rows. Test in Task 3: `TestRecordUnchangedRowsAreSkipped`.
4. **One `Recorder` shared by goroutines** on a non-transaction handle is race-free under `-race`. Test in Task 1: `TestRecorderConcurrent`.
5. **An update that assigns the primary key** reports the row under its new key, with the old key in `OldValues`. Test in Task 3: `TestRecordKeyChange`.

---

### Task 1: Public types and the Recorder

**Files:**
- Modify: `veloxtype/change.go` (delete `Change`; `FieldChange` loses `Changed`)
- Modify: `change.go` (root: remove `Mutation.Track`/`Changes`; add types and `RowsOf`; keep `Diff`)
- Create: `record.go` (root: `Sink`, `RecordFunc`, `Recorder`, `RecordOption`, `RecordIgnore`)
- Modify: `internal/engine/change.go` (drop `TrackByName`, `ChangesOf` and `trackState`'s public entry; keep `tracker`, `DiffRows`, `EqualValues`)
- Test: `record_test.go`, `internal/engine/change_test.go`

**Interfaces:**
- Produces:
  - `engine.ChangeRow{Op Op; Soft bool; In *EntityInfo; Old, New unsafe.Pointer}` is what the engine produces.
  - Root `velox.Change{Entity string; Op Op; Key any; Soft bool; row engine.ChangeRow}` is defined in `change.go`, not as a `veloxtype` alias: `veloxtype` cannot name engine types.
  - `veloxtype.Change` is deleted. `veloxtype.FieldChange{Name string; Old, New any}` stays, aliased in the root.
  - Methods `OldValues() map[string]any`, `NewValues() map[string]any`, `ChangedFields() []FieldChange`.
  - `velox.RowsOf[E any, PE Entity[E]](c Change) (old, cur *E, ok bool)`.
  - `velox.Sink`, `velox.RecordFunc`, `*velox.Recorder` (methods `Record`, `All() iter.Seq[Change]`, `Len`, `Reset`), `velox.RecordOption`, `velox.RecordIgnore(columns ...string) RecordOption`.
  - Engine-side: `engine.Recording{Sink func(ctx, Change) error; Ignore []string}`, plus `engine.RecorderMark(s any) (mark func() int, truncate func(int))`. The latter is the hook R5 uses; it is set only when the sink is a `*Recorder`.

- [ ] **Step 1: Write the failing tests** in `record_test.go`:
  - `TestChangeValues`: build a Change for `testmodel.Invoice` through an engine test helper. Assert:
    - `OldValues()["total"] == 10.0` and `NewValues()["total"] == 15.0`;
    - `ChangedFields() == []FieldChange{{"total", 10.0, 15.0}}`;
    - a `Sensitive` column shows `"***"` in all three;
    - an insert Change has `OldValues() == nil` and `ChangedFields() == nil`;
    - a delete Change has `NewValues() == nil`.
  - `TestRowsOf`: `RowsOf[testmodel.Invoice]` returns typed rows and `ok == true`; `RowsOf[testmodel.Ticket]` on an Invoice Change returns `nil, nil, false`.
  - `TestRecorderConcurrent`: 8 goroutines × 100 `Record` calls, then `Len() == 800`. Run with `-race`.
  - `TestRecorderAllIsASnapshot`: a `Record` call after `All()` has started does not appear in that iteration.
- [ ] **Step 2: Run** `go test -race -run 'TestChangeValues|TestRowsOf|TestRecorder' .`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** the types.
  - Map values come from `in.kit.Value` through `plain`, as `Mutation.Value` does.
  - Equality uses `engine.EqualValues`.
  - `Recorder` is a `sync.Mutex` plus `[]Change`; `All` copies under the lock.
- [ ] **Step 4: Remove** `Mutation.Track` and `Mutation.Changes`, and their tests: `internal/apitest/track_test.go`, `track_sqlite_test.go`, `example_track_test.go`. Task 3 moves their SQL assertions onto recording handles.
- [ ] **Step 5: Run** `go test -race . ./internal/engine ./veloxtype`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: Change, Recorder and Sink for recording writes`.

### Task 2: The recording handle and where it propagates

**Files:**
- Modify: `internal/engine/db.go` (field `rec *Recording` on `DB`; method `Record(r *Recording) *DB` copies like `WithoutPolicy`, `policy.go:16`)
- Modify: `internal/engine/tx.go:270` and `:532` (the tx and savepoint `DB` keep `rec`)
- Modify: `db.go` (root: `func (db *DB) Record(s Sink, opts ...RecordOption) *DB`)
- Modify: `policy.go:194` (`Mutation.DB()` returns the recording handle the mutation ran on; verify only)
- Test: `internal/apitest/record_handle_test.go`

**Interfaces:**
- Consumes: Task 1's `engine.Recording`.
- Produces: `(*engine.DB).Recording() *Recording` (nil when not recording), which Tasks 3–6 check.

- [ ] **Step 1: Write the failing tests** (fake driver):
  - `TestRecordHandleOnly`: an update via `db` sends today's SQL exactly (the existing golden) and records nothing. The same update via `db.Record(&rec)` records.
  - `TestRecordPropagates`: `rdb.Tx(...)`'s `tx`, a nested savepoint, and `rdb.WithoutPolicy("r")` each record; `db.Record(&a).Record(&b)` records into `b` only.
  - `TestRecordOuterHandleInTxIsRefused`: inside `rdb.Tx(ctx, fn)`, `db.Update…Exec(fnCtx)` returns `ErrOutsideTx`, and `rec.Len() == 0`.
  - `TestRecordHookDBRecords`: a hook's write via `m.DB()` on a recording handle is recorded.
- [ ] **Step 2: Run** them. Expected: FAIL.
- [ ] **Step 3: Implement** the handle field and its copying.
- [ ] **Step 4: Run** `go test ./internal/apitest -run TestRecord`. Expected: PASS for the handle tests. Write recording itself comes in Task 3.
- [ ] **Step 5: Commit** `velox: db.Record returns a handle that records its writes`.

### Task 3: Recording updates and deletes (locked-read path)

**Files:**
- Modify: `internal/engine/write.go:272` (`if r := db.Recording(); r != nil && (st.Op == OpUpdate || st.Op == OpDelete)` → `trackedWrite` with every column, then `emit`)
- Modify: `internal/engine/change.go` (`trackedWrite` takes the columns instead of a `trackState`; `tracker.changes` produces `ChangeRow`s; skip unchanged rows (R2); `MaxRows`)
- Create: `internal/engine/record.go` (`emit(ctx, db, rows []ChangeRow) error`; the re-entry guard for R6, an unexported context key set while the sink runs; the R4 outside-transaction rule: commit, then call the sink)
- Test: `internal/apitest/record_write_test.go`, `internal/engine/record_test.go`

**Interfaces:**
- Consumes: `db.Recording()`, `ChangeRow`.
- Produces: `emit`, which Tasks 4–6 call.

- [ ] **Step 1: Write the failing tests.** Exact SQL per dialect for each, asserted against `fakeconn`:
  - `TestRecordUpdateSQL`: on PostgreSQL with `engine.WithoutReturningOld()` (the Task 7 test knob; until then the fallback is the only path):
    ```
    BEGIN
    SELECT "id", <every column> FROM "invoices" WHERE "total" < $1 AND "org_id" = $2 ORDER BY "id" FOR UPDATE
    UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "total" < $3 AND "org_id" = $4 AND "id" IN ($5, $6) RETURNING <every column>
    COMMIT
    ```
    SQLite is the same without `FOR UPDATE`. MySQL adds ``SELECT … WHERE `id` IN (?, ?)`` after the UPDATE.
  - `TestRecordDeleteSQL`: the locked read, then `DELETE … AND "id" IN (…)`. Changes have `NewValues() == nil`.
  - `TestRecordPinnedUpdateNoTx`: `Where(I.ID.EQ(1))` outside a transaction sends no BEGIN when the row is pinned by key.
  - `TestRecordUnchangedRowsAreSkipped`: setting `total` to its current value, or changing only `updated_at` under `RecordIgnore("updated_at")`, gives `rec.Len() == 0`.
  - `TestRecordKeyChange`: `Set(T.ID.Set(9))` on id 1 gives `Key == int64(9)` and `OldValues()["id"] == int64(1)`.
  - `TestRecordSinkWritesAreNotRecorded`: a `RecordFunc` that inserts an audit row through `rdb` produces one audit row per change, and the audit insert is not recorded.
  - `TestRecordSinkErrorInTx`: the sink's error rolls back the user's `Tx`; the row is unchanged afterwards (real SQLite).
  - `TestRecordSinkErrorOutsideTx`: the error is returned and the row stays changed (real SQLite).
  - `TestRecordTooManyRows`: with `MaxRows(2)`, an update matching 3 rows returns `ErrTooManyRows` and changes nothing.
- [ ] **Step 2: Run** `go test ./internal/apitest -run 'TestRecord(Update|Delete|Pinned|Unchanged|Key|Sink|TooMany)'`. Expected: FAIL.
- [ ] **Step 3: Implement** in `write.go`, `change.go` and `record.go`.
  - Delete's locked read selects every column.
  - The policy scope of the read is the write's own (Writable), as `trackedWrite` already applies.
- [ ] **Step 4: Run** the tests and `go test -race ./internal/engine ./internal/apitest`. Expected: PASS.
- [ ] **Step 5: Commit** `velox: a recording handle reports each updated and deleted row`.

### Task 4: Recording inserts and upserts

**Files:**
- Modify: `internal/engine/insert.go` (`sendOne`), `internal/engine/lean.go` (`SendBatch`); both call `emit` after the send, from the entity rows (already holding what RETURNING or the MySQL fill read back)
- Create: `internal/engine/record_upsert.go` (below PostgreSQL 18: a locked read by the conflict columns before the INSERT, then classify each row as insert or update)
- Test: `internal/apitest/record_insert_test.go`

**Interfaces:**
- Consumes: `emit`.

- [ ] **Step 1: Write the failing tests:**
  - `TestRecordInsert`: `Insert` and `InsertAll` give one Change per row, in the order given, with `NewValues()["status"] == "new"` filled by a declared DB default (`dDef` from `internal/apitest/dbdefault_test.go`). No statement is added beyond today's goldens.
  - `TestRecordUpsert`: one row conflicts and one is new. Expect an `OpUpdate` Change with the real old value and an `OpInsert` Change.
    - SQL below PostgreSQL 18: `SELECT <cols> FROM "tickets" WHERE ("org_id", "title") IN (…) FOR UPDATE`, then the INSERT. SQLite and MySQL use the OR-of-ANDs form `keysIn` already renders.
  - `TestRecordUpsertDoNothing`: a kept row is not reported.
- [ ] **Step 2: Run** them. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - An upsert outside a transaction runs in a transaction velox opens.
  - On MySQL, the new values are read back by key after the INSERT.
- [ ] **Step 4: Run** `go test -race ./internal/apitest -run TestRecord`. Expected: PASS.
- [ ] **Step 5: Commit** `velox: a recording handle reports inserted and upserted rows`.

### Task 5: Soft delete, bidirectional edges, join tables

**Files:**
- Modify: `schema/mixin/softdelete/softdelete.go:135` (mark its update as a soft delete through an internal helper, `internal/engine.MarkSoftDelete(u)`, reached via `untyped` internals; no public API)
- Modify: `internal/engine/record.go` (a marked update emits `Op: OpDelete, Soft: true`)
- Modify: `internal/engine/bidi.go` (each re-point statement goes through the recorded path, or emits its rows)
- Delete: `errTrackBidi` and its refusal
- Test: `internal/integration/record_test.go` (soft delete on 3 databases), `internal/apitest/record_edges_test.go`

- [ ] **Step 1: Write the failing tests:**
  - `TestRecordSoftDelete`: `rdb.Delete[fixture.SDoc]().Where(D.ID.EQ(2))` gives one Change with `Op == OpDelete`, `Soft == true`, `OldValues()["deleted_at"] == nil`, and `NewValues()["deleted_at"]` set. Under `softdelete.Skip`, it gives `Soft == false` and `NewValues() == nil`.
  - `TestRecordBidiRepoint`: setting a spouse on a `testmodel` bidi O2O reports each row whose key changed.
  - `TestRecordM2MAdd`: `Rel.Add` reports join-entity `OpInsert` Changes, and `Remove` reports `OpDelete`.
- [ ] **Step 2: Run** them. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/apitest ./internal/integration ./schema/mixin/softdelete -run Record`. Expected: PASS.
- [ ] **Step 5: Commit** `velox: soft deletes, re-pointed edges and join rows are recorded`.

### Task 6: Recorder and rollback

**Files:**
- Modify: `internal/engine/tx.go` (at `Tx` and `savepoint` begin, take `mark()` when the handle's sink is a `*Recorder`; truncate on rollback and before each `TxRetrySerializable` attempt)
- Test: `internal/apitest/record_tx_test.go`

- [ ] **Step 1: Write the failing tests** (real SQLite):
  - `TestRecorderTxRollback`: a `Tx` whose fn errors after two recorded updates leaves `rec.Len() == 0`.
  - `TestRecorderSavepointRollback`: an outer update, then a nested `Tx` that records one update and fails. Expect `rec.Len() == 1`.
  - `TestRecorderRetry`: `TxRetrySerializable(2)`, where the first attempt fails with a serialization error after recording. Expect `rec.Len()` equal to the successful attempt's count. Simulate with the existing retry test helper.
  - `TestRecordFuncSeesRollback`: a `RecordFunc` is called for changes of a rolled-back savepoint (R5).
- [ ] **Step 2: Run** them. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** them. Expected: PASS.
- [ ] **Step 5: Commit** `velox: a Recorder drops what a rolled-back transaction recorded`.

### Task 7: PostgreSQL 18, one statement

**Files:**
- Create: `internal/engine/pgversion.go` (`serverVersion(ctx, db) int`, read once per pool with `SHOW server_version_num`, cached in the shared state; replicas: the primary)
- Modify: `internal/engine/render.go` (render `RETURNING old."c", …, new."c", …` for UPDATE and an upsert, and `RETURNING old."c", …` for DELETE, when `Statement.returnOld` is set)
- Modify: `internal/engine/change.go` (PostgreSQL ≥ 18: one statement, rows scanned as old and new pairs; a not-pinned write outside a transaction runs in one, for `MaxRows`)
- Add: `engine.WithoutReturningOld()`, a DB option for tests that forces the locked-read path
- Test: `internal/apitest/record_pg18_test.go` (fake driver answering `SHOW server_version_num` with 180000 or 170000), `internal/integration/record_test.go` (the CI PostgreSQL is 18)

- [ ] **Step 1: Write the failing tests:**
  - `TestRecordPG18UpdateSQL`: version 180000 gives exactly one statement:
    `UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "id" = $3 AND "org_id" = $4 RETURNING old."id", …, new."id", …`.
    The same with 170000 gives Task 3's statements.
  - `TestRecordPG18Delete`: `DELETE … RETURNING old."id", …`.
  - `TestRecordPG18Upsert`: `INSERT … ON CONFLICT (…) DO UPDATE SET … RETURNING old."id", …, new."id", …`. An old with a NULL key is an insert.
  - `TestRecordVersionReadOnce`: two recorded writes send one `SHOW server_version_num`.
  - Integration `TestRecordOnRealDatabases`, on each database: update, delete, upsert, soft delete. Old and new match what a separate read shows. On PostgreSQL, run once with the default (18) and once with `WithoutReturningOld()`.
- [ ] **Step 2: Run** them. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/apitest -run PG18` and `go test ./internal/integration -run Record`. Expected: PASS (SQLite locally; PostgreSQL and MySQL in the merge gate).
- [ ] **Step 5: Commit** `engine: PostgreSQL 18 reads old and new values in the write itself`.

### Task 8: Docs, example, snapshot, gate

**Files:**
- Rewrite: `docs/decisions/0094-track-changes.md` → `docs/decisions/0094-change-recording.md` (decision, the rules R1–R11, the rejected designs table from the spec), plus its README row
- Modify: `CHANGELOG.md` (replace the Track line), `docs/guide.md` (replace the Hooks section on Track with a "Recording changes" section showing the audit example and its SQL), `docs/coming-from-ent.md` (`m.OldX(ctx)` → `db.Record`)
- Create: `example_record_test.go` (`ExampleDB_Record`: a nextapp-like audit batch with `OldValues` / `NewValues` / `ChangedFields`, real SQLite, `// Output:`)
- Update: `internal/repocheck/testdata/api.txt`

- [ ] **Step 1: Write** `ExampleDB_Record` and run `go test -run ExampleDB_Record .`. Expected: PASS, with output of the form `Invoice 1 update total 10 -> 15`.
- [ ] **Step 2: Update** the API snapshot and read the diff. Expected: only the names in Task 1's Interfaces added, and `Track`, `Changes`, `Change.Changed`, `FieldChange.Changed`, `Change.Fields` removed.
- [ ] **Step 3: Run** `go test -run TestGenericTypesCompileSmall -v ./internal/repocheck`. Expected: every bound held, with `entityDescHooks` and `entityDescMixin` at or below their current measurements.
- [ ] **Step 4: Run** `scripts/quick.sh`. Expected: `quick: ok`.
- [ ] **Step 5: Commit** `docs: change recording replaces Track (ADR 0094)`.
- [ ] **Step 6 (controller only): Run** the merge gate, `VELOX_CI_GROUPS=graphql bash <scratchpad>/gate.sh`. Expected: `ok`. Then also run with `VELOX_CI_POSTGRES_IMAGE=postgres:16` so the locked-read path runs on a real PostgreSQL below 18.
