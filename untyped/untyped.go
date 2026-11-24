// Package untyped is velox's API with the entity's type erased: an entity's
// Table, its queries and writes, keyed by column and relation indexes, with
// a row a *E in an any. Code that handles every entity alike is compiled
// once on it, where velox's typed API would be compiled once per entity:
// velox-graphql's runtime, and a mixin's hook that writes rows itself, as
// package softdelete's does.
//
// It builds the statements velox's typed API builds and sends them through
// the same engine, so hooks, policies, scopes, caps and errors are the same
// code. Every entry point checks what it is given (a column index, a
// relation, a row's entity, a value's type) and fails with an error.
//
// For generated code and extensions; outside the 1.0 compatibility
// promise: a minor release may change it, with a changelog line.
package untyped

import (
	"fmt"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// The velox types whose state this package reads hold it as their only
// field, or, for Query, its first: a pointer to one is a pointer to its
// state.
var (
	_ [unsafe.Sizeof(velox.DB{}) - unsafe.Sizeof(engine.DB{})]struct{}                                                  = [0]struct{}{}
	_ [unsafe.Sizeof(velox.Mutation{}) - unsafe.Sizeof(engine.MutationState{})]struct{}                                 = [0]struct{}{}
	_ [unsafe.Sizeof(velox.Meta[struct{}]{}) - unsafe.Sizeof(engine.MetaState[struct{}]{})]struct{}                     = [0]struct{}{}
	_ [unsafe.Sizeof(velox.Query[struct{}]{}) - unsafe.Sizeof(engine.QueryState{}) - unsafe.Sizeof(uintptr(0))]struct{} = [0]struct{}{}
	_ [unsafe.Sizeof(velox.Setter[struct{}]{}) - unsafe.Sizeof(velox.Assignment{})]struct{}                             = [0]struct{}{}
)

// engineOf is db's engine.DB, nil for a nil db.
func engineOf(db *velox.DB) *engine.DB { return (*engine.DB)(unsafe.Pointer(db)) }

// metaOf returns entity E's state, or the error of a type that is not an
// entity.
func metaOf[E any]() (*engine.MetaState[E], error) {
	p, ok := any((*E)(nil)).(interface{ VeloxMeta() *velox.Meta[E] })
	if !ok {
		return nil, fmt.Errorf("velox: %v is not an entity: run go tool velox-gen", engine.ElemType((*E)(nil)))
	}
	m := (*engine.MetaState[E])(unsafe.Pointer(p.VeloxMeta()))
	return m, m.Err
}

// targetOf names entity E as a relation's target, as velox's relations do.
func targetOf[E any]() engine.RelTarget {
	return engine.RelTarget{Get: ownerOf[E], One: engine.ElemType((*velox.One[E])(nil)), Many: engine.ElemType((*velox.Many[E])(nil))}
}

// ownerOf is entity E's metadata.
func ownerOf[E any]() (*engine.EntityInfo, error) {
	m, err := metaOf[E]()
	if err != nil {
		return nil, err
	}
	return m.Info, nil
}

// Table is an entity's metadata and scan plan with the entity's type erased:
// what an untyped query (Query) reads, and what the untyped
// conditions are made with, keyed by column and relation indexes. For
// generated code. Code that handles every entity alike, such as
// velox-graphql's runtime, is compiled once on it, where the typed API would
// be compiled once per entity. TableOf returns an entity's. A row is a *E
// in an any, and every method that takes one checks it is of the Table's
// entity; Rows is a run of them.
//
// The zero Table is no entity: its queries fail.
//
// Its methods:
//
//	Info() *velox.EntityInfo // the entity's metadata; nil with Err
//	Err() error // the metadata's error
//	Field(row any, col int) any // a pointer to column col's field of row; nil for another row
//	Value(row any, col int) any // that field's value
//	New() any // a new zero row, a *E
//	Fill(owner any, edge int, target Table, row any) error // Rel.Fill
//	Filled(owner any, edge int, target Table) (any, bool) // Rel.Filled
//	Pred(col int, op PredOp, v any) velox.Predicate // a condition on column col
//	Has(edge int, target Table, ps ...velox.Predicate) velox.Predicate // Rel.HasWith
//	RelAgg(edge int, target Table, agg Term, ps ...velox.Predicate) Term // Rel.Count, Sum, …: agg of target over each row's related rows
//	Of(edge int, target Table, owners ...any) velox.Predicate // Rel.Of
//	ScopedKey(p velox.Predicate, edge int, target Table) velox.Predicate // p on edge's foreign key, a hidden target's key read as null
//	HasRowFilter() bool // Meta.HasRowFilter
//	Convert(col int, v any) (any, error) // v as column col's Go type, from one of the same kind
//	NewRows(n int) Rows // n new zero rows, for InsertRows
//	SetField(row any, col int, v any) error // sets a field, v converted as Convert does
//	Assign(col int, v any) (velox.Assignment, error) // a handle's Set, untyped; nil is SetNull
//	Validate(row any, cols ...int) error // Validate
//	ValidateSet(set ...velox.Assignment) error // ValidateSet
type Table = engine.Table

// Rows is a run of rows of one entity, one after another in memory: what
// an untyped query returns. RowsOf views them as a []E.
// The zero Rows is empty.
//
// Its methods:
//
//	Len() int // the number of rows
//	At(i int) any // row i, a *E into the rows; it panics out of range
type Rows = engine.TableRows

// PredOp is the operator of an untyped condition on a column (Table.Pred).
type PredOp = engine.PredOp

// The operators of Table.Pred, each that of the column handles' method of
// the same name.
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

	PredEqualFold    = engine.PredEqualFold    // LOWER(column) = LOWER(v); a text column
	PredContainsFold = engine.PredContainsFold // LOWER(column) LIKE LOWER(%v%), v's LIKE characters escaped; a text column

	PredJSONHas   = engine.PredJSONHas   // the JSON array in column holds the element v, a string, a number or a bool; a JSON column
	PredJSONEmpty = engine.PredJSONEmpty // the JSON array in column is []; v nil; a JSON column
)

