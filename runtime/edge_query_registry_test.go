package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// WithDriverContext / DriverFromContext
// =============================================================================

func TestWithConfigContext_and_ConfigFromContext(t *testing.T) {
	drv := newTestDB(t)
	cfg := Config{Driver: drv}
	ctx := WithConfigContext(context.Background(), cfg)

	got := ConfigFromContext(ctx)
	assert.Equal(t, cfg.Driver, got.Driver)
}

func TestConfigFromContext_ZeroWhenMissing(t *testing.T) {
	got := ConfigFromContext(context.Background())
	assert.Nil(t, got.Driver)
}

// =============================================================================
// MaskNotFound
// =============================================================================

func TestMaskNotFound(t *testing.T) {
	t.Run("masks_not_found", func(t *testing.T) {
		err := NewNotFoundError("User")
		assert.Nil(t, MaskNotFound(err))
	})

	t.Run("passes_other_errors", func(t *testing.T) {
		err := errors.New("something else")
		assert.Equal(t, err, MaskNotFound(err))
	})

	t.Run("passes_nil", func(t *testing.T) {
		assert.Nil(t, MaskNotFound(nil))
	})
}

// =============================================================================
// Mutator Registry (mutator.go)
// =============================================================================

func TestRegisterMutator_and_FindMutator(t *testing.T) {
	defer cleanupRegistries(t, "TestMutatorEntity")

	called := false
	RegisterMutator("TestMutatorEntity", func(_ context.Context, _ Config, _ any) (any, error) {
		called = true
		return nil, nil
	})

	fn := FindMutator("TestMutatorEntity")
	require.NotNil(t, fn)

	_, _ = fn(context.Background(), Config{}, nil)
	assert.True(t, called)

	assert.Nil(t, FindMutator("NonExistentEntity"))
}

// =============================================================================
// Query Factory Registry
// =============================================================================

func TestRegisterQueryFactory_and_NewEntityQuery(t *testing.T) {
	defer cleanupRegistries(t, "TestQueryEntity")

	RegisterQueryFactory("TestQueryEntity", func(_ Config) any {
		return "query_instance"
	})

	result := NewEntityQuery("TestQueryEntity", Config{})
	assert.Equal(t, "query_instance", result)
}

func TestNewEntityQuery_Panics(t *testing.T) {
	assert.Panics(t, func() {
		NewEntityQuery("NonExistentQueryEntity", Config{})
	})
}

// =============================================================================
// ValidateRegistries
// =============================================================================

func TestValidateRegistries_Consistent(t *testing.T) {
	name := "TestValConsistent"
	defer cleanupRegistries(t, name)

	RegisterMutator(name, func(_ context.Context, _ Config, _ any) (any, error) { return nil, nil })
	RegisterQueryFactory(name, func(_ Config) any { return nil })

	err := ValidateRegistries()
	assert.NoError(t, err)
}

func TestValidateRegistries_MissingQueryFactory(t *testing.T) {
	name := "TestValMissQF"
	defer cleanupRegistries(t, name)

	RegisterMutator(name, func(_ context.Context, _ Config, _ any) (any, error) { return nil, nil })

	err := ValidateRegistries()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query factory missing")
}

func TestValidateRegistries_MissingMutator(t *testing.T) {
	name := "TestValMissMut"
	defer cleanupRegistries(t, name)

	RegisterQueryFactory(name, func(_ Config) any { return nil })

	err := ValidateRegistries()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mutator missing")
}

// =============================================================================
// Helpers
// =============================================================================

func cleanupRegistries(t *testing.T, name string) {
	t.Helper()
	mutatorMu.Lock()
	delete(mutators, name)
	for i, n := range registeredNames {
		if n == name {
			registeredNames = append(registeredNames[:i], registeredNames[i+1:]...)
			break
		}
	}
	mutatorMu.Unlock()

	queryMu.Lock()
	delete(queryFactories, name)
	queryMu.Unlock()
}
