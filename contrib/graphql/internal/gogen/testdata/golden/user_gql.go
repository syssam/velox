package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion28

// Account is User under its GraphQL type name, so that gqlgen's autobind finds
// the Go type of the GraphQL type Account.
type Account = User

// SettingsGQL is the field settings as JSON, the GraphQL scalar it is: gqlgen
// binds the field to it.
func (u *User) SettingsGQL() (veloxgql.JSON, error) {
	b, err := json.Marshal(u.Settings)
	return veloxgql.JSON(b), err
}

// MarshalGQL writes e as its value of the GraphQL enum UserRole.
func (e UserRole) MarshalGQL(w io.Writer) {
	switch e {
	case UserRoleAdmin:
		_, _ = io.WriteString(w, `"ADMIN"`)
	case UserRoleEditor:
		_, _ = io.WriteString(w, `"EDITOR"`)
	case UserRoleViewer:
		_, _ = io.WriteString(w, `"VIEWER"`)
	default: // not a value of the enum
		gqlgraph.WriteString(w, string(e))
	}
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum UserRole.
func (e *UserRole) UnmarshalGQL(v any) error {
	switch v {
	case "ADMIN":
		*e = UserRoleAdmin
	case "EDITOR":
		*e = UserRoleEditor
	case "VIEWER":
		*e = UserRoleViewer
	default:
		return gqlgraph.InputErrorf("UserRole must be ADMIN, EDITOR or VIEWER, not %v", v)
	}
	return nil
}

// Posts is the edge posts: a connection to the Post nodes it leads to. Its
// queries batch with the same edge of every row on u's page.
func (u *User) Posts(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy []*PostOrder, where *PostWhereInput,
) (*PostConnection, error) {
	return newPostConnection(ctx, userType.Edge(u, userEdgePosts), after, first, before, last, orderBy, where)
}

// Groups is the edge groups: the Group nodes it leads to, in key order. It is
// read in one query with the same edge of every row on u's page, and fails on
// more than MaxList nodes.
func (u *User) Groups(ctx context.Context) ([]*Group, error) {
	v, err := gqlgraph.EdgeList(ctx, &groupType, userType.Edge(u, userEdgeGroups), 0)
	n, _ := v.([]*Group)
	return n, err
}

// Likes is the edge likes: a connection to the Like nodes it leads to. Its
// queries batch with the same edge of every row on u's page.
func (u *User) Likes(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*LikeConnection, error) {
	return newLikeConnection(ctx, userType.Edge(u, userEdgeLikes), after, first, before, last)
}

// AccountConnection is the GraphQL type AccountConnection. Its fields run their
// queries when gqlgen resolves them.
type AccountConnection struct {
	c *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *AccountConnection) Edges(ctx context.Context) ([]*AccountEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]AccountEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*AccountEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = AccountEdge{Node: p.Node(i).(*User), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *AccountConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Account nodes in the whole connection, not only
// on this page.
func (c *AccountConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// AccountEdge is the GraphQL type AccountEdge.
type AccountEdge struct {
	Node   *User
	Cursor veloxgql.Cursor
}

// AccountPaginateOption is an option of a query's Paginate to Account nodes:
// WithAccountFilter. Of two options that set the same thing, the later wins.
type AccountPaginateOption struct {
	where *AccountWhereInput
}

// WithAccountFilter filters the connection by where.
func WithAccountFilter(where *AccountWhereInput) AccountPaginateOption {
	return AccountPaginateOption{where: where}
}

// PaginateQuery is the connection to the Account nodes q returns, filtered by
// where, ordered by the key, and paged by the other arguments: velox.Query's
// Paginate. q must not be ordered, and must select the key.
func (*User) PaginateQuery(
	ctx context.Context, q velox.Query[User],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []AccountPaginateOption,
) (*AccountConnection, error) {
	var o AccountPaginateOption
	for _, x := range opts {
		if x.where != nil {
			o.where = x.where
		}
	}
	return newAccountConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.where)
}

// newAccountConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newAccountConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *AccountWhereInput,
) (*AccountConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last, Where: gqlgraph.WhereOf(where, (*AccountWhereInput).Predicate)}
	c, err := gqlgraph.Paginate(ctx, from, &userType, args)
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: c}, nil
}

