package gen

import (
	"context"
	"io"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion15

// IsNode marks Post as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Post) IsNode() {}

// GlobalID is the global ID of the Post, the GraphQL field id: its type and
// key, opaque to the client.
func (x *Post) GlobalID() string { return veloxgql.EncodeID("Post", x.ID) }

// MarshalGQL writes e as its value of the GraphQL enum PostStatus.
func (e PostStatus) MarshalGQL(w io.Writer) {
	switch e {
	case PostStatusDraft:
		_, _ = io.WriteString(w, `"DRAFT"`)
	case PostStatusInReview:
		_, _ = io.WriteString(w, `"IN_REVIEW"`)
	case PostStatusPublished:
		_, _ = io.WriteString(w, `"LIVE"`)
	default: // not a value of the enum
		gqlimpl.WriteString(w, string(e))
	}
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum PostStatus.
func (e *PostStatus) UnmarshalGQL(v any) error {
	switch v {
	case "DRAFT":
		*e = PostStatusDraft
	case "IN_REVIEW":
		*e = PostStatusInReview
	case "LIVE":
		*e = PostStatusPublished
	default:
		return gqlimpl.InputErrorf("PostStatus must be DRAFT, IN_REVIEW or LIVE, not %v", v)
	}
	return nil
}

// Author is the edge author: the Account it leads to, or nil. It is read in one
// query with the edge of every row read with x.
func (x *Post) Author(ctx context.Context) (*User, error) {
	v, err := gqlimpl.EdgeOne(ctx, &userConnGQL, &postConnGQL, x, postEdgeAuthor, "Post.writer")
	n, _ := v.(*User)
	return n, err
}

// Likes is the edge likes: the Like nodes it leads to, in key order. It is read
// in one query with the edge of every row read with x, up to MaxList; see
// gqlimpl.EdgeList.
func (x *Post) Likes(ctx context.Context) ([]*Like, error) {
	v, err := gqlimpl.EdgeList(ctx, &likeConnGQL, &postConnGQL, x, postEdgeLikes, "Post.likes", 0)
	n, _ := v.([]*Like)
	return n, err
}

// Related is the edge related: a connection to the Post nodes it leads to. Its
// queries batch with the edge of every row read with x; see
// gqlimpl.EdgeConnection.
func (x *Post) Related(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy []*PostOrder, where *PostWhereInput,
) (*PostConnection, error) {
	c, err := PostsConnection(ctx, after, first, before, last, orderBy, where)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: gqlimpl.EdgeConnection(
		ctx, c.c, &postConnGQL, x, postEdgeRelated, "Post.related",
		[]any{after, first, before, last, orderBy, where},
	)}, nil
}

// RelatedBy is the edge related_by: a connection to the Post nodes it leads to.
// Its queries batch with the edge of every row read with x; see
// gqlimpl.EdgeConnection.
func (x *Post) RelatedBy(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy []*PostOrder, where *PostWhereInput,
) (*PostConnection, error) {
	c, err := PostsConnection(ctx, after, first, before, last, orderBy, where)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: gqlimpl.EdgeConnection(
		ctx, c.c, &postConnGQL, x, postEdgeRelatedBy, "Post.relatedBy",
		[]any{after, first, before, last, orderBy, where},
	)}, nil
}

