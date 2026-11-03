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

const _ = veloxgql.GeneratedCodeIsVersion10

// Account is User under its GraphQL type name, so that gqlgen's autobind finds
// the Go type of the GraphQL type Account.
type Account = User

// IsNode marks User as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*User) IsNode() {}

// IDGQL is the global ID of the User, the GraphQL field id: the type Account
// and the key id, encoded.
func (x *User) IDGQL() string { return veloxgql.EncodeID("Account", x.ID) }

// userNodeGQL is the Node type Account for Node and Nodes.
func userNodeGQL() gqlimpl.NodeKind {
	return gqlimpl.NewNode("Account", velox.TableOf[User](), userColID,
		func(id string) (any, error) {
			k, err := veloxgql.DecodeID[int64](id, "Account")
			return k, err
		},
		userColumnsGQL)
}

// userKeyGQL is the key of x, an Account.
func userKeyGQL(x *User) int64 { return x.ID }

// SettingsGQL is the field settings as JSON, the GraphQL scalar it is: gqlgen
// binds the field to it.
func (x *User) SettingsGQL() (veloxgql.JSON, error) {
	b, err := json.Marshal(x.Settings)
	return veloxgql.JSON(b), err
}

// MarshalGQL writes e as its value of the GraphQL enum UserRole.
func (e UserRole) MarshalGQL(w io.Writer) {
	var s string
	switch e {
	case "admin":
		s = "ADMIN"
	case "editor":
		s = "EDITOR"
	case "viewer":
		s = "VIEWER"
	default:
		s = string(e) // not a value of the enum
	}
	b, _ := json.Marshal(s) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum UserRole.
func (e *UserRole) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	switch s {
	case "ADMIN":
		*e = "admin"
	case "EDITOR":
		*e = "editor"
	case "VIEWER":
		*e = "viewer"
	default:
		return gqlimpl.InputErrorf("UserRole must be ADMIN, EDITOR or VIEWER")
	}
	return nil
}

// PostsGQL is the edge posts: a connection to the Post nodes it leads to,
// paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *User) PostsGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	c, err := PostsConnectionOf(ctx, db.Query[Post](), after, first, before, last, orderBy, where)
	if err != nil {
		return nil, err
	}
	return &PostConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, Users.Posts, "Account.posts", []any{after, first, before, last, orderBy, where})}, nil
}

// GroupsGQL is the edge groups: the Group nodes it leads to, in key order. It
// is read with the edge of every row read with x, in one query, which reads at
// most the MaxList of veloxgql.LimitsFromContext(ctx) rows for each, whatever
// the DB's WithMaxRows: more fail x's with velox.ErrTooManyRows, in an error
// that names the edge.
func (x *User) GroupsGQL(ctx context.Context) ([]*Group, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	q := db.QueryTable(velox.TableOf[Group]())
	if fields, ok := gqlimpl.Selected(ctx, "Group"); ok {
		q = gqlimpl.Narrow(q, groupColumnsGQL(fields))
	}
	return gqlimpl.EdgeList(ctx, q, x, Users.Groups, "Account.memberships", 0)
}

// LikesGQL is the edge likes: a connection to the Like nodes it leads to,
// paged, ordered and filtered by the arguments. Its queries run when gqlgen
// asks for the connection's fields, each with those of every row read with x,
// in one query.
func (x *User) LikesGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	c, err := LikesConnectionOf(ctx, db.Query[Like](), after, first, before, last)
	if err != nil {
		return nil, err
	}
	return &LikeConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, Users.Likes, "Account.likes", []any{after, first, before, last})}, nil
}

