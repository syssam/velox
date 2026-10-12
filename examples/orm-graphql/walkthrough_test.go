// Package example walks through the GraphQL layer velox-gen generates beside
// the ORM: the where input and the order input of a list, and how a request
// becomes a query. No GraphQL engine runs here, and none is imported: the
// inputs are plain Go values, which is how a resolver receives them.
package example

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlinput"

	"example.com/orm-graphql/velox"
	"example.com/orm-graphql/velox/entity"
	"example.com/orm-graphql/velox/filter"
	"example.com/orm-graphql/velox/migrate"
	"example.com/orm-graphql/velox/todo"
	"example.com/orm-graphql/velox/user"

	_ "modernc.org/sqlite"
)

var (
	ctx = context.Background()
	seq atomic.Int64
)

// open returns a handle on a fresh database holding two users and their
// todos: ann has "write" (priority 3), "test" (2, done) and "ship" (1); bob
// has "read" (5).
func open(t *testing.T) (conn *velox.DB, ann, bob *entity.User) {
	t.Helper()
	conn, err := velox.Open("sqlite", fmt.Sprintf("file:ormgql%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", seq.Add(1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, migrate.Create(ctx, conn))
	newUser := func(name string) *entity.User {
		u, err := velox.User.Create(conn).Set(user.Name.Set(name), user.Email.Set(name+"@example.com")).Save(ctx)
		require.NoError(t, err)
		return u
	}
	ann, bob = newUser("ann"), newUser("bob")
	for _, row := range []struct {
		title    string
		priority int
		status   todo.StatusEnum
		owner    *entity.User
	}{
		{"write", 3, todo.StatusTodo, ann},
		{"test", 2, todo.StatusDone, ann},
		{"ship", 1, todo.StatusTodo, ann},
		{"read", 5, todo.StatusTodo, bob},
	} {
		_, err := velox.Todo.Create(conn).
			Set(todo.Title.Set(row.title), todo.Priority.Set(row.priority), todo.Status.Set(row.status), todo.Owner.Set(row.owner.ID)).
			Save(ctx)
		require.NoError(t, err)
	}
	return conn, ann, bob
}

// Todos is the resolver of
//
//	todos(after: Cursor, first: Int, before: Cursor, last: Int,
//	      orderBy: TodoOrder, where: TodoWhereInput): TodoConnection!
//
// All of it: the where input gives a condition, the order input an order
// term, and Paginate is the query's own. Both inputs may be nil.
func Todos(ctx context.Context, ex velox.Executor, after *velox.Cursor, first *int, before *velox.Cursor, last *int, orderBy *entity.TodoOrder, where *filter.TodoWhereInput) (*entity.TodoConnection, error) {
	return velox.Todo.Query(ex).
		Where(where.P()).
		OrderBy(orderBy.Term()).
		Paginate(ctx, after, first, before, last)
}

func ptr[T any](v T) *T { return &v }

// A where input is a struct: a field that is set is a condition, and the
// conditions of an input are joined by AND.
func TestWhereInput(t *testing.T) {
	conn, _, _ := open(t)
	page, err := Todos(ctx, conn, nil, nil, nil, nil,
		&entity.TodoOrder{Field: todo.OrderFieldPriority, Direction: gqlinput.OrderDirectionDesc},
		&filter.TodoWhereInput{
			Status:       ptr(todo.StatusTodo),
			PriorityGTE:  ptr(2),
			HasOwnerWith: []*filter.UserWhereInput{{Name: ptr("ann")}},
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"write"}, titles(page))

	// No input is no condition and the order of the id.
	page, err = Todos(ctx, conn, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 4, page.TotalCount)
}

// not, and, or nest; an empty input inside them is refused, because "no
// condition" there would silently widen what a list returns.
func TestWhereInput_Nesting(t *testing.T) {
	conn, _, _ := open(t)
	where := &filter.TodoWhereInput{
		Or: []*filter.TodoWhereInput{
			{Title: ptr("ship")},
			{Not: &filter.TodoWhereInput{HasOwnerWith: []*filter.UserWhereInput{{Name: ptr("ann")}}}},
		},
	}
	page, err := Todos(ctx, conn, nil, nil, nil, nil, &entity.TodoOrder{Field: todo.OrderFieldTitle}, where)
	require.NoError(t, err)
	assert.Equal(t, []string{"read", "ship"}, titles(page))

	_, err = Todos(ctx, conn, nil, nil, nil, nil, nil, &filter.TodoWhereInput{Not: &filter.TodoWhereInput{}})
	require.Error(t, err, "an empty not")
}

// A where input reads and writes itself as the document a request carries,
// so a filter can be kept (a saved view, a report definition) and applied
// later. Enum values are spelled as the GraphQL schema spells them.
func TestWhereInput_AsADocument(t *testing.T) {
	conn, _, _ := open(t)
	const saved = `{"status":"TODO","priorityLT":5,"hasOwnerWith":[{"nameIn":["ann","bob"]}]}`

	var where filter.TodoWhereInput
	require.NoError(t, json.Unmarshal([]byte(saved), &where))
	page, err := Todos(ctx, conn, nil, nil, nil, nil, &entity.TodoOrder{Field: todo.OrderFieldTitle}, &where)
	require.NoError(t, err)
	assert.Equal(t, []string{"ship", "write"}, titles(page))

	// Written back, it is the same document.
	out, err := json.Marshal(where)
	require.NoError(t, err)
	assert.JSONEq(t, saved, string(out))

	// What the input has no field for is refused, not skipped: a typo in a
	// kept filter would otherwise drop a condition.
	err = json.Unmarshal([]byte(`{"titel":"ship"}`), &where)
	require.Error(t, err)
	// So is a value that is none of the enum's.
	err = json.Unmarshal([]byte(`{"status":"LATER"}`), &where)
	require.Error(t, err)
}

// The list is paged by cursor, in the order asked for.
func TestPaginate(t *testing.T) {
	conn, _, _ := open(t)
	byPriority := &entity.TodoOrder{Field: todo.OrderFieldPriority, Direction: gqlinput.OrderDirectionDesc}
	first, err := Todos(ctx, conn, nil, ptr(2), nil, nil, byPriority, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"read", "write"}, titles(first))
	assert.True(t, first.PageInfo.HasNextPage)

	next, err := Todos(ctx, conn, first.PageInfo.EndCursor, ptr(2), nil, nil, byPriority, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"test", "ship"}, titles(next))
	assert.False(t, next.PageInfo.HasNextPage)
}

