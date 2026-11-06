package gqlimpl

import (
	"context"
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

// ConnOf is the Conn of the GraphQL type typ with the order terms order, then
// key, as the generated code writes it, and the connection's order.
func ConnOf[E any](typ string, order, key []OrderTerm[E]) (*Conn, []Order) {
	c := &Conn{Type: typ}
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

// NewConnectionOf is NewConnection on a typed query, with the Conn of typ,
// order and key.
func NewConnectionOf[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (ConnectionOf[E], error) {
	conn, o := ConnOf(typ, order, key)
	c, err := NewConnection(ctx, velox.Untyped(q), conn, args, o)
	return ConnectionOf[E]{c}, err
}

// TableConn is a Conn of E's entity alone, as an edge's owner or target: a
// selection reads every column.
func TableConn[E any](typ string) *Conn {
	return &Conn{Type: typ, Table: velox.TableOf[E]}
}

// InputConn is a Conn of E's entity with inputs, whose key is the column key
// and whose inputs' fields are names, by column.
func InputConn[E any](typ string, key int, names []string) *Conn {
	c := TableConn[E](typ)
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
func RowOf(c *Conn, x any) any {
	if p := rowOf(c, x); p != nil {
		return p
	}
	return nil
}

func NodeOf(c *Conn, p any) any {
	q, _ := p.(unsafe.Pointer)
	return nodeOf(c, q)
}

// NodesOf is nodesOf of ps, each an unsafe.Pointer as RowOf returns it.
func NodesOf(c *Conn, ps []any) any {
	var rows []unsafe.Pointer
	for _, p := range ps {
		rows = append(rows, p.(unsafe.Pointer))
	}
	return nodesOf(c, rows)
}

// NewNodeWith is NewNode with the decoder of IDs decode.
func NewNodeWith(c *Conn, decode func(id string) (any, error)) NodeKind {
	return newNode(c, decode)
}

// EdgeConnectionOf is EdgeConnection of a ConnectionOf.
func EdgeConnectionOf[E, T any](ctx context.Context, c ConnectionOf[T], x *E, r velox.Rel[E, T], field string, args any) ConnectionOf[T] {
	return ConnectionOf[T]{EdgeConnection(ctx, c.Connection, TableConn[E]("Owner"), x, int(r.I), field, args)}
}

// EdgeOneOf is EdgeOne, typed, as the generated code calls it: the target's
// type is the part of field after the dot's edge, which the selections do not
// name.
func EdgeOneOf[E, T any](ctx context.Context, x *E, r velox.Rel[E, T], field string) (*T, error) {
	return As[*T](EdgeOne(ctx, TableConn[T]("Target"), TableConn[E]("Owner"), x, int(r.I), field))
}

// EdgeListOf is EdgeList, typed, as the generated code calls it.
func EdgeListOf[E, T any](ctx context.Context, x *E, r velox.Rel[E, T], field string, maxList int) ([]*T, error) {
	return As[[]*T](EdgeList(ctx, TableConn[T]("Target"), TableConn[E]("Owner"), x, int(r.I), field, maxList))
}

// Paginate is NewConnectionOf and Page in one call.
func Paginate[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*PageOf[E], error) {
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