// AccountConnection is the GraphQL type AccountConnection, a connection to a
// list of Account. It holds the query of the whole list and the page asked for;
// gqlgen calls its methods for the fields a query selects, and each runs its
// query only then: edges and pageInfo share one page query, and totalCount runs
// a COUNT.
type AccountConnection struct {
	c *gqlimpl.Connection
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *AccountConnection) Edges(ctx context.Context) ([]*AccountEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]AccountEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*AccountEdge, len(p.Rows))
	for i, x := range p.Rows {
		slab[i] = AccountEdge{Node: (*User)(x), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page; see Edges.
func (c *AccountConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Account nodes in the whole connection, not only
// on this page. It counts the rows when gqlgen asks for it, which is only when
// the field totalCount is selected.
func (c *AccountConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// AccountEdge is the GraphQL type AccountEdge, an edge in a connection to a
// list of Account.
type AccountEdge struct {
	// Node is the Account at the end of the edge.
	Node *User
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// AccountsConnection is the connection to every Account node, through the DB
// veloxgql.DBFromContext reads from ctx, paged and ordered by the arguments of
// a connection to Account: a Query field's resolver returns it. It fails before
// any query runs on arguments gqlimpl.NewConnection refuses.
func AccountsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return AccountsConnectionOf(ctx, db.Query[User](), after, first, before, last, where)
}

// AccountsConnectionOf is the connection to the Account nodes q returns,
// filtered by where, ordered by the key, and paged by the other arguments. q
// keeps its filters and With loads, and the scope the application gave it; its
// Offset is cleared and its Limit replaced, since the page is the connection's.
// q must have no OrderBy: the connection orders it, and a q with one is refused
// with an internal error, since its order would come first and break the
// cursors. q must also select the key and the columns orderBy names, which the
// cursors hold: a q whose Columns or Omit drops one is refused with an internal
// error. In a gqlgen field, the page reads only the columns the selection needs
// (userColumnsGQL), of those q selects; a column the selection does not need is
// left zero in the nodes, except the key, the foreign keys and the order
// columns, so an application's resolver must not read one it did not ask for.
// It runs no query.
func AccountsConnectionOf(ctx context.Context, q velox.Query[User], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
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
	c, err := gqlimpl.NewConnection(ctx, b, &userConnGQL, gqlimpl.Args{After: after, First: first, Before: before, Last: last}, nil)
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: c}, nil
}

// userConnGQL is what the connections to Account are made of: the key, which
// ends every order, and the order fields' columns.
var userConnGQL = gqlimpl.Conn{
	Type: "Account",
	Key: []gqlimpl.Col{
		{Field: "id", Col: userColID},
	},
	Columns: userColumnsGQL,
}

// userColumnsGQL is the columns a selection of the Account fields fields reads:
// the key, every foreign key on the row, every order field's column, and the
// column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
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

// Pred is the condition w stands for, on Account nodes: every predicate w sets
// holds, and a w that sets none, or a nil w, holds for every node. It fails,
// before any query runs, on an ID of another type or a malformed one, and on a
// filter over the limits of veloxgql.LimitsFromContext(ctx): the errors are
// gqlimpl.NewFilter's.
func (w *AccountWhereInput) Pred(ctx context.Context) (velox.Pred[User], error) {
	p, err := w.PredicateGQL(ctx)
	return velox.Pred[User](p), err
}

// PredicateGQL is Pred, untyped, for velox-graphql's runtime.
func (w *AccountWhereInput) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, "AccountWhereInput", velox.TableOf[User]())
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID[int64](f, "id", w.ID, "Account", userColID, velox.PredEQ)
	gqlimpl.ID[int64](f, "idNEQ", w.IDNEQ, "Account", userColID, velox.PredNEQ)
	gqlimpl.IDList[int64](f, "idIn", w.IDIn, "Account", userColID, velox.PredIn)
	gqlimpl.IDList[int64](f, "idNotIn", w.IDNotIn, "Account", userColID, velox.PredNotIn)
	gqlimpl.ID[int64](f, "idGT", w.IDGT, "Account", userColID, velox.PredGT)
	gqlimpl.ID[int64](f, "idGTE", w.IDGTE, "Account", userColID, velox.PredGTE)
	gqlimpl.ID[int64](f, "idLT", w.IDLT, "Account", userColID, velox.PredLT)
	gqlimpl.ID[int64](f, "idLTE", w.IDLTE, "Account", userColID, velox.PredLTE)
	gqlimpl.Value(f, "displayName", w.DisplayName, userColName, velox.PredEQ)
	gqlimpl.Value(f, "displayNameNEQ", w.DisplayNameNEQ, userColName, velox.PredNEQ)
	gqlimpl.List(f, "displayNameIn", w.DisplayNameIn, userColName, velox.PredIn)
	gqlimpl.List(f, "displayNameNotIn", w.DisplayNameNotIn, userColName, velox.PredNotIn)
	gqlimpl.Value(f, "displayNameContains", w.DisplayNameContains, userColName, velox.PredContains)
	gqlimpl.Value(f, "displayNameHasPrefix", w.DisplayNameHasPrefix, userColName, velox.PredHasPrefix)
	gqlimpl.Value(f, "displayNameHasSuffix", w.DisplayNameHasSuffix, userColName, velox.PredHasSuffix)
	gqlimpl.Has(f, "hasPosts", w.HasPosts, userEdgePosts, velox.TableOf[Post]())
	gqlimpl.HasWith(f, "hasPostsWith", w.HasPostsWith, userEdgePosts, velox.TableOf[Post]())
	return f.Pred()
}

// userInputGQL is the GraphQL name of the CreateAccountInput field that sets
// the User field whose Go name is goField, for the errors of its validators: ""
// for a field it does not set.
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

// userGroupsEdgeGQL is the many-to-many edge groups of User, through UserGroup,
// as its inputs write it.
func userGroupsEdgeGQL() gqlimpl.M2M[User, Group, UserGroup, int64, int64] {
	return gqlimpl.NewM2M(userKeyGQL, Groups.ID, groupKeyGQL, UserGroups.UserID, UserGroups.GroupID,
		func(j *UserGroup) int64 { return j.GroupID },
		func(own, k int64) UserGroup { return UserGroup{UserID: own, GroupID: k} })
}

// CreateAccountInput is the GraphQL input CreateAccountInput; its method Create
// writes it. A field that is a pointer, or a list, is optional: nil means not
// given.
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
	return fmt.Sprintf("CreateAccountInput(CreatedAt=%v, UpdatedAt=%v, DisplayName=%v, Email=%v, Role=%v, Nickname=%v, Bio=%v, Age=%v, Score=%v, ExternalID=%v, Avatar=%v, Quota=%v, Active=%v, Settings=%v, MembershipIDs=%v, BestFriendID=%v)", gqlimpl.Show(in.CreatedAt), gqlimpl.Show(in.UpdatedAt), in.DisplayName, in.Email, gqlimpl.Show(in.Role), gqlimpl.Show(in.Nickname), gqlimpl.Show(in.Bio), gqlimpl.Show(in.Age), gqlimpl.Show(in.Score), gqlimpl.Show(in.ExternalID), gqlimpl.Show(in.Avatar), in.Quota, gqlimpl.Show(in.Active), gqlimpl.ShowJSON(in.Settings), in.MembershipIDs, gqlimpl.Show(in.BestFriendID))
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateAccountInput) GoString() string { return in.String() }

