package tests

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/vx/orm"
	client "github.com/syssam/velox/vx/tests/db"
	"github.com/syssam/velox/vx/tests/db/entity"
	"github.com/syssam/velox/vx/tests/db/filter"
	"github.com/syssam/velox/vx/tests/db/like"
	"github.com/syssam/velox/vx/tests/db/post"
	"github.com/syssam/velox/vx/tests/db/predicate"
	"github.com/syssam/velox/vx/tests/db/user"
)

// What an independent review of the runtime found. Each test is the case as
// it was reported, failing before the fix.

// A cursor that says NULL for a key that cannot be NULL was not made here.
func TestPaginate_NullCursorForAKeyThatCannotBeNull(t *testing.T) {
	db, _ := open(t)
	bad := orm.Cursor(base64.RawURLEncoding.EncodeToString([]byte(`{"k":["id"],"v":[null]}`)))
	_, err := client.User.Query(db).Paginate(context.Background(), &bad, intp(1), nil, nil)
	require.ErrorIs(t, err, orm.ErrBadCursor)
}

// The default of a join row's column is written like any other value: a
// time without its monotonic clock reading, which on SQLite was stored as
// text nothing read back could equal, so that paging by it never advanced.
func TestJoinRowDefaultIsStoredLikeAnyOtherValue(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann := newUser(t, db, "ann", 30)
	p1, p2, p3 := newPost(t, db, ann, "p1"), newPost(t, db, ann, "p2"), newPost(t, db, ann, "p3")
	_, err := client.User.UpdateByID(db, ann.ID).Set(user.LikedPosts.Add(p1.ID, p2.ID, p3.ID)).SaveOne(ctx)
	require.NoError(t, err)
	var seen []int
	var after *orm.Cursor
	for page := 0; page < 6; page++ {
		conn, err := client.Like.Query(db).OrderBy(like.LikedAt.Asc()).Paginate(ctx, after, intp(1), nil, nil)
		require.NoError(t, err)
		for _, e := range conn.Edges {
			seen = append(seen, e.Node.ID)
		}
		if !conn.PageInfo.HasNextPage {
			break
		}
		after = conn.PageInfo.EndCursor
	}
	assert.Len(t, seen, 3, "the walk by liked_at: %v", seen)
}

// The entity a create returns holds what was stored, so a guard on one of
// its times matches the row; and a time given to a condition is compared
// without its monotonic clock reading.
func TestTimesAreComparedAsStored(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	read, err := client.User.Get(ctx, db, u.ID)
	require.NoError(t, err)
	assert.True(t, read.UpdatedAt.Equal(u.UpdatedAt), "created with %v, stored %v", u.UpdatedAt, read.UpdatedAt)
	// The optimistic-lock guard, with the time of the entity just created.
	_, err = client.User.UpdateByID(db, u.ID).Where(user.UpdatedAt.EQ(u.UpdatedAt)).Set(user.Age.Add(1)).SaveOne(ctx)
	require.NoError(t, err)

	// With a monotonic reading, and with digits below the microsecond
	// (which a clock may or may not give: darwin's does not, linux's does).
	// The first version of this test used time.Now() alone, passed on the
	// machine it was written on, and the row was not found by its own time
	// wherever the clock had nanoseconds.
	for i, at := range []time.Time{time.Now(), time.Now().Add(789 * time.Nanosecond), time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)} {
		p, err := client.Post.Create(db).Set(post.Title.Set("t"), post.Author.Set(u.ID), post.CreatedAt.Set(at)).Save(ctx)
		require.NoError(t, err)
		for name, cond := range map[string]predicate.Post{
			"EQ": post.CreatedAt.EQ(at), "GTE": post.CreatedAt.GTE(at), "LTE": post.CreatedAt.LTE(at), "In": post.CreatedAt.In(at),
		} {
			n, err := client.Post.Query(db).Where(post.ID.EQ(p.ID), cond).Count(ctx)
			require.NoError(t, err, name)
			assert.Equal(t, 1, n, "time %d, %s", i, name)
		}
		for name, cond := range map[string]predicate.Post{"NEQ": post.CreatedAt.NEQ(at), "GT": post.CreatedAt.GT(at), "LT": post.CreatedAt.LT(at)} {
			n, err := client.Post.Query(db).Where(post.ID.EQ(p.ID), cond).Count(ctx)
			require.NoError(t, err, name)
			assert.Equal(t, 0, n, "time %d, %s", i, name)
		}
		// The guard of an optimistic lock, with the time that was given.
		_, err = client.Post.UpdateByID(db, p.ID).Where(post.CreatedAt.EQ(at)).Set(post.Title.Set("u")).SaveOne(ctx)
		require.NoError(t, err, "time %d", i)
	}
}

