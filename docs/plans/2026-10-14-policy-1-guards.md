# Policy 1: guards and deadlines — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make velox refuse unbounded reads and unfiltered writes, cap limits and rows, bound statements and transactions in time, settle the error-message convention, and harden the hooks against misuse.

**Architecture:** A new `guard` step runs first in both of Core's execution paths (`DB.plain`, `DB.run`) and checks the statement as the caller built it. The row cap wraps the executor's rows in `DB.exec`. Deadlines are derived contexts applied around execution (outside the `Around` hooks) and around `DB.Tx`; a transaction's deadline travels on the transaction's `*DB`, so statements inside it inherit the deadline whichever context they are given.

**Tech Stack:** Go 1.27 (generic methods), `context.WithDeadlineCause`, pgx v5, database/sql, the in-repo `FakeExec`.

**Spec:** `docs/specs/2026-10-14-policy-design.md` (§1, §4, §5, §6). Core's design, `docs/specs/2026-10-14-core-design.md`, is the base it extends.

## Global Constraints

- Go 1.27. Every method of a generic velox type is generic (`[_ Lazy[E]]` or its own type parameter); `methods_test.go` enforces it.
- Nothing runs at program start; no velox type reachable from generated code holds `reflect.Type` (Core §3 rule 7).
- Every error velox returns starts `velox: <Entity>: ` when there is an entity (`velox: begin: `, `velox: commit: `, `velox: transaction: ` otherwise). Sentinels are bare phrases, always returned wrapped with `%w`.
- Error strings start lower case (staticcheck ST1005): sentinels are `"not found"`, `"more than one row"`, `"query has no Limit"`, `"update or delete has no Where"`, `"generated code is out of date"`, `"limit is over the maximum"`, `"read more rows than the cap"`, `"deadline set by velox passed"`.
- Core's budget tests (`budget_test.go`) keep passing unchanged: a DB with no caps and no deadlines pays only a few branches.
- `gofmt`, `go vet` and `golangci-lint run` (v2.14.0) are clean; `go test -race ./...` passes in the root module.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes on the spec

