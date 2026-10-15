# Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every statement a fingerprint, its Go call site, a sqlcommenter SQL comment, the context's tags and its result. Add a transaction hook. Ship `contrib/otel`, a separate module that turns all of it into OpenTelemetry spans and metrics.

**Architecture:**
- **Fingerprint.** The render cache stores each shape's fingerprint beside its text, so a cached statement gets it free.
- **Statement fields.** A new `observe` step fills `Dialect`, `Tags`, `Caller` and `Tx` before any hook runs:
  - `prepare` (the run path) always calls it;
  - `plain` calls it only when the DB writes a SQL comment, because nothing else there reads them.
- **The SQL comment.** It is appended after rendering. The text after the statement is cached per (call site, listed tag values), so a cached statement pays one string join.
- **`Result`.** `DB.exec` sets it.
- **Transactions.** `TxAround` hooks wrap each `db.Tx` call. The innermost `next` runs velox's attempt loop. Every statement inside points at the call's `TxInfo`.
- **`contrib/otel`** is a module of its own (`package veloxotel`). It is one adapter value whose `around` and `txAround` methods are the hooks. It imports only the OpenTelemetry API.

**Tech Stack:**
- Go 1.27 (generic methods);
- `crypto/sha256`, `runtime.Callers`/`runtime.CallersFrames`, `runtime/debug.ReadBuildInfo`;
- the in-repo `FakeExec`;
- pgx v5, `modernc.org/sqlite`;
- OpenTelemetry Go v1.47.0: the API (`go.opentelemetry.io/otel`, `/trace`, `/metric`); the SDK (`/sdk`, `/sdk/metric`) in tests and the example only.

**Spec:** `docs/specs/2026-10-15-observability-design.md` (all of it). It builds on:
- Core's pipeline, `docs/specs/2026-10-14-core-design.md` §5 and §8;
- the policy design's statement fields and hook rules, `docs/specs/2026-10-14-policy-design.md` §2.3 and §6.

## Global Constraints

- **Go 1.27.** Every method of a generic velox type is generic (`[_ Lazy[E]]` or its own type parameter); `methods_test.go` enforces it. This plan adds no generic type, function or method to the core module: everything new is non-generic.
- **Nothing runs at program start.** No package-level variable is initialized by a function call.
  - The build info, velox's own package path and every cache are filled lazily, on first use.
  - The transaction ID counter is a zero-valued `atomic.Uint64`.
- **No velox type reachable from generated code holds `reflect.Type`** (Core §3 rule 7). `Caller`, `Tag`, `Result`, `TxInfo` and `TxRetry` hold strings, numbers, a `context.Context`, a `time.Time` and errors only.
- **Nothing is instantiated per entity for observability** (spec success criterion 5).
- **`DB.plain`'s `st` and `read` must not escape.** Never pass `st` to a function value inside `plain` or anything it calls. Check it with `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"`. The output may say `leaking param content: st`, never `leaking param: st`, and must say `read does not escape`. This is "the escape check" below.
- **With nothing configured, Core's budget tests (`budget_test.go`) pass unchanged.**
- **With callers and comments on,** a cached one-row read makes at most 1 allocation more than the same read without them (spec §6). That is asserted by `TestObservedOneRowBudget`. The 300 ns is read from benchmarks and recorded (Ruling 13).
- **Errors.**
  - Every error starts `velox: <Entity>: `, or `velox: transaction: ` for a transaction's.
  - Sentinels are bare lower-case phrases, wrapped with `%w`.
  - This plan adds no sentinel. A `TxAround` hook's own errors are returned unchanged.
- **`contrib/otel`.**
  - It is the module `github.com/syssam/velox2/contrib/otel`, package `veloxotel`, with `replace github.com/syssam/velox2 => ../..` for development.
  - Its non-test code imports only `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/attribute`, `go.opentelemetry.io/otel/codes`, `go.opentelemetry.io/otel/trace`, `go.opentelemetry.io/otel/metric`, velox and the standard library.
  - SDK packages appear only in `_test.go` files.
  - Versions are pinned at `v1.47.0`, the latest stable release on 2026-10-15 (`go list -m -versions go.opentelemetry.io/otel`).
  - Semantic-convention names are string constants in `attrs.go`, never imported from a `semconv` package.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` run `contrib/otel`'s gofmt, vet, golangci-lint and `go test -race`, like the example's block. Its PostgreSQL test runs under the existing `VELOX_TEST_POSTGRES`.
- **Clean.**
  - `gofmt`, `go vet` and golangci-lint v2.14.0 are clean in the root module, `cmd/velox-gen`, `example` and `contrib/otel`.
  - `go test -race ./...` passes in all four.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes on the spec

1. **`TxAround` wraps the whole call: velox runs the attempts inside `next`.** A hook is called once per `db.Tx` call and must call `next` exactly once. `next` runs every `RetrySerializable` attempt and the commit, and `TxInfo.Attempt` changes before each attempt. Calling `next` a second time returns an error without running `fn`.
   - Why: §2.2 asks for one span per `db.Tx` call, holding the final attempt count and an event per retry. A hook called per attempt could not know which attempt is the last.
   - The spec's "must call next once per attempt" therefore reads as "once; next runs every attempt".
2. **`TxInfo` gains `Retries []TxRetry` and `Outcome TxOutcome`.**
   - `TxRetry{Err error; At time.Time}` is appended by velox before each retry.
   - `TxOutcome` is `TxCommit`, `TxRollback` or `TxError`.
   - Why: §2.2's retry events and `velox.tx.outcome` need what only velox knows: which attempts failed, when, and whether begin, `fn` or commit ended the call. Reading error text would be brittle.
   - The outcome rules:
     - `fn`'s error is `TxRollback`, unless the transaction's context had ended (the deadline passed), which is `TxError`;
     - begin or commit failing, or the deadline passing before commit, is `TxError`;
     - a panic or `runtime.Goexit` in `fn` is `TxRollback`.
3. **A `TxInfo` is made only when the DB has a hook of any kind** (`Rewrite`, `Around` or `TxAround`); otherwise `Statement.Tx` is nil. Nothing else can read it, so a DB with nothing configured allocates nothing more per transaction.
4. **`Statement` gains `Dialect Dialect`.** An `Around` hook has only the statement, and §2.1's `db.system.name` needs the dialect.
5. **`Result`.**
   - `Rows` counts the rows read (an `INSERT … RETURNING`'s and `Count`'s single row included).
   - `Affected` is the driver's count for a write sent without `RETURNING`, the rows returned for one sent with it, and -1 for a read or a failed statement.
   - Rows are counted only when an `Around` hook exists or a row cap applies; otherwise nothing can read `Result.Rows`, and counting would add an allocation.
   - `Result` is reset at the start of each execution, so a retrying hook sees the last attempt's.
6. **Call sites.**
   - **Which frames are velox's.** Only the root package `github.com/syssam/velox2`, whose path is read at run time from one of its own frames. Its sub-packages, such as `example` and `internal/testrepo`, are applications.
   - **How the stack is read.** Program counters are read in batches of 8, up to 64 frames. Each counter's frames are resolved once, inlined frames included, and cached per DB; with nothing found within 64 frames, `Caller` is zero.
   - **The cache's bound.** It holds up to 4,096 counters (`maxPCs`). Past that, a counter is resolved each time, as the render cache renders past its bound.
   - **`File`.** It is relative to the module that holds the frame's package, found from `debug.ReadBuildInfo` (the main module and every dependency, longest path first). A `-trimpath` file name has its module path (and `@version`) stripped. In a function name, `%2e` is unescaped and a trailing `_test` is dropped, and package `main` is `BuildInfo.Path`. Failing all that, `File` is the base name.
   - **`WithCallerSkip` turns callers on.** It also implies `WithCallers`, since naming packages to skip means the application wants call sites.
7. **The comment cache is keyed by call site and the listed tags' values, not by shape.** The text after the SQL, ` /*…*/`, does not depend on the shape. Joining it to the statement's text is the one allocation §6 allows. Keying by shape as well would only multiply the entries.
   - The cache holds at most 4,096 entries and 4 MiB of keys and texts, and evicts the oldest entry (FIFO) to make room, as §1.3 says. The render cache, by contrast, stops growing.
   - The key encodes each part with its length, and an unset tag apart from an empty one, so no two call sites or tag lists share a key.
8. **Encoding.** Every byte of a key or value outside the unreserved set `A–Z a–z 0–9 - . _ ~` is percent-encoded, with uppercase hex. Then the value is quoted.
   - So `caller='billing%2Finvoice.go%3A42'`: the spec's example shows the caller unencoded.
   - No tag value can contain `'` or `*/`, so none can end the comment.
   - Keys are written in the spec's order (`app`, `caller`, then the listed tags), not sorted.
9. **Panics at `Open`, as `WithAround(nil)` does:**
   - `WithSQLComment` with an empty `app`;
   - `WithTxAround(nil)`;
   - `WithTags` with an odd number of strings, which panics when called.
10. **`SQL(ctx)` includes the comment, with the call site of the `SQL` call.** It returns the text that would be sent from there.
11. **`Statement.Tags` and the hooks.**
    - `Statement.Tags` is the context's slice. On the run path `prepare` clones it before the hooks run, like the other slices. A `Rewrite` hook may change `Tags` and `Caller`, and the comment is rendered from what it leaves.
    - A `Rewrite` hook sees no comment. An `Around` hook sees the sent text.
12. **Span attribute names for the call site** are `code.file.path`, `code.line.number` and `code.function.name`. These are the names in the semantic-convention release whose `db.*` names the spec uses. That release deprecated the spec's `code.filepath`, `code.lineno` and `code.function`.
13. **§6's 300 ns is measured, not asserted.**
    - Allocations are asserted by a test.
    - The time is read from `BenchmarkOneRow` against `BenchmarkOneRowObserved`, and recorded in the spec's §6 and Core §8. Core §8 does the same, because times vary between machines and under `-race`.
14. **`error.type`.**
    - `ErrNoWhere` and `ErrLimitTooLarge` map to `velox.unbounded`. Every other velox sentinel not in §2.1's list maps to `_OTHER`.
    - A statement refused before execution (guard, scope, policy, hooks) reaches no `Around` hook, so it makes no statement span and no metric point. It appears as the error of the transaction it ends.
    - `velox.timeout` is matched by `errors.Is(err, velox.ErrTimeout)`, or by `context.DeadlineExceeded` while the hook's context has the cause `ErrTimeout`.
    - A failed `db.Tx` gives its span the status Error and an `error.type`, with the same mapping.
15. **The transaction span.**
    - It is of kind internal.
    - `velox.tx.isolation` is `sql.IsolationLevel.String()`, for example `Default`.
    - Retry events are added after `next` returns, stamped with `TxRetry.At`.
16. **The adapter never recovers a panic.** Spans are ended in deferred functions, so a panic in `fn` or an `Iter` loop body ends them, with status Error and the description `panic`, and goes on unchanged.
17. **Instrumentation.**
    - The tracer and meter are named `github.com/syssam/velox2/contrib/otel`, with no version until velox tags releases.
    - An instrument-creation error goes to `otel.Handle`, and the instrument the API returned is used.
18. **`contrib/otel`'s tests run on a `database/sql` fake driver in its test files.** Velox's `FakeExec` lives in the root package's test files and cannot be imported. The entities are `internal/testmodel`'s: Go's internal rule goes by import path, and `contrib/otel`'s path is under `github.com/syssam/velox2`.
19. **The example's tracing section lives in `example/tracing.go`,** so the call-site line numbers in its golden output do not move when `main.go` changes.
    - When the examples were split into one directory per topic, the tracing section became its own example, `example/otel`.