// An interceptor that returns nothing: no rows, and a count of none.
func TestInterceptorReturningNil(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	db.Intercept(velox.InterceptFunc(func(velox.Querier) velox.Querier {
		return velox.QuerierFunc(func(context.Context, velox.Query) (velox.Value, error) { return nil, nil })
	}))
	users, err := client.User.Query(db).All(ctx)
	require.NoError(t, err)
	require.Empty(t, users)
	n, err := client.User.Query(db).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

// An order input that names something the entity is not ordered by is an
// error, and never a column tried by name (whose values a page's cursors
// would then hand out).
func TestOrderTerm_UnknownField(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	for _, field := range []post.OrderField{"content", "NOPE"} {
		term := (&entity.PostOrder{Field: field}).Term()
		_, err := client.Post.Query(db).OrderBy(term).All(ctx)
		require.ErrorContains(t, err, "Post cannot be ordered by", string(field))
		_, err = client.Post.Query(db).OrderBy(term).Paginate(ctx, nil, intp(1), nil, nil)
		require.ErrorContains(t, err, "Post cannot be ordered by", string(field))
	}
}

// An operand of the wrong kind fails the query; it used to become an empty
// pattern, which matches every row. A value of a type declared on string is
// a string.
func TestConditionOperands(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	type label string
	count := func(p orm.P) (int, error) { return client.User.Query(db).Where(predicate.User(p)).Count(ctx) }

	n, err := count(orm.P{Op: orm.OpContains, Col: "name", Val: label("zzz")})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a named string that matches nothing")
	n, err = count(orm.P{Op: orm.OpHasPrefix, Col: "name", Val: label("an")})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = count(orm.P{Op: orm.OpContains, Col: "name", Val: 7})
	require.ErrorContains(t, err, `the condition on "name" takes a string`)
	n, err = count(orm.P{Op: orm.OpIn, Col: "name", Val: []label{"ann", "nobody"}})
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a list of another slice type")
	_, err = count(orm.P{Op: orm.OpIn, Col: "name", Val: "ann"})
	require.ErrorContains(t, err, `the condition on "name" takes a list`)
}

// An update with nothing to write still says how many rows it is of, so
// that orm.One tells a row that is there from one that is not.
func TestUpdateWithNothingToWrite(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	require.NoError(t, orm.One(client.User.UpdateByID(db, u.ID).Set(user.UpdatedAt.Keep()).Exec(ctx)))
	require.NoError(t, orm.One(client.User.UpdateByID(db, u.ID).Fill(struct{}{}).Exec(ctx)))
	require.ErrorIs(t, orm.One(client.User.UpdateByID(db, 9999).Set(user.UpdatedAt.Keep()).Exec(ctx)), orm.ErrNotFound)
	n, err := client.User.Update(db).Where(user.Age.GT(35)).Set(user.UpdatedAt.Keep()).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the rows the conditions match")
}

// Keep after Set on one column: the last one wins, as between two Sets, and
// what a hook is told agrees with what is written.
func TestUpdate_KeepAfterSet(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	_, err := client.User.UpdateByID(db, u.ID).Set(user.UpdatedAt.Set(past)).SaveOne(ctx)
	require.NoError(t, err)
	var told bool
	db.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			_, told = m.Field("updated_at")
			return next.Mutate(ctx, m)
		})
	})
	forged := past.Add(-time.Hour)
	got, err := client.User.UpdateByID(db, u.ID).Set(user.Age.Set(31), user.UpdatedAt.Set(forged), user.UpdatedAt.Keep()).SaveOne(ctx)
	require.NoError(t, err)
	assert.False(t, told, "the hook was told updated_at is set")
	assert.True(t, got.UpdatedAt.Equal(past), "updated_at is %v: kept would be %v, the value set before Keep %v", got.UpdatedAt, past, forged)
}

// An interceptor that hands back something else than the rows is an error,
// not no rows.
func TestInterceptorReturningSomethingElse(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	db.Intercept(velox.InterceptFunc(func(next velox.Querier) velox.Querier {
		return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
			if _, err := next.Query(ctx, q); err != nil {
				return nil, err
			}
			return "something else", nil
		})
	}))
	_, err := client.User.Query(db).All(ctx)
	require.ErrorContains(t, err, "an interceptor returned string")
	_, err = client.User.Query(db).Count(ctx)
	require.ErrorContains(t, err, "an interceptor returned string")
	_, err = client.User.Query(db).IDs(ctx)
	require.ErrorContains(t, err, "an interceptor returned string")
}

