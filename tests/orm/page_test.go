package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox/tests/orm/velox/predicate"

	client "github.com/syssam/velox/tests/orm/velox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/tests/orm/velox/entity"
	"github.com/syssam/velox/tests/orm/velox/user"
	testschema "github.com/syssam/velox/testschema"
)

func intp(n int) *int { return &n }

// nodes are the rows of a page.
func nodes(c *entity.UserConnection) []*entity.User {
	out := make([]*entity.User, len(c.Edges))
	for i, e := range c.Edges {
		out[i] = e.Node
	}
	return out
}

// Walking a query page by page, in either direction, visits exactly the rows
// of the query in its order, whatever the order: ties on the leading columns,
// descending columns, a time column.
func TestPaginate_WalkEqualsQuery(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	for i := range 23 {
		newUser(t, db, fmt.Sprintf("u%02d", i), 20+i%4) // ages tie in groups
	}
	orders := map[string][]predicate.UserOrderTerm{
		"id only":            nil,
		"age asc":            {user.Age.Asc()},
		"age desc, name asc": {user.Age.Desc(), user.Name.Asc()},
		"created_at desc":    {user.CreatedAt.Desc()},
		"id desc":            {user.ID.Desc()},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			q := func() *entity.UserQuery { return client.User.Query(db).Where(user.Age.GTE(21)).OrderBy(order...) }
			// The reference: the whole query, ordered with the id as tie-break.
			want, err := q().OrderBy(user.ID.Asc()).IDs(ctx)
			if name == "id desc" {
				want, err = q().IDs(ctx)
			}
			require.NoError(t, err)
			require.NotEmpty(t, want)

			for _, size := range []int{1, 4, 7, 100} {
				var got []int
				var after *velox.Cursor
				for pages := 0; ; pages++ {
					require.Less(t, pages, 50, "pagination does not end")
					p, err := q().Paginate(ctx, after, intp(size), nil, nil)
					require.NoError(t, err)
					assert.Equal(t, len(want), p.TotalCount)
					assert.Equal(t, after != nil, p.PageInfo.HasPreviousPage)
					for _, e := range p.Edges {
						require.NotEmpty(t, e.Cursor)
						got = append(got, e.Node.ID)
					}
					if !p.PageInfo.HasNextPage {
						break
					}
					require.Len(t, p.Edges, size)
					require.Equal(t, p.Edges[size-1].Cursor, *p.PageInfo.EndCursor)
					after = p.PageInfo.EndCursor
				}
				assert.Equal(t, want, got, "forward, page size %d", size)

				got = nil
				var before *velox.Cursor
				for pages := 0; ; pages++ {
					require.Less(t, pages, 50, "pagination does not end")
					p, err := q().Paginate(orm.WithoutTotal(ctx), nil, nil, before, intp(size))
					require.NoError(t, err)
					assert.Equal(t, -1, p.TotalCount, "a total the caller did not want")
					assert.Equal(t, before != nil, p.PageInfo.HasNextPage)
					ids := make([]int, len(p.Edges))
					for i, e := range p.Edges {
						ids[i] = e.Node.ID
					}
					got = append(ids, got...)
					if !p.PageInfo.HasPreviousPage {
						break
					}
					require.Equal(t, p.Edges[0].Cursor, *p.PageInfo.StartCursor)
					before = p.PageInfo.StartCursor
				}
				assert.Equal(t, want, got, "backward, page size %d", size)
			}

			// Every row's own cursor continues from that row.
			all, err := q().Paginate(ctx, nil, nil, nil, nil)
			require.NoError(t, err)
			require.Len(t, all.Edges, len(want))
			mid := len(want) / 2
			rest, err := q().Paginate(ctx, &all.Edges[mid].Cursor, nil, nil, nil)
			require.NoError(t, err)
			require.Len(t, rest.Edges, len(want)-mid-1)
			if len(rest.Edges) > 0 {
				assert.Equal(t, want[mid+1], rest.Edges[0].Node.ID)
			}
			// A window between two cursors.
			between, err := q().Paginate(ctx, &all.Edges[0].Cursor, nil, &all.Edges[len(want)-1].Cursor, nil)
			require.NoError(t, err)
			assert.Len(t, between.Edges, len(want)-2)
		})
	}
}

// An order column may hold NULL: the walk passes the NULL rows where the
// database sorts them, in both directions and whichever way the column is
// ordered. (A cursor on a NULL value used to be an error.)
func TestPaginate_NullableOrderColumn(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	for i := range 17 {
		u := newUser(t, db, fmt.Sprintf("u%02d", i), 20+i)
		if i%3 != 0 { // a third of the rows keep a NULL nickname; the rest tie in pairs
			_, err := client.User.UpdateByID(db, u.ID).Set(user.Nickname.Set(fmt.Sprintf("n%d", i/2))).SaveOne(ctx)
			require.NoError(t, err)
		}
	}
	for name, order := range map[string]predicate.UserOrderTerm{"asc": user.Nickname.Asc(), "desc": user.Nickname.Desc()} {
		t.Run(name, func(t *testing.T) {
			q := func() *entity.UserQuery { return client.User.Query(db).OrderBy(order) }
			want, err := q().OrderBy(user.ID.Asc()).IDs(ctx)
			require.NoError(t, err)
			require.Len(t, want, 17)
			for _, size := range []int{1, 3, 5} {
				var got []int
				var after *velox.Cursor
				for pages := 0; ; pages++ {
					require.Less(t, pages, 40, "pagination does not end")
					p, err := q().Paginate(ctx, after, intp(size), nil, nil)
					require.NoError(t, err)
					for _, e := range p.Edges {
						got = append(got, e.Node.ID)
					}
					if !p.PageInfo.HasNextPage {
						break
					}
					after = p.PageInfo.EndCursor
				}
				assert.Equal(t, want, got, "forward, page size %d", size)

				got = nil
				var before *velox.Cursor
				for pages := 0; ; pages++ {
					require.Less(t, pages, 40, "pagination does not end")
					p, err := q().Paginate(ctx, nil, nil, before, intp(size))
					require.NoError(t, err)
					ids := make([]int, len(p.Edges))
					for i, e := range p.Edges {
						ids[i] = e.Node.ID
					}
					got = append(ids, got...)
					if !p.PageInfo.HasPreviousPage {
						break
					}
					before = p.PageInfo.StartCursor
				}
				assert.Equal(t, want, got, "backward, page size %d", size)
			}
		})
	}
}

