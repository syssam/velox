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

// CustomerConnection is the GraphQL type CustomerConnection. Its fields run
// their queries when they are resolved.
type CustomerConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *CustomerConnection) Edges(ctx context.Context) ([]*CustomerEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]CustomerEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*CustomerEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = CustomerEdge{Node: p.Node(i).(*Customer), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *CustomerConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Customer nodes in the whole connection, not only
// on this page.
func (c *CustomerConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// CustomerEdge is the GraphQL type CustomerEdge.
type CustomerEdge struct {
	Node   *Customer
	Cursor veloxgql.Cursor
}

// CustomerPaginateOption is an option of a query's Paginate to Customer nodes:
// WithCustomerOrder. Of two options that set the same thing, the later wins.
type CustomerPaginateOption struct {
	orderBy *CustomerOrder
}

// WithCustomerOrder orders the connection by orderBy and then the key.
func WithCustomerOrder(orderBy *CustomerOrder) CustomerPaginateOption {
	return CustomerPaginateOption{orderBy: orderBy}
}

// PaginateQuery is the connection to the Customer nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key and the order
// columns.
func (*Customer) PaginateQuery(
	ctx context.Context, q velox.Query[Customer],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []CustomerPaginateOption,
) (*CustomerConnection, error) {
	var o CustomerPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
	}
	return newCustomerConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy)
}

// newCustomerConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newCustomerConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *CustomerOrder,
) (*CustomerConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &customerType, args)
	if err != nil {
		return nil, err
	}
	return &CustomerConnection{Conn: c}, nil
}

// customerType describes the GraphQL type Customer to gqlgraph.
var customerType = gqlgraph.Type{
	Name:  "Customer",
	Table: untyped.TableOf[Customer],
	Key:   []gqlgraph.Column{{Name: "id", Index: customerColID}},
	Order: []gqlgraph.Column{
		{Name: "NAME", Index: customerColName},
		{Name: "TIER", Index: customerColTier},
		{Name: "VIP", Index: customerColVip},
		{Name: "JOINED_AT", Index: customerColJoinedAt},
	},
	AlwaysRead: []int{customerColID},
	Fields: []string{
		customerColID:       "id",
		customerColName:     "name",
		customerColTier:     "tier",
		customerColVip:      "vip",
		customerColJoinedAt: "joinedAt",
	},
	LenientFilters: true,
}

// CustomerOrder is the GraphQL input CustomerOrder, an order of connections to
// Customer.
type CustomerOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field CustomerOrderField
}

// CustomerOrderField is the GraphQL enum CustomerOrderField, the fields
// connections to Customer can be ordered by.
type CustomerOrderField string

// The values of CustomerOrderField.
const (
	// CustomerOrderFieldName orders by the field name.
	CustomerOrderFieldName CustomerOrderField = "NAME"
	// CustomerOrderFieldTier orders by the field tier.
	CustomerOrderFieldTier CustomerOrderField = "TIER"
	// CustomerOrderFieldVip orders by the field vip.
	CustomerOrderFieldVip CustomerOrderField = "VIP"
	// CustomerOrderFieldJoinedAt orders by the field joined_at.
	CustomerOrderFieldJoinedAt CustomerOrderField = "JOINED_AT"
)

// IsValid reports whether o is a value of CustomerOrderField.
func (o CustomerOrderField) IsValid() bool { return customerType.IsOrderField(string(o)) }

// String returns o's value.
func (o CustomerOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum CustomerOrderField.
func (o CustomerOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum CustomerOrderField.
func (o *CustomerOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &customerType)
}
