package gen

import (
	"context"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion11

// IsNode marks Category as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Category) IsNode() {}

// IDGQL is the global ID of the Category, the GraphQL field id: the type
// Category and the key id, encoded.
func (x *Category) IDGQL() string { return veloxgql.EncodeID("Category", x.ID) }

// categoryNodeGQL is the Node type Category for Node and Nodes.
func categoryNodeGQL() gqlimpl.NodeKind {
	return gqlimpl.NewNode("Category", velox.TableOf[Category](), categoryColID,
		func(id string) (any, error) {
			k, err := veloxgql.DecodeID[int64](id, "Category")
			return k, err
		},
		categoryColumnsGQL)
}

// categoryKeyGQL is the key of x, a Category.
func categoryKeyGQL(x *Category) int64 { return x.ID }

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
	return &CategoryConnection{c: gqlimpl.EdgeConnection(ctx, c.c, velox.TableOf[Category](), unsafe.Pointer(x), categoryEdgeChildren, "Category.children", []any{after, first, before, last, where})}, nil
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
	q := db.QueryTable(velox.TableOf[Category]())
	if fields, ok := gqlimpl.Selected(ctx, "Category"); ok {
		q = gqlimpl.Narrow(q, categoryColumnsGQL(fields))
	}
	p, err := gqlimpl.EdgeOne(ctx, q, velox.TableOf[Category](), unsafe.Pointer(x), categoryEdgeParent, "Category.parent")
	return (*Category)(p), err
}

// CategoryConnection is the GraphQL type CategoryConnection, a connection to a
// list of Category. It holds the query of the whole list and the page asked
// for; gqlgen calls its methods for the fields a query selects, and each runs
// its query only then: edges and pageInfo share one page query, and totalCount
// runs a COUNT.
type CategoryConnection struct {
	c *gqlimpl.Connection
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
		slab[i] = CategoryEdge{Node: (*Category)(x), Cursor: p.Cursors[i]}
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
	c, err := gqlimpl.NewConnection(ctx, b, &categoryConnGQL, gqlimpl.Args{After: after, First: first, Before: before, Last: last}, nil)
	if err != nil {
		return nil, err
	}
	return &CategoryConnection{c: c}, nil
}

// categoryConnGQL is what the connections to Category are made of: the key,
// which ends every order, and the order fields' columns.
var categoryConnGQL = gqlimpl.Conn{
	Type: "Category",
	Key: []gqlimpl.Col{
		{Field: "id", Col: categoryColID},
	},
	Columns: categoryColumnsGQL,
}

// categoryColumnsGQL is the columns a selection of the Category fields fields
// reads: the key, every foreign key on the row, every order field's column, and
// the column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
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

// Pred is the condition w stands for, on Category nodes: every predicate w sets
// holds, and a w that sets none, or a nil w, holds for every node. It fails,
// before any query runs, on an ID of another type or a malformed one, and on a
// filter over the limits of veloxgql.LimitsFromContext(ctx): the errors are
// gqlimpl.NewFilter's.
func (w *CategoryWhereInput) Pred(ctx context.Context) (velox.Pred[Category], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[Category](p), err
}

// PredicateGQL is Pred, untyped, for velox-graphql's runtime.
func (w *CategoryWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, "CategoryWhereInput", velox.TableOf[Category]())
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID[int64](f, "id", w.ID, "Category", categoryColID, velox.PredEQ)
	gqlimpl.ID[int64](f, "idNEQ", w.IDNEQ, "Category", categoryColID, velox.PredNEQ)
	gqlimpl.IDList[int64](f, "idIn", w.IDIn, "Category", categoryColID, velox.PredIn)
	gqlimpl.IDList[int64](f, "idNotIn", w.IDNotIn, "Category", categoryColID, velox.PredNotIn)
	gqlimpl.ID[int64](f, "idGT", w.IDGT, "Category", categoryColID, velox.PredGT)
	gqlimpl.ID[int64](f, "idGTE", w.IDGTE, "Category", categoryColID, velox.PredGTE)
	gqlimpl.ID[int64](f, "idLT", w.IDLT, "Category", categoryColID, velox.PredLT)
	gqlimpl.ID[int64](f, "idLTE", w.IDLTE, "Category", categoryColID, velox.PredLTE)
	gqlimpl.Has(f, "hasParent", w.HasParent, categoryEdgeParent, velox.TableOf[Category]())
	gqlimpl.HasWith(f, "hasParentWith", w.HasParentWith, categoryEdgeParent, velox.TableOf[Category]())
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

// categoryChildrenEdgeGQL is the edge children of Category, whose key
// Category's rows hold, as its inputs write it.
func categoryChildrenEdgeGQL() gqlimpl.Owned {
	return gqlimpl.Owned{Owner: velox.TableOf[Category](), Target: velox.TableOf[Category](), OwnerKey: categoryColID, TargetKey: categoryColID, FK: categoryColCategoryChildren}
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

// Create inserts x, a Category, with the input's fields and edges set; a nil x
// is a new one. The input's fields overwrite x's: to own a field, leave it out
// with graphql.Skip(graphql.SkipMutationCreateInput) or set it in a velox.Hook.
// x's other fields, foreign keys included, are trusted, not checked. It runs
// atomically on the DB in ctx, under the policies, and changes x even when it
// fails.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateCategoryInput) Create(ctx context.Context, x *Category) (*Category, error) {
	if x == nil {
		x = new(Category)
	}
	c := gqlimpl.NewCreate(ctx, "CreateCategoryInput", "Category", "gen.Category", velox.TableOf[Category](), unsafe.Pointer(x), categoryInputGQL, categoryColTitle)
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

// Update updates the Category with the ID id from the input and from set, the
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
func (in *UpdateCategoryInput) Update(ctx context.Context, id string, set ...velox.Setter[Category]) (*Category, error) {
	u, key := gqlimpl.NewUpdate[int64](ctx, "UpdateCategoryInput", "Category", "gen.Category", velox.TableOf[Category](), categoryColID, id, categoryInputGQL)
	if in.Title != nil {
		u.Set(categoryColTitle, *in.Title)
	}
	addChildIDsKeys, removeChildIDsKeys := gqlimpl.AddRemove[int64](u, "Category", "addChildIDs", in.AddChildIDs, "removeChildIDs", in.RemoveChildIDs)
	if in.ParentID != nil {
		parentIDKey := gqlimpl.Key[int64](u, "parentID", *in.ParentID, "Category")
		gqlimpl.Exists(u, "parentID", "Category", velox.TableOf[Category](), categoryColID, []int64{parentIDKey})
		u.SetKey(categoryColCategoryChildren, parentIDKey)
	}
	if gqlimpl.ClearEdge(u, "parentID", in.ParentID != nil, "clearParent", in.ClearParent) {
		u.SetKey(categoryColCategoryChildren, nil)
	}
	gqlimpl.Children(u, "addChildIDs", "Category", gqlimpl.Edit{Clear: in.ClearChildren, Remove: removeChildIDsKeys, Add: addChildIDsKeys}, key, categoryChildrenEdgeGQL())
	p, err := u.Exec(key, velox.Assignments(set))
	return (*Category)(p), err
}
