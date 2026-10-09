package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
)

// TestUpdateOneExec_SkipsReadBackWithoutHooks pins that UpdateOne.Exec, which
// returns no entity, does not run the SELECT that reads the updated row back
// — and that everything which can observe that row still gets it: Save, and
// Exec once a hook is registered (a hook receives the saved entity as the
// mutation's value).
func TestUpdateOneExec_SkipsReadBackWithoutHooks(t *testing.T) {
	ctx := context.Background()
	client, cd := openCountingClient(t)
	p := createPost(t, client, createUser(t, client, "Ann", "ann@example.com"), "first", "body")

	selects := func(fn func()) int {
		before := cd.countByPrefix("SELECT")
		fn()
		return cd.countByPrefix("SELECT") - before
	}

	n := selects(func() {
		require.NoError(t, client.Post.UpdateOneID(p.ID).SetTitle("second").Exec(ctx))
	})
	assert.Zero(t, n, "Exec without hooks must not read the row back")
	assert.Equal(t, "second", client.Post.GetX(ctx, p.ID).Title, "the update itself must still be written")

	n = selects(func() {
		got, err := client.Post.UpdateOneID(p.ID).SetTitle("third").Save(ctx)
		require.NoError(t, err)
		assert.Equal(t, "third", got.Title)
	})
	assert.Equal(t, 1, n, "Save returns the entity, so it reads it back")

	// A missing row is reported by the UPDATE, not by the read-back.
	err := client.Post.UpdateOneID(p.ID + 1000).SetTitle("x").Exec(ctx)
	assert.True(t, integration.IsNotFound(err), "got %v", err)

	// Select() columns are validated even though Exec reads nothing.
	err = client.Post.UpdateOneID(p.ID).SetTitle("fourth").Select("no_such_column").Exec(ctx)
	require.Error(t, err)

	// The same builder used for Exec and then Save returns the entity.
	b := client.Post.UpdateOneID(p.ID).SetTitle("fifth")
	require.NoError(t, b.Exec(ctx))

	// With a hook, the hook must see the saved entity.
	var seen any
	client.Post.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			v, err := next.Mutate(ctx, m)
			seen = v
			return v, err
		})
	})
	n = selects(func() {
		require.NoError(t, client.Post.UpdateOneID(p.ID).SetTitle("sixth").Exec(ctx))
	})
	assert.Equal(t, 1, n, "Exec with a hook must read the row back for the hook")
	saved, ok := seen.(*entity.Post)
	require.True(t, ok, "hook value is %T", seen)
	require.NotNil(t, saved)
	assert.Equal(t, "sixth", saved.Title)
}
