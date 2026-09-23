package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/pet"
	"github.com/syssam/velox/tests/integration/user"
)

// TestEdgeFieldFK_ProjectedParentKeepsM2OEdge pins that a projected query
// still loads a to-one edge whose foreign key is a user-declared field
// (edge.From("owner", ...).Field("owner_id")). The key is an ordinary column,
// so Select(name) left it out and the loader had nothing to join on: Owner
// came back nil with no error. Ent adds the key to the projection whenever
// the edge is loaded; so does velox now.
func TestEdgeFieldFK_ProjectedParentKeepsM2OEdge(t *testing.T) {
	c := openTestClient(t)
	ctx := context.Background()
	u := createUser(t, c, "owner", "owner@pet")
	_, err := c.Pet.Create().SetName("rex").SetOwnerID(u.ID).Save(ctx)
	require.NoError(t, err)

	check := func(t *testing.T, got []*entity.Pet) {
		t.Helper()
		require.Len(t, got, 1)
		assert.Equal(t, "rex", got[0].Name)
		require.NotNil(t, got[0].Edges.Owner, "owner dropped by the projection")
		assert.Equal(t, u.ID, got[0].Edges.Owner.ID)
	}
	t.Run("WithOwner().Select", func(t *testing.T) {
		got, err := c.Pet.Query().WithOwner().Select(pet.FieldName).All(ctx)
		require.NoError(t, err)
		check(t, got)
	})
	t.Run("WithEdgeLoad on a projected query", func(t *testing.T) {
		q := c.Pet.Query()
		edgeLoader(t, q).WithEdgeLoad(pet.EdgeOwner)
		edgeLoader(t, q).GetCtx().AppendFieldOnce(pet.FieldName)
		got, err := q.All(ctx)
		require.NoError(t, err)
		check(t, got)
	})
	t.Run("nested under a projected edge query", func(t *testing.T) {
		q := c.User.Query()
		edgeLoader(t, q).WithEdgeLoad(user.EdgePets, runtime.Select(pet.FieldName), runtime.WithEdge(pet.EdgeOwner))
		users, err := q.All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 1)
		require.Len(t, users[0].Edges.Pets, 1)
		require.NotNil(t, users[0].Edges.Pets[0].Edges.Owner)
		assert.Equal(t, u.ID, users[0].Edges.Pets[0].Edges.Owner.ID)
	})
}

// TestMultiDialect_EdgeFieldFK_PartitionLimitWithNullKeys limits a to-many
// edge whose foreign key is a nullable user-declared field, with rows that
// belong to no parent mixed in. The partition is the key column; rows with a
// NULL key must never be counted against a parent.
func TestMultiDialect_EdgeFieldFK_PartitionLimitWithNullKeys(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		u1 := createUser(t, c, "a", "a@petnull")
		u2 := createUser(t, c, "b", "b@petnull")
		for range 4 {
			_, err := c.Pet.Create().SetName("x").SetOwnerID(u1.ID).Save(ctx)
			require.NoError(t, err)
			_, err = c.Pet.Create().SetName("y").SetOwnerID(u2.ID).Save(ctx)
			require.NoError(t, err)
			_, err = c.Pet.Create().SetName("stray").Save(ctx)
			require.NoError(t, err)
		}
		q := c.User.Query()
		edgeLoader(t, q).WithEdgeLoad(user.EdgePets, runtime.Limit(3), runtime.Select(pet.FieldName))
		got, err := q.All(ctx)
		require.NoError(t, err)
		require.Len(t, got, 2)
		for _, u := range got {
			require.Len(t, u.Edges.Pets, 3, u.Name)
			for _, p := range u.Edges.Pets {
				require.NotNil(t, p.OwnerID)
				assert.Equal(t, u.ID, *p.OwnerID)
			}
		}
	})
}
