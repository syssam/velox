package gqlgraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// Args are a connection's paging arguments, as the client gave them.
//
// first (with or without after) pages forward and last (with or without before)
// backward; with neither, the page is the MaxPage of
// veloxgql.LimitsFromContext(ctx) rows forward, or backward from before when
// before is given. first and last together, after and before together, first
// with before and last with after all fail with ErrBadPagination: keyset paging
// bounds a page on one side only.
//
// Order is the orderBy argument, the order of the nodes before c's key.
// Where is the where argument, WhereOf's.
type Args struct {
	After, Before *veloxgql.Cursor
	First, Last   *int
	Order         []Order
	Where         Where
}

// Where is a connection's where argument: the generated where input, and
// its condition. The callers of an edge's batch share the input
// (reflect.DeepEqual), not the condition, which may hold a function that
// reflect.DeepEqual never finds equal. The zero Where filters nothing.
type Where struct {
	input any
	pred  func(context.Context) (velox.Predicate, error)
}

// WhereOf is the Where of w, a generated where input, whose condition is
// pred(w, ctx), its Predicate method; a nil w filters nothing:
//
//	gqlgraph.WhereOf(where, (*PostWhereInput).Predicate)
func WhereOf[W any](w *W, pred func(*W, context.Context) (velox.Predicate, error)) Where {
	if w == nil {
		return Where{}
	}
	return Where{input: w, pred: func(ctx context.Context) (velox.Predicate, error) { return pred(w, ctx) }}
}

// A Source is where a connection's nodes come from: a query (Query) or an
// edge of a row (Type.Edge), whose connections batch.
type Source interface {
	paginate(ctx context.Context, c *Type, args Args, order []Order) (*Connection, error)
}

// Query is the source of the nodes q returns, for Paginate.
func Query(q untyped.TableQuery) Source { return query{q} }

// query is Query's Source.
type query struct{ q untyped.TableQuery }

func (s query) paginate(ctx context.Context, c *Type, args Args, order []Order) (*Connection, error) {
	return paginate(ctx, s.q, c, args, order)
}

// Type is what the generated code knows of one GraphQL type, as a literal
// it writes once per type: the type's name, its entity's Table, the key's
// columns, the order fields and their columns, the columns a selection reads
// and the fields of its inputs. The type's connections, edges, where filters
// and inputs all take it.
type Type struct {
	// Name is the GraphQL type's name, "Post".
	Name string
	// Table is the entity's Table, velox.TableOf of the entity. It is a
	// function so that nothing runs at start.
	Table func() untyped.Table
	// Key is the primary key's columns, each ASC: Name is the column's
	// name, which a cursor's terms name "key:<column>".
	Key []Column
	// Order is the order fields: Name is the GraphQL enum value, "TITLE".
	Order []Column
	// AlwaysRead is the columns every selection of the type reads: the key
	// and every foreign key on the row. A connection reads the columns of
	// its own order too, which its cursors hold.
	AlwaysRead []int
	// Fields is, by column, the type's field that reads the column, or "".
	Fields []string
	// Edges is, by relation, the type's edge field that reads the
	// relation, or "": an edge reads no column but AlwaysRead, and its field
	// alone batches it (Type.Edge).
	Edges []string
	// WhereFields describes the fields of the type's where input to Predicate,
	// in the order of the input's struct.
	WhereFields []WhereField
	// Inputs is, by column, the field of the type's create and update inputs
	// that sets the column, or "": Validate reports a validator's error on
	// such a column as BAD_USER_INPUT naming the field. CreateInputs and
	// UpdateInputs stand for it in one input when the two inputs differ.
	Inputs, CreateInputs, UpdateInputs []string
	// LenientFilters gives the type's where input entgql's lenient
	// semantics, as velox-graphql's option filters=lenient generates it: an
	// empty In or NotIn list, a false IsNil, NotNil or IsEmpty, and an or of
	// no inputs add nothing, where they otherwise match no node or the
	// negated test; a null entry of an and, an or or a has<Edge>With is left
	// out; a nested input that sets no predicate is an error
	// (BAD_USER_INPUT) in a not, an or or a has<Edge>With, and left out of
	// an and; and MaxFilterWidth does not apply, as entgql has no such
	// limit. MaxFilterDepth does, and a where argument's budget is 10 times
	// MaxFilterNodes, 10000 by default. Without it a where input fails
	// closed, as SQL reads it (ADR 0156).
	LenientFilters bool

	fieldsOnce sync.Once
	fieldCol   map[string]int // Fields and Edges by name: a column, or -1
}