// TableQuery is a SELECT on the entity of a Table, velox.Query with the
// entity's type erased. It builds the Statement a velox.Query builds,
// through the same pipeline, so the policies, a scope, the hooks, routing and
// the errors are the same; Query starts one, and FromQuery makes one of a
// velox.Query. Like a Query it is a value: each chaining
// method returns a new TableQuery. Its rows are *E in an any, and its
// columns and relations are indexes, as Statement numbers them.
//
// Its methods are Query's of the same names, with columns and relations by
// index and rows as *E in an any:
//
//	Table() Table
//	Where(ps ...velox.Predicate) TableQuery
//	OrderBy(os ...velox.OrderTerm) TableQuery
//	Ordered() bool
//	Limit(n int) TableQuery
//	Offset(n int) TableQuery
//	AllowUnbounded() TableQuery
//	MaxRows(n int) TableQuery
//	ForUpdate() TableQuery
//	ForShare() TableQuery
//	Writable() TableQuery
//	Columns(cs ...int) TableQuery
//	Selects(cs ...int) bool
//	After(row any) TableQuery
//	Join(edge int, target Table) TableQuery
//	LeftJoin(edge int, target Table) TableQuery
//	Select(ts ...Term) TableQuery
//	GroupBy(ts ...Term) TableQuery
//	Having(ps ...velox.Predicate) TableQuery
//	Rows(ctx context.Context) velox.RawRows
//	All(ctx context.Context) (Rows, error)
//	One(ctx context.Context) (any, error)
//	First(ctx context.Context) (any, error)
//	Count(ctx context.Context) (int64, error)
//	Exists(ctx context.Context) (bool, error)
//	SQL(ctx context.Context) (string, []any, error)
//	LoadEach(ctx context.Context, owner Table, edge, each int, owners []any) ([]Rows, error)
//	CountEach(ctx context.Context, owner Table, edge int, owners []any) ([]int64, error)
type TableQuery = engine.TableQuery

// Term is one SQL value of a TableQuery's entity: a column (Col), an
// expression (Calc) or an aggregate of either (Sum, Avg, Min, Max, Count,
// CountDistinct and CountAll): velox.Term with the entity's type erased and
// columns by index. TableQuery.Select reads terms, GroupBy groups by them,
// Expr compares them in Having, and OrderByTerm orders by one. Like the
// typed projection, it is a read of the entity: the Read policy, scope and
// interceptors apply.
type Term = engine.Term

// Col is column col of the query's entity as a Term. An index the entity
// lacks fails the statement.
func Col(col int) Term { return engine.ColumnTerm(int32(col)) }

// Calc is the SQL expression text as a Term, written as velox.Calc's: each ?
// is filled by the next argument, a Term written as it is and any other
// value bound; the text is guarded as velox.Expr's is.
func Calc(text string, args ...any) Term {
	slots, vals := termSlots(args)
	return engine.ExprTerm(text, slots, vals)
}

