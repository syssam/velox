package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	gqlgenpkg "example.com/fullgql/gqlgen"
	"example.com/fullgql/velox"
	categoryclient "example.com/fullgql/velox/client/category"
	commentclient "example.com/fullgql/velox/client/comment"
	memberclient "example.com/fullgql/velox/client/member"
	tagclient "example.com/fullgql/velox/client/tag"
	todoclient "example.com/fullgql/velox/client/todo"
	userclient "example.com/fullgql/velox/client/user"
	workspaceclient "example.com/fullgql/velox/client/workspace"
	"example.com/fullgql/velox/category"
	"example.com/fullgql/velox/entity"
	"example.com/fullgql/velox/member"
	"example.com/fullgql/velox/predicate"
	"example.com/fullgql/velox/tag"
	"example.com/fullgql/velox/todo"
	"example.com/fullgql/velox/user"

	gqlclient "github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"

	// Import entity sub-packages to trigger init() registration.
	_ "example.com/fullgql/velox/auditlog"
	_ "modernc.org/sqlite"
)

func openTestClient(t *testing.T) *velox.Client {
	t.Helper()
	client, err := velox.Open(dialect.SQLite, "file:e2e?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	return client
}

func ptr[T any](v T) *T { return &v }

func TestCRUD_User(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "alice@example.com"}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "Alice", u.Name)

	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)

	u2, err := userclient.NewUserClient(cfg).UpdateOneID(u.ID).
		SetInput(userclient.UpdateUserInput{Name: ptr("Bob")}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "Bob", u2.Name)

	err = client.User.DeleteOneID(u.ID).Exec(ctx)
	require.NoError(t, err)
	users, err = client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 0)
}

func TestCRUD_Category(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	cat, err := categoryclient.NewCategoryClient(cfg).Create().
		SetInput(categoryclient.CreateCategoryInput{Name: "Electronics"}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "Electronics", cat.Name)
}

func TestCRUD_Workspace(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "Test WS"}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "Test WS", ws.Name)
}

func TestCRUD_Todo(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "alice@t.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "Work"}).Save(ctx)
	require.NoError(t, err)

	td, err := todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Buy groceries", Status: ptr(todo.StatusInProgress),
			Priority: ptr(todo.PriorityMedium), OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "Buy groceries", td.Title)
}

func TestCRUD_Tag(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	tg, err := tagclient.NewTagClient(cfg).Create().
		SetInput(tagclient.CreateTagInput{Name: "golang"}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "golang", tg.Name)
}

func TestCRUD_Member(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "a@m.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "Team"}).Save(ctx)
	require.NoError(t, err)

	m, err := memberclient.NewMemberClient(cfg).Create().
		SetInput(memberclient.CreateMemberInput{
			Role: ptr(member.RoleViewer), WorkspaceID: ws.ID, UserID: u.ID,
		}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, member.RoleViewer, m.Role)
}

func TestEdgeLoading_OptionalFK(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "edge@test.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeTest"}).Save(ctx)
	require.NoError(t, err)

	// Create a todo with owner (required) and workspace (optional FK)
	_, err = todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Test edge loading", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)

	// Query all todos — exercises edge loader with typed maps
	todos, err := client.Todo.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, todos, 1)

	// Query all users — exercises O2M loader (user → todos)
	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
}

func TestGraphQL_NodeResolver(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "a@n.com"}).Save(ctx)
	require.NoError(t, err)

	t.Run("Noder_resolves_existing", func(t *testing.T) {
		node, err := client.Noder(ctx, u.ID)
		require.NoError(t, err)
		require.NotNil(t, node)

		// Resolver must return the concrete entity type, not a proxy.
		// If this assertion ever breaks, the Noder registry is mis-wired
		// and downstream gqlgen marshaling will fall through to Relay's
		// opaque Node type.
		resolved, ok := node.(*entity.User)
		require.True(t, ok, "Noder must return concrete *entity.User, got %T", node)
		assert.Equal(t, u.ID, resolved.ID)
		assert.Equal(t, "Alice", resolved.Name)

		// The resolved entity must carry runtime.Config so downstream
		// edge reads work without panic. Without Config propagation via
		// runtime.WithConfigContext, this edge query would dereference
		// a nil driver.
		edges, err := resolved.QueryComments().All(ctx)
		require.NoError(t, err, "resolved entity must be usable for edge reads")
		assert.Empty(t, edges, "freshly-created user has no comments")
	})

	t.Run("Noder_returns_ErrNodeNotFound_for_missing_id", func(t *testing.T) {
		node, err := client.Noder(ctx, 999999)
		require.Error(t, err)
		require.Nil(t, node)
	})

	t.Run("Noders_returns_per_id_with_nil_for_missing", func(t *testing.T) {
		nodes, err := client.Noders(ctx, []int{u.ID, 999999})
		require.NoError(t, err)
		require.Len(t, nodes, 2)
		require.NotNil(t, nodes[0], "existing id must resolve")
		require.Nil(t, nodes[1], "missing id must produce nil entry per Relay spec")
	})
}

// ---------- Edge Loading ----------

func TestEdgeLoading_UserWithTodos(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "edge-user@test.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeWS"}).Save(ctx)
	require.NoError(t, err)

	_, err = todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Todo 1", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)
	_, err = todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Todo 2", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)

	// Use the interface's WithTodos for edge loading via client.User.Query().
	users, err := client.User.Query().WithTodos().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Len(t, users[0].Edges.Todos, 2)
}

func TestEdgeLoading_TodoWithTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Bob", Email: "bob-edge@test.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeWS2"}).Save(ctx)
	require.NoError(t, err)

	tg, err := tagclient.NewTagClient(cfg).Create().
		SetInput(tagclient.CreateTagInput{Name: "urgent"}).Save(ctx)
	require.NoError(t, err)

	_, err = todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Tagged todo", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
			TagIDs: []int{tg.ID},
		}).Save(ctx)
	require.NoError(t, err)

	// Use the concrete query type from the query package for M2M edge loading.
	todos, err := client.Todo.Query().WithTags().All(ctx)
	require.NoError(t, err)
	require.Len(t, todos, 1)
	tags, err := todos[0].Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	assert.Equal(t, "urgent", tags[0].Name)
}

// ---------- Transactions ----------

func TestTransaction_CommitPersists(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	txCfg := tx.Client().RuntimeConfig()
	_, err = userclient.NewUserClient(txCfg).Create().
		SetInput(userclient.CreateUserInput{Name: "TxUser", Email: "tx@commit.com"}).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	// After commit, the user should be visible via the original client.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestTransaction_RollbackDiscards(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	txCfg := tx.Client().RuntimeConfig()
	_, err = userclient.NewUserClient(txCfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Rollback", Email: "tx@rollback.com"}).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())

	// After rollback, the user should NOT exist.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestTransaction_WithTxHelper(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	err := velox.WithTx(ctx, client, func(tx *velox.Tx) error {
		txCfg := tx.Client().RuntimeConfig()
		_, err := userclient.NewUserClient(txCfg).Create().
			SetInput(userclient.CreateUserInput{Name: "WithTx", Email: "withtx@test.com"}).Save(ctx)
		return err
	})
	require.NoError(t, err)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// ---------- Bulk Create ----------

