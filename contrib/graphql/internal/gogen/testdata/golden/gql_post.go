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
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion1

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
		return gqlimpl.InputErrorf("PostStatus must be DRAFT, IN_REVIEW or LIVE")
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
// MaxList of veloxgql.LimitsFrom(ctx) rows, whatever the DB's WithMaxRows: more
// fail it with velox.ErrTooManyRows, in an error that names the edge.
func (x *Post) LikesGQL(ctx context.Context) ([]*Like, error) {
	rows, ok := x.Likes.Get()
	if !ok {
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		if rows, err = db.Query[Like]().Where(Likes.PostID.EQ(x.ID)).OrderBy(Likes.UserID.Asc(), Likes.PostID.Asc()).Unbounded().MaxRows(veloxgql.LimitsFrom(ctx).MaxList).All(ctx); err != nil {
			return nil, fmt.Errorf("veloxgql: Post.likes: %w", err)
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
	c *gqlimpl.Connection[Post]
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
// query runs on arguments gqlimpl.NewConnection refuses.
func PostsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return PostsConnectionOf(ctx, db.Query[Post](), after, first, before, last, orderBy, where)
}

// PostsConnectionOf is the connection to the Post nodes q returns, filtered by
// where, ordered by orderBy and then the key, and paged by the other arguments.
// q keeps its filters and With loads, and the scope the application gave it;
// its Offset is cleared and its Limit replaced, since the page is the
// connection's. q must have no OrderBy: the connection orders it, and a q with
// one is refused with an internal error, since its order would come first and
// break the cursors. q must also select the key and the columns orderBy names:
// a Columns or Omit that drops them breaks the cursors, and nothing refuses it.
// It runs no query.
func PostsConnectionOf(ctx context.Context, q velox.Query[Post], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	if where != nil {
		p, err := where.Pred(ctx)
		if err != nil {
			return nil, err
		}
		q = q.Where(p)
	}
	var order []gqlimpl.OrderTerm[Post]
	for _, o := range orderBy {
		term, err := postTermGQL(o)
		if err != nil {
			return nil, err
		}
		order = append(order, term)
	}
	c, err := gqlimpl.NewConnection(ctx, q, "Post", gqlimpl.Args{After: after, First: first, Before: before, Last: last}, order, postKeyGQL())
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: c}, nil
}

// postKeyGQL is the order of Post's key, which ends every order of a connection
// to Post.
func postKeyGQL() []gqlimpl.OrderTerm[Post] {
	return []gqlimpl.OrderTerm[Post]{
		gqlimpl.Term("id", veloxgql.OrderDirectionAsc, Posts.ID.Asc(), Posts.ID.Desc(),
			func(x *Post) int64 { return x.ID },
			func(x *Post, v int64) { x.ID = v }),
	}
}

// postTermGQL is the order term o, an order of connections to Post.
func postTermGQL(o *PostOrder) (gqlimpl.OrderTerm[Post], error) {
	switch o.Field {
	case PostOrderFieldTitle:
		return gqlimpl.Term(string(o.Field), o.Direction, Posts.Title.Asc(), Posts.Title.Desc(),
			func(x *Post) string { return x.Title },
			func(x *Post, v string) { x.Title = v }), nil
	case PostOrderFieldViews:
		return gqlimpl.Term(string(o.Field), o.Direction, Posts.Views.Asc(), Posts.Views.Desc(),
			func(x *Post) int { return x.Views },
			func(x *Post, v int) { x.Views = v }), nil
	}
	return gqlimpl.OrderTerm[Post]{}, gqlimpl.InputErrorf("PostOrder: %q is not a value of PostOrderField", o.Field)
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
		return gqlimpl.InputErrorf("PostOrderField must be TITLE or VIEWS")
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
// holds, and a w that sets none, or a nil w, holds for every node. It fails,
// before any query runs, on an ID of another type (veloxgql.ErrWrongIDType) or
// a malformed one (veloxgql.ErrBadID), on a nesting deeper than the
// MaxFilterDepth of veloxgql.LimitsFrom(ctx) (veloxgql.ErrFilterTooDeep), on an
// and, an or, a has…With, an In or a NotIn of more than its MaxFilterWidth
// entries (veloxgql.ErrFilterTooWide), and on a where argument of more than its
// MaxFilterNodes inputs, predicates and values in all
// (veloxgql.ErrFilterTooLarge).
func (w *PostWhereInput) Pred(ctx context.Context) (velox.Pred[Post], error) {
	f := gqlimpl.NewFilter[Post](ctx, "PostWhereInput")
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID(f, "writerID", w.WriterID, "Account", Posts.AuthorID.EQ)
	gqlimpl.ID(f, "writerIDNEQ", w.WriterIDNEQ, "Account", Posts.AuthorID.NEQ)
	gqlimpl.IDList(f, "writerIDIn", w.WriterIDIn, "Account", Posts.AuthorID.In)
	gqlimpl.IDList(f, "writerIDNotIn", w.WriterIDNotIn, "Account", Posts.AuthorID.NotIn)
	gqlimpl.Value(f, "title", w.Title, Posts.Title.EQ)
	gqlimpl.Value(f, "titleNEQ", w.TitleNEQ, Posts.Title.NEQ)
	gqlimpl.List(f, "titleIn", w.TitleIn, Posts.Title.In)
	gqlimpl.List(f, "titleNotIn", w.TitleNotIn, Posts.Title.NotIn)
	gqlimpl.Value(f, "titleContains", w.TitleContains, Posts.Title.Contains)
	gqlimpl.Value(f, "titleHasPrefix", w.TitleHasPrefix, Posts.Title.HasPrefix)
	gqlimpl.Value(f, "titleHasSuffix", w.TitleHasSuffix, Posts.Title.HasSuffix)
	gqlimpl.Value(f, "slug", w.Slug, Posts.Slug.EQ)
	gqlimpl.Value(f, "slugNEQ", w.SlugNEQ, Posts.Slug.NEQ)
	gqlimpl.List(f, "slugIn", w.SlugIn, Posts.Slug.In)
	gqlimpl.List(f, "slugNotIn", w.SlugNotIn, Posts.Slug.NotIn)
	gqlimpl.Value(f, "status", w.Status, Posts.Status.EQ)
	gqlimpl.Value(f, "statusNEQ", w.StatusNEQ, Posts.Status.NEQ)
	gqlimpl.List(f, "statusIn", w.StatusIn, Posts.Status.In)
	gqlimpl.List(f, "statusNotIn", w.StatusNotIn, Posts.Status.NotIn)
	gqlimpl.Value(f, "publishedAt", w.PublishedAt, Posts.PublishedAt.EQ)
	gqlimpl.Value(f, "publishedAtNEQ", w.PublishedAtNEQ, Posts.PublishedAt.NEQ)
	gqlimpl.List(f, "publishedAtIn", w.PublishedAtIn, Posts.PublishedAt.In)
	gqlimpl.List(f, "publishedAtNotIn", w.PublishedAtNotIn, Posts.PublishedAt.NotIn)
	gqlimpl.Value(f, "publishedAtGT", w.PublishedAtGT, Posts.PublishedAt.GT)
	gqlimpl.Value(f, "publishedAtGTE", w.PublishedAtGTE, Posts.PublishedAt.GTE)
	gqlimpl.Value(f, "publishedAtLT", w.PublishedAtLT, Posts.PublishedAt.LT)
	gqlimpl.Value(f, "publishedAtLTE", w.PublishedAtLTE, Posts.PublishedAt.LTE)
	gqlimpl.Is(f, "publishedAtIsNil", w.PublishedAtIsNil, Posts.PublishedAt.IsNull)
	gqlimpl.Is(f, "publishedAtNotNil", w.PublishedAtNotNil, Posts.PublishedAt.NotNull)
	gqlimpl.Is(f, "hasWriter", w.HasWriter, Posts.Author.Has)
	gqlimpl.HasWith(f, "hasWriterWith", w.HasWriterWith, Posts.Author.HasWhere)
	return f.Pred()
}
