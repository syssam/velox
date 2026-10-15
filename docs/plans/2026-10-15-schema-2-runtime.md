# Schema DSL, Plan 2: the runtime reads generated metadata — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the runtime the metadata path that velox-gen will write (plan 3): an entity described by a literal `Desc[E]`, not discovered by reflecting over its struct. This plan adds:
- typed `Hooks()` and `Policy()` methods in ent's middleware shape;
- validators, immutable columns and update defaults;
- many-to-many relations through join entities.

The internal test model moves to this path. The old `Schema(*velox.Schema)` path keeps working beside it until plan 3 removes it.

**Architecture:**
- **`MetaOnce.Load(desc)`** builds an entity's `EntityInfo` from a `Desc[E]`.
  - The literal names every column, key, index, the tenant column and every relation.
  - It also holds `Addr` and `RelAddr` functions that return a field's address.
  - Reflection is used only to read the Go type of the pointer `Addr` returns, and the entity's type name.
- **Hooks are stored erased on `EntityInfo`.** Each write runs them as a chain around a core: validators, defaults, tenant, the policy's `Insert`, then the pipeline.
- **Many-to-many relations.** A many-to-many relation is a link through a join entity, a generated struct like any other. Loading it reads the join rows, then the targets. `Has` and `HasWhere` render two nested EXISTS.
- **The old path stays until plan 3.** It is `Schema`, `Mixer`, `BeforeInsert`/`BeforeUpdate`/`BeforeDelete`, `Shape` and `MetaOnce.Get`. That keeps every task, the examples and bench/scale green.

**Tech Stack:** Go 1.27 (generic methods), standard library; the in-repo `FakeExec`; SQLite (`modernc.org/sqlite`), pgx v5 and MySQL in `internal/itest`.

**Spec:** `docs/specs/2026-10-15-schema-design.md`:
- §2, the metadata, which is what this plan's `Desc[E]` is;
- §3, behaviour beside the generated code;
- §1.1, field options: `Immutable`, `UpdateDefault`, `Sensitive` and validators;
- §1.2, many-to-many.

It builds on:
- Core: `docs/specs/2026-10-14-core-design.md` §1, §3, §5, §8;
- the policy design: `docs/specs/2026-10-14-policy-design.md` §2–§3.

Plan 1 (`docs/plans/2026-10-15-schema-1-dsl.md`, merged) produced the `Graph` that plan 3 turns into `Desc[E]` literals.

## Global Constraints

- **Go 1.27.** Every method of a generic velox type is generic (`[_ Lazy[E]]` or its own type parameter); `methods_test.go` enforces it.
- **Nothing runs at program start.** No package-level variable is initialized by a function call, and there is no `init`.
- **Core §3 rule 7.** No velox type reachable from generated code holds a `reflect.Type`. Generic code calls non-generic helpers for anything that uses `reflect`, as `buildEntity`, `elem` and `elemType` do today.
- **Core's budgets.** `budget_test.go` passes unchanged:
  - an entity without hooks pays nothing for them;
  - no allocation is added to a write of such an entity.
- **The escape check.** `DB.plain`'s `st` and `read` must not escape:

  ```bash
  go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"
  ```

  It may print `leaking param content: st`, never `leaking param: st`, and must print `read does not escape`.
- **Errors.**
  - Every error starts `velox: <Entity>: `.
  - Sentinels are bare lower-case phrases, wrapped with `%w`.
  - This plan adds one sentinel, `ErrImmutable = errors.New("column is immutable")`, and one error type, `*ValidationError`.
- **The old path keeps working.** The runtime packages and everything that reaches them must still build and pass, unchanged except where a task says otherwise: `example/`, `bench/scale`, `cmd/velox-gen` and the old generator's golden test.
- **Clean.**
  - `gofmt`, `go vet` and golangci-lint v2.14.0 are clean in the root module, `cmd/velox-gen`, `example` and `contrib/otel`.
  - `go test -race ./...` passes in all four.
  - `internal/itest` passes against PostgreSQL (pgx and database/sql) and MySQL under `scripts/ci-local.sh`.
- **No machine-specific paths** in committed files. Use `GOCACHE=/private/tmp/claude-501/velox2-gocache` only in your shell.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Rulings this plan makes on the spec

1. **Coexistence.** Plan 2 adds the `Desc` path beside `Schema(*velox.Schema)`. Plan 3 deletes the old path once velox-gen writes `Desc` literals and every entity has moved. Why: the old path is what the examples, bench/scale and the old generator use, and each plan must leave the repository green.
2. **`velox.On(h, ops ...Op)` takes the existing `Op` constants,** `OpInsert`, `OpUpdate` and `OpDelete`, in place of the spec's `OpCreate|OpUpdate|OpDelete` bit set. `Op` is an enumeration that `Statement.Op` already uses, so it cannot also be a bit set. If wrong: the spec's sample changes from `|` to commas.
3. **`Mutation.Result()` carries `Affected`.**
   - A generated key is written back into the row, as today: after `next`, `m.Rows()[0].ID` holds it.
   - `InsertAll` reads no keys back (Core rough spot, in the followups).
   - If wrong: one field to add.
4. **Error wrapping.**
   - An error velox returns from inside `next` passes back through the hooks unchanged, when a hook returns it as is.
   - An error a hook makes itself is wrapped `velox: <Entity>: hook: <err>`, with `errors.Is` and `errors.As` still reaching it.
   - This replaces `before insert:`, `before update:` and `before delete:` for the new path.
5. **The order of a write** (spec §3).
   - Hooks before `next`.
   - Then, inside `next`, for an insert:
     1. defaults;
     2. the tenant (fill, then check);
     3. the old path's `BeforeInsert`, when the entity is on it;
     4. validators;
     5. the policy's `Insert`;
     6. the pipeline.
   - For an update or delete, inside `next`:
     1. `UpdateDefault` (update only);
     2. the immutable check;
     3. validators;
     4. the pipeline, whose guard runs first.
   - Hooks therefore see neither the defaults nor the `UpdateDefault` assignments, while validators see what hooks set. `Save` still writes back every assignment sent, these included.
6. **`InsertAll`'s hooks run once per call, over all its rows.** When the rows need several statements, `next` sends all of them in one transaction, as today.
7. **What validators check.**
   - For an insert: every column the statement sends.
   - For an update or `Save`: every `AssignSet` value, but not `Add` or `Sub`.
   - A nil pointer is not checked, because it is the absence of a value.
   - A failure is a `*ValidationError` and nothing is sent.
