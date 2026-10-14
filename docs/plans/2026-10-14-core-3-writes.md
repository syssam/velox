# Core 3 — Writes and Transactions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** velox writes:
- inserts, with defaults, generated keys read back, upserts, and batched multi-row inserts;
- updates with typed setters, `Save` of named columns, and deletes;
- transactions, with savepoints and retry on serialization failures;
- constraint violations reported as one `*ConstraintError` on PostgreSQL, MySQL and SQLite.

**Architecture:**
- **One statement type.** Every write becomes the same non-generic `Statement` as a query, now with `Set`, `Rows`, `Conflict` and `Returning`. It passes through the same pipeline (rewrite and around hooks, render cache), and the renderer gains INSERT, UPDATE and DELETE.
- **Executors.** They gain `exec` (statements that return no rows) and `begin` (transactions); a transaction's `*DB` is the same type with a transaction executor.
- **Builders.** They are values like `Query`: `db.Insert(&x)`, `db.InsertAll(xs)`, `db.Update[E]()` and `db.Delete[E]()`, plus `db.Save(ctx, &x, cols…)`.
- **Driver errors.** PostgreSQL errors are read from pgx's `*pgconn.PgError`. MySQL and SQLite errors are recognized by their type name and text, so the library never imports those drivers.

**Tech Stack:** Go 1.27, pgx v5.11.0 (already required; `pgconn` is part of it), go-sql-driver/mysql and modernc.org/sqlite for tests only.

**Spec:** `docs/specs/2026-10-14-core-design.md`, §4.2 (create), §4.4–4.6 (update, delete, transactions), §5 (pipeline), §6 (dialects), §7 (errors). Also read the "Before plan 3" items in `docs/plans/2026-10-14-core-1-followups.md`. This plan resolves three of them: a hook changing a statement's kind, schema-qualified table names, and MySQL/SQLite render tests for writes. Task 7 updates the spec where this plan settles what it left open.

**This is plan 3 of 5 for Core.** Plans 1 (querying) and 2 (the generator) are merged on `main`.

## Global Constraints

- The root module gains no dependency. `github.com/jackc/pgx/v5/pgconn` comes from the pgx module, which is already required. go-sql-driver/mysql and modernc.org/sqlite stay test-only: the library recognizes their errors by type name (`*mysql.MySQLError`, `*sqlite.Error`) and message text.
- Every method of a generic velox type is a generic method (`[_ Lazy[...]]`), except the handles' `column(*E) int32`. `methods_test.go` enforces this.
- Builders are values. Every chain method returns a modified copy, and a slice it appends to is clipped first (`slices.Clip`) so copies never share a backing array.
- Every error velox creates starts with `velox: `. Driver errors are wrapped as `velox: <Entity>: <error>`, or become a `*ConstraintError`. `errors.Is` and `errors.As` still reach the driver's error.
- Generated code does not change: `GeneratedCodeIsVersion1` stays, `velox-gen -check` must still pass, and the handles only gain methods in the library.
- MySQL:
  - Upserts use the row alias (`AS new … ON DUPLICATE KEY UPDATE`), which needs MySQL 8.0.19 or later.
  - `Save` needs the DSN option `clientFoundRows=true`. Without it, MySQL counts a row whose values did not change as not affected.
  - CI's MySQL DSN gains it.
- SQLite enforces foreign keys only when the connection enables them (`_pragma=foreign_keys(1)` in modernc's DSN). velox documents this and does not change it.
- The one-row query allocation budget (`TestOneRowAllocationBudget`, at most 8 more allocations than hand-written code) must still pass.

## Review Focus

1. **No wrong key is read back after a MySQL upsert.**
   - **The risk:** after `ON DUPLICATE KEY UPDATE`, MySQL's LastInsertId does not name the row. With `clientFoundRows` a kept row reports 1 row affected, the same as an insert.
   - **Required:** an upsert on MySQL reads no key back.
   - **Pinned by:** `TestInsertMySQLUpsertReadsNothingBack` (Task 6).
2. **A panic, or a canceled context, inside `Tx` still rolls back**, and the rollback uses a context without cancellation. Pinned by `TestTxRollsBackOnPanic` and `TestTxRollsBackWhenTheContextIsCanceled` (Task 5).
3. **`InsertAll` over several statements is atomic.** Outside a transaction it runs them in one, so a failure in the second batch rolls back the first. Pinned by `TestInsertAllBatchesInOneTransaction` (Task 6).
4. **Hooks cannot change the caller's write data or the statement's kind.** A rewrite hook editing `Set`, `Rows` or `Conflict` in place must not reach the builder. A hook that changes `Op`, sets a column out of range, or makes a row of the wrong width must get an error, not a panic or wrong SQL. Pinned by `TestHooksOnWrites` (Task 2).
5. **Wrapped constraint violations are still classified, and other errors are untouched.** A constraint violation wrapped by other errors (`velox: commit: …`, `fmt.Errorf("%w")`) is still a `*ConstraintError`. Any other driver error keeps its identity for `errors.Is`. Pinned by `TestConstraintErrors` and `TestOtherErrorsAreLeftAlone` (Task 3).

---

## File Structure

| File | Responsibility |
|---|---|
| `statement.go` (modify) | `AssignOp`, `Assignment`, `ConflictClause`; new `Statement` fields |
| `render.go` (modify) | `statement`, `table`, `where`, `columns`, INSERT/UPDATE/DELETE, upsert clause |
| `render_cache.go` (modify) | Shape key covers write fields; cached path renders any kind |
| `schema.go`, `meta.go` (modify) | `TableIn`, `EntityInfo.Schema`; `DefaultFunc`, `ColumnInfo.DefaultFunc` |
| `executor.go` (rewrite) | `exec` and `begin` on both executors; `result`, `txOptions`, `txControl` |
| `db.go` (modify) | `Open`/`OpenPgx` build the new executors; the pipeline's exec path; hook guards for writes; `dbErr` classifies constraints |
| `errors.go` (modify), `errclass.go` | `ConstraintKind`, `ConstraintError`; reading PostgreSQL, MySQL and SQLite errors; `retryable` |
| `pred.go` (modify), `internal/handlegen/handlegen.go` (modify), `handles.go` (regenerated) | `Setter[E]`; handle methods `Set`, `SetNull`, `Add`, `Sub` |
| `write.go` | `UpdateQuery`, `DeleteQuery`, `Save`, shared write helpers |
| `tx.go` | `Tx`, savepoints, `TxOption`s, retry |
| `insert.go` | `InsertQuery`: `Insert`, `InsertAll`, `OnConflict`/`Update`/`DoNothing`, defaults, generated keys, batching |
| `export_test.go` (modify) | `FakeExec` gains `exec`, `begin`, a log, queued errors, results; `SetMaxParams` |
| `internal/testmodel/invoice.go` (modify) | `Timestamps.Mixin` sets `CreatedAt` with `DefaultFunc(time.Now)` |
| `internal/itest/writes_test.go` | Writes and transactions on SQLite, PostgreSQL (both executors) and MySQL |

---

### Task 1: Write statements and their SQL

**Files:**
- Modify: `statement.go`, `render.go`, `render_cache.go`, `schema.go`, `meta.go`
- Test: `render_test.go`, `render_cache_test.go`, `meta_test.go`

**Interfaces:**
- Consumes: plan 1's `writer`, `render`, `renderCache`, `shapeKey`, `Statement`, and the render test helpers `invInfo`, `hID`, `hOrg`, `renderCases`, `argsEqual`, `pgCols`, `mysqlCols`.
- Produces:
  - `type AssignOp uint8` with `AssignSet`, `AssignAdd`, `AssignSub`
  - `type Assignment struct{ Column int; Op AssignOp; Value any }`
  - `type ConflictClause struct{ Columns, Update []int; DoNothing bool }`
  - new `Statement` fields `Set []Assignment`, `Rows [][]any`, `Conflict *ConflictClause`, `Returning []int`, `AllRows bool`
  - writer methods `statement`, `table`, `where`, `columns`, `insertStmt`, `conflict`, `updateStmt`, `deleteStmt`
  - `appendInts(k []byte, xs []int) []byte`
  - `func (s *Schema) TableIn(schema, table string)`; `EntityInfo.Schema string`

- [ ] **Step 1: Write the failing tests**

In `render_test.go`, add a helper and these cases to the slice `renderCases()` returns, before its closing `}`:

```go
		{"insert one row", func() *Statement { return ins([]int{1, 2}, [][]any{{int64(7), "A"}}) }, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2)`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?)",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?)`,
		}, []any{int64(7), "A"}},
		{"insert two rows returning", func() *Statement {
			st := ins([]int{1, 2}, [][]any{{int64(7), "A"}, {int64(8), "B"}})
			st.Returning = []int{0}
			return st
		}, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2), ($3, $4) RETURNING "id"`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?), (?, ?)",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?), (?, ?) RETURNING "id"`,
		}, []any{int64(7), "A", int64(8), "B"}},
		{"upsert update", func() *Statement {
			st := ins([]int{1, 2, 3}, [][]any{{int64(7), "A", 1.5}})
			st.Conflict = &ConflictClause{Columns: []int{1, 2}, Update: []int{3}}
			return st
		}, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number", "total") VALUES ($1, $2, $3) ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total"`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`, `total`) VALUES (?, ?, ?) AS new ON DUPLICATE KEY UPDATE `total` = new.`total`",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number", "total") VALUES (?, ?, ?) ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total"`,
		}, []any{int64(7), "A", 1.5}},
		{"upsert do nothing", func() *Statement {
			st := ins([]int{1, 2}, [][]any{{int64(7), "A"}})
			st.Conflict = &ConflictClause{Columns: []int{1, 2}, DoNothing: true}
			return st
		}, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2) ON CONFLICT ("org_id", "number") DO NOTHING`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?) AS new ON DUPLICATE KEY UPDATE `org_id` = `org_id`",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?) ON CONFLICT ("org_id", "number") DO NOTHING`,
		}, []any{int64(7), "A"}},
		{"update", func() *Statement {
			st := stmt(hOrg.EQ(7))
			st.Op = OpUpdate
			st.Set = []Assignment{{Column: 3, Op: AssignAdd, Value: 2.0}, {Column: 4, Op: AssignSet}, {Column: 2, Op: AssignSub, Value: "x"}}
			return st
		}, map[Dialect]string{
			Postgres: `UPDATE "invoices" SET "total" = "total" + $1, "paid_at" = $2, "number" = "number" - $3 WHERE "org_id" = $4`,
			MySQL:    "UPDATE `invoices` SET `total` = `total` + ?, `paid_at` = ?, `number` = `number` - ? WHERE `org_id` = ?",
			SQLite:   `UPDATE "invoices" SET "total" = "total" + ?, "paid_at" = ?, "number" = "number" - ? WHERE "org_id" = ?`,
		}, []any{2.0, nil, "x", int64(7)}},
		{"delete", func() *Statement {
			st := stmt(hID.In(1, 2))
			st.Op = OpDelete
			return st
		}, map[Dialect]string{
			Postgres: `DELETE FROM "invoices" WHERE "id" IN ($1, $2)`,
			MySQL:    "DELETE FROM `invoices` WHERE `id` IN (?, ?)",
			SQLite:   `DELETE FROM "invoices" WHERE "id" IN (?, ?)`,
		}, []any{int64(1), int64(2)}},
```

Add, after `stmt`:

```go
func ins(cols []int, rows [][]any) *Statement {
	return &Statement{Op: OpInsert, Entity: invInfo, Columns: cols, Rows: rows}
}