func TestPaginate_ArgsCursorsAndPolicy(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	newUser(t, db, "cat", 50)

	_, err := client.User.Query(db).Paginate(ctx, nil, intp(1), nil, intp(1))
	require.Error(t, err)
	_, err = client.User.Query(db).Paginate(ctx, nil, intp(-1), nil, nil)
	require.Error(t, err)
	bad := velox.Cursor("not a cursor")
	_, err = client.User.Query(db).Paginate(ctx, &bad, nil, nil, nil)
	assert.ErrorIs(t, err, velox.ErrBadCursor)

	// A cursor is only good for the ordering that made it.
	byAge, err := client.User.Query(db).OrderBy(user.Age.Asc()).Paginate(ctx, nil, intp(1), nil, nil)
	require.NoError(t, err)
	_, err = client.User.Query(db).OrderBy(user.Name.Asc()).Paginate(ctx, byAge.PageInfo.EndCursor, nil, nil, nil)
	assert.ErrorIs(t, err, velox.ErrBadCursor)

	// first: 0 asks for no rows, and still answers the total.
	p, err := client.User.Query(db).Paginate(ctx, nil, intp(0), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, p.Edges)
	assert.Equal(t, 3, p.TotalCount)
	assert.Nil(t, p.PageInfo.EndCursor)
	assert.Nil(t, p.PageInfo.StartCursor)

	// Limit and Offset of the query do not leak into a page.
	p, err = client.User.Query(db).Limit(1).Offset(1).Paginate(ctx, nil, intp(2), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann", "bob"}, names(nodes(p)))
	assert.True(t, p.PageInfo.HasNextPage)

	// The policy narrows the page and its total; eager loads come with it.
	only := testschema.FilterUserQueryToNameContext(ctx, "bob")
	p, err = client.User.Query(db).WithPosts().Paginate(only, nil, intp(10), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, names(nodes(p)))
	assert.Equal(t, 1, p.TotalCount)
	_, err = p.Edges[0].Node.Edges.PostsOrErr()
	assert.NoError(t, err)

	// A cursor as a GraphQL engine reads and writes it.
	var buf strings.Builder
	p.Edges[0].Cursor.MarshalGQL(&buf)
	var back velox.Cursor
	require.NoError(t, back.UnmarshalGQL(strings.Trim(buf.String(), `"`)))
	assert.Equal(t, p.Edges[0].Cursor, back)
	assert.ErrorIs(t, back.UnmarshalGQL(7), velox.ErrBadCursor)
}

// A list endpoint that takes its filter and order as field names: nothing
// here knows it is talking to GraphQL, REST or anything else.
func TestFieldPredicateAndOrder(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	newUser(t, db, "cat", 50)

	list := func(filters [][3]any, orderBy string, desc bool) ([]string, error) {
		q := client.User.Query(db)
		for _, f := range filters {
			p, err := orm.FieldPredicate(&entity.UserMeta, f[0].(string), f[1].(orm.Op), f[2])
			if err != nil {
				return nil, err
			}
			q.Where(predicate.User(p))
		}
		o, err := orm.FieldOrder(&entity.UserMeta, orderBy, desc)
		if err != nil {
			return nil, err
		}
		us, err := q.OrderBy(predicate.UserOrderTerm(o)).All(ctx)
		return names(us), err
	}

	got, err := list([][3]any{{"age", orm.OpGTE, 40}}, "name", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"cat", "bob"}, got)
	// A JSON number arrives as float64; 40.0 is 40, 40.5 is not an age.
	got, err = list([][3]any{{"age", orm.OpEQ, float64(40)}}, "id", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, got)
	_, err = list([][3]any{{"age", orm.OpEQ, 40.5}}, "id", false)
	require.Error(t, err)
	got, err = list([][3]any{{"name", orm.OpIn, []any{"ann", "cat"}}, {"name", orm.OpHasPrefix, "c"}}, "id", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"cat"}, got)
	got, err = list([][3]any{{"nickname", orm.OpIsNil, nil}}, "id", false)
	require.NoError(t, err)
	assert.Len(t, got, 3)

	// What a request must not be able to do.
	_, err = list([][3]any{{"no_such_field", orm.OpEQ, 1}}, "id", false)
	require.ErrorContains(t, err, "unknown User field")
	_, err = list([][3]any{{"age", orm.OpEQ, "forty"}}, "id", false)
	require.Error(t, err)
	_, err = list([][3]any{{"age", orm.OpContains, "4"}}, "id", false)
	require.Error(t, err, "a text operator on a number")
	_, err = list([][3]any{{"name", orm.OpIn, "ann"}}, "id", false)
	require.Error(t, err)
	_, err = list(nil, "no_such_field", false)
	require.Error(t, err)
	_, err = list([][3]any{{"name", orm.OpHasEdge, nil}}, "id", false)
	require.Error(t, err)
}
