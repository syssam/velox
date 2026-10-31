# Core 4 — Relations, Eager Loading and Keyset Pagination — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** velox relations:
- `velox.One[T]` / `velox.Many[T]` fields get keys by convention or declaration (`s.Many(&x.Lines).ForeignKey(…)`, `s.One(&x.Org).References(&x.OrgID)`).
- Each relation gets a typed handle (`velox.Rel[Invoice, Item]`).
- `Has` / `HasWhere` render `EXISTS`.
- `With` loads relations, nested, filtered and ordered, in one query per relation chunked under the dialect's parameter limit.
- `After(last)` gives keyset pagination.
- A statement over the parameter limit is refused before it is sent.
- `EQ(nil)` renders `IS NULL`.

**Architecture:**
- **Relation metadata.** The generated shape lists each relation with its target (`velox.RelationTo[Item]("Items")`). That gives the non-generic metadata a way to reach the target's `EntityInfo` without reflection or a registry.
- **Lazy resolution.** A relation is resolved on first use, behind its own `sync.Once`. Resolving reads the target's `EntityInfo` but never resolves the target's own relations, so cyclic and self relations cannot deadlock.
- **Loading.** Loading is non-generic. Each entity's `MetaOnce.Get` builds a small *kit* of closures (read a column's value, scan rows into a `[]E`, pick elements), compiled once in the entity's own package. The loader fills the `One`/`Many` holder through its field offset.
- **Generic code stays at call sites.** Handles and `With` are generic only at their call sites, so the compile-cost rules hold (spec §3).

**Tech Stack:** Go 1.27. No new dependencies (`encoding/json` from the standard library).

**Spec:** `docs/history/specs/2026-10-14-core-design.md` — §1 rule 7 (relations), §2 (generated code, handle types), §3 (compile-cost rules), §4.3 (read: `With`, `Has`, keyset). Also read "Plan 4 (relations)" in `docs/history/plans/2026-10-14-core-1-followups.md`, which this plan resolves. Task 6 updates the spec where this plan settles what it left open.

**This is plan 4 of 5 for Core.** Plans 1–3 (querying, generator, writes) are merged on `main`.

## Global Constraints