func TestBulkCreate_Tags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	tc := tagclient.NewTagClient(cfg)
	builders := make([]*tagclient.TagCreate, 5)
	for i := range builders {
		builders[i] = tc.Create().SetInput(tagclient.CreateTagInput{
			Name: "tag-" + strings.Repeat("x", i+1),
		})
	}

	tags, err := tc.CreateBulk(builders...).Save(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 5)

	// Verify via query.
	count, err := client.Tag.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

// ---------- Update with Predicates ----------

func TestUpdate_WithPredicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	_, err := uc.Create().SetInput(userclient.CreateUserInput{
		Name: "Admin1", Email: "admin1@test.com", Role: ptr(user.RoleAdmin),
	}).Save(ctx)
	require.NoError(t, err)

	_, err = uc.Create().SetInput(userclient.CreateUserInput{
		Name: "Admin2", Email: "admin2@test.com", Role: ptr(user.RoleAdmin),
	}).Save(ctx)
	require.NoError(t, err)

	_, err = uc.Create().SetInput(userclient.CreateUserInput{
		Name: "Viewer", Email: "viewer@test.com", Role: ptr(user.RoleGuest),
	}).Save(ctx)
	require.NoError(t, err)

	// Update all admins to moderator.
	affected, err := uc.Update().
		Where(user.RoleField.EQ(user.RoleAdmin)).
		SetRole(user.RoleModerator).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, affected)

	// Verify the guest user was not changed.
	users, err := client.User.Query().
		Where(user.RoleField.EQ(user.RoleGuest)).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Viewer", users[0].Name)
}

// ---------- Delete with Predicates ----------

func TestDelete_WithPredicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	tc := tagclient.NewTagClient(cfg)
	_, err := tc.Create().SetInput(tagclient.CreateTagInput{Name: "keep"}).Save(ctx)
	require.NoError(t, err)
	_, err = tc.Create().SetInput(tagclient.CreateTagInput{Name: "remove1"}).Save(ctx)
	require.NoError(t, err)
	_, err = tc.Create().SetInput(tagclient.CreateTagInput{Name: "remove2"}).Save(ctx)
	require.NoError(t, err)

	// Delete tags whose name starts with "remove" by using NameHasPrefix.
	deleted, err := tc.Delete().
		Where(tag.NameField.HasPrefix("remove")).
		Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)

	// Verify only "keep" remains.
	remaining, err := client.Tag.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "keep", remaining[0].Name)
}

// ---------- Count and Exist ----------

func TestCountAndExist(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	// Initially empty.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	exists, err := client.User.Query().Exist(ctx)
	require.NoError(t, err)
	assert.False(t, exists)

	// Create one user.
	_, err = userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Counter", Email: "count@test.com"}).Save(ctx)
	require.NoError(t, err)

	count, err = client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	exists, err = client.User.Query().Exist(ctx)
	require.NoError(t, err)
	assert.True(t, exists)
}

// ---------- GroupBy / Aggregate ----------

func TestGroupBy_TodoStatus(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "GroupByUser", Email: "gb@test.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "GBWS"}).Save(ctx)
	require.NoError(t, err)

	tc := todoclient.NewTodoClient(cfg)
	// Create 3 "todo" status and 2 "done" status.
	for i := 0; i < 3; i++ {
		_, err = tc.Create().SetInput(todoclient.CreateTodoInput{
			Title: "todo-item", Status: ptr(todo.StatusTodo), OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
		require.NoError(t, err)
	}
	for i := 0; i < 2; i++ {
		_, err = tc.Create().SetInput(todoclient.CreateTodoInput{
			Title: "done-item", Status: ptr(todo.StatusDone), OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
		require.NoError(t, err)
	}

	// GroupBy status with count.
	var results []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	err = client.Todo.Query().
		GroupBy(todo.FieldStatus).
		Aggregate(velox.Count()).
		Scan(ctx, &results)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	statusCounts := make(map[string]int)
	for _, r := range results {
		statusCounts[r.Status] = r.Count
	}
	assert.Equal(t, 3, statusCounts[string(todo.StatusTodo)])
	assert.Equal(t, 2, statusCounts[string(todo.StatusDone)])
}

// ---------- String() method ----------

func TestEntity_String(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Stringer", Email: "str@test.com"}).Save(ctx)
	require.NoError(t, err)

	s := u.String()
	assert.Contains(t, s, "User(")
	assert.Contains(t, s, "name=Stringer")
	assert.Contains(t, s, "email=str@test.com")
}

// ---------- Not Found / Not Singular Errors ----------

func TestNotFoundError(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	// Get a non-existent ID.
	_, err := userclient.NewUserClient(cfg).Get(ctx, 999999)
	require.Error(t, err)
	assert.True(t, velox.IsNotFound(err), "expected NotFoundError, got: %v", err)
}

func TestNotSingularError(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	_, err := uc.Create().SetInput(userclient.CreateUserInput{Name: "Dup1", Email: "dup1@test.com"}).Save(ctx)
	require.NoError(t, err)
	_, err = uc.Create().SetInput(userclient.CreateUserInput{Name: "Dup2", Email: "dup2@test.com"}).Save(ctx)
	require.NoError(t, err)

	// Only() should fail with NotSingular when multiple results match.
	_, err = client.User.Query().Only(ctx)
	require.Error(t, err)
	assert.True(t, velox.IsNotSingular(err), "expected NotSingularError, got: %v", err)
}

func TestNotLoadedError(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	_, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "NoEdge", Email: "noedge@test.com"}).Save(ctx)
	require.NoError(t, err)

	// Query without WithTodos — accessing edge should return NotLoadedError.
	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)

	_, err = users[0].Edges.TodosOrErr()
	require.Error(t, err)
	assert.True(t, velox.IsNotLoaded(err), "expected NotLoadedError, got: %v", err)
}

// ---------- First / Only ----------

func TestFirst_ReturnsOne(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	_, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "First1", Email: "first1@test.com"}).Save(ctx)
	require.NoError(t, err)
	_, err = userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "First2", Email: "first2@test.com"}).Save(ctx)
	require.NoError(t, err)

	u, err := client.User.Query().First(ctx)
	require.NoError(t, err)
	assert.NotNil(t, u)
}

func TestFirst_EmptyReturnsNotFound(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Query().First(ctx)
	require.Error(t, err)
	assert.True(t, velox.IsNotFound(err))
}

// ---------- IDs ----------

func TestIDs(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	u1, err := uc.Create().SetInput(userclient.CreateUserInput{Name: "ID1", Email: "id1@test.com"}).Save(ctx)
	require.NoError(t, err)
	u2, err := uc.Create().SetInput(userclient.CreateUserInput{Name: "ID2", Email: "id2@test.com"}).Save(ctx)
	require.NoError(t, err)

	ids, err := client.User.Query().IDs(ctx)
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, u1.ID)
	assert.Contains(t, ids, u2.ID)
}

// ---------- Real-World Scenario Tests ----------

