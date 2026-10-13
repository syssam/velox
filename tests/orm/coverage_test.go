package tests

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/orm"
	client "github.com/syssam/velox/tests/orm/velox"
	"github.com/syssam/velox/tests/orm/velox/post"
	"github.com/syssam/velox/tests/orm/velox/tag"
	"github.com/syssam/velox/tests/orm/velox/user"
	testschema "github.com/syssam/velox/testschema"
)

// An entity encodes to the JSON velox's entity of the same row encodes to:
// the keys are the schema's field names, not the columns', and the loaded
// edges are under "edges". An API response or an audit record must not
// change shape with the ORM.
func TestJSON_SameAsVelox(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)
	ann := newUser(t, db, "ann", 30)
	_, err := client.User.UpdateByID(db, ann.ID).Set(user.Nickname.Set("annie")).SaveOne(ctx)
	require.NoError(t, err)
	p := newPost(t, db, ann, "p1")

	decode := func(v any) map[string]any {
		t.Helper()
		b, err := json.Marshal(v)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		return m
	}
	got, want := decode(must2(client.User.Get(ctx, db, ann.ID))), decode(ref.User.GetX(ctx, ann.ID))
	assert.Equal(t, want, got)
	assert.Contains(t, got, "id")
	assert.Contains(t, got, "nickname")

	// With an edge loaded, it is under "edges" by the edge's name. (velox's
	// client in this repository is generated with back-references, which
	// encoding/json refuses as a cycle, so there is nothing to compare with.)
	loaded, err := client.User.Query(db).Where(user.ID.EQ(ann.ID)).With(user.Posts).Only(ctx)
	require.NoError(t, err)
	withEdges := decode(loaded)
	require.Contains(t, withEdges, "edges")
	posts, _ := withEdges["edges"].(map[string]any)["posts"].([]any)
	require.Len(t, posts, 1)
	assert.Equal(t, "p1", posts[0].(map[string]any)["title"])

	// The same for an entity whose edge holds its key in a column without a
	// schema field: the key does not appear.
	assert.Equal(t, decode(ref.Post.GetX(ctx, p.ID)), decode(must2(client.Post.Get(ctx, db, p.ID))))
}