20. **CI runs `contrib/otel` from Task 6, where the module is created, not Task 8.** Task 7 changes the module, and `ci-local.sh` is the gate before every push, so the module is checked from its first commit.

## Review Focus

1. **A tag value or app name containing `'`, `*/`, `/*`, a newline or non-ASCII** must never end the SQL comment or inject SQL. The sent text has exactly one `/*` and one `*/` (Task 3, `TestCommentCannotBeEscaped`).
2. **Two call sites, or two tag lists, that would collide under naive concatenation** (`a.go`:12 against `a.go1`:2; values `["a", "b\x00c"]` against `["a\x00b", "c"]`; unset against empty) get different comments. Under concurrent use each statement gets its own (Task 3, `TestCommentKeysAreUnambiguous`, `TestCommentIsSafeForConcurrentUse`).
3. **A panic in `fn` or in an `Iter` loop body** ends the `velox.tx` and statement spans and propagates unchanged. A panicking `fn` leaves `Outcome == TxRollback` (Task 4, `TestTxAroundRules`; Task 7, `TestPanicsEndTheSpans`).
4. **A statement shape has one fingerprint** on every path: cached, first render, past the cache's bound, and after a `Rewrite` hook replaced the entity with a copy. The comment never changes it (Task 1, `TestFingerprintIsTheShapes`; Task 3, `TestCommentLeavesTheFingerprint`).
5. **Metric attribute sets stay bounded.** Tags, the tenant (even with `RecordTenant`), the SQL text and, by default, the fingerprint never appear on a metric point (Task 7, `TestMetricsAttributesAreBounded`).

---

### Task 1: Fingerprints in the render cache

**Files:**
- Modify: `render_cache.go` (the `cached` entry, `get`, `fingerprint`, the byte bound)
- Modify: `statement.go` (`Statement.Fingerprint`)
- Modify: `db.go` (`plain`, `prepare`)
- Test: `fingerprint_test.go` (create, package `velox_test`), `render_cache_test.go` (add)

**Interfaces:**
- Consumes: nothing new.
- Produces:

```go
// render_cache.go
type cached struct{ text, fp string } // a shape's SQL text and its fingerprint
// renderCache.m becomes map[*EntityInfo]map[string]cached. c.bytes counts
// len(key) + len(text) + len(fp) per entry.
func (c *renderCache) get(d Dialect, st *Statement) (cached, []any) // the pipeline's entry point
func (c *renderCache) render(d Dialect, st *Statement) (string, []any) // kept for the tests: e, args := c.get(d, st); return e.text, args
func fingerprint(text string) string // the first 8 bytes of SHA-256(text) as 16 lowercase hex characters

// statement.go: Statement gains, right after SQL:
//   Fingerprint string // set by rendering: the shape's ID, the hash of SQL without the comment
```

`plain` and `prepare` set `st.SQL`, `st.Fingerprint` and `st.Args` from `get`. `prepare`'s uncached branch (a hook replaced the entity) sets `st.Fingerprint = fingerprint(st.SQL)`. A statement rendered past the cache's bounds gets `fingerprint(text)` from `get`.

- [ ] **Step 1: Write the failing tests**

`fingerprint_test.go`:

```go
package velox_test

import (
	"context"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// lastStatement returns an Around hook that copies each statement it sees
// into *st.
func lastStatement(st *velox.Statement) velox.Option {
	return velox.WithAround(func(ctx context.Context, s *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		*st = *s
		return err
	})
}

// TestFingerprintGolden pins observability design §1.1 for one statement on
// each dialect: a change to rendering or to the hash shows here, and the
// release notes must say so.
func TestFingerprintGolden(t *testing.T) {
	for d, want := range map[velox.Dialect]struct{ sql, fp string }{
		velox.Postgres: {`SELECT "id" FROM "invoices" WHERE "number" = $1 LIMIT $2`, "da95560fb97b3ada"},
		velox.MySQL:    {"SELECT `id` FROM `invoices` WHERE `number` = ? LIMIT ?", "9ca18764b9e4f21a"},
		velox.SQLite:   {`SELECT "id" FROM "invoices" WHERE "number" = ? LIMIT ?`, "df6b3bf7e65701b1"},
	} {
		var st velox.Statement
		db := velox.OpenFake(d, &velox.FakeExec{}, lastStatement(&st))
		for range 2 { // rendered, then cached
			if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
				t.Fatal(err)
			}
			if st.SQL != want.sql || st.Fingerprint != want.fp {
				t.Errorf("%v: %s %s, want %s %s", d, st.SQL, st.Fingerprint, want.sql, want.fp)
			}
		}
	}
}

// TestFingerprintIsTheShapes is Review Focus 4: every value of the arguments,
// and a Rewrite hook's copy of the entity (rendered without the cache), give
// the shape's one fingerprint; another shape gets another.
func TestFingerprintIsTheShapes(t *testing.T) {
	var st velox.Statement
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, lastStatement(&st))
	fp := func(q velox.Query[testmodel.Invoice]) string {
		t.Helper()
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		return st.Fingerprint
	}
	x := fp(db.Query[testmodel.Invoice]().Where(I.Number.EQ("x")).Limit(1))
	if y := fp(db.Query[testmodel.Invoice]().Where(I.Number.EQ("y")).Limit(7)); y != x {
		t.Errorf("other values: %s, want %s", y, x)
	}
	if other := fp(db.Query[testmodel.Invoice]().Where(I.Number.EQ("x"), I.Paid.EQ(true)).Limit(1)); other == x {
		t.Error("two shapes share a fingerprint")
	}
	copied := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, lastStatement(&st),
		velox.WithRewrite(func(_ context.Context, s *velox.Statement) error {
			info := *s.Entity
			s.Entity = &info
			return nil
		}))
	if _, err := copied.Query[testmodel.Invoice]().Where(I.Number.EQ("z")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if st.Fingerprint != x {
		t.Errorf("uncached render: %s, want %s", st.Fingerprint, x)
	}
}
```

Append to `render_cache_test.go` (package `velox`):

```go
func TestFingerprintFormat(t *testing.T) {
	// SHA-256 of the empty string starts e3b0c44298fc1c14.
	if got := fingerprint(""); got != "e3b0c44298fc1c14" {
		t.Errorf("fingerprint(\"\") = %s", got)
	}
}

// TestCacheStoresTheFingerprint: a hit returns the fingerprint stored at the
// miss, and a statement past the cache's bound gets the same one.
func TestCacheStoresTheFingerprint(t *testing.T) {
	var c renderCache
	miss, _ := c.get(Postgres, stmt(hOrg.EQ(7)))
	hit, _ := c.get(Postgres, stmt(hOrg.EQ(8)))
	text, _ := render(Postgres, stmt(hOrg.EQ(7)))
	if miss != hit || miss.text != text || miss.fp != fingerprint(text) {
		t.Errorf("miss %+v, hit %+v, want text %s", miss, hit, text)
	}
	defer func(n int) { maxShapes = n }(maxShapes)
	maxShapes = 0
	var full renderCache
	if over, _ := full.get(Postgres, stmt(hOrg.EQ(9))); over != miss || full.size() != 0 {
		t.Errorf("past the bound: %+v, %d shapes held", over, full.size())
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'Fingerprint|CacheStoresTheFingerprint' .`
Expected: compile errors, because `Statement.Fingerprint`, `get`, `cached` and `fingerprint` are undefined.

- [ ] **Step 3: Implement**

`fingerprint`:

```go
// fingerprint names a statement's shape: the first 8 bytes, as 16 lowercase
// hex characters, of the SHA-256 of its SQL text without any comment
// (observability design §1.1). velox sends every value as a parameter, so the
// text is the shape.
func fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
```

`get` is today's `render` body with `cached{text, fingerprint(text)}` stored and returned. The bound check becomes `c.bytes+len(key)+len(e.text)+len(e.fp) <= maxCacheBytes`. Update `renderCache`'s doc comment: the cache holds each shape's text and fingerprint.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./... && go test -run 'Budget|UsesTheCache' -count=3 .`
Expected: PASS. Core's budgets are unchanged, because the fingerprint is a stored string.

- [ ] **Step 5: Commit**

```bash
git add render_cache.go render_cache_test.go statement.go db.go fingerprint_test.go
git commit -m "Fingerprint each statement shape in the render cache

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Tags, results and call sites

**Files:**
- Create: `tags.go` (`Tag`, `WithTags`, `tagsOf`)
- Create: `callers.go` (`Caller`, `WithCallers`, `WithCallerSkip`, `callers`, `relFile`, `pkgOf`, `inPkg`)
- Create: `internal/testrepo/testrepo.go` (a wrapper package for the skip test)
- Modify: `statement.go` (`Result`; the fields `Dialect`, `Tags`, `Caller`, `Result`)
- Modify: `db.go` (`config.callers`, `observe`, the `Tags` clone in `prepare`, `Result` in `exec`, `cappedRows` becomes `countedRows`)
- Test: `tags_test.go`, `callers_test.go`, `result_test.go` (create, package `velox_test`), `callers_internal_test.go` (create, package `velox`)

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces:

```go
// tags.go
type Tag struct{ Key, Value string }
// WithTags returns ctx with the tags kv (key, value, key, value, …) added: a
// key already on ctx keeps its place and takes the new value; a new key is
// appended. An odd number of strings panics; none returns ctx unchanged.
func WithTags(ctx context.Context, kv ...string) context.Context
func tagsOf(ctx context.Context) []Tag // nil when none; the slice is shared, never changed in place

// statement.go
type Result struct {
	Rows     int64 // rows read
	Affected int64 // rows a write affected; -1 when the driver does not say
}
// Statement gains, after InTx: Dialect Dialect; Tags []Tag; Caller Caller
// and, after Args: Result Result. Doc comments as in Rulings 4, 5 and 11.

// callers.go
type Caller struct {
	File string // relative to the module root when it can be determined, else the base name
	Line int
	Func string // the function's full name, as runtime.Frame.Function gives it
}
func WithCallers() Option                    // sets c.callers = &callers{} when nil
func WithCallerSkip(pkgPath ...string) Option // implies WithCallers; appends to c.callers.skip
type callers struct {
	skip  []string // package paths that are not the caller, besides velox's own
	once  sync.Once
	self  string   // velox's package path, from a frame of its own
	mods  []string // module paths from the build info, longest first
	main  string   // BuildInfo.Path: the import path of package main
	mu    sync.RWMutex
	pcs   map[uintptr]pcFrame // at most maxPCs entries
}
type pcFrame struct {
	caller Caller
	app    bool // a frame at this PC (inlined ones included) is outside velox and skip
}
var maxPCs = 4096
func (c *callers) capture() Caller // batches of 8 PCs, up to 64 frames; zero when none is the application's
func pkgOf(fn string) string        // the package path of a function name: cut at '[', then the first '.' after the last '/', with "%2e" unescaped
func inPkg(fn, pkg string) bool     // fn is a function of package pkg (pkgOf(fn) == pkg)
func relFile(file, fn, mainPkg string, mods []string) string

