package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// Users is the edge users: a connection to the User nodes it leads to. Its
// queries batch with the same edge of every row on g's page.
func (g *Group) Users(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*UserConnection, error) {
	return newUserConnection(ctx, groupType.Edge(g, groupEdgeUsers), after, first, before, last)
}

// Memberships is the edge memberships: a connection to the Membership nodes it
// leads to. Its queries batch with the same edge of every row on g's page.
func (g *Group) Memberships(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*MembershipConnection, error) {
	return newMembershipConnection(ctx, groupType.Edge(g, groupEdgeMemberships), after, first, before, last)
}

// GroupConnection is the GraphQL type GroupConnection. Its fields run their
// queries when they are resolved.
type GroupConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *GroupConnection) Edges(ctx context.Context) ([]*GroupEdge, error) {
	p, err := c.Conn.Page(ctx)
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
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Group nodes in the whole connection, not only on
// this page.
func (c *GroupConnection) TotalCount(ctx context.Context) (int, error) { return c.Conn.TotalCount(ctx) }

// GroupEdge is the GraphQL type GroupEdge.
type GroupEdge struct {
	Node   *Group
	Cursor veloxgql.Cursor
}

// GroupPaginateOption is an option of a query's Paginate to Group nodes. It has
// none: Group has no order or where input.
type GroupPaginateOption struct{}

// PaginateQuery is the connection to the Group nodes q returns, ordered by the
// key, and paged by the other arguments: velox.Query's Paginate. q must not be
// ordered, and must select the key.
func (*Group) PaginateQuery(
	ctx context.Context, q velox.Query[Group],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []GroupPaginateOption,
) (*GroupConnection, error) {
	return newGroupConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last)
}

// newGroupConnection is PaginateQuery of the nodes from leads to: a query's, or
// an edge's, whose queries batch.
func newGroupConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*GroupConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	c, err := gqlgraph.Paginate(ctx, from, &groupType, args)
	if err != nil {
		return nil, err
	}
	return &GroupConnection{Conn: c}, nil
}

// groupType describes the GraphQL type Group to gqlgraph.
var groupType = gqlgraph.Type{
	Name:       "Group",
	Table:      untyped.TableOf[Group],
	Key:        []gqlgraph.Column{{Name: "id", Index: groupColID}},
	AlwaysRead: []int{groupColID},
	Fields:     []string{groupColID: "id", groupColName: "name"},
	Edges:      []string{groupEdgeUsers: "users", groupEdgeMemberships: "memberships"},
	Inputs:     []string{groupColName: "name"},
}

// CreateGroupInput is the GraphQL input CreateGroupInput. A nil pointer or list
// is a field not given, and so is a null.
type CreateGroupInput struct {
	Name string
	// UserIDs are the IDs of the User nodes the edge users leads to.
	UserIDs []int64 `graphql:"userIDs"`
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateGroupInput) Setters() ([]velox.Setter[Group], error) {
	var set []velox.Setter[Group]
	set = append(set, Groups.Name.Set(in.Name))
	if len(in.UserIDs) > 0 {
		userIDs, err := gqlgraph.IDs("CreateGroupInput.userIDs", in.UserIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Groups.Users.Add(userIDs...))
	}
	return gqlgraph.Validate("CreateGroupInput", &groupType, set)
}

// UpdateGroupInput is the GraphQL input UpdateGroupInput. A nil pointer or list
// is a field not given, and so is a null. A true clear field clears its field
// or edge.
type UpdateGroupInput struct {
	Name *string
	// AddUserIDs are the IDs of User nodes to add to the edge users.
	AddUserIDs []int64 `graphql:"addUserIDs"`
	// RemoveUserIDs are the IDs of User nodes to remove from the edge users; an
	// ID the edge does not lead to is ignored.
	RemoveUserIDs []int64 `graphql:"removeUserIDs"`
	// ClearUsers removes every User from the edge users, before the IDs to
	// remove and to add.
	ClearUsers bool
}

// Setters returns the input's fields and edges as setters for
// db.Update[Group]().SetInput. A field not given sets nothing, and a clear
// field of an edge with a list of IDs clears it before they are added.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateGroupInput) Setters() ([]velox.Setter[Group], error) {
	var set []velox.Setter[Group]
	if v := in.Name; v != nil {
		set = append(set, Groups.Name.Set(*v))
	}
	if in.ClearUsers {
		set = append(set, Groups.Users.Clear())
	}
	if len(in.RemoveUserIDs) > 0 {
		removeUserIDs, err := gqlgraph.IDs("UpdateGroupInput.removeUserIDs", in.RemoveUserIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Groups.Users.Remove(removeUserIDs...))
	}
	if len(in.AddUserIDs) > 0 {
		addUserIDs, err := gqlgraph.IDs("UpdateGroupInput.addUserIDs", in.AddUserIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Groups.Users.Add(addUserIDs...))
	}
	return gqlgraph.Validate("UpdateGroupInput", &groupType, set)
}
