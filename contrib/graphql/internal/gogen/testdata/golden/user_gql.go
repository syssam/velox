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
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion13

// Account is User under its GraphQL type name, so that gqlgen's autobind finds
// the Go type of the GraphQL type Account.
type Account = User

// IsNode marks User as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*User) IsNode() {}

// GlobalID is the global ID of the User, the GraphQL field id: its type and
// key, opaque to the client.
func (x *User) GlobalID() string { return veloxgql.EncodeID("Account", x.ID) }

// SettingsGQL is the field settings as JSON, the GraphQL scalar it is: gqlgen
// binds the field to it.
func (x *User) SettingsGQL() (veloxgql.JSON, error) {
	b, err := json.Marshal(x.Settings)
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
		gqlimpl.WriteString(w, string(e))
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
		return gqlimpl.InputErrorf("UserRole must be ADMIN, EDITOR or VIEWER, not %v", v)
	}
	return nil
}

// Posts is the edge posts: a connection to the Post nodes it leads to. Its
// queries batch with the edge of every row read with x; see
// gqlimpl.EdgeConnection.
func (x *User) Posts(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy []*PostOrder, where *PostWhereInput,
) (*PostConnection, error) {
	c, err := PostsConnection(ctx, after, first, before, last, orderBy, where)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: gqlimpl.EdgeConnection(
		ctx, c.c, &userConnGQL, x, userEdgePosts, "Account.posts",
		[]any{after, first, before, last, orderBy, where},
	)}, nil
}

// Groups is the edge groups: the Group nodes it leads to, in key order. It is
// read in one query with the edge of every row read with x, up to MaxList; see
// gqlimpl.EdgeList.
func (x *User) Groups(ctx context.Context) ([]*Group, error) {
	v, err := gqlimpl.EdgeList(ctx, &groupConnGQL, &userConnGQL, x, userEdgeGroups, "Account.memberships", 0)
	n, _ := v.([]*Group)
	return n, err
}

// Likes is the edge likes: a connection to the Like nodes it leads to. Its
// queries batch with the edge of every row read with x; see
// gqlimpl.EdgeConnection.
func (x *User) Likes(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
) (*LikeConnection, error) {
	c, err := LikesConnection(ctx, after, first, before, last)
	if err != nil {
		return nil, err
	}
	return &LikeConnection{c: gqlimpl.EdgeConnection(
		ctx, c.c, &userConnGQL, x, userEdgeLikes, "Account.likes",
		[]any{after, first, before, last},
	)}, nil
}