- `Statement` gains `MaxRows int` (the per-query cap set by `Query.MaxRows`; 0 means the DB's cap), so the cap reaches `exec` and hooks can see it.
- `WithMaxLimit(n)` panics for `n < 2`: `One` uses `Limit 2`, and a maximum below it would break every `One`.
- The row cap applies to row-returning SELECTs only: not to `Count`, and not to an INSERT's `RETURNING` (an `InsertAll` of more rows than the cap has already written them).
- `Timeout(d)` on a transaction is a cap: it bounds the transaction to `d` (within `MaxTx`) even when the context has a later deadline; the `Tx` default applies only to a context with no deadline.
- A transaction whose velox deadline has passed when `fn` returns is rolled back, not committed, on every driver.
- `WithRewrite(nil)`/`WithAround(nil)` panic when called (they are always called in `Open`'s argument list).
- A zero `Predicate` is caught twice: in the guard (the caller's conditions) and after the `Rewrite` hooks (a hook's).

## Review Focus

1. An `InsertAll` with `RETURNING` of more rows than `WithMaxRows` must succeed and set every key (Task 4).
2. A caller's own earlier deadline is kept: the error is `context.DeadlineExceeded` but not `ErrTimeout` (Task 5).
3. `RetrySerializable` stops retrying once the transaction's deadline has passed (Task 5).
4. `One`, `Count` and relation loads are never refused by `ErrUnbounded` or `WithMaxLimit` (Task 3).
5. `Iter` under a row cap yields the first n rows, then the error, and closes the rows (Task 4).

---

### Task 1: Error convention

**Files:**
- Modify: `errors.go`, `query.go:338,342`, `write.go:201`, `meta.go:220,230`, `db.go:125-129`
- Modify: `errors_test.go`
- Create: `convention_test.go`

**Interfaces:**
- Produces: sentinels with bare text; every wrap site uses `fmt.Errorf("velox: %s: %w…", name, Sentinel…)`; rewrite-hook errors come back as `velox: <Entity>: rewrite: <err>`. Later tasks wrap their sentinels the same way.

- [ ] **Step 1: Write the failing tests**

Replace `TestSentinelErrorsStartWithVelox` in `errors_test.go` with:

```go
// TestSentinelsAreBarePhrases: sentinels are always returned wrapped in an
// error that says "velox: <Entity>: ", so their own text has no prefix and
// the prefix never doubles (policy design §6).
func TestSentinelsAreBarePhrases(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrStale} {
		if s := err.Error(); strings.HasPrefix(s, "velox") || strings.Contains(s, ": ") {
			t.Errorf("%q is not a bare phrase", s)
		}
	}
}
```

Create `convention_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestReturnedErrorsNameTheEntityOnce pins the error convention: one
// "velox: <Entity>: " prefix, then the sentinel's phrase.
func TestReturnedErrorsNameTheEntityOnce(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)

	_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
	want(t, err, velox.ErrNotFound, "velox: testmodel.Invoice: not found")

	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	_, err = db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).One(ctx)
	want(t, err, velox.ErrNotSingular, "velox: testmodel.Invoice: more than one row")

	inv := testmodel.Invoice{ID: 7}
	err = db.Save(ctx, &inv, I.Number) // FakeExec reports 0 rows affected
	want(t, err, velox.ErrNotFound, "velox: testmodel.Invoice: not found")

	refused := errors.New("refused")
	hooked := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(func(context.Context, *velox.Statement) error { return refused }))
	_, err = hooked.Query[testmodel.Invoice]().Limit(1).All(ctx)
	want(t, err, refused, "velox: testmodel.Invoice: rewrite: refused")
}

func want(t *testing.T, err, sentinel error, text string) {
	t.Helper()
	if !errors.Is(err, sentinel) || err.Error() != text {
		t.Errorf("got %v, want %q wrapping %v", err, text, sentinel)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'TestSentinelsAreBarePhrases|TestReturnedErrorsNameTheEntityOnce' .`
Expected: FAIL — `"velox: not found" is not a bare phrase`; `got velox: not found: testmodel.Invoice, want "velox: testmodel.Invoice: not found"`; `got refused, want "velox: testmodel.Invoice: rewrite: refused"`.

- [ ] **Step 3: Implement**

`errors.go`, the sentinel block:

```go
// Errors velox returns. Each is returned wrapped, in an error whose text
// starts "velox: <Entity>: " (policy design §6), so a sentinel's own text is
// a bare phrase; test for them with errors.Is.
var (
	// ErrNotFound: One found no row, or Save no row with the key.
	ErrNotFound = errors.New("not found")
	// ErrNotSingular: One found more than one row.
	ErrNotSingular = errors.New("more than one row")
	// ErrUnbounded: a query with no Limit that did not say Unbounded.
	ErrUnbounded = errors.New("query has no Limit")
	// ErrNoWhere: an update or delete with no Where that did not say All.
	ErrNoWhere = errors.New("update or delete has no Where")
	// ErrStale: an entity's struct changed after velox-gen last ran.
	ErrStale = errors.New("generated code is out of date")
)
```

`query.go` (`One`):

```go
	case 0:
		return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotFound)
	case 1:
		return &out[0], nil
	}
	return nil, fmt.Errorf("velox: %s: %w", q.meta.info.Name, ErrNotSingular)
```

`write.go` (`Save`): `return fmt.Errorf("velox: %s: %w", in.Name, ErrNotFound)`

`meta.go` (`checkShape`):

```go
		return fmt.Errorf("velox: %s: %w: columns %s; run go tool velox-gen", entity, ErrStale, d)
	…
		return fmt.Errorf("velox: %s: %w: relations %s; run go tool velox-gen", entity, ErrStale, d)
```

`db.go` (`run`, the rewrite loop):

```go
	for _, rw := range db.cfg.rewrites {
		if err := rw(ctx, st); err != nil {
			return fmt.Errorf("velox: %s: rewrite: %w", entity.Name, err)
		}
	}
```

- [ ] **Step 4: Run the whole module's tests**

Run: `go test ./... && (cd cmd/velox-gen && go test ./...)`
Expected: PASS. A test that compared a sentinel's or hook's error text exactly fails here; update its expected text to the new form (keep `errors.Is` checks as they are).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Errors name the entity once; sentinels are bare phrases; rewrite errors say so

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Hook hardening and the guard step

**Files:**
- Create: `guard.go`
- Modify: `db.go` (`WithRewrite`, `WithAround`, `plain`, `run`, `checkColumns`), `statement.go` (`Statement.Count` comment)
- Test: `hardening_test.go` (create, package `velox_test`)

**Interfaces:**
- Produces: `func (db *DB) guard(st *Statement) error` in `guard.go`, called first in `plain` and in `run` (before the statement is copied for hooks). Task 3 adds checks to it.

- [ ] **Step 1: Write the failing tests**

`hardening_test.go`:

```go
package velox_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestNilHooksPanicAtOpen(t *testing.T) {
	for name, f := range map[string]func(){
		"WithRewrite": func() { velox.WithRewrite(nil) },
		"WithAround":  func() { velox.WithAround(nil) },
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), name+"(nil)") {
					t.Errorf("%s(nil): recovered %v", name, r)
				}
			}()
			f()
		}()
	}
}

func TestRewriteCannotNilOrForeignEntity(t *testing.T) {
	cases := map[string]struct {
		hook func(st *velox.Statement)
		want string
	}{
		"nil": {func(st *velox.Statement) { st.Entity = nil }, "set the statement's Entity to nil"},
		"foreign": {func(st *velox.Statement) {
			info := velox.EntityInfo{Name: "other.Thing", Table: "things", Columns: st.Entity.Columns}
			st.Entity = &info
		}, "not a copy of it"},
	}
	for name, c := range cases {
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { c.hook(st); return nil }))
		_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") || e.Calls != 0 {
			t.Errorf("%s: %v, calls %d", name, err, e.Calls)
		}
	}
}

func TestZeroPredicateIsRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	for name, p := range map[string]velox.Pred[testmodel.Invoice]{
		"top level": {},
		"nested":    velox.And(I.ID.EQ(1), velox.Pred[testmodel.Invoice]{}),
	} {
		_, err := db.Query[testmodel.Invoice]().Where(p).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
			t.Errorf("%s: %v, calls %d", name, err, e.Calls)
		}
	}
	hooked := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.Predicate{})
		return nil
	}))
	if _, err := hooked.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Errorf("from a hook: %v, calls %d", err, e.Calls)
	}
}

func TestQuerySelectingNoColumnsIsRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	all := []velox.Column[testmodel.Invoice]{I.CreatedAt, I.UpdatedAt, I.ID, I.OrgID, I.Number, I.Total, I.Paid, I.PaidAt, I.Note, I.Data}
	_, err := db.Query[testmodel.Invoice]().Omit(all...).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "selects no columns") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

// TestInCopiesItsValues: a predicate holds its own copy of In's values, so
// neither the caller nor a hook can change a built query's arguments
// through the slice (closes the Core follow-up on the one-level copy).
func TestInCopiesItsValues(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	ids := []int64{1, 2}
	q := db.Query[testmodel.Invoice]().Where(I.ID.In(ids...)).Limit(5)
	ids[0] = 9
	if _, args := q.SQL(); args[0] != int64(1) {
		t.Errorf("args %v: In shares the caller's slice", args)
	}
}
```

If `testmodel.Invoice`'s columns differ from the list in `TestQuerySelectingNoColumnsIsRefused`, take them from `internal/testmodel/invoice_velox.go` (every column handle of `Invoices`).

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'TestNilHooksPanicAtOpen|TestRewriteCannotNilOrForeignEntity|TestZeroPredicateIsRefused|TestQuerySelectingNoColumnsIsRefused|TestInCopiesItsValues' .`
Expected: FAIL (no panic for nil hooks; nil entity panics; zero predicates reach the executor; `SELECT  FROM` is sent). `TestInCopiesItsValues` passes already: it pins existing behaviour.

- [ ] **Step 3: Implement**

`db.go`:

```go
// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given. A
// nil hook panics.
func WithRewrite(r Rewrite) Option {
	if r == nil {
		panic("velox: WithRewrite(nil)")
	}
	return func(c *config) { c.rewrites = append(c.rewrites, r) }
}

// WithAround adds an around hook. The first one given is the outermost. A nil
// hook panics.
func WithAround(a Around) Option {
	if a == nil {
		panic("velox: WithAround(nil)")
	}
	return func(c *config) { c.arounds = append(c.arounds, a) }
}
```

`guard.go`:

```go
package velox

import "fmt"

// guard checks a statement as the caller built it, before any hook sees it
// (policy design §1): conditions velox or a hook adds later never count as
// the caller's.
func (db *DB) guard(st *Statement) error {
	for _, p := range st.Where {
		if hasZero(p.n) {
			return errZeroPredicate(st.Entity.Name)
		}
	}
	if st.Op == OpSelect && !st.Count && st.Columns != nil && len(st.Columns) == 0 {
		return fmt.Errorf("velox: %s: the query selects no columns", st.Entity.Name)
	}
	return nil
}

// hasZero reports whether n is, or holds, the zero Predicate, which renders
// as nothing.
func hasZero(n node) bool {
	if n.op == 0 {
		return true
	}
	for _, k := range n.kids {
		if hasZero(k) {
			return true
		}
	}
	return false
}

func errZeroPredicate(entity string) error {
	return fmt.Errorf("velox: %s: a condition is the zero Predicate; make conditions with column handles, And, Or and Not", entity)
}
```

`db.go`, `plain`:

```go
func (db *DB) plain(ctx context.Context, st *Statement, read func(rows) error, res *result) error {
	if err := db.guard(st); err != nil {
		return err
	}
	st.SQL, st.Args = db.cfg.cache.render(db.d, st)
	return db.exec(ctx, st, read, res)
}
```

`db.go`, `run`: call `db.guard(st)` first (before the `if db.hooked()` copy), and replace the block from `if st.Op != op {` to the `checkColumns` call with:

```go
	if st.Op != op {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's kind", entity.Name)
	}
	switch {
	case st.Entity == nil:
		return fmt.Errorf("velox: %s: a Rewrite hook set the statement's Entity to nil", entity.Name)
	case st.Entity != entity && (st.Entity.kit != entity.kit || len(st.Entity.Columns) != len(entity.Columns)):
		// A copy (info := *st.Entity) keeps the scan plan; anything else
		// would scan rows into the wrong fields.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with %s, which is not a copy of it", entity.Name, st.Entity.Name)
	}
	if len(db.cfg.rewrites) > 0 {
		if err := checkColumns(st); err != nil {
			return err
		}
	}
```

`db.go`, `checkColumns`'s `walk`, add a first case:

```go
		case 0:
			return errZeroPredicate(st.Entity.Name)
```

`statement.go`, the `Count` field:

```go
	Count     bool        // SELECT COUNT(*): Count clears Columns, OrderBy, Limit and Offset; a Rewrite hook that sets them gets them rendered
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS, `TestRewriteCanReplaceTheEntity` included (it copies the entity).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Harden hooks: nil hooks panic at Open, a hook cannot nil or swap the entity, zero predicates and empty column lists are refused

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Required limits and Where

**Files:**
- Modify: `guard.go`, `db.go` (`config`, new `WithMaxLimit`), `errors.go`, `query.go` (`Limit`, `Unbounded` docs), `write.go` (`All` docs)
- Test: `guard_test.go` (create, package `velox_test`)
- Modify: every test in the repository that runs a query with no `Limit` (see Step 4)

**Interfaces:**
- Consumes: `func (db *DB) guard(st *Statement) error` (Task 2).
- Produces: `ErrLimitTooLarge`; `func WithMaxLimit(n int) Option`; `config.maxLimit int`.

- [ ] **Step 1: Write the failing tests**

`guard_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestReadWithoutLimitIsRefused(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().All(ctx); !errors.Is(err, velox.ErrUnbounded) {
		t.Errorf("All: %v", err)
	}
	if _, err := db.Query[testmodel.Invoice]().Limit(0).All(ctx); !errors.Is(err, velox.ErrUnbounded) {
		t.Errorf("Limit(0): %v", err)
	}
	for _, err := range db.Query[testmodel.Invoice]().Iter(ctx) {
		if !errors.Is(err, velox.ErrUnbounded) {
			t.Errorf("Iter: %v", err)
		}
	}
	if e.Calls != 0 {
		t.Fatalf("%d statements sent", e.Calls)
	}
	// Review Focus 4: One, Unbounded and Count are not refused.
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Errorf("One: %v", err)
	}
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err != nil {
		t.Errorf("Unbounded: %v", err)
	}
	e.Rows = [][]any{{int64(1)}} // Count scans one number
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
		t.Errorf("Count: %v", err)
	}
}

func TestWriteWithoutWhereIsRefused(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Exec(ctx); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("Update: %v", err)
	}
	if _, err := db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("Delete: %v", err)
	}
	if e.Calls != 0 {
		t.Fatalf("%d statements sent", e.Calls)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).All().Exec(ctx); err != nil {
		t.Errorf("Update All: %v", err)
	}
	if _, err := db.Delete[testmodel.Invoice]().All().Exec(ctx); err != nil {
		t.Errorf("Delete All: %v", err)
	}
	inv := testmodel.Invoice{ID: 1}
	if err := db.Save(ctx, &inv, I.Number); err != nil {
		t.Errorf("Save: %v", err)
	}
}

// TestGuardRunsBeforeHooks: a Where a hook adds does not make a write
// filtered, and a refused statement never reaches the hooks.
func TestGuardRunsBeforeHooks(t *testing.T) {
	ran := 0
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		ran++
		st.Where = append(st.Where, velox.ColumnEQ(st.Entity.Tenant, int64(1)))
		return nil
	}))
	if _, err := db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) || ran != 0 {
		t.Errorf("%v, hook ran %d times", err, ran)
	}
}

func TestMaxLimit(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxLimit(50))
	if _, err := db.Query[testmodel.Invoice]().Limit(51).All(ctx); !errors.Is(err, velox.ErrLimitTooLarge) || e.Calls != 0 {
		t.Errorf("51: %v, calls %d", err, e.Calls)
	}
	if _, err := db.Query[testmodel.Invoice]().Limit(50).All(ctx); err != nil {
		t.Errorf("50: %v", err)
	}
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Errorf("One: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("WithMaxLimit(1) did not panic")
		}
	}()
	velox.WithMaxLimit(1)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'TestReadWithoutLimitIsRefused|TestWriteWithoutWhereIsRefused|TestGuardRunsBeforeHooks|TestMaxLimit' .`
Expected: FAIL to compile (`velox.ErrLimitTooLarge`, `velox.WithMaxLimit` undefined).

- [ ] **Step 3: Implement**

`errors.go`, in the sentinel block:

```go
	// ErrLimitTooLarge: a query whose Limit is over the DB's WithMaxLimit.
	ErrLimitTooLarge = errors.New("limit is over the maximum")
```

`db.go`: add `maxLimit int // WithMaxLimit; 0: none` to `config`, and:

```go
// WithMaxLimit refuses, with ErrLimitTooLarge, a query whose Limit is over n.
// n must be at least 2, the limit One uses; a smaller n panics.
func WithMaxLimit(n int) Option {
	if n < 2 {
		panic(fmt.Sprintf("velox: WithMaxLimit(%d): the maximum must be at least 2, the limit One uses", n))
	}
	return func(c *config) { c.maxLimit = n }
}
```

`guard.go`, at the end of `guard` before `return nil`:

```go
	switch st.Op {
	case OpSelect:
		if st.Count {
			break
		}
		if st.Limit <= 0 && !st.Unbounded {
			return fmt.Errorf("velox: %s: %w: add Limit, or Unbounded to read every row", st.Entity.Name, ErrUnbounded)
		}
		if m := db.cfg.maxLimit; m > 0 && st.Limit > m {
			return fmt.Errorf("velox: %s: %w: Limit %d, the maximum is %d", st.Entity.Name, ErrLimitTooLarge, st.Limit, m)
		}
	case OpUpdate, OpDelete:
		if len(st.Where) == 0 && !st.AllRows {
			return fmt.Errorf("velox: %s: %w: add Where, or All to change every row", st.Entity.Name, ErrNoWhere)
		}
	}
```

Docs: `Query.Limit` — "n <= 0 removes the limit; a query with no limit fails with ErrUnbounded unless it says Unbounded."; `Query.Unbounded` — "A query with neither a Limit nor Unbounded fails with ErrUnbounded."; `UpdateQuery.All` and `DeleteQuery.All` — "An update (delete) with neither a Where nor All fails with ErrNoWhere." Remove the words "(sub-project 2)" and "Raised by the guardrails" wherever they remain.

- [ ] **Step 4: Update the tests that read with no limit**

Run: `go test ./... 2>&1 | grep -E 'ErrUnbounded|query has no Limit|has no Where' -B3`
For every test this reports, in the root module and in `internal/itest`: a test about reading every row gets `.Unbounded()`; a test whose point is something else and reads a known number of rows gets `.Unbounded()` too (never a `Limit` that could hide rows the test expects). An update or delete that meant every row gets `.All()`. The `query` helper in `internal/itest/itest_test.go` gets `.Unbounded()`. Do not weaken an assertion.

Run: `go test ./... && (cd cmd/velox-gen && go test ./...) && go vet ./...`
Expected: PASS. (`internal/itest` runs on SQLite here; Task 6 runs it on every database.)

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Refuse reads with no Limit and writes with no Where; WithMaxLimit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Row cap

**Files:**
- Modify: `db.go` (`config`, `WithMaxRows`, `exec`, new `rowCap`, `cappedRows`), `statement.go` (`MaxRows` field), `query.go` (`MaxRows` method), `errors.go`
- Test: `rowcap_test.go` (create, package `velox_test`)

**Interfaces:**
- Produces: `ErrTooManyRows`; `func WithMaxRows(n int) Option`; `Statement.MaxRows int`; `func (q Query[E]) MaxRows[_ Lazy[E]](n int) Query[E]`.

- [ ] **Step 1: Write the failing tests**

`rowcap_test.go`:

```go
package velox_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func fiveInvoices() [][]any {
	return [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3), invoiceRow(4), invoiceRow(5)}
}

func TestRowCap(t *testing.T) {
	e := &velox.FakeExec{Rows: fiveInvoices()}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(3))
	_, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") || e.Closed != e.Calls {
		t.Errorf("%v; closed %d of %d", err, e.Closed, e.Calls)
	}
	if got, err := db.Query[testmodel.Invoice]().Unbounded().MaxRows(10).All(ctx); err != nil || len(got) != 5 {
		t.Errorf("MaxRows(10): %d rows, %v", len(got), err)
	}
	if got, err := db.Query[testmodel.Invoice]().Unbounded().MaxRows(10).MaxRows(0).All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Errorf("MaxRows(0) restores the DB's cap: %d rows, %v", len(got), err)
	}
	e.Rows = [][]any{{int64(5)}}
	if n, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
		t.Errorf("Count: %d, %v", n, err)
	}
}

// TestRowCapInIter is Review Focus 5.
func TestRowCapInIter(t *testing.T) {
	e := &velox.FakeExec{Rows: fiveInvoices()}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(3))
	var rows int
	var last error
	for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			last = err
			continue
		}
		rows++
	}
	if rows != 3 || !errors.Is(last, velox.ErrTooManyRows) || e.Closed != 1 {
		t.Errorf("%d rows, then %v; closed %d", rows, last, e.Closed)
	}
}

// TestRowCapSparesInsertReturning is Review Focus 1.
func TestRowCapSparesInsertReturning(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(2))
	xs := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}
	if err := db.InsertAll(xs).Exec(ctx); err != nil || xs[2].ID != 3 {
		t.Errorf("%v, ids %d %d %d", err, xs[0].ID, xs[1].ID, xs[2].ID)
	}
}

func TestRowCapCoversRelationLoads(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1)},
		{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1), itemRow(12, 1, "C", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(2))
	_, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.Contains(err.Error(), "Items") {
		t.Errorf("%v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'TestRowCap' .`
Expected: FAIL to compile (`velox.WithMaxRows`, `velox.ErrTooManyRows`, `MaxRows` undefined).

- [ ] **Step 3: Implement**

`errors.go`:

```go
	// ErrTooManyRows: a query returned more rows than WithMaxRows or the
	// query's MaxRows allow.
	ErrTooManyRows = errors.New("read more rows than the cap")
```

`statement.go`, after `Unbounded`:

```go
	MaxRows   int             // SELECT: the most rows it may return; 0: the DB's WithMaxRows
```

`query.go`, after `Unbounded`:

```go
// MaxRows returns the query allowed to return up to n rows, in place of the
// DB's WithMaxRows cap; n <= 0 restores the DB's cap. Say it where reading
// many rows is meant, so the exception is visible where the query is
// written.
func (q Query[E]) MaxRows[_ Lazy[E]](n int) Query[E] {
	q.st.MaxRows = max(n, 0)
	return q
}
```

`db.go`: add `maxRows int // WithMaxRows; 0: none` to `config`, and:

```go
// WithMaxRows fails, with ErrTooManyRows, a query that returns more than n
// rows: a relation load and Iter included, Count and an insert's RETURNING
// not. A query's MaxRows overrides it. n <= 0 sets no cap.
func WithMaxRows(n int) Option { return func(c *config) { c.maxRows = max(n, 0) } }

// rowCap is the most rows st may return: its own MaxRows, else the DB's; 0
// for no cap.
func (db *DB) rowCap(st *Statement) int {
	if st.Op != OpSelect || st.Count {
		return 0
	}
	if st.MaxRows > 0 {
		return st.MaxRows
	}
	return db.cfg.maxRows
}

// cappedRows ends the rows after left of them, and records that there were
// more.
type cappedRows struct {
	rows
	left int
	over bool
}

func (c *cappedRows) Next() bool {
	if !c.rows.Next() {
		return false
	}
	if c.left == 0 {
		c.over = true
		return false
	}
	c.left--
	return true
}
```

`db.go`, `exec`: after `r, err := db.ex.query(...)` and its error check, before the deferred close:

```go
	var capped *cappedRows
	if n := db.rowCap(st); n > 0 {
		capped = &cappedRows{rows: r, left: n}
		r = capped
	}
```

and replace the final `if err := read(r)…; return nil` with:

```go
	if err := read(r); err != nil {
		return dbErr(st.Entity, err)
	}
	if capped != nil && capped.over {
		return fmt.Errorf("velox: %s: %w: more than %d; raise it with the query's MaxRows", st.Entity.Name, ErrTooManyRows, db.rowCap(st))
	}
	return nil
```

- [ ] **Step 4: Run the tests**

Run: `go test ./... && go test -run 'Budget' -count=3 .`
Expected: PASS; the budget tests unchanged (no cap is configured there).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Row cap: WithMaxRows and Query.MaxRows, for every read including relation loads and Iter

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Deadlines

**Files:**
- Create: `deadline.go`
- Modify: `db.go` (`DB.txDeadline`, `config.deadlines`, `plain`, `run`), `tx.go` (`Tx`, `transaction`, `savepoint`), `executor.go` (`txOptions.timeout`), `errors.go`, `export_test.go` (`FakeExec.Block`, `FakeExec.Deadline`)
- Test: `deadline_test.go` (create, package `velox_test`); `budget_test.go` (a benchmark)

**Interfaces:**
- Produces: `type Deadlines struct{ Statement, MaxStatement, Tx, MaxTx time.Duration }`; `func WithDeadlines(d Deadlines) Option`; `func Timeout(d time.Duration) TxOption`; `ErrTimeout`; `FakeExec.Block bool`, `FakeExec.Deadline time.Time`.

- [ ] **Step 1: Give FakeExec a context it can wait on**

`export_test.go`, add to `FakeExec`:

```go
	// Block makes query and exec wait until their context ends, then return
	// its error.
	Block bool
	// Deadline is the deadline of the last query's or exec's context; zero
	// when it had none.
	Deadline time.Time
```

and:

```go
// wait records ctx's deadline and, when Block is set, waits for ctx to end.
func (e *FakeExec) wait(ctx context.Context) error {
	e.mu.Lock()
	e.Deadline, _ = ctx.Deadline()
	block := e.Block
	e.mu.Unlock()
	if !block {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}
```

Call it first in `query` and `exec` (`if err := e.wait(ctx); err != nil { return nil, err }` and `return result{}, err` respectively), renaming their unused `_ context.Context` parameter to `ctx`.

- [ ] **Step 2: Write the failing tests**

`deadline_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func near(t *testing.T, got time.Time, want time.Duration) {
	t.Helper()
	if d := time.Until(got); got.IsZero() || d > want || d < want-time.Second {
		t.Errorf("deadline in %v, want about %v", d, want)
	}
}

func TestStatementDefaultDeadline(t *testing.T) {
	e := &velox.FakeExec{Block: true}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Statement: 50 * time.Millisecond}))
	start := time.Now()
	_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
	if !errors.Is(err, velox.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") {
		t.Errorf("%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

// TestCallersEarlierDeadlineIsKept is Review Focus 2.
func TestCallersEarlierDeadlineIsKept(t *testing.T) {
	e := &velox.FakeExec{Block: true}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Statement: time.Hour, MaxStatement: time.Hour}))
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	want, _ := cctx.Deadline()
	_, err := db.Query[testmodel.Invoice]().Limit(1).All(cctx)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) || !e.Deadline.Equal(want) {
		t.Errorf("%v; deadline %v, want the caller's %v", err, e.Deadline, want)
	}
}

func TestMaxStatementBringsADeadlineForward(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{MaxStatement: 2 * time.Second}))
	cctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(cctx); err != nil {
		t.Fatal(err)
	}
	near(t, e.Deadline, 2*time.Second)
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	near(t, e.Deadline, 2*time.Second) // no deadline at all is later than the cap
}

func TestNoDeadlinesLeaveTheContextAlone(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil || !e.Deadline.IsZero() {
		t.Errorf("%v, deadline %v", err, e.Deadline)
	}
}

// TestDeadlineWrapsTheAroundHooks: the hooks see velox's deadline, and a
// hook that retries retries within it.
func TestDeadlineWrapsTheAroundHooks(t *testing.T) {
	e := &velox.FakeExec{Block: true}
	attempts := 0
	retry := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		for {
			attempts++
			if err := next(ctx); err == nil || ctx.Err() != nil {
				return err
			}
		}
	})
	db := velox.OpenFake(velox.Postgres, e, retry, velox.WithDeadlines(velox.Deadlines{Statement: 50 * time.Millisecond}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); !errors.Is(err, velox.ErrTimeout) || attempts != 1 {
		t.Errorf("%v after %d attempts", err, attempts)
	}
}

func TestTxDeadline(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Tx: 50 * time.Millisecond}))
	err := db.Tx(ctx, func(tx *velox.DB) error {
		e.Block = true
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx) // the caller's ctx: the tx's deadline still applies
		return err
	})
	if !errors.Is(err, velox.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("%v", err)
	}
	if last := e.Log[len(e.Log)-1]; last != "ROLLBACK" {
		t.Errorf("log %v", e.Log)
	}
}

func TestTxPastItsDeadlineIsNotCommitted(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(*velox.DB) error {
		time.Sleep(60 * time.Millisecond)
		return nil
	}, velox.Timeout(20*time.Millisecond))
	if !errors.Is(err, velox.ErrTimeout) || !strings.HasPrefix(err.Error(), "velox: transaction: ") {
		t.Errorf("%v", err)
	}
	for _, s := range e.Log {
		if s == "COMMIT" {
			t.Errorf("committed: %v", e.Log)
		}
	}
}

// TestRetriesStopAtTheTxDeadline is Review Focus 3.
func TestRetriesStopAtTheTxDeadline(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	runs := 0
	err := db.Tx(ctx, func(*velox.DB) error {
		runs++
		time.Sleep(20 * time.Millisecond)
		return &pgconn.PgError{Code: "40001"}
	}, velox.RetrySerializable(1000), velox.Timeout(100*time.Millisecond))
	if !errors.Is(err, velox.ErrTimeout) || runs > 10 {
		t.Errorf("%v after %d runs", err, runs)
	}
}

func TestTimeoutReplacesTheTxDefault(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithDeadlines(velox.Deadlines{Tx: 10 * time.Millisecond, MaxTx: time.Hour}))
	err := db.Tx(ctx, func(*velox.DB) error {
		time.Sleep(40 * time.Millisecond)
		return nil
	}, velox.Timeout(time.Minute))
	if err != nil {
		t.Errorf("%v", err)
	}
}

func TestStatementsInATxGetItsDeadline(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	var got time.Time
	err := db.Tx(ctx, func(tx *velox.DB) error {
		return tx.Tx(ctx, func(nested *velox.DB) error { // a savepoint inherits it too
			_, err := nested.Query[testmodel.Invoice]().Limit(1).All(ctx)
			got = e.Deadline // before RELEASE SAVEPOINT records its own
			return err
		})
	}, velox.Timeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	near(t, got, 3*time.Second)
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test -run 'Deadline|TxPast|RetriesStop|TimeoutReplaces|StatementsInATx' .`
Expected: FAIL to compile (`velox.WithDeadlines`, `velox.Deadlines`, `velox.Timeout`, `velox.ErrTimeout` undefined).

- [ ] **Step 4: Implement**

`errors.go`:

```go
	// ErrTimeout: a deadline velox set (WithDeadlines, Timeout) passed. The
	// error also matches context.DeadlineExceeded; a deadline the caller set
	// matches that alone.
	ErrTimeout = errors.New("deadline set by velox passed")
```

`deadline.go`:

```go
package velox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Deadlines are the time limits velox gives statements and transactions
// (policy design §5). A zero field is off.
type Deadlines struct {
	Statement    time.Duration // a statement whose context has no deadline gets this one
	MaxStatement time.Duration // no statement runs longer: a later deadline is brought forward
	Tx           time.Duration // a transaction whose context has no deadline gets this one
	MaxTx        time.Duration // no transaction runs longer, retries and commit included
}

// WithDeadlines sets the DB's time limits. A statement's limit covers its
// Around hooks and, with Iter, the caller's loop body; a transaction's covers
// fn, every RetrySerializable attempt and the commit, and bounds every
// statement inside it whatever context that statement is given.
func WithDeadlines(d Deadlines) Option { return func(c *config) { c.deadlines = d } }

// Timeout bounds the transaction to d, retries and commit included, in place
// of the DB's Tx default and within its MaxTx. An earlier deadline on the
// context still applies.
func Timeout(d time.Duration) TxOption { return func(o *txOptions) { o.timeout = d } }

// bound returns ctx with velox's limits applied: def when ctx has no
// deadline, at most maxd from now, and no later than also unless it is zero.
// cancel is nil when ctx is returned unchanged. A deadline velox sets has the
// cause ErrTimeout.
func bound(ctx context.Context, def, maxd time.Duration, also time.Time) (context.Context, context.CancelFunc) {
	now := time.Now()
	dl, has := ctx.Deadline()
	var want time.Time
	if !has && def > 0 {
		want = now.Add(def)
	}
	if maxd > 0 {
		if m := now.Add(maxd); want.IsZero() || m.Before(want) {
			want = m
		}
	}
	if !also.IsZero() && (want.IsZero() || also.Before(want)) {
		want = also
	}
	if want.IsZero() || has && !want.Before(dl) {
		return ctx, nil
	}
	return context.WithDeadlineCause(ctx, want, ErrTimeout)
}

// timedOut reports whether ctx ended because a deadline velox set passed.
func timedOut(ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrTimeout)
}

// errTimeout is the error of a statement or transaction (who) that ran past
// velox's deadline.
func errTimeout(who string) error {
	return fmt.Errorf("velox: %s: %w: %w", who, ErrTimeout, context.DeadlineExceeded)
}

// timed reports whether a statement on db may get a deadline from velox.
func (db *DB) timed() bool {
	d := db.cfg.deadlines
	return d.Statement > 0 || d.MaxStatement > 0 || !db.txDeadline.IsZero()
}

// bound applies db's statement limits, and its transaction's deadline, to ctx.
func (db *DB) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return bound(ctx, db.cfg.deadlines.Statement, db.cfg.deadlines.MaxStatement, db.txDeadline)
}
```

`executor.go`, `txOptions`: add `timeout time.Duration // Timeout; 0: the DB's Tx default` (import `time`).

`db.go`: add `deadlines Deadlines` to `config`; add `txDeadline time.Time // in a transaction: the deadline velox set for it; zero when none` to `DB`.

`db.go`, `plain` — replace its last line `return db.exec(ctx, st, read, res)` with:

```go
	if !db.timed() {
		return db.exec(ctx, st, read, res)
	}
	tctx, cancel := db.bound(ctx)
	if cancel == nil {
		return db.exec(ctx, st, read, res)
	}
	defer cancel()
	if err := db.exec(tctx, st, read, res); err != nil {
		if timedOut(tctx) {
			return errTimeout(st.Entity.Name)
		}
		return err
	}
	return nil
```

`db.go`, `run`: move everything from `if len(db.cfg.arounds) == 0 {` to the end of `run` into a new method `func (db *DB) execute(ctx context.Context, st *Statement, read func(rows) error, res *result) error` (unchanged code), and end `run` with:

```go
	if db.timed() {
		if tctx, cancel := db.bound(ctx); cancel != nil {
			defer cancel()
			if err := db.execute(tctx, st, read, res); err != nil {
				if timedOut(tctx) {
					return errTimeout(st.Entity.Name)
				}
				return err
			}
			return nil
		}
	}
	return db.execute(ctx, st, read, res)
```

`tx.go`, `Tx` (the non-nested path):

```go
	var o txOptions
	for _, opt := range opts {
		opt(&o)
	}
	def, maxd := db.cfg.deadlines.Tx, db.cfg.deadlines.MaxTx
	if o.timeout > 0 {
		def = 0
		if maxd <= 0 || o.timeout < maxd {
			maxd = o.timeout
		}
	}
	tctx, cancel := bound(ctx, def, maxd, time.Time{})
	var deadline time.Time
	if cancel != nil {
		defer cancel()
		deadline, _ = tctx.Deadline()
	}
	for attempt := 0; ; attempt++ {
		err := db.transaction(tctx, fn, o, deadline)
		if err != nil && cancel != nil && timedOut(tctx) && !errors.Is(err, ErrTimeout) {
			err = errors.Join(errTimeout("transaction"), err)
		}
		if err == nil || attempt >= o.retries || !retryable(err) || tctx.Err() != nil {
			return err
		}
	}
```

Note `errTimeout("transaction")` reads `velox: transaction: deadline set by velox passed: context deadline exceeded`.

`tx.go`, `transaction(ctx context.Context, fn func(*DB) error, o txOptions, deadline time.Time)`: build the transaction's DB as `&DB{d: db.d, ex: ex, cfg: db.cfg, inTx: true, txDeadline: deadline}`, and before committing:

```go
	finished = true
	if timedOut(ctx) {
		// Past velox's deadline: roll back on every driver rather than leave
		// it to the driver whether a late commit succeeds.
		if rerr := ctl.rollback(end); rerr != nil && !ended(rerr) {
			return errors.Join(errTimeout("transaction"), fmt.Errorf("velox: rollback: %w", rerr))
		}
		return errTimeout("transaction")
	}
	if err := ctl.commit(ctx); err != nil {
```

`tx.go`, `savepoint`: the nested DB gets `txDeadline: db.txDeadline`. Update `Tx`'s doc comment: "WithDeadlines' Tx and MaxTx, and Timeout, bound the whole call; a transaction past its deadline is rolled back."

`budget_test.go`, add (not asserted; it documents the cost of a deadline):

```go
func BenchmarkOneRowWithDeadline(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, velox.WithDeadlines(velox.Deadlines{Statement: time.Second}))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./... && go test -run 'Budget' -count=3 . && go test -run '^$' -bench 'OneRow' -benchtime 2000x .`
Expected: PASS; the budget tests unchanged; the benchmark prints the deadline's extra allocations (record them in the commit message).

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "Deadlines: WithDeadlines for statements and transactions, Timeout, ErrTimeout

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Real databases, documents and the CI run

**Files:**
- Modify: `internal/itest/itest_test.go` (`target.raw`)
- Create: `internal/itest/guards_test.go`
- Modify: `docs/specs/2026-10-14-policy-design.md`, `docs/specs/2026-10-14-core-design.md`, `docs/plans/2026-10-14-core-1-followups.md`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Give targets a raw handle**

`internal/itest/itest_test.go`: add `raw *sql.DB // a second way in, for holding locks; nil for SQLite` to `target`; set it to `sqlDB` for both PostgreSQL targets and to `db` for MySQL. SQLite leaves it nil (its one connection cannot lock against itself).

- [ ] **Step 2: Write the real-database tests**

`internal/itest/guards_test.go`:

```go
package itest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestGuardsOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if _, err := tg.db.Query[testmodel.Invoice]().All(ctx); !errors.Is(err, velox.ErrUnbounded) {
			t.Errorf("no limit: %v", err)
		}
		if _, err := tg.db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) {
			t.Errorf("no where: %v", err)
		}
		if _, err := tg.db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
			t.Errorf("five rows, cap 3: %v", err)
		}
		if got, err := tg.db.Query[testmodel.Invoice]().Limit(3).All(ctx); err != nil || len(got) != 3 {
			t.Errorf("limit 3: %d, %v", len(got), err)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
			t.Errorf("count: %d, %v", n, err)
		}
	}, velox.WithMaxRows(3))
}

// lockRow holds a lock on invoice 1 from another connection until the test
// ends, so a velox write to it waits.
func lockRow(t *testing.T, tg target) {
	t.Helper()
	lock, err := tg.raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback() })
	if _, err := lock.ExecContext(ctx, rebind(tg.d, "UPDATE invoices SET total = total WHERE id = ?"), 1); err != nil {
		t.Fatal(err)
	}
}

func TestStatementDeadlineOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.raw == nil {
			return // SQLite: one connection, nothing to wait on
		}
		lockRow(t, tg)
		start := time.Now()
		_, err := tg.db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx)
		if !errors.Is(err, velox.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%v", err)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("returned after %v", d)
		}
	}, velox.WithDeadlines(velox.Deadlines{Statement: 300 * time.Millisecond}))
}

func TestTxDeadlineOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.raw == nil {
			return
		}
		err := tg.db.Tx(ctx, func(tx *velox.DB) error {
			if _, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(2)).Where(I.ID.EQ(2)).Exec(ctx); err != nil {
				return err
			}
			lockRow(t, tg)
			_, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx)
			return err
		}, velox.Timeout(300*time.Millisecond))
		if !errors.Is(err, velox.ErrTimeout) {
			t.Fatalf("%v", err)
		}
		var total float64
		if err := tg.raw.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), 2).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total != 20 {
			t.Errorf("invoice 2 total %v: the timed-out transaction was not rolled back", total)
		}
	})
}
```

- [ ] **Step 3: Run them on every database**

Run: `scripts/ci-local.sh`
Expected: exit 0: gofmt, vet, lint, `-race` tests on SQLite, PostgreSQL (pgx and database/sql) and MySQL, the fuzz smoke runs, `velox-gen -check`/`verify`, and the scale check. If MySQL's deadline test returns only after `innodb_lock_wait_timeout`, the driver did not cancel: record what you see in the report before changing anything.

- [ ] **Step 4: Update the documents**

- `docs/specs/2026-10-14-policy-design.md`: §2.3 lists `MaxRows int // SELECT: the most rows it may return; 0: the DB's cap` among `Statement`'s fields (added by plan 1); §4 says `WithMaxLimit(n)` panics for n < 2 and that the row cap counts row-returning SELECTs only (not `Count`, not `RETURNING`); §5 says `Timeout(d)` is a cap, the `Tx` default applies only to a context with no deadline, a transaction's deadline bounds statements inside it whatever context they get, and a transaction past its deadline is rolled back on every driver.
- `docs/specs/2026-10-14-core-design.md` §7: sentinels' text is now the bare phrase; `ErrUnbounded` and `ErrNoWhere` are raised (no longer "by sub-project 2"); add `ErrLimitTooLarge`, `ErrTooManyRows`, `ErrTimeout`.
- `docs/plans/2026-10-14-core-1-followups.md`, "Sub-project 2" section: mark resolved — hook misuse hardening (nil hooks, nil/foreign entity, zero predicate), the hook-error convention, the one-level copy (closed by `TestInCopiesItsValues`), the `Count` contract, empty `Columns`; and in "Left by plan 3", the line about UPDATE/DELETE with neither Where nor All. Add a "Left by policy plan 1" section for anything this plan leaves.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Guards and deadlines on every database; spec and records

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
