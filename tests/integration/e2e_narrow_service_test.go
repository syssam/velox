package integration_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/integration/narrowsvc"
	"github.com/syssam/velox/tests/integration/post"
)

// TestNarrowService_ReadsWritesAndTransacts drives a service that never names
// the root Client or Tx (see package narrowsvc) with a real client: entity
// clients built with From read and write through the client's driver, see
// the hooks registered on it, and — built from the ConfigProvider RunTx hands
// out — run inside the transaction, committed or rolled back as a unit.
func TestNarrowService_ReadsWritesAndTransacts(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	u := createUser(t, client, "Ann", "ann@example.com")
	svc := narrowsvc.New(client)

	got, err := svc.UserByEmail(ctx, "ann@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	// A hook registered on the root client reaches mutations made through
	// From: both write to the same hook store.
	var hooked []string
	client.User.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			hooked = append(hooked, m.Op().String())
			return next.Mutate(ctx, m)
		})
	})

	require.NoError(t, svc.RenameWithPost(ctx, u.ID, "Ann B", "kept", false))
	assert.Equal(t, "Ann B", client.User.GetX(ctx, u.ID).Name)
	kept, err := client.Post.Query().Where(post.TitleField.EQ("kept")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, kept)
	assert.Len(t, hooked, 1, "the user update must run the client's hook")

	// Rolled back as a unit: neither the rename nor the post survives.
	err = svc.RenameWithPost(ctx, u.ID, "Ann C", "dropped", true)
	require.ErrorIs(t, err, narrowsvc.ErrRejected)
	assert.Equal(t, "Ann B", client.User.GetX(ctx, u.ID).Name)
	dropped, err := client.Post.Query().Where(post.TitleField.EQ("dropped")).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, dropped)

	// StartTx: the caller ends the transaction itself.
	require.NoError(t, svc.Rename(ctx, u.ID, "Ann D", true))
	assert.Equal(t, "Ann D", client.User.GetX(ctx, u.ID).Name)
	require.NoError(t, svc.Rename(ctx, u.ID, "Ann E", false))
	assert.Equal(t, "Ann D", client.User.GetX(ctx, u.ID).Name, "a transaction that was not committed must not be kept")

	// A client already bound to a transaction cannot start another one: the
	// error must come back with a nil Transaction, not a typed-nil *Tx.
	outer, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = outer.Rollback() }()
	inner, err := outer.Client().StartTx(ctx)
	require.Error(t, err)
	require.Nil(t, inner)
}

// TestNarrowService_DoesNotImportRoot pins what makes the pattern worth
// using: the service package depends on the entity client packages it names
// and not on the generated root package, whose Client type reaches every
// entity's client. An import of the root package here is the regression.
func TestNarrowService_DoesNotImportRoot(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	out, err := exec.Command(goBin, "list", "-deps", "./narrowsvc").CombinedOutput()
	require.NoError(t, err, string(out))
	deps := strings.Fields(string(out))
	require.Contains(t, deps, "github.com/syssam/velox/tests/integration/client/user")
	for _, banned := range []string{
		"github.com/syssam/velox/tests/integration",
		"github.com/syssam/velox/tests/integration/client/comment",
		"github.com/syssam/velox/tests/integration/query",
	} {
		assert.NotContains(t, deps, banned, "narrowsvc must not depend on %s", banned)
	}
}
