package edgeschema_test

import (
	"context"
	"testing"
	"time"

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

// TestEdgeSchema_M2MThroughAppliesJoinDefaults pins that adding an M2M edge
// through an edge schema fills the join entity's defaults. The join row was
// written with only the two keys, so Membership.joined_at (Default(time.Now),
// NOT NULL) failed every AddGroupIDs / AddUserIDs with a constraint error;
// only Membership.Create() worked. Ent runs the join entity's defaults for
// the edge; velox reads them from a registry the join entity fills at init.
func TestEdgeSchema_M2MThroughAppliesJoinDefaults(t *testing.T) {
	ctx := context.Background()
	client, err := velox.Open("sqlite", "file:edge_defaults.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()
	require.NoError(t, client.Schema.Create(ctx))

	g1 := client.Group.Create().SetName("g1").SaveX(ctx)
	g2 := client.Group.Create().SetName("g2").SaveX(ctx)
	before := time.Now().Add(-time.Minute)

	u, err := client.User.Create().SetName("A").SetEmail("a@x").AddGroupIDs(g1.ID).Save(ctx)
	require.NoError(t, err, "create through the M2M edge")
	_, err = client.User.UpdateOne(u).AddGroupIDs(g2.ID).Save(ctx)
	require.NoError(t, err, "update through the M2M edge")
	_, err = client.Group.Create().SetName("g3").AddUserIDs(u.ID).Save(ctx)
	require.NoError(t, err, "create from the inverse side")

	ms, err := client.Membership.Query().Where(membership.UserIDField.EQ(u.ID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, ms, 3)
	for _, m := range ms {
		assert.Equal(t, membership.RoleMember, m.Role, "static default")
		assert.True(t, m.JoinedAt.After(before), "Default(time.Now) applied: %v", m.JoinedAt)
	}
}

// TestEdgeSchema_ThroughEdgeTargetsTheJoinTable pins that mutating the
// generated through edge (User.memberships) addresses the join table. Its
// edge spec said M2O, which means "the key is on the users table", so
// AddMembershipIDs / ClearMemberships built SQL against users.user_id and
// failed with "no such column". It is O2M with the key on memberships (Ent
// emits O2M, Inverse: true); the join row's user_id is NOT NULL, so moving
// or clearing it is a constraint error from the right table.
func TestEdgeSchema_ThroughEdgeTargetsTheJoinTable(t *testing.T) {
	ctx := context.Background()
	client, err := velox.Open("sqlite", "file:edge_through.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()
	require.NoError(t, client.Schema.Create(ctx))

	a := client.User.Create().SetName("A").SetEmail("a@x").SaveX(ctx)
	b := client.User.Create().SetName("B").SetEmail("b@x").SaveX(ctx)
	g := client.Group.Create().SetName("g").SaveX(ctx)
	m := client.Membership.Create().SetUserID(a.ID).SetGroupID(g.ID).SaveX(ctx)

	_, err = client.User.UpdateOne(b).AddMembershipIDs(m.ID).Save(ctx)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "no such column", "SQL must target the memberships table")
	assert.True(t, velox.IsConstraintError(err), "moving an owned membership: got %v", err)

	_, err = client.User.UpdateOne(a).ClearMemberships().Save(ctx)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "no such column")

	got, err := client.Membership.Get(ctx, m.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.UserID, "the failed mutations changed nothing")
}
