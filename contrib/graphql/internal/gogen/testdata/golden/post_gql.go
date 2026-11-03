package gen

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion10

// IsNode marks Post as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Post) IsNode() {}

// IDGQL is the global ID of the Post, the GraphQL field id: the type Post and
// the key id, encoded.
func (x *Post) IDGQL() string { return veloxgql.EncodeID("Post", x.ID) }

// postNodeGQL is the Node type Post for Node and Nodes.
func postNodeGQL() gqlimpl.NodeKind {
	return gqlimpl.NewNode("Post", velox.TableOf[Post](), postColID,
		func(id string) (any, error) {
			k, err := veloxgql.DecodeID[int64](id, "Post")
			return k, err
		},
		postColumnsGQL)
}

// postKeyGQL is the key of x, a Post.
func postKeyGQL(x *Post) int64 { return x.ID }

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
// none. It is read with the edge of every row read with x, in one query.
func (x *Post) AuthorGQL(ctx context.Context) (*User, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[User]())
	if fields, ok := gqlimpl.Selected(ctx, "Account"); ok {
		q = gqlimpl.Narrow(q, userColumnsGQL(fields))
	}
	return gqlimpl.EdgeOne(ctx, q, x, Posts.Author, "Post.writer")
}

// LikesGQL is the edge likes: the Like nodes it leads to, in key order. It is
// read with the edge of every row read with x, in one query, which reads at
// most the MaxList of veloxgql.LimitsFromContext(ctx) rows for each, whatever
// the DB's WithMaxRows: more fail x's with velox.ErrTooManyRows, in an error
// that names the edge.
func (x *Post) LikesGQL(ctx context.Context) ([]*Like, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[Like]())
	if fields, ok := gqlimpl.Selected(ctx, "Like"); ok {
		q = gqlimpl.Narrow(q, likeColumnsGQL(fields))
	}
	return gqlimpl.EdgeList(ctx, q, x, Posts.Likes, "Post.likes", 0)
}

// RelatedGQL is the edge related: a connection to the Post nodes it leads to,
// paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *Post) RelatedGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	c, err := PostsConnectionOf(ctx, db.Query[Post](), after, first, before, last, orderBy, where)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, Posts.Related, "Post.related", []any{after, first, before, last, orderBy, where})}, nil
}

// RelatedByGQL is the edge related_by: a connection to the Post nodes it leads
// to, paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *Post) RelatedByGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	c, err := PostsConnectionOf(ctx, db.Query[Post](), after, first, before, last, orderBy, where)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, Posts.RelatedBy, "Post.relatedBy", []any{after, first, before, last, orderBy, where})}, nil
}

// PostConnection is the GraphQL type PostConnection, a connection to a list of
// Post. It holds the query of the whole list and the page asked for; gqlgen
// calls its methods for the fields a query selects, and each runs its query
// only then: edges and pageInfo share one page query, and totalCount runs a
// COUNT.
type PostConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *PostConnection) Edges(ctx context.Context) ([]*PostEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]PostEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*PostEdge, len(p.Rows))
	for i, x := range p.Rows {
		slab[i] = PostEdge{Node: (*Post)(x), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
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
// veloxgql.DBFromContext reads from ctx, paged and ordered by the arguments of
// a connection to Post: a Query field's resolver returns it. It fails before
// any query runs on arguments gqlimpl.NewConnection refuses.
func PostsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
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
// break the cursors. q must also select the key and the columns orderBy names,
// which the cursors hold: a q whose Columns or Omit drops one is refused with
// an internal error. In a gqlgen field, the page reads only the columns the
// selection needs (postColumnsGQL), of those q selects; a column the selection
// does not need is left zero in the nodes, except the key, the foreign keys and
// the order columns, so an application's resolver must not read one it did not
// ask for. It runs no query.
func PostsConnectionOf(ctx context.Context, q velox.Query[Post], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	order := make([]gqlimpl.Order, len(orderBy))
	for i, o := range orderBy {
		order[i] = gqlimpl.Order{Field: string(o.Field), Direction: o.Direction}
	}
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &postConnGQL, order)
	if err != nil {
		return nil, err
	}
	if where != nil {
		p, err := where.PredicateGQL(ctx)
		if err != nil {
			return nil, err
		}
		b = b.Where(p)
	}
	c, err := gqlimpl.NewConnection(ctx, b, &postConnGQL, gqlimpl.Args{After: after, First: first, Before: before, Last: last}, order)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: c}, nil
}

