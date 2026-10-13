package tests

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/syssam/velox/tests/orm/velox/predicate"

	client "github.com/syssam/velox/tests/orm/velox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/tests/orm/velox/entity"
	"github.com/syssam/velox/tests/orm/velox/post"
	"github.com/syssam/velox/tests/orm/velox/tag"
	"github.com/syssam/velox/tests/orm/velox/user"
	testschema "github.com/syssam/velox/testschema"
)

func TestByID_GetUpdateOneDeleteOne(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	assert.Equal(t, "User", user.ModelName)

	got, err := client.User.Get(ctx, db, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "ann", got.Name)
	_, err = client.User.Get(ctx, db, u.ID+99)
	assert.ErrorIs(t, err, velox.ErrNotFound)

	// Get is a plain function: it can be handed to a generic helper.
	fetch := func(get func(context.Context, velox.Executor, int) (*entity.User, error)) (*entity.User, error) {
		return get(ctx, db, u.ID)
	}
	_, err = fetch(client.User.Get)
	require.NoError(t, err)

	up, err := client.User.UpdateByID(db, u.ID).Set(user.Age.Set(31)).SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, 31, up.Age)
	_, err = client.User.UpdateByID(db, u.ID+99).Set(user.Age.Set(1)).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)

	// A missing row is an error for DeleteOne; Delete().Exec reports a count.
	assert.ErrorIs(t, client.User.DeleteByID(ctx, db, u.ID+99), velox.ErrNotFound)
	require.NoError(t, client.User.DeleteByID(ctx, db, u.ID))
	assert.ErrorIs(t, client.User.DeleteByID(ctx, db, u.ID), velox.ErrNotFound)
}

func TestSetNillable(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	nick, age := "annie", 44
	var noNick *string
	var noAge *int

	u, err := client.User.Create(db).
		Set(user.Name.Set("ann"), user.Email.Set("a@example.com"), user.Age.Set(30), user.Nickname.SetNillable(noNick)).
		Save(ctx)
	require.NoError(t, err)
	assert.Nil(t, u.Nickname, "SetNillable(nil) set something")

	u, err = client.User.UpdateByID(db, u.ID).Set(user.Nickname.SetNillable(&nick), user.Age.SetNillable(noAge)).SaveOne(ctx)
	require.NoError(t, err)
	require.NotNil(t, u.Nickname)
	assert.Equal(t, "annie", *u.Nickname)
	assert.Equal(t, 30, u.Age, "SetNillable(nil) changed the column")

	// SetNillable(nil) after Set does not undo it, and is not a clear.
	u, err = client.User.UpdateByID(db, u.ID).Set(user.Age.Set(age), user.Age.SetNillable(noAge), user.Nickname.SetNillable(noNick)).SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, 44, u.Age)
	assert.NotNil(t, u.Nickname)

	// In a bulk create, per row.
	us, err := client.User.CreateBulk(db,
		client.User.Create(db).Set(user.Name.Set("b1"), user.Email.Set("b1@example.com"), user.Age.Set(1), user.Nickname.SetNillable(&nick)),
		client.User.Create(db).Set(user.Name.Set("b2"), user.Email.Set("b2@example.com"), user.Age.Set(2), user.Nickname.SetNillable(noNick)),
	).Save(ctx)
	require.NoError(t, err)
	assert.NotNil(t, us[0].Nickname)
	assert.Nil(t, us[1].Nickname)
}

