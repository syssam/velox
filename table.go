package velox

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// The untyped surface is engine code under these names: velox's own compiled
// code calls none of it, so the bodies it inlines are not carried in velox's
// export data, and a package of entities compiles as it would without it.

// Table is an entity's metadata and scan plan with the entity's type erased:
// what an untyped query (DB.QueryTable) reads, and what the untyped
// conditions are made with, keyed by column and relation indexes. For
// generated code. Code that handles every entity alike, such as
// velox-graphql's runtime, is compiled once on it, where the typed API would
// be compiled once per entity. TableOf returns an entity's. A row is an
// unsafe.Pointer to an E; TableRows is a run of them.
//
// The zero Table is no entity: its queries fail.
//
// Its methods:
//
//	Info() *EntityInfo // the entity's metadata; nil with Err
//	Err() error // the metadata's error
//	Field(p unsafe.Pointer, col int) any // a pointer to column col's field of the row at p
//	Value(p unsafe.Pointer, col int) any // that field's value
//	Ptr(p unsafe.Pointer) any // the row at p as an *E
//	New() unsafe.Pointer // a new zero row
//	Fill(owner unsafe.Pointer, edge int, target Table, row unsafe.Pointer) error // Rel.Fill
//	Filled(owner unsafe.Pointer, edge int, target Table) (unsafe.Pointer, bool) // Rel.Filled
//	Pred(col int, op PredOp, v any) Predicate // a condition on column col
//	Has(edge int, target Table, ps ...Predicate) Predicate // Rel.HasWhere
//	Of(edge int, target Table, owners ...unsafe.Pointer) Predicate // Rel.Of
//	HasRowFilter() bool // Meta.HasRowFilter
//	Convert(col int, v any) (any, error) // v as column col's Go type, from one of the same kind
//	NewRows(n int) TableRows // n new zero rows, for InsertRows
//	SetField(p unsafe.Pointer, col int, v any) error // sets a field, v converted as Convert does
//	Assign(col int, v any) (Assignment, error) // a handle's Set, untyped; nil is SetNull
//	Validate(p unsafe.Pointer, cols ...int) error // Validate
//	ValidateSet(set ...Assignment) error // ValidateSet
type Table = engine.Table

// TableRows is a run of rows of one entity, one after another in memory: what
// an untyped query returns. For generated code. RowsOf views them as a []E.
// The zero TableRows is empty.
//
// Its methods:
//
//	Len() int // the number of rows
//	At(i int) unsafe.Pointer // row i; it panics out of range
type TableRows = engine.TableRows

// PredOp is the operator of an untyped condition on a column (Table.Pred).
// For generated code.
type PredOp = engine.PredOp

// The operators of Table.Pred, each that of the column handles' method of
// the same name. For generated code.
const (
	PredEQ        = engine.PredEQ        // column = v
	PredNEQ       = engine.PredNEQ       // column <> v
	PredIn        = engine.PredIn        // column IN (v...), v a []any; false for none
	PredNotIn     = engine.PredNotIn     // column NOT IN (v...), v a []any; true for none
	PredGT        = engine.PredGT        // column > v
	PredGTE       = engine.PredGTE       // column >= v
	PredLT        = engine.PredLT        // column < v
	PredLTE       = engine.PredLTE       // column <= v
	PredContains  = engine.PredContains  // column LIKE %v%, v's LIKE characters escaped; a text column
	PredHasPrefix = engine.PredHasPrefix // column LIKE v%; a text column
	PredHasSuffix = engine.PredHasSuffix // column LIKE %v; a text column
	PredIsNull    = engine.PredIsNull    // column IS NULL; v nil; a nullable column
	PredNotNull   = engine.PredNotNull   // column IS NOT NULL; v nil; a nullable column
)

