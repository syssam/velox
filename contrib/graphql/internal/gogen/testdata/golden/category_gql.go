package gen

import (
	"context"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion9

// IsNode marks Category as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Category) IsNode() {}

// IDGQL is the global ID of the Category, the GraphQL field id: the type
// Category and the key id, encoded.
func (x *Category) IDGQL() string { return veloxgql.EncodeID("Category", x.ID) }

// categoryNodeGQL is the Node type Category for Node and Nodes.
func categoryNodeGQL() gqlimpl.NodeKind {
	return gqlimpl.NewNode[Category, *Category](
		"Category",
		func(id string) (int64, error) { return veloxgql.DecodeID[int64](id, "Category") },
		func(db *velox.DB) velox.Query[Category] { return db.Query[Category]() },
		categoryColumnsGQL,
		Categories.ID.EQ, Categories.ID.In,
		func(x *Category) int64 { return x.ID },
	)
}

// ChildrenGQL is the edge children: a connection to the Category nodes it leads
// to, paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *Category) ChildrenGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *CategoryWhereInput) (*CategoryConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	c, err := CategoriesConnectionOf(ctx, db.Query[Category](), after, first, before, last, where)
	if err != nil {
		return nil, err
	}
	return &CategoryConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, Categories.Children, "Category.children", []any{after, first, before, last, where})}, nil
}

// ParentGQL is the edge parent: the Category it leads to, or nil when there is
// none. It is read with the edge of every row read with x, in one query.
func (x *Category) ParentGQL(ctx context.Context) (*Category, error) {
	if x.CategoryChildren == nil {
		return nil, nil
	}
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.Query[Category]()
	if fields, ok := gqlimpl.Selected(ctx, "Category"); ok {
		q = gqlimpl.Narrow(q, categoryColumnsGQL(fields))
	}
	return gqlimpl.EdgeOne(ctx, q, x, Categories.Parent, "Category.parent")
}