// The zero predicate is no condition, wherever it appears.
func TestZeroPredicate(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	var none predicate.User

	n, err := client.User.Query(db).Where(none).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = client.User.Query(db).Where(user.And(none, user.Age.GT(35))).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = client.User.Query(db).Where(user.Or(none, user.Age.GT(35))).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "no condition OR something is no condition")
	// In an update or a delete the zero predicate is refused, alone or nested:
	// a forgotten assignment must not become a statement over every row.
	_, err = client.User.Update(db).Set(user.Age.Add(1)).Where(none).Exec(ctx)
	require.ErrorContains(t, err, "zero predicate")
	_, err = client.User.Delete(db).Where(none).Exec(ctx)
	require.ErrorContains(t, err, "zero predicate")
	_, err = client.User.Delete(db).Where(user.Or(none, user.Age.GT(35))).Exec(ctx)
	require.ErrorContains(t, err, "zero predicate")
	_, err = client.User.UpdateByID(db, 1).Set(user.Age.Add(1)).Where(user.And(none)).SaveOne(ctx)
	require.ErrorContains(t, err, "zero predicate")
	n, err = client.User.Query(db).Where(user.Age.GTE(0)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a refused mutation wrote")
	// Every row is still reachable, by saying so: no Where.
	n, err = client.User.Update(db).Set(user.Age.Add(1)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// An operator the runtime does not know is an error, not a panic.
	_, err = client.User.Query(db).Where(predicate.User{Op: 250}).All(ctx)
	require.ErrorContains(t, err, "unknown predicate operator")
	_, err = client.User.Delete(db).Where(predicate.User{Op: 250}).Exec(ctx)
	require.Error(t, err)
	n, err = client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a delete with a broken condition deleted rows")
}

func TestSelectGroupAggregate(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	newPost(t, db, ann, "a1")
	newPost(t, db, ann, "a2")
	newPost(t, db, bob, "b1")

	var names []string
	require.NoError(t, client.User.Query(db).OrderBy(user.Name.Asc()).Select("name").Scan(ctx, &names))
	assert.Equal(t, []string{"ann", "bob"}, names)

	var rows []struct {
		AuthorID int `json:"user_posts"`
		N        int `json:"n"`
	}
	require.NoError(t, client.Post.Query(db).Select().GroupBy("user_posts").Aggregate(orm.As(orm.Count(), "n")).Scan(ctx, &rows))
	got := map[int]int{}
	for _, r := range rows {
		got[r.AuthorID] = r.N
	}
	assert.Equal(t, map[int]int{ann.ID: 2, bob.ID: 1}, got)

	n, err := client.User.Query(db).Where(user.Age.GT(35)).Select().Aggregate(orm.Count()).Int(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	maxAge, err := client.User.Query(db).Select().Aggregate(orm.Max(user.ColumnAge)).Int(ctx)
	require.NoError(t, err)
	assert.Equal(t, 40, maxAge)
	sum, err := client.User.Query(db).Select().Aggregate(orm.Sum(user.ColumnAge)).Int(ctx)
	require.NoError(t, err)
	assert.Equal(t, 70, sum)
	none, err := client.User.Query(db).Where(user.Age.GT(99)).Select().Aggregate(orm.Max(user.ColumnAge)).Int(ctx)
	require.NoError(t, err)
	assert.Zero(t, none)

	// A column that does not exist is refused before any SQL runs.
	err = client.User.Query(db).Select("nickname_typo").Scan(ctx, &names)
	require.ErrorContains(t, err, `no column "nickname_typo"`)
	err = client.User.Query(db).Select(string(user.Name)).GroupBy("nope").Scan(ctx, &names)
	require.ErrorContains(t, err, `no column "nope"`)

	// The policy applies to a selection as to any read.
	names = nil
	require.NoError(t, client.User.Query(db).Select("name").Scan(testschema.FilterUserQueryToNameContext(ctx, "ann"), &names))
	assert.Equal(t, []string{"ann"}, names)
}

func TestRawAndCompile(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, _ := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	newPost(t, db, ann, "a1")

	// A condition the typed API cannot say: users that authored a post,
	// written against the SQL builder.
	authors := predicate.User(orm.Raw(func(s *sql.Selector) {
		p := sql.Table(post.Table)
		s.Where(sql.In(s.C("id"), sql.Select(p.C("user_posts")).From(p)))
	}))
	us, err := client.User.Query(db).Where(authors, user.Age.LT(99)).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann"}, names(us))

	// A typed predicate handed to code that takes selector functions.
	db.Intercept(velox.TraverseFunc(func(_ context.Context, q velox.Query) error {
		if oq, ok := q.(*orm.Query); ok && oq.Type() == "User" {
			oq.WhereP(orm.Compile(user.Or(user.Age.GT(35), user.Name.EQ("nobody"))))
		}
		return nil
	}))
	us, err = client.User.Query(db).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, names(us))
}

func TestExecAndQuerySQL(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)

	// A statement issued through a transaction is part of it.
	err := db.RunTx(ctx, func(tx *velox.Tx) error {
		if _, err := orm.ExecSQL(ctx, tx, "UPDATE users SET age = age + 1"); err != nil {
			return err
		}
		rows, err := orm.QuerySQL(ctx, tx, "SELECT age FROM users")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		require.True(t, rows.Next())
		var age int
		require.NoError(t, rows.Scan(&age))
		assert.Equal(t, 31, age, "the query did not see the transaction's own write")
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	got, err := client.User.Get(ctx, db, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 30, got.Age, "the raw statement was not rolled back with its transaction")

	res, err := orm.ExecSQL(ctx, db, "UPDATE users SET age = age + 1")
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}

func TestLocks(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	newPost(t, db, ann, "a1")

	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		u, err := client.User.Query(tx).Where(user.ID.EQ(ann.ID)).ForUpdate().Only(ctx)
		if err != nil {
			return err
		}
		_, err = client.User.UpdateByID(tx, u.ID).Set(user.Age.Set(u.Age + 1)).SaveOne(ctx)
		if err != nil {
			return err
		}
		// With an eager load, with DISTINCT (which Postgres does not allow
		// under a lock), with options, and shared.
		if _, err := client.User.Query(tx).WithPosts().Distinct().ForUpdate(orm.SkipLocked).All(ctx); err != nil {
			return err
		}
		if _, err := client.User.Query(tx).ForShare(orm.NoWait).All(ctx); err != nil {
			return err
		}
		// A count of a locking query does not lock (an aggregate cannot).
		_, err = client.User.Query(tx).ForUpdate().Count(ctx)
		return err
	}))
	got, err := client.User.Get(ctx, db, ann.ID)
	require.NoError(t, err)
	assert.Equal(t, 31, got.Age)
}