func TestRenderSchemaQualifiedTable(t *testing.T) {
	info := &EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices", Tenant: -1, Columns: []ColumnInfo{{Name: "id"}}}
	for d, want := range map[Dialect]string{
		Postgres: `SELECT "id" FROM "billing"."invoices"`,
		MySQL:    "SELECT `id` FROM `billing`.`invoices`",
		SQLite:   `SELECT "id" FROM "billing"."invoices"`,
	} {
		if got, _ := render(d, &Statement{Op: OpSelect, Entity: info}); got != want {
			t.Errorf("%v: %s", d, got)
		}
	}
	del, _ := render(Postgres, &Statement{Op: OpDelete, Entity: info})
	if del != `DELETE FROM "billing"."invoices"` {
		t.Errorf("delete: %s", del)
	}
}
```

In `render_cache_test.go`, add:

```go
func TestCacheKeepsWriteShapesApart(t *testing.T) {
	var c renderCache
	one, _ := c.render(Postgres, ins([]int{1}, [][]any{{int64(1)}}))
	two, _ := c.render(Postgres, ins([]int{1}, [][]any{{int64(1)}, {int64(2)}}))
	up := ins([]int{1}, [][]any{{int64(1)}})
	up.Conflict = &ConflictClause{Columns: []int{1}, Update: []int{1}}
	keep := ins([]int{1}, [][]any{{int64(1)}})
	keep.Conflict = &ConflictClause{Columns: []int{1}, DoNothing: true}
	upSQL, _ := c.render(Postgres, up)
	keepSQL, _ := c.render(Postgres, keep)
	set := &Statement{Op: OpUpdate, Entity: invInfo, Set: []Assignment{{Column: 3, Op: AssignSet, Value: 1.0}}}
	add := &Statement{Op: OpUpdate, Entity: invInfo, Set: []Assignment{{Column: 3, Op: AssignAdd, Value: 1.0}}}
	setSQL, _ := c.render(Postgres, set)
	addSQL, _ := c.render(Postgres, add)
	ret := ins([]int{1}, [][]any{{int64(1)}})
	ret.Returning = []int{0}
	retSQL, _ := c.render(Postgres, ret)
	for _, p := range [][2]string{{one, two}, {upSQL, keepSQL}, {setSQL, addSQL}, {one, retSQL}} {
		if p[0] == p[1] {
			t.Errorf("two shapes share SQL: %s", p[0])
		}
	}
}
```

In `meta_test.go`, add:

```go
type qualified struct{ ID int64 }

func (x *qualified) Schema(s *Schema) { s.TableIn("billing", "invoices") }

