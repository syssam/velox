package gen

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion1

// IsNode marks Category as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Category) IsNode() {}

// IDGQL is the global ID of the Category, the GraphQL field id: the type
// Category and the key id, encoded.
func (x *Category) IDGQL() string { return veloxgql.EncodeID("Category", x.ID) }

// ChildrenGQL is the edge children: a connection to the Category nodes it leads
// to, paged, ordered and filtered by the arguments. With no argument it is over
// x.Children when a With loaded it, in the order loaded; else its queries are
// on Category, filtered by the relation, and run when gqlgen asks for the
// connection's fields.
func (x *Category) ChildrenGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *CategoryWhereInput) (*CategoryConnection, error) {
	if after == nil && first == nil && before == nil && last == nil && where == nil {
		if rows, ok := x.Children.Get(); ok {
			return &CategoryConnection{c: gqlimpl.LoadedConnection(ctx, rows, "Category", categoryKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return CategoriesConnectionOf(ctx, db.Query[Category]().Where(Categories.CategoryChildren.EQ(x.ID)), after, first, before, last, where)
}

// ParentGQL is the edge parent: the Category it leads to, or nil when there is
// none. It is x.Parent when a With loaded it, and else one query.
func (x *Category) ParentGQL(ctx context.Context) (*Category, error) {
	if v, ok := x.Parent.Get(); ok {
		return v, nil
	}
	if x.CategoryChildren == nil {
		return nil, nil
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	v, err := db.Query[Category]().Where(Categories.ID.EQ(*x.CategoryChildren)).One(ctx)
	if errors.Is(err, velox.ErrNotFound) {
		return nil, nil
	}
	return v, err
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
	edges := make([]*CategoryEdge, len(p.Rows))
	for i, x := range p.Rows {
		edges[i] = &CategoryEdge{Node: x, Cursor: p.Cursors[i]}
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
// veloxgql.DBFrom reads from ctx, paged and ordered by the arguments of a
// connection to Category: a Query field's resolver returns it. It fails before
// any query runs on arguments gqlimpl.NewConnection refuses.
func CategoriesConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *CategoryWhereInput) (*CategoryConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
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
// cursors. q must also select the key and the columns orderBy names: a Columns
// or Omit that drops them breaks the cursors, and nothing refuses it. It runs
// no query.
func CategoriesConnectionOf(ctx context.Context, q velox.Query[Category], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *CategoryWhereInput) (*CategoryConnection, error) {
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

// CategoryWhereInput is the GraphQL input CategoryWhereInput, a filter of
// Category nodes: a node matches when every predicate given holds.
type CategoryWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *CategoryWhereInput
	And []*CategoryWhereInput
	Or  []*CategoryWhereInput

	// The edge parent: whether it has a node, or one that matches.
	HasParent     *bool
	HasParentWith []*CategoryWhereInput
}

// Pred is the condition w stands for, on Category nodes: every predicate w sets
// holds, and a w that sets none, or a nil w, holds for every node. It fails,
// before any query runs, on an ID of another type (veloxgql.ErrWrongIDType) or
// a malformed one (veloxgql.ErrBadID), on a nesting deeper than the
// MaxFilterDepth of veloxgql.LimitsFrom(ctx) (veloxgql.ErrFilterTooDeep), on an
// and, an or, a has…With, an In or a NotIn of more than its MaxFilterWidth
// entries (veloxgql.ErrFilterTooWide), and on a where argument of more than its
// MaxFilterNodes inputs, predicates and values in all
// (veloxgql.ErrFilterTooLarge).
func (w *CategoryWhereInput) Pred(ctx context.Context) (velox.Pred[Category], error) {
	f := gqlimpl.NewFilter[Category](ctx, "CategoryWhereInput")
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.Is(f, "hasParent", w.HasParent, Categories.Parent.Has)
	gqlimpl.HasWith(f, "hasParentWith", w.HasParentWith, Categories.Parent.HasWhere)
	return f.Pred()
}

// categoryFieldGQL is the GraphQL name of the mutation-input field that sets
// the Category field whose Go name is goField, for the errors of its
// validators: "" for a field no input sets.
func categoryFieldGQL(goField string) string {
	switch goField {
	case "Title":
		return "title"
	case "CategoryChildren":
		return "parentID"
	}
	return ""
}

// CreateCategoryInput is the GraphQL input CreateCategoryInput, the input of
// createCategory. A field that is a pointer, or a list, is optional: nil means
// not given.
type CreateCategoryInput struct {
	Title string
	// ChildrenIDs are the IDs of the Category nodes the edge children leads to.
	ChildrenIDs []string
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *string
}

// String returns the input's fields except its sensitive ones.
func (in CreateCategoryInput) String() string {
	var p2 any
	if in.ParentID != nil {
		p2 = *in.ParentID
	}
	return fmt.Sprintf("CreateCategoryInput(Title=%v, ChildrenIDs=%v, ParentID=%v)", in.Title, in.ChildrenIDs, p2)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateCategoryInput) GoString() string { return in.String() }

// CreateCategory creates one Category from input, with its edges, and returns
// it, as the mutation createCategory does. It first decodes every ID and JSON
// value of input, and fails before any statement on one it cannot decode
// (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom reads from
// ctx, in one transaction (gqlimpl.Atomic): the operation's, or one of its own.
// The tenant scope and the policies apply as to any write: each ID must name a
// row the caller may read, and a row it re-points one the caller may change, or
// CreateCategory fails with NOT_FOUND and keeps nothing. A validator's refusal
// is BAD_USER_INPUT, naming the input field; velox's other errors are returned
// as they are.
func CreateCategory(ctx context.Context, input CreateCategoryInput) (*Category, error) {
	var (
		k1  []int64
		k2  int64
		err error
	)
	if k1, err = gqlimpl.DecodeIDs[int64](ctx, "CreateCategoryInput.childrenIDs", input.ChildrenIDs, "Category"); err != nil {
		return nil, err
	}
	if input.ParentID != nil {
		if k2, err = gqlimpl.DecodeID[int64]("CreateCategoryInput.parentID", *input.ParentID, "Category"); err != nil {
			return nil, err
		}
	}
	var out *Category
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		var x Category
		if input.ParentID != nil {
			if err := gqlimpl.Exist(ctx, db.Query[Category]().Where(Categories.ID.In(k2)), 1, "CreateCategoryInput.parentID", "Category"); err != nil {
				return err
			}
			x.CategoryChildren = &k2
		}
		x.Title = input.Title
		if err := db.Insert(&x).Exec(ctx); err != nil {
			return gqlimpl.WriteError(err, "CreateCategoryInput", "gen.Category", categoryFieldGQL)
		}
		if len(k1) > 0 {
			if n, err := db.Update[Category]().Set(Categories.CategoryChildren.Set(x.ID)).Where(Categories.ID.In(k1...), Categories.CategoryChildren.IsNull()).Exec(ctx); err != nil {
				return err
			} else if err := gqlimpl.Repointed(ctx, n, len(k1), db.Query[Category]().Where(Categories.ID.In(k1...), Categories.CategoryChildren.NotNull(), Categories.CategoryChildren.NEQ(x.ID)), "CreateCategoryInput.childrenIDs", "Category", "Category"); err != nil {
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

// UpdateCategoryInput is the GraphQL input UpdateCategoryInput, the input of
// updateCategory. A field that is a pointer, or a list, is optional: nil means
// not given. A clear field that is true clears a field or an edge, and is
// refused with the field or the edge's ID given too.
type UpdateCategoryInput struct {
	Title *string
	// AddChildrenIDs are the IDs of Category nodes to add to the edge children.
	AddChildrenIDs []string
	// RemoveChildrenIDs are the IDs of Category nodes to remove from the edge
	// children; an ID the edge does not lead to is ignored.
	RemoveChildrenIDs []string
	// ClearChildren removes every Category from the edge children, before the
	// IDs to remove and to add.
	ClearChildren bool
	// ParentID is the ID of the Category the edge parent leads to.
	ParentID *string
	// ClearParent unlinks the edge parent: it leads to no Category afterwards.
	ClearParent bool
}

// String returns the input's fields except its sensitive ones.
func (in UpdateCategoryInput) String() string {
	var p0 any
	if in.Title != nil {
		p0 = *in.Title
	}
	var p4 any
	if in.ParentID != nil {
		p4 = *in.ParentID
	}
	return fmt.Sprintf("UpdateCategoryInput(Title=%v, AddChildrenIDs=%v, RemoveChildrenIDs=%v, ClearChildren=%v, ParentID=%v, ClearParent=%v)", p0, in.AddChildrenIDs, in.RemoveChildrenIDs, in.ClearChildren, p4, in.ClearParent)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in UpdateCategoryInput) GoString() string { return in.String() }

// UpdateCategory updates the Category with the ID id from input, as the
// mutation updateCategory does, and returns it as it is afterwards: a field
// input does not give is left as it is, and a clear field sets a field to NULL
// or empties an edge. It first decodes id and every ID and JSON value of input,
// and refuses a field given with its clear field, or an ID both to add and to
// remove, before any statement (BAD_USER_INPUT). It then writes through the DB
// veloxgql.DBFrom reads from ctx, in one transaction (gqlimpl.Atomic): the
// operation's, or one of its own. The fields go in one UPDATE, which the tenant
// scope and the Read and Write policies scope; an input that sets none sends no
// UPDATE, and a COUNT under the same scope (Writable) checks the row instead,
// so that no edge of a row the caller may not change is written. The edges
// follow, each cleared, then its IDs removed, then its IDs added: an ID to
// remove the edge does not lead to is ignored, and one to add it already leads
// to is kept. Clearing an edge whose key is on the targets' rows, or removing
// from it, unlinks only the children the tenant scope and the target's Write
// policy let the caller change; one they hide stays linked. Each ID must name a
// row the caller may read, and a row it re-points one the caller may change; a
// child another node holds is refused (BAD_USER_INPUT). An id that names no row
// the caller may change, or an ID no row it may read, fails with NOT_FOUND and
// keeps nothing. A validator's refusal is BAD_USER_INPUT, naming the input
// field; velox's other errors are returned as they are. The row is read back as
// written, under the tenant scope and without the Read policy
// (velox.SkipPolicy): an update that takes the row out of what the caller may
// read still returns it. On MySQL the UPDATE's row count needs
// clientFoundRows=true, as velox's Update does.
func UpdateCategory(ctx context.Context, id string, input UpdateCategoryInput) (*Category, error) {
	var (
		key int64
		k1  []int64
		k2  []int64
		k4  int64
		err error
	)
	if key, err = gqlimpl.DecodeID[int64]("updateCategory.id", id, "Category"); err != nil {
		return nil, err
	}
	if k1, err = gqlimpl.DecodeIDs[int64](ctx, "UpdateCategoryInput.addChildrenIDs", input.AddChildrenIDs, "Category"); err != nil {
		return nil, err
	}
	if k2, err = gqlimpl.DecodeIDs[int64](ctx, "UpdateCategoryInput.removeChildrenIDs", input.RemoveChildrenIDs, "Category"); err != nil {
		return nil, err
	}
	if input.ParentID != nil {
		if k4, err = gqlimpl.DecodeID[int64]("UpdateCategoryInput.parentID", *input.ParentID, "Category"); err != nil {
			return nil, err
		}
	}
	if err = gqlimpl.Disjoint("UpdateCategoryInput", "addChildrenIDs", "removeChildrenIDs", k1, k2); err != nil {
		return nil, err
	}
	if input.ParentID != nil && input.ClearParent {
		return nil, gqlimpl.InputErrorf("UpdateCategoryInput: parentID and clearParent together")
	}
	var out *Category
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		var set []velox.Setter[Category]
		if input.Title != nil {
			set = append(set, Categories.Title.Set(*input.Title))
		}
		if input.ParentID != nil {
			if err := gqlimpl.Exist(ctx, db.Query[Category]().Where(Categories.ID.In(k4)), 1, "UpdateCategoryInput.parentID", "Category"); err != nil {
				return err
			}
			set = append(set, Categories.CategoryChildren.Set(k4))
		}
		if input.ClearParent {
			set = append(set, Categories.CategoryChildren.SetNull())
		}
		if len(set) > 0 {
			n, err := db.Update[Category]().Set(set...).Where(Categories.ID.EQ(key)).Exec(ctx)
			if err != nil {
				return gqlimpl.WriteError(err, "UpdateCategoryInput", "gen.Category", categoryFieldGQL)
			}
			if n == 0 {
				return gqlimpl.NotFound("updateCategory", "Category")
			}
		} else if n, err := db.Query[Category]().Where(Categories.ID.EQ(key)).Writable().Count(ctx); err != nil {
			return err
		} else if n == 0 {
			return gqlimpl.NotFound("updateCategory", "Category")
		}
		if input.ClearChildren {
			if _, err := db.Update[Category]().Set(Categories.CategoryChildren.SetNull()).Where(Categories.CategoryChildren.EQ(key)).Exec(ctx); err != nil {
				return err
			}
		}
		if len(k2) > 0 {
			if _, err := db.Update[Category]().Set(Categories.CategoryChildren.SetNull()).Where(Categories.ID.In(k2...), Categories.CategoryChildren.EQ(key)).Exec(ctx); err != nil {
				return err
			}
		}
		if len(k1) > 0 {
			if _, err := db.Update[Category]().Set(Categories.CategoryChildren.Set(key)).Where(Categories.ID.In(k1...), Categories.CategoryChildren.IsNull()).Exec(ctx); err != nil {
				return err
			}
			if n, err := db.Query[Category]().Where(Categories.ID.In(k1...), Categories.CategoryChildren.EQ(key)).Count(ctx); err != nil {
				return err
			} else if err := gqlimpl.Repointed(ctx, n, len(k1), db.Query[Category]().Where(Categories.ID.In(k1...), Categories.CategoryChildren.NotNull(), Categories.CategoryChildren.NEQ(key)), "UpdateCategoryInput.addChildrenIDs", "Category", "Category"); err != nil {
				return err
			}
		}
		x, err := db.Query[Category]().Where(Categories.ID.EQ(key)).One(velox.SkipPolicy(ctx, "graphql: the row this update wrote"))
		if errors.Is(err, velox.ErrNotFound) {
			return gqlimpl.NotFound("updateCategory", "Category")
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
