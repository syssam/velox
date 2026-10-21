package gen

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	return LikesConnectionOf(ctx, db.Query[Like](), after, first, before, last)
}

// LikesConnectionOf is the connection to the Like nodes q returns, ordered by
// the key, and paged by the other arguments. q keeps its filters and With
// loads, and the scope the application gave it; its Offset is cleared and its
// Limit replaced, since the page is the connection's. q must have no OrderBy:
// the connection orders it, and a q with one is refused with an internal error,
// since its order would come first and break the cursors. q must also select
// the key and the columns orderBy names: a Columns or Omit that drops them
// breaks the cursors, and nothing refuses it. It runs no query.
func LikesConnectionOf(ctx context.Context, q velox.Query[Like], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
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

// likeFieldGQL is the GraphQL name of the mutation-input field that sets the
// Like field whose Go name is goField, for the errors of its validators: "" for
// a field no input sets.
func likeFieldGQL(goField string) string {
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

// CreateLikeInput is the GraphQL input CreateLikeInput, the input of
// createLike. A field that is a pointer, or a list, is optional: nil means not
// given.
type CreateLikeInput struct {
	Weight    *int8
	CreatedAt *time.Time
	// UserID is the ID of the Account the edge user leads to.
	UserID string
	// PostID is the ID of the Post the edge post leads to.
	PostID string
}

// String returns the input's fields except its sensitive ones.
func (in CreateLikeInput) String() string {
	var p0 any
	if in.Weight != nil {
		p0 = *in.Weight
	}
	var p1 any
	if in.CreatedAt != nil {
		p1 = *in.CreatedAt
	}
	return fmt.Sprintf("CreateLikeInput(Weight=%v, CreatedAt=%v, UserID=%v, PostID=%v)", p0, p1, in.UserID, in.PostID)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateLikeInput) GoString() string { return in.String() }

// CreateLike creates one Like from input, with its edges, and returns it, as
// the mutation createLike does. It first decodes every ID and JSON value of
// input, and fails before any statement on one it cannot decode
// (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom reads from
// ctx, in one transaction (gqlimpl.Atomic): the operation's, or one of its own.
// The tenant scope and the policies apply as to any write: each ID must name a
// row the caller may read, and a row it re-points one the caller may change, or
// CreateLike fails with NOT_FOUND and keeps nothing. A validator's refusal is
// BAD_USER_INPUT, naming the input field; velox's other errors are returned as
// they are.
func CreateLike(ctx context.Context, input CreateLikeInput) (*Like, error) {
	var (
		k2  int64
		k3  int64
		err error
	)
	if k2, err = gqlimpl.DecodeID[int64]("CreateLikeInput.userID", input.UserID, "Account"); err != nil {
		return nil, err
	}
	if k3, err = gqlimpl.DecodeID[int64]("CreateLikeInput.postID", input.PostID, "Post"); err != nil {
		return nil, err
	}
	var out *Like
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		var x Like
		if err := gqlimpl.Exist(ctx, db.Query[User]().Where(Users.ID.In(k2)), 1, "CreateLikeInput.userID", "Account"); err != nil {
			return err
		}
		x.UserID = k2
		if err := gqlimpl.Exist(ctx, db.Query[Post]().Where(Posts.ID.In(k3)), 1, "CreateLikeInput.postID", "Post"); err != nil {
			return err
		}
		x.PostID = k3
		if input.Weight != nil {
			x.Weight = *input.Weight
		}
		if input.CreatedAt != nil {
			x.CreatedAt = *input.CreatedAt
		}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			return gqlimpl.WriteError(err, "CreateLikeInput", "gen.Like", likeFieldGQL)
		}
		out = &x
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