// Create inserts x, an Account, with the input's fields and edges set; a nil x
// is a new one. The input's fields overwrite x's: to own a field, leave it out
// with graphql.Skip(graphql.SkipMutationCreateInput) or set it in a velox.Hook.
// x's other fields, foreign keys included, are trusted, not checked. It runs
// atomically on the DB in ctx, under the policies, and changes x even when it
// fails. CreateAccountInput leaves out CreatedBy, OrgID, which an insert
// requires: set them on x, or in a velox.Hook, or they are stored as the zero
// value.
//
// See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func (in *CreateAccountInput) Create(ctx context.Context, x *User) (*User, error) {
	c, x := gqlimpl.NewCreate(ctx, "CreateAccountInput", "Account", "gen.User", x, userInputGQL, Users.Name, Users.Email, Users.PasswordHash, Users.Quota)
	if in.CreatedAt != nil {
		x.CreatedAt = *in.CreatedAt
		gqlimpl.NotZero(c, "createdAt", x.CreatedAt)
		c.Given(Users.CreatedAt)
	}
	if in.UpdatedAt != nil {
		x.UpdatedAt = *in.UpdatedAt
		gqlimpl.NotZero(c, "updatedAt", x.UpdatedAt)
		c.Given(Users.UpdatedAt)
	}
	x.Name = in.DisplayName
	x.Email = in.Email
	if in.Role != nil {
		x.Role = *in.Role
		c.Given(Users.Role)
	}
	if in.Nickname != nil {
		x.Nickname = in.Nickname
		c.Given(Users.Nickname)
	}
	if in.Bio != nil {
		x.Bio = in.Bio
		c.Given(Users.Bio)
	}
	if in.Age != nil {
		x.Age = in.Age
		c.Given(Users.Age)
	}
	x.PasswordHash = in.PasswordHash
	if in.Score != nil {
		x.Score = *in.Score
		c.Given(Users.Score)
	}
	if in.ExternalID != nil {
		x.ExternalID = *in.ExternalID
		gqlimpl.NotZero(c, "externalID", x.ExternalID)
		c.Given(Users.ExternalID)
	}
	if in.Avatar != nil {
		x.Avatar = *in.Avatar
		c.Given(Users.Avatar)
	}
	x.Quota = in.Quota
	if in.Active != nil {
		x.Active = *in.Active
		c.Given(Users.Active)
	}
	if in.Settings != nil {
		settingsJSON := gqlimpl.DecodeJSON[map[string]any](c, "settings", *in.Settings)
		x.Settings = settingsJSON
		c.Given(Users.Settings)
	}
	membershipIDsKeys := gqlimpl.Keys[int64](c, "membershipIDs", in.MembershipIDs, "Group")
	gqlimpl.Join(c, "membershipIDs", "Group", userGroupsEdgeGQL(), membershipIDsKeys...)
	if in.BestFriendID != nil {
		bestFriendIDKey := gqlimpl.Key[int64](c, "bestFriendID", *in.BestFriendID, "Account")
		gqlimpl.Exists(c, "bestFriendID", "Account", 1, Users.ID.EQ(bestFriendIDKey))
		x.UserBestFriend = &bestFriendIDKey
	}
	return c.Insert()
}
