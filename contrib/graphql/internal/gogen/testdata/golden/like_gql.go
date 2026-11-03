package gen

import (
	"context"
	"time"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion11

// UserGQL is the edge user: the Account it leads to, or nil. It is read in one
// query with the edge of every row read with x.
func (x *Like) UserGQL(ctx context.Context) (*User, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[User]())
	if fields, ok := gqlimpl.Selected(ctx, "Account"); ok {
		q = gqlimpl.Narrow(q, userColumnsGQL(fields))
	}
	p, err := gqlimpl.EdgeOne(ctx, q, velox.TableOf[Like](), unsafe.Pointer(x), likeEdgeUser, "Like.user")
	return (*User)(p), err
}

// PostGQL is the edge post: the Post it leads to, or nil. It is read in one
// query with the edge of every row read with x.
func (x *Like) PostGQL(ctx context.Context) (*Post, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[Post]())
	if fields, ok := gqlimpl.Selected(ctx, "Post"); ok {
		q = gqlimpl.Narrow(q, postColumnsGQL(fields))
	}
	p, err := gqlimpl.EdgeOne(ctx, q, velox.TableOf[Like](), unsafe.Pointer(x), likeEdgePost, "Like.post")
	return (*Post)(p), err
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
	for i, x := range p.Rows {
		slab[i] = LikeEdge{Node: (*Like)(x), Cursor: p.Cursors[i]}
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
func LikesConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
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
func LikesConnectionOf(ctx context.Context, q velox.Query[Like], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &likeConnGQL, nil)
	if err != nil {
		return nil, err
	}
	c, err := gqlimpl.NewConnection(ctx, b, &likeConnGQL, gqlimpl.Args{After: after, First: first, Before: before, Last: last}, nil)
	if err != nil {
		return nil, err
	}
	return &LikeConnection{c: c}, nil
}

// likeConnGQL is the key and the order columns of the connections to Like.
var likeConnGQL = gqlimpl.Conn{
	Type: "Like",
	Key: []gqlimpl.Col{
		{Field: "user_id", Col: likeColUserID},
		{Field: "post_id", Col: likeColPostID},
	},
	Columns: likeColumnsGQL,
}

// likeColumnsGQL returns the columns a selection of the given Like fields
// reads. It returns nil, every column, for a field that is not velox-graphql's;
// see gqlimpl.Conn.
func likeColumnsGQL(fields []string) []int {
	cols := []int{likeColUserID, likeColPostID}
	for _, f := range fields {
		switch f {
		case "user", "post":
		case "weight":
			cols = append(cols, likeColWeight)
		case "createdAt":
			cols = append(cols, likeColCreatedAt)
		default:
			return nil
		}
	}
	return cols
}

// likeInputGQL is the CreateLikeInput field that sets the Like field goField,
// or "". A validator's error names it.
func likeInputGQL(goField string) string {
	switch goField {
	case "Weight":
		return "weight"
	case "CreatedAt":
		return "createdAt"
	case "UserID":
		return "userID"
	case "PostID":
		return "postID"
	}
	return ""
}

// CreateLikeInput is the GraphQL input CreateLikeInput, which Create writes. A
// nil pointer or list is a field not given.
type CreateLikeInput struct {
	Weight    *int8
	CreatedAt *time.Time
	// UserID is the ID of the Account the edge user leads to.
	UserID string
	// PostID is the ID of the Post the edge post leads to.
	PostID string
}

// Create inserts x, a Like, with the input's fields and edges set; a nil x is a
// new one. It runs atomically on the DB in ctx, under the policies.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateLikeInput) Create(ctx context.Context, x *Like) (*Like, error) {
	if x == nil {
		x = new(Like)
	}
	c := gqlimpl.NewCreate(ctx, "CreateLikeInput", "Like", "gen.Like", velox.TableOf[Like](), unsafe.Pointer(x), likeInputGQL)
	if in.Weight != nil {
		x.Weight = *in.Weight
		c.Given(likeColWeight)
	}
	if in.CreatedAt != nil {
		x.CreatedAt = *in.CreatedAt
		gqlimpl.NotZero(c, "createdAt", x.CreatedAt)
		c.Given(likeColCreatedAt)
	}
	userIDKey := gqlimpl.Key[int64](c, "userID", in.UserID, "Account")
	gqlimpl.Exists(c, "userID", "Account", velox.TableOf[User](), userColID, []int64{userIDKey})
	x.UserID = userIDKey
	postIDKey := gqlimpl.Key[int64](c, "postID", in.PostID, "Post")
	gqlimpl.Exists(c, "postID", "Post", velox.TableOf[Post](), postColID, []int64{postIDKey})
	x.PostID = postIDKey
	if err := c.Insert(); err != nil {
		return nil, err
	}
	return x, nil
}