- **Generic methods only.** Every method of a generic velox type is a generic method (`[_ Lazy[...]]` or a method's own type parameter), with two exceptions:
  - the handles' `column(*E) int32`;
  - `One[T]`/`Many[T]`'s `MarshalJSON`, which this plan adds and records in spec §3.

  `methods_test.go` enforces this; Task 3 adds the `MarshalJSON` exemption to it.
- **Relation holders store `any`.** `One[T]` and `Many[T]` store their value as `any` and have the same layout as the non-generic `holder{v any; loaded bool}`. A test pins that.
- **Nothing at init.** No registry. A relation's target is reached only through the generated shape's `RelationTo[T]`.
- **Generated code changes.**
  - `GeneratedCodeIsVersion1` becomes `GeneratedCodeIsVersion2`.
  - `Shape.Relations` becomes `[]Relation`.
  - The handles struct gains one `velox.Rel[E, T]` field per relation, after the columns.
  - `velox-gen -check` and `verify` must pass on `internal/testmodel`.
- **Every error velox creates starts with `velox: `.**
- **Allocation budget.** `TestOneRowAllocationBudget` must still pass (a query with no `With` and no `After` does no new work).
- **The root module gains no dependency.**

## Review Focus

1. **Cyclic and self relations never deadlock or recurse forever.**
   - The cases: `Invoice.Items` ↔ `Item.Invoice`, `Sample.Parent` → `Sample`, and a nested `With` that loops back to the owner.
   - Pinned by `TestRelationsResolveWithoutDeadlock` (Task 1) and `TestWithNestedLoopsBack` (Task 3).
2. **Loaded entities attach to the right owners.**
   - The rules:
     - Keys of different Go types are a `SchemaError` at resolution, never a silent miss.
     - A nil nullable key loads nothing and sends nothing.
     - An owner with no related rows gets an empty, loaded `Many`.
   - Pinned by `TestResolveRefusesMismatchedKeys` (Task 1) and `TestWithAttachesToOwners` (Task 3).
3. **`EXISTS` correlates to the right table.** Even when the relation points back to its own table, or a `Has` is nested inside a `HasWhere`, the subquery's alias keeps the inner and outer columns apart. Pinned by `TestRenderExists` (Task 2) and `TestHas` on every database (Task 6).
4. **Chunked loading is complete and ordered.** Loading over more keys than one statement may carry returns every related row exactly once, in the requested order within each owner. Pinned by `TestWithChunksUnderTheParameterLimit` (Task 3).
5. **`After` refuses orderings it cannot page correctly.**
   - The rule: an ordering that is not unique, or that includes a nullable column, is an error.
   - Without the rule, it would skip or repeat rows silently.
   - Pinned by `TestAfterRefusesUnsafeOrderings` (Task 4).

---

## File Structure

| File | Responsibility |
|---|---|
| `relation.go` (modify) | `holder`; `Relation`, `RelationTo`; `relation`, `link`, resolution; `Rel[E, T]` handle; `Has`/`HasWhere`; load options; `MarshalJSON` |
| `meta.go` (modify) | relation fields in `flatten`/`buildInfo`; `ColumnInfo.off`; `EntityInfo.rels`, `EntityInfo.kit`; `CheckEntity` resolves relations; `kit` built in `Get` |
| `schema.go` (modify) | `Schema.Many`, `Schema.One`, `RelDef[T]` with `ForeignKey`, `References` |
| `doc.go` (modify) | `GeneratedCodeIsVersion2` |
| `pred.go`, `render.go`, `render_cache.go`, `db.go` (modify) | `opExists`, `opInvalid`; `exists` rendering with aliases; cache key; `checkColumns`; nil `EQ`; parameter limit |
| `query.go` (modify) | `Query.err`, `Query.loads`, `Query.after`; `With`; `After`; `prepare` |
| `load.go` | the non-generic loader: chunking, grouping, filling holders |
| `write.go` (modify) | `UpdateQuery`/`DeleteQuery` `Where` record a predicate's error |
| `internal/testmodel/*` | `Item` entity; `Invoice.Items`; `Sample.ParentID`; generated files |
| `cmd/velox-gen/{model,discover,render}.go` | relation targets, `Rel` handles, `RelationTo`, version 2 |
| `internal/itest/relations_test.go` | relations on every database |

---

### Task 1: Relation metadata and generated relation handles

**Files:**
- Modify: `relation.go`, `meta.go`, `schema.go`, `doc.go`, `meta_test.go`, `internal/testmodel/invoice.go`, `internal/testmodel/sample.go`, `cmd/velox-gen/model.go`, `cmd/velox-gen/discover.go`, `cmd/velox-gen/render.go`, `cmd/velox-gen/golden_test.go`
- Create: `internal/testmodel/item.go`, `relation_test.go` (package `velox`)
- Regenerate: `internal/testmodel/*_velox.go` (with `go -C cmd/velox-gen test -run TestTestmodelIsReproduced -update`)

**Interfaces:**
- Produces:
  - `type Relation struct{ name string; target func() (*EntityInfo, error) }`, made by `func RelationTo[T any, PT Entity[T]](name string) Relation`
  - `Shape.Relations []Relation`
  - `type relation struct` with `resolve(owner *EntityInfo) (link, error)`
  - `type link struct{ target *EntityInfo; ownerCol, targetCol int }`, meaning owner.Columns[ownerCol] = target.Columns[targetCol]
  - `EntityInfo.rels []*relation`, in shape order
  - `ColumnInfo.off uintptr`
  - `func (s *Schema) Many[T any](f *Many[T]) *RelDef[T]` and `One`
  - `RelDef[T].ForeignKey[K any](func(*T) *K)` and `RelDef[T].References[V any](*V)`
  - `type Rel[E, T any] struct{ I int32 }`, extended in Task 3
  - `func metaOf[E any]() (*Meta[E], error)`
  - `GeneratedCodeIsVersion2`
- Relation conventions:
  - **Declared ForeignKey:** the target holds the key, in the column the function returns.
  - **Declared References:** the owner holds the key, in the named column, and the target's primary key is the other side.
  - **Undeclared One:** the owner holds the key in a column named `<Relation>ID` (for example `Invoice` holds `InvoiceID`) if it has one.
  - **Any other relation:** the target holds the key in a field named `<Owner type>ID` (for example `Item.InvoiceID`).
  - **Key types:** the side that holds no foreign key uses its single-column primary key. The two key columns must have the same Go type, ignoring one level of pointer, and that type must be comparable.

- [ ] **Step 1: Write the failing tests**

Add the test entities. Create `internal/testmodel/item.go`:

```go
package testmodel

import "github.com/syssam/velox2"

// Item is a line of an Invoice. Invoice.Items finds it by the convention
// "<Owner>ID" (InvoiceID); Item.Invoice belongs to the Invoice whose key
// InvoiceID holds, by the convention "<Relation>ID".
type Item struct {
	ID        int64
	InvoiceID int64
	SKU       string
	Qty       int64
	Invoice   velox.One[Invoice]
}

// Schema names Item's table.
func (x *Item) Schema(s *velox.Schema) { s.Table("items") }
```

In `internal/testmodel/invoice.go`, add `Items velox.Many[Item]` after `Data []byte` in `Invoice`.

In `internal/testmodel/sample.go`:
- Add `ParentID *int64` after `Blob []byte`.
- Change `Sample`'s comment to `// Sample has a field of every handle kind, and both kinds of relation, declared.`
- Change its `Schema` to:

```go
// Schema names Sample's table and declares both relations' keys.
func (x *Sample) Schema(s *velox.Schema) {
	s.Table("samples")
	s.Many(&x.Lines).ForeignKey(func(l *Line) *int64 { return &l.SampleID })
	s.One(&x.Parent).References(&x.ParentID)
}
```

Create `relation_test.go`:

```go
package velox

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

// The relation fixtures: a company has employees, each with a manager.
type company struct {
	ID    int64
	Staff Many[employee]
}

func (x *company) Schema(*Schema) {}

var companyMeta MetaOnce[company]

func (*company) VeloxMeta() *Meta[company] {
	return companyMeta.Get(func() Shape[company] {
		return Shape[company]{Columns: []string{"ID"}, Relations: []Relation{RelationTo[employee]("Staff")},
			Addr: func(x *company, i int) any { return &x.ID }}
	})
}

type employee struct {
	ID        int64
	CompanyID int64
	BossID    *int64
	Boss      One[employee]
	Company   One[company]
}

func (x *employee) Schema(s *Schema) { s.One(&x.Boss).References(&x.BossID) }

var employeeMeta MetaOnce[employee]

func (*employee) VeloxMeta() *Meta[employee] {
	return employeeMeta.Get(func() Shape[employee] {
		return Shape[employee]{Columns: []string{"ID", "CompanyID", "BossID"},
			Relations: []Relation{RelationTo[employee]("Boss"), RelationTo[company]("Company")},
			Addr: func(x *employee, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.CompanyID
				case 2:
					return &x.BossID
				}
				return nil
			}}
	})
}

// TestRelationsResolveWithoutDeadlock is half of Review Focus 1.
func TestRelationsResolveWithoutDeadlock(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := CheckEntity[company](); err != nil {
				t.Error(err)
			}
			if err := CheckEntity[employee](); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	co, em := (*company)(nil).VeloxMeta().info, (*employee)(nil).VeloxMeta().info
	for _, c := range []struct {
		owner         *EntityInfo
		rel           int
		target        *EntityInfo
		ownerCol, col string
	}{
		{co, 0, em, "id", "company_id"}, // Many by convention: employee.CompanyID
		{em, 0, em, "boss_id", "id"},    // One, declared References
		{em, 1, co, "company_id", "id"}, // One by convention: employee.CompanyID
	} {
		l, err := c.owner.rels[c.rel].resolve(c.owner)
		if err != nil || l.target != c.target || c.owner.Columns[l.ownerCol].Name != c.ownerCol || l.target.Columns[l.targetCol].Name != c.col {
			t.Errorf("%s.%s: %+v, %v", c.owner.Name, c.owner.rels[c.rel].name, l, err)
		}
	}
}

type keyed struct {
	ID    string
	Lines Many[keyedLine]
}

func (x *keyed) Schema(*Schema) {}

type keyedLine struct {
	ID      int64
	KeyedID int64 // an int64 cannot refer to keyed's string key
}

func (x *keyedLine) Schema(*Schema) {}

var keyedLineMeta MetaOnce[keyedLine]

func (*keyedLine) VeloxMeta() *Meta[keyedLine] {
	return keyedLineMeta.Get(func() Shape[keyedLine] {
		return Shape[keyedLine]{Columns: []string{"ID", "KeyedID"}, Addr: func(*keyedLine, int) any { return nil }}
	})
}

// TestResolveRefusesMismatchedKeys is half of Review Focus 2.
func TestResolveRefusesMismatchedKeys(t *testing.T) {
	x := new(keyed)
	info, err := buildInfo(reflect.TypeFor[keyed](), unsafe.Pointer(x), any(x), []string{"ID"}, []Relation{RelationTo[keyedLine]("Lines")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = info.rels[0].resolve(info)
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "the key types differ: velox.keyed.ID is string and velox.keyedLine.KeyedID is int64") {
		t.Errorf("%v", err)
	}
}

type badRels struct {
	ID    int64
	Lines Many[keyedLine]
	Other One[keyedLine]
}

func (x *badRels) Schema(s *Schema) {
	s.Many(&x.Lines).References(&x.ID)
	s.One(&x.Other).ForeignKey(func(l *keyedLine) *int64 { return &l.KeyedID }).References(&x.ID)
}

func TestRelationDeclarationErrors(t *testing.T) {
	x := new(badRels)
	_, err := buildInfo(reflect.TypeFor[badRels](), unsafe.Pointer(x), any(x), []string{"ID"},
		[]Relation{RelationTo[keyedLine]("Lines"), RelationTo[keyedLine]("Other")})
	for _, want := range []string{
		"References applies to a One relation; a Many relation's key is on its target: use ForeignKey",
		"relation Other declares both ForeignKey and References",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

type lonely struct {
	ID    int64
	Lines Many[keyedLine] // keyedLine has no LonelyID
}

func (x *lonely) Schema(*Schema) {}

func TestMissingConventionalKey(t *testing.T) {
	x := new(lonely)
	info, err := buildInfo(reflect.TypeFor[lonely](), unsafe.Pointer(x), any(x), []string{"ID"}, []Relation{RelationTo[keyedLine]("Lines")})
	if err == nil {
		_, err = info.rels[0].resolve(info)
	}
	if err == nil || !strings.Contains(err.Error(), "velox.keyedLine has no field LonelyID; name one, or declare the key with s.Many(&x.Lines).ForeignKey") {
		t.Errorf("%v", err)
	}
}

func TestHolderLayout(t *testing.T) {
	var o One[int]
	var m Many[int]
	var h holder
	if unsafe.Sizeof(o) != unsafe.Sizeof(h) || unsafe.Sizeof(m) != unsafe.Sizeof(h) ||
		unsafe.Offsetof(o.loaded) != unsafe.Offsetof(h.loaded) || unsafe.Offsetof(m.loaded) != unsafe.Offsetof(h.loaded) {
		t.Fatal("One and Many must have holder's layout")
	}
}
```

The conventional key field is named after the owner's type with its first letter upper-cased (`company` → `CompanyID`), so the unexported test types follow the same rule as exported entities.

In `meta_test.go`:
- `infoOf[E any](cols, rels []string)` turns each name into `Relation{name: n}`, so its callers keep passing names.
- `TestStaleRelations` keeps passing `[]string{"Lines"}`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: Relation`, `undefined: RelationTo`, `s.Many undefined`, `undefined: holder`.

- [ ] **Step 3: Write the library code**

`doc.go`: rename the constant and its comment:

```go
// GeneratedCodeIsVersion2 is referenced by every file velox-gen writes, so
// that generated code and this library fail to compile together when they
// disagree. It is replaced by GeneratedCodeIsVersion3 when generated code must
// change. (Version 2: relations in Shape and Rel handles.)
const GeneratedCodeIsVersion2 = true
```

`meta.go`:

1. In `ColumnInfo`, after `OnUpdate`, add `off uintptr // the field's offset in the entity`.
2. In `EntityInfo`, after `Indexes`, add:

```go
	rels []*relation // in the order of the generated shape
	kit  *kit        // set by MetaOnce.Get (Task 3); nil in tests that call buildInfo
```

3. `Shape.Relations` becomes `Relations []Relation`.
4. `flatten` collects relation fields as `field`s: change its signature to `flatten(t reflect.Type, base uintptr, cols, rels []field) ([]field, []field, error)`, and in the relation case `rels = append(rels, field{name: f.Name, off: base + f.Offset, typ: f.Type})`.
5. `checkShape(entity string, fs, rs []field, cols []string, rels []Relation)` compares names: build `got` from `rs[i].name` and `want` from `rels[i].name`.
6. `buildInfo(t reflect.Type, base unsafe.Pointer, x any, cols []string, rels []Relation)`:
   - after `s.cols` is built, set the offsets: `s.cols[i] = ColumnInfo{…, off: f.off}`;
   - before `applyMixins`, build the relations, so that `Schema` can declare them:

```go
	s.rels = make([]*relation, len(rs))
	for i, r := range rs {
		s.rels[i] = &relation{name: r.name, many: strings.HasPrefix(r.typ.Name(), "Many["), off: r.off, typ: r.typ, target: rels[i].target, ref: -1}
	}
```

   and set `rels: s.rels` in the `EntityInfo` literal.
7. Replace `CheckEntity`:

```go
// CheckEntity builds entity E's metadata and resolves its relations, and
// returns what is wrong: a mistake in its Schema or Mixin methods, a relation
// whose key is missing or mistyped, or generated code that is out of date.
// velox-gen verify calls it for every entity.
func CheckEntity[E any, PE Entity[E]]() error {
	m := PE(nil).VeloxMeta()
	if m.err != nil {
		return m.err
	}
	var errs []error
	for _, r := range m.info.rels {
		if _, err := r.resolve(m.info); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// metaOf returns entity E's Meta, for generic code that cannot name E's
// pointer type in its constraints (a method of Rel[E, T]).
func metaOf[E any]() (*Meta[E], error) {
	p, ok := any((*E)(nil)).(interface{ VeloxMeta() *Meta[E] })
	if !ok {
		return nil, fmt.Errorf("velox: %v is not an entity: run go tool velox-gen", reflect.TypeFor[E]())
	}
	m := p.VeloxMeta()
	return m, m.err
}
```

The `kit` type is declared in Task 3. For now add an empty declaration to `relation.go`, `type kit struct{}`, which Task 3 replaces.

`schema.go`: add `rels []*relation` to `Schema`, and:

```go
// Many returns the to-many relation f, to declare its key. Undeclared, the
// key is the target's field named after this entity: InvoiceID on Line for a
// relation of Invoice.
func (s *Schema) Many[T any](f *Many[T]) *RelDef[T] {
	return &RelDef[T]{s: s, r: s.findRel(unsafe.Pointer(f), reflect.TypeFor[Many[T]]())}
}

// One returns the to-one relation f, to declare its key. Undeclared, the key
// is this entity's field named after the relation (OrgID for a relation Org)
// when there is one, and otherwise the target's field named after this entity.
func (s *Schema) One[T any](f *One[T]) *RelDef[T] {
	return &RelDef[T]{s: s, r: s.findRel(unsafe.Pointer(f), reflect.TypeFor[One[T]]())}
}

// findRel returns the relation p points to, or nil after recording an error.
func (s *Schema) findRel(p unsafe.Pointer, t reflect.Type) *relation {
	if uintptr(p) >= uintptr(s.base) && uintptr(p)-uintptr(s.base) < s.size {
		off := uintptr(p) - uintptr(s.base)
		for _, r := range s.rels {
			if r.off == off && r.typ == t {
				return r
			}
		}
	}
	s.fail("", "a pointer given to Many or One is not a relation field of the entity")
	return nil
}

// RelDef declares the key of one relation.
type RelDef[T any] struct {
	s *Schema
	r *relation // nil when the field pointer was invalid (the error is recorded)
}

// ForeignKey declares the target's column that holds this entity's primary
// key: func(l *Line) *int64 { return &l.InvoiceID }.
func (d *RelDef[T]) ForeignKey[K any](fk func(*T) *K) *RelDef[T] {
	if d.r == nil {
		return d
	}
	if d.r.ref >= 0 {
		d.s.fail(d.r.name, "relation %s declares both ForeignKey and References", d.r.name)
		return d
	}
	d.r.fk = func() (uintptr, reflect.Type) {
		x := new(T)
		return uintptr(unsafe.Pointer(fk(x))) - uintptr(unsafe.Pointer(x)), reflect.TypeFor[K]()
	}
	return d
}

// References declares this entity's column that holds the target's primary
// key, for a One relation to an entity this one belongs to: References(&x.OrgID).
func (d *RelDef[T]) References[V any](p *V) *RelDef[T] {
	if d.r == nil {
		return d
	}
	if d.r.many {
		d.s.fail(d.r.name, "References applies to a One relation; a Many relation's key is on its target: use ForeignKey")
		return d
	}
	if d.r.fk != nil {
		d.s.fail(d.r.name, "relation %s declares both ForeignKey and References", d.r.name)
		return d
	}
	d.r.ref = d.s.find(unsafe.Pointer(p), reflect.TypeFor[V]())
	if d.r.ref < 0 {
		d.r.ref = -2 // the error is recorded; keep resolution from guessing
	}
	return d
}
```

`relation.go`: keep `One`, `Many` and their `Get`, and add:

```go
// holder has the layout of One[T] and Many[T], so that velox's non-generic
// loader can fill a relation field through its offset (TestHolderLayout).
type holder struct {
	v      any
	loaded bool
}

// Relation is one relation field of a generated Shape: its name and how to
// reach its target entity.
type Relation struct {
	name   string
	target func() (*EntityInfo, error)
}

// RelationTo is what velox-gen writes for a relation field whose target is
// entity T. Nothing runs until the relation is first used.
func RelationTo[T any, PT Entity[T]](name string) Relation {
	return Relation{name: name, target: func() (*EntityInfo, error) {
		m := PT(nil).VeloxMeta()
		return m.info, m.err
	}}
}

// relation is a relation field of an entity, resolved on first use.
// Resolving reads the target's EntityInfo, never the target's relations, so
// relations that point at each other, or at their own entity, cannot wait on
// each other.
type relation struct {
	name   string
	many   bool
	off    uintptr      // the field's offset in the owner
	typ    reflect.Type // One[T] or Many[T]
	target func() (*EntityInfo, error)
	ref    int                            // References: the owner's column; -1 none, -2 invalid
	fk     func() (uintptr, reflect.Type) // ForeignKey: the target's field; nil none

	once sync.Once
	link link
	err  error
}

// link is a resolved relation: owner.Columns[ownerCol] equals
// target.Columns[targetCol].
type link struct {
	target    *EntityInfo
	ownerCol  int
	targetCol int
}

func (r *relation) resolve(owner *EntityInfo) (link, error) {
	r.once.Do(func() { r.link, r.err = r.build(owner) })
	return r.link, r.err
}

func (r *relation) build(owner *EntityInfo) (link, error) {
	fail := func(format string, args ...any) (link, error) {
		return link{}, &SchemaError{Entity: owner.Name, Field: r.name, Reason: fmt.Sprintf(format, args...)}
	}
	if r.target == nil {
		return fail("relation %s has no target in the generated code; run go tool velox-gen", r.name)
	}
	t, err := r.target()
	if err != nil {
		return fail("relation %s: its target is invalid: %v", r.name, err)
	}
	l := link{target: t}
	kind := "One"
	if r.many {
		kind = "Many"
	}
	switch {
	case r.ref == -2:
		return fail("relation %s: References names no column", r.name)
	case r.ref >= 0:
		l.ownerCol = r.ref
		if l.targetCol, err = singleKey(t); err != nil {
			return fail("relation %s: %v", r.name, err)
		}
	case r.fk != nil:
		off, typ := r.fk()
		if l.targetCol = columnAt(t, off, typ); l.targetCol < 0 {
			return fail("relation %s: ForeignKey's function does not return a column of %s", r.name, t.Name)
		}
		if l.ownerCol, err = singleKey(owner); err != nil {
			return fail("relation %s: %v", r.name, err)
		}
	default:
		if c, ok := fieldColumn(owner, r.name+"ID"); ok && !r.many {
			l.ownerCol = c
			if l.targetCol, err = singleKey(t); err != nil {
				return fail("relation %s: %v", r.name, err)
			}
			break
		}
		want := typeName(owner.Name) + "ID"
		c, ok := fieldColumn(t, want)
		if !ok {
			return fail("%s has no field %s; name one, or declare the key with s.%s(&x.%s).ForeignKey", t.Name, want, kind, r.name)
		}
		l.targetCol = c
		if l.ownerCol, err = singleKey(owner); err != nil {
			return fail("relation %s: %v", r.name, err)
		}
	}
	oc, tc := owner.Columns[l.ownerCol], t.Columns[l.targetCol]
	if base(oc.Type) != base(tc.Type) {
		return fail("the key types differ: %s.%s is %v and %s.%s is %v", owner.Name, oc.Field, base(oc.Type), t.Name, tc.Field, base(tc.Type))
	}
	if !base(oc.Type).Comparable() {
		return fail("relation %s: a key of type %v cannot be compared", r.name, base(oc.Type))
	}
	return l, nil
}

// singleKey returns an entity's primary key column; relations need one.
func singleKey(in *EntityInfo) (int, error) {
	if len(in.PrimaryKey) != 1 {
		return -1, fmt.Errorf("%s has a composite primary key; a relation needs a single-column key", in.Name)
	}
	return in.PrimaryKey[0], nil
}

// columnAt returns the column of the field at offset off with type typ.
func columnAt(in *EntityInfo, off uintptr, typ reflect.Type) int {
	for i, c := range in.Columns {
		if c.off == off && c.Type == typ {
			return i
		}
	}
	return -1
}

// fieldColumn returns the column of the field named field.
func fieldColumn(in *EntityInfo, field string) (int, bool) {
	for i, c := range in.Columns {
		if c.Field == field {
			return i, true
		}
	}
	return -1, false
}

// base is t without one level of pointer: *int64 and int64 are the same key.
func base(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

// typeName is the type's name without its package, first letter upper-cased:
// "Invoice" for "billing.Invoice" (and "Company" for an unexported company).
func typeName(name string) string {
	n := name[strings.LastIndexByte(name, '.')+1:]
	return strings.ToUpper(n[:1]) + n[1:]
}

// Rel is the handle of a relation of entity E to entity T, written by
// velox-gen: its I is the relation's index in E's shape.
type Rel[E, T any] struct {
	I int32
}
```

Imports for `relation.go`: `fmt`, `reflect`, `strings`, `sync`.

- [ ] **Step 4: Change velox-gen**

`cmd/velox-gen/model.go`:
- `relations []string` becomes `relations []rel`.
- Add the type:

```go
// rel is one relation field of an entity.
type rel struct {
	field  string
	target types.Type // T of velox.One[T] or velox.Many[T]
}
```

`cmd/velox-gen/discover.go`: in `fields`, the relation case becomes:

```go
		case relation(f.Type()):
			w.add(f)
			n := types.Unalias(f.Type()).(*types.Named)
			w.e.relations = append(w.e.relations, rel{field: f.Name(), target: n.TypeArgs().At(0)})
```

`cmd/velox-gen/render.go`:
- The version line names `GeneratedCodeIsVersion2`.
- In the handles struct, after the column lines (both in the type and in the value), write one line per relation `i`. Declare a helper beside `handle`:

```go
	relHandle := func(r rel) string {
		return fmt.Sprintf("%s.Rel[%s, %s]", vx, E, types.TypeString(r.target, im.qualifier))
	}
```

  The struct type gets `fmt.Fprintf(&b, "\t%s %s\n", r.field, relHandle(r))` and the value gets `fmt.Fprintf(&b, "\t%s: %s{I: %d},\n", r.field, relHandle(r), i)`.
- The shape's relation line becomes:

```go
	if len(e.relations) > 0 {
		rs := make([]string, len(e.relations))
		for i, r := range e.relations {
			rs[i] = fmt.Sprintf("%s.RelationTo[%s](%q)", vx, types.TypeString(r.target, im.qualifier), r.field)
		}
		fmt.Fprintf(&b, "\t\tRelations: []%s.Relation{%s},\n", vx, strings.Join(rs, ", "))
	}
```

`gofmt` (`format.Source`) aligns the struct fields.

`cmd/velox-gen/golden_test.go`: the testmodel now writes four files, so change `len(res.write) != 3` to `!= 4` and the message to `want 4 (invoice, item, line, sample)`.

Any fixture test that asserts the old version constant or `Relations: []string{` changes to the new text. List each such change in the report.

- [ ] **Step 5: Regenerate and run everything**

Run:

```bash
go -C cmd/velox-gen test -run TestTestmodelIsReproduced -update . ; \
go generate . && go test -race ./... && go vet ./... && \
go -C cmd/velox-gen test ./... && go -C cmd/velox-gen build -o "$PWD/bin/velox-gen" . && \
bin/velox-gen -check ./... && bin/velox-gen verify ./...
```

Expected:
- All tests `ok`.
- `-check` exits 0.
- `verify` reports 4 entities, 0 with errors. The testmodel's four relations resolve:
  - `Invoice.Items` and `Item.Invoice` by convention;
  - `Sample.Lines` declared through `ForeignKey`;
  - `Sample.Parent` declared through `References`, with a nullable key.

Read the regenerated `invoice_velox.go` and check that it has `Items velox.Rel[Invoice, Item]` and `Relations: []velox.Relation{velox.RelationTo[Item]("Items")}`.

- [ ] **Step 6: Commit**

```bash
git add -A doc.go meta.go schema.go relation.go relation_test.go meta_test.go internal/testmodel cmd/velox-gen
git commit -m "Resolve relations from generated targets; generate Rel handles" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 2: `Has` and `HasWhere`

**Files:**
- Modify: `pred.go`, `relation.go`, `render.go`, `render_cache.go`, `db.go` (`checkColumns`), `query.go`, `write.go`
- Test: `exists_test.go` (package `velox`), `query_test.go` (package `velox_test`)

**Interfaces:**
- Consumes: `relation.resolve`, `link`, `metaOf`, `Rel` (Task 1).
- Produces:
  - ops `opExists` (node: `col` = relation index, `v` = `link`, `kids` = the target's conditions) and `opInvalid` (node: `v` = `error`)
  - `Rel[E, T].Has[_ Lazy[T]]() Pred[E]` and `HasWhere[_ Lazy[T]](ps ...Pred[T]) Pred[E]`
  - `writer.alias string`, `writer.depth int`
  - `func predErr(n node) error`
  - `Query[E].err error`, also on `UpdateQuery` and `DeleteQuery`

- [ ] **Step 1: Write the failing tests**

`exists_test.go`:

```go
package velox

import "testing"

// TestRenderExists is Review Focus 3: the subquery's table is aliased, the
// correlation names the outer table (or the outer alias when nested), and the
// inner conditions are unqualified, so they bind to the innermost table.
func TestRenderExists(t *testing.T) {
	em := (*employee)(nil).VeloxMeta().info
	co := (*company)(nil).VeloxMeta().info
	staff, err := co.rels[0].resolve(co)
	if err != nil {
		t.Fatal(err)
	}
	boss, err := em.rels[0].resolve(em)
	if err != nil {
		t.Fatal(err)
	}
	hasBoss := node{op: opExists, col: 0, v: boss}
	st := &Statement{Op: OpSelect, Entity: co, Columns: []int{0}, Where: []Predicate{{node{op: opExists, col: 0, v: staff,
		kids: []node{{op: opEQ, col: 1, v: int64(7)}, hasBoss}}}}}
	for d, want := range map[Dialect]string{
		Postgres: `SELECT "id" FROM "company" WHERE EXISTS (SELECT 1 FROM "employee" AS "vx1" WHERE "vx1"."company_id" = "company"."id" AND "company_id" = $1 AND EXISTS (SELECT 1 FROM "employee" AS "vx2" WHERE "vx2"."id" = "vx1"."boss_id"))`,
		MySQL:    "SELECT `id` FROM `company` WHERE EXISTS (SELECT 1 FROM `employee` AS `vx1` WHERE `vx1`.`company_id` = `company`.`id` AND `company_id` = ? AND EXISTS (SELECT 1 FROM `employee` AS `vx2` WHERE `vx2`.`id` = `vx1`.`boss_id`))",
	} {
		got, args := render(d, st)
		if got != want || len(args) != 1 {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
		var c renderCache
		c.render(d, st)
		if cached, _ := c.render(d, st); cached != want {
			t.Errorf("%v cached: %s", d, cached)
		}
	}
	self := &Statement{Op: OpDelete, Entity: em, Where: []Predicate{{hasBoss}}}
	if got, _ := render(SQLite, self); got != `DELETE FROM "employee" WHERE EXISTS (SELECT 1 FROM "employee" AS "vx1" WHERE "vx1"."id" = "employee"."boss_id")` {
		t.Errorf("self relation: %s", got)
	}
}

func TestCacheKeepsRelationsApart(t *testing.T) {
	em := (*employee)(nil).VeloxMeta().info
	boss, _ := em.rels[0].resolve(em)
	comp, _ := em.rels[1].resolve(em)
	var c renderCache
	a, _ := c.render(Postgres, &Statement{Op: OpSelect, Entity: em, Where: []Predicate{{node{op: opExists, col: 0, v: boss}}}})
	b, _ := c.render(Postgres, &Statement{Op: OpSelect, Entity: em, Where: []Predicate{{node{op: opExists, col: 1, v: comp}}}})
	if a == b {
		t.Errorf("two relations share SQL: %s", a)
	}
}
```

In `query_test.go`, declare `var It = testmodel.Items` beside the file's `I` (later tasks' tests use it too), and add:

```go
func TestHasThroughTheAPI(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	text, args := db.Query[testmodel.Invoice]().Where(I.Items.HasWhere(It.Qty.GT(2)), velox.Not(I.Items.Has())).Limit(5).SQL()
	want := `SELECT ` + allInvoiceCols + ` FROM "invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id" AND "qty" > $1) AND NOT (EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id")) LIMIT $2`
	if text != want || len(args) != 2 {
		t.Errorf("\n got %s\nwant %s", text, want)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(I.Items.Has()).Exec(ctx); err != nil || !strings.Contains(e.SQL, `DELETE FROM "invoices" WHERE EXISTS`) {
		t.Errorf("%v %s", err, e.SQL)
	}
}

func TestABrokenRelationIsAnError(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	bad := velox.Rel[testmodel.Invoice, testmodel.Item]{I: 9}
	if _, err := db.Query[testmodel.Invoice]().Where(bad.Has()).Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "has no relation 9") {
		t.Errorf("%v", err)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(bad.Has()).Exec(ctx); err == nil {
		t.Error("Update accepted a broken relation")
	}
}
```

`allInvoiceCols` is the quoted, comma-separated column list of `Invoice`. Use the helper `query_test.go` already has for it if there is one; otherwise add the constant, read from `db.Query[testmodel.Invoice]().SQL()` once by hand.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `undefined: opExists`, `I.Items.HasWhere undefined`.

- [ ] **Step 3: Write the implementation**

`pred.go`: add `opExists` and `opInvalid` at the end of the op list, and:

```go
// predErr returns the error a condition carries: a relation that could not be
// resolved when the condition was made.
func predErr(n node) error {
	if n.op == opInvalid {
		return n.v.(error)
	}
	if n.op == opExists {
		return nil // its kids were checked when HasWhere made it
	}
	for _, k := range n.kids {
		if err := predErr(k); err != nil {
			return err
		}
	}
	return nil
}
```

`relation.go`:

```go
// relLink resolves relation i of entity E.
func relLink[E any](i int32) (link, error) {
	m, err := metaOf[E]()
	if err != nil {
		return link{}, err
	}
	if i < 0 || int(i) >= len(m.info.rels) {
		return link{}, fmt.Errorf("velox: %s has no relation %d; run go tool velox-gen", m.info.Name, i)
	}
	return m.info.rels[i].resolve(m.info)
}

// Has is true when the relation holds at least one entity.
func (r Rel[E, T]) Has[_ Lazy[T]]() Pred[E] { return r.HasWhere() }

// HasWhere is true when at least one related entity meets every condition:
// EXISTS (SELECT 1 FROM target WHERE key = owner's key AND ps…).
func (r Rel[E, T]) HasWhere[_ Lazy[T]](ps ...Pred[T]) Pred[E] {
	l, err := relLink[E](r.I)
	if err == nil {
		for _, p := range ps {
			if err = predErr(p.n); err != nil {
				break
			}
		}
	}
	if err != nil {
		return Pred[E]{node{op: opInvalid, v: err}}
	}
	return Pred[E]{node{op: opExists, col: r.I, v: l, kids: nodes(ps)}}
}
```

`render.go`:
- Add `alias string` and `depth int` to `writer`, with the comment `// the alias of the subquery being written, and its nesting`.
- In `pred`, add `case opExists: w.exists(n)`.
- Add the method:

```go
// exists writes EXISTS over a relation. The subquery's table is aliased
// vx<depth>, so it is told apart from the outer table even when they are the
// same table; the correlation names the outer side by its alias, or by its
// table at the top; the target's conditions are unqualified and bind to the
// innermost table.
func (w *writer) exists(n node) {
	l := n.v.(link)
	owner, outer := w.in, w.alias
	w.depth++
	alias := "vx" + strconv.Itoa(w.depth)
	w.str("EXISTS (SELECT 1 FROM ")
	w.in = l.target
	w.table()
	w.str(" AS ")
	w.ident(alias)
	w.str(" WHERE ")
	w.ident(alias)
	w.str(".")
	w.col(int32(l.targetCol))
	w.str(" = ")
	if outer != "" {
		w.ident(outer)
	} else {
		w.in = owner
		w.table()
		w.in = l.target
	}
	w.str(".")
	w.ident(owner.Columns[l.ownerCol].Name)
	w.alias = alias
	for _, k := range n.kids {
		w.str(" AND ")
		w.pred(k)
	}
	w.str(")")
	w.in, w.alias = owner, outer
	w.depth--
}
```

Add `"strconv"` to `render.go`'s imports.

`render_cache.go`: `nodeKey` already writes `op`, `col` (the relation index) and the kids. That is enough, because the outer entity keys the map and the relation index then fixes the target. Add a comment to that effect above `nodeKey`.

`db.go` `checkColumns`: in `walk`, add `case opExists: return nil // its kids are conditions on the target, made by HasWhere` before the default.

`query.go`:
- Add `err error // a condition's error, returned by every way of running the query` to `Query`.
- In `Where`, after appending, add: `if q.err == nil { if err := predErr(p.n); err != nil { q.err = err } }`, checking each `p`.
- In `All`, `Iter`, `Count` and `SQL`, after the `meta.err` check, add the same check of `q.err`:
  - `All` and `Count` return it;
  - `Iter` yields it;
  - `SQL` returns `"", nil`.

`write.go`: `UpdateQuery` and `DeleteQuery` get the same `err` field. Their `Where` records it the same way. `UpdateQuery.statement`, `DeleteQuery.Exec` and `DeleteQuery.SQL` return it after the `meta.err` check.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`, including `TestOneRowAllocationBudget`.

- [ ] **Step 5: Commit**

```bash
git add pred.go relation.go render.go render_cache.go db.go query.go write.go exists_test.go query_test.go
git commit -m "Add Has and HasWhere: EXISTS over relations with aliased subqueries" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 3: Eager loading with `With`

**Files:**
- Modify: `relation.go` (`Rel` load options, `MarshalJSON`), `meta.go` (`kit` built in `Get`), `query.go` (`With`, `All`, `Iter`), `methods_test.go` (exemption)
- Create: `load.go`
- Test: `load_test.go` (package `velox_test`)

**Interfaces:**
- Consumes:
  - `relLink`, `link`, `relation.off`, `relation.many` (Task 1);
  - `predErr` (Task 2);
  - `maxParams` and `SetMaxParams` (plan 3);
  - `run`/`plain`.
- Produces:
  - `type kit struct{ value func(unsafe.Pointer, int) any; scan func(rows) (any, error); join func([]any) (any, []unsafe.Pointer); pick func(any, []int) any; ptr func(any, int) any; empty func() any }`
  - `type loadSpec struct{ rel int32; where []Predicate; order []OrderTerm; with []loadSpec; err error }`
  - `Rel[E, T]` fields `where []Predicate; order []OrderTerm; with []loadSpec; err error`, and methods `Where`, `OrderBy`, `With[U any](r Rel[T, U]) Rel[E, T]`
  - `Query[E].With[T any](r Rel[E, T]) Query[E]`
  - `func loadRelations(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, specs []loadSpec) error`
  - `One[T].MarshalJSON` and `Many[T].MarshalJSON`

- [ ] **Step 1: Write the failing tests**

`load_test.go`:

```go
package velox_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// It is declared in query_test.go (Task 2).

// invoiceRow is a full Invoice row for the fake executor, in column order.
func invoiceRow(id int64) []any {
	return []any{t0, t0, id, int64(1), "N", 1.0, false, nil, nil, []byte("d")}
}

func itemRow(id, invoice int64, sku string, qty int64) []any {
	return []any{id, invoice, sku, qty}
}

// TestWithAttachesToOwners is half of Review Focus 2.
func TestWithAttachesToOwners(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1), itemRow(12, 1, "C", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "invoice_id" IN ($1, $2, $3) ORDER BY "id"`; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
	skus := func(i int) []string {
		items, loaded := invs[i].Items.Get()
		if !loaded || items == nil {
			t.Fatalf("invoice %d: loaded %v, items %v", i, loaded, items)
		}
		var out []string
		for _, it := range items {
			out = append(out, it.SKU)
		}
		return out
	}
	if !reflect.DeepEqual(skus(0), []string{"A", "C"}) || !reflect.DeepEqual(skus(1), []string{"B"}) || len(skus(2)) != 0 {
		t.Errorf("%v %v %v", skus(0), skus(1), skus(2))
	}
}

func TestWithBelongsToSharesAndSkipsNil(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)},
		{invoiceRow(1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().With(It.Invoice).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, okA := items[0].Invoice.Get()
	b, okB := items[1].Invoice.Get()
	if !okA || !okB || a == nil || a != b || a.ID != 1 {
		t.Errorf("%v %v %v %v", a, b, okA, okB)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" IN ($1)`) {
		t.Errorf("keys not deduplicated: %s", e.Log[1])
	}
}

func TestWithNullableKeyLoadsNothing(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{sampleRow(1, nil)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	ss, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
	if err != nil || e.Calls != 1 {
		t.Fatalf("%v, calls %d (a nil key must send nothing)", err, e.Calls)
	}
	if p, loaded := ss[0].Parent.Get(); !loaded || p != nil {
		t.Errorf("%v %v", p, loaded)
	}
}

// TestWithNestedLoopsBack is half of Review Focus 1.
func TestWithNestedLoopsBack(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1)},
		{itemRow(10, 1, "A", 1)},
		{invoiceRow(1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice)).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := invs[0].Items.Get()
	back, loaded := items[0].Invoice.Get()
	if !loaded || back == nil || back.ID != 1 {
		t.Errorf("%v %v", back, loaded)
	}
}

func TestWithFilterAndOrder(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {}}}
	db := velox.OpenFake(velox.MySQL, e)
	if _, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc())).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "SELECT `id`, `invoice_id`, `sku`, `qty` FROM `items` WHERE `invoice_id` IN (?) AND `qty` > ? ORDER BY `sku` DESC"; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
}

