package gen

import (
	"context"
	"io"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion27

// MarshalGQL writes e as a GraphQL String, its stored value: the field
// visibility of Group is a String, as graphql.Skip(graphql.SkipEnumField) asks.
func (e GroupVisibility) MarshalGQL(w io.Writer) {
	switch e {
	case GroupVisibilityPublic:
		_, _ = io.WriteString(w, `"public"`)
	case GroupVisibilityPrivate:
		_, _ = io.WriteString(w, `"private"`)
	default: // not a value of the enum
		gqlgraph.WriteString(w, string(e))
	}
}

// UnmarshalGQL sets e from v, a String that is one of its stored values.
func (e *GroupVisibility) UnmarshalGQL(v any) error {
	switch v {
	case "public":
		*e = GroupVisibilityPublic
	case "private":
		*e = GroupVisibilityPrivate
	default:
		return gqlgraph.InputErrorf("GroupVisibility must be public or private, not %v", v)
	}
	return nil
}

// Users is the edge users: a connection to the Account nodes it leads to. Its
// queries batch with the same edge of every row on g's page.
func (g *Group) Users(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *AccountWhereInput,
) (*AccountConnection, error) {
	return newAccountConnection(ctx, groupType.Edge(g, groupEdgeUsers), after, first, before, last, where)
}

// GroupConnection is the GraphQL type GroupConnection. Its fields run their
// queries when they are resolved.
type GroupConnection struct {
	c *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *GroupConnection) Edges(ctx context.Context) ([]*GroupEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]GroupEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*GroupEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = GroupEdge{Node: p.Node(i).(*Group), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *GroupConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Group nodes in the whole connection, not only on
// this page.
func (c *GroupConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// GroupEdge is the GraphQL type GroupEdge.
type GroupEdge struct {
	Node   *Group
	Cursor veloxgql.Cursor
}

// GroupPaginateOption is an option of a query's Paginate to Group nodes:
// WithGroupOrder. Of two options that set the same thing, the later wins.
type GroupPaginateOption struct {
	orderBy *GroupOrder
}

// WithGroupOrder orders the connection by orderBy and then the key.
func WithGroupOrder(orderBy *GroupOrder) GroupPaginateOption {
	return GroupPaginateOption{orderBy: orderBy}
}

// PaginateQuery is the connection to the Group nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key and the order
// columns.
func (*Group) PaginateQuery(
	ctx context.Context, q velox.Query[Group],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []GroupPaginateOption,
) (*GroupConnection, error) {
	var o GroupPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
	}
	return newGroupConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy)
}

// newGroupConnection is PaginateQuery of the nodes from leads to: a query's, or
// an edge's, whose queries batch.
func newGroupConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *GroupOrder,
) (*GroupConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &groupType, args)
	if err != nil {
		return nil, err
	}
	return &GroupConnection{c: c}, nil
}

// groupType describes the GraphQL type Group to gqlgraph.
var groupType = gqlgraph.Type{
	Name:  "Group",
	Table: untyped.TableOf[Group],
	Key:   []gqlgraph.Column{{Name: "id", Index: groupColID}},
	Order: []gqlgraph.Column{
		{Name: "NAME", Index: groupColName},
	},
	AlwaysRead: []int{groupColID},
	Fields: []string{
		groupColID:         "id",
		groupColName:       "name",
		groupColVisibility: "visibility",
	},
	Edges: []string{groupEdgeUsers: "users"},
}

// GroupOrder is the GraphQL input GroupOrder, an order of connections to Group.
type GroupOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field GroupOrderField
}

// GroupOrderField is the GraphQL enum GroupOrderField, the fields connections
// to Group can be ordered by.
type GroupOrderField string

// The values of GroupOrderField.
const (
	// GroupOrderFieldName orders by the field name.
	GroupOrderFieldName GroupOrderField = "NAME"
)

// IsValid reports whether o is a value of GroupOrderField.
func (o GroupOrderField) IsValid() bool { return groupType.IsOrderField(string(o)) }

// String returns o's value.
func (o GroupOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum GroupOrderField.
func (o GroupOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum GroupOrderField.
func (o *GroupOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &groupType)
}