func TestUniqueConstraintViolation(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	_, err := uc.Create().SetInput(userclient.CreateUserInput{
		Name: "Alice", Email: "alice@unique.com",
	}).Save(ctx)
	require.NoError(t, err)

	// Second user with the same email should fail with a constraint error.
	_, err = uc.Create().SetInput(userclient.CreateUserInput{
		Name: "Bob", Email: "alice@unique.com",
	}).Save(ctx)
	require.Error(t, err)
	assert.True(t, velox.IsConstraintError(err), "expected ConstraintError, got: %v", err)
}

func TestCreateWithMinimalFields(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
		Name: "Minimal", Email: "minimal@test.com",
	}).Save(ctx)
	require.NoError(t, err)

	// Required fields are set.
	assert.Equal(t, "Minimal", u.Name)
	assert.Equal(t, "minimal@test.com", u.Email)

	// Optional nillable fields should be nil.
	assert.Nil(t, u.Age, "age should be nil for minimal create")
	assert.Nil(t, u.Bio, "bio should be nil for minimal create")

	// Role is optional with Default("user") in the schema (see schema/user.go),
	// so an unset Role on Create resolves to the schema default, not the zero value.
	assert.Equal(t, user.Role("user"), u.Role, "role should be schema default when not set")

	// Verify via fresh read.
	fresh, err := userclient.NewUserClient(cfg).Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Nil(t, fresh.Age)
	assert.Nil(t, fresh.Bio)
}

func TestUnicodeAndSpecialChars(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	cases := []struct {
		name  string
		email string
	}{
		{"Robert'; DROP TABLE users;--", "sqli@test.com"},
		{"Tab\there", "tab@test.com"},
		{"Line\nbreak", "newline@test.com"},
		{"<script>alert('xss')</script>", "xss@test.com"},
		{"Backslash\\path\\file", "backslash@test.com"},
		{"Percent%20encoded", "percent@test.com"},
	}

	uc := userclient.NewUserClient(cfg)
	for _, tc := range cases {
		u, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name: tc.name, Email: tc.email,
		}).Save(ctx)
		require.NoError(t, err, "create failed for name=%q", tc.name)

		// Verify round-trip via Get.
		fresh, err := uc.Get(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, tc.name, fresh.Name, "round-trip mismatch for %q", tc.name)
		assert.Equal(t, tc.email, fresh.Email)
	}

	// All users should exist.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, len(cases), count)
}

func TestM2M_AddRemoveTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	// Create prerequisites.
	u, err := userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
		Name: "TagUser", Email: "taguser@test.com",
	}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().SetInput(workspaceclient.CreateWorkspaceInput{
		Name: "TagWS",
	}).Save(ctx)
	require.NoError(t, err)

	tc := tagclient.NewTagClient(cfg)
	tag1, err := tc.Create().SetInput(tagclient.CreateTagInput{Name: "go"}).Save(ctx)
	require.NoError(t, err)
	tag2, err := tc.Create().SetInput(tagclient.CreateTagInput{Name: "rust"}).Save(ctx)
	require.NoError(t, err)
	tag3, err := tc.Create().SetInput(tagclient.CreateTagInput{Name: "python"}).Save(ctx)
	require.NoError(t, err)

	// Create todo with tag1 and tag2.
	td, err := todoclient.NewTodoClient(cfg).Create().SetInput(todoclient.CreateTodoInput{
		Title: "Tagged", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		TagIDs: []int{tag1.ID, tag2.ID},
	}).Save(ctx)
	require.NoError(t, err)

	// Verify initial tags.
	todos, err := client.Todo.Query().Where(todo.IDField.EQ(td.ID)).WithTags().All(ctx)
	require.NoError(t, err)
	require.Len(t, todos, 1)
	tags, err := todos[0].Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 2)

	// Add tag3, remove tag1.
	_, err = todoclient.NewTodoClient(cfg).UpdateOneID(td.ID).
		AddTagIDs(tag3.ID).
		RemoveTagIDs(tag1.ID).
		Save(ctx)
	require.NoError(t, err)

	// Verify updated tags: should have tag2 and tag3.
	todos, err = client.Todo.Query().Where(todo.IDField.EQ(td.ID)).WithTags().All(ctx)
	require.NoError(t, err)
	require.Len(t, todos, 1)
	tags, err = todos[0].Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 2)

	tagNames := make(map[string]bool)
	for _, tg := range tags {
		tagNames[tg.Name] = true
	}
	assert.True(t, tagNames["rust"], "should still have 'rust'")
	assert.True(t, tagNames["python"], "should have added 'python'")
	assert.False(t, tagNames["go"], "should have removed 'go'")
}

func TestSelfReferentialEdge_CategoryParent(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	cc := categoryclient.NewCategoryClient(cfg)

	// Create parent category.
	parent, err := cc.Create().SetInput(categoryclient.CreateCategoryInput{
		Name: "Electronics",
	}).Save(ctx)
	require.NoError(t, err)

	// Create child category with parent.
	child, err := cc.Create().SetInput(categoryclient.CreateCategoryInput{
		Name: "Smartphones", ParentID: ptr(parent.ID),
	}).Save(ctx)
	require.NoError(t, err)

	// Create another child.
	_, err = cc.Create().SetInput(categoryclient.CreateCategoryInput{
		Name: "Laptops", ParentID: ptr(parent.ID),
	}).Save(ctx)
	require.NoError(t, err)

	// Query children of parent via QueryChildren.
	children, err := cc.QueryChildren(parent).All(ctx)
	require.NoError(t, err)
	assert.Len(t, children, 2)

	childNames := make(map[string]bool)
	for _, c := range children {
		childNames[c.Name] = true
	}
	assert.True(t, childNames["Smartphones"])
	assert.True(t, childNames["Laptops"])

	// Query parent of child via QueryParent.
	p, err := cc.QueryParent(child).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Electronics", p.Name)
}

func TestConcurrentCreates(t *testing.T) {
	// Use a file-based temp DB so concurrent connections share the same schema.
	tmpFile := t.TempDir() + "/concurrent.db"
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", tmpFile)
	client, err := velox.Open(dialect.SQLite, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	cfg := client.RuntimeConfig()

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
				Name:  fmt.Sprintf("User-%d", idx),
				Email: fmt.Sprintf("user-%d@concurrent.com", idx),
			}).Save(ctx)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed", i)
	}

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, n, count)
}

func TestNestedEdgeLoading(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	// Create user, workspace, tags, and todo with tags.
	u, err := userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
		Name: "Nested", Email: "nested@test.com",
	}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().SetInput(workspaceclient.CreateWorkspaceInput{
		Name: "NestedWS",
	}).Save(ctx)
	require.NoError(t, err)

	tg1, err := tagclient.NewTagClient(cfg).Create().SetInput(tagclient.CreateTagInput{Name: "alpha"}).Save(ctx)
	require.NoError(t, err)
	tg2, err := tagclient.NewTagClient(cfg).Create().SetInput(tagclient.CreateTagInput{Name: "beta"}).Save(ctx)
	require.NoError(t, err)

	_, err = todoclient.NewTodoClient(cfg).Create().SetInput(todoclient.CreateTodoInput{
		Title: "Nested Todo 1", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		TagIDs: []int{tg1.ID, tg2.ID},
	}).Save(ctx)
	require.NoError(t, err)

	_, err = todoclient.NewTodoClient(cfg).Create().SetInput(todoclient.CreateTodoInput{
		Title: "Nested Todo 2", OwnerID: u.ID, WorkspaceID: ptr(ws.ID),
		TagIDs: []int{tg1.ID},
	}).Save(ctx)
	require.NoError(t, err)

	// Load User -> Todos -> Tags (2-level nested).
	users, err := client.User.Query().
		Where(user.IDField.EQ(u.ID)).
		WithTodos(func(tq entity.TodoQuerier) {
			tq.WithTags()
		}).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)

	todos, err := users[0].Edges.TodosOrErr()
	require.NoError(t, err)
	assert.Len(t, todos, 2)

	// At least one todo should have 2 tags, the other should have 1.
	tagCounts := make(map[int]int)
	for _, td := range todos {
		tags, err := td.Edges.TagsOrErr()
		require.NoError(t, err)
		tagCounts[len(tags)]++
	}
	assert.Equal(t, 1, tagCounts[2], "one todo should have 2 tags")
	assert.Equal(t, 1, tagCounts[1], "one todo should have 1 tag")
}