// TestWithChunksUnderTheParameterLimit is Review Focus 4.
func TestWithChunksUnderTheParameterLimit(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 3)() // one parameter for the filter, two keys per chunk
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(11, 1, "B", 1), itemRow(10, 1, "A", 1), itemRow(12, 2, "C", 1)},
		{itemRow(13, 3, "D", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0))).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 3 || !strings.Contains(e.Log[1], "IN ($1, $2)") || !strings.Contains(e.Log[2], "IN ($1)") {
		t.Fatalf("%q", e.Log)
	}
	var got []string
	for _, inv := range invs {
		items, _ := inv.Items.Get()
		for _, it := range items {
			got = append(got, it.SKU)
		}
	}
	if !reflect.DeepEqual(got, []string{"B", "A", "C", "D"}) {
		t.Errorf("%v: every row once, in the order the database returned within each owner", got)
	}
}

func TestIterRefusesWith(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	for _, err := range db.Query[testmodel.Invoice]().With(I.Items).Unbounded().Iter(ctx) {
		if err == nil || !strings.Contains(err.Error(), "Iter does not load relations") {
			t.Errorf("%v", err)
		}
	}
}

func TestRelationsInJSON(t *testing.T) {
	type out struct {
		Items velox.Many[testmodel.Item] `json:",omitzero"`
	}
	b, _ := json.Marshal(out{})
	if string(b) != `{}` {
		t.Errorf("not loaded: %s", b)
	}
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 2)}}}
	invs, err := velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(out{Items: invs[0].Items})
	if string(b) != `{"Items":[{"ID":10,"InvoiceID":1,"SKU":"A","Qty":2,"Invoice":null}]}` {
		t.Errorf("loaded: %s", b)
	}
}
```

Notes for the tests:
- **`sampleRow(id, parentID any)`** returns a full `Sample` row in column order, with `ParentID` set to `parentID`. Read `sample_velox.go` for the column order and write it beside `itemRow`.
- **`invoiceRow`** must match `Invoice`'s columns in order. Check against `invoice_velox.go` and adjust the literal if the order differs.
- **The fake's `assign`** must accept `nil` for a pointer field (`PaidAt`, `Note`, `ParentID`). If it does not, extend it to set the field to its zero value for `nil`.
- **JSON of a not-loaded `One`.** In the expected JSON above, `Invoice` (a not-loaded `One` without `omitzero`) marshals as `null`.

`FakeExec` gains `Results [][][]any`. Each query call takes the next result when there is one, and otherwise falls back to `Rows`. Add it to `export_test.go`:

```go
	Results [][][]any // the rows of successive queries; Rows once they run out
