# Core 1 — Foundation: Querying — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working read path for velox2: entity metadata from plain structs and their `Schema` methods, typed column handles, SQL for PostgreSQL, MySQL and SQLite, the execution pipeline with its rewrite and around hooks, the native-pgx and `database/sql` executors, and the query API (`Where`, `OrderBy`, `Limit`, `Offset`, `Columns`, `Omit`, `All`, `One`, `Iter`, `Count`, `SQL`), tested on all three databases.

**Architecture:** One root package `velox` (module `github.com/syssam/velox2`). Generic code is a thin typed layer: column handles produce `Pred[E]` values that wrap a non-generic `node`. A query becomes one non-generic `*Statement`, which rewrite hooks may change, a dialect renders (through a cache keyed by the statement's shape), around hooks wrap, and an executor runs. Rows are scanned through the per-entity scan plan that velox-gen will write. In this plan that file is written by hand, in `internal/testmodel`.

**Tech Stack:** Go 1.27 (generic methods), `github.com/jackc/pgx/v5` v5.11.0, `github.com/go-sql-driver/mysql`, `modernc.org/sqlite`, Docker Compose (PostgreSQL 16, MySQL 8.4) for the local CI mirror.

**Spec:** `docs/history/specs/2026-10-14-core-design.md`. Read §1 (entity model), §2 (generated code), §3 (compile-cost rules), §4.1 and §4.3 (API), §5 (pipeline), §6 (dialects), §7 (errors) and §8 (performance budget) before starting.

**This is plan 1 of 5 for Core.** It is followed by: 2, the generator (`velox-gen`, `-check`, `verify`, naming); 3, writes (insert, upsert, update, `Save`, delete, transactions, constraint errors); 4, relations (`With`, `Has`/`HasWhere`, keyset pagination); 5, scale guards (`bench/scale`, compile-size guards). Each later plan is written once the one before it is built.

## Global Constraints

- Module path `github.com/syssam/velox2`; the root package is named `velox`. `go.mod` says `go 1.27.0`.
- Every method of a generic velox type is a generic method with a type parameter callers never write (`[_ Lazy[V]]`), except the unexported `column(*E) int32` of the column handle types (spec §3 rule 1).
- `One[T]` and `Many[T]` hold their value as `any`; `T` appears only in method signatures (spec §3 rule 2).
- Nothing proportional to the number of entities runs at program start: generated (and hand-written test) entity files declare only static data, zero values and the `VeloxMeta` method (spec §3 rule 4).
- Every error velox creates has a message beginning `velox: `. Errors from the database or driver are wrapped as `velox: <Entity>: <error>`.
- Dependencies of the root module: pgx v5.11.0, go-sql-driver/mysql (test only), modernc.org/sqlite (test only). Nothing else.
- PostgreSQL: `velox.OpenPgx(pool)` (native pgx) is the recommended form; `velox.Open(velox.Postgres, sqlDB)` also works. MySQL and SQLite: `velox.Open(dialect, sqlDB)`.
- Placeholders: `$n` on PostgreSQL, `?` on MySQL and SQLite. Identifiers are always quoted: `"x"` on PostgreSQL and SQLite, `` `x` `` on MySQL, with the quote character doubled inside.
- Tool minimums (spec §10): gopls v0.23.0, staticcheck 2026.2.2 (v0.8.2), golangci-lint v2.14.0. Older versions reject generic methods.
- Deliberate deferrals from spec §5, recorded here so no one adds them early: the route stage's option (sub-project 4) and `Statement.Tags` (sub-project 3).

## Review Focus

1. **User text containing `%`, `_` or `\` passed to `HasPrefix`, `HasSuffix` or `Contains`** must match literally on all three databases (`HasPrefix("50%")` must not match `500X`). Pinned by `TestLikeTakesTextLiterally` in Task 6.
2. **Whether text matching ignores case differs by database**: PostgreSQL's `LIKE` is case-sensitive; MySQL's default collation and SQLite (for ASCII) are not. This must be documented on the methods and pinned per dialect, not left to surprise. Pinned by `TestTextMatchingFollowsTheDatabase` in Task 6 (all three databases from Task 8).
3. **Table or column names that are reserved words or contain the quote character** (`user`, `order`, `` a`b"c ``) must produce valid SQL on every dialect. Pinned by `TestRenderQuotesIdentifiers` in Task 4.
4. **Running the same `Query` value more than once, with a rewrite hook that adds a condition**, must send identical SQL each time and leave the query unchanged. Pinned by `TestQueryCanRunAgain` in Task 5.
5. **Many goroutines sharing one `DB`**, and so one render cache, must each get their own arguments with no data race. Pinned by `TestCacheIsSafeForConcurrentUse` (run with `-race`) in Task 7.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `.gitignore`, `.golangci.yml` | Module and tool configuration |
| `doc.go` | Package documentation, `GeneratedCodeIsVersion1`, `Lazy`, the `go:generate` line |
| `errors.go` | Sentinel errors and `SchemaError` |
| `names.go` | `snake` (Go identifier → snake_case) |
| `schema.go` | `Schema`, `ColumnDef`, `Schemer`, `Mixer`: what an entity's `Schema` method declares, field pointers resolved to columns |
| `meta.go` | `EntityInfo`, `ColumnInfo`, `IndexInfo`, `Shape`, `Meta`, `MetaOnce`, `Entity`; building metadata, the stale-code check, mixins |
| `relation.go` | `One[T]`, `Many[T]` relation holders |
| `pred.go` | `node`, `Pred`, `Predicate`, `Order`, `OrderTerm`, `Column`, `And`/`Or`/`Not`, `ColumnEQ`, LIKE escaping |
| `internal/handlegen/handlegen.go`, `internal/handlegen/gen/main.go` | Generator of `handles.go` |
| `handles.go` | Generated: the 8 column handle types and their methods |
| `dialect.go` | `Dialect`, quoting, placeholders, the LIKE escape clause |
| `statement.go` | `Op`, `Statement`, `Rewrite`, `Around` |
| `render.go` | Statement → SQL and arguments |
| `render_cache.go` | Cache of SQL text by statement shape |
| `executor.go` | The internal `rows`/`executor` interfaces; `database/sql` and pgx executors |
| `db.go` | `DB`, `Open`, `OpenPgx`, `Option`, `WithRewrite`, `WithAround`, the pipeline |
| `query.go` | `Query[E]` and its methods |
| `export_test.go` | Test-only access: a fake executor and the hand-written baseline |
| `internal/testmodel/` | Test entities with hand-written `*_velox.go` files in generated form |
| `internal/itest/` | Integration tests over the dialect matrix |
| `docker-compose.ci.yml`, `scripts/ci-local.sh`, `.github/workflows/ci.yml` | CI and its local mirror |

---

### Task 1: Module scaffold, errors, snake_case

**Files:**
- Create: `go.mod`, `.gitignore`, `doc.go`, `errors.go`, `names.go`
- Test: `errors_test.go`, `names_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `const GeneratedCodeIsVersion1 = true`; `type Lazy[T any] interface{ *T }`; `var ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrStale error`; `type SchemaError struct{ Entity, Field, Reason string }` with `Error() string`; `func snake(s string) string`.

- [ ] **Step 1: Initialize the repository and module**

```bash
cd <repo>
git init -b main
go mod init github.com/syssam/velox2
go mod edit -go=1.27.0
printf '/coverage.out\n/bin/\n' > .gitignore
```

- [ ] **Step 2: Write the failing tests**

`errors_test.go`:

```go
package velox

import (
	"errors"
	"strings"
	"testing"
)

func TestSentinelErrorsStartWithVelox(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrStale} {
		if !strings.HasPrefix(err.Error(), "velox: ") {
			t.Errorf("%q does not start with %q", err, "velox: ")
		}
	}
}