func TestTableIn(t *testing.T) {
	info, err := infoOf[qualified]([]string{"ID"}, nil)
	if err != nil || info.Schema != "billing" || info.Table != "invoices" {
		t.Fatalf("%+v, %v", info, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: ConflictClause`, `undefined: Assignment`, `unknown field Rows`, `unknown field Schema`, `s.TableIn undefined`.

- [ ] **Step 3: Write the implementation**

In `statement.go`, add before the `Statement` type:

```go
// AssignOp is how an UPDATE assigns a column.
type AssignOp uint8

// The assignment operators.
const (
	AssignSet AssignOp = iota + 1 // column = value
	AssignAdd                     // column = column + value
	AssignSub                     // column = column - value
)

// Assignment is one column an UPDATE sets.
type Assignment struct {
	Column int // index into EntityInfo.Columns
	Op     AssignOp
	Value  any
}

// ConflictClause is an INSERT's upsert clause.
type ConflictClause struct {
	Columns   []int // the unique key whose conflict the insert resolves
	Update    []int // columns overwritten with the inserted row's values
	DoNothing bool  // keep the existing row; also what an empty Update means
}
```

In the `Statement` struct, change the `Columns` comment to `// SELECT: the selected columns, nil selects all; INSERT: the columns written`, and add after `Unbounded bool …`:

```go
	Set       []Assignment    // UPDATE: the assignments
	Rows      [][]any         // INSERT: the rows, one value per Columns entry
	Conflict  *ConflictClause // INSERT: the upsert clause, if any
	Returning []int           // INSERT: columns read back (PostgreSQL and SQLite)
	AllRows   bool            // UPDATE and DELETE: the statement said All
```

In `schema.go`, add `schema string` beside `table string` in the `Schema` struct, and add:

```go
// TableIn sets the table name and the schema (PostgreSQL) or database
// (MySQL, SQLite) that holds it: SQL names the table schema.table.
func (s *Schema) TableIn(schema, table string) { s.schema, s.table = schema, table }
```

In `meta.go`, add to `EntityInfo`, after `Table string`, the field `Schema string // the schema holding Table; empty for the default`. In `buildInfo`, change the `info := &EntityInfo{Name: name, Table: s.table, …}` literal to include `Schema: s.schema`.

In `render.go`, add `"fmt"` to the imports. In `selectStmt`, replace these lines:

```go
	w.str(" FROM ")
	w.ident(w.in.Table)
	for i, p := range st.Where {
		if i == 0 {
			w.str(" WHERE ")
		} else {
			w.str(" AND ")
		}
		w.pred(p.n)
	}
```

with:

```go
	w.str(" FROM ")
	w.table()
	w.where(st.Where)
```

Replace `render` with the following, and add the new writer methods:

```go
// render returns a statement's SQL and arguments.
func render(d Dialect, st *Statement) (string, []any) {
	w := writer{d: d, in: st.Entity}
	w.statement(st)
	return w.b.String(), w.args
}

// statement writes a statement of any kind.
func (w *writer) statement(st *Statement) {
	switch st.Op {
	case OpSelect:
		w.selectStmt(st)
	case OpInsert:
		w.insertStmt(st)
	case OpUpdate:
		w.updateStmt(st)
	case OpDelete:
		w.deleteStmt(st)
	default:
		panic(fmt.Sprintf("velox: render: unknown statement kind %d", st.Op))
	}
}

// table writes the entity's table, qualified by its schema when it has one.
func (w *writer) table() {
	if w.in.Schema != "" {
		w.ident(w.in.Schema)
		w.str(".")
	}
	w.ident(w.in.Table)
}

func (w *writer) where(ps []Predicate) {
	for i, p := range ps {
		if i == 0 {
			w.str(" WHERE ")
		} else {
			w.str(" AND ")
		}
		w.pred(p.n)
	}
}

func (w *writer) columns(cs []int) {
	for i, c := range cs {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(c))
	}
}

func (w *writer) insertStmt(st *Statement) {
	w.str("INSERT INTO ")
	w.table()
	w.str(" (")
	w.columns(st.Columns)
	w.str(") VALUES ")
	for r, row := range st.Rows {
		if r > 0 {
			w.str(", ")
		}
		w.str("(")
		for i, v := range row {
			if i > 0 {
				w.str(", ")
			}
			w.arg(v)
		}
		w.str(")")
	}
	if st.Conflict != nil {
		w.conflict(st.Conflict)
	}
	if len(st.Returning) > 0 && w.d != MySQL {
		w.str(" RETURNING ")
		w.columns(st.Returning)
	}
}

// conflict writes the upsert clause. MySQL has neither a conflict target nor
// DO NOTHING: ON DUPLICATE KEY UPDATE applies to any unique key, keeping the
// existing row is a no-op assignment, and the inserted values come from the
// row alias new (MySQL 8.0.19 and later).
func (w *writer) conflict(cf *ConflictClause) {
	keep := cf.DoNothing || len(cf.Update) == 0
	if w.d == MySQL {
		w.str(" AS new ON DUPLICATE KEY UPDATE ")
		if keep {
			w.col(int32(cf.Columns[0]))
			w.str(" = ")
			w.col(int32(cf.Columns[0]))
			return
		}
		for i, c := range cf.Update {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(c))
			w.str(" = new.")
			w.col(int32(c))
		}
		return
	}
	w.str(" ON CONFLICT (")
	w.columns(cf.Columns)
	w.str(")")
	if keep {
		w.str(" DO NOTHING")
		return
	}
	w.str(" DO UPDATE SET ")
	for i, c := range cf.Update {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(c))
		w.str(" = EXCLUDED.")
		w.col(int32(c))
	}
}

func (w *writer) updateStmt(st *Statement) {
	w.str("UPDATE ")
	w.table()
	w.str(" SET ")
	for i, a := range st.Set {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(a.Column))
		w.str(" = ")
		switch a.Op {
		case AssignAdd:
			w.col(int32(a.Column))
			w.str(" + ")
		case AssignSub:
			w.col(int32(a.Column))
			w.str(" - ")
		}
		w.arg(a.Value)
	}
	w.where(st.Where)
}

func (w *writer) deleteStmt(st *Statement) {
	w.str("DELETE FROM ")
	w.table()
	w.where(st.Where)
}
```

In `render_cache.go`, change the cached path's `w.selectStmt(st)` to `w.statement(st)`. At the end of `shapeKey`, before `return k`, add:

```go
	k = binary.AppendUvarint(k, uint64(len(st.Set)))
	for _, a := range st.Set {
		k = binary.AppendUvarint(k, uint64(a.Column))
		k = append(k, byte(a.Op))
	}
	k = binary.AppendUvarint(k, uint64(len(st.Rows)))
	if cf := st.Conflict; cf != nil {
		k = append(k, 1, flag(cf.DoNothing))
		k = appendInts(k, cf.Columns)
		k = appendInts(k, cf.Update)
	} else {
		k = append(k, 0)
	}
	k = appendInts(k, st.Returning)
```

and add:

```go
func appendInts(k []byte, xs []int) []byte {
	k = binary.AppendUvarint(k, uint64(len(xs)))
	for _, x := range xs {
		k = binary.AppendUvarint(k, uint64(x))
	}
	return k
}
```

The row width is already in the key: it is the length of `Columns`, which the key encodes.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`. `TestCacheRendersLikeRender` now covers the write cases on all three dialects.

- [ ] **Step 5: Commit**

```bash
git add statement.go render.go render_cache.go schema.go meta.go render_test.go render_cache_test.go meta_test.go
git commit -m "Render INSERT, UPDATE, DELETE and upserts; schema-qualified tables" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 2: Executors that write, and the pipeline's write path

**Files:**
- Rewrite: `executor.go`
- Modify: `db.go`, `query.go` (call sites), `export_test.go`
- Test: `pipeline_internal_test.go` (new, package `velox`)

**Interfaces:**
- Consumes: Task 1's statement fields; plan 1's pipeline (`run`, `plain`, `exec`, `checkColumns`, `hooked`).
- Produces:
  - `type result struct{ affected, lastID int64; hasLastID bool }`
  - `type txOptions struct{ isolation sql.IsolationLevel; readOnly bool; retries int }`
  - `type txControl interface{ commit(context.Context) error; rollback(context.Context) error }`
  - `executor` gains `exec(ctx, sql string, args []any) (result, error)` and `begin(ctx, txOptions) (executor, txControl, error)`
  - `DB` gains `depth int`
  - `run(ctx, st, read func(rows) error, res *result) error`, and the same new parameter on `plain` and `exec`. A nil `read` runs `exec` and fills `*res`.
  - `func execWrite(ctx context.Context, db *DB, st *Statement) (result, error)`
  - `FakeExec` gains fields `Affected int64; LastID int64; HasLastID bool; Log []string; Errs []error; CommitErr error; Isolation sql.IsolationLevel; ReadOnly bool`, with methods `exec` and `begin`.

- [ ] **Step 1: Write the failing tests**

`pipeline_internal_test.go`:

```go
package velox

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestWritesGoThroughExec(t *testing.T) {
	e := &FakeExec{Affected: 3, LastID: 9, HasLastID: true}
	db := OpenFake(Postgres, e)
	st := Statement{Op: OpUpdate, Entity: invInfo, Set: []Assignment{{Column: 3, Op: AssignSet, Value: 1.5}}}
	res, err := execWrite(context.Background(), db, &st)
	if err != nil || res.affected != 3 || res.lastID != 9 || !res.hasLastID {
		t.Fatalf("%+v, %v", res, err)
	}
	if e.SQL != `UPDATE "invoices" SET "total" = $1` || !reflect.DeepEqual(e.Args, []any{1.5}) {
		t.Errorf("%s %v", e.SQL, e.Args)
	}
}

// TestHooksOnWrites is Review Focus 4.
func TestHooksOnWrites(t *testing.T) {
	ctx := context.Background()
	update := func() Statement {
		return Statement{Op: OpUpdate, Entity: invInfo, Set: []Assignment{{Column: 3, Op: AssignSet, Value: 1.5}}}
	}
	t.Run("a hook's edits do not reach the caller", func(t *testing.T) {
		edit := func(_ context.Context, st *Statement) error {
			st.Set[0].Value = 99.0
			if st.Rows != nil {
				st.Rows[0][0] = "changed"
			}
			if st.Conflict != nil {
				st.Conflict.Update[0] = 1
			}
			return nil
		}
		db := OpenFake(Postgres, &FakeExec{}, WithRewrite(edit))
		st := update()
		set := st.Set
		if _, err := execWrite(ctx, db, &st); err != nil {
			t.Fatal(err)
		}
		if set[0].Value != 1.5 {
			t.Errorf("the hook changed the caller's Set: %v", set[0].Value)
		}
		row := []any{"orig"}
		cf := &ConflictClause{Columns: []int{1}, Update: []int{2}}
		ins := Statement{Op: OpInsert, Entity: invInfo, Columns: []int{2}, Rows: [][]any{row}, Conflict: cf}
		if _, err := execWrite(ctx, db, &ins); err != nil {
			t.Fatal(err)
		}
		if row[0] != "orig" || cf.Update[0] != 2 {
			t.Errorf("the hook changed the caller's Rows or Conflict: %v %v", row, cf.Update)
		}
	})
	for name, c := range map[string]struct {
		hook Rewrite
		st   Statement
		want string
	}{
		"kind": {func(_ context.Context, st *Statement) error { st.Op = OpDelete; return nil }, update(), "changed the statement's kind"},
		"set column": {func(_ context.Context, st *Statement) error {
			st.Set = append(st.Set, Assignment{Column: 99, Op: AssignSet})
			return nil
		}, update(), "set column 99"},
		"returning column": {func(_ context.Context, st *Statement) error { st.Returning = []int{42}; return nil },
			Statement{Op: OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}}, "set column 42"},
		"row width": {func(_ context.Context, st *Statement) error { st.Rows[0] = append(st.Rows[0], 2); return nil },
			Statement{Op: OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}}, "a row of 2 values for 1 columns"},
	} {
		t.Run(name, func(t *testing.T) {
			e := &FakeExec{}
			db := OpenFake(Postgres, e, WithRewrite(c.hook))
			st := c.st
			if _, err := execWrite(ctx, db, &st); err == nil || !strings.Contains(err.Error(), c.want) || e.Calls != 0 {
				t.Errorf("%v (calls %d), want an error containing %q", err, e.Calls, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestWritesGoThroughExec|TestHooksOnWrites' .`
Expected: FAIL to build: `undefined: execWrite`, and `unknown field Affected in struct literal of type FakeExec`.

- [ ] **Step 3: Rewrite `executor.go`**

```go
package velox

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// rows is what an executor returns for a query; *sql.Rows (wrapped) and
// pgx.Rows both provide it.
type rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
	Close()
}

// result is what a statement that returns no rows reports.
type result struct {
	affected  int64
	lastID    int64
	hasLastID bool // the driver reports LastInsertId (MySQL, SQLite)
}

// txOptions are a transaction's options; TxOption sets them.
type txOptions struct {
	isolation sql.IsolationLevel
	readOnly  bool
	retries   int
}

// txControl ends a transaction.
type txControl interface {
	commit(ctx context.Context) error
	rollback(ctx context.Context) error
}

// executor runs rendered statements.
type executor interface {
	query(ctx context.Context, sql string, args []any) (rows, error)
	exec(ctx context.Context, sql string, args []any) (result, error)
	// begin starts a transaction and returns the executor that runs in it.
	begin(ctx context.Context, o txOptions) (executor, txControl, error)
}

type sqlConn interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sqlExecutor runs statements through database/sql: through db outside a
// transaction, through a *sql.Tx inside one.
type sqlExecutor struct {
	c  sqlConn
	db *sql.DB // nil inside a transaction
}

func (e sqlExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.c.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{r}, nil
}

func (e sqlExecutor) exec(ctx context.Context, s string, args []any) (result, error) {
	res, err := e.c.ExecContext(ctx, s, args...)
	if err != nil {
		return result{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return result{}, err
	}
	id, idErr := res.LastInsertId()
	return result{affected: n, lastID: id, hasLastID: idErr == nil}, nil
}

func (e sqlExecutor) begin(ctx context.Context, o txOptions) (executor, txControl, error) {
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: o.isolation, ReadOnly: o.readOnly})
	if err != nil {
		return nil, nil, err
	}
	return sqlExecutor{c: tx}, sqlTx{tx}, nil
}

type sqlTx struct{ tx *sql.Tx }

func (t sqlTx) commit(context.Context) error   { return t.tx.Commit() }
func (t sqlTx) rollback(context.Context) error { return t.tx.Rollback() }

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }

type pgxConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type pgxBeginner interface {
	BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error)
}

// pgxExecutor runs statements through pgx's native protocol: through the
// pool outside a transaction, through a pgx.Tx inside one.
type pgxExecutor struct {
	c    pgxConn
	pool pgxBeginner // nil inside a transaction
}

func (e pgxExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.c.Query(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (e pgxExecutor) exec(ctx context.Context, s string, args []any) (result, error) {
	tag, err := e.c.Exec(ctx, s, args...)
	if err != nil {
		return result{}, err
	}
	return result{affected: tag.RowsAffected()}, nil
}

func (e pgxExecutor) begin(ctx context.Context, o txOptions) (executor, txControl, error) {
	mode := pgx.ReadWrite
	if o.readOnly {
		mode = pgx.ReadOnly
	}
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgxIsolation(o.isolation), AccessMode: mode})
	if err != nil {
		return nil, nil, err
	}
	return pgxExecutor{c: tx}, pgxTx{tx}, nil
}

// pgxIsolation maps database/sql's isolation levels to pgx's; the default
// leaves the server's.
func pgxIsolation(l sql.IsolationLevel) pgx.TxIsoLevel {
	switch l {
	case sql.LevelReadUncommitted:
		return pgx.ReadUncommitted
	case sql.LevelReadCommitted:
		return pgx.ReadCommitted
	case sql.LevelRepeatableRead, sql.LevelSnapshot:
		return pgx.RepeatableRead
	case sql.LevelSerializable, sql.LevelLinearizable:
		return pgx.Serializable
	}
	return ""
}

type pgxTx struct{ tx pgx.Tx }

func (t pgxTx) commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTx) rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
```

- [ ] **Step 4: Change the pipeline in `db.go`**

1. Add `depth int // savepoint nesting inside a transaction` to `DB`.
2. Change `Open` to `return newDB(d, sqlExecutor{c: db, db: db}, opts)` and `OpenPgx` to `return newDB(Postgres, pgxExecutor{c: pool, pool: pool}, opts)`.
3. Give `plain`, `run` and `exec` a final parameter `res *result`, and pass it through `plain → exec`, `run → exec` (both direct and inside `next`).
4. In `run`, replace the clone block with:

```go
	if db.hooked() {
		st.Where = slices.Clone(st.Where)
		st.OrderBy = slices.Clone(st.OrderBy)
		st.Columns = slices.Clone(st.Columns)
		st.Set = slices.Clone(st.Set)
		st.Returning = slices.Clone(st.Returning)
		if st.Rows != nil {
			rows := make([][]any, len(st.Rows))
			for i, r := range st.Rows {
				rows[i] = slices.Clone(r)
			}
			st.Rows = rows
		}
		if st.Conflict != nil {
			cf := *st.Conflict
			cf.Columns, cf.Update = slices.Clone(cf.Columns), slices.Clone(cf.Update)
			st.Conflict = &cf
		}
	}
```

5. Change `entity := st.Entity` to `entity, op := st.Entity, st.Op`. After the rewrite loop, before the `checkColumns` call, add:

```go
	if st.Op != op {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's kind", entity.Name)
	}
```

6. In `checkColumns`, add after the `OrderBy` loop:

```go
	for _, a := range st.Set {
		if err := bad(a.Column); err != nil {
			return err
		}
	}
	for _, c := range st.Returning {
		if err := bad(c); err != nil {
			return err
		}
	}
	if cf := st.Conflict; cf != nil {
		for _, c := range append(slices.Clip(cf.Columns), cf.Update...) {
			if err := bad(c); err != nil {
				return err
			}
		}
	}
	for _, r := range st.Rows {
		if len(r) != len(st.Columns) {
			return fmt.Errorf("velox: %s: a Rewrite hook made a row of %d values for %d columns", st.Entity.Name, len(r), len(st.Columns))
		}
	}
```

7. At the start of `exec`, add the no-rows path:

```go
	if read == nil {
		r, err := db.ex.exec(ctx, st.SQL, st.Args)
		if err != nil {
			return dbErr(st.Entity, err)
		}
		if res != nil {
			*res = r
		}
		return nil
	}
```

8. Add:

```go
// execWrite runs a statement that returns no rows through the pipeline.
func execWrite(ctx context.Context, db *DB, st *Statement) (result, error) {
	var res result
	var err error
	if db.hooked() {
		err = db.run(ctx, st, nil, &res)
	} else {
		err = db.plain(ctx, st, nil, &res)
	}
	return res, err
}
```

In `query.go`, pass `nil` as the new last argument at every `q.db.run(…)` and `q.db.plain(…)` call (six calls, in `All`, `Iter` and `Count`). In `cloneStatement`, also clip `c.Set` and `c.Returning`.

- [ ] **Step 5: Extend the fake executor**

In `export_test.go`, add `"database/sql"` to the imports, these fields to `FakeExec`:

```go
	Affected  int64   // what exec reports
	LastID    int64
	HasLastID bool
	Log       []string // every statement, and BEGIN, COMMIT, ROLLBACK
	Errs      []error  // returned by the next calls, one each, before Err
	CommitErr error
	Isolation sql.IsolationLevel // the last transaction's options
	ReadOnly  bool
```

and replace `query` with the following, plus the new methods:

```go
// next is the error the next call returns: the first of Errs, then Err.
func (e *FakeExec) next() error {
	if len(e.Errs) > 0 {
		err := e.Errs[0]
		e.Errs = e.Errs[1:]
		return err
	}
	return e.Err
}

func (e *FakeExec) query(_ context.Context, sql string, args []any) (rows, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	if err := e.next(); err != nil {
		return nil, err
	}
	return &fakeRows{data: e.Rows, e: e}, nil
}

func (e *FakeExec) exec(_ context.Context, sql string, args []any) (result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SQL, e.Args = sql, args
	e.Calls++
	e.Log = append(e.Log, sql)
	if err := e.next(); err != nil {
		return result{}, err
	}
	return result{affected: e.Affected, lastID: e.LastID, hasLastID: e.HasLastID}, nil
}

func (e *FakeExec) begin(_ context.Context, o txOptions) (executor, txControl, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Log = append(e.Log, "BEGIN")
	e.Isolation, e.ReadOnly = o.isolation, o.readOnly
	return e, fakeTx{e}, nil
}

type fakeTx struct{ e *FakeExec }

func (t fakeTx) commit(context.Context) error {
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	t.e.Log = append(t.e.Log, "COMMIT")
	return t.e.CommitErr
}

func (t fakeTx) rollback(context.Context) error {
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	t.e.Log = append(t.e.Log, "ROLLBACK")
	return nil
}
```

The existing tests that set `Err` keep working: `next` returns `Err` when `Errs` is empty.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`. The SQLite integration tests exercise the rebuilt `sqlExecutor`'s query path. Its `exec` and `begin` paths are exercised in Task 7.

- [ ] **Step 7: Commit**

```bash
git add executor.go db.go query.go export_test.go pipeline_internal_test.go
git commit -m "Give executors exec and begin; run writes through the pipeline" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 3: Constraint errors and retryable failures

**Files:**
- Modify: `errors.go`, `db.go` (`dbErr`)
- Create: `errclass.go`
- Test: `errclass_test.go` (package `velox`)

**Interfaces:**
- Consumes: `dbErr`, `EntityInfo` (plan 1).
- Produces:
  - `type ConstraintKind uint8` with `Unique`, `ForeignKey`, `NotNull`, `Check` and `String()`
  - `type ConstraintError struct{ Entity string; Kind ConstraintKind; Constraint string; Err error }` with `Error()` and `Unwrap()`
  - `func constraint(entity string, err error) *ConstraintError`
  - `func retryable(err error) bool`
  - `mysqlError`, `sqliteError` and `between` helpers

- [ ] **Step 1: Write the failing tests**

`errclass_test.go`:

```go
package velox

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func myErr(n uint16, msg string) error {
	return &mysql.MySQLError{Number: n, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: msg}
}

// TestConstraintErrors is half of Review Focus 5.
func TestConstraintErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		kind ConstraintKind
		name string
	}{
		{&pgconn.PgError{Code: "23505", ConstraintName: "p_code_uq"}, Unique, "p_code_uq"},
		{&pgconn.PgError{Code: "23503", ConstraintName: "c_p_fk"}, ForeignKey, "c_p_fk"},
		{&pgconn.PgError{Code: "23502", ColumnName: "code"}, NotNull, "code"},
		{&pgconn.PgError{Code: "23514", ConstraintName: "p_n_ck"}, Check, "p_n_ck"},
		{myErr(1062, "Duplicate entry 'a' for key 'p.p_code_uq'"), Unique, "p_code_uq"},
		{myErr(1452, "Cannot add or update a child row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ForeignKey, "c_p_fk"},
		{myErr(1451, "Cannot delete or update a parent row: a foreign key constraint fails (`velox`.`c`, CONSTRAINT `c_p_fk` FOREIGN KEY (`p_id`) REFERENCES `p` (`id`))"), ForeignKey, "c_p_fk"},
		{myErr(1048, "Column 'code' cannot be null"), NotNull, "code"},
		{myErr(3819, "Check constraint 'p_n_ck' is violated."), Check, "p_n_ck"},
		{fmt.Errorf("velox: commit: %w", &pgconn.PgError{Code: "23505", ConstraintName: "late_uq"}), Unique, "late_uq"},
	} {
		got := dbErr(invInfo, c.err)
		var ce *ConstraintError
		if !errors.As(got, &ce) || ce.Kind != c.kind || ce.Constraint != c.name || ce.Entity != "velox.inv" {
			t.Errorf("%v: got %#v", c.err, got)
			continue
		}
		if !errors.Is(got, c.err) {
			t.Errorf("%v: the driver's error is not reachable", c.err)
		}
	}
	ce := &ConstraintError{Entity: "billing.Invoice", Kind: Unique, Constraint: "p_code_uq", Err: io.EOF}
	if want := "velox: billing.Invoice: unique constraint p_code_uq violated: EOF"; ce.Error() != want {
		t.Errorf("%q", ce.Error())
	}
}

// TestOtherErrorsAreLeftAlone is the other half of Review Focus 5.
func TestOtherErrorsAreLeftAlone(t *testing.T) {
	for _, err := range []error{io.EOF, &pgconn.PgError{Code: "42601"}, myErr(1064, "You have an error in your SQL syntax")} {
		got := dbErr(invInfo, err)
		var ce *ConstraintError
		if errors.As(got, &ce) || !errors.Is(got, err) || got.Error() != "velox: velox.inv: "+err.Error() {
			t.Errorf("%v became %v", err, got)
		}
	}
}

func TestRetryable(t *testing.T) {
	for err, want := range map[error]bool{
		&pgconn.PgError{Code: "40001"}: true,
		&pgconn.PgError{Code: "40P01"}: true,
		&pgconn.PgError{Code: "23505"}: false,
		myErr(1213, "Deadlock found when trying to get lock"): true,
		myErr(1205, "Lock wait timeout exceeded"):              true,
		myErr(1062, "Duplicate entry"):                        false,
		fmt.Errorf("velox: commit: %w", &pgconn.PgError{Code: "40001"}): true,
		io.EOF: false,
	} {
		if got := retryable(err); got != want {
			t.Errorf("retryable(%v) = %v, want %v", err, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestConstraintErrors|TestOtherErrors|TestRetryable' .`
Expected: FAIL to build: `undefined: ConstraintError`, `undefined: retryable`.

- [ ] **Step 3: Write the implementation**

Append to `errors.go` (and add `"fmt"` to its imports):

```go
// ConstraintKind is the kind of constraint a statement violated.
type ConstraintKind uint8

// The kinds of constraint.
const (
	Unique ConstraintKind = iota + 1 // a unique index or primary key
	ForeignKey
	NotNull
	Check
)

func (k ConstraintKind) String() string {
	switch k {
	case Unique:
		return "unique"
	case ForeignKey:
		return "foreign key"
	case NotNull:
		return "not null"
	case Check:
		return "check"
	}
	return "unknown"
}

// ConstraintError reports a violated constraint, in the same form on every
// dialect; find it with errors.As. Err is the driver's error.
type ConstraintError struct {
	Entity string
	Kind   ConstraintKind
	// Constraint is the constraint's name. SQLite reports the columns instead
	// ("invoices.org_id, invoices.number"), and a NOT NULL error names the
	// column.
	Constraint string
	Err        error
}

func (e *ConstraintError) Error() string {
	name := e.Constraint
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("velox: %s: %s constraint %s violated: %v", e.Entity, e.Kind, name, e.Err)
}

func (e *ConstraintError) Unwrap() error { return e.Err }
```

`errclass.go`:

```go
package velox

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// constraint reads a driver error as a violated constraint, or returns nil.
// PostgreSQL's errors are read from pgx's fields. velox does not import the
// MySQL and SQLite drivers, so their errors are recognized by type name and
// read from their text.
func constraint(entity string, err error) *ConstraintError {
	var kind ConstraintKind
	var name string
	var pg *pgconn.PgError
	switch {
	case errors.As(err, &pg):
		kind, name = pgKind(pg)
	default:
		if n, msg, ok := mysqlError(err); ok {
			kind, name = mysqlKind(n, msg)
		} else if code, msg, ok := sqliteError(err); ok {
			kind, name = sqliteKind(code, msg)
		}
	}
	if kind == 0 {
		return nil
	}
	return &ConstraintError{Entity: entity, Kind: kind, Constraint: name, Err: err}
}

func pgKind(pg *pgconn.PgError) (ConstraintKind, string) {
	switch pg.Code {
	case "23505":
		return Unique, pg.ConstraintName
	case "23503":
		return ForeignKey, pg.ConstraintName
	case "23502":
		return NotNull, pg.ColumnName
	case "23514":
		return Check, pg.ConstraintName
	}
	return 0, ""
}

// mysqlError reads a go-sql-driver/mysql error, whose text is
// "Error 1062 (23000): Duplicate entry 'a' for key 'p.p_code_uq'".
func mysqlError(err error) (int, string, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if fmt.Sprintf("%T", e) != "*mysql.MySQLError" {
			continue
		}
		msg := e.Error()
		var n int
		if _, scanErr := fmt.Sscanf(msg, "Error %d", &n); scanErr == nil {
			return n, msg, true
		}
	}
	return 0, "", false
}

func mysqlKind(n int, msg string) (ConstraintKind, string) {
	switch n {
	case 1062:
		name := between(msg, "for key '", "'")
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:] // MySQL 8 prefixes the table
		}
		return Unique, name
	case 1451, 1452:
		return ForeignKey, between(msg, "CONSTRAINT `", "`")
	case 1048:
		return NotNull, between(msg, "Column '", "'")
	case 3819:
		return Check, between(msg, "Check constraint '", "'")
	}
	return 0, ""
}

// sqliteError reads a modernc.org/sqlite error, whose Code is SQLite's
// extended result code and whose text ends with the constraint's subject:
// "constraint failed: UNIQUE constraint failed: p.code (2067)".
func sqliteError(err error) (int, string, bool) {
	var c interface{ Code() int }
	if errors.As(err, &c) && fmt.Sprintf("%T", c) == "*sqlite.Error" {
		return c.Code(), err.Error(), true
	}
	return 0, "", false
}

func sqliteKind(code int, msg string) (ConstraintKind, string) {
	subject := ""
	if i := strings.LastIndex(msg, "failed: "); i >= 0 {
		subject = msg[i+len("failed: "):]
		if j := strings.LastIndex(subject, " ("); j >= 0 {
			subject = subject[:j]
		}
	}
	switch code {
	case 2067, 1555: // SQLITE_CONSTRAINT_UNIQUE, SQLITE_CONSTRAINT_PRIMARYKEY
		return Unique, subject
	case 787: // SQLITE_CONSTRAINT_FOREIGNKEY
		return ForeignKey, subject
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		return NotNull, subject
	case 275: // SQLITE_CONSTRAINT_CHECK
		return Check, subject
	}
	return 0, ""
}

// between returns the text of s after the first from and before the next to.
func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i+len(from):]
	j := strings.Index(s, to)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// retryable reports a serialization failure or a deadlock, after which
// running the transaction again can succeed.
func retryable(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	if n, _, ok := mysqlError(err); ok {
		return n == 1213 || n == 1205
	}
	if code, _, ok := sqliteError(err); ok {
		return code&0xff == 5 || code&0xff == 6 // SQLITE_BUSY, SQLITE_LOCKED and their extended codes
	}
	return false
}
```

In `db.go`, replace `dbErr` with:

```go
// dbErr names the entity in an error from the database or the driver, and
// turns a violated constraint into a *ConstraintError.
func dbErr(info *EntityInfo, err error) error {
	if ce := constraint(info.Name, err); ce != nil {
		return ce
	}
	return fmt.Errorf("velox: %s: %w", info.Name, err)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`. The SQLite classification is tested against the real driver in Task 7.

- [ ] **Step 5: Commit**

```bash
git add errors.go errclass.go db.go errclass_test.go
git commit -m "Report constraint violations as ConstraintError on every dialect" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 4: Setters, update, delete and `Save`

**Files:**
- Modify: `pred.go`, `internal/handlegen/handlegen.go`, `handles.go` (regenerated), `schema.go`, `meta.go`, `query.go` (`cloneStatement`), `internal/testmodel/invoice.go`
- Create: `write.go`
- Test: `handles_test.go`, `write_test.go` (package `velox_test`)

**Interfaces:**
- Consumes: Tasks 1–3 (`Assignment`, `execWrite`, `ColumnEQ`, `Statement.AllRows`).
- Produces:
  - `type Setter[E any] struct{ a Assignment }`
  - handle methods: `Set(v V) Setter[E]` on every kind, `SetNull() Setter[E]` on the `Null…` kinds, and `Add(v V) Setter[E]` and `Sub(v V) Setter[E]` on `NumberCol` and `NullNumberCol`
  - `ColumnDef.DefaultFunc[_ Lazy[V]](f func() V) *ColumnDef[V]`; `ColumnInfo.DefaultFunc func() any`
  - `type UpdateQuery[E any]` with `Set`, `Where`, `All`, `Exec(ctx) (int64, error)` and `SQL()`, made by `func (db *DB) Update[E any, PE Entity[E]]() UpdateQuery[E]`
  - `type DeleteQuery[E any]` with `Where`, `All`, `Exec` and `SQL`, made by `func (db *DB) Delete[E any, PE Entity[E]]() DeleteQuery[E]`
  - `func (db *DB) Save[E any, PE Entity[E]](ctx context.Context, x PE, cols ...Column[E]) error`
  - helpers `withOnUpdate`, `fieldValue[E]`, `setField[E]`

- [ ] **Step 1: Write the failing tests**

In `handles_test.go`, add to the `cases` table of `TestHandleMethodsBuildNodes`'s neighbour, a new test:

```go
func TestSetters(t *testing.T) {
	n := NumberCol[ent, int64]{I: 3}
	ns := NullStringCol[ent, string]{I: 5}
	b := Col[ent, bool]{I: 6}
	for name, c := range map[string]struct{ got, want Assignment }{
		"Set":     {n.Set(7).a, Assignment{Column: 3, Op: AssignSet, Value: int64(7)}},
		"Add":     {n.Add(2).a, Assignment{Column: 3, Op: AssignAdd, Value: int64(2)}},
		"Sub":     {n.Sub(2).a, Assignment{Column: 3, Op: AssignSub, Value: int64(2)}},
		"SetNull": {ns.SetNull().a, Assignment{Column: 5, Op: AssignSet}},
		"Set on a Null handle": {ns.Set("x").a, Assignment{Column: 5, Op: AssignSet, Value: "x"}},
		"Set bool": {b.Set(true).a, Assignment{Column: 6, Op: AssignSet, Value: true}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: %+v, want %+v", name, c.got, c.want)
		}
	}
}
```

`write_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestUpdateSetsOnUpdateColumns(t *testing.T) {
	e := &velox.FakeExec{Affected: 2}
	db := velox.OpenFake(velox.Postgres, e)
	n, err := db.Update[testmodel.Invoice]().Set(I.Total.Add(5), I.Note.SetNull()).Where(I.OrgID.EQ(1)).Exec(ctx)
	if err != nil || n != 2 {
		t.Fatalf("%d, %v", n, err)
	}
	want := `UPDATE "invoices" SET "total" = "total" + $1, "note" = $2, "updated_at" = $3 WHERE "org_id" = $4`
	if e.SQL != want {
		t.Fatalf("\n got %s\nwant %s", e.SQL, want)
	}
	if e.Args[0] != 5.0 || e.Args[1] != nil || e.Args[3] != int64(1) {
		t.Errorf("args %v", e.Args)
	}
	if at, ok := e.Args[2].(time.Time); !ok || time.Since(at) > time.Minute {
		t.Errorf("updated_at %v", e.Args[2])
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.UpdatedAt.Set(t0)).Where(I.ID.EQ(1)).Exec(ctx); err != nil || e.SQL != `UPDATE "invoices" SET "updated_at" = $1 WHERE "id" = $2` {
		t.Errorf("an explicit UpdatedAt must not be set twice: %s %v", e.SQL, err)
	}
}

func TestUpdateNeedsSet(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "Update has no Set") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

func TestAllMarksTheStatement(t *testing.T) {
	var all []bool
	spy := func(_ context.Context, st *velox.Statement) error { all = append(all, st.AllRows); return nil }
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(spy))
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[testmodel.Invoice]().All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, []bool{true, true, false}) {
		t.Errorf("AllRows %v", all)
	}
}

func TestDelete(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.MySQL, e)
	n, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(4)).Exec(ctx)
	if err != nil || n != 1 || e.SQL != "DELETE FROM `invoices` WHERE `id` = ?" {
		t.Errorf("%d %v %s", n, err, e.SQL)
	}
	if text, _ := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(4)).SQL(); text != e.SQL {
		t.Errorf("SQL() %s", text)
	}
}

