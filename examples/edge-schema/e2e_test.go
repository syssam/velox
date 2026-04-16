package edgeschema_test

import (
	"context"
	"testing"

	"example.com/edge-schema/velox"
	"example.com/edge-schema/velox/membership"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// TestEdgeSchema demonstrates M2M with an intermediate entity.
//
// Scenario: Alice joins two groups. Each membership records when she joined
// and her role in that group. The Membership table is not just a join —
// it carries role + joined_at as first-class data.
//
// This is the canonical Django-style M2M with intermediate model, exposed
// as first-class types so the extra fields (role, joined_at) are queryable
// and mutable like any other entity.
func TestEdgeSchema(t *testing.T) {
	ctx := context.Background()
	client, err := velox.Open("sqlite", "file:edge.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()

	require.NoError(t, client.Schema.Create(ctx))

	// Seed a user and two groups.
	alice := client.User.Create().
		SetName("Alice").SetEmail("alice@example.com").
		SaveX(ctx)

	engineering := client.Group.Create().SetName("Engineering").SaveX(ctx)
	design := client.Group.Create().SetName("Design").SaveX(ctx)

	// Create two memberships with different roles. This is where the edge
	// schema earns its keep — we're not just joining two rows, we're
	// recording how and when the relationship was formed.
	client.Membership.Create().
		SetUserID(alice.ID).
		SetGroupID(engineering.ID).
		SetRole(membership.RoleOwner).
		SaveX(ctx)

	client.Membership.Create().
		SetUserID(alice.ID).
		SetGroupID(design.ID).
		SetRole(membership.RoleMember).
		SaveX(ctx)

	// --- 1. Query all memberships for a user, including the extra fields. ---
	memberships, err := client.Membership.Query().
		Where(membership.UserIDField.EQ(alice.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, memberships, 2)

	for _, m := range memberships {
		assert.False(t, m.JoinedAt.IsZero(), "joined_at default should auto-fill")
		assert.NotEmpty(t, m.Role)
	}

	// --- 2. Filter by role — something a plain join table can't do. ---
	owned, err := client.Membership.Query().
		Where(
			membership.UserIDField.EQ(alice.ID),
			membership.RoleField.EQ(membership.RoleOwner),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	assert.Equal(t, engineering.ID, owned[0].GroupID)
}
