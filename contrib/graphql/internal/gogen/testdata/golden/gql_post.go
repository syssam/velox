package gen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// IsNode marks Post as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Post) IsNode() {}

// IDGQL is the global ID of the Post, the GraphQL field id: the type Post and
// the key id, encoded.
func (x *Post) IDGQL() string { return veloxgql.EncodeID("Post", x.ID) }

// TagsGQL is the field tags as JSON, the GraphQL scalar it is: gqlgen binds the
// field to it.
func (x *Post) TagsGQL() (veloxgql.JSON, error) {
	b, err := json.Marshal(x.Tags)
	return veloxgql.JSON(b), err
}

// MarshalGQL writes e as its value of the GraphQL enum PostStatus.
func (e PostStatus) MarshalGQL(w io.Writer) {
	var s string
	switch e {
	case "draft":
		s = "DRAFT"
	case "in_review":
		s = "IN_REVIEW"
	case "published":
		s = "LIVE"
	default:
		s = string(e) // not a value of the enum
	}
	b, _ := json.Marshal(s) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum PostStatus.
func (e *PostStatus) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	switch s {
	case "DRAFT":
		*e = "draft"
	case "IN_REVIEW":
		*e = "in_review"
	case "LIVE":
		*e = "published"
	default:
		return errors.New("PostStatus must be DRAFT, IN_REVIEW or LIVE")
	}
	return nil
}

