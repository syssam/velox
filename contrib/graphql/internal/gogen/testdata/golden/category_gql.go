package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion15

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
	Type:   "Category",
	Table:  velox.TableOf[Category],
	Key:    []gqlimpl.Col{{Field: "id", Col: categoryColID}},
	Read:   []int{categoryColID, categoryColCategoryChildren},
	Fields: []string{categoryColID: "id", categoryColTitle: "title"},
	Edges:  []string{"children", "parent"},
	Inputs: []string{
		categoryColTitle:            "title",
		categoryColCategoryChildren: "parentID",
	},
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

// CreateCategoryInput is the GraphQL input CreateCategoryInput, whose Setters
// gives its fields as setters. A nil pointer or list is a field not given, and
// so is a null.
type CreateCategoryInput struct {
	Title string
	// ChildIDs are the IDs of the Category nodes the edge children leads to.
	ChildIDs []string
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *string
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
func (in *CreateCategoryInput) Setters() ([]velox.Setter[Category], error) {
	var set []velox.Setter[Category]
	set = append(set, Categories.Title.Set(in.Title))
	if len(in.ChildIDs) > 0 {
		childIDs, err := gqlimpl.DecodeIDs[int64]("CreateCategoryInput.childIDs", in.ChildIDs, "Category")
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Children.Add(childIDs...))
	}
	if v := in.ParentID; v != nil {
		parentID, err := gqlimpl.DecodeID[int64]("CreateCategoryInput.parentID", *v, "Category")
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Parent.Set(parentID))
	}
	return gqlimpl.Checked("CreateCategoryInput", &categoryConnGQL, set)
}

// UpdateCategoryInput is the GraphQL input UpdateCategoryInput, whose Setters
// gives its fields as setters. A nil pointer or list is a field not given, and
// so is a null. A true clear field clears its field or edge.
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

// Setters returns the setters of the input's fields and edges, for
// db.Update[Category]().Set: a field not given sets nothing, and a clear field
// wins over its field.
//
// It does no I/O. A value a validator refuses, or an ID that does not decode or
// names another type, is BAD_USER_INPUT naming the input's field. See
// "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each field does and what the
// write checks.
func (in *UpdateCategoryInput) Setters() ([]velox.Setter[Category], error) {
	var set []velox.Setter[Category]
	if v := in.Title; v != nil {
		set = append(set, Categories.Title.Set(*v))
	}
	if in.ClearChildren {
		set = append(set, Categories.Children.Clear())
	}
	if len(in.RemoveChildIDs) > 0 {
		removeChildIDs, err := gqlimpl.DecodeIDs[int64]("UpdateCategoryInput.removeChildIDs", in.RemoveChildIDs, "Category")
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Children.Remove(removeChildIDs...))
	}
	if len(in.AddChildIDs) > 0 {
		addChildIDs, err := gqlimpl.DecodeIDs[int64]("UpdateCategoryInput.addChildIDs", in.AddChildIDs, "Category")
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Children.Add(addChildIDs...))
	}
	if in.ClearParent {
		set = append(set, Categories.CategoryChildren.SetNull())
	} else if v := in.ParentID; v != nil {
		parentID, err := gqlimpl.DecodeID[int64]("UpdateCategoryInput.parentID", *v, "Category")
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Parent.Set(parentID))
	}
	return gqlimpl.Checked("UpdateCategoryInput", &categoryConnGQL, set)
}