func must2[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// The whole of the mutation a hook receives: increments, NULLs and edge
// changes are readable and changeable through it, not only values.
func TestHooks_MutationAPI(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	_, err := client.User.UpdateByID(db, ann.ID).Set(user.Nickname.Set("annie")).SaveOne(ctx)
	require.NoError(t, err)
	p := newPost(t, db, ann, "p1")
	var tags []int
	for _, name := range []string{"go", "sql", "orm"} {
		tg, err := client.Tag.Create(db).Set(tag.Name.Set(name)).Save(ctx)
		require.NoError(t, err)
		tags = append(tags, tg.ID)
	}

	var act func(m velox.Mutation) error
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if act != nil {
				if err := act(m); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	run := func(f func(m velox.Mutation) error, do func() error) {
		t.Helper()
		act = f
		defer func() { act = nil }()
		require.NoError(t, do())
	}
	age := func() int { return must2(client.User.Get(ctx, db, ann.ID)).Age }

	// An increment is seen as one, and a hook can add its own.
	run(func(m velox.Mutation) error {
		assert.Equal(t, []string{"age"}, m.AddedFields())
		v, ok := m.AddedField("age")
		assert.True(t, ok)
		assert.Equal(t, 2, v)
		_, isSet := m.Field("age")
		assert.False(t, isSet, "an increment is not a value")
		return m.AddField("age", 3)
	}, func() error {
		_, err := client.User.UpdateByID(db, ann.ID).Set(user.Age.Add(2)).SaveOne(ctx)
		return err
	})
	assert.Equal(t, 35, age())

	// A NULL is seen as one; a hook can clear a field and take a change back.
	run(func(m velox.Mutation) error {
		assert.Equal(t, []string{"nickname"}, m.ClearedFields())
		assert.True(t, m.FieldCleared("nickname"))
		assert.False(t, m.FieldCleared("age"))
		return nil
	}, func() error {
		_, err := client.User.UpdateByID(db, ann.ID).Set(user.Nickname.Clear()).SaveOne(ctx)
		return err
	})
	run(func(m velox.Mutation) error {
		if err := m.ResetField("name"); err != nil {
			return err
		}
		if err := m.ClearField("nickname"); err != nil {
			return err
		}
		assert.Error(t, m.ClearField("no_such_field"))
		assert.Error(t, m.ResetField("no_such_field"))
		assert.Error(t, m.AddField("no_such_field", 1))
		return nil
	}, func() error {
		_, err := client.User.UpdateByID(db, ann.ID).Set(user.Name.Set("renamed"), user.Nickname.Set("back"), user.Age.Set(40)).SaveOne(ctx)
		return err
	})
	u := must2(client.User.Get(ctx, db, ann.ID))
	assert.Equal(t, "ann", u.Name, "the change the hook took back was written")
	assert.Nil(t, u.Nickname)
	assert.Equal(t, 40, u.Age)

	// Edge changes: what is connected, disconnected and cleared.
	tagsOf := func() []string {
		got := must2(client.Post.Query(db).Where(post.ID.EQ(p.ID)).With(post.Tags.Load(client.Tag.Query(nil).OrderBy(tag.Name.Asc()))).Only(ctx))
		var names []string
		for _, tg := range got.Edges.Tags {
			names = append(names, tg.Name)
		}
		return names
	}
	run(func(m velox.Mutation) error {
		assert.Equal(t, []string{"tags"}, m.AddedEdges())
		assert.Equal(t, []velox.Value{tags[0], tags[1]}, m.AddedIDs("tags"))
		assert.Empty(t, m.RemovedEdges())
		assert.Nil(t, m.AddedIDs("no_such_edge"))
		return nil
	}, func() error {
		_, err := client.Post.UpdateByID(db, p.ID).Set(post.Tags.Add(tags[0], tags[1])).SaveOne(ctx)
		return err
	})
	assert.Equal(t, []string{"go", "sql"}, tagsOf())
	run(func(m velox.Mutation) error {
		assert.Equal(t, []string{"tags"}, m.RemovedEdges())
		assert.Equal(t, []velox.Value{tags[0]}, m.RemovedIDs("tags"))
		// The hook takes the removal back and connects another instead.
		if err := m.ResetEdge("tags"); err != nil {
			return err
		}
		assert.Empty(t, m.RemovedEdges())
		assert.Error(t, m.ResetEdge("no_such_edge"))
		return nil
	}, func() error {
		_, err := client.Post.UpdateByID(db, p.ID).Set(post.Tags.Remove(tags[0])).SaveOne(ctx)
		return err
	})
	assert.Equal(t, []string{"go", "sql"}, tagsOf(), "the removal the hook took back happened")
	run(func(m velox.Mutation) error {
		assert.Equal(t, []string{"tags"}, m.ClearedEdges())
		assert.True(t, m.EdgeCleared("tags"))
		return nil
	}, func() error {
		_, err := client.Post.UpdateByID(db, p.ID).Set(post.Tags.Clear()).SaveOne(ctx)
		return err
	})
	assert.Empty(t, tagsOf())
	// Changes apply in the order they were made: a clear after an add leaves
	// nothing, an add after a clear leaves what was added.
	_, err = client.Post.UpdateByID(db, p.ID).Set(post.Tags.Add(tags[0])).SaveOne(ctx)
	require.NoError(t, err)
	run(func(m velox.Mutation) error { return m.ClearEdge("tags") }, func() error {
		_, err := client.Post.UpdateByID(db, p.ID).Set(post.Tags.Add(tags[2]), post.Title.Set("p1b")).SaveOne(ctx)
		return err
	})
	assert.Empty(t, tagsOf(), "the hook's clear came after the add")
	_, err = client.Post.UpdateByID(db, p.ID).Set(post.Tags.Add(tags[0])).SaveOne(ctx)
	require.NoError(t, err)
	_, err = client.Post.UpdateByID(db, p.ID).Set(post.Tags.Clear(), post.Tags.Add(tags[2])).SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"orm"}, tagsOf(), "an add after a clear in one mutation")
	// A to-one edge is a connection like another.
	run(func(m velox.Mutation) error {
		assert.Contains(t, m.AddedEdges(), "author")
		assert.Equal(t, []velox.Value{ann.ID}, m.AddedIDs("author"))
		assert.Error(t, m.ClearEdge("no_such_edge"))
		return nil
	}, func() error {
		_, err := client.Post.Create(db).Set(post.Title.Set("p2"), post.Content.Set("c"), post.Author.Set(ann.ID)).Save(ctx)
		return err
	})
}

