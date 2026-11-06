package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion13

// IsNode marks Category as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Category) IsNode() {}

// GlobalID is the global ID of the Category, the GraphQL field id: its type and
// key, opaque to the client.
func (x *Category) GlobalID() string { return veloxgql.EncodeID("Category", x.ID) }

// Children is the edge children: a connection to the Category nodes it leads
// to. Its queries batch with the edge of every row read with x; see
// gqlimpl.EdgeConnection.
func (x *Category) Children(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *CategoryWhereInput,
) (*CategoryConnection, error) {
	c, err := CategoriesConnection(ctx, after, first, before, last, where)
	if err != nil {
		return nil, err
	}
	return &CategoryConnection{c: gqlimpl.EdgeConnection(
		ctx, c.c, &categoryConnGQL, x, categoryEdgeChildren, "Category.children",
		[]any{after, first, before, last, where},
	)}, nil
}

// Parent is the edge parent: the Category it leads to, or nil. It is read in
// one query with the edge of every row read with x.
func (x *Category) Parent(ctx context.Context) (*Category, error) {
	if x.CategoryChildren == nil {
		return nil, nil
	}
	v, err := gqlimpl.EdgeOne(ctx, &categoryConnGQL, &categoryConnGQL, x, categoryEdgeParent, "Category.parent")
	n, _ := v.(*Category)
	return n, err
}