func TestSchemaErrorNamesEntityAndField(t *testing.T) {
	cases := []struct {
		err  *SchemaError
		want string
	}{
		{&SchemaError{Entity: "billing.Invoice", Field: "Total", Reason: "bad"}, "velox: billing.Invoice.Total: bad"},
		{&SchemaError{Entity: "billing.Invoice", Reason: "no primary key"}, "velox: billing.Invoice: no primary key"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	var se *SchemaError
	if !errors.As(error(cases[0].err), &se) {
		t.Error("errors.As does not find *SchemaError")
	}
}
```

`names_test.go`:

```go
package velox

import "testing"

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"ID": "id", "OrgID": "org_id", "CreatedAt": "created_at", "HTTPServer": "http_server",
		"URLPath": "url_path", "userID": "user_id", "E0001": "e0001", "A": "a", "Invoice": "invoice",
		"lineItem": "line_item",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: ErrNotFound`, `undefined: SchemaError`, `undefined: snake`.

- [ ] **Step 4: Write the implementation**

`doc.go`:

```go
// Package velox is an ORM for Go 1.27 and later. Entities are plain structs
// owned by the packages that use them; velox-gen writes a small file of typed
// column handles beside each one.
package velox

//go:generate go run ./internal/handlegen/gen

// GeneratedCodeIsVersion1 is referenced by every file velox-gen writes, so
// that generated code and this library fail to compile together when they
// disagree. It is replaced by GeneratedCodeIsVersion2 when generated code must
// change.
const GeneratedCodeIsVersion1 = true

// Lazy is the constraint of the type parameter that every method of a
// generic velox type carries, and that callers never write. A generic method
// is compiled only where it is called; an ordinary method of a generic type is
// compiled into every package that uses the type.
type Lazy[T any] interface{ *T }
```

`errors.go`:

```go
package velox

import "errors"

// Errors velox returns, wrapped with the entity's name; test them with
// errors.Is.
var (
	// ErrNotFound: One found no row.
	ErrNotFound = errors.New("velox: not found")
	// ErrNotSingular: One found more than one row.
	ErrNotSingular = errors.New("velox: more than one row")
	// ErrUnbounded: a query with no Limit that did not say Unbounded. Raised
	// by the guardrails (sub-project 2).
	ErrUnbounded = errors.New("velox: query has no Limit")
	// ErrNoWhere: an update or delete with no Where that did not say All.
	// Raised by the guardrails (sub-project 2).
	ErrNoWhere = errors.New("velox: update or delete has no Where")
	// ErrStale: an entity's struct changed after velox-gen last ran.
	ErrStale = errors.New("velox: generated code is out of date")
)

// SchemaError reports a mistake in an entity's Schema method or in a
// mixin's Mixin method.
type SchemaError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	Field  string // the Go field name; empty when the error is about the entity
	Reason string
}

func (e *SchemaError) Error() string {
	if e.Field == "" {
		return "velox: " + e.Entity + ": " + e.Reason
	}
	return "velox: " + e.Entity + "." + e.Field + ": " + e.Reason
}
```

`names.go`:

```go
package velox

import "strings"

// snake returns the snake_case of a Go identifier: OrgID becomes org_id and
// HTTPServer becomes http_server.
func snake(s string) string {
	lower := func(c byte) bool { return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' }
	upper := func(c byte) bool { return 'A' <= c && c <= 'Z' }
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := range len(s) {
		c := s[i]
		if upper(c) {
			if i > 0 && (lower(s[i-1]) || upper(s[i-1]) && i+1 < len(s) && lower(s[i+1])) {
				b.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./...`
Expected: `ok  	github.com/syssam/velox2`

- [ ] **Step 6: Commit**

```bash
git add go.mod .gitignore doc.go errors.go names.go errors_test.go names_test.go docs
git commit -m "Add the velox2 module, its errors and snake_case naming" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Entity metadata

**Files:**
- Create: `schema.go`, `meta.go`, `relation.go`
- Test: `meta_test.go`

**Interfaces:**
- Consumes: `snake`, `SchemaError`, `ErrStale`, `Lazy` (Task 1).
- Produces:
  - `type Schemer interface{ Schema(*Schema) }`, `type Mixer interface{ Mixin(*Schema) }`
  - `type Schema struct{…}` with `Table(name string)`, `Column[V any](p *V) *ColumnDef[V]`, `PrimaryKey(fields ...any)`, `Unique(fields ...any)`, `Index(fields ...any)`, `Tenant(field any)`
  - `type ColumnDef[V any] struct{…}` with `Name[_ Lazy[V]](string)`, `SQLType[_ Lazy[V]](string)`, `Default[_ Lazy[V]](V)`, `OnUpdate[_ Lazy[V]](func() V)`, each returning `*ColumnDef[V]`
  - `type EntityInfo struct{ Name, Table string; Columns []ColumnInfo; PrimaryKey []int; Tenant int; Indexes []IndexInfo }` with `Column(name string) (int, bool)`
  - `type ColumnInfo struct{ Field, Name string; Type reflect.Type; Nullable bool; SQLType string; Default any; HasDefault bool; OnUpdate func() any }`
  - `type IndexInfo struct{ Columns []int; Unique bool }`
  - `type Shape[E any] struct{ Columns, Relations []string; Addr func(x *E, i int) any }`
  - `type Meta[E any] struct{ info *EntityInfo; addr func(*E, int) any; err error }`
  - `type MetaOnce[E any] struct{…}` with `Get[_ Lazy[E]](shape func() Shape[E]) *Meta[E]`
  - `type Entity[E any] interface{ *E; VeloxMeta() *Meta[E] }`
  - `type One[T any]` with `Get[_ Lazy[T]]() (*T, bool)`; `type Many[T any]` with `Get[_ Lazy[T]]() ([]T, bool)`
  - `func buildInfo(t reflect.Type, base unsafe.Pointer, x any, cols, rels []string) (*EntityInfo, error)`

- [ ] **Step 1: Write the failing tests**

`meta_test.go`:

```go
package velox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

type stamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (t *stamps) Mixin(s *Schema) { s.Column(&t.UpdatedAt).OnUpdate(time.Now) }

type other struct{ ID int64 }

func (*other) Schema(*Schema) {}

type order struct {
	stamps
	ID       int64
	TenantID int64
	Code     string
	Total    float64
	ShipAt   *time.Time
	Lines    Many[other]
	Owner    One[other]
	Display  string `velox:"-"`
	cache    string
}

func (x *order) Schema(s *Schema) {
	s.Table("orders")
	s.Column(&x.Total).SQLType("numeric(18,2)").Default(1.5)
	s.Column(&x.Code).Name("order_code")
	s.Unique(&x.TenantID, &x.Code)
	s.Tenant(&x.TenantID)
}

var (
	orderCols = []string{"CreatedAt", "UpdatedAt", "ID", "TenantID", "Code", "Total", "ShipAt"}
	orderRels = []string{"Lines", "Owner"}
)

func infoOf[E any](cols, rels []string) (*EntityInfo, error) {
	x := new(E)
	return buildInfo(reflect.TypeFor[E](), unsafe.Pointer(x), any(x), cols, rels)
}

func TestEntityInfo(t *testing.T) {
	_ = order{cache: "never a column"}
	info, err := infoOf[order](orderCols, orderRels)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "velox.order" || info.Table != "orders" {
		t.Errorf("name %q table %q", info.Name, info.Table)
	}
	var names []string
	for _, c := range info.Columns {
		names = append(names, c.Name)
	}
	if want := []string{"created_at", "updated_at", "id", "tenant_id", "order_code", "total", "ship_at"}; !reflect.DeepEqual(names, want) {
		t.Errorf("columns %v, want %v", names, want)
	}
	for i, c := range info.Columns {
		if c.Nullable != (c.Field == "ShipAt") {
			t.Errorf("column %d (%s) nullable = %v", i, c.Field, c.Nullable)
		}
	}
	total := info.Columns[5]
	if total.SQLType != "numeric(18,2)" || !total.HasDefault || total.Default != 1.5 {
		t.Errorf("total: %+v", total)
	}
	if info.Columns[1].OnUpdate == nil {
		t.Error("the mixin's OnUpdate on updated_at was not applied")
	}
	if !reflect.DeepEqual(info.PrimaryKey, []int{2}) || info.Tenant != 3 {
		t.Errorf("primary key %v tenant %d", info.PrimaryKey, info.Tenant)
	}
	if want := []IndexInfo{{Columns: []int{3, 4}, Unique: true}}; !reflect.DeepEqual(info.Indexes, want) {
		t.Errorf("indexes %+v", info.Indexes)
	}
	if i, ok := info.Column("order_code"); !ok || i != 4 {
		t.Errorf("Column(order_code) = %d, %v", i, ok)
	}
}

func TestStaleColumns(t *testing.T) {
	_, err := infoOf[order](orderCols[:6], orderRels)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("got %v, want ErrStale", err)
	}
	want := "velox: generated code is out of date: velox.order: columns #6 is ShipAt in the struct and (none) in the generated code; run go tool velox-gen"
	if err.Error() != want {
		t.Errorf("message\n got %s\nwant %s", err, want)
	}
}

func TestStaleRelations(t *testing.T) {
	if _, err := infoOf[order](orderCols, []string{"Lines"}); !errors.Is(err, ErrStale) {
		t.Fatalf("got %v, want ErrStale", err)
	}
}

type badSchema struct {
	ID     int64
	Name   string
	hidden int
}

func (x *badSchema) Schema(s *Schema) {
	s.Column(&x.hidden).Name("h")
	s.Tenant(x.ID)
	s.Tenant(&x.ID)
	s.Tenant(&x.ID)
}

func TestSchemaErrors(t *testing.T) {
	_, err := infoOf[badSchema]([]string{"ID", "Name"}, nil)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a *SchemaError", err)
	}
	for _, part := range []string{"is not a column", "expected a pointer to a field", "Tenant is declared twice"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("%q does not mention %q", err, part)
		}
	}
}

type noKey struct{ Name string }

func (*noKey) Schema(*Schema) {}

func TestNoPrimaryKey(t *testing.T) {
	_, err := infoOf[noKey]([]string{"Name"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no primary key") {
		t.Fatalf("got %v", err)
	}
}

type lineItem struct{ ID int64 }

func (*lineItem) Schema(*Schema) {}

func TestTableDefaultsToSnakeCase(t *testing.T) {
	info, err := infoOf[lineItem]([]string{"ID"}, nil)
	if err != nil || info.Table != "line_item" {
		t.Fatalf("table %v, err %v", info, err)
	}
}

type embedsPointer struct {
	*stamps
	ID int64
}

func (*embedsPointer) Schema(*Schema) {}

func TestEmbeddedPointerIsRefused(t *testing.T) {
	_, err := infoOf[embedsPointer]([]string{"ID"}, nil)
	if err == nil || !strings.Contains(err.Error(), "embedded pointer") {
		t.Fatalf("got %v", err)
	}
}

func TestMetaOnceBuildsOnce(t *testing.T) {
	var o MetaOnce[other]
	calls := 0
	shape := func() Shape[other] {
		calls++
		return Shape[other]{Columns: []string{"ID"}, Addr: func(x *other, i int) any { return &x.ID }}
	}
	a, b := o.Get(shape), o.Get(shape)
	if calls != 1 || a != b || a.err != nil || a.info.Table != "other" {
		t.Fatalf("calls %d, same %v, meta %+v", calls, a == b, a)
	}
}

func TestRelationHolders(t *testing.T) {
	if _, loaded := (Many[other]{}).Get(); loaded {
		t.Error("a zero Many reports loaded")
	}
	if v, loaded := (One[other]{}).Get(); loaded || v != nil {
		t.Error("a zero One reports loaded")
	}
	if !isRelation(reflect.TypeFor[Many[other]]()) || isRelation(reflect.TypeFor[time.Time]()) {
		t.Error("isRelation")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: Schema`, `undefined: Many`, `undefined: buildInfo` and others.

- [ ] **Step 3: Write the implementation**

`relation.go`:

```go
package velox

// One holds a to-one relation, filled by With. Its zero value means not
// loaded. The value is held as any so that an entity's type does not reach
// the types of the entities it relates to: every package that uses an
// instantiated generic type compiles its type descriptor and everything the
// descriptor reaches (design §3 rule 2).
type One[T any] struct {
	v      any // *T
	loaded bool
}

// Get returns the related entity (nil when there is none) and whether the
// relation was loaded.
func (o One[T]) Get[_ Lazy[T]]() (*T, bool) {
	p, _ := o.v.(*T)
	return p, o.loaded
}

// Many holds a to-many relation, filled by With. Its zero value means not
// loaded; the value is held as any for the reason given on One.
type Many[T any] struct {
	v      any // []T
	loaded bool
}

// Get returns the related entities and whether the relation was loaded.
func (m Many[T]) Get[_ Lazy[T]]() ([]T, bool) {
	s, _ := m.v.([]T)
	return s, m.loaded
}
```

`schema.go`:

```go
package velox

import (
	"fmt"
	"reflect"
	"unsafe"
)

// Schemer is implemented by every entity: a struct with a Schema method on
// its pointer type.
type Schemer interface{ Schema(*Schema) }

// Mixer is implemented by a struct meant to be embedded in entities. velox
// calls Mixin on every Mixer embedded directly in an entity, before the
// entity's own Schema.
type Mixer interface{ Mixin(*Schema) }

type field struct {
	name string
	off  uintptr
	typ  reflect.Type
}

// Schema collects what an entity's Schema method declares. Fields are named
// by pointer; a pointer is matched to a column by its offset in the entity and
// its type.
type Schema struct {
	entity string
	base   unsafe.Pointer
	size   uintptr
	fields []field
	cols   []ColumnInfo
	table  string
	pk     []int
	tenant int
	idx    []IndexInfo
	errs   []error
}

func (s *Schema) fail(fieldName, format string, args ...any) {
	s.errs = append(s.errs, &SchemaError{Entity: s.entity, Field: fieldName, Reason: fmt.Sprintf(format, args...)})
}

// find returns the column that a pointer into the entity names, or -1 after
// recording an error.
func (s *Schema) find(p unsafe.Pointer, t reflect.Type) int {
	if uintptr(p) < uintptr(s.base) || uintptr(p)-uintptr(s.base) >= s.size {
		s.fail("", "a pointer given to Schema does not point into the entity")
		return -1
	}
	off := uintptr(p) - uintptr(s.base)
	for i, f := range s.fields {
		if f.off == off && f.typ == t {
			return i
		}
	}
	s.fail("", "the %v at offset %d is not a column (it is unexported, tagged velox:\"-\", or a relation)", t, off)
	return -1
}

func (s *Schema) findAny(p any) int {
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		s.fail("", "expected a pointer to a field, got %T", p)
		return -1
	}
	return s.find(v.UnsafePointer(), v.Type().Elem())
}

func (s *Schema) indexes(fields []any) []int {
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if i := s.findAny(f); i >= 0 {
			out = append(out, i)
		}
	}
	return out
}

// Table sets the table name. The default is the snake_case of the type name.
func (s *Schema) Table(name string) { s.table = name }

// Column returns the column that the field pointer p names, to declare its
// options.
func (s *Schema) Column[V any](p *V) *ColumnDef[V] {
	return &ColumnDef[V]{s: s, i: s.find(unsafe.Pointer(p), reflect.TypeFor[V]())}
}

// PrimaryKey declares the primary key. The default is the field named ID.
func (s *Schema) PrimaryKey(fields ...any) { s.pk = s.indexes(fields) }

// Unique declares a unique index over the fields.
func (s *Schema) Unique(fields ...any) {
	s.idx = append(s.idx, IndexInfo{Columns: s.indexes(fields), Unique: true})
}

// Index declares a non-unique index over the fields.
func (s *Schema) Index(fields ...any) {
	s.idx = append(s.idx, IndexInfo{Columns: s.indexes(fields)})
}

// Tenant declares the column that scopes every row to a tenant.
func (s *Schema) Tenant(field any) {
	if s.tenant >= 0 {
		s.fail("", "Tenant is declared twice")
		return
	}
	s.tenant = s.findAny(field)
}

// ColumnDef declares the options of one column.
type ColumnDef[V any] struct {
	s *Schema
	i int
}

// colInfo returns the column being declared, or a throwaway one when the
// field pointer was invalid (the error is already recorded).
func colInfo(s *Schema, i int) *ColumnInfo {
	if i < 0 {
		return &ColumnInfo{}
	}
	return &s.cols[i]
}

// Name sets the column name. The default is the snake_case of the field name.
func (c *ColumnDef[V]) Name[_ Lazy[V]](name string) *ColumnDef[V] {
	colInfo(c.s, c.i).Name = name
	return c
}

// SQLType sets the column's SQL type, for the migration planner. The default
// follows the field's Go type.
func (c *ColumnDef[V]) SQLType[_ Lazy[V]](t string) *ColumnDef[V] {
	colInfo(c.s, c.i).SQLType = t
	return c
}

// Default sets the column's default value.
func (c *ColumnDef[V]) Default[_ Lazy[V]](v V) *ColumnDef[V] {
	ci := colInfo(c.s, c.i)
	ci.Default, ci.HasDefault = v, true
	return c
}

// OnUpdate sets a function whose result every update writes to the column.
func (c *ColumnDef[V]) OnUpdate[_ Lazy[V]](f func() V) *ColumnDef[V] {
	colInfo(c.s, c.i).OnUpdate = func() any { return f() }
	return c
}
```

`meta.go`:

```go
package velox

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// EntityInfo is what velox knows about an entity, built once on its first
// use. It is the non-generic view that hooks see in a Statement.
type EntityInfo struct {
	Name       string // the Go type, e.g. "billing.Invoice"
	Table      string
	Columns    []ColumnInfo
	PrimaryKey []int // indexes into Columns
	Tenant     int   // index into Columns; -1 when the entity has none
	Indexes    []IndexInfo
}

// Column returns the index of the column with the given name.
func (e *EntityInfo) Column(name string) (int, bool) {
	for i := range e.Columns {
		if e.Columns[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

// ColumnInfo describes one column.
type ColumnInfo struct {
	Field      string       // the Go field name
	Name       string       // the column name
	Type       reflect.Type // the field's Go type
	Nullable   bool         // the field is a pointer
	SQLType    string       // empty: the dialect's default for Type
	Default    any
	HasDefault bool
	OnUpdate   func() any // nil when none
}

// IndexInfo describes one index.
type IndexInfo struct {
	Columns []int
	Unique  bool
}

// Shape is what velox-gen writes for an entity: the fields it saw and the
// scan plan.
type Shape[E any] struct {
	Columns   []string
	Relations []string
	Addr      func(x *E, i int) any
}

// Meta is an entity's metadata and scan plan.
type Meta[E any] struct {
	info *EntityInfo
	addr func(*E, int) any
	err  error
}

// MetaOnce builds an entity's Meta on first use. Its zero value is ready, so
// a generated file declares one without running anything at program start.
type MetaOnce[E any] struct {
	once sync.Once
	m    *Meta[E]
}

// Get returns the entity's Meta, building it from shape the first time.
func (o *MetaOnce[E]) Get[_ Lazy[E]](shape func() Shape[E]) *Meta[E] {
	o.once.Do(func() {
		sh := shape()
		x := new(E)
		info, err := buildInfo(reflect.TypeFor[E](), unsafe.Pointer(x), any(x), sh.Columns, sh.Relations)
		o.m = &Meta[E]{info: info, addr: sh.Addr, err: err}
	})
	return o.m
}

// Entity is the constraint velox's generic API puts on an entity type E: its
// pointer has the method velox-gen writes.
type Entity[E any] interface {
	*E
	VeloxMeta() *Meta[E]
}

var (
	veloxPkg  = reflect.TypeFor[Schema]().PkgPath()
	mixerType = reflect.TypeFor[Mixer]()
)

// isRelation reports a One or Many field. It goes by the type's name: a
// marker method would give every instantiation a method table, compiled into
// every package that uses it (design §3 rule 3).
func isRelation(t reflect.Type) bool {
	if t.PkgPath() != veloxPkg {
		return false
	}
	n := t.Name()
	return strings.HasPrefix(n, "One[") || strings.HasPrefix(n, "Many[")
}

// flatten lists an entity's columns and relations in declaration order,
// descending into embedded structs.
func flatten(t reflect.Type, base uintptr, cols []field, rels []string) ([]field, []string, error) {
	for i := range t.NumField() {
		f := t.Field(i)
		switch {
		case f.Tag.Get("velox") == "-":
		case f.Anonymous && f.Type.Kind() == reflect.Pointer:
			return nil, nil, fmt.Errorf("embedded pointer %s is not supported; embed the struct itself", f.Name)
		case f.Anonymous && f.Type.Kind() == reflect.Struct:
			var err error
			if cols, rels, err = flatten(f.Type, base+f.Offset, cols, rels); err != nil {
				return nil, nil, err
			}
		case !f.IsExported():
		case isRelation(f.Type):
			rels = append(rels, f.Name)
		default:
			cols = append(cols, field{name: f.Name, off: base + f.Offset, typ: f.Type})
		}
	}
	return cols, rels, nil
}

func firstDiff(got, want []string) string {
	for i := range max(len(got), len(want)) {
		g, w := "(none)", "(none)"
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			return fmt.Sprintf("#%d is %s in the struct and %s in the generated code", i, g, w)
		}
	}
	return ""
}

func checkShape(entity string, fs []field, rs, cols, rels []string) error {
	got := make([]string, len(fs))
	for i := range fs {
		got[i] = fs[i].name
	}
	if d := firstDiff(got, cols); d != "" {
		return fmt.Errorf("%w: %s: columns %s; run go tool velox-gen", ErrStale, entity, d)
	}
	if d := firstDiff(rs, rels); d != "" {
		return fmt.Errorf("%w: %s: relations %s; run go tool velox-gen", ErrStale, entity, d)
	}
	return nil
}

// applyMixins calls Mixin on every Mixer embedded directly in the entity.
func applyMixins(s *Schema, t reflect.Type, base unsafe.Pointer) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.Anonymous || f.Type.Kind() != reflect.Struct || f.Tag.Get("velox") == "-" {
			continue
		}
		if reflect.PointerTo(f.Type).Implements(mixerType) {
			reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Interface().(Mixer).Mixin(s)
		}
	}
}

// buildInfo builds an entity's metadata from its type, the column and
// relation names velox-gen saw (cols, rels), its mixins and its Schema
// method. x is a zero entity at base.
func buildInfo(t reflect.Type, base unsafe.Pointer, x any, cols, rels []string) (*EntityInfo, error) {
	name := t.String()
	fs, rs, err := flatten(t, 0, nil, nil)
	if err != nil {
		return nil, &SchemaError{Entity: name, Reason: err.Error()}
	}
	if err := checkShape(name, fs, rs, cols, rels); err != nil {
		return nil, err
	}
	s := &Schema{entity: name, base: base, size: t.Size(), fields: fs, tenant: -1}
	s.cols = make([]ColumnInfo, len(fs))
	for i, f := range fs {
		s.cols[i] = ColumnInfo{Field: f.name, Name: snake(f.name), Type: f.typ, Nullable: f.typ.Kind() == reflect.Pointer}
	}
	applyMixins(s, t, base)
	sc, ok := x.(Schemer)
	if !ok {
		return nil, &SchemaError{Entity: name, Reason: "has no Schema(*velox.Schema) method"}
	}
	sc.Schema(s)
	if len(s.errs) > 0 {
		return nil, errors.Join(s.errs...)
	}
	info := &EntityInfo{Name: name, Table: s.table, Columns: s.cols, PrimaryKey: s.pk, Tenant: s.tenant, Indexes: s.idx}
	if info.Table == "" {
		info.Table = snake(t.Name())
	}
	if len(info.PrimaryKey) == 0 {
		for i, f := range fs {
			if f.name == "ID" {
				info.PrimaryKey = []int{i}
			}
		}
		if len(info.PrimaryKey) == 0 {
			return nil, &SchemaError{Entity: name, Reason: "no primary key: name a field ID or call s.PrimaryKey"}
		}
	}
	return info, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./...`
Expected: `ok  	github.com/syssam/velox2`

- [ ] **Step 5: Commit**

```bash
git add schema.go meta.go relation.go meta_test.go
git commit -m "Build entity metadata from structs, Schema methods and mixins" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Conditions and column handles

**Files:**
- Create: `pred.go`, `internal/handlegen/handlegen.go`, `internal/handlegen/gen/main.go`, `handles.go` (generated)
- Test: `handles_test.go`, `methods_test.go`

**Interfaces:**
- Consumes: `Lazy` (Task 1).
- Produces:
  - `type node struct{ op op; col int32; v, w any; kids []node }` and the `op` constants `opEQ … opNot`
  - `type Pred[E any] struct{ n node }`, `type Predicate struct{ n node }`, `type Order[E any] struct{ t OrderTerm }`, `type OrderTerm struct{ Column int; Desc bool }`
  - `type Column[E any] interface{ column(*E) int32 }`
  - `func ColumnEQ(column int, v any) Predicate`
  - `func And[E any](ps ...Pred[E]) Pred[E]`, `func Or[E any](ps ...Pred[E]) Pred[E]`, `func Not[E any](p Pred[E]) Pred[E]`
  - `func list[V any](vs []V) []any`, `func escapeLike(s string) string`
  - Handle types `Col`, `OrderedCol`, `NumberCol`, `StringCol`, `NullCol`, `NullOrderedCol`, `NullNumberCol`, `NullStringCol`, each `struct{ I int32 }`. Methods: all have `EQ`, `NEQ`, `In`, `NotIn`, `Asc`, `Desc`. Ordered, number and string handles add `GT`, `GTE`, `LT`, `LTE`, `Between`. String handles add `Like`, `HasPrefix`, `HasSuffix`, `Contains`, `EqualFold`. `Null…` handles add `IsNull`, `NotNull`.
  - `func Generate() ([]byte, error)` in `internal/handlegen`

- [ ] **Step 1: Write the failing tests**

`handles_test.go`:

```go
package velox

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/syssam/velox2/internal/handlegen"
)

type ent struct{}

func TestHandleMethodsBuildNodes(t *testing.T) {
	n := NumberCol[ent, int64]{I: 3}
	s := StringCol[ent, string]{I: 4}
	ns := NullStringCol[ent, string]{I: 5}
	cases := []struct {
		name      string
		got, want node
	}{
		{"EQ", n.EQ(7).n, node{op: opEQ, col: 3, v: int64(7)}},
		{"In", n.In(1, 2).n, node{op: opIn, col: 3, v: []any{int64(1), int64(2)}}},
		{"Between", n.Between(1, 9).n, node{op: opBetween, col: 3, v: int64(1), w: int64(9)}},
		{"HasPrefix escapes", s.HasPrefix(`5%_\`).n, node{op: opLike, col: 4, v: `5\%\_\\%`}},
		{"HasSuffix", s.HasSuffix("x").n, node{op: opLike, col: 4, v: "%x"}},
		{"Contains", s.Contains("x").n, node{op: opLike, col: 4, v: "%x%"}},
		{"Like is raw", s.Like("a%").n, node{op: opLike, col: 4, v: "a%"}},
		{"EqualFold", s.EqualFold("Ab").n, node{op: opFold, col: 4, v: "Ab"}},
		{"IsNull", ns.IsNull().n, node{op: opIsNull, col: 5}},
		{"Not", Not(n.EQ(1)).n, node{op: opNot, kids: []node{{op: opEQ, col: 3, v: int64(1)}}}},
		{"Or", Or(n.EQ(1), n.EQ(2)).n, node{op: opOr, kids: []node{{op: opEQ, col: 3, v: int64(1)}, {op: opEQ, col: 3, v: int64(2)}}}},
		{"ColumnEQ", ColumnEQ(2, "t").n, node{op: opEQ, col: 2, v: "t"}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if o := n.Desc(); o.t != (OrderTerm{Column: 3, Desc: true}) {
		t.Errorf("Desc: %+v", o.t)
	}
	var c Column[ent] = n
	if got := c.column(nil); got != 3 {
		t.Errorf("column: %d", got)
	}
}

func TestHandlesFileIsCurrent(t *testing.T) {
	want, err := handlegen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("handles.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("handles.go is out of date: run go generate .")
	}
}
```

`methods_test.go`:

```go
package velox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestMethodsOfGenericTypesAreGeneric enforces design §3 rule 1: a method of
// a generic type is a generic method, compiled only where it is called. The
// one exception is column, which makes a handle a Column[E].
func TestMethodsOfGenericTypesAreGeneric(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil {
				continue
			}
			rt := fd.Recv.List[0].Type
			if s, ok := rt.(*ast.StarExpr); ok {
				rt = s.X
			}
			switch rt.(type) {
			case *ast.IndexExpr, *ast.IndexListExpr:
			default:
				continue
			}
			if fd.Name.Name == "column" {
				continue
			}
			if fd.Type.TypeParams == nil || fd.Type.TypeParams.NumFields() == 0 {
				t.Errorf("%s: method %s of a generic type is not a generic method; add [_ Lazy[...]] (design §3 rule 1)",
					fset.Position(fd.Pos()), fd.Name.Name)
			}
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: NumberCol`, and `package github.com/syssam/velox2/internal/handlegen is not in std` (or "no required module provides package").

- [ ] **Step 3: Write `pred.go`**

```go
package velox

import "strings"

type op uint8

const (
	opEQ op = iota + 1
	opNEQ
	opIn
	opNotIn
	opGT
	opGTE
	opLT
	opLTE
	opBetween
	opLike
	opFold
	opIsNull
	opNotNull
	opAnd
	opOr
	opNot
)

// node is a condition, typed by nothing: the generic API wraps it.
type node struct {
	op   op
	col  int32
	v, w any
	kids []node
}

// Pred is a condition on entity E, made by a column handle or by And, Or and
// Not.
type Pred[E any] struct{ n node }

// Predicate is a condition in a Statement: the form hooks see and add.
type Predicate struct{ n node }

// Order is an ordering by one column of entity E, made by a handle's Asc or
// Desc.
type Order[E any] struct{ t OrderTerm }

// OrderTerm is an ordering by one column of a Statement's entity.
type OrderTerm struct {
	Column int // index into EntityInfo.Columns
	Desc   bool
}

// Column is any column handle of entity E, for Columns and Omit.
type Column[E any] interface{ column(*E) int32 }

// ColumnEQ returns the condition column = v, for hooks that add conditions;
// column is an index into the statement's EntityInfo.Columns.
func ColumnEQ(column int, v any) Predicate {
	return Predicate{node{op: opEQ, col: int32(column), v: v}}
}

// And is true when every p is. And of nothing is true.
func And[E any](ps ...Pred[E]) Pred[E] { return Pred[E]{node{op: opAnd, kids: nodes(ps)}} }

// Or is true when any p is. Or of nothing is false.
func Or[E any](ps ...Pred[E]) Pred[E] { return Pred[E]{node{op: opOr, kids: nodes(ps)}} }

// Not is true when p is false.
func Not[E any](p Pred[E]) Pred[E] { return Pred[E]{node{op: opNot, kids: []node{p.n}}} }

func nodes[E any](ps []Pred[E]) []node {
	out := make([]node, len(ps))
	for i := range ps {
		out[i] = ps[i].n
	}
	return out
}

func list[V any](vs []V) []any {
	out := make([]any, len(vs))
	for i := range vs {
		out[i] = vs[i]
	}
	return out
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike makes s match itself in a LIKE pattern whose escape is \.
func escapeLike(s string) string { return likeEscaper.Replace(s) }
```

- [ ] **Step 4: Write the handle generator**

`internal/handlegen/handlegen.go`:

```go
// Package handlegen writes velox's handles.go: the column handle types and
// their methods. go generate runs it in the module root, and a test in package
// velox fails when handles.go differs from its output.
package handlegen

import (
	"bytes"
	"fmt"
	"go/format"
)

type method struct{ name, doc, sig, body string }

func cmp(name, op, doc string) method {
	return method{name, doc, "(v V) Pred[E]", "return Pred[E]{node{op: " + op + ", col: c.I, v: v}}"}
}

const caseNote = " Whether case is ignored follows the database:" +
	" PostgreSQL's LIKE does not ignore it; MySQL's default collations and SQLite (for ASCII) do."

var (
	base = []method{
		cmp("EQ", "opEQ", "is column = v."),
		cmp("NEQ", "opNEQ", "is column <> v."),
		{"In", "is column IN (vs...). With no values it is false.", "(vs ...V) Pred[E]",
			"return Pred[E]{node{op: opIn, col: c.I, v: list(vs)}}"},
		{"NotIn", "is column NOT IN (vs...). With no values it is true.", "(vs ...V) Pred[E]",
			"return Pred[E]{node{op: opNotIn, col: c.I, v: list(vs)}}"},
		{"Asc", "orders by the column, ascending.", "() Order[E]",
			"return Order[E]{OrderTerm{Column: int(c.I)}}"},
		{"Desc", "orders by the column, descending.", "() Order[E]",
			"return Order[E]{OrderTerm{Column: int(c.I), Desc: true}}"},
	}
	ordered = []method{
		cmp("GT", "opGT", "is column > v."),
		cmp("GTE", "opGTE", "is column >= v."),
		cmp("LT", "opLT", "is column < v."),
		cmp("LTE", "opLTE", "is column <= v."),
		{"Between", "is column BETWEEN lo AND hi.", "(lo, hi V) Pred[E]",
			"return Pred[E]{node{op: opBetween, col: c.I, v: lo, w: hi}}"},
	}
	text = []method{
		{"Like", "is column LIKE pattern: % and _ are wildcards and \\ escapes." + caseNote, "(pattern string) Pred[E]",
			"return Pred[E]{node{op: opLike, col: c.I, v: pattern}}"},
		{"HasPrefix", "is true when the column starts with s, taken literally." + caseNote, "(s V) Pred[E]",
			`return Pred[E]{node{op: opLike, col: c.I, v: escapeLike(string(s)) + "%"}}`},
		{"HasSuffix", "is true when the column ends with s, taken literally." + caseNote, "(s V) Pred[E]",
			`return Pred[E]{node{op: opLike, col: c.I, v: "%" + escapeLike(string(s))}}`},
		{"Contains", "is true when the column contains s, taken literally." + caseNote, "(s V) Pred[E]",
			`return Pred[E]{node{op: opLike, col: c.I, v: "%" + escapeLike(string(s)) + "%"}}`},
		{"EqualFold", "is LOWER(column) = LOWER(s).", "(s V) Pred[E]",
			"return Pred[E]{node{op: opFold, col: c.I, v: string(s)}}"},
	}
	null = []method{
		{"IsNull", "is column IS NULL.", "() Pred[E]", "return Pred[E]{node{op: opIsNull, col: c.I}}"},
		{"NotNull", "is column IS NOT NULL.", "() Pred[E]", "return Pred[E]{node{op: opNotNull, col: c.I}}"},
	}
)

type kind struct {
	name, tparams, doc string
	methods            [][]method
}

var kinds = []kind{
	{"Col", "[E, V any]", "is the handle of a column of entity E whose values, of type V, are neither ordered nor text: bool, []byte and other types.", [][]method{base}},
	{"OrderedCol", "[E, V any]", "is the handle of an ordered column: time.Time and types registered as ordered.", [][]method{base, ordered}},
	{"NumberCol", "[E, V any]", "is the handle of a numeric column.", [][]method{base, ordered}},
	{"StringCol", "[E any, V ~string]", "is the handle of a text column.", [][]method{base, ordered, text}},
	{"NullCol", "[E, V any]", "is Col for a column that allows NULL: a field of type *V.", [][]method{base, null}},
	{"NullOrderedCol", "[E, V any]", "is OrderedCol for a column that allows NULL: a field of type *V.", [][]method{base, ordered, null}},
	{"NullNumberCol", "[E, V any]", "is NumberCol for a column that allows NULL: a field of type *V.", [][]method{base, ordered, null}},
	{"NullStringCol", "[E any, V ~string]", "is StringCol for a column that allows NULL: a field of type *V.", [][]method{base, ordered, text, null}},
}

// Generate returns the source of handles.go.
func Generate() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/handlegen. DO NOT EDIT.\n\npackage velox\n")
	for _, k := range kinds {
		fmt.Fprintf(&b, "\n// %s %s I is the column's index; velox-gen sets it.\ntype %s%s struct{ I int32 }\n", k.name, k.doc, k.name, k.tparams)
		fmt.Fprintf(&b, "\nfunc (c %s[E, V]) column(*E) int32 { return c.I }\n", k.name)
		for _, ms := range k.methods {
			for _, m := range ms {
				fmt.Fprintf(&b, "\n// %s %s\nfunc (c %s[E, V]) %s[_ Lazy[V]]%s { %s }\n", m.name, m.doc, k.name, m.name, m.sig, m.body)
			}
		}
	}
	return format.Source(b.Bytes())
}
```

`internal/handlegen/gen/main.go`:

```go
// Command gen writes handles.go. go generate runs it in the module root.
package main

import (
	"log"
	"os"

	"github.com/syssam/velox2/internal/handlegen"
)

func main() {
	src, err := handlegen.Generate()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("handles.go", src, 0o644); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 5: Generate `handles.go`**

Run: `go generate . && head -12 handles.go && grep -c '^func ' handles.go`
Expected: the file begins `// Code generated by internal/handlegen. DO NOT EDIT.` followed by `package velox` and `// Col is the handle of a column of entity E…`. The count is `104`: 8 `column` methods and 96 others (Col 6, OrderedCol 11, NumberCol 11, StringCol 16, NullCol 8, NullOrderedCol 13, NullNumberCol 13, NullStringCol 18).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: `ok  	github.com/syssam/velox2`, and vet prints nothing.

- [ ] **Step 7: Commit**

```bash
git add pred.go handles.go handles_test.go methods_test.go internal/handlegen
git commit -m "Add conditions and the generated column handle types" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Dialects and SELECT rendering

**Files:**
- Create: `dialect.go`, `statement.go`, `render.go`
- Test: `render_test.go`

**Interfaces:**
- Consumes: `node`, `Predicate`, `OrderTerm`, the handle types, `And`/`Or`/`Not` (Task 3); `EntityInfo`, `ColumnInfo` (Task 2).
- Produces:
  - `type Dialect uint8` with constants `Postgres`, `MySQL`, `SQLite` and `String() string`
  - `type Op uint8` with constants `OpSelect`, `OpInsert`, `OpUpdate`, `OpDelete`
  - `type Statement struct{ Op Op; Entity *EntityInfo; Count bool; Columns []int; Where []Predicate; OrderBy []OrderTerm; Limit, Offset int; Unbounded, InTx bool; SQL string; Args []any }`
  - `type Rewrite func(ctx context.Context, st *Statement) error`
  - `type Around func(ctx context.Context, st *Statement, next func(context.Context) error) error`
  - `func render(d Dialect, st *Statement) (string, []any)`
  - `type writer struct{…}` with `selectStmt(st *Statement)` (Task 7 reuses it with `argsOnly`)
  - Test helpers used by Task 7: `type inv struct{}`, `invInfo`, `hID`, `hOrg`, `hNumber`, `hTotal`, `hPaidAt`, `func stmt(where ...Pred[inv]) *Statement`, `func renderCases() []renderCase`, `func argsEqual(a, b []any) bool`, `const pgCols`

- [ ] **Step 1: Write the failing tests**

`render_test.go`:

```go
package velox

import (
	"reflect"
	"testing"
	"time"
)

type inv struct{}

var (
	invInfo = &EntityInfo{Name: "velox.inv", Table: "invoices", Tenant: -1, Columns: []ColumnInfo{
		{Name: "id"}, {Name: "org_id"}, {Name: "number"}, {Name: "total"}, {Name: "paid_at"},
	}}
	hID     = NumberCol[inv, int64]{I: 0}
	hOrg    = NumberCol[inv, int64]{I: 1}
	hNumber = StringCol[inv, string]{I: 2}
	hTotal  = NumberCol[inv, float64]{I: 3}
	hPaidAt = NullOrderedCol[inv, time.Time]{I: 4}
)

const (
	pgCols    = `"id", "org_id", "number", "total", "paid_at"`
	mysqlCols = "`id`, `org_id`, `number`, `total`, `paid_at`"
)

func stmt(where ...Pred[inv]) *Statement {
	st := &Statement{Op: OpSelect, Entity: invInfo}
	for _, p := range where {
		st.Where = append(st.Where, Predicate{p.n})
	}
	return st
}

type renderCase struct {
	name string
	st   func() *Statement
	sql  map[Dialect]string
	args []any
}

func renderCases() []renderCase {
	return []renderCase{
		{"every row", func() *Statement { return stmt() }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices"`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices`",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices"`,
		}, nil},
		{"where, order, limit", func() *Statement {
			st := stmt(hOrg.EQ(7), hTotal.GT(10))
			st.OrderBy = []OrderTerm{{Column: 4, Desc: true}, {Column: 0}}
			st.Limit = 50
			return st
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND "total" > $2 ORDER BY "paid_at" DESC, "id" LIMIT $3`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` = ? AND `total` > ? ORDER BY `paid_at` DESC, `id` LIMIT ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = ? AND "total" > ? ORDER BY "paid_at" DESC, "id" LIMIT ?`,
		}, []any{int64(7), float64(10), 50}},
		{"in and not in", func() *Statement { return stmt(hID.In(1, 2, 3), hNumber.NotIn("x")) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" IN ($1, $2, $3) AND "number" NOT IN ($4)`,
		}, []any{int64(1), int64(2), int64(3), "x"}},
		{"empty in and not in", func() *Statement { return stmt(hID.In(), hID.NotIn()) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 0 AND 1 = 1`,
		}, nil},
		{"like escape clause", func() *Statement { return stmt(hNumber.HasPrefix("a")) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE $1 ESCAPE '\'`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `number` LIKE ? ESCAPE '\\\\'",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE ? ESCAPE '\'`,
		}, []any{"a%"}},
		{"and, or, not", func() *Statement {
			return stmt(Or(hID.EQ(1), And(hOrg.EQ(2), Not(hPaidAt.IsNull()))))
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = $1 OR ("org_id" = $2 AND NOT ("paid_at" IS NULL)))`,
		}, []any{int64(1), int64(2)}},
		{"empty and, empty or", func() *Statement { return stmt(And[inv](), Or[inv]()) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 1 AND 1 = 0`,
		}, nil},
		{"between, equal fold, not null", func() *Statement {
			return stmt(hTotal.Between(1, 2), hNumber.EqualFold("Ab"), hPaidAt.NotNull())
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "total" BETWEEN $1 AND $2 AND LOWER("number") = LOWER($3) AND "paid_at" IS NOT NULL`,
		}, []any{float64(1), float64(2), "Ab"}},
		{"count", func() *Statement { st := stmt(hOrg.EQ(1)); st.Count = true; return st }, map[Dialect]string{
			Postgres: `SELECT COUNT(*) FROM "invoices" WHERE "org_id" = $1`,
		}, []any{int64(1)}},
		{"offset without limit", func() *Statement { st := stmt(); st.Offset = 20; return st }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" OFFSET $1`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` LIMIT 18446744073709551615 OFFSET ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" LIMIT -1 OFFSET ?`,
		}, []any{20}},
		{"some columns, limit and offset", func() *Statement {
			st := stmt()
			st.Columns, st.Limit, st.Offset = []int{2, 0}, 1, 5
			return st
		}, map[Dialect]string{
			Postgres: `SELECT "number", "id" FROM "invoices" LIMIT $1 OFFSET $2`,
		}, []any{1, 5}},
	}
}

func argsEqual(a, b []any) bool { return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b)) }

func TestRender(t *testing.T) {
	for _, c := range renderCases() {
		for d, want := range c.sql {
			got, args := render(d, c.st())
			if got != want {
				t.Errorf("%s, %v:\n got %s\nwant %s", c.name, d, got, want)
			}
			if !argsEqual(args, c.args) {
				t.Errorf("%s, %v: args %#v, want %#v", c.name, d, args, c.args)
			}
		}
	}
}

func TestRenderQuotesIdentifiers(t *testing.T) {
	info := &EntityInfo{Name: "velox.weird", Table: "user", Tenant: -1, Columns: []ColumnInfo{{Name: "order"}, {Name: "a`b\"c"}}}
	st := &Statement{Op: OpSelect, Entity: info}
	for d, want := range map[Dialect]string{
		Postgres: `SELECT "order", "a` + "`" + `b""c" FROM "user"`,
		MySQL:    "SELECT `order`, `a``b\"c` FROM `user`",
		SQLite:   `SELECT "order", "a` + "`" + `b""c" FROM "user"`,
	} {
		if got, _ := render(d, st); got != want {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
	}
}

func TestDialectString(t *testing.T) {
	if Postgres.String() != "postgres" || MySQL.String() != "mysql" || SQLite.String() != "sqlite" {
		t.Error(Postgres, MySQL, SQLite)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: Statement`, `undefined: render`, `undefined: Postgres`.

- [ ] **Step 3: Write the implementation**

`dialect.go`:

```go
package velox

import (
	"strconv"
	"strings"
)

// Dialect is a SQL dialect.
type Dialect uint8

// The dialects velox supports.
const (
	Postgres Dialect = iota + 1
	MySQL
	SQLite
)

func (d Dialect) String() string {
	switch d {
	case Postgres:
		return "postgres"
	case MySQL:
		return "mysql"
	case SQLite:
		return "sqlite"
	}
	return "Dialect(" + strconv.Itoa(int(d)) + ")"
}

// quote writes ident quoted, with the quote character doubled inside it.
func (d Dialect) quote(b *strings.Builder, ident string) {
	q := byte('"')
	if d == MySQL {
		q = '`'
	}
	b.WriteByte(q)
	for i := range len(ident) {
		if ident[i] == q {
			b.WriteByte(q)
		}
		b.WriteByte(ident[i])
	}
	b.WriteByte(q)
}

// placeholder writes the n-th (from 1) parameter placeholder.
func (d Dialect) placeholder(b *strings.Builder, n int) {
	if d == Postgres {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
		return
	}
	b.WriteByte('?')
}

// likeEscape is the ESCAPE clause that makes \ the LIKE escape character. In
// MySQL a backslash in a string literal is itself escaped.
func (d Dialect) likeEscape() string {
	if d == MySQL {
		return ` ESCAPE '\\'`
	}
	return ` ESCAPE '\'`
}

// noLimit is what goes after LIMIT when a query has an offset and no limit:
// MySQL and SQLite accept OFFSET only after LIMIT.
func (d Dialect) noLimit() string {
	if d == MySQL {
		return "18446744073709551615"
	}
	return "-1"
}
```

`statement.go`:

```go
package velox

import "context"

// Op is the kind of a Statement.
type Op uint8

// The kinds of statement.
const (
	OpSelect Op = iota + 1
	OpInsert
	OpUpdate
	OpDelete
)

// Statement is one operation on its way to the database. Every query becomes
// one, and the pipeline's hooks see and may change it: rewrite hooks before it
// is rendered, around hooks while it runs (design §5).
type Statement struct {
	Op        Op
	Entity    *EntityInfo
	Count     bool        // SELECT COUNT(*) instead of columns
	Columns   []int       // the selected columns; nil selects all
	Where     []Predicate // all must hold
	OrderBy   []OrderTerm
	Limit     int // 0: no limit
	Offset    int
	Unbounded bool // the query said Unbounded
	InTx      bool
	SQL       string // set by rendering, after the rewrite hooks
	Args      []any  // set by rendering
}

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open.
type Rewrite func(ctx context.Context, st *Statement) error

// Around is a hook that wraps a statement's execution. It must call next
// exactly once, unless it returns an error instead. The first Around given to
// Open is the outermost.
type Around func(ctx context.Context, st *Statement, next func(context.Context) error) error
```

`render.go`:

```go
package velox

import "strings"

// writer renders a statement. With argsOnly it collects the arguments and
// writes no text: the text is cached (render_cache.go), and one code path for
// both keeps the arguments in the order of the placeholders.
type writer struct {
	d        Dialect
	in       *EntityInfo
	b        strings.Builder
	args     []any
	argsOnly bool
}

func (w *writer) str(s string) {
	if !w.argsOnly {
		w.b.WriteString(s)
	}
}

func (w *writer) ident(s string) {
	if !w.argsOnly {
		w.d.quote(&w.b, s)
	}
}

func (w *writer) col(i int32) { w.ident(w.in.Columns[i].Name) }

func (w *writer) arg(v any) {
	w.args = append(w.args, v)
	if !w.argsOnly {
		w.d.placeholder(&w.b, len(w.args))
	}
}

var cmpText = [...]string{opEQ: " = ", opNEQ: " <> ", opGT: " > ", opGTE: " >= ", opLT: " < ", opLTE: " <= "}

func (w *writer) selectStmt(st *Statement) {
	w.str("SELECT ")
	switch {
	case st.Count:
		w.str("COUNT(*)")
	case st.Columns == nil:
		for i := range w.in.Columns {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(i))
		}
	default:
		for i, c := range st.Columns {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(c))
		}
	}
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
	for i, o := range st.OrderBy {
		if i == 0 {
			w.str(" ORDER BY ")
		} else {
			w.str(", ")
		}
		w.col(int32(o.Column))
		if o.Desc {
			w.str(" DESC")
		}
	}
	if st.Limit > 0 {
		w.str(" LIMIT ")
		w.arg(st.Limit)
	}
	if st.Offset > 0 {
		if st.Limit == 0 && w.d != Postgres {
			w.str(" LIMIT ")
			w.str(w.d.noLimit())
		}
		w.str(" OFFSET ")
		w.arg(st.Offset)
	}
}

func (w *writer) pred(n node) {
	switch n.op {
	case opEQ, opNEQ, opGT, opGTE, opLT, opLTE:
		w.col(n.col)
		w.str(cmpText[n.op])
		w.arg(n.v)
	case opIn, opNotIn:
		vs := n.v.([]any)
		if len(vs) == 0 {
			if n.op == opIn {
				w.str("1 = 0")
			} else {
				w.str("1 = 1")
			}
			return
		}
		w.col(n.col)
		if n.op == opNotIn {
			w.str(" NOT IN (")
		} else {
			w.str(" IN (")
		}
		for i, v := range vs {
			if i > 0 {
				w.str(", ")
			}
			w.arg(v)
		}
		w.str(")")
	case opBetween:
		w.col(n.col)
		w.str(" BETWEEN ")
		w.arg(n.v)
		w.str(" AND ")
		w.arg(n.w)
	case opLike:
		w.col(n.col)
		w.str(" LIKE ")
		w.arg(n.v)
		w.str(w.d.likeEscape())
	case opFold:
		w.str("LOWER(")
		w.col(n.col)
		w.str(") = LOWER(")
		w.arg(n.v)
		w.str(")")
	case opIsNull:
		w.col(n.col)
		w.str(" IS NULL")
	case opNotNull:
		w.col(n.col)
		w.str(" IS NOT NULL")
	case opAnd, opOr:
		if len(n.kids) == 0 {
			if n.op == opAnd {
				w.str("1 = 1")
			} else {
				w.str("1 = 0")
			}
			return
		}
		w.str("(")
		for i, k := range n.kids {
			if i > 0 {
				if n.op == opAnd {
					w.str(" AND ")
				} else {
					w.str(" OR ")
				}
			}
			w.pred(k)
		}
		w.str(")")
	case opNot:
		w.str("NOT (")
		w.pred(n.kids[0])
		w.str(")")
	}
}

// render returns a statement's SQL and arguments. Only SELECT exists until
// the writes plan.
func render(d Dialect, st *Statement) (string, []any) {
	if st.Op != OpSelect {
		panic("velox: render: only SELECT is implemented")
	}
	w := writer{d: d, in: st.Entity}
	w.selectStmt(st)
	return w.b.String(), w.args
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: `ok  	github.com/syssam/velox2`

- [ ] **Step 5: Commit**

```bash
git add dialect.go statement.go render.go render_test.go
git commit -m "Render SELECT statements for PostgreSQL, MySQL and SQLite" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: DB, executors, pipeline and the query API

**Files:**
- Create: `executor.go`, `db.go`, `query.go`, `export_test.go`, `internal/testmodel/invoice.go`, `internal/testmodel/invoice_velox.go`
- Test: `query_test.go`, `pipeline_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–4.
- Produces:
  - `type rows interface{ Next() bool; Scan(dst ...any) error; Err() error; Close() }`, `type executor interface{ query(ctx context.Context, sql string, args []any) (rows, error) }`
  - `type DB struct{…}`, `func Open(d Dialect, db *sql.DB, opts ...Option) *DB`, `func OpenPgx(pool *pgxpool.Pool, opts ...Option) *DB`, `func (db *DB) Dialect() Dialect`, `type Option func(*config)`, `func WithRewrite(Rewrite) Option`, `func WithAround(Around) Option`
  - `func newDB(d Dialect, ex executor, opts []Option) *DB`, `func (db *DB) run(ctx context.Context, st *Statement) (rows, error)`, `type config struct{ rewrites []Rewrite; arounds []Around }` (Task 7 adds `cache renderCache`)
  - `func (db *DB) Query[E any, PE Entity[E]]() *Query[E]` and on `*Query[E]`: `Where`, `OrderBy`, `Limit`, `Offset`, `Unbounded`, `Columns`, `Omit` (each returns `*Query[E]`), `All(ctx) ([]E, error)`, `One(ctx) (*E, error)`, `Iter(ctx) iter.Seq2[E, error]`, `Count(ctx) (int64, error)`, `SQL() (string, []any)`
  - Test-only (`export_test.go`): `type FakeExec struct{ Rows [][]any; SQL string; Args []any; Calls int }`, `func OpenFake(d Dialect, e *FakeExec, opts ...Option) *DB`, `func RawRows(db *DB, sql string, args []any) (interface{ Next() bool; Scan(...any) error; Close() }, error)`
  - `internal/testmodel`: `type Timestamps`, `type Invoice` (columns, in order: CreatedAt, UpdatedAt, ID, OrgID, Number, Total, Paid, PaidAt, Note, Data), `var Invoices` handles

- [ ] **Step 1: Add the pgx dependency**

Run: `go get github.com/jackc/pgx/v5@v5.11.0`
Expected: `go: added github.com/jackc/pgx/v5 v5.11.0`

- [ ] **Step 2: Write the test entity**

These two files are written by hand in exactly the form velox-gen will write (spec §2). Plan 2's golden tests compare velox-gen's output with `invoice_velox.go` byte for byte.

`internal/testmodel/invoice.go`:

```go
// Package testmodel holds entities for velox's own tests. Its *_velox.go
// files are written by hand in exactly the form velox-gen writes them.
package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Timestamps is a mixin: embedded in an entity, its fields are columns.
type Timestamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Mixin makes every update set UpdatedAt.
func (t *Timestamps) Mixin(s *velox.Schema) {
	s.Column(&t.UpdatedAt).OnUpdate(time.Now)
}

// Invoice is the entity velox's tests query.
type Invoice struct {
	Timestamps
	ID      int64
	OrgID   int64
	Number  string
	Total   float64
	Paid    bool
	PaidAt  *time.Time
	Note    *string
	Data    []byte
	Display string `velox:"-"`
}

// Schema declares Invoice's table, unique key and tenant column.
func (x *Invoice) Schema(s *velox.Schema) {
	s.Table("invoices")
	s.Unique(&x.OrgID, &x.Number)
	s.Tenant(&x.OrgID)
}
```

`internal/testmodel/invoice_velox.go`:

```go
// Code generated by velox-gen. DO NOT EDIT.

package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Fails to compile if this file and the velox library disagree.
const _ = velox.GeneratedCodeIsVersion1

// Invoices holds the typed handles of Invoice's columns and relations.
var Invoices = struct {
	CreatedAt velox.OrderedCol[Invoice, time.Time]
	UpdatedAt velox.OrderedCol[Invoice, time.Time]
	ID        velox.NumberCol[Invoice, int64]
	OrgID     velox.NumberCol[Invoice, int64]
	Number    velox.StringCol[Invoice, string]
	Total     velox.NumberCol[Invoice, float64]
	Paid      velox.Col[Invoice, bool]
	PaidAt    velox.NullOrderedCol[Invoice, time.Time]
	Note      velox.NullStringCol[Invoice, string]
	Data      velox.Col[Invoice, []byte]
}{
	CreatedAt: velox.OrderedCol[Invoice, time.Time]{I: 0},
	UpdatedAt: velox.OrderedCol[Invoice, time.Time]{I: 1},
	ID:        velox.NumberCol[Invoice, int64]{I: 2},
	OrgID:     velox.NumberCol[Invoice, int64]{I: 3},
	Number:    velox.StringCol[Invoice, string]{I: 4},
	Total:     velox.NumberCol[Invoice, float64]{I: 5},
	Paid:      velox.Col[Invoice, bool]{I: 6},
	PaidAt:    velox.NullOrderedCol[Invoice, time.Time]{I: 7},
	Note:      velox.NullStringCol[Invoice, string]{I: 8},
	Data:      velox.Col[Invoice, []byte]{I: 9},
}

var invoiceMeta velox.MetaOnce[Invoice]

// VeloxMeta is called by velox, not by applications.
func (*Invoice) VeloxMeta() *velox.Meta[Invoice] { return invoiceMeta.Get(invoiceShape) }

func invoiceShape() velox.Shape[Invoice] {
	return velox.Shape[Invoice]{
		Columns: []string{"CreatedAt", "UpdatedAt", "ID", "OrgID", "Number", "Total", "Paid", "PaidAt", "Note", "Data"},
		Addr: func(x *Invoice, i int) any {
			switch i {
			case 0:
				return &x.CreatedAt
			case 1:
				return &x.UpdatedAt
			case 2:
				return &x.ID
			case 3:
				return &x.OrgID
			case 4:
				return &x.Number
			case 5:
				return &x.Total
			case 6:
				return &x.Paid
			case 7:
				return &x.PaidAt
			case 8:
				return &x.Note
			case 9:
				return &x.Data
			}
			return nil
		},
	}
}
```

- [ ] **Step 3: Write the test-only exports**

`export_test.go`:

```go
package velox

import (
	"context"
	"fmt"
	"time"
)

// FakeExec is an executor that returns Rows for every query and records what
// it was sent.
type FakeExec struct {
	Rows  [][]any
	SQL   string
	Args  []any
	Calls int
}

func (e *FakeExec) query(_ context.Context, sql string, args []any) (rows, error) {
	e.SQL, e.Args = sql, args
	e.Calls++
	return &fakeRows{data: e.Rows}, nil
}

// OpenFake returns a DB of dialect d that runs statements on e.
func OpenFake(d Dialect, e *FakeExec, opts ...Option) *DB { return newDB(d, e, opts) }

// RawRows runs sql on db's executor, bypassing the pipeline: the baseline of
// hand-written code.
func RawRows(db *DB, sql string, args []any) (interface {
	Next() bool
	Scan(...any) error
	Close()
}, error) {
	return db.ex.query(context.Background(), sql, args)
}

type fakeRows struct {
	data [][]any
	i    int
}

func (r *fakeRows) Next() bool { r.i++; return r.i <= len(r.data) }
func (r *fakeRows) Err() error { return nil }
func (r *fakeRows) Close()     {}

func (r *fakeRows) Scan(dst ...any) error {
	row := r.data[r.i-1]
	if len(dst) != len(row) {
		return fmt.Errorf("fake: %d destinations for %d columns", len(dst), len(row))
	}
	for k, d := range dst {
		if err := assign(d, row[k]); err != nil {
			return err
		}
	}
	return nil
}

func assign(dst, src any) error {
	switch d := dst.(type) {
	case *int64:
		*d = src.(int64)
	case *string:
		*d = src.(string)
	case *float64:
		*d = src.(float64)
	case *bool:
		*d = src.(bool)
	case *[]byte:
		*d = src.([]byte)
	case *time.Time:
		*d = src.(time.Time)
	case **time.Time:
		if src == nil {
			*d = nil
		} else {
			v := src.(time.Time)
			*d = &v
		}
	case **string:
		if src == nil {
			*d = nil
		} else {
			v := src.(string)
			*d = &v
		}
	default:
		return fmt.Errorf("fake: cannot scan into %T", dst)
	}
	return nil
}
```

- [ ] **Step 4: Write the failing tests**

`query_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	I   = testmodel.Invoices
)

const allCols = `"created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "note", "data"`

func invoiceRow(id int64) []any {
	return []any{t0, t0, id, int64(1), fmt.Sprintf("INV-%d", id), 12.5, true, t0, nil, []byte("d")}
}

func TestAllScansEveryColumn(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2)}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != 2 || got[1].Number != "INV-2" || got[1].PaidAt == nil ||
		!got[1].PaidAt.Equal(t0) || got[1].Note != nil || string(got[1].Data) != "d" || !got[1].CreatedAt.Equal(t0) {
		t.Fatalf("%+v", got)
	}
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE "org_id" = $1 ORDER BY "id" LIMIT $2`
	if e.SQL != want {
		t.Errorf("SQL\n got %s\nwant %s", e.SQL, want)
	}
	if !reflect.DeepEqual(e.Args, []any{int64(1), 10}) {
		t.Errorf("args %#v", e.Args)
	}
}

func TestOne(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(3)}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(3)).One(ctx)
	if err != nil || got.ID != 3 {
		t.Fatalf("%+v, %v", got, err)
	}
	if !strings.HasSuffix(e.SQL, `WHERE "id" = $1 LIMIT $2`) || e.Args[1] != 2 {
		t.Errorf("One must ask for 2 rows: %s %v", e.SQL, e.Args)
	}
	e.Rows = nil
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNotFound) || !strings.Contains(err.Error(), "testmodel.Invoice") {
		t.Errorf("no rows: %v", err)
	}
	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("two rows: %v", err)
	}
}

func TestColumnsAndOmit(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5), "INV-5"}}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Columns(I.ID, I.Number).All(ctx)
	if err != nil || len(got) != 1 || got[0].ID != 5 || got[0].Number != "INV-5" || got[0].Total != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "id", "number" FROM "invoices"`; e.SQL != want {
		t.Errorf("Columns SQL %s", e.SQL)
	}
	row := invoiceRow(6)
	e.Rows = [][]any{append(row[:8:8], row[9])}
	got, err = db.Query[testmodel.Invoice]().Omit(I.Note).All(ctx)
	if err != nil || got[0].ID != 6 || got[0].Note != nil {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "data" FROM "invoices"`; e.SQL != want {
		t.Errorf("Omit SQL %s", e.SQL)
	}
}

func TestCount(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e)
	n, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(5).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("%d, %v", n, err)
	}
	if want := `SELECT COUNT(*) FROM "invoices" WHERE "paid" = $1`; e.SQL != want {
		t.Errorf("SQL %s", e.SQL)
	}
}

func TestIterStopsEarly(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
	db := velox.OpenFake(velox.Postgres, e)
	var seen []int64
	for inv, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, inv.ID)
		if len(seen) == 2 {
			break
		}
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Errorf("%v", seen)
	}
}

func TestSQLNeedsNoDatabase(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.MySQL, e)
	text, args := db.Query[testmodel.Invoice]().Where(I.Number.HasPrefix("A")).Limit(3).SQL()
	want := "SELECT `created_at`, `updated_at`, `id`, `org_id`, `number`, `total`, `paid`, `paid_at`, `note`, `data` FROM `invoices` WHERE `number` LIKE ? ESCAPE '\\\\' LIMIT ?"
	if text != want || !reflect.DeepEqual(args, []any{"A%", 3}) || e.Calls != 0 {
		t.Errorf("%s %v calls %d", text, args, e.Calls)
	}
}

// TestQueryCanRunAgain is Review Focus 4.
func TestQueryCanRunAgain(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	tenant := func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(st.Entity.Tenant, int64(7)))
		return nil
	}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(tenant))
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5)
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3`
	for run := range 3 {
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if e.SQL != want || !reflect.DeepEqual(e.Args, []any{true, int64(7), 5}) {
			t.Fatalf("run %d:\n got %s %v\nwant %s", run, e.SQL, e.Args, want)
		}
	}
	if text, _ := q.SQL(); strings.Contains(text, "org_id") {
		t.Errorf("the rewrite hook changed the query itself: %s", text)
	}
}

type staleEntity struct {
	ID   int64
	Name string
}

func (*staleEntity) Schema(*velox.Schema) {}

var staleMeta velox.MetaOnce[staleEntity]

func (*staleEntity) VeloxMeta() *velox.Meta[staleEntity] {
	return staleMeta.Get(func() velox.Shape[staleEntity] {
		return velox.Shape[staleEntity]{Columns: []string{"ID"}}
	})
}

func TestStaleGeneratedCodeIsReportedOnUse(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[staleEntity]().All(ctx)
	if !errors.Is(err, velox.ErrStale) || !strings.Contains(err.Error(), "velox_test.staleEntity") || e.Calls != 0 {
		t.Fatalf("%v, calls %d", err, e.Calls)
	}
	if text, _ := db.Query[staleEntity]().SQL(); text != "" {
		t.Errorf("SQL of an invalid entity: %q", text)
	}
}
```

`pipeline_test.go`:

```go
package velox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestRewriteCanRefuse(t *testing.T) {
	refused := errors.New("refused")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(context.Context, *velox.Statement) error { return refused }))
	if _, err := db.Query[testmodel.Invoice]().All(ctx); !errors.Is(err, refused) || e.Calls != 0 {
		t.Fatalf("%v, calls %d", err, e.Calls)
	}
}

func TestAroundsWrapInOrder(t *testing.T) {
	var trace []string
	around := func(name string) velox.Around {
		return func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			if st.SQL == "" {
				t.Error("around ran before rendering")
			}
			trace = append(trace, name+">")
			err := next(ctx)
			trace = append(trace, "<"+name)
			return err
		}
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithAround(around("a")), velox.WithAround(around("b")))
	if _, err := db.Query[testmodel.Invoice]().All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a>", "b>", "<b", "<a"}; !reflect.DeepEqual(trace, want) {
		t.Errorf("%v, want %v", trace, want)
	}
}

func TestAroundMustCallNext(t *testing.T) {
	lazy := func(context.Context, *velox.Statement, func(context.Context) error) error { return nil }
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithAround(lazy))
	if _, err := db.Query[testmodel.Invoice]().All(ctx); err == nil || !strings.Contains(err.Error(), "without calling next") {
		t.Fatalf("%v", err)
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: newDB`, `undefined: rows`, `undefined: velox.OpenFake`, `db.Query undefined`.

- [ ] **Step 6: Write the implementation**

`executor.go`:

```go
package velox

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
)

// rows is what an executor returns; *sql.Rows (wrapped) and pgx.Rows both
// provide it.
type rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
	Close()
}

// executor runs rendered statements.
type executor interface {
	query(ctx context.Context, sql string, args []any) (rows, error)
}

type sqlQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// sqlExecutor runs statements through database/sql.
type sqlExecutor struct{ q sqlQueryer }

func (e sqlExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.q.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{r}, nil
}

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }

type pgxQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// pgxExecutor runs statements through pgx's native protocol.
type pgxExecutor struct{ q pgxQueryer }

func (e pgxExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.q.Query(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return r, nil
}
```

`db.go`:

```go
package velox

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB runs statements, inside or outside a transaction. It is the only type
// that does: generic methods cannot be in an interface, and one concrete type
// means every function taking a *DB works in a transaction unchanged.
type DB struct {
	d    Dialect
	ex   executor
	cfg  *config
	inTx bool
}

type config struct {
	rewrites []Rewrite
	arounds  []Around
}

// Option configures a DB's pipeline.
type Option func(*config)

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given.
func WithRewrite(r Rewrite) Option { return func(c *config) { c.rewrites = append(c.rewrites, r) } }

// WithAround adds an around hook. The first one given is the outermost.
func WithAround(a Around) Option { return func(c *config) { c.arounds = append(c.arounds, a) } }

// Open returns a DB that runs statements through db, a *sql.DB the
// application opened with a driver for dialect d. For PostgreSQL, OpenPgx is
// faster.
func Open(d Dialect, db *sql.DB, opts ...Option) *DB { return newDB(d, sqlExecutor{db}, opts) }

// OpenPgx returns a DB for PostgreSQL that runs statements through pool with
// pgx's native protocol: about 0.4 times the time and allocations of
// database/sql for a 1,000-row result (design, Appendix A.5).
func OpenPgx(pool *pgxpool.Pool, opts ...Option) *DB { return newDB(Postgres, pgxExecutor{pool}, opts) }

func newDB(d Dialect, ex executor, opts []Option) *DB {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	return &DB{d: d, ex: ex, cfg: cfg}
}

// Dialect returns the DB's dialect.
func (db *DB) Dialect() Dialect { return db.d }

// dbErr names the entity in an error from the database or the driver.
func dbErr(info *EntityInfo, err error) error { return fmt.Errorf("velox: %s: %w", info.Name, err) }

// run takes a statement through the pipeline: rewrite hooks, rendering,
// around hooks, execution.
func (db *DB) run(ctx context.Context, st *Statement) (rows, error) {
	if len(db.cfg.rewrites) > 0 {
		st.Where = slices.Clone(st.Where)
		for _, rw := range db.cfg.rewrites {
			if err := rw(ctx, st); err != nil {
				return nil, err
			}
		}
	}
	st.SQL, st.Args = render(db.d, st)
	if len(db.cfg.arounds) == 0 {
		r, err := db.ex.query(ctx, st.SQL, st.Args)
		if err != nil {
			return nil, dbErr(st.Entity, err)
		}
		return r, nil
	}
	var out rows
	next := func(ctx context.Context) error {
		r, err := db.ex.query(ctx, st.SQL, st.Args)
		if err != nil {
			return dbErr(st.Entity, err)
		}
		out = r
		return nil
	}
	for i := len(db.cfg.arounds) - 1; i >= 0; i-- {
		a, inner := db.cfg.arounds[i], next
		next = func(ctx context.Context) error { return a(ctx, st, inner) }
	}
	if err := next(ctx); err != nil {
		if out != nil {
			out.Close()
		}
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("velox: %s: an Around hook returned without calling next", st.Entity.Name)
	}
	return out, nil
}
```

`query.go`:

```go
package velox

import (
	"context"
	"fmt"
	"iter"
	"slices"
)

// Query is a SELECT on entity E, built by chaining and run by All, One, Iter
// or Count. Running a query does not change it, so it can run again.
type Query[E any] struct {
	db   *DB
	meta *Meta[E]
	st   Statement
}

// Query starts a query on entity E:
//
//	db.Query[billing.Invoice]().Where(billing.Invoices.Paid.EQ(false)).Limit(50).All(ctx)
func (db *DB) Query[E any, PE Entity[E]]() *Query[E] {
	m := PE(nil).VeloxMeta()
	return &Query[E]{db: db, meta: m, st: Statement{Op: OpSelect, Entity: m.info, InTx: db.inTx}}
}

// Where adds conditions. All of a query's conditions must hold.
func (q *Query[E]) Where[_ Lazy[E]](ps ...Pred[E]) *Query[E] {
	for _, p := range ps {
		q.st.Where = append(q.st.Where, Predicate{p.n})
	}
	return q
}

// OrderBy adds orderings, applied in the order given.
func (q *Query[E]) OrderBy[_ Lazy[E]](os ...Order[E]) *Query[E] {
	for _, o := range os {
		q.st.OrderBy = append(q.st.OrderBy, o.t)
	}
	return q
}

// Limit returns at most n rows. n <= 0 removes the limit.
func (q *Query[E]) Limit[_ Lazy[E]](n int) *Query[E] {
	q.st.Limit = max(n, 0)
	return q
}

// Offset skips the first n rows. n <= 0 removes the offset.
func (q *Query[E]) Offset[_ Lazy[E]](n int) *Query[E] {
	q.st.Offset = max(n, 0)
	return q
}

// Unbounded declares that the query has no Limit on purpose. The guardrails
// refuse a query with neither (sub-project 2).
func (q *Query[E]) Unbounded[_ Lazy[E]]() *Query[E] {
	q.st.Unbounded = true
	return q
}

// Columns selects only the given columns; the other fields keep their zero
// values. With no columns it selects every column.
func (q *Query[E]) Columns[_ Lazy[E]](cs ...Column[E]) *Query[E] {
	if len(cs) == 0 {
		q.st.Columns = nil
		return q
	}
	q.st.Columns = make([]int, len(cs))
	for i, c := range cs {
		q.st.Columns[i] = int(c.column(nil))
	}
	return q
}

// Omit selects every column except the given ones.
func (q *Query[E]) Omit[_ Lazy[E]](cs ...Column[E]) *Query[E] {
	if q.meta.err != nil {
		return q
	}
	skip := make(map[int32]bool, len(cs))
	for _, c := range cs {
		skip[c.column(nil)] = true
	}
	q.st.Columns = make([]int, 0, len(q.meta.info.Columns))
	for i := range q.meta.info.Columns {
		if !skip[int32(i)] {
			q.st.Columns = append(q.st.Columns, i)
		}
	}
	return q
}

// cloneStatement copies a query's statement for one run, so that running it
// leaves the query as it was.
func cloneStatement(st *Statement) Statement {
	c := *st
	c.Where = slices.Clip(c.Where)
	c.OrderBy = slices.Clip(c.OrderBy)
	c.Columns = slices.Clip(c.Columns)
	return c
}

func columnCount(st *Statement) int {
	if st.Columns != nil {
		return len(st.Columns)
	}
	return len(st.Entity.Columns)
}

// scanRow scans the current row into p through the entity's scan plan.
func scanRow[E any](r rows, m *Meta[E], cols []int, dst []any, p *E) error {
	for i := range dst {
		c := i
		if cols != nil {
			c = cols[i]
		}
		dst[i] = m.addr(p, c)
	}
	return r.Scan(dst...)
}

// All runs the query and returns every row.
func (q *Query[E]) All[_ Lazy[E]](ctx context.Context) ([]E, error) {
	if q.meta.err != nil {
		return nil, q.meta.err
	}
	st := cloneStatement(&q.st)
	r, err := q.db.run(ctx, &st)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	dst := make([]any, columnCount(&st))
	out := make([]E, 0, min(st.Limit, 1024))
	for r.Next() {
		out = append(out, *new(E))
		if err := scanRow(r, q.meta, st.Columns, dst, &out[len(out)-1]); err != nil {
			return nil, dbErr(st.Entity, err)
		}
	}
	if err := r.Err(); err != nil {
		return nil, dbErr(st.Entity, err)
	}
	return out, nil
}

// One runs the query for exactly one row. Its error wraps ErrNotFound when no
// row matches and ErrNotSingular when more than one does.
func (q *Query[E]) One[_ Lazy[E]](ctx context.Context) (*E, error) {
	c := *q
	c.st.Limit = 2
	out, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	switch len(out) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, q.meta.info.Name)
	case 1:
		return &out[0], nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotSingular, q.meta.info.Name)
}

