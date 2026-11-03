package gen

import (
	"context"
	"io"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion11

// IsNode marks Group as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Group) IsNode() {}

// IDGQL is the global ID of the Group, the GraphQL field id: the type Group and
// the key id, encoded.
func (x *Group) IDGQL() string { return veloxgql.EncodeID("Group", x.ID) }

// groupNodeGQL is the Node type Group for Node and Nodes.
func groupNodeGQL() gqlimpl.NodeKind {
	return gqlimpl.NewNode("Group", velox.TableOf[Group](), groupColID,
		func(id string) (any, error) {
			k, err := veloxgql.DecodeID[int64](id, "Group")
			return k, err
		},
		groupColumnsGQL)
}

// groupKeyGQL is the key of x, a Group.
func groupKeyGQL(x *Group) int64 { return x.ID }

// MarshalGQL writes e as a GraphQL String, its stored value: the field
// visibility of Group is a String, as graphql.Skip(graphql.SkipEnumField) asks.
func (e GroupVisibility) MarshalGQL(w io.Writer) {
	switch e {
	case GroupVisibilityPublic:
		_, _ = io.WriteString(w, `"public"`)
	case GroupVisibilityPrivate:
		_, _ = io.WriteString(w, `"private"`)
	default: // not a value of the enum
		gqlimpl.WriteString(w, string(e))
	}
}

// UnmarshalGQL sets e from v, a String that is one of its stored values.
func (e *GroupVisibility) UnmarshalGQL(v any) error {
	switch v {
	case "public":
		*e = GroupVisibilityPublic
	case "private":
		*e = GroupVisibilityPrivate
	default:
		return gqlimpl.InputErrorf("GroupVisibility must be public or private, not %v", v)
	}
	return nil
}

// UsersGQL is the edge users: a connection to the Account nodes it leads to,
// paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *Group) UsersGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	c, err := AccountsConnectionOf(ctx, db.Query[User](), after, first, before, last, where)
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: gqlimpl.EdgeConnection(ctx, c.c, velox.TableOf[Group](), unsafe.Pointer(x), groupEdgeUsers, "Group.users", []any{after, first, before, last, where})}, nil
}

// GroupConnection is the GraphQL type GroupConnection, a connection to a list
// of Group. It holds the query of the whole list and the page asked for; gqlgen
// calls its methods for the fields a query selects, and each runs its query
// only then: edges and pageInfo share one page query, and totalCount runs a
// COUNT.
type GroupConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *GroupConnection) Edges(ctx context.Context) ([]*GroupEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]GroupEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*GroupEdge, len(p.Rows))
	for i, x := range p.Rows {
		slab[i] = GroupEdge{Node: (*Group)(x), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page; see Edges.
func (c *GroupConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Group nodes in the whole connection, not only on
// this page. It counts the rows when gqlgen asks for it, which is only when the
// field totalCount is selected.
func (c *GroupConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// GroupEdge is the GraphQL type GroupEdge, an edge in a connection to a list of
// Group.
type GroupEdge struct {
	// Node is the Group at the end of the edge.
	Node *Group
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// GroupsConnection is the connection to every Group node, through the DB
// veloxgql.DBFromContext reads from ctx, paged and ordered by the arguments of
// a connection to Group: a Query field's resolver returns it. It fails before
// any query runs on arguments gqlimpl.NewConnection refuses.
func GroupsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *GroupOrder) (*GroupConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return GroupsConnectionOf(ctx, db.Query[Group](), after, first, before, last, orderBy)
}

// GroupsConnectionOf is the connection to the Group nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments. q keeps its
// filters and With loads, and the scope the application gave it; its Offset is
// cleared and its Limit replaced, since the page is the connection's. q must
// have no OrderBy: the connection orders it, and a q with one is refused with
// an internal error, since its order would come first and break the cursors. q
// must also select the key and the columns orderBy names, which the cursors
// hold: a q whose Columns or Omit drops one is refused with an internal error.
// In a gqlgen field, the page reads only the columns the selection needs
// (groupColumnsGQL), of those q selects; a column the selection does not need
// is left zero in the nodes, except the key, the foreign keys and the order
// columns, so an application's resolver must not read one it did not ask for.
// It runs no query.
func GroupsConnectionOf(ctx context.Context, q velox.Query[Group], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *GroupOrder) (*GroupConnection, error) {
	var order []gqlimpl.Order
	if o := orderBy; o != nil {
		order = []gqlimpl.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &groupConnGQL, order)
	if err != nil {
		return nil, err
	}
	c, err := gqlimpl.NewConnection(ctx, b, &groupConnGQL, gqlimpl.Args{After: after, First: first, Before: before, Last: last}, order)
	if err != nil {
		return nil, err
	}
	return &GroupConnection{c: c}, nil
}

// groupConnGQL is what the connections to Group are made of: the key, which
// ends every order, and the order fields' columns.
var groupConnGQL = gqlimpl.Conn{
	Type: "Group",
	Key: []gqlimpl.Col{
		{Field: "id", Col: groupColID},
	},
	Order: []gqlimpl.Col{
		{Field: "NAME", Col: groupColName},
	},
	Columns: groupColumnsGQL,
}

// groupColumnsGQL is the columns a selection of the Group fields fields reads:
// the key, every foreign key on the row, every order field's column, and the
// column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
func groupColumnsGQL(fields []string) []int {
	cols := []int{groupColID, groupColName}
	for _, f := range fields {
		switch f {
		case "id", "name", "users":
		case "visibility":
			cols = append(cols, groupColVisibility)
		default:
			return nil
		}
	}
	return cols
}

// GroupOrder is the GraphQL input GroupOrder, an order of connections to Group.
type GroupOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field GroupOrderField
}

// GroupOrderField is the GraphQL enum GroupOrderField, the fields connections
// to Group can be ordered by.
type GroupOrderField string

// The values of GroupOrderField.
const (
	// GroupOrderFieldName orders by the field name.
	GroupOrderFieldName GroupOrderField = "NAME"
)

// IsValid reports whether o is a value of GroupOrderField.
func (o GroupOrderField) IsValid() bool {
	switch o {
	case "NAME":
		return true
	}
	return false
}

// String returns o's value.
func (o GroupOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum GroupOrderField.
func (o GroupOrderField) MarshalGQL(w io.Writer) { gqlimpl.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum GroupOrderField.
func (o *GroupOrderField) UnmarshalGQL(v any) error {
	switch v {
	case "NAME":
		*o = GroupOrderFieldName
	default:
		return gqlimpl.InputErrorf("GroupOrderField must be NAME, not %v", v)
	}
	return nil
}
