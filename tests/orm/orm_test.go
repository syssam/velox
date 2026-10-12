package tests

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	client "github.com/syssam/velox/tests/orm/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/orm"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/orm/db/entity"
	"github.com/syssam/velox/tests/orm/db/migrate"
	"github.com/syssam/velox/tests/orm/db/pet"
	"github.com/syssam/velox/tests/orm/db/post"
	"github.com/syssam/velox/tests/orm/db/tag"
	"github.com/syssam/velox/tests/orm/db/user"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

var dbSeq atomic.Int64

// open returns a handle and a velox client over the same fresh database.
// The tables are created by the generated migrate package, and the velox
// client, generated from the same schema, is the reference the tests compare
// against: it runs on tables it did not create. The database is SQLite
// in memory, or a new database on the Postgres server VELOX_ORM_TEST_POSTGRES names
// (e.g. postgres://postgres:pass@localhost:5544/postgres?sslmode=disable).
func open(t *testing.T) (*orm.DB, *integration.Client) {
	t.Helper()
	ctx := context.Background()
	var drv *velsql.Driver
	if admin := os.Getenv("VELOX_ORM_TEST_POSTGRES"); admin != "" {
		drv = openPostgres(t, admin)
	} else {
		dsn := fmt.Sprintf("file:orm%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", dbSeq.Add(1))
		var err error
		drv, err = velsql.Open(dialect.SQLite, dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = drv.Close() })
	}
	db := orm.OpenDB(drv.Dialect(), drv.DB())
	require.NoError(t, migrate.Create(ctx, db))
	return db, integration.NewClient(integration.Driver(drv))
}

// openPostgres creates a database for one test and drops it afterwards.
func openPostgres(t *testing.T, admin string) *velsql.Driver {
	t.Helper()
	ctx := context.Background()
	adm, err := stdsql.Open("postgres", admin)
	require.NoError(t, err)
	name := fmt.Sprintf("velox_%d_%d", os.Getpid(), dbSeq.Add(1))
	_, err = adm.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	u, err := url.Parse(admin)
	require.NoError(t, err)
	u.Path = "/" + name
	drv, err := velsql.Open(dialect.Postgres, u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = drv.Close()
		_, _ = adm.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		_ = adm.Close()
	})
	return drv
}

func newUser(t *testing.T, db orm.Executor, name string, age int) *entity.User {
	t.Helper()
	u, err := client.User.Create(db).
		Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(age)).
		Save(context.Background())
	require.NoError(t, err)
	return u
}

func newPost(t *testing.T, db orm.Executor, author *entity.User, title string) *entity.Post {
	t.Helper()
	p, err := client.Post.Create(db).
		Set(post.Title.Set(title), post.Content.Set("body of "+title), post.Author.Set(author.ID)).
		Save(context.Background())
	require.NoError(t, err)
	return p
}

func TestCreate_DefaultsValidatorsAndRequired(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)

	u := newUser(t, db, "ann", 30)
	require.NotZero(t, u.ID)
	assert.Equal(t, "ann", u.Name)
	assert.Equal(t, user.RoleUser, u.Role, "the schema default of an enum is applied")
	assert.False(t, u.CreatedAt.IsZero(), "the mixin default func is applied")
	assert.Nil(t, u.Nickname)

	// The reference client reads back exactly what was written.
	got := ref.User.GetX(ctx, u.ID)
	assert.Equal(t, "ann", got.Name)
	assert.Equal(t, 30, got.Age)
	assert.Equal(t, "user", string(got.Role))

	// A validator of the schema rejects the value before any SQL.
	_, err := client.User.Create(db).Set(user.Name.Set(""), user.Email.Set("x@example.com"), user.Age.Set(1)).Save(ctx)
	var verr *velox.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "name", verr.Field)

	// A required field that is missing is reported by name.
	_, err = client.User.Create(db).Set(user.Name.Set("bob")).Save(ctx)
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "email", verr.Field)

	// An invalid enum value is rejected.
	_, err = client.User.Create(db).Set(user.Name.Set("eve"), user.Email.Set("eve@example.com"), user.Age.Set(1), user.Role.Set("root")).Save(ctx)
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "role", verr.Field)

	// A unique violation is a constraint error.
	_, err = client.User.Create(db).Set(user.Name.Set("ann2"), user.Email.Set("ann@example.com"), user.Age.Set(1)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)

	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "rejected creates wrote nothing")
}

