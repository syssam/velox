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

// Users is the edge users: the Account nodes it leads to, in key order. It is
// read in one query with the same edge of every row on g's page, and fails on
// more than MaxList nodes.
func (g *Group) Users(ctx context.Context) ([]*User, error) {
	v, err := gqlgraph.EdgeList(ctx, &userType, groupType.Edge(g, groupEdgeUsers), 0)
	n, _ := v.([]*User)
	return n, err
}

// GroupConnection is the GraphQL type GroupConnection. Its fields run their
// queries when they are resolved.
type GroupConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *GroupConnection) Edges(ctx context.Context) ([]*GroupEdge, error) {
	p, err := c.Conn.Page(ctx)
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
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Group nodes in the whole connection, not only on
// this page.
func (c *GroupConnection) TotalCount(ctx context.Context) (int, error) { return c.Conn.TotalCount(ctx) }

// GroupEdge is the GraphQL type GroupEdge.
type GroupEdge struct {
	Node   *Group
	Cursor veloxgql.Cursor
}

// GroupPaginateOption is an option of a query's Paginate to Group nodes. It has
// none: Group has no order or where input.
type GroupPaginateOption struct{}

// PaginateQuery is the connection to the Group nodes q returns, ordered by the
// key, and paged by the other arguments: velox.Query's Paginate. q must not be
// ordered, and must select the key.
func (*Group) PaginateQuery(
	ctx context.Context, q velox.Query[Group],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []GroupPaginateOption,
) (*GroupConnection, error) {
	return newGroupConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last)
}

// newGroupConnection is PaginateQuery of the nodes from leads to: a query's, or
// an edge's, whose queries batch.
func newGroupConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*GroupConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	c, err := gqlgraph.Paginate(ctx, from, &groupType, args)
	if err != nil {
		return nil, err
	}
	return &GroupConnection{Conn: c}, nil
}

// groupType describes the GraphQL type Group to gqlgraph.
var groupType = gqlgraph.Type{
	Name:       "Group",
	Table:      untyped.TableOf[Group],
	Key:        []gqlgraph.Column{{Name: "id", Index: groupColID}},
	AlwaysRead: []int{groupColID},
	Fields: []string{
		groupColID:         "id",
		groupColName:       "name",
		groupColVisibility: "visibility",
	},
	Edges:          []string{groupEdgeUsers: "users"},
	LenientFilters: true,
}