// TableQuery is a SELECT on the entity of a Table, Query with the entity's
// type erased. For generated code. It builds the Statement a Query builds,
// through the same pipeline, so the policies, a scope, the hooks, routing and
// the errors are the same; DB.QueryTable starts one, Untyped makes one of a
// Query and Typed a Query of one. Like a Query it is a value: each chaining
// method returns a new TableQuery. Its rows are unsafe.Pointers to the
// entity, and its columns and relations are indexes, as Statement numbers
// them.
//
// Its methods are Query's of the same names, with columns and relations by
// index and rows as unsafe.Pointers:
//
//	Table() Table
//	Where(ps ...Predicate) TableQuery
//	OrderBy(os ...OrderTerm) TableQuery
//	Ordered() bool
//	Limit(n int) TableQuery
//	Offset(n int) TableQuery
//	Unbounded() TableQuery
//	MaxRows(n int) TableQuery
//	ForUpdate() TableQuery
//	ForShare() TableQuery
//	Writable() TableQuery
//	Columns(cs ...int) TableQuery
//	Selects(cs ...int) bool
//	After(p unsafe.Pointer) TableQuery
//	Join(edge int, target Table) TableQuery
//	LeftJoin(edge int, target Table) TableQuery
//	All(ctx context.Context) (TableRows, error)
//	One(ctx context.Context) (unsafe.Pointer, error)
//	First(ctx context.Context) (unsafe.Pointer, error)
//	Count(ctx context.Context) (int64, error)
//	Exists(ctx context.Context) (bool, error)
//	SQL(ctx context.Context) (string, []any, error)
//	LoadEach(ctx context.Context, owner Table, edge, each int, owners []unsafe.Pointer) ([]TableRows, error)
//	CountEach(ctx context.Context, owner Table, edge int, owners []unsafe.Pointer) ([]int64, error)
type TableQuery = engine.TableQuery

// tableState is the part of a Meta that holds its Table, built on the first
// TableOf: an entity that no untyped code reads builds none. It holds the
// Table as any: a Meta that names Table puts Table's methods, and what they
// reference, into the export data of every package of entities
// (internal/repocheck's compile-cost test).
type tableState struct {
	once sync.Once
	t    any // a Table
}

// TableOf returns entity E's Table. For generated code. It builds E's
// metadata on first use, as a query on E does; a Table of an entity whose
// metadata is invalid fails its queries with the metadata's error.
func TableOf[E any]() Table {
	m, err := metaOf[E]()
	if m == nil {
		return engine.NewTable(nil, err, nil, nil, nil, engine.RelTarget{})
	}
	return tableOfMeta(m)
}

// tableOfMeta is m's Table, built on first use.
func tableOfMeta[E any](m *Meta[E]) Table {
	m.tbl.once.Do(func() {
		var typ any
		var addr func(p unsafe.Pointer, col int) any
		if m.err == nil {
			a := m.addr
			addr = func(p unsafe.Pointer, col int) any { return a((*E)(p), col) }
			typ = engine.ElemType((*E)(nil))
		}
		m.tbl.t = engine.NewTable(m.info, m.err, addr, m.nilCols, typ, targetOf[E]())
	})
	t, _ := m.tbl.t.(Table)
	return t
}

// RowsOf returns r as a []E, sharing its memory. For generated code. Rows of
// another entity than E give nil.
func RowsOf[E any](r TableRows) []E {
	p, n, size, info := engine.RowsData(r)
	if n == 0 {
		return nil
	}
	if m, _ := metaOf[E](); m == nil || m.info != info || size != unsafe.Sizeof(*new(E)) {
		return nil // rows of another entity
	}
	return unsafe.Slice((*E)(p), n)
}

// QueryTable starts a query on t's entity: Query, untyped. For generated
// code. See TableQuery.
func (db *DB) QueryTable(t Table) TableQuery { return engine.QueryTable(&db.e, db, t) }

// Untyped returns q as a TableQuery: the same statement, relations, After row
// and joins. For generated code.
func Untyped[E any](q Query[E]) TableQuery {
	var t Table // the zero Table, of no entity, fails the query
	if q.meta != nil {
		t = tableOfMeta(q.meta)
	}
	var e *engine.DB
	if q.db != nil {
		e = &q.db.e
	}
	return engine.TableQueryOf(q.db, e, t, q.st, q.err, q.loads, unsafe.Pointer(q.after), q.joins)
}