// db.go
// config gains: callers *callers // WithCallers; nil: off
// observe sets st's Dialect, Tags, Caller (with WithCallers) and, from Task 4,
// Tx. prepare calls it after the guard, before the entity hooks.
func (db *DB) observe(ctx context.Context, st *Statement)
```

**Behaviour:**
- **`plain`** sets only `st.Dialect = db.d` in this task. Task 3 makes it call `observe` when the DB writes a comment.
- **`prepare`'s hooked branch** clones `st.Tags` like the other slices (Ruling 11).
- **`exec`** starts with `st.Result = Result{Affected: -1}`. Then:
  - **A statement sent with no rows to read:** `st.Result.Affected = r.affected` on success.
  - **A statement that returns rows:** rename `cappedRows` to `countedRows`:

    ```go
    // countedRows counts the rows read and, when capped, ends them after
    // left of them and records that there were more.
    type countedRows struct {
    	rows
    	n      int64
    	left   int
    	capped bool
    	over   bool
    }
    ```

    - Wrap the rows when `db.rowCap(st) > 0 || len(db.cfg.arounds) > 0`.
    - In the deferred function, after `Close`, set `st.Result.Rows = n`. Unless the statement is a SELECT, also set `st.Result.Affected = n`.
    - The row that trips the cap is not counted.
- **`capture` and `relFile`** follow Ruling 6.
  - `capture` loads `self`, `mods` and `main` once.
  - It calls `runtime.Callers(skip, pcs[:])` on a `[8]uintptr` on the stack.
  - For each PC, under the read lock, it uses `pcs` when present. Otherwise it resolves `runtime.CallersFrames([]uintptr{pc})` outside the lock. It takes the first frame whose `Function` is in neither `self` nor `skip`, and stores the result while `len(pcs) < maxPCs`.
  - The `File` it stores is `relFile(frame.File, frame.Function, main, mods)`.
  - `inPkg` cuts `fn` at its first `[` (generic instantiations' names hold `/` and `.` inside brackets).

`internal/testrepo/testrepo.go`:

```go
// Package testrepo is an application's repository layer, for the tests of
// velox.WithCallerSkip: it runs statements on behalf of its callers.
package testrepo

import (
	"context"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// FirstInvoice reads one invoice.
func FirstInvoice(ctx context.Context, db *velox.DB) error {
	_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
	return err
}
```

- [ ] **Step 1: Write the failing tests**

`callers_test.go`:

```go
package velox_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
	"github.com/syssam/velox2/internal/testrepo"
)

// callerOf returns a DB with opts whose Rewrite hook records each
// statement's Caller in *got.
func callerOf(got *velox.Caller, opts ...velox.Option) *velox.DB {
	opts = append(opts, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { *got = st.Caller; return nil }))
	return velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, opts...)
}

func here() int { _, _, line, _ := runtime.Caller(1); return line }

func TestCallerIsTheApplicationFrame(t *testing.T) {
	var got velox.Caller
	db := callerOf(&got, velox.WithCallers())
	line := here() + 1
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	want := velox.Caller{File: "callers_test.go", Line: line, Func: "github.com/syssam/velox2_test.TestCallerIsTheApplicationFrame"}
	if got != want {
		t.Errorf("All: %+v, want %+v", got, want)
	}
	line = here() + 1
	for _, err := range db.Query[testmodel.Invoice]().Limit(1).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.Line != line || got.File != "callers_test.go" {
		t.Errorf("Iter: %+v, want line %d", got, line)
	}
	err := db.Tx(ctx, func(tx *velox.DB) error {
		line = here() + 1
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Line != line || got.Func != "github.com/syssam/velox2_test.TestCallerIsTheApplicationFrame.func1" {
		t.Errorf("in Tx: %+v, want line %d in func1", got, line)
	}
}

func TestCallerOffIsZero(t *testing.T) {
	var got velox.Caller
	if _, err := callerOf(&got).Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if got != (velox.Caller{}) {
		t.Errorf("without WithCallers: %+v", got)
	}
}

func TestCallerSkipsWrapperPackages(t *testing.T) {
	var got velox.Caller
	if err := testrepo.FirstInvoice(ctx, callerOf(&got, velox.WithCallers())); err != nil {
		t.Fatal(err)
	}
	if got.File != "internal/testrepo/testrepo.go" || got.Func != "github.com/syssam/velox2/internal/testrepo.FirstInvoice" {
		t.Errorf("unskipped: %+v", got)
	}
	db := callerOf(&got, velox.WithCallerSkip("github.com/syssam/velox2/internal/testrepo"))
	line := here() + 1
	if err := testrepo.FirstInvoice(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got.File != "callers_test.go" || got.Line != line {
		t.Errorf("skipped: %+v, want callers_test.go:%d", got, line)
	}
}
```

`FirstInvoice` may be inlined into the test. The frame must still be found, because each PC's inlined frames are expanded (Ruling 6).

`callers_internal_test.go` holds a table test of `relFile` with `mods = []string{"github.com/syssam/velox2/example", "github.com/syssam/velox2", "gopkg.in/yaml.v3"}`:

| file | fn | mainPkg | want |
|---|---|---|---|
| `/src/velox2/db.go` | `github.com/syssam/velox2.(*DB).plain` | | `db.go` |
| `/src/velox2/internal/testrepo/testrepo.go` | `github.com/syssam/velox2/internal/testrepo.FirstInvoice` | | `internal/testrepo/testrepo.go` |
| `/src/velox2/example/billing/invoice.go` | `github.com/syssam/velox2/example/billing.(*Invoice).Schema` | | `billing/invoice.go` (the longer module wins) |
| `/src/velox2/example/main.go` | `main.run.func1` | `github.com/syssam/velox2/example` | `main.go` |
| `/src/velox2/example/tool/main.go` | `main.run` | `github.com/syssam/velox2/example/tool` | `tool/main.go` |
| `/src/velox2/callers_test.go` | `github.com/syssam/velox2_test.TestX` | | `callers_test.go` |
| `/src/velox2/internal/testrepo/x_test.go` | `github.com/syssam/velox2/internal/testrepo_test.TestX` | | `internal/testrepo/x_test.go` |
| `github.com/syssam/velox2/example/billing/invoice.go` | `github.com/syssam/velox2/example/billing.F` | | `billing/invoice.go` (`-trimpath`) |
| `gopkg.in/yaml.v3@v3.0.1/decode.go` | `gopkg.in/yaml%2ev3.(*decoder).unmarshal` | | `decode.go` |
| `/src/yaml/decode.go` | `gopkg.in/yaml%2ev3.Unmarshal` | | `decode.go` |
| `/x/other/f.go` | `example.com/other.F` | | `f.go` (no module: base name) |

It also holds table tests of `pkgOf` and `inPkg`:
- `github.com/syssam/velox2.Query[go.shape.struct { github.com/syssam/velox2/internal/testmodel.ID int64 }].All` is in `github.com/syssam/velox2`.
- `github.com/syssam/velox2/example.run` is not in `github.com/syssam/velox2`.
- `github.com/syssam/velox2_test.TestX` is not in `github.com/syssam/velox2`.
- `gopkg.in/yaml%2ev3.Marshal` is in `gopkg.in/yaml.v3`.

Finally, `TestPCCacheIsBounded`:
- set `maxPCs = 2`;
- capture from three different call sites, three times each;
- assert every capture is right and `len(c.pcs) <= 2`.

`tags_test.go`:
- **Merging.** `WithTags(WithTags(bg, "route", "a", "user", "1"), "route", "b", "team", "x")` gives `tagsOf` as `[{route b} {user 1} {team x}]`, through an exported test helper `velox.TagsOf` in `export_test.go`. The parent context still gives `[{route a} {user 1}]`.
- **No pairs.** `WithTags(bg)` returns `bg` itself.
- **Odd count.** `WithTags(bg, "k")` panics with a message containing `WithTags`.
- **Hooks see the tags.** A `Rewrite` hook sees `st.Tags == [{route a}]` and `st.Dialect == velox.MySQL`, on a MySQL `OpenFake`.
- **A hook's edits stay in its statement.** A `Rewrite` hook that sets `st.Tags[0].Value = "changed"` leaves the context's `TagsOf` unchanged.

`result_test.go`: an outer `Around` hook records `st.Result` after `next` returns.
- `All` over 3 rows: `{Rows: 3, Affected: -1}`.
- `Count`: `{1, -1}`.
- `Update` with `FakeExec.Affected = 4`: `{0, 4}`.
- `Insert` of an `Invoice` (generated key, PostgreSQL, `RETURNING`) with `FakeExec.Rows = [][]any{{int64(9)}}`: `{1, 1}`.
- `Iter` broken after 2 of 3 rows: `{2, -1}`.
- `FakeExec.Err` set: `{0, -1}`.
- A hook that retries once after the first `next` (`FakeExec.Errs = []error{boom}`, then 3 rows) sees `{3, -1}` after the second call.
- With `WithMaxRows(2)` and 3 rows: `{2, -1}` and `ErrTooManyRows`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'Caller|Tags|Result|RelFile|PkgOf|InPkg|PCCache' .`
Expected: compile errors, because the new API is undefined.

- [ ] **Step 3: Implement** the interfaces and behaviour above. Add `TagsOf` to `export_test.go`. The doc comment on `WithCallers` says what it costs: one `runtime.Callers` per statement, and nothing more once a call site's PCs are cached.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./... && go test -run 'Budget' -count=3 .`, then the escape check (Global Constraints).
Expected: PASS. The escape check shows no `leaking param: st`.

- [ ] **Step 5: Commit**

```bash
git add tags.go callers.go statement.go db.go export_test.go internal/testrepo tags_test.go callers_test.go callers_internal_test.go result_test.go
git commit -m "Statement tags, results and call sites

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The SQL comment

**Files:**
- Create: `comment.go` (`WithSQLComment`, `commenter`, `encode`)
- Modify: `db.go` (`config.comment`; `plain` and `prepare` append the comment)
- Test: `comment_test.go` (create, package `velox_test`), `comment_internal_test.go` (create, package `velox`)

**Interfaces:**
- Consumes: `Caller`, `Tag`, `Statement.Caller`, `Statement.Tags`, `observe`, `Statement.Fingerprint` (Tasks 1 and 2).
- Produces:

```go
// WithSQLComment appends a sqlcommenter comment to every statement's text:
// app, the call site when WithCallers is on, then each tag in tags that the
// context sets (WithTags), in that order. An empty app panics.
//
// List only tags whose values are stable per code path, such as a route
// template, never an ID: pgx caches prepared statements by their text, and
// pg_stat_statements and MySQL's digest group by it, so a per-request value
// would prepare a statement per request and split every statistic.
func WithSQLComment(app string, tags ...string) Option

type commenter struct {
	prefix string   // "app='<encoded app>'"
	tags   []string // the listed tag keys, in order
	keys   []string // the same keys, encoded
	mu     sync.RWMutex
	m      map[string]string // key → " /*…*/"
	order  []string          // keys, oldest first, for eviction
	bytes  int               // len(key) + len(text) held
}
var maxComments, maxCommentBytes = 4096, 4 << 20
// text returns the comment, with its leading space, for the statement's
// Caller and Tags. It does not keep st.
func (c *commenter) text(st *Statement) string
func encode(s string) string // percent-encodes every byte outside A–Z a–z 0–9 - . _ ~, uppercase hex
```

**Behaviour:**
- **`plain`** calls `db.observe(ctx, st)` when `db.cfg.comment != nil`. After rendering it calls `st.SQL += db.cfg.comment.text(st)`, a method call, never a function value.
- **`prepare`** appends the comment after rendering, in both the cached and uncached branches. `st.Fingerprint` is computed before the append.
- **The key.** `text` builds it in a `[256]byte` stack buffer and looks it up with `c.m[string(key)]`, which does not allocate. It holds, in order:
  - `len(File)`, `File` and `Line`, each as a uvarint, with File's bytes after its length;
  - for each listed tag, a byte 0 (unset) or 1 (set), then the value's length and bytes.

  A key longer than the buffer grows it on the heap (a miss path).
- **The text** is ` /*` + `prefix` + `,caller='<encode(File:Line)>'` (when `Caller.File != ""`) + `,<key>='<encode(value)>'` for each set listed tag + `*/`.
- **Insertion.** It happens under the write lock, checking for a duplicate first. While the entry would push `len(m)` past `maxComments` or `bytes` past `maxCommentBytes`, it evicts `order[0]`. An entry larger than `maxCommentBytes` alone is returned uncached.

- [ ] **Step 1: Write the failing tests**

`comment_test.go`:

```go
package velox_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestSQLCommentText(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithCallers(), velox.WithSQLComment("billing", "route", "missing"))
	c := velox.WithTags(ctx, "route", "GET /invoices", "user", "42")
	line := here() + 1
	if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(c); err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id" FROM "invoices" WHERE "number" = $1 LIMIT $2 /*app='billing',caller='comment_test.go%3A` +
		strconv.Itoa(line) + `',route='GET%20%2Finvoices'*/`
	if e.SQL != want {
		t.Errorf("sent:\n%s\nwant:\n%s", e.SQL, want)
	}
	// SQL(ctx) returns the text that would be sent from its own call site.
	line = here() + 1
	text, _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).SQL(c)
	if err != nil || !strings.HasSuffix(text, "%3A"+strconv.Itoa(line)+`',route='GET%20%2Finvoices'*/`) {
		t.Errorf("SQL(ctx): %s, %v", text, err)
	}
}

func TestSQLCommentWithoutCallers(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.SQLite, e, velox.WithSQLComment("billing"))
	if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id" FROM "invoices" WHERE "number" = ? LIMIT ? /*app='billing'*/`; e.SQL != want {
		t.Errorf("sent %s", e.SQL)
	}
}