// What the database refuses is a *velox.ConstraintError around the driver's
// error; an edge that was not loaded says so by name.
func TestErrors_ConstraintAndNotLoaded(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)

	_, err := client.User.Create(db).Set(user.Name.Set("dup"), user.Email.Set("ann@example.com"), user.Age.Set(1)).Save(ctx)
	var cerr *velox.ConstraintError
	require.ErrorAs(t, err, &cerr, "a unique violation")
	assert.Contains(t, err.Error(), "velox: constraint failed")
	require.Error(t, errors.Unwrap(cerr))
	_, err = client.Post.Create(db).Set(post.Title.Set("p"), post.Content.Set("c"), post.Author.Set(404)).Save(ctx)
	require.ErrorAs(t, err, &cerr, "a foreign key that points nowhere")
	// A bulk create is one statement: one bad row fails all of them.
	_, err = client.User.CreateBulk(db,
		client.User.Create(nil).Set(user.Name.Set("ok"), user.Email.Set("ok@example.com"), user.Age.Set(1)),
		client.User.Create(nil).Set(user.Name.Set("dup"), user.Email.Set("ann@example.com"), user.Age.Set(1)),
	).Save(ctx)
	require.ErrorAs(t, err, &cerr)
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := client.User.Get(ctx, db, ann.ID)
	require.NoError(t, err)
	_, err = got.Edges.PostsOrErr()
	var nl *velox.NotLoadedError
	require.ErrorAs(t, err, &nl)
	assert.Equal(t, "posts", nl.Edge())
	assert.Equal(t, `velox: edge "posts" was not loaded`, err.Error())
	loaded, err := client.User.Query(db).With(user.Posts).Only(ctx)
	require.NoError(t, err)
	posts, err := loaded.Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Empty(t, posts)
}

// An interceptor can narrow the window of a query, not only its conditions;
// the remaining aggregates; closing the handle.
func TestInterceptorWindow_Aggregates_Close(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	for i, name := range []string{"a", "b", "c", "d"} {
		newUser(t, db, name, 20+10*i)
	}
	lo, err := client.User.Query(db).Select().Aggregate(orm.Min(user.ColumnAge)).Int(ctx)
	require.NoError(t, err)
	assert.Equal(t, 20, lo)
	var mean []float64
	require.NoError(t, client.User.Query(db).Select().Aggregate(orm.Mean(user.ColumnAge)).Scan(ctx, &mean))
	assert.Equal(t, []float64{35}, mean)

	db.Intercept(velox.TraverseFunc(func(_ context.Context, q velox.Query) error {
		if oq, ok := q.(*orm.Query); ok && oq.Type() == "User" {
			oq.Limit(2)
			oq.Offset(1)
		}
		return nil
	}))
	us, err := client.User.Query(db).OrderBy(user.Age.Asc()).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, names(us))

	require.NoError(t, db.Close())
	_, err = client.User.Query(db).All(ctx)
	require.Error(t, err, "a closed handle answered")
}

// Inside a transaction, a mutation of several statements that fails is undone
// by a savepoint and the transaction goes on. On Postgres, without the
// savepoint, the failed statement would abort the whole transaction.
func TestTx_SavepointAroundAMutation(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	p := newPost(t, db, ann, "p1")
	tg, err := client.Tag.Create(db).Set(tag.Name.Set("go")).Save(ctx)
	require.NoError(t, err)

	var cerr *velox.ConstraintError
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		// The post row is inserted, then the join row fails.
		_, err := client.Post.Create(tx).Set(post.Title.Set("ghost"), post.Content.Set("c"), post.Author.Set(ann.ID), post.Tags.Add(9999)).Save(ctx)
		require.ErrorAs(t, err, &cerr)
		// The title is updated, then the join row fails.
		_, err = client.Post.UpdateByID(tx, p.ID).Set(post.Title.Set("half"), post.Tags.Add(9999)).SaveOne(ctx)
		require.ErrorAs(t, err, &cerr)
		// The transaction still works, sees neither, and commits what follows.
		n, err := client.Post.Query(tx).Where(post.Title.In("ghost", "half")).Count(ctx)
		if err != nil {
			return err
		}
		assert.Zero(t, n)
		_, err = client.Post.UpdateByID(tx, p.ID).Set(post.Title.Set("kept"), post.Tags.Add(tg.ID)).SaveOne(ctx)
		return err
	}))
	got, err := client.Post.Query(db).With(post.Tags).All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "kept", got[0].Title)
	assert.Len(t, got[0].Edges.Tags, 1)

	// A single-statement mutation opens no savepoint: nothing to undo.
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		_, err := client.User.UpdateByID(tx, ann.ID).Set(user.Age.Set(31)).SaveOne(ctx)
		return err
	}))
}

