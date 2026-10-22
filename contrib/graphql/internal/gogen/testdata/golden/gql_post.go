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

// RelatedGQL is the edge related: a connection to the Post nodes it leads to,
// paged, ordered and filtered by the arguments. With no argument it is over
// x.Related when a With loaded it, in the order loaded; else its queries are on
// Post, filtered by the relation, and run when gqlgen asks for the connection's
// fields.
func (x *Post) RelatedGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	if after == nil && first == nil && before == nil && last == nil && orderBy == nil && where == nil {
		if rows, ok := x.Related.Get(); ok {
			return &PostConnection{c: gqlimpl.LoadedConnection(ctx, rows, "Post", postKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return PostsConnectionOf(ctx, db.Query[Post]().Where(Posts.RelatedBy.HasWhere(Posts.ID.EQ(x.ID))), after, first, before, last, orderBy, where)
}

// RelatedByGQL is the edge related_by: a connection to the Post nodes it leads
// to, paged, ordered and filtered by the arguments. With no argument it is over
// x.RelatedBy when a With loaded it, in the order loaded; else its queries are
// on Post, filtered by the relation, and run when gqlgen asks for the
// connection's fields.
func (x *Post) RelatedByGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	if after == nil && first == nil && before == nil && last == nil && orderBy == nil && where == nil {
		if rows, ok := x.RelatedBy.Get(); ok {
			return &PostConnection{c: gqlimpl.LoadedConnection(ctx, rows, "Post", postKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return PostsConnectionOf(ctx, db.Query[Post]().Where(Posts.Related.HasWhere(Posts.ID.EQ(x.ID))), after, first, before, last, orderBy, where)
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

// postFieldGQL is the GraphQL name of the mutation-input field that sets the
// Post field whose Go name is goField, for the errors of its validators: "" for
// a field no input sets.
func postFieldGQL(goField string) string {
	switch goField {
	case "Title":
		return "title"
	case "Slug":
		return "slug"
	case "Status":
		return "status"
	case "PublishedAt":
		return "publishedAt"
	case "Tags":
		return "tags"
	case "Rating":
		return "rating"
	case "EditedBy":
		return "editedBy"
	case "AuthorID":
		return "writerID"
	case "Views":
		return "views"
	}
	return ""
}

// CreatePostInput is the GraphQL input CreatePostInput, the input of
// createPost. A field that is a pointer, or a list, is optional: nil means not
// given.
type CreatePostInput struct {
	Title       string
	Slug        string
	Status      *PostStatus
	PublishedAt *time.Time
	Tags        *veloxgql.JSON
	Rating      *float64
	// Who last edited the post; the update default fills it.
	EditedBy string
	// WriterID is the ID of the Account the edge author leads to.
	WriterID string
	// RelatedIDs are the IDs of the Post nodes the edge related leads to.
	RelatedIDs []string
	// RelatedByIDs are the IDs of the Post nodes the edge related_by leads to.
	RelatedByIDs []string
}

// String returns the input's fields except its sensitive ones.
func (in CreatePostInput) String() string {
	var p2 any
	if in.Status != nil {
		p2 = *in.Status
	}
	var p3 any
	if in.PublishedAt != nil {
		p3 = *in.PublishedAt
	}
	var p4 any
	if in.Tags != nil {
		p4 = string(*in.Tags)
	}
	var p5 any
	if in.Rating != nil {
		p5 = *in.Rating
	}
	return fmt.Sprintf("CreatePostInput(Title=%v, Slug=%v, Status=%v, PublishedAt=%v, Tags=%v, Rating=%v, EditedBy=%v, WriterID=%v, RelatedIDs=%v, RelatedByIDs=%v)", in.Title, in.Slug, p2, p3, p4, p5, in.EditedBy, in.WriterID, in.RelatedIDs, in.RelatedByIDs)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreatePostInput) GoString() string { return in.String() }

// CreatePost creates one Post from input, with its edges, and returns it, as
// the mutation createPost does. It first decodes every ID and JSON value of
// input, and fails before any statement on one it cannot decode
// (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom reads from
// ctx, in one transaction (gqlimpl.Atomic): the operation's, or one of its own.
// The tenant scope and the policies apply as to any write: each ID must name a
// row the caller may read, and a row it re-points one the caller may change, or
// CreatePost fails with NOT_FOUND and keeps nothing. A validator's refusal is
// BAD_USER_INPUT, naming the input field, and comes before any statement;
// velox's other errors are returned as they are. The row is returned as
// written, not read again through the Read policy. There is no savepoint inside
// the extension's transaction, so a resolver must return this function's error:
// one that swallows it commits the partial writes. A self-edge is written one
// way, as the core's loads read it.
func CreatePost(ctx context.Context, input CreatePostInput) (*Post, error) {
	var (
		j4  []string
		k7  int64
		k8  []int64
		k9  []int64
		err error
	)
	if input.Tags != nil {
		if j4, err = gqlimpl.FromJSON[[]string]("CreatePostInput.tags", *input.Tags); err != nil {
			return nil, err
		}
	}
	if k7, err = gqlimpl.DecodeID[int64]("CreatePostInput.writerID", input.WriterID, "Account"); err != nil {
		return nil, err
	}
	if k8, err = gqlimpl.DecodeIDs[int64](ctx, "CreatePostInput.relatedIDs", input.RelatedIDs, "Post"); err != nil {
		return nil, err
	}
	if k9, err = gqlimpl.DecodeIDs[int64](ctx, "CreatePostInput.relatedByIDs", input.RelatedByIDs, "Post"); err != nil {
		return nil, err
	}
	var x Post
	cols := []velox.Column[Post]{Posts.Title, Posts.Slug, Posts.EditedBy}
	x.Title = input.Title
	x.Slug = input.Slug
	if input.Status != nil {
		x.Status = *input.Status
		cols = append(cols, Posts.Status)
	}
	if input.PublishedAt != nil {
		x.PublishedAt = input.PublishedAt
		cols = append(cols, Posts.PublishedAt)
	}
	if input.Tags != nil {
		x.Tags = j4
		cols = append(cols, Posts.Tags)
	}
	if input.Rating != nil {
		x.Rating = input.Rating
		cols = append(cols, Posts.Rating)
	}
	x.EditedBy = input.EditedBy
	if err := velox.Validate(&x, cols...); err != nil {
		return nil, gqlimpl.WriteError(err, "CreatePostInput", "gen.Post", postFieldGQL)
	}
	var out *Post
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		if err := gqlimpl.Exist(ctx, db.Query[User]().Where(Users.ID.In(k7)), 1, "CreatePostInput.writerID", "Account"); err != nil {
			return err
		}
		x.AuthorID = k7
		if len(k8) > 0 {
			if err := gqlimpl.Exist(ctx, db.Query[Post]().Where(Posts.ID.In(k8...)), len(k8), "CreatePostInput.relatedIDs", "Post"); err != nil {
				return err
			}
		}
		if len(k9) > 0 {
			if err := gqlimpl.Exist(ctx, db.Query[Post]().Where(Posts.ID.In(k9...)), len(k9), "CreatePostInput.relatedByIDs", "Post"); err != nil {
				return err
			}
		}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			return gqlimpl.WriteError(err, "CreatePostInput", "gen.Post", postFieldGQL)
		}
		if len(k8) > 0 {
			rows := make([]PostRelated, len(k8))
			for i, k := range k8 {
				rows[i] = PostRelated{PostID: x.ID, RelatedID: k}
			}
			if err := db.InsertAll(rows).OnConflict(PostRelateds.PostID, PostRelateds.RelatedID).DoNothing().Exec(ctx); err != nil {
				return err
			}
		}
		if len(k9) > 0 {
			rows := make([]PostRelated, len(k9))
			for i, k := range k9 {
				rows[i] = PostRelated{RelatedID: x.ID, PostID: k}
			}
			if err := db.InsertAll(rows).OnConflict(PostRelateds.RelatedID, PostRelateds.PostID).DoNothing().Exec(ctx); err != nil {
				return err
			}
		}
		out = &x
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdatePostInput is the GraphQL input UpdatePostInput, the input of
// updatePost. A field that is a pointer, or a list, is optional: nil means not
// given. A clear field that is true clears a field or an edge, and is refused
// with the field or the edge's ID given too.
type UpdatePostInput struct {
	Title       *string
	Status      *PostStatus
	PublishedAt *time.Time
	// ClearPublishedAt sets the field published_at to NULL.
	ClearPublishedAt bool
	Tags             *veloxgql.JSON
	Views            *int
	Rating           *float64
	// ClearRating sets the field rating to NULL.
	ClearRating bool
	// WriterID is the ID of the Account the edge author leads to.
	WriterID *string
	// AddRelatedIDs are the IDs of Post nodes to add to the edge related.
	AddRelatedIDs []string
	// RemoveRelatedIDs are the IDs of Post nodes to remove from the edge
	// related; an ID the edge does not lead to is ignored.
	RemoveRelatedIDs []string
	// ClearRelated removes every Post from the edge related, before the IDs to
	// remove and to add.
	ClearRelated bool
	// AddRelatedByIDs are the IDs of Post nodes to add to the edge related_by.
	AddRelatedByIDs []string
	// RemoveRelatedByIDs are the IDs of Post nodes to remove from the edge
	// related_by; an ID the edge does not lead to is ignored.
	RemoveRelatedByIDs []string
	// ClearRelatedBy removes every Post from the edge related_by, before the
	// IDs to remove and to add.
	ClearRelatedBy bool
}

// String returns the input's fields except its sensitive ones.
func (in UpdatePostInput) String() string {
	var p0 any
	if in.Title != nil {
		p0 = *in.Title
	}
	var p1 any
	if in.Status != nil {
		p1 = *in.Status
	}
	var p2 any
	if in.PublishedAt != nil {
		p2 = *in.PublishedAt
	}
	var p4 any
	if in.Tags != nil {
		p4 = string(*in.Tags)
	}
	var p5 any
	if in.Views != nil {
		p5 = *in.Views
	}
	var p6 any
	if in.Rating != nil {
		p6 = *in.Rating
	}
	var p8 any
	if in.WriterID != nil {
		p8 = *in.WriterID
	}
	return fmt.Sprintf("UpdatePostInput(Title=%v, Status=%v, PublishedAt=%v, ClearPublishedAt=%v, Tags=%v, Views=%v, Rating=%v, ClearRating=%v, WriterID=%v, AddRelatedIDs=%v, RemoveRelatedIDs=%v, ClearRelated=%v, AddRelatedByIDs=%v, RemoveRelatedByIDs=%v, ClearRelatedBy=%v)", p0, p1, p2, in.ClearPublishedAt, p4, p5, p6, in.ClearRating, p8, in.AddRelatedIDs, in.RemoveRelatedIDs, in.ClearRelated, in.AddRelatedByIDs, in.RemoveRelatedByIDs, in.ClearRelatedBy)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in UpdatePostInput) GoString() string { return in.String() }

// UpdatePost updates the Post with the ID id from input, as the mutation
// updatePost does, and returns it as it is afterwards: a field input does not
// give is left as it is, and a clear field sets a field to NULL or empties an
// edge. It first decodes id and every ID and JSON value of input, and refuses a
// field given with its clear field, or an ID both to add and to remove, before
// any statement (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom
// reads from ctx, in one transaction (gqlimpl.Atomic): the operation's, or one
// of its own. The fields go in one UPDATE, which the tenant scope and the Read
// and Write policies scope; an input that sets none sends no UPDATE, and a
// COUNT under the same scope (Writable) checks the row instead, so that no edge
// of a row the caller may not change is written. The edges follow, each
// cleared, then its IDs removed, then its IDs added: an ID to remove the edge
// does not lead to is ignored, and one to add it already leads to is kept.
// Clearing an edge whose key is on the targets' rows, or removing from it,
// unlinks only the children the tenant scope and the target's Write policy let
// the caller change; one they hide stays linked. Clearing a many-to-many edge,
// or removing from it, deletes only the links to targets the tenant scope and
// the target's Read policy let the caller read; a link to one they hide stays.
// Each ID must name a row the caller may read, and a row it re-points one the
// caller may change; a child another node holds is refused (BAD_USER_INPUT). An
// id that names no row the caller may change, or an ID no row it may read,
// fails with NOT_FOUND and keeps nothing. A validator's refusal is
// BAD_USER_INPUT, naming the input field, and comes before any statement;
// velox's other errors are returned as they are. The row is read back as
// written, under the tenant scope and without the Read policy
// (velox.SkipPolicy): an update that takes the row out of what the caller may
// read still returns it. On MySQL the UPDATE's row count needs
// clientFoundRows=true, as velox's Update does. An update that sets no field
// checks its row and then writes the edges, so a row changed in between is a
// time-of-check race, the same one ent has. There is no savepoint inside the
// extension's transaction, so a resolver must return this function's error.
func UpdatePost(ctx context.Context, id string, input UpdatePostInput) (*Post, error) {
	var (
		key int64
		j4  []string
		k8  int64
		k9  []int64
		k10 []int64
		k12 []int64
		k13 []int64
		err error
	)
	if key, err = gqlimpl.DecodeID[int64]("updatePost.id", id, "Post"); err != nil {
		return nil, err
	}
	if input.Tags != nil {
		if j4, err = gqlimpl.FromJSON[[]string]("UpdatePostInput.tags", *input.Tags); err != nil {
			return nil, err
		}
	}
	if input.WriterID != nil {
		if k8, err = gqlimpl.DecodeID[int64]("UpdatePostInput.writerID", *input.WriterID, "Account"); err != nil {
			return nil, err
		}
	}
	if k9, err = gqlimpl.DecodeIDs[int64](ctx, "UpdatePostInput.addRelatedIDs", input.AddRelatedIDs, "Post"); err != nil {
		return nil, err
	}
	if k10, err = gqlimpl.DecodeIDs[int64](ctx, "UpdatePostInput.removeRelatedIDs", input.RemoveRelatedIDs, "Post"); err != nil {
		return nil, err
	}
	if k12, err = gqlimpl.DecodeIDs[int64](ctx, "UpdatePostInput.addRelatedByIDs", input.AddRelatedByIDs, "Post"); err != nil {
		return nil, err
	}
	if k13, err = gqlimpl.DecodeIDs[int64](ctx, "UpdatePostInput.removeRelatedByIDs", input.RemoveRelatedByIDs, "Post"); err != nil {
		return nil, err
	}
	if input.PublishedAt != nil && input.ClearPublishedAt {
		return nil, gqlimpl.InputErrorf("UpdatePostInput: publishedAt and clearPublishedAt together")
	}
	if input.Rating != nil && input.ClearRating {
		return nil, gqlimpl.InputErrorf("UpdatePostInput: rating and clearRating together")
	}
	if err = gqlimpl.Disjoint("UpdatePostInput", "addRelatedIDs", "removeRelatedIDs", k9, k10); err != nil {
		return nil, err
	}
	if err = gqlimpl.Disjoint("UpdatePostInput", "addRelatedByIDs", "removeRelatedByIDs", k12, k13); err != nil {
		return nil, err
	}
	var set []velox.Setter[Post]
	if input.Title != nil {
		set = append(set, Posts.Title.Set(*input.Title))
	}
	if input.Status != nil {
		set = append(set, Posts.Status.Set(*input.Status))
	}
	if input.PublishedAt != nil {
		set = append(set, Posts.PublishedAt.Set(*input.PublishedAt))
	}
	if input.ClearPublishedAt {
		set = append(set, Posts.PublishedAt.SetNull())
	}
	if input.Tags != nil {
		set = append(set, Posts.Tags.Set(j4))
	}
	if input.Views != nil {
		set = append(set, Posts.Views.Set(*input.Views))
	}
	if input.Rating != nil {
		set = append(set, Posts.Rating.Set(*input.Rating))
	}
	if input.ClearRating {
		set = append(set, Posts.Rating.SetNull())
	}
	if err = velox.ValidateSet(set...); err != nil {
		return nil, gqlimpl.WriteError(err, "UpdatePostInput", "gen.Post", postFieldGQL)
	}
	var out *Post
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		if input.WriterID != nil {
			if err := gqlimpl.Exist(ctx, db.Query[User]().Where(Users.ID.In(k8)), 1, "UpdatePostInput.writerID", "Account"); err != nil {
				return err
			}
			set = append(set, Posts.AuthorID.Set(k8))
		}
		if len(set) > 0 {
			n, err := db.Update[Post]().Set(set...).Where(Posts.ID.EQ(key)).Exec(ctx)
			if err != nil {
				return gqlimpl.WriteError(err, "UpdatePostInput", "gen.Post", postFieldGQL)
			}
			if n == 0 {
				return gqlimpl.NotFound("updatePost", "Post")
			}
		} else if n, err := db.Query[Post]().Where(Posts.ID.EQ(key)).Writable().Count(ctx); err != nil {
			return err
		} else if n == 0 {
			return gqlimpl.NotFound("updatePost", "Post")
		}
		unlink2 := func(ks []int64) error {
			vs, err := gqlimpl.Readable(ctx, db.Query[Post]().Where(Posts.ID.In(ks...)).Columns(Posts.ID), len(ks), func(x *Post) int64 { return x.ID })
			if err != nil || len(vs) == 0 {
				return err
			}
			_, err = db.Delete[PostRelated]().Where(PostRelateds.PostID.EQ(key), PostRelateds.RelatedID.In(vs...)).Exec(ctx)
			return err
		}
		if input.ClearRelated {
			if err := gqlimpl.UnlinkAll(ctx, db.Query[PostRelated]().Where(PostRelateds.PostID.EQ(key)).OrderBy(PostRelateds.PostID.Asc(), PostRelateds.RelatedID.Asc()), func(j *PostRelated) int64 { return j.RelatedID }, unlink2); err != nil {
				return err
			}
		}
		if len(k10) > 0 {
			if err := unlink2(k10); err != nil {
				return err
			}
		}
		if len(k9) > 0 {
			if err := gqlimpl.Exist(ctx, db.Query[Post]().Where(Posts.ID.In(k9...)), len(k9), "UpdatePostInput.addRelatedIDs", "Post"); err != nil {
				return err
			}
			rows := make([]PostRelated, len(k9))
			for i, c := range k9 {
				rows[i] = PostRelated{PostID: key, RelatedID: c}
			}
			if err := db.InsertAll(rows).OnConflict(PostRelateds.PostID, PostRelateds.RelatedID).DoNothing().Exec(ctx); err != nil {
				return err
			}
		}
		unlink3 := func(ks []int64) error {
			vs, err := gqlimpl.Readable(ctx, db.Query[Post]().Where(Posts.ID.In(ks...)).Columns(Posts.ID), len(ks), func(x *Post) int64 { return x.ID })
			if err != nil || len(vs) == 0 {
				return err
			}
			_, err = db.Delete[PostRelated]().Where(PostRelateds.RelatedID.EQ(key), PostRelateds.PostID.In(vs...)).Exec(ctx)
			return err
		}
		if input.ClearRelatedBy {
			if err := gqlimpl.UnlinkAll(ctx, db.Query[PostRelated]().Where(PostRelateds.RelatedID.EQ(key)).OrderBy(PostRelateds.RelatedID.Asc(), PostRelateds.PostID.Asc()), func(j *PostRelated) int64 { return j.PostID }, unlink3); err != nil {
				return err
			}
		}
		if len(k13) > 0 {
			if err := unlink3(k13); err != nil {
				return err
			}
		}
		if len(k12) > 0 {
			if err := gqlimpl.Exist(ctx, db.Query[Post]().Where(Posts.ID.In(k12...)), len(k12), "UpdatePostInput.addRelatedByIDs", "Post"); err != nil {
				return err
			}
			rows := make([]PostRelated, len(k12))
			for i, c := range k12 {
				rows[i] = PostRelated{RelatedID: key, PostID: c}
			}
			if err := db.InsertAll(rows).OnConflict(PostRelateds.RelatedID, PostRelateds.PostID).DoNothing().Exec(ctx); err != nil {
				return err
			}
		}
		x, err := db.Query[Post]().Where(Posts.ID.EQ(key)).One(velox.SkipPolicy(ctx, "graphql: the row this update wrote"))
		if errors.Is(err, velox.ErrNotFound) {
			return gqlimpl.NotFound("updatePost", "Post")
		}
		if err != nil {
			return err
		}
		out = x
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeletePost deletes the Post with the ID id, and its rows in the join tables
// of its many-to-many edges, as the mutation deletePost does, and returns the
// ID, encoded again from the key. It first decodes id, before any statement
// (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom reads from
// ctx, in one transaction (gqlimpl.Atomic): the operation's, or one of its own.
// The DELETE is scoped by the tenant and the Read and Write policies. The join
// rows, which neither scopes, go first, after a COUNT under the DELETE's scope
// (Writable) has found the row: those of a row the caller may not delete are
// never touched. Every join row of the row goes, whatever the tenant of the row
// at its other end: the row is gone, and a row of no tenant is unlinked from
// every tenant's rows. An id that names no row the caller may change fails with
// NOT_FOUND and deletes nothing. The rows of other entities whose key holds the
// row's are not touched: the database's foreign keys decide, and a key that
// refuses the DELETE fails it with a *velox.ConstraintError, which
// veloxgql.ErrorPresenter shows as BAD_USER_INPUT. velox's errors are returned
// as they are.
func DeletePost(ctx context.Context, id string) (string, error) {
	key, err := gqlimpl.DecodeID[int64]("deletePost.id", id, "Post")
	if err != nil {
		return "", err
	}
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		if n, err := db.Query[Post]().Where(Posts.ID.EQ(key)).Writable().Count(ctx); err != nil {
			return err
		} else if n == 0 {
			return gqlimpl.NotFound("deletePost", "Post")
		}
		if _, err := db.Delete[PostRelated]().Where(velox.Or(PostRelateds.PostID.EQ(key), PostRelateds.RelatedID.EQ(key))).Exec(ctx); err != nil {
			return err
		}
		n, err := db.Delete[Post]().Where(Posts.ID.EQ(key)).Exec(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return gqlimpl.NotFound("deletePost", "Post")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return veloxgql.EncodeID("Post", key), nil
}