// postConnGQL is what the connections to Post are made of: the key, which ends
// every order, and the order fields' columns.
var postConnGQL = gqlimpl.Conn{
	Type: "Post",
	Key: []gqlimpl.Col{
		{Field: "id", Col: postColID},
	},
	Order: []gqlimpl.Col{
		{Field: "TITLE", Col: postColTitle},
		{Field: "VIEWS", Col: postColViews},
		{Field: "AUTHOR_NAME", Col: userColName, Edge: postEdgeAuthor + 1, Target: velox.TableOf[User]},
	},
	Columns: postColumnsGQL,
}

// postColumnsGQL is the columns a selection of the Post fields fields reads:
// the key, every foreign key on the row, every order field's column, and the
// column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
func postColumnsGQL(fields []string) []int {
	cols := []int{postColID, postColAuthorID, postColTitle, postColViews}
	for _, f := range fields {
		switch f {
		case "id", "title", "views", "writer", "likes", "related", "relatedBy":
		case "slug":
			cols = append(cols, postColSlug)
		case "status":
			cols = append(cols, postColStatus)
		case "publishedAt":
			cols = append(cols, postColPublishedAt)
		case "tags":
			cols = append(cols, postColTags)
		case "rating":
			cols = append(cols, postColRating)
		case "editedBy":
			cols = append(cols, postColEditedBy)
		default:
			return nil
		}
	}
	return cols
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
	// PostOrderFieldAuthorName orders by the field author.name.
	PostOrderFieldAuthorName PostOrderField = "AUTHOR_NAME"
)