// An edge of an entity is a method an engine binds a field to: user.todos
// takes the arguments of a connection. The entity holds no connection, so
// the method reads the handle from the context, where the application put it
// for the request.
func TestEdgeResolver(t *testing.T) {
	conn, ann, _ := open(t)
	request := velox.WithExecutor(ctx, conn)

	page, err := ann.Todos(request, nil, nil, nil, nil,
		&entity.TodoOrder{Field: todo.OrderFieldTitle},
		&filter.TodoWhereInput{StatusNEQ: ptr(todo.StatusDone)})
	require.NoError(t, err)
	assert.Equal(t, []string{"ship", "write"}, titles(page))

	// A to-one edge.
	owner, err := page.Edges[0].Node.Owner(request)
	require.NoError(t, err)
	assert.Equal(t, "ann", owner.Name)
}

// node(id:) goes by the types of node: each knows how to read one by id.
func TestNode(t *testing.T) {
	conn, ann, _ := open(t)
	for _, nt := range velox.NodeTypes() {
		if nt.Name != "User" {
			continue
		}
		n, err := nt.Get(ctx, conn, ann.ID)
		require.NoError(t, err)
		assert.Equal(t, "ann", n.(*entity.User).Name)
	}
}

func titles(page *entity.TodoConnection) []string {
	out := make([]string, len(page.Edges))
	for i, e := range page.Edges {
		out[i] = e.Node.Title
	}
	return out
}
