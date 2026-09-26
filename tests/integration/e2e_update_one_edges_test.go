package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/pet"
	"github.com/syssam/velox/tests/integration/tag"
	testschema "github.com/syssam/velox/testschema"
)

// TestMultiDialect_UpdateOneEdgesOnlyChecksPredicate pins that UpdateOne
// checks its predicate before writing edges when no column changes. The
// predicate was applied only by the UPDATE statement, and with nothing to
// update there was none: UpdateOne(id).Where(p).AddXxxIDs(...) linked a row
// p excludes, and — since a mutation policy's row filter arrives as the
// same predicate — a filtered-out row could be linked by any caller.
func TestMultiDialect_UpdateOneEdgesOnlyChecksPredicate(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		base := context.Background()
		alice := createUser(t, c, "alice", "alice@uoe")
		p := createPost(t, c, alice, "p", "c")
		tg := createTag(t, c, "go")

		_, err := c.Tag.UpdateOne(tg).Where(tag.NameField.EQ("does-not-match")).AddPostIDs(p.ID).Save(base)
		require.True(t, velox.IsNotFound(err), "the predicate excludes the row: %v", err)
		n, err := c.Tag.QueryPosts(tg).Count(base)
		require.NoError(t, err)
		require.Zero(t, n, "no edge written")

		_, err = c.Tag.UpdateOne(tg).Where(tag.NameField.EQ("go")).AddPostIDs(p.ID).Save(base)
		require.NoError(t, err, "a matching predicate still writes the edge")
		n, err = c.Tag.QueryPosts(tg).Count(base)
		require.NoError(t, err)
		require.Equal(t, 1, n)

		// The mutation policy's row filter: bob is outside alice's scope.
		bob := createUser(t, c, "bob", "bob@uoe")
		rex, err := c.Pet.Create().SetName("rex").Save(base)
		require.NoError(t, err)
		scoped := testschema.FilterUserMutationToNameContext(base, "alice")
		_, err = c.User.UpdateOne(bob).SkipDefaults().AddPetIDs(rex.ID).Save(scoped)
		require.Error(t, err, "bob is outside the policy's scope")
		got, err := c.Pet.Query().Where(pet.IDField.EQ(rex.ID)).Only(base)
		require.NoError(t, err)
		require.Nil(t, got.OwnerID, "the pet was not attached to bob")
	})
}