// IsValid reports whether o is a value of PostOrderField.
func (o PostOrderField) IsValid() bool {
	switch o {
	case "TITLE", "VIEWS", "AUTHOR_NAME":
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
		return gqlimpl.InputErrorf("PostOrderField must be TITLE, VIEWS or AUTHOR_NAME")
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

	// Predicates on the ID.
	ID      *string
	IDNEQ   *string
	IDIn    []string
	IDNotIn []string
	IDGT    *string
	IDGTE   *string
	IDLT    *string
	IDLTE   *string

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
// before any query runs, on an ID of another type or a malformed one, and on a
// filter over the limits of veloxgql.LimitsFromContext(ctx): the errors are
// gqlimpl.NewFilter's.
func (w *PostWhereInput) Pred(ctx context.Context) (velox.Pred[Post], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[Post](p), err
}

// PredicateGQL is Pred, untyped, for velox-graphql's runtime.
func (w *PostWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, "PostWhereInput", velox.TableOf[Post]())
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID[int64](f, "id", w.ID, "Post", postColID, velox.PredEQ)
	gqlimpl.ID[int64](f, "idNEQ", w.IDNEQ, "Post", postColID, velox.PredNEQ)
	gqlimpl.IDList[int64](f, "idIn", w.IDIn, "Post", postColID, velox.PredIn)
	gqlimpl.IDList[int64](f, "idNotIn", w.IDNotIn, "Post", postColID, velox.PredNotIn)
	gqlimpl.ID[int64](f, "idGT", w.IDGT, "Post", postColID, velox.PredGT)
	gqlimpl.ID[int64](f, "idGTE", w.IDGTE, "Post", postColID, velox.PredGTE)
	gqlimpl.ID[int64](f, "idLT", w.IDLT, "Post", postColID, velox.PredLT)
	gqlimpl.ID[int64](f, "idLTE", w.IDLTE, "Post", postColID, velox.PredLTE)
	gqlimpl.FKID[int64](f, "writerID", w.WriterID, "Account", postColAuthorID, velox.PredEQ, postEdgeAuthor, velox.TableOf[User]())
	gqlimpl.FKID[int64](f, "writerIDNEQ", w.WriterIDNEQ, "Account", postColAuthorID, velox.PredNEQ, postEdgeAuthor, velox.TableOf[User]())
	gqlimpl.FKIDList[int64](f, "writerIDIn", w.WriterIDIn, "Account", postColAuthorID, velox.PredIn, postEdgeAuthor, velox.TableOf[User]())
	gqlimpl.FKIDList[int64](f, "writerIDNotIn", w.WriterIDNotIn, "Account", postColAuthorID, velox.PredNotIn, postEdgeAuthor, velox.TableOf[User]())
	gqlimpl.Value(f, "title", w.Title, postColTitle, velox.PredEQ)
	gqlimpl.Value(f, "titleNEQ", w.TitleNEQ, postColTitle, velox.PredNEQ)
	gqlimpl.List(f, "titleIn", w.TitleIn, postColTitle, velox.PredIn)
	gqlimpl.List(f, "titleNotIn", w.TitleNotIn, postColTitle, velox.PredNotIn)
	gqlimpl.Value(f, "titleContains", w.TitleContains, postColTitle, velox.PredContains)
	gqlimpl.Value(f, "titleHasPrefix", w.TitleHasPrefix, postColTitle, velox.PredHasPrefix)
	gqlimpl.Value(f, "titleHasSuffix", w.TitleHasSuffix, postColTitle, velox.PredHasSuffix)
	gqlimpl.Value(f, "slug", w.Slug, postColSlug, velox.PredEQ)
	gqlimpl.Value(f, "slugNEQ", w.SlugNEQ, postColSlug, velox.PredNEQ)
	gqlimpl.List(f, "slugIn", w.SlugIn, postColSlug, velox.PredIn)
	gqlimpl.List(f, "slugNotIn", w.SlugNotIn, postColSlug, velox.PredNotIn)
	gqlimpl.Value(f, "status", w.Status, postColStatus, velox.PredEQ)
	gqlimpl.Value(f, "statusNEQ", w.StatusNEQ, postColStatus, velox.PredNEQ)
	gqlimpl.List(f, "statusIn", w.StatusIn, postColStatus, velox.PredIn)
	gqlimpl.List(f, "statusNotIn", w.StatusNotIn, postColStatus, velox.PredNotIn)
	gqlimpl.Value(f, "publishedAt", w.PublishedAt, postColPublishedAt, velox.PredEQ)
	gqlimpl.Value(f, "publishedAtNEQ", w.PublishedAtNEQ, postColPublishedAt, velox.PredNEQ)
	gqlimpl.List(f, "publishedAtIn", w.PublishedAtIn, postColPublishedAt, velox.PredIn)
	gqlimpl.List(f, "publishedAtNotIn", w.PublishedAtNotIn, postColPublishedAt, velox.PredNotIn)
	gqlimpl.Value(f, "publishedAtGT", w.PublishedAtGT, postColPublishedAt, velox.PredGT)
	gqlimpl.Value(f, "publishedAtGTE", w.PublishedAtGTE, postColPublishedAt, velox.PredGTE)
	gqlimpl.Value(f, "publishedAtLT", w.PublishedAtLT, postColPublishedAt, velox.PredLT)
	gqlimpl.Value(f, "publishedAtLTE", w.PublishedAtLTE, postColPublishedAt, velox.PredLTE)
	gqlimpl.Is(f, "publishedAtIsNil", w.PublishedAtIsNil, postColPublishedAt, velox.PredIsNull)
	gqlimpl.Is(f, "publishedAtNotNil", w.PublishedAtNotNil, postColPublishedAt, velox.PredNotNull)
	gqlimpl.Has(f, "hasWriter", w.HasWriter, postEdgeAuthor, velox.TableOf[User]())
	gqlimpl.HasWith(f, "hasWriterWith", w.HasWriterWith, postEdgeAuthor, velox.TableOf[User]())
	return f.Pred()
}

// postCreateInputGQL is the GraphQL name of the CreatePostInput field that sets
// the Post field whose Go name is goField, for the errors of its validators: ""
// for a field it does not set.
func postCreateInputGQL(goField string) string {
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
	}
	return ""
}

