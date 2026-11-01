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

const _ = veloxgql.GeneratedCodeIsVersion6

// Account is User under its GraphQL type name, so that gqlgen's autobind finds
// the Go type of the GraphQL type Account.
type Account = User

// IsNode marks User as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*User) IsNode() {}

// IDGQL is the global ID of the User, the GraphQL field id: the type Account
// and the key id, encoded.
func (x *User) IDGQL() string { return veloxgql.EncodeID("Account", x.ID) }

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
	q := db.Query[Group]()
	if fields, ok := gqlimpl.Selected(ctx, "Group"); ok {
		q = gqlimpl.Narrow(q, groupColumnsGQL(fields))
	}
	return gqlimpl.EdgeList(ctx, q, x, Users.Groups, "Account.memberships")
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
	c *gqlimpl.Connection[User]
}

// Edges are the edges of the page, in the connection's order. The first call of
// Edges or PageInfo runs the page query, and the other reads its page.
func (c *AccountConnection) Edges(ctx context.Context) ([]*AccountEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	edges := make([]*AccountEdge, len(p.Rows))
	for i, x := range p.Rows {
		edges[i] = &AccountEdge{Node: x, Cursor: p.Cursors[i]}
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
// left zero in the nodes, except the key, the foreign keys, the order columns
// and the tenant, so an application's resolver must not read one it did not ask
// for. It runs no query.
func AccountsConnectionOf(ctx context.Context, q velox.Query[User], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	if !q.Selects(Users.ID) {
		return nil, fmt.Errorf("veloxgql: AccountsConnectionOf: the base query does not select the key, which the cursors hold")
	}
	if fields, ok := gqlimpl.Selected(ctx, "Account"); ok {
		q = gqlimpl.Narrow(q, userColumnsGQL(fields))
	}
	if where != nil {
		p, err := where.Pred(ctx)
		if err != nil {
			return nil, err
		}
		q = q.Where(p)
	}
	c, err := gqlimpl.NewConnection(ctx, q, "Account", gqlimpl.Args{After: after, First: first, Before: before, Last: last}, nil, userKeyGQL())
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: c}, nil
}

// userKeyGQL is the order of User's key, which ends every order of a connection
// to Account.
func userKeyGQL() []gqlimpl.OrderTerm[User] {
	return []gqlimpl.OrderTerm[User]{
		gqlimpl.Term("id", veloxgql.OrderDirectionAsc, Users.ID.Asc(), Users.ID.Desc(),
			func(x *User) int64 { return x.ID },
			func(x *User, v int64) { x.ID = v }),
	}
}

// userColumnsGQL is the columns a selection of the Account fields fields reads:
// the key, every foreign key on the row, every order field's column, the tenant
// column, and the column of each field. It is nil, every column, when a field
// is not one velox-graphql wrote, such as an application's own.
func userColumnsGQL(fields []string) []velox.Column[User] {
	cols := []velox.Column[User]{Users.ID, Users.UserBestFriend, Users.OrgID}
	for _, f := range fields {
		switch f {
		case "id", "posts", "memberships", "likes":
		case "createdAt":
			cols = append(cols, Users.CreatedAt)
		case "updatedAt":
			cols = append(cols, Users.UpdatedAt)
		case "displayName":
			cols = append(cols, Users.Name)
		case "email":
			cols = append(cols, Users.Email)
		case "role":
			cols = append(cols, Users.Role)
		case "nickname":
			cols = append(cols, Users.Nickname)
		case "bio":
			cols = append(cols, Users.Bio)
		case "age":
			cols = append(cols, Users.Age)
		case "score":
			cols = append(cols, Users.Score)
		case "externalID":
			cols = append(cols, Users.ExternalID)
		case "avatar":
			cols = append(cols, Users.Avatar)
		case "quota":
			cols = append(cols, Users.Quota)
		case "active":
			cols = append(cols, Users.Active)
		case "settings":
			cols = append(cols, Users.Settings)
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
// before any query runs, on an ID of another type (veloxgql.ErrWrongIDType) or
// a malformed one (veloxgql.ErrBadID), on a nesting deeper than the
// MaxFilterDepth of veloxgql.LimitsFromContext(ctx)
// (veloxgql.ErrFilterTooDeep), on an and, an or, a has…With, an In or a NotIn
// of more than its MaxFilterWidth entries (veloxgql.ErrFilterTooWide), and on a
// where argument of more than its MaxFilterNodes inputs, predicates and values
// in all (veloxgql.ErrFilterTooLarge).
func (w *AccountWhereInput) Pred(ctx context.Context) (velox.Pred[User], error) {
	f := gqlimpl.NewFilter[User](ctx, "AccountWhereInput")
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID(f, "id", w.ID, "Account", Users.ID.EQ)
	gqlimpl.ID(f, "idNEQ", w.IDNEQ, "Account", Users.ID.NEQ)
	gqlimpl.IDList(f, "idIn", w.IDIn, "Account", Users.ID.In)
	gqlimpl.IDList(f, "idNotIn", w.IDNotIn, "Account", Users.ID.NotIn)
	gqlimpl.ID(f, "idGT", w.IDGT, "Account", Users.ID.GT)
	gqlimpl.ID(f, "idGTE", w.IDGTE, "Account", Users.ID.GTE)
	gqlimpl.ID(f, "idLT", w.IDLT, "Account", Users.ID.LT)
	gqlimpl.ID(f, "idLTE", w.IDLTE, "Account", Users.ID.LTE)
	gqlimpl.Value(f, "displayName", w.DisplayName, Users.Name.EQ)
	gqlimpl.Value(f, "displayNameNEQ", w.DisplayNameNEQ, Users.Name.NEQ)
	gqlimpl.List(f, "displayNameIn", w.DisplayNameIn, Users.Name.In)
	gqlimpl.List(f, "displayNameNotIn", w.DisplayNameNotIn, Users.Name.NotIn)
	gqlimpl.Value(f, "displayNameContains", w.DisplayNameContains, Users.Name.Contains)
	gqlimpl.Value(f, "displayNameHasPrefix", w.DisplayNameHasPrefix, Users.Name.HasPrefix)
	gqlimpl.Value(f, "displayNameHasSuffix", w.DisplayNameHasSuffix, Users.Name.HasSuffix)
	gqlimpl.Is(f, "hasPosts", w.HasPosts, Users.Posts.Has)
	gqlimpl.HasWith(f, "hasPostsWith", w.HasPostsWith, Users.Posts.HasWhere)
	return f.Pred()
}

// userFieldGQL is the GraphQL name of the mutation-input field that sets the
// User field whose Go name is goField, for the errors of its validators: "" for
// a field no input sets.
func userFieldGQL(goField string) string {
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

// CreateAccountInput is the GraphQL input CreateAccountInput, the input of
// createAccount. A field that is a pointer, or a list, is optional: nil means
// not given.
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
	var p0 any
	if in.CreatedAt != nil {
		p0 = *in.CreatedAt
	}
	var p1 any
	if in.UpdatedAt != nil {
		p1 = *in.UpdatedAt
	}
	var p4 any
	if in.Role != nil {
		p4 = *in.Role
	}
	var p5 any
	if in.Nickname != nil {
		p5 = *in.Nickname
	}
	var p6 any
	if in.Bio != nil {
		p6 = *in.Bio
	}
	var p7 any
	if in.Age != nil {
		p7 = *in.Age
	}
	var p9 any
	if in.Score != nil {
		p9 = *in.Score
	}
	var p10 any
	if in.ExternalID != nil {
		p10 = *in.ExternalID
	}
	var p11 any
	if in.Avatar != nil {
		p11 = *in.Avatar
	}
	var p13 any
	if in.Active != nil {
		p13 = *in.Active
	}
	var p14 any
	if in.Settings != nil {
		p14 = string(*in.Settings)
	}
	var p16 any
	if in.BestFriendID != nil {
		p16 = *in.BestFriendID
	}
	return fmt.Sprintf("CreateAccountInput(CreatedAt=%v, UpdatedAt=%v, DisplayName=%v, Email=%v, Role=%v, Nickname=%v, Bio=%v, Age=%v, Score=%v, ExternalID=%v, Avatar=%v, Quota=%v, Active=%v, Settings=%v, MembershipIDs=%v, BestFriendID=%v)", p0, p1, in.DisplayName, in.Email, p4, p5, p6, p7, p9, p10, p11, in.Quota, p13, p14, in.MembershipIDs, p16)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateAccountInput) GoString() string { return in.String() }

// CreateAccount creates an Account from input, with its edges, as the mutation
// createAccount does, and returns the row as written. It runs atomically on the
// DB veloxgql.DBFromContext reads from ctx, under the tenant scope and the
// policies. See "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each input field does, what
// the caller may link and change, and the errors.
func CreateAccount(ctx context.Context, input CreateAccountInput) (*User, error) {
	var (
		j14 map[string]any
		k15 []int64
		k16 int64
		err error
	)
	if input.Settings != nil {
		if j14, err = gqlimpl.FromJSON[map[string]any]("CreateAccountInput.settings", *input.Settings); err != nil {
			return nil, err
		}
	}
	if k15, err = gqlimpl.DecodeIDs[int64](ctx, "CreateAccountInput.membershipIDs", input.MembershipIDs, "Group"); err != nil {
		return nil, err
	}
	if input.BestFriendID != nil {
		if k16, err = gqlimpl.DecodeID[int64]("CreateAccountInput.bestFriendID", *input.BestFriendID, "Account"); err != nil {
			return nil, err
		}
	}
	var x User
	cols := []velox.Column[User]{Users.Name, Users.Email, Users.PasswordHash, Users.Quota}
	if input.CreatedAt != nil {
		x.CreatedAt = *input.CreatedAt
		if err := gqlimpl.NotZero("CreateAccountInput.createdAt", x.CreatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, Users.CreatedAt)
	}
	if input.UpdatedAt != nil {
		x.UpdatedAt = *input.UpdatedAt
		if err := gqlimpl.NotZero("CreateAccountInput.updatedAt", x.UpdatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, Users.UpdatedAt)
	}
	x.Name = input.DisplayName
	x.Email = input.Email
	if input.Role != nil {
		x.Role = *input.Role
		cols = append(cols, Users.Role)
	}
	if input.Nickname != nil {
		x.Nickname = input.Nickname
		cols = append(cols, Users.Nickname)
	}
	if input.Bio != nil {
		x.Bio = input.Bio
		cols = append(cols, Users.Bio)
	}
	if input.Age != nil {
		x.Age = input.Age
		cols = append(cols, Users.Age)
	}
	x.PasswordHash = input.PasswordHash
	if input.Score != nil {
		x.Score = *input.Score
		cols = append(cols, Users.Score)
	}
	if input.ExternalID != nil {
		x.ExternalID = *input.ExternalID
		if err := gqlimpl.NotZero("CreateAccountInput.externalID", x.ExternalID); err != nil {
			return nil, err
		}
		cols = append(cols, Users.ExternalID)
	}
	if input.Avatar != nil {
		x.Avatar = *input.Avatar
		cols = append(cols, Users.Avatar)
	}
	x.Quota = input.Quota
	if input.Active != nil {
		x.Active = *input.Active
		cols = append(cols, Users.Active)
	}
	if input.Settings != nil {
		x.Settings = j14
		cols = append(cols, Users.Settings)
	}
	if err := velox.Validate(&x, cols...); err != nil {
		return nil, gqlimpl.WriteError(err, "CreateAccountInput", "gen.User", userFieldGQL)
	}
	var out *User
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		if len(k15) > 0 {
			if err := gqlimpl.CheckExists(ctx, db.Query[Group]().Where(Groups.ID.In(k15...)), len(k15), "CreateAccountInput.membershipIDs", "Group"); err != nil {
				return err
			}
		}
		if input.BestFriendID != nil {
			if err := gqlimpl.CheckExists(ctx, db.Query[User]().Where(Users.ID.EQ(k16)), 1, "CreateAccountInput.bestFriendID", "Account"); err != nil {
				return err
			}
			x.UserBestFriend = &k16
		}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			return gqlimpl.WriteError(err, "CreateAccountInput", "gen.User", userFieldGQL)
		}
		if len(k15) > 0 {
			rows := make([]UserGroup, len(k15))
			for i, k := range k15 {
				rows[i] = UserGroup{UserID: x.ID, GroupID: k}
			}
			if err := db.InsertAll(rows).OnConflict(UserGroups.UserID, UserGroups.GroupID).DoNothing().Exec(ctx); err != nil {
				return err
			}
		}
		out = &x
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