// AuthorGQL is the edge author: the Account it leads to, or nil when there is
// none. It is x.Author when a With loaded it, and else one query.
func (x *Post) AuthorGQL(ctx context.Context) (*User, error) {
	if v, ok := x.Author.Get(); ok {
		return v, nil
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	v, err := db.Query[User]().Where(Users.ID.EQ(x.AuthorID)).One(ctx)
	if errors.Is(err, velox.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

// LikesGQL is the edge likes: the Like nodes it leads to, in key order. It is
// x.Likes when a With loaded it, and else one query, which reads at most the
// DB's WithMaxRows rows.
func (x *Post) LikesGQL(ctx context.Context) ([]*Like, error) {
	rows, ok := x.Likes.Get()
	if !ok {
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		if rows, err = db.Query[Like]().Where(Likes.PostID.EQ(x.ID)).OrderBy(Likes.UserID.Asc(), Likes.PostID.Asc()).Unbounded().All(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]*Like, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

// PostConnection is the GraphQL type PostConnection, a connection to a list of
// Post. It holds the query of the whole list and the page asked for; gqlgen
// calls its methods for the fields a query selects, and each runs its query
// only then: edges and pageInfo share one page query, and totalCount runs a
// COUNT.
type PostConnection struct {
	c *veloxgql.Connection[Post]
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *PostConnection) Edges(ctx context.Context) ([]*PostEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	edges := make([]*PostEdge, len(p.Rows))
	for i, x := range p.Rows {
		edges[i] = &PostEdge{Node: x, Cursor: p.Cursors[i]}
	}
	return edges, nil
}

// PageInfo is the page info of the page; see Edges.
func (c *PostConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Post nodes in the whole connection, not only on
// this page. It counts the rows when gqlgen asks for it, which is only when the
// field totalCount is selected.
func (c *PostConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// PostEdge is the GraphQL type PostEdge, an edge in a connection to a list of
// Post.
type PostEdge struct {
	// Node is the Post at the end of the edge.
	Node *Post
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// PostsConnection is the connection to every Post node, through the DB
// veloxgql.DBFrom reads from ctx, paged and ordered by the arguments of a
// connection to Post: a Query field's resolver returns it. It fails before any
// query runs on arguments veloxgql.NewConnection refuses.
func PostsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return postConnectionGQL(ctx, db.Query[Post](), after, first, before, last, orderBy, where)
}

// postConnectionGQL is the connection to the Post nodes q returns, filtered by
// where, ordered by orderBy and then the key, and paged by the other arguments.
// It runs no query.
func postConnectionGQL(ctx context.Context, q velox.Query[Post], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	if where != nil {
		p, err := where.Pred(ctx, 0)
		if err != nil {
			return nil, err
		}
		q = q.Where(p)
	}
	var order []veloxgql.OrderTerm[Post]
	for _, o := range orderBy {
		term, err := postTermGQL(o)
		if err != nil {
			return nil, err
		}
		order = append(order, term)
	}
	c, err := veloxgql.NewConnection(q, "Post", veloxgql.Args{After: after, First: first, Before: before, Last: last}, order, postKeyGQL())
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: c}, nil
}

// postKeyGQL is the order of Post's key, which ends every order of a connection
// to Post.
func postKeyGQL() []veloxgql.OrderTerm[Post] {
	return []veloxgql.OrderTerm[Post]{
		veloxgql.Term("id", veloxgql.OrderDirectionAsc, Posts.ID.Asc(), Posts.ID.Desc(),
			func(x *Post) int64 { return x.ID },
			func(x *Post, v int64) { x.ID = v }),
	}
}

// postTermGQL is the order term o, an order of connections to Post.
func postTermGQL(o *PostOrder) (veloxgql.OrderTerm[Post], error) {
	switch o.Field {
	case PostOrderFieldTitle:
		return veloxgql.Term(string(o.Field), o.Direction, Posts.Title.Asc(), Posts.Title.Desc(),
			func(x *Post) string { return x.Title },
			func(x *Post, v string) { x.Title = v }), nil
	case PostOrderFieldViews:
		return veloxgql.Term(string(o.Field), o.Direction, Posts.Views.Asc(), Posts.Views.Desc(),
			func(x *Post) int {
				var v int
				if x.Views != nil {
					v = *x.Views
				}
				return v
			},
			func(x *Post, v int) { x.Views = &v }), nil
	}
	return veloxgql.OrderTerm[Post]{}, fmt.Errorf("PostOrder: %q is not a value of PostOrderField", o.Field)
}

// PostOrder is the GraphQL input PostOrder, an order of connections to Post.
type PostOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field PostOrderField
}

// PostOrderField is the GraphQL enum PostOrderField, the fields connections to
// Post can be ordered by.
type PostOrderField string

// The values of PostOrderField.
const (
	// PostOrderFieldTitle orders by the field title.
	PostOrderFieldTitle PostOrderField = "TITLE"
	// PostOrderFieldViews orders by the field views.
	PostOrderFieldViews PostOrderField = "VIEWS"
)

// IsValid reports whether o is a value of PostOrderField.
func (o PostOrderField) IsValid() bool {
	switch o {
	case "TITLE", "VIEWS":
		return true
	}
	return false
}

// String returns o's value.
func (o PostOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum PostOrderField.
func (o PostOrderField) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(o)) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets o from v, a value of the GraphQL enum PostOrderField.
func (o *PostOrderField) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	if !PostOrderField(s).IsValid() {
		return errors.New("PostOrderField must be TITLE or VIEWS")
	}
	*o = PostOrderField(s)
	return nil
}

// PostWhereInput is the GraphQL input PostWhereInput, a filter of Post nodes: a
// node matches when every predicate given holds.
type PostWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *PostWhereInput
	And []*PostWhereInput
	Or  []*PostWhereInput

	// Predicates on the edge author (writer), by the ID of its node.
	WriterID      *string
	WriterIDNEQ   *string
	WriterIDIn    []string
	WriterIDNotIn []string

	// Predicates on the field title.
	Title          *string
	TitleNEQ       *string
	TitleIn        []string
	TitleNotIn     []string
	TitleContains  *string
	TitleHasPrefix *string
	TitleHasSuffix *string

	// Predicates on the field slug.
	Slug      *string
	SlugNEQ   *string
	SlugIn    []string
	SlugNotIn []string

	// Predicates on the field status.
	Status      *PostStatus
	StatusNEQ   *PostStatus
	StatusIn    []PostStatus
	StatusNotIn []PostStatus

	// Predicates on the field published_at (publishedAt).
	PublishedAt       *time.Time
	PublishedAtNEQ    *time.Time
	PublishedAtIn     []time.Time
	PublishedAtNotIn  []time.Time
	PublishedAtGT     *time.Time
	PublishedAtGTE    *time.Time
	PublishedAtLT     *time.Time
	PublishedAtLTE    *time.Time
	PublishedAtIsNil  *bool
	PublishedAtNotNil *bool

	// The edge author (writer): whether it has a node, or one that matches.
	HasWriter     *bool
	HasWriterWith []*AccountWhereInput
}

// Pred is the condition w stands for, on Post nodes: every predicate w sets
// holds, and a w that sets none, or a nil w, holds for every node. depth is how
// deeply w is nested in the where argument, 0 at the top. It fails, before any
// query runs, on an ID of another type (veloxgql.ErrWrongIDType) or a malformed
// one (veloxgql.ErrBadID), on a nesting deeper than veloxgql.MaxFilterDepth
// (veloxgql.ErrFilterTooDeep), and on an and, an or or a has…With of more
// than veloxgql.MaxFilterWidth inputs (veloxgql.ErrFilterTooWide).
func (w *PostWhereInput) Pred(ctx context.Context, depth int) (velox.Pred[Post], error) {
	f := veloxgql.NewFilter[Post](ctx, "PostWhereInput", depth)
	if w == nil {
		return f.Pred()
	}
	veloxgql.Not(f, "not", w.Not)
	veloxgql.And(f, "and", w.And)
	veloxgql.Or(f, "or", w.Or)
	veloxgql.ID(f, "writerID", w.WriterID, "Account", Posts.AuthorID.EQ)
	veloxgql.ID(f, "writerIDNEQ", w.WriterIDNEQ, "Account", Posts.AuthorID.NEQ)
	veloxgql.IDList(f, "writerIDIn", w.WriterIDIn, "Account", Posts.AuthorID.In)
	veloxgql.IDList(f, "writerIDNotIn", w.WriterIDNotIn, "Account", Posts.AuthorID.NotIn)
	veloxgql.Value(f, "title", w.Title, Posts.Title.EQ)
	veloxgql.Value(f, "titleNEQ", w.TitleNEQ, Posts.Title.NEQ)
	veloxgql.List(f, "titleIn", w.TitleIn, Posts.Title.In)
	veloxgql.List(f, "titleNotIn", w.TitleNotIn, Posts.Title.NotIn)
	veloxgql.Value(f, "titleContains", w.TitleContains, Posts.Title.Contains)
	veloxgql.Value(f, "titleHasPrefix", w.TitleHasPrefix, Posts.Title.HasPrefix)
	veloxgql.Value(f, "titleHasSuffix", w.TitleHasSuffix, Posts.Title.HasSuffix)
	veloxgql.Value(f, "slug", w.Slug, Posts.Slug.EQ)
	veloxgql.Value(f, "slugNEQ", w.SlugNEQ, Posts.Slug.NEQ)
	veloxgql.List(f, "slugIn", w.SlugIn, Posts.Slug.In)
	veloxgql.List(f, "slugNotIn", w.SlugNotIn, Posts.Slug.NotIn)
	veloxgql.Value(f, "status", w.Status, Posts.Status.EQ)
	veloxgql.Value(f, "statusNEQ", w.StatusNEQ, Posts.Status.NEQ)
	veloxgql.List(f, "statusIn", w.StatusIn, Posts.Status.In)
	veloxgql.List(f, "statusNotIn", w.StatusNotIn, Posts.Status.NotIn)
	veloxgql.Value(f, "publishedAt", w.PublishedAt, Posts.PublishedAt.EQ)
	veloxgql.Value(f, "publishedAtNEQ", w.PublishedAtNEQ, Posts.PublishedAt.NEQ)
	veloxgql.List(f, "publishedAtIn", w.PublishedAtIn, Posts.PublishedAt.In)
	veloxgql.List(f, "publishedAtNotIn", w.PublishedAtNotIn, Posts.PublishedAt.NotIn)
	veloxgql.Value(f, "publishedAtGT", w.PublishedAtGT, Posts.PublishedAt.GT)
	veloxgql.Value(f, "publishedAtGTE", w.PublishedAtGTE, Posts.PublishedAt.GTE)
	veloxgql.Value(f, "publishedAtLT", w.PublishedAtLT, Posts.PublishedAt.LT)
	veloxgql.Value(f, "publishedAtLTE", w.PublishedAtLTE, Posts.PublishedAt.LTE)
	veloxgql.Is(f, "publishedAtIsNil", w.PublishedAtIsNil, Posts.PublishedAt.IsNull)
	veloxgql.Is(f, "publishedAtNotNil", w.PublishedAtNotNil, Posts.PublishedAt.NotNull)
	veloxgql.Is(f, "hasWriter", w.HasWriter, Posts.Author.Has)
	veloxgql.HasWith(f, "hasWriterWith", w.HasWriterWith, Posts.Author.HasWhere)
	return f.Pred()
}
