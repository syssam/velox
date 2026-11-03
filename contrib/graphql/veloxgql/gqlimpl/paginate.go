package gqlimpl

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/plugin"
)

// Args are a connection's paging arguments, as the client gave them.
//
// first (with or without after) pages forward and last (with or without before)
// backward; with neither, the page is the MaxPage of
// veloxgql.LimitsFromContext(ctx) rows forward, or backward from before when
// before is given. first and last together, after and before together, first
// with before and last with after all fail with ErrBadPagination: keyset paging
// bounds a page on one side only.
type Args struct {
	After, Before *veloxgql.Cursor
	First, Last   *int
}

// Conn is what the connections to the nodes of one GraphQL type are made of,
// as a literal table the generated code writes once per type: the type, the
// key's columns, the order fields and their columns, and the columns a
// selection reads.
type Conn struct {
	// Type is the GraphQL type of the nodes, "Post".
	Type string
	// Key is the primary key's columns, each ASC: Field is the column's
	// name, which a cursor's terms name "key:<column>".
	Key []Col
	// Order is the order fields: Field is the GraphQL enum value, "TITLE".
	Order []Col
	// Columns is the columns a selection of the type's fields reads (see
	// Narrow); nil, every column, for a field it does not know.
	Columns func(fields []string) []int
}

// Col is a column of a connection's key or of an order field.
type Col struct {
	// Field is the order field, or the key column's name.
	Field string
	// Col is the column's index in its entity: the node's, or the target's
	// of Edge.
	Col int
	// Edge is, for a column of the entity that a to-one relation of the node
	// leads to, the relation's index plus one; 0 for the node's own column.
	// A connection ordered by it reads the relation with an inner JOIN, in
	// the page query and in the count: a row with no related entity, or none
	// that the related entity's Read policy lets the caller read, is not in
	// the connection. A cursor's row carries a related entity with the
	// term's value set.
	Edge int
	// Target is the relation's target, for an Edge.
	Target func() velox.Table
}

// Order is one term of a connection's order, as the client gave it: the
// order field and its direction. The zero direction is ASC, as the SDL's
// default for XOrder.direction.
type Order struct {
	Field     string
	Direction veloxgql.OrderDirection
}

