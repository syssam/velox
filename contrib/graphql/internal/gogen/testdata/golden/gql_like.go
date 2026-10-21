package gen

import (
	"context"
	"errors"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion1

// UserGQL is the edge user: the Account it leads to, or nil when there is none.
// It is x.User when a With loaded it, and else one query.
func (x *Like) UserGQL(ctx context.Context) (*User, error) {
	if v, ok := x.User.Get(); ok {
		return v, nil
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	v, err := db.Query[User]().Where(Users.ID.EQ(x.UserID)).One(ctx)
	if errors.Is(err, velox.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

// PostGQL is the edge post: the Post it leads to, or nil when there is none. It
// is x.Post when a With loaded it, and else one query.
func (x *Like) PostGQL(ctx context.Context) (*Post, error) {
	if v, ok := x.Post.Get(); ok {
		return v, nil
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	v, err := db.Query[Post]().Where(Posts.ID.EQ(x.PostID)).One(ctx)
	if errors.Is(err, velox.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

// LikeConnection is the GraphQL type LikeConnection, a connection to a list of
// Like. It holds the query of the whole list and the page asked for; gqlgen
// calls its methods for the fields a query selects, and each runs its query
// only then: edges and pageInfo share one page query, and totalCount runs a
// COUNT.
type LikeConnection struct {
	c *gqlimpl.Connection[Like]
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *LikeConnection) Edges(ctx context.Context) ([]*LikeEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	edges := make([]*LikeEdge, len(p.Rows))
	for i, x := range p.Rows {
		edges[i] = &LikeEdge{Node: x, Cursor: p.Cursors[i]}
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
// veloxgql.DBFrom reads from ctx, paged and ordered by the arguments of a
// connection to Like: a Query field's resolver returns it. It fails before any
// query runs on arguments gqlimpl.NewConnection refuses.
func LikesConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return likeConnectionGQL(ctx, db.Query[Like](), after, first, before, last)
}

// likeConnectionGQL is the connection to the Like nodes q returns, ordered by
// the key, and paged by the other arguments. It runs no query.
func likeConnectionGQL(ctx context.Context, q velox.Query[Like], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
	c, err := gqlimpl.NewConnection(ctx, q, "Like", gqlimpl.Args{After: after, First: first, Before: before, Last: last}, nil, likeKeyGQL())
	if err != nil {
		return nil, err
	}
	return &LikeConnection{c: c}, nil
}

// likeKeyGQL is the order of Like's key, which ends every order of a connection
// to Like.
func likeKeyGQL() []gqlimpl.OrderTerm[Like] {
	return []gqlimpl.OrderTerm[Like]{
		gqlimpl.Term("user_id", veloxgql.OrderDirectionAsc, Likes.UserID.Asc(), Likes.UserID.Desc(),
			func(x *Like) int64 { return x.UserID },
			func(x *Like, v int64) { x.UserID = v }),
		gqlimpl.Term("post_id", veloxgql.OrderDirectionAsc, Likes.PostID.Asc(), Likes.PostID.Desc(),
			func(x *Like) int64 { return x.PostID },
			func(x *Like, v int64) { x.PostID = v }),
	}
}
