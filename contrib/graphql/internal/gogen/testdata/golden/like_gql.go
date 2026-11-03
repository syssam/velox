package gen

import (
	"context"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion10

// UserGQL is the edge user: the Account it leads to, or nil when there is none.
// It is read with the edge of every row read with x, in one query.
func (x *Like) UserGQL(ctx context.Context) (*User, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[User]())
	if fields, ok := gqlimpl.Selected(ctx, "Account"); ok {
		q = gqlimpl.Narrow(q, userColumnsGQL(fields))
	}
	return gqlimpl.EdgeOne(ctx, q, x, Likes.User, "Like.user")
}

// PostGQL is the edge post: the Post it leads to, or nil when there is none. It
// is read with the edge of every row read with x, in one query.
func (x *Like) PostGQL(ctx context.Context) (*Post, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[Post]())
	if fields, ok := gqlimpl.Selected(ctx, "Post"); ok {
		q = gqlimpl.Narrow(q, postColumnsGQL(fields))
	}
	return gqlimpl.EdgeOne(ctx, q, x, Likes.Post, "Like.post")
}

// LikeConnection is the GraphQL type LikeConnection, a connection to a list of
// Like. It holds the query of the whole list and the page asked for; gqlgen
// calls its methods for the fields a query selects, and each runs its query
// only then: edges and pageInfo share one page query, and totalCount runs a
// COUNT.
type LikeConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
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

// PageInfo is the page info of the page; see Edges.
func (c *LikeConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Like nodes in the whole connection, not only on
// this page. It counts the rows when gqlgen asks for it, which is only when the
// field totalCount is selected.
func (c *LikeConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// LikeEdge is the GraphQL type LikeEdge, an edge in a connection to a list of
// Like.
type LikeEdge struct {
	// Node is the Like at the end of the edge.
	Node *Like
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// LikesConnection is the connection to every Like node, through the DB
// veloxgql.DBFromContext reads from ctx, paged and ordered by the arguments of
// a connection to Like: a Query field's resolver returns it. It fails before
// any query runs on arguments gqlimpl.NewConnection refuses.
func LikesConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return LikesConnectionOf(ctx, db.Query[Like](), after, first, before, last)
}

// LikesConnectionOf is the connection to the Like nodes q returns, ordered by
// the key, and paged by the other arguments. q keeps its filters and With
// loads, and the scope the application gave it; its Offset is cleared and its
// Limit replaced, since the page is the connection's. q must have no OrderBy:
// the connection orders it, and a q with one is refused with an internal error,
// since its order would come first and break the cursors. q must also select
// the key and the columns orderBy names, which the cursors hold: a q whose
// Columns or Omit drops one is refused with an internal error. In a gqlgen
// field, the page reads only the columns the selection needs (likeColumnsGQL),
// of those q selects; a column the selection does not need is left zero in the
// nodes, except the key, the foreign keys and the order columns, so an
// application's resolver must not read one it did not ask for. It runs no
// query.
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

// likeConnGQL is what the connections to Like are made of: the key, which ends
// every order, and the order fields' columns.
var likeConnGQL = gqlimpl.Conn{
	Type: "Like",
	Key: []gqlimpl.Col{
		{Field: "user_id", Col: likeColUserID},
		{Field: "post_id", Col: likeColPostID},
	},
	Columns: likeColumnsGQL,
}

// likeColumnsGQL is the columns a selection of the Like fields fields reads:
// the key, every foreign key on the row, every order field's column, and the
// column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
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

// likeInputGQL is the GraphQL name of the CreateLikeInput field that sets the
// Like field whose Go name is goField, for the errors of its validators: "" for
// a field it does not set.
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

// CreateLikeInput is the GraphQL input CreateLikeInput; its method Create
// writes it. A field that is a pointer, or a list, is optional: nil means not
// given.
type CreateLikeInput struct {
	Weight    *int8
	CreatedAt *time.Time
	// UserID is the ID of the Account the edge user leads to.
	UserID string
	// PostID is the ID of the Post the edge post leads to.
	PostID string
}

// Create inserts x, a Like, with the input's fields and edges set; a nil x is a
// new one. The input's fields overwrite x's: to own a field, leave it out with
// graphql.Skip(graphql.SkipMutationCreateInput) or set it in a velox.Hook. x's
// other fields, foreign keys included, are trusted, not checked. It runs
// atomically on the DB in ctx, under the policies, and changes x even when it
// fails.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateLikeInput) Create(ctx context.Context, x *Like) (*Like, error) {
	c, x := gqlimpl.NewCreate(ctx, "CreateLikeInput", "Like", "gen.Like", x, likeInputGQL)
	if in.Weight != nil {
		x.Weight = *in.Weight
		c.Given(Likes.Weight)
	}
	if in.CreatedAt != nil {
		x.CreatedAt = *in.CreatedAt
		gqlimpl.NotZero(c, "createdAt", x.CreatedAt)
		c.Given(Likes.CreatedAt)
	}
	userIDKey := gqlimpl.Key[int64](c, "userID", in.UserID, "Account")
	gqlimpl.Exists(c, "userID", "Account", 1, Users.ID.EQ(userIDKey))
	x.UserID = userIDKey
	postIDKey := gqlimpl.Key[int64](c, "postID", in.PostID, "Post")
	gqlimpl.Exists(c, "postID", "Post", 1, Posts.ID.EQ(postIDKey))
	x.PostID = postIDKey
	return c.Insert()
}
