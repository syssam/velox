package gen

import (
	"context"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion15

// User is the edge user: the Account it leads to, or nil. It is read in one
// query with the edge of every row read with x.
func (x *Like) User(ctx context.Context) (*User, error) {
	v, err := gqlimpl.EdgeOne(ctx, &userConnGQL, &likeConnGQL, x, likeEdgeUser, "Like.user")
	n, _ := v.(*User)
	return n, err
}

// Post is the edge post: the Post it leads to, or nil. It is read in one query
// with the edge of every row read with x.
func (x *Like) Post(ctx context.Context) (*Post, error) {
	v, err := gqlimpl.EdgeOne(ctx, &postConnGQL, &likeConnGQL, x, likeEdgePost, "Like.post")
	n, _ := v.(*Post)
	return n, err
}

// LikeConnection is the GraphQL type LikeConnection, a connection to a list of
// Like. Its fields run their queries when gqlgen asks; see gqlimpl.Connection.
type LikeConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *LikeConnection) Edges(ctx context.Context) ([]*LikeEdge, error) {
	p, err := c.c.Page(ctx)
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
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Like nodes in the whole connection, not only on
// this page.
func (c *LikeConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// LikeEdge is the GraphQL type LikeEdge, an edge in a connection to a list of
// Like.
type LikeEdge struct {
	// Node is the Like at the end of the edge.
	Node *Like
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// LikesConnection is the connection to every Like node, on the DB in ctx, for a
// Query field's resolver. It is LikesConnectionOf of the whole table.
func LikesConnection(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*LikeConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return LikesConnectionOf(ctx, db.Query[Like](), after, first, before, last)
}

// LikesConnectionOf is the connection to the Like nodes q returns, ordered by
// the key, and paged by the other arguments. q must not be ordered and must
// select the key and the order columns; see gqlimpl.ConnectionBase and
// gqlimpl.NewConnection.
func LikesConnectionOf(
	ctx context.Context, q velox.Query[Like],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*LikeConnection, error) {
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &likeConnGQL, nil)
	if err != nil {
		return nil, err
	}
	c, err := gqlimpl.NewConnection(
		ctx, b, &likeConnGQL,
		gqlimpl.Args{After: after, First: first, Before: before, Last: last},
		nil,
	)
	if err != nil {
		return nil, err
	}
	return &LikeConnection{c: c}, nil
}

// likeConnGQL is what the runtime knows of the GraphQL type Like: its table,
// the key and the order columns of its connections, the columns its selections
// read and the fields of its inputs.
var likeConnGQL = gqlimpl.Conn{
	Type:  "Like",
	Table: velox.TableOf[Like],
	Key: []gqlimpl.Col{
		{Field: "user_id", Col: likeColUserID},
		{Field: "post_id", Col: likeColPostID},
	},
	Read:   []int{likeColUserID, likeColPostID},
	Fields: []string{likeColWeight: "weight", likeColCreatedAt: "createdAt"},
	Edges:  []string{"user", "post"},
	Inputs: []string{
		likeColWeight:    "weight",
		likeColCreatedAt: "createdAt",
		likeColUserID:    "userID",
		likeColPostID:    "postID",
	},
}

// CreateLikeInput is the GraphQL input CreateLikeInput, whose Setters gives its
// fields as setters. A nil pointer or list is a field not given, and so is a
// null.
type CreateLikeInput struct {
	Weight    *int8
	CreatedAt *time.Time
	// UserID is the ID of the Account the edge user leads to.
	UserID string
	// PostID is the ID of the Post the edge post leads to.
	PostID string
}

// Setters returns the setters of the input's fields and edges, for
// db.Insert(x).Set: a field not given sets nothing, and x keeps its value or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or an ID that does not decode or
// names another type, is BAD_USER_INPUT naming the input's field. See
// "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each field does and what the
// write checks.
func (in *CreateLikeInput) Setters() ([]velox.Setter[Like], error) {
	var set []velox.Setter[Like]
	if v := in.Weight; v != nil {
		set = append(set, Likes.Weight.Set(*v))
	}
	if v := in.CreatedAt; v != nil {
		if err := gqlimpl.NotZero("CreateLikeInput.createdAt", *v); err != nil {
			return nil, err
		}
		set = append(set, Likes.CreatedAt.Set(*v))
	}
	userID, err := gqlimpl.DecodeID[int64]("CreateLikeInput.userID", in.UserID, "Account")
	if err != nil {
		return nil, err
	}
	set = append(set, Likes.User.Set(userID))
	postID, err := gqlimpl.DecodeID[int64]("CreateLikeInput.postID", in.PostID, "Post")
	if err != nil {
		return nil, err
	}
	set = append(set, Likes.Post.Set(postID))
	return gqlimpl.Checked("CreateLikeInput", &likeConnGQL, set)
}