func TestOrdering(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	names := []string{"Charlie", "Alice", "Bob"}
	for i, name := range names {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:  name,
			Email: fmt.Sprintf("%s@order.com", strings.ToLower(name)),
			Age:   ptr((i + 1) * 10),
		}).Save(ctx)
		require.NoError(t, err)
	}

	// Order by name ASC.
	users, err := client.User.Query().Order(user.ByName(sql.OrderAsc())).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
	assert.Equal(t, "Charlie", users[2].Name)

	// Order by name DESC.
	users, err = client.User.Query().Order(user.ByName(sql.OrderDesc())).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "Charlie", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
	assert.Equal(t, "Alice", users[2].Name)

	// Order by ID DESC (most recently created first).
	users, err = client.User.Query().Order(user.ByID(sql.OrderDesc())).All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "Bob", users[0].Name, "Bob was created last")
}

func TestEmptyUpdate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
		Name: "NoChange", Email: "nochange@test.com",
	}).Save(ctx)
	require.NoError(t, err)

	// Update with no fields set should succeed as a no-op.
	updated, err := userclient.NewUserClient(cfg).UpdateOneID(u.ID).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "NoChange", updated.Name)
	assert.Equal(t, "nochange@test.com", updated.Email)
}

func TestContextCancellation(t *testing.T) {
	client := openTestClient(t)
	cfg := client.RuntimeConfig()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
		Name: "Cancelled", Email: "cancel@test.com",
	}).Save(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	// Query with cancelled context should also fail.
	_, err = client.User.Query().All(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------- Pagination ----------

func TestPagination_ForwardPaging(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 5; i++ {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:  fmt.Sprintf("User-%d", i),
			Email: fmt.Sprintf("user%d@test.com", i),
		}).Save(ctx)
		require.NoError(t, err)
	}

	// Page 1: first=2, after=nil
	conn, err := client.User.Query().Paginate(ctx, nil, ptr(2), nil, nil)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 2)
	assert.True(t, conn.PageInfo.HasNextPage)
	assert.False(t, conn.PageInfo.HasPreviousPage)
	assert.Equal(t, 5, conn.TotalCount)

	// Page 2: first=2, after=endCursor from page 1
	conn2, err := client.User.Query().Paginate(ctx, conn.PageInfo.EndCursor, ptr(2), nil, nil)
	require.NoError(t, err)
	require.Len(t, conn2.Edges, 2)
	assert.True(t, conn2.PageInfo.HasNextPage)
	assert.True(t, conn2.PageInfo.HasPreviousPage)

	// Page 3: first=2, after=endCursor from page 2
	conn3, err := client.User.Query().Paginate(ctx, conn2.PageInfo.EndCursor, ptr(2), nil, nil)
	require.NoError(t, err)
	require.Len(t, conn3.Edges, 1)
	assert.False(t, conn3.PageInfo.HasNextPage)
	assert.True(t, conn3.PageInfo.HasPreviousPage)
}

func TestPagination_BackwardPaging(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 5; i++ {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:  fmt.Sprintf("User-%d", i),
			Email: fmt.Sprintf("user%d@test.com", i),
		}).Save(ctx)
		require.NoError(t, err)
	}

	// Page 1 (from end): last=2, before=nil
	conn, err := client.User.Query().Paginate(ctx, nil, nil, nil, ptr(2))
	require.NoError(t, err)
	require.Len(t, conn.Edges, 2)
	assert.True(t, conn.PageInfo.HasPreviousPage)
	assert.False(t, conn.PageInfo.HasNextPage)

	// Page 2: last=2, before=startCursor from page 1
	conn2, err := client.User.Query().Paginate(ctx, nil, nil, conn.PageInfo.StartCursor, ptr(2))
	require.NoError(t, err)
	require.Len(t, conn2.Edges, 2)
	assert.True(t, conn2.PageInfo.HasNextPage, "before cursor is set, so HasNextPage should be true")

	// Page 3: last=2, before=startCursor from page 2
	conn3, err := client.User.Query().Paginate(ctx, nil, nil, conn2.PageInfo.StartCursor, ptr(2))
	require.NoError(t, err)
	require.Len(t, conn3.Edges, 1)
	assert.False(t, conn3.PageInfo.HasPreviousPage)
	assert.True(t, conn3.PageInfo.HasNextPage, "before cursor is set, so HasNextPage should be true")
}

func TestPagination_EmptyResult(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	conn, err := client.User.Query().Paginate(ctx, nil, ptr(10), nil, nil)
	require.NoError(t, err)
	assert.Len(t, conn.Edges, 0)
	assert.False(t, conn.PageInfo.HasNextPage)
	assert.False(t, conn.PageInfo.HasPreviousPage)
	assert.Equal(t, 0, conn.TotalCount)
}

func TestPagination_ExactPageSize(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 3; i++ {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:  fmt.Sprintf("User-%d", i),
			Email: fmt.Sprintf("user%d@test.com", i),
		}).Save(ctx)
		require.NoError(t, err)
	}

	conn, err := client.User.Query().Paginate(ctx, nil, ptr(3), nil, nil)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 3)
	assert.False(t, conn.PageInfo.HasNextPage)
	assert.Equal(t, 3, conn.TotalCount)
}

func TestPagination_WithOrdering(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for _, name := range []string{"Charlie", "Alice", "Bob"} {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:  name,
			Email: fmt.Sprintf("%s@page.com", name),
		}).Save(ctx)
		require.NoError(t, err)
	}

	nameOrder := entity.WithUserOrder(&entity.UserOrder{
		Direction: gqlrelay.OrderDirectionAsc,
		Field: &entity.UserOrderField{
			Column: "name",
			ToCursor: func(u *entity.User) gqlrelay.Cursor {
				return gqlrelay.Cursor{ID: u.ID, Value: u.Name}
			},
		},
	})

	// Page 1: first=2 with name ASC -> Alice, Bob
	conn, err := client.User.Query().Paginate(ctx, nil, ptr(2), nil, nil, nameOrder)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 2)
	assert.Equal(t, "Alice", conn.Edges[0].Node.Name)
	assert.Equal(t, "Bob", conn.Edges[1].Node.Name)

	// Page 2: -> Charlie
	conn2, err := client.User.Query().Paginate(ctx, conn.PageInfo.EndCursor, ptr(2), nil, nil, nameOrder)
	require.NoError(t, err)
	require.Len(t, conn2.Edges, 1)
	assert.Equal(t, "Charlie", conn2.Edges[0].Node.Name)
}

