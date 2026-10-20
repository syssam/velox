package gen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

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
		return errors.New("UserRole must be ADMIN, EDITOR or VIEWER")
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
			return &PostConnection{c: veloxgql.LoadedConnection(rows, "Post", postKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return postConnectionGQL(ctx, db.Query[Post]().Where(Posts.AuthorID.EQ(x.ID)), after, first, before, last, orderBy, where)
}

// GroupsGQL is the edge groups: the Group nodes it leads to, in key order. It
// is x.Groups when a With loaded it, and else one query, which reads at most
// veloxgql.MaxList rows, whatever the DB's WithMaxRows: more fail it with
// velox.ErrTooManyRows, in an error that names the edge.
func (x *User) GroupsGQL(ctx context.Context) ([]*Group, error) {
	rows, ok := x.Groups.Get()
	if !ok {
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		if rows, err = db.Query[Group]().Where(Groups.Users.HasWhere(Users.ID.EQ(x.ID))).OrderBy(Groups.ID.Asc()).Unbounded().MaxRows(veloxgql.MaxList).All(ctx); err != nil {
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
			return &LikeConnection{c: veloxgql.LoadedConnection(rows, "Like", likeKeyGQL())}, nil
		}
	}
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return likeConnectionGQL(ctx, db.Query[Like]().Where(Likes.UserID.EQ(x.ID)), after, first, before, last)
}

// AccountConnection is the GraphQL type AccountConnection, a connection to a
// list of Account. It holds the query of the whole list and the page asked for;
// gqlgen calls its methods for the fields a query selects, and each runs its
// query only then: edges and pageInfo share one page query, and totalCount runs
// a COUNT.
type AccountConnection struct {
	c *veloxgql.Connection[User]
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
// any query runs on arguments veloxgql.NewConnection refuses.
func AccountsConnection(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return nil, err
	}
	return userConnectionGQL(ctx, db.Query[User](), after, first, before, last, where)
}

// userConnectionGQL is the connection to the Account nodes q returns, filtered
// by where, ordered by the key, and paged by the other arguments. It runs no
// query.
func userConnectionGQL(ctx context.Context, q velox.Query[User], after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *AccountWhereInput) (*AccountConnection, error) {
	if where != nil {
		p, err := where.Pred(ctx, 0)
		if err != nil {
			return nil, err
		}
		q = q.Where(p)
	}
	c, err := veloxgql.NewConnection(q, "Account", veloxgql.Args{After: after, First: first, Before: before, Last: last}, nil, userKeyGQL())
	if err != nil {
		return nil, err
	}
	return &AccountConnection{c: c}, nil
}

// userKeyGQL is the order of User's key, which ends every order of a connection
// to Account.
func userKeyGQL() []veloxgql.OrderTerm[User] {
	return []veloxgql.OrderTerm[User]{
		veloxgql.Term("id", veloxgql.OrderDirectionAsc, Users.ID.Asc(), Users.ID.Desc(),
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
// holds, and a w that sets none, or a nil w, holds for every node. depth is how
// deeply w is nested in the where argument, 0 at the top. It fails, before any
// query runs, on an ID of another type (veloxgql.ErrWrongIDType) or a malformed
// one (veloxgql.ErrBadID), on a nesting deeper than veloxgql.MaxFilterDepth
// (veloxgql.ErrFilterTooDeep), on an and, an or, a has…With, an In or a NotIn
// of more than veloxgql.MaxFilterWidth entries, and on a where argument of more
// than veloxgql.MaxFilterNodes inputs, predicates and values in all (both
// veloxgql.ErrFilterTooWide).
func (w *AccountWhereInput) Pred(ctx context.Context, depth int) (velox.Pred[User], error) {
	f := veloxgql.NewFilter[User](ctx, "AccountWhereInput", depth)
	if w == nil {
		return f.Pred()
	}
	veloxgql.Not(f, "not", w.Not)
	veloxgql.And(f, "and", w.And)
	veloxgql.Or(f, "or", w.Or)
	veloxgql.Value(f, "displayName", w.DisplayName, Users.Name.EQ)
	veloxgql.Value(f, "displayNameNEQ", w.DisplayNameNEQ, Users.Name.NEQ)
	veloxgql.List(f, "displayNameIn", w.DisplayNameIn, Users.Name.In)
	veloxgql.List(f, "displayNameNotIn", w.DisplayNameNotIn, Users.Name.NotIn)
	veloxgql.Value(f, "displayNameContains", w.DisplayNameContains, Users.Name.Contains)
	veloxgql.Value(f, "displayNameHasPrefix", w.DisplayNameHasPrefix, Users.Name.HasPrefix)
	veloxgql.Value(f, "displayNameHasSuffix", w.DisplayNameHasSuffix, Users.Name.HasSuffix)
	veloxgql.Is(f, "hasPosts", w.HasPosts, Users.Posts.Has)
	veloxgql.HasWith(f, "hasPostsWith", w.HasPostsWith, Users.Posts.HasWhere)
	return f.Pred()
}
