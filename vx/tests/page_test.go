package tests

import (
	"context"
	"fmt"
	"testing"

	"github.com/syssam/velox/vx/tests/db/predicate"

	client "github.com/syssam/velox/vx/tests/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testschema "github.com/syssam/velox/testschema"
	"github.com/syssam/velox/vx/orm"
	"github.com/syssam/velox/vx/tests/db/entity"
	"github.com/syssam/velox/vx/tests/db/user"
)

func intp(n int) *int { return &n }

// Walking a query page by page, in either direction, visits exactly the rows
// of the query in its order, whatever the order: ties on the leading columns,
// descending columns, a time column.
func TestPaginate_WalkEqualsQuery(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	for i := range 23 {
		newUser(t, db, fmt.Sprintf("u%02d", i), 20+i%4) // ages tie in groups
	}
	orders := map[string][]predicate.UserOrder{
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
				after := ""
				for pages := 0; ; pages++ {
					require.Less(t, pages, 50, "pagination does not end")
					p, err := q().Paginate(ctx, orm.PageArgs{First: intp(size), After: after, Total: true})
					require.NoError(t, err)
					assert.Equal(t, len(want), p.Total)
					assert.Equal(t, after != "", p.HasPrev)
					require.Len(t, p.Cursors, len(p.Items))
					for _, u := range p.Items {
						got = append(got, u.ID)
					}
					if !p.HasNext {
						break
					}
					require.Len(t, p.Items, size)
					after = p.EndCursor
				}
				assert.Equal(t, want, got, "forward, page size %d", size)

				got = nil
				before := ""
				for pages := 0; ; pages++ {
					require.Less(t, pages, 50, "pagination does not end")
					p, err := q().Paginate(ctx, orm.PageArgs{Last: intp(size), Before: before})
					require.NoError(t, err)
					assert.Equal(t, -1, p.Total, "a total that was not asked for")
					assert.Equal(t, before != "", p.HasNext)
					ids := make([]int, len(p.Items))
					for i, u := range p.Items {
						ids[i] = u.ID
					}
					got = append(ids, got...)
					if !p.HasPrev {
						break
					}
					before = p.StartCursor
				}
				assert.Equal(t, want, got, "backward, page size %d", size)
			}

			// Every row's own cursor continues from that row.
			all, err := q().Paginate(ctx, orm.PageArgs{})
			require.NoError(t, err)
			require.Len(t, all.Items, len(want))
			mid := len(want) / 2
			rest, err := q().Paginate(ctx, orm.PageArgs{After: all.Cursors[mid]})
			require.NoError(t, err)
			require.Len(t, rest.Items, len(want)-mid-1)
			if len(rest.Items) > 0 {
				assert.Equal(t, want[mid+1], rest.Items[0].ID)
			}
			// A window between two cursors.
			between, err := q().Paginate(ctx, orm.PageArgs{After: all.Cursors[0], Before: all.Cursors[len(want)-1]})
			require.NoError(t, err)
			assert.Len(t, between.Items, len(want)-2)
		})
	}
}

func TestPaginate_ArgsCursorsAndPolicy(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	newUser(t, db, "ann", 30)
	newUser(t, db, "bob", 40)
	newUser(t, db, "cat", 50)

	_, err := client.User.Query(db).Paginate(ctx, orm.PageArgs{First: intp(1), Last: intp(1)})
	require.Error(t, err)
	_, err = client.User.Query(db).Paginate(ctx, orm.PageArgs{First: intp(-1)})
	require.Error(t, err)
	_, err = client.User.Query(db).Paginate(ctx, orm.PageArgs{After: "not a cursor"})
	assert.ErrorIs(t, err, orm.ErrBadCursor)

	// A cursor is only good for the ordering that made it.
	byAge, err := client.User.Query(db).OrderBy(user.Age.Asc()).Paginate(ctx, orm.PageArgs{First: intp(1)})
	require.NoError(t, err)
	_, err = client.User.Query(db).OrderBy(user.Name.Asc()).Paginate(ctx, orm.PageArgs{After: byAge.EndCursor})
	assert.ErrorIs(t, err, orm.ErrBadCursor)

	// First: 0 asks for no rows, and still answers the total.
	p, err := client.User.Query(db).Paginate(ctx, orm.PageArgs{First: intp(0), Total: true})
	require.NoError(t, err)
	assert.Empty(t, p.Items)
	assert.Equal(t, 3, p.Total)
	assert.Equal(t, "", p.EndCursor)

	// Limit and Offset of the query do not leak into a page.
	p, err = client.User.Query(db).Limit(1).Offset(1).Paginate(ctx, orm.PageArgs{First: intp(2)})
	require.NoError(t, err)
	assert.Equal(t, []string{"ann", "bob"}, names(p.Items))
	assert.True(t, p.HasNext)

	// The policy narrows the page and its total; eager loads come with it.
	only := testschema.FilterUserQueryToNameContext(ctx, "bob")
	p, err = client.User.Query(db).WithPosts().Paginate(only, orm.PageArgs{First: intp(10), Total: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, names(p.Items))
	assert.Equal(t, 1, p.Total)
	_, err = p.Items[0].Edges.PostsOrErr()
	assert.NoError(t, err)
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
		us, err := q.OrderBy(predicate.UserOrder(o)).All(ctx)
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
	got, err = list([][3]any{{"nickname", orm.OpIsNull, nil}}, "id", false)
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