// The optimistic-lock shape: update the row with this id only if it still has
// the value read earlier. A row that another writer changed in between is
// "not found", and is not written; it used to be reported as updated.
func TestSaveOne_GuardIsPartOfTheUpdate(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)

	// The other writer strikes after the mutation started and before its
	// statement runs.
	strike := true
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if strike && m.Op().Is(velox.OpUpdateOne) {
				strike = false
				if _, err := orm.ExecSQL(ctx, m.(*orm.Mutation).Executor(), fmt.Sprintf("UPDATE users SET age = 99 WHERE id = %d", u.ID)); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	for _, guard := range [][]predicate.User{
		{user.ID.EQ(u.ID), user.Age.EQ(30)},    // id first
		{user.Age.EQ(30), user.ID.EQ(u.ID)},    // guard first
		{user.Name.EQ("ann"), user.Age.EQ(30)}, // no id at all
	} {
		strike = true
		_, err := orm.ExecSQL(ctx, db, fmt.Sprintf("UPDATE users SET age = 30, name = 'ann' WHERE id = %d", u.ID))
		require.NoError(t, err)
		_, err = client.User.Update(db).Set(user.Name.Set("changed")).Where(guard...).SaveOne(ctx)
		assert.ErrorIs(t, err, velox.ErrNotFound, "the guard no longer holds")
		got, err := client.User.Get(ctx, db, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "ann", got.Name, "a row that failed its guard was written")
		assert.Equal(t, 99, got.Age)
	}

	// With the guard holding, the row is updated and returned.
	strike = false
	got, err := client.User.Update(db).Set(user.Name.Set("changed")).Where(user.ID.EQ(u.ID), user.Age.EQ(99)).SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, "changed", got.Name)
}

// Iter visits the rows of a query one at a time and stops when the loop does.
func TestIter(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	for i := range 7 {
		newUser(t, db, fmt.Sprintf("u%d", i), 20+i)
	}
	var got []string
	for u, err := range client.User.Query(db).Where(user.Age.GTE(22)).OrderBy(user.Age.Asc()).Iter(ctx) {
		require.NoError(t, err)
		got = append(got, u.Name)
		if len(got) == 3 {
			break // the rows are released; the next statement must work
		}
	}
	assert.Equal(t, []string{"u2", "u3", "u4"}, got)
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 7, n)

	// The policy applies, an error arrives as the last pair, and an eager
	// load, which needs every row, is refused.
	got = nil
	for u, err := range client.User.Query(db).Iter(testschema.FilterUserQueryToNameContext(ctx, "u5")) {
		require.NoError(t, err)
		got = append(got, u.Name)
	}
	assert.Equal(t, []string{"u5"}, got)
	var last error
	for _, err := range client.User.Query(db).WithPosts().Iter(ctx) {
		last = err
	}
	require.ErrorContains(t, last, "cannot eager-load")
	for _, err := range client.User.Query(db).Where(predicate.User{Op: 250}).Iter(ctx) {
		last = err
	}
	require.ErrorContains(t, last, "unknown predicate operator")
}