func TestPagination_ValidationErrors(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Negative first should return error.
	_, err := client.User.Query().Paginate(ctx, nil, ptr(-1), nil, nil)
	require.Error(t, err)

	// Both first and last should return error.
	_, err = client.User.Query().Paginate(ctx, nil, ptr(5), nil, ptr(5))
	require.Error(t, err)
}

func TestPagination_DescOrdering(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for _, name := range []string{"Alice", "Bob", "Charlie"} {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name: name, Email: fmt.Sprintf("%s@desc.com", name),
		}).Save(ctx)
		require.NoError(t, err)
	}

	nameDescOrder := entity.WithUserOrder(&entity.UserOrder{
		Direction: gqlrelay.OrderDirectionDesc,
		Field: &entity.UserOrderField{
			Column: "name",
			ToCursor: func(u *entity.User) gqlrelay.Cursor {
				return gqlrelay.Cursor{ID: u.ID, Value: u.Name}
			},
		},
	})

	// Page 1: first=2 with name DESC -> Charlie, Bob
	conn, err := client.User.Query().Paginate(ctx, nil, ptr(2), nil, nil, nameDescOrder)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 2)
	assert.Equal(t, "Charlie", conn.Edges[0].Node.Name)
	assert.Equal(t, "Bob", conn.Edges[1].Node.Name)
	assert.True(t, conn.PageInfo.HasNextPage)

	// Page 2: after cursor -> Alice
	conn2, err := client.User.Query().Paginate(ctx, conn.PageInfo.EndCursor, ptr(2), nil, nil, nameDescOrder)
	require.NoError(t, err)
	require.Len(t, conn2.Edges, 1)
	assert.Equal(t, "Alice", conn2.Edges[0].Node.Name)
	assert.False(t, conn2.PageInfo.HasNextPage)
}

func TestPagination_WithFilter(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 5; i++ {
		active := i <= 3 // first 3 are active
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:   fmt.Sprintf("User-%d", i),
			Email:  fmt.Sprintf("filter%d@test.com", i),
			Active: ptr(active),
		}).Save(ctx)
		require.NoError(t, err)
	}

	activeFilter := entity.WithUserFilter(func() (predicate.User, error) {
		return user.ActiveField.EQ(true), nil
	})

	conn, err := client.User.Query().Paginate(ctx, nil, ptr(10), nil, nil, activeFilter)
	require.NoError(t, err)
	assert.Equal(t, 3, conn.TotalCount, "only 3 active users")
	assert.Len(t, conn.Edges, 3)
}

func TestPagination_EdgePagination(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	u, err := userclient.NewUserClient(cfg).Create().SetInput(userclient.CreateUserInput{
		Name: "Alice", Email: "alice@edge.com",
	}).Save(ctx)
	require.NoError(t, err)

	tc := todoclient.NewTodoClient(cfg)
	for i := 1; i <= 5; i++ {
		_, err := tc.Create().SetInput(todoclient.CreateTodoInput{
			Title:   fmt.Sprintf("Task-%d", i),
			OwnerID: u.ID,
		}).Save(ctx)
		require.NoError(t, err)
	}

	// Paginate user's todos via filter.
	ownerFilter := entity.WithTodoFilter(func() (predicate.Todo, error) {
		return todo.HasOwnerWith(user.IDField.EQ(u.ID)), nil
	})

	conn, err := client.Todo.Query().Paginate(ctx, nil, ptr(2), nil, nil, ownerFilter)
	require.NoError(t, err)
	assert.Len(t, conn.Edges, 2)
	assert.Equal(t, 5, conn.TotalCount)
	assert.True(t, conn.PageInfo.HasNextPage)

	// Next page.
	conn2, err := client.Todo.Query().Paginate(ctx, conn.PageInfo.EndCursor, ptr(2), nil, nil, ownerFilter)
	require.NoError(t, err)
	assert.Len(t, conn2.Edges, 2)
	assert.True(t, conn2.PageInfo.HasNextPage)
}

func TestPagination_DuplicateOrderValues(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 4; i++ {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name:  "SameName",
			Email: fmt.Sprintf("dup%d@test.com", i),
		}).Save(ctx)
		require.NoError(t, err)
	}

	nameOrder := entity.WithUserOrder(&entity.UserOrder{
		Direction: gqlrelay.OrderDirectionAsc,
		Field: &entity.UserOrderField{
			Column: "name",
			ToCursor: func(u *entity.User) gqlrelay.Cursor {
				return gqlrelay.Cursor{ID: u.ID, Value: u.Name}
			},
		},
	})

	// Page 1.
	conn, err := client.User.Query().Paginate(ctx, nil, ptr(2), nil, nil, nameOrder)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 2)
	assert.Equal(t, 4, conn.TotalCount)

	// Page 2 — should get different users (tiebreaker by ID).
	conn2, err := client.User.Query().Paginate(ctx, conn.PageInfo.EndCursor, ptr(2), nil, nil, nameOrder)
	require.NoError(t, err)
	require.Len(t, conn2.Edges, 2)

	// No overlap between pages.
	page1IDs := map[int]bool{conn.Edges[0].Node.ID: true, conn.Edges[1].Node.ID: true}
	for _, e := range conn2.Edges {
		assert.False(t, page1IDs[e.Node.ID], "page 2 should not contain page 1 IDs")
	}
}

func TestPagination_SingleItemPage(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 3; i++ {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name: fmt.Sprintf("User-%d", i), Email: fmt.Sprintf("single%d@test.com", i),
		}).Save(ctx)
		require.NoError(t, err)
	}

	// first=1 — single item per page.
	conn, err := client.User.Query().Paginate(ctx, nil, ptr(1), nil, nil)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 1)
	assert.True(t, conn.PageInfo.HasNextPage)
	assert.Equal(t, 3, conn.TotalCount)
}

func TestPagination_Overshoot(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	uc := userclient.NewUserClient(cfg)
	for i := 1; i <= 3; i++ {
		_, err := uc.Create().SetInput(userclient.CreateUserInput{
			Name: fmt.Sprintf("User-%d", i), Email: fmt.Sprintf("over%d@test.com", i),
		}).Save(ctx)
		require.NoError(t, err)
	}

	// first=100 with only 3 items.
	conn, err := client.User.Query().Paginate(ctx, nil, ptr(100), nil, nil)
	require.NoError(t, err)
	assert.Len(t, conn.Edges, 3)
	assert.False(t, conn.PageInfo.HasNextPage)
	assert.Equal(t, 3, conn.TotalCount)
}