8. **Immutable columns.** An immutable column is refused in an update's or `Save`'s assignments (those a hook adds included) and in an upsert's `Update` list. The error wraps `ErrImmutable`. An insert may set it. If wrong: the refusal moves.
9. **`Policy()` and `Hooks()` are called once, on a nil `*E`,** when the metadata is built. A method that reads its receiver panics, and the panic becomes the entity's `*SchemaError` (as a panicking `Schema` method does today). The doc comments say the receiver is always nil.
10. **Many-to-many through a join entity.**
    - Each join table is an entity of its own (plan 3 generates `UserGroup{UserID, GroupID}` with a two-column primary key). Its rows are written with `Insert` and `Delete`.
    - A many-to-many relation is `EdgeThrough[J, T]`. Loading reads J's rows for the owners' keys, then T's rows for the keys found, so T gets its tenant and Read policy and J gets none.
    - If wrong: a dedicated link API would replace `Insert` of join rows.
11. **The entity's name stays `reflect.TypeOf(x).String()`** ("testmodel.Invoice"). Reading a type's name is not discovering its columns (success criterion 2), and every error message keeps its text.
12. **`Sensitive` reaches `ColumnInfo.Sensitive` only.**
    - velox logs no arguments, and `contrib/otel` records none, so nothing else needs redacting today.
    - `String()` and the JSON tag belong to the generated code (plan 3).
    - `contrib/otel` gains no change.
13. **`ColumnInfo.OnUpdate` is renamed `UpdateDefault`,** the DSL's name. The old path's `ColumnDef.OnUpdate` method keeps its name until plan 3 removes it.

## Review Focus

1. **A hook that returns `next`'s error unchanged** must not double the prefix (`velox: T: hook: velox: T: …`). Pinned in Task 3: `TestHookPassesVeloxErrorsThrough`.
2. **A hook that sets an immutable column, or a value its validator refuses,** must be refused after the hook runs, not let through because validation ran first. Pinned in Task 3: `TestHookSetIsValidated`.
3. **An entity without hooks** must cost what it costs today. Pinned by `budget_test.go`, unchanged, which Task 5 runs on the moved test model.
4. **A many-to-many `With` where two owners share a target, or one owner has none,** must give each owner exactly its targets, in the requested order, and an empty loaded `Many` for the owner with none. Pinned in Task 4: `TestThroughLoadSharedAndEmpty`.
5. **A many-to-many `HasWhere` on a tenant-scoped target** must put the tenant condition inside the inner EXISTS, not the outer. Pinned in Task 4: `TestThroughHasScopesTarget`.

---

## File Structure

| File | Responsibility |
|---|---|
| `desc.go` (new) | `Desc`, `ColumnDesc`, `MetaOnce.Load`, `buildDesc`, `EdgeTo`, `EdgeThrough` |
| `hook.go` (new) | `Hook`, `Mutator`, `MutateFunc`, `On`, the erased chain, `Result` |
| `validate.go` (new) | `ValidationError`, the validator and immutable checks |
| `meta.go` | `EntityInfo` gains `check` and `hooks`; `ColumnInfo` gains `Immutable` and `Sensitive`; `OnUpdate` → `UpdateDefault` |
| `relation.go`, `load.go`, `render.go`, `scope.go`, `render_cache.go` | many-to-many links |
| `scope_api.go` | `Mutation[E]` gains `Rows` and `Result`; doc comments |
| `insert.go`, `write.go`, `db.go` | the chain around every write; validators and immutables inside `next` |
| `errors.go` | `ErrImmutable`; `hookError` used for stage `hook` |
| `desc_model_test.go` (new, package `velox_test`) | `dAuthor`, `dBook`, `dTag`, `dBookTag`: hand-written `Desc` entities |
| `desc_test.go`, `hook_test.go`, `validate_test.go`, `through_test.go` (new) | the tests of Tasks 1–4 |
| `internal/testmodel/*` | moved to `Desc` (Task 5) |
| `internal/itest/through_test.go` (new) | many-to-many on real databases |
| `cmd/velox-gen/testdata/testmodel/` (new) | the old generator's golden fixture, a copy of today's `internal/testmodel` |

---

### Task 1: The `Desc` path

**Files:**
- Create: `desc.go`, `desc_model_test.go`, `desc_test.go`
- Modify: `meta.go`, `relation.go`, `insert.go`, `write.go` (rename `OnUpdate` → `UpdateDefault`), `schema.go` (`ColumnDef.OnUpdate` sets `UpdateDefault`), and every test that reads `ColumnInfo.OnUpdate`