// A query or mutation runs on the handle it was built from. One built from
// nothing, or from a nil transaction, is an error at the terminal, not a
// panic; the query that describes an eager load needs none.
func TestExecutorIsBoundAtTheHead(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	newPost(t, db, ann, "p1")

	var noTx *velox.Tx
	for name, ex := range map[string]velox.Executor{"nil": nil, "nil transaction": noTx} {
		_, err := client.User.Query(ex).All(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.Query(ex).Count(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.Query(ex).Paginate(ctx, nil, nil, nil, nil)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.Query(ex).Select().Aggregate(orm.Count()).Int(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		for _, err := range client.User.Query(ex).Iter(ctx) {
			require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		}
		_, err = client.User.Create(ex).Set(user.Name.Set("x")).Save(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.Update(ex).Set(user.Age.Set(1)).Exec(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.UpdateByID(ex, ann.ID).Set(user.Age.Set(1)).SaveOne(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.Delete(ex).Exec(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = client.User.CreateBulk(ex, client.User.Create(db).Set(user.Name.Set("x"))).Save(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
		_, err = ann.QueryPosts(ex).All(ctx)
		require.ErrorIs(t, err, velox.ErrNoExecutor, name)
	}
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a refused mutation wrote")

	// A builder is finished and run by whoever holds it; nothing but the
	// builder has to be passed along.
	build := func(ex velox.Executor) *entity.UserQuery { return client.User.Query(ex).Where(user.Age.GTE(18)) }
	run := func(q *entity.UserQuery) (int, error) { return q.OrderBy(user.ID.Asc()).Count(ctx) }
	n, err = run(build(db))
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// The rows of a bulk create come from the builders; the statement runs
	// on the handle CreateBulk was given.
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		_, err := client.User.CreateBulk(tx, client.User.Create(nil).Set(user.Name.Set("bob"), user.Email.Set("bob@example.com"), user.Age.Set(40))).Save(ctx)
		return err
	}))

	// The query given to Load describes the load: its conditions, order and
	// limit are used, and the load runs with the query it belongs to.
	users, err := client.User.Query(db).Where(user.ID.EQ(ann.ID)).
		WithPosts(client.Post.Query(nil).Where(post.Title.EQ("p1"))).All(ctx)
	require.NoError(t, err)
	require.Len(t, users[0].Edges.Posts, 1)
	// A nil query describes nothing: the load is the whole edge, as it is
	// without one.
	var none *entity.PostQuery
	for name, q := range map[string]*entity.UserQuery{
		"a nil *PostQuery": client.User.Query(db).WithPosts(none),
		"nil":              client.User.Query(db).WithPosts(nil),
	} {
		users, err = q.Where(user.ID.EQ(ann.ID)).All(ctx)
		require.NoError(t, err, name)
		require.Len(t, users[0].Edges.Posts, 1, name)
	}
	users, err = client.User.Query(db).Where(user.ID.EQ(ann.ID)).
		WithPosts(client.Post.Query(nil).Where(post.Title.EQ("no such post"))).All(ctx)
	require.NoError(t, err)
	require.Empty(t, users[0].Edges.Posts, "a description that is given is used")

	// The names of the columns, for the places that take a string.
	assert.Equal(t, "name", user.ColumnName)
	var names []string
	require.NoError(t, client.User.Query(db).OrderBy(user.Name.Asc()).Select(user.ColumnName).Scan(ctx, &names))
	assert.Equal(t, []string{"ann", "bob"}, names)
}

// A row written from an input struct goes through the same path as one
// written with setters: velox reads the same row, hooks see the fields,
// validators and the policy decide.
func TestFill_SamePathAsSetters(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)

	var seen []string
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if m.Type() == "User" {
				seen = m.Fields()
			}
			return next.Mutate(ctx, m)
		})
	})
	type input struct {
		Name, Email string
		Age         *int
		Nickname    orm.Opt[string]
	}
	age := 30
	u, err := client.User.Create(db).Fill(input{Name: "ann", Email: "ann@example.com", Age: &age, Nickname: orm.Some("annie")}).Save(ctx)
	require.NoError(t, err)
	for _, f := range []string{"name", "email", "age", "nickname"} {
		assert.Contains(t, seen, f, "the hook did not see a field of the input")
	}
	r := ref.User.GetX(ctx, u.ID)
	assert.Equal(t, "ann", r.Name)
	assert.Equal(t, 30, r.Age)
	require.NotNil(t, r.Nickname)
	assert.Equal(t, "annie", *r.Nickname)
	assert.Equal(t, string(u.Role), string(r.Role), "the default of a field the input does not have")

	// A partial update: the validator sees the value, null clears, and the
	// row's updated_at moves.
	zero := 0
	_, err = client.User.UpdateByID(db, u.ID).Fill(input{Age: &zero}, "Name", "Email").SaveOne(ctx)
	require.ErrorContains(t, err, "less than minimum", "the validator of age did not see the input's value")
	age = 31
	got, err := client.User.UpdateByID(db, u.ID).Fill(input{Age: &age, Nickname: orm.Null[string]()}, "Name", "Email").SaveOne(ctx)
	require.NoError(t, err)
	r = ref.User.GetX(ctx, u.ID)
	assert.Equal(t, 31, r.Age)
	assert.Nil(t, r.Nickname)
	assert.Equal(t, "ann", r.Name)
	assert.False(t, got.UpdatedAt.Before(u.UpdatedAt))
	assert.ElementsMatch(t, []string{"age", "updated_at"}, seen, "the hook saw more or less than the update writes")

	// Denied like any other mutation of the entity.
	enforce := testschema.EnforceUserPrivacyContext(ctx)
	_, err = client.User.Create(db).Fill(input{Name: "x", Email: "x@example.com", Age: &age}).Save(enforce)
	require.ErrorIs(t, err, privacy.Deny)
	_, err = client.User.UpdateByID(db, u.ID).Fill(input{Age: &age}, "Name", "Email").SaveOne(enforce)
	require.ErrorIs(t, err, privacy.Deny)
	n, err := ref.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

// A transaction can be started with options; a read-only one refuses writes
// where the database enforces it (Postgres), and reads everywhere.
func TestTxOptions(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	opts := &stdsql.TxOptions{ReadOnly: true}
	var n int
	err := db.RunTxOptions(ctx, opts, func(tx *velox.Tx) error {
		var err error
		n, err = client.User.Query(tx).Count(ctx)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	err = db.RunTxOptions(ctx, opts, func(tx *velox.Tx) error {
		_, err := client.User.Create(tx).Set(user.Name.Set("bob"), user.Email.Set("bob@example.com"), user.Age.Set(40)).Save(ctx)
		return err
	})
	if db.Dialect() == "postgres" {
		require.Error(t, err, "a read-only transaction wrote")
	}
	// The pattern of the Go database documentation: a deferred Rollback after
	// Commit does nothing.
	tx, err := db.BeginTx(ctx, &stdsql.TxOptions{Isolation: stdsql.LevelSerializable})
	require.NoError(t, err)
	defer func() { assert.ErrorIs(t, tx.Rollback(), velox.ErrTxDone) }()
	_, err = client.User.UpdateByID(tx, 1).Set(user.Age.Set(31)).SaveOne(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

// Keep leaves a column out of an update, and its update default with it:
// for a change that is not the user's, which must not move updated_at.
func TestUpdate_KeepSkipsTheUpdateDefault(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	past := ann.UpdatedAt.Add(-48 * time.Hour).Truncate(time.Second)
	_, err := client.User.UpdateByID(db, ann.ID).Set(user.UpdatedAt.Set(past)).SaveOne(ctx)
	require.NoError(t, err)

	// An ordinary update moves it.
	moved, err := client.User.UpdateByID(db, ann.ID).Set(user.Age.Set(31)).SaveOne(ctx)
	require.NoError(t, err)
	assert.True(t, moved.UpdatedAt.After(past), "the update default did not apply")

	_, err = client.User.UpdateByID(db, ann.ID).Set(user.UpdatedAt.Set(past)).SaveOne(ctx)
	require.NoError(t, err)
	kept, err := client.User.UpdateByID(db, ann.ID).Set(user.Age.Set(32), user.UpdatedAt.Keep()).SaveOne(ctx)
	require.NoError(t, err)
	assert.Equal(t, 32, kept.Age)
	assert.True(t, kept.UpdatedAt.Equal(past), "updated_at moved from %v to %v", past, kept.UpdatedAt)

	// A hook does not see a kept column as set, and a bulk update keeps too.
	var fields []string
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			fields = m.Fields()
			return next.Mutate(ctx, m)
		})
	})
	n, err := client.User.Update(db).Where(user.ID.EQ(ann.ID)).Set(user.Age.Add(1), user.UpdatedAt.Keep()).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NotContains(t, fields, "updated_at")
	again, err := client.User.Get(ctx, db, ann.ID)
	require.NoError(t, err)
	assert.Equal(t, 33, again.Age)
	assert.True(t, again.UpdatedAt.Equal(past))

	// Set after Keep wins; a new row has nothing to keep.
	set, err := client.User.UpdateByID(db, ann.ID).Set(user.UpdatedAt.Keep(), user.UpdatedAt.Set(past.Add(time.Hour))).SaveOne(ctx)
	require.NoError(t, err)
	assert.True(t, set.UpdatedAt.Equal(past.Add(time.Hour)))
	_, err = client.User.Create(db).Set(user.Name.Set("bob"), user.Email.Set("bob@example.com"), user.UpdatedAt.Keep()).Save(ctx)
	require.ErrorContains(t, err, "only an update can do")
}

func TestEnumValues(t *testing.T) {
	assert.Equal(t, []user.RoleEnum{user.RoleAdmin, user.RoleUser, user.RoleGuest}, user.RoleValues())
}

// Columns reads the named columns into the entities and leaves the rest
// zero, together with what the query cannot do without.
func TestQuery_Columns(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	bob := newUser(t, db, "bob", 40)
	newPost(t, db, ann, "a1")
	newPost(t, db, bob, "b1")

	us, err := client.User.Query(db).Columns(user.ColumnName).OrderBy(user.Age.Desc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, us, 2)
	assert.Equal(t, "bob", us[0].Name)
	assert.Equal(t, bob.ID, us[0].ID, "the id is always read")
	assert.Equal(t, 40, us[0].Age, "what the query is ordered by is read")
	assert.Empty(t, us[0].Email, "a column that was not asked for")
	assert.True(t, us[0].CreatedAt.IsZero())

	// A page: the cursor is made of the order's columns.
	page, err := client.User.Query(db).Columns(user.ColumnEmail).OrderBy(user.Name.Asc()).Paginate(ctx, nil, intp(1), nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Edges, 1)
	assert.Equal(t, "ann@example.com", page.Edges[0].Node.Email)
	page, err = client.User.Query(db).Columns(user.ColumnEmail).OrderBy(user.Name.Asc()).Paginate(ctx, page.PageInfo.EndCursor, intp(1), nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Edges, 1)
	assert.Equal(t, "bob@example.com", page.Edges[0].Node.Email)

	// An edge stored in the entity's own table is loaded: its key is read.
	ps, err := client.Post.Query(db).Columns(post.ColumnTitle).WithAuthor().OrderBy(post.Title.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 2)
	require.NotNil(t, ps[0].Edges.Author)
	assert.Equal(t, "ann", ps[0].Edges.Author.Name)
	assert.Empty(t, ps[0].Status, "a column that was not asked for")
	// And one stored on the other side, from the id.
	withPosts, err := client.User.Query(db).Columns(user.ColumnName).WithPosts().OrderBy(user.Name.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, withPosts[0].Edges.Posts, 1)
	assert.Equal(t, "a1", withPosts[0].Edges.Posts[0].Title, "the neighbors are read whole")

	// One row, row by row, and a copy that does not share the choice.
	one, err := client.User.Query(db).Columns(user.ColumnName).Where(user.ID.EQ(ann.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann", one.Name)
	assert.Empty(t, one.Email)
	for u, err := range client.User.Query(db).Columns(user.ColumnName).Iter(ctx) {
		require.NoError(t, err)
		assert.NotEmpty(t, u.Name)
		assert.Empty(t, u.Email)
	}
	narrow := client.User.Query(db).Columns(user.ColumnName)
	wider := narrow.Clone().Columns(user.ColumnEmail)
	only, err := narrow.Where(user.ID.EQ(ann.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Empty(t, only.Email, "Columns on a clone reached the query it was cloned from")
	both, err := wider.Where(user.ID.EQ(ann.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann@example.com", both.Email)

	// The count and the ids are of the rows, whatever is read of them.
	n, err := client.User.Query(db).Columns(user.ColumnName).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// What cannot be done says so.
	_, err = client.User.Query(db).Columns("no_such_column").All(ctx)
	require.ErrorContains(t, err, `User has no column "no_such_column" to read`)
	// The neighbors of an eager load with only some of their columns: each
	// kind of edge keeps what it is matched to its parent by.
	loaded, err := client.User.Query(db).WithPosts(client.Post.Query(nil).Columns(post.ColumnTitle).OrderBy(post.Title.Asc())).OrderBy(user.Name.Asc()).All(ctx)
	require.NoError(t, err)
	require.Len(t, loaded[0].Edges.Posts, 1)
	assert.Equal(t, "a1", loaded[0].Edges.Posts[0].Title)
	assert.Empty(t, loaded[0].Edges.Posts[0].Status, "a column of the neighbor that was not asked for")
	require.Len(t, loaded[1].Edges.Posts, 1)
	assert.Equal(t, "b1", loaded[1].Edges.Posts[0].Title)
	// To one, the key on this side.
	authored, err := client.Post.Query(db).WithAuthor(client.User.Query(nil).Columns(user.ColumnName)).OrderBy(post.Title.Asc()).All(ctx)
	require.NoError(t, err)
	require.NotNil(t, authored[0].Edges.Author)
	assert.Equal(t, "ann", authored[0].Edges.Author.Name)
	assert.Empty(t, authored[0].Edges.Author.Email)
	// Through a join table, and nested under another load.
	tagged := must(client.Tag.Create(db).Set(tag.Name.Set("go")).Save(ctx))
	require.NoError(t, client.Post.UpdateByID(db, authored[0].ID).Set(post.Tags.Add(tagged.ID)).ExecOne(ctx))
	nested, err := client.User.Query(db).Where(user.ID.EQ(ann.ID)).
		WithPosts(client.Post.Query(nil).Columns(post.ColumnTitle).WithTags(client.Tag.Query(nil).Columns(tag.ColumnName))).Only(ctx)
	require.NoError(t, err)
	require.Len(t, nested.Edges.Posts, 1)
	require.Len(t, nested.Edges.Posts[0].Edges.Tags, 1)
	assert.Equal(t, "go", nested.Edges.Posts[0].Edges.Tags[0].Name)
	_, err = client.User.Query(db).WithPosts(client.Post.Query(nil).Columns("no_such_column")).All(ctx)
	require.ErrorContains(t, err, `Post has no column "no_such_column" to read`)
}

// Code written once for several entities: orm.Predicate as the constraint,
// a column as the plain column type of the level the code needs.
func between[T any, E orm.Predicate](col *orm.OrderedColumn[T, E], lo, hi T) []E {
	return []E{col.GTE(lo), col.LTE(hi)}
}

func named[P orm.Predicate](col *orm.StringColumn[string, P], prefix string, raw func(*sql.Selector)) P {
	parts := []P{col.HasPrefix(prefix)}
	if raw != nil {
		parts = append(parts, P(orm.Raw(raw)))
	}
	return orm.And(parts...)
}

func TestGenericOverEntities(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	newPost(t, db, ann, "a1")
	newPost(t, db, bob, "b1")

	// The predicate type is inferred from the column: these are a
	// predicate.User and a predicate.Post, and do not mix.
	us, err := client.User.Query(db).Where(between(user.Age.AsOrdered(), 35, 45)...).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, names(us))
	ps, err := client.Post.Query(db).Where(named(post.Title.AsString(), "a", nil)).All(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, "a1", ps[0].Title)
	us, err = client.User.Query(db).Where(named(user.Name.AsString(), "", sql.FieldGT("age", 35))).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, names(us))

	// And, Or and Not are the entity packages' own.
	for name, c := range map[string]struct{ generic, own predicate.User }{
		"and":           {orm.And(user.Age.GT(35), user.Name.EQ("bob")), user.And(user.Age.GT(35), user.Name.EQ("bob"))},
		"or":            {orm.Or(user.Age.GT(35), user.Name.EQ("ann")), user.Or(user.Age.GT(35), user.Name.EQ("ann"))},
		"not":           {orm.Not(user.Age.GT(35)), user.Not(user.Age.GT(35))},
		"or of nothing": {orm.Or[predicate.User](), user.Or()},
	} {
		assert.Equal(t, c.own, c.generic, name)
		a, err := client.User.Query(db).Where(c.generic).Count(ctx)
		require.NoError(t, err, name)
		b, err := client.User.Query(db).Where(c.own).Count(ctx)
		require.NoError(t, err, name)
		assert.Equal(t, b, a, name)
	}

	// A nullable column, and an enum's.
	isNull := func(c *orm.ValueColumn[string, predicate.User]) predicate.User { return c.IsNil() }
	assert.Equal(t, user.Nickname.IsNil(), isNull(user.Nickname.AsValue()))
	role := user.Role.AsEnum()
	n, err := client.User.Query(db).Where(role.In(user.RoleUser, user.RoleAdmin)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// The value a create writes for an enum field that is not set has a name:
// a function, since a second constant of the type would be a second name
// for one of its values.
func TestEnumDefaultConstant(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	assert.Equal(t, user.DefaultRole(), u.Role)
	p := newPost(t, db, u, "a1")
	got, err := client.Post.Get(ctx, db, p.ID)
	require.NoError(t, err)
	assert.Equal(t, post.DefaultStatus(), got.Status)
}

// BatchSize inserts the rows of a bulk create a few at a time. The entities
// come back in the order of the builders whatever the size, each batch is a
// bulk create of its own to the hooks, and the batches are one transaction
// only inside one.
func TestCreateBulk_BatchSize(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	mk := func(name string) *entity.UserCreate {
		return client.User.Create(db).Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(1))
	}
	rows := func(names ...string) []*entity.UserCreate {
		bs := make([]*entity.UserCreate, len(names))
		for i, n := range names {
			bs[i] = mk(n)
		}
		return bs
	}
	var log []string
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			name, _ := m.Field("name")
			log = append(log, fmt.Sprint("before ", name))
			v, err := next.Mutate(ctx, m)
			log = append(log, fmt.Sprint("after ", name))
			return v, err
		})
	})

	// Five rows two at a time: three statements, the last of one row. Every
	// batch's hooks have returned before the next batch's see their rows.
	us, err := client.User.CreateBulk(db, rows("a", "b", "c", "d", "e")...).BatchSize(2).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, names(us))
	for _, u := range us {
		assert.NotZero(t, u.ID)
	}
	assert.Equal(t, []string{
		"before a", "before b", "after b", "after a",
		"before c", "before d", "after d", "after c",
		"before e", "after e",
	}, log)

	// A size that is not one, or that holds every row, is one statement.
	for _, n := range []int{0, -1, 3, 4} {
		log = nil
		name := fmt.Sprintf("n%d", n+1)
		us, err = client.User.CreateBulk(db, rows(name+"x", name+"y", name+"z")...).BatchSize(n).Save(ctx)
		require.NoError(t, err, n)
		assert.Len(t, us, 3, n)
		assert.Equal(t, []string{"before " + name + "x", "before " + name + "y", "before " + name + "z"}, log[:3], n)
	}
	before, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)

	// A batch that fails, on a database handle: the batches before it are in
	// the table and are what Save returns with the error; the failed one and
	// those after it are not. Here the fourth row repeats an address.
	us, err = client.User.CreateBulk(db, rows("p", "q", "r", "a", "s", "t")...).BatchSize(2).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "%v", err)
	assert.Equal(t, []string{"p", "q"}, names(us))
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+2, n)
	n, err = client.User.Query(db).Where(user.Name.In("r", "s", "t")).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "a row of the failed batch, or of one after it")

	// The same in a transaction that is rolled back for the error: nothing.
	err = db.RunTx(ctx, func(tx *velox.Tx) error {
		return client.User.CreateBulk(tx, rows("v", "w", "x", "a")...).BatchSize(3).Exec(ctx)
	})
	require.True(t, velox.IsConstraintError(err), "%v", err)
	n, err = client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+2, n)

	// Without a size the whole create is one statement, and nothing of it
	// stays.
	us, err = client.User.CreateBulk(db, rows("y", "z", "a")...).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "%v", err)
	assert.Nil(t, us)
	n, err = client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+2, n)
}

