package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion28

// User is the edge user: the User it leads to, or nil. It is read in one query
// with the same edge of every row on f's page.
func (f *Follow) User(ctx context.Context) (*User, error) {
	v, err := gqlgraph.EdgeOne(ctx, &userType, followType.Edge(f, followEdgeUser))
	n, _ := v.(*User)
	return n, err
}

// Follower is the edge follower: the User it leads to, or nil. It is read in
// one query with the same edge of every row on f's page.
func (f *Follow) Follower(ctx context.Context) (*User, error) {
	v, err := gqlgraph.EdgeOne(ctx, &userType, followType.Edge(f, followEdgeFollower))
	n, _ := v.(*User)
	return n, err
}

// FollowConnection is the GraphQL type FollowConnection. Its fields run their
// queries when gqlgen resolves them.
type FollowConnection struct {
	c *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *FollowConnection) Edges(ctx context.Context) ([]*FollowEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]FollowEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*FollowEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = FollowEdge{Node: p.Node(i).(*Follow), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *FollowConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Follow nodes in the whole connection, not only on
// this page.
func (c *FollowConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// FollowEdge is the GraphQL type FollowEdge.
type FollowEdge struct {
	Node   *Follow
	Cursor veloxgql.Cursor
}

// FollowPaginateOption is an option of a query's Paginate to Follow nodes. It
// has none: Follow has no order or where input.
type FollowPaginateOption struct{}

// PaginateQuery is the connection to the Follow nodes q returns, ordered by the
// key, and paged by the other arguments: velox.Query's Paginate. q must not be
// ordered, and must select the key.
func (*Follow) PaginateQuery(
	ctx context.Context, q velox.Query[Follow],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []FollowPaginateOption,
) (*FollowConnection, error) {
	return newFollowConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last)
}

// newFollowConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newFollowConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*FollowConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	c, err := gqlgraph.Paginate(ctx, from, &followType, args)
	if err != nil {
		return nil, err
	}
	return &FollowConnection{c: c}, nil
}

// followType describes the GraphQL type Follow to gqlgraph.
var followType = gqlgraph.Type{
	Name:       "Follow",
	Table:      untyped.TableOf[Follow],
	Key:        []gqlgraph.Column{{Name: "id", Index: followColID}},
	AlwaysRead: []int{followColID, followColUserID, followColFollowerID},
	Fields:     []string{followColID: "id", followColNote: "note"},
	Edges:      []string{followEdgeUser: "user", followEdgeFollower: "follower"},
	Inputs: []string{
		followColNote:       "note",
		followColUserID:     "userId",
		followColFollowerID: "followerId",
	},
}

// CreateFollowInput is the GraphQL input CreateFollowInput. A nil pointer or
// list is a field not given, and so is a null.
type CreateFollowInput struct {
	Note string
	// UserID is the ID of the User the edge user leads to.
	UserID int64
	// FollowerID is the ID of the User the edge follower leads to.
	FollowerID int64
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateFollowInput) Setters() ([]velox.Setter[Follow], error) {
	var set []velox.Setter[Follow]
	set = append(set, Follows.Note.Set(in.Note))
	set = append(set, Follows.User.Set(in.UserID))
	set = append(set, Follows.Follower.Set(in.FollowerID))
	return gqlgraph.Validate("CreateFollowInput", &followType, set)
}
