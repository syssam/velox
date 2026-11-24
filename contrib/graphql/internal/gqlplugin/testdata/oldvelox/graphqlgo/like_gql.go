package gen

import (
	"context"
	"io"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// User is the edge user: the Account it leads to, or nil. It is read in one
// query with the same edge of every row on l's page.
func (l *Like) User(ctx context.Context) (*User, error) {
	v, err := gqlgraph.EdgeOne(ctx, &userType, likeType.Edge(l, likeEdgeUser))
	n, _ := v.(*User)
	return n, err
}

// Post is the edge post: the Post it leads to, or nil. It is read in one query
// with the same edge of every row on l's page.
func (l *Like) Post(ctx context.Context) (*Post, error) {
	v, err := gqlgraph.EdgeOne(ctx, &postType, likeType.Edge(l, likeEdgePost))
	n, _ := v.(*Post)
	return n, err
}

// LikeConnection is the GraphQL type LikeConnection. Its fields run their
// queries when they are resolved.
type LikeConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *LikeConnection) Edges(ctx context.Context) ([]*LikeEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]LikeEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*LikeEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = LikeEdge{Node: p.Node(i).(*Like), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *LikeConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Like nodes in the whole connection, not only on
// this page.
func (c *LikeConnection) TotalCount(ctx context.Context) (int, error) { return c.Conn.TotalCount(ctx) }

// LikeEdge is the GraphQL type LikeEdge.
type LikeEdge struct {
	Node   *Like
	Cursor veloxgql.Cursor
}

// LikePaginateOption is an option of a query's Paginate to Like nodes:
// WithLikeOrder. Of two options that set the same thing, the later wins.
type LikePaginateOption struct {
	orderBy *LikeOrder
}

// WithLikeOrder orders the connection by orderBy and then the key.
func WithLikeOrder(orderBy *LikeOrder) LikePaginateOption {
	return LikePaginateOption{orderBy: orderBy}
}

// PaginateQuery is the connection to the Like nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key and the order
// columns.
func (*Like) PaginateQuery(
	ctx context.Context, q velox.Query[Like],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []LikePaginateOption,
) (*LikeConnection, error) {
	var o LikePaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
	}
	return newLikeConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy)
}

// newLikeConnection is PaginateQuery of the nodes from leads to: a query's, or
// an edge's, whose queries batch.
func newLikeConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *LikeOrder,
) (*LikeConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &likeType, args)
	if err != nil {
		return nil, err
	}
	return &LikeConnection{Conn: c}, nil
}

// likeType describes the GraphQL type Like to gqlgraph.
var likeType = gqlgraph.Type{
	Name:  "Like",
	Table: untyped.TableOf[Like],
	Key: []gqlgraph.Column{
		{Name: "user_id", Index: likeColUserID},
		{Name: "post_id", Index: likeColPostID},
	},
	Order: []gqlgraph.Column{
		{Name: "USER_ID", Index: likeColUserID},
		{Name: "POST_ID", Index: likeColPostID},
		{Name: "CREATED_AT", Index: likeColCreatedAt},
	},
	AlwaysRead: []int{likeColUserID, likeColPostID},
	Fields: []string{
		likeColUserID:    "userId",
		likeColPostID:    "postId",
		likeColWeight:    "weight",
		likeColCreatedAt: "createdAt",
	},
	Edges: []string{likeEdgeUser: "user", likeEdgePost: "post"},
	Inputs: []string{
		likeColWeight:    "weight",
		likeColCreatedAt: "createdAt",
		likeColUserID:    "userId",
		likeColPostID:    "postId",
	},
	LenientFilters: true,
}

// LikeOrder is the GraphQL input LikeOrder, an order of connections to Like.
type LikeOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field LikeOrderField
}

// LikeOrderField is the GraphQL enum LikeOrderField, the fields connections to
// Like can be ordered by.
type LikeOrderField string

// The values of LikeOrderField.
const (
	// LikeOrderFieldUserID orders by the field user_id.
	LikeOrderFieldUserID LikeOrderField = "USER_ID"
	// LikeOrderFieldPostID orders by the field post_id.
	LikeOrderFieldPostID LikeOrderField = "POST_ID"
	// LikeOrderFieldCreatedAt orders by the field created_at.
	LikeOrderFieldCreatedAt LikeOrderField = "CREATED_AT"
)

// IsValid reports whether o is a value of LikeOrderField.
func (o LikeOrderField) IsValid() bool { return likeType.IsOrderField(string(o)) }

// String returns o's value.
func (o LikeOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum LikeOrderField.
func (o LikeOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum LikeOrderField.
func (o *LikeOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &likeType)
}

// CreateLikeInput is the GraphQL input CreateLikeInput. A nil pointer or list
// is a field not given, and so is a null.
type CreateLikeInput struct {
	Weight    *int8
	CreatedAt *time.Time
	// UserID is the ID of the Account the edge user leads to.
	UserID int64
	// PostID is the ID of the Post the edge post leads to.
	PostID int64
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateLikeInput) Setters() ([]velox.Setter[Like], error) {
	var set []velox.Setter[Like]
	if v := in.Weight; v != nil {
		set = append(set, Likes.Weight.Set(*v))
	}
	if v := in.CreatedAt; v != nil {
		set = append(set, Likes.CreatedAt.Set(*v))
	}
	set = append(set, Likes.User.Set(in.UserID))
	set = append(set, Likes.Post.Set(in.PostID))
	return gqlgraph.Validate("CreateLikeInput", &likeType, set)
}