// columns is the columns a selection of the type's fields reads (see
// narrow): AlwaysRead, the column of each order term of the node's own, and
// then the column of each field. It is nil, every column, when a field is
// none of Fields and Edges, such as an application's own.
func (c *Type) columns(fields []string, order []Order) []int {
	c.fieldsOnce.Do(func() {
		c.fieldCol = make(map[string]int, len(c.Fields)+len(c.Edges))
		for col, f := range c.Fields {
			if f != "" {
				c.fieldCol[f] = col
			}
		}
		for _, f := range c.Edges {
			if f != "" {
				c.fieldCol[f] = -1
			}
		}
	})
	cols := slices.Clone(c.AlwaysRead)
	for _, o := range order {
		if col := c.find(o.Field); col != nil && col.Edge == 0 && !slices.Contains(cols, col.Index) {
			cols = append(cols, col.Index)
		}
	}
	for _, f := range fields {
		col, ok := c.fieldCol[f]
		if !ok {
			return nil
		}
		if col >= 0 && !slices.Contains(cols, col) {
			cols = append(cols, col)
		}
	}
	return cols
}

// inputNames is the function that names, by an entity field's Go name, the
// field of the input that sets it: names by column, or "".
func inputNames(t untyped.Table, names []string) func(goField string) string {
	return func(goField string) string {
		info := t.Info()
		if info == nil {
			return ""
		}
		for col, c := range info.Columns {
			if c.Field == goField && col < len(names) {
				return names[col]
			}
		}
		return ""
	}
}

// table is the Table of c's entity: the zero Table, whose queries fail,
// when c names none.
func (c *Type) table() untyped.Table {
	if c.Table == nil {
		return untyped.Table{}
	}
	return c.Table()
}

// Column is a column of a connection's key or of an order field.
type Column struct {
	// Name is the order field, or the key column's name.
	Name string
	// Index is the column's index in its entity: the node's, or the target's
	// of Edge.
	Index int
	// Edge is, for a column of the entity that a to-one relation of the node
	// leads to, the relation's index plus one; 0 for the node's own column.
	// A connection ordered by it reads the relation with a LEFT JOIN, in
	// the page query and in the count, so the order leaves the connection's
	// rows and its count as they are: a row with no related entity, or none
	// that the related entity's Read policy lets the caller read, has NULL
	// for the term, after every value ascending, and its cursor holds null.
	// A cursor's row carries a related entity with the term's value set, or
	// none for null.
	Edge int
	// Target is the relation's target, for an Edge.
	Target func() untyped.Table
	// Count says the order field is the number of the to-many relation
	// Edge's nodes (graphql.OrderField on the edge), not a column; Index is
	// then -1. The count is a subquery that applies the target's Read
	// policy, interceptors and filters (velox's Rel.Count), so a node the
	// caller may not read is not counted. The connection joins nothing for
	// it, so its rows and totalCount are as without the order. A cursor
	// holds null for it: After computes a row's count from its key.
	Count bool
}

// Order is one term of a connection's order, as the client gave it: the
// order field and its direction. The zero direction is ASC, as the SDL's
// default for XOrder.direction.
type Order struct {
	Field     string
	Direction veloxgql.OrderDirection
}

// find is the order field named field; nil for none.
func (c *Type) find(field string) *Column {
	for i := range c.Order {
		if c.Order[i].Name == field {
			return &c.Order[i]
		}
	}
	return nil
}

// term is one term of a connection's order, or one column of its key: the
// field, its direction, and what paging needs of the column.
type term struct {
	field string
	dir   veloxgql.OrderDirection
	col   int
	edge  int           // the relation's index plus one; 0 for the node's own column
	tbl   untyped.Table // the column's entity: the node's, or the relation's target
	base  reflect.Type
	null  bool // the field is a pointer: nil is NULL, which a cursor holds as null
	// A term of a related row (edge) is NULL too when the row has none: the
	// connection reads the relation with a left join.

	// count is, for a Column's Count, the number of the relation's nodes
	// as a term of the node's entity; zero for a column.
	count   untyped.Term
	isCount bool

	// expr is, for an ordering of the query by an expression, that
	// ordering, whose value the page reads beside each row (exprTerm); nil
	// for a column.
	expr *velox.OrderTerm
}