**Interfaces:**
- Produces (used by every later task and by plan 3's generator):

```go
// Desc is an entity's metadata as velox-gen writes it: literals, read on the
// entity's first use. Its column order is the scan order.
type Desc[E any] struct {
	Table      string
	Schema     string        // the schema (PostgreSQL) or database holding Table; "" for the default
	Columns    []ColumnDesc
	PrimaryKey []int         // indexes into Columns
	Tenant     int           // index into Columns; -1 when the entity has none
	Indexes    []IndexInfo
	Relations  []Relation    // in the order of the generated relation handles
	Addr       func(x *E, i int) any // &x.<field of column i>
	RelAddr    func(x *E, i int) any // &x.<field of relation i>: a *One[T] or *Many[T]
	Check      func(col int, v any) error // the validators of column col, given its value (never a nil pointer); nil when none
}

// ColumnDesc describes one column of a Desc.
type ColumnDesc struct {
	Field         string // the Go field name
	Name          string // the column name
	SQLType       string // empty: the dialect's default
	Default       any    // a value of the field's type (or of its element type, for a pointer field); nil when none
	DefaultFunc   func() any
	UpdateDefault func() any
	Immutable     bool
	Sensitive     bool
}

// Load returns the entity's Meta, building it from desc the first time. Like
// Get, a panic in desc, Policy or Hooks becomes the Meta's *SchemaError.
func (o *MetaOnce[E]) Load[_ Lazy[E]](desc func() Desc[E]) *Meta[E]

// EdgeTo is a relation to entity T keyed owner.Columns[ownerCol] =
// T.Columns[targetCol].
func EdgeTo[T any, PT Entity[T]](name string, ownerCol, targetCol int) Relation

// EdgeThrough is a many-to-many relation to T through the join entity J:
// owner.Columns[ownerCol] = J.Columns[joinOwner] and J.Columns[joinTarget] =
// T.Columns[targetCol]. Task 4 implements it.
func EdgeThrough[J, T any, PJ Entity[J], PT Entity[T]](name string, ownerCol, joinOwner, joinTarget, targetCol int) Relation
```

- `ColumnInfo` gains `Immutable bool` and `Sensitive bool`, and `OnUpdate` becomes `UpdateDefault` (Ruling 13).
- `EntityInfo` gains the unexported `check func(col int, v any) error` (Task 2) and `hooks []hookFn` (Task 3).

**How `MetaOnce.Load` works:**
1. Under `o.once`, call `desc()`.
2. Build `x := new(E)` and pass a non-generic `descView` to `buildDesc(x any, base unsafe.Pointer, v descView) (*EntityInfo, error)`:

   ```go
   type descView struct {
   	d       *descFields // the non-generic fields of Desc: Table, Schema, Columns, PrimaryKey, Tenant, Indexes, Relations, Check
   	addr    func(i int) any // d.Addr(x, i)
   	relAddr func(i int) any // d.RelAddr(x, i)
   }
   ```

3. `buildDesc` recovers panics (`&SchemaError{Entity: name, Reason: "the generated metadata panicked: …"}`). It then builds `ColumnInfo` per `ColumnDesc`:
   - `Type` is `reflect.TypeOf(addr(i)).Elem()`, held as `any`.
   - `Nullable` is that type's kind being a pointer.
   - `off` is `uintptr(unsafe.Pointer(reflect.ValueOf(addr(i)).Pointer())) - uintptr(base)`.
   - `HasDefault` is `Default != nil`.
   - It copies the rest.
4. **It refuses,** as `*SchemaError`:
   - an `Addr` that returns nil or a non-pointer for a column;
   - `PrimaryKey` empty, or naming no column;
   - `Tenant` other than -1 that is out of range;
   - a literal `Default` on a non-pointer bool or numeric field (the rule `ColumnDef.Default` enforces, with its message);
   - everything `validate(info)` refuses.
5. **Relations.** For each relation `i`:
   - `relAddr(i)` must be a `*One[T]` or `*Many[T]`; check with `isRelation` on its element type.
   - It gives `off`, `typ` and `many`, as `buildInfo` sets them.
   - `ref`, `hasFK`, `fkOff` and `fkTyp` stay unset, and the new fields `ownerCol`/`targetCol` come from `EdgeTo`.
6. **After the build:**
   - `Meta{info, addr: d.Addr, err}`;
   - `info.kit = kitOf(info, d.Addr)`;
   - `info.check = d.Check`.
   - Task 3 adds `Policy()` and `Hooks()` here.

**`EdgeTo`** returns a `Relation` whose `target` is `PT(nil).VeloxMeta()`'s info (as `RelationTo` does), with `explicit: true`, `ownerCol` and `targetCol`. `relation.build` uses `link{target, ownerCol, targetCol}` when explicit:
- it checks both indexes are in range;
- it keeps the existing key-type and comparability checks;
- the inference paths (`References`, `ForeignKey`, the field naming rule) remain only for the old path.

**`EdgeThrough`** is declared here with a body that records the relation and makes `build` fail with "many-to-many relations arrive in Task 4". Task 4 replaces that body; declaring it now fixes the signature plan 3 targets.

- [ ] **Step 1: Write the test model**

`desc_model_test.go`, in `package velox_test`, holds hand-written entities in the shape plan 3's generator will write. The shape is:
- the struct;
- a handles variable;
- a `MetaOnce` and a `VeloxMeta` method;
- a function returning `Desc`.

```go
package velox_test

import (
	"errors"
	"time"

	"github.com/syssam/velox2"
)

// dAuthor is tenant-scoped, has an immutable creation time, a validated name
// and a sensitive email.
type dAuthor struct {
	ID        int64
	OrgID     int64
	Name      string
	Email     *string
	CreatedAt time.Time
	Books     velox.Many[dBook]
}

var dAuthors = struct {
	ID        velox.NumberCol[dAuthor, int64]
	OrgID     velox.NumberCol[dAuthor, int64]
	Name      velox.StringCol[dAuthor, string]
	Email     velox.NullStringCol[dAuthor, string]
	CreatedAt velox.OrderedCol[dAuthor, time.Time]
	Books     velox.Rel[dAuthor, dBook]
}{
	velox.NumberCol[dAuthor, int64]{I: 0}, velox.NumberCol[dAuthor, int64]{I: 1},
	velox.StringCol[dAuthor, string]{I: 2}, velox.NullStringCol[dAuthor, string]{I: 3},
	velox.OrderedCol[dAuthor, time.Time]{I: 4}, velox.Rel[dAuthor, dBook]{I: 0},
}

var dAuthorMeta velox.MetaOnce[dAuthor]

func (*dAuthor) VeloxMeta() *velox.Meta[dAuthor] { return dAuthorMeta.Load(dAuthorDesc) }

var errEmptyName = errors.New("must not be empty")

func dAuthorDesc() velox.Desc[dAuthor] {
	return velox.Desc[dAuthor]{
		Table: "authors",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "OrgID", Name: "org_id"},
			{Field: "Name", Name: "name"},
			{Field: "Email", Name: "email", Sensitive: true},
			{Field: "CreatedAt", Name: "created_at", DefaultFunc: func() any { return time.Now() }, Immutable: true},
		},
		PrimaryKey: []int{0},
		Tenant:     1,
		Relations:  []velox.Relation{velox.EdgeTo[dBook]("Books", 0, 1)},
		Addr: func(x *dAuthor, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OrgID
			case 2:
				return &x.Name
			case 3:
				return &x.Email
			case 4:
				return &x.CreatedAt
			}
			return nil
		},
		RelAddr: func(x *dAuthor, i int) any {
			if i == 0 {
				return &x.Books
			}
			return nil
		},
		Check: func(col int, v any) error {
			if col == 2 {
				if s := v.(string); s == "" {
					return errEmptyName
				} else if len(s) > 20 {
					return errors.New("longer than 20")
				}
			}
			return nil
		},
	}
}

// dBook belongs to an author.
type dBook struct {
	ID       int64
	AuthorID int64
	Title    string
	Author   velox.One[dAuthor]
}

var dBooks = struct {
	ID       velox.NumberCol[dBook, int64]
	AuthorID velox.NumberCol[dBook, int64]
	Title    velox.StringCol[dBook, string]
	Author   velox.Rel[dBook, dAuthor]
}{velox.NumberCol[dBook, int64]{I: 0}, velox.NumberCol[dBook, int64]{I: 1}, velox.StringCol[dBook, string]{I: 2}, velox.Rel[dBook, dAuthor]{I: 0}}

var dBookMeta velox.MetaOnce[dBook]

func (*dBook) VeloxMeta() *velox.Meta[dBook] { return dBookMeta.Load(dBookDesc) }

func dBookDesc() velox.Desc[dBook] {
	return velox.Desc[dBook]{
		Table:      "books",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []velox.Relation{velox.EdgeTo[dAuthor]("Author", 1, 0)},
		Addr: func(x *dBook, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.AuthorID
			case 2:
				return &x.Title
			}
			return nil
		},
		RelAddr: func(x *dBook, i int) any {
			if i == 0 {
				return &x.Author
			}
			return nil
		},
	}
}
```

Task 4 adds `dTag` and `dBookTag` (and a `Tags` relation on `dBook`) to this file. Tasks 2 and 3 use `dAuthor` and `dBook`.

- [ ] **Step 2: Write the failing tests**

`desc_test.go`:

```go
package velox_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

func TestDescMetadata(t *testing.T) {
	info := velox.InfoOf[dAuthor]() // export_test.go: the entity's EntityInfo, or the test fails on its error
	if info.Name != "velox_test.dAuthor" || info.Table != "authors" || info.Tenant != 1 || len(info.PrimaryKey) != 1 {
		t.Fatalf("%+v", info)
	}
	email, created := info.Columns[3], info.Columns[4]
	if !email.Nullable || !email.Sensitive || velox.GoType(email) != reflect.TypeFor[*string]() {
		t.Fatalf("email %+v", email)
	}
	if !created.Immutable || created.DefaultFunc == nil || created.Nullable {
		t.Fatalf("created_at %+v", created)
	}
}

func TestDescQueriesAndLoads(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(7), "ada", nil, someTime}},
		{{int64(10), int64(1), "notes"}, {int64(11), int64(1), "more"}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	as, err := db.Query[dAuthor]().Where(dAuthors.Name.EQ("ada")).With(dAuthors.Books).Limit(1).All(velox.WithTenant(ctx, int64(7)))
	if err != nil {
		t.Fatal(err)
	}
	books, ok := as[0].Books.Get()
	if !ok || len(books) != 2 || books[1].Title != "more" {
		t.Fatalf("books %+v %v", books, ok)
	}
	if !strings.Contains(e.Log[1], `"books"."author_id" IN`) && !strings.Contains(e.Log[1], `"author_id" IN`) {
		t.Fatalf("relation SQL %s", e.Log[1])
	}
}

type badDesc struct{ ID int64 }

var badDescMeta velox.MetaOnce[badDesc]

func (*badDesc) VeloxMeta() *velox.Meta[badDesc] {
	return badDescMeta.Load(func() velox.Desc[badDesc] {
		return velox.Desc[badDesc]{Table: "b", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: 3,
			Addr: func(x *badDesc, i int) any { return &x.ID }}
	})
}

type panicDesc struct{ ID int64 }

var panicDescMeta velox.MetaOnce[panicDesc]

func (*panicDesc) VeloxMeta() *velox.Meta[panicDesc] {
	return panicDescMeta.Load(func() velox.Desc[panicDesc] { panic("boom") })
}

func TestDescErrors(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Query[badDesc]().Limit(1).All(ctx)
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("got %v", err)
	}
	_, err = db.Query[panicDesc]().Limit(1).All(ctx)
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "panicked: boom") {
		t.Fatalf("got %v", err)
	}
	if err := velox.CheckEntity[dAuthor](); err != nil {
		t.Fatal(err)
	}
}
```

- `someTime` is any fixed `time.Time`; declare it in the test file if no test file in the package already has one.
- `InfoOf` may not exist in `export_test.go`. If not, add `func InfoOf[E any, PE Entity[E]]() *EntityInfo { m := PE(nil).VeloxMeta(); if m.err != nil { panic(m.err) }; return m.info }`.
- Reuse the package's existing `ctx` test variable.
- Adjust the relation-SQL assertion to the exact text `FakeExec` logs. Read one existing relation test, such as `relation_test.go`, for the quoting.

- [ ] **Step 3: Run them to see them fail**

Run: `go test -run 'TestDesc' .`
Expected: FAIL: `velox.Desc` is undefined.

- [ ] **Step 4: Implement**

Write `desc.go` as described above. Give it a top comment: "the metadata path velox-gen writes (schema design §2); the struct-scanning path in meta.go and schema.go remains until schema plan 3".

Rename `OnUpdate` to `UpdateDefault` in `ColumnInfo` and at its uses (`withOnUpdate` becomes `withUpdateDefaults`), in tests too. Keep `ColumnDef.OnUpdate` as the old path's method name, setting `UpdateDefault`.

- [ ] **Step 5: Run the tests**

Run: `go test ./... && go vet ./... && go test -run TestBudget -count=1 .` plus the escape check from the Global Constraints.
Expected: PASS, and the escape check unchanged.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "Desc: entity metadata from generated literals

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Validators and immutable columns

**Files:**
- Create: `validate.go`, `validate_test.go`
- Modify: `errors.go` (add `ErrImmutable`), `insert.go` (validate rows; refuse immutables in an upsert's `Update`), `write.go` (validate and refuse immutables in `Update` and `Save`)

**Interfaces:**
- Consumes: `EntityInfo.check`, `ColumnInfo.Immutable` (Task 1).
- Produces:

```go
// ValidationError is a value a column's validators refused; nothing was sent.
type ValidationError struct {
	Entity string // the Go type, e.g. "gen.User"
	Field  string // the Go field name
	Err    error  // the validator's error
}

func (e *ValidationError) Error() string // "velox: gen.User: Name: must not be empty"
func (e *ValidationError) Unwrap() error  // e.Err

// ErrImmutable: "column is immutable"; an update, Save or upsert's Update list
// assigned an Immutable column: "velox: gen.User: created_at: column is immutable".
var ErrImmutable = errors.New("column is immutable")

// validateRow checks the row of in at p: every column in cols whose value is
// not a nil pointer. Non-generic.
func validateRow(in *EntityInfo, p unsafe.Pointer, cols []int) error

// validateSet refuses an Immutable column among set, then checks every
// AssignSet value that is not nil.
func validateSet(in *EntityInfo, set []Assignment) error
```

**Where they run in this task.** Task 3 later moves both inside the hook chain's core without changing them.
- **Insert:** `validateRow` runs right after `prepareRow`, in `oneStatement` and in `prepareMany`, over the statement's columns.
- **Update:** `validateSet` runs on the statement's `Set` in `UpdateQuery.statement`, after `withUpdateDefaults`.
- **`Save`:** `validateSet` runs after `withUpdateDefaults`.
- **Upsert:** in `InsertQuery.check`, refuse with `ErrImmutable` any `Update` column that is `Immutable`.
- **Values:** `validateRow` reads values through `in.kit.value(p, col)`, which dereferences. A pointer field's value is its element, or nil when the field is nil.

- [ ] **Step 1: Write the failing tests**

`validate_test.go`:

```go
package velox_test

import (
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox2"
)

func TestInsertValidates(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	tctx := velox.WithTenant(ctx, int64(7))
	err := db.Insert(&dAuthor{Name: ""}).Exec(tctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || ve.Field != "Name" || !errors.Is(err, errEmptyName) || err.Error() != "velox: velox_test.dAuthor: Name: must not be empty" {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Fatal("a refused row sends nothing")
	}
	if err := db.InsertAll([]dAuthor{{Name: "ok"}, {Name: "this name is far too long"}}).Exec(tctx); !errors.As(err, &ve) {
		t.Fatalf("InsertAll validates every row: %v", err)
	}
}

func TestUpdateValidatesAndRefusesImmutable(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
	tctx := velox.WithTenant(ctx, int64(7))
	_, err := db.Update[dAuthor]().Set(dAuthors.Name.Set("")).Where(dAuthors.ID.EQ(1)).Exec(tctx)
	var ve *velox.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v", err)
	}
	_, err = db.Update[dAuthor]().Set(dAuthors.CreatedAt.Set(time.Now())).Where(dAuthors.ID.EQ(1)).Exec(tctx)
	if !errors.Is(err, velox.ErrImmutable) || err.Error() != "velox: velox_test.dAuthor: created_at: column is immutable" {
		t.Fatalf("got %v", err)
	}
	a := dAuthor{ID: 1, OrgID: 7, Name: "ada", CreatedAt: time.Now()}
	if err := db.Save(tctx, &a, dAuthors.CreatedAt); !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("Save: %v", err)
	}
	if err := db.Insert(&dAuthor{Name: "ada"}).OnConflict(dAuthors.ID).Update(dAuthors.CreatedAt).Exec(tctx); !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("upsert: %v", err)
	}
}

func TestNilPointerIsNotValidated(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	if err := db.Insert(&dAuthor{Name: "ada"}).Exec(velox.WithTenant(ctx, int64(7))); err != nil {
		t.Fatal(err)
	}
}
```

- Use the handles' real setter name (`Set`, or whatever `handles.go` calls it). Read `handles.go` and fix the test's calls to match it.
- Any existing test that asserts `OnUpdate` columns must keep passing after the rename.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'Validat|Immutable' .`
Expected: FAIL: `ValidationError` and `ErrImmutable` are undefined.

- [ ] **Step 3: Implement** `validate.go` and the four call sites above. Write a doc comment on `ValidationError` and `ErrImmutable` stating Rulings 7 and 8.

- [ ] **Step 4: Run the tests**

Run: `go test ./... && go test -run TestBudget -count=1 .`
Expected: PASS. Budgets are unchanged: an entity with `check == nil` and no immutable column skips both functions after one branch.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Validators and immutable columns on the Desc path

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Typed hooks and the `Policy()` and `Hooks()` methods

**Files:**
- Create: `hook.go`, `hook_test.go`
- Modify: `desc.go` (detect `Policy()` and `Hooks()`), `meta.go` (`EntityInfo.hooks`), `scope_api.go` (`Mutation[E]` over a non-generic `mutation`; `Rows`, `Result`), `insert.go`, `write.go`, `db.go` (the chain around every write), `errors.go`

**Interfaces:**
- Consumes: `MetaOnce.Load` (Task 1), `validateRow` and `validateSet` (Task 2).
- Produces:

```go
// Mutator applies a write of entity E.
type Mutator[E any] interface {
	Mutate(ctx context.Context, m *Mutation[E]) error
}

// MutateFunc adapts a function to a Mutator.
type MutateFunc[E any] func(ctx context.Context, m *Mutation[E]) error

func (f MutateFunc[E]) Mutate(ctx context.Context, m *Mutation[E]) error // an ordinary method: Mutator's method set needs it (methods_test exception, like One.MarshalJSON)

// Hook is middleware over an entity's writes: it returns a Mutator that does
// its work and calls next.Mutate exactly once (or not at all, to refuse).
type Hook[E any] func(next Mutator[E]) Mutator[E]

// On returns h applied only to the given kinds of write: OpInsert, OpUpdate,
// OpDelete. Other writes skip it.
func On[E any](h Hook[E], ops ...Op) Hook[E]

// Result is what a write did, read after next returns.
type Result struct {
	Affected int64 // rows inserted, matched or deleted; 0 under SQL(ctx)
}

// Mutation gains:
func (m *Mutation[E]) Rows[_ Lazy[E]]() []*E    // an insert's rows, which a hook may change before next; nil for update and delete
func (m *Mutation[E]) Result[_ Lazy[E]]() Result
// Op, Value, Set and Where are as today. Value, Set and Where on an insert
// record an error ("Set and Where are for updates and deletes; change Rows").
```

- `methods_test.go`'s list of exceptions gains `MutateFunc.Mutate`, with the reason "interface satisfaction".
- The entity's methods, which velox detects when it builds the metadata:

```go
func (*E) Policy() velox.Policy[E]   // replaces s.Policy
func (*E) Hooks() []velox.Hook[E]     // replaces BeforeInsert, BeforeUpdate, BeforeDelete
```

**Internals:**

```go
// mutation is a write in flight, without E: what Mutation[E] wraps.
type mutation struct {
	op     Op               // set before the hooks run: Mutation.Op reads it, since an insert's st does not exist yet
	st     *Statement       // the update or delete; for an insert, nil until next builds it
	rows   []unsafe.Pointer // an insert's rows
	res    Result
	err    error // a misuse recorded by Value, Set or Where
	core   error // the error next returned, to pass through unwrapped (Ruling 4)
	sql    string
	args   []any // what next rendered under SQL(ctx)
}

type mutateFn func(ctx context.Context, mu *mutation) error

// hookFn is a Hook[E] with E erased, made once when the metadata is built.
type hookFn func(next mutateFn) mutateFn

// mutate runs in's hooks around core. With no hooks it calls core alone,
// allocating nothing more.
func mutate(ctx context.Context, in *EntityInfo, mu *mutation, core mutateFn) error
```

- **Erasing a hook,** in `MetaOnce.Load`:

  ```go
  func erase[E any](h Hook[E]) hookFn {
  	return func(next mutateFn) mutateFn {
  		typed := h(MutateFunc[E](func(ctx context.Context, m *Mutation[E]) error { return next(ctx, m.mu) }))
  		return func(ctx context.Context, mu *mutation) error { return typed.Mutate(ctx, &Mutation[E]{mu: mu}) }
  	}
  }
  ```

  `Mutation[E]` becomes `struct{ mu *mutation }`:
  - `Op()` returns `mu.op`;
  - `Value`, `Set` and `Where` read and change `mu.st`;
  - `mu.err` holds what `m.err` held.
- **`mutate`** builds the chain from the inside out, so the first listed hook is the outermost.
  - The core is wrapped so that its returned error is recorded in `mu.core`.
  - After the chain:
    - an error equal to `mu.core` is returned as is;
    - a nil error with a recorded `mu.err` returns `&hookError{in.Name, "hook", mu.err}`;
    - any other error is wrapped `&hookError{in.Name, "hook", err}` (Ruling 4).
- **`On(h, ops...)`** returns `func(next Mutator[E]) Mutator[E] { wrapped := h(next); return MutateFunc[E](func(ctx, m) error { if slices.Contains(ops, m.Op()) { return wrapped.Mutate(ctx, m) }; return next.Mutate(ctx, m) }) }`.
- **Detection,** in `MetaOnce.Load`, after `buildDesc` succeeds:
  - `if p, ok := any((*E)(nil)).(interface{ Policy() Policy[E] }); ok { info.policy = policyOf(p.Policy()) }`.
  - The same for `Hooks() []Hook[E]`, erasing each into `info.hooks`.
  - A nil hook in the list is a `*SchemaError`: "Hooks()[i] is nil".
  - Panics are recovered as in Task 1.
  - `policyOf[E]` is the body of today's `Schema.Policy`, factored out, and `Schema.Policy` calls it.
- **Wiring each write.** The current body of each operation becomes `core`, with validation inside (Ruling 5):
  - **`InsertQuery.Exec` / `SQL`:**
    - `mu.rows` holds `p` (Insert) or `&xs[i]` (InsertAll).
    - The core is today's `insertOne` / `insertMany` / `oneStatement`+`dryRun` body: `fillDefaults`, `prepareRow` (tenant, old `BeforeInsert`, policy `Insert`), `validateRow`, then execution.
    - For `SQL(ctx)` the core stores `mu.sql` and `mu.args` instead of executing, and `Result` stays zero.
    - The core sets `mu.res.Affected`.
  - **`UpdateQuery.Exec` / `SQL`, `DeleteQuery.Exec` / `SQL` and `Save`:**
    - Build the statement as today, but without `withUpdateDefaults`, and set `mu.st = &st`.
    - The core:
      1. `withUpdateDefaults` (update and `Save` only);
      2. `checkAssignedOnce`;
      3. `validateSet`;
      4. `execWrite` (or `dryRun` under `SQL`), which runs the guard first inside `prepare`;
      5. sets `mu.res.Affected`.
    - `Save` writes back every `AssignSet` value of `*mu.st` after a successful core, as today.
- **The old path still works.** The old `beforeUpdate`/`beforeDelete` still run in `prepare`'s `entityHooks`, and the old `beforeInsert` in `prepareRow`. An entity declares one style or the other, because the old ones exist only on the `Schema` path.
- **Docs.** `IsDryRun`'s doc comment changes "BeforeInsert, BeforeUpdate and BeforeDelete hooks" to "entity hooks (Hooks), and the old BeforeInsert, BeforeUpdate and BeforeDelete".

- [ ] **Step 1: Write the failing tests**

`hook_test.go` adds methods on the test model's `dBook`:

```go
package velox_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

// trace records the order hooks and the core ran in, per test.
var trace []string

func (*dBook) Hooks() []velox.Hook[dBook] {
	return []velox.Hook[dBook]{
		func(next velox.Mutator[dBook]) velox.Mutator[dBook] {
			return velox.MutateFunc[dBook](func(ctx context.Context, m *velox.Mutation[dBook]) error {
				trace = append(trace, "outer before "+m.Op().String())
				if m.Op() == velox.OpInsert {
					for _, b := range m.Rows() {
						b.Title = strings.TrimSpace(b.Title)
					}
				}
				err := next.Mutate(ctx, m)
				trace = append(trace, "outer after")
				return err
			})
		},
		velox.On(func(next velox.Mutator[dBook]) velox.Mutator[dBook] {
			return velox.MutateFunc[dBook](func(ctx context.Context, m *velox.Mutation[dBook]) error {
				trace = append(trace, "delete only")
				if hookRefuses {
					return errRefused
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpDelete),
	}
}

var (
	errRefused  = errors.New("refused")
	hookRefuses bool
)

func TestHookOrderAndRows(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{{int64(5)}}})
	b := dBook{AuthorID: 1, Title: "  dune  "}
	if err := db.Insert(&b).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Title != "dune" || b.ID != 5 {
		t.Fatalf("the hook trims before next, and next writes back the key: %+v", b)
	}
	if strings.Join(trace, "|") != "outer before INSERT|outer after" {
		t.Fatalf("trace %v", trace)
	}
}

func TestOnSelectsOps(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 2})
	n, err := db.Delete[dBook]().Where(dBooks.AuthorID.EQ(1)).Exec(ctx)
	if err != nil || n != 2 || strings.Join(trace, "|") != "outer before DELETE|delete only|outer after" {
		t.Fatalf("%d %v %v", n, err, trace)
	}
}

func TestHookErrorIsWrapped(t *testing.T) {
	hookRefuses = true
	defer func() { hookRefuses = false }()
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Delete[dBook]().Where(dBooks.ID.EQ(1)).Exec(ctx)
	if !errors.Is(err, errRefused) || err.Error() != "velox: velox_test.dBook: hook: refused" {
		t.Fatalf("got %v", err)
	}
}

func TestHookPassesVeloxErrorsThrough(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Delete[dBook]().Exec(ctx) // no Where: the guard refuses inside next
	if !errors.Is(err, velox.ErrNoWhere) || strings.Count(err.Error(), "velox:") != 1 {
		t.Fatalf("got %v", err)
	}
}

func TestHooksUnderSQL(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	q, _, err := db.Insert(&dBook{AuthorID: 1, Title: " x "}).SQL(ctx)
	if err != nil || !strings.HasPrefix(q, "INSERT") || strings.Join(trace, "|") != "outer before INSERT|outer after" {
		t.Fatalf("%q %v %v", q, err, trace)
	}
}
```

`dAuthor` also gets methods that test validation after a hook (Review Focus 2) and `Policy()`:

```go
func (*dAuthor) Hooks() []velox.Hook[dAuthor] {
	return []velox.Hook[dAuthor]{func(next velox.Mutator[dAuthor]) velox.Mutator[dAuthor] {
		return velox.MutateFunc[dAuthor](func(ctx context.Context, m *velox.Mutation[dAuthor]) error {
			if v, _ := ctx.Value(blankName{}).(bool); v && m.Op() == velox.OpUpdate {
				m.Set(dAuthors.Name.Set("")) // validated after the hook
			}
			if v, _ := ctx.Value(touchCreated{}).(bool); v && m.Op() == velox.OpUpdate {
				m.Set(dAuthors.CreatedAt.Set(someTime)) // immutable: refused after the hook
			}
			return next.Mutate(ctx, m)
		})
	}}
}

type blankName struct{}
type touchCreated struct{}

func (*dAuthor) Policy() velox.Policy[dAuthor] {
	return velox.Policy[dAuthor]{Insert: func(ctx context.Context, a *dAuthor) error {
		if a.Name == "mallory" {
			return velox.Deny("not mallory")
		}
		return nil
	}}
}

func TestHookSetIsValidated(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
	tctx := velox.WithTenant(ctx, int64(7))
	_, err := db.Update[dAuthor]().Set(dAuthors.Name.Set("ok")).Where(dAuthors.ID.EQ(1)).Exec(context.WithValue(tctx, blankName{}, true))
	var ve *velox.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v", err)
	}
	_, err = db.Update[dAuthor]().Set(dAuthors.Name.Set("ok")).Where(dAuthors.ID.EQ(1)).Exec(context.WithValue(tctx, touchCreated{}, true))
	if !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("got %v", err)
	}
}