// find is the order field named field; nil for none.
func (c *Conn) find(field string) *Col {
	for i := range c.Order {
		if c.Order[i].Field == field {
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
	edge  int         // the relation's index plus one; 0 for the node's own column
	tbl   velox.Table // the column's entity: the node's, or the relation's target
	base  reflect.Type
	null  bool // the field is a pointer: nil is NULL, which a cursor holds as null
}

// newTerm is the term of c, in direction dir, on the connection's entity t.
func newTerm(t velox.Table, c *Col, dir veloxgql.OrderDirection) (term, error) {
	tm := term{field: c.Field, dir: dir, col: c.Col, edge: c.Edge, tbl: t}
	if c.Edge != 0 {
		tm.tbl = c.Target()
	}
	if err := tm.tbl.Err(); err != nil {
		return tm, err
	}
	info := tm.tbl.Info()
	if c.Col < 0 || c.Col >= len(info.Columns) {
		return tm, fmt.Errorf("veloxgql: %s has no column %d; run go tool velox-gen", info.Name, c.Col)
	}
	ft := velox.GoType(info.Columns[c.Col])
	if ft.Kind() == reflect.Pointer {
		ft, tm.null = ft.Elem(), true
	}
	tm.base = baseType(ft)
	if tm.base == nil {
		return tm, fmt.Errorf("veloxgql: %s.%s: keyset paging cannot continue after a %v", info.Name, info.Columns[c.Col].Field, ft)
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
func (t *term) order(desc bool) velox.OrderTerm {
	return velox.OrderTerm{Column: t.col, Desc: desc, Rel: int32(t.edge)}
}

// row is the row of the term's column in the connection's row p: p, or its
// related entity; nil when a joined row has none.
func (t *term) row(p unsafe.Pointer, own velox.Table) unsafe.Pointer {
	if t.edge == 0 {
		return p
	}
	r, _ := own.Filled(p, t.edge-1)
	return r
}

// get returns the term's value in the connection's row p as the base type,
// nil for NULL.
func (t *term) get(p unsafe.Pointer, own velox.Table) any {
	r := t.row(p, own)
	if r == nil {
		// A row read without the join: the zero value, which no cursor of
		// it is used from.
		return reflect.Zero(t.base).Interface()
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
func (t *term) set(p unsafe.Pointer, own velox.Table, v any) error {
	r := t.row(p, own)
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
	Rows    []unsafe.Pointer
	Cursors []veloxgql.Cursor
	// Info is the page info. Its StartCursor and EndCursor point into
	// Cursors.
	Info veloxgql.PageInfo
}

// Connection is a connection to a list of nodes: the query of the whole list
// and what one page of it is. It runs nothing until it is asked: Page runs
// the page query, once however often and from however many goroutines it
// is called, and TotalCount the COUNT, once too. So a GraphQL selection of
// only totalCount runs no page query, and one without it no COUNT.
//
// A Connection is made by NewConnection, and must not be copied.
type Connection struct {
	query velox.TableQuery // the whole list, unordered
	typ   string           // the GraphQL type of the nodes
	n     int              // the page size
	back  bool             // paging backward: last, or before
	terms []term           // the order's terms, then the key's columns
	key   int              // the index in terms of the key's first column
	after unsafe.Pointer   // the row the page starts after, in the paging direction
	// edge is set by EdgeConnection when the connection is its part of a
	// batch.
	edge *edge

	pageOnce  sync.Once
	page      *Page
	pageErr   error
	countOnce sync.Once
	count     int
	countErr  error
}

// ConnectionBase checks q, the base query of XsConnectionOf, against c and
// order, and returns it reading only the columns the selection of the field
// of ctx needs (Selected, Narrow, c.Columns). q must select the key and the
// columns of order's own fields, which the cursors hold: one that does not
// is refused with an internal error. An order field c does not know is left
// to NewConnection.
func ConnectionBase(ctx context.Context, q velox.TableQuery, c *Conn, order []Order) (velox.TableQuery, error) {
	fn := plugin.GoPlural(c.Type) + "ConnectionOf"
	for _, k := range c.Key {
		if !q.Selects(k.Col) {
			return q, fmt.Errorf("veloxgql: %s: the base query does not select the key, which the cursors hold", fn)
		}
	}
	for _, o := range order {
		// The join reads the target's columns, and no column of the entity's.
		if col := c.find(o.Field); col != nil && col.Edge == 0 && !q.Selects(col.Col) {
			return q, fmt.Errorf("veloxgql: %s: the base query does not select the column of %s, which orderBy names and the cursors hold", fn, o.Field)
		}
	}
	if fields, ok := Selected(ctx, c.Type); ok {
		q = Narrow(q, c.Columns(fields))
	}
	return q, nil
}

// NewConnection is the connection to the nodes of c's type that q returns,
// paged by args, in the order order and then c's key, each column ASC. q
// holds the connection's filters and no order: the connection orders it, and
// a q with an OrderBy is refused with an internal error, since its order
// would come first and break the cursors. q must also select the key and the
// order's columns (ConnectionBase checks them): a cursor is taken from the
// rows. Its Offset is cleared and its Limit replaced, as the page is the
// connection's. It checks the arguments and decodes the cursor, and runs no
// query.
//
// It fails with an input error for an order field c does not know; with
// ErrBadPagination for first and last together, either below 0, another
// combination Args refuses, or an order of more than MaxOrderTerms terms;
// with ErrPageTooLarge for either over MaxPage (both the limits of
// veloxgql.LimitsFromContext(ctx)); and with ErrBadCursor for a cursor of
// another type (ErrWrongIDType too), taken under another order, or whose
// values are not of the order's types.
func NewConnection(ctx context.Context, q velox.TableQuery, c *Conn, args Args, order []Order) (*Connection, error) {
	typ := c.Type
	own := q.Table()
	terms := make([]term, 0, len(order)+len(c.Key))
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
	if q.Ordered() {
		// The application's bug, not the client's: an uncoded error.
		return nil, fmt.Errorf("veloxgql: %sConnectionOf: the base query has an OrderBy; the connection orders by its orderBy argument", plugin.GoPlural(typ))
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
	for i := range c.Key {
		t, err := newTerm(own, &c.Key[i], veloxgql.OrderDirectionAsc)
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	conn := &Connection{query: q.Offset(0), typ: typ, n: n, back: back, key: len(order)}
	var joined []int
	for _, t := range terms {
		if t.edge != 0 && !slices.Contains(joined, t.edge) {
			joined = append(joined, t.edge)
			conn.query = conn.query.Join(t.edge-1, t.tbl)
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
//     hasNextPage forward and hasPreviousPage backward. The other is false:
//     Relay lets a server say false when it cannot tell cheaply.
//
// The page query reads at most n+1 rows whatever the DB's WithMaxRows cap: the
// MaxPage of veloxgql.LimitsFromContext(ctx) bounds it. The base query's With
// loads share that cap of n+1 rows, unless they have Rel.MaxRows or
// Rel.LimitEach. The page's rows become a group of the operation, whose edges
// batch (see EdgeConnection); in the connection of an edge (EdgeConnection) the
// page is the connection's part of the batch, which may return n+1 rows per
// owner.
func (c *Connection) Page(ctx context.Context) (*Page, error) {
	c.pageOnce.Do(func() {
		c.pageErr = errPanicked // the other callers' error, when run panics
		c.page, c.pageErr = c.run(ctx)
	})
	return c.page, c.pageErr
}

// TotalCount returns the number of nodes in the whole connection, not only
// on its page, running a COUNT on the first call.
func (c *Connection) TotalCount(ctx context.Context) (int, error) {
	c.countOnce.Do(func() {
		c.countErr = errPanicked // the other callers' error, when the count panics
		if c.edge != nil {
			c.count, c.countErr = c.edge.count(c.query)
			return
		}
		n, err := c.query.Count(ctx)
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
	if c.after != nil {
		q = q.After(c.after)
	}
	var rows velox.Rows
	var err error
	if c.edge != nil {
		rows, err = c.edge.rows(q)
	} else {
		// MaxRows: the page is bounded by MaxPage, and the DB's row
		// cap, which may be below it, must not fail a full page.
		rows, err = q.Limit(c.n + 1).MaxRows(c.n + 1).All(ctx)
	}
	if err != nil {
		return nil, err
	}
	n := min(rows.Len(), c.n)
	more := rows.Len() > c.n
	p := &Page{Rows: make([]unsafe.Pointer, n), Cursors: make([]veloxgql.Cursor, n)}
	for i := range n {
		p.Rows[i] = rows.At(i)
	}
	if c.back {
		// The pointers, not the rows: a batch's rows are shared.
		slices.Reverse(p.Rows)
	}
	// The cursors' terms are one slab, not a slice for each row.
	last := len(c.terms) - 1
	slab := make([]veloxgql.CursorTerm, last*len(p.Rows))
	own := c.query.Table()
	for i, x := range p.Rows {
		p.Cursors[i] = c.cursor(own, x, slab[i*last:(i+1)*last:(i+1)*last])
	}
	if c.edge == nil {
		// A batch made its rows a group already.
		gqlcore.Add(ctx, p.Rows)
	}
	if c.back {
		p.Info.HasPreviousPage = more
	} else {
		p.Info.HasNextPage = more
	}
	if n > 0 {
		p.Info.StartCursor, p.Info.EndCursor = &p.Cursors[0], &p.Cursors[n-1]
	}
	return p, nil
}

// cursor is x's cursor: the order's terms with x's values, the key's columns
// but the last as terms named "key:<column>", and the last column as the key.
// terms is its room for the terms, of the length of the order's but the last.
func (c *Connection) cursor(own velox.Table, x unsafe.Pointer, terms []veloxgql.CursorTerm) veloxgql.Cursor {
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
func (c *Connection) row(cur veloxgql.Cursor) (unsafe.Pointer, error) {
	last := len(c.terms) - 1
	order := make([]cursorOrder, last)
	for i := range c.terms[:last] {
		order[i] = cursorOrder{Field: c.field(i), Direction: c.terms[i].dir}
	}
	if err := checkCursor(cur, c.typ, order); err != nil {
		return nil, err
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