// postUpdateInputGQL is the GraphQL name of the UpdatePostInput field that sets
// the Post field whose Go name is goField, for the errors of its validators: ""
// for a field it does not set.
func postUpdateInputGQL(goField string) string {
	switch goField {
	case "Title":
		return "title"
	case "Status":
		return "status"
	case "PublishedAt":
		return "publishedAt"
	case "Tags":
		return "tags"
	case "Views":
		return "views"
	case "Rating":
		return "rating"
	case "AuthorID":
		return "writerID"
	}
	return ""
}

// postRelatedEdgeGQL is the many-to-many edge related of Post, through
// PostRelated, as its inputs write it.
func postRelatedEdgeGQL() gqlimpl.M2M[Post, Post, PostRelated, int64, int64] {
	return gqlimpl.NewM2M(postKeyGQL, Posts.ID, postKeyGQL, PostRelateds.PostID, PostRelateds.RelatedID,
		func(j *PostRelated) int64 { return j.RelatedID },
		func(own, k int64) PostRelated { return PostRelated{PostID: own, RelatedID: k} })
}

// postRelatedByEdgeGQL is the many-to-many edge related_by of Post, through
// PostRelated, as its inputs write it.
func postRelatedByEdgeGQL() gqlimpl.M2M[Post, Post, PostRelated, int64, int64] {
	return gqlimpl.NewM2M(postKeyGQL, Posts.ID, postKeyGQL, PostRelateds.RelatedID, PostRelateds.PostID,
		func(j *PostRelated) int64 { return j.PostID },
		func(own, k int64) PostRelated { return PostRelated{RelatedID: own, PostID: k} })
}

// CreatePostInput is the GraphQL input CreatePostInput; its method Create
// writes it. A field that is a pointer, or a list, is optional: nil means not
// given.
type CreatePostInput struct {
	Title       string
	Slug        string
	Status      *PostStatus
	PublishedAt *time.Time
	Tags        *[]string
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

// Create inserts x, a Post, with the input's fields and edges set; a nil x is a
// new one. The input's fields overwrite x's: to own a field, leave it out with
// graphql.Skip(graphql.SkipMutationCreateInput) or set it in a velox.Hook. x's
// other fields, foreign keys included, are trusted, not checked. It runs
// atomically on the DB in ctx, under the policies, and changes x even when it
// fails. CreatePostInput leaves out Views, which an insert requires: set it on
// x, or in a velox.Hook, or it is stored as the zero value.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreatePostInput) Create(ctx context.Context, x *Post) (*Post, error) {
	c, x := gqlimpl.NewCreate(ctx, "CreatePostInput", "Post", "gen.Post", x, postCreateInputGQL, Posts.Title, Posts.Slug, Posts.EditedBy)
	x.Title = in.Title
	x.Slug = in.Slug
	if in.Status != nil {
		x.Status = *in.Status
		c.Given(Posts.Status)
	}
	if in.PublishedAt != nil {
		x.PublishedAt = in.PublishedAt
		c.Given(Posts.PublishedAt)
	}
	if in.Tags != nil {
		x.Tags = *in.Tags
		c.Given(Posts.Tags)
	}
	if in.Rating != nil {
		x.Rating = in.Rating
		c.Given(Posts.Rating)
	}
	x.EditedBy = in.EditedBy
	writerIDKey := gqlimpl.Key[int64](c, "writerID", in.WriterID, "Account")
	gqlimpl.Exists(c, "writerID", "Account", 1, Users.ID.EQ(writerIDKey))
	x.AuthorID = writerIDKey
	relatedIDsKeys := gqlimpl.Keys[int64](c, "relatedIDs", in.RelatedIDs, "Post")
	gqlimpl.Join(c, "relatedIDs", "Post", postRelatedEdgeGQL(), relatedIDsKeys...)
	relatedByIDsKeys := gqlimpl.Keys[int64](c, "relatedByIDs", in.RelatedByIDs, "Post")
	gqlimpl.Join(c, "relatedByIDs", "Post", postRelatedByEdgeGQL(), relatedByIDsKeys...)
	return c.Insert()
}