func TestRawSQLWithoutAHandle(t *testing.T) {
	ctx := context.Background()
	_, err := orm.ExecSQL(ctx, orm.ExecutorFrom(ctx), "SELECT 1")
	require.ErrorIs(t, err, orm.ErrNoExecutor)
	var tx *orm.Tx
	_, err = orm.QuerySQL(ctx, tx, "SELECT 1")
	require.ErrorIs(t, err, orm.ErrNoExecutor)
}

// The zero predicate inside an edge condition of a delete or an update is a
// forgotten condition like any other.
func TestMutation_ZeroPredicateInsideAnEdgeCondition(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	u := newUser(t, db, "ann", 30)
	newPost(t, db, u, "p1")
	_, err := client.Post.Delete(db).Where(post.Author.HasWith(predicate.User{})).Exec(ctx)
	require.ErrorContains(t, err, "zero predicate")
	n, err := client.Post.Query(db).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "nothing was deleted")
	_, err = client.Post.Update(db).Where(post.Author.HasWith(user.Or(predicate.User{}))).Set(post.Title.Set("x")).Exec(ctx)
	require.ErrorContains(t, err, "zero predicate")
}

// A where input given as a value, and not by its address, is a mistake of
// the caller's: it must not read as "no condition".
func TestWhereInput_ByValue(t *testing.T) {
	db, _ := open(t)
	p := orm.Where(filter.UserWhereInput{Name: ptr("ann")}, nil)
	assert.False(t, p.IsZero())
	_, err := client.User.Query(db).Where(predicate.User(p)).Count(context.Background())
	require.ErrorContains(t, err, "given by its address")
}

// The depth limit of a where input is counted through its edges: it used to
// start again at zero on the other side of each has...With, so that inputs
// which filter through each other nested without bound.
func TestWhereInput_DepthThroughAnEdge(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	nestedPosts := func(n int) *filter.PostWhereInput {
		w := &filter.PostWhereInput{Title: ptr("x")}
		for i := 0; i < n; i++ {
			w = &filter.PostWhereInput{Not: w}
		}
		return w
	}
	nestedUsers := func(n int, inner *filter.UserWhereInput) *filter.UserWhereInput {
		for i := 0; i < n; i++ {
			inner = &filter.UserWhereInput{Not: inner}
		}
		return inner
	}
	// Within the limit, across the edge.
	ok := nestedUsers(2, &filter.UserWhereInput{HasPostsWith: []*filter.PostWhereInput{nestedPosts(2)}})
	_, err := client.User.Query(db).Where(ok.P()).Count(ctx)
	require.NoError(t, err)
	// Four levels, the edge, and five more: each side alone is allowed.
	deep := nestedUsers(4, &filter.UserWhereInput{HasPostsWith: []*filter.PostWhereInput{nestedPosts(5)}})
	_, err = client.User.Query(db).Where(deep.P()).Count(ctx)
	require.ErrorIs(t, err, orm.ErrWhereDepth)
	// An empty input on the other side is still refused.
	empty := &filter.UserWhereInput{HasPostsWith: []*filter.PostWhereInput{{}}}
	_, err = client.User.Query(db).Where(empty.P()).Count(ctx)
	require.ErrorContains(t, err, "empty PostWhereInput")
}

// A bulk create takes its builders as they are when the rows are inserted:
// what is set on one after CreateBulk was called used to be lost (the
// builder's assignments were copied when the bulk was made), where velox
// and Ent keep the builders.
func TestCreateBulk_BuildersCompletedAfterwards(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	a := client.User.Create(db).Set(user.Name.Set("a"), user.Email.Set("a@x"))
	b := client.User.Create(db).Set(user.Name.Set("b"), user.Email.Set("b@x"))
	bulk := client.User.CreateBulk(db, a, b)
	a.Set(user.Age.Set(41))
	b.Set(user.Age.Set(42))
	users, err := bulk.Save(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	require.Equal(t, 41, users[0].Age)
	require.Equal(t, 42, users[1].Age)
}

// A page of a locking query locks its rows: the page is read by a query
// built from the caller's, which used to leave the lock behind.
func TestPaginate_KeepsTheLock(t *testing.T) {
	mdb, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mdb.Close() })
	db := orm.OpenDB("postgres", mdb)
	mock.ExpectQuery(`FROM "users".*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = client.User.Query(db).ForUpdate().Paginate(orm.WithoutTotal(context.Background()), nil, intp(2), nil, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