// Agg marks t, an expression such as Calc("SUM(?)", Col(2)), as an
// aggregate, as velox.Agg does. velox does not read the text.
func Agg(t Term) Term { return engine.AggMarkTerm(t) }

// Expr is velox.Expr with terms for columns: the SQL text as a condition for
// Where or Having, each ? filled by the next argument, a Term written as it
// is and any other value bound.
func Expr(text string, args ...any) velox.Predicate {
	slots, vals := termSlots(args)
	return engine.NewPredicate(engine.ExprNode(text, slots, vals))
}

// ExprRaw is velox.ExprRaw with terms for columns: the raw SQL text as a
// condition, for Where or Having, which may read other tables with no
// policy applied inside it. Each ? is filled by the next argument, a Term
// written qualified and any other value bound. reason must say why.
func ExprRaw(reason, text string, args ...any) velox.Predicate {
	slots, vals := termSlots(args)
	return engine.NewPredicate(engine.ExprRawNode(reason, text, slots, vals))
}

// termSlots classifies the arguments of a Calc or an Expr: a Term fills its ?
// as the term, anything else is bound.
func termSlots(args []any) ([]engine.ExprSlot, []any) {
	slots := make([]engine.ExprSlot, len(args))
	vals := make([]any, 0, len(args))
	for i, a := range args {
		if t, ok := a.(Term); ok {
			slots[i] = engine.ExprSlot{Col: -1, Term: &t}
			continue
		}
		slots[i] = engine.ExprSlot{Col: -1}
		vals = append(vals, a)
	}
	return slots, vals
}

// SetExpr assigns column col the SQL expression text, as velox.SetExpr does:
// each ? is filled by the next argument, a Term written as it is and any
// other value bound. Add it to an update with TableUpdate.Assign.
func SetExpr(col int, text string, args ...any) velox.Assignment {
	return engine.ExprAssignment(col, Calc(text, args...))
}

// CountAll is COUNT(*) as a Term.
func CountAll() Term { return engine.AggTerm(engine.TermCountAll, Term{}) }

// Sum is SUM of t. NULL when no row has a value.
func Sum(t Term) Term { return engine.AggTerm(engine.TermSum, t) }

// Avg is AVG of t. NULL when no row has a value.
func Avg(t Term) Term { return engine.AggTerm(engine.TermAvg, t) }

// Min is MIN of t.
func Min(t Term) Term { return engine.AggTerm(engine.TermMin, t) }

// Max is MAX of t.
func Max(t Term) Term { return engine.AggTerm(engine.TermMax, t) }

// Count is COUNT of t: the rows where it is not NULL.
func Count(t Term) Term { return engine.AggTerm(engine.TermCount, t) }

// CountDistinct is COUNT(DISTINCT t).
func CountDistinct(t Term) Term { return engine.AggTerm(engine.TermCountDistinct, t) }

// OrderByTerm orders by t, descending when desc: for TableQuery.OrderBy. NULL
// sorts as an ordering by a column does. After and a cursor cannot follow it.
func OrderByTerm(t Term, desc bool) velox.OrderTerm { return velox.OrderTerm{Expr: t, Desc: desc} }

// TableInsert is an INSERT of rows of a Table's entity: InsertQuery with the
// entity's type erased and columns by index.
// Insert, InsertRows and InsertBulk start one. It builds the statements an
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
// entity's type erased, its setters by column index.
// Update starts one.
//
// Its methods are UpdateQuery's, and Set, SetNull and Assign:
//
//	Set(col int, v any) TableUpdate // Table.Assign's assignment; a wrong type fails Exec
//	SetNull(col int) TableUpdate
//	Assign(as ...velox.Assignment) TableUpdate // assignments made elsewhere (Assignments); a skipped SetPtr is dropped
//	KeepDefaults(cols ...int) TableUpdate // UpdateQuery.KeepDefaults, by column index
//	Where(ps ...velox.Predicate) TableUpdate
//	All() TableUpdate
//	Exec(ctx context.Context) (int64, error)
//	SQL(ctx context.Context) (string, []any, error)
type TableUpdate = engine.TableUpdate

// TableDelete is a DELETE of a Table's entity: DeleteQuery with the
// entity's type erased. Delete starts one.
//
// Its methods are DeleteQuery's:
//
//	Where(ps ...velox.Predicate) TableDelete
//	All() TableDelete
//	Exec(ctx context.Context) (int64, error)
//	SQL(ctx context.Context) (string, []any, error)
type TableDelete = engine.TableDelete

