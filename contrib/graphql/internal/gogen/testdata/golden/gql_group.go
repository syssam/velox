package gen

import (
	"context"
	"encoding/json"
	"errors"
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
	return errors.New("GroupVisibility must be public or private")
}

// UsersGQL is the edge users: a connection to the Account nodes it leads to,
// paged, ordered and filtered by the arguments. With no argument it is over
// x.Users when a With loaded it, in the order loaded; else its queries are on
// User, filtered by the relation, and run when gqlgen asks for the connection's
// fields.
func (x *Group) UsersGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	if after == nil && first == nil && before == nil && last == nil && where == nil {
		if rows, ok := x.Users.Get(); ok {
			return &AccountConnection{c: gqlimpl.LoadedConnection(rows, "Account", userKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return userConnectionGQL(ctx, db.Query[User]().Where(Users.Groups.HasWhere(Groups.ID.EQ(x.ID))), after, first, before, last, where)
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
	return groupConnectionGQL(ctx, db.Query[Group](), after, first, before, last, orderBy)
}

// groupConnectionGQL is the connection to the Group nodes q returns, ordered by
// orderBy and then the key, and paged by the other arguments. It runs no query.
func groupConnectionGQL(ctx context.Context, q velox.Query[Group], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *GroupOrder) (*GroupConnection, error) {
	var order []gqlimpl.OrderTerm[Group]
	if o := orderBy; o != nil {
		term, err := groupTermGQL(o)
		if err != nil {
			return nil, err
		}
		order = append(order, term)
	}
	c, err := gqlimpl.NewConnection(q, "Group", gqlimpl.Args{After: after, First: first, Before: before, Last: last}, order, groupKeyGQL())
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

// groupTermGQL is the order term o, an order of connections to Group.
func groupTermGQL(o *GroupOrder) (gqlimpl.OrderTerm[Group], error) {
	switch o.Field {
	case GroupOrderFieldName:
		return gqlimpl.Term(string(o.Field), o.Direction, Groups.Name.Asc(), Groups.Name.Desc(),
			func(x *Group) string { return x.Name },
			func(x *Group, v string) { x.Name = v }), nil
	}
	return gqlimpl.OrderTerm[Group]{}, fmt.Errorf("veloxgql: GroupOrder: %q is not a value of GroupOrderField", o.Field)
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
		return errors.New("GroupOrderField must be NAME")
	}
	*o = GroupOrderField(s)
	return nil
}