// AccountConnection is the GraphQL type AccountConnection, a connection to a
// list of Account. Its fields run their queries when gqlgen asks; see
// gqlimpl.Connection.
type AccountConnection struct {
	c *gqlimpl.Connection
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

// AccountEdge is the GraphQL type AccountEdge, an edge in a connection to a
// list of Account.
type AccountEdge struct {
	// Node is the Account at the end of the edge.
	Node *User
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// AccountsConnection is the connection to every Account node, on the DB in ctx,
// for a Query field's resolver. It is AccountsConnectionOf of the whole table.
func AccountsConnection(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *AccountWhereInput,
) (*AccountConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return AccountsConnectionOf(ctx, db.Query[User](), after, first, before, last, where)
}

// AccountsConnectionOf is the connection to the Account nodes q returns,
// filtered by where, ordered by the key, and paged by the other arguments. q
// must not be ordered and must select the key and the order columns; see
// gqlimpl.ConnectionBase and gqlimpl.NewConnection.
func AccountsConnectionOf(
	ctx context.Context, q velox.Query[User],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	where *AccountWhereInput,
) (*AccountConnection, error) {
	b, err := gqlimpl.ConnectionBase(ctx, velox.Untyped(q), &userConnGQL, nil)
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
	c, err := gqlimpl.NewConnection(
		ctx, b, &userConnGQL,
		gqlimpl.Args{After: after, First: first, Before: before, Last: last},
		nil,
	)
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: c}, nil
}

// userConnGQL is what the runtime knows of the GraphQL type Account: its table,
// the key and the order columns of its connections, the columns its selections
// read and the fields of its inputs.
var userConnGQL = gqlimpl.Conn{
	Type:  "Account",
	Table: velox.TableOf[User],
	Key: []gqlimpl.Col{
		{Field: "id", Col: userColID},
	},
	Columns:     userColumnsGQL,
	CreateField: userInputGQL,
}

// userColumnsGQL returns the columns a selection of the given Account fields
// reads. It returns nil, every column, for a field that is not velox-graphql's;
// see gqlimpl.Conn.
func userColumnsGQL(fields []string) []int {
	cols := []int{userColID, userColUserBestFriend}
	for _, f := range fields {
		switch f {
		case "id", "posts", "memberships", "likes":
		case "createdAt":
			cols = append(cols, userColCreatedAt)
		case "updatedAt":
			cols = append(cols, userColUpdatedAt)
		case "displayName":
			cols = append(cols, userColName)
		case "email":
			cols = append(cols, userColEmail)
		case "role":
			cols = append(cols, userColRole)
		case "nickname":
			cols = append(cols, userColNickname)
		case "bio":
			cols = append(cols, userColBio)
		case "age":
			cols = append(cols, userColAge)
		case "score":
			cols = append(cols, userColScore)
		case "externalID":
			cols = append(cols, userColExternalID)
		case "avatar":
			cols = append(cols, userColAvatar)
		case "quota":
			cols = append(cols, userColQuota)
		case "active":
			cols = append(cols, userColActive)
		case "settings":
			cols = append(cols, userColSettings)
		default:
			return nil
		}
	}
	return cols
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
	ID      *string
	IDNEQ   *string
	IDIn    []string
	IDNotIn []string
	IDGT    *string
	IDGTE   *string
	IDLT    *string
	IDLTE   *string

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
// w. Its errors are gqlimpl.NewFilter's.
func (w *AccountWhereInput) Pred(ctx context.Context) (velox.Pred[User], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[User](p), err
}

// PredicateGQL is Pred, untyped, for velox-graphql's runtime.
func (w *AccountWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, &userConnGQL)
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.IDField[int64](f, "id", userColID, gqlimpl.Ops[string]{
		EQ:    w.ID,
		NEQ:   w.IDNEQ,
		In:    w.IDIn,
		NotIn: w.IDNotIn,
		GT:    w.IDGT,
		GTE:   w.IDGTE,
		LT:    w.IDLT,
		LTE:   w.IDLTE,
	})
	gqlimpl.Field(f, "displayName", userColName, gqlimpl.Ops[string]{
		EQ:        w.DisplayName,
		NEQ:       w.DisplayNameNEQ,
		In:        w.DisplayNameIn,
		NotIn:     w.DisplayNameNotIn,
		Contains:  w.DisplayNameContains,
		HasPrefix: w.DisplayNameHasPrefix,
		HasSuffix: w.DisplayNameHasSuffix,
	})
	gqlimpl.Has(f, "hasPosts", w.HasPosts, userEdgePosts, velox.TableOf[Post]())
	gqlimpl.HasWith(f, "hasPostsWith", w.HasPostsWith, userEdgePosts, velox.TableOf[Post]())
	return f.Pred()
}

// userInputGQL is the CreateAccountInput field that sets the User field
// goField, or "". A validator's error names it.
func userInputGQL(goField string) string {
	switch goField {
	case "CreatedAt":
		return "createdAt"
	case "UpdatedAt":
		return "updatedAt"
	case "Name":
		return "displayName"
	case "Email":
		return "email"
	case "Role":
		return "role"
	case "Nickname":
		return "nickname"
	case "Bio":
		return "bio"
	case "Age":
		return "age"
	case "PasswordHash":
		return "passwordHash"
	case "Score":
		return "score"
	case "ExternalID":
		return "externalID"
	case "Avatar":
		return "avatar"
	case "Quota":
		return "quota"
	case "Active":
		return "active"
	case "Settings":
		return "settings"
	case "UserBestFriend":
		return "bestFriendID"
	}
	return ""
}

// userGroupsEdgeGQL is the many-to-many edge groups of User, through UserGroup.
func userGroupsEdgeGQL() gqlimpl.M2M {
	return gqlimpl.M2M{
		Owner:      velox.TableOf[User](),
		Target:     velox.TableOf[Group](),
		Join:       velox.TableOf[UserGroup](),
		OwnerKey:   userColID,
		TargetKey:  groupColID,
		JoinOwner:  userGroupColUserID,
		JoinTarget: userGroupColGroupID,
	}
}

// CreateAccountInput is the GraphQL input CreateAccountInput, which Create
// writes. A nil pointer or list is a field not given.
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
	MembershipIDs []string
	// BestFriendID is the ID of the Account the edge best_friend leads to.
	BestFriendID *string
}