// newTerm is the term of c, in direction dir, on the connection's entity t.
func newTerm(t untyped.Table, c *Column, dir veloxgql.OrderDirection) (term, error) {
	tm := term{field: c.Name, dir: dir, col: c.Index, edge: c.Edge, tbl: t}
	if c.Edge != 0 {
		tm.tbl = c.Target()
	}
	if err := tm.tbl.Err(); err != nil {
		return tm, err
	}
	if c.Count {
		if c.Edge == 0 {
			return tm, fmt.Errorf("veloxgql: the order field %s counts no relation; run go tool velox-gen", c.Name)
		}
		tm.count, tm.isCount = t.RelAgg(c.Edge-1, tm.tbl, untyped.CountAll()), true
		return tm, nil
	}
	info := tm.tbl.Info()
	if c.Index < 0 || c.Index >= len(info.Columns) {
		return tm, fmt.Errorf("veloxgql: %s has no column %d; run go tool velox-gen", info.Name, c.Index)
	}
	ft := velox.GoType(info.Columns[c.Index])
	if ft.Kind() == reflect.Pointer {
		ft, tm.null = ft.Elem(), true
	}
	tm.base = baseType(ft)
	if tm.base == nil {
		return tm, fmt.Errorf("veloxgql: %s.%s: keyset paging cannot continue after a %v", info.Name, info.Columns[c.Index].Field, ft)
	}
	return tm, nil
}

// The base types: velox's standard Go type of a column, which a cursor holds.
var (
	timeType = reflect.TypeFor[time.Time]()
	uuidType = reflect.TypeFor[[16]byte]()
	kindType = map[reflect.Kind]reflect.Type{
		reflect.String: reflect.TypeFor[string](), reflect.Bool: reflect.TypeFor[bool](),
		reflect.Int: reflect.TypeFor[int](), reflect.Int8: reflect.TypeFor[int8](), reflect.Int16: reflect.TypeFor[int16](),
		reflect.Int32: reflect.TypeFor[int32](), reflect.Int64: reflect.TypeFor[int64](),
		reflect.Uint: reflect.TypeFor[uint](), reflect.Uint8: reflect.TypeFor[uint8](), reflect.Uint16: reflect.TypeFor[uint16](),
		reflect.Uint32: reflect.TypeFor[uint32](), reflect.Uint64: reflect.TypeFor[uint64](),
		reflect.Float32: reflect.TypeFor[float32](), reflect.Float64: reflect.TypeFor[float64](),
	}
)

// baseType is the base type of a field of the type ft, the type the
// generated code's term converted it to: a time.Time is one, a 16-byte array
// is a [16]byte, and any other the predeclared type of its kind; nil for a
// type keyset paging does not continue after.
func baseType(ft reflect.Type) reflect.Type {
	switch {
	case ft.Kind() == reflect.Struct && ft.ConvertibleTo(timeType):
		return timeType
	case ft.Kind() == reflect.Array && ft.Len() == 16 && ft.Elem().Kind() == reflect.Uint8:
		return uuidType
	}
	return kindType[ft.Kind()]
}

// order is the term's column ordered ascending, or descending for desc.
// An expression keeps its NULL placement, turned with its direction.
func (t *term) order(desc bool) velox.OrderTerm {
	if t.expr != nil {
		o := *t.expr
		if desc != o.Desc && o.Nulls != 0 {
			o.Nulls = velox.NullsFirst + velox.NullsLast - o.Nulls
		}
		o.Desc = desc
		return o
	}
	if t.isCount {
		return untyped.OrderByTerm(t.count, desc)
	}
	return velox.OrderTerm{Column: t.col, Desc: desc, Rel: int32(t.edge)}
}

// row is the row of the term's column in the connection's row p: p, or its
// related entity; nil when a joined row has none.
func (t *term) row(p any, own untyped.Table) any {
	if t.edge == 0 {
		return p
	}
	r, _ := own.Filled(p, t.edge-1, t.tbl)
	return r
}