// The zero predicate is no condition, and compiles to no function: what a
// predicate that was a function was when it was none. A query takes that for
// no condition. A mutation refuses it: a rule that meant to narrow a
// statement, and narrowed it by nothing, must not let it run on every row.
func TestCompile_NoCondition(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	assert.Nil(t, orm.Compile(predicate.User{}))
	assert.NotNil(t, orm.Compile(user.Age.GT(1)))

	var scope predicate.User // what a caller who sees every row is given
	db.Intercept(velox.TraverseFunc(func(_ context.Context, q velox.Query) error {
		q.(*orm.Query).WhereP(orm.Compile(scope))
		return nil
	}))
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	scope = user.Name.EQ("ann")
	n, err = client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var rule predicate.User
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if m.Op().Is(velox.OpUpdate | velox.OpDelete) {
				m.(*orm.Mutation).WhereP(orm.Compile(rule))
			}
			return next.Mutate(ctx, m)
		})
	})
	scope = predicate.User{}
	_, err = client.User.Update(db).Set(user.Age.Set(1)).Exec(ctx)
	require.ErrorContains(t, err, "WhereP of the User mutation was given no function")
	_, err = client.User.Delete(db).Where(user.Name.EQ("ann")).Exec(ctx)
	require.ErrorContains(t, err, "was given no function")
	n, err = client.User.Query(db).Where(user.Age.GT(1)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a statement the rule did not narrow ran")
	rule = user.Name.EQ("bob")
	changed, err := client.User.Update(db).Set(user.Age.Set(1)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
}

// A hook reads which rows a statement is about to touch: the conditions of
// the mutation, as functions a query of the entity takes.
func TestMutation_PredicatesFuncs(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, bob := newUser(t, db, "ann", 30), newUser(t, db, "bob", 40)
	cat := newUser(t, db, "cat", 50)
	var touched [][]int
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			om := m.(*orm.Mutation)
			if !m.Op().Is(velox.OpUpdate | velox.OpUpdateOne | velox.OpDelete) {
				return next.Mutate(ctx, m)
			}
			var ps []predicate.User
			for _, f := range om.PredicatesFuncs() {
				ps = append(ps, predicate.User(orm.Raw(f)))
			}
			ids, err := client.User.Query(om.Executor()).Where(ps...).OrderBy(user.ID.Asc()).IDs(ctx)
			if err != nil {
				return nil, err
			}
			touched = append(touched, ids)
			return next.Mutate(ctx, m)
		})
	})
	// Conditions of every kind a mutation has: typed, nested, through an
	// edge, and one a rule added as a function.
	newPost(t, db, bob, "b1")
	_, err := client.User.Update(db).Set(user.Age.Add(1)).Where(user.Or(user.Age.GT(45), user.Posts.Has())).Exec(ctx)
	require.NoError(t, err)
	_, err = client.User.UpdateByID(db, ann.ID).Set(user.Age.Set(31)).SaveOne(ctx)
	require.NoError(t, err)
	_, err = client.User.Delete(db).Where(user.Name.EQ("cat"), user.Name.EQ("bob")).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, [][]int{{bob.ID, cat.ID}, {ann.ID}, {}}, touched)
}