```

In `query`, use the following, under the mutex, instead of `data: e.Rows`:

```go
	data := e.Rows
	if len(e.Results) > 0 {
		data, e.Results = e.Results[0], e.Results[1:]
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `I.Items.With undefined`, `db.Query[…]().With undefined`, `unknown field Results`.

- [ ] **Step 3: Write the implementation**

`relation.go`:

Replace `type kit struct{}` with the real kit:

```go
// kit is the code velox needs to handle an entity whose type it does not
// know at the point of use: loading a relation to it. MetaOnce.Get builds it,
// so it is compiled once, in the entity's own package.
type kit struct {
	value func(p unsafe.Pointer, col int) any          // the value of column col of the entity at p
	scan  func(r rows) (any, error)                    // every row, into a new []E
	join  func(parts []any) (any, []unsafe.Pointer)    // one []E of the parts, and a pointer to each element
	pick  func(all any, idx []int) any                 // a new []E of all's elements at idx
	ptr   func(all any, i int) any                     // &all[i], as *E
	empty func() any                                   // []E{}
}
```

Extend `Rel`, and add its load options:

```go
// Rel is the handle of a relation of entity E to entity T, written by
// velox-gen: I is the relation's index in E's shape. Passed to Query.With it
// loads the relation; its Where, OrderBy and With say what to load. Like a
// Query it is a value: each method returns a new Rel.
type Rel[E, T any] struct {
	I     int32
	where []Predicate
	order []OrderTerm
	with  []loadSpec
	err   error
}

// Where returns the relation loading only the related entities that meet
// every condition.
func (r Rel[E, T]) Where[_ Lazy[T]](ps ...Pred[T]) Rel[E, T] {
	r.where = slices.Clip(r.where)
	for _, p := range ps {
		if r.err == nil {
			r.err = predErr(p.n)
		}
		r.where = append(r.where, Predicate(p))
	}
	return r
}

// OrderBy returns the relation loading its entities in this order within
// each owner. The default is the target's primary key.
func (r Rel[E, T]) OrderBy[_ Lazy[T]](os ...Order[T]) Rel[E, T] {
	r.order = slices.Clip(r.order)
	for _, o := range os {
		r.order = append(r.order, o.t)
	}
	return r
}

// With returns the relation also loading a relation of each loaded entity.
func (r Rel[E, T]) With[U any](next Rel[T, U]) Rel[E, T] {
	r.with = append(slices.Clip(r.with), next.spec())
	return r
}

// spec is the relation's load options, without types.
func (r Rel[E, T]) spec[_ Lazy[T]]() loadSpec {
	return loadSpec{rel: r.I, where: r.where, order: r.order, with: r.with, err: r.err}
}

// MarshalJSON writes the related entity, or null when there is none or it
// was not loaded. Tag the field json:",omitzero" to leave a relation that was
// not loaded out. (An ordinary method on a generic type, against design §3
// rule 1, because encoding/json looks for it by name; its body is one call.)
func (o One[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.v) }

// MarshalJSON writes the related entities, or null when they were not
// loaded; see One.MarshalJSON.
func (m Many[T]) MarshalJSON() ([]byte, error) { return json.Marshal(m.v) }
```

Imports: add `encoding/json`, `slices`, `unsafe`.

`meta.go`, in `MetaOnce.Get`, after `o.m = &Meta[E]{…}`:

```go
		if info != nil && sh.Addr != nil {
			info.kit = kitOf(info, sh.Addr)
		}
```

and add:

```go
// kitOf builds entity E's kit from its scan plan.
func kitOf[E any](info *EntityInfo, addr func(*E, int) any) *kit {
	m := &Meta[E]{info: info, addr: addr}
	return &kit{
		value: func(p unsafe.Pointer, col int) any {
			return reflect.ValueOf(addr((*E)(p), col)).Elem().Interface()
		},
		scan: func(r rows) (any, error) {
			dst := make([]any, len(info.Columns))
			var part []E
			for r.Next() {
				part = append(part, *new(E))
				if err := scanRow(r, m, nil, dst, &part[len(part)-1]); err != nil {
					return nil, err
				}
			}
			return part, nil
		},
		join: func(parts []any) (any, []unsafe.Pointer) {
			var all []E
			for _, p := range parts {
				all = append(all, p.([]E)...)
			}
			ptrs := make([]unsafe.Pointer, len(all))
			for i := range all {
				ptrs[i] = unsafe.Pointer(&all[i])
			}
			return all, ptrs
		},
		pick: func(all any, idx []int) any {
			s := all.([]E)
			out := make([]E, len(idx))
			for i, j := range idx {
				out[i] = s[j]
			}
			return out
		},
		ptr:   func(all any, i int) any { return &all.([]E)[i] },
		empty: func() any { return []E{} },
	}
}
```


`load.go`:

```go
package velox

import (
	"context"
	"fmt"
	"reflect"
	"unsafe"
)

// loadSpec is what Query.With loads: relation rel of the query's entity,
// filtered, ordered, and with relations of its own.
type loadSpec struct {
	rel   int32
	where []Predicate
	order []OrderTerm
	with  []loadSpec
	err   error
}

// keyOf returns a key value as a map key: a pointer is followed, and a nil
// pointer is no key.
func keyOf(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		return rv.Elem().Interface(), true
	}
	return v, true
}

// loadRelations loads each spec's relation for the owners at ptrs.
func loadRelations(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, specs []loadSpec) error {
	for _, sp := range specs {
		if err := loadOne(ctx, db, owner, ptrs, sp); err != nil {
			return err
		}
	}
	return nil
}

func loadOne(ctx context.Context, db *DB, owner *EntityInfo, ptrs []unsafe.Pointer, sp loadSpec) error {
	if sp.err != nil {
		return sp.err
	}
	if int(sp.rel) < 0 || int(sp.rel) >= len(owner.rels) {
		return fmt.Errorf("velox: %s has no relation %d; run go tool velox-gen", owner.Name, sp.rel)
	}
	r := owner.rels[sp.rel]
	l, err := r.resolve(owner)
	if err != nil {
		return err
	}
	t := l.target
	if owner.kit == nil || t.kit == nil {
		return fmt.Errorf("velox: %s: relation %s: the entities have no scan plan; run go tool velox-gen", owner.Name, r.name)
	}
	// The distinct keys, in the order the owners have them; and each owner's key.
	keys := make([]any, 0, len(ptrs))
	ownerKey := make([]any, len(ptrs))
	seen := make(map[any]bool, len(ptrs))
	for i, p := range ptrs {
		k, ok := keyOf(owner.kit.value(p, l.ownerCol))
		if !ok {
			continue
		}
		ownerKey[i] = k
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	// Every owner is loaded, even one with no key or no related row.
	byKey := make(map[any][]int, len(keys)) // key → indexes into all
	var all any
	var targets []unsafe.Pointer
	if len(keys) > 0 {
		if all, targets, err = fetch(ctx, db, t, l, sp, keys); err != nil {
			return fmt.Errorf("velox: %s: loading %s: %w", owner.Name, r.name, err)
		}
		for i, p := range targets {
			if k, ok := keyOf(t.kit.value(p, l.targetCol)); ok {
				byKey[k] = append(byKey[k], i)
			}
		}
		if len(sp.with) > 0 {
			if err := loadRelations(ctx, db, t, targets, sp.with); err != nil {
				return err
			}
		}
	}
	for i, p := range ptrs {
		h := (*holder)(unsafe.Add(p, r.off))
		idx := byKey[ownerKey[i]]
		switch {
		case r.many && all != nil:
			h.v = t.kit.pick(all, idx)
		case r.many:
			h.v = t.kit.empty() // no keys, so nothing was fetched: still loaded, and empty
		case len(idx) > 0:
			h.v = t.kit.ptr(all, idx[0])
		default:
			h.v = nil
		}
		h.loaded = true
	}
	return nil
}
```

`fetch` runs the relation's queries, one per chunk of keys:

```go
// fetch reads the related rows for keys, in chunks that keep each statement
// under the dialect's parameter limit, ordered within each owner as asked
// (by the target's primary key when nothing is asked).
func fetch(ctx context.Context, db *DB, t *EntityInfo, l link, sp loadSpec, keys []any) (any, []unsafe.Pointer, error) {
	order := sp.order
	if len(order) == 0 {
		for _, k := range t.PrimaryKey {
			order = append(order, OrderTerm{Column: k})
		}
	}
	fixed := 0 // the parameters of the filter
	for _, p := range sp.where {
		w := writer{d: db.d, in: t, argsOnly: true}
		w.pred(p.n)
		fixed += len(w.args)
	}
	per := max(1, maxParams[db.d]-fixed)
	var parts []any // each chunk's []T
	for start := 0; start < len(keys); start += per {
		chunk := keys[start:min(start+per, len(keys))]
		st := Statement{Op: OpSelect, Entity: t, Unbounded: true, InTx: db.inTx, OrderBy: order,
			Where: append([]Predicate{{node{op: opIn, col: int32(l.targetCol), v: chunk}}}, sp.where...)}
		var part any
		read := func(r rows) (err error) {
			part, err = t.kit.scan(r)
			return err
		}
		var err error
		if db.hooked() {
			err = db.run(ctx, &st, read, nil)
		} else {
			err = db.plain(ctx, &st, read, nil)
		}
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
	}
	all, ptrs := t.kit.join(parts) // pointers into the one slice pick and ptr index
	return all, ptrs, nil
}
```

`query.go`:
- Add `loads []loadSpec` to `Query`, with `With`:

```go
// With returns the query also loading relation r of every entity it returns,
// with one more query per relation (chunked under the dialect's parameter
// limit). Chain With for several relations; r's With loads nested ones.
// Nothing is ever loaded lazily.
func (q Query[E]) With[T any](r Rel[E, T]) Query[E] {
	q.loads = append(slices.Clip(q.loads), r.spec())
	return q
}
```

- `All`: after the rows are read without error, when `len(q.loads) > 0`:

```go
	if len(q.loads) > 0 && len(out) > 0 {
		ptrs := make([]unsafe.Pointer, len(out))
		for i := range out {
			ptrs[i] = unsafe.Pointer(&out[i])
		}
		if err := loadRelations(ctx, q.db, q.meta.info, ptrs, q.loads); err != nil {
			return nil, err
		}
	}
```

- `Iter`: when `len(q.loads) > 0`, yield `fmt.Errorf("velox: %s: Iter does not load relations; use All", q.meta.info.Name)` and return.
- `Count`: ignores `loads`.

`methods_test.go`: its check exempts methods named `column`. Also exempt `MarshalJSON` on `One` and `Many`, with a comment pointing at spec §3.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`, including `TestOneRowAllocationBudget`, `TestHolderLayout` and `TestMethodsOfGenericTypesAreGeneric`.

- [ ] **Step 5: Commit**

```bash
git add relation.go meta.go query.go load.go load_test.go methods_test.go export_test.go
git commit -m "Load relations with With: chunked, nested, filtered, ordered" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 4: Keyset pagination, the parameter limit, and `EQ(nil)`

**Files:**
- Modify: `query.go` (`After`), `db.go` (`exec`), `render.go`, `render_cache.go`
- Test: `after_test.go` (package `velox_test`), `render_test.go`

**Interfaces:**
- Consumes: `fieldValue` (plan 3), `maxParams`, `node` ops.
- Produces:
  - `Query[E].After[_ Lazy[E]](x *E) Query[E]`
  - `func (q Query[E]) prepare[_ Lazy[E]]() (Statement, error)`, used by `All`, `Iter`, `Count` and `SQL`
  - the parameter-limit error
  - `isNilArg`

- [ ] **Step 1: Write the failing tests**

`after_test.go`:

```go
package velox_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestAfter(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	last := &testmodel.Invoice{ID: 7, Total: 2.5}
	text, args := db.Query[testmodel.Invoice]().OrderBy(I.Total.Desc(), I.ID.Asc()).After(last).Limit(10).SQL()
	if !strings.HasSuffix(text, ` WHERE ("total" < $1 OR ("total" = $2 AND "id" > $3)) ORDER BY "total" DESC, "id" LIMIT $4`) {
		t.Errorf("%s", text)
	}
	if len(args) != 4 || args[0] != 2.5 || args[1] != 2.5 || args[2] != int64(7) {
		t.Errorf("%v", args)
	}
	text, _ = db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).After(last).Limit(10).SQL()
	if !strings.Contains(text, `WHERE "paid" = $1 AND ("id" > $2) ORDER BY "id"`) {
		t.Errorf("%s", text)
	}
}

// TestAfterRefusesUnsafeOrderings is Review Focus 5.
func TestAfterRefusesUnsafeOrderings(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	last := &testmodel.Invoice{ID: 1}
	for name, c := range map[string]struct {
		q    velox.Query[testmodel.Invoice]
		want string
	}{
		"no order":   {db.Query[testmodel.Invoice]().After(last), "After needs an OrderBy"},
		"not unique": {db.Query[testmodel.Invoice]().OrderBy(I.Total.Asc()).After(last), "After needs an OrderBy that includes the primary key or a unique index"},
		"nullable":   {db.Query[testmodel.Invoice]().OrderBy(I.PaidAt.Asc(), I.ID.Asc()).After(last), "After cannot page over the nullable column paid_at"},
	} {
		_, err := c.q.Limit(10).All(ctx)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A unique index other than the primary key will do.
	if _, err := db.Query[testmodel.Invoice]().OrderBy(I.OrgID.Asc(), I.Number.Asc()).After(last).Limit(10).All(ctx); err != nil {
		t.Errorf("unique index: %v", err)
	}
}

func TestParameterLimit(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 3)()
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Invoice]().Where(I.ID.In(1, 2, 3, 4)).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "the statement has 5 parameters; postgres allows 3") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}
```

Use `Dialect.String()`'s actual spelling for "postgres" in the expected message; read `dialect.go`.

In `render_test.go`, add cases to `renderCases()`:

```go
		{"eq nil", func() *Statement { return stmt(Predicate{node{op: opEQ, col: 9, v: nil}}, Predicate{node{op: opNEQ, col: 9, v: []byte(nil)}}) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "data" IS NULL AND "data" IS NOT NULL`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `data` IS NULL AND `data` IS NOT NULL",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "data" IS NULL AND "data" IS NOT NULL`,
		}, nil},