// TestCommentLeavesTheFingerprint is Review Focus 4: the comment is not part
// of the shape. A Rewrite hook sees no comment, an Around hook the sent text.
func TestCommentLeavesTheFingerprint(t *testing.T) {
	var rewritten string
	var st velox.Statement
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithCallers(), velox.WithSQLComment("billing"),
		velox.WithRewrite(func(_ context.Context, s *velox.Statement) error { rewritten = s.SQL; return nil }),
		lastStatement(&st))
	if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if st.Fingerprint != "da95560fb97b3ada" || !strings.Contains(st.SQL, "/*app='billing',caller='comment_test.go%3A") || rewritten != "" {
		t.Errorf("fingerprint %s, sent %s, the Rewrite hook saw %q", st.Fingerprint, st.SQL, rewritten)
	}
}

// TestCommentCannotBeEscaped is Review Focus 1.
func TestCommentCannotBeEscaped(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.MySQL, e, velox.WithSQLComment("it's */ x", "route"))
	evil := velox.WithTags(ctx, "route", "*/ DROP TABLE invoices; /* ' \n é")
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(evil); err != nil {
		t.Fatal(err)
	}
	comment := e.SQL[strings.Index(e.SQL, " /*"):]
	if strings.Count(e.SQL, "/*") != 1 || strings.Count(e.SQL, "*/") != 1 || !strings.HasSuffix(e.SQL, "*/") {
		t.Errorf("the comment can be escaped: %s", e.SQL)
	}
	if want := ` /*app='it%27s%20%2A%2F%20x',route='%2A%2F%20DROP%20TABLE%20invoices%3B%20%2F%2A%20%27%20%0A%20%C3%A9'*/`; comment != want {
		t.Errorf("comment %s\nwant    %s", comment, want)
	}
}

// TestCommentIsSafeForConcurrentUse is Review Focus 2, under -race: each
// statement carries its own context's tag.
func TestCommentIsSafeForConcurrentUse(t *testing.T) {
	var mu sync.Mutex
	sent := map[string]string{}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithCallers(), velox.WithSQLComment("billing", "route"),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			mu.Lock()
			sent[st.Tags[0].Value] = st.SQL
			mu.Unlock()
			return next(ctx)
		}))
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			for i := range 50 {
				v := strconv.Itoa(g*1000 + i%7)
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTags(ctx, "route", v)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	for v, text := range sent {
		if !strings.HasSuffix(text, ",route='"+v+"'*/") {
			t.Errorf("route %s sent %s", v, text)
		}
	}
}
```

`comment_internal_test.go`:
- **`encode`.** `encode("AZaz09-._~")` is unchanged, and `encode("a b/c:d'e*")` is `a%20b%2Fc%3Ad%27e%2A`.
- **`TestCommentKeysAreUnambiguous`** (Review Focus 2). On one commenter with tags `("a", "b")`, these pairs give different texts:
  - `Caller{File: "a.go", Line: 12}` against `Caller{File: "a.go1", Line: 2}`;
  - tags `a="a", b="b\x00c"` against `a="a\x00b", b="c"`;
  - `a` unset against `a=""`.
- **`TestCommentCacheEvictsTheOldest`.** With `maxComments = 3`, ten tag values in turn:
  - all render right;
  - `len(c.m) == 3`, and `c.m` holds the last three keys and not the first;
  - `len(c.order) == 3`.
- **`TestCommentCacheBoundedInBytes`.** With `maxCommentBytes = 300`, 50 values of 20 bytes keep `c.bytes <= 300`. A 400-byte value renders right and is not cached.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'Comment|Encode' .`
Expected: compile errors, because `WithSQLComment` is undefined.

- [ ] **Step 3: Implement** the interfaces and behaviour above.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./... && go test -run Budget -count=3 .`, then the escape check (Global Constraints).
Expected: PASS, and no `leaking param: st`.

- [ ] **Step 5: Commit**

```bash
git add comment.go db.go comment_test.go comment_internal_test.go
git commit -m "sqlcommenter SQL comments, cached per call site and tags

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The transaction hook

**Files:**
- Create: `txhook.go` (`TxAround`, `TxInfo`, `TxRetry`, `TxOutcome`, `WithTxAround`, `txAround`)
- Modify: `tx.go` (`Tx`'s attempt loop, `transaction`'s outcome, `savepoint` passes `tx` on)
- Modify: `db.go` (the `DB.tx` field, `config.txArounds`; `observe` sets `st.Tx`)
- Modify: `statement.go` (`Statement.Tx`)
- Test: `txhook_test.go` (create, package `velox_test`)

**Interfaces:**
- Consumes: `observe` (Task 2).
- Produces:

```go
// txhook.go
type TxAround func(ctx context.Context, tx *TxInfo, next func(context.Context) error) error

type TxInfo struct {
	ID        uint64             // unique within the process
	Isolation sql.IsolationLevel
	ReadOnly  bool
	Attempt   int             // 1 for the first try; RetrySerializable increments it
	Context   context.Context // the context the hooks passed to next
	Retries   []TxRetry       // the attempts RetrySerializable retried, in order
	Outcome   TxOutcome       // how the last attempt ended; set before next returns
}

type TxRetry struct {
	Err error     // what the attempt failed with
	At  time.Time // when velox decided to retry
}

type TxOutcome uint8

const (
	TxCommit   TxOutcome = iota + 1 // fn returned nil and the commit succeeded
	TxRollback                      // fn returned an error, or panicked
	TxError                         // begin or commit failed, or the transaction's deadline passed
)

func (o TxOutcome) String() string // "commit", "rollback", "error"; "unknown" otherwise
func WithTxAround(h TxAround) Option // nil panics: "velox: WithTxAround(nil)"
var txIDs atomic.Uint64
// txAround runs attempts through db.cfg.txArounds, the first given outermost.
func (db *DB) txAround(ctx context.Context, info *TxInfo, attempts func(context.Context) error) error

// db.go: DB gains tx *TxInfo // the transaction's, when hooks are configured
//        config gains txArounds []TxAround
// observe sets st.Tx = db.tx.
// statement.go: Statement gains, after InTx: Tx *TxInfo // the transaction the statement runs in, when the DB has hooks; nil otherwise
```

**Behaviour** (Rulings 1–3):
- **The attempt loop.** `Tx` builds `info` when `db.hooked() || len(db.cfg.txArounds) > 0`, with `Context: tctx`. It wraps its loop in `attempts(ctx)`:
  - attempts are numbered from 1, and `info.Attempt` is set before each;
  - the loop stops when `err == nil || attempt > o.retries || !retryable(err) || ctx.Err() != nil`;
  - before retrying, it appends `TxRetry{err, time.Now()}`.

  It keeps today's `errors.Join(errTimeout("transaction"), err)` on `tctx`. With no `txArounds` it calls `attempts(tctx)`, otherwise `db.txAround(tctx, info, attempts)`.
- **`transaction`** takes `info` (which may be nil) and passes `tx: info` to the `DB` it gives `fn`. It sets `info.Outcome` on every path, as Ruling 2 says. The deferred rollback of a panicking or `Goexit`ing `fn` sets `TxRollback`.
- **`savepoint`** passes `tx: db.tx` to its nested `DB` and runs no `TxAround` hook.
- **`txAround`** builds the chain as `execute` does for `Around`. The innermost `next`:

```go
next := func(ctx context.Context) error {
	if called {
		return errors.New("velox: transaction: a TxAround hook called next twice")
	}
	called = true
	info.Context = ctx
	last = attempts(ctx)
	return last
}
```

  After the chain returns nil:
  - if `!called`, it returns `velox: transaction: a TxAround hook returned without calling next`;
  - if `last != nil`, it returns `velox: transaction: a TxAround hook discarded the error from next`.

  A hook's own error is returned unchanged.
- **Doc comments.**
  - `TxAround`'s: the hook is called once per `db.Tx` call, not for a nested `Tx` (a savepoint). It must call `next` once and return its error, or return an error of its own. `next` runs every attempt and the commit. A multi-batch `InsertAll` outside a transaction runs in one `db.Tx`, so the hooks see it too.
  - `Tx`'s gains one sentence: the hooks wrap the whole call.

- [ ] **Step 1: Write the failing tests**

`txhook_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
)

type hookKey struct{}

// TestTxAroundWrapsTheCall: the first hook is the outermost; statements,
// a savepoint's included, see the call's TxInfo and the context the
// innermost hook passed, though touch runs them on the outer ctx.
func TestTxAroundWrapsTheCall(t *testing.T) {
	var log []string
	hook := func(name string) velox.Option {
		return velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
			log = append(log, name+" in")
			err := next(context.WithValue(ctx, hookKey{}, name))
			log = append(log, name+" out")
			return err
		})
	}
	var seen []*velox.TxInfo
	var from []any
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, hook("a"), hook("b"),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			seen = append(seen, st.Tx)
			from = append(from, st.Tx.Context.Value(hookKey{}))
			return next(ctx)
		}))
	err := db.Tx(ctx, func(tx *velox.DB) error {
		if err := touch(tx); err != nil {
			return err
		}
		return tx.Tx(ctx, touch) // a savepoint: no hook runs again
	}, velox.ReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a in", "b in", "b out", "a out"}; !reflect.DeepEqual(log, want) {
		t.Errorf("hooks ran %q", log)
	}
	if want := []string{"BEGIN", touchSQL, "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1", "COMMIT"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("sent %q", e.Log)
	}
	if len(seen) != 2 || seen[0] != seen[1] || seen[0] == nil || !reflect.DeepEqual(from, []any{"b", "b"}) {
		t.Fatalf("statements saw %v with contexts from %v", seen, from)
	}
	if info := seen[0]; info.ID == 0 || info.Attempt != 1 || !info.ReadOnly || info.Outcome != velox.TxCommit || len(info.Retries) != 0 {
		t.Errorf("TxInfo %+v", info)
	}
}

func TestTxAroundSeesEveryAttempt(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &velox.FakeExec{Errs: []error{serialization}}
	calls := 0
	var info *velox.TxInfo
	var attempts []int
	db := velox.OpenFake(velox.Postgres, e,
		velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
			calls++
			info = tx
			return next(ctx)
		}),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			attempts = append(attempts, st.Tx.Attempt)
			return next(ctx)
		}))
	if err := db.Tx(ctx, touch, velox.RetrySerializable(2)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(attempts, []int{1, 2}) || info.Attempt != 2 || info.Outcome != velox.TxCommit {
		t.Fatalf("hook calls %d, attempts %v, info %+v", calls, attempts, info)
	}
	if len(info.Retries) != 1 || !errors.Is(info.Retries[0].Err, serialization) || info.Retries[0].At.IsZero() {
		t.Errorf("retries %+v", info.Retries)
	}
}

func TestTxIDsAreUnique(t *testing.T) {
	var ids []uint64
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		ids = append(ids, tx.ID)
		return next(ctx)
	}))
	for range 2 {
		if err := db.Tx(ctx, func(*velox.DB) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(ids) != 2 || ids[0] == 0 || ids[0] == ids[1] {
		t.Errorf("ids %v", ids)
	}
}
```

