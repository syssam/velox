package gen

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Interface Compliance Tests
// =============================================================================

// mockEntityGenerator implements EntityGenerator for testing.
type mockEntityGenerator struct{}

func (m *mockEntityGenerator) GenMutation(_ *Type) (*jen.File, error) {
	return jen.NewFile("mock"), nil
}
func (m *mockEntityGenerator) GenPredicate(_ *Type) (*jen.File, error) {
	return jen.NewFile("mock"), nil
}
func (m *mockEntityGenerator) GenPackage(_ *Type) (*jen.File, error) { return jen.NewFile("mock"), nil }

// mockGraphGenerator implements GraphGenerator for testing.
type mockGraphGenerator struct{}

func (m *mockGraphGenerator) GenClient() (*jen.File, error)  { return jen.NewFile("mock"), nil }
func (m *mockGraphGenerator) GenVelox() (*jen.File, error)   { return jen.NewFile("mock"), nil }
func (m *mockGraphGenerator) GenErrors() (*jen.File, error)  { return jen.NewFile("mock"), nil }
func (m *mockGraphGenerator) GenTx() (*jen.File, error)      { return jen.NewFile("mock"), nil }
func (m *mockGraphGenerator) GenRuntime() (*jen.File, error) { return jen.NewFile("mock"), nil }
func (m *mockGraphGenerator) GenPredicatePackage() (*jen.File, error) {
	return jen.NewFile("mock"), nil
}

// mockFeatureGenerator implements FeatureGenerator for testing.
type mockFeatureGenerator struct{}

func (m *mockFeatureGenerator) SupportsFeature(_ string) bool          { return false }
func (m *mockFeatureGenerator) GenFeature(_ string) (*jen.File, error) { return nil, nil }

// mockOptionalFeatureGenerator implements OptionalFeatureGenerator for testing.
type mockOptionalFeatureGenerator struct{}

func (m *mockOptionalFeatureGenerator) GenSchemaConfig() (*jen.File, error)       { return nil, nil }
func (m *mockOptionalFeatureGenerator) GenIntercept() (*jen.File, error)          { return nil, nil }
func (m *mockOptionalFeatureGenerator) GenPrivacy() (*jen.File, error)            { return nil, nil }
func (m *mockOptionalFeatureGenerator) GenSnapshot() (*jen.File, error)           { return nil, nil }
func (m *mockOptionalFeatureGenerator) GenVersionedMigration() (*jen.File, error) { return nil, nil }
func (m *mockOptionalFeatureGenerator) GenGlobalID() (*jen.File, error)           { return nil, nil }
func (m *mockOptionalFeatureGenerator) GenEntQL() (*jen.File, error)              { return nil, nil }

// mockMinimalDialect implements MinimalDialect for testing.
type mockMinimalDialect struct {
	mockEntityGenerator
	mockGraphGenerator
}

func (m *mockMinimalDialect) Name() string { return "mock" }

// mockDialectGenerator implements DialectGenerator for testing.
type mockDialectGenerator struct {
	mockMinimalDialect
	mockFeatureGenerator
	mockOptionalFeatureGenerator
}

