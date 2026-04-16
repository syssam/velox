package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"
)

// TestClientMutate_CreateUser verifies that client.Mutate(ctx, mutation)
// dispatches to the registered User mutator and persists the entity.
// This is the dynamic entry point used by GraphQL resolvers and generic
// tooling that holds a velox.Mutation without knowing the concrete entity
// type at compile time.
func TestClientMutate_CreateUser(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Construct the mutation directly and populate it — the dynamic dispatch
	// path doesn't need to go through the typed builder, only through a
	// velox.Mutation-conforming value.
	m := user.NewUserMutation(client.RuntimeConfig(), integration.OpCreate)
	m.SetName("MutateViaClient")
	m.SetEmail("mutate@test.com")
	m.SetAge(30)
	m.SetRole(user.RoleUser)
	m.SetCreatedAt(now)
	m.SetUpdatedAt(now)

	val, err := client.Mutate(ctx, m)
	require.NoError(t, err)
	u, ok := val.(*entity.User)
	require.True(t, ok, "client.Mutate should return a *entity.User for a User create; got %T", val)
	assert.NotZero(t, u.ID)
	assert.Equal(t, "MutateViaClient", u.Name)

	// Verify it round-trips through a normal Query.
	got, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "mutate@test.com", got.Email)
}

// TestClientMutate_UnknownType verifies client.Mutate returns an error for
// an unrecognized mutation type.
func TestClientMutate_UnknownType(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.Mutate(ctx, unknownMutation{})
	assert.Error(t, err, "client.Mutate must reject unregistered mutation types")
}

// unknownMutation is a stub velox.Mutation whose Type() doesn't match any
// registered entity. It returns zero values for every interface method so
// the only thing client.Mutate can do is fail at the registry lookup.
type unknownMutation struct{}

func (unknownMutation) Op() integration.Op                          { return integration.OpCreate }
func (unknownMutation) Type() string                                { return "NonExistentEntity" }
func (unknownMutation) Fields() []string                            { return nil }
func (unknownMutation) Field(string) (integration.Value, bool)      { return nil, false }
func (unknownMutation) SetField(string, integration.Value) error    { return nil }
func (unknownMutation) AddedFields() []string                       { return nil }
func (unknownMutation) AddedField(string) (integration.Value, bool) { return nil, false }
func (unknownMutation) AddField(string, integration.Value) error    { return nil }
func (unknownMutation) ClearedFields() []string                     { return nil }
func (unknownMutation) FieldCleared(string) bool                    { return false }
func (unknownMutation) ClearField(string) error                     { return nil }
func (unknownMutation) ResetField(string) error                     { return nil }
func (unknownMutation) AddedEdges() []string                        { return nil }
func (unknownMutation) AddedIDs(string) []integration.Value         { return nil }
func (unknownMutation) RemovedEdges() []string                      { return nil }
func (unknownMutation) RemovedIDs(string) []integration.Value       { return nil }
func (unknownMutation) ClearedEdges() []string                      { return nil }
func (unknownMutation) EdgeCleared(string) bool                     { return false }
func (unknownMutation) ClearEdge(string) error                      { return nil }
func (unknownMutation) ResetEdge(string) error                      { return nil }
func (unknownMutation) OldField(context.Context, string) (integration.Value, error) {
	return nil, nil
}