// A missing row says what was missing, in the words velox's did after its
// own prefix.
func TestNotFound_Text(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	_, err := client.User.Get(ctx, db, 404)
	require.ErrorIs(t, err, velox.ErrNotFound)
	assert.EqualError(t, err, "velox: user not found")
	_, err = client.User.Query(db).Where(user.Name.EQ("nobody")).Only(ctx)
	assert.EqualError(t, err, "velox: user not found")
	err = client.User.DeleteByID(ctx, db, 404)
	require.ErrorIs(t, err, velox.ErrNotFound)
	assert.EqualError(t, err, "velox: user not found")
	_, err = client.User.UpdateByID(db, 404).Set(user.Age.Set(1)).SaveOne(ctx)
	assert.EqualError(t, err, "velox: user not found")
	made := fmt.Errorf("loading: %w", velox.NewNotFoundError("price list"))
	assert.True(t, velox.IsNotFound(made))
	assert.EqualError(t, made, "loading: velox: price list not found")
}

// The generated root package gives the library's handle, errors and hook
// types under its own name, so that a file which starts its queries there
// needs one import. They are the library's, not copies: a value made with
// one package's name is what the other's names.
func TestRootPackage_GivesTheLibrarysNames(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)

	// One type: what is written with one package's name is passed where
	// the other's is asked for.
	generated := func(d *client.DB, ex client.Executor) (*client.DB, client.Executor) { return d, ex }
	library := func(tx *velox.Tx) *velox.Tx { return tx }
	conn, ex := generated(db, db)
	err := conn.RunTx(ctx, func(tx *client.Tx) error {
		_, err := client.User.Query(library(tx)).Count(ctx)
		return err
	})
	require.NoError(t, err)

	// One set of errors.
	_, err = client.User.Get(ctx, ex, 404)
	assert.True(t, client.IsNotFound(err))
	require.ErrorIs(t, err, client.ErrNotFound)
	var nf *client.NotFoundError
	require.ErrorAs(t, err, &nf)
	assert.Same(t, velox.ErrNotFound, client.ErrNotFound)
	assert.NoError(t, client.MaskNotFound(err))

	// The forwarding functions do what the library's do.
	assert.Equal(t, 0, client.Must(client.User.Query(ex).Count(ctx)))
	assert.Equal(t, velox.OpCreate, client.OpCreate)
	assert.Same(t, conn, client.ExecutorFrom(client.WithExecutor(ctx, conn)))
	assert.Panics(t, func() { client.Check(velox.ErrTxDone) })
	var hook client.Hook = func(next client.Mutator) client.Mutator { return next }
	conn.Use(hook)
}