func TestQuery_PredicatesOrderAndTerminals(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	for i, name := range []string{"ann", "bob", "cid", "dee"} {
		newUser(t, db, name, 20+i*10)
	}

	users, err := client.User.Query(db).Where(user.Age.GTE(30), user.Name.NEQ("dee")).OrderBy(user.Age.Desc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, []string{"cid", "bob"}, []string{users[0].Name, users[1].Name})

	users, err = client.User.Query(db).Where(user.Or(user.Name.EQ("ann"), user.And(user.Age.GT(40), user.Not(user.Name.HasPrefix("x"))))).OrderBy(user.ID.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, []string{"ann", "dee"}, []string{users[0].Name, users[1].Name})

	users, err = client.User.Query(db).Where(user.Name.In("ann", "bob", "zed"), user.Nickname.IsNil()).OrderBy(user.Name.Asc()).Limit(1).Offset(1).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "bob", users[0].Name)

	_, err = client.User.Query(db).Where(user.Name.EQ("nobody")).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	_, err = client.User.Query(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	_, err = client.User.Query(db).Where(user.Name.EQ("nobody")).First(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)

	n, err := client.User.Query(db).Where(user.Age.LT(40)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	ok, err := client.User.Query(db).Where(user.Name.Contains("e")).Exists(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	ids, err := client.User.Query(db).Where(user.Age.GT(25)).OrderBy(user.ID.Asc()).IDs(ctx)
	require.NoError(t, err)
	assert.Len(t, ids, 3)
}

func TestUpdate_ExecSaveAndDelete(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)
	a, b := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)

	n, err := client.User.Update(db).Set(user.Age.Add(1)).Where(user.Age.GTE(30)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 31, ref.User.GetX(ctx, a.ID).Age)
	assert.Equal(t, 41, ref.User.GetX(ctx, b.ID).Age)

	before := ref.User.GetX(ctx, a.ID).UpdatedAt
	got, err := client.User.Update(db).Set(user.Name.Set("anna"), user.Nickname.Set("an")).Where(user.ID.EQ(a.ID)).SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, "anna", got.Name)
	require.NotNil(t, got.Nickname)
	assert.Equal(t, "an", *got.Nickname)
	assert.False(t, got.UpdatedAt.Before(before), "the update default runs")

	got, err = client.User.Update(db).Set(user.Nickname.Clear()).Where(user.ID.EQ(a.ID)).SaveOne(ctx)
	require.NoError(t, err)
	assert.Nil(t, got.Nickname)

	_, err = client.User.Update(db).Set(user.Name.Set("x")).Where(user.ID.EQ(a.ID + 1000)).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)

	// Save on more than one row changes nothing.
	_, err = client.User.Update(db).Set(user.Name.Set("same")).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	assert.Equal(t, "anna", ref.User.GetX(ctx, a.ID).Name)
	assert.Equal(t, "bob", ref.User.GetX(ctx, b.ID).Name)

	// Validators run on update too.
	_, err = client.User.Update(db).Set(user.Name.Set("")).Where(user.ID.EQ(a.ID)).Exec(ctx)
	var verr *velox.ValidationError
	require.ErrorAs(t, err, &verr)

	n, err = client.User.Delete(db).Where(user.ID.EQ(b.ID)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	left, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, left)
}

func TestCreateBulk(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)
	var bs []*entity.UserCreate
	for i := range 5 {
		name := fmt.Sprintf("u%d", i)
		bs = append(bs, client.User.Create(db).Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(i+20)))
	}
	users, err := client.User.CreateBulk(db, bs...).Save(ctx)
	require.NoError(t, err)
	require.Len(t, users, 5)
	for i, u := range users {
		require.NotZero(t, u.ID)
		got := ref.User.GetX(ctx, u.ID)
		assert.Equal(t, fmt.Sprintf("u%d", i), got.Name)
		assert.Equal(t, i+20, got.Age)
	}
	// One invalid row fails the whole batch before any SQL.
	_, err = client.User.CreateBulk(db,
		client.User.Create(db).Set(user.Name.Set("ok"), user.Email.Set("ok@example.com"), user.Age.Set(1)),
		client.User.Create(db).Set(user.Name.Set(""), user.Email.Set("bad@example.com"), user.Age.Set(1)),
	).Save(ctx)
	var verr *velox.ValidationError
	require.ErrorAs(t, err, &verr)
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
}

