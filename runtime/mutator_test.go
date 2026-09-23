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
	savedColumnRegistry := columnRegistry
	savedRegisteredNames := registeredNames

	// Reset globals for isolated test.
	mutators = map[string]MutatorFunc{}
	columnRegistry = map[string]func(string) bool{}
	registeredNames = nil

	defer func() {
		mutators = savedMutators
		columnRegistry = savedColumnRegistry
		registeredNames = savedRegisteredNames
	}()

	validCols := map[string]bool{"id": true, "name": true, "email": true}
	mutatorCalled := false

	RegisterEntity(EntityRegistration{
		Name:  "User",
		Table: "users",
		ValidColumn: func(col string) bool {
			return validCols[col]
		},
		Mutator: func(_ context.Context, _ Config, _ any) (any, error) {
			mutatorCalled = true
			return nil, nil
		},
	})

	// Verify mutator is registered and callable.
	fn := FindMutator("User")
	require.NotNil(t, fn, "mutator should be registered")
	_, _ = fn(context.Background(), Config{}, nil)
	assert.True(t, mutatorCalled, "mutator func should have been called")

	// Verify column validation is registered.
	err := ValidColumn("users", "name")
	assert.NoError(t, err, "valid column should pass")
	err = ValidColumn("users", "nonexistent")
	assert.Error(t, err, "invalid column should fail")

	// Verify registered names.
	assert.Contains(t, registeredNames, "User")
}
