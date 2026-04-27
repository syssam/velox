package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
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
// Has-prefixed accessors (non-panicking companions)
// =============================================================================

// TestHasAccessors_NonPanicking pins the contract that the Has* accessors
// added for Client.ValidateRegistries are read-only and never panic. Their
// reason for existing is to let the generated startup-time validator probe
// the registries without inheriting NewEntityQuery / NewEntityClient's panic
// behavior, which is load-bearing on the hot path but unsuitable for a
// fail-fast diagnostic that needs to *report* missing entries, not crash on
// them.
func TestHasAccessors_NonPanicking(t *testing.T) {
	t.Run("missing_entries_return_false", func(t *testing.T) {
		assert.False(t, HasMutator("NoSuchEntity_For_Has_Test"))
		assert.False(t, HasQueryFactory("NoSuchEntity_For_Has_Test"))
		assert.False(t, HasEntityClient("NoSuchEntity_For_Has_Test"))
		assert.False(t, HasEntityRegistration("NoSuchEntity_For_Has_Test"))
		assert.False(t, HasEntityPolicy("NoSuchEntity_For_Has_Test"))
		assert.False(t, HasNodeResolver("no_such_table_for_has_test"))
		assert.False(t, HasColumns("no_such_table_for_has_test"))
	})

	t.Run("populated_entries_return_true", func(t *testing.T) {
		const name = "TestHasAccessor"
		const table = "test_has_accessor_tbl"
		defer cleanupRegistries(t, name)
		defer cleanupColumns(t, table)
		defer cleanupNodeResolver(t, table)
		defer cleanupPolicy(t, name)

		RegisterMutator(name, func(_ context.Context, _ Config, _ any) (any, error) { return nil, nil })
		RegisterQueryFactory(name, func(_ Config) any { return nil })
		RegisterEntityClient(name, func(_ Config) any { return nil })
		RegisterColumns(table, func(string) bool { return true })
		RegisterNodeResolver(table, NodeResolver{Type: name, Resolve: func(_ context.Context, _ any) (any, error) { return nil, nil }})

		assert.True(t, HasMutator(name))
		assert.True(t, HasQueryFactory(name))
		assert.True(t, HasEntityClient(name))
		assert.True(t, HasEntityRegistration(name))
		assert.True(t, HasNodeResolver(table))
		assert.True(t, HasColumns(table))
		// Policy registry has its own RegisterEntityPolicy entry point;
		// nil policy is a documented no-op so we can't reuse one of the
		// pieces above. Use a sentinel policy instead.
		RegisterEntityPolicy(name, sentinelPolicy{})
		assert.True(t, HasEntityPolicy(name))
	})
}

// TestHasEntityRegistration_RequiresAllThree pins the "any one of the three
// pieces missing" semantic of HasEntityRegistration. The generated
// ValidateRegistries collapses three checks into this one call as a
// readability aid; if a future refactor weakens the conjunction (say, to
// "at least one populated") the validator stops detecting partial imports.
func TestHasEntityRegistration_RequiresAllThree(t *testing.T) {
	cases := []struct {
		name   string
		mut    bool
		query  bool
		client bool
		want   bool
	}{
		{"none", false, false, false, false},
		{"mut_only", true, false, false, false},
		{"query_only", false, true, false, false},
		{"client_only", false, false, true, false},
		{"missing_query", true, false, true, false},
		{"missing_client", true, true, false, false},
		{"missing_mut", false, true, true, false},
		{"all_three", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := "TestHasER_" + tc.name
			defer cleanupRegistries(t, n)

			if tc.mut {
				RegisterMutator(n, func(_ context.Context, _ Config, _ any) (any, error) { return nil, nil })
			}
			if tc.query {
				RegisterQueryFactory(n, func(_ Config) any { return nil })
			}
			if tc.client {
				RegisterEntityClient(n, func(_ Config) any { return nil })
			}
			assert.Equal(t, tc.want, HasEntityRegistration(n))
		})
	}
}

// sentinelPolicy is a stand-in policy used only to distinguish "registered"
// from "unregistered" in HasEntityPolicy assertions. It is NOT exercised
// against any actual query/mutation in this test — the Policy interface
// requires velox.Query / velox.Mutation, but the methods here always return
// nil so the parameter shapes do not matter at the call sites we use.
type sentinelPolicy struct{}

func (sentinelPolicy) EvalQuery(context.Context, velox.Query) error       { return nil }
func (sentinelPolicy) EvalMutation(context.Context, velox.Mutation) error { return nil }

func cleanupColumns(t *testing.T, table string) {
	t.Helper()
	columnMu.Lock()
	delete(columnRegistry, table)
	columnMu.Unlock()
}

func cleanupNodeResolver(t *testing.T, table string) {
	t.Helper()
	nodeMu.Lock()
	delete(nodeRegistry, table)
	nodeMu.Unlock()
}

func cleanupPolicy(t *testing.T, name string) {
	t.Helper()
	policyMu.Lock()
	delete(policyRegistry, name)
	policyMu.Unlock()
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