// String returns the input's fields except its sensitive ones.
func (in CreateAccountInput) String() string {
	return fmt.Sprintf("CreateAccountInput("+
		"CreatedAt=%v, UpdatedAt=%v, DisplayName=%v, Email=%v, "+
		"Role=%v, Nickname=%v, Bio=%v, Age=%v, Score=%v, "+
		"ExternalID=%v, Avatar=%v, Quota=%v, Active=%v, Settings=%v, "+
		"MembershipIDs=%v, BestFriendID=%v)",
		gqlimpl.Show(in.CreatedAt), gqlimpl.Show(in.UpdatedAt),
		in.DisplayName, in.Email, gqlimpl.Show(in.Role),
		gqlimpl.Show(in.Nickname), gqlimpl.Show(in.Bio),
		gqlimpl.Show(in.Age), gqlimpl.Show(in.Score),
		gqlimpl.Show(in.ExternalID), gqlimpl.Show(in.Avatar),
		in.Quota, gqlimpl.Show(in.Active),
		gqlimpl.ShowJSON(in.Settings), in.MembershipIDs,
		gqlimpl.Show(in.BestFriendID),
	)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateAccountInput) GoString() string { return in.String() }

// Create inserts x, an Account, with the input's fields and edges set; a nil x
// is a new one. It runs atomically on the DB in ctx, under the policies.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateAccountInput) Create(ctx context.Context, x *User) (*User, error) {
	if x == nil {
		x = new(User)
	}
	c := gqlimpl.NewCreate(ctx, &userConnGQL, x, userColName, userColEmail, userColPasswordHash, userColQuota)
	if in.CreatedAt != nil {
		x.CreatedAt = *in.CreatedAt
		gqlimpl.NotZero(c, "createdAt", x.CreatedAt)
		c.Given(userColCreatedAt)
	}
	if in.UpdatedAt != nil {
		x.UpdatedAt = *in.UpdatedAt
		gqlimpl.NotZero(c, "updatedAt", x.UpdatedAt)
		c.Given(userColUpdatedAt)
	}
	x.Name = in.DisplayName
	x.Email = in.Email
	if in.Role != nil {
		x.Role = *in.Role
		c.Given(userColRole)
	}
	if in.Nickname != nil {
		x.Nickname = in.Nickname
		c.Given(userColNickname)
	}
	if in.Bio != nil {
		x.Bio = in.Bio
		c.Given(userColBio)
	}
	if in.Age != nil {
		x.Age = in.Age
		c.Given(userColAge)
	}
	x.PasswordHash = in.PasswordHash
	if in.Score != nil {
		x.Score = *in.Score
		c.Given(userColScore)
	}
	if in.ExternalID != nil {
		x.ExternalID = *in.ExternalID
		gqlimpl.NotZero(c, "externalID", x.ExternalID)
		c.Given(userColExternalID)
	}
	if in.Avatar != nil {
		x.Avatar = *in.Avatar
		c.Given(userColAvatar)
	}
	x.Quota = in.Quota
	if in.Active != nil {
		x.Active = *in.Active
		c.Given(userColActive)
	}
	if in.Settings != nil {
		settingsJSON := gqlimpl.DecodeJSON[map[string]any](c, "settings", *in.Settings)
		x.Settings = settingsJSON
		c.Given(userColSettings)
	}
	membershipIDsKeys := gqlimpl.Keys[int64](c, "membershipIDs", in.MembershipIDs, "Group")
	gqlimpl.Join(c, "membershipIDs", "Group", userGroupsEdgeGQL(), membershipIDsKeys)
	if in.BestFriendID != nil {
		bestFriendIDKey := gqlimpl.Key[int64](c, "bestFriendID", *in.BestFriendID, "Account")
		gqlimpl.Exists(c, "bestFriendID", "Account", velox.TableOf[User](), userColID, []int64{bestFriendIDKey})
		x.UserBestFriend = &bestFriendIDKey
	}
	if err := c.Insert(); err != nil {
		return nil, err
	}
	return x, nil
}