Add `TestTxAroundRules` to the same file: a table of hooks, each run on a fresh `FakeExec` with `db.Tx(ctx, touch)` or the `fn` given.
- **A hook returning its own error** without calling `next`: that error is returned unchanged (`==`), and `e.Log` is empty.
- **A hook returning nil without `next`**: the error is `velox: transaction: a TxAround hook returned without calling next`.
- **A hook discarding `next`'s error** (`fn` returns `boom`, the hook returns nil): `velox: transaction: a TxAround hook discarded the error from next`.
- **A hook calling `next` twice**: its second call returns an error whose text contains `called next twice`, and `fn` ran once.
- **Outcomes**, each observed by a hook that reads `tx.Outcome` after `next`:
  - `fn` returns `boom`: `TxRollback`, and `boom` is returned;
  - `FakeExec.CommitErr` set: `TxError`;
  - `velox.Timeout(time.Millisecond)` with `fn` sleeping 20 ms and returning nil: `TxError`;
  - `fn` panics: `TxRollback`. The panic reaches the test's `recover` unchanged, and the hook's deferred code ran (Review Focus 3).
- **`TxOutcome.String`** for all three outcomes and for 0 (`"unknown"`).
- **`WithTxAround(nil)`** panics with `velox: WithTxAround(nil)`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'TxAround|TxIDs' .`
Expected: compile errors, because the new API is undefined.

- [ ] **Step 3: Implement** the interfaces and behaviour above.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./... && go test -run Budget -count=3 .`
Expected: PASS, every existing `tx_test.go` and `deadline_test.go` test unchanged.

- [ ] **Step 5: Commit**

```bash
git add txhook.go tx.go db.go statement.go txhook_test.go
git commit -m "TxAround hooks and TxInfo on statements in a transaction

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Core guards and documents

**Files:**
- Modify: `budget_test.go` (`TestObservedOneRowBudget`, `BenchmarkOneRowObserved`)
- Modify: `compilecost_test.go` (remeasure; `TestObservabilityIsNotGeneric`)
- Modify: `statement.go` (`Around`'s doc: the `Iter` panic note)
- Modify: `docs/specs/2026-10-14-core-design.md` (§5, §8)
- Modify: `docs/plans/2026-10-14-core-1-followups.md` (mark the sub-project 3 item resolved)

**Interfaces:**
- Consumes: `WithCallers`, `WithSQLComment`, `WithTags` (Tasks 2 and 3).
- Produces: nothing new.

- [ ] **Step 1: Write the tests**

Append to `budget_test.go`:

```go
// TestObservedOneRowBudget pins observability design §6: WithCallers and
// WithSQLComment add at most one allocation, the comment's join, to a cached
// one-row read. Its time is read from BenchmarkOneRowObserved against
// BenchmarkOneRow.
func TestObservedOneRowBudget(t *testing.T) {
	tagged := velox.WithTags(ctx, "route", "GET /invoices/{id}")
	measure := func(opts ...velox.Option) (float64, string) {
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		run := func() {
			if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(tagged); err != nil {
				t.Fatal(err)
			}
		}
		run() // fill the render, call-site and comment caches
		return testing.AllocsPerRun(200, run), e.SQL
	}
	plain, _ := measure()
	observed, text := measure(velox.WithCallers(), velox.WithSQLComment("billing", "route"))
	if !strings.Contains(text, "/*app='billing',caller='budget_test.go%3A") || !strings.HasSuffix(text, ",route='GET%20%2Finvoices%2F%7Bid%7D'*/") {
		t.Fatalf("the comment is not as expected: %s", text)
	}
	t.Logf("with callers and comments %.0f allocations, without %.0f", observed, plain)
	if observed > plain+1 {
		t.Errorf("callers and comments add %.0f allocations to a cached one-row read: over the budget of 1 (observability design §6)", observed-plain)
	}
}

func BenchmarkOneRowObserved(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, velox.WithCallers(), velox.WithSQLComment("billing", "route"))
	tagged := velox.WithTags(ctx, "route", "GET /invoices/{id}")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(tagged); err != nil {
			b.Fatal(err)
		}
	}
}
```

Append to `compilecost_test.go` (package `velox`):

```go
// TestObservabilityIsNotGeneric pins observability design success criterion
// 5: nothing in Core's observability files is instantiated per entity.
func TestObservabilityIsNotGeneric(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"tags.go", "callers.go", "comment.go", "txhook.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				if d.Type.TypeParams != nil {
					t.Errorf("%s: %s is generic", fset.Position(d.Pos()), d.Name.Name)
				}
			case *ast.TypeSpec:
				if d.TypeParams != nil {
					t.Errorf("%s: %s is generic", fset.Position(d.Pos()), d.Name.Name)
				}
			}
			return true
		})
	}
}
```

Add `go/ast`, `go/parser` and `go/token` to its imports.

- [ ] **Step 2: Run the guards**

Run: `go test -run 'Budget|ObservabilityIsNotGeneric|GenericTypesCompileSmall' -count=1 -v . 2>&1 | grep -E 'allocations|bytes|FAIL|ok'`
Expected: PASS. The core budget tests log the same numbers as before this plan, and the observed test logs at most +1.

If a `genericObjects` row (expect `Statement`, `entity` and `entityUse`) now measures above its `max`:
- Find the cause. The new `Statement` fields reach `Caller`, `Tag`, `Result`, `TxInfo`, `TxRetry`, `sql.IsolationLevel` and `time.Time`. Expect a small growth, from export data only.
- Update that row's measured comment and `max` with the rule in the comment above the table. Write the date and "after observability" beside it.
- A growth above 4 KiB on any row is a finding: report it rather than raise the bound.

- [ ] **Step 3: Measure and record the time**

Run: `go test -run '^$' -bench 'OneRow$|OneRowObserved' -count 6 . | tee /dev/stderr | grep -E 'Benchmark'`

Take the medians. The difference is velox's added time for callers and comments. In `docs/specs/2026-10-14-core-design.md` §8, add a row to the second table: "One-row query, WithCallers and WithSQLComment | <ns> ns, <n> allocations |". Write the same figures into the observability spec in Task 8 (§6).

If the difference exceeds 300 ns, profile it (`-cpuprofile`), cut the cost, and say so in the commit. The usual causes are:
- unwinding too many frames per batch;
- taking the read lock once per PC rather than once per batch.

- [ ] **Step 4: Update the documents**
  - **`Around`'s doc comment.** Add: "With Iter the caller's loop body runs inside the hooks, so a hook that recovers a panic and returns normally makes Go report that a range function recovered a loop body panic: let panics through." This is the follow-up item under "Sub-project 3 (observability)".
  - **Core design §5:**
    - the `Statement` block gains `Dialect`, `Tags`, `Caller`, `Tx`, `Fingerprint` and `Result`, each with its comment;
    - the render stage's row says "and the fingerprint, stored in the render cache; observability appends the SQL comment after rendering";
    - add a sentence on `velox.WithTxAround`;
    - delete "the statement's `Tags` field arrive with sub-projects 4 and 3" (it has arrived) and keep the route stage's sentence.
  - **`core-1-followups.md`.** Replace the "Sub-project 3 (observability)" item with "Resolved in observability: `Around`'s doc says that `Iter`'s loop body runs inside the hooks, so a hook must not recover a panic and return normally."

- [ ] **Step 5: Run everything once more and commit**

Run: `go test -race ./... && (cd cmd/velox-gen && go test ./...) && go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json`
Expected: PASS.

```bash
git add budget_test.go compilecost_test.go statement.go docs/specs/2026-10-14-core-design.md docs/plans/2026-10-14-core-1-followups.md
git commit -m "Budget and compile-cost guards for observability; Core design and Around docs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: contrib/otel: the module and statement spans

**Files:**
- Create: `contrib/otel/go.mod`, `contrib/otel/go.sum`
- Create: `contrib/otel/doc.go` (package comment)
- Create: `contrib/otel/config.go` (`Config`, `Options`, `Around`, `adapter`, `newAdapter`)
- Create: `contrib/otel/attrs.go` (attribute-name constants, `dbSystem`, `errorType`)
- Create: `contrib/otel/statement.go` (`adapter.around`)
- Create: `contrib/otel/fakedb_test.go`, `contrib/otel/statement_test.go`, `contrib/otel/errortype_internal_test.go`, `contrib/otel/deps_test.go`
- Modify: `scripts/ci-local.sh`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes:
  - `velox.Statement` with `Dialect`, `Fingerprint`, `Caller`, `Tags`, `Result`, `Tx`, `Tenant`, `TenantBypass`, `PolicyBypass`, `Count`;
  - `velox.WithCallers`, `velox.WithSQLComment`, `velox.WithAround`, `velox.TxInfo`, `velox.ErrTimeout` and the other sentinels, `velox.ConstraintError` (Tasks 1–4).
- Produces:

```go
package veloxotel

type Config struct {
	TracerProvider trace.TracerProvider // nil: otel.GetTracerProvider()
	MeterProvider  metric.MeterProvider // nil: otel.GetMeterProvider()
	// App names the application in the SQL comment; empty: no comment.
	App string
	// CommentTags are the velox.WithTags keys the SQL comment carries, in
	// this order. List only tags whose values are stable per code path (a
	// route template, never an ID): see velox.WithSQLComment.
	CommentTags       []string
	OmitQueryText     bool // leave db.query.text off spans
	RecordTenant      bool // add velox.tenant to spans
	MetricFingerprint bool // add velox.fingerprint to the statement metrics (Task 7)
}

// Options returns the velox options that trace (and, from Task 7, measure)
// every statement: WithCallers, WithAround, WithTxAround (Task 7), and
// WithSQLComment when App is set. Place them after an Around hook of your
// own that retries, to get one span per attempt.
func Options(cfg Config) []velox.Option
func Around(cfg Config) velox.Around

type adapter struct {
	cfg    Config
	tracer trace.Tracer
	// Task 7 adds the instruments.
}
const scope = "github.com/syssam/velox2/contrib/otel" // the tracer's and meter's name
func newAdapter(cfg Config) *adapter
func (a *adapter) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error

// attrs.go: unexported string constants for every name in spec §2 (Ruling 12
// for code.*), and:
func dbSystem(d velox.Dialect) string                 // "postgresql", "mysql", "sqlite"
func operation(st *velox.Statement) string            // "SELECT", "INSERT", "UPDATE", "DELETE"
func collection(st *velox.Statement) string           // Entity.Table, "<Schema>.<Table>" when Schema is set
func errorType(ctx context.Context, err error) string // spec §2.1's list, Ruling 14
```

