package gqlimpl

import (
	"context"
	"strings"
	"time"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// OrderTerm is an order term or a key column of E, for the tests: the
// column a Conn names, and the term's direction.
type OrderTerm[E any] struct {
	col Col
	dir veloxgql.OrderDirection
}

// TermOf is the term field, in direction dir, of E's column col.
func TermOf[E any](field string, dir veloxgql.OrderDirection, col int) OrderTerm[E] {
	return OrderTerm[E]{col: Col{Field: field, Col: col}, dir: dir}
}

// JoinedTermOf is the term field, in direction dir, of column col of T, the
// target of E's to-one relation edge.
func JoinedTermOf[E, T any](field string, dir veloxgql.OrderDirection, edge, col int) OrderTerm[E] {
	return OrderTerm[E]{col: Col{Field: field, Col: col, Edge: edge + 1, Target: velox.TableOf[T]}, dir: dir}
}

// TypeOf is the Conn of the GraphQL type typ with the order terms order, then
// key, as the generated code writes it, and the connection's order.
func TypeOf[E any](typ string, order, key []OrderTerm[E]) (*Type, []Order) {
	c := &Type{Name: typ}
	var o []Order
	for _, t := range order {
		if c.find(t.col.Field) == nil {
			c.Order = append(c.Order, t.col)
		}
		o = append(o, Order{Field: t.col.Field, Direction: t.dir})
	}
	for _, t := range key {
		c.Key = append(c.Key, t.col)
	}
	return c, o
}

// PageOf is a Page with its rows typed.
type PageOf[E any] struct {
	Rows    []*E
	Cursors []veloxgql.Cursor
	Info    veloxgql.PageInfo
}

// TypedPage is p with its rows typed: its cursors are p's, so the info's
// cursors still point into them.
func TypedPage[E any](p *Page) *PageOf[E] {
	if p == nil {
		return nil
	}
	return &PageOf[E]{Rows: unsafe.Slice((**E)(unsafe.Pointer(unsafe.SliceData(p.Rows))), len(p.Rows)), Cursors: p.Cursors, Info: p.Info}
}

// ConnectionOf is a Connection whose Page is typed.
type ConnectionOf[E any] struct{ *Connection }

// Page is Connection.Page, typed.
func (c ConnectionOf[E]) Page(ctx context.Context) (*PageOf[E], error) {
	p, err := c.Connection.Page(ctx)
	return TypedPage[E](p), err
}

// NewConnectionOf is newConnection on a typed query, with the Type of typ,
// order and key.
func NewConnectionOf[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (ConnectionOf[E], error) {
	conn, o := TypeOf(typ, order, key)
	c, err := newConnection(ctx, velox.Untyped(q), conn, args, o)
	return ConnectionOf[E]{c}, err
}

// TableType is a Conn of E's entity alone, as an edge's owner or target: a
// selection reads every column.
func TableType[E any](typ string) *Type {
	return &Type{Name: typ, Table: velox.TableOf[E]}
}

// InputType is a Conn of E's entity with inputs, whose key is the column key
// and whose inputs' fields are names, by column.
func InputType[E any](typ string, key int, names []string) *Type {
	c := TableType[E](typ)
	c.Key = []Col{{Field: "id", Col: key}}
	c.Inputs = names
	return c
}

// As is v as a P, with err, as the generated code types a result.
func As[P any](v any, err error) (P, error) {
	p, _ := v.(P)
	return p, err
}

// RowOf and NodeOf export the erasure of a row to the tests.
func RowOf(c *Type, x any) any {
	if p := rowOf(c, x); p != nil {
		return p
	}
	return nil
}

func NodeOf(c *Type, p any) any {
	q, _ := p.(unsafe.Pointer)
	return nodeOf(c, q)
}

// NodesOf is nodesOf of ps, each an unsafe.Pointer as RowOf returns it.
func NodesOf(c *Type, ps []any) any {
	var rows []unsafe.Pointer
	for _, p := range ps {
		rows = append(rows, p.(unsafe.Pointer))
	}
	return nodesOf(c, rows)
}

// NewNodeWith is NewNode with the decoder of IDs decode.
func NewNodeWith(c *Type, decode func(id string) (any, error)) NodeKind {
	return newNode(c, decode)
}

// OwnerType is the Type of E's entity alone, as an edge's owner, whose
// relation rel is the edge field field ("Owner.children"): its Name is the
// part before the dot.
func OwnerType[E any](field string, rel int) *Type {
	typ, name, _ := strings.Cut(field, ".")
	c := TableType[E](typ)
	c.Edges = make([]string, rel+1)
	c.Edges[rel] = name
	return c
}

// EdgeOf is the Edge of x through r, whose GraphQL field is field.
func EdgeOf[E, T any](x *E, r velox.Rel[E, T], field string) Edge {
	return OwnerType[E](field, int(r.I)).Edge(x, int(r.I))
}

// EdgeConnectionOf is the connection c of the edge of x through r, whose
// GraphQL field is field, read with the arguments args: what Paginate of an
// Edge does with the connection it makes, on any c.
func EdgeConnectionOf[E, T any](ctx context.Context, c ConnectionOf[T], x *E, r velox.Rel[E, T], field string, args any) ConnectionOf[T] {
	return ConnectionOf[T]{edgeConnection(ctx, c.Connection, EdgeOf(x, r, field), args)}
}

// EdgeOneOf is EdgeOne, typed, as the generated code calls it: the target's
// type is the part of field after the dot's edge, which the selections do not
// name.
func EdgeOneOf[E, T any](ctx context.Context, x *E, r velox.Rel[E, T], field string) (*T, error) {
	return As[*T](EdgeOne(ctx, TableType[T]("Target"), EdgeOf(x, r, field)))
}

// EdgeListOf is EdgeList, typed, as the generated code calls it.
func EdgeListOf[E, T any](ctx context.Context, x *E, r velox.Rel[E, T], field string, maxList int) ([]*T, error) {
	return As[[]*T](EdgeList(ctx, TableType[T]("Target"), EdgeOf(x, r, field), maxList))
}

// PageWith is NewConnectionOf and Page in one call.
func PageWith[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*PageOf[E], error) {
	c, err := NewConnectionOf(ctx, q, typ, args, order, key)
	if err != nil {
		return nil, err
	}
	return c.Page(ctx)
}

// CursorOrder and CheckCursor export the cursor's unexported checks to the
// tests.
type CursorOrder = cursorOrder

func CheckCursor(c veloxgql.Cursor, typ string, order []CursorOrder) error {
	return checkCursor(c, typ, order)
}

// CursorValue converts v, a value of a decoded cursor, to T as a term's set
// does, with cursorSet: a veloxgql scalar as its base type.
func CursorValue[T any](v any) (T, error) {
	var t T
	if err := cursorSet(basePtr(&t), v, gqlcore.TypeName[T]()); err != nil {
		var zero T
		return zero, err
	}
	return t, nil
}

// basePtr is p, a pointer to a veloxgql scalar, as a pointer to its base type,
// which is all package gqlcore handles; any other p is returned as it is.
func basePtr(p any) any {
	switch x := p.(type) {
	case *veloxgql.Time:
		return (*time.Time)(x)
	case *veloxgql.UUID:
		return (*[16]byte)(x)
	case *veloxgql.Uint64:
		return (*uint64)(x)
	case *veloxgql.Bytes:
		return (*[]byte)(x)
	}
	return p
}

// Selected, Narrow and Columns export the selection's columns to the tests.
var (
	Selected = selected
	Narrow   = narrow
)

func (c *Type) Columns(fields []string) []int { return c.columns(fields) }