// Iter runs the query and yields one row at a time; only the current row is
// held in memory.
func (q *Query[E]) Iter[_ Lazy[E]](ctx context.Context) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		var zero E
		if q.meta.err != nil {
			yield(zero, q.meta.err)
			return
		}
		st := cloneStatement(&q.st)
		r, err := q.db.run(ctx, &st)
		if err != nil {
			yield(zero, err)
			return
		}
		defer r.Close()
		dst := make([]any, columnCount(&st))
		for r.Next() {
			var e E
			if err := scanRow(r, q.meta, st.Columns, dst, &e); err != nil {
				yield(zero, dbErr(st.Entity, err))
				return
			}
			if !yield(e, nil) {
				return
			}
		}
		if err := r.Err(); err != nil {
			yield(zero, dbErr(st.Entity, err))
		}
	}
}

// Count returns the number of rows matching the query's conditions; its
// ordering, limit, offset and columns are ignored.
func (q *Query[E]) Count[_ Lazy[E]](ctx context.Context) (int64, error) {
	if q.meta.err != nil {
		return 0, q.meta.err
	}
	st := cloneStatement(&q.st)
	st.Count, st.Columns, st.OrderBy, st.Limit, st.Offset = true, nil, nil, 0, 0
	r, err := q.db.run(ctx, &st)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var n int64
	if r.Next() {
		if err := r.Scan(&n); err != nil {
			return 0, dbErr(st.Entity, err)
		}
	}
	if err := r.Err(); err != nil {
		return 0, dbErr(st.Entity, err)
	}
	return n, nil
}