func TestPolicyMethod(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	err := db.Insert(&dAuthor{Name: "mallory"}).Exec(velox.WithTenant(ctx, int64(7)))
	if !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("got %v", err)
	}
}
```

Add one test that `Save` runs the hooks with `Op() == OpUpdate` and writes back a value a hook `Set`.

`TestBudget*` stays as it is. It uses `internal/testmodel` entities, which move to `Desc` only in Task 5. Add one alloc check here: a cached one-row `Insert` of `dBook` with its hooks removed must match the old path's `Insert` budget. Write it with an entity like `dBook` without methods, for example `dPlain`, declared beside it.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run 'Hook|Policy|On' .`
Expected: FAIL: `velox.Hook` is undefined.

- [ ] **Step 3: Implement** as described under "Internals". Keep each write's core in its file (`insert.go`, `write.go`) as a function taking `(ctx, *mutation)`.

- [ ] **Step 4: Run every check**

Run: `go test ./... && go test -race ./... && go vet ./...`, the budgets, and the escape check.
Expected: PASS. `hooks_internal_test.go`, `policy_test.go` and `schema_policy_internal_test.go` still pass untouched: they use the old path.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Typed hooks and the Policy and Hooks methods

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Many-to-many through a join entity

**Files:**
- Create: `through_test.go`, `internal/itest/through_test.go`
- Modify: `desc.go` (`EdgeThrough`), `relation.go` (`link` gains the join), `load.go` (loading through), `render.go` (nested EXISTS), `render_cache.go` (only if the key needs it; see below), `desc_model_test.go` (`dTag`, `dBookTag`, `dBook.Tags`)