**Behaviour of `around`** (spec §2.1):
- **The parent.** It is `trace.ContextWithSpan(ctx, trace.SpanFromContext(st.Tx.Context))` when `st.Tx != nil && st.Tx.Context != nil` and that span's context is valid. Otherwise it is `ctx`.
- **The span.** `a.tracer.Start(parent, operation(st)+" "+collection(st), trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(…))`. The attributes are those the spec's table lists, except `Result`'s. `velox.count=true` is added for `st.Count`, and `velox.tag.<key>` for each tag.
- **Running.** It calls `err := next(sctx)` with the span's context.
- **After `next`:**
  - `db.response.returned_rows = st.Result.Rows` for `OpSelect`;
  - `velox.affected_rows = st.Result.Affected` for writes, when `>= 0`.
- **On an error:**
  - `span.SetStatus(codes.Error, err.Error())`;
  - `span.SetAttributes(attribute.String("error.type", errorType(ctx, err)))`;
  - `span.RecordError(err)`.

  Then `span.End()`, and return `err` unchanged.
- **Panics** (Ruling 16). A deferred function ends a span that was not ended, with `codes.Error` and the description `panic`, without recovering.

`errorType`, in order:
1. `errors.Is` `ErrDenied` → `velox.denied`; `ErrNoTenant` → `velox.no_tenant`; `ErrUnbounded`, `ErrNoWhere` or `ErrLimitTooLarge` → `velox.unbounded`; `ErrTooManyRows` → `velox.too_many_rows`.
2. `velox.timeout` when `errors.Is(err, velox.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) && errors.Is(context.Cause(ctx), velox.ErrTimeout)`.
3. `errors.As` `*velox.ConstraintError` → `constraint.unique`, `constraint.foreign_key`, `constraint.not_null` or `constraint.check` by `Kind`.
4. `context.Canceled` → `context.canceled`; `context.DeadlineExceeded` → `context.deadline_exceeded`.
5. Otherwise `_OTHER`.

`contrib/otel/go.mod`:

```
module github.com/syssam/velox2/contrib/otel

go 1.27.0

require (
	github.com/syssam/velox2 v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/metric v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/metric v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
)

replace github.com/syssam/velox2 => ../..
```

Then `cd contrib/otel && go mod tidy`. `github.com/jackc/pgx/v5` becomes direct once the tests import `pgconn`.

`contrib/otel/fakedb_test.go`, a `database/sql` driver the tests script:

```go
package veloxotel_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"time"
)

// fakeDB is a database/sql connector whose every query returns rows (with
// the columns cols, testmodel.Invoice's when nil) and whose every exec
// reports affected. errs are returned by the next calls, BEGIN included, one
// each; a nil entry lets its call succeed. With block, calls wait for their
// context to end.
type fakeDB struct {
	mu        sync.Mutex
	rows      [][]driver.Value
	cols      []string
	errs      []error
	affected  int64
	commitErr error
	block     bool
	log       []string
}

func (f *fakeDB) Connect(context.Context) (driver.Conn, error) { return &fakeConn{f}, nil }
func (f *fakeDB) Driver() driver.Driver                        { return fakeDriver{} }

// open returns f as a *sql.DB.
func (f *fakeDB) open() *sql.DB { return sql.OpenDB(f) }

func (f *fakeDB) call(ctx context.Context, what string) error {
	if f.block {
		<-ctx.Done()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, what)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return nil
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("fake: open it with sql.OpenDB") }

type fakeConn struct{ f *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fake: no prepared statements") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return c.BeginTx(context.Background(), driver.TxOptions{}) }

func (c *fakeConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := c.f.call(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	return fakeTx{c.f}, nil
}

func (c *fakeConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := c.f.call(ctx, q); err != nil {
		return nil, err
	}
	return &fakeRows{data: c.f.rows, cols: c.f.cols}, nil
}

func (c *fakeConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if err := c.f.call(ctx, q); err != nil {
		return nil, err
	}
	return driver.RowsAffected(c.f.affected), nil
}

type fakeTx struct{ f *fakeDB }

func (t fakeTx) Commit() error {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.f.log = append(t.f.log, "COMMIT")
	return t.f.commitErr
}

func (t fakeTx) Rollback() error {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.f.log = append(t.f.log, "ROLLBACK")
	return nil
}

type fakeRows struct {
	data [][]driver.Value
	cols []string
	i    int
}

// Columns names cols, or testmodel.Invoice's columns when cols is nil.
func (r *fakeRows) Columns() []string {
	if r.cols != nil {
		return r.cols
	}
	return []string{"created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "note", "data"}
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}

var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// invoiceRow is one testmodel.Invoice row.
func invoiceRow(id int64) []driver.Value {
	return []driver.Value{t0, t0, id, int64(1), "INV", 12.5, true, nil, nil, []byte("d")}
}
```

The tests open `velox.Open(velox.Postgres, f.open(), …)`. The dialect only shapes the SQL. Constraint and serialization errors are `*pgconn.PgError` values in `f.errs`.

- [ ] **Step 1: Write the failing tests**

`contrib/otel/statement_test.go`:

```go
package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"runtime"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = velox.AllTenants(context.Background(), "otel tests")
	I   = testmodel.Invoices
)

// traced returns a DB on f traced into an in-memory exporter, with opts
// given before veloxotel's options.
func traced(t *testing.T, f *fakeDB, cfg veloxotel.Config, opts ...velox.Option) (*velox.DB, *tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	cfg.TracerProvider = tp
	return velox.Open(velox.Postgres, f.open(), append(opts, veloxotel.Options(cfg)...)...), exp, tp
}

func attrs(s tracetest.SpanStub) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes {
		m[kv.Key] = kv.Value
	}
	return m
}

func here() int { _, _, line, _ := runtime.Caller(1); return line }

func TestSelectSpan(t *testing.T) {
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1), invoiceRow(2)}}
	db, exp, _ := traced(t, f, veloxotel.Config{App: "billing", CommentTags: []string{"route"}})
	c := velox.WithTags(ctx, "route", "GET /invoices", "team", "billing")
	line := here() + 1
	if _, err := db.Query[testmodel.Invoice]().Where(I.Number.EQ("x")).Limit(5).All(c); err != nil {
		t.Fatal(err)
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("%d spans", len(spans))
	}
	s := spans[0]
	if s.Name != "SELECT invoices" || s.SpanKind != trace.SpanKindClient || s.Status.Code != codes.Unset {
		t.Errorf("span %q kind %v status %v", s.Name, s.SpanKind, s.Status)
	}
	a := attrs(s)
	for k, want := range map[attribute.Key]attribute.Value{
		"db.system.name":            attribute.StringValue("postgresql"),
		"db.operation.name":         attribute.StringValue("SELECT"),
		"db.collection.name":        attribute.StringValue("invoices"),
		"db.response.returned_rows": attribute.Int64Value(2),
		"velox.entity":              attribute.StringValue("testmodel.Invoice"),
		"velox.tenant_bypass":       attribute.StringValue("otel tests"),
		"velox.tag.route":           attribute.StringValue("GET /invoices"),
		"velox.tag.team":            attribute.StringValue("billing"),
		"code.file.path":            attribute.StringValue("statement_test.go"),
		"code.line.number":          attribute.Int64Value(int64(line)),
		"code.function.name":        attribute.StringValue("github.com/syssam/velox2/contrib/otel_test.TestSelectSpan"),
	} {
		if a[k] != want {
			t.Errorf("%s = %v, want %v", k, a[k].Emit(), want.Emit())
		}
	}
	if fp := a["velox.fingerprint"].AsString(); len(fp) != 16 {
		t.Errorf("velox.fingerprint %q", fp)
	}
	if q := a["db.query.text"].AsString(); q != f.log[0] {
		t.Errorf("db.query.text %q, sent %q", q, f.log[0])
	}
	for _, k := range []attribute.Key{"velox.affected_rows", "velox.count", "velox.tenant", "velox.policy_bypass", "error.type"} {
		if _, ok := a[k]; ok {
			t.Errorf("unexpected %s", k)
		}
	}
}
```

Further tests in `statement_test.go`:
- **Every operation.** Through the same helper:
  - `Count` → `SELECT invoices` with `velox.count=true` and `db.response.returned_rows=1`;
  - `Insert` of an `Invoice` → `INSERT invoices`, with `velox.affected_rows=1` and `db.response.returned_rows` absent. On PostgreSQL the insert reads the key back with `RETURNING`, so set `f.cols = []string{"id"}` and `f.rows = [][]driver.Value{{int64(9)}}` for this case, and reset both after it;
  - `Update` with `f.affected = 3` → `UPDATE invoices` with `velox.affected_rows=3`;
  - `Delete` → `DELETE invoices`.
- **Schema-qualified collection.** A `Rewrite` hook that sets `info := *st.Entity; info.Schema = "billing"; st.Entity = &info` names the span `SELECT billing.invoices`.
- **`OmitQueryText`** leaves `db.query.text` off.
- **`RecordTenant`** under `velox.WithTenant(context.Background(), int64(7))` gives `velox.tenant = "7"`.
- **`SkipPolicy`.** On `testmodel.Ticket` with `velox.SkipPolicy(ctx, "support")`: `velox.policy_bypass = "support"`.
- **No span from `SQL(ctx)`.** `exp.GetSpans()` stays empty.
- **Error types through the hook.** Each case: status Error, the `error.type` given, and an `exception` event whose `exception.message` is the error's text.
  - `f.errs = []error{&pgconn.PgError{Code: "23505", ConstraintName: "invoices_org_number_uq"}}` → `constraint.unique`;
  - `velox.WithMaxRows(2)` with 3 rows → `velox.too_many_rows`;
  - a canceled context → `context.canceled`;
  - a context whose deadline passed → `context.deadline_exceeded`;
  - `velox.WithDeadlines(velox.Deadlines{Statement: time.Millisecond})` with `f.block = true` → `velox.timeout`;
  - `errors.New("boom")` → `_OTHER`.
- **One span per attempt.** An `Around` hook of the test's own, given before `veloxotel.Options`, retries once when `next` fails. With `f.errs = []error{boom}` that makes two spans: the first with `_OTHER`, the second without an error.
- **The parent.** Use a test `TxAround` that starts a span `"tx"` with `tp.Tracer("t")` and passes its context to `next`. A statement run inside `fn` on the outer `ctx`, which holds no span, has the `"tx"` span as parent. Outside a transaction, under a span `"req"` that the test started, its parent is `"req"`.
- **`Iter` panics** (Review Focus 3). A loop body that panics: the span ends with status Error and the description `panic`, and the test's `recover` gets the panic value.

`contrib/otel/errortype_internal_test.go` (package `veloxotel`) maps directly:
- `fmt.Errorf("velox: X: %w", velox.ErrDenied)` → `velox.denied`; likewise `ErrNoTenant`, `ErrUnbounded`, `ErrNoWhere`, `ErrLimitTooLarge`, `ErrTooManyRows` and `ErrTimeout`;
- `context.DeadlineExceeded` with a context made by `context.WithDeadlineCause(bg, past, velox.ErrTimeout)` → `velox.timeout`;
- each `ConstraintKind`;
- `velox.ErrTenantMismatch` → `_OTHER`;
- a `*velox.PolicyError` (it `Is` `ErrDenied`) → `velox.denied`;
- `dbSystem` for each dialect, and `collection` with and without a schema.

`contrib/otel/deps_test.go`:

```go
package veloxotel_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoSDKDependency pins spec §2: the adapter needs the OpenTelemetry API
// only. The SDK may appear in tests, never in the package.
func TestNoSDKDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "go.opentelemetry.io/otel/sdk") || strings.Contains(p, "semconv") {
			t.Errorf("veloxotel imports %s", p)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd contrib/otel && go test ./...`
Expected: compile errors, because `veloxotel.Options` and `Config` are undefined.

- [ ] **Step 3: Implement** `doc.go`, `config.go`, `attrs.go` and `statement.go`.