func TestSave(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X"}
	if err := db.Save(ctx, &inv, I.Number); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "number" = $1, "updated_at" = $2 WHERE "id" = $3`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if inv.UpdatedAt.IsZero() || e.Args[0] != "X" || e.Args[2] != int64(3) || e.Args[1] != inv.UpdatedAt {
		t.Errorf("args %v, UpdatedAt %v", e.Args, inv.UpdatedAt)
	}
	e.Affected = 0
	if err := db.Save(ctx, &inv, I.Number); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("no row: %v", err)
	}
	if err := db.Save(ctx, &inv); err == nil || !strings.Contains(err.Error(), "Save names no columns") {
		t.Errorf("no columns: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `n.Set undefined`, `db.Update undefined`, `db.Save undefined`.

- [ ] **Step 3: Write the implementation**

In `pred.go`, add:

```go
// Setter is an assignment to a column of entity E, made by a handle's Set,
// SetNull, Add or Sub.
type Setter[E any] struct{ a Assignment }
```

In `internal/handlegen/handlegen.go`, add method groups and give each kind its setters:

```go
	set = []method{
		{"Set", "sets the column to v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet, Value: v}}"},
	}
	arith = []method{
		{"Add", "adds v to the column: column = column + v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignAdd, Value: v}}"},
		{"Sub", "subtracts v from the column: column = column - v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSub, Value: v}}"},
	}
```

Add to the `null` group:

```go
		{"SetNull", "sets the column to NULL.", "() Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet}}"},
```

and change `kinds`' method lists to:

| kind | groups |
|---|---|
| `Col` | `{base, set}` |
| `OrderedCol` | `{base, ordered, set}` |
| `NumberCol` | `{base, ordered, set, arith}` |
| `StringCol` | `{base, ordered, text, set}` |
| `NullCol` | `{base, set, null}` |
| `NullOrderedCol` | `{base, ordered, set, null}` |
| `NullNumberCol` | `{base, ordered, set, arith, null}` |
| `NullStringCol` | `{base, ordered, text, set, null}` |

Then run `go generate .`.

In `schema.go`, change `Default`'s doc comment to `// Default sets the value an insert writes when the field holds its zero value.` and `OnUpdate`'s to `// OnUpdate sets a function whose result every insert and update writes to the column.` Then add:

```go
// DefaultFunc sets a function whose result an insert writes when the field
// holds its zero value, such as time.Now for a creation time.
func (c *ColumnDef[V]) DefaultFunc[_ Lazy[V]](f func() V) *ColumnDef[V] {
	colInfo(c.s, c.i).DefaultFunc = func() any { return f() }
	return c
}
```

In `meta.go`'s `ColumnInfo`, add after `HasDefault bool`: `DefaultFunc func() any // nil when none`.

In `internal/testmodel/invoice.go`, change the mixin to:

```go
// Mixin sets CreatedAt when an Invoice is inserted, and UpdatedAt on every
// insert and update.
func (t *Timestamps) Mixin(s *velox.Schema) {
	s.Column(&t.CreatedAt).DefaultFunc(time.Now)
	s.Column(&t.UpdatedAt).OnUpdate(time.Now)
}
```

`write.go`:

```go
package velox

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// UpdateQuery is an UPDATE of entity E, built by chaining and run by Exec.
// Like Query it is a value: extending it leaves it as it was.
type UpdateQuery[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
}

// Update starts an UPDATE of entity E:
//
//	db.Update[billing.Invoice]().Set(I.PaidAt.Set(now)).Where(I.ID.EQ(id)).Exec(ctx)
func (db *DB) Update[E any, PE Entity[E]]() UpdateQuery[E] {
	m := PE(nil).VeloxMeta()
	return UpdateQuery[E]{db: db, meta: m, st: Statement{Op: OpUpdate, Entity: m.info, InTx: db.inTx}}
}

// Set adds assignments, made by the handles' Set, SetNull, Add and Sub.
func (u UpdateQuery[E]) Set[_ Lazy[E]](ss ...Setter[E]) UpdateQuery[E] {
	u.st.Set = slices.Clip(u.st.Set)
	for _, s := range ss {
		u.st.Set = append(u.st.Set, s.a)
	}
	return u
}

// Where adds conditions; all of them must hold.
func (u UpdateQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) UpdateQuery[E] {
	u.st.Where = slices.Clip(u.st.Where)
	for _, p := range ps {
		u.st.Where = append(u.st.Where, Predicate(p))
	}
	return u
}

// All declares that the update has no Where on purpose and changes every
// row. The guardrails refuse an update with neither (sub-project 2).
func (u UpdateQuery[E]) All[_ Lazy[E]]() UpdateQuery[E] {
	u.st.AllRows = true
	return u
}

// statement is the UPDATE Exec and SQL run: Set's assignments, then one for
// every column with OnUpdate that Set did not name.
func (u UpdateQuery[E]) statement[_ Lazy[E]]() (Statement, error) {
	if u.meta.err != nil {
		return Statement{}, u.meta.err
	}
	if len(u.st.Set) == 0 {
		return Statement{}, fmt.Errorf("velox: %s: Update has no Set", u.meta.info.Name)
	}
	st := cloneStatement(&u.st)
	st.Set = withOnUpdate(u.meta.info, st.Set, nil)
	return st, nil
}

// Exec runs the update and returns the number of rows it changed.
func (u UpdateQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	st, err := u.statement()
	if err != nil {
		return 0, err
	}
	res, err := execWrite(ctx, u.db, &st)
	return res.affected, err
}

// SQL returns the statement and arguments Exec would send, without a
// database and before rewrite hooks; "" when the update cannot run.
func (u UpdateQuery[E]) SQL[_ Lazy[E]]() (string, []any) {
	st, err := u.statement()
	if err != nil {
		return "", nil
	}
	return render(u.db.d, &st)
}

// DeleteQuery is a DELETE of entity E, built by chaining and run by Exec.
type DeleteQuery[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
}

// Delete starts a DELETE of entity E.
func (db *DB) Delete[E any, PE Entity[E]]() DeleteQuery[E] {
	m := PE(nil).VeloxMeta()
	return DeleteQuery[E]{db: db, meta: m, st: Statement{Op: OpDelete, Entity: m.info, InTx: db.inTx}}
}

// Where adds conditions; all of them must hold.
func (d DeleteQuery[E]) Where[_ Lazy[E]](ps ...Pred[E]) DeleteQuery[E] {
	d.st.Where = slices.Clip(d.st.Where)
	for _, p := range ps {
		d.st.Where = append(d.st.Where, Predicate(p))
	}
	return d
}

// All declares that the delete has no Where on purpose and removes every
// row. The guardrails refuse a delete with neither (sub-project 2).
func (d DeleteQuery[E]) All[_ Lazy[E]]() DeleteQuery[E] {
	d.st.AllRows = true
	return d
}

// Exec runs the delete and returns the number of rows it removed.
func (d DeleteQuery[E]) Exec[_ Lazy[E]](ctx context.Context) (int64, error) {
	if d.meta.err != nil {
		return 0, d.meta.err
	}
	st := cloneStatement(&d.st)
	res, err := execWrite(ctx, d.db, &st)
	return res.affected, err
}

// SQL returns the statement and arguments Exec would send, without a
// database and before rewrite hooks.
func (d DeleteQuery[E]) SQL[_ Lazy[E]]() (string, []any) {
	if d.meta.err != nil {
		return "", nil
	}
	st := cloneStatement(&d.st)
	return render(d.db.d, &st)
}

// Save writes the named columns of *x to the row with x's primary key, and
// every column with OnUpdate, whose new value it also stores in *x. Its error
// wraps ErrNotFound when no row has that key. On MySQL, open the database with
// clientFoundRows=true: otherwise a row whose values did not change counts as
// not found.
func (db *DB) Save[E any, PE Entity[E]](ctx context.Context, x PE, cols ...Column[E]) error {
	m := x.VeloxMeta()
	if m.err != nil {
		return m.err
	}
	in := m.info
	if len(cols) == 0 {
		return fmt.Errorf("velox: %s: Save names no columns", in.Name)
	}
	p := (*E)(x)
	st := Statement{Op: OpUpdate, Entity: in, InTx: db.inTx}
	for _, c := range cols {
		i := int(c.column(nil))
		st.Set = append(st.Set, Assignment{Column: i, Op: AssignSet, Value: fieldValue(m, p, i)})
	}
	st.Set = withOnUpdate(in, st.Set, func(i int, v any) { setField(m, p, i, v) })
	for _, k := range in.PrimaryKey {
		st.Where = append(st.Where, ColumnEQ(k, fieldValue(m, p, k)))
	}
	res, err := execWrite(ctx, db, &st)
	if err != nil {
		return err
	}
	if res.affected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, in.Name)
	}
	return nil
}

// withOnUpdate appends an assignment for every column with OnUpdate that set
// does not assign, passing each value to apply when it is not nil.
func withOnUpdate(info *EntityInfo, set []Assignment, apply func(col int, v any)) []Assignment {
	named := make(map[int]bool, len(set))
	for _, a := range set {
		named[a.Column] = true
	}
	out := slices.Clip(set)
	for i := range info.Columns {
		if f := info.Columns[i].OnUpdate; f != nil && !named[i] {
			v := f()
			if apply != nil {
				apply(i, v)
			}
			out = append(out, Assignment{Column: i, Op: AssignSet, Value: v})
		}
	}
	return out
}

// fieldValue is the value of column i's field in *p.
func fieldValue[E any](m *Meta[E], p *E, i int) any {
	return reflect.ValueOf(m.addr(p, i)).Elem().Interface()
}

// setField stores v in column i's field in *p.
func setField[E any](m *Meta[E], p *E, i int, v any) {
	reflect.ValueOf(m.addr(p, i)).Elem().Set(reflect.ValueOf(v))
}
```

In `query.go`'s `cloneStatement`, add `c.Set = slices.Clip(c.Set)` and `c.Returning = slices.Clip(c.Returning)`, if Task 2 did not already.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go generate . && go test -race ./... && go vet ./...`
Expected:
- All `ok`.
- `TestHandlesFileIsCurrent` and `TestMethodsOfGenericTypesAreGeneric` pass. The new `statement` method of `UpdateQuery` is generic, so the guard holds.
- `velox-gen -check` would still pass, because generated files do not change. Run `go -C cmd/velox-gen build -o "$PWD/bin/velox-gen" . && bin/velox-gen -check ./...` to confirm.

- [ ] **Step 5: Commit**

```bash
git add pred.go handles.go internal/handlegen schema.go meta.go query.go internal/testmodel/invoice.go write.go handles_test.go write_test.go
git commit -m "Add setters, Update, Delete and Save" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 5: Transactions

**Files:**
- Create: `tx.go`
- Test: `tx_test.go` (package `velox_test`)

**Interfaces:**
- Consumes: `executor.begin`, `txOptions`, `txControl` and `DB.depth` (Task 2); `retryable` (Task 3); `UpdateQuery` (Task 4, used by the tests).
- Produces:
  - `type TxOption func(*txOptions)`
  - `func Isolation(level sql.IsolationLevel) TxOption`, `func ReadOnly() TxOption`, `func RetrySerializable(n int) TxOption`
  - `func (db *DB) Tx(ctx context.Context, fn func(tx *DB) error, opts ...TxOption) error`
  - `DB.Tx` is used by Task 6's `InsertAll`.

- [ ] **Step 1: Write the failing tests**

`tx_test.go`:

```go
package velox_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func touch(tx *velox.DB) error {
	_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
	return err
}

const touchSQL = `UPDATE "invoices" SET "paid" = $1, "updated_at" = $2 WHERE "id" = $3`

func TestTxCommits(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Tx(ctx, touch); err != nil {
		t.Fatal(err)
	}
	if want := []string{"BEGIN", touchSQL, "COMMIT"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%q", e.Log)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	boom := errors.New("boom")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(tx *velox.DB) error {
		if err := touch(tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, e.Log)
	}
}

// TestTxRollsBackOnPanic is half of Review Focus 2.
func TestTxRollsBackOnPanic(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	defer func() {
		if p := recover(); p != "oops" || e.Log[len(e.Log)-1] != "ROLLBACK" {
			t.Errorf("recovered %v, log %q", p, e.Log)
		}
	}()
	_ = db.Tx(ctx, func(*velox.DB) error { panic("oops") })
	t.Error("Tx did not re-raise the panic")
}

// TestTxRollsBackWhenTheContextIsCanceled is the other half of Review Focus 2.
func TestTxRollsBackWhenTheContextIsCanceled(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	c, cancel := context.WithCancel(ctx)
	err := db.Tx(c, func(*velox.DB) error {
		cancel()
		return c.Err()
	})
	if !errors.Is(err, context.Canceled) || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, e.Log)
	}
}

