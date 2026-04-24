package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/examples/realworld/velox"
	taskclient "github.com/syssam/velox/examples/realworld/velox/client/task"
	userclient "github.com/syssam/velox/examples/realworld/velox/client/user"
	workspaceclient "github.com/syssam/velox/examples/realworld/velox/client/workspace"
	"github.com/syssam/velox/examples/realworld/velox/entity"
	"github.com/syssam/velox/examples/realworld/velox/task"
	"github.com/syssam/velox/examples/realworld/velox/user"
	"github.com/syssam/velox/privacy"

	_ "modernc.org/sqlite"
)

func ptr[T any](v T) *T { return &v }

// adminCtx returns a context with an admin viewer — required because the
// User/Workspace mutation policies call HasRole("admin") and all query
// policies call DenyIfNoViewer().
func adminCtx() context.Context {
	return privacy.WithViewer(context.Background(), &privacy.SimpleViewer{
		UserID:    "admin-1",
		UserRoles: []string{"admin"},
	})
}

func openTestClient(t *testing.T) *velox.Client {
	t.Helper()
	client, err := velox.Open(dialect.SQLite, "file:realworld_e2e?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.Schema.Create(adminCtx()))
	return client
}

// TestRealworld_WorkspaceTaskFlow_EdgeResolution drives a realistic
// multi-entity flow through the generated ORM + GraphQL edge methods:
// create workspace, create tasks, then resolve edges (O2M pagination, M2O)
// exactly as a GraphQL resolver would at request time. Also covers the
// Unwrap() tx-boundary contract.
func TestRealworld_WorkspaceTaskFlow_EdgeResolution(t *testing.T) {
	client := openTestClient(t)
	ctx := adminCtx()

	// Seed a user (for completeness — realworld's schema doesn't
	// connect users to tasks via edges, but the scenario is more
	// realistic with an admin on record).
	alice, err := client.User.Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "alice@example.com", Role: ptr(user.RoleAdmin)}).
		Save(ctx)
	require.NoError(t, err)
	require.NotZero(t, alice.ID)

	// Create a workspace.
	ws, err := client.Workspace.Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "Alpha"}).
		Save(ctx)
	require.NoError(t, err)

	// Create 3 tasks in that workspace.
	titles := []string{"Design schema", "Implement API", "Write tests"}
	for _, title := range titles {
		_, err := client.Task.Create().
			SetInput(taskclient.CreateTaskInput{
				Title:       title,
				WorkspaceID: ws.ID,
				Priority:    ptr(task.PriorityHigh),
			}).
			Save(ctx)
		require.NoError(t, err)
	}

	t.Run("O2M_RelayConnection_WorkspaceTasks", func(t *testing.T) {
		// Reload workspace cold — Edges cache unpopulated.
		loaded, err := client.Workspace.Get(ctx, ws.ID)
		require.NoError(t, err)

		conn, err := loaded.Tasks(ctx, nil, ptr(10), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Equal(t, 3, conn.TotalCount)
		require.Len(t, conn.Edges, 3)

		got := make([]string, 0, len(conn.Edges))
		for _, e := range conn.Edges {
			require.NotNil(t, e.Node)
			got = append(got, e.Node.Title)
		}
		assert.ElementsMatch(t, titles, got)
	})

	t.Run("M2O_TaskWorkspace", func(t *testing.T) {
		// Pick one task and reload cold.
		tasks, err := client.Task.Query().All(ctx)
		require.NoError(t, err)
		require.Len(t, tasks, 3)

		loaded, err := client.Task.Get(ctx, tasks[0].ID)
		require.NoError(t, err)

		gotWS, err := loaded.Workspace(ctx)
		require.NoError(t, err)
		require.NotNil(t, gotWS)
		require.Equal(t, ws.ID, gotWS.ID)
		require.Equal(t, "Alpha", gotWS.Name)
	})

	t.Run("TxCreatedTaskEdgeReadsAfterUnwrap", func(t *testing.T) {
		var createdTask *entity.Task

		err := velox.WithTx(ctx, client, func(tx *velox.Tx) error {
			ct, terr := tx.Task.Create().
				SetInput(taskclient.CreateTaskInput{
					Title:       "Tx-created",
					WorkspaceID: ws.ID,
				}).
				Save(ctx)
			if terr != nil {
				return terr
			}
			createdTask = ct
			return nil
		})
		require.NoError(t, err)
		require.NotNil(t, createdTask)

		// Without Unwrap, edge reads on a tx-created entity must fail
		// post-commit — this is the documented Unwrap() contract.
		_, readErr := createdTask.Workspace(ctx)
		require.Error(t, readErr, "edge read on un-Unwrapped tx entity must fail post-commit")

		// After Unwrap, the entity is detached from the committed
		// tx driver and reads succeed.
		createdTask.Unwrap()
		gotWS, err := createdTask.Workspace(ctx)
		require.NoError(t, err)
		require.NotNil(t, gotWS)
		require.Equal(t, ws.ID, gotWS.ID)
	})
}