In this task, `Options` returns `WithCallers`, `WithAround(a.around)` and, when `App` is set, `WithSQLComment(cfg.App, cfg.CommentTags...)`.

The package comment (`doc.go`) says:
- what `Options` installs, and that `Around` is available alone;
- that statement spans in a transaction are children of the `velox.tx` span, whatever context the code passes;
- that refusals before execution make no statement span;
- the pgx cache reason for `CommentTags`.

- [ ] **Step 4: Wire CI**

In `scripts/ci-local.sh`, after the example's block:

```bash
# contrib/otel is its own module: its own vet, lint and tests. Its PostgreSQL
# test runs under VELOX_TEST_POSTGRES, exported above.
(
  cd contrib/otel
  test -z "$(gofmt -l .)" || { echo "gofmt needed in contrib/otel:"; gofmt -l .; exit 1; }
  go vet ./...
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
  go test -race -count=1 ./...
)
```

In `.github/workflows/ci.yml`, after the `example` step:

```yaml
      - name: contrib/otel
        working-directory: contrib/otel
        run: |
          test -z "$(gofmt -l .)"
          go vet ./...
          go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
          go test -race -count=1 ./...
```

- [ ] **Step 5: Run the tests**

Run: `(cd contrib/otel && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./... && go test -race -count=1 ./...) && scripts/ci-local.sh`
Expected: PASS, and `ci-local.sh` exits 0.

- [ ] **Step 6: Commit**

```bash
git add contrib/otel scripts/ci-local.sh .github/workflows/ci.yml
git commit -m "contrib/otel: statement spans; CI runs the module

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: contrib/otel: the transaction span and metrics

**Files:**
- Create: `contrib/otel/tx.go` (`adapter.txAround`, `TxAround`)
- Create: `contrib/otel/metrics.go` (the instruments; `adapter.record`)
- Modify: `contrib/otel/config.go` (`Options` adds `WithTxAround`; `newAdapter` creates the meter and instruments)
- Modify: `contrib/otel/statement.go` (records the statement metrics)
- Test: `contrib/otel/tx_test.go`, `contrib/otel/metrics_test.go`

**Interfaces:**
- Consumes: `velox.TxAround`, `velox.TxInfo` (with `Retries`, `Outcome`), `velox.WithTxAround`, `velox.TxOutcome.String` (Task 4); `errorType`, `adapter` (Task 6).
- Produces:

```go
func TxAround(cfg Config) velox.TxAround
func (a *adapter) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error

// metrics.go
// adapter gains:
//   duration metric.Float64Histogram // db.client.operation.duration, "s"
//   returned metric.Int64Histogram   // db.client.response.returned_rows, "{row}"
//   txDuration metric.Float64Histogram // velox.tx.duration, "s"
var durationBounds = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
var rowBounds = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536}
func (a *adapter) record(ctx context.Context, st *velox.Statement, seconds float64, err error)
```

**Behaviour:**
- **`txAround`** (spec §2.2, Rulings 15 and 16):
  - start `velox.tx` (`trace.SpanKindInternal`) with `velox.tx.isolation` and `velox.tx.read_only`;
  - call `next` with the span's context;
  - then, for each `tx.Retries`, add `span.AddEvent("retry", trace.WithTimestamp(r.At), trace.WithAttributes(attribute.String("error.type", errorType(ctx, r.Err))))`;
  - set `velox.tx.attempts = tx.Attempt` and `velox.tx.outcome = tx.Outcome.String()`;
  - on an error, set status Error, `error.type`, and `RecordError`;
  - end the span, and record `velox.tx.duration` with `velox.tx.outcome`, `velox.tx.read_only` and `velox.tx.retried = tx.Attempt > 1`;
  - return `next`'s error unchanged.

  A deferred function ends the span on a panic, with outcome `rollback`, status Error and the description `panic`.
- **`record`**, called by `around` after `next` returns (not on a panic):
  - `duration` with `db.system.name`, `db.operation.name`, `db.collection.name`, `velox.entity`, `error.type` (when `err != nil`), and `velox.fingerprint` only with `MetricFingerprint`;
  - for `OpSelect` only, `returned` with the same attributes and the value `st.Result.Rows`.
- **Instruments.** They are created in `newAdapter` with `metric.WithUnit` and `metric.WithExplicitBucketBoundaries`. A creation error goes to `otel.Handle(err)` (Ruling 17).
- **`Options`** now returns `WithCallers`, `WithAround`, `WithTxAround`, and `WithSQLComment` when `App` is set.

- [ ] **Step 1: Write the failing tests**

`contrib/otel/tx_test.go`:

```go
package veloxotel_test

import (
	"database/sql/driver"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/internal/testmodel"
)

func byName(spans tracetest.SpanStubs) map[string][]tracetest.SpanStub {
	m := map[string][]tracetest.SpanStub{}
	for _, s := range spans {
		m[s.Name] = append(m[s.Name], s)
	}
	return m
}

// TestTxSpanTree: spec success criterion 1. fn runs its statements on the
// outer ctx, which carries the request's span, not the transaction's; they
// still nest under velox.tx, and velox.tx under the request.
func TestTxSpanTree(t *testing.T) {
	// Attempt 1: BEGIN, the SELECT, then the UPDATE fails on a serialization
	// failure; attempt 2 runs all three cleanly.
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}, affected: 1, errs: []error{nil, nil, &pgconn.PgError{Code: "40001"}}}
	db, exp, tp := traced(t, f, veloxotel.Config{})
	req, root := tp.Tracer("t").Start(ctx, "POST /pay")
	err := db.Tx(req, func(tx *velox.DB) error {
		if _, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(req); err != nil {
			return err
		}
		_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(req)
		return err
	}, velox.RetrySerializable(1))
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	spans := byName(exp.GetSpans())
	tx := spans["velox.tx"]
	if len(tx) != 1 || tx[0].Parent.SpanID() != root.SpanContext().SpanID() {
		t.Fatalf("velox.tx spans %v", tx)
	}
	a := attrs(tx[0])
	if a["velox.tx.attempts"] != attribute.IntValue(2) || a["velox.tx.outcome"] != attribute.StringValue("commit") ||
		a["velox.tx.isolation"] != attribute.StringValue("Default") || a["velox.tx.read_only"] != attribute.BoolValue(false) {
		t.Errorf("velox.tx attributes %v", tx[0].Attributes)
	}
	if ev := tx[0].Events; len(ev) != 1 || ev[0].Name != "retry" || !ev[0].Time.Before(tx[0].EndTime) {
		t.Errorf("events %v", ev)
	}
	if len(spans["SELECT invoices"]) != 2 || len(spans["UPDATE invoices"]) != 2 {
		t.Fatalf("statement spans: %d SELECT, %d UPDATE", len(spans["SELECT invoices"]), len(spans["UPDATE invoices"]))
	}
	for _, s := range append(spans["SELECT invoices"], spans["UPDATE invoices"]...) {
		if s.Parent.SpanID() != tx[0].SpanContext.SpanID() {
			t.Errorf("%s's parent is not velox.tx", s.Name)
		}
	}
	if failed := spans["UPDATE invoices"][0]; attrs(failed)["error.type"] != attribute.StringValue("_OTHER") {
		t.Errorf("the failed UPDATE: %v", failed.Attributes)
	}
}
```

Further tests in `tx_test.go`:
- **Outcomes.**
  - `fn` returning `boom`: outcome `rollback`, status Error, `error.type=_OTHER`;
  - `f.commitErr` set: outcome `error`;
  - `fn` returning the error of `tx.Query[testmodel.Ticket]().Limit(1).All(ctx)`, which `Ticket`'s policy denies because `ctx` names no user (`testmodel.AsUser` is not set), gives `error.type=velox.denied` on `velox.tx`. No `SELECT tickets` span exists, because the refusal comes before execution.
- **`TestPanicsEndTheSpans`** (Review Focus 3).
  - `fn` panics: the `velox.tx` span is exported with outcome `rollback` and status Error `panic`, and the panic reaches the test's `recover`.
  - Likewise an `Iter` loop body inside `fn`: both its statement span and `velox.tx` end.
- **A savepoint** (`tx.Tx(...)` inside `fn`) adds no second `velox.tx` span.

`contrib/otel/metrics_test.go` uses `sdkmetric.NewManualReader()` and `sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))`. A helper `points(t, r, name) []metricdata.HistogramDataPoint[float64]` (and an `int64` variant) collects with `r.Collect(context.Background(), &rm)`. The tests:
- **`TestStatementMetrics`.** `All` (2 rows), `Count`, `Update`, and one failing `All` (`boom`) give:
  - `db.client.operation.duration` points whose attribute sets are exactly `{db.system.name, db.operation.name, db.collection.name, velox.entity}`, plus `error.type=_OTHER` for the failure;
  - a total count of 4;
  - `db.client.response.returned_rows`, for SELECTs only, with a sum of 2 + 1 + 0 = 3;
  - the units `s` and `{row}`;
  - the bounds `durationBounds` and `rowBounds` (from `HistogramDataPoint.Bounds`).
- **`TestMetricsAttributesAreBounded`** (Review Focus 5). With `RecordTenant: true`, tags set, an `App` comment, and queries under two tenants and two route values, no point on any instrument carries `velox.tenant`, a `velox.tag.*`, `db.query.text`, `code.*` or `velox.fingerprint`.
- **`MetricFingerprint: true`** adds `velox.fingerprint` to both statement instruments. Two shapes make two points.
- **`velox.tx.duration`.** A committed transaction and a retried one give the attribute sets `{velox.tx.outcome=commit, velox.tx.read_only=false, velox.tx.retried=false}` and `{…retried=true}`.

- [ ] **Step 2: Run them to see them fail**

Run: `cd contrib/otel && go test -run 'Tx|Panics|Metric' ./...`
Expected: FAIL. There is no `velox.tx` span, and no metric is recorded.

- [ ] **Step 3: Implement** `tx.go`, `metrics.go`, and the changes to `config.go` and `statement.go`.

- [ ] **Step 4: Benchmark the adapter** (spec §6: reported, not budgeted)

Add `contrib/otel/bench_test.go`:
- `BenchmarkAdapter`: a one-row `All` on the fake driver with `Options` and an SDK tracer provider whose span processor is `sdktrace.NewSimpleSpanProcessor` over a discarding exporter (`tracetest.NewNoopExporter()`), plus a meter provider with a manual reader;
- `BenchmarkBareSpan`: the same query with a hand-written `Around` that only starts and ends one span on the same provider.

The difference is the adapter's own cost. Write both medians of `go test -run '^$' -bench . -count 6` into the commit message. Task 8 copies them into spec §6.

- [ ] **Step 5: Run the tests**

Run: `cd contrib/otel && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./... && go test -race -count=1 ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

The commit message is the subject "contrib/otel: the transaction span and the metrics", a body with the two benchmark medians from Step 4 (ns/op and allocs/op each), and the trailer:

```bash
git add contrib/otel
git commit -F- <<'EOF'
contrib/otel: the transaction span and the metrics

BenchmarkAdapter: … ns/op, … allocs/op; BenchmarkBareSpan: … ns/op, … allocs/op (medians of 6, filled in from Step 4).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 8: The example's tracing section, the real-database test, documents

**Files:**
- Create: `example/tracing.go`
- Modify: `example/main.go` (call `tracing` as step 10), `example/go.mod`, `example/go.sum`, `example/README.md`, `example/testdata/output.golden`
- Create: `contrib/otel/pg_test.go`
- Modify: `docs/specs/2026-10-15-observability-design.md`, `docs/specs/2026-10-14-core-design.md`, `docs/plans/2026-10-14-core-1-followups.md`

**Interfaces:**
- Consumes: `veloxotel.Options`, `veloxotel.Config` (Tasks 6 and 7); `velox.WithTags` (Task 2).
- Produces: nothing new.

- [ ] **Step 1: Add the example's tracing section**

`example/go.mod`:
- add `require github.com/syssam/velox2/contrib/otel v0.0.0-00010101000000-000000000000` and `replace github.com/syssam/velox2/contrib/otel => ../contrib/otel`;
- add the SDK requirements (`go.opentelemetry.io/otel/sdk v1.47.0`, `go.opentelemetry.io/otel/sdk/metric v1.47.0`);
- run `cd example && go mod tidy`.

`example/tracing.go`:

```go
package main