**Interfaces:**
- Consumes: `EdgeThrough`'s signature (Task 1).
- Produces:

```go
type link struct {
	target     *EntityInfo
	ownerCol   int
	targetCol  int
	join       *EntityInfo // many-to-many: the join entity; nil otherwise
	joinOwner  int         // join's column equal to owner.Columns[ownerCol]
	joinTarget int         // join's column equal to target.Columns[targetCol]
}
```

**What changes:**
- **Resolving.** `relation.build` for a through relation resolves both entities. It checks that each pair of key types matches: owner and join-owner, join-target and target.
- **Loading** (`loadOne`, when `l.join != nil`):
  1. Fetch the join rows with `fetch(ctx, db, l.join, link{target: l.join, targetCol: l.joinOwner}, loadSpec{}, keys, maxRows)`.
  2. Build `ownerKey → []targetKey`, in join-row order and without duplicates.
  3. Fetch the targets with `fetch(ctx, db, l.target, link{target: l.target, targetCol: l.targetCol}, sp, distinctTargetKeys, maxRows)`. The user's `Where`, `OrderBy` and nested `With` apply here.
  4. For each owner, its `Many` holds the fetched targets whose key is among its target keys, in fetched order. An owner with none gets an empty loaded `Many`, as today.
- **Rendering.** `writer.exists` for a join link writes:

  ```sql
  EXISTS (SELECT 1 FROM <join> AS vxN WHERE vxN.<joinOwner> = <outer>.<ownerCol> AND EXISTS (SELECT 1 FROM <target> AS vxN+1 WHERE vxN+1.<targetCol> = vxN.<joinTarget> AND <kids…>))
  ```

  - The kids bind to the innermost alias, as today.
  - `w.depth` advances twice.
  - With no kids, the inner EXISTS is still written: it asserts that the target row exists and is visible to the scope.