// CategoryConnection is the GraphQL type CategoryConnection, a connection to a
// list of Category. Its fields run their queries when gqlgen asks; see
// gqlimpl.Connection.
type CategoryConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *CategoryConnection) Edges(ctx context.Context) ([]*CategoryEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]CategoryEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*CategoryEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = CategoryEdge{Node: p.Node(i).(*Category), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *CategoryConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Category nodes in the whole connection, not only
// on this page.
func (c *CategoryConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// CategoryEdge is the GraphQL type CategoryEdge, an edge in a connection to a
// list of Category.
type CategoryEdge struct {
	// Node is the Category at the end of the edge.
	Node *Category
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// CategoriesConnection is the connection to every Category node, on the DB in
// ctx, for a Query field's resolver. It is CategoriesConnectionOf of the whole
// table.
func CategoriesConnection(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *CategoryWhereInput,
) (*CategoryConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return CategoriesConnectionOf(ctx, db.Query[Category](), after, first, before, last, where)
}

// CategoriesConnectionOf is the connection to the Category nodes q returns,
// filtered by where, ordered by the key, and paged by the other arguments. q
// must not be ordered and must select the key and the order columns; see
// gqlimpl.ConnectionBase and gqlimpl.NewConnection.
func CategoriesConnectionOf(
	ctx context.Context, q velox.Query[Category],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *CategoryWhereInput,
) (*CategoryConnection, error) {
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &categoryConnGQL, nil)
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
		ctx, b, &categoryConnGQL,
		gqlimpl.Args{After: after, First: first, Before: before, Last: last},
		nil,
	)
	if err != nil {
		return nil, err
	}
	return &CategoryConnection{c: c}, nil
}

// categoryConnGQL is what the runtime knows of the GraphQL type Category: its
// table, the key and the order columns of its connections, the columns its
// selections read and the fields of its inputs.
var categoryConnGQL = gqlimpl.Conn{
	Type:  "Category",
	Table: velox.TableOf[Category],
	Key: []gqlimpl.Col{
		{Field: "id", Col: categoryColID},
	},
	Columns:     categoryColumnsGQL,
	CreateField: categoryInputGQL,
	UpdateField: categoryInputGQL,
}

// categoryColumnsGQL returns the columns a selection of the given Category
// fields reads. It returns nil, every column, for a field that is not
// velox-graphql's; see gqlimpl.Conn.
func categoryColumnsGQL(fields []string) []int {
	cols := []int{categoryColID, categoryColCategoryChildren}
	for _, f := range fields {
		switch f {
		case "id", "children", "parent":
		case "title":
			cols = append(cols, categoryColTitle)
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

// Pred is the condition w stands for on Category nodes, true for a nil or empty
// w. Its errors are gqlimpl.NewFilter's.
func (w *CategoryWhereInput) Pred(ctx context.Context) (velox.Pred[Category], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[Category](p), err
}

// PredicateGQL is Pred, untyped, for velox-graphql's runtime.
func (w *CategoryWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, &categoryConnGQL)
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.IDField[int64](f, "id", categoryColID, gqlimpl.Ops[string]{
		EQ:    w.ID,
		NEQ:   w.IDNEQ,
		In:    w.IDIn,
		NotIn: w.IDNotIn,
		GT:    w.IDGT,
		GTE:   w.IDGTE,
		LT:    w.IDLT,
		LTE:   w.IDLTE,
	})
	gqlimpl.Has(f, "hasParent", w.HasParent, categoryEdgeParent, velox.TableOf[Category]())
	gqlimpl.HasWith(f, "hasParentWith", w.HasParentWith, categoryEdgeParent, velox.TableOf[Category]())
	return f.Pred()
}

// categoryInputGQL is the CreateCategoryInput and UpdateCategoryInput field
// that sets the Category field goField, or "". A validator's error names it.
func categoryInputGQL(goField string) string {
	switch goField {
	case "Title":
		return "title"
	case "CategoryChildren":
		return "parentID"
	}
	return ""
}

// categoryChildrenEdgeGQL is the edge children of Category, whose key
// Category's rows hold.
func categoryChildrenEdgeGQL() gqlimpl.Owned {
	return gqlimpl.Owned{
		Owner:     velox.TableOf[Category](),
		Target:    velox.TableOf[Category](),
		OwnerKey:  categoryColID,
		TargetKey: categoryColID,
		FK:        categoryColCategoryChildren,
	}
}

// CreateCategoryInput is the GraphQL input CreateCategoryInput, which Create
// writes. A nil pointer or list is a field not given.
type CreateCategoryInput struct {
	Title string
	// ChildIDs are the IDs of the Category nodes the edge children leads to.
	ChildIDs []string
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *string
}

// Create inserts x, a Category, with the input's fields and edges set; a nil x
// is a new one. It runs atomically on the DB in ctx, under the policies.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateCategoryInput) Create(ctx context.Context, x *Category) (*Category, error) {
	if x == nil {
		x = new(Category)
	}
	c := gqlimpl.NewCreate(ctx, &categoryConnGQL, x, categoryColTitle)
	x.Title = in.Title
	childIDsKeys := gqlimpl.Keys[int64](c, "childIDs", in.ChildIDs, "Category")
	gqlimpl.Repoint(c, "childIDs", "Category", categoryChildrenEdgeGQL(), childIDsKeys)
	if in.ParentID != nil {
		parentIDKey := gqlimpl.Key[int64](c, "parentID", *in.ParentID, "Category")
		gqlimpl.Exists(c, "parentID", "Category", velox.TableOf[Category](), categoryColID, []int64{parentIDKey})
		x.CategoryChildren = &parentIDKey
	}
	if err := c.Insert(); err != nil {
		return nil, err
	}
	return x, nil
}

// UpdateCategoryInput is the GraphQL input UpdateCategoryInput, which Update
// writes. A nil pointer or list is a field not given. A true clear field clears
// its field or edge.
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

// Update updates the Category with the ID id from the input and from set, and
// returns the row as written. A setter of set wins over the input's for the
// same column.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *UpdateCategoryInput) Update(ctx context.Context, id string, set ...velox.Setter[Category]) (*Category, error) {
	u, key := gqlimpl.NewUpdate[int64](ctx, &categoryConnGQL, id)
	if in.Title != nil {
		u.Set(categoryColTitle, *in.Title)
	}
	addChildIDsKeys, removeChildIDsKeys := gqlimpl.AddRemove[int64](
		u, "Category",
		"addChildIDs", in.AddChildIDs,
		"removeChildIDs", in.RemoveChildIDs,
	)
	if in.ParentID != nil {
		parentIDKey := gqlimpl.Key[int64](u, "parentID", *in.ParentID, "Category")
		gqlimpl.Exists(u, "parentID", "Category", velox.TableOf[Category](), categoryColID, []int64{parentIDKey})
		u.SetKey(categoryColCategoryChildren, parentIDKey)
	}
	if gqlimpl.ClearEdge(u, "parentID", in.ParentID != nil, "clearParent", in.ClearParent) {
		u.SetKey(categoryColCategoryChildren, nil)
	}
	gqlimpl.Children(u, "addChildIDs", "Category", gqlimpl.Edit{
		Clear:  in.ClearChildren,
		Remove: removeChildIDsKeys,
		Add:    addChildIDsKeys,
	}, key, categoryChildrenEdgeGQL())
	v, err := u.Exec(key, velox.Assignments(set))
	n, _ := v.(*Category)
	return n, err
}