// SQL returns the statement and arguments the query would send, without a
// database. Rewrite hooks have not run on it, so a condition a hook adds (a
// tenant's, for example) is not shown. It returns "" when the entity's
// metadata is invalid; All returns that error.
func (q *Query[E]) SQL[_ Lazy[E]]() (string, []any) {
	if q.meta.err != nil {
		return "", nil
	}
	st := cloneStatement(&q.st)
	return render(q.db.d, &st)
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: `ok  	github.com/syssam/velox2` and `?   	github.com/syssam/velox2/internal/testmodel	[no test files]`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum executor.go db.go query.go export_test.go query_test.go pipeline_test.go internal/testmodel
git commit -m "Add DB, the pipeline, both executors and the query API" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Integration tests on SQLite

**Files:**
- Create: `internal/itest/itest_test.go`
- Modify: `go.mod`, `go.sum` (modernc.org/sqlite)

**Interfaces:**
- Consumes: `velox.Open`, `velox.SQLite`, the query API (Task 5); `testmodel.Invoice`, `testmodel.Invoices` (Task 5).
- Produces: `type target struct{ name string; d velox.Dialect; db *velox.DB; exec func(t *testing.T, query string, args ...any); ddl string }`; `func targets(t *testing.T) []target`; `func sqliteTarget(t *testing.T) target`; `func sqlExec(db *sql.DB, d velox.Dialect) func(*testing.T, string, ...any)`; `func each(t *testing.T, f func(t *testing.T, tg target))`. Task 8 adds PostgreSQL and MySQL targets to `targets`.

- [ ] **Step 1: Add the SQLite driver**

Run: `go get modernc.org/sqlite@latest`
Expected: `go: added modernc.org/sqlite v…`

- [ ] **Step 2: Write the tests**

`internal/itest/itest_test.go`:

```go
// Package itest runs velox against real databases: SQLite always, PostgreSQL
// and MySQL when VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL name them
// (scripts/ci-local.sh starts both and sets the variables).
package itest

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	I   = testmodel.Invoices
)

// target is one database the tests run against.
type target struct {
	name string
	d    velox.Dialect
	db   *velox.DB
	exec func(t *testing.T, query string, args ...any) // setup SQL, written with ? placeholders
	ddl  string
}

func targets(t *testing.T) []target {
	t.Helper()
	return []target{sqliteTarget(t)}
}

func sqliteTarget(t *testing.T) target {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // one connection keeps the in-memory database alive
	t.Cleanup(func() { _ = db.Close() })
	return target{
		name: "sqlite", d: velox.SQLite, db: velox.Open(velox.SQLite, db), exec: sqlExec(db, velox.SQLite),
		ddl: `CREATE TABLE invoices (created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, id INTEGER PRIMARY KEY,
			org_id INTEGER NOT NULL, number TEXT NOT NULL, total REAL NOT NULL, paid BOOLEAN NOT NULL,
			paid_at DATETIME, note TEXT, data BLOB NOT NULL)`,
	}
}

func sqlExec(db *sql.DB, d velox.Dialect) func(*testing.T, string, ...any) {
	return func(t *testing.T, q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind(d, q), args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// rebind turns ? placeholders into $n for PostgreSQL.
func rebind(d velox.Dialect, q string) string {
	if d != velox.Postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for i := range len(q) {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

// setup creates the invoices table with five rows. Org 1 owns ids 1–4. The
// numbers hold LIKE wildcards and a backslash; even ids have a note; paid
// invoices have paid_at.
func setup(t *testing.T, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS invoices")
	tg.exec(t, tg.ddl)
	note := "a note"
	for _, r := range []struct {
		id, org int64
		number  string
		total   float64
		paid    bool
	}{
		{1, 1, "INV-1", 10, true}, {2, 1, "INV-2", 20, false}, {3, 1, "50%_OFF", 30, true},
		{4, 1, "500X", 40, false}, {5, 2, `back\slash`, 50, true},
	} {
		var paidAt *time.Time
		if r.paid {
			paidAt = &t0
		}
		var n *string
		if r.id%2 == 0 {
			n = &note
		}
		tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			t0, t0, r.id, r.org, r.number, r.total, r.paid, paidAt, n, []byte("d"))
	}
}

func each(t *testing.T, f func(t *testing.T, tg target)) {
	for _, tg := range targets(t) {
		t.Run(tg.name, func(t *testing.T) {
			setup(t, tg)
			f(t, tg)
		})
	}
}

func ids(xs []testmodel.Invoice) []int64 {
	out := make([]int64, len(xs))
	for i := range xs {
		out[i] = xs[i].ID
	}
	return out
}

func query(t *testing.T, tg target, ps ...velox.Pred[testmodel.Invoice]) []int64 {
	t.Helper()
	got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ids(got)
}

func wantIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("ids %v, want %v", got, want)
	}
}

func TestFilterOrderLimit(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.Total.Desc()).Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ids(got), []int64{4, 3})
	})
}

func TestOneScansEveryType(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		two, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if two.Number != "INV-2" || two.Total != 20 || two.Paid || two.PaidAt != nil || two.Note == nil ||
			*two.Note != "a note" || string(two.Data) != "d" || !two.CreatedAt.Equal(t0) || two.OrgID != 1 {
			t.Errorf("%+v", two)
		}
		three, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(3)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !three.Paid || three.PaidAt == nil || !three.PaidAt.Equal(t0) || three.Note != nil {
			t.Errorf("%+v", three)
		}
	})
}

func TestOneErrors(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if _, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(99)).One(ctx); !errors.Is(err, velox.ErrNotFound) {
			t.Errorf("no row: %v", err)
		}
		if _, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).One(ctx); !errors.Is(err, velox.ErrNotSingular) {
			t.Errorf("four rows: %v", err)
		}
	})
}

// TestLikeTakesTextLiterally is Review Focus 1.
func TestLikeTakesTextLiterally(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, I.Number.HasPrefix("50%")), []int64{3})
		wantIDs(t, query(t, tg, I.Number.HasSuffix("_OFF")), []int64{3})
		wantIDs(t, query(t, tg, I.Number.Contains("_")), []int64{3})
		wantIDs(t, query(t, tg, I.Number.Contains(`\`)), []int64{5})
		wantIDs(t, query(t, tg, I.Number.Like("INV-_")), []int64{1, 2})
	})
}

// TestTextMatchingFollowsTheDatabase is Review Focus 2: whether LIKE ignores
// case is the database's choice, and the handles' documentation says so.
func TestTextMatchingFollowsTheDatabase(t *testing.T) {
	want := map[velox.Dialect][]int64{velox.Postgres: nil, velox.MySQL: {1, 2}, velox.SQLite: {1, 2}}
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, I.Number.HasPrefix("inv")), want[tg.d])
		wantIDs(t, query(t, tg, I.Number.EqualFold("inv-1")), []int64{1})
	})
}

func TestEmptyLists(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, I.ID.In()), nil)
		wantIDs(t, query(t, tg, I.OrgID.EQ(1), I.ID.NotIn()), []int64{1, 2, 3, 4})
	})
}

func TestLogicAndNull(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		wantIDs(t, query(t, tg, velox.Or(I.ID.EQ(1), I.ID.EQ(5)), velox.Not(I.Paid.EQ(false))), []int64{1, 5})
		wantIDs(t, query(t, tg, I.Note.IsNull()), []int64{1, 3, 5})
		wantIDs(t, query(t, tg, I.PaidAt.NotNull()), []int64{1, 3, 5})
		wantIDs(t, query(t, tg, I.Total.Between(20, 40)), []int64{2, 3, 4})
	})
}

func TestCountOffsetColumnsIter(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		n, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Count(ctx)
		if err != nil || n != 4 {
			t.Errorf("count %d, %v", n, err)
		}
		got, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Offset(3).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ids(got), []int64{4, 5})
		some, err := tg.db.Query[testmodel.Invoice]().Columns(I.ID, I.Number).Where(I.ID.EQ(3)).One(ctx)
		if err != nil || some.Number != "50%_OFF" || some.Total != 0 || some.Data != nil {
			t.Errorf("%+v, %v", some, err)
		}
		var seen int
		for _, err := range tg.db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			if seen++; seen == 2 {
				break
			}
		}
	})
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/itest/ -v -run . 2>&1 | grep -E '^(=== RUN|--- |ok|FAIL|PASS)' | head -40`
Expected: every `TestX/sqlite` subtest `--- PASS`, then `ok  	github.com/syssam/velox2/internal/itest`.

If `TestOneScansEveryType` fails on time values, the driver did not parse `DATETIME`. Check that the DDL declares `DATETIME` (modernc.org/sqlite parses columns declared `DATE`, `DATETIME` and `TIMESTAMP`) before changing anything else.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/itest
git commit -m "Test querying end to end on SQLite" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Render cache and the allocation budget

**Files:**
- Create: `render_cache.go`
- Modify: `db.go` (the `config` struct and `run`)
- Test: `render_cache_test.go`, `budget_test.go`

**Interfaces:**
- Consumes: `writer`, `render` (Task 4); `config`, `run`, `FakeExec`, `OpenFake`, `RawRows` (Task 5); render test helpers `stmt`, `renderCases`, `argsEqual`, `pgCols`, `hID`, `hOrg`, `hNumber` (Task 4).
- Produces: `type renderCache struct{ mu sync.RWMutex; m map[string]string }` with `render(d Dialect, st *Statement) (string, []any)`; `var maxShapes = 4096`; `config.cache renderCache`.

- [ ] **Step 1: Write the failing tests**

`render_cache_test.go`:

```go
package velox

import (
	"sync"
	"testing"
)

func TestCacheRendersLikeRender(t *testing.T) {
	var c renderCache
	for range 2 { // the second pass is served from the cache
		for _, rc := range renderCases() {
			for d := range rc.sql {
				want, wantArgs := render(d, rc.st())
				got, args := c.render(d, rc.st())
				if got != want || !argsEqual(args, wantArgs) {
					t.Errorf("%s, %v:\n got %s %v\nwant %s %v", rc.name, d, got, args, want, wantArgs)
				}
			}
		}
	}
	if len(c.m) == 0 {
		t.Error("nothing was cached")
	}
}

func TestCacheKeepsShapesApart(t *testing.T) {
	var c renderCache
	one, _ := c.render(Postgres, stmt(hID.In(1)))
	two, _ := c.render(Postgres, stmt(hID.In(1, 2)))
	if one == two {
		t.Error("In lists of different lengths share SQL")
	}
	some := stmt()
	some.Columns = []int{0}
	a, _ := c.render(Postgres, some)
	b, _ := c.render(Postgres, stmt())
	if a == b {
		t.Error("a column subset shares SQL with every column")
	}
	m, _ := c.render(MySQL, stmt())
	if m == b {
		t.Error("dialects share SQL")
	}
}

// TestCacheIsSafeForConcurrentUse is Review Focus 5; run it with -race.
func TestCacheIsSafeForConcurrentUse(t *testing.T) {
	var c renderCache
	want := `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND "number" IN ($2, $3)`
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				v := int64(g*1000 + i)
				text, args := c.render(Postgres, stmt(hOrg.EQ(v), hNumber.In("a", "b")))
				if text != want || args[0] != v || args[2] != "b" {
					t.Errorf("goroutine %d: %s %v", g, text, args)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestCacheStopsGrowing(t *testing.T) {
	defer func(n int) { maxShapes = n }(maxShapes)
	maxShapes = 3
	var c renderCache
	for n := 1; n <= 10; n++ {
		vs := make([]int64, n)
		want, _ := render(Postgres, stmt(hID.In(vs...)))
		if got, _ := c.render(Postgres, stmt(hID.In(vs...))); got != want {
			t.Fatalf("n=%d: %s", n, got)
		}
	}
	if len(c.m) != 3 {
		t.Errorf("cache holds %d shapes, the limit is 3", len(c.m))
	}
}
```

`budget_test.go`:

```go
package velox_test

import (
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestOneRowAllocationBudget pins design §8: a one-row query makes at most 8
// more allocations than hand-written code scanning the same rows.
func TestOneRowAllocationBudget(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run() // fill the render cache
	vx := testing.AllocsPerRun(200, run)
	text, args := e.SQL, e.Args
	raw := testing.AllocsPerRun(200, func() {
		r, err := velox.RawRows(db, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]testmodel.Invoice, 0, 1)
		for r.Next() {
			out = append(out, testmodel.Invoice{})
			x := &out[len(out)-1]
			if err := r.Scan(&x.CreatedAt, &x.UpdatedAt, &x.ID, &x.OrgID, &x.Number, &x.Total, &x.Paid, &x.PaidAt, &x.Note, &x.Data); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
	})
	t.Logf("velox %.0f allocations, hand-written %.0f", vx, raw)
	if vx > raw+8 {
		t.Errorf("velox makes %.0f allocations for a one-row query and hand-written code %.0f: over the budget of +8 (design §8)", vx, raw)
	}
}

func BenchmarkOneRow(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: renderCache`, `undefined: maxShapes`.

- [ ] **Step 3: Write `render_cache.go`**

```go
package velox

import (
	"encoding/binary"
	"sync"
	"unsafe"
)

// maxShapes bounds the cache. Programs have a fixed set of query shapes,
// except that In lists of every length are shapes of their own; past the
// bound, statements are rendered without the cache.
var maxShapes = 4096

// renderCache keeps the SQL text of each statement shape: everything about a
// statement except its argument values. A statement whose shape is cached
// only collects its arguments.
type renderCache struct {
	mu sync.RWMutex
	m  map[string]string
}

func (c *renderCache) render(d Dialect, st *Statement) (string, []any) {
	var buf [256]byte
	key := shapeKey(buf[:0], d, st)
	c.mu.RLock()
	text, ok := c.m[string(key)]
	c.mu.RUnlock()
	if ok {
		w := writer{d: d, in: st.Entity, argsOnly: true, args: make([]any, 0, 8)}
		w.selectStmt(st)
		return text, w.args
	}
	text, args := render(d, st)
	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[string]string)
	}
	if len(c.m) < maxShapes {
		c.m[string(key)] = text
	}
	c.mu.Unlock()
	return text, args
}

func flag(b bool) byte {
	if b {
		return 1
	}
	return 0
}

func shapeKey(k []byte, d Dialect, st *Statement) []byte {
	k = append(k, byte(d), byte(st.Op), flag(st.Count), flag(st.Columns != nil), flag(st.Limit > 0), flag(st.Offset > 0))
	k = binary.LittleEndian.AppendUint64(k, uint64(uintptr(unsafe.Pointer(st.Entity))))
	k = binary.AppendUvarint(k, uint64(len(st.Columns)))
	for _, c := range st.Columns {
		k = binary.AppendUvarint(k, uint64(c))
	}
	k = binary.AppendUvarint(k, uint64(len(st.Where)))
	for _, p := range st.Where {
		k = nodeKey(k, p.n)
	}
	k = binary.AppendUvarint(k, uint64(len(st.OrderBy)))
	for _, o := range st.OrderBy {
		k = binary.AppendUvarint(k, uint64(o.Column))
		k = append(k, flag(o.Desc))
	}
	return k
}

func nodeKey(k []byte, n node) []byte {
	k = append(k, byte(n.op))
	k = binary.AppendUvarint(k, uint64(n.col))
	if n.op == opIn || n.op == opNotIn {
		k = binary.AppendUvarint(k, uint64(len(n.v.([]any))))
	}
	k = binary.AppendUvarint(k, uint64(len(n.kids)))
	for _, c := range n.kids {
		k = nodeKey(k, c)
	}
	return k
}
```

- [ ] **Step 4: Route the pipeline through the cache**

In `db.go`, change `config` to:

```go
type config struct {
	rewrites []Rewrite
	arounds  []Around
	cache    renderCache
}
```

and in `run`, change the rendering line from `st.SQL, st.Args = render(db.d, st)` to:

```go
	st.SQL, st.Args = db.cfg.cache.render(db.d, st)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./... && go test -run '^$' -bench BenchmarkOneRow -benchmem .`
Expected: all packages `ok`. `TestOneRowAllocationBudget` logs, for example, `velox 7 allocations, hand-written 3`, and passes. The benchmark prints one line with `allocs/op`. If the budget test fails, report the logged numbers; do not raise the budget, which is the spec's §8.

- [ ] **Step 6: Commit**

```bash
git add render_cache.go render_cache_test.go budget_test.go db.go
git commit -m "Cache SQL text by statement shape and pin the allocation budget" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: PostgreSQL and MySQL, CI and its local mirror

**Files:**
- Create: `docker-compose.ci.yml`, `scripts/ci-local.sh`, `.github/workflows/ci.yml`, `.golangci.yml`
- Modify: `internal/itest/itest_test.go` (`targets` and two new target functions), `go.mod`, `go.sum` (go-sql-driver/mysql)

**Interfaces:**
- Consumes: `velox.OpenPgx`, `velox.Open`, `target`, `sqlExec`, `sqliteTarget` (Tasks 5–6).
- Produces: `func postgresTargets(t *testing.T, dsn string) []target`, `func mysqlTarget(t *testing.T, dsn string) target`. The environment variables `VELOX_TEST_POSTGRES` and `VELOX_TEST_MYSQL`, and the command `scripts/ci-local.sh`.

- [ ] **Step 1: Add the MySQL driver**

Run: `go get github.com/go-sql-driver/mysql@latest`
Expected: `go: added github.com/go-sql-driver/mysql v…`

- [ ] **Step 2: Add the PostgreSQL and MySQL targets**

In `internal/itest/itest_test.go`, add these imports to the import block:

```go
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
```

Replace `targets` with:

```go
func targets(t *testing.T) []target {
	t.Helper()
	ts := []target{sqliteTarget(t)}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		ts = append(ts, postgresTargets(t, dsn)...)
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		ts = append(ts, mysqlTarget(t, dsn))
	}
	return ts
}
```

and add:

```go
const pgDDL = `CREATE TABLE invoices (created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
	id bigint PRIMARY KEY, org_id bigint NOT NULL, number text NOT NULL, total double precision NOT NULL,
	paid boolean NOT NULL, paid_at timestamptz, note text, data bytea NOT NULL)`

