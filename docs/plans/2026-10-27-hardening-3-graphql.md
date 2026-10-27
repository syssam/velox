# Pre-1.0 Hardening, Wave 3: GraphQL — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0 freezes them, contrib/graphql's generated mutations, its SDL and veloxgql get their final behaviour:
- gogen/mutation.go is factored, with the goldens unchanged, and the generated godoc points to one "Mutations" section of contrib/graphql's doc, which states the contracts (G49, G51, G44, G52);
- each mutation runs in a savepoint inside the operation's transaction, so a resolver that swallows its error drops only its own writes (G45);
- an O2O re-point refuses a hidden current child, and a delete removes the join rows of one-sided many-to-many edges (G47, G48), with `GeneratedCodeIsVersion3`;
- a Required edge with a NULL-able key (a Required self edge's) has an SDL that says what the database can do: `parent` is nullable, and the update inputs that could only fail are gone;
- bidirectional self edges are proven end to end through create and update;
- the node key has where predicates, application enums bind with `@goModel`, `Cost` reads `@skip` and `@include`, and `ErrorPresenter` takes options (G1, G11–G13, G24, G37);
- the small defects G6, G9 and G50, and `velox.IsNotFound`, close;
- the core names savepoints uniquely per transaction, so a generated mutation's savepoint cannot replace an application's on MySQL (check finding 12).

**Architecture:** Fourteen tasks, each building and passing the gate at its commit.
- Task 1 is G49: a refactor with no golden change. It comes first (spec, wave split), so that every later diff of mutation.go is small.
- Tasks 2–3 change generated text only: Task 2 the godoc (G51 with G44 and G52's contracts), Task 3 the not-found check and the owner-key check (`velox.IsNotFound`, G50).
- Task 4 is a core fix that Task 5 makes reachable: savepoint names come from a per-transaction counter, not the depth.
- Tasks 5–7 change mutation semantics: the savepoint (G45), the hidden child (G47, with the one version bump), and one-sided join rows (G48).
- Tasks 8–9 are Wave 2's leftovers: the Required self edge (SDL and inputs), then the bidirectional e2e tests.
- Tasks 10–14 are the SDL and veloxgql features and small defects: G1, G11–G13, G6 with G9, G24, G37.

Everything is in contrib/graphql and example/graphql, except Task 3's `velox.IsNotFound` and Task 4's savepoint names, in the root package.

**Tech Stack:** Go 1.27. gqlgen v0.17.86 (example/graphql pins it; `pinnedGqlgen` in example/graphql/bench_test.go:62). golangci-lint v2.14.0 and revive v1.17.0, as `scripts/ci-local.sh` pins them. Docker for PostgreSQL 18 and MySQL 8.4.

**Spec:** `docs/specs/2026-10-25-pre-1.0-hardening-design.md`. Its final block, **"Rulings on the builder check"**, is binding and overrides the rulings above it where they differ.
- "Spec ruling N" is item N of the spec's Rulings, and "builder ruling N" is item N of that final block.
- The evidence for each id is in `docs/reviews/2026-10-25-triage-G.md`.
- Wave 2's leftovers are in `docs/plans/2026-10-26-hardening-2-core-runtime.md`, "Left by this plan", and its execution rulings 9 and 10.
- Every file:line below was taken at 23d0dee. Abbreviation: **CG** is `contrib/graphql`.
- The builder check of this plan (at 110a775) is applied: its findings are cited as "check finding N", and the controller's rulings on it are under "Controller rulings".

## Global Constraints

- **Shell.** Run `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2` before any go command.
  - The machine is memory-constrained and shared. Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test. Do not kill other processes.
  - The gate is `bash scripts/ci-local.sh` with Docker. Only one runs at a time. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
  - `bench/scale` can fail on time alone when the machine is loaded. If the gate fails only on a bench/scale time threshold, run the gate once more with nothing else running. Never raise a threshold.
- **Commit, then check.** Every task commits first, then runs G (where it says) and the gate on the committed tree.
  - On a failure, the task fixes the cause and amends its own commit (`git commit --amend --no-edit`, staging by name), then runs both again.
  - The gate passes at every task's commit: rc=0.
- **Commits.**
  - The message is `Area: what changed`. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage files by name with `git add <paths>`, never with `git add -A` or `.`, and never stage `.DS_Store`.
  - `git status --short` lists what a task changed. Every path in it but `.DS_Store` is staged.
  - Never push.
- **No deprecated aliases.** A replaced name is deleted.
- **Lint and revive stay at 0** in every module, generated code included.
- **Greps.** Apple's git greps with BSD regex, where `-E` never matches `\b`. Every grep here uses `git grep -P`.
- **Test names.** Before adding a test function or a test type, run `git grep -nP 'func <Name>\(|type <name>\b' -- '*.go'`. Expected: nothing. Every name below was checked at 23d0dee. A name added by an earlier task of this plan counts. The e2e tests written as source strings (e2eWhere, e2eMutRun) count too: they are in the same grep.
- **Goldens.** A golden changes only through its `-update` run or a hand edit that a step names. Read every changed golden line: only what the task changes may differ.
- **Historical documents.** Nothing under `docs/reviews/`, `docs/specs/` or `docs/plans/` changes, except this plan.
- **The followups file** (`docs/plans/2026-10-14-core-1-followups.md`) is not touched. Wave 6 marks its items, citing these commits.
- **No machine-specific paths** in committed files.
- **Generated-code versions.** veloxgql's `GeneratedCodeIsVersion2` becomes `GeneratedCodeIsVersion3` once, in Task 6 (spec ruling 10, wave split). velox's `GeneratedCodeIsVersion6` does not change: velox-gen's output does not change in this wave.
- **Compile cost.** compilecost_test.go's bounds hold: entityDescUse 757760 (measured 748016), entityDescHooks 1049600 (measured 1037136). Only Tasks 3 and 4 touch the root package; each checks them.

### R: the regeneration steps

Each task runs the steps it names, in this order, from the repo root.

- **RG** `(cd contrib/graphql && go test ./internal/sdl ./internal/gogen -update)`. This rewrites `CG/internal/sdl/testdata/velox.graphqls` (`-update` of render_test.go:18) and `CG/internal/gogen/testdata/golden/*` (`-update` of render_test.go:26). `CG/internal/sdl/testdata/model.json` is written by hand and never by `-update`.
- **R5** `(cd example && go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema)`.
- **RQ** `(cd example && go -C graphql tool gqlgen generate)`, run after R5 whenever the example's SDL changes (`example/graphql/gen/velox.graphqls`). It rewrites `example/graphql/graph/`; `git status --short -- example/graphql/graph` shows what changed, and nothing there is untracked.

No task runs velox-gen's own goldens (Wave 2's R2, R3) or the example output goldens (R7): velox-gen's output and the examples' stdout do not change.

**D: the databases**, for a step that names them. Never run D while a gate runs: both use the compose project `velox2-ci`.

```bash
docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# … the step's command …
docker compose -f docker-compose.ci.yml -p velox2-ci down -v
```

**The generated check (G)** is CI's "generated files are up to date". It runs on the committed tree, since it diffs against HEAD. Expected: every command exits 0, `verify` prints `velox-gen verify: N entities, 0 with errors` per package, and the `git diff` is empty.

```bash
go build -o bin/velox-gen ./cmd/velox-gen
for m in testmodel bindmodel; do bin/velox-gen -check -out internal/$m -package $m ./internal/$m/schema && bin/velox-gen verify -out internal/$m ./internal/$m/schema; done
(cd example && for d in */schema; do x=${d%/schema}; [ "$x" = graphql ] && continue; go tool velox-gen -check -out ./$x/gen ./$x/schema && go tool velox-gen verify -out ./$x/gen ./$x/schema; done \
  && go tool velox-gen -check -out ./graphql/gen -plugin velox-graphql ./graphql/schema \
  && go tool velox-gen verify -out ./graphql/gen -plugin velox-graphql ./graphql/schema \
  && go -C graphql tool gqlgen generate && git diff --exit-code HEAD -- graphql/graph \
  && test -z "$(git ls-files --others --exclude-standard -- graphql/graph)")
```

**The CG tests (T)**, for a step that names them: `(cd contrib/graphql && go test -count=1 ./...)`. The e2e tests in `cmd/velox-graphql` build a module each and take minutes. A step that names "T under D" runs it inside D, so TestMutationsRun runs on sqlite, pgx, postgres and mysql.

## Review Focus

1. **A resolver that swallows a mutation's error, then succeeds.** The swallowed mutation's writes must be gone and the operation's other writes kept. Before G45 they were committed. Pin: Task 5 `TestAtomicSavepoint`, its swallowing case.
2. **An O2O re-point whose current child the caller cannot see.** It must fail with BAD_USER_INPUT "already linked", leave the hidden child linked, and leave the new child unlinked. It must not fail with the database's unique-constraint error, which names the child's table. Pin: Task 6 `TestRepointHiddenChild`.
3. **Deleting the target of a one-sided many-to-many edge under foreign keys.** The join rows must go first, or the database refuses the DELETE. Pin: Task 7 `TestDeleteOneSidedM2M` under eachFK.
4. **A Required self edge whose parent was deleted (ON DELETE SET NULL).** Querying the child's `parent` must return `null` and no error. A non-null `parent: Folder!` would null the whole child, and its parents up to the nearest nullable field. Pin: Task 8 `TestRequiredSelfEdge`.
5. **A variable that turns `@include` off.** The cost must drop what the variable excludes. A missing variable must count the field, because its cost cannot be known. Pin: Task 13 `TestCostSkipInclude`, its variable and missing-variable cases.
6. **Re-pointing a bidirectional O2O partner onto a taken one through GraphQL.** It must fail with a ConstraintUnique, and the three rows must be as they were. Pin: Task 9 `TestBidiPartner`, its taken case.
7. **A savepoint whose ROLLBACK TO fails, after a resolver swallowed the mutation's error.** The transaction is broken, so the operation must not commit: the response carries an error and nothing is kept. Pin: Task 5 `TestAtomicSavepoint`, its failing-rollback case.
8. **An application that runs a generated mutation inside its own `tx.Tx` on the operation's DB, on MySQL.** The mutation's savepoint must not replace the application's, whose later ROLLBACK TO must still work. Pin: Task 4 `TestSavepointNamesUnique`.

---

### Task 1: gogen/mutation.go factored, goldens unchanged (G49)

**Files:**
- Modify: `CG/internal/gogen/mutation.go` (the sites below).
- Test: none new. The goldens (`CG/internal/gogen/testdata/golden/*`) and example/graphql/gen pin the output.

**Interfaces:**
- Consumes: nothing.
- Produces, in mutation.go, unexported, for Tasks 3, 6, 7 and 8:

```go
// joinCols is the join entity of the M2M edge ed of e, and its two fields:
// own holds e's key, and other the target's. An inverse edge's owner is the
// join entity's second field. Its error names e and ed.
func (r *renderer) joinCols(e *plugin.Entity, ed *plugin.Edge) (j *plugin.Entity, own, other *plugin.Field, err error)

// fkOf is the foreign-key field ed.Field of on, the entity that holds it
// (e for an edge that owns its key, else the target). Its error names e, ed
// and on.
func fkOf(e *plugin.Entity, ed *plugin.Edge, on *plugin.Entity) (*plugin.Field, error)

// writeRepointed writes the link of a target-held key: the UPDATE that sets
// fkh to v on the rows ids names whose key is NULL, then gqlimpl.Repointed
// on a count of cnt expected rows. With countAfter false (writeCreate) the
// count is the UPDATE's own row count (`if n, err := db.Update…Exec(ctx)`,
// mutation.go:570-572). With countAfter true (writeUpdate) it is a separate
// Count of the rows of ids that hold v, after the UPDATE (mutation.go:1003-1006),
// because a re-link to the same owner changes no row on MySQL without
// clientFoundRows. Each mode writes exactly what its site writes today.
func (r *renderer) writeRepointed(f *file, indent string, tgt *plugin.Entity, fkh, v, ids, cnt string, countAfter bool, fi sdl.InputField, at func(sdl.InputField) string, owner string)

// checkExists returns the gqlimpl.CheckExists call on the rows of tgt whose
// key pk is in keys, n of them expected.
func checkExists(impl, tgt, pk, keys, n, at, typ string) string
```

- [ ] **Step 1: Factor.** Replace the repeated code with the four helpers:
  - the "is no field" errors at mutation.go:483, :543, :829 and :978 become `fkOf`;
  - the "of two fields" errors and own/other splits at :523, :917 and :1095 become `joinCols`;
  - the Repointed emissions at :572 (writeCreate) and :1006 (writeUpdate's `link` closure) become `writeRepointed`, with countAfter false at :572 and true at :1006 (check finding 8), so that create's goldens do not change;
  - the CheckExists emissions at :490, :495, :853 and :959 become `checkExists`.

  An error's text stays exactly as it is now.
- [ ] **Step 2: Check the goldens are unchanged.** Run `(cd contrib/graphql && go test -count=1 ./internal/gogen ./internal/sdl)`. Expected: PASS with no `-update`. `git status --short` lists only mutation.go.
- [ ] **Step 3: Check the size.** `git diff --stat` shows mutation.go with fewer lines than its 1225. Then `git grep -cP 'is no field of' -- contrib/graphql/internal/gogen/mutation.go` gives 1, and `git grep -cP 'of two fields' -- contrib/graphql/internal/gogen/mutation.go` gives 1.
- [ ] **Step 4: Commit.** Message: `velox-graphql: factor the mutation writer (G49)`. Then run the gate.

---

### Task 2: Short generated godoc, and a "Mutations" section with the contracts (G51, G44, G52)

**Files:**
- Modify: `CG/doc.go:160-241`. The mutation paragraphs move under a new heading `// # Mutations`, placed before :160. It holds the semantics the generated godoc stops repeating, and the contracts below.
- Modify: `CG/internal/gogen/mutation.go:390` (CreateX), `:737` (UpdateX) and `:1129-1134` (DeleteX), to the exact copy in Step 3.
- Test: `CG/internal/gogen/render_test.go`, new `TestMutationGodocPoints`.
- Regenerate: RG, R5.

**Interfaces:**
- Consumes: Task 1's helpers.
- Produces: the section name `Mutations`, which the generated godoc names. Later tasks edit its text (Tasks 5, 6, 7, 8, 9).

- [ ] **Step 1: Write the failing test.** `TestMutationGodocPoints` renders testModel and mutationsModel. For every `func Create*`, `func Update*` and `func Delete*` in the output, it reads the doc comment and normalises it first, `doc = strings.Join(strings.Fields(doc), " ")`: f.doc wraps at 77 columns (gogen/render.go:138-154), and the 89-character pointer always spans comment lines (check finding 1). Then it checks:

```go
const pointer = `See "Mutations" in the documentation of package github.com/syssam/velox2/contrib/graphql`
if !strings.Contains(doc, pointer) { t.Errorf("%s: no pointer in %q", fn, doc) }
if n := len(strings.Fields(doc)); n > 80 { t.Errorf("%s: %d words, want at most 80", fn, n) }
for _, gone := range []string{"savepoint", "written one way", "FOR UPDATE", "clientFoundRows"} {
	if strings.Contains(doc, gone) { t.Errorf("%s: %q is the package doc's", fn, gone) }
}
```

- [ ] **Step 2: Run it.** `(cd contrib/graphql && go test -count=1 -run TestMutationGodocPoints ./internal/gogen)`. Expected: FAIL. Today's docs are 169 (CreateX), 425 (UpdateX) and 207 (DeletePost) words, counted on the goldens.
- [ ] **Step 3: Write the godoc.** The exact copy, with `<Fn>`, `<T>` and `<mutation>` substituted. The pointer sentence is the same in all three:
  - CreateX: `<Fn> creates a <T> from input, with its edges, as the mutation <mutation> does, and returns the row as written. It runs atomically on the DB veloxgql.DBFromContext reads from ctx, under the tenant scope and the policies. See "Mutations" in the documentation of package github.com/syssam/velox2/contrib/graphql for what each input field does, what the caller may link and change, and the errors.`
  - UpdateX: `<Fn> updates the <T> with the ID id from input, as the mutation <mutation> does, and returns the row as written. It runs atomically …` (the rest as CreateX's).
  - DeleteX: `<Fn> deletes the <T> with the ID id, and the join rows that hold its key, as the mutation <mutation> does, and returns id encoded again from the key. It runs atomically …` (the rest as CreateX's). The `joinRows` variant (mutation.go:1126-1128) goes. The sentence names the join rows for every entity, since Task 7 makes other entities' edges count.
- [ ] **Step 4: Write CG/doc.go's "# Mutations" section.** Move :160-241 under the heading, and keep their text, with these changes:
  - **G44, spec ruling 20.** One paragraph states the contract: "A mutation returns the row as written. Create<T> returns the row it inserted. Update<T> reads it back under the tenant scope without the Read policy: its UPDATE is scoped by the Read policy too, so the caller could read the row before it wrote it. A mutation that takes the row out of what the caller may read still returns it. This is a contract, and will not change." Remove the bullet at :219-220, which this replaces.
  - **G52, spec ruling 21.** One paragraph states what a link needs: "A link to a target needs what the write changes. A many-to-many link inserts a join row and changes no target row, so the target must be one the caller may read: the tenant scope and the target's Read policy. A one-to-many or one-to-one link from the owner sets the target's foreign key, so the target must be one the caller may change: the tenant scope and its Read and Write policies. Unlinking follows the same rule."
  - **The stale COUNT.** At :179, "go only after a COUNT under the DELETE's own scope" becomes "go only after an Exists under the DELETE's own scope (Writable, locking the row with FOR UPDATE wherever the dialect can)". lockedCheck (mutation.go:1215) sends an Exists, not a COUNT.
  - The semantics the old UpdateX godoc carried, and the package doc lacks, move in once: an input that sets no field sends no UPDATE and checks its row with a locking Exists; and the MySQL `clientFoundRows=true` note, which is at :217-218 already. Nothing is said twice.
- [ ] **Step 5: Regenerate and run.** RG, R5, then `(cd contrib/graphql && go test -count=1 ./internal/gogen ./internal/sdl)`. Expected: PASS. Read the golden diff: only doc comments of CreateX, UpdateX and DeleteX differ.
- [ ] **Step 6: Commit.** Message: `velox-graphql: one Mutations section, short generated godoc (G51, G44, G52)`. Run G, then the gate.

---

### Task 3: `velox.IsNotFound`; the owner-key check is `EQ` (Wave 2 leftover 1, G50)

**Files:**
- Modify: `errors.go` (root), after `ErrNotFound`'s block. Test: `errors_test.go`, new `TestIsNotFound`.
- Modify: `CG/internal/gogen/mutation.go:1043-1044` (the read-back in UpdateX) and `CG/internal/gogen/render.go:688` (Node's lookup), to `velox.IsNotFound(err)`.
- Modify: `CG/internal/gogen/mutation.go`, the CheckExists of a single owner key (:490 and :853 before Task 1; now Task 1's `checkExists` call sites for one key), to `EQ(k)`.
- Modify: `CG/veloxgql/gqlimpl/edge.go:102`, `shared.Is(err, velox.ErrNotFound)` to `velox.IsNotFound(err)`. This is for one spelling only: `shared.Is` is already nil-safe (CG/veloxgql/internal/shared/errwalk.go:43), so it changes no behaviour.
- Test: `CG/internal/gogen/render_test.go`, new `TestSingleOwnerKeyEQ`.
- Regenerate: RG, R5.

**Interfaces:**
- Consumes: Task 1's `checkExists`.
- Produces:

```go
// errors.go
// IsNotFound reports whether err is, or wraps, ErrNotFound. It walks err's
// tree as errors.Is does, but skips a nil error in it: a typed-nil error that a
// driver wraps does not panic. ent's IsNotFound is the precedent.
func IsNotFound(err error) bool
```

- [ ] **Step 1: Write the failing tests.**
  - `TestIsNotFound` checks these cases:
    - `IsNotFound(nil)` is false;
    - `ErrNotFound` is true;
    - `fmt.Errorf("x: %w", ErrNotFound)` is true;
    - `errors.Join(errors.New("a"), ErrNotFound)` is true;
    - `errors.Join((*net.OpError)(nil), ErrNotFound)` does not panic and is true, and `errors.Join(errors.New("x"), (*net.OpError)(nil))` is false: the typed nils of errclass_test.go:147-148 (C27).
  - `TestSingleOwnerKeyEQ` renders testModel and checks that no output file matches `\.In\(k\d+\)\), 1,`, and that gql_post.go contains `Users.ID.EQ(k7)), 1, "CreatePostInput.writerID"`.
- [ ] **Step 2: Run them.** `go test -count=1 -run TestIsNotFound .` then `(cd contrib/graphql && go test -count=1 -run TestSingleOwnerKeyEQ ./internal/gogen)`. Expected: FAIL, the first because IsNotFound is undefined.
- [ ] **Step 3: Implement.**
  - `IsNotFound` is `return is(err, ErrNotFound)`, using errclass.go:96's nil-skipping walk.
  - In gogen, write `%s.IsNotFound(err)` with `f.velox()`. Drop the `f.use("errors", "errors")` at those two sites, so that a file that no longer needs `errors` does not import it.
  - The owner-key CheckExists writes `.EQ(%s)` when it checks one key, and keeps `.In(%s...)` for lists.
- [ ] **Step 4: Regenerate and run.**
  - Run RG and R5.
  - Run `go test -count=1 -run 'TestIsNotFound|TestGenericTypesCompileSmall' -v .`. Expected: PASS, and the logged sizes are within 757760 (entityDescUse) and 1049600 (entityDescHooks).
  - Run `(cd contrib/graphql && go test -count=1 ./internal/gogen ./internal/sdl ./veloxgql/...)`. Expected: PASS.
  - Read the golden diff. It holds the 12 not-found lines listed under "Leftover greps", the 9 CheckExists lines, and import blocks that lost `"errors"`. Nothing else differs.
- [ ] **Step 5: Commit.** Message: `velox: IsNotFound; velox-graphql: nil-safe not-found, EQ on one owner key (G50)`. Run G, then the gate.

---

### Task 4: Savepoint names are unique per transaction (check finding 12)

**Why.** tx.go:370 names a savepoint after its depth: `"velox_" + strconv.Itoa(db.depth+1)`. Two savepoints made from DBs of the same depth, one inside the other, get the same name. That happens when an application runs a generated mutation inside its own `tx.Tx` on the operation's DB:
- `veloxgql.DBFromContext` returns the operation's depth-0 DB, so after Task 5, Atomic's `db.Tx` names its savepoint `velox_1` again;
- `foreign` lets the statement through, because the two share a root (tx.go:530).

PostgreSQL and SQLite stack savepoints of one name, so a RELEASE or ROLLBACK TO reaches the newest. MySQL does not: "If the current transaction has a savepoint with the same name, the old savepoint is deleted and a new one is set" (MySQL 8.4 manual, SAVEPOINT). So the application's later `ROLLBACK TO SAVEPOINT velox_1` fails, and that breaks the transaction (Wave 2 execution ruling 3). Before Task 5 nothing generated made a savepoint inside the operation's transaction, so Task 5 makes this reachable. The fix is in the core, and it lands first.

**Files:**
- Modify: `tx.go`: txFrame (:510-515) gains a counter, and savepoint (:370) names from it.
- Modify: `tx_test.go:169-170`, TestNestedTxUsesSavepoints: its second, sequential savepoint becomes `velox_2`.
- Test: `tx_test.go`, new `TestSavepointNamesUnique`.

**Interfaces:**
- Consumes: nothing.
- Produces: nothing exported. A transaction's savepoints are named `velox_1`, `velox_2`, … in the order they are made, never reused within the transaction.

- [ ] **Step 1: Write the failing test.** `TestSavepointNamesUnique` uses `velox.OpenFake(velox.MySQL, e)`:

  ```go
  err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
  	return tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
  		// What Atomic does: a savepoint from the depth-0 DB, inside sp's.
  		if err := tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err != nil {
  			return err
  		}
  		return boom
  	})
  })
  ```

  `e.Log` is `["BEGIN", "SAVEPOINT velox_1", "SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_2", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "ROLLBACK"]`, and err is boom. Today the second line pair is `velox_1`.
- [ ] **Step 2: Run it.** `go test -count=1 -run 'TestSavepointNamesUnique|TestNestedTx' .`. Expected: TestSavepointNamesUnique FAILS, and TestNestedTxUsesSavepoints passes.
- [ ] **Step 3: Implement.**
  - txFrame gains `savepoints atomic.Uint64`. It is atomic like `ended`, so concurrent misuse is a race on the connection, not on this field.
  - savepoint names `"velox_" + strconv.FormatUint(db.txRoot.savepoints.Add(1), 10)`.
  - Update TestNestedTxUsesSavepoints's want to `"SAVEPOINT velox_2", "ROLLBACK TO SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_2"` for its second savepoint. TestNestedTxDepth's want is unchanged (velox_1, velox_2).
  - savepoint's doc says the names are unique within the transaction, because MySQL replaces a savepoint of the same name.
- [ ] **Step 4: Run.**
  - `go test -count=1 -run 'Savepoint|NestedTx|Tx' .`. Expected: PASS. The other pinned logs (insert_test.go:261, :277; shard_test.go:485; tx_test.go:95, :135, :145, :665; txhook_test.go:53) make one savepoint per transaction, so they keep `velox_1`.
  - `go test -count=1 -run 'TestGenericTypesCompileSmall' -v .`. Expected: within the bounds.
- [ ] **Step 5: Commit.** Message: `velox: savepoint names unique per transaction`. Run the gate.

---

### Task 5: A savepoint per mutation (G45; spec ruling 18, builder ruling 9)

**Files:**
- Modify: `CG/veloxgql/gqlimpl/mutation.go:14-33`, `Atomic`, and its doc.
- Delete: `CG/veloxgql/internal/shared/tx.go`.
- Modify: `CG/veloxgql/extension.go:296-298`. The comment line "It marks tx as the operation's, so gqlimpl.Atomic runs in it." (:297) goes, and :298 becomes `resp = next(withState(ctx, tx, e.limits))`.
- Modify: `CG/veloxgql/extension_test.go:423-457`. `TestExtensionMarksItsTx` becomes `TestExtensionGivesMutationsItsTx`: with the mark gone, it checks the DB that DBFromContext gives.
- Modify: `CG/veloxgql/gqlimpl/mutation_test.go:199-260`, `TestAtomic` (its doc and its "in the extension's transaction" case), and new `TestAtomicSavepoint`.
- Modify: `CG/veloxgql/gqlimpl/doc.go:6-8`; `CG/doc.go:188-193` (in the Mutations section after Task 2).
- Test: `example/graphql/bench_test.go`, new `TestMutationBudget`.

**Interfaces:**
- Consumes: Task 4's unique savepoint names.
- Produces: `func Atomic(ctx context.Context, fn func(ctx context.Context, db *velox.DB) error) error`, with an unchanged signature. It is now always `db.Tx(ctx, fn)`.

- [ ] **Step 1: Write the failing tests.**
  - **`TestAtomicSavepoint`** (gqlimpl). Put a `savepointConn` driver wrapper in mutation_test.go: the countless pattern of CG/cmd/velox-graphql/mutation_e2e_test.go:194-226, recording each ExecContext text that starts with `SAVEPOINT`, `RELEASE` or `ROLLBACK TO`, and failing `ROLLBACK TO` with a driver error when its `failRollback` field is set. mutationDB gains a variant that opens through it. Cases:
    - **In the extension's transaction, fn succeeds:** the recorder holds `SAVEPOINT velox_1` then `RELEASE SAVEPOINT velox_1`; txRecord's transactions are `["commit"]`; fn's DB is not the operation's DB.
    - **Swallowing:** the resolver calls Atomic with insertThenFail, ignores its error, then inserts person 2 and returns nil. The response has no errors. countPeople is 1, and person 1 is absent. The recorder holds exactly `[SAVEPOINT velox_1, ROLLBACK TO SAVEPOINT velox_1, RELEASE SAVEPOINT velox_1]`: rollbackSavepoint releases after it rolls back (tx.go:431-437; check finding 2).
    - **Swallowing, and ROLLBACK TO fails** (check finding 13): as the swallowing case, with failRollback set, and the resolver ignores the second insert's error too and returns nil. The failed rollback breaks the transaction (txFrame.broken), so the later insert fails and the Extension's commit is refused. The response carries an error, txRecord's transactions are `["rollback"]`, and countPeople is 0.
    - **Outside a server:** no savepoint is recorded, and the transaction rolls back (the current TestAtomic case, kept).
  - **`TestAtomic`'s "in the extension's transaction" case** keeps asserting one transaction and one commit. Its `fnDB != opDB` check flips to "fn got a savepoint's DB, not the operation's". Its doc drops "or a savepoint".
  - **`TestExtensionGivesMutationsItsTx`** checks, through DBFromContext:
    - a mutation's DB is not db;
    - a query's DB is db;
    - a mutation that WithTxFilter leaves out gets db;
    - NewContext's DB is db.
  - **`TestMutationBudget`** (example/graphql/bench_test.go). Open the example's DB through `open` on a `*sql.DB` from `sql.OpenDB` of a connector that wraps the sqlite driver and counts SAVEPOINT and RELEASE, with `velox.WithAround` counting statements, as benchHandler does. Seed it with `seed`. Then send through `orgFromHeader(server(db))`, with `X-Org-ID: 1`:

    ```graphql
    mutation { updatePost(id: "<post 1's ID>", input: {title: "Alpha 2"}) { title } }
    ```

    The test asserts:
    - the response is `{"data":{"updatePost":{"title":"Alpha 2"}}}`;
    - statements == `mutationStmts`;
    - savepoint statements == 2;
    - when `pinnedGqlgenBuilt()`, `testing.AllocsPerRun(20, …)` ≤ `mutationAllocsBudget`.

    Every run sends the same mutation, which sends the same statements: an UPDATE to an unchanged value is still sent.
- [ ] **Step 2: Measure before.** With the test written and Atomic unchanged:
  - Set both constants to 0 and run `(cd example && go test -count=1 -run TestMutationBudget -v ./graphql)`.
  - Record the logged statements, savepoints (expected 0) and allocations, as **before**.
  - Expected: FAIL on the savepoint count.
- [ ] **Step 3: Implement.** Atomic is:

  ```go
  db, err := veloxgql.DBFromContext(ctx)
  if err != nil {
  	return err
  }
  return db.Tx(ctx, fn)
  ```

  Its doc says:
  - In the Extension's transaction, `db.Tx` makes a savepoint, so fn's error rolls back fn's writes only. A resolver that swallows the error keeps the operation's other writes, and the operation commits.
  - Outside one, fn runs in a transaction of its own.
  - Without a DB, Atomic fails with ErrNoDB.

  Delete shared/tx.go and the mark at extension.go:298. gqlimpl/doc.go:7 says "in a savepoint inside the operation's transaction, or in a transaction of its own". The Mutations section replaces "There is no savepoint … commits the partial writes." with that rule.
- [ ] **Step 4: Measure after, and set the budget.**
  - Run TestMutationBudget again. Record **after**.
  - Set `mutationStmts` to the measured statement count, which must equal before's: Around does not see SAVEPOINT or RELEASE (tx.go:341).
  - Set `mutationAllocsBudget` to after's allocations × 1.1, rounded up to a multiple of 100.
  - Write both measurements, before and after, in the constants' comment.
  - Expected: PASS.
- [ ] **Step 5: Run.**
  - `(cd contrib/graphql && go test -count=1 ./veloxgql/...)`. Expected: PASS.
  - Then T under D. Expected: PASS. TestMutationsRun's `TestAtomic` is unchanged: outside a server, nothing nests.
  - `git grep -nP 'OperationTx' -- '*.go'` prints nothing.
  - **Nesting in an application's Tx** (check finding 12) is safe after Task 4: a mutation called inside the application's own `tx.Tx` gets a savepoint of a new name. No doc sentence is needed. A RELEASE that fails after fn succeeded returns its error with fn's writes still in the transaction (tx.go:393-395); a resolver that swallows it keeps them. That is core behaviour, left to Wave 6's notes ("Left by this plan").
- [ ] **Step 6: Commit.** Message: `veloxgql: a savepoint per mutation; mutation budget (G45)`. Run G, then the gate.

---

### Task 6: A hidden current child refuses an O2O re-point; `GeneratedCodeIsVersion3` (G47; spec rulings 10, 22; builder ruling 12)

**Files:**
- Modify: `CG/veloxgql/gqlimpl/mutation.go`, new `StillLinked` after `Repointed` (:142). Its message uses `article(typ)` (:228).
- Modify: `CG/veloxgql/gqlimpl/doc.go:6-10`, the list of the mutations' runtime names, which gains StillLinked after Repointed (Task 5 edits the same sentence).
- Modify: `CG/internal/gogen/mutation.go`, UpdateX's `one` branch (:1018-1024 at 23d0dee): emit StillLinked between the scoped unlink and the link.
- Modify (version 2→3): `CG/veloxgql/doc.go:6, :184, :188` (its "replaced by" text names GeneratedCodeIsVersion4), `CG/veloxgql/api_test.go:58, :187-201`, `CG/veloxgql/gqlimpl/doc.go:13`, `CG/doc.go:142`, `CG/internal/gogen/render.go:206`, `CG/internal/gogen/render_test.go:659`. Do not touch root `oldapi_test.go:39`: that is velox's old constant.
- Test: `CG/veloxgql/gqlimpl/mutation_test.go`, new `TestStillLinked`; `CG/cmd/velox-graphql/mutation_e2e_test.go`, new e2e `TestRepointHiddenChild` in e2eMutRun, with a new policy file `e2eMutProfilePolicy`.
- Modify: the Mutations section of CG/doc.go, one sentence.
- Regenerate: RG, R5.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// StillLinked fails when q, run without the tenant scope or the Read policy,
// finds a row: a child that the owner of the GraphQL type owner still holds
// after the caller's scoped unlink, which the caller may not see. For
// generated code. Its error is BAD_USER_INPUT "veloxgql: <at>: the <owner> is
// already linked to a or an <typ>" (article(typ), as Repointed writes), which
// says nothing about the child. The query's own error is returned as it is.
func StillLinked[E any](ctx context.Context, q velox.Query[E], at, owner, typ string) error
```

StillLinked runs `q.Exists` on `velox.WithAllTenants(velox.WithoutPolicy(ctx, reason), reason)`, where reason is `"graphql: a hidden child of a one-to-one re-point"`. Both functions take a reason string (scope_api.go:47, :58).

- [ ] **Step 1: Write the failing tests.**
  - **`TestStillLinked`** (gqlimpl, mutationDB). Use a `posts` row of tenant "b", read under `WithTenant(ctx, "a")`. StillLinked on `Query[post]().Where(id = that row)` fails with code BAD_USER_INPUT and the text `veloxgql: UpdateXInput.yID: the X is already linked to a Y`; with typ "Account" the text ends `linked to an Account`. On a missing id it returns nil.
  - **`e2eMutProfilePolicy`.** A Read policy on Profile hides `handle = "hidden"`. Write it in the style of e2eMutCirclePolicy, and add it to TestMutationsRun's file map at mutation_e2e_test.go:1777 as `"gen/profile_policy.go"`.
  - **`TestRepointHiddenChild`**, under `each`:
    - Insert writer w, profile p1 (handle "hidden", writer w), and profile p2 (handle "free", no writer).
    - `gen.UpdateWriter(ctx, writerID(w.ID), gen.UpdateWriterInput{ProfileID: ptr(profileID(p2.ID))})` fails with code BAD_USER_INPUT and the message exactly `veloxgql: UpdateWriterInput.profileID: the Writer is already linked to a Profile`.
    - Read without the policy, p1's writer_id is still w and p2's is NULL.
    - With p1's handle set to "seen" by raw SQL, the same update succeeds: p2 is w's, and p1's writer_id is NULL.

    Add `TestRepointHiddenChild` to the PASS list at :1801.
  - The other tests in e2eMutRun create profiles too. Each one whose handle is "hidden" changes its handle; `git grep -nP 'Handle: "hidden"' -- contrib/graphql/cmd` prints nothing at 23d0dee.
- [ ] **Step 2: Run them.** `(cd contrib/graphql && go test -count=1 -run TestStillLinked ./veloxgql/gqlimpl)`. Expected: FAIL, StillLinked is undefined.
- [ ] **Step 3: Implement.**
  - **StillLinked**, as above.
  - **gogen.** In the `one` branch, after the scoped unlink and before the link, emit:

    ```go
    if err := gqlimpl.StillLinked(ctx, db.Query[<Tgt>]().Where(<fkh>.EQ(<v>), <tpk>.NEQ(<d>)), "<In>.<edge>ID", "<T>", "<Target>"); err != nil {
    	return err
    }
    ```

    It runs on the mutation's DB, inside its transaction, after the locking check that UpdateX made earlier (builder ruling 12).
  - **The rows it finds.** After the scoped unlink, a child still holding the key is exactly one the caller could not change. The ruling's count of the current child is this Exists: an Exists is enough, because the answer is yes or no.
  - **The version.** Bump it at every site listed under Files.
  - **The doc.** The Mutations section gains: "Re-pointing a one-to-one edge from the owner, when its current child is one the caller may not change, hidden by the tenant scope or a policy, fails with BAD_USER_INPUT \"already linked\", and the child stays linked. The error names only the two types, no ID, tenant or column." (Check finding 10: the child may be another tenant's, written through WithAllTenants or raw SQL.)
- [ ] **Step 4: Regenerate and run.**
  - Run RG and R5. Read the golden diff: only the version lines differ. No golden has an O2O re-point from the owner today; `git grep -nP 'SetNull\(\)\)\.Where\([^)]*\.EQ\(key\), \w+\.ID\.NEQ' -- contrib/graphql/internal/gogen/testdata example/graphql/gen` prints nothing.
  - Run `(cd contrib/graphql && go test -count=1 ./internal/... ./veloxgql/...)`. Expected: PASS.
  - Run T under D. Expected: PASS, with `TestRepointHiddenChild` on all four targets.
- [ ] **Step 5: Commit.** Message: `velox-graphql: refuse an O2O re-point over a hidden child; GeneratedCodeIsVersion3 (G47)`. Run G, then the gate.

---

### Task 7: A delete removes one-sided many-to-many join rows (G48; spec ruling 23)

**Files:**
- Modify: `CG/internal/gogen/mutation.go`, writeDelete's join gathering (:1081-1117 at 23d0dee).
- Test: `CG/internal/gogen/render_test.go:1067`, TestDeleteJoinRows, gets a new case. `CG/cmd/velox-graphql/mutation_e2e_test.go` gets a new entity Badge in e2eMutSchema (:36-110), the DDL arrays (:403-470), and a new e2e `TestDeleteOneSidedM2M`.
- The one-sided edge goes on a new entity, not on Article (check finding 3). On Article, DeleteArticle would delete article_circles rows too, and TestDelete's "An article, of no M2M edge, is one DELETE" (mutation_e2e_test.go:1476-1491) and TestDeletePolicy's doc (:1510) would go stale. Badge has no mutations, so no test counts its statements.
- Modify: the Mutations section of CG/doc.go, the delete sentence.
- Regenerate: RG, R5. Expected: no golden changes. Every M2M edge in model.json is two-sided (Group.users/User.groups, Post.related/related_by), and so is the example's.

**Interfaces:**
- Consumes: Task 1's `joinCols`.
- Produces: nothing new.

- [ ] **Step 1: Write the failing tests.**
  - **TestDeleteJoinRows.** Add a case of a model where GroupPin has `edge.To("posts", Post)` M2M with join GroupPinPost, and Post has no inverse edge and has deleteX. DeletePost's body deletes from GroupPinPost where `PostID.EQ(key)` (the join's second field), after lockedCheck, as it does Post's own join rows.
  - **e2eMutSchema.** A new entity `Badge`, with a field `label` (String), the edge `edge.To("circles", Circle.Type)` and no annotation. Circle gets no inverse. Its join table is badge_circles(badge_id, circle_id), as velox-gen names it (check with `ls gen/badge_circle_velox.go` in the e2e module). Add `badges` and `badge_circles` to every DDL array:
    - sqliteDDL, pgDDL and mysqlDDL: plain;
    - sqliteFKDDL and pgFKDDL: badge_circles' columns with inline `REFERENCES badges (id)` and `REFERENCES circles (id)`, with no ON DELETE, so a leftover join row refuses the circle's DELETE;
    - mysqlFKDDL: the same as table-level `FOREIGN KEY (badge_id) REFERENCES badges (id), FOREIGN KEY (circle_id) REFERENCES circles (id)`, since MySQL ignores an inline REFERENCES (check finding 14);
    - dropDDL: badge_circles first, badges before circles.
  - **`TestDeleteOneSidedM2M`**, under eachFK:
    - Insert circles c and c2, badges b1 and b2, and join rows (b1, c), (b2, c) and (b1, c2).
    - `gen.DeleteCircle(ctx, circleID(c.ID))` returns circleID(c.ID). Under the foreign keys it would otherwise fail with a *velox.ConstraintError.
    - badge_circles holds only (b1, c2), and both badges remain.

    Add it to the PASS list.
- [ ] **Step 2: Run them.** `(cd contrib/graphql && go test -count=1 -run TestDeleteJoinRows ./internal/gogen)`. Expected: FAIL, no GroupPinPost delete.
- [ ] **Step 3: Implement.** After the loop over e.Edges, walk `r.m.Entities` in model order. For every M2M edge that is not Inverse and whose Target is e.Name, add `joinCols`'s join entity with its `other` field (the column holding e's key). Use the same de-duplication by join entity and column, so a two-sided edge, already gathered from e's own inverse, adds nothing. The Mutations section's delete sentence becomes "deletes the row and every join row that holds its key, of its own many-to-many edges and of other entities' edges to it". It adds: "velox-gen's own migrations give a join table's foreign keys ON DELETE CASCADE, as ent's do, so the database would remove them anyway; the delete matters for a schema written by hand." Check finding 11: ent deletes only the node and relies on that cascade (internal/gen/dbnames.go:202 for velox-gen), so the unscoped join-row delete, after the Writable locked check, gives what ent gives.
- [ ] **Step 4: Regenerate and run.** RG, R5: `git status --short` lists no golden or example change. Then T under D. Expected: PASS.
- [ ] **Step 5: Commit.** Message: `velox-graphql: delete one-sided M2M join rows with their target (G48)`. Run G, then the gate.

---

### Task 8: A Required edge with a NULL-able key: `parent` is nullable, and the update inputs that could only fail are gone (Wave 2 leftovers 3 and 4; controller rulings 1 and 6)

**Background (Wave 2, execution ruling 9).** A Required self edge's implicit key is NULL-able and ON DELETE SET NULL, as in ent. velox refuses it nil before anything is sent, on insert and on every Set: a ValidationError "missing required edge \"T.e\"" (validate.go:95-100 and :126-130, called at write.go:410). So:
- after the parent's delete, a child's key is NULL, and the SDL's `parent: Folder!` would be a non-null violation that nulls the enclosing object;
- on the side that does not own the key, an update's `remove<Edge>IDs`, `clear<Edge>`, and a one-to-one `<edge>ID` first set the old target's key to NULL. UpdateX's `one` branch always sends that unlink first (mutation.go:1032), even when there is no current child. velox refuses it, as ent's builder check does, so these inputs can only fail.

**The rule (controller ruling 6).**
- **The side that owns the key** keeps its inputs. `parentID` on update sets the row's own key to a new, non-null parent, so re-parenting works. The edge is Required, so it has no `clear<Edge>`, as today.
- **The side that does not own the key**, when the other side's key is a RequiredKey, keeps its create inputs (`childIDs`, `nextID`). Their links set a NULL key to the new row and never unlink. On update:
  - an O2M keeps `add<Edge>IDs` and loses `remove<Edge>IDs` and `clear<Edge>`;
  - an O2O loses `<edge>ID` and `clear<Edge>`.

  A child moves by its own `parentID` or `prevID`.
- **No WriteError wrap.** With these inputs gone, no generated statement sets a RequiredKey key to NULL. An add sets a NULL key to the owner, a create link does the same, an owner-side update sets the row's own key to a non-nil value, and a delete leaves the key to the foreign key. So no target-row write can return the ValidationError. Controller ruling 3's wrap has nothing to wrap, and it is not built: no gogen change, and no golden change.

**Files:**
- Modify: `CG/internal/sdl/input.go`. New exported `RequiredKey`, and edgeInputs (:235-337) uses it.
- Modify: `CG/internal/sdl/render.go:400-403`, the unique edge's type.
- Test: `CG/internal/sdl/input_test.go:223` TestEdgeInputsFollowTheKey and `CG/internal/sdl/render_test.go:540` TestEdges, new cases on a test-local model (model.json is not changed). CG/cmd/velox-graphql/mutation_e2e_test.go gets a new Folder entity and the e2e `TestRequiredSelfEdge`.
- Modify: CG/doc.go, the Mutations section and the edge-input paragraph (:228-236 at 23d0dee).
- Regenerate: RG, R5. Expected: no golden changes. model.json's Category parent is not Required, and the example has no Required edge with a NULL-able key.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// RequiredKey reports whether a Required edge of t that owns its key holds
// the field fk, and fk is NULL-able: the key of any Required edge whose key
// is NULL-able, such as a self edge's. velox refuses such a key nil
// (velox.ColumnDesc.RequiredEdge), and the database's ON DELETE SET NULL can
// still clear it. velox-gen marks the same columns (internal/gen/lower.go:246-253).
func RequiredKey(t *plugin.Entity, fk *plugin.Field) bool
```

- [ ] **Step 1: Write the failing SDL tests.** The model is local: Folder has `children` (O2M to Folder) and `parent` (M2O, OwnsKey, Required, Field `folder_children`, Nullable), and `next` (O2O non-owner) with `prev` (O2O, OwnsKey, Required, Field `folder_next`, Nullable). Folder has create and update.
  - **TestEdges:**
    - Folder's fields include `parent: Folder` and `prev: Folder`, with no `!`.
    - A Required M2O with a NOT NULL key (Post.author) keeps `author: User!`.
  - **TestEdgeInputsFollowTheKey** pins both inputs whole:
    - `CreateFolderInput` is exactly `name: String!`, `childIDs: [ID!]`, `parentID: ID!`, `nextID: ID`, `prevID: ID!`, in the order edgeInputs writes them.
    - `UpdateFolderInput` is exactly `name: String`, `addChildIDs: [ID!]`, `parentID: ID`, `prevID: ID`. There is no `removeChildIDs`, `clearChildren`, `nextID`, `clearNext`, `clearParent` or `clearPrev`.
    - A non-Required self edge (model.json's Category children/parent, or a local copy) keeps `removeChildIDs` and `clearChildren`.
- [ ] **Step 2: Run them.** `(cd contrib/graphql && go test -count=1 -run 'TestEdges|TestEdgeInputsFollowTheKey' ./internal/sdl)`. Expected: FAIL: `parent: Folder!`, and removeChildIDs and nextID are present.
- [ ] **Step 3: Implement.**
  - **RequiredKey.** Some edge of t has Required, OwnsKey and `Field == fk.Name`, and `fk.Nullable` holds (check finding 17: this matches velox-gen's RequiredEdge).
  - **render.go:402.** `if ed.Required && !(fk != nil && fk.Nullable)`, with fk from `g.keyField(ed)`. A Required unique edge whose key is NULL-able is `T`. Create keeps `parentID: ID!`: input.go's `case create && ed.Required` is unchanged.
  - **input.go.** Add one `case` above `case ed.Unique:` (:325), for `!create && RequiredKey(target entity, fk)`:
    - a unique edge writes nothing;
    - an O2M writes only `add<One>IDs`.

    Create falls through to the existing branches. The doc comment of edgeInputs gains the rule.
  - **CG/doc.go.** It says:
    - a Required edge whose key is NULL-able (a Required self edge's) can be cleared by the database's ON DELETE SET NULL, so its field is nullable in the SDL, as entgql's is not (controller ruling 1);
    - on the side that does not own the key, an update only adds: a child moves by its own `<edge>ID`, and cannot be removed or unlinked from the other side, since that would leave its Required key NULL.
- [ ] **Step 4: Write the e2e test.**
  - **Folder** in e2eMutSchema has a `name` field and these edges, with create and update mutations:

    ```go
    edge.To("children", Folder.Type).From("parent").Unique().Required()
    edge.To("next", Folder.Type).Unique().From("prev").Unique().Required()
    ```

    velox-gen names the implicit columns folder_children and folder_next. Check them in gen/folder_velox.go. Add `func folderID(id int64) string { return veloxgql.EncodeID("Folder", id) }` beside profileID (mutation_e2e_test.go:533).
  - **The DDL.** folders(id, name, folder_children NULL, folder_next NULL UNIQUE) in every array.
    - sqliteFKDDL and pgFKDDL give both keys an inline `REFERENCES folders (id) ON DELETE SET NULL`.
    - mysqlFKDDL uses table-level `FOREIGN KEY (folder_children) REFERENCES folders (id) ON DELETE SET NULL` and the same for folder_next. MySQL parses an inline REFERENCES and ignores it, which is why mysqlFKDDL (mutation_e2e_test.go:452-458) uses FOREIGN KEY clauses (check finding 14).
    - dropDDL drops folders first.
  - **`TestRequiredSelfEdge`**, under eachFK:
    - Insert the root r by raw SQL, with folder_children and folder_next NULL: velox refuses a nil Required key.
    - `CreateFolder(ctx, {Name: "c", ParentID: folderID(r.ID), PrevID: folderID(r.ID)})` succeeds: c's folder_children and folder_next are r.
    - Reflect on `gen.UpdateFolderInput`. It has AddChildIDs, ParentID and PrevID, and no RemoveChildIDs, ClearChildren, NextID or ClearNext.
    - **The owning side re-parents.** Create c2 with ParentID r and PrevID c. `UpdateFolder(ctx, folderID(c2.ID), {ParentID: ptr(folderID(c.ID))})` succeeds: c2's folder_children is c.
    - **The other side adds.** Insert d by raw SQL with folder_children NULL (it may be NULL in the database). `UpdateFolder(ctx, folderID(r.ID), {AddChildIDs: [folderID(d.ID)]})` succeeds: d's folder_children is r.
    - **SET NULL.** Delete r with raw SQL. Then c's `ParentGQL(ctx)` returns `nil, nil`, and so does `PrevGQL`.

    Add it to the PASS list.
- [ ] **Step 5: Regenerate and run.** RG and R5: no golden or example change. Then `(cd contrib/graphql && go test -count=1 ./internal/...)` and T under D. Expected: PASS.
- [ ] **Step 6: Commit.** Message: `velox-graphql: a Required edge with a NULL-able key is nullable, and its other side only adds`. Run G, then the gate.

---

### Task 9: Bidirectional self edges end to end through GraphQL (Wave 2 leftover 2)

**Files:**
- Modify: `CG/cmd/velox-graphql/mutation_e2e_test.go`: e2eMutSchema's Writer, every DDL array, the env context (:522), and new e2e `TestBidiPeers` and `TestBidiPartner`.
- Modify: `CG/cmd/velox-graphql/e2e_test.go`: new e2e `TestCategoryTwin` in e2eWhere, and its name in TestWhereRun's list (:1371).
- Modify: `CG/doc.go:221` (in the Mutations section after Task 2), one sentence.

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: Extend the schema.** Writer gains:
  - `edge.To("peers", Writer.Type)`: a self M2M with no inverse, so it is bidirectional. Its join table is writer_peers(writer_id, peer_id); check it in gen/writer_peer_velox.go.
  - `edge.To("partner", Writer.Type).Unique()`: a self O2O with no inverse, bidirectional, on the implicit column writer_partner.

  The DDL:
  - writers gains `writer_partner` (NULL, UNIQUE) in every array.
  - The FK variants give it `ON DELETE SET NULL` to writers (id). sqliteFKDDL, pgFKDDL and mysqlFKDDL reuse `sqliteDDL[0]`, `pgDDL[0]` and `mysqlDDL[0]` for writers today (:415, :434, :453), so each gets its own writers statement with the self-reference (check finding 14).
  - writer_peers is plain in the three plain arrays. In sqliteFKDDL and pgFKDDL both columns have an inline `REFERENCES writers (id)`.
  - mysqlFKDDL writes every one of these foreign keys as a table-level `FOREIGN KEY (…) REFERENCES writers (id)` clause, `ON DELETE SET NULL` for writer_partner, as its existing statements do (:452-458): MySQL parses an inline REFERENCES and ignores it.
  - dropDDL drops writer_peers first. writers' self-reference needs no order.
- [ ] **Step 2: Pin the tenant conversion.** At :522, `velox.WithTenant(…, int64(7))` becomes `velox.WithTenant(…, 7)`, an untyped int. Wave 2's conversion (the spec's builder ruling 8) makes it the int64 org_id. Every test runs under it. The other `int64(…)` tenants stay typed.
- [ ] **Step 3: Write `TestBidiPeers`**, under eachFK. Insert writers a and b.
  - `UpdateWriter(a, {AddPeerIDs: [b]})`: writer_peers holds (a, b) and (b, a), and b's PeersGQL lists a.
  - `UpdateWriter(a, {RemovePeerIDs: [b]})`: writer_peers is empty.
  - Re-add, then `UpdateWriter(b, {ClearPeers: true})`: writer_peers is empty.
  - `CreateWriter({Name: "c", Password: "x", PeerIDs: [a]})` writes both rows.
  - `DeleteWriter(c)`: a's peers are empty, and the FK did not refuse.
- [ ] **Step 4: Write `TestBidiPartner`**, under each. Insert writers a, b and d.
  - `CreateWriter({…, PartnerID: a})` gives c: c's writer_partner is a, and a's is c.
  - `UpdateWriter(c, {PartnerID: b})`: c ↔ b, and a's writer_partner is NULL.
  - `UpdateWriter(b, {ClearPartner: true})`: both b and c are NULL.
  - **Taken.** Link a ↔ d. Then `UpdateWriter(c, {PartnerID: a})` fails with a `*velox.ConstraintError` of Kind `velox.ConstraintUnique` (errors.As), shown by ErrorPresenter as BAD_USER_INPUT. a, c and d are unchanged.
  - **Self-link.** `UpdateWriter(c, {PartnerID: c})` fails with code BAD_USER_INPUT. Its message starts `veloxgql: UpdateWriterInput.partnerID: invalid value: a row cannot be its own `, and it wraps a `*velox.ValidationError`.
- [ ] **Step 5: Write `TestCategoryTwin`** in e2eWhere. CreateCategory with `TwinID: ptr(catID(c1.ID))` (catID is e2e_test.go:911; check finding 18 looked for a categoryID, which does not exist) sets category_twin on both rows: the new row's is c1, and c1's is the new row. Read them with raw SQL on the categories table (e2e_test.go:796).
- [ ] **Step 6: Update the doc.** CG/doc.go:221's bullet "A self-edge is written one way, as the core's loads read it." becomes:

  > A bidirectional self-edge (a self-referencing edge with no inverse) is written both ways, as in ent: a many-to-many one writes and removes both join rows, and a one-to-one one keeps both rows' keys pointing at each other. A self-edge with an inverse is written one way.
- [ ] **Step 7: Run.** Add TestBidiPeers and TestBidiPartner to TestMutationsRun's list. Then T under D. Expected: PASS, both on all four targets, and TestCategoryTwin in TestWhereRun.
- [ ] **Step 8: Commit.** Message: `velox-graphql: bidirectional self edges end to end`. Run the gate. G is not needed: no generated file is checked in.

---

### Task 10: Where predicates on the node key (G1; builder ruling 13)

**Files:**
- Modify: `CG/internal/sdl/where.go`: predicatesOf (:61), wantsWhere (:107), whereOf (:150 and :163).
- Modify: `CG/internal/gogen/where.go` only if an ordered ID op has no helper. `gqlimpl.ID` takes the handle method as `op`, so `Posts.ID.GT` fits.
- Test: `CG/internal/sdl/render_test.go`, new `TestNodeKeyWhere`; `CG/cmd/velox-graphql/e2e_test.go`, e2eWhere's `TestWhereIDs` (:1259), extended.
- Modify, because `entity()` (CG/internal/sdl/render_test.go:153-160) is a Node with an int64 `id`, so every where input of a test entity gains the key's eight predicates after not/and/or (check finding 6):
  - `CG/internal/sdl/where_test.go`: a helper `nodeKey() []string` beside `logic` (:15), giving `id: ID, idNEQ: ID, idIn: [ID!], idNotIn: [ID!], idGT: ID, idGTE: ID, idLT: ID, idLTE: ID`. Every `append(logic(…), …)` want of an `entity()`-built input (:44, :80, :110, :125, :137, :157) becomes `append(append(logic(…), nodeKey()...), …)`. The "ID" subtest (:52-59), whose key has `graphql.WhereOps()`, now wants nodeKey()'s eight: OpsDefault of an ordered key.
  - `CG/internal/sdl/where_test.go:239`, TestWhereCarriesDirectives: `[3:]` becomes `[11:]`, so the slice is the salary predicates again (10).
  - `CG/cmd/velox-graphql/e2e_test.go:254` and `:269`, TestPluginThroughVeloxGen: `"  or: [PostWhereInput!]\n  authorID: ID\n…"` and `"  or: [CategoryWhereInput!]\n  mentorID: ID\n…"` gain `  id: ID\n  idNEQ: ID\n  idIn: [ID!]\n  idNotIn: [ID!]\n  idGT: ID\n  idGTE: ID\n  idLT: ID\n  idLTE: ID\n` after the `or` line.
  - `example/graphql/main_test.go:605-611`, TestSensitiveFieldAbsent: UserWhereInput's inputFields gain `id, idNEQ, idIn, idNotIn, idGT, idGTE, idLT, idLTE` after `or`.
  - Any other test that compares a whole where input. Find them with `git grep -nP 'fieldsOf\(s, "\w+WhereInput"\)|WhereInput"\) \{ inputFields' -- '*_test.go'`; at 23d0dee that prints where_test.go :45, :56, :81, :111, :128, :138, :158, :239 and example/graphql/main_test.go:603. rules_test.go:291 reads directives by name and is unaffected.
- Modify: CG/doc.go, the where-input section: "The node key has id, idNEQ, idIn, idNotIn, and for an ordered key idGT, idGTE, idLT and idLTE, typed ID and decoded with the type check".
- Regenerate: RG, R5, RQ.

**Interfaces:**
- Consumes: nothing.
- Produces: the kind `"orderedID"` in predicatesOf: `has` and `defaults` are both `OpsEquality|OpsComparison`. `"ID"` stays equality only.

- [ ] **Step 1: Write the failing test.** `TestNodeKeyWhere` covers a where input that has the predicates, typed `ID` (lists `[ID!]`):

  | Entity | Key | Predicates |
  |---|---|---|
  | int64 key, no annotation on it | int64 | id, idNEQ, idIn, idNotIn, idGT, idGTE, idLT, idLTE |
  | uint64 key | uint64 | the same eight |
  | string key | string | the same eight |
  | uuid key | uuid | id, idNEQ, idIn, idNotIn |
  | enum key | enum | id, idNEQ, idIn, idNotIn |
  | int64 key with `graphql.WhereOps()` | int64 | the same eight (OpsDefault) |
  | int64 key with `graphql.WhereOps(graphql.OpEQ)` | int64 | id only |
  | int64 key, entity has no other WhereOps or WhereEdge | int64 | no where input at all |

  The last row is controller ruling 2. A time or bytes key is not in the table: keyBase (CG/internal/sdl/surface.go:262-276) makes a Node only of a one-column int, uint, string, enum or uuid key, so such an entity has no `id` and no key predicates (check finding 5). One more case asserts that: an entity with a time key and a WhereOps field has a where input with no `id`.
- [ ] **Step 2: Run it.** `(cd contrib/graphql && go test -count=1 -run TestNodeKeyWhere ./internal/sdl)`. Expected: FAIL, no id predicates.
- [ ] **Step 3: Implement.** In whereOf, the node key (`o.node && i == e.PrimaryKey[0]`):
  - **Ops.** It is not skipped when `a.WhereOps == 0`: its ops are OpsDefault, unless the field's annotation has SkipWhereInput. An explicit WhereOps wins.
  - **Kind.** Its kind is `"orderedID"` for the int types, uint types and string, and `"ID"` for enum and uuid, the other key types a Node can have. Builder ruling 13's "time" is moot: a time key is never a Node.
  - **wantsWhere** is unchanged: the key alone does not make a where input.
  - **gogen.** The Go code goes through idCall (where.go:113) with the predicate's handle method, as a foreign key's ID predicates do.
  - **Order.** The key's predicates come first in the input, after not/and/or, since the key is field 0.
- [ ] **Step 4: Extend the e2e.** Add these cases to `TestWhereIDs`:
  - `EventWhereInput{ID: ptr(eventID(x))}` finds x, and Event has no IDGT field: it is a UUID key. Check this with `reflect.TypeOf(gen.EventWhereInput{}).FieldByName("IDGT")`.
  - `OwnerWhereInput{IDGT: ptr(ownerID(2))}` returns the owners with id > 2. Owner's key is ids.UserID, so this goes through IDAs.
  - `OwnerWhereInput{IDIn: []string{eventID(x)}}` fails with veloxgql.ErrWrongIDType.
- [ ] **Step 5: Regenerate and run.**
  - Run RG, R5 and RQ.
  - Read the SDL diff. CategoryWhereInput (:113), PostWhereInput (:275) and AccountWhereInput (:400) in CG/internal/sdl/testdata/velox.graphqls gain the eight int64 predicates, and so do example PostWhereInput (:134), LabelWhereInput (:231) and UserWhereInput (:317).
  - The gogen goldens and example/graphql/gen gain the filter lines, and example/graphql/graph changes with gqlgen.
  - Then `(cd contrib/graphql && go test -count=1 ./...)` and `(cd example && go test -count=1 ./graphql)`. Expected: PASS, with the test updates listed under Files.
- [ ] **Step 6: Commit.** Message: `velox-graphql: where predicates on the node key (G1)`. Run G, then the gate.

---

### Task 11: Application enums bind with `@goModel`; `@goModel` is velox-graphql's own (G11, G12, G13)

**Files:**
- Modify: `CG/internal/sdl/render.go`:
  - :490-512, the enum's name: an app enum records its model;
  - :837, the enum line;
  - :561, the directive refusal.
  - The gqlEnum struct gains `model string`.
- Modify: `CG/internal/gogen/render_test.go:277`, whose unbound reason goes: an app enum is bound now. (surface.go:293 already says the application's enums "bind with @goModel", so it does not change; check finding 15.)
- Modify: `CG/internal/sdl/rules.go:172-174`, appDirective's comment: "(@goField and @goModel, velox-graphql's own, are refused in graphql.Directives.)".
- Note: `w.dirs` (render.go:678-686) writes only goField and the prelude's directives when `!w.app`. @goModel goes in neither list: it is written by the enum line itself, not through dirs, so velox.graphqls carries it whether or not the application's directives are written. Its declaration is in the vocabulary header (render.go:755) either way.
- Modify: `CG/doc.go:225-226`. The bullet "Not written: … and application enums bound with @goModel" loses that clause. The gqlgen.yml section says an application's enum type is bound by `@goModel` and must implement gqlgen's `graphql.Marshaler` and `graphql.Unmarshaler` (MarshalGQL, UnmarshalGQL) itself, as gogen's writeEnum doc says (render.go:388).
- Test: `CG/internal/sdl/render_test.go`, new `TestAppEnumGoModel` and `TestGoModelRefused`.
- Create: `CG/cmd/velox-graphql/enum_e2e_test.go`, with the top-level `TestAppEnumRenamed`, which builds a module of its own (check finding 15). It is its own module, not an inner test of TestCollectRun: it needs no database but SQLite, and adding Alarm to e2eCollectSchema would need a table on every target, a resolver in e2eCollectResolvers and a change to TestCollectRun's file map. It reuses `app`, `e2eCollectGqlgen` (the application's gqlgen.yml, collect_e2e_test.go) and TestCollectRun's run sequence (:909-930), and adds its own sources `e2eEnumSchema`, `e2eLevelsGQL`, `e2eEnumResolvers` and `e2eEnumRun`.

**Interfaces:**
- Consumes: nothing.
- Produces: nothing exported.

- [ ] **Step 1: Write the failing tests.**
  - **`TestAppEnumGoModel`.** In a local model, Event has field `level`, an enum with Enum "Level", PkgPath "example.com/app/levels" and ElemType "levels.Level". The SDL holds `enum EventLevel @goModel(model: "example.com/app/levels.Level") {`. velox-gen's own enums (UserRole) have no @goModel.
  - **`TestGoModelRefused`.** A field annotation `graphql.Directives(graphql.Directive{Name: "goModel"})`, and the same on an entity, fail Render with `@goModel is velox-graphql's own: it binds types to the Go code velox generates and the application's enum types`.
- [ ] **Step 2: Run them.** `(cd contrib/graphql && go test -count=1 -run 'TestAppEnumGoModel|TestGoModelRefused' ./internal/sdl)`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - **The model.** An enum that is not velox-gen's (`!g.goEnums[f.Enum]`) records `model: f.PkgPath + "." + f.Enum`, and the enum line is `w.line("enum ", en.name, w.dirs(...), " {")` with the @goModel directive.
  - **Refusal.** Add `case d.Name == "goModel":` beside goField's at :561.
  - **The comment.** At :492, "Plan 2 binds it" becomes "@goModel binds it".
- [ ] **Step 4: Write `TestAppEnumRenamed`** (G12) in enum_e2e_test.go. It writes the module with `app(t, files, true)`, then runs velox-gen with the plugin, `gqlgen generate`, `mod tidy -e`, `vet ./...` and `test -count=1 -v .`, as TestCollectRun does.
  - **The files.** `schema/schema.go` (e2eEnumSchema), `levels/levels.go` (e2eLevelsGQL), `gqlgen.yml` (e2eCollectGqlgen), `graph/schema.resolvers.go` (e2eEnumResolvers: Node, Nodes and Alerts, in e2eCollectResolvers' form) and `enum_test.go` (e2eEnumRun).
  - **The schema.** Alarm has `graphql.Type("Alert")`, a field `level` of `levels.Level`, and `graphql.QueryField("alerts")`. e2eLevelsGQL is e2eLevels plus `MarshalGQL(io.Writer)` and `UnmarshalGQL(any) error` on Level, which write and read the upper-case GraphQL value. e2eLevels stays as it is for the other tests.
  - **The run.** gqlgen generate succeeds with no `models:` entry for the enum. e2eEnumRun's `TestAlertLevel` creates the alarms table on in-memory SQLite, inserts one alarm of level "high", and serves `{ alerts { edges { node { level } } } }` through gqlgen's handler with veloxgql.NewExtension. It returns `"level":"HIGH"`. The values are as enumValues writes them; check the SDL.
  - **The PASS check.** TestAppEnumRenamed requires `--- PASS: TestAlertLevel ` in the output.
- [ ] **Step 5: Regenerate and run.** RG: no golden changes, since model.json has no app enum. Then `(cd contrib/graphql && go test -count=1 ./...)`. Expected: PASS.
- [ ] **Step 6: Commit.** Message: `velox-graphql: bind application enums with @goModel (G11, G12, G13)`. Run the gate.

---

### Task 12: Merged directive arguments are not shared; a lone CR is a newline (G6, G9)

**Files:**
- Modify: `CG/annotation.go:167` (Merge) and `:288` (Directives).
- Modify: `CG/internal/sdl/render.go:702`, desc().
- Test: `CG/annotation_test.go`, new `TestMergeClonesArgs`; `CG/internal/sdl/render_test.go:648`, TestDescriptions, new cases.

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: Write the failing tests.**
  - **`TestMergeClonesArgs`.**
    - Merge annotation a, which has `Directive{Name: "d", Args: map[string]any{"k": 1}}`, into b. Setting `merged.Directives[0].Args["k"] = 2` leaves a's Args at 1.
    - The same holds for `graphql.Directives(ds...)`: changing the result's Args leaves ds's map unchanged.
  - **TestDescriptions.** A comment `"a\rb"` renders as the block description `"""\na\nb\n"""`, the form a `"a\nb"` comment takes today. `"a\r\n\rb"` renders as `"a\n\nb"`'s form.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.**
  - **Merge.** After the `slices.Concat`, set `out.Directives[i].Args = maps.Clone(out.Directives[i].Args)` for each directive.
  - **Directives.** Clone each directive's Args after the `slices.Clone` in the same way.
  - **desc().** After the `\r\n` replace, `strings.ReplaceAll(text, "\r", "\n")`.
- [ ] **Step 4: Run.** `(cd contrib/graphql && go test -count=1 . ./internal/sdl)`. Expected: PASS. RG makes no golden change.
- [ ] **Step 5: Commit.** Message: `velox-graphql: clone merged directive arguments, normalize a lone CR (G6, G9)`. Run the gate.

---

### Task 13: `Cost` reads `@skip` and `@include` (G24)

**Files:**
- Modify: `CG/veloxgql/cost.go`: `coster.set` (:64) and `coster.field` (:102). New method `skipped`.
- Modify: `CG/veloxgql/cost.go:24`, `CG/veloxgql/doc.go:172-173` and `CG/doc.go:320`. The text "@skip and @include are not read" becomes "a selection that @skip(if: true) or @include(if: false) leaves out costs nothing, its condition read from the variables; one whose variable is missing counts".
- Test: `CG/veloxgql/cost_test.go`. The case at :163-166 changes, and new `TestCostSkipInclude`.

**Interfaces:**
- Consumes: nothing.
- Produces: `func (c *coster) skipped(ds ast.DirectiveList) bool` (unexported).

- [ ] **Step 1: Write the failing test.** `TestCostSkipInclude` has these cases, with `l.MaxPage = 7`:

  | Query | Vars | Want |
  |---|---|---|
  | `{ users(first: 2) @skip(if: true) { totalCount } }` | — | 0 |
  | `{ users(first: 2) @include(if: false) { totalCount } }` | — | 0 |
  | `{ users(first: 2) @skip(if: false) { totalCount } }` | — | 1+2 |
  | `query ($s: Boolean!) { users(first: 2) @skip(if: $s) { totalCount } }` | `{"s": true}` | 0 |
  | the same | `{"s": false}` | 1+2 |
  | `{ users(first: 2) { totalCount } ... @include(if: false) { node(id: "x") { id } } }` | — | 1+2 |
  | `{ ...F @skip(if: true) users(first: 1) { totalCount } } fragment F on Query { many: users(first: 5) { totalCount } }` | — | 1+1 |
  | `query ($s: Boolean!) { users(first: 2) @skip(if: $s) { totalCount } }` | nil (no vars) | 1+2 |

  The last row is a missing variable: costOf skips `VariableValues` when vars is nil (cost_test.go:51-66), so `$s` is missing, and the field counts. Every query here validates under gqlparser's default rules, as costOf requires (check finding 7): the fragment's field is aliased `many`, since two `users` fields with different arguments conflict, and `$s` is `Boolean!`, as `if` expects. The case at :163 changes to expect 0.
- [ ] **Step 2: Run it.** `(cd contrib/graphql && go test -count=1 -run 'TestCost' ./veloxgql)`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - **skipped.** For `skip` and `include` in ds, read the `if` argument with `Value(c.vars)`. It returns true when skip's value is `true` or include's is `false`. A value that is not a bool, or a missing variable, does not skip.
  - **Where it runs.** set calls it for a Field's, an InlineFragment's and a FragmentSpread's Directives before costing them. A spread checks its own directives before `c.spread`, so the per-name cache stays correct.
- [ ] **Step 4: Run.** `(cd contrib/graphql && go test -count=1 ./veloxgql/...)`. Expected: PASS.
- [ ] **Step 5: Commit.** Message: `veloxgql: Cost reads @skip and @include (G24)`. Run the gate.

---

### Task 14: `ErrorPresenter` takes options; `OnCanceled` (G37)

**Files:**
- Modify: `CG/veloxgql/presenter.go:85-147`.
- Modify: `CG/veloxgql/api_test.go`. TestPublicAPI's list gains `OnCanceled` and `PresenterOption`.
- Test: `CG/veloxgql/presenter_test.go`, new `TestErrorPresenterOnCanceled`.
- No call site changes: every one passes `nil` or a log (example/graphql/main.go:76, collect_e2e_test.go:458, mutation_e2e_test.go:1746).

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// PresenterOption configures ErrorPresenter. OnCanceled makes one; the zero
// PresenterOption is not valid, and ErrorPresenter panics on it, as
// NewExtension does on a zero Option.
type PresenterOption struct{ apply func(*presenter) }

// OnCanceled sets the function an error that is only the cancellation of the
// request goes to, in place of the default: slog at level debug when log is
// nil, and nothing when log is not. A nil f drops such errors.
func OnCanceled(f func(ctx context.Context, err error)) PresenterOption

func ErrorPresenter(log func(ctx context.Context, err error), opts ...PresenterOption) graphql.ErrorPresenterFunc
```

`presenter` is an unexported struct holding log and canceled. ErrorPresenter fills it from log, as today, then applies opts.

- [ ] **Step 1: Write the failing test.** `TestErrorPresenterOnCanceled`, with a canceled ctx and the cases of TestErrorPresenterCanceled (presenter_test.go:405-430):
  - `ErrorPresenter(l.log, OnCanceled(c.log))`: a bare `context.Canceled` goes to c, not l, and the client sees what it does today.
  - The same presenter sends `errors.Join(context.Canceled, driverErr)` to l, not c.
  - `ErrorPresenter(nil, OnCanceled(nil))`: a bare cancellation logs nothing. Capture slog with a handler at level debug, as TestErrorPresenterSlog does.
  - `ErrorPresenter(nil, veloxgql.PresenterOption{})` panics with `veloxgql.ErrorPresenter: zero PresenterOption`.
- [ ] **Step 2: Run it.** `(cd contrib/graphql && go test -count=1 -run 'TestErrorPresenter|TestPublicAPI' ./veloxgql)`. Expected: FAIL to compile.
- [ ] **Step 3: Implement** as specified. The doc of ErrorPresenter names OnCanceled in its cancellation paragraph (:95-101).
- [ ] **Step 4: Run.** `(cd contrib/graphql && go test -count=1 ./veloxgql/...)`. Expected: PASS.
- [ ] **Step 5: Commit.** Message: `veloxgql: ErrorPresenter options, OnCanceled (G37)`. Run the gate.

---

## Leftover greps

Each was run at 23d0dee. After the task named, each prints nothing.

1. **Task 3.** `git grep -nP 'errors\.Is\(err, velox\.ErrNotFound\)' -- contrib/graphql/internal/gogen/testdata example/graphql/gen` prints:

   ```
   contrib/graphql/internal/gogen/testdata/golden/gql_category.go:474
   contrib/graphql/internal/gogen/testdata/golden/gql_node.go:44, :66, :88, :110
   contrib/graphql/internal/gogen/testdata/golden/gql_post.go:896
   example/graphql/gen/gql_node.go:46, :68, :90
   example/graphql/gen/gql_post.go:709
   example/graphql/gen/gql_tag.go:547
   example/graphql/gen/gql_user.go:562
   ```

   The writers are `CG/internal/gogen/mutation.go:1044` and `render.go:688`. `example/{start,tenant,tour}/main.go` use `errors.Is(err, velox.ErrNotFound)` on purpose, as a user would, and are outside this grep.
2. **Task 3.** `git grep -nP '\.In\(k\d+\)\), 1,' -- contrib/graphql/internal/gogen/testdata example/graphql/gen` prints:
   - golden gql_category.go:285, :415;
   - golden gql_like.go:247, :251;
   - golden gql_post.go:574, :800;
   - golden gql_user.go:535;
   - example gql_post.go:480, :643.
3. **Task 5.** `git grep -nP 'OperationTx' -- '*.go'` prints:
   - `CG/veloxgql/extension.go:298`;
   - `CG/veloxgql/extension_test.go:433, :454`;
   - `CG/veloxgql/gqlimpl/mutation.go:29`;
   - `CG/veloxgql/internal/shared/tx.go:13, :15, :19, :21`.
4. **Tasks 2 and 5.** `git grep -nP 'no savepoint|swallows it commits' -- '*.go'` prints:
   - `CG/doc.go:191, :193`;
   - `CG/internal/gogen/mutation.go:390, :737`;
   - golden gql_category.go:260, :375; gql_like.go:216, :218; gql_post.go:522, :524, :719; gql_user.go:452;
   - `CG/veloxgql/gqlimpl/doc.go:7`;
   - example gql_post.go:444, :446, :591; gql_tag.go:343, :345, :453; gql_user.go:403, :405, :500.
5. **Tasks 2 and 9.** `git grep -nP 'written one way, as the core' -- '*.go'` prints:
   - `CG/doc.go:221`;
   - `CG/internal/gogen/mutation.go:390`;
   - golden gql_category.go:261, gql_like.go:219, gql_post.go:525, gql_user.go:453;
   - example gql_post.go:447, gql_tag.go:346, gql_user.go:406.
6. **Task 6.** `git grep -nP 'GeneratedCodeIsVersion2' -- '*.go' ':!oldapi_test.go'` prints:
   - `CG/doc.go:142`;
   - `CG/internal/gogen/render.go:206`, `render_test.go:659`;
   - `CG/veloxgql/api_test.go:58, :187, :188, :193, :196, :201`;
   - `CG/veloxgql/doc.go:6, :184, :188`;
   - `CG/veloxgql/gqlimpl/doc.go:13`;
   - one line in each of the 6 goldens gql_{category,group,like,node,post,user}.go;
   - one line in each of example gql_{node,post,tag,user}.go.
7. **Task 13.** `git grep -nP '@skip and @include are not read|counts, @skip and$' -- '*.go'` prints:
   - `CG/doc.go:320`;
   - `CG/veloxgql/cost.go:24`;
   - `CG/veloxgql/cost_test.go:163`;
   - `CG/veloxgql/doc.go:172`.
8. **Task 11.** `git grep -nP 'a later task binds with @goModel|Plan 2 binds it|application enums bound with @goModel' -- '*.go'` prints:
   - `CG/doc.go:226`;
   - `CG/internal/gogen/render_test.go:277`;
   - `CG/internal/sdl/render.go:492`.
9. **Task 2.** `git grep -nP 'after a COUNT under' -- '*.go'` prints `CG/doc.go:179`.
10. **Task 14.** `git grep -nP 'func ErrorPresenter\(log func\(ctx context\.Context, err error\)\) ' -- '*.go'` prints `CG/veloxgql/presenter.go:116`.

## Rulings this plan makes

1. **(call) Task order.** G49 first, with the goldens unchanged, as the spec says. Then the text-only golden changes (G51, then IsNotFound with G50), so that the semantic tasks' golden diffs are small. Task 4's core fix comes before G45, which makes it reachable. The version bump goes with G47, as the spec says.
2. **(call) G51's copy is pinned** (Task 2, Step 3), and so is the test: at most 80 words, with the pointer, counted on the whitespace-normalised doc. The three godocs are 61 to 70 words, counted on CreateCategory, UpdateCategory and DeletePost. They keep what a caller of the function needs at the call site: what it does, what it returns, and that it runs atomically under the scopes. The rest is in "Mutations".
3. **(call) G44 and G52 are pinned as contracts in CG/doc.go only** (spec rulings 20 and 21). Neither changes code: an M2M add's CheckExists stays non-Writable.
4. **(call) `velox.IsNotFound`** is a root function, as ent's is. Generated code and gqlimpl use it. Users may keep `errors.Is`: the examples' calls stay.
5. **(call) G45's budget test is a new example test**, TestMutationBudget (builder ruling 9), not TestNestedRequestBudget. The statement count is pinned exactly and must not change. The allocation budget is measured +10%, for the pinned gqlgen only, as nestedAllocsBudget is.
6. **(call) G47's check runs after the scoped unlink.** A child still holding the key is one the caller could not change. One Exists, scope-free (builder ruling 12), inside the mutation's savepoint, answers the question. A count before the unlink would need two queries, scoped and unscoped, to say the same.
7. **(call) G48 gathers other entities' non-inverse M2M edges to e**, de-duplicated by join entity and column, in model order. The goldens do not change: their M2M edges are two-sided. The e2e hangs the one-sided edge on a new entity, Badge, so that no existing statement-count test changes (check finding 3).
8. **(call) `RequiredKey` is exported from sdl**, where edgeInputs and the SDL type need it. The model has no flag for it: velox-gen's RequiredEdge is on velox's ColumnDesc, not in plugin.Field. It matches any Required owning edge with a NULL-able key, as velox-gen's marking does (check finding 17).
9. **(call) No WriteError wrap of target-row writes (Task 8).** Controller ruling 6 removes every update input that would set a RequiredKey key to NULL, so no generated statement can raise `missing required edge`. Controller ruling 3 chose the closure over a new name; with nothing left to wrap, neither is built.
10. **(call) G1's kinds.** `orderedID` covers the int types, uint types and string. `ID` covers uuid and enum. A time or bytes key is never a Node (keyBase, surface.go:262-276), so builder ruling 13's "time" and "bytes" are moot. An enum is not in builder ruling 13; its order is a string's, not the enum's declared one, so it gets equality only. An explicit `graphql.WhereOps()` on an ordered key is OpsDefault, which is all eight.
11. **(call) G11's model is `PkgPath + "." + Enum`.** plugin.Field.Enum is the app type's name without its package (plugin.go:146). The app type must implement gqlgen's Marshaler and Unmarshaler, as gogen already says. velox-graphql writes none for it. G12's test is a module of its own (enum_e2e_test.go).
12. **(call) G24 counts a selection whose condition cannot be read.** Cost is an upper bound, and a missing variable is one gqlgen's validation would refuse anyway.
13. **(call) G37's option type is `PresenterOption`, a struct with an unexported func,** as `Option` is. `OnCanceled(nil)` drops cancellations silently. A zero PresenterOption panics.
14. **(call) Task 9 pins the tenant conversion through the e2e env.** `WithTenant(ctx, 7)` is an untyped int, and Writer's org_id is int64. Every mutation e2e test then runs through Wave 2's conversion (the spec's builder ruling 8).
15. **(call) Savepoint names come from a per-transaction counter (Task 4; check finding 12).** The check asked whether naming is per depth: it is (tx.go:370, `db.depth+1`). An application that runs a generated mutation inside its own `tx.Tx` on the operation's DB makes two savepoints of depth 1, both `velox_1`. On MySQL the second replaces the first, so the application's later ROLLBACK TO fails and breaks the transaction. That is a real core bug, reachable once Task 5 lands, so Task 4 fixes it in the core first. No doc sentence asks applications to avoid the pattern.
16. **(call) Foreign keys in the MySQL test DDL are table-level `FOREIGN KEY` clauses** (Tasks 7, 8, 9; check finding 14). MySQL ignores an inline REFERENCES.

## Controller rulings

The controller ruled on the five decisions of 110a775, and on check finding 4.

1. **A Required self edge's `parent` is nullable `T`** when its key is NULL-able (Task 8). The key is NULL-able, so `T!` would be a non-null violation that nulls the enclosing object. A truthful schema beats entgql parity: entgql renders `T!` (ent-contrib entgql/schema.go:744), and so does old velox (graphqlgen/schema_input.go:75). Create keeps `parentID: ID!`.
2. **G1's key predicates go only on existing where inputs** (Task 10). An entity that wants id filters alone gets them with `graphql.WhereOps()` on its key.
3. **WriteError with a closure, and no new LinkError name**, for target-row validation errors. Ruling 6 then leaves nothing to wrap (plan ruling 9).
4. **Deferrals accepted.** G5, G8, G46 and G34 (`TestPredTwiceOnOneContext`, CG/veloxgql/gqlimpl/where_test.go:579) go to Wave 5. R7 and R8's alias note go to Wave 6.
5. **G47's message as written, with `article(typ)`:** `veloxgql: <In>.<edge>ID: the <Owner> is already linked to a or an <Target>`. StillLinked joins gqlimpl/doc.go's list. The Mutations doc says the child may be hidden by the tenant scope or a policy.
6. **Check finding 4: the update inputs of a Required key's other side.**
   - On the side that does not own the key, an update's `<edge>ID` or `clear<Edge>` would first set the old target's Required key to NULL, which velox refuses, as ent's builder check does. So when the other side's key is a RequiredKey, the update input has neither. An O2M also loses `remove<Edge>IDs` and `clear<Edge>`, and keeps `add<Edge>IDs`. Create keeps its inputs.
   - On the side that owns the key, `parentID` on update stays: it sets the row's own key to a new non-null parent, so re-parenting works.
   - Both sides are stated and tested in Task 8.
   - RequiredKey's doc covers any Required owning edge with a NULL-able key, not only self edges.

## Decisions for the controller before execution

None. The five open at 110a775 are ruled above.

## Rulings made during execution

(none yet)

## Left by this plan

- **Wave 5 (tests).** G5, G8, G46, and G34 (`TestPredTwiceOnOneContext`, CG/veloxgql/gqlimpl/where_test.go:579).
- **Wave 6 (docs).**
  - R7.
  - R8's note on the import alias of the annotation package beside gqlgen's `graphql` (spec ruling 19).
  - A RELEASE SAVEPOINT that fails after fn succeeded returns its error with fn's writes still in the transaction (tx.go:393-395). A resolver that swallows that error keeps them (check finding 13). This is core behaviour, to be documented.
  - `maps.Clone` in G6 is shallow: a directive argument that is itself a map stays shared, as the triage's fix accepts (check finding 16).
