package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	petclient "github.com/syssam/velox/tests/integration/client/pet"
)

// TestFieldBoundEdge_MutationStateIsTheField pins that an edge bound to a
// field (Pet.owner via .Field("owner_id")) reads and writes the field's
// state, as in Ent. The edge had its own map and flag that nothing wrote or
// read: after SetOwnerID a hook saw OwnerIDs() == [] and no "owner" in
// AddedEdges(), ClearOwnerID was invisible to OwnerCleared()/ClearedEdges(),
// and ClearOwner()/ClearEdge("owner") returned without clearing anything —
// so generic hooks and privacy rules keyed on edge changes never fired.
func TestFieldBoundEdge_MutationStateIsTheField(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	u1 := createUser(t, c, "u1", "u1@x")
	u2 := createUser(t, c, "u2", "u2@x")
	pet, err := c.Pet.Create().SetName("rex").SetOwnerID(u1.ID).Save(ctx)
	require.NoError(t, err)

	var seen *petclient.PetMutation
	var clearInHook func(*petclient.PetMutation)
	c.Pet.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			pm := m.(*petclient.PetMutation)
			if clearInHook != nil {
				clearInHook(pm)
			}
			seen = pm
			return next.Mutate(ctx, m)
		})
	})

	_, err = c.Pet.UpdateOneID(pet.ID).SetOwnerID(u2.ID).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []int{u2.ID}, seen.OwnerIDs())
	require.Contains(t, seen.AddedEdges(), "owner")
	require.Equal(t, []integration.Value{u2.ID}, seen.AddedIDs("owner"))

	_, err = c.Pet.UpdateOneID(pet.ID).ClearOwnerID().Save(ctx)
	require.NoError(t, err)
	require.True(t, seen.OwnerCleared())
	require.Contains(t, seen.ClearedEdges(), "owner")
	require.True(t, seen.EdgeCleared("owner"))

	for name, clear := range map[string]func(*petclient.PetMutation){
		"ClearOwner": func(m *petclient.PetMutation) { m.ClearOwner() },
		"ClearEdge":  func(m *petclient.PetMutation) { require.NoError(t, m.ClearEdge("owner")) },
	} {
		_, err = c.Pet.UpdateOneID(pet.ID).SetOwnerID(u1.ID).Save(ctx)
		require.NoError(t, err)
		clearInHook = clear
		_, err = c.Pet.UpdateOneID(pet.ID).SetName("renamed").Save(ctx)
		clearInHook = nil
		require.NoError(t, err, name)
		got, err := c.Pet.Get(ctx, pet.ID)
		require.NoError(t, err)
		require.Nil(t, got.OwnerID, "%s in a hook must clear owner_id", name)
	}
}
