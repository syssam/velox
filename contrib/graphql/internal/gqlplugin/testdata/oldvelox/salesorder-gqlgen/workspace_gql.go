package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// WorkspaceConnection is the GraphQL type WorkspaceConnection. Its fields run
// their queries when gqlgen resolves them.
type WorkspaceConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *WorkspaceConnection) Edges(ctx context.Context) ([]*WorkspaceEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]WorkspaceEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*WorkspaceEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = WorkspaceEdge{Node: p.Node(i).(*Workspace), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *WorkspaceConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Workspace nodes in the whole connection, not only
// on this page.
func (c *WorkspaceConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// WorkspaceEdge is the GraphQL type WorkspaceEdge.
type WorkspaceEdge struct {
	Node   *Workspace
	Cursor veloxgql.Cursor
}

// WorkspacePaginateOption is an option of a query's Paginate to Workspace
// nodes. It has none: Workspace has no order or where input.
type WorkspacePaginateOption struct{}

// PaginateQuery is the connection to the Workspace nodes q returns, ordered by
// the key, and paged by the other arguments: velox.Query's Paginate. q must not
// be ordered, and must select the key.
func (*Workspace) PaginateQuery(
	ctx context.Context, q velox.Query[Workspace],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []WorkspacePaginateOption,
) (*WorkspaceConnection, error) {
	return newWorkspaceConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last)
}

// newWorkspaceConnection is PaginateQuery of the nodes from leads to: a
// query's, or an edge's, whose queries batch.
func newWorkspaceConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*WorkspaceConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	c, err := gqlgraph.Paginate(ctx, from, &workspaceType, args)
	if err != nil {
		return nil, err
	}
	return &WorkspaceConnection{Conn: c}, nil
}

// workspaceType describes the GraphQL type Workspace to gqlgraph.
var workspaceType = gqlgraph.Type{
	Name:       "Workspace",
	Table:      untyped.TableOf[Workspace],
	Key:        []gqlgraph.Column{{Name: "id", Index: workspaceColID}},
	AlwaysRead: []int{workspaceColID},
	Fields: []string{
		workspaceColID:        "id",
		workspaceColSlug:      "slug",
		workspaceColSettings:  "settings",
		workspaceColAssignees: "assignees",
	},
	LenientFilters: true,
}