// UpdatePostInput is the GraphQL input UpdatePostInput; its method Update
// writes it. A field that is a pointer, or a list, is optional: nil means not
// given. A clear field that is true clears a field or an edge, and is refused
// with the field or the edge's ID given too.
type UpdatePostInput struct {
	Title       *string
	Status      *PostStatus
	PublishedAt *time.Time
	// ClearPublishedAt sets the field published_at to NULL.
	ClearPublishedAt bool
	Tags             *[]string
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

// Update updates the Post with the ID id from the input and from set, the
// application's own setters, and returns the row as written. A setter of set
// wins over the input's field or edge that sets the same column, which is left
// as set says. The input's fields are validated as the client's, and set apart
// from them: a setter of set that its validator refuses is the application's
// error, returned as velox's. It runs atomically on the DB in ctx, under the
// policies.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *UpdatePostInput) Update(ctx context.Context, id string, set ...velox.Setter[Post]) (*Post, error) {
	u, key := gqlimpl.NewUpdate[Post, int64](ctx, "UpdatePostInput", "Post", "gen.Post", id, postUpdateInputGQL)
	if in.Title != nil {
		u.Set(Posts.Title.Set(*in.Title))
	}
	if in.Status != nil {
		u.Set(Posts.Status.Set(*in.Status))
	}
	if in.PublishedAt != nil {
		u.Set(Posts.PublishedAt.Set(*in.PublishedAt))
	}
	if gqlimpl.Cleared(u, "publishedAt", in.PublishedAt != nil, "clearPublishedAt", in.ClearPublishedAt) {
		u.Set(Posts.PublishedAt.SetNull())
	}
	if in.Tags != nil {
		u.Set(Posts.Tags.Set(*in.Tags))
	}
	if in.Views != nil {
		u.Set(Posts.Views.Set(*in.Views))
	}
	if in.Rating != nil {
		u.Set(Posts.Rating.Set(*in.Rating))
	}
	if gqlimpl.Cleared(u, "rating", in.Rating != nil, "clearRating", in.ClearRating) {
		u.Set(Posts.Rating.SetNull())
	}
	if in.WriterID != nil {
		writerIDKey := gqlimpl.Key[int64](u, "writerID", *in.WriterID, "Account")
		gqlimpl.Exists(u, "writerID", "Account", 1, Users.ID.EQ(writerIDKey))
		u.SetKey(Posts.AuthorID.Set(writerIDKey))
	}
	addRelatedIDsKeys, removeRelatedIDsKeys := gqlimpl.AddRemove[int64](u, "Post", "addRelatedIDs", in.AddRelatedIDs, "removeRelatedIDs", in.RemoveRelatedIDs)
	addRelatedByIDsKeys, removeRelatedByIDsKeys := gqlimpl.AddRemove[int64](u, "Post", "addRelatedByIDs", in.AddRelatedByIDs, "removeRelatedByIDs", in.RemoveRelatedByIDs)
	gqlimpl.Through(u, "addRelatedIDs", "Post", gqlimpl.Edit[int64]{Clear: in.ClearRelated, Remove: removeRelatedIDsKeys, Add: addRelatedIDsKeys}, key, postRelatedEdgeGQL())
	gqlimpl.Through(u, "addRelatedByIDs", "Post", gqlimpl.Edit[int64]{Clear: in.ClearRelatedBy, Remove: removeRelatedByIDsKeys, Add: addRelatedByIDsKeys}, key, postRelatedByEdgeGQL())
	return u.Exec(Posts.ID.EQ(key), set...)
}
