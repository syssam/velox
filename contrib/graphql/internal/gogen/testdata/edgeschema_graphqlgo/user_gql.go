package gen

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion28

// Groups is the edge groups: a connection to the Group nodes it leads to. Its
// queries batch with the same edge of every row on u's page.
func (u *User) Groups(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*GroupConnection, error) {
	return newGroupConnection(ctx, userType.Edge(u, userEdgeGroups), after, first, before, last)
}

// Following is the edge following: a connection to the User nodes it leads to.
// Its queries batch with the same edge of every row on u's page.
func (u *User) Following(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*UserConnection, error) {
	return newUserConnection(ctx, userType.Edge(u, userEdgeFollowing), after, first, before, last)
}

// Followers is the edge followers: a connection to the User nodes it leads to.
// Its queries batch with the same edge of every row on u's page.
func (u *User) Followers(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*UserConnection, error) {
	return newUserConnection(ctx, userType.Edge(u, userEdgeFollowers), after, first, before, last)
}

// Memberships is the edge memberships: a connection to the Membership nodes it
// leads to. Its queries batch with the same edge of every row on u's page.
func (u *User) Memberships(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*MembershipConnection, error) {
	return newMembershipConnection(ctx, userType.Edge(u, userEdgeMemberships), after, first, before, last)
}

// Follows is the edge follows: a connection to the Follow nodes it leads to.
// Its queries batch with the same edge of every row on u's page.
func (u *User) Follows(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*FollowConnection, error) {
	return newFollowConnection(ctx, userType.Edge(u, userEdgeFollows), after, first, before, last)
}

// UserConnection is the GraphQL type UserConnection. Its fields run their
// queries when they are resolved.
type UserConnection struct {
	c *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *UserConnection) Edges(ctx context.Context) ([]*UserEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]UserEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*UserEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = UserEdge{Node: p.Node(i).(*User), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *UserConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of User nodes in the whole connection, not only on
// this page.
func (c *UserConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// UserEdge is the GraphQL type UserEdge.
type UserEdge struct {
	Node   *User
	Cursor veloxgql.Cursor
}

// UserPaginateOption is an option of a query's Paginate to User nodes. It has
// none: User has no order or where input.
type UserPaginateOption struct{}

// PaginateQuery is the connection to the User nodes q returns, ordered by the
// key, and paged by the other arguments: velox.Query's Paginate. q must not be
// ordered, and must select the key.
func (*User) PaginateQuery(
	ctx context.Context, q velox.Query[User],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []UserPaginateOption,
) (*UserConnection, error) {
	return newUserConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last)
}

// newUserConnection is PaginateQuery of the nodes from leads to: a query's, or
// an edge's, whose queries batch.
func newUserConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*UserConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	c, err := gqlgraph.Paginate(ctx, from, &userType, args)
	if err != nil {
		return nil, err
	}
	return &UserConnection{c: c}, nil
}

// userType describes the GraphQL type User to gqlgraph.
var userType = gqlgraph.Type{
	Name:       "User",
	Table:      untyped.TableOf[User],
	Key:        []gqlgraph.Column{{Name: "id", Index: userColID}},
	AlwaysRead: []int{userColID},
	Fields:     []string{userColID: "id", userColName: "name"},
	Edges: []string{
		userEdgeGroups:      "groups",
		userEdgeFollowing:   "following",
		userEdgeFollowers:   "followers",
		userEdgeMemberships: "memberships",
		userEdgeFollows:     "follows",
	},
	Inputs: []string{userColName: "name"},
}

// CreateUserInput is the GraphQL input CreateUserInput. A nil pointer or list
// is a field not given, and so is a null.
type CreateUserInput struct {
	Name string
	// GroupIDs are the IDs of the Group nodes the edge groups leads to.
	GroupIDs []int64 `graphql:"groupIDs"`
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateUserInput) Setters() ([]velox.Setter[User], error) {
	var set []velox.Setter[User]
	set = append(set, Users.Name.Set(in.Name))
	if len(in.GroupIDs) > 0 {
		groupIDs, err := gqlgraph.IDs("CreateUserInput.groupIDs", in.GroupIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Groups.Add(groupIDs...))
	}
	return gqlgraph.Validate("CreateUserInput", &userType, set)
}

// UpdateUserInput is the GraphQL input UpdateUserInput. A nil pointer or list
// is a field not given, and so is a null. A true clear field clears its field
// or edge.
type UpdateUserInput struct {
	Name *string
	// AddGroupIDs are the IDs of Group nodes to add to the edge groups.
	AddGroupIDs []int64 `graphql:"addGroupIDs"`
	// RemoveGroupIDs are the IDs of Group nodes to remove from the edge groups;
	// an ID the edge does not lead to is ignored.
	RemoveGroupIDs []int64 `graphql:"removeGroupIDs"`
	// ClearGroups removes every Group from the edge groups, before the IDs to
	// remove and to add.
	ClearGroups bool
	// RemoveFollowingIDs are the IDs of User nodes to remove from the edge
	// following; an ID the edge does not lead to is ignored.
	RemoveFollowingIDs []int64 `graphql:"removeFollowingIDs"`
	// ClearFollowing removes every User from the edge following, before the IDs
	// to remove.
	ClearFollowing bool
	// RemoveFollowerIDs are the IDs of User nodes to remove from the edge
	// followers; an ID the edge does not lead to is ignored.
	RemoveFollowerIDs []int64 `graphql:"removeFollowerIDs"`
	// ClearFollowers removes every User from the edge followers, before the IDs
	// to remove.
	ClearFollowers bool
}

// Setters returns the input's fields and edges as setters for
// db.Update[User]().SetInput. A field not given sets nothing, and a clear field
// of an edge with a list of IDs clears it before they are added.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateUserInput) Setters() ([]velox.Setter[User], error) {
	var set []velox.Setter[User]
	if v := in.Name; v != nil {
		set = append(set, Users.Name.Set(*v))
	}
	if in.ClearGroups {
		set = append(set, Users.Groups.Clear())
	}
	if len(in.RemoveGroupIDs) > 0 {
		removeGroupIDs, err := gqlgraph.IDs("UpdateUserInput.removeGroupIDs", in.RemoveGroupIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Groups.Remove(removeGroupIDs...))
	}
	if len(in.AddGroupIDs) > 0 {
		addGroupIDs, err := gqlgraph.IDs("UpdateUserInput.addGroupIDs", in.AddGroupIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Groups.Add(addGroupIDs...))
	}
	if in.ClearFollowing {
		set = append(set, Users.Following.Clear())
	}
	if len(in.RemoveFollowingIDs) > 0 {
		removeFollowingIDs, err := gqlgraph.IDs("UpdateUserInput.removeFollowingIDs", in.RemoveFollowingIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Following.Remove(removeFollowingIDs...))
	}
	if in.ClearFollowers {
		set = append(set, Users.Followers.Clear())
	}
	if len(in.RemoveFollowerIDs) > 0 {
		removeFollowerIDs, err := gqlgraph.IDs("UpdateUserInput.removeFollowerIDs", in.RemoveFollowerIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Followers.Remove(removeFollowerIDs...))
	}
	return gqlgraph.Validate("UpdateUserInput", &userType, set)
}
