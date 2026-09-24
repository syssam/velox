package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"

	"github.com/google/uuid"

	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/token"
	"github.com/syssam/velox/tests/integration/user"
)

// TestUpsertDoNothing_SkipsEdgesOfTheSkippedRow pins that an ON CONFLICT DO
// NOTHING create that hits a duplicate writes nothing — not its edges
// either. RETURNING yields no row, the ID stayed unset, and sqlgraph went on
// to insert the edges with a nil ID: the M2M join insert failed with an
// internal NOT NULL error, and an O2M add to a child the existing row
// already owns failed as "already connected to a different owner_id".
func TestUpsertDoNothing_SkipsEdgesOfTheSkippedRow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		alice := createUser(t, c, "alice", "alice@x")
		post := createPost(t, c, alice, "p", "c")

		t.Run("m2m", func(t *testing.T) {
			_, err := c.Tag.Create().SetName("go").Save(ctx)
			require.NoError(t, err)
			err = c.Tag.Create().SetName("go").AddPostIDs(post.ID).
				OnConflictColumns(tag.FieldName).DoNothing().Exec(ctx)
			require.NoError(t, err)
			n, err := post.QueryTags().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, n, "the skipped row's edge was written")
		})

		t.Run("o2m", func(t *testing.T) {
			pet, err := c.Pet.Create().SetName("rex").SetOwnerID(alice.ID).Save(ctx)
			require.NoError(t, err)
			err = c.User.Create().SetName("alice2").SetEmail("alice@x").SetAge(1).
				SetCreatedAt(now).SetUpdatedAt(now).AddPetIDs(pet.ID).
				OnConflictColumns(user.FieldEmail).DoNothing().Exec(ctx)
			require.NoError(t, err)
			got, err := c.Pet.Get(ctx, pet.ID)
			require.NoError(t, err)
			require.Equal(t, alice.ID, *got.OwnerID)
		})

		// The skipped create reports no ID: 0 for an auto-increment key and the
		// zero UUID for a caller-generated one. It used to hand back the UUID
		// generated for the row that was never inserted.
		t.Run("returned_id", func(t *testing.T) {
			_, err := c.Token.Create().SetName("dn").Save(ctx)
			require.NoError(t, err)
			id, err := c.Token.Create().SetName("dn").
				OnConflictColumns(token.FieldName).DoNothing().ID(ctx)
			require.NoError(t, err)
			require.Equal(t, uuid.Nil, id)

			uid, err := c.User.Create().SetName("x").SetEmail("alice@x").SetAge(1).
				SetCreatedAt(now).SetUpdatedAt(now).
				OnConflictColumns(user.FieldEmail).DoNothing().ID(ctx)
			require.NoError(t, err)
			require.Zero(t, uid)
		})
	})
}
