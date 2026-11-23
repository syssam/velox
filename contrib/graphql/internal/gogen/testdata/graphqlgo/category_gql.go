package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion28

// Children is the edge children: a connection to the Category nodes it leads
// to. Its queries batch with the same edge of every row on c's page.
func (c *Category) Children(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *CategoryWhereInput,
) (*CategoryConnection, error) {
	return newCategoryConnection(ctx, categoryType.Edge(c, categoryEdgeChildren), after, first, before, last, where)
}

// Parent is the edge parent: the Category it leads to, or nil. It is read in
// one query with the same edge of every row on c's page.
func (c *Category) Parent(ctx context.Context) (*Category, error) {
	if c.CategoryChildren == nil {
		return nil, nil
	}
	v, err := gqlgraph.EdgeOne(ctx, &categoryType, categoryType.Edge(c, categoryEdgeParent))
	n, _ := v.(*Category)
	return n, err
}

// CategoryConnection is the GraphQL type CategoryConnection. Its fields run
// their queries when they are resolved.
type CategoryConnection struct {
	c *gqlgraph.Connection
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

// CategoryEdge is the GraphQL type CategoryEdge.
type CategoryEdge struct {
	Node   *Category
	Cursor veloxgql.Cursor
}

// CategoryPaginateOption is an option of a query's Paginate to Category nodes:
// WithCategoryFilter. Of two options that set the same thing, the later wins.
type CategoryPaginateOption struct {
	where *CategoryWhereInput
}

// WithCategoryFilter filters the connection by where.
func WithCategoryFilter(where *CategoryWhereInput) CategoryPaginateOption {
	return CategoryPaginateOption{where: where}
}

// PaginateQuery is the connection to the Category nodes q returns, filtered by
// where, ordered by the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key.
func (*Category) PaginateQuery(
	ctx context.Context, q velox.Query[Category],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []CategoryPaginateOption,
) (*CategoryConnection, error) {
	var o CategoryPaginateOption
	for _, x := range opts {
		if x.where != nil {
			o.where = x.where
		}
	}
	return newCategoryConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.where)
}

// newCategoryConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newCategoryConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *CategoryWhereInput,
) (*CategoryConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last, Where: gqlgraph.WhereOf(where, (*CategoryWhereInput).Predicate)}
	c, err := gqlgraph.Paginate(ctx, from, &categoryType, args)
	if err != nil {
		return nil, err
	}
	return &CategoryConnection{c: c}, nil
}

// categoryType describes the GraphQL type Category to gqlgraph.
var categoryType = gqlgraph.Type{
	Name:       "Category",
	Table:      untyped.TableOf[Category],
	Key:        []gqlgraph.Column{{Name: "id", Index: categoryColID}},
	AlwaysRead: []int{categoryColID, categoryColCategoryChildren},
	Fields:     []string{categoryColID: "id", categoryColTitle: "title"},
	Edges: []string{
		categoryEdgeChildren: "children",
		categoryEdgeParent:   "parent",
	},
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality | gqlgraph.OpsOrder, Col: categoryColID},
		{Name: "hasParent", Kind: gqlgraph.WhereHas, Edge: categoryEdgeParent, Target: untyped.TableOf[Category]},
		{Name: "hasParentWith", Kind: gqlgraph.WhereHasWith, Edge: categoryEdgeParent, Target: untyped.TableOf[Category]},
	},
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
	ID      *int64
	IDNEQ   *int64
	IDIn    []int64
	IDNotIn []int64
	IDGT    *int64
	IDGTE   *int64
	IDLT    *int64
	IDLTE   *int64

	// The edge parent: whether it has a node, or one that matches.
	HasParent     *bool
	HasParentWith []*CategoryWhereInput
}

// Pred is the condition w stands for on Category nodes, true for a nil or empty
// w. It fails on a filter over the limits of veloxgql.LimitsFromContext(ctx).
func (w *CategoryWhereInput) Pred(ctx context.Context) (velox.Pred[Category], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[Category](p), err
}

// Predicate is Pred, untyped.
func (w *CategoryWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &categoryType, w)
}

// CreateCategoryInput is the GraphQL input CreateCategoryInput. A nil pointer
// or list is a field not given, and so is a null.
type CreateCategoryInput struct {
	Title string
	// ChildIDs are the IDs of the Category nodes the edge children leads to.
	ChildIDs []int64 `graphql:"childIDs"`
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *int64 `graphql:"parentID"`
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateCategoryInput) Setters() ([]velox.Setter[Category], error) {
	var set []velox.Setter[Category]
	set = append(set, Categories.Title.Set(in.Title))
	if len(in.ChildIDs) > 0 {
		childIDs, err := gqlgraph.IDs("CreateCategoryInput.childIDs", in.ChildIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Children.Add(childIDs...))
	}
	if v := in.ParentID; v != nil {
		set = append(set, Categories.Parent.Set(*v))
	}
	return gqlgraph.Validate("CreateCategoryInput", &categoryType, set)
}

// UpdateCategoryInput is the GraphQL input UpdateCategoryInput. A nil pointer
// or list is a field not given, and so is a null. A true clear field clears its
// field or edge.
type UpdateCategoryInput struct {
	Title *string
	// AddChildIDs are the IDs of Category nodes to add to the edge children.
	AddChildIDs []int64 `graphql:"addChildIDs"`
	// RemoveChildIDs are the IDs of Category nodes to remove from the edge
	// children; an ID the edge does not lead to is ignored.
	RemoveChildIDs []int64 `graphql:"removeChildIDs"`
	// ClearChildren removes every Category from the edge children, before the
	// IDs to remove and to add.
	ClearChildren bool
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *int64 `graphql:"parentID"`
	// ClearParent unlinks the edge parent: it leads to no Category afterwards.
	ClearParent bool
}

// Setters returns the input's fields and edges as setters for
// db.Update[Category]().SetInput. A field not given sets nothing, and a clear
// field wins over its field, and a clear field of an edge with a list of IDs
// clears it before they are added.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateCategoryInput) Setters() ([]velox.Setter[Category], error) {
	var set []velox.Setter[Category]
	if v := in.Title; v != nil {
		set = append(set, Categories.Title.Set(*v))
	}
	if in.ClearChildren {
		set = append(set, Categories.Children.Clear())
	}
	if len(in.RemoveChildIDs) > 0 {
		removeChildIDs, err := gqlgraph.IDs("UpdateCategoryInput.removeChildIDs", in.RemoveChildIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Children.Remove(removeChildIDs...))
	}
	if len(in.AddChildIDs) > 0 {
		addChildIDs, err := gqlgraph.IDs("UpdateCategoryInput.addChildIDs", in.AddChildIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Categories.Children.Add(addChildIDs...))
	}
	if in.ClearParent {
		set = append(set, Categories.CategoryChildren.SetNull())
	} else if v := in.ParentID; v != nil {
		set = append(set, Categories.Parent.Set(*v))
	}
	return gqlgraph.Validate("UpdateCategoryInput", &categoryType, set)
}