// userType describes the GraphQL type Account to gqlgraph.
var userType = gqlgraph.Type{
	Name:       "Account",
	Table:      untyped.TableOf[User],
	Key:        []gqlgraph.Column{{Name: "id", Index: userColID}},
	AlwaysRead: []int{userColID, userColUserBestFriend},
	Fields: []string{
		userColID:         "id",
		userColCreatedAt:  "createdAt",
		userColUpdatedAt:  "updatedAt",
		userColName:       "displayName",
		userColEmail:      "email",
		userColRole:       "role",
		userColNickname:   "nickname",
		userColBio:        "bio",
		userColAge:        "age",
		userColScore:      "score",
		userColExternalID: "externalID",
		userColAvatar:     "avatar",
		userColQuota:      "quota",
		userColActive:     "active",
		userColSettings:   "settings",
	},
	Edges: []string{
		userEdgePosts:  "posts",
		userEdgeGroups: "memberships",
		userEdgeLikes:  "likes",
	},
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality | gqlgraph.OpsOrder, Col: userColID},
		{Name: "displayName", Ops: gqlgraph.OpsEquality | gqlgraph.OpContains | gqlgraph.OpHasPrefix | gqlgraph.OpHasSuffix, Col: userColName},
		{Name: "hasPosts", Kind: gqlgraph.WhereHas, Edge: userEdgePosts, Target: untyped.TableOf[Post]},
		{Name: "hasPostsWith", Kind: gqlgraph.WhereHasWith, Edge: userEdgePosts, Target: untyped.TableOf[Post]},
	},
	Inputs: []string{
		userColCreatedAt:      "createdAt",
		userColUpdatedAt:      "updatedAt",
		userColName:           "displayName",
		userColEmail:          "email",
		userColRole:           "role",
		userColNickname:       "nickname",
		userColBio:            "bio",
		userColAge:            "age",
		userColPasswordHash:   "passwordHash",
		userColScore:          "score",
		userColExternalID:     "externalID",
		userColAvatar:         "avatar",
		userColQuota:          "quota",
		userColActive:         "active",
		userColSettings:       "settings",
		userColUserBestFriend: "bestFriendID",
	},
}

// AccountWhereInput is the GraphQL input AccountWhereInput, a filter of Account
// nodes: a node matches when every predicate given holds.
type AccountWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *AccountWhereInput
	And []*AccountWhereInput
	Or  []*AccountWhereInput

	// Predicates on the ID.
	ID      *int64
	IDNEQ   *int64
	IDIn    []int64
	IDNotIn []int64
	IDGT    *int64
	IDGTE   *int64
	IDLT    *int64
	IDLTE   *int64

	// Predicates on the field name (displayName).
	DisplayName          *string
	DisplayNameNEQ       *string
	DisplayNameIn        []string
	DisplayNameNotIn     []string
	DisplayNameContains  *string
	DisplayNameHasPrefix *string
	DisplayNameHasSuffix *string

	// The edge posts: whether it has a node, or one that matches.
	HasPosts     *bool
	HasPostsWith []*PostWhereInput
}

// Pred is the condition w stands for on Account nodes, true for a nil or empty
// w. It fails on a filter over the limits of veloxgql.LimitsFromContext(ctx).
func (w *AccountWhereInput) Pred(ctx context.Context) (velox.Pred[User], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[User](p), err
}

// Predicate is Pred, untyped.
func (w *AccountWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &userType, w)
}

