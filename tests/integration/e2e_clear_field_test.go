package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
)

// TestClearField_OnlyNillableFieldsAreClearable pins that the generic
// ClearField agrees with the typed API: a field without a ClearXxx (Optional
// but not Nillable — velox keeps those NOT NULL) is rejected. ClearField
// accepted it, recorded it in ClearedFields() — so hook.HasClearedFields
// conditions fired — and the UPDATE then ignored it, leaving the value.
func TestClearField_OnlyNillableFieldsAreClearable(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	u := createUser(t, c, "alice", "alice@x")
	createPost(t, c, u, "p", "content")

	var clearErr error
	var cleared []string
	c.Post.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			if m.Op().Is(integration.OpUpdateOne) {
				clearErr = m.ClearField("content")
				cleared = m.ClearedFields()
			}
			return next.Mutate(ctx, m)
		})
	})
	p, err := c.Post.Query().Only(ctx)
	require.NoError(t, err)
	_, err = c.Post.UpdateOneID(p.ID).SetTitle("t2").Save(ctx)
	require.NoError(t, err)
	require.ErrorContains(t, clearErr, "content", "ClearField on a NOT NULL field must fail")
	require.Empty(t, cleared, "a rejected clear must not show in ClearedFields")

	// A Nillable field still clears.
	nick := "nick"
	_, err = c.User.UpdateOneID(u.ID).SetNillableNickname(&nick).Save(ctx)
	require.NoError(t, err)
	c.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			if m.Op().Is(integration.OpUpdateOne) {
				require.NoError(t, m.ClearField("nickname"))
			}
			return next.Mutate(ctx, m)
		})
	})
	got, err := c.User.UpdateOneID(u.ID).SetName("alice2").Save(ctx)
	require.NoError(t, err)
	require.Nil(t, got.Nickname)
}
