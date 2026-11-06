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

const _ = veloxgql.GeneratedCodeIsVersion15

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
	Key:   []gqlimpl.Col{{Field: "id", Col: userColID}},
	Read:  []int{userColID, userColUserBestFriend},
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
	Edges: []string{"posts", "memberships", "likes"},
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

// CreateAccountInput is the GraphQL input CreateAccountInput, whose Setters
// gives its fields as setters. A nil pointer or list is a field not given, and
// so is a null.
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

// Setters returns the setters of the input's fields and edges, for
// db.Insert(x).Set: a field not given sets nothing, and x keeps its value or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or an ID that does not decode or
// names another type, is BAD_USER_INPUT naming the input's field. See
// "Mutations" in the documentation of package
// github.com/syssam/velox/contrib/graphql for what each field does and what the
// write checks.
func (in *CreateAccountInput) Setters() ([]velox.Setter[User], error) {
	var set []velox.Setter[User]
	if v := in.CreatedAt; v != nil {
		if err := gqlimpl.NotZero("CreateAccountInput.createdAt", *v); err != nil {
			return nil, err
		}
		set = append(set, Users.CreatedAt.Set(*v))
	}
	if v := in.UpdatedAt; v != nil {
		if err := gqlimpl.NotZero("CreateAccountInput.updatedAt", *v); err != nil {
			return nil, err
		}
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
		if err := gqlimpl.NotZero("CreateAccountInput.externalID", *v); err != nil {
			return nil, err
		}
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
		settings, err := gqlimpl.FromJSON[map[string]any]("CreateAccountInput.settings", *v)
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Settings.Set(settings))
	}
	if len(in.MembershipIDs) > 0 {
		membershipIDs, err := gqlimpl.DecodeIDs[int64]("CreateAccountInput.membershipIDs", in.MembershipIDs, "Group")
		if err != nil {
			return nil, err
		}
		set = append(set, Users.Groups.Add(membershipIDs...))
	}
	if v := in.BestFriendID; v != nil {
		bestFriendID, err := gqlimpl.DecodeID[int64]("CreateAccountInput.bestFriendID", *v, "Account")
		if err != nil {
			return nil, err
		}
		set = append(set, Users.BestFriend.Set(bestFriendID))
	}
	return gqlimpl.Checked("CreateAccountInput", &userConnGQL, set)
}