- **Scope.** It is unchanged. `sc.node` reads `n.v.(link).target`, which is T, and appends T's tenant and Read conditions to the kids. They land inside the inner EXISTS (Review Focus 5). The join entity has no tenant or policy of its own. If it ever does, it is not applied; say so in `EdgeThrough`'s doc comment.
- **The render cache.** `nodeKey` keys `opExists` by the relation index, which fixes the link, so the key needs no change. Pin it with a test: two `HasWhere` on different through relations of one entity render different SQL.
- **`checkColumns`.** For an `opExists`, it checks `link.ownerCol`, which is still the owner's column.

**The test model.** Add to `desc_model_test.go`:
- `dTag{ID int64; Name string; Books velox.Many[dBook]}`;
- `dBookTag{BookID int64; TagID int64}`, with `PrimaryKey: []int{0, 1}`, `Tenant: -1` and no relations;
- `dBook.Tags velox.Many[dTag]`, its relation index 1 and handle `dBooks.Tags`, with `velox.EdgeThrough[dBookTag, dTag]("Tags", 0, 0, 1, 0)`;
- `dTag.Books`, with `velox.EdgeThrough[dBookTag, dBook]("Books", 0, 1, 0, 0)`.

The many-to-many tests make `dTag` tenant-scoped through a tenant column `OrgID`. That lets them check Review Focus 5.

