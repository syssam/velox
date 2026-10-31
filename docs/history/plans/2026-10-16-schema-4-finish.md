# Schema DSL, Plan 4: one path — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the schema DSL and generated code the only way to declare an entity. This plan:
- fixes the runtime gaps plans 2 and 3 left;
- moves every remaining user onto the new path: the root tests' inline entities, the eight examples, bench/scale and the compile-cost tests;
- then deletes the old path, which is the runtime's struct scanning and `Schema(*velox.Schema)` API plus velox-gen's struct-scanner mode.

**Architecture:**
- **Runtime fixes first** (Task 1):
  - `HasWhere` resolves its relation lazily, which removes the deadlock in `Policy()`/`Hooks()` bodies;
  - `Mutation.DB()` lets a hook query in the write's own transaction;
  - `EdgeTo`'s target is checked against the field.
- **Migrations next** (Tasks 2–4). Each keeps CI green because the old path still exists.
- **The removal** (Task 5) deletes the old path and its tests, simplifies velox-gen to one mode, and makes `UpdateDefault` update-only.
- **Documents** last (Task 6).

**Tech Stack:** Go 1.27, standard library, `internal/tempmod`, SQLite, pgx v5, MySQL (itest).

**Spec:** `docs/history/specs/2026-10-15-schema-design.md`:
- §5–§6 (what this replaces);
- §7 (the full runtime suite on generated entities, compile-cost guards re-measured, bench/scale re-run).

The followups document's "Left by schema plan 2" and "Left by schema plan 3" sections list what this plan closes.

## Global Constraints

- **Go 1.27.**
  - Every method of a generic velox type is generic (`methods_test.go`).
  - Nothing runs at program start.
  - No `reflect` in generic code (Core §3 rule 7).
- **Core's budgets** (`go test -run Budget -v .`) pass, and the escape check holds:

  ```bash
  go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"
  ```

  It must print `leaking param content: st` and `read does not escape`.