// get returns the term's value in the connection's row p as the base type,
// nil for NULL.
func (t *term) get(p any, own untyped.Table) any {
	if t.isCount || t.expr != nil {
		return nil // not in the row: After computes it from the key, or the page reads it beside the row
	}
	r := t.row(p, own)
	if r == nil {
		// The left join found no related row, or the caller may not read
		// it: the term is NULL, as the database orders it.
		return nil
	}
	f := t.tbl.Field(r, t.col)
	// The common types, without reflect.
	switch x := f.(type) {
	case *int64:
		return *x
	case *string:
		return *x
	case *int:
		return *x
	case *time.Time:
		return *x
	case *bool:
		return *x
	case *float64:
		return *x
	}
	v := reflect.ValueOf(f).Elem()
	if t.null {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Type() != t.base {
		v = v.Convert(t.base)
	}
	return v.Interface()
}

// set sets the term's column in the connection's row p from v, a cursor's
// value: of the base type in a cursor from a page, a JSON value in one from
// the wire, nil for NULL.
func (t *term) set(p any, own untyped.Table, v any) error {
	if t.isCount || t.expr != nil {
		return nil // After computes it from the row's key, or takes the value
	}
	r := t.row(p, own)
	if r == nil && t.edge != 0 && v == nil && !t.null {
		// A NULL of a column that cannot hold one: the row has no related
		// row, as the left join read it.
		return nil
	}
	if r == nil {
		r = t.tbl.New()
		if err := own.Fill(p, t.edge-1, t.tbl, r); err != nil {
			return err
		}
	}
	f := reflect.ValueOf(t.tbl.Field(r, t.col)).Elem()
	if t.null && v == nil {
		f.SetZero()
		return nil
	}
	b := reflect.New(t.base).Elem()
	if reflect.TypeOf(v) == t.base {
		b.Set(reflect.ValueOf(v))
	} else if err := cursorSet(b.Addr().Interface(), v, t.base.String()); err != nil {
		return err
	}
	if t.null {
		x := reflect.New(f.Type().Elem())
		x.Elem().Set(b.Convert(f.Type().Elem()))
		f.Set(x)
		return nil
	}
	f.Set(b.Convert(f.Type()))
	return nil
}

// cursorSet sets *p, a pointer to a base type named name, from v, a value of
// a decoded cursor, as cursorValue does.
func cursorSet(p, v any, name string) error {
	switch err := gqlcore.SetValue(p, v); {
	case errors.Is(err, gqlcore.ErrUnsupported):
		return fmt.Errorf("veloxgql: a cursor value of the Go type %s, which is not supported", name)
	case err != nil:
		return InputErrorf("%w: a value is not %s %s", veloxgql.ErrBadCursor, english.Article(name), name)
	}
	return nil
}

// Page is a page of a connection: its rows in the connection's order, each
// row's cursor, and the page info. A row is a pointer to the node's entity.
type Page struct {
	Rows    []any
	Cursors []veloxgql.Cursor
	// Info is the page info. Its StartCursor and EndCursor point into
	// Cursors.
	Info veloxgql.PageInfo
	t    untyped.Table // the rows' entity
}

// Node returns row i as a *E of the node's entity E, in an any. It panics
// when i is out of range.
func (p *Page) Node(i int) any { return p.Rows[i] }

// Connection is a connection to a list of nodes: the query of the whole list
// and what one page of it is. It runs nothing until it is asked: Page runs
// the page query, once however often and from however many goroutines it
// is called, and TotalCount the COUNT, once too. So a GraphQL selection of
// only totalCount runs no page query, and one without it no COUNT.
//
// A Connection is made by Paginate, and must not be copied. The nil
// *Connection is the empty connection: its page has no rows and its
// totalCount is 0, so the zero connection type of generated code, which an
// application returns for a list it denies, is one.
//
// Page and TotalCount read under the context they are given, for its
// cancellation and deadline, with the values of the context Paginate was
// given first (decision 0172): a value Paginate's context does not hold,
// such as the field context of the reading field, comes from the reading
// context. A transaction is Paginate's context's alone: the reading
// context's mark is dropped. Paginate's cancellation is not kept, so a
// resolver may cancel its context once it returns the connection.
type Connection struct {
	query untyped.TableQuery // the whole list, unordered
	typ   string             // the GraphQL type of the nodes
	n     int                // the page size
	back  bool               // paging backward: last, or before
	terms []term             // q's orderings, the order's terms, then the key's columns
	key   int                // the index in terms of the key's first column
	exprs int                // the number of q's orderings, first in terms, each read beside the row
	after any                // the row the page starts after, in the paging direction
	vals  []any              // after's values of q's orderings by an expression
	// edge is set by edgeConnection when the connection is its part of a
	// batch.
	edge *edgeMode
	from context.Context // Paginate's context, without its cancellation: readIn

	pageOnce  sync.Once
	page      *Page
	pageErr   error
	countOnce sync.Once
	count     int
	countErr  error
}

// Paginate is the connection to the nodes of c's type that from leads to,
// filtered by args.Where, paged by args, in the order args.Order and then
// c's key, each column ASC. For generated code: the body of a type's
// newXConnection, which the entity's PaginateQuery calls with a Query and an
// edge method with an Edge. It checks the query and the arguments, decodes
// the cursor, and runs no query. An Edge's connection batches (see Edge's
// paginate). It fails with veloxgql.ErrNoSelectionSource, and
// veloxgql.ErrNoDB, in a context no adapter made (gqlgenx.NewExtension,
// graphqlgo.With), which tells it nothing of the field it resolves.
//
// A Query's q keeps its filters, its With loads and the scope the
// application gave it. It may be ordered by expressions (a Calc, a JSON
// path or another Term that is not a relation's aggregate) and by the
// node's own columns of a string, bool, number or time type, mixed in any
// order, each with its Nulls: they come first in the order, before order
// and the key, and each page reads their values beside the rows for the
// cursors, which hold them (ordervalue.go, decision 0166). A cursor's value
// of a column, or of an expression whose kind velox knows, is checked
// against that kind. A column that q orders by and order names too fails
// with ErrBadPagination. An ordering by a relation's aggregate or by a
// related row's column is refused with an internal error: it comes from
// order. q must select the key and the columns of order's own fields,
// which the cursors hold: a q that does either wrong is refused with an
// internal error. Its Offset is cleared and its Limit
// replaced, as the page is the connection's. It reads only the columns the
// selection of the field of ctx needs (selected, narrow, c.columns): a
// column the selection does not need is left zero in the nodes, except the
// key, the foreign keys and the columns of order, so an application's
// resolver must not read one it did not ask for.
//
// It fails with the where input's error; with an input error for an order
// field c does not know; with ErrBadPagination for first and last together,
// either below 0, another combination Args refuses, or an order of more
// than MaxOrderTerms terms; with ErrPageTooLarge for either over MaxPage
// (both the limits of veloxgql.LimitsFromContext(ctx)); and with
// ErrBadCursor for a cursor of another type, taken under another order, or
// whose values are not of the order's types.
//
// A msgpack cursor, entgql's format, which veloxgql.Cursor's UnmarshalGQL
// keeps, is read as entgql reads one: its key as the key, and its values
// as the order's, whatever order it was taken under. One whose number of
// values is not the order's number of terms fails with ErrBadCursor.
//
// The connection's reads, run later, take their context values from ctx
// and their cancellation and deadline from the context of the read (see
// Connection): a value an application puts in ctx before it paginates,
// which a Policy or an Interceptor reads, holds for the page and for
// totalCount alike, after ctx is cancelled too. An Edge's batch reads under
// one context for all its owners, as Edge's paginate says: a value given
// per owner separates the batch only through what the batch's key holds.
func Paginate(ctx context.Context, from Source, c *Type, args Args) (*Connection, error) {
	if err := needSource(ctx); err != nil {
		return nil, err
	}
	return from.paginate(ctx, c, args, args.Order)
}

// paginate is Paginate of a query q.
func paginate(ctx context.Context, q untyped.TableQuery, c *Type, args Args, order []Order) (*Connection, error) {
	if args.Where.pred != nil {
		p, err := args.Where.pred(ctx)
		if err != nil {
			return nil, err
		}
		q = q.Where(p)
	}
	q, err := connectionBase(ctx, q, c, order)
	if err != nil {
		return nil, err
	}
	return newConnection(ctx, q, c, args, order)
}

// newConnFunc is the call that pages c's nodes, as an application's error
// names it: "Paginate of Post", a query's Paginate.
func newConnFunc(c *Type) string { return "Paginate of " + c.Name }

// connectionBase checks q, Paginate's query, against c and order, and
// returns it reading only the columns the selection of the field of ctx
// needs. An order field c does not know is left to newConnection.
func connectionBase(ctx context.Context, q untyped.TableQuery, c *Type, order []Order) (untyped.TableQuery, error) {
	fn := newConnFunc(c)
	for _, k := range c.Key {
		if !q.Selects(k.Index) {
			return q, fmt.Errorf("veloxgql: %s: q does not select the key, which the cursors hold", fn)
		}
	}
	for _, o := range order {
		// The join reads the target's columns, and no column of the entity's.
		if col := c.find(o.Field); col != nil && col.Edge == 0 && !q.Selects(col.Index) {
			return q, fmt.Errorf("veloxgql: %s: q does not select the column of %s, which orderBy names and the cursors hold", fn, o.Field)
		}
	}
	if fields, ok := selected(ctx, c.Name); ok {
		q = narrow(q, c.columns(fields, order))
	}
	return q, nil
}

// newConnection is Paginate of a q connectionBase checked.
func newConnection(ctx context.Context, q untyped.TableQuery, c *Type, args Args, order []Order) (*Connection, error) {
	typ := c.Name
	own := q.Table()
	pre, err := exprTerms(q, c, order)
	if err != nil {
		return nil, err
	}
	if pre != nil {
		q = q.Unordered()
	}
	terms := make([]term, 0, len(pre)+len(order)+len(c.Key))
	terms = append(terms, pre...)
	for _, o := range order {
		col := c.find(o.Field)
		if col == nil {
			return nil, InputErrorf("%sOrder: %q is not a value of %sOrderField", typ, o.Field, typ)
		}
		t, err := newTerm(own, col, o.Direction)
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	lim := veloxgql.LimitsFromContext(ctx)
	n, back, err := pageSize(args, lim.MaxPage)
	if err != nil {
		return nil, gqlcore.Within(typ+" connection", err)
	}
	if len(c.Key) == 0 {
		return nil, fmt.Errorf("veloxgql: %s connection: no key to order by last", typ)
	}
	if len(order) > lim.MaxOrderTerms {
		return nil, InputErrorf("%s connection: %w: orderBy has %d terms, and the most is %d", typ, veloxgql.ErrBadPagination, len(order), lim.MaxOrderTerms)
	}
	keyDir := veloxgql.OrderDirectionAsc
	if len(order) == 1 && order[0].Direction == veloxgql.OrderDirectionDesc {
		// entgql's tie-break: with one term, the key in its direction.
		keyDir = veloxgql.OrderDirectionDesc
	}
	for i := range c.Key {
		t, err := newTerm(own, &c.Key[i], keyDir)
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	conn := &Connection{query: q.Offset(0), typ: typ, n: n, back: back, key: len(pre) + len(order), exprs: len(pre), from: context.WithoutCancel(ctx)}
	var joined []int
	for _, t := range terms {
		if t.edge != 0 && !t.isCount && !slices.Contains(joined, t.edge) {
			joined = append(joined, t.edge)
			conn.query = conn.query.LeftJoin(t.edge-1, t.tbl)
		}
	}
	for i := range terms {
		switch terms[i].dir {
		case "":
			terms[i].dir = veloxgql.OrderDirectionAsc
		case veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc:
		default:
			return nil, InputErrorf("%s connection: %w: the order %s has the direction %q, not ASC or DESC", typ, veloxgql.ErrBadPagination, terms[i].field, terms[i].dir)
		}
	}
	conn.terms = terms
	cur := args.After
	if back {
		cur = args.Before
	}
	if cur != nil {
		if conn.after, err = conn.row(*cur); err != nil {
			return nil, err
		}
	}
	return conn, nil
}

// Page returns the connection's page, running its query on the first call:
//   - The order always ends with the key, so that it is total.
//   - Paging backward (last or before) reverses every term's direction,
//     continues after the cursor with velox's Query.After on that order, and
//     reverses the rows read.
//   - It reads one row more than the page: that row's presence is
//     hasNextPage forward and hasPreviousPage backward. The other is true
//     when the page starts at a cursor, after or before, as entgql says,
//     and false otherwise: Relay lets a server say false when it cannot
//     tell cheaply.
//
// The page query reads at most n+1 rows whatever the DB's WithMaxRows cap: the
// MaxPage of veloxgql.LimitsFromContext(ctx) bounds it. The base query's With
// loads share that cap of n+1 rows, unless they have Rel.MaxRows or
// Rel.LimitEach. The page's rows become a group of the operation, whose edges
// batch (see Edge); in the connection of an edge (Type.Edge) the
// page is the connection's part of the batch, which may return n+1 rows per
// owner.
func (c *Connection) Page(ctx context.Context) (*Page, error) {
	if c == nil {
		return &Page{}, nil
	}
	c.pageOnce.Do(func() {
		c.pageErr = errPanicked // the other callers' error, when run panics
		c.page, c.pageErr = c.run(c.readIn(ctx))
	})
	return c.page, c.pageErr
}

// TotalCount returns the number of nodes in the whole connection, not only
// on its page, running a COUNT on the first call.
func (c *Connection) TotalCount(ctx context.Context) (int, error) {
	if c == nil {
		return 0, nil
	}
	c.countOnce.Do(func() {
		c.countErr = errPanicked // the other callers' error, when the count panics
		if c.edge != nil {
			c.count, c.countErr = c.edge.count(c.query)
			return
		}
		n, err := c.query.Count(c.readIn(ctx))
		c.count, c.countErr = int(n), err
	})
	return c.count, c.countErr
}

// run reads the page.
func (c *Connection) run(ctx context.Context) (*Page, error) {
	orders := make([]velox.OrderTerm, len(c.terms))
	for i := range c.terms {
		orders[i] = c.terms[i].order((c.terms[i].dir == veloxgql.OrderDirectionDesc) != c.back)
	}
	q := c.query.OrderBy(orders...)
	switch {
	case c.exprs > 0 && c.after != nil:
		q = q.OrderValues().AfterValues(c.after, c.vals)
	case c.exprs > 0:
		q = q.OrderValues()
	case c.after != nil:
		q = q.After(c.after)
	}
	var rows untyped.Rows
	var err error
	if c.edge != nil {
		rows, err = c.edge.rows(q)
	} else {
		// MaxRows: the page is bounded by MaxPage, and the DB's row
		// cap, which may be below it, must not fail a full page.
		rows, err = q.Limit(c.n + 1).MaxRows(c.n + 1).All(ctx)
	}
	if errors.Is(err, untyped.ErrOrderValue) {
		return nil, InputErrorf("%w: a value of an ordering by an expression is not one a page wrote", veloxgql.ErrBadCursor)
	}
	if err != nil {
		return nil, err
	}
	n := min(rows.Len(), c.n)
	more := rows.Len() > c.n
	p := &Page{Rows: make([]any, n), Cursors: make([]veloxgql.Cursor, n), t: c.query.Table()}
	var vals [][]any // each row's values of q's orderings by an expression
	if c.exprs > 0 {
		vals = make([][]any, n)
	}
	for i := range n {
		p.Rows[i] = rows.At(i)
		if vals != nil {
			if vals[i], err = cursorValues(rows.OrderValues(i)); err != nil {
				return nil, err
			}
		}
	}
	if c.back {
		// The pointers, not the rows: a batch's rows are shared.
		slices.Reverse(p.Rows)
		slices.Reverse(vals)
	}
	// The cursors' terms are one slab, not a slice for each row.
	last := len(c.terms) - 1
	slab := make([]veloxgql.CursorTerm, last*len(p.Rows))
	own := c.query.Table()
	for i, x := range p.Rows {
		p.Cursors[i] = c.cursor(own, x, slab[i*last:(i+1)*last:(i+1)*last])
		for k := range c.exprs {
			p.Cursors[i].Terms[k].Value = vals[i][k]
		}
	}
	if c.edge == nil {
		// A batch made its rows a group already.
		gqlcore.Add(ctx, p.Rows)
	}
	if c.back {
		p.Info.HasPreviousPage = more
		// entgql's: a page before a cursor has one after it.
		p.Info.HasNextPage = c.after != nil
	} else {
		p.Info.HasNextPage = more
		p.Info.HasPreviousPage = c.after != nil
	}
	if n > 0 {
		p.Info.StartCursor, p.Info.EndCursor = &p.Cursors[0], &p.Cursors[n-1]
	}
	return p, nil
}

// cursor is x's cursor: the order's terms with x's values, the key's columns
// but the last as terms named "key:<column>", and the last column as the key.
// terms is its room for the terms, of the length of the order's but the last.
func (c *Connection) cursor(own untyped.Table, x any, terms []veloxgql.CursorTerm) veloxgql.Cursor {
	last := len(c.terms) - 1
	for i := range c.terms[:last] {
		terms[i] = veloxgql.CursorTerm{Field: c.field(i), Direction: c.terms[i].dir, Value: c.terms[i].get(x, own)}
	}
	return veloxgql.Cursor{Type: c.typ, Terms: terms, Key: c.terms[last].get(x, own)}
}

// field is the field of term i in a cursor.
func (c *Connection) field(i int) string {
	if i >= c.key {
		return "key:" + c.terms[i].field
	}
	return c.terms[i].field
}

// row checks cur against the connection's type and order, and returns the
// row it stands for: a zero row with the order's and the key's columns set
// from it, for Query.After.
func (c *Connection) row(cur veloxgql.Cursor) (any, error) {
	if m, ok := cur.Key.(*gqlcore.MsgpackCursor); ok {
		var err error
		if cur, err = c.msgpack(m); err != nil {
			return nil, err
		}
	}
	last := len(c.terms) - 1
	order := make([]cursorOrder, last)
	for i := range c.terms[:last] {
		order[i] = cursorOrder{Field: c.field(i), Direction: c.terms[i].dir}
	}
	if err := checkCursor(cur, c.typ, order); err != nil {
		return nil, err
	}
	if c.exprs > 0 {
		c.vals = make([]any, c.exprs)
		for i := range c.vals {
			v, err := orderValue(cur.Terms[i].Value)
			if err != nil {
				return nil, gqlcore.Within(c.typ+" cursor", err)
			}
			c.vals[i] = v
		}
	}
	own := c.query.Table()
	x := own.New()
	for i := range c.terms[:last] {
		if err := c.terms[i].set(x, own, cur.Terms[i].Value); err != nil {
			return nil, gqlcore.Within(c.typ+" cursor", err)
		}
	}
	if err := c.terms[last].set(x, own, cur.Key); err != nil {
		return nil, gqlcore.Within(c.typ+" cursor", err)
	}
	return x, nil
}

// msgpack is l, a msgpack cursor, as a cursor of the connection's own type
// and order, as entgql reads one: its key is the key, and its values are
// the order's, whatever order it was taken under. A cursor whose number of
// values is not the order's number of terms, or a connection whose key has
// more than one column, which a msgpack cursor cannot hold, or whose query
// is ordered at all, by an expression or a column, which entgql's cursor
// never held, is refused as a cursor
// taken under another order. A term on the key's own column, as
// orderBy ID, whose value is nil takes the key: such a cursor holds the key
// alone for it.
func (c *Connection) msgpack(l *gqlcore.MsgpackCursor) (veloxgql.Cursor, error) {
	last := len(c.terms) - 1
	vs, ok := l.Values(c.key)
	if !ok || c.key != last || c.exprs > 0 {
		return veloxgql.Cursor{}, errOtherOrder(c.typ)
	}
	key := c.terms[last]
	terms := make([]veloxgql.CursorTerm, len(vs))
	for i, v := range vs {
		if t := c.terms[i]; v == nil && t.edge == 0 && t.col == key.col {
			v = l.Key
		}
		terms[i] = veloxgql.CursorTerm{Field: c.field(i), Direction: c.terms[i].dir, Value: v}
	}
	return veloxgql.Cursor{Type: c.typ, Terms: terms, Key: l.Key}, nil
}

// pageSize is the page size args ask for, and whether they page backward;
// maxPage is the most, and the size when args give none.
func pageSize(args Args, maxPage int) (int, bool, error) {
	switch {
	case args.First != nil && args.Last != nil:
		return 0, false, InputErrorf("%w: first and last given together", veloxgql.ErrBadPagination)
	case args.After != nil && args.Before != nil:
		return 0, false, InputErrorf("%w: after and before given together", veloxgql.ErrBadPagination)
	case args.First != nil && args.Before != nil:
		return 0, false, InputErrorf("%w: first pages forward, and before goes with last", veloxgql.ErrBadPagination)
	case args.Last != nil && args.After != nil:
		return 0, false, InputErrorf("%w: last pages backward, and after goes with first", veloxgql.ErrBadPagination)
	}
	back := args.Last != nil || args.Before != nil
	n, name := args.First, "first"
	if back {
		n, name = args.Last, "last"
	}
	switch {
	case n == nil:
		return maxPage, back, nil
	case *n < 0:
		return 0, false, InputErrorf("%w: %s is %d, below 0", veloxgql.ErrBadPagination, name, *n)
	case *n > maxPage:
		return 0, false, InputErrorf("%w: %s is %d, and the most is %d", veloxgql.ErrPageTooLarge, name, *n, maxPage)
	}
	return *n, back, nil
}