// Typed returns q as a Query of E, q's entity: the inverse of Untyped. For
// generated code. A q of another entity gives a query that fails.
func Typed[E any](q TableQuery) Query[E] {
	owner, _, st, loads, after, joins, qerr := engine.TableQueryParts(q)
	db, _ := owner.(*DB)
	m, err := metaOf[E]()
	if m == nil {
		return Query[E]{db: db, meta: &Meta[E]{err: err}}
	}
	out := Query[E]{db: db, meta: m, st: st, err: qerr, loads: loads, after: (*E)(after), joins: joins}
	if info := q.Table().Info(); m.err == nil && info != m.info {
		name := "no entity"
		if info != nil {
			name = info.Name
		}
		out.err = fmt.Errorf("velox: %s: Typed was given a query of %s", m.info.Name, name)
	}
	return out
}

// AndPredicates is true when every p is: And, of untyped conditions. For
// generated code.
func AndPredicates(ps ...Predicate) Predicate { return engine.AndPredicates(ps...) }

// OrPredicates is true when any p is: Or, of untyped conditions. For
// generated code.
func OrPredicates(ps ...Predicate) Predicate { return engine.OrPredicates(ps...) }

// NotPredicate is true when p is false: Not, of an untyped condition. For
// generated code.
func NotPredicate(p Predicate) Predicate { return engine.NotPredicate(p) }

// TableInsert is an INSERT of rows of a Table's entity: InsertQuery with the
// entity's type erased and columns by index. For generated code.
// DB.InsertTable and DB.InsertRows start one. It builds the statements an
// InsertQuery builds, through the same hooks, policies and scope.
//
// Its methods are InsertQuery's:
//
//	OnConflict(cols ...int) TableInsert
//	Update(cols ...int) TableInsert
//	DoNothing() TableInsert
//	Exec(ctx context.Context) error
//	SQL(ctx context.Context) (string, []any, error)
type TableInsert = engine.TableInsert

// TableUpdate is an UPDATE of a Table's entity: UpdateQuery with the
// entity's type erased, its setters by column index. For generated code.
// DB.UpdateTable starts one.
//
// Its methods are UpdateQuery's, and Set, SetNull and Assign:
//
//	Set(col int, v any) TableUpdate // Table.Assign's assignment; a wrong type fails Exec
//	SetNull(col int) TableUpdate
//	Assign(as ...Assignment) TableUpdate // assignments made elsewhere (Assignments)
//	Where(ps ...Predicate) TableUpdate
//	All() TableUpdate
//	Exec(ctx context.Context) (int64, error)
//	SQL(ctx context.Context) (string, []any, error)
type TableUpdate = engine.TableUpdate

// TableDelete is a DELETE of a Table's entity: DeleteQuery with the
// entity's type erased. For generated code. DB.DeleteTable starts one.
//
// Its methods are DeleteQuery's:
//
//	Where(ps ...Predicate) TableDelete
//	All() TableDelete
//	Exec(ctx context.Context) (int64, error)
//	SQL(ctx context.Context) (string, []any, error)
type TableDelete = engine.TableDelete

// InsertTable starts an INSERT of the row at p, of t's entity: Insert,
// untyped. For generated code. See TableInsert.
func (db *DB) InsertTable(t Table, p unsafe.Pointer) TableInsert {
	return engine.InsertTable(&db.e, t, p)
}

// InsertRows starts an INSERT of every row of rows, of t's entity: InsertAll,
// untyped. For generated code. Rows of another entity fail the insert. See
// TableInsert.
func (db *DB) InsertRows(t Table, rows TableRows) TableInsert {
	return engine.InsertRows(&db.e, t, rows)
}

// UpdateTable starts an UPDATE of t's entity: Update, untyped. For generated
// code. See TableUpdate.
func (db *DB) UpdateTable(t Table) TableUpdate { return engine.UpdateTable(&db.e, t) }

// DeleteTable starts a DELETE of t's entity: Delete, untyped. For generated
// code. See TableDelete.
func (db *DB) DeleteTable(t Table) TableDelete { return engine.DeleteTable(&db.e, t) }

// Assignments returns the assignments of set, sharing its memory: what
// TableUpdate.Assign takes. For generated code.
func Assignments[E any](set []Setter[E]) []Assignment {
	return unsafe.Slice((*Assignment)(unsafe.Pointer(unsafe.SliceData(set))), len(set))
}