// CreateAccountInput is the GraphQL input CreateAccountInput. A nil pointer or
// list is a field not given, and so is a null.
type CreateAccountInput struct {
	CreatedAt *time.Time
	UpdatedAt *time.Time
	// The display name.
	DisplayName  string
	Email        string
	Role         *UserRole
	Nickname     *string
	Bio          *string
	Age          *int
	PasswordHash string
	Score        *float64
	ExternalID   *uuid.UUID
	Avatar       *[]byte
	Quota        uint64
	Active       *bool
	Settings     *veloxgql.JSON
	// MembershipIDs are the IDs of the Group nodes the edge groups leads to.
	MembershipIDs []int64
	// BestFriendID is the ID of the Account the edge best_friend leads to.
	BestFriendID *int64
}

// String returns the input's fields except its sensitive ones.
func (in CreateAccountInput) String() string {
	return fmt.Sprintf("CreateAccountInput("+
		"CreatedAt=%v, UpdatedAt=%v, DisplayName=%v, Email=%v, "+
		"Role=%v, Nickname=%v, Bio=%v, Age=%v, Score=%v, "+
		"ExternalID=%v, Avatar=%v, Quota=%v, Active=%v, Settings=%v, "+
		"MembershipIDs=%v, BestFriendID=%v)",
		gqlgraph.Show(in.CreatedAt), gqlgraph.Show(in.UpdatedAt),
		in.DisplayName, in.Email, gqlgraph.Show(in.Role),
		gqlgraph.Show(in.Nickname), gqlgraph.Show(in.Bio),
		gqlgraph.Show(in.Age), gqlgraph.Show(in.Score),
		gqlgraph.Show(in.ExternalID), gqlgraph.Show(in.Avatar),
		in.Quota, gqlgraph.Show(in.Active),
		gqlgraph.ShowJSON(in.Settings), in.MembershipIDs,
		gqlgraph.Show(in.BestFriendID),
	)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateAccountInput) GoString() string { return in.String() }

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateAccountInput) Setters() ([]velox.Setter[User], error) {
	var set []velox.Setter[User]
	if v := in.CreatedAt; v != nil {
		set = append(set, Users.CreatedAt.Set(*v))
	}
	if v := in.UpdatedAt; v != nil {
		set = append(set, Users.UpdatedAt.Set(*v))
	}
	set = append(set, Users.Name.Set(in.DisplayName))
	set = append(set, Users.Email.Set(in.Email))
	if v := in.Role; v != nil {
		set = append(set, Users.Role.Set(*v))
	}
	if v := in.Nickname; v != nil {
		set = append(set, Users.Nickname.Set(*v))
	}
	if v := in.Bio; v != nil {
		set = append(set, Users.Bio.Set(*v))
	}
	if v := in.Age; v != nil {
		set = append(set, Users.Age.Set(*v))
	}
	set = append(set, Users.PasswordHash.Set(in.PasswordHash))
	if v := in.Score; v != nil {
		set = append(set, Users.Score.Set(*v))
	}
	if v := in.ExternalID; v != nil {
		set = append(set, Users.ExternalID.Set(*v))
	}
	if v := in.Avatar; v != nil {
		set = append(set, Users.Avatar.Set(*v))
	}
	set = append(set, Users.Quota.Set(in.Quota))
	if v := in.Active; v != nil {
		set = append(set, Users.Active.Set(*v))
	}
	if v := in.Settings; v != nil {
		settings, err := gqlgraph.FromJSON[map[string]any]("CreateAccountInput.settings", *v)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Settings.Set(settings))
	}
	if len(in.MembershipIDs) > 0 {
		membershipIDs, err := gqlgraph.IDs("CreateAccountInput.membershipIDs", in.MembershipIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Groups.Add(membershipIDs...))
	}
	if v := in.BestFriendID; v != nil {
		set = append(set, Users.BestFriend.Set(*v))
	}
	return gqlgraph.Validate("CreateAccountInput", &userType, set)
}