```

Use the column index of `data` in `invInfo`. `stmt` takes predicates; follow its existing signature. Use the file's own way of expressing "no args" if `nil` does not match how `argsEqual` treats an empty slice.

In `render_cache_test.go`, assert that `EQ(nil)` and `EQ(1)` on the same column get different SQL from one cache.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to build: `After undefined`.

- [ ] **Step 3: Write the implementation**

`query.go`: add `after *E // After's row, copied` to `Query`, and:

```go
// After returns the query continuing after x, the last row of the previous
// page: keyset pagination, which unlike Offset neither skips nor repeats rows
// when rows are added between pages, and costs the same on every page. The
// query's OrderBy must include every column of the primary key or of a
// unique index, and no nullable column.
func (q Query[E]) After[_ Lazy[E]](x *E) Query[E] {
	c := *x
	q.after = &c
	return q
}

// prepare is the statement a run sends: the query's, with After's condition.
func (q Query[E]) prepare[_ Lazy[E]]() (Statement, error) {
	st := cloneStatement(&q.st)
	if q.after == nil {
		return st, nil
	}
	in := q.meta.info
	if len(st.OrderBy) == 0 {
		return st, fmt.Errorf("velox: %s: After needs an OrderBy", in.Name)
	}
	ordered := make(map[int]bool, len(st.OrderBy))
	for _, o := range st.OrderBy {
		if in.Columns[o.Column].Nullable {
			return st, fmt.Errorf("velox: %s: After cannot page over the nullable column %s", in.Name, in.Columns[o.Column].Name)
		}
		ordered[o.Column] = true
	}
	covers := func(cols []int) bool {
		for _, c := range cols {
			if !ordered[c] {
				return false
			}
		}
		return len(cols) > 0
	}
	unique := covers(in.PrimaryKey)
	for _, ix := range in.Indexes {
		unique = unique || (ix.Unique && covers(ix.Columns))
	}
	if !unique {
		return st, fmt.Errorf("velox: %s: After needs an OrderBy that includes the primary key or a unique index", in.Name)
	}
	// (o1 > v1) OR (o1 = v1 AND o2 > v2) OR …, with < for descending columns.
	var or []node
	for i, o := range st.OrderBy {
		var and []node
		for _, prev := range st.OrderBy[:i] {
			and = append(and, node{op: opEQ, col: int32(prev.Column), v: fieldValue(q.meta, q.after, prev.Column)})
		}
		cmp := opGT
		if o.Desc {
			cmp = opLT
		}
		and = append(and, node{op: cmp, col: int32(o.Column), v: fieldValue(q.meta, q.after, o.Column)})
		if len(and) == 1 {
			or = append(or, and[0])
		} else {
			or = append(or, node{op: opAnd, kids: and})
		}
	}
	st.Where = append(st.Where, Predicate{node{op: opOr, kids: or}})
	return st, nil
}
```

