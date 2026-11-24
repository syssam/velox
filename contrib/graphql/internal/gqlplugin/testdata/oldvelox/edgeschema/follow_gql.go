package gen

import (
	"context"
	"io"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	_ "github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

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
// queries when they are resolved.
type FollowConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *FollowConnection) Edges(ctx context.Context) ([]*FollowEdge, error) {
	p, err := c.Conn.Page(ctx)
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
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Follow nodes in the whole connection, not only on
// this page.
func (c *FollowConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// FollowEdge is the GraphQL type FollowEdge.
type FollowEdge struct {
	Node   *Follow
	Cursor veloxgql.Cursor
}

// FollowPaginateOption is an option of a query's Paginate to Follow nodes:
// WithFollowOrder. Of two options that set the same thing, the later wins.
type FollowPaginateOption struct {
	orderBy *FollowOrder
}

// WithFollowOrder orders the connection by orderBy and then the key.
func WithFollowOrder(orderBy *FollowOrder) FollowPaginateOption {
	return FollowPaginateOption{orderBy: orderBy}
}

// PaginateQuery is the connection to the Follow nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key and the order
// columns.
func (*Follow) PaginateQuery(
	ctx context.Context, q velox.Query[Follow],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []FollowPaginateOption,
) (*FollowConnection, error) {
	var o FollowPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
	}
	return newFollowConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy)
}

// newFollowConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newFollowConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *FollowOrder,
) (*FollowConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &followType, args)
	if err != nil {
		return nil, err
	}
	return &FollowConnection{Conn: c}, nil
}

// followType describes the GraphQL type Follow to gqlgraph.
var followType = gqlgraph.Type{
	Name:  "Follow",
	Table: untyped.TableOf[Follow],
	Key:   []gqlgraph.Column{{Name: "id", Index: followColID}},
	Order: []gqlgraph.Column{
		{Name: "NOTE", Index: followColNote},
		{Name: "USER_ID", Index: followColUserID},
		{Name: "FOLLOWER_ID", Index: followColFollowerID},
	},
	AlwaysRead: []int{followColID, followColUserID, followColFollowerID},
	Fields: []string{
		followColID:         "id",
		followColNote:       "note",
		followColUserID:     "userId",
		followColFollowerID: "followerId",
	},
	Edges: []string{followEdgeUser: "user", followEdgeFollower: "follower"},
	Inputs: []string{
		followColNote:       "note",
		followColUserID:     "userId",
		followColFollowerID: "followerId",
	},
	LenientFilters: true,
}

// FollowOrder is the GraphQL input FollowOrder, an order of connections to
// Follow.
type FollowOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field FollowOrderField
}

// FollowOrderField is the GraphQL enum FollowOrderField, the fields connections
// to Follow can be ordered by.
type FollowOrderField string

// The values of FollowOrderField.
const (
	// FollowOrderFieldNote orders by the field note.
	FollowOrderFieldNote FollowOrderField = "NOTE"
	// FollowOrderFieldUserID orders by the field user_id.
	FollowOrderFieldUserID FollowOrderField = "USER_ID"
	// FollowOrderFieldFollowerID orders by the field follower_id.
	FollowOrderFieldFollowerID FollowOrderField = "FOLLOWER_ID"
)

// IsValid reports whether o is a value of FollowOrderField.
func (o FollowOrderField) IsValid() bool { return followType.IsOrderField(string(o)) }

// String returns o's value.
func (o FollowOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum FollowOrderField.
func (o FollowOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum FollowOrderField.
func (o *FollowOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &followType)
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