// TestEntityGeneratorInterface verifies EntityGenerator interface compliance.
func TestEntityGeneratorInterface(t *testing.T) {
	var _ EntityGenerator = &mockEntityGenerator{}

	t.Run("interface has 3 methods", func(t *testing.T) {
		m := &mockEntityGenerator{}

		// Verify all methods exist and return (*jen.File, error)
		f, err := m.GenMutation(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPredicate(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPackage(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)
	})
}

// TestGraphGeneratorInterface verifies GraphGenerator interface compliance.
func TestGraphGeneratorInterface(t *testing.T) {
	var _ GraphGenerator = &mockGraphGenerator{}

	t.Run("interface has 6 methods", func(t *testing.T) {
		m := &mockGraphGenerator{}

		// Verify all methods exist and return (*jen.File, error)
		f, err := m.GenClient()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenVelox()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenErrors()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenTx()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenRuntime()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPredicatePackage()
		assert.NotNil(t, f)
		assert.NoError(t, err)
	})
}

// TestFeatureGeneratorInterface verifies FeatureGenerator interface compliance.
func TestFeatureGeneratorInterface(t *testing.T) {
	var _ FeatureGenerator = &mockFeatureGenerator{}

	t.Run("interface has 2 methods", func(t *testing.T) {
		m := &mockFeatureGenerator{}

		// Verify methods exist
		assert.False(t, m.SupportsFeature("test"))
		f, err := m.GenFeature("test")
		assert.Nil(t, f)
		assert.NoError(t, err)
	})
}

// TestOptionalFeatureGeneratorInterface verifies OptionalFeatureGenerator interface compliance.
func TestOptionalFeatureGeneratorInterface(t *testing.T) {
	var _ OptionalFeatureGenerator = &mockOptionalFeatureGenerator{}

	t.Run("interface has 7 methods", func(t *testing.T) {
		m := &mockOptionalFeatureGenerator{}

		// Verify all methods exist and return nil, nil
		f, err := m.GenSchemaConfig()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenIntercept()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPrivacy()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenSnapshot()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenVersionedMigration()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenGlobalID()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenEntQL()
		assert.Nil(t, f)
		assert.NoError(t, err)
	})
}

// TestMinimalDialectInterface verifies MinimalDialect interface compliance.
func TestMinimalDialectInterface(t *testing.T) {
	var _ MinimalDialect = &mockMinimalDialect{}

	t.Run("composes EntityGenerator and GraphGenerator", func(t *testing.T) {
		m := &mockMinimalDialect{}

		// From MinimalDialect
		assert.Equal(t, "mock", m.Name())

		// From EntityGenerator
		f, err := m.GenMutation(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)

		// From GraphGenerator
		f, err = m.GenClient()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenVelox()
		assert.NotNil(t, f)
		assert.NoError(t, err)
	})
}

// TestDialectGeneratorInterface verifies DialectGenerator interface compliance.
func TestDialectGeneratorInterface(t *testing.T) {
	var _ DialectGenerator = &mockDialectGenerator{}

	t.Run("composes all interfaces", func(t *testing.T) {
		m := &mockDialectGenerator{}

		// From MinimalDialect
		assert.Equal(t, "mock", m.Name())

		// From EntityGenerator
		f, err := m.GenMutation(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPredicate(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPackage(nil)
		assert.NotNil(t, f)
		assert.NoError(t, err)

		// From GraphGenerator
		f, err = m.GenClient()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenVelox()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenTx()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenRuntime()
		assert.NotNil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPredicatePackage()
		assert.NotNil(t, f)
		assert.NoError(t, err)

		// From FeatureGenerator
		assert.False(t, m.SupportsFeature("test"))
		f, err = m.GenFeature("test")
		assert.Nil(t, f)
		assert.NoError(t, err)

		// From OptionalFeatureGenerator
		f, err = m.GenSchemaConfig()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenIntercept()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenPrivacy()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenSnapshot()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenVersionedMigration()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenGlobalID()
		assert.Nil(t, f)
		assert.NoError(t, err)
		f, err = m.GenEntQL()
		assert.Nil(t, f)
		assert.NoError(t, err)
	})
}

// TestInterfaceHierarchy verifies the interface hierarchy is correct.
func TestInterfaceHierarchy(t *testing.T) {
	t.Run("MinimalDialect embeds EntityGenerator and GraphGenerator", func(t *testing.T) {
		var m MinimalDialect = &mockMinimalDialect{}

		// Can be assigned to both sub-interfaces
		var _ EntityGenerator = m
		var _ GraphGenerator = m
	})

	t.Run("DialectGenerator embeds MinimalDialect, FeatureGenerator, OptionalFeatureGenerator", func(t *testing.T) {
		var d DialectGenerator = &mockDialectGenerator{}

		// Can be assigned to all sub-interfaces
		var _ MinimalDialect = d
		var _ EntityGenerator = d
		var _ GraphGenerator = d
		var _ FeatureGenerator = d
		var _ OptionalFeatureGenerator = d
	})
}

// TestCapabilityDetection verifies type assertion for optional capabilities.
func TestCapabilityDetection(t *testing.T) {
	t.Run("MinimalDialect can be detected", func(t *testing.T) {
		var d any = &mockMinimalDialect{}

		_, ok := d.(MinimalDialect)
		assert.True(t, ok)

		_, ok = d.(FeatureGenerator)
		assert.False(t, ok)
	})

	t.Run("DialectGenerator supports all capabilities", func(t *testing.T) {
		var d any = &mockDialectGenerator{}

		_, ok := d.(MinimalDialect)
		assert.True(t, ok)

		_, ok = d.(FeatureGenerator)
		assert.True(t, ok)

		_, ok = d.(OptionalFeatureGenerator)
		assert.True(t, ok)

		_, ok = d.(DialectGenerator)
		assert.True(t, ok)
	})
}
