package gen

import (
	"context"
	"io"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion14

// IsNode marks Group as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Group) IsNode() {}

// GlobalID is the global ID of the Group, the GraphQL field id: its type and
// key, opaque to the client.
func (x *Group) GlobalID() string { return veloxgql.EncodeID("Group", x.ID) }

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

// Users is the edge users: a connection to the Account nodes it leads to. Its
// queries batch with the edge of every row read with x; see
// gqlimpl.EdgeConnection.
func (x *Group) Users(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *AccountWhereInput,
) (*AccountConnection, error) {
	c, err := AccountsConnection(ctx, after, first, before, last, where)
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: gqlimpl.EdgeConnection(
		ctx, c.c, &groupConnGQL, x, groupEdgeUsers, "Group.users",
		[]any{after, first, before, last, where},
	)}, nil
}

// GroupConnection is the GraphQL type GroupConnection, a connection to a list
// of Group. Its fields run their queries when gqlgen asks; see
// gqlimpl.Connection.
type GroupConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *GroupConnection) Edges(ctx context.Context) ([]*GroupEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]GroupEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*GroupEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = GroupEdge{Node: p.Node(i).(*Group), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *GroupConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Group nodes in the whole connection, not only on
// this page.
func (c *GroupConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// GroupEdge is the GraphQL type GroupEdge, an edge in a connection to a list of
// Group.
type GroupEdge struct {
	// Node is the Group at the end of the edge.
	Node *Group
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// GroupsConnection is the connection to every Group node, on the DB in ctx, for
// a Query field's resolver. It is GroupsConnectionOf of the whole table.
func GroupsConnection(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *GroupOrder,
) (*GroupConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return GroupsConnectionOf(ctx, db.Query[Group](), after, first, before, last, orderBy)
}

// GroupsConnectionOf is the connection to the Group nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments. q must not be
// ordered and must select the key and the order columns; see
// gqlimpl.ConnectionBase and gqlimpl.NewConnection.
func GroupsConnectionOf(
	ctx context.Context, q velox.Query[Group],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *GroupOrder,
) (*GroupConnection, error) {
	var order []gqlimpl.Order
	if o := orderBy; o != nil {
		order = []gqlimpl.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &groupConnGQL, order)
	if err != nil {
		return nil, err
	}
	c, err := gqlimpl.NewConnection(
		ctx, b, &groupConnGQL,
		gqlimpl.Args{After: after, First: first, Before: before, Last: last},
		order,
	)
	if err != nil {
		return nil, err
	}
	return &GroupConnection{c: c}, nil
}

// groupConnGQL is what the runtime knows of the GraphQL type Group: its table,
// the key and the order columns of its connections, the columns its selections
// read and the fields of its inputs.
var groupConnGQL = gqlimpl.Conn{
	Type:  "Group",
	Table: velox.TableOf[Group],
	Key:   []gqlimpl.Col{{Field: "id", Col: groupColID}},
	Order: []gqlimpl.Col{
		{Field: "NAME", Col: groupColName},
	},
	Read: []int{groupColID, groupColName},
	Fields: []string{
		groupColID:         "id",
		groupColName:       "name",
		groupColVisibility: "visibility",
	},
	Edges: []string{"users"},
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
