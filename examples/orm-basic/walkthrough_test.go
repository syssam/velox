// Package example walks through the code velox-gen generates for the schema
// in ./schema: the same four entities as examples/basic, so that the two can
// be read side by side. Each test is one thing an application does.
package example

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/orm-basic/velox"
	"example.com/orm-basic/velox/entity"
	"example.com/orm-basic/velox/migrate"
	"example.com/orm-basic/velox/post"
	"example.com/orm-basic/velox/tag"
	"example.com/orm-basic/velox/user"

	_ "modernc.org/sqlite"
)

var (
	ctx = context.Background()
	seq atomic.Int64
)

// open returns a handle on a fresh in-memory database with the tables of the
// schema. A *velox.DB is the whole connection: it has no field per entity.
func open(t *testing.T) *velox.DB {
	t.Helper()
	conn, err := velox.Open("sqlite", fmt.Sprintf("file:ormbasic%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", seq.Add(1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, migrate.Create(ctx, conn))
	return conn
}

func newUser(t *testing.T, ex velox.Executor, name string, age int) *entity.User {
	t.Helper()
	u, err := velox.User.Create(ex).
		Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(age)).
		Save(ctx)
	require.NoError(t, err)
	return u
}

// A row is created by naming its columns: user.Name is the column, and
// user.Name.Set(v) the assignment. What is not set takes the schema's default.
func TestCreate(t *testing.T) {
	conn := open(t)
	ann, err := velox.User.Create(conn).
		Set(
			user.Name.Set("ann"),
			user.Email.Set("ann@example.com"),
			user.Age.Set(30),
		).
		Save(ctx)
	require.NoError(t, err)
	assert.NotZero(t, ann.ID)
	assert.Equal(t, user.RoleUser, ann.Role, "the default of the schema")
	assert.False(t, ann.CreatedAt.IsZero(), "the time mixin")

	// Several rows with one statement.
	tags, err := velox.Tag.CreateBulk(conn,
		velox.Tag.Create(conn).Set(tag.Name.Set("go")),
		velox.Tag.Create(conn).Set(tag.Name.Set("sql")),
	).Save(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 2)
}

// The errors are package velox's, whatever was generated.
func TestErrors(t *testing.T) {
	conn := open(t)
	newUser(t, conn, "ann", 30)

	// The schema's validators run before any SQL.
	_, err := velox.User.Create(conn).Set(user.Name.Set(""), user.Email.Set("x@example.com")).Save(ctx)
	var invalid *velox.ValidationError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "name", invalid.Name)

	// What the database refuses says which kind of constraint it was.
	_, err = velox.User.Create(conn).Set(user.Name.Set("ann2"), user.Email.Set("ann@example.com")).Save(ctx)
	require.True(t, velox.IsConstraintError(err))
	require.ErrorIs(t, err, velox.ErrUnique)

	// A row that is not there.
	_, err = velox.User.Get(ctx, conn, 404)
	require.True(t, velox.IsNotFound(err))
	assert.EqualError(t, err, "velox: user not found")
}

// A condition is a method of the column. One of another entity's columns
// does not compile here: velox.User.Query(conn).Where(post.Title.EQ("x")).
func TestQuery(t *testing.T) {
	conn := open(t)
	newUser(t, conn, "ann", 30)
	newUser(t, conn, "bob", 17)
	newUser(t, conn, "cat", 45)

	adults, err := velox.User.Query(conn).
		Where(user.Age.GTE(18)).
		OrderBy(user.Age.Desc()).
		All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"cat", "ann"}, names(adults))

	// And, Or and Not of the entity's own conditions.
	some, err := velox.User.Query(conn).
		Where(user.Or(user.Name.EQ("bob"), user.And(user.Age.GT(40), user.Not(user.Name.Contains("z"))))).
		OrderBy(user.Name.Asc()).
		All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob", "cat"}, names(some))

	// One row, a count, whether any.
	bob, err := velox.User.Query(conn).Where(user.Email.EQ("bob@example.com")).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, 17, bob.Age)
	n, err := velox.User.Query(conn).Where(user.Role.EQ(user.RoleUser)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	any, err := velox.User.Query(conn).Where(user.Role.EQ(user.RoleAdmin)).Exists(ctx)
	require.NoError(t, err)
	assert.False(t, any)
}