- [ ] **Step 1: Write the failing tests**

`through_test.go`. It covers:
- `TestThroughLoadSharedAndEmpty`:
  - three books;
  - book 1 → tags {1, 2}, book 2 → tags {2}, book 3 → none;
  - `FakeExec.Results` gives the books, then the join rows `{1,1},{1,2},{2,2}`, then tags 1 and 2.
  - It asserts each book's tags, in order, that book 3's `Tags.Get()` is `([], true)`, and that the third query's arguments are the distinct target keys.
- `TestThroughHasRenders`: `db.Query[dBook]().Where(dBooks.Tags.HasWhere(dTags.Name.EQ("go"))).Limit(1).SQL(ctx)` equals the exact nested EXISTS text. Read `render_test.go` for how exact SQL is asserted and quoted on PostgreSQL.
- `TestThroughHasScopesTarget`: under `WithTenant(ctx, int64(7))`, the tenant condition `"org_id" = $n` appears after the inner EXISTS's `WHERE`, not in the outer.
- `TestThroughCacheKey`: `dTag.Books.Has()` and `dBook.Tags.Has()` give different SQL from the cache. If the two are on different entities, key the test on a second through relation of one entity instead; add one to `dBook` if needed.
- `TestThroughJoinRowsWrite`: `db.Insert(&dBookTag{BookID: 1, TagID: 2}).Exec(ctx)` sends `INSERT INTO "book_tags" ("book_id", "tag_id")…`, and `Delete[dBookTag]` with both keys works.