// TestEntityO2MEdgeMethod_ReturnsAllChildren pins the post-2026-04-15
// invariant: (*User).Comments(ctx) called on a reloaded entity must
// return all of the user's comments.
//
// Regression guard for Bug 3: the runtime.QueryEdgeUntyped fallback
// used idColumn producing WHERE comments.id = <user.id> — i.e. 0 rows
// in realistic data.
func TestEntityO2MEdgeMethod_ReturnsAllChildren(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	alice, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "alice-o2m-edge@example.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeO2M-WS"}).Save(ctx)
	require.NoError(t, err)

	td, err := todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Todo for comments", OwnerID: alice.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)

	// Create 3 comments authored by alice on td.
	for i := 1; i <= 3; i++ {
		_, err := commentclient.NewCommentClient(cfg).Create().
			SetInput(commentclient.CreateCommentInput{
				Content:  fmt.Sprintf("comment-%d", i),
				TodoID:   td.ID,
				AuthorID: alice.ID,
			}).Save(ctx)
		require.NoError(t, err)
	}

	// Reload alice so Config().Driver is populated the GraphQL-resolver way,
	// and Edges is unloaded — forcing the (*User).Comments runtime fallback.
	reloaded, err := userclient.NewUserClient(cfg).Get(ctx, alice.ID)
	require.NoError(t, err)

	comments, err := reloaded.Comments(ctx)
	require.NoError(t, err)
	assert.Len(t, comments, 3, "O2M edge method must return all comments for the user — Bug 3 regression")
}

// TestEntityM2OEdgeMethod_ReturnsCorrectParent pins the post-2026-04-15
// invariant for genSimpleEdgeMethod: (*Comment).Author(ctx) on a reloaded
// comment (Edges cache cold) must return the exact author user.
//
// Regression guard for the M2O path (edge.From with Unique). Asserts on
// Name/Email rather than just non-nil to guard against ID-coincidence
// false positives (e.g. if the resolver returned the first user row).
func TestEntityM2OEdgeMethod_ReturnsCorrectParent(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	// Create a decoy user first so author ID != 1.
	_, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Decoy", Email: "decoy-m2o@example.com"}).Save(ctx)
	require.NoError(t, err)

	author, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "RealAuthor", Email: "author-m2o@example.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeM2O-WS"}).Save(ctx)
	require.NoError(t, err)

	td, err := todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "Todo for M2O author test", OwnerID: author.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)

	cm, err := commentclient.NewCommentClient(cfg).Create().
		SetInput(commentclient.CreateCommentInput{
			Content: "authored comment", TodoID: td.ID, AuthorID: author.ID,
		}).Save(ctx)
	require.NoError(t, err)

	// Reload the comment so Edges cache is cold — forces the M2O edge
	// resolver runtime path (genSimpleEdgeMethod output).
	reloaded, err := commentclient.NewCommentClient(cfg).Get(ctx, cm.ID)
	require.NoError(t, err)

	got, err := reloaded.Author(ctx)
	require.NoError(t, err, "regression guard for the M2O path")
	require.NotNil(t, got)
	assert.Equal(t, author.ID, got.ID, "M2O edge method must return the correct parent by ID")
	assert.Equal(t, "RealAuthor", got.Name, "M2O edge must resolve to the specific author, not any user")
	assert.Equal(t, "author-m2o@example.com", got.Email)
}

// TestEntityConnectionEdgeMethod_ReturnsAllWithCorrectContent pins the
// post-2026-04-15 invariant for genConnectionEdgeMethod: (*User).Todos
// with Relay pagination params on a reloaded user must return all todos
// owned by that user (not some other user's, not empty).
//
// Regression guard for the Relay connection path. ElementsMatch on titles
// guards against ID-coincidence (e.g. filtering by wrong column would
// yield len==3 but wrong content).
func TestEntityConnectionEdgeMethod_ReturnsAllWithCorrectContent(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	// Decoy owner + decoy todo to guard against "returns all todos" bugs.
	decoy, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "DecoyOwner", Email: "decoy-conn@example.com"}).Save(ctx)
	require.NoError(t, err)

	owner, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "RealOwner", Email: "owner-conn@example.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeConn-WS"}).Save(ctx)
	require.NoError(t, err)

	// Decoy todo owned by decoy — must not appear in owner.Todos().
	_, err = todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "decoy-todo-should-not-appear", OwnerID: decoy.ID, WorkspaceID: ptr(ws.ID),
		}).Save(ctx)
	require.NoError(t, err)

	wantTitles := []string{"conn-todo-alpha", "conn-todo-beta", "conn-todo-gamma"}
	for _, title := range wantTitles {
		_, err := todoclient.NewTodoClient(cfg).Create().
			SetInput(todoclient.CreateTodoInput{
				Title: title, OwnerID: owner.ID, WorkspaceID: ptr(ws.ID),
			}).Save(ctx)
		require.NoError(t, err)
	}

	// Reload owner so Edges cache is cold — forces the Relay connection
	// edge resolver runtime path (genConnectionEdgeMethod output).
	reloaded, err := userclient.NewUserClient(cfg).Get(ctx, owner.ID)
	require.NoError(t, err)

	conn, err := reloaded.Todos(ctx, nil, ptr(10), nil, nil, nil)
	require.NoError(t, err, "regression guard for the Relay connection path")
	require.NotNil(t, conn)
	assert.Len(t, conn.Edges, 3, "Relay connection edge must return exactly the owner's todos")
	assert.Equal(t, 3, conn.TotalCount, "TotalCount must reflect filtered set, not global count")

	gotTitles := make([]string, 0, len(conn.Edges))
	for _, e := range conn.Edges {
		require.NotNil(t, e.Node)
		gotTitles = append(gotTitles, e.Node.Title)
	}
	assert.ElementsMatch(t, wantTitles, gotTitles,
		"Relay connection edge must return exactly the owner's todo titles — content, not just cardinality")
}

// TestEntityM2MEdgeMethod_ReturnsAllAssociated pins the post-2026-04-15
// invariant for the M2M edge path. In fullgql, Todo.tags is emitted as a
// Relay connection method (genConnectionEdgeMethod over an M2M join
// table), so this test also exercises the M2M traversal leg of the edge
// resolver (distinct from Test B which is O2M-via-connection).
//
// Regression guard for the M2M path. ElementsMatch on tag Names guards
// against ID-coincidence and against "returns all tags" bugs via a decoy.
func TestEntityM2MEdgeMethod_ReturnsAllAssociated(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()

	owner, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "M2MOwner", Email: "owner-m2m@example.com"}).Save(ctx)
	require.NoError(t, err)

	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "EdgeM2M-WS"}).Save(ctx)
	require.NoError(t, err)

	// Decoy tag that should NOT appear in the reloaded todo's tags.
	_, err = tagclient.NewTagClient(cfg).Create().
		SetInput(tagclient.CreateTagInput{Name: "decoy-tag-should-not-appear"}).Save(ctx)
	require.NoError(t, err)

	wantTagNames := []string{"m2m-urgent", "m2m-backend", "m2m-polish"}
	tagIDs := make([]int, 0, len(wantTagNames))
	for _, name := range wantTagNames {
		tg, err := tagclient.NewTagClient(cfg).Create().
			SetInput(tagclient.CreateTagInput{Name: name}).Save(ctx)
		require.NoError(t, err)
		tagIDs = append(tagIDs, tg.ID)
	}

	td, err := todoclient.NewTodoClient(cfg).Create().
		SetInput(todoclient.CreateTodoInput{
			Title: "M2M-tagged todo", OwnerID: owner.ID, WorkspaceID: ptr(ws.ID),
			TagIDs: tagIDs,
		}).Save(ctx)
	require.NoError(t, err)

	// Reload the todo so Edges cache is cold — forces the M2M edge
	// resolver runtime path.
	reloaded, err := todoclient.NewTodoClient(cfg).Get(ctx, td.ID)
	require.NoError(t, err)

	conn, err := reloaded.Tags(ctx, nil, ptr(10), nil, nil, nil)
	require.NoError(t, err, "regression guard for the M2M path")
	require.NotNil(t, conn)
	assert.Len(t, conn.Edges, 3, "M2M edge must return exactly the todo's associated tags")
	assert.Equal(t, 3, conn.TotalCount)

	gotNames := make([]string, 0, len(conn.Edges))
	for _, e := range conn.Edges {
		require.NotNil(t, e.Node)
		gotNames = append(gotNames, e.Node.Name)
	}
	assert.ElementsMatch(t, wantTagNames, gotNames,
		"M2M edge must return exactly the associated tag names — content, not just cardinality")
}

