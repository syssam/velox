package gen

import (
	"context"
	"io"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// MemoConnection is the GraphQL type MemoConnection. Its fields run their
// queries when gqlgen resolves them.
type MemoConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *MemoConnection) Edges(ctx context.Context) ([]*MemoEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]MemoEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*MemoEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = MemoEdge{Node: p.Node(i).(*Memo), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *MemoConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Memo nodes in the whole connection, not only on
// this page.
func (c *MemoConnection) TotalCount(ctx context.Context) (int, error) { return c.Conn.TotalCount(ctx) }

// MemoEdge is the GraphQL type MemoEdge.
type MemoEdge struct {
	Node   *Memo
	Cursor veloxgql.Cursor
}

// MemoPaginateOption is an option of a query's Paginate to Memo nodes:
// WithMemoOrder. Of two options that set the same thing, the later wins.
type MemoPaginateOption struct {
	orderBy *MemoOrder
}

// WithMemoOrder orders the connection by orderBy and then the key.
func WithMemoOrder(orderBy *MemoOrder) MemoPaginateOption {
	return MemoPaginateOption{orderBy: orderBy}
}

// PaginateQuery is the connection to the Memo nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key and the order
// columns.
func (*Memo) PaginateQuery(
	ctx context.Context, q velox.Query[Memo],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []MemoPaginateOption,
) (*MemoConnection, error) {
	var o MemoPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
	}
	return newMemoConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy)
}

// newMemoConnection is PaginateQuery of the nodes from leads to: a query's, or
// an edge's, whose queries batch.
func newMemoConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *MemoOrder,
) (*MemoConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &memoType, args)
	if err != nil {
		return nil, err
	}
	return &MemoConnection{Conn: c}, nil
}

// memoType describes the GraphQL type Memo to gqlgraph.
var memoType = gqlgraph.Type{
	Name:  "Memo",
	Table: untyped.TableOf[Memo],
	Key:   []gqlgraph.Column{{Name: "id", Index: memoColID}},
	Order: []gqlgraph.Column{
		{Name: "TITLE", Index: memoColTitle},
	},
	AlwaysRead:     []int{memoColID},
	Fields:         []string{memoColID: "id", memoColTitle: "title"},
	LenientFilters: true,
}

// MemoOrder is the GraphQL input MemoOrder, an order of connections to Memo.
type MemoOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field MemoOrderField
}

// MemoOrderField is the GraphQL enum MemoOrderField, the fields connections to
// Memo can be ordered by.
type MemoOrderField string

// The values of MemoOrderField.
const (
	// MemoOrderFieldTitle orders by the field title.
	MemoOrderFieldTitle MemoOrderField = "TITLE"
)

// IsValid reports whether o is a value of MemoOrderField.
func (o MemoOrderField) IsValid() bool { return memoType.IsOrderField(string(o)) }

// String returns o's value.
func (o MemoOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum MemoOrderField.
func (o MemoOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum MemoOrderField.
func (o *MemoOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &memoType)
}
