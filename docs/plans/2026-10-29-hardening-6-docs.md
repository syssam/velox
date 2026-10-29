# Pre-1.0 Hardening, Wave 6: docs — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0, make the documentation say what the code does, and add the documents a release needs:
- the spec's "Docs fixed as proposed" list: C1, C2, C13, C16, C36, C50, C56, C61, C76, C90; M21; R7, R10. G44 and G52 (rulings 20–21) are already done (contrib/graphql/doc.go:241-245);
- C14 (Like keeps `string`, and the godoc says why; spec ruling 3), R8 (the import alias; spec ruling 19), M9's limits (builder ruling 6), M25 (the README; the LICENSE stays blocked);
- the doc leftovers of Waves 1–5;
- the quality backlog's Wave 6 items: Example functions, docs/decisions, CHANGELOG, CONTRIBUTING, SECURITY, and the versioning and deprecation policy;
- the followups file, every item marked.

**Architecture:** Fifteen tasks, each one commit that passes its check.
- Tasks 1–8 change Go comments, with a pin test for each behaviour a new sentence claims. Task 6 is the one code fix (decision 1). Task 5 changes one message.
- Task 9 corrects the historical specs in place, with dated notes.
- Task 10 adds Example functions to the root, migrate and veloxgql packages.
- Tasks 11–13 add the repository's documents: README, CONTRIBUTING, SECURITY, CHANGELOG, docs/decisions.
- Task 14 marks the followups file. Task 15 is the gate.
- No velox-gen output changes, and no generated-code version moves. handles.go is regenerated in Task 3; its doc comments are its only change.

**Tech Stack:** Go 1.27. golangci-lint v2.14.0. modernc sqlite v1.60.1 for the Examples and the SQLite pin tests. gqlparser v2, as contrib/graphql pins it, for ExampleCost. Docker for PostgreSQL and MySQL ([D] steps only).

**Spec:** `docs/specs/2026-10-25-pre-1.0-hardening-design.md`. Its final block, **"Rulings on the builder check"**, is binding and overrides the rulings above it.
- "Builder ruling N" is item N of that final block. Ruling 6 governs M9: the typed normaliser is deferred whole, and its limits are documented as the one exception to "drift never hides a real difference". Ruling 19 governs R8: the module path `contrib/graphql` stays, and the doc shows the import alias.
- The evidence for each id is in `docs/reviews/2026-10-25-triage-{C,G,M}.md`.
- The quality backlog is the user's 2026-10-27 proposal (Examples, a decisions doc, govulncheck and apidiff; folded into Waves 5–6 and the release step).
- Every file:line below was read at 669f8d2.

**Scope.**
- **This wave:** C1, C2, C13, C14, C16, C36, C50, C56, C61, C76, C90; M9 (limits), M21, M25 (README); R7, R8, R10.
- **Done earlier, recorded here with evidence:**
  - G44 and G52: contrib/graphql/doc.go:241-245 ("A mutation returns the row as written … This is a contract, and will not change").
  - Wave 3's Write-frozen bidi partner and entgql's nullable `T`: contrib/graphql/doc.go:316-319 and the limits list.
  - Wave 4's "PostgreSQL's unique constraint is `Index.Constraint`, SQLite's an unnamed Index": migrate/model.go:65-70.
  - C55 and C51: scope_api.go:155-159 ("privacy rules decide, hooks query"); the root doc's WithTenant conversion (commit b04ac97).
- **Blocked on the user, not touched:** the LICENSE, and README text that depends on it; a push and the first tag. The README holds one marked placeholder for the licence (Task 11), and CONTRIBUTING links to it.

## Global Constraints