// postgresTargets returns PostgreSQL twice: through native pgx (OpenPgx) and
// through database/sql. Both are opened per test, so no connection keeps a
// statement cached across the DROP and CREATE of setup.
func postgresTargets(t *testing.T, dsn string) []target {
	t.Helper()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := sqlExec(sqlDB, velox.Postgres)
	return []target{
		{name: "postgres-pgx", d: velox.Postgres, db: velox.OpenPgx(pool), exec: exec, ddl: pgDDL},
		{name: "postgres-sql", d: velox.Postgres, db: velox.Open(velox.Postgres, sqlDB), exec: exec, ddl: pgDDL},
	}
}

const mysqlDDL = "CREATE TABLE invoices (created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL, " +
	"id BIGINT PRIMARY KEY, org_id BIGINT NOT NULL, number VARCHAR(64) NOT NULL, total DOUBLE NOT NULL, " +
	"paid BOOLEAN NOT NULL, paid_at DATETIME(6) NULL, note TEXT NULL, data BLOB NOT NULL)"

func mysqlTarget(t *testing.T, dsn string) target {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return target{name: "mysql", d: velox.MySQL, db: velox.Open(velox.MySQL, db), exec: sqlExec(db, velox.MySQL), ddl: mysqlDDL}
}
```

- [ ] **Step 3: Write the CI configuration and its local mirror**

`docker-compose.ci.yml`:

```yaml
# The databases CI runs against, for scripts/ci-local.sh.
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: velox
      POSTGRES_PASSWORD: velox
      POSTGRES_DB: velox
    ports: ["127.0.0.1:55432:5432"]
    tmpfs: [/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U velox"]
      interval: 1s
      timeout: 3s
      retries: 60
  mysql:
    image: mysql:8.4
    environment:
      MYSQL_ROOT_PASSWORD: velox
      MYSQL_DATABASE: velox
      MYSQL_USER: velox
      MYSQL_PASSWORD: velox
    ports: ["127.0.0.1:53306:3306"]
    tmpfs: [/var/lib/mysql]
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "-uvelox", "-pvelox"]
      interval: 1s
      timeout: 3s
      retries: 120