func TestNestedTxUsesSavepoints(t *testing.T) {
	boom := errors.New("boom")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(tx *velox.DB) error {
		if err := tx.Tx(ctx, touch); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(*velox.DB) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("inner error %v", err)
		}
		return nil
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "COMMIT"}
	if err != nil || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

func TestRetrySerializable(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &velox.FakeExec{Errs: []error{serialization}}
	db := velox.OpenFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(tx *velox.DB) error { runs++; return touch(tx) }, velox.RetrySerializable(2))
	if err != nil || runs != 2 {
		t.Fatalf("%v, runs %d", err, runs)
	}
	want := []string{"BEGIN", touchSQL, "ROLLBACK", "BEGIN", touchSQL, "COMMIT"}
	if !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%q", e.Log)
	}
	e = &velox.FakeExec{Errs: []error{serialization}}
	db = velox.OpenFake(velox.Postgres, e)
	if err := db.Tx(ctx, touch); !errors.Is(err, serialization) {
		t.Errorf("without RetrySerializable: %v", err)
	}
	e = &velox.FakeExec{Errs: []error{errors.New("not retryable")}}
	db = velox.OpenFake(velox.Postgres, e)
	runs = 0
	if err := db.Tx(ctx, func(tx *velox.DB) error { runs++; return touch(tx) }, velox.RetrySerializable(3)); err == nil || runs != 1 {
		t.Errorf("a non-retryable error: %v, runs %d", err, runs)
	}
}