- **Shell.** Run `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2` before any go command.
  - The machine is memory-constrained and shared. Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test. Do not kill other processes.
  - If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **The per-task check (C).** Every task ends with it, on its committed tree.
  - Lint: `go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` in every module the task touches (root, `example`, `contrib/veloxotel`, `contrib/graphql`).
  - Tests: `go test -race -count=1 <the touched packages>`. This includes their Examples and these doc tests, which a task's comment change can break:
    - `TestGodocCitesNoSpec` (godoc_test.go:25, root, schema, plugin, migrate and cmd/velox-gen, tests included; contrib/graphql/godoc_test.go:24; contrib/veloxotel/godoc_test.go:23). It refuses a comment that cites `docs/specs`, `§`, "Ruling N", "GraphQL plan 3" and the like. **No comment written by this plan names a spec, a plan, a ruling or a wave.**
    - `TestGeneratedOnlyAPIIsMarked` (godoc_test.go:82) and `TestExportedMethodsHaveDocs` (:171).
    - `TestSentinelDocs` (errors_test.go:46).
    - `TestBuildersAreDocumented` (schema/edge/edge_test.go:81, schema/field/field_test.go:280).
    - `TestHandlesFileIsCurrent` (handles_test.go:88): handles.go must equal `go generate .`'s output.
    - `TestMutationGodocPoints` (contrib/graphql/internal/gogen/render_test.go:1483) and `TestGeneratePackageDoc` (cmd/velox-gen/generate_test.go:254), `TestRenderPackageDoc` (internal/gen/render_test.go:246): the generated godoc. No task here changes it; they must still pass.
  - G (CI's "generated files are up to date", verbatim in docs/plans/2026-10-27-hardening-3-graphql.md, "The generated check (G)") runs only where a task names it, and in the gate.
  - The full gate, `bash scripts/ci-local.sh`, runs once, in Task 15.
  - On a failure, fix the cause, stage by name, `git commit --amend --no-edit`, then run C again.
- **Commits.**
  - The message is `Area: what changed (ids)`. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage files by name with `git add <paths>`, never with `git add -A` or `.`, and never stage `.DS_Store`.
  - `git status --short` lists what a task changed, and every path in it but `.DS_Store` is staged.
  - Never push.
- **Doc style.** Short declarative sentences, as in migrate/doc.go. Wrap Go comments at the file's width (about 78 columns). One claim per sentence, and each claim has its evidence below. The quoted copy in each step is this plan's decision: write it as given, rewrapped. Small wording changes for grammar are fine; a change of claim is not.
- **Every behaviour claim needs evidence from a run:** a test (an existing one, named, or a pin test the task adds), a scratch program, or a grep. A sentence whose evidence fails is a finding: stop and report it with the output. Do not soften the sentence to pass.
- **Greps.** Apple's git greps with BSD regex, where `-E` never matches `\b`. Every grep here is `git grep -P`.
- **Test names.** Before adding a test function, run `git grep -nP 'func <Name>\(' -- '*.go'`. Expected: nothing. Every new name below was checked at 669f8d2.
- **[D] values** need PostgreSQL or MySQL. Docker was not run while planning. The implementer runs that step under D (below), and records the result in the commit message. If it differs from the plan's expectation, stop and report; do not write the sentence.
- **Never publish the user's email address**, in SECURITY.md or anywhere else.
- **Compile cost.** Tasks 1–4 touch the root package's non-test files: comments, and two unexported renames. compilecost_test.go's bounds must hold unchanged; a change in them is a finding.
- **How the values were found.** Every "today" value below came from a run at 669f8d2: a throwaway `zz*_test.go` in the package, deleted after its run, a scratch schema package under `internal/zzscratch/` run through a velox-gen built from the tree, deleted after, or a grep.

### D: the databases

Never run D while a gate runs: both use the compose project `velox2-ci`.

```bash
docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# … the step's command …
docker compose -f docker-compose.ci.yml -p velox2-ci down -v
```

The compose file runs PostgreSQL 18 and MySQL 8.4.

## Review Focus

1. **A doc sentence true on SQLite and false on another database.** A user reads EqualFold's or migrate's doc and expects it on their server. Pin: Task 3 `TestEqualFoldIsASCIIOnSQLite` claims SQLite only; Task 8's PostgreSQL and MySQL sentences each have a [D] run recorded in the commit.
2. **An Example whose output differs from run to run.** A user copies it and it fails at random, or CI flakes on IDs, times or map order. Pin: Task 10 runs `go test -run '^Example' -count=3` in each package.
3. **A README or CONTRIBUTING command that does not run as written.** A new contributor pastes it. Pin: Task 11 and Task 12 run every command block that needs no Docker and no push, and record the result.
4. **A velox-gen error that prints an absolute path.** A user's CI log then holds the build machine's paths, and the message differs between machines. Pin: Task 6 `TestMigrateSQLPositionsAreRelative`, for a refusal whose positions sit inside its reason.
5. **A followups item marked resolved by a commit that does not exist.** A reader follows the hash and finds nothing. Pin: Task 14 Step 4 checks every hash with `git cat-file -e` and that the counts add up to 220.

---

### Task 1: Unexported names and wraps (C16, C36, C50)

**Files:**
- Modify: `render_cache.go:109` (`func flag(b bool) byte`), its calls at :119 (five), :131, :140, :162, :199.
- Modify: `pred.go:11-12`, `guard.go:130`, `scope.go:405`, `pred.go:150`.
- Modify: `scope.go:89-96` (scopeInsert's doc), `scope.go:620-623` (node's doc), `insert_scope_test.go:208`.

**Interfaces:** None.

- [ ] **Step 1: C16.** Rename `flag` to `bit` in render_cache.go, with every call. Its doc comment keeps its meaning.
- [ ] **Step 2: C36.** In pred.go's const block, name the zero op: `opZero op = iota // the zero Pred[E]{}: no condition was given` before `opEQ`, and `opEQ` loses its `+ 1`, so every other value is unchanged. Use it at guard.go:130 (`n.op == opZero`), scope.go:405 (`p.n.op == opZero`) and pred.go:150 (`case opZero, opInvalid, opExists:`).
- [ ] **Step 3: C50.** Rewrap scopeInsert's doc (scope.go:91 runs to 127 columns) and node's doc (scope.go:621-622 breaks after "and"). Replace the comment at insert_scope_test.go:208, "A policy that denies denies the upsert, as an update.", with "A policy that denies the insert refuses the upsert: the Insert policy runs before the update branch's conditions." (The assertion on :210 is `wantPolicyError(t, err, velox.OpInsert)`.)
- [ ] **Step 4: Run** leftover greps 1 and 2. Expected: nothing.
- [ ] **Step 5: Run C** (root). Commit: `velox: bit and opZero name the cache key's flags and the zero predicate; doc wraps (C16, C36, C50)`.

---

### Task 2: Statements, hooks, transactions and routing (C1, C61, C90; Wave 2's WithShardFor, Wave 3's RELEASE)

**Files:**
- Modify: `statement.go:145-148` (Statement.Entity's paragraph), `statement.go:242-247` (Rewrite), `statement.go:250-268` (Around).
- Modify: `callers.go:14-15` (Caller), `callers.go:22-26` (WithCallers).
- Modify: `route.go:147-152` (WithShardFor).
- Modify: `tx.go:94-97` (the nested Tx paragraph).
- Test: `hook_test.go`, `shard_test.go`, `tx_test.go`.

**Interfaces:** None.

- [ ] **Step 1: Write the pin tests.** Each is expected to PASS today.
  - `TestRewriteLeavesExistsTables` (hook_test.go): a Rewrite hook that sets `st.Entity` to a copy of itself with `Table: "t_" + st.Entity.Table`. `db.Query[testmodel.Invoice]().Where(I.Items.Has()).Limit(1).SQL(ctx)` on PostgreSQL contains `FROM "t_invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1"`. (Measured: the outer table moves, the EXISTS still reads `items`.)
  - `TestShardForCallsPerWrite` (shard_test.go, with `openShards(false)`, tenant `int64(1)`). Reset `s.lookups` and `s.euP.Calls` between the cases:
    - `Insert` of one `testmodel.Invoice` (`s.euP.Rows = [][]any{{int64(1)}}`): 1 lookup, 1 statement.
    - `InsertAll` of 8000 invoices (each with `Data: []byte{}`): 3 lookups, 2 statements, nil error. The split runs in a transaction, which looks up once, and each statement once.
    - `Tx` whose fn inserts one invoice: 2 lookups, 1 statement.
  - `TestNestedTxReleaseFails` (tx_test.go): a FakeExec whose `Errs` fails the `RELEASE SAVEPOINT` statement with `errors.New("release failed")`. An outer `Tx` runs an UPDATE in a nested `Tx`, and returns nil after the nested `Tx` returned its error. The nested `Tx`'s error is `velox: release savepoint: release failed`. The outer commits. No `ROLLBACK TO SAVEPOINT` is sent, so the UPDATE stays in the transaction (assert on the FakeExec's recorded SQL).
- [ ] **Step 2: Run** `go test -count=1 -run 'TestRewriteLeavesExistsTables|TestShardForCallsPerWrite|TestNestedTxReleaseFails' .` Expected: PASS.
- [ ] **Step 3: Write the docs.**
  - **C1, on Rewrite**, after its first paragraph: "A hook that points st.Entity at another table changes the statement's own table only: the tables in an EXISTS that Has or HasWhere adds keep their names. To scope those by tenant, use the target's policy: its Read is ANDed into every Has and HasWhere subquery." Evidence: `TestRewriteLeavesExistsTables`; Policy.Read's doc (scope_api.go:162-164).
  - **C1, on Statement.Entity's paragraph** (:145-148), one sentence: "Pointing it at another table does not change the tables inside an EXISTS (see Rewrite)."
  - **C90, on Around**, a sentence after "A hook must call next…": "A hook must not assign to *st after next returns: velox records in st where the statement ran, and the query's With loads read from there." Evidence: grep `git grep -nP 'a\.st\.ran' -- query.go` prints query.go:408 and :412; each.go:227 reads `c.ran`.
  - **C61, on Caller**: "Caller is zero when none of the 64 frames above velox's call is outside velox and the packages WithCallerSkip names." Evidence: `TestCaptureReadsAtMost64Frames` (callers_internal_test.go:163: the 64th frame is found, the 65th gives `Caller{}`).
  - **WithShardFor** (route.go:149-150), after "for each statement and Tx whose context has a tenant and no WithShard": "One write can call it more than once: an InsertAll split into two statements calls it three times, for its transaction and for each statement, and a Tx calls it when it begins and for each statement in it." Evidence: `TestShardForCallsPerWrite`.
  - **The nested Tx** (tx.go:94-97), after "rolls back only that fn's work": "When releasing the savepoint fails, the nested Tx returns that error and does not roll back to the savepoint: its fn's work stays in the transaction, which commits unless an enclosing fn returns an error." Evidence: `TestNestedTxReleaseFails`.
- [ ] **Step 4: Run C** (root). Commit: `velox: Rewrite, Around, Caller, WithShardFor and the nested Tx say what they do (C1, C61, C90)`.

---

### Task 3: Reads, policies and handles (C2, C13's EqualFold, C14, C56)

**Files:**
- Modify: `relation.go:25` (One).
- Modify: `scope_api.go:165-170` (Policy.Write).
- Modify: `internal/handlegen/handlegen.go:43` (Like), `:51` (EqualFold). Regenerate `handles.go` with `go generate .` (its lines :212, :232, :529, :549 change).
- Test: `policy_test.go`, `handles_test.go`.

**Interfaces:** None.

- [ ] **Step 1: Write the pin tests.** Each is expected to PASS today. Both run on SQLite through `velox.Open(velox.SQLite, db)` over a temp-file database; the DDL is `velox-gen migrate sql -dialect sqlite ./internal/testmodel/schema`'s tables for invoices and tickets (build velox-gen from the tree first: `go build -o bin/velox-gen ./cmd/velox-gen`).
  - `TestWritePolicyFiltersToZeroRows` (policy_test.go): a ticket owned by `bob` (inserted under `velox.WithoutPolicy(…, "seed")`). As `testmodel.WithUser(ctx, "ann")`, `db.Update[testmodel.Ticket]().Set(Tk.Title.Set("x")).Where(Tk.ID.EQ(id)).Exec` returns `0, nil`, and `db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(id)).Exec` returns `0, nil`. Under WithoutPolicy the row is still there with its title. (Measured with Ticket's Write policy: 0 rows, nil error.)
  - `TestEqualFoldIsASCIIOnSQLite` (handles_test.go): an invoice numbered `ÉCOLE`. `Where(I.Number.EqualFold("ÉcOLE")).Count` is 1, and `Where(I.Number.EqualFold("école")).Count` is 0. (Measured: SQLite's `LOWER('ÉCOLE')` is `École`.)
- [ ] **Step 2: Run** `go test -count=1 -run 'TestWritePolicyFiltersToZeroRows|TestEqualFoldIsASCIIOnSQLite' .` Expected: PASS.
- [ ] **Step 3: Write the docs.**
  - **C2, on One**: "Every to-one edge velox-gen writes matches at most one row: its key is the target's primary key, or, for a one-to-one edge, a unique column. A loaded One holds that row, or nil when there is none." Evidence: `TestOneToOneKeyIsUnique` (internal/gen/build_test.go:2227); `TestWithAttachesToOwners` (load_test.go:29).
  - **C56, on Policy.Write**: "A row Write hides is left alone, and that is not an error: an Update or Delete that matches only such rows affects 0 rows, as with SQL row-level security." Evidence: `TestWritePolicyFiltersToZeroRows`.
  - **C13, EqualFold** (handlegen.go:51): "is LOWER(column) = LOWER(s). SQLite's LOWER folds ASCII letters only, so on SQLite it ignores the case of those alone." Evidence: `TestEqualFoldIsASCIIOnSQLite`.
  - **C14, Like** (handlegen.go:43), appended to its doc: "It takes a string, not the column's type, because a pattern is not a value of the column." Spec ruling 3.
- [ ] **Step 4: Regenerate** with `go generate .`, then `git diff --stat handles.go`. Expected: only doc-comment lines of Like and EqualFold, on both handle types.
- [ ] **Step 5: Run C** (root; `TestHandlesFileIsCurrent` is in it). Commit: `velox: One, Policy.Write, EqualFold and Like say what they match (C2, C13, C14, C56)`.

---

### Task 4: Writes (Wave 2's bidi paragraph and EdgeBidi wording, Wave 5's upsert, edge.Required)

**Files:**
- Modify: `doc.go:102` ("# Writes", before "The tenant scope and the entity's policy apply…").
- Modify: `desc.go:463`, `desc.go:473` (EdgeBidi).
- Modify: `insert.go:52-56` (OnConflict).
- Modify: `schema/field/builders.go:329-331` (TimeBuilder.UpdateDefault), `schema/field/field.go:161-162` (typed.UpdateDefault).
- Modify: `schema/edge/edge.go:117`, `:182` (Required on AssocBuilder and InverseBuilder).
- Test: `internal/gen/build_test.go` (after `TestRequiredSelfKeyIsNullable`, :2316).

**Interfaces:** None.

- [ ] **Step 1: Write the pin test.** `TestRequiredSelfBoundKeyIsNotNull`: `reqSelfBound`, an edge `edge.To("children", reqSelfBound.Type).From("parent").Unique().Required().Field("parent_id")` with `field.Int64("parent_id")` (not Optional, not Nillable). On every dialect (`allDialects`) the `parent_id` column is NOT NULL. Expected: PASS today (measured with a scratch schema: the bound key keeps NOT NULL).
- [ ] **Step 2: Run** `go test -count=1 -run 'TestRequiredSelf' ./internal/gen`. Expected: PASS.
- [ ] **Step 3: Write the docs.**
  - **doc.go, # Writes**, a paragraph before its last sentence: "A self-referencing edge with no inverse is bidirectional, as in ent. A link writes both directions: both join rows of a many-to-many edge, or both rows' keys of a one-to-one edge. An unlink removes both. Inserting a join row fails when either row is there already; OnConflict(…).DoNothing() is the idempotent insert." Evidence: `TestBidiLinks` (bidi_test.go:152), `TestBidiOne` (:337); Desc.Bidi's doc (desc.go:28-39).
  - **desc.go:463**: "so a partner it cannot see cannot be linked" becomes "so a partner it may not change cannot be linked". **desc.go:473**: "or one the caller cannot see" becomes "or one the caller may not change". Evidence: the partner's link is an `OpUpdate` statement scoped as a write (bidi.go:360), so the Read and Write policies apply (Policy.Write's doc, scope_api.go:165-166); contrib/graphql/doc.go:316-319 says the same of the GraphQL path.
  - **OnConflict** (insert.go:52-56), rewrapped (:55 runs past the width), with: "An upsert's update branch writes only the columns Update names: an UpdateDefault, such as an updated_at column's, does not fire, as in ent." Evidence: `TestUpsertUpdateBranchReadBack` (internal/itest/writes_test.go:283, on all four targets; Wave 5's controller ruling 2), and Task 10's `ExampleInsertQuery_OnConflict`.
  - **Both UpdateDefault docs**: "An upsert's update branch does not call it." Same evidence.
  - **edge.Required** (:117 and :182): "Required makes the edge's foreign key NOT NULL. An edge to its own schema is the exception, as in ent: its implicit key stays NULL-able, with ON DELETE SET NULL. A key bound to a Field that is not Optional stays NOT NULL." Evidence: `TestRequiredSelfKeyIsNullable` (internal/gen/build_test.go:2316), `TestRequiredSelfBoundKeyIsNotNull`, `TestLowerRequiredEdge` (internal/gen/lower_test.go:1245, a non-self edge).
- [ ] **Step 4: Run** leftover greps 3 and 8. Expected: nothing.
- [ ] **Step 5: Run C** (root, `./schema/...`, `./internal/gen`). Commit: `velox: bidi edges, upserts and Required keys documented as they behave`.

---

### Task 5: schema, plugin and velox-gen (C13's Snake, C76, R10)

**Files:**
- Modify: `plugin/names.go:50-57` (Snake).
- Modify: `cmd/velox-gen/main.go:3-4` (package doc), `schema/schema.go:51-52` (Schema), `schema/schema.go:5-7` (Annotation).
- Modify: the "no schemas" message, `cmd/velox-gen/generate.go:81` and `internal/gen/load.go:84`, and its pins (`cmd/velox-gen/generate_test.go:450-456`, `:580`; `git grep -nP 'a schema is a struct that embeds schema\.Schema' -- '*_test.go'` lists them all).
- Test: `plugin/names_test.go`, `cmd/velox-gen/generate_test.go`.

**Interfaces:** None.

- [ ] **Step 1: Write the pin tests.** Each is expected to PASS today.
  - `TestSnakeIsByteWise` (plugin/names_test.go): `Snake("CaféBar") == "caf_ã©bar"` and `Snake("Ünit") == "ã\u009cnit"`. (Measured. A schema named `Café` gets the table `caf_ã©s`.)
  - `TestDescribeSkipsIndirectSchema` (cmd/velox-gen/generate_test.go; skip under `-short`, it builds a module): `tempModule` with `type Base struct{ schema.Schema }`, `type Café struct{ Base }` and `type Order struct{ Base }`. `velox-gen describe` lists `Base` and no `Order`, exit 0. (Measured: the struct that embeds Base is skipped without a message.)
- [ ] **Step 2: Run** them. Expected: PASS.
- [ ] **Step 3: Write the docs.**
  - **Snake**, after "ported exactly": "Like ent's, it works on bytes, so it is for ASCII identifiers: a letter outside ASCII comes out mangled (CaféBar gives caf_ã©bar)." Evidence: `TestSnakeIsByteWise`. Spec C13: the output is frozen; document only (decision 2).
  - **velox-gen's package doc** (main.go:3): "a package whose structs embed schema.Schema" becomes "a package whose structs embed schema.Schema directly: a struct that embeds another struct that embeds it is skipped". **schema.Schema**: "Schema is embedded, directly, by every schema; velox-gen skips a struct that embeds it through another struct." Evidence: `TestDescribeSkipsIndirectSchema`.
  - **The message** gains "directly": `no schemas in %s: a schema is a struct that embeds schema.Schema directly`. Update every pin the grep lists.
  - **R10, on Annotation**: "Two annotations of one name on one element merge when the later implements Merger; otherwise the later replaces the earlier, without a message. A third-party annotation should take a name no other package uses, such as one that starts with its module path." Evidence: `TestAnnotationsMerge` (schema/load/marshal_test.go:217: the mixin's `Table("a")` is replaced by the schema's `Table("b")`, and the Merger's values add up); mergeAnnotations (schema/load/marshal.go:228-244).
- [ ] **Step 4: Run C** (root, which holds `./plugin`, `./schema/...`, `./cmd/velox-gen`, `./internal/gen`). Commit: `velox-gen: Snake's ASCII limit, direct embedding and annotation names documented (C13, C76, R10)`.

---

### Task 6: velox-gen prints positions inside a reason relative to the working directory (decision 1)

**Files:**
- Modify: `cmd/velox-gen/main.go:459-495` (`relErrors`, `relPos`).
- Test: `cmd/velox-gen/migrate_sql_test.go` (after `TestMigrateSQLRefusals`, :106).

**Interfaces:** None.

Today two refusals print the build machine's absolute paths, against the spec's promise of paths relative to the working directory (migrate spec, the refusal block at :1086-1094). Measured with a scratch schema package (`velox-gen migrate sql`, run from the module root):

```
velox-gen: /Users/…/internal/zzscratch/schema/post.go: schema Post: MySQL rows hold at most 65535 bytes; these need 80012:
  /Users/…/internal/zzscratch/schema/post.go:12: field "body": varchar(10000), 40002 bytes
  /Users/…/internal/zzscratch/schema/post.go:13: field "summary": varchar(10000), 40002 bytes
  the other columns and the NULL bitmap: 8 bytes
  use Text() or a smaller MaxLen
velox-gen: schema/order.go:12: schema Order: field "status": enum type order_status is also field "state" of schema Invoice (/Users/…/internal/zzscratch/schema/invoice.go:11), with other values
```

The row-limit's `Pos` is a file with no `:line` (internal/gen/tables_check.go:342), which relPos returns as is (main.go:485), and both messages carry positions inside `Reason`, which relErrors does not read.

- [ ] **Step 1: Write the failing test.** `TestMigrateSQLPositionsAreRelative` (skip under `-short`):
  - `tempModule` with `schema/post.go`: a `Post` with `field.String("body").MaxLen(10000)` and `field.String("summary").MaxLen(10000)`, one per line. `velox migrate sql -dialect mysql` exits 1, stdout empty, and stderr is exactly the row-limit block above with `schema/post.go` for each path (lines from the file as written).
  - `tempModule` with `schema/order.go` (`Order`, `field.Enum("status").Values("new", "paid").SchemaType(map[string]string{field.Postgres: "order_status"})`) and `schema/invoice.go` (`Invoice`, `field.Enum("state").Values("paid", "new")` with the same SchemaType). `velox migrate sql -dialect postgres` exits 1, and stderr holds `(schema/invoice.go:<line>), with other values`.
  - In both, `!strings.Contains(errs, dir)`.
- [ ] **Step 2: Run it.** Expected: FAIL, with the absolute paths above.
- [ ] **Step 3: Implement** in main.go.
  - `relPos` makes an absolute position without `:line` relative too: a `Pos` whose suffix after the last `:` is not all digits is a file, made relative as a whole (keep the EvalSymlinks and the inside-dir rule).
  - `relErrors` also rewrites `Reason`: every occurrence of the working directory as a path prefix (`dir + string(filepath.Separator)`, and the same for `filepath.EvalSymlinks(dir)` when it differs) is removed. A file outside dir keeps its absolute path, as relPos does.
  - internal/gen is unchanged: `TestTablesRowLimit` (internal/gen/tables_test.go:1245) and `TestSharedEnumValuesPos` (internal/gen/build_test.go:2161) still expect gen's absolute positions.
- [ ] **Step 4: Run** `go test -count=1 -run 'TestMigrateSQLPositionsAreRelative|TestRuleErrorHasPos|TestMigrateSQLRefusals' ./cmd/velox-gen`. Expected: PASS.
- [ ] **Step 5: Run C** (root). Commit: `velox-gen: positions inside a refusal's reason print relative to the working directory`.

If the controller defers decision 1, skip this task, and Task 9 keeps the spec's refusal block with a dated note that these two still print absolute paths.

---

### Task 7: contrib/graphql (R7, R8, Wave 5's ClearProfile, Wave 3's G6)

**Files:**
- Modify: `contrib/graphql/annotation.go:214` (Type), `:74-75` (Directive), `:291-293` (Directives).
- Modify: `contrib/graphql/doc.go:1-8` (the alias), after `:239` (the clear), and the sentence that lists Type (find it with `git grep -nP 'graphql\.Type|\bType\(' -- contrib/graphql/doc.go`; if none, after the short schema at :30).
- Test: `contrib/graphql/annotation_test.go` (after `TestMergeClonesArgs`, :353).

**Interfaces:** None.

- [ ] **Step 1: Write the pin test.** `TestDirectivesCopyArgsOneLevel`: `args := map[string]any{"x": map[string]any{"y": 1}}`, `a := graphql.Directives(graphql.Directive{Name: "d", Args: args})`. Then `args["z"] = 2` leaves `a.Directives[0].Args` without `"z"`, and `args["x"].(map[string]any)["y"] = 3` shows through: `a.Directives[0].Args["x"].(map[string]any)["y"] == 3`. Expected: PASS today (measured: one level copied, a nested map shared).
- [ ] **Step 2: Write the docs.**
  - **R7, on Type**: "An ID and a cursor hold the type's name, so renaming the type changes every ID and cursor that clients hold. DecodeID refuses an ID of the old name with ErrWrongIDType." Evidence: veloxgql/doc.go:11-13 ("base64url … of `<Type>:<key>`"); `Cursor.Type` and EncodeCursor (veloxgql/cursor.go:61-67); Task 10's `ExampleDecodeID`. The same two sentences go in doc.go where it lists Type.
  - **R8**, after doc.go's first paragraph: "A file that also imports gqlgen's graphql package names this one: import vgraphql "github.com/syssam/velox2/contrib/graphql"." (Decision 3.) Evidence: no file in the repo imports both (`git grep -lP '"github.com/99designs/gqlgen/graphql"' -- '*.go'` and the annotation package's importers are disjoint); a schema package never imports gqlgen (doc.go:2-4).
  - **ClearProfile**, after the re-point paragraph (:233-239): "Clearing such an edge, when its current child is one the caller may not change, is a silent no-op, not an error: an error would tell the caller that a child it may not change is linked there." Evidence: `TestWriteHiddenProfile` (contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go:1091; its doc says the same; Wave 5's controller ruling 1).
  - **G6, on Directive and Directives**: "Directives, and Merge, copy each Args map but not the values in it: a nested map or slice stays shared with the caller's, so do not change it after." Evidence: `TestDirectivesCopyArgsOneLevel`; annotation.go:169 and :297 (`maps.Clone`).
- [ ] **Step 3: Run C** (contrib/graphql: `go test -race -count=1 . ./veloxgql/... ./internal/...`, and `./cmd/velox-graphql -run 'TestWriteHiddenProfile'`, which builds a module). Commit: `contrib/graphql: renaming a type, the import alias, a hidden clear and shared directive args documented (R7, R8)`.

---

### Task 8: migrate (M9, M21, Wave 4's [D] limits, the Atlas dev image)

**Files:**
- Modify: `migrate/doc.go:95-99` (M9), `:113-119` and `:169-177` (M21, nested domains), `:146-148` (`"$user"`), after `:203` (a referenced unique), `:220`, `:226` (Atlas).
- Modify: `migrate/baseline.go:23-26` (M21, WITHOUT OVERLAPS).
- Test: `migrate/predicate_test.go` (package `migrate`).

**Interfaces:** None.

- [ ] **Step 1: Write the pin test.** `TestSamePredicateKnownLimits`: on `velox.Postgres`, `samePredicate(d, a, b)` returns `same == true, parsed == true` for `d >= '2020-01-01 10:00'::timestamp` against `d >= '2020-01-01 10:00'`, and for `qty::text = '05'` against `qty = '05'`; and `same == false` for `d < now() - '1 day'::interval` against `d < now() - '1 day'`. Expected: PASS today (measured: true true, true true, false). Its comment names it the documented exception.
- [ ] **Step 2: [D] scratch runs.** Each is a throwaway `zz_test.go` in package migrate, with `freshDB` (migrate/dbs_test.go:23), deleted after. Record each result in the commit message. Expected values are the Wave 4 execution's observations; a different result is a finding, and its sentence is not written.
  - **M21 (MySQL).** `CREATE TABLE users (id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY, name text NOT NULL) AUTO_INCREMENT=1000`; `Baseline` with a matching `want` returns no Warning that names AUTO_INCREMENT or 1000. `Verify`'s fresh database, after one insert, gives id 1. [D]
  - **WITHOUT OVERLAPS (PostgreSQL 18).** `CREATE EXTENSION IF NOT EXISTS btree_gist; CREATE TABLE r (id int NOT NULL, during tstzrange NOT NULL, UNIQUE (id, during WITHOUT OVERLAPS))`; `Inspect` reads it as a unique index with a note that names its method (gist), and `Baseline`'s snapshot renders it as a plain btree `CREATE UNIQUE INDEX`. [D]
  - **`"$user"` (PostgreSQL).** With the default search_path, a first `Up` whose file runs `CREATE SCHEMA velox` (the DSN's user is `velox`) records its history in `public`; a second `Status` then reads `current_schema()` = `velox`, finds no history there, and reports every file pending. [D]
  - **A referenced unique (PostgreSQL).** A table with `Index.Constraint` unique on `email`, and another table's foreign key to it. A plan that drops the constraint fails on `Up` with SQLSTATE 2BP01 (`cannot drop constraint … because other objects depend on it`). [D]
  - The nested-domain note is already pinned by `TestInspectPostgresNestedDomain` (migrate/inspect_postgres_test.go:681) [D]; run it under D.
- [ ] **Step 3: Write the docs.**
  - **M9**, after "One whose meaning differs is a change, so a hand-edited check is found.": "The exception is a cast whose meaning depends on the column's type, which the comparison does not know: on PostgreSQL, d >= '2020-01-01 10:00'::timestamp matches d >= '2020-01-01 10:00' on a date column, and qty::text = '05' matches qty = '05', though the database reads each pair apart." Evidence: `TestSamePredicateKnownLimits`; predicate.go:35-39.
  - **M21**, in Baseline's warnings list (baseline.go:23-26) and both doc.go lists (:113-119, step 1 at :171-174): "MySQL's AUTO_INCREMENT counter is not read, and gets no warning: a database built from 0001_baseline.sql starts each table's IDs at 1." Evidence: inspect_mysql.go:174 reads only the `auto_increment` word (`c.Identity = has("auto_increment")`); the [D] run.
  - **Nested domains**, in step 1's list (doc.go:173): "a PostgreSQL domain type, which it does not create (for a domain over another, the nearest)". Evidence: `TestInspectPostgresNestedDomain` [D].
  - **WITHOUT OVERLAPS**, in Baseline's doc: "A PostgreSQL 18 UNIQUE … WITHOUT OVERLAPS constraint is read with a note, and 0001_baseline.sql builds it as a plain unique index, without the overlap rule." Evidence: the [D] run.
  - **`"$user"`**, after doc.go:148: "On PostgreSQL that schema is current_schema(). Under the default search_path, a file that creates a schema named after the session's user moves it, and the next run looks for the history there: name the schema in the DSN's search_path." Evidence: the [D] run.
  - **A referenced unique**, a paragraph after :203: "A unique constraint or index that a foreign key references cannot be dropped alone: PostgreSQL refuses, and the file fails. Drop or change the foreign key in the same migration." Evidence: the [D] run.
  - **Atlas** (doc.go:220, :226): `docker://postgres/16/dev` becomes `docker://postgres/18/dev`, the version CI and docker-compose.ci.yml run.
- [ ] **Step 4: Run C** (root; `./migrate` under D as well, for `TestInspectPostgresNestedDomain`). Commit: `migrate: M9's limits, AUTO_INCREMENT, WITHOUT OVERLAPS, "$user" and referenced uniques documented (M9, M21)`, with the [D] results in the body.

---

### Task 9: The historical specs, corrected in place (Waves 1, 4 and 5)

**Files:**
- Modify: `docs/specs/2026-10-23-migrate-design.md`: :182, :294-295, :420-456, :467, :496-510, :544, :1086-1094, :1144, :1219-1223, :1519, :1532.
- Modify: `docs/specs/2026-10-20-api-cleanup-design.md:188`.
- Modify: `docs/specs/2026-10-19-graphql-design.md:314`.
- Modify: `docs/specs/2026-10-14-core-design.md:852` and a note after the scale notes (after "Note, 2026-10-22: the LIKE escape is `!`").

**Interfaces:** None.

Each corrected passage ends with `*(Updated 2026-10-29 by hardening wave 6: <what changed, and the commit>.)*`, the repository's convention (api-cleanup :188 has one). The old text is replaced, not struck through (decision 9).

- [ ] **Step 1: The derived names** (migrate spec :182, :294-295; api-cleanup :188). A unique field's index is `<table>_<col>_key` on PostgreSQL and SQLite, and the column's name on MySQL; a declared unique index has no `_unique` suffix. The note cites 25e9e88 (ent's index, unique and foreign-key names). Evidence: the CREATE output below (`users_email_key`, `` `email` ``).
- [ ] **Step 2: The CREATE and rename examples** (:420-456, :496-510). Replace them with today's output for the spec's schema: an `orgs` table, and `users` with `org_id` (a required edge from orgs, ON DELETE CASCADE), a unique `email` and an enum `role` (`viewer`, `admin`, default `viewer`, comment `c`). Measured with `velox-gen migrate diff`:

```sql
-- PostgreSQL
CREATE TYPE "users_role" AS ENUM ('viewer', 'admin');
CREATE TABLE "users" (
  "id" bigint GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME "users_id_seq") NOT NULL,
  "org_id" bigint NOT NULL,
  "email" text NOT NULL,
  "role" "users_role" NOT NULL DEFAULT 'viewer',
  CONSTRAINT "users_pkey" PRIMARY KEY ("id"),
  CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id") ON DELETE CASCADE
);
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
CREATE INDEX "users_org_id" ON "users" ("org_id");
COMMENT ON COLUMN "users"."role" IS 'c';

-- MySQL
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `org_id` bigint NOT NULL,
  `email` varchar(255) NOT NULL,
  `role` enum('viewer','admin') NOT NULL DEFAULT 'viewer' COMMENT 'c',
  PRIMARY KEY (`id`),
  UNIQUE INDEX `email` (`email`),
  INDEX `users_orgs_users` (`org_id`),
  CONSTRAINT `users_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- SQLite
CREATE TABLE "users" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "org_id" integer NOT NULL,
  "email" text NOT NULL,
  "role" text NOT NULL DEFAULT 'viewer',
  CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id") ON DELETE CASCADE,
  CONSTRAINT "users_role_check" CHECK ("role" IN ('viewer', 'admin'))
);
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
CREATE INDEX "users_org_id" ON "users" ("org_id");
```

  Renaming `users` to `accounts` (`-rename-table users=accounts`):

```sql
-- PostgreSQL
ALTER TABLE "users" RENAME TO "accounts";
ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq";
ALTER INDEX "users_email_key" RENAME TO "accounts_email_key";
ALTER INDEX "users_org_id" RENAME TO "accounts_org_id";
ALTER TABLE "accounts" RENAME CONSTRAINT "users_orgs_users" TO "accounts_orgs_users";
ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey";
ALTER TYPE "users_role" RENAME TO "accounts_role";

-- MySQL (the unique index is named by its column, so it keeps its name)
ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;
RENAME TABLE `users` TO `accounts`;
ALTER TABLE `accounts` RENAME INDEX `users_orgs_users` TO `accounts_orgs_users`;
ALTER TABLE `accounts` ADD CONSTRAINT `accounts_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`) ON DELETE CASCADE;
```

  SQLite renames the table, then rebuilds it to rename the check and the foreign key; say so in one sentence rather than print the rebuild. Drop the `-- review:` lines; keep the spec's sentence that names ruling 49.
- [ ] **Step 3: :467**, "never a `UNIQUE` constraint", gains: a PostgreSQL index with `Index.Constraint` is a UNIQUE constraint (Wave 4, migrate/model.go:65-70).
- [ ] **Step 4: :544** says PostgreSQL 12 and later prove NOT NULL from a valid check; add that every supported version (14 and up) does. **:1144** "PostgreSQL 16" becomes "PostgreSQL 14 and 18 (CI's matrix; ci-local.sh runs 18, and 14 with VELOX_CI_MATRIX=1), MySQL 8.4" (ci-local.sh:3; builder ruling 16).
- [ ] **Step 5: :1519.** A `MaxLen` above 16381 is `text` on MySQL (d139204), so `varchar(16382)` is never written; the row limit refuses a table whose columns sum past 65535 bytes, naming each varchar and varbinary. Evidence: a scratch schema gave `varchar(16381)` for `MaxLen(16381)` and `text` for `MaxLen(16382)`; `TestTablesRowLimit`.
- [ ] **Step 6: The refusal block, :1086-1094**, gets today's messages, with Task 6's relative paths:
  - rule 44: `schema/user.go:13: schema User: field "email": name "users_email_key" is also schema Account's index 2; give index 2 another StorageKey` (measured);
  - rule 52: `… enum type order_status is also field "state" of schema Invoice (schema/invoice.go:11), with other values`;
  - the row limit as in Task 6.
  - The messages carry no "(rule N)" suffix; say so in the note.
- [ ] **Step 7: Baseline's warnings, :1219-1223 and ruling 6 at :1532.** They lose "a PostgreSQL UNIQUE constraint read as an index" (Wave 4 keeps it as `Index.Constraint`), and gain "a PostgreSQL domain type, which it does not create (for nested domains, the nearest)". Ruling 6's AUTO_INCREMENT sentence stays, and gains "and Baseline's doc says so (M21)". Evidence: the notes Inspect writes are the sequence's, the extension type's and the domain's (`git grep -nP 'baselineNote\(' -- migrate/inspect_postgres.go` prints :310, :322, :330).
- [ ] **Step 8: graphql spec :314.** `children` becomes `childIDs`, as entgql gives: `plugin.Singular` singularizes irregular plurals now (`TestSingular`, plugin/names_test.go:14, `"children": "child"`).
- [ ] **Step 9: core-design.** A note after the scale notes, "Note, 2026-10-29: build cost is gated as CPU-time ratios.": since Wave 5, bench/scale gates each phase's user+system CPU time as a ratio to a reference build in the same run (`cold_build_x`, `schema_edit_rebuild_x`); wall-clock seconds are printed for information. Thresholds (bench/scale/thresholds.json): N=500 cold 13.0, edit 10.7; N=500 with GraphQL 29.5 and 28.5, each 2× the largest of three runs, calibrated on darwin. :852's `schema_edit_rebuild_s` gets a pointer to the note. Evidence: bench/scale/thresholds.json; Wave 5's ruling 6 (docs/plans/2026-10-28-hardening-5-tests.md).
- [ ] **Step 10: Run** leftover greps 4, 5, 6, 7 and 9. Expected: each prints only lines inside a dated note, or nothing.
- [ ] **Step 11: Commit** `docs: the migrate, API-cleanup, GraphQL and core specs corrected to the code, with dated notes`. C: none (no Go changes); run `git diff --check`.

---

### Task 10: Example functions (quality backlog)

**Files:**
- Create: `example_test.go` (package `velox_test`), `migrate/example_test.go` (package `migrate_test`), `contrib/graphql/veloxgql/example_test.go` (package `veloxgql_test`).

**Interfaces:** The root file uses the package vars `I` and `It` and `ctx` from query_test.go; it must not redeclare them.

Drafts of the three files, each run and lint-clean at 669f8d2, are in `/private/tmp/claude-501/-Users-s-y-s-Jobs-go-velox2/307a7d09-1a6a-4283-a67c-6dfbe3e93815/scratchpad/` (`root_example_test.go`, `migrate_example_test.go`, `veloxgql_example_test.go`). Use them if they are there; the steps below are what they hold.

- [ ] **Step 1: The root Examples.** A helper `exampleDB()` opens `file::memory:` on modernc sqlite with `SetMaxOpenConns(1)` and runs a const `exampleDDL`: `velox-gen migrate sql -dialect sqlite`'s statements for testmodel's invoices (with the unique index `invoice_org_id_number`) and items. Each Example has a one-paragraph doc comment.
  - `Example`: insert A-2, A-1, A-3 for tenant 1; `Where(I.Number.HasPrefix("A-")).OrderBy(I.Number.Asc()).Limit(2).All`; `Count`; `One` of `Z-9` and `velox.IsNotFound`. Output: `2 A-1`, `1 A-2`, `3 <nil>`, `true`.
  - `ExampleQuery_SQL`: `velox.Open(velox.Postgres, nil)`, tenant 7, `Columns(I.ID, I.Number).Where(I.Total.GT(100), I.Paid.EQ(false)).OrderBy(I.Number.Asc()).Limit(10).SQL(ctx)`. Output: `SELECT "id", "number" FROM "invoices" WHERE "total" > $1 AND "paid" = $2 AND "org_id" = $3 ORDER BY "number" LIMIT $4` and `[100 false 7 10] <nil>`.
  - `ExampleQuery_With`: one invoice, two items (pen 2, ink 5), `With(I.Items).One`, `Items.Get()`. Output: `true 2`, `pen 2`, `ink 5`.
  - `ExampleDB_Tx`: a Tx that sets Paid and returns `errDeclined`. Output: `true` (errors.Is), `false <nil>` (the row unchanged).
  - `ExampleWithTenant`: two rows in org 1, one in org 2, untyped constants. Output: `2 1`, `velox: testmodel.Invoice: no tenant in the context`.
  - `ExampleInsertQuery_OnConflict`: insert A-1 total 10, then the same number total 25 with `OnConflict(I.OrgID, I.Number).Update(I.Total)`. Output: `1 25 true`, the last being `got.UpdatedAt.Equal(first.UpdatedAt)`.
- [ ] **Step 2: The migrate Examples.** A helper `exampleUsers(withEmail bool) *migrate.Schema`: a one-table SQLite schema written by hand (`users`: `id integer` Identity, `name text`, and with withEmail `email text` default `''`), primary key `id`. Its comment says it is hand-written, and that velox-gen builds a Schema from a schema package.
  - `ExampleNewPlan`: from without email to with. Output: `add-column users.email` and `ALTER TABLE "users" ADD COLUMN "email" text NOT NULL DEFAULT ''`.
  - `ExampleUp`: `NewDir(Config{Dialect: velox.SQLite})`, `NewPlan(d.Snapshot, want)`, `d.Add("init", p, want)` into an `fstest.MapFS` (names printed sorted), `Up` on a SQLite file under `os.MkdirTemp` (removed after), then `Status`. Output: `0001_init.sql`, `schema.json`, `velox.json`, `velox.sum`, `1 init`, `1 0 <nil>`.
- [ ] **Step 3: The veloxgql Examples.**
  - `ExampleDecodeID`: `EncodeID("User", int64(42))`, `IDType`, `DecodeID[int64](id, "User")`, and `DecodeID[int64](id, "Account")` with `errors.Is(err, veloxgql.ErrWrongIDType)`. Output: `VXNlcjo0Mg`, `User`, `42 <nil>`, `true`. Its doc: renaming a type with graphql.Type changes every ID clients hold (R7).
  - `ExampleCost`: a gqlparser schema with a `users(first:)` connection whose nodes have a `friends(first:)` connection, `gqlparser.LoadQueryWithRules(schema, query, nil)` (LoadQuery is deprecated; staticcheck SA1019), `users(first: 10) { … friends(first: 5) … }`, `Limits{MaxPage: 100, MaxList: 100}`. Output: `191`. Its doc: "Cost prices a request before it runs: each field is 1, and a connection multiplies what it selects by its page size."
- [ ] **Step 4: Run** in each of the root, `./migrate` and `contrib/graphql/veloxgql`: `go test -count=3 -run '^Example' -v <pkg>`. Expected: every Example passes three times.
- [ ] **Step 5: Run C** (root; contrib/graphql). Commit: `Examples for queries, With, Tx, tenants, upserts, migrations, IDs and cost`.

---

### Task 11: README (M25) and the examples' READMEs

**Files:**
- Create: `README.md`.
- Modify: `example/README.md:67`, `:75` (Atlas image), `:84-89` (baseline warnings, step 1); `example/start/README.md` (after "Guards are always on…").

**Interfaces:** None.

- [ ] **Step 1: Write README.md.** Sections, each a few short sentences:
  - **velox2**: what it is (an ORM for Go with an ent-style schema DSL, generated typed queries, and its own migrations), and that it is before 1.0.
  - **Modules**: `github.com/syssam/velox2` (the runtime, `schema`, `migrate`, `plugin`, `cmd/velox-gen`), `github.com/syssam/velox2/contrib/graphql` (the GraphQL generator and `veloxgql`), `github.com/syssam/velox2/contrib/veloxotel` (OpenTelemetry). `example/` is a module of examples. Evidence: the four go.mod files.
  - **Requirements**: Go 1.27 (`go 1.27.0` in every go.mod); PostgreSQL 14, MySQL 8.4 and SQLite 3.35 or newer, MariaDB not supported (migrate/session.go:328-332, `TestCheckVersionFloors`).
  - **Install**: `go get github.com/syssam/velox2` and `go get -tool github.com/syssam/velox2/cmd/velox-gen`, then `go tool velox-gen ./schema` (cmd/velox-gen/main.go:8-11).
  - **Start**: a link to example/start/README.md and example/README.md, with the start example's `cd example && go run ./start`.
  - **Migrations**: both paths in two sentences each, velox's planner (`migrate diff`, `migrate apply`) and Atlas (`migrate sql` through `external_schema`), and "a directory is managed by one of them" (migrate/doc.go:229-230); a link to example/README.md#migrations. (M25 requires this section.)
  - **Documentation**: pkg.go.dev for each module, docs/decisions/, CHANGELOG.md, CONTRIBUTING.md, SECURITY.md.
  - **License**: one marked placeholder, the only one in the repository: `<!-- LICENSE: TBD -->` and the line "The licence is not chosen yet." Nothing else in README depends on the licence.
- [ ] **Step 2: example/README.md.** `postgres/16` becomes `postgres/18` (:67, :75), as in Task 8. Step 1's warnings (:87-89) gain "MySQL's AUTO_INCREMENT counter, which it does not keep: a database built from the file starts IDs at 1" (Task 8's M21 evidence).
- [ ] **Step 3: example/start/README.md** gains a paragraph: "The table comes from `migrations/`. `go tool velox-gen migrate diff -dialect sqlite -name init -dir ./start/migrations ./start/schema` wrote it, and `main.go` applies it with `migrate.Up` to a temporary SQLite file. After a schema change, run `migrate diff` again without `-name init`, and regenerate." Evidence: example/start/main.go:30-35.
- [ ] **Step 4: Run** every command in the three files that needs no Docker, no network beyond the module proxy, and no push: from `example/`, `go run ./start` (output equals `start/testdata/output.golden`), `go tool velox-gen -check -out ./start/gen ./start/schema` (exit 0), `go tool velox-gen migrate diff -dialect sqlite -dir ./start/migrations -check ./start/schema` (exit 0). Record each in the commit message.
- [ ] **Step 5: Run** leftover grep 10. Expected: README.md's placeholder line only. Commit: `README, and the examples' migration and Atlas notes (M25)`.

---

### Task 12: CONTRIBUTING, SECURITY, CHANGELOG

**Files:**
- Create: `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`.

**Interfaces:** None.

- [ ] **Step 1: CONTRIBUTING.md.** Short sections:
  - **Layout**: the four modules and what each holds.
  - **Checks**: `bash scripts/ci-local.sh` is CI's local mirror (Docker); `VELOX_CI_MATRIX=1` adds the PostgreSQL 14 leg (ci-local.sh:3, :15-17). The per-module lint is golangci-lint v2.14.0.
  - **Database tests**: `VELOX_TEST_POSTGRES`, `VELOX_TEST_MYSQL`, `VELOX_TEST_MYSQL_ADMIN`, `VELOX_DB`, `VELOX_ITEST_REQUIRE`, `VELOX_CI_POSTGRES_IMAGE`, each with the meaning its first use gives (`git grep -nP 'VELOX_<NAME>' -- '*.go' '*.sh'`). The DSNs of docker-compose.ci.yml.
  - **Generated code**: handles.go (`go generate .`), the testmodels and examples (G), goldens (`-update`).
  - **Coverage floors**: scripts/coverage-floors.txt, checked by scripts/covfloor. A floor is the measure minus 1.0; a new package needs a line (the file's header, :1-20).
  - **Fuzz targets**: the nine, each run as `go test -run XXX -fuzz '^Name$' -fuzztime 10s <pkg>` (ci.yml:66-73, :138). A new target needs a line in ci.yml and in ci-local.sh. A crasher's testdata/fuzz file is committed with its fix.
  - **Benchmarks**: `scripts/bench-compare.sh` compares with bench/baseline, report-only; `-update` writes the baselines (bench-compare.sh:1-10). bench/scale gates CPU-time ratios (Task 9 Step 9's note).
  - **Docs**: godoc cites no spec or plan (`TestGodocCitesNoSpec`); every exported method has a doc (`TestExportedMethodsHaveDocs`); short declarative sentences.
  - **Licence**: "See the License section of README.md." (the one placeholder).
- [ ] **Step 2: SECURITY.md.** Report a vulnerability through GitHub's private vulnerability reporting on the repository (Security, then "Report a vulnerability"); do not open a public issue. Supported: the latest minor release after 1.0; before 1.0, main. No email address (decision 8).
- [ ] **Step 3: CHANGELOG.md.** Keep a Changelog's shape, one section `## Unreleased (1.0.0)` (decision 11): a summary of what a user of the pre-hardening tree must know, grouped Added / Changed / Removed. It includes the Wave 1 notes: velox-gen refuses GoTypes it cannot write: an unnamed struct, a generic instance, an unnamed interface (C74; `TestUnwritable` and `TestJSONUnwritableType`, 3396a6b); in the gen package's hand-written files, a file go build leaves out binds no methods, and a file only some systems build (a build constraint or a `_GOOS` name) is refused at the line that says so (`TestMethodsSkipsIgnoredFiles`, `TestMethodsRefusesSystemFiles`, 3396a6b). Each entry names its id. Source: the `git log --oneline 3841a08..HEAD` subjects of Waves 1–6.
- [ ] **Step 4: Run** CONTRIBUTING's commands that need no Docker: `go generate .` then `git diff --exit-code handles.go`; `go run ./scripts/covfloor -h` (or its usage, exit as documented); `bash -n scripts/ci-local.sh scripts/bench-compare.sh`. Record each in the commit message.
- [ ] **Step 5: Commit** `CONTRIBUTING, SECURITY and CHANGELOG`.

---

### Task 13: docs/decisions and the versioning policy

**Files:**
- Create: `docs/decisions/README.md` (the index) and one file per decision, `docs/decisions/NNNN-slug.md`.

**Interfaces:** None.

Each record is short: Status (accepted, 2026-10-29), Context (two or three sentences), Decision, Consequences, Evidence (tests and files, by name). A record cites the spec or plan it came from; docs/ may, Go comments may not.

- [ ] **Step 1: Write the records.**
  1. `0001-ent-names`: tables, columns, indexes, foreign keys and inflection follow ent's, so an ent database baselines without renames. Evidence: `TestEntNames` (plugin/names_test.go:75), `TestMigrateBaselineEnt` (cmd/velox-gen/migrate_baseline_test.go:92), 25e9e88.
  2. `0002-no-inflect-dependency`: Plural's rules are copied from go-openapi/inflect v0.19.0 into the stdlib-only plugin package (plugin/names.go, Plural's doc), and their outputs never change.
  3. `0003-nullability`: Optional stays NOT NULL, as old velox; Nillable makes a column NULL-able.
  4. `0004-versioning`: velox follows semantic versioning from 1.0, except the generated-code API (doc.go:223-233). `GeneratedCodeIsVersion6` (velox) and `GeneratedCodeIsVersion3` (veloxgql) are replaced, not kept beside, when generated code must change, only in a minor release, with a CHANGELOG entry that says to run velox-gen again (decision 5). The plugin protocol (`ProtocolVersion` 2, `ModelVersion` 2) follows the same rule. A newer velox reads every older version of schema.json (`snapshotVersion`, migrate/json.go:11) and velox.json (migrate/dir.go:169), and refuses a newer one (decision 6; `TestSnapshotRefusesVersion`, migrate/json_test.go:137).
  5. `0005-deprecation`: before 1.0, an API is removed outright; after 1.0, it gets a `// Deprecated:` paragraph naming its replacement and stays until the next major version (decision 7).
  6. `0006-own-migrate-and-atlas`: velox keeps its own planner and ships `migrate sql` for Atlas; a directory is managed by one of them (migrate/doc.go:205-230).
  7. `0007-policies-decide-hooks-query` (C55): scope_api.go:155-159.
  8. `0008-write-policies-filter` (C56): Task 3's sentence and `TestWritePolicyFiltersToZeroRows`.
  9. `0009-mutations-return-as-written` (G44, G52): contrib/graphql/doc.go:241-245; Read on many-to-many links (doc.go:222-225).
  10. `0010-savepoint-per-mutation` (G45): `TestAtomicSavepoint` (contrib/graphql/veloxgql/gqlimpl/mutation_test.go:410).
  11. `0011-bidirectional-edges` (G17, builder ruling 18): Task 4's paragraph, `TestBidiLinks`, `TestBidiOne`.
  12. `0012-drift-never-hides` with M9 as its one exception (builder ruling 6): `TestSamePredicateKnownLimits`.
  13. `0013-version-floors` (MS1, C32, builder ruling 16): PostgreSQL 14, MySQL 8.4, SQLite 3.35; `TestCheckVersionFloors` (migrate/session_test.go:58).
  14. `0014-with-stays-a-method` (G18): a21f54a's measurement.
  15. `0015-contrib-graphql-path` (R8, ruling 19): and the `vgraphql` alias.
  16. `0016-withtenant-converts` (C51): `TestTenantConversion` (tenant_test.go:698).
  17. `0017-upsert-skips-updatedefault` (Wave 5's controller ruling 2): `TestUpsertUpdateBranchReadBack`, `ExampleInsertQuery_OnConflict`.
- [ ] **Step 2: The index** lists each record with its one-line decision.
- [ ] **Step 3: Check** every test name and file:line a record cites: `git grep -nP 'func <Name>\(' -- '*.go'` finds each, and each cited line holds what the record says. Commit: `docs/decisions: seventeen records, the versioning and deprecation policy among them`.

---

### Task 14: The followups file

**Files:**
- Modify: `docs/plans/2026-10-14-core-1-followups.md` (412 lines, unchanged since b8f006f, so the triage's line numbers hold).

**Interfaces:** Consumes every commit of this wave (Tasks 1–13), for the "This wave" ids.

- [ ] **Step 1: Mark each triaged item** at the start of its text, after its `- ` (a line with several ids, such as :50 or :63, gets one tag each):
  - resolved: `[x] **C7: resolved in 39dc38a.**`;
  - stale: `[x] **C3: stale.** <the triage row's note, one clause>`;
  - deferred: `**C24: deferred after 1.0.**`; moved: `**C53: moved to the pgx split.**`; blocked: `**C5: blocked on the user.**`;
  - this wave: `[x] **C1: resolved in <this wave's commit>.**`, with M25 `[x] **M25: README in <commit>; the LICENSE is blocked on the user.**`.
  The map is the appendix below. Line :168 ("Shard resolution is written twice in `route.go`"), which no triage row names, is stale: route.go resolves a shard in one function, `shardOf` (route.go:390), called at :440, :481, :519 and tx.go:364.
- [ ] **Step 2: Add three sections** at the end:
  - `## Deferred after 1.0`: the 30 deferred ids, each with the spec's reason in a clause; and the after-1.0 items the waves left: Wave 1, the `sqlite_` prefix refusal, `float(M,D)` and `point` row bytes, the sweep after SIGKILL, the empty directory after a sweep, the Mixin line for directly called mixin fields, and its cosmetics; Wave 4, the servicefile message and an online `ADD CONSTRAINT … USING INDEX`; Wave 5, G32, the itest's DDL, and the nested With row-cap error's context; this wave's decision 2 if refused.
  - `## Blocked on the user`: the 7 blocked ids; the LICENSE; the push and the first tag; after the first push, the Linux re-measure of bench/scale and a bench/baseline recorded on an idle machine (`scripts/bench-compare.sh -update`).
  - `## Moved to the pgx split`: C53, G26, G38, G65.
- [ ] **Step 3: The "1.0 release" section (:406-412)**: the README line is resolved (Task 11's commit), the LICENSE line blocked; the apidiff line stays (M24, blocked).
- [ ] **Step 4: Check.** Every hash in the file resolves: `git grep -ohP '\b[0-9a-f]{7}\b' -- docs/plans/2026-10-14-core-1-followups.md | sort -u | while read h; do git cat-file -e "$h^{commit}" || echo "missing $h"; done` prints nothing. The tags count 220 ids: 120 resolved, 41 stale, 30 deferred, 4 pgx, 7 blocked, 17 this wave, 1 withdrawn (R9). Commit: `Followups: every item marked resolved, stale, deferred, blocked or moved`.

---

### Task 15: The wave's gate

- [ ] Run G, then `VELOX_CI_MATRIX=1 bash scripts/ci-local.sh` (it brings up D itself; never beside another D). Expected: rc 0. Record the commit and the rc in the plan's "Rulings made during execution".
- [ ] Run the leftover greps. Expected: as each says.

## Leftover greps

Each was run at 669f8d2.

1. **Task 1.** `git grep -nP '\bflag\(' -- render_cache.go` prints :109, :119, :131, :140, :162, :199. After: nothing.
2. **Task 1.** `git grep -nP '\bop == 0\b|case 0, opInvalid' -- '*.go' ':!contrib'` prints guard.go:130, pred.go:150, scope.go:405. After: nothing.
3. **Task 4.** `git grep -nP "Required makes the edge's foreign key NOT NULL\.$" -- schema` prints schema/edge/edge.go:117, :182. After: nothing (the sentence continues).
4. **Task 9.** `git grep -nP '_unique\b' -- docs/specs/2026-10-23-migrate-design.md docs/specs/2026-10-20-api-cleanup-design.md` prints api-cleanup :188; migrate :182, :294, :295, :432, :442, :455, :500, :509, :1091. After: only lines inside a dated note.
5. **Tasks 8, 11.** `git grep -nP 'postgres/16' -- example migrate` prints example/README.md:67, :75; migrate/doc.go:220, :226. After: nothing.
6. **Task 9.** `git grep -nP 'PostgreSQL 16|PostgreSQL 12\+|varchar\(16382\)' -- docs/specs/2026-10-23-migrate-design.md` prints :544, :1144, :1519. After: only lines inside a dated note.
7. **Task 9.** `git grep -nP 'childrenIDs' -- docs/specs` prints graphql-design :314. After: only inside a dated note.
8. **Task 4.** `git grep -nP 'cannot see' -- desc.go` prints :463, :473. After: nothing.
9. **Task 9.** `git grep -nP 'schema_edit_rebuild_s' -- docs/specs` prints core-design :852. After: :852 and the new note.
10. **Tasks 11, 12.** `git grep -nP -i 'licen[cs]e:? *tbd|not chosen' -- '*.md' ':!docs'` prints nothing today. After: README.md's placeholder and its line only.
11. **Task 5.** `git grep -nP 'a schema is a struct that embeds schema\.Schema"' -- '*.go'` prints cmd/velox-gen/generate.go:81, internal/gen/load.go:84 and the tests. After: nothing (every copy says "directly").

## Rulings this plan makes

1. **(evidence) G44, G52, C55, C51, the Write-frozen bidi partner and Index.Constraint's SQLite form are documented already** (Scope).
2. **(call) Each new behaviour sentence gets a pin test** where one runs without Docker. A [D] sentence is written only after its D run, recorded in the commit.
3. **(call) The bound Required self key is a sentence on `edge.Required`,** not an ent-adoption caveat: ent's key is the same NULL-able one, and a bound non-Optional field keeps NOT NULL in both.
4. **(call) The nested Tx's failed RELEASE is documented as velox behaves on every dialect** (the error, no rollback to the savepoint). What a server does to a transaction after a failed statement is the server's, and is not claimed.
5. **(call) The ent adoption differences are in the followups' deferred list and Desc.Bidi's doc**, where they already are (desc.go:32-39 for the M2M mirror), not in a new user doc.
6. **(evidence) Line :168 of the followups file is stale** (Task 14).
7. **(call) The "no schemas" message says "directly"** (Task 5): the case it fires for is the one C76 documents.

## Decisions for the controller

1. **The absolute paths in two refusals (a defect found while planning).** Fix now, as Task 6 does, or defer. **Recommend: fix now.** The spec promises relative paths; the fix is local to cmd/velox-gen, and a CI log otherwise holds machine paths.
2. **Non-ASCII schema names.** Document only (spec C13: Snake's output is frozen), or also refuse a schema or edge name with a byte above 0x7F in velox-gen. **Recommend: document only.** ent mangles them the same way, so a refusal would block adopting such an ent database; the doc and `TestSnakeIsByteWise` make the limit visible.
3. **The import alias beside gqlgen's graphql package.** **Recommend `vgraphql`.** It keeps the package's name readable and is not taken by gqlgen or entgql.
4. **The Write-frozen bidi partner.** Keep it documented as a unique violation (contrib/graphql/doc.go:316-319), or add an error kind. **Recommend: keep.** A new kind is an API to freeze for a rare case.
5. **Generated-code versions.** Replace the constant on a bump (doc.go:230-233 today), or keep N−1 defined beside N as gRPC does. **Recommend: replace.** A bump means generated code had to change, so old code could not run correctly on it; the bump lands only in a minor release, with a CHANGELOG line.
6. **Snapshot and velox.json versions.** **Recommend: a newer velox reads every older version and refuses a newer one.** Both are version 1 today; this is the policy, recorded in 0004.
7. **Deprecation.** **Recommend: none before 1.0; after 1.0, `// Deprecated:` until the next major.**
8. **Security reports.** **Recommend: GitHub private vulnerability reporting, and no email address.** It works once the repository is pushed, which is blocked on the user anyway.
9. **Historical specs.** Correct them in place with dated notes, or leave them and point to newer docs. **Recommend: in place.** That is the repository's convention (api-cleanup :188), and readers land on the specs.
10. **The root Examples' model.** Use internal/testmodel with inline SQLite DDL, or add a public example model package. **Recommend: testmodel.** A new package needs a coverage-floor line and a generator run, and the Examples read the same either way; the cost is that pkg.go.dev shows an internal import.
11. **The CHANGELOG's shape.** One "Unreleased (1.0.0)" summary, or a per-wave history. **Recommend: the summary.** No release has been made, so there is no prior version to diff against.

## Controller rulings

The controller takes every recommendation in "Decisions for the controller":

1. Fix the absolute paths now (Task 6).
2. Non-ASCII schema names are documented only.
3. The alias is `vgraphql`.
4. The Write-frozen bidi partner stays a unique violation.
5. A generated-code version bump replaces the constant, in a minor release with a CHANGELOG line.
6. A newer velox reads every older snapshot and velox.json version and refuses a newer one.
7. No deprecations before 1.0; after 1.0, `// Deprecated:` until the next major version.
8. Security reports go through GitHub private vulnerability reporting.
9. Historical specs are corrected in place with dated notes.
10. The root Examples use internal/testmodel. The example programs are a separate module (example/go.mod), so the root package cannot import them.
11. The CHANGELOG has one "Unreleased (1.0.0)" summary.

## Appendix: the followups map

Each item is `id :<followups line> <commit(s)>`. A line with a letter (50a, 50b) is one of several ids on one line. Ids from other reviews have no line. G10 is resolved by 1d216d8 (Wave 1 Task 4), though the triage called it stale. C9 is closed by a test, Wave 5's ruling 1.

- **Resolved, with the commit** (120): C7 :22 39dc38a; C9 :24 TestCacheKeysMoreShapes (render_cache_test.go:183); C11 :41 39dc38a; C12 :42 39dc38a 9c25cef; C15 :48 06d3b4d; C18 :50b 58a01e5; C19 :54 82b4728; C20 :55 4036977; C21 :56 1d216d8; C22 :57 82b4728; C23 :61a 1642c74; C25 :62 06d3b4d; C26 :63a 4b3ae1c; C27 :63b 4b3ae1c; C28 :63c 4b3ae1c; C29 :64 013fed4; C30 :66 6ad8bd5; C31 :67 58a01e5; C32 :68 9c25cef; C33 :72 06d3b4d; C34 :73 1642c74; C35 :74 06d3b4d; C37 :76 06d3b4d; C38 :77 cea3e56; C39 :81 0fd3988; C40 :82 013fed4; C41 :83a 6ad8bd5; C42 :83b 013fed4; C43 :84 6ad8bd5; C44 :85 39dc38a cea3e56 56804e2; C45 :86 0fd3988; C46 :87 0fd3988; C47 :88 013fed4; C48 :89 013fed4; C49 :90 d3717d2; C51 :96 b04ac97; C55 :105 b04ac97; C62 :116 cea3e56; C64 :119 de04720; C65 :120 de04720; C66 :121 58a01e5; C67 :122 de04720; C68 :123 cea3e56; C70 :126 00b804b; C71 :132 1d216d8; C72 :133 1d216d8; C74 :135 3396a6b; C75 :136 3396a6b; C77 :138 25e9e88; C81 :146 013fed4; C87 :162 00b804b; C89 :164 1642c74; C91 :166 6ad8bd5; C92 :167 969c1d5; C93 :169 969c1d5; C94 :170 0fd3988; C95 :171 0fd3988; C96 :172 0fd3988; C97 :173 00b804b; C98 :174 e3c1301; C99 :175 06d3b4d; C100 :176 39dc38a; G1 :183 ee62fa8; G3 :185 3396a6b; G4 :186 82b4728; G5 :187 7849ede; G6 :188 005ff21; G8 :190 7849ede; G9 :191 005ff21; G10 :192 1d216d8; G11 :202 7777eea; G12 :203 7777eea; G13 :204 7777eea; G17 :230 e84aeef 1e5519c; G18 :231 a21f54a; G21 :234 39dc38a; G22 :235 39dc38a; G24 :237 effe2ff ec8c88f; G29 :268 82b4728; G30 :269 82b4728; G31 :270 3396a6b; G34 :281 7849ede; G37 :286 c5fb070; G44 :300 7936ecb; G45 :301 b47cef8 b1e173d; G46 :302 7849ede; G47 :304 8bcb28f 723721c; G48 :305 c8afa2f 6ac575a (closed: not reachable); G49 :306 e99e1aa; G50 :307 d14454a 5fb22dc; G51 :308 7936ecb; G52 :309 7936ecb; G53 :315 00b804b; G59 :321 00b804b; G61 :323 7849ede; G70 :340 4b3ae1c; R1 :schema review #16 020d2f1; R2 :schema review #16; left out by spec §6b.18 20edb85; R3 :schema review #16; left out by §6b.18 020d2f1; R4 :schema review #9; left out by §6b.18 82b4728; M1 :370 be9e688; M2 :371 9ad86e1; M3 :372 b9c06c5 ea42b1d 7fa0a38; M4 :373 b9c06c5 ea42b1d; M5 :374 d139204; M7 :376 ab40063; M8 :377 d139204; M10 :379 d139204; M13 :384 4956730 (the message; full support deferred); M14 :385 f1a8748; M15 :392 6675c89; M16 :393 e3c1301; M17 :394 e3c1301; M20 :399 020b907; M22 :403 e46a843 eadbb02; M23 :404 fb7067e; M28 :412 a21f54a; MS1 :spec ruling 41 9c25cef; N1 :naming task-5-report:103 3396a6b; N2 :final-review Minor 4 39dc38a.
- **Stale (resolved: the triage found it already fixed or moot)** (41): C3 :15, C4 :16, C6 :21, C8 :23, C17 :50a, C54 :101, C57 :108, C60 :114, C69 :125, C78 :139, C83 :152, G2 :184, G7 :189, G16 :223, G19 :232, G23 :236, G25 :238, G27 :261, G28 :267, G33 :273, G35 :284, G39 :294, G42 :297, G54 :316, G56 :318, G60 :322, G62 :324, G64 :331, G66 :335, G67 :336, G69 :339, G71 :341, G72 :342, G73 :343, G74 :344, G75 :345, G76 :346, M27 :411, MS2 (spec ruling 20 / §4), MS3 (spec Plans 3), MS4 (spec ruling 53).
- **Deferred after 1.0** (30): C24 :61b, C52 :97 / 144, C59 :113, C73 :134, C79 :143, C80 :145, C82 :151, C84 :156, C85 :157, C86 :161, C88 :163, G14 :221, G15 :222, G32 :272, G36 :285, G40 :295, G41 :296, G43 :299, G55 :317, G57 :319, G58 :320, G63 :330, G68 :338, R5 (schema review #15; left out by §6b.18), R6 (schema review #15), M6 :375, M11 :382, M12 :383, M18 :395, M19 :396.
- **Moved to the pgx split** (4): C53 :100, G26 :260, G38 :293, G65 :334.
- **Blocked on the user** (7): C5 :20, C10 :40, C58 :111, C63 :117, G20 :233, M24 :408, M26 :410.
- **This wave** (17): C1 :12, C2 :14, C13 :46, C14 :47, C16 :49, C36 :75, C50 :91, C56 :106, C61 :115, C76 :137, C90 :165, R7 (contrib review #17), R8 (contrib review #15; left out by spec §6c.16 with no follow-up), R10 (schema review #13), M9 :378, M21 :400, M25 :409.
- **Withdrawn** (1): R9 (core review #5).