// The imports the code below uses; add those printSpans and printMetrics
// need (fmt, slices, strings, attribute).
import (
	"context"
	"database/sql"
	"io"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/example/billing"
)

// tracing pays an invoice in a transaction on a DB traced by contrib/otel,
// exporting to memory, and prints the span tree and the metric points. It
// prints no span IDs, times or durations, so its output is deterministic.
func tracing(acct context.Context, w io.Writer, sqlDB *sql.DB) error {
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	db := velox.Open(velox.SQLite, sqlDB, veloxotel.Options(veloxotel.Config{
		TracerProvider: tp,
		MeterProvider:  mp,
		App:            "billing",
		CommentTags:    []string{"route"},
	})...)
	I := billing.Invoices

	// Middleware would set the route tag and start the request's span.
	req := velox.WithTags(acct, "route", "POST /invoices/{number}/pay")
	req, span := tp.Tracer("example").Start(req, "POST /invoices/{number}/pay")
	err := db.Tx(req, func(tx *velox.DB) error {
		inv, err := tx.Query[billing.Invoice]().Where(I.Number.EQ("INV-004")).One(req)
		if err != nil {
			return err
		}
		inv.Status = billing.Paid
		return tx.Save(req, inv, I.Status)
	})
	span.End()
	if err != nil {
		return err
	}
	printSpans(w, spans.GetSpans())
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		return err
	}
	printMetrics(w, rm)
	return nil
}
```

**`printSpans`** prints the tree from `Parent.SpanID()`, children in `StartTime` order, indented two spaces per level. Each line is the span's name, then:
- for `velox.tx`: `isolation=… read_only=… attempts=… outcome=…`;
- for a statement: `fingerprint=<velox.fingerprint> caller=<code.file.path>:<code.line.number>`, and on the next line, indented, the `db.query.text` sent.

**`printMetrics`** prints, per instrument in name order:
- its name and unit;
- then one line per data point, sorted by its attribute set's `Encoded(attribute.DefaultEncoder())`: the attributes as `k=v` in key order, then `count=<Count>`;
- for `db.client.response.returned_rows`, also `sum=<Sum>`.

It never prints durations, times or IDs.

In `main.go`, before `return nil`:

```go
	// 10. Tracing with contrib/otel: spans, call sites, the SQL comment and
	// metrics (example/tracing.go).
	say("\n== 10. Tracing")
	return tracing(acct, w, sqlDB)
```

Update `example/README.md`:
- "What it shows" gains item 10, for tracing with `contrib/otel` on an in-memory exporter;
- "Layout" mentions `tracing.go`.

Run: `cd example && go test -run TestWalkthrough -update . && go test -race -count=1 ./... && git diff testdata/output.golden`

Read the new section and check it:
- a tree `POST /invoices/{number}/pay` → `velox.tx` → `SELECT invoices`, `UPDATE invoices`;
- `outcome=commit attempts=1`;
- 16-character fingerprints;
- `caller=tracing.go:<line>` for both statements;
- sent texts ending ` /*app='billing',caller='tracing.go%3A<line>',route='POST%20%2Finvoices%2F%7Bnumber%7D%2Fpay'*/`;
- duration points with `db.system.name=sqlite`, and `returned_rows` with `sum=1` for the SELECT;
- no number that changes between runs.

Run `go test -count=3 ./...` to confirm.

- [ ] **Step 2: Add the real-PostgreSQL test**

`contrib/otel/pg_test.go`:

```go
package veloxotel_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/internal/testmodel"
)

const pgInvoices = `CREATE TABLE velox_otel.invoices (created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
	id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, org_id bigint NOT NULL, number text NOT NULL,
	total double precision NOT NULL, paid boolean NOT NULL, paid_at timestamptz, note text, data bytea NOT NULL)`

// TestPgxPreparesACommentedStatementOnce: spec success criterion 3. The SQL
// comment is stable per call site, so pgx's statement cache prepares a
// call site's statement once; another call site is another statement, and a
// tag the comment does not list changes nothing.
func TestPgxPreparesACommentedStatementOnce(t *testing.T) {
	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("VELOX_TEST_POSTGRES is not set")
	}
	bg := context.Background()
	admin, err := pgx.Connect(bg, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(bg) }()
	for _, q := range []string{"DROP SCHEMA IF EXISTS velox_otel CASCADE", "CREATE SCHEMA velox_otel", pgInvoices} {
		if _, err := admin.Exec(bg, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1 // pg_prepared_statements is per session
	cfg.ConnConfig.RuntimeParams["search_path"] = "velox_otel"
	pool, err := pgxpool.NewWithConfig(bg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tp := sdktrace.NewTracerProvider()
	db := velox.OpenPgx(pool, veloxotel.Options(veloxotel.Config{TracerProvider: tp, App: "otel-test", CommentTags: []string{"route"}})...)
	prepared := func() (n int) {
		t.Helper()
		// The simple protocol, so that this query is not prepared itself.
		err := pool.QueryRow(bg, `SELECT count(*) FROM pg_prepared_statements WHERE statement LIKE '%FROM "invoices"%/*app=%'`,
			pgx.QueryExecModeSimpleProtocol).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	route := velox.WithTags(ctx, "route", "GET /invoices/{id}")
	for i := range 3 { // one call site, three times, each with another unlisted tag
		c := velox.WithTags(route, "user", string(rune('a'+i)))
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(c); err != nil {
			t.Fatal(err)
		}
	}
	if n := prepared(); n != 1 {
		t.Errorf("one call site prepared %d statements, want 1", n)
	}
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(route); err != nil {
		t.Fatal(err)
	}
	if n := prepared(); n != 2 {
		t.Errorf("two call sites prepared %d statements, want 2", n)
	}
}
```

Run: `scripts/ci-local.sh`
Expected: exit 0. This test runs, rather than skips, in the `contrib/otel` block; check with `cd contrib/otel && VELOX_TEST_POSTGRES=… go test -run Pgx -v .`.

- [ ] **Step 3: Update the documents**

**`docs/specs/2026-10-15-observability-design.md`.** Record this plan's rulings in place:
- §1.1: the fingerprint is stored in the render cache, which counts its bytes.
- §1.2: Ruling 6. Callers are read in batches of 8 up to 64 frames; the cache holds 4,096 PCs per DB; the `File` rule; `WithCallerSkip` implies `WithCallers`.
- §1.3: Rulings 7, 8, 10 and 11. Correct the example's caller to `caller='billing%2Finvoice.go%3A42'`.
- §1.4: Ruling 9 (odd count) and the shared slice.
- §1.5: Ruling 5.
- §1.6: Rulings 1–3, with `TxRetry` and `TxOutcome` in the code block.
- §1.7: add `Dialect`.
- §2.1: Rulings 12, 14 and 16.
- §2.2: Ruling 15.
- §2.4: v1.47.0.
- §3: `tracing.go`.
- §6: the measured figures from Task 5 Step 3 and Task 7 Step 4, and Ruling 13.

**`docs/specs/2026-10-14-core-design.md`.** In §5's table, the execute stage's "Spans and fingerprints (sub-project 3)" becomes "Spans and metrics (`contrib/otel`)". The render row already says fingerprint and comment (Task 5).

**`docs/plans/2026-10-14-core-1-followups.md`.** Append:

```markdown
## Left by observability

API rough spots found while building `./example`:
- `WithTenant` needs the tenant column's exact Go type: on an `int64` column, `WithTenant(ctx, 1)` (an untyped constant, so an `int`) fails at run time with `ErrTenantMismatch`; nothing catches it at compile time.
- `InsertAll` reads no generated keys back, so code that inserts parents and then their children must read the parents' IDs again.
- There is no `Sum`, `Avg` or `GROUP BY`: the example totals revenue per organization in Go, over `Iter`.
- A policy function that only allows or denies must still return a zero `Pred[E]{}` beside its error.
- The core package imports pgx, so an application that uses only SQLite or MySQL still links pgx.
- On SQLite, `ConstraintError.Constraint` holds the columns (`invoices.org_id, invoices.number`), not the constraint's name.
- The function given to `Tx` gets no context, so code inside it uses the outer one. `Statement.Tx` and `contrib/otel`'s parenting on `Tx.Context` work around it for spans only.

Left by the observability plan:
- §6's 300 ns is read from benchmarks and recorded, not asserted.
- A serialization failure or deadlock that `RetrySerializable` retries has `error.type` `_OTHER`; there is no value of its own.
- A statement refused before execution (guard, scope, policy, hooks) makes no statement span and no metric point; it shows only as the error of a transaction it ends.
- Inside a transaction, a statement span's parent is always `velox.tx`, even when the application started a span of its own inside `fn` and passed its context.
- A statement with no application frame within 64 frames (an `Iter` driven through `iter.Pull`, say) has a zero `Caller`.
- The `TxInfo` allocated per transaction when hooks are configured has no budget test.
```

Add anything else the task reviews left open, in the same form.

- [ ] **Step 4: Run everything**

Run: `scripts/ci-local.sh`
Expected: exit 0. This covers the root module, `cmd/velox-gen`, `example` and `contrib/otel`, each with gofmt, vet, lint and `-race` tests, and the real-PostgreSQL test.

- [ ] **Step 5: Commit**

```bash
git add example contrib/otel/pg_test.go docs/specs/2026-10-15-observability-design.md docs/specs/2026-10-14-core-design.md docs/plans/2026-10-14-core-1-followups.md
git commit -m "Example tracing section, pgx statement-cache test; spec and records

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review against the spec

| Spec | Where |
|---|---|
| §1.1 fingerprint, cached, uncached, comment excluded, golden per dialect | Task 1; Task 3 (`TestCommentLeavesTheFingerprint`) |
| §1.2 call site, PC cache, `WithCallerSkip`, default off | Task 2 |
| §1.3 comment keys, encoding, cache bounds, two texts, hooks, cache safety docs | Task 3 (doc comment on `WithSQLComment`) |
| §1.4 tags | Task 2 |
| §1.5 results | Task 2 |
| §1.6 `TxAround`, `TxInfo`, savepoints, `Statement.Tx` | Task 4 |
| §1.7 statement fields | Tasks 1, 2, 4 |
| §2 `Options`, `Around`, `TxAround`, no SDK | Tasks 6, 7 (`TestNoSDKDependency`) |
| §2.1 spans: names, attributes, `SQL(ctx)` no span, errors, parent, retries | Task 6; tree in Task 7 |
| §2.2 transaction span | Task 7 |
| §2.3 metrics, bounds, cardinality | Task 7 |
| §2.4 versions, constants | Global Constraints; Task 6 `attrs.go` |
| §3 example tracing section | Task 8 |
| §4 errors unchanged | Tasks 4, 6, 7 (hooks return errors unchanged; panics not recovered) |
| §5 testing, each bullet | Tasks 1–4 (core), 6–7 (`contrib/otel`), 8 (real database), 6 (CI) |
| §6 performance | Task 5 (core), Task 7 Step 4 (adapter) |
| Success criterion 5 (compile cost) | Task 5 (`TestObservabilityIsNotGeneric`, remeasure) |