Use `prepare` in `All`, `Iter`, `Count` and `SQL` instead of `cloneStatement(&q.st)`, returning or yielding its error. `Count` applies `After`'s condition too, because it is a condition.

`db.go`, at the top of `exec`:

```go
	if lim := maxParams[db.d]; len(st.Args) > lim {
		return fmt.Errorf("velox: %s: the statement has %d parameters; %s allows %d", st.Entity.Name, len(st.Args), db.d, lim)
	}
```

`render.go`: add

```go
// isNilArg reports a value that SQL compares with IS NULL, not =.
func isNilArg(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []byte:
		return x == nil
	}
	return false
}
```

In `pred`, before the comparison case, add:

```go
	case n.op == opEQ && isNilArg(n.v), n.op == opNEQ && isNilArg(n.v):
```

The `switch` is on `n.op`, not a tagless switch. So restructure: at the top of `pred`, before the switch:

```go
	if (n.op == opEQ || n.op == opNEQ) && isNilArg(n.v) {
		w.col(n.col)
		if n.op == opEQ {
			w.str(" IS NULL")
		} else {
			w.str(" IS NOT NULL")
		}
		return
	}
```

`render_cache.go` `nodeKey`: after writing `col`, for `opEQ`/`opNEQ` append `flag(isNilArg(n.v))`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./...`
Expected: all `ok`, including `TestOneRowAllocationBudget`: `prepare` without `After` is the old `cloneStatement`.

- [ ] **Step 5: Commit**

```bash
git add query.go db.go render.go render_cache.go after_test.go render_test.go render_cache_test.go
git commit -m "Add After for keyset pagination; refuse statements over the parameter limit; EQ(nil) is IS NULL" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