```

`scripts/ci-local.sh`:

```bash
#!/usr/bin/env bash
# Runs what CI runs, against the same database versions, in local containers.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -f docker-compose.ci.yml -p velox2-ci)
"${compose[@]}" up -d --wait
trap '"${compose[@]}" down -v' EXIT

export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true'

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt needed:"; echo "$unformatted"; exit 1
fi
go vet ./...
golangci-lint run ./...
go test -race -count=1 ./...
```

`.golangci.yml`:

```yaml
version: "2"
linters:
  default: standard
```

`.github/workflows/ci.yml`:

```yaml
name: CI
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: velox
          POSTGRES_PASSWORD: velox
          POSTGRES_DB: velox
        ports: ["55432:5432"]
        options: >-
          --health-cmd "pg_isready -U velox" --health-interval 2s --health-timeout 5s --health-retries 30
      mysql:
        image: mysql:8.4
        env:
          MYSQL_ROOT_PASSWORD: velox
          MYSQL_DATABASE: velox
          MYSQL_USER: velox
          MYSQL_PASSWORD: velox
        ports: ["53306:3306"]
        options: >-
          --health-cmd "mysqladmin ping -h 127.0.0.1 -uvelox -pvelox" --health-interval 2s --health-timeout 5s --health-retries 60
    env:
      VELOX_TEST_POSTGRES: postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable
      VELOX_TEST_MYSQL: velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.27"
      - name: gofmt
        run: test -z "$(gofmt -l .)"
      - run: go vet ./...
      - name: golangci-lint
        run: |
          go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
          golangci-lint run ./...
      - run: go test -race -count=1 ./...