// An update names what changes; SaveOne is for exactly one row and returns
// it, Exec touches every row that matches and returns how many.
func TestUpdateAndDelete(t *testing.T) {
	conn := open(t)
	ann := newUser(t, conn, "ann", 30)
	newUser(t, conn, "bob", 17)

	ann, err := velox.User.UpdateByID(conn, ann.ID).
		Set(user.Age.Add(1), user.Role.Set(user.RoleAdmin)).
		SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, 31, ann.Age)
	assert.Equal(t, user.RoleAdmin, ann.Role)

	n, err := velox.User.Update(conn).
		Set(user.Role.Set(user.RoleGuest)).
		Where(user.Age.LT(18)).
		Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	require.NoError(t, velox.User.DeleteByID(ctx, conn, ann.ID))
	require.True(t, velox.IsNotFound(velox.User.DeleteByID(ctx, conn, ann.ID)), "it is gone")
	n, err = velox.User.Delete(conn).Where(user.Role.EQ(user.RoleGuest)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

// An edge is a value of the entity's package, like a column: post.Author,
// user.Posts, post.Tags.
func TestEdges(t *testing.T) {
	conn := open(t)
	ann, bob := newUser(t, conn, "ann", 30), newUser(t, conn, "bob", 40)
	golang, err := velox.Tag.Create(conn).Set(tag.Name.Set("go")).Save(ctx)
	require.NoError(t, err)

	// A post belongs to its author and carries tags.
	for i, title := range []string{"Go generics", "Go errors", "SQL joins"} {
		author := ann
		if i == 2 {
			author = bob
		}
		b := velox.Post.Create(conn).Set(post.Title.Set(title), post.Author.Set(author.ID))
		if i < 2 {
			b.Set(post.Tags.Add(golang.ID), post.Status.Set(post.StatusPublished))
		}
		_, err := b.Save(ctx)
		require.NoError(t, err)
	}

	// Filter through an edge.
	gophers, err := velox.User.Query(conn).
		Where(user.Posts.HasWith(post.Tags.HasWith(tag.Name.EQ("go")))).
		All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann"}, names(gophers))

	// Load an edge with the rows: one more query for the edge, whatever the
	// number of users. The query given describes what to load of it.
	users, err := velox.User.Query(conn).
		OrderBy(user.Name.Asc()).
		With(user.Posts.Load(velox.Post.Query(nil).Where(post.Status.EQ(post.StatusPublished)).OrderBy(post.Title.Asc()))).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, []string{"Go errors", "Go generics"}, titles(users[0].Edges.Posts))
	assert.Empty(t, users[1].Edges.Posts, "bob has no published post")

	// From an entity to its neighbors. The entity holds no connection: the
	// query is started on the handle it is given, a transaction as well.
	n, err := ann.QueryPosts(conn).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// A transaction is a *velox.Tx, and is given where a *velox.DB is: the same
// functions run inside it. RunTx commits when the function returns nil.
func TestTransaction(t *testing.T) {
	conn := open(t)
	failed := errors.New("changed my mind")
	err := conn.RunTx(ctx, func(tx *velox.Tx) error {
		newUser(t, tx, "ann", 30) // newUser takes a velox.Executor
		n, err := velox.User.Query(tx).Count(ctx)
		if err != nil {
			return err
		}
		assert.Equal(t, 1, n, "inside the transaction")
		return failed
	})
	require.ErrorIs(t, err, failed)
	n, err := velox.User.Query(conn).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "rolled back")
}

// A list is paged by a cursor, without GraphQL as well: Paginate is part of
// every query.
func TestPaginate(t *testing.T) {
	conn := open(t)
	for _, name := range []string{"ann", "bob", "cat", "dan", "eve"} {
		newUser(t, conn, name, 30)
	}
	two := 2
	page, err := velox.User.Query(conn).OrderBy(user.Name.Asc()).Paginate(ctx, nil, &two, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 5, page.TotalCount)
	assert.True(t, page.PageInfo.HasNextPage)
	require.Len(t, page.Edges, 2)
	assert.Equal(t, "ann", page.Edges[0].Node.Name)

	next, err := velox.User.Query(conn).OrderBy(user.Name.Asc()).Paginate(ctx, page.PageInfo.EndCursor, &two, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "cat", next.Edges[0].Node.Name)
}

func names(us []*entity.User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}

func titles(ps []*entity.Post) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}