### Task 5: Relations on real databases, spec and records

**Files:**
- Modify: `internal/itest/itest_test.go` (items table and fixture), `docs/history/specs/2026-10-14-core-design.md`, `docs/history/plans/2026-10-14-core-1-followups.md`
- Create: `internal/itest/relations_test.go`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Add the items table**

In `internal/itest/itest_test.go`, every target creates and fills an `items` table beside `invoices`:
- `setup` drops `items` and `invoices`, creates both, and inserts the invoices as now, plus these items as `(id, invoice_id, sku, qty)`: `(1, 1, 'A', 2)`, `(2, 1, 'B', 0)`, `(3, 2, 'C', 5)`.
- The DDL per dialect, as a second DDL string on `target`:
  - PostgreSQL: `CREATE TABLE items (id bigint PRIMARY KEY, invoice_id bigint NOT NULL, sku text NOT NULL, qty bigint NOT NULL)`;
  - MySQL: `CREATE TABLE items (id BIGINT PRIMARY KEY, invoice_id BIGINT NOT NULL, sku VARCHAR(64) NOT NULL, qty BIGINT NOT NULL)`;
  - SQLite: `CREATE TABLE items (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL, sku TEXT NOT NULL, qty INTEGER NOT NULL)`.

- [ ] **Step 2: Write the integration tests**