```

Run: `chmod +x scripts/ci-local.sh`

- [ ] **Step 4: Run the full matrix locally**

Run: `scripts/ci-local.sh 2>&1 | tail -15`
Expected: both containers start healthy. gofmt, vet and golangci-lint print nothing. `go test` prints `ok` for `github.com/syssam/velox2` and `github.com/syssam/velox2/internal/itest`, and the containers are removed. To see the subtests: `VELOX_TEST_POSTGRES=… VELOX_TEST_MYSQL=… go test ./internal/itest -v` shows `/sqlite`, `/postgres-pgx`, `/postgres-sql` and `/mysql` passing for every test.

If `TestTextMatchingFollowsTheDatabase/mysql` fails, print the column collation: `SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_NAME='invoices' AND COLUMN_NAME='number'`. The expectation assumes MySQL 8.4's default `utf8mb4_0900_ai_ci`, which ignores case. Fix the test's expectation only if the server's default differs, and record it in the handles' doc comment.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/itest docker-compose.ci.yml scripts/ci-local.sh .github .golangci.yml
git commit -m "Run the query tests on PostgreSQL and MySQL, in CI and locally" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Spec coverage of this plan

| Spec section | Covered here | Later plan |
|---|---|---|
| §1 entity model: columns, names, NULL, primary key, mixins, `velox:"-"`, `Schema` declarative | Task 2 | — |
| §1 relations | Holders (Task 2) | Loading, `ForeignKey`, many-to-many: plan 4 |
| §2 generated code | Format fixed by hand in `internal/testmodel` (Task 5) | velox-gen, naming, `-check`, `verify`: plan 2 |
| §2 handle types | All 8 types with their read methods (Task 3) | `Set`, `Add`, `Sub`, `SetNull`, `Sum`, `Avg`: plans 3 and 4 |
| §3 rules 1–3 | Guard test (Task 3), holders (Task 2) | Size guards: plan 5 |
| §3 rule 4 (nothing at init) | `MetaOnce` (Task 2) | Generator test: plan 2 |
| §4.1 one handle type | `DB` (Task 5) | Transactions: plan 3 |
| §4.3 read API | Task 5; `Has`, `With`, `After` in plan 4 | plan 4 |
| §4.2, §4.4–4.6 writes and transactions | — | plan 3 |
| §5 pipeline | Rewrite, render, around, execute, scan (Tasks 5, 7) | Route: sub-project 4; `Tags`: sub-project 3 |
| §6 dialects and executors | Tasks 4, 5, 8 | Constraint errors, retry codes: plan 3 |
| §7 errors | Tasks 1, 2, 5 | `ConstraintError`: plan 3 |
| §8 budget | One-row allocation budget (Task 7) | Real-database runs: plan 5 |
| §9 testing | Dialect matrix, CI mirror (Task 8), handle and method guards (Task 3) | Golden generator tests: plan 2; scale harness, fuzzing: plan 5 |
