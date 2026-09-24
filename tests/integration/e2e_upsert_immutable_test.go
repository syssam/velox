package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/integration/token"
	"github.com/syssam/velox/tests/integration/user"
)

// TestUpsertUpdateNewValues_KeepsImmutableColumns pins that UpdateNewValues
// leaves immutable columns and a user-defined ID alone on conflict. It
// rendered only ResolveWithNewValues, so the conflict UPDATE rewrote every
// inserted column: created_at took the new row's value, and a UUID primary
// key was replaced by the freshly generated one — leaving any reference to
// the old ID dangling. Ent adds a SetIgnore for the ID and each immutable
// field (feature/upsert.tmpl).
func TestUpsertUpdateNewValues_KeepsImmutableColumns(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)

	t.Run("immutable_field", func(t *testing.T) {
		old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		u, err := c.User.Create().SetName("a").SetEmail("up@x").SetAge(30).
			SetCreatedAt(old).SetUpdatedAt(old).Save(ctx)
		require.NoError(t, err)

		err = c.User.Create().SetName("b").SetEmail("up@x").SetAge(31).
			SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).
			OnConflictColumns(user.FieldEmail).UpdateNewValues().Exec(ctx)
		require.NoError(t, err)

		got, err := c.User.Get(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, "b", got.Name, "mutable columns take the new values")
		require.True(t, got.CreatedAt.Equal(old), "immutable created_at was overwritten: %v", got.CreatedAt)
	})

	t.Run("user_defined_id", func(t *testing.T) {
		tk, err := c.Token.Create().SetName("tok").Save(ctx)
		require.NoError(t, err)

		err = c.Token.Create().SetName("tok").SetWeight(5).
			OnConflictColumns(token.FieldName).UpdateNewValues().Exec(ctx)
		require.NoError(t, err)

		ids, err := c.Token.Query().IDs(ctx)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		require.Equal(t, tk.ID, ids[0], "the conflict UPDATE replaced the primary key")
	})

	// On the conflict path the stored row keeps its ID, and RETURNING
	// reports it; the builder must hand that back rather than the UUID it
	// generated for the row it did not insert (Ent parity, create.tmpl).
	t.Run("returned_id_is_the_stored_row", func(t *testing.T) {
		tk, err := c.Token.Create().SetName("ret").Save(ctx)
		require.NoError(t, err)

		id, err := c.Token.Create().SetName("ret").SetWeight(7).
			OnConflictColumns(token.FieldName).UpdateNewValues().ID(ctx)
		require.NoError(t, err)
		require.Equal(t, tk.ID, id, "UpdateNewValues")

		id, err = c.Token.Create().SetName("ret").
			OnConflictColumns(token.FieldName).Ignore().ID(ctx)
		require.NoError(t, err)
		require.Equal(t, tk.ID, id, "Ignore")
	})
}