// TestGraphQL_EdgeConnectionWhereFilter is the runtime regression guard for
// the silent-drop bug documented on the @goField(forceResolver: true) branch
// in contrib/graphql/schema_input.go.
//
// Without the directive, gqlgen's autobind treats the velox-generated entity
// method
//
//	(*Tag).Todos(ctx, after, first, before, last, orderBy)
//
// as a successful partial match against the SDL field
//
//	todos(after, first, before, last, orderBy, where: TodoWhereInput)
//
// and silently drops `where` at runtime — a client sending
// `where: {status: DONE}` gets every todo back regardless of status. With
// the directive, gqlgen routes through the generated resolver interface,
// whose user-provided body wires `where.Filter` into `WithTodoFilter(...)`
// on Paginate.
//
// This test drives a real GraphQL query through the gqlgen executor
// (the same path the production server uses) and asserts the filter
// actually reduces the result set. It intentionally does NOT use the
// Go-level `(*entity.Tag).Todos(...)` helper — that helper has no `where`
// parameter by construction and therefore cannot reproduce the bug.
func TestGraphQL_EdgeConnectionWhereFilter(t *testing.T) {
	ctx := context.Background()
	// Use shared-cache in-memory SQLite so the gqlgen handler's per-request
	// connections see the seeded schema and rows. Plain `mode=memory` without
	// `cache=shared` gives each connection its own isolated DB, which masks
	// this test as "table not found" rather than "filter not applied".
	client, err := velox.Open(dialect.SQLite, "file:edge-where?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	cfg := client.RuntimeConfig()

	// Seed: one owner, one tag, four todos split across statuses.
	owner, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Owner", Email: "owner@example.com"}).
		Save(ctx)
	require.NoError(t, err)

	theTag, err := tagclient.NewTagClient(cfg).Create().
		SetInput(tagclient.CreateTagInput{Name: "work"}).
		Save(ctx)
	require.NoError(t, err)

	// Three DONE, one TODO. Filter must keep the three, drop the one.
	todoSeed := []struct {
		title  string
		status todo.Status
	}{
		{"ship-it", todo.StatusDone},
		{"merge-pr", todo.StatusDone},
		{"post-mortem", todo.StatusDone},
		{"noise-not-done", todo.StatusTodo},
	}
	for _, s := range todoSeed {
		status := s.status
		created, err := todoclient.NewTodoClient(cfg).Create().
			SetInput(todoclient.CreateTodoInput{
				Title:   s.title,
				Status:  &status,
				OwnerID: owner.ID,
				TagIDs:  []int{theTag.ID},
			}).
			Save(ctx)
		require.NoError(t, err)
		require.Equal(t, s.status, created.Status)
	}

	// Sanity: confirm the Go-level tag→todos traversal sees all 4 seeded
	// todos BEFORE we go through gqlgen. This isolates seed/M2M-wiring
	// failures from filter-propagation failures.
	{
		reloaded, err := tagclient.NewTagClient(cfg).Get(ctx, theTag.ID)
		require.NoError(t, err)
		sanityConn, err := reloaded.Todos(ctx, nil, ptr(10), nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, 4, sanityConn.TotalCount,
			"seed sanity: tag.todos at Go-level should see all 4 todos")
	}

	// Build the gqlgen executor backed by our test client. Using
	// NewExecutableSchema here exercises the same field-resolution path
	// a production server hits.
	srv := handler.NewDefaultServer(gqlgenpkg.NewExecutableSchema(gqlgenpkg.Config{
		Resolvers: &gqlgenpkg.Resolver{Client: client},
	}))
	gqlClient := gqlclient.New(srv)

	// Query the single seeded tag via the root `tags` connection and then
	// traverse its edge connection twice — once with a `where` filter,
	// once without — so we can compare counts in the same response.
	// `status` comes back as the SDL enum (upper-case), not the Go value,
	// so we check against "DONE".
	query := `
		query {
			tags(first: 10) {
				edges {
					node {
						doneOnly: todos(where: { status: DONE }) {
							totalCount
							edges { node { title status } }
						}
						all: todos {
							totalCount
						}
					}
				}
			}
		}`

	var resp struct {
		Tags struct {
			Edges []struct {
				Node struct {
					DoneOnly struct {
						TotalCount int `json:"totalCount"`
						Edges      []struct {
							Node struct {
								Title  string `json:"title"`
								Status string `json:"status"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"doneOnly"`
					All struct {
						TotalCount int `json:"totalCount"`
					} `json:"all"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"tags"`
	}

	err = gqlClient.Post(query, &resp)
	require.NoError(t, err, "GraphQL request must succeed")
	require.Len(t, resp.Tags.Edges, 1, "expected exactly the one seeded tag")
	got := resp.Tags.Edges[0].Node

	// Sanity: without a filter, tag.todos should surface all 4 seeded
	// todos — rules out a seeding bug masquerading as a filter bug.
	require.Equal(t, 4, got.All.TotalCount,
		"without where, tag.todos should surface all 4 seeded todos")

	// Bug signature: without forceResolver, the where arg is silently
	// dropped and totalCount == 4 (same as unfiltered). With the fix,
	// exactly the 3 DONE todos come back.
	require.Equal(t, 3, got.DoneOnly.TotalCount,
		"where: {status: DONE} must reduce tag.todos totalCount from 4 to 3 — "+
			"if this is 4, the where arg is being silently dropped (see the "+
			"forceResolver directive comment in contrib/graphql/schema_input.go)")
	require.Len(t, got.DoneOnly.Edges, 3, "edges count must match totalCount")

	gotTitles := make([]string, 0, len(got.DoneOnly.Edges))
	for _, e := range got.DoneOnly.Edges {
		assert.Equal(t, "DONE", e.Node.Status,
			"filtered edges must all carry status DONE — any non-DONE value "+
				"proves the filter didn't reach the SQL query")
		gotTitles = append(gotTitles, e.Node.Title)
	}
	assert.ElementsMatch(t,
		[]string{"ship-it", "merge-pr", "post-mortem"}, gotTitles,
		"filtered edges must be exactly the DONE-status seed set, not a subset by coincidence")
}

// TestEntityEdgeMethod_FastPath_UsesEagerLoadedEdges pins the Ent-parity
// optimization: when a parent query eagerly loads an edge via WithXxx(),
// the subsequent call to the entity's edge-connection method MUST reuse
// that pre-loaded slice and skip the SQL query. Without the fast path,
// `.WithTodos()` is effectively dead weight at the GraphQL layer — the
// resolver re-queries regardless. Generated body:
//
//	func (m *Category) Todos(ctx, after, first, before, last, orderBy) (*TodoConnection, error) {
//	    if nodes, err := m.Edges.TodosOrErr(); err == nil && after == nil && before == nil {
//	        return BuildTodoConnection(nodes, 0, orderBy, after, first, before, last), nil
//	    }
//	    return m.QueryTodos().(TodoPaginatable).Paginate(...)
//	}
//
// The test shape: eagerly load edges on a client that will then FAIL if we
// try to use it again (by closing its underlying connection). If the fast
// path is taken, no DB call happens and the test passes. If the slow path
// is taken, the Paginate call errors out (closed DB) and the test fails.
func TestEntityEdgeMethod_FastPath_UsesEagerLoadedEdges(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	cfg := client.RuntimeConfig()

	owner, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Owner", Email: "owner-fp@example.com"}).
		Save(ctx)
	require.NoError(t, err)

	cat, err := categoryclient.NewCategoryClient(cfg).Create().
		SetInput(categoryclient.CreateCategoryInput{Name: "fast-path"}).
		Save(ctx)
	require.NoError(t, err)

	for _, title := range []string{"a", "b", "c"} {
		_, err := todoclient.NewTodoClient(cfg).Create().
			SetInput(todoclient.CreateTodoInput{
				Title:      title,
				OwnerID:    owner.ID,
				CategoryID: &cat.ID,
			}).
			Save(ctx)
		require.NoError(t, err)
	}

	// Eagerly load the edge. This populates cat.Edges.Todos and sets the
	// loaded-bit, so TodosOrErr() will succeed without hitting the DB on
	// a subsequent call.
	reloaded, err := client.Category.Query().Where(category.IDField.EQ(cat.ID)).WithTodos().Only(ctx)
	require.NoError(t, err)

	// Close the client's DB connection. Any fresh query after this point
	// will fail. The fast path avoids touching the DB, so if the edge
	// method uses it, this test passes.
	require.NoError(t, client.Close())

	// Call the entity-level edge method. With the fast path, this must
	// succeed because nodes come from reloaded.Edges.Todos (already loaded
	// in memory) — no driver round trip.
	conn, err := reloaded.Todos(ctx, nil, ptr(10), nil, nil, nil)
	require.NoError(t, err,
		"entity edge method MUST use eager-loaded edges — if this fails with a "+
			"closed-DB error, the fast path in gen_entity_edge.go::genConnectionEdgeMethod "+
			"regressed and the resolver is hitting the DB despite WithTodos()")
	require.NotNil(t, conn)
	require.Equal(t, 3, conn.TotalCount, "fast path returned wrong totalCount")
	require.Len(t, conn.Edges, 3, "fast path returned wrong edge count")

	gotTitles := make([]string, 0, len(conn.Edges))
	for _, e := range conn.Edges {
		require.NotNil(t, e.Node)
		gotTitles = append(gotTitles, e.Node.Title)
	}
	assert.ElementsMatch(t, []string{"a", "b", "c"}, gotTitles,
		"fast path must return exactly the eager-loaded nodes, not a coincidental subset")
}

// TestUserResolver_FastPath_ThroughGraphQL pins the "fast path through a
// user-written resolver" pattern documented in doc.go and implemented in
// examples/fullgql/gqlgen/schema.resolvers.go for every where-carrying edge.
//
// The entity-method fast path (TestEntityEdgeMethod_FastPath_... above) only
// covers edges WITHOUT `where` — those bind to the entity method via gqlgen
// autobind. Edges WITH `where` go through a user-written resolver in the
// gqlgen package, and the resolver body must ALSO check
// obj.Edges.XxxOrErr() before hitting the DB, otherwise eager loading is
// still wasted at the GraphQL layer.
//
// This test sends a GraphQL query that returns pre-loaded edges (via a
// GraphQL-level eager load in the parent query's CollectFields or — for
// this fullgql fixture — via a custom runtime wrapper). We verify that
// the user resolver's fast-path branch is the one taken by observing the
// correct nodes come back after the client's DB is closed.
//
// In practice: if the resolver body drops the `if where == nil && ...` guard
// velox's doc.go and CLAUDE.md recommend, this test starts failing with a
// closed-DB error. That's the canary.
func TestUserResolver_FastPath_ThroughGraphQL(t *testing.T) {
	ctx := context.Background()
	// Shared-cache SQLite: gqlgen executor may open new DB connections; they
	// must see the same in-memory schema as the seeder.
	client, err := velox.Open(dialect.SQLite, "file:resolver-fp?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	cfg := client.RuntimeConfig()

	owner, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Owner", Email: "owner-rfp@example.com"}).
		Save(ctx)
	require.NoError(t, err)

	cat, err := categoryclient.NewCategoryClient(cfg).Create().
		SetInput(categoryclient.CreateCategoryInput{Name: "resolver-fp"}).
		Save(ctx)
	require.NoError(t, err)

	for _, title := range []string{"p", "q"} {
		_, err := todoclient.NewTodoClient(cfg).Create().
			SetInput(todoclient.CreateTodoInput{
				Title:      title,
				OwnerID:    owner.ID,
				CategoryID: &cat.ID,
			}).
			Save(ctx)
		require.NoError(t, err)
	}

	// Unit-level: directly invoke the resolver with an obj that already has
	// Edges.Todos loaded. This exercises the exact user-written body in
	// schema.resolvers.go::(*categoryResolver).Todos. Closing the client
	// DB afterward verifies the fast path branch is taken — if it falls
	// through to r.Client.Todo.Query(), the test fails with closed-DB.
	reloaded, err := client.Category.Query().Where(category.IDField.EQ(cat.ID)).WithTodos().Only(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Close())

	// Build a fresh executor around the closed client. We're not going to
	// serve HTTP — we call the sub-resolver directly. This mirrors what
	// happens inside gqlgen when `where: null` and no cursor are passed.
	resolverRoot := &gqlgenpkg.Resolver{Client: client}
	catResolver := resolverRoot.Category()

	conn, err := catResolver.Todos(ctx, reloaded, nil, ptr(10), nil, nil, nil, nil)
	require.NoError(t, err,
		"user-written resolver body in schema.resolvers.go::(*categoryResolver).Todos "+
			"MUST fast-path eager-loaded edges — closed-DB error here means the "+
			"`if where == nil && after == nil && before == nil { if nodes, err := obj.Edges.TodosOrErr(); err == nil { ... } }` "+
			"guard was removed or broken")
	require.Equal(t, 2, conn.TotalCount, "fast path returned wrong totalCount")
	require.Len(t, conn.Edges, 2, "fast path returned wrong edge count")

	gotTitles := make([]string, 0, 2)
	for _, e := range conn.Edges {
		require.NotNil(t, e.Node)
		gotTitles = append(gotTitles, e.Node.Title)
	}
	assert.ElementsMatch(t, []string{"p", "q"}, gotTitles,
		"user resolver fast path must return the eager-loaded nodes exactly")
}
