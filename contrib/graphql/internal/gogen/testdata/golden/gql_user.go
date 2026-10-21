package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion1

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
// paged, ordered and filtered by the arguments. With no argument it is over
// x.Posts when a With loaded it, in the order loaded; else its queries are on
// Post, filtered by the relation, and run when gqlgen asks for the connection's
// fields.
func (x *User) PostsGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy []*PostOrder, where *PostWhereInput) (*PostConnection, error) {
	if after == nil && first == nil && before == nil && last == nil && orderBy == nil && where == nil {
		if rows, ok := x.Posts.Get(); ok {
			return &PostConnection{c: gqlimpl.LoadedConnection(ctx, rows, "Post", postKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return PostsConnectionOf(ctx, db.Query[Post]().Where(Posts.AuthorID.EQ(x.ID)), after, first, before, last, orderBy, where)
}

// GroupsGQL is the edge groups: the Group nodes it leads to, in key order. It
// is x.Groups when a With loaded it, and else one query, which reads at most
// the MaxList of veloxgql.LimitsFrom(ctx) rows, whatever the DB's WithMaxRows:
// more fail it with velox.ErrTooManyRows, in an error that names the edge.
func (x *User) GroupsGQL(ctx context.Context) ([]*Group, error) {
	rows, ok := x.Groups.Get()
	if !ok {
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		if rows, err = db.Query[Group]().Where(Groups.Users.HasWhere(Users.ID.EQ(x.ID))).OrderBy(Groups.ID.Asc()).Unbounded().MaxRows(veloxgql.LimitsFrom(ctx).MaxList).All(ctx); err != nil {
			return nil, fmt.Errorf("veloxgql: Account.groups: %w", err)
		}
	}
	out := make([]*Group, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

// LikesGQL is the edge likes: a connection to the Like nodes it leads to,
// paged, ordered and filtered by the arguments. With no argument it is over
// x.Likes when a With loaded it, in the order loaded; else its queries are on
// Like, filtered by the relation, and run when gqlgen asks for the connection's
// fields.
func (x *User) LikesGQL(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*LikeConnection, error) {
	if after == nil && first == nil && before == nil && last == nil {
		if rows, ok := x.Likes.Get(); ok {
			return &LikeConnection{c: gqlimpl.LoadedConnection(ctx, rows, "Like", likeKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return LikesConnectionOf(ctx, db.Query[Like]().Where(Likes.UserID.EQ(x.ID)), after, first, before, last)
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
// veloxgql.DBFrom reads from ctx, paged and ordered by the arguments of a
// connection to Account: a Query field's resolver returns it. It fails before
// any query runs on arguments gqlimpl.NewConnection refuses.
func AccountsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
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
// cursors. q must also select the key and the columns orderBy names: a Columns
// or Omit that drops them breaks the cursors, and nothing refuses it. It runs
// no query.
func AccountsConnectionOf(ctx context.Context, q velox.Query[User], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
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

// AccountWhereInput is the GraphQL input AccountWhereInput, a filter of Account
// nodes: a node matches when every predicate given holds.
type AccountWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *AccountWhereInput
	And []*AccountWhereInput
	Or  []*AccountWhereInput

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
// MaxFilterDepth of veloxgql.LimitsFrom(ctx) (veloxgql.ErrFilterTooDeep), on an
// and, an or, a has…With, an In or a NotIn of more than its MaxFilterWidth
// entries (veloxgql.ErrFilterTooWide), and on a where argument of more than its
// MaxFilterNodes inputs, predicates and values in all
// (veloxgql.ErrFilterTooLarge).
func (w *AccountWhereInput) Pred(ctx context.Context) (velox.Pred[User], error) {
	f := gqlimpl.NewFilter[User](ctx, "AccountWhereInput")
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
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
	// PostIDs are the IDs of the Post nodes the edge posts leads to.
	PostIDs []string
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
	var p17 any
	if in.BestFriendID != nil {
		p17 = *in.BestFriendID
	}
	return fmt.Sprintf("CreateAccountInput(CreatedAt=%v, UpdatedAt=%v, DisplayName=%v, Email=%v, Role=%v, Nickname=%v, Bio=%v, Age=%v, Score=%v, ExternalID=%v, Avatar=%v, Quota=%v, Active=%v, Settings=%v, PostIDs=%v, MembershipIDs=%v, BestFriendID=%v)", p0, p1, in.DisplayName, in.Email, p4, p5, p6, p7, p9, p10, p11, in.Quota, p13, p14, in.PostIDs, in.MembershipIDs, p17)
}

// GoString is String, so that %#v does not print the sensitive fields either.
func (in CreateAccountInput) GoString() string { return in.String() }

// CreateAccount creates one Account from input, with its edges, and returns it,
// as the mutation createAccount does. It first decodes every ID and JSON value
// of input, and fails before any statement on one it cannot decode
// (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom reads from
// ctx, in one transaction (gqlimpl.Atomic): the operation's, or one of its own.
// The tenant scope and the policies apply as to any write: each ID must name a
// row the caller may read, and a row it re-points one the caller may change, or
// CreateAccount fails with NOT_FOUND and keeps nothing. A validator's refusal
// is BAD_USER_INPUT, naming the input field; velox's other errors are returned
// as they are.
func CreateAccount(ctx context.Context, input CreateAccountInput) (*User, error) {
	var (
		j14 map[string]any
		k15 []int64
		k16 []int64
		k17 int64
		err error
	)
	if input.Settings != nil {
		if j14, err = gqlimpl.FromJSON[map[string]any]("CreateAccountInput.settings", *input.Settings); err != nil {
			return nil, err
		}
	}
	if k15, err = gqlimpl.DecodeIDs[int64](ctx, "CreateAccountInput.postIDs", input.PostIDs, "Post"); err != nil {
		return nil, err
	}
	if k16, err = gqlimpl.DecodeIDs[int64](ctx, "CreateAccountInput.membershipIDs", input.MembershipIDs, "Group"); err != nil {
		return nil, err
	}
	if input.BestFriendID != nil {
		if k17, err = gqlimpl.DecodeID[int64]("CreateAccountInput.bestFriendID", *input.BestFriendID, "Account"); err != nil {
			return nil, err
		}
	}
	var out *User
	err = gqlimpl.Atomic(ctx, func(ctx context.Context, db *velox.DB) error {
		var x User
		if len(k16) > 0 {
			if err := gqlimpl.Exist(ctx, db.Query[Group]().Where(Groups.ID.In(k16...)), len(k16), "CreateAccountInput.membershipIDs", "Group"); err != nil {
				return err
			}
		}
		if input.BestFriendID != nil {
			if err := gqlimpl.Exist(ctx, db.Query[User]().Where(Users.ID.In(k17)), 1, "CreateAccountInput.bestFriendID", "Account"); err != nil {
				return err
			}
			x.UserBestFriend = &k17
		}
		if input.CreatedAt != nil {
			x.CreatedAt = *input.CreatedAt
		}
		if input.UpdatedAt != nil {
			x.UpdatedAt = *input.UpdatedAt
		}
		x.Name = input.DisplayName
		x.Email = input.Email
		if input.Role != nil {
			x.Role = *input.Role
		}
		if input.Nickname != nil {
			x.Nickname = input.Nickname
		}
		if input.Bio != nil {
			x.Bio = input.Bio
		}
		if input.Age != nil {
			x.Age = input.Age
		}
		x.PasswordHash = input.PasswordHash
		if input.Score != nil {
			x.Score = *input.Score
		}
		if input.ExternalID != nil {
			x.ExternalID = *input.ExternalID
		}
		if input.Avatar != nil {
			x.Avatar = *input.Avatar
		}
		x.Quota = input.Quota
		if input.Active != nil {
			x.Active = *input.Active
		}
		if input.Settings != nil {
			x.Settings = j14
		}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			return gqlimpl.WriteError(err, "CreateAccountInput", "gen.User", userFieldGQL)
		}
		if len(k15) > 0 {
			if n, err := db.Update[Post]().Set(Posts.AuthorID.Set(x.ID)).Where(Posts.ID.In(k15...)).Exec(ctx); err != nil {
				return err
			} else if err := gqlimpl.Matched(n, len(k15), "CreateAccountInput.postIDs", "Post"); err != nil {
				return err
			}
		}
		if len(k16) > 0 {
			rows := make([]UserGroup, len(k16))
			for i, k := range k16 {
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