func TestEdges_EagerLoadEveryKind(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	p1, p2, p3 := newPost(t, db, ann, "a1"), newPost(t, db, ann, "a2"), newPost(t, db, bob, "b1")
	_ = p3

	// O2M with a per-edge predicate, order and nested M2O.
	users, err := client.User.Query(db).OrderBy(user.ID.Asc()).
		WithPosts(client.Post.Query(db).Where(post.Title.NEQ("a1")).OrderBy(post.Title.Desc()).WithAuthor()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	posts, err := users[0].Edges.PostsOrErr()
	require.NoError(t, err)
	require.Len(t, posts, 1)
	assert.Equal(t, "a2", posts[0].Title)
	author, err := posts[0].Edges.AuthorOrErr()
	require.NoError(t, err)
	assert.Equal(t, ann.ID, author.ID)
	require.Len(t, users[1].Edges.Posts, 1)

	// An edge that was not requested reports so.
	_, err = users[0].Edges.PetsOrErr()
	var nle *velox.NotLoadedError
	require.ErrorAs(t, err, &nle)

	// M2O.
	ps, err := client.Post.Query(db).OrderBy(post.ID.Asc()).WithAuthor().All(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 3)
	assert.Equal(t, []int{ann.ID, ann.ID, bob.ID}, []int{ps[0].Edges.Author.ID, ps[1].Edges.Author.ID, ps[2].Edges.Author.ID})

	// M2M, both directions, with edge mutations.
	t1, err := client.Tag.Create(db).Set(tag.Name.Set("go")).Save(ctx)
	require.NoError(t, err)
	t2, err := client.Tag.Create(db).Set(tag.Name.Set("sql")).Save(ctx)
	require.NoError(t, err)
	_, err = client.Post.Update(db).Set(post.Tags.Add(t1.ID, t2.ID)).Where(post.ID.EQ(p1.ID)).Exec(ctx)
	require.NoError(t, err)
	_, err = client.Post.Update(db).Set(post.Tags.Add(t1.ID)).Where(post.ID.EQ(p2.ID)).Exec(ctx)
	require.NoError(t, err)
	// The reference client sees the same join rows.
	refTags, err := ref.Post.GetX(ctx, p1.ID).QueryTags().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, refTags)

	ps, err = client.Post.Query(db).OrderBy(post.ID.Asc()).WithTags(client.Tag.Query(db).OrderBy(tag.Name.Asc())).All(ctx)
	require.NoError(t, err)
	require.Len(t, ps[0].Edges.Tags, 2)
	assert.Equal(t, []string{"go", "sql"}, []string{ps[0].Edges.Tags[0].Name, ps[0].Edges.Tags[1].Name})
	require.Len(t, ps[1].Edges.Tags, 1)
	assert.Empty(t, ps[2].Edges.Tags)
	tags, err := ps[2].Edges.TagsOrErr()
	require.NoError(t, err, "loaded and empty is not an error")
	assert.Empty(t, tags)

	ts, err := client.Tag.Query(db).OrderBy(tag.Name.Asc()).WithPosts(client.Post.Query(db).OrderBy(post.Title.Asc())).All(ctx)
	require.NoError(t, err)
	require.Len(t, ts[0].Edges.Posts, 2)
	assert.Equal(t, []string{"a1", "a2"}, []string{ts[0].Edges.Posts[0].Title, ts[0].Edges.Posts[1].Title})
	require.Len(t, ts[1].Edges.Posts, 1)

	_, err = client.Post.Update(db).Set(post.Tags.Remove(t2.ID)).Where(post.ID.EQ(p1.ID)).Exec(ctx)
	require.NoError(t, err)
	n, err := p1.QueryTags(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = client.Post.Update(db).Set(post.Tags.Clear()).Where(post.ID.EQ(p1.ID)).Exec(ctx)
	require.NoError(t, err)
	n, err = p1.QueryTags(db).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)

	// A per-parent limit.
	users, err = client.User.Query(db).OrderBy(user.ID.Asc()).WithPosts(client.Post.Query(db).OrderBy(post.Title.Asc()).Limit(1)).All(ctx)
	require.NoError(t, err)
	require.Len(t, users[0].Edges.Posts, 1)
	assert.Equal(t, "a1", users[0].Edges.Posts[0].Title)
	require.Len(t, users[1].Edges.Posts, 1)
}