// Select().SQL(ctx) is the statement a query would run, with the policy's
// and the interceptors' conditions in it, and runs nothing.
func TestQuerySQL(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)

	query, args, err := client.User.Query(db).Where(user.Age.GT(18), user.Name.EQ("ann")).OrderBy(user.Age.Desc()).Limit(5).Select().SQL(ctx)
	require.NoError(t, err)
	assert.Contains(t, query, "SELECT")
	assert.Contains(t, query, "users")
	assert.Contains(t, query, "ORDER BY")
	assert.Contains(t, query, "LIMIT 5")
	assert.Equal(t, []any{18, "ann"}, args)
	// The statement is the one that runs: the same rows.
	rows, err := velox.QuerySQL(ctx, db, query, args...)
	require.NoError(t, err)
	n := 0
	for rows.Next() {
		n++
	}
	require.NoError(t, rows.Close())
	assert.Equal(t, 1, n)

	// Columns and aggregates.
	query, _, err = client.User.Query(db).Select(user.ColumnName).SQL(ctx)
	require.NoError(t, err)
	assert.NotContains(t, query, "email")
	query, _, err = client.User.Query(db).Select().Aggregate(orm.Count()).SQL(ctx)
	require.NoError(t, err)
	assert.Contains(t, query, "COUNT(*)")
	assert.NotContains(t, query, "email")

	// What the policy adds is part of it; what the policy denies is the error.
	_, args, err = client.User.Query(db).Select().SQL(testschema.FilterUserQueryToNameContext(ctx, "ann"))
	require.NoError(t, err)
	assert.Contains(t, args, any("ann"))
	_, _, err = client.User.Query(db).Select("no_such_column").SQL(ctx)
	require.ErrorContains(t, err, `no column "no_such_column"`)
	_, _, err = client.User.Query(nil).Select().SQL(ctx)
	require.ErrorIs(t, err, velox.ErrNoExecutor)

	// Nothing ran: a handle whose database has no tables renders the
	// statement all the same.
	empty, err := velox.Open("sqlite", "file:sqlonly?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = empty.Close() })
	query, _, err = client.User.Query(empty).Where(user.Age.GT(1)).Select().SQL(ctx)
	require.NoError(t, err)
	assert.Contains(t, query, "WHERE")
	_, err = client.User.Query(empty).All(ctx)
	require.Error(t, err)
}

// A constraint error says which kind it is and what the database named.
func TestErrors_ConstraintKind(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)

	_, err := client.User.Create(db).Set(user.Name.Set("dup"), user.Email.Set("ann@example.com"), user.Age.Set(1)).Save(ctx)
	var cerr *velox.ConstraintError
	require.ErrorAs(t, err, &cerr)
	require.ErrorIs(t, err, velox.ErrUnique)
	assert.NotErrorIs(t, err, velox.ErrForeignKey)
	assert.Contains(t, cerr.Constraint(), "email", "what the database named as violated")

	_, err = client.Post.Create(db).Set(post.Title.Set("p"), post.Content.Set("c"), post.Author.Set(ann.ID+1000)).Save(ctx)
	require.ErrorAs(t, err, &cerr)
	require.ErrorIs(t, err, velox.ErrForeignKey)
	assert.NotErrorIs(t, err, velox.ErrUnique)

	// The same through an update and inside a transaction.
	bob := newUser(t, db, "bob", 40)
	err = db.RunTx(ctx, func(tx *velox.Tx) error {
		_, err := client.User.UpdateByID(tx, bob.ID).Set(user.Email.Set("ann@example.com")).SaveOne(ctx)
		return err
	})
	require.ErrorIs(t, err, velox.ErrUnique)

	// The kind is read from the database's own error; one that is no
	// constraint's has none.
	assert.ErrorIs(t, velox.NewConstraintError("x", errors.New("CHECK constraint failed: age_positive")), velox.ErrCheck)
	assert.NotErrorIs(t, velox.NewConstraintError("x", errors.New("x")), velox.ErrCheck)
	_, err = client.User.Get(ctx, db, 404)
	assert.NotErrorIs(t, err, velox.ErrUnique)
}

// Two serializable transactions that each read what the other writes: the
// database fails one of them and asks for it to be tried again. Postgres
// only: SQLite runs one writer at a time.
func TestIsRetryable_SerializationFailure(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	if db.Dialect() != "postgres" {
		t.Skip("needs a database with concurrent serializable transactions")
	}
	opts := &stdsql.TxOptions{Isolation: stdsql.LevelSerializable}
	t1, err := db.BeginTx(ctx, opts)
	require.NoError(t, err)
	defer func() { _ = t1.Rollback() }()
	t2, err := db.BeginTx(ctx, opts)
	require.NoError(t, err)
	defer func() { _ = t2.Rollback() }()
	var errs []error
	for i, tx := range []*velox.Tx{t1, t2} {
		_, err := client.User.Query(tx).Count(ctx)
		require.NoError(t, err)
		name := fmt.Sprintf("u%d", i)
		_, err = client.User.Create(tx).Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(20)).Save(ctx)
		errs = append(errs, err)
	}
	errs = append(errs, t1.Commit(), t2.Commit())
	var failed error
	for _, err := range errs {
		if err != nil {
			require.Nil(t, failed, "more than one statement failed: %v, %v", failed, err)
			failed = err
		}
	}
	require.Error(t, failed, "both serializable transactions committed")
	assert.True(t, velox.IsRetryable(failed), "not retryable: %v", failed)
	assert.False(t, velox.IsConstraintError(failed))
}
