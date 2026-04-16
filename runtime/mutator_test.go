package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterEntity(t *testing.T) {
	// Save global state for cleanup.
	savedMutators := mutators
	savedEntityClients := entityClients
	savedRegisteredTypes := registeredTypes
	savedColumnRegistry := columnRegistry
	savedRegisteredNames := registeredNames

	// Reset globals for isolated test.
	mutators = map[string]MutatorFunc{}
	entityClients = map[string]EntityClientFunc{}
	registeredTypes = map[string]*RegisteredTypeInfo{}
	columnRegistry = map[string]func(string) bool{}
	registeredNames = nil

	defer func() {
		mutators = savedMutators
		entityClients = savedEntityClients
		registeredTypes = savedRegisteredTypes
		columnRegistry = savedColumnRegistry
		registeredNames = savedRegisteredNames
	}()

	// Build test registration.
	validCols := map[string]bool{"id": true, "name": true, "email": true}
	typeInfo := &RegisteredTypeInfo{
		Table:    "users",
		Columns:  []string{"id", "name", "email"},
		IDColumn: "id",
		ScanValues: func(columns []string) ([]any, error) {
			return make([]any, len(columns)), nil
		},
		New: func() any { return map[string]any{} },
		Assign: func(_ any, _ []string, _ []any) error {
			return nil
		},
		GetID: func(_ any) any { return 1 },
	}

	mutatorCalled := false
	clientCalled := false

	RegisterEntity(EntityRegistration{
		Name:     "User",
		Table:    "users",
		TypeInfo: typeInfo,
		ValidColumn: func(col string) bool {
			return validCols[col]
		},
		Mutator: func(_ context.Context, _ Config, _ any) (any, error) {
			mutatorCalled = true
			return nil, nil
		},
		Client: func(_ Config) any {
			clientCalled = true
			return nil
		},
	})

	// Verify mutator is registered and callable.
	fn := FindMutator("User")
	require.NotNil(t, fn, "mutator should be registered")
	_, _ = fn(context.Background(), Config{}, nil)
	assert.True(t, mutatorCalled, "mutator func should have been called")

	// Verify type info is registered.
	info := FindRegisteredType("users")
	require.NotNil(t, info, "type info should be registered")
	assert.Equal(t, "users", info.Table)
	assert.Equal(t, []string{"id", "name", "email"}, info.Columns)

	// Verify column validation is registered.
	err := ValidColumn("users", "name")
	assert.NoError(t, err, "valid column should pass")
	err = ValidColumn("users", "nonexistent")
	assert.Error(t, err, "invalid column should fail")

	// Verify entity client is registered.
	clientFn := entityClients["User"]
	require.NotNil(t, clientFn, "entity client should be registered")
	clientFn(Config{})
	assert.True(t, clientCalled, "client func should have been called")

	// Verify registered names.
	names := RegisteredTypeNames()
	assert.Contains(t, names, "User")
}
