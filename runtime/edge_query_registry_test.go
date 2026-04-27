package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
)

// =============================================================================
// WithDriverContext / DriverFromContext
// =============================================================================

func TestWithDriverContext_and_DriverFromContext(t *testing.T) {
	drv := newTestDB(t)
	ctx := WithDriverContext(context.Background(), drv)

	got := DriverFromContext(ctx)
	assert.Equal(t, drv, got)
}

func TestDriverFromContext_NilWhenMissing(t *testing.T) {
	got := DriverFromContext(context.Background())
	assert.Nil(t, got)
}

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

func TestRegisteredTypeNames(t *testing.T) {
	defer cleanupRegistries(t, "TestTypeNamesEntity")

	RegisterMutator("TestTypeNamesEntity", func(_ context.Context, _ Config, _ any) (any, error) {
		return nil, nil
	})

	names := RegisteredTypeNames()
	assert.Contains(t, names, "TestTypeNamesEntity")
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
// Entity Client Registry
// =============================================================================

func TestRegisterEntityClient_and_NewEntityClient(t *testing.T) {
	defer cleanupRegistries(t, "TestClientEntity")

	RegisterEntityClient("TestClientEntity", func(_ Config) any {
		return "client_instance"
	})

	result := NewEntityClient("TestClientEntity", Config{})
	assert.Equal(t, "client_instance", result)
}

func TestNewEntityClient_Panics(t *testing.T) {
	assert.Panics(t, func() {
		NewEntityClient("NonExistentClientEntity", Config{})
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
	RegisterEntityClient(name, func(_ Config) any { return nil })

	err := ValidateRegistries()
	assert.NoError(t, err)
}

func TestValidateRegistries_MissingQueryFactory(t *testing.T) {
	name := "TestValMissQF"
	defer cleanupRegistries(t, name)

	RegisterMutator(name, func(_ context.Context, _ Config, _ any) (any, error) { return nil, nil })
	RegisterEntityClient(name, func(_ Config) any { return nil })

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

func TestValidateRegistries_MissingEntityClient(t *testing.T) {
	name := "TestValMissEC"
	defer cleanupRegistries(t, name)

	RegisterMutator(name, func(_ context.Context, _ Config, _ any) (any, error) { return nil, nil })
	RegisterQueryFactory(name, func(_ Config) any { return nil })

	err := ValidateRegistries()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "entity client missing")
}

// =============================================================================
// QueryBase.GetIDColumn / GetCtx / BuildSelector
// =============================================================================

func TestQueryBase_GetIDColumn_GetCtx(t *testing.T) {
	qb := NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")
	assert.Equal(t, "id", qb.GetIDColumn())
	assert.NotNil(t, qb.GetCtx())
	assert.Equal(t, "User", qb.GetCtx().Type)
}

func TestQueryBase_BuildSelector(t *testing.T) {
	drv := &mockDriver{dialectName: "sqlite"}
	qb := NewQueryBase(drv, "users", []string{"id", "name", "age"}, "id", []string{"team_id"}, "User")

	t.Run("all_columns", func(t *testing.T) {
		sel, err := qb.BuildSelector(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, sel)
	})

	t.Run("with_field_projection", func(t *testing.T) {
		clone := qb.Clone()
		clone.Ctx.Fields = []string{"name"}
		sel, err := clone.BuildSelector(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, sel)
	})

	t.Run("with_fk_columns", func(t *testing.T) {
		clone := qb.Clone()
		clone.WithFKs = true
		sel, err := clone.BuildSelector(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, sel)
	})

	t.Run("with_distinct", func(t *testing.T) {
		clone := qb.Clone()
		v := true
		clone.Ctx.Unique = &v
		sel, err := clone.BuildSelector(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, sel)
	})

	t.Run("path_error", func(t *testing.T) {
		clone := qb.Clone()
		clone.Path = func(_ context.Context) (*sql.Selector, error) {
			return nil, errors.New("path error")
		}
		_, err := clone.BuildSelector(context.Background())
		assert.Error(t, err)
	})
}

// =============================================================================
// ScanMapRows (scan.go)
// =============================================================================

func TestScanMapRows_BuildError(t *testing.T) {
	drv := newTestDB(t)

	_, err := ScanMapRows(context.Background(), drv, func(_ context.Context) (*sql.Selector, error) {
		return nil, errors.New("build error")
	})
	assert.Error(t, err)
}

func TestScanMapRows_QueryError(t *testing.T) {
	drv := &failDriver{
		Driver:    newTestDB(t),
		failAfter: 0,
		err:       errors.New("query failed"),
	}

	_, err := ScanMapRows(context.Background(), drv, func(ctx context.Context) (*sql.Selector, error) {
		qb := NewQueryBase(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
		return qb.BuildSelector(ctx)
	})
	assert.Error(t, err)
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

	clientMu.Lock()
	delete(entityClients, name)
	clientMu.Unlock()
}