`internal/itest/relations_test.go`:

```go
package itest

import (
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var It = testmodel.Items

func TestWith(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		invs, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).
			With(I.Items.Where(It.Qty.GTE(0)).OrderBy(It.SKU.Desc()).With(It.Invoice)).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int64][]string{1: {"B", "A"}, 2: {"C"}, 3: nil, 4: nil, 5: nil}
		for _, inv := range invs {
			items, loaded := inv.Items.Get()
			if !loaded || items == nil {
				t.Fatalf("invoice %d: loaded %v %v", inv.ID, loaded, items)
			}
			var skus []string
			for _, it := range items {
				skus = append(skus, it.SKU)
				back, ok := it.Invoice.Get()
				if !ok || back == nil || back.ID != inv.ID {
					t.Errorf("item %d: its invoice %v %v", it.ID, back, ok)
				}
			}
			wantStrings(t, skus, want[inv.ID])
		}
	})
}

func TestHas(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		ids := func(ps ...velox.Pred[testmodel.Invoice]) []int64 {
			got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, inv := range got {
				out = append(out, inv.ID)
			}
			return out
		}
		wantIDs(t, ids(I.Items.Has()), []int64{1, 2})
		wantIDs(t, ids(I.Items.HasWhere(It.Qty.GT(3))), []int64{2})
		wantIDs(t, ids(velox.Not(I.Items.Has())), []int64{3, 4, 5})
		wantIDs(t, ids(I.Items.HasWhere(It.Invoice.HasWhere(I.Number.EQ("INV-2")))), []int64{2})
		n, err := tg.db.Update[testmodel.Invoice]().Set(I.Note.SetNull()).Where(velox.Not(I.Items.Has())).Exec(ctx)
		if err != nil || n != 3 {
			t.Errorf("update with EXISTS: %d, %v", n, err)
		}
	})
}

func TestAfterPages(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		q := tg.db.Query[testmodel.Invoice]().OrderBy(I.Total.Desc(), I.ID.Asc()).Limit(2)
		var got []int64
		var last *testmodel.Invoice
		for range 4 {
			page := q
			if last != nil {
				page = q.After(last)
			}
			rows, err := page.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				got = append(got, r.ID)
			}
			last = &rows[len(rows)-1]
		}
		wantIDs(t, got, []int64{5, 4, 3, 2, 1})
	})
}
```

`wantIDs` takes `[]int64` in its existing form. If its signature differs, adapt the calls, not the expectations.

- [ ] **Step 3: Run the full mirror**

Run: `scripts/ci-local.sh 2>&1 | tail -25; docker ps --format '{{.Names}}' | grep velox2-ci || echo none`
Expected:
- Everything passes on `/sqlite`, `/postgres-pgx`, `/postgres-sql` and `/mysql`.
- `velox-gen -check` and `verify` pass, with 4 entities and 0 errors.
- The container check prints `none`.

When a test fails on a real database, find the root cause:
- A velox bug gets a minimal fix and a unit regression test.
- An expectation that contradicts the database's documented behavior is corrected, with evidence.

Record either in the report. Never weaken an assertion.

- [ ] **Step 4: Update the spec and the follow-ups record**

`docs/history/specs/2026-10-14-core-design.md`:
- **§1 rule 7.** Replace the foreign-key bullet with:

  > - Keys, by convention:
  >   - **To-one:** a One relation named R uses the owner's field `RID` when there is one: the owner belongs to the target, whose primary key RID holds.
  >   - **Otherwise:** the target holds the key in a field named after the owner: `InvoiceID` on `Line` for a relation of `Invoice`.
  > - **Declared keys:** `s.Many(&x.Lines).ForeignKey(func(l *Line) *int64 { return &l.InvoiceID })` names the target's column, and `s.One(&x.Org).References(&x.OrgID)` the owner's.
  > - **Key types:** a relation joins one column to a single-column primary key of the same Go type (ignoring one level of pointer).
  > - **When a mistake surfaces:** at the relation's first use, and in CI through `velox-gen verify`.

- **§1 code example.** Change `s.Many(&x.Lines).ForeignKey(…)` if it differs from this API. It does not.
- **§2.**
  - The generated example uses `velox.GeneratedCodeIsVersion2`.
  - The handles are `Lines velox.Rel[Invoice, Line]` and `Org velox.Rel[Invoice, identity.Org]`.
  - The shape is `Relations: []velox.Relation{velox.RelationTo[Line]("Lines"), velox.RelationTo[identity.Org]("Org")}`.
  - In the handle table, the relation row's handle is `Rel` (one type for both kinds), adding `Has` `HasWhere` `Where` `OrderBy` `With`.
- **§3 rule 1.** After the `column` exception, add: "`One[T]` and `Many[T]` also have `MarshalJSON`, an ordinary method because `encoding/json` finds it by name; its body is one call. Tag relation fields `json:",omitzero"` to leave unloaded ones out."
- **§4.3.** Change the `With` bullet to:

  > - `With` loads a relation with one extra query per relation, `WHERE key IN (…)`, chunked under each dialect's parameter limit.
  >   - Chain `With` for several relations; a relation's own `With` loads nested ones.
  >   - Related entities come in the order given by the relation's `OrderBy`, by default the target's primary key.
  >   - A to-one entity can be shared by several owners.
  >   - `Iter` does not load relations.
  >   - Nothing is ever loaded lazily.

  Also change the keyset bullet to:

  > - Keyset pagination: `After(lastRow)` continues after the previous page's last row. The `OrderBy` must include the primary key or a unique index, and no nullable column.
  > - Offset pagination is `Offset(n)`.
  > - A statement with more parameters than the dialect allows is refused before it is sent.
  > - `EQ(nil)` and `NEQ(nil)` render `IS NULL` and `IS NOT NULL`.

`docs/history/plans/2026-10-14-core-1-followups.md`:
- Delete the two "Plan 4 (relations)" bullets, which are resolved:
  - IN chunking: `With` chunks, and a too-long statement is refused.
  - EQ nil: renders `IS NULL`.
- Add a section "Left by plan 4 (relations)". The plan's final review fills it.

- [ ] **Step 5: Commit**

```bash
git add internal/itest docs
git commit -m "Test relations, Has and After on every database; spec and records" -m "Co-Authored-By: <the model you are> <noreply@anthropic.com>"
```

---

## Spec coverage of this plan

| Spec | Task |
|---|---|
| §1 rule 7: relations, keys by convention or `ForeignKey`, never lazy, unloaded left out of JSON | 1, 3 |
| §2: relation handles, generated `Relations` | 1 |
| §3: rule 1 (generic methods; `MarshalJSON` exception), rule 2 (holders store `any`), rule 4 (nothing at init) | 1, 3 |
| §4.3: `With`, chunked; `Has`/`HasWhere` render `EXISTS`; keyset `After` | 2, 3, 4, 5 |
| Follow-ups: IN lists bounded; `EQ(nil)` | 3, 4 |