// PostConnection is the GraphQL type PostConnection, a connection to a list of
// Post. Its fields run their queries when gqlgen asks; see gqlimpl.Connection.
type PostConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *PostConnection) Edges(ctx context.Context) ([]*PostEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]PostEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*PostEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = PostEdge{Node: p.Node(i).(*Post), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *PostConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Post nodes in the whole connection, not only on
// this page.
func (c *PostConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// PostEdge is the GraphQL type PostEdge, an edge in a connection to a list of
// Post.
type PostEdge struct {
	// Node is the Post at the end of the edge.
	Node *Post
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// PostsConnection is the connection to every Post node, on the DB in ctx, for a
// Query field's resolver. It is PostsConnectionOf of the whole table.
func PostsConnection(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy []*PostOrder, where *PostWhereInput,
) (*PostConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return PostsConnectionOf(ctx, db.Query[Post](), after, first, before, last, orderBy, where)
}

// PostsConnectionOf is the connection to the Post nodes q returns, filtered by
// where, ordered by orderBy and then the key, and paged by the other arguments.
// q must not be ordered and must select the key and the order columns; see
// gqlimpl.ConnectionBase and gqlimpl.NewConnection.
func PostsConnectionOf(
	ctx context.Context, q velox.Query[Post],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy []*PostOrder, where *PostWhereInput,
) (*PostConnection, error) {
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
	c, err := gqlimpl.NewConnection(
		ctx, b, &postConnGQL,
		gqlimpl.Args{After: after, First: first, Before: before, Last: last},
		order,
	)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: c}, nil
}

// postConnGQL is what the runtime knows of the GraphQL type Post: its table,
// the key and the order columns of its connections, the columns its selections
// read and the fields of its inputs.
var postConnGQL = gqlimpl.Conn{
	Type:  "Post",
	Table: velox.TableOf[Post],
	Key:   []gqlimpl.Col{{Field: "id", Col: postColID}},
	Order: []gqlimpl.Col{
		{Field: "TITLE", Col: postColTitle},
		{Field: "VIEWS", Col: postColViews},
		{Field: "AUTHOR_NAME", Col: userColName, Edge: postEdgeAuthor + 1, Target: velox.TableOf[User]},
	},
	Read: []int{postColID, postColAuthorID, postColTitle, postColViews},
	Fields: []string{
		postColID:          "id",
		postColTitle:       "title",
		postColSlug:        "slug",
		postColStatus:      "status",
		postColPublishedAt: "publishedAt",
		postColTags:        "tags",
		postColViews:       "views",
		postColRating:      "rating",
		postColEditedBy:    "editedBy",
	},
	Edges: []string{"writer", "likes", "related", "relatedBy"},
	CreateInputs: []string{
		postColTitle:       "title",
		postColSlug:        "slug",
		postColStatus:      "status",
		postColPublishedAt: "publishedAt",
		postColTags:        "tags",
		postColRating:      "rating",
		postColEditedBy:    "editedBy",
		postColAuthorID:    "writerID",
	},
	UpdateInputs: []string{
		postColTitle:       "title",
		postColStatus:      "status",
		postColPublishedAt: "publishedAt",
		postColTags:        "tags",
		postColViews:       "views",
		postColRating:      "rating",
		postColAuthorID:    "writerID",
	},
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
func (o PostOrderField) MarshalGQL(w io.Writer) { gqlimpl.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum PostOrderField.
func (o *PostOrderField) UnmarshalGQL(v any) error {
	switch v {
	case "TITLE":
		*o = PostOrderFieldTitle
	case "VIEWS":
		*o = PostOrderFieldViews
	case "AUTHOR_NAME":
		*o = PostOrderFieldAuthorName
	default:
		return gqlimpl.InputErrorf("PostOrderField must be TITLE, VIEWS or AUTHOR_NAME, not %v", v)
	}
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

// Pred is the condition w stands for on Post nodes, true for a nil or empty w.
// Its errors are gqlimpl.NewFilter's.
func (w *PostWhereInput) Pred(ctx context.Context) (velox.Pred[Post], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[Post](p), err
}

// PredicateGQL is Pred, untyped, for velox-graphql's runtime.
func (w *PostWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, &postConnGQL)
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.IDField[int64](f, "id", postColID, gqlimpl.Ops[string]{
		EQ:    w.ID,
		NEQ:   w.IDNEQ,
		In:    w.IDIn,
		NotIn: w.IDNotIn,
		GT:    w.IDGT,
		GTE:   w.IDGTE,
		LT:    w.IDLT,
		LTE:   w.IDLTE,
	})
	gqlimpl.FKField[int64](f, "writerID", postColAuthorID, postEdgeAuthor, &userConnGQL, gqlimpl.Ops[string]{
		EQ:    w.WriterID,
		NEQ:   w.WriterIDNEQ,
		In:    w.WriterIDIn,
		NotIn: w.WriterIDNotIn,
	})
	gqlimpl.Field(f, "title", postColTitle, gqlimpl.Ops[string]{
		EQ:        w.Title,
		NEQ:       w.TitleNEQ,
		In:        w.TitleIn,
		NotIn:     w.TitleNotIn,
		Contains:  w.TitleContains,
		HasPrefix: w.TitleHasPrefix,
		HasSuffix: w.TitleHasSuffix,
	})
	gqlimpl.Field(f, "slug", postColSlug, gqlimpl.Ops[string]{
		EQ:    w.Slug,
		NEQ:   w.SlugNEQ,
		In:    w.SlugIn,
		NotIn: w.SlugNotIn,
	})
	gqlimpl.Field(f, "status", postColStatus, gqlimpl.Ops[PostStatus]{
		EQ:    w.Status,
		NEQ:   w.StatusNEQ,
		In:    w.StatusIn,
		NotIn: w.StatusNotIn,
	})
	gqlimpl.Field(f, "publishedAt", postColPublishedAt, gqlimpl.Ops[time.Time]{
		EQ:     w.PublishedAt,
		NEQ:    w.PublishedAtNEQ,
		In:     w.PublishedAtIn,
		NotIn:  w.PublishedAtNotIn,
		GT:     w.PublishedAtGT,
		GTE:    w.PublishedAtGTE,
		LT:     w.PublishedAtLT,
		LTE:    w.PublishedAtLTE,
		IsNil:  w.PublishedAtIsNil,
		NotNil: w.PublishedAtNotNil,
	})
	gqlimpl.Has(f, "hasWriter", w.HasWriter, postEdgeAuthor, velox.TableOf[User]())
	gqlimpl.HasWith(f, "hasWriterWith", w.HasWriterWith, postEdgeAuthor, velox.TableOf[User]())
	return f.Pred()
}

// CreatePostInput is the GraphQL input CreatePostInput, whose Setters gives its
// fields as setters. A nil pointer or list is a field not given, and so is a
// null.
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

// Setters returns the setters of the input's fields and edges, for
// db.Insert(x).Set: a field not given sets nothing, and x keeps its value or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or an ID that does not decode or
// names another type, is BAD_USER_INPUT naming the input's field. See
// "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each field does and what the
// write checks.
func (in *CreatePostInput) Setters() ([]velox.Setter[Post], error) {
	var set []velox.Setter[Post]
	set = append(set, Posts.Title.Set(in.Title))
	set = append(set, Posts.Slug.Set(in.Slug))
	if v := in.Status; v != nil {
		set = append(set, Posts.Status.Set(*v))
	}
	if v := in.PublishedAt; v != nil {
		set = append(set, Posts.PublishedAt.Set(*v))
	}
	if v := in.Tags; v != nil {
		set = append(set, Posts.Tags.Set(*v))
	}
	if v := in.Rating; v != nil {
		set = append(set, Posts.Rating.Set(*v))
	}
	set = append(set, Posts.EditedBy.Set(in.EditedBy))
	writerID, err := gqlimpl.DecodeID[int64]("CreatePostInput.writerID", in.WriterID, "Account")
	if err != nil {
		return nil, err
	}
	set = append(set, Posts.Author.Set(writerID))
	if len(in.RelatedIDs) > 0 {
		relatedIDs, err := gqlimpl.DecodeIDs[int64]("CreatePostInput.relatedIDs", in.RelatedIDs, "Post")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.Related.Add(relatedIDs...))
	}
	if len(in.RelatedByIDs) > 0 {
		relatedByIDs, err := gqlimpl.DecodeIDs[int64]("CreatePostInput.relatedByIDs", in.RelatedByIDs, "Post")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.RelatedBy.Add(relatedByIDs...))
	}
	return gqlimpl.Checked("CreatePostInput", &postConnGQL, set)
}

// UpdatePostInput is the GraphQL input UpdatePostInput, whose Setters gives its
// fields as setters. A nil pointer or list is a field not given, and so is a
// null. A true clear field clears its field or edge.
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

// Setters returns the setters of the input's fields and edges, for
// db.Update[Post]().Set: a field not given sets nothing, and a clear field wins
// over its field.
//
// It does no I/O. A value a validator refuses, or an ID that does not decode or
// names another type, is BAD_USER_INPUT naming the input's field. See
// "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each field does and what the
// write checks.
func (in *UpdatePostInput) Setters() ([]velox.Setter[Post], error) {
	var set []velox.Setter[Post]
	if v := in.Title; v != nil {
		set = append(set, Posts.Title.Set(*v))
	}
	if v := in.Status; v != nil {
		set = append(set, Posts.Status.Set(*v))
	}
	if in.ClearPublishedAt {
		set = append(set, Posts.PublishedAt.SetNull())
	} else if v := in.PublishedAt; v != nil {
		set = append(set, Posts.PublishedAt.Set(*v))
	}
	if v := in.Tags; v != nil {
		set = append(set, Posts.Tags.Set(*v))
	}
	if v := in.Views; v != nil {
		set = append(set, Posts.Views.Set(*v))
	}
	if in.ClearRating {
		set = append(set, Posts.Rating.SetNull())
	} else if v := in.Rating; v != nil {
		set = append(set, Posts.Rating.Set(*v))
	}
	if v := in.WriterID; v != nil {
		writerID, err := gqlimpl.DecodeID[int64]("UpdatePostInput.writerID", *v, "Account")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.Author.Set(writerID))
	}
	if in.ClearRelated {
		set = append(set, Posts.Related.Clear())
	}
	if len(in.RemoveRelatedIDs) > 0 {
		removeRelatedIDs, err := gqlimpl.DecodeIDs[int64]("UpdatePostInput.removeRelatedIDs", in.RemoveRelatedIDs, "Post")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.Related.Remove(removeRelatedIDs...))
	}
	if len(in.AddRelatedIDs) > 0 {
		addRelatedIDs, err := gqlimpl.DecodeIDs[int64]("UpdatePostInput.addRelatedIDs", in.AddRelatedIDs, "Post")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.Related.Add(addRelatedIDs...))
	}
	if in.ClearRelatedBy {
		set = append(set, Posts.RelatedBy.Clear())
	}
	if len(in.RemoveRelatedByIDs) > 0 {
		removeRelatedByIDs, err := gqlimpl.DecodeIDs[int64]("UpdatePostInput.removeRelatedByIDs", in.RemoveRelatedByIDs, "Post")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.RelatedBy.Remove(removeRelatedByIDs...))
	}
	if len(in.AddRelatedByIDs) > 0 {
		addRelatedByIDs, err := gqlimpl.DecodeIDs[int64]("UpdatePostInput.addRelatedByIDs", in.AddRelatedByIDs, "Post")
		if err != nil {
			return nil, err
		}
		set = append(set, Posts.RelatedBy.Add(addRelatedByIDs...))
	}
	return gqlimpl.Checked("UpdatePostInput", &postConnGQL, set)
}