func TestTxOptionsAndInTx(t *testing.T) {
	var inTx []bool
	spy := func(_ context.Context, st *velox.Statement) error { inTx = append(inTx, st.InTx); return nil }
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(spy))
	if err := db.Tx(ctx, touch, velox.Isolation(sql.LevelSerializable), velox.ReadOnly()); err != nil {
		t.Fatal(err)
	}
	if err := touch(db); err != nil {
		t.Fatal(err)
	}
	if e.Isolation != sql.LevelSerializable || !e.ReadOnly || !reflect.DeepEqual(inTx, []bool{true, false}) {
		t.Errorf("isolation %v, read-only %v, InTx %v", e.Isolation, e.ReadOnly, inTx)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestTx|TestNestedTx|TestRetry' .`
Expected: FAIL to build: `db.Tx undefined`, `undefined: velox.RetrySerializable`.

- [ ] **Step 3: Write `tx.go`**

```go
package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// TxOption configures a transaction.
type TxOption func(*txOptions)

// Isolation sets the transaction's isolation level; the default is the
// database's.
func Isolation(level sql.IsolationLevel) TxOption {
	return func(o *txOptions) { o.isolation = level }
}

// ReadOnly starts a read-only transaction.
func ReadOnly() TxOption { return func(o *txOptions) { o.readOnly = true } }

// RetrySerializable runs the transaction again, up to n more times, when it
// fails on a serialization failure or a deadlock: PostgreSQL 40001 and 40P01,
// MySQL 1213 and 1205, SQLite BUSY and LOCKED. fn must be safe to run again.
func RetrySerializable(n int) TxOption { return func(o *txOptions) { o.retries = n } }

// Tx runs fn in a transaction. fn's error rolls it back and is returned; nil
// commits it; a panic rolls it back and is raised again. tx is a *DB like any
// other, so a function that takes a *DB works inside a transaction unchanged.
//
// Inside a transaction, Tx makes a savepoint instead, so an error in a nested
// fn rolls back only that fn's work. Options apply to the outermost Tx only.
func (db *DB) Tx(ctx context.Context, fn func(tx *DB) error, opts ...TxOption) error {
	if db.inTx {
		return db.savepoint(ctx, fn)
	}
	var o txOptions
	for _, opt := range opts {
		opt(&o)
	}
	for attempt := 0; ; attempt++ {
		err := db.transaction(ctx, fn, o)
		if err == nil || attempt >= o.retries || !retryable(err) {
			return err
		}
	}
}

// transaction runs fn once in a new transaction. Rollback uses a context
// without ctx's cancellation, so a canceled ctx still rolls back.
func (db *DB) transaction(ctx context.Context, fn func(*DB) error, o txOptions) error {
	ex, ctl, err := db.ex.begin(ctx, o)
	if err != nil {
		return fmt.Errorf("velox: begin: %w", err)
	}
	end := context.WithoutCancel(ctx)
	defer func() {
		if p := recover(); p != nil {
			_ = ctl.rollback(end)
			panic(p)
		}
	}()
	if err := fn(&DB{d: db.d, ex: ex, cfg: db.cfg, inTx: true}); err != nil {
		if rerr := ctl.rollback(end); rerr != nil {
			return errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		return err
	}
	if err := ctl.commit(ctx); err != nil {
		return fmt.Errorf("velox: commit: %w", err)
	}
	return nil
}

// savepoint runs fn under a savepoint of the current transaction.
func (db *DB) savepoint(ctx context.Context, fn func(*DB) error) error {
	name := "velox_" + strconv.Itoa(db.depth+1)
	if _, err := db.ex.exec(ctx, "SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("velox: savepoint: %w", err)
	}
	end := context.WithoutCancel(ctx)
	defer func() {
		if p := recover(); p != nil {
			_, _ = db.ex.exec(end, "ROLLBACK TO SAVEPOINT "+name, nil)
			panic(p)
		}
	}()
	if err := fn(&DB{d: db.d, ex: db.ex, cfg: db.cfg, inTx: true, depth: db.depth + 1}); err != nil {
		if _, rerr := db.ex.exec(end, "ROLLBACK TO SAVEPOINT "+name, nil); rerr != nil {
			return errors.Join(err, fmt.Errorf("velox: rollback to savepoint: %w", rerr))
		}
		return err
	}
	if _, err := db.ex.exec(ctx, "RELEASE SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("velox: release savepoint: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add tx.go tx_test.go
git commit -m "Add transactions with savepoints and retry on serialization failures" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 6: Inserts

**Files:**
- Create: `insert.go`
- Modify: `export_test.go` (add `SetMaxParams`)
- Test: `insert_test.go` (package `velox_test`)

**Interfaces:**
- Consumes:
  - `execWrite`, `run`/`plain` with `res` (Task 2);
  - `fieldValue`, `setField` (Task 4);
  - `DB.Tx` (Task 5);
  - `ColumnInfo.DefaultFunc` (Task 4) and `ConflictClause` (Task 1).
- Produces:
  - `type InsertQuery[E any]`, made by `func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E]` and `func (db *DB) InsertAll[E any, PE Entity[E]](xs []E) InsertQuery[E]`
  - its methods `OnConflict`, `Update`, `DoNothing` and `Exec(ctx) error`
  - `var maxParams map[Dialect]int`
  - test-only `func SetMaxParams(d Dialect, n int) (restore func())`

- [ ] **Step 1: Write the failing tests**

In `export_test.go`, add:

```go
// SetMaxParams lowers a dialect's parameter limit for a test.
func SetMaxParams(d Dialect, n int) (restore func()) {
	old := maxParams[d]
	maxParams[d] = n
	return func() { maxParams[d] = old }
}
```

`insert_test.go`:

```go
package velox_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

const insertCols = `"created_at", "updated_at", "org_id", "number", "total", "paid", "paid_at", "note", "data"`