`internal/itest/through_test.go` covers the same load and `Has` on SQLite, PostgreSQL (both drivers) and MySQL, following `internal/itest/relations_test.go`'s structure:
- tables `books`, `tags` and `book_tags` created in the test;
- rows inserted;
- `With(dBooks.Tags)` loaded and compared;
- `Has` counted.

The itest model lives in `internal/itest` itself, as hand-written `Desc` entities in a `model_test.go`, mirroring the root test's.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -run Through .`
Expected: FAIL: the many-to-many relation fails at `build` (Task 1's placeholder).

- [ ] **Step 3: Implement** as described under "What changes".

- [ ] **Step 4: Run every check**

Run: `go test ./... && go test -race ./...`, then `scripts/ci-local.sh` for the real databases. Running the whole script is fine: it runs everything.
Expected: PASS on every dialect.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "Many-to-many relations through a join entity

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The test model moves to `Desc`; documents

**Files:**
- Modify: `internal/testmodel/*.go`:
  - the `*_velox.go` files are replaced by hand-written `*_desc.go`, keeping the struct shapes;
  - the `Schema` methods are removed;
  - `ticket.go`'s policy and hooks become `Policy()` and `Hooks()` methods.
- Modify: the tests whose expected error texts change (`before insert:`, `before update:` and `before delete:` become `hook:` for `Ticket`).
- Create: `cmd/velox-gen/testdata/testmodel/`, a copy of today's `internal/testmodel` (structs, `Schema` methods and generated files).
- Modify: `cmd/velox-gen/golden_test.go` (`TestTestmodelIsReproduced` reads that copy), `cmd/velox-gen/discover_test.go` (`TestDiscoveryOfTestmodel` likewise).
- Modify: `docs/specs/2026-10-15-schema-design.md` (§3: `velox.On(h, velox.OpInsert, …)` and `Result`), `docs/plans/2026-10-14-core-1-followups.md` ("Left by schema plan 2").

**Interfaces:**
- Consumes: everything above.
- Produces: the test model on the `Desc` path, which plan 3 regenerates from a DSL schema.

**How:**
1. Copy `internal/testmodel` to `cmd/velox-gen/testdata/testmodel` first. Point the old generator's golden and discovery tests at the copy, and run them, so they keep guarding the old generator until plan 3 replaces it.
   - The copy is a fixture inside `testdata`, so `go vet ./...` skips it. The tests load it through their existing fixture helpers.
   - Give it its own `go.mod` only if those helpers need one; read `discover_test.go`'s `loadFixture`.
2. **Rewrite each `internal/testmodel` entity** (Invoice, Item, Line, Sample, Ticket) to the `Desc` path:
   - A `*_desc.go` file with the handles variable, `MetaOnce`, `VeloxMeta` calling `Load`, and a `…Desc()` function.
   - It does **not** start with velox-gen's header. Its header is `// Written by hand in the shape velox-gen will generate (schema plan 3).` This keeps the old generator's `-check ./...` from treating it as its own. The old `*_velox.go` files are deleted.
   - Columns, order, names, keys, the tenant, indexes and relations stay exactly as today's metadata has them.
   - Read each `Schema` method and the generated `Shape` to transcribe them. The `Timestamps` mixin's `DefaultFunc` and `OnUpdate` become `DefaultFunc` and `UpdateDefault` in `ColumnDesc`.
   - Relations become `EdgeTo` with the column indexes today's resolution finds. `velox.CheckEntity` must pass for each.
3. **Ticket:**
   - `Policy()` returns today's `Policy[Ticket]`.
   - `Hooks()` returns three hooks:
     - Stamp's `CreatedBy` fill, as an insert-only hook (`On(…, OpInsert)`);
     - the empty-title checks for insert and update;
     - the delete narrowing `m.Where(Tickets.Done.EQ(true))`, on `OpDelete`.
   - Keep the `Stamp` struct as an embedded field, so tests that build a `Ticket` keep compiling.
4. **Update the tests.** Run `go test ./...` and `internal/itest`, and update each test that fails. The only changes allowed are:
   - error texts whose stage is now `hook` rather than `before insert`, `before update` or `before delete`;
   - assertions about which stage ran first. A Ticket title check now runs before the tenant fill (Ruling 5); state the reason in the test's comment.

   **Any other failure is a bug in Tasks 1–4.** Fix it there, and say so in the report.
5. `budget_test.go` must pass unchanged on the moved model. A failure here is a bug in Task 3's no-hooks path.
6. **Documents:**
   - The spec's §3 sample uses `velox.On(audit.Hook[User](), velox.OpInsert, velox.OpUpdate, velox.OpDelete)` and says `Result()` carries `Affected` (Rulings 2 and 3). Edit the text in place.
   - In the followups, the "Left by schema plan 2" section lists:
     - the old path (`Schema`, `Mixer`, `BeforeX`, `Shape`, `MetaOnce.Get`, `RelationTo`, reflection over structs) is removed in plan 3;
     - join entities have no tenant or policy of their own;
     - `InsertAll` still reads no keys back, so `Rows()` after `next` has no IDs for it;
     - a hook can change the rows of an insert but not how many there are.

- [ ] **Step 1: Copy the fixture and repoint the old generator's tests; run `go -C cmd/velox-gen test ./...`.** Expected: PASS.
- [ ] **Step 2: Rewrite the test model** (steps 2–3 above).
- [ ] **Step 3: Run everything**

Run:

```bash
go test ./... && go test -race ./... && go vet ./...
go -C cmd/velox-gen test ./...
(cd example && go test ./... && go tool velox-gen -check ./...)
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
scripts/ci-local.sh
```

Also run the budgets and the escape check.
Expected: PASS everywhere. `bin/velox-gen -check ./...` at the root (in `ci-local.sh`) passes, because no `internal/testmodel` file carries its header any more.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "The test model on the Desc path; Ticket's policy and hooks as methods

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