func TestEdges_TraversalAndPredicates(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, bob, cid := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40), newUser(t, db, "cid", 50)
	p1 := newPost(t, db, ann, "a1")
	newPost(t, db, ann, "a2")
	newPost(t, db, bob, "b1")
	_ = cid

	// Entity-level traversal, with the target's own predicates on top.
	posts, err := ann.QueryPosts(db).Where(post.Title.EQ("a2")).All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	assert.Equal(t, "a2", posts[0].Title)
	author, err := p1.QueryAuthor(db).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, ann.ID, author.ID)

	// Has / HasWith.
	users, err := client.User.Query(db).Where(user.Posts.Has()).OrderBy(user.ID.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	users, err = client.User.Query(db).Where(user.Posts.HasWith(post.Title.HasPrefix("b"))).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "bob", users[0].Name)
	users, err = client.User.Query(db).Where(user.Not(user.Posts.Has())).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "cid", users[0].Name)
	ps, err := client.Post.Query(db).Where(post.Author.HasWith(user.Age.GT(35))).All(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, "b1", ps[0].Title)

	// A field-bound optional M2O: set through the field, cleared through the edge.
	rex, err := client.Pet.Create(db).Set(pet.Name.Set("rex"), pet.OwnerID.Set(ann.ID)).Save(ctx)
	require.NoError(t, err)
	require.NotNil(t, rex.OwnerID)
	owner, err := rex.QueryOwner(db).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, ann.ID, owner.ID)
	n, err := ann.QueryPets(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	got, err := client.Pet.Update(db).Set(pet.OwnerID.Clear()).Where(pet.ID.EQ(rex.ID)).SaveOne(ctx)
	require.NoError(t, err)
	assert.Nil(t, got.OwnerID)
	_, err = got.QueryOwner(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
}

func TestTx_CommitRollbackAndPanic(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)

	require.NoError(t, db.RunTx(ctx, func(tx *orm.Tx) error {
		u := newUser(t, tx, "ann", 30)
		newPost(t, tx, u, "in tx")
		// The transaction sees its own writes.
		n, err := client.User.Query(tx).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		return nil
	}))
	assert.Equal(t, 1, refCount(t, ref))
	posts, err := ref.Post.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, posts)

	boom := errors.New("boom")
	err = db.RunTx(ctx, func(tx *orm.Tx) error {
		newUser(t, tx, "bob", 40)
		return boom
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 1, refCount(t, ref), "an error rolls the transaction back")

	require.PanicsWithValue(t, "bang", func() {
		_ = db.RunTx(ctx, func(tx *orm.Tx) error {
			newUser(t, tx, "cid", 50)
			panic("bang")
		})
	})
	assert.Equal(t, 1, refCount(t, ref), "a panic rolls the transaction back")

	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	newUser(t, tx, "dee", 60)
	require.NoError(t, tx.Commit())
	assert.ErrorIs(t, tx.Rollback(), orm.ErrTxDone, "a deferred Rollback after Commit is harmless")
	assert.Equal(t, 2, refCount(t, ref))
}

func refCount(t *testing.T, ref *integration.Client) int {
	t.Helper()
	n, err := ref.User.Query().Count(context.Background())
	require.NoError(t, err)
	return n
}

// Adding or removing no neighbors changes nothing. An edge change without ids
// is how the SQL layer spells "clear the edge", so Remove() of an empty list
// once meant "remove all", and Add() of an empty list was an INSERT without rows.
func TestEdgeChange_EmptyListIsNoOp(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	t1, err := client.Tag.Create(db).Set(tag.Name.Set("a")).Save(ctx)
	require.NoError(t, err)
	t2, err := client.Tag.Create(db).Set(tag.Name.Set("b")).Save(ctx)
	require.NoError(t, err)

	p, err := client.Post.Create(db).
		Set(post.Title.Set("t"), post.Content.Set("c"), post.Author.Set(u.ID), post.Tags.Add()).
		Save(ctx)
	require.NoError(t, err, "Add() with no ids on create")

	_, err = client.Post.Update(db).Set(post.Tags.Add(t1.ID, t2.ID)).Where(post.ID.EQ(p.ID)).Exec(ctx)
	require.NoError(t, err)
	_, err = client.Post.Update(db).Set(post.Tags.Remove(), post.Tags.Add()).Where(post.ID.EQ(p.ID)).Exec(ctx)
	require.NoError(t, err, "Remove() and Add() with no ids on update")

	n, err := p.QueryTags(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "Remove() with no ids removed tags")
}