func TestInsertReadsBackTheKey(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(42)}}}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "invoices" (` + insertCols + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING "id"`
	if inv.ID != 42 || e.SQL != want {
		t.Fatalf("ID %d\n got %s\nwant %s", inv.ID, e.SQL, want)
	}
	if inv.CreatedAt.IsZero() || inv.UpdatedAt.IsZero() || e.Args[0] != inv.CreatedAt {
		t.Errorf("defaults not filled: %v %v", inv.CreatedAt, inv.UpdatedAt)
	}
}

func TestInsertKeepsAGivenKeyAndCreatedAt(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 7, OrgID: 1, Number: "N", CreatedAt: t0}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "RETURNING") || !strings.Contains(e.SQL, `"id"`) || inv.ID != 7 || !inv.CreatedAt.Equal(t0) {
		t.Errorf("%s, ID %d, CreatedAt %v", e.SQL, inv.ID, inv.CreatedAt)
	}
	if time.Since(inv.UpdatedAt) > time.Minute {
		t.Error("OnUpdate column not set on insert")
	}
}

func TestInsertMySQLReadsLastInsertID(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, LastID: 9, HasLastID: true}
	db := velox.OpenFake(velox.MySQL, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 9 || strings.Contains(e.SQL, "RETURNING") {
		t.Errorf("%v, ID %d, %s", err, inv.ID, e.SQL)
	}
}

// TestInsertMySQLUpsertReadsNothingBack is Review Focus 1.
func TestInsertMySQLUpsertReadsNothingBack(t *testing.T) {
	for _, affected := range []int64{1, 2} {
		e := &velox.FakeExec{Affected: affected, LastID: 9, HasLastID: true}
		db := velox.OpenFake(velox.MySQL, e)
		inv := testmodel.Invoice{OrgID: 1, Number: "N"}
		if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).DoNothing().Exec(ctx); err != nil || inv.ID != 0 {
			t.Errorf("affected %d: %v, ID %d", affected, err, inv.ID)
		}
	}
}

func TestUpsertSQL(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}}}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3}
	if err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total" RETURNING "id"`; !strings.HasSuffix(e.SQL, want) || inv.ID != 1 {
		t.Errorf("%s, ID %d", e.SQL, inv.ID)
	}
	if err := db.Insert(&inv).Update(I.Total).Exec(ctx); err == nil || !strings.Contains(err.Error(), "an upsert needs OnConflict") {
		t.Errorf("Update without OnConflict: %v", err)
	}
}

func TestInsertAll(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.InsertAll([]testmodel.Invoice(nil)).Exec(ctx); err != nil || e.Calls != 0 {
		t.Errorf("empty: %v, calls %d", err, e.Calls)
	}
	mixed := []testmodel.Invoice{{ID: 1}, {}}
	if err := db.InsertAll(mixed).Exec(ctx); err == nil || !strings.Contains(err.Error(), "every entity's key set, or every key left at zero") {
		t.Errorf("mixed keys: %v", err)
	}
	two := []testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}}
	if err := db.InsertAll(two).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, "($10, $11, $12, $13, $14, $15, $16, $17, $18)") || len(e.Args) != 18 || two[0].CreatedAt.IsZero() {
		t.Errorf("%s (%d args)", e.SQL, len(e.Args))
	}
}

// TestInsertAllBatchesInOneTransaction is Review Focus 3.
func TestInsertAllBatchesInOneTransaction(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 18)() // two rows of nine columns per statement
	rows := []testmodel.Invoice{{Number: "A"}, {Number: "B"}, {Number: "C"}}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.InsertAll(rows).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 4 || e.Log[0] != "BEGIN" || e.Log[3] != "COMMIT" || strings.Count(e.Log[1], "(") != 3 || strings.Count(e.Log[2], "(") != 2 {
		t.Errorf("%q", e.Log)
	}
	secondBatch := errors.New("second batch failed")
	failing := &velox.FakeExec{Errs: []error{nil, secondBatch}} // BEGIN consumes nothing; the first INSERT succeeds
	db = velox.OpenFake(velox.Postgres, failing)
	if err := db.InsertAll(rows).Exec(ctx); !errors.Is(err, secondBatch) || failing.Log[len(failing.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, failing.Log)
	}
}
```

`strings.Count(e.Log[1], "(")` is 3: one parenthesis for the column list and one per row. The second statement has one row, so 2.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestInsert|TestUpsert' .`
Expected: FAIL to build: `db.Insert undefined`, `undefined: velox.SetMaxParams`.

- [ ] **Step 3: Write `insert.go`**

```go
package velox

import (
	"context"
	"fmt"
	"reflect"
)

// maxParams is each dialect's limit on the parameters of one statement.
var maxParams = map[Dialect]int{Postgres: 65535, MySQL: 65535, SQLite: 32766}

// InsertQuery is an INSERT of one entity or of many, built by chaining and
// run by Exec. Like Query it is a value: extending it leaves it as it was.
type InsertQuery[E any] struct {
	db       *DB
	meta     *Meta[E]
	one      *E
	many     []E
	conflict *ConflictClause
}

// Insert starts an INSERT of the entity x points to:
//
//	db.Insert(&inv).Exec(ctx)
func (db *DB) Insert[E any, PE Entity[E]](x PE) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: x.VeloxMeta(), one: (*E)(x)}
}

// InsertAll starts an INSERT of every entity in xs, in as few statements as
// the dialect's parameter limit allows.
func (db *DB) InsertAll[E any, PE Entity[E]](xs []E) InsertQuery[E] {
	return InsertQuery[E]{db: db, meta: PE(nil).VeloxMeta(), many: xs}
}

func columnIndexes[E any](cs []Column[E]) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = int(c.column(nil))
	}
	return out
}

// OnConflict names the columns of the unique key whose conflict the insert
// resolves; follow it with Update or DoNothing (without either it does
// nothing). On MySQL, ON DUPLICATE KEY UPDATE resolves a conflict on any
// unique key.
func (q InsertQuery[E]) OnConflict[_ Lazy[E]](cols ...Column[E]) InsertQuery[E] {
	q.conflict = &ConflictClause{Columns: columnIndexes(cols)}
	return q
}

// Update overwrites the named columns of the existing row with the inserted
// values when the insert conflicts.
func (q InsertQuery[E]) Update[_ Lazy[E]](cols ...Column[E]) InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = columnIndexes(cols), false
	q.conflict = &cf
	return q
}

// DoNothing keeps the existing row when the insert conflicts.
func (q InsertQuery[E]) DoNothing[_ Lazy[E]]() InsertQuery[E] {
	var cf ConflictClause
	if q.conflict != nil {
		cf = *q.conflict
	}
	cf.Update, cf.DoNothing = nil, true
	q.conflict = &cf
	return q
}

// Exec runs the insert.
//
// Exec first fills, in each entity, every zero field whose column has Default
// or DefaultFunc, and every column with OnUpdate. An integer primary key left
// at zero is not sent: the database assigns it. Insert reads that key back
// into *x: with RETURNING on PostgreSQL and SQLite, from LastInsertId on MySQL
// for an insert without OnConflict. After a conflict that kept the existing
// row, the key stays zero.
//
// InsertAll reads nothing back. Give every entity its key, or leave every key
// at zero for the database to assign. Outside a transaction, an InsertAll that
// needs several statements runs them in one transaction.
func (q InsertQuery[E]) Exec[_ Lazy[E]](ctx context.Context) error {
	if q.meta.err != nil {
		return q.meta.err
	}
	if q.conflict != nil && len(q.conflict.Columns) == 0 {
		return fmt.Errorf("velox: %s: an upsert needs OnConflict with the columns of a unique key", q.meta.info.Name)
	}
	if q.one != nil {
		return insertOne(ctx, q.db, q.meta, q.one, q.conflict)
	}
	return insertMany(ctx, q.db, q.meta, q.many, q.conflict)
}

// fillDefaults sets, in *p, every zero field whose column has Default or
// DefaultFunc, and every column with OnUpdate.
func fillDefaults[E any](m *Meta[E], p *E) {
	for i := range m.info.Columns {
		c := &m.info.Columns[i]
		v := reflect.ValueOf(m.addr(p, i)).Elem()
		switch {
		case c.OnUpdate != nil:
			v.Set(reflect.ValueOf(c.OnUpdate()))
		case c.HasDefault && v.IsZero():
			v.Set(reflect.ValueOf(c.Default))
		case c.DefaultFunc != nil && v.IsZero():
			v.Set(reflect.ValueOf(c.DefaultFunc()))
		}
	}
}

// generatedKey is the index of a single integer primary key column holding
// zero in *p, which the database assigns; -1 when there is none.
func generatedKey[E any](m *Meta[E], p *E) int {
	if len(m.info.PrimaryKey) != 1 {
		return -1
	}
	k := m.info.PrimaryKey[0]
	v := reflect.ValueOf(m.addr(p, k)).Elem()
	if (v.CanInt() || v.CanUint()) && v.IsZero() {
		return k
	}
	return -1
}

// insertColumns lists the columns an INSERT writes: every column but skip.
func insertColumns(in *EntityInfo, skip int) []int {
	cols := make([]int, 0, len(in.Columns))
	for i := range in.Columns {
		if i != skip {
			cols = append(cols, i)
		}
	}
	return cols
}

func rowValues[E any](m *Meta[E], p *E, cols []int) []any {
	vals := make([]any, len(cols))
	for j, c := range cols {
		vals[j] = fieldValue(m, p, c)
	}
	return vals
}

func insertOne[E any](ctx context.Context, db *DB, m *Meta[E], p *E, cf *ConflictClause) error {
	fillDefaults(m, p)
	gen := generatedKey(m, p)
	cols := insertColumns(m.info, gen)
	st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Rows: [][]any{rowValues(m, p, cols)}, Conflict: cf, InTx: db.inTx}
	if gen < 0 {
		_, err := execWrite(ctx, db, &st)
		return err
	}
	if db.d != MySQL {
		st.Returning = []int{gen}
		read := func(r rows) error {
			if r.Next() {
				return r.Scan(m.addr(p, gen))
			}
			return nil
		}
		if db.hooked() {
			return db.run(ctx, &st, read, nil)
		}
		return db.plain(ctx, &st, read, nil)
	}
	res, err := execWrite(ctx, db, &st)
	if err != nil {
		return err
	}
	if cf == nil && res.hasLastID && res.affected == 1 {
		v := reflect.ValueOf(m.addr(p, gen)).Elem()
		if v.CanInt() {
			v.SetInt(res.lastID)
		} else {
			v.SetUint(uint64(res.lastID))
		}
	}
	return nil
}

func insertMany[E any](ctx context.Context, db *DB, m *Meta[E], xs []E, cf *ConflictClause) error {
	if len(xs) == 0 {
		return nil
	}
	for i := range xs {
		fillDefaults(m, &xs[i])
	}
	gen := generatedKey(m, &xs[0])
	for i := range xs {
		if (generatedKey(m, &xs[i]) >= 0) != (gen >= 0) {
			return fmt.Errorf("velox: %s: InsertAll needs every entity's key set, or every key left at zero", m.info.Name)
		}
	}
	cols := insertColumns(m.info, gen)
	per := max(1, maxParams[db.d]/len(cols))
	batches := func(db *DB) error {
		for start := 0; start < len(xs); start += per {
			end := min(start+per, len(xs))
			st := Statement{Op: OpInsert, Entity: m.info, Columns: cols, Conflict: cf, InTx: db.inTx, Rows: make([][]any, 0, end-start)}
			for i := start; i < end; i++ {
				st.Rows = append(st.Rows, rowValues(m, &xs[i], cols))
			}
			if _, err := execWrite(ctx, db, &st); err != nil {
				return err
			}
		}
		return nil
	}
	if len(xs) <= per || db.inTx {
		return batches(db)
	}
	return db.Tx(ctx, batches)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`, including `TestOneRowAllocationBudget` and `TestMethodsOfGenericTypesAreGeneric`.

- [ ] **Step 5: Commit**

