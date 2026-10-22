package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion1

// IsNode marks Group as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Group) IsNode() {}

// IDGQL is the global ID of the Group, the GraphQL field id: the type Group and
// the key id, encoded.
func (x *Group) IDGQL() string { return veloxgql.EncodeID("Group", x.ID) }

// MarshalGQL writes e as a GraphQL String, its stored value: the field
// visibility of Group is a String, as graphql.Skip(graphql.SkipEnumField) asks.
func (e GroupVisibility) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(e)) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets e from v, a String that is one of its stored values.
func (e *GroupVisibility) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	switch s {
	case "public", "private":
		*e = GroupVisibility(s)
		return nil
	}
	return gqlimpl.InputErrorf("GroupVisibility must be public or private")
}

// UsersGQL is the edge users: a connection to the Account nodes it leads to,
// paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *Group) UsersGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	c, err := AccountsConnectionOf(ctx, db.Query[User](), after, first, before, last, where)
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, Groups.Users)}, nil
}

// GroupConnection is the GraphQL type GroupConnection, a connection to a list
// of Group. It holds the query of the whole list and the page asked for; gqlgen
// calls its methods for the fields a query selects, and each runs its query
// only then: edges and pageInfo share one page query, and totalCount runs a
// COUNT.
type GroupConnection struct {
	c *gqlimpl.Connection[Group]
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *GroupConnection) Edges(ctx context.Context) ([]*GroupEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	edges := make([]*GroupEdge, len(p.Rows))
	for i, x := range p.Rows {
		edges[i] = &GroupEdge{Node: x, Cursor: p.Cursors[i]}
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
// veloxgql.DBFrom reads from ctx, paged and ordered by the arguments of a
// connection to Group: a Query field's resolver returns it. It fails before any
// query runs on arguments gqlimpl.NewConnection refuses.
func GroupsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *GroupOrder) (*GroupConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
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
// is left zero in the nodes, except the key, the foreign keys, the order
// columns and the tenant, so an application's resolver must not read one it did
// not ask for. It runs no query.
func GroupsConnectionOf(ctx context.Context, q velox.Query[Group], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *GroupOrder) (*GroupConnection, error) {
	if !q.Selects(Groups.ID) {
		return nil, fmt.Errorf("veloxgql: GroupsConnectionOf: the base query does not select the key, which the cursors hold")
	}
	if o := orderBy; o != nil {
		switch {
		case o.Field == GroupOrderFieldName && !q.Selects(Groups.Name):
			return nil, fmt.Errorf("veloxgql: GroupsConnectionOf: the base query does not select the column of %s, which orderBy names and the cursors hold", o.Field)
		}
	}
	if fields, ok := gqlimpl.Selected(ctx, "Group"); ok {
		q = gqlimpl.Narrow(q, groupColumnsGQL(fields))
	}
	var order []gqlimpl.OrderTerm[Group]
	if o := orderBy; o != nil {
		term, err := groupTermGQL(o)
		if err != nil {
			return nil, err
		}
		order = append(order, term)
	}
	c, err := gqlimpl.NewConnection(ctx, q, "Group", gqlimpl.Args{After: after, First: first, Before: before, Last: last}, order, groupKeyGQL())
	if err != nil {
		return nil, err
	}
	return &GroupConnection{c: c}, nil
}

// groupKeyGQL is the order of Group's key, which ends every order of a
// connection to Group.
func groupKeyGQL() []gqlimpl.OrderTerm[Group] {
	return []gqlimpl.OrderTerm[Group]{
		gqlimpl.Term("id", veloxgql.OrderDirectionAsc, Groups.ID.Asc(), Groups.ID.Desc(),
			func(x *Group) int64 { return x.ID },
			func(x *Group, v int64) { x.ID = v }),
	}
}

// groupColumnsGQL is the columns a selection of the Group fields fields reads:
// the key, every foreign key on the row, every order field's column, the tenant
// column, and the column of each field. It is nil, every column, when a field
// is not one velox-graphql wrote, such as an application's own.
func groupColumnsGQL(fields []string) []velox.Column[Group] {
	cols := []velox.Column[Group]{Groups.ID, Groups.Name}
	for _, f := range fields {
		switch f {
		case "id", "name", "users":
		case "visibility":
			cols = append(cols, Groups.Visibility)
		default:
			return nil
		}
	}
	return cols
}

// groupTermGQL is the order term o, an order of connections to Group.
func groupTermGQL(o *GroupOrder) (gqlimpl.OrderTerm[Group], error) {
	switch o.Field {
	case GroupOrderFieldName:
		return gqlimpl.Term(string(o.Field), o.Direction, Groups.Name.Asc(), Groups.Name.Desc(),
			func(x *Group) string { return x.Name },
			func(x *Group, v string) { x.Name = v }), nil
	}
	return gqlimpl.OrderTerm[Group]{}, gqlimpl.InputErrorf("GroupOrder: %q is not a value of GroupOrderField", o.Field)
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
func (o GroupOrderField) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(o)) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets o from v, a value of the GraphQL enum GroupOrderField.
func (o *GroupOrderField) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	if !GroupOrderField(s).IsValid() {
		return gqlimpl.InputErrorf("GroupOrderField must be NAME")
	}
	*o = GroupOrderField(s)
	return nil
}