// CategoryConnection is the GraphQL type CategoryConnection, a connection to a
// list of Category. It holds the query of the whole list and the page asked
// for; gqlgen calls its methods for the fields a query selects, and each runs
// its query only then: edges and pageInfo share one page query, and totalCount
// runs a COUNT.
type CategoryConnection struct {
	c *gqlimpl.Connection[Category]
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *CategoryConnection) Edges(ctx context.Context) ([]*CategoryEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]CategoryEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*CategoryEdge, len(p.Rows))
	for i, x := range p.Rows {
		slab[i] = CategoryEdge{Node: x, Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page; see Edges.
func (c *CategoryConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Category nodes in the whole connection, not only
// on this page. It counts the rows when gqlgen asks for it, which is only when
// the field totalCount is selected.
func (c *CategoryConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// CategoryEdge is the GraphQL type CategoryEdge, an edge in a connection to a
// list of Category.
type CategoryEdge struct {
	// Node is the Category at the end of the edge.
	Node *Category
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// CategoriesConnection is the connection to every Category node, through the DB
// veloxgql.DBFromContext reads from ctx, paged and ordered by the arguments of
// a connection to Category: a Query field's resolver returns it. It fails
// before any query runs on arguments gqlimpl.NewConnection refuses.
func CategoriesConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *CategoryWhereInput) (*CategoryConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return CategoriesConnectionOf(ctx, db.Query[Category](), after, first, before, last, where)
}

// CategoriesConnectionOf is the connection to the Category nodes q returns,
// filtered by where, ordered by the key, and paged by the other arguments. q
// keeps its filters and With loads, and the scope the application gave it; its
// Offset is cleared and its Limit replaced, since the page is the connection's.
// q must have no OrderBy: the connection orders it, and a q with one is refused
// with an internal error, since its order would come first and break the
// cursors. q must also select the key and the columns orderBy names, which the
// cursors hold: a q whose Columns or Omit drops one is refused with an internal
// error. In a gqlgen field, the page reads only the columns the selection needs
// (categoryColumnsGQL), of those q selects; a column the selection does not
// need is left zero in the nodes, except the key, the foreign keys and the
// order columns, so an application's resolver must not read one it did not ask
// for. It runs no query.
func CategoriesConnectionOf(ctx context.Context, q velox.Query[Category], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *CategoryWhereInput) (*CategoryConnection, error) {
	if !q.Selects(Categories.ID) {
		return nil, fmt.Errorf("veloxgql: CategoriesConnectionOf: the base query does not select the key, which the cursors hold")
	}
	if fields, ok := gqlimpl.Selected(ctx, "Category"); ok {
		q = gqlimpl.Narrow(q, categoryColumnsGQL(fields))
	}
	if where != nil {
		p, err := where.Pred(ctx)
		if err != nil {
			return nil, err
		}
		q = q.Where(p)
	}
	c, err := gqlimpl.NewConnection(ctx, q, "Category", gqlimpl.Args{After: after, First: first, Before: before, Last: last}, nil, categoryKeyGQL())
	if err != nil {
		return nil, err
	}
	return &CategoryConnection{c: c}, nil
}

// categoryKeyGQL is the order of Category's key, which ends every order of a
// connection to Category.
func categoryKeyGQL() []gqlimpl.OrderTerm[Category] {
	return []gqlimpl.OrderTerm[Category]{
		gqlimpl.Term("id", veloxgql.OrderDirectionAsc, Categories.ID.Asc(), Categories.ID.Desc(),
			func(x *Category) int64 { return x.ID },
			func(x *Category, v int64) { x.ID = v }),
	}
}

// categoryColumnsGQL is the columns a selection of the Category fields fields
// reads: the key, every foreign key on the row, every order field's column, and
// the column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
func categoryColumnsGQL(fields []string) []velox.Column[Category] {
	cols := []velox.Column[Category]{Categories.ID, Categories.CategoryChildren}
	for _, f := range fields {
		switch f {
		case "id", "children", "parent":
		case "title":
			cols = append(cols, Categories.Title)
		default:
			return nil
		}
	}
	return cols
}

// CategoryWhereInput is the GraphQL input CategoryWhereInput, a filter of
// Category nodes: a node matches when every predicate given holds.
type CategoryWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *CategoryWhereInput
	And []*CategoryWhereInput
	Or  []*CategoryWhereInput

	// Predicates on the ID.
	ID      *string
	IDNEQ   *string
	IDIn    []string
	IDNotIn []string
	IDGT    *string
	IDGTE   *string
	IDLT    *string
	IDLTE   *string

	// The edge parent: whether it has a node, or one that matches.
	HasParent     *bool
	HasParentWith []*CategoryWhereInput
}

// Pred is the condition w stands for, on Category nodes: every predicate w sets
// holds, and a w that sets none, or a nil w, holds for every node. It fails,
// before any query runs, on an ID of another type or a malformed one, and on a
// filter over the limits of veloxgql.LimitsFromContext(ctx): the errors are
// gqlimpl.NewFilter's.
func (w *CategoryWhereInput) Pred(ctx context.Context) (velox.Pred[Category], error) {
	f := gqlimpl.NewFilter[Category](ctx, "CategoryWhereInput")
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID(f, "id", w.ID, "Category", Categories.ID.EQ)
	gqlimpl.ID(f, "idNEQ", w.IDNEQ, "Category", Categories.ID.NEQ)
	gqlimpl.IDList(f, "idIn", w.IDIn, "Category", Categories.ID.In)
	gqlimpl.IDList(f, "idNotIn", w.IDNotIn, "Category", Categories.ID.NotIn)
	gqlimpl.ID(f, "idGT", w.IDGT, "Category", Categories.ID.GT)
	gqlimpl.ID(f, "idGTE", w.IDGTE, "Category", Categories.ID.GTE)
	gqlimpl.ID(f, "idLT", w.IDLT, "Category", Categories.ID.LT)
	gqlimpl.ID(f, "idLTE", w.IDLTE, "Category", Categories.ID.LTE)
	gqlimpl.Is(f, "hasParent", w.HasParent, Categories.Parent.Has)
	gqlimpl.HasWith(f, "hasParentWith", w.HasParentWith, Categories.Parent.HasWhere)
	return f.Pred()
}

// categoryInputGQL is the GraphQL name of the CreateCategoryInput and
// UpdateCategoryInput field that sets the Category field whose Go name is
// goField, for the errors of its validators: "" for a field it does not set.
func categoryInputGQL(goField string) string {
	switch goField {
	case "Title":
		return "title"
	case "CategoryChildren":
		return "parentID"
	}
	return ""
}

// CreateCategoryInput is the GraphQL input CreateCategoryInput; its method
// Create writes it. A field that is a pointer, or a list, is optional: nil
// means not given.
type CreateCategoryInput struct {
	Title string
	// ChildIDs are the IDs of the Category nodes the edge children leads to.
	ChildIDs []string
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *string
}

// Create inserts x, a Category, with the input's fields and edges set, and
// returns it. A nil x is a new one; fields the input does not set are inserted
// as the caller set them, so a resolver sets there what the client must not
// choose. It runs atomically on the DB in ctx, under the policies, and changes
// x even when it fails.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateCategoryInput) Create(ctx context.Context, x *Category) (*Category, error) {
	c, x := gqlimpl.NewCreate(ctx, "CreateCategoryInput", "gen.Category", x, categoryInputGQL, Categories.Title)
	x.Title = in.Title
	childIDsKeys := gqlimpl.Keys[int64](c, "childIDs", in.ChildIDs, "Category")
	gqlimpl.Repoint(c, "childIDs", "Category", len(childIDsKeys), Categories.ID.In(childIDsKeys...), Categories.CategoryChildren, func(x *Category) int64 { return x.ID })
	if in.ParentID != nil {
		parentIDKey := gqlimpl.Key[int64](c, "parentID", *in.ParentID, "Category")
		gqlimpl.Exists(c, "parentID", "Category", 1, Categories.ID.EQ(parentIDKey))
		x.CategoryChildren = &parentIDKey
	}
	return c.Insert()
}

// UpdateCategoryInput is the GraphQL input UpdateCategoryInput; its method
// Update writes it. A field that is a pointer, or a list, is optional: nil
// means not given. A clear field that is true clears a field or an edge, and is
// refused with the field or the edge's ID given too.
type UpdateCategoryInput struct {
	Title *string
	// AddChildIDs are the IDs of Category nodes to add to the edge children.
	AddChildIDs []string
	// RemoveChildIDs are the IDs of Category nodes to remove from the edge
	// children; an ID the edge does not lead to is ignored.
	RemoveChildIDs []string
	// ClearChildren removes every Category from the edge children, before the
	// IDs to remove and to add.
	ClearChildren bool
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *string
	// ClearParent unlinks the edge parent: it leads to no Category afterwards.
	ClearParent bool
}

// Update updates the Category with the ID id from the input and then from set,
// the application's own setters, validated with the input's, and returns the
// row as written. It runs atomically on the DB in ctx, under the policies.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *UpdateCategoryInput) Update(ctx context.Context, id string, set ...velox.Setter[Category]) (*Category, error) {
	u, key := gqlimpl.NewUpdate[Category, int64](ctx, "UpdateCategoryInput", "gen.Category", id, categoryInputGQL)
	if in.Title != nil {
		u.Set(Categories.Title.Set(*in.Title))
	}
	addChildIDsKeys := gqlimpl.Keys[int64](u, "addChildIDs", in.AddChildIDs, "Category")
	removeChildIDsKeys := gqlimpl.Keys[int64](u, "removeChildIDs", in.RemoveChildIDs, "Category")
	gqlimpl.Disjoint(u, "addChildIDs", "removeChildIDs", addChildIDsKeys, removeChildIDsKeys)
	if in.ParentID != nil {
		parentIDKey := gqlimpl.Key[int64](u, "parentID", *in.ParentID, "Category")
		gqlimpl.Exists(u, "parentID", "Category", 1, Categories.ID.EQ(parentIDKey))
		u.SetKey(Categories.CategoryChildren.Set(parentIDKey))
	}
	if gqlimpl.ClearEdge(u, "parentID", in.ParentID != nil, "clearParent", in.ClearParent) {
		u.SetKey(Categories.CategoryChildren.SetNull())
	}
	gqlimpl.Children[Category](u, "addChildIDs", "Category", in.ClearChildren, removeChildIDsKeys, addChildIDsKeys, Categories.ID, Categories.CategoryChildren, key)
	return u.Exec(Categories.ID.EQ(key), set...)
}
