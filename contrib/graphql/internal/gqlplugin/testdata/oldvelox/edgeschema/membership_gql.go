package gen

import (
	"context"
	"io"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	_ "github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// MarshalGQL writes e as its value of the GraphQL enum MembershipRole.
func (e MembershipRole) MarshalGQL(w io.Writer) {
	switch e {
	case MembershipRoleOwner:
		_, _ = io.WriteString(w, `"OWNER"`)
	case MembershipRoleMember:
		_, _ = io.WriteString(w, `"MEMBER"`)
	default: // not a value of the enum
		gqlgraph.WriteString(w, string(e))
	}
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum MembershipRole.
func (e *MembershipRole) UnmarshalGQL(v any) error {
	switch v {
	case "OWNER":
		*e = MembershipRoleOwner
	case "MEMBER":
		*e = MembershipRoleMember
	default:
		return gqlgraph.InputErrorf("MembershipRole must be OWNER or MEMBER, not %v", v)
	}
	return nil
}

// User is the edge user: the User it leads to, or nil. It is read in one query
// with the same edge of every row on m's page.
func (m *Membership) User(ctx context.Context) (*User, error) {
	v, err := gqlgraph.EdgeOne(ctx, &userType, membershipType.Edge(m, membershipEdgeUser))
	n, _ := v.(*User)
	return n, err
}

// Group is the edge group: the Group it leads to, or nil. It is read in one
// query with the same edge of every row on m's page.
func (m *Membership) Group(ctx context.Context) (*Group, error) {
	v, err := gqlgraph.EdgeOne(ctx, &groupType, membershipType.Edge(m, membershipEdgeGroup))
	n, _ := v.(*Group)
	return n, err
}

// MembershipConnection is the GraphQL type MembershipConnection. Its fields run
// their queries when they are resolved.
type MembershipConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *MembershipConnection) Edges(ctx context.Context) ([]*MembershipEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]MembershipEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*MembershipEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = MembershipEdge{Node: p.Node(i).(*Membership), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *MembershipConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Membership nodes in the whole connection, not
// only on this page.
func (c *MembershipConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// MembershipEdge is the GraphQL type MembershipEdge.
type MembershipEdge struct {
	Node   *Membership
	Cursor veloxgql.Cursor
}

// MembershipPaginateOption is an option of a query's Paginate to Membership
// nodes. It has none: Membership has no order or where input.
type MembershipPaginateOption struct{}

// PaginateQuery is the connection to the Membership nodes q returns, ordered by
// the key, and paged by the other arguments: velox.Query's Paginate. q must not
// be ordered, and must select the key.
func (*Membership) PaginateQuery(
	ctx context.Context, q velox.Query[Membership],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []MembershipPaginateOption,
) (*MembershipConnection, error) {
	return newMembershipConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last)
}

// newMembershipConnection is PaginateQuery of the nodes from leads to: a
// query's, or an edge's, whose queries batch.
func newMembershipConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*MembershipConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last}
	c, err := gqlgraph.Paginate(ctx, from, &membershipType, args)
	if err != nil {
		return nil, err
	}
	return &MembershipConnection{Conn: c}, nil
}

// membershipType describes the GraphQL type Membership to gqlgraph.
var membershipType = gqlgraph.Type{
	Name:       "Membership",
	Table:      untyped.TableOf[Membership],
	Key:        []gqlgraph.Column{{Name: "id", Index: membershipColID}},
	AlwaysRead: []int{membershipColID, membershipColUserID, membershipColGroupID},
	Fields: []string{
		membershipColID:       "id",
		membershipColRole:     "role",
		membershipColJoinedAt: "joinedAt",
		membershipColUserID:   "userId",
		membershipColGroupID:  "groupId",
	},
	Edges: []string{membershipEdgeUser: "user", membershipEdgeGroup: "group"},
	CreateInputs: []string{
		membershipColRole:     "role",
		membershipColJoinedAt: "joinedAt",
		membershipColUserID:   "userId",
		membershipColGroupID:  "groupId",
	},
	UpdateInputs: []string{
		membershipColRole:    "role",
		membershipColUserID:  "userId",
		membershipColGroupID: "groupId",
	},
	LenientFilters: true,
}

// CreateMembershipInput is the GraphQL input CreateMembershipInput. A nil
// pointer or list is a field not given, and so is a null.
type CreateMembershipInput struct {
	Role     *MembershipRole
	JoinedAt *time.Time
	// UserID is the ID of the User the edge user leads to.
	UserID int64
	// GroupID is the ID of the Group the edge group leads to.
	GroupID int64
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateMembershipInput) Setters() ([]velox.Setter[Membership], error) {
	var set []velox.Setter[Membership]
	if v := in.Role; v != nil {
		set = append(set, Memberships.Role.Set(*v))
	}
	if v := in.JoinedAt; v != nil {
		set = append(set, Memberships.JoinedAt.Set(*v))
	}
	set = append(set, Memberships.User.Set(in.UserID))
	set = append(set, Memberships.Group.Set(in.GroupID))
	return gqlgraph.Validate("CreateMembershipInput", &membershipType, set)
}

// UpdateMembershipInput is the GraphQL input UpdateMembershipInput. A nil
// pointer or list is a field not given, and so is a null.
type UpdateMembershipInput struct {
	Role *MembershipRole
	// UserID is the ID of the User the edge user leads to.
	UserID *int64
	// GroupID is the ID of the Group the edge group leads to.
	GroupID *int64
}

// Setters returns the input's fields and edges as setters for
// db.Update[Membership]().SetInput. A field not given sets nothing.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateMembershipInput) Setters() ([]velox.Setter[Membership], error) {
	var set []velox.Setter[Membership]
	if v := in.Role; v != nil {
		set = append(set, Memberships.Role.Set(*v))
	}
	if v := in.UserID; v != nil {
		set = append(set, Memberships.User.Set(*v))
	}
	if v := in.GroupID; v != nil {
		set = append(set, Memberships.Group.Set(*v))
	}
	return gqlgraph.Validate("UpdateMembershipInput", &membershipType, set)
}