- **Every test keeps its intent.** A test that only exercises the removed API (`Schema`, `Mixin`, `ColumnDef`, `RelDef`, `BeforeX`, `Shape`, struct scanning, the old generator) is deleted with that API. A test of behaviour (relations, scoping, policies, hooks, guards, rendering) is moved to a `Desc` or generated entity and keeps its assertions.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` match each other, and both pass:
  - race tests in the root, `cmd/velox-gen`, `example` and `contrib/otel`;
  - golangci-lint v2.14.0 in all four;
  - itest on PostgreSQL and MySQL;
  - velox-gen `-check` and `verify` on every schema package;
  - bench/scale.
- **No machine-specific paths** in committed files. Set `GOCACHE=<scratchpad>/velox2-gocache` and `GOFLAGS=-p=2` only in your shell; the machine is memory-constrained.
- **The full gate** is `<scratchpad>/fix1.sh`. It covers four modules, lint, the example, contrib/otel and itest on PostgreSQL and MySQL. Tasks 3–5 also change its velox-gen lines; update the script in step with CI.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes on the spec

1. **Lazy `HasWhere`.**
   - `Rel.HasWhere` records the owner and relation index (`relRef`) instead of resolving the link at call time.
   - `prepare` resolves every `opExists` node before the guard runs, copying on write as the scope does.
   - Why: a `Policy()` or `Hooks()` body that builds a predicate ran inside the entity's metadata build and deadlocked on its `once`.
   - If wrong: an invalid relation is reported when the statement runs, not when the predicate is built. That is the same error with a different trigger.
2. **`Mutation.DB()`.** `func (m *Mutation[E]) DB() *DB` returns the `*DB` the write runs on: the transaction's, inside `db.Tx`, or the database's.
   - A hook that reads or writes other rows uses it, so its work commits or rolls back with the write.
   - It is set only when the entity has hooks, so writes without hooks pay nothing. It is stored in `mutation` as an `any` and converted in the method: a typed `*DB` field made every Desc entity's package 79 KiB larger.
   - It replaces `velox.DBOf(ctx)`, a context key the user found ugly. Ent's equivalent is `m.Client()`.
3. **`EdgeTo` and `EdgeThrough` record their target's holder types.** `EdgeTo[T]` records `*One[T]` and `*Many[T]`. `buildDesc` refuses a relation whose `RelAddr` field has neither type: "relation %s is a %v, not a One or Many of %s". Mismatched hand-written or stale code is refused at first use, not loaded empty.
4. **`UpdateDefault` fills on update only, as in ent.** An insert fills a column from `Default` or `DefaultFunc` alone. `mixin.Time`'s `updated_at` has both `Default(time.Now)` and `UpdateDefault(time.Now)`, so inserts still fill it. Today's insert-time fill is the old path's `OnUpdate` rule, and it goes with the old path.
5. **bench/scale measures one generated package,** the user's choice ("orm is hard to per domain").
   - The application is a schema package of n schemas, generated into one `gen` package, and s services that each use five entities.
   - The metrics are:
     - `cold_build_s`;
     - `schema_edit_rebuild_s`: add a field to one schema, regenerate, rebuild every service;
     - `service_binary_mb`;
     - `link_peak_mb`.
   - The leaf and root metrics are gone: one package has no leaves.
   - Thresholds are re-baselined by the file's existing rule: 4× the largest of three idle runs for times, 2× for sizes. The `_measured` note is rewritten.
6. **The examples teach the new way.** Each example's `model/` becomes `schema/` (the DSL) plus `gen/` (generated, plus the hand-written `Policy()`/`Hooks()` files). The text and golden output are updated.
   - The hooks example shows the typed `Hooks()`:
     - an insert hook deriving `Slug`;
     - an update hook through `Mutation`;
     - a delete hook narrowing with `Where`;
     - `On`;
     - `m.DB()` for an audit row in the same transaction.
   - The mixin example becomes a generic hook function the entity lists, since mixins carry no behaviour (spec §3).
7. **velox-gen has one mode after Task 5:**

   ```
   velox-gen [-out dir] [-package name] [-check] [package]
   velox-gen verify [-out dir] [package]
   velox-gen describe [-json] [package]
   ```

   - The package defaults to `./schema`.
   - A pattern that names no schema package is an error: `no schemas in …`.

## Review Focus

1. **A `Policy()` whose body builds `Has()` outside its returned functions** must load and enforce the policy, not hang. Pinned in Task 1: `TestPolicyBodyMayBuildHas`, which uses a timeout.
2. **A hook that uses `m.DB()` inside `db.Tx`** must write in the same transaction, so a rollback undoes its row. Pinned in Task 1: `TestMutationDBIsTheTransaction`.
3. **A test moved off the old path** must assert what it asserted before. Pinned by review: Task 2's report lists every moved test with its old and new entity.
4. **After the removal**, no exported identifier of the old path may remain:
   - `Schema` the type, `Schemer`, `Mixer`, `ColumnDef`, `RelDef`, `Shape`, `RelationTo`;
   - `MetaOnce.Get`, `GeneratedCodeIsVersion2`.

   Pinned in Task 5: `TestOldAPIIsGone`, which greps the package's exported API with `go doc -all`.
5. **bench/scale's thresholds** must be set from idle runs, not from a loaded machine. Pinned in Task 4: the report records the load average of each run.

---

## File Structure

| File | Responsibility |
|---|---|
| `relation.go`, `db.go`, `render.go`, `scope.go`, `render_cache.go`, `pred.go` | lazy `opExists` resolution (Task 1) |
| `hook.go`, `scope_api.go` | `Mutation.DB` (Task 1) |
| `desc.go` | the holder-type check (Task 1); the "plan 3" header comment (Task 5) |
| root `*_test.go` with old-path entities | moved to `Desc` entities (Task 2) |
| `example/*/model` → `example/*/schema` + `example/*/gen` | the examples (Task 3) |
| `bench/scale/main.go`, `bench/scale/thresholds.json`, `bench/scale/scale_test.go` | one generated package (Task 4) |
| `schema.go`, `meta.go`, `relation.go`, `insert.go`, `write.go`, `compilecost_test.go`, `doc.go` | the old path removed (Task 5) |
| `cmd/velox-gen/{discover,classify,render,model,write,names,load}.go` and their tests, `cmd/velox-gen/testdata/testmodel` | the old mode removed (Task 5) |
| `scripts/ci-local.sh`, `.github/workflows/ci.yml` | velox-gen lines (Tasks 3–5) |
| `docs/history/specs/*`, `docs/history/plans/2026-10-14-core-1-followups.md`, `example/README.md` | documents (Task 6) |

---

### Task 1: Runtime fixes — lazy `HasWhere`, `DBOf`, the holder check

(Revised after review: `DBOf(ctx)` became `Mutation.DB()`; see Ruling 2. The text below is as first written.)

**Files:**
- Modify: `relation.go` (`HasWhere`, `relLink`), `pred.go` (the `opExists` value), `db.go` (`prepare` resolves), `scope.go`, `render.go`, `render_cache.go` (read the resolved link), `hook.go` (`DBOf`), `desc.go` (`EdgeTo`/`EdgeThrough` record the holder types; `buildDesc` checks them)
- Test: `relation_test.go` or a new `lazy_test.go`, `hook_test.go`, `desc_test.go`

**Interfaces:**
- Produces:

```go
// DBOf returns the DB a write runs on, inside that write's entity hooks:
// the transaction's, inside db.Tx, or the database's. ok is false outside
// a hook.
func DBOf(ctx context.Context) (*DB, bool)
```

- Internal: `HasWhere` builds `node{op: opExists, col: r.I, v: relRef{owner: ownerInfo, i: r.I}}`. `ownerInfo` is a non-generic `func() (*EntityInfo, error)` made from `metaOf[E]`, which runs only when called.

**How:**
- **Resolving.** `db.prepare` (and `dryRun`, which calls it) first walks `st.Where` and `st.Conflict.Where` and resolves every `opExists` whose `v` is a `relRef`. It calls `relRef.owner()`, takes `rels[i]` and calls `resolve`, then replaces `v` with the `link`.
  - Nodes are copied on write, so a shared `Pred` is never changed. Follow `scoper.node`'s copy rule.
  - An error from resolving fails the statement: `velox: <Entity>: <the relation error>`.
  - **Where the error used to surface:** today `HasWhere` puts an `opInvalid` node when the link fails. Make every caller that reads `predErr` treat an unresolved `relRef` as valid. The error surfaces at `prepare`.
- **`checkColumns`, `nodeKey`, `scope` and `render`** see only resolved links. The resolution pass runs before all of them. Keep the cache key: rel index plus kids.
- **Policy predicates.** A `Read`/`Write` predicate built inside a policy function is resolved like any other, because the scope's `policyCond` result goes through the same pass.
  - The scope adds policy conditions after `prepare`'s resolution pass. Either run the pass inside the scope as well, on the conditions it adds, or resolve inside `scoper.node` when it meets a `relRef`.
  - Choose one; both pass the tests. Say which in the report.
- **`DBOf`.** In `mutate` (hook.go), only when `len(in.hooks) > 0`, call `ctx = context.WithValue(ctx, dbKey{}, db)`. `mutate` does not have the `*DB` today, so pass it in from its callers, which do have it.
  - This must not regress the compile cost. Plan 2 found that a `*DB` field in `mutation` made every entity package carry DB's export data. Keep `*DB` out of `mutation` and out of every type an entity reaches; a parameter to the non-generic `mutate` is fine.
  - Run `go test -run TestGenericTypesCompileSmall .` to confirm.
- **The holder check.**
  - `EdgeTo[T, PT]` sets `Relation.holders = [2]any{elemType((*One[T])(nil)), elemType((*Many[T])(nil))}`.
  - `EdgeThrough[J, T, …]` records the same for T.
  - `buildDesc` compares `reflect.TypeOf(relAddr(i)).Elem()` with them and refuses a mismatch as a `*SchemaError`.

- [ ] **Step 1: Write the failing tests**
  - **`TestPolicyBodyMayBuildHas`** (Review Focus 1). It uses an entity in `desc_model_test.go` style whose `Policy()` body does `parentDone := dXs.Parent.HasWhere(dXs.Done.EQ(true))` outside its `Read` func, and returns `Read` using it. Run a `Query` inside a goroutine with a 5-second timeout. It must return within the timeout, and its SQL must hold the EXISTS.
  - **`TestHasWhereErrorAtStatement`.** A `HasWhere` on a relation whose target metadata is invalid fails at `All`/`SQL` with the relation error, and nothing is sent.
  - **`TestDBOfIsTheTransaction`** (Review Focus 2). On SQLite (in-memory, `velox.Open`), an entity's hook uses `DBOf(ctx)` to insert an audit row. Inside `db.Tx(ctx, func(tx *velox.DB) error { insert; return errors.New("rollback") })`, both rows are gone after the rollback. Outside a transaction, both rows remain. Outside a hook, `DBOf(context.Background())` returns false.
  - **`TestEdgeToHolderMismatch`.** A `Desc` whose `EdgeTo[A]` points at a `Many[B]` field is refused, with the message above.
  - **Existing tests:** the existing `HasWhere` tests (exists_test.go, relation_test.go, through_test.go) pass unchanged. Their SQL is identical.
- [ ] **Step 2: Run them to see them fail.** Run: `go test -run 'PolicyBodyMayBuildHas|HasWhereErrorAtStatement|DBOf|HolderMismatch' -timeout 60s .`. Expected: FAIL. `TestPolicyBodyMayBuildHas` times out, and the others fail as their assertions say.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run every check.** Run `go test ./...`, `go test -race ./...`, the budgets, the escape check and `TestGenericTypesCompileSmall`. Expected: PASS, budgets unchanged.
- [ ] **Step 5: Commit**

```bash
git add <files> && git commit -m "Resolve HasWhere lazily; DBOf for hooks; check EdgeTo's target

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The root tests leave the old path

**Files:**
- Modify: every root `*_test.go` that declares an entity with a `Schema` method or calls `MetaOnce.Get`. That is `hooks_internal_test.go`, `insert_scope_internal_test.go`, `insert_test.go`, `insert_scope_test.go`, `query_test.go`, `policy_test.go`, `relation_test.go`, `sql_test.go`, `hook_test.go` and `meta_test.go` (behaviour parts only), plus `schema_policy_internal_test.go` (behaviour parts only).
- Create: Desc entities as needed. Put external tests' entities in `desc_model_test.go`, or in a new `desc_model2_test.go` if it grows. Put internal tests' entities in a new `desc_internal_model_test.go` (package `velox`): an internal test cannot import `internal/testmodel`, which imports velox.

**Interfaces:**
- Consumes: the `Desc` API (desc.go) and the generated `internal/testmodel`.
- Produces: nothing new. The tests run on the new path.

**How:**
1. **Classify every test** in those files. Write the result into the report as a table: test, kind (behaviour / old-API), and entity before → after.
   - **Behaviour** tests (queries, relations, scoping, policy recursion and cycles, hooks' internals, guards, rendering, upserts) are moved.
     - Use a `internal/testmodel` entity when one has the shape needed; it is external tests only.
     - Otherwise, write a `Desc` entity in the shape of `desc_model_test.go`: a struct, handles, `MetaOnce`, `VeloxMeta`, and a `…Desc()` with switch-based `Addr`/`RelAddr` and an explicit `Tenant`.
     - Policies and hooks become `Policy()`/`Hooks()` methods bound with `BindPolicy`/`BindHooks`.
   - **Old-API** tests exist only to test `Schema`/`Mixin`/`ColumnDef`/`RelDef`/`BeforeX`/`Shape`/struct scanning and their errors. Leave them for Task 5, which deletes them with the API. Mark each in the report.
2. **Keep every assertion of a behaviour test.**
   - The only allowed changes are entity names, handle names, column indexes and positional SQL, and error-stage text: `before insert` becomes `hook`.
   - Any other change is a runtime bug, or a test that depended on old-path-only behaviour. In the second case, keep the assertion and say why in the report. If keeping it is impossible, report BLOCKED.
3. **Relation-convention tests.** Old-path inference (`<Owner>ID` field naming, `References`, `ForeignKey`) has no Desc equivalent: Desc relations are explicit. A test that checks only the inference is old-API. A test that checks loading or EXISTS through such a relation is behaviour; move it to an explicit `EdgeTo`.

- [ ] **Step 1: Write the classification table.**
- [ ] **Step 2: Move the behaviour tests, file by file.** Run that file's tests after each.
- [ ] **Step 3: Run every check.** Run `go test ./...`, `-race`, the budgets and the escape check. Expected: PASS. `grep -l 'Schema(s \*\(velox\.\)\?Schema)' *_test.go` lists only files whose remaining old-path tests are old-API, and every one of those is named in the report.
- [ ] **Step 4: Commit**

```bash
git add <files> && git commit -m "The root tests' entities move to the Desc path

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The examples are generated from schemas

**Files:**
- Replace: `example/{start,relations,tenant,policy,hooks,tx,otel}/model/*` with `example/<x>/schema/*.go`, plus generated `example/<x>/gen/*`, plus hand-written `example/<x>/gen/*.go` for `Policy()`/`Hooks()` and helpers. Do the same for `example/tour/billing`, which becomes `example/tour/schema` + `example/tour/gen`.
- Modify: each example's `main.go`, `README.md`, and `testdata/output.golden` (regenerated with `go test ./<x> -update`, then read); `example/README.md` ("How the examples are built", "Regenerate"); `example/go.mod` if needed.
- Modify: `scripts/ci-local.sh`, `.github/workflows/ci.yml` and the scratchpad `fix1.sh`. Their example blocks run `go tool velox-gen -check -out <x>/gen ./<x>/schema` and `verify` for each example, in place of `-check ./...` and `verify ./...`. Keep the old lines only while the old mode exists, then remove them in Task 5.

**How:**
1. **Transcribe each `model/` to a schema.** Tables, column names and relations must stay the same, so that each example's DDL, SQL and golden output change only where the example's teaching changes.
   - Use `StorageKey`/`schema.Table` where the default differs.
   - Column order may change. The ID comes first.
   - Golden output that prints entities may change with the generated `String()` and JSON tags. That is acceptable: read and confirm each change.
2. **Hooks and policies become methods in `gen/`** (Ruling 6).
   - The hooks example teaches the typed `Hooks()`:
     - an insert hook deriving `Slug` from `Title`;
     - an update hook reading `m.Value(gen.Tasks.Title)` and `m.Set(gen.Tasks.Slug.Set(…))`;
     - a delete hook narrowing with `m.Where`;
     - `On`;
     - an audit row written with `m.DB()`, which rolls back with the write;
     - a reusable generic hook function, which replaces the `Audit` mixin's `BeforeInsert`;
     - `IsDryRun`.

     Its README and comments explain each.
   - The policy example's `Policy()` methods move to `gen/`.
3. **`main.go` imports `…/example/<x>/gen`** instead of `…/model`.
4. **Regenerate** with the module's tool, from `example/`:

   ```bash
   go tool velox-gen -out ./<x>/gen ./<x>/schema
   ```

5. **`example/README.md`.**
   - "How the examples are built" says: entities are declared in `schema/` with the ent-style DSL; `gen/` is written by velox-gen, except the files you write with `Policy()`/`Hooks()`, which velox-gen binds.
   - "Regenerate" gives the per-example command.
   - The table's hooks row describes the new hooks.

- [ ] **Step 1: Convert one example (`start`) end to end.** Run `go -C example test ./start`, then `go tool velox-gen -check -out ./start/gen ./start/schema`.
- [ ] **Step 2: Convert the rest, one at a time,** running each example's test after it.
- [ ] **Step 3: Run every check:** `(cd example && go vet ./... && go test -race ./... && golangci-lint)`, then each example's `-check` and `verify`, then the full gate (`fix1.sh`, updated). Expected: PASS.
- [ ] **Step 4: Commit**

```bash
git add example scripts/ci-local.sh .github/workflows/ci.yml && git commit -m "The examples are generated from schemas

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: bench/scale measures one generated package

**Files:**
- Modify: `bench/scale/main.go` (`generate`, `run`, the report and its JSON fields), `bench/scale/thresholds.json`, `bench/scale/scale_test.go`
- Modify: `docs/history/specs/2026-10-14-core-design.md` §9: a dated note that the scale figures now measure one generated package, with the new numbers. Do not rewrite history.

**How (Ruling 5):**
1. **`generate(n, services)` writes:**
   - `schema/e<i>.go` for `i` in `[0, n)`, each a schema with the fields today's entities have: `name` (string), `amount` (float64), `created_at` (time), `note` (Optional string). Edges:
     - every 10th schema `e<10k>` (k > 0) has `edge.From("up", E<10*((k-1)/2)>.Type).Ref("downs").Unique().Field("up_id")`, with the matching `To("downs", …)` on the target;
     - every other schema has an edge to the previous one in its group of ten.

     This keeps today's relation shape. Adjust it so that Build accepts it, and write the rule in a comment.
   - `s<j>/main.go` for each service, importing `example.com/tmp/gen` and running a `db.Query[gen.E<k>]().Where(gen.E<k>s.ID.EQ(1)).Limit(1).SQL(ctx)` for five entities chosen as today.
2. **`run`:**
   1. Build velox-gen from the repo, as today.
   2. Run `velox-gen -out gen ./schema`.
   3. Measure the cold build of every service.
   4. **`schema_edit_rebuild_s`:** add a field to `schema/e<n-1>.go`, run velox-gen again, rebuild every service, and time the regenerate and the rebuild together.
   5. Measure the service binary size and the link peak, as today.
3. **The report and `thresholds.json` keys** become `cold_build_s`, `schema_edit_rebuild_s`, `service_binary_mb` and `link_peak_mb`. `check` errors on an unknown or missing key, so the file and the code agree.
4. **Re-baseline (Review Focus 5).**
   - Run `go run ./bench/scale -n 500 -s 100` three times with `-json` while the machine is idle: load average under 4, from `sysctl -n vm.loadavg` before each run. If it stays above 4 for 30 minutes, record the load and continue.
   - Set each threshold by the file's rule: times are 4× the largest of the three runs, sizes and memory 2×.
   - Rewrite `_measured` with the date, the machine, the three runs, the load averages, and the rule.
5. **`scale_test.go`** keeps its small harness test. Update it to the new generator and keys.

- [ ] **Step 1: Rewrite the generator and harness; run `go test ./bench/scale`.**
- [ ] **Step 2: Measure three idle runs, recording the load averages.**
- [ ] **Step 3: Write the thresholds and the spec note; run `go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json`.** Expected: exit 0.
- [ ] **Step 4: Commit**

```bash
git add bench/scale docs/history/specs/2026-10-14-core-design.md && git commit -m "bench/scale measures one generated package

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The old path is removed

**Files:**
- Modify or delete in the root:
  - `schema.go`: delete it, along with `Schema`, `Schemer`, `Mixer`, `ColumnDef`, `RelDef`, `BeforeInsert`/`BeforeUpdate`/`BeforeDelete` and `policyOf` (keep `BindPolicy`'s conversion where it lives in hook.go).
  - `meta.go`: remove `Shape`, `MetaOnce.Get`, `buildEntity`, `buildInfo`, `flatten`, `applyMixins`, `checkShape`, `firstDiff`, `isRelation`'s old use, `descMethods`, the old `beforeInsert`/`beforeUpdate`/`beforeDelete` fields and their runs in insert.go and db.go (`entityHooks`, `mutationHooks`), and the old `CheckEntity` branches.
  - `relation.go`: remove `RelationTo`, the inference in `build` (`ref`, `hasFK`, `fkOff`, `fkTyp`, `fieldColumn`, `typeName`, `columnAt`), and the `relation` fields only the old path set.
  - `doc.go`: remove `GeneratedCodeIsVersion2`.
  - `desc.go`: its header comment says it is the only metadata path.
  - Old-API tests: delete every test Task 2 marked.
  - `compilecost_test.go`: remove the old rows (`entity`, `entityUse`, `entityPolicySrc`, `Shape`, `ColumnDef`, `RelDef`, `Schema` and any other old-path type rows) and `policyGrowthMax`'s old-path measure. Keep the Desc rows, plus one growth bound for Policy and Hooks on the Desc path (`entityDescHooks` over `entityDescUse`), using the file's margin rule.
- Delete in `cmd/velox-gen`: the old mode.
  - `discover.go`, `classify.go`, `render.go`, `model.go`, the old parts of `write.go`, `load.go` and `names.go`, and `verify.go`'s old program;
  - their tests (`discover_test.go`, `golden_test.go`, `generated_test.go`, the old `cli_test.go`/`verify_test.go` cases);
  - `cmd/velox-gen/testdata/testmodel`.
  - `go.mod`: drop `golang.org/x/tools` if nothing uses it any more (`go mod tidy`).
- Modify:
  - `cmd/velox-gen/main.go`: one mode (Ruling 7). Mode selection, `ErrNotPackage`'s fallback and `declaresOldSchema` go. Update the doc comment.
  - `internal/gen/load.go`: `ErrNotPackage` stays only if something still needs it.
  - `scripts/ci-local.sh`, `.github/workflows/ci.yml` and `fix1.sh`: remove `bin/velox-gen -check ./...` and `verify ./...`.
- **`UpdateDefault` (Ruling 4).** In insert.go's `fillDefault`/`fillDefaults`, an insert no longer applies `UpdateDefault`. Update the tests that assert insert-time `UpdateDefault`. Each such test was asserting the old `OnUpdate` rule; say so in the report.
- Create: `TestOldAPIIsGone` (Review Focus 4). It runs `go doc -all .` from the repo root (it may build the doc in a test helper) and asserts that none of the old names appear as exported identifiers: `type Schema `, `Schemer`, `Mixer`, `ColumnDef`, `RelDef`, `Shape`, `RelationTo`, `func (o *MetaOnce[E]) Get`, `GeneratedCodeIsVersion2`. It skips under `-short`.

- [ ] **Step 1: Remove the runtime's old path and its tests.** Run `go build ./... && go test ./...` after each file, and fix what the removal reveals. A behaviour test that still needs the old path is a gap in Task 2: move it now and list it.
- [ ] **Step 2: Remove velox-gen's old mode and its tests.** Run `go -C cmd/velox-gen test ./...`.
- [ ] **Step 3: Make `UpdateDefault` update-only, and update its tests.**
- [ ] **Step 4: CI and the compile-cost rows.**
- [ ] **Step 5: Run the full gate.** Run `fix1.sh` (updated), the budgets, the escape check, `go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json` (idle), and `TestOldAPIIsGone`. Expected: PASS.
- [ ] **Step 6: Commit** (one or a few commits)

```bash
git commit -m "Remove the struct-scanning path and velox-gen's old mode

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Documents

**Files:**
- Modify:
  - `docs/history/specs/2026-10-14-core-design.md`: §1 and §2 open with a note, "Superseded by `2026-10-15-schema-design.md` (entities are declared in the schema DSL and generated)", and keep their text as history.
  - `docs/history/specs/2026-10-15-schema-design.md`: §3 documents `Mutation.DB`, the lazy `HasWhere` (the deadlock note becomes "building predicates in the method body is fine"), and `UpdateDefault` update-only. §6 says "replaced" in the past tense, and Status becomes "implemented (schema plans 1–4)".
  - `docs/history/specs/2026-10-14-policy-design.md`: §2.2/§3's `s.Policy`/`BeforeX` forms carry a note pointing to the schema design §3.
  - `docs/history/plans/2026-10-14-core-1-followups.md`:
    - remove each item this plan closed (the old path, `UpdateDefault`, the deadlock, tx access in hooks, the `EdgeTo` check, plan 3's mode-selection notes);
    - fix the stale "plan 3" lines;
    - keep the open items: `InsertAll` reads no keys back, join rows are not tenant-scoped, the ent differences (`Optional` pointers, int64 IDs, irregular plurals, index names), `velox.yaml`, and plugins arriving with GraphQL.
  - `docs/history/plans/2026-10-16-schema-3-generator.md`: Ruling 1's wording, the d0/d1 example, and the old-mode exception. Mark them as superseded by plan 4.
  - Package doc comments: `doc.go` (root) and `schema/doc.go` describe the one path, with a short example of `go tool velox-gen ./schema` and a hand-written `Policy()`.

- [ ] **Step 1: Edit the documents.** Grep for stale references: `Schema(\*velox.Schema)`, `BeforeInsert`, `MetaOnce.Get`, `old mode`, `plan 3` and `struct scanner` in `docs/` and in Go comments. Fix each one that describes the present. Leave the ones that are history.
- [ ] **Step 2: Run `go vet ./... && go test -short ./...`** (doc comments compile in examples, if any).
- [ ] **Step 3: Commit**

```bash
git add docs doc.go schema/doc.go && git commit -m "Documents: one path, the schema DSL

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