```bash
git add insert.go insert_test.go export_test.go
git commit -m "Add Insert and InsertAll with defaults, generated keys, upserts and batches" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 7: Writes on real databases, CI, spec and records

**Files:**
- Create: `internal/itest/writes_test.go`
- Modify: `internal/itest/itest_test.go` (DDL), `scripts/ci-local.sh` and `.github/workflows/ci.yml` (MySQL DSN), `db.go` (`Open`'s documentation), `docs/specs/2026-10-14-core-design.md`, `docs/plans/2026-10-14-core-1-followups.md`

**Interfaces:**
- Consumes: everything above; the itest harness (`each`, `target`, `query`, `wantIDs`, `t0`, `I`, `ctx`).
- Produces: write tests on SQLite, postgres-pgx, postgres-sql and mysql.

- [ ] **Step 1: Give the test tables generated keys and a named unique key**

In `internal/itest/itest_test.go`:
- In `pgDDL`, change `id bigint PRIMARY KEY` to `id bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 100) PRIMARY KEY`. PostgreSQL's identity does not advance past explicitly inserted ids, so it starts above the fixture's 1–5.
- In `mysqlDDL`, change `id BIGINT PRIMARY KEY` to `id BIGINT AUTO_INCREMENT PRIMARY KEY`.
- SQLite's `id INTEGER PRIMARY KEY` already generates keys.
- In all three DDLs, add `, CONSTRAINT invoices_org_number_uq UNIQUE (org_id, number)` before the closing parenthesis.

- [ ] **Step 2: Turn on clientFoundRows for MySQL**

In `scripts/ci-local.sh` and `.github/workflows/ci.yml`, change the MySQL DSN to `velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true`.

In `db.go`, add to `Open`'s documentation:

```go
//
// For MySQL, include parseTime=true and clientFoundRows=true in the DSN: the
// first scans DATETIME columns into time.Time, and the second makes a row an
// update matched count as affected even when its values did not change, which
// Save relies on. For SQLite, foreign keys are enforced only when the DSN asks
// for it (modernc.org/sqlite: _pragma=foreign_keys(1)).
```

- [ ] **Step 3: Write the integration tests**

`internal/itest/writes_test.go`:

```go
package itest

import (
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func newInvoice(org int64, number string) testmodel.Invoice {
	return testmodel.Invoice{OrgID: org, Number: number, Total: 1, Data: []byte("d")}
}

func TestInsertAndReadBack(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		inv := newInvoice(3, "NEW")
		if err := tg.db.Insert(&inv).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if inv.ID <= 5 || inv.CreatedAt.IsZero() {
			t.Fatalf("ID %d, CreatedAt %v", inv.ID, inv.CreatedAt)
		}
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(inv.ID)).One(ctx)
		if err != nil || got.Number != "NEW" || got.CreatedAt.Sub(inv.CreatedAt).Abs() > time.Millisecond {
			t.Errorf("%+v, %v", got, err)
		}
		given := newInvoice(3, "GIVEN")
		given.ID = 50
		if err := tg.db.Insert(&given).Exec(ctx); err != nil || given.ID != 50 {
			t.Errorf("%v, ID %d", err, given.ID)
		}
	})
}

func TestUniqueViolation(t *testing.T) {
	want := map[velox.Dialect]string{
		velox.Postgres: "invoices_org_number_uq",
		velox.MySQL:    "invoices_org_number_uq",
		velox.SQLite:   "invoices.org_id, invoices.number",
	}
	each(t, func(t *testing.T, tg target) {
		dup := newInvoice(1, "INV-1")
		err := tg.db.Insert(&dup).Exec(ctx)
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) || ce.Kind != velox.Unique || ce.Constraint != want[tg.d] || ce.Entity != "testmodel.Invoice" {
			t.Errorf("%v (%#v)", err, ce)
		}
	})
}

func TestUpsert(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		keep := newInvoice(1, "INV-1")
		keep.Total = 77
		if err := tg.db.Insert(&keep).OnConflict(I.OrgID, I.Number).DoNothing().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		over := newInvoice(1, "INV-1")
		over.Total = 99
		if err := tg.db.Insert(&over).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.Number.EQ("INV-1")).One(ctx)
		if err != nil || got.ID != 1 || got.Total != 99 {
			t.Errorf("%+v, %v", got, err)
		}
		if tg.d != velox.MySQL && over.ID != 1 {
			t.Errorf("RETURNING after DO UPDATE: ID %d, want 1", over.ID)
		}
		if n, _ := tg.db.Query[testmodel.Invoice]().Count(ctx); n != 5 {
			t.Errorf("rows %d, want 5: an upsert inserted", n)
		}
	})
}

func TestInsertAll(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		rows := []testmodel.Invoice{newInvoice(9, "A"), newInvoice(9, "B"), newInvoice(9, "C")}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(9)).Count(ctx); err != nil || n != 3 {
			t.Errorf("%d, %v", n, err)
		}
	})
}

func TestUpdateDeleteSave(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		n, err := tg.db.Update[testmodel.Invoice]().Set(I.Total.Add(1)).Where(I.OrgID.EQ(1)).Exec(ctx)
		if err != nil || n != 4 {
			t.Fatalf("update: %d, %v", n, err)
		}
		one, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil || one.Total != 21 || !one.UpdatedAt.After(t0) {
			t.Fatalf("%+v, %v", one, err)
		}
		one.Number = "CHANGED"
		if err := tg.db.Save(ctx, one, I.Number); err != nil {
			t.Fatal(err)
		}
		again, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil || again.Number != "CHANGED" {
			t.Errorf("%+v, %v", again, err)
		}
		if err := tg.db.Save(ctx, one, I.Number); err != nil {
			t.Errorf("saving unchanged values must still find the row (clientFoundRows on MySQL): %v", err)
		}
		missing := newInvoice(1, "X")
		missing.ID = 999
		if err := tg.db.Save(ctx, &missing, I.Number); !errors.Is(err, velox.ErrNotFound) {
			t.Errorf("missing row: %v", err)
		}
		n, err = tg.db.Delete[testmodel.Invoice]().Where(I.ID.EQ(5)).Exec(ctx)
		if err != nil || n != 1 {
			t.Errorf("delete: %d, %v", n, err)
		}
	})
}

func TestTransactions(t *testing.T) {
	boom := errors.New("boom")
	each(t, func(t *testing.T, tg target) {
		err := tg.db.Tx(ctx, func(tx *velox.DB) error {
			a := newInvoice(7, "A")
			if err := tx.Insert(&a).Exec(ctx); err != nil {
				return err
			}
			if err := tx.Tx(ctx, func(inner *velox.DB) error {
				b := newInvoice(7, "B")
				if err := inner.Insert(&b).Exec(ctx); err != nil {
					return err
				}
				return boom
			}); !errors.Is(err, boom) {
				t.Errorf("savepoint: %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, numbers(t, tg, 7), []string{"A"})
		err = tg.db.Tx(ctx, func(tx *velox.DB) error {
			c := newInvoice(8, "C")
			if err := tx.Insert(&c).Exec(ctx); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatal(err)
		}
		if n, _ := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(8)).Count(ctx); n != 0 {
			t.Errorf("a rolled-back insert is visible: %d rows", n)
		}
	})
}

func numbers(t *testing.T, tg target, org int64) []string {
	t.Helper()
	got, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(org)).OrderBy(I.Number.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(got))
	for i := range got {
		out[i] = got[i].Number
	}
	return out
}
```

Add beside `wantIDs` in `itest_test.go`:

```go
func wantStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 4: Run the full mirror**

Run: `scripts/ci-local.sh 2>&1 | tail -25; docker ps --format '{{.Names}}' | grep velox2-ci || echo "no velox2-ci containers"`
Expected: everything passes, with every write test passing on `/sqlite`, `/postgres-pgx`, `/postgres-sql` and `/mysql`. `velox-gen -check` and `verify` pass, and no containers remain.

If a constraint name differs from the expectation on a dialect, print the driver's error. Adjust the expectation only when the driver reports the name differently from the probe recorded in this plan: PostgreSQL `invoices_org_number_uq`, MySQL key `invoices.invoices_org_number_uq`, SQLite `invoices.org_id, invoices.number`. Record the evidence.

- [ ] **Step 5: Update the spec and the follow-ups record**

In `docs/specs/2026-10-14-core-design.md`:
- **§4.2.** Replace the code block with:

```go
err := db.Insert(&inv).Exec(ctx)                 // type parameters inferred from &inv
err = db.InsertAll(lines).Exec(ctx)              // multi-row INSERT, batched per dialect limits
err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx)
```

and add after it: "Inserts are builders because Go infers a generic function's type parameters from its arguments but not from an interface a column handle implements: `velox.OnConflict(I.OrgID)` could not infer the entity, `db.Insert(&inv).OnConflict(I.OrgID)` can." Change "LastInsertId on MySQL (single auto-increment key; other keys must be set by the application on MySQL)" to "LastInsertId on MySQL for an insert without OnConflict (MySQL's insert id does not name the row after ON DUPLICATE KEY UPDATE)". Change "Upsert renders `ON CONFLICT … DO UPDATE` or `ON DUPLICATE KEY UPDATE`" to "Upsert renders `ON CONFLICT … DO UPDATE` / `DO NOTHING`, or on MySQL `AS new ON DUPLICATE KEY UPDATE` (8.0.19 and later)".
- **§4.4.** After the `Save` line in the code block, add "On MySQL, `Save` needs `clientFoundRows=true` in the DSN."
- **§5.** In the `Statement` block, replace `Values    []Assignment // inserts and updates` with:

```go
    Set       []Assignment    // UPDATE
    Rows      [][]any         // INSERT: one value per Columns entry
    Conflict  *ConflictClause // INSERT: upsert
    Returning []int           // INSERT: read back (PostgreSQL, SQLite)
    AllRows   bool            // UPDATE, DELETE: said All
```

- **§6.** In the table, change MySQL's column header to `MySQL 8.0.19+`. Under the table, add: "MySQL DSNs need `parseTime=true` and, for `Save`, `clientFoundRows=true`. SQLite enforces foreign keys only when the DSN asks for it."
- **§7.** Replace the `*ConstraintError` sentence with: "`*ConstraintError{Entity, Kind, Constraint, Err}` with `Kind` one of `Unique`, `ForeignKey`, `NotNull`, `Check`; `Constraint` is the constraint's name, except that SQLite reports the columns and a NOT NULL error the column."

In `docs/plans/2026-10-14-core-1-followups.md`, "Before plan 3 (writes)":
- Delete the bullets about a hook changing `Statement.Op` and about schema-qualified table names (resolved by Tasks 1–2).
- Change the golden-SQL bullet to: "Golden SQL for MySQL and SQLite on the read-path render cases that still assert PostgreSQL only (writes cover all three since plan 3), an OR-between-siblings case, and the NEQ/GTE/LT/LTE operators."
- Rename the section to "Still open from before plan 3".

- [ ] **Step 6: Commit**

```bash
git add internal/itest scripts/ci-local.sh .github/workflows/ci.yml db.go docs
git commit -m "Test writes and transactions on every database; spec and records" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

## Spec coverage of this plan

| Spec | Task |
|---|---|
| §4.2: Insert, InsertAll, upsert, read back keys and defaults | 6 (unit), 7 (real databases) |
| §4.4: Update with Set/Add/Sub/SetNull, Save, no write-back of a whole struct | 4, 7 |
| §4.5: Delete | 4, 7 |
| §4.6: Tx, rollback on error and panic, savepoints, RetrySerializable | 5, 7 |
| §5: one Statement for writes; hooks see and may change it | 1, 2 |
| §6: placeholders, RETURNING vs LastInsertId, upsert syntax, retry codes | 1, 3, 6 |
| §7: `*ConstraintError` on all three dialects; `velox:` prefix | 3, 7 |
| Guardrails `ErrNoWhere` (`All()` exists; the guardrails come in sub-project 2) | 4 (`AllRows`) |