// TableOf returns entity E's Table. It builds E's metadata on first use, as
// a query on E does; a Table of an entity whose metadata is invalid fails
// its queries with the metadata's error.
func TableOf[E any]() Table {
	m, err := metaOf[E]()
	if m == nil {
		return engine.NewTable(nil, err, nil, nil, nil, engine.RelTarget{})
	}
	return engine.TableOfState(m, targetOf[E])
}

// RowsOf returns r as a []E, sharing its memory. Rows of another entity than
// E give nil.
func RowsOf[E any](r Rows) []E {
	p, n, size, info := engine.RowsData(r)
	if n == 0 {
		return nil
	}
	if m, _ := metaOf[E](); m == nil || m.Info != info || size != unsafe.Sizeof(*new(E)) {
		return nil // rows of another entity
	}
	return unsafe.Slice((*E)(p), n)
}

// MutationTable returns the Table of m's entity, for a hook that makes the
// write itself, such as a soft delete's update (see velox.Mutation).
func MutationTable(m *velox.Mutation) Table {
	return engine.MutationTable((*engine.MutationState)(unsafe.Pointer(m)))
}

// Rewrites reports whether db has a Rewrite hook (velox.WithRewrite). Such
// a hook may point a statement at another table or schema, which raw SQL
// that names an entity's table never sees: an extension that reads the
// tables by name should read through queries instead on such a DB.
func Rewrites(db *velox.DB) bool { return len(engineOf(db).Cfg.Rewrites) > 0 }

// Query starts a query on t's entity: velox's Query, untyped. See
// TableQuery.
func Query(db *velox.DB, t Table) TableQuery { return engine.QueryTable(engineOf(db), t) }

// FromQuery returns q as a TableQuery: the same statement, relations, After
// row and joins.
func FromQuery[E any](q velox.Query[E]) TableQuery {
	s := (*engine.QueryState)(unsafe.Pointer(&q))
	var t Table // the zero Table, of no entity, fails the query
	if s.DB != nil {
		t = TableOf[E]()
	}
	return engine.TableQueryOf(s.DB, t, s.St, s.Err, s.Loads, s.After, s.Joins)
}

// And is true when every p is: velox.And, of untyped conditions.
func And(ps ...velox.Predicate) velox.Predicate { return engine.AndPredicates(ps...) }

// Or is true when any p is: velox.Or, of untyped conditions.
func Or(ps ...velox.Predicate) velox.Predicate { return engine.OrPredicates(ps...) }

// Not is true when p is false: velox.Not, of an untyped condition.
func Not(p velox.Predicate) velox.Predicate { return engine.NotPredicate(p) }

// Insert starts an INSERT of row, a *E of t's entity: velox's Insert,
// untyped. Another row fails the insert. See TableInsert.
func Insert(db *velox.DB, t Table, row any) TableInsert {
	return engine.InsertTable(engineOf(db), t, row)
}

// InsertRows starts an INSERT of every row of rows, of t's entity: velox's
// InsertAll, untyped. Rows of another entity fail the insert. See
// TableInsert.
func InsertRows(db *velox.DB, t Table, rows Rows) TableInsert {
	return engine.InsertRows(engineOf(db), t, rows)
}

// InsertBulk starts an INSERT of the rows of qs, each an Insert of one row
// of t's entity built on db: velox's InsertBulk, untyped. A query of
// another DB or Table, an InsertRows or InsertBulk, or one with its own
// OnConflict fails the insert. See TableInsert.
func InsertBulk(db *velox.DB, t Table, qs ...TableInsert) TableInsert {
	return engine.InsertBulkTable(engineOf(db), t, qs)
}

// Update starts an UPDATE of t's entity: velox's Update, untyped. See
// TableUpdate.
func Update(db *velox.DB, t Table) TableUpdate { return engine.UpdateTable(engineOf(db), t) }

// Delete starts a DELETE of t's entity: velox's Delete, untyped. See
// TableDelete.
func Delete(db *velox.DB, t Table) TableDelete { return engine.DeleteTable(engineOf(db), t) }

// Assignments returns the assignments of set, sharing its memory: what
// TableUpdate.Assign takes. An edge setter's assignment has Column -1, which
// a TableUpdate refuses.
func Assignments[E any](set []velox.Setter[E]) []velox.Assignment {
	return unsafe.Slice((*velox.Assignment)(unsafe.Pointer(unsafe.SliceData(set))), len(set))
}
