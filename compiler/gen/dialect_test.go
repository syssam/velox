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

func (m *mockEntityGenerator) GenEntity(_ *Type) *jen.File    { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenCreate(_ *Type) *jen.File    { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenUpdate(_ *Type) *jen.File    { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenDelete(_ *Type) *jen.File    { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenQuery(_ *Type) *jen.File     { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenMutation(_ *Type) *jen.File  { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenPredicate(_ *Type) *jen.File { return jen.NewFile("mock") }
func (m *mockEntityGenerator) GenPackage(_ *Type) *jen.File   { return jen.NewFile("mock") }

// mockGraphGenerator implements GraphGenerator for testing.
type mockGraphGenerator struct{}

func (m *mockGraphGenerator) GenClient() *jen.File           { return jen.NewFile("mock") }
func (m *mockGraphGenerator) GenVelox() *jen.File            { return jen.NewFile("mock") }
func (m *mockGraphGenerator) GenTx() *jen.File               { return jen.NewFile("mock") }
func (m *mockGraphGenerator) GenRuntime() *jen.File          { return jen.NewFile("mock") }
func (m *mockGraphGenerator) GenPredicatePackage() *jen.File { return jen.NewFile("mock") }

// mockFeatureGenerator implements FeatureGenerator for testing.
type mockFeatureGenerator struct{}

func (m *mockFeatureGenerator) SupportsFeature(_ string) bool { return false }
func (m *mockFeatureGenerator) GenFeature(_ string) *jen.File { return nil }

// mockOptionalFeatureGenerator implements OptionalFeatureGenerator for testing.
type mockOptionalFeatureGenerator struct{}

func (m *mockOptionalFeatureGenerator) GenSchemaConfig() *jen.File       { return nil }
func (m *mockOptionalFeatureGenerator) GenIntercept() *jen.File          { return nil }
func (m *mockOptionalFeatureGenerator) GenPrivacy() *jen.File            { return nil }
func (m *mockOptionalFeatureGenerator) GenSnapshot() *jen.File           { return nil }
func (m *mockOptionalFeatureGenerator) GenVersionedMigration() *jen.File { return nil }
func (m *mockOptionalFeatureGenerator) GenGlobalID() *jen.File           { return nil }
func (m *mockOptionalFeatureGenerator) GenEntQL() *jen.File              { return nil }

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

	t.Run("interface has 8 methods", func(t *testing.T) {
		m := &mockEntityGenerator{}

		// Verify all methods exist and return *jen.File
		assert.NotNil(t, m.GenEntity(nil))
		assert.NotNil(t, m.GenCreate(nil))
		assert.NotNil(t, m.GenUpdate(nil))
		assert.NotNil(t, m.GenDelete(nil))
		assert.NotNil(t, m.GenQuery(nil))
		assert.NotNil(t, m.GenMutation(nil))
		assert.NotNil(t, m.GenPredicate(nil))
		assert.NotNil(t, m.GenPackage(nil))
	})
}

// TestGraphGeneratorInterface verifies GraphGenerator interface compliance.
func TestGraphGeneratorInterface(t *testing.T) {
	var _ GraphGenerator = &mockGraphGenerator{}

	t.Run("interface has 5 methods", func(t *testing.T) {
		m := &mockGraphGenerator{}

		// Verify all methods exist and return *jen.File
		assert.NotNil(t, m.GenClient())
		assert.NotNil(t, m.GenVelox())
		assert.NotNil(t, m.GenTx())
		assert.NotNil(t, m.GenRuntime())
		assert.NotNil(t, m.GenPredicatePackage())
	})
}

// TestFeatureGeneratorInterface verifies FeatureGenerator interface compliance.
func TestFeatureGeneratorInterface(t *testing.T) {
	var _ FeatureGenerator = &mockFeatureGenerator{}

	t.Run("interface has 2 methods", func(t *testing.T) {
		m := &mockFeatureGenerator{}

		// Verify methods exist
		assert.False(t, m.SupportsFeature("test"))
		assert.Nil(t, m.GenFeature("test"))
	})
}

// TestOptionalFeatureGeneratorInterface verifies OptionalFeatureGenerator interface compliance.
func TestOptionalFeatureGeneratorInterface(t *testing.T) {
	var _ OptionalFeatureGenerator = &mockOptionalFeatureGenerator{}

	t.Run("interface has 7 methods", func(t *testing.T) {
		m := &mockOptionalFeatureGenerator{}

		// Verify all methods exist
		assert.Nil(t, m.GenSchemaConfig())
		assert.Nil(t, m.GenIntercept())
		assert.Nil(t, m.GenPrivacy())
		assert.Nil(t, m.GenSnapshot())
		assert.Nil(t, m.GenVersionedMigration())
		assert.Nil(t, m.GenGlobalID())
		assert.Nil(t, m.GenEntQL())
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
		assert.NotNil(t, m.GenEntity(nil))
		assert.NotNil(t, m.GenCreate(nil))

		// From GraphGenerator
		assert.NotNil(t, m.GenClient())
		assert.NotNil(t, m.GenVelox())
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
		assert.NotNil(t, m.GenEntity(nil))
		assert.NotNil(t, m.GenCreate(nil))
		assert.NotNil(t, m.GenUpdate(nil))
		assert.NotNil(t, m.GenDelete(nil))
		assert.NotNil(t, m.GenQuery(nil))
		assert.NotNil(t, m.GenMutation(nil))
		assert.NotNil(t, m.GenPredicate(nil))
		assert.NotNil(t, m.GenPackage(nil))

		// From GraphGenerator
		assert.NotNil(t, m.GenClient())
		assert.NotNil(t, m.GenVelox())
		assert.NotNil(t, m.GenTx())
		assert.NotNil(t, m.GenRuntime())
		assert.NotNil(t, m.GenPredicatePackage())

		// From FeatureGenerator
		assert.False(t, m.SupportsFeature("test"))
		assert.Nil(t, m.GenFeature("test"))

		// From OptionalFeatureGenerator
		assert.Nil(t, m.GenSchemaConfig())
		assert.Nil(t, m.GenIntercept())
		assert.Nil(t, m.GenPrivacy())
		assert.Nil(t, m.GenSnapshot())
		assert.Nil(t, m.GenVersionedMigration())
		assert.Nil(t, m.GenGlobalID())
		assert.Nil(t, m.GenEntQL())
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

// TestDialectOptionType verifies DialectOption type.
func TestDialectOptionType(t *testing.T) {
	t.Run("DialectOption is a function type", func(t *testing.T) {
		called := false
		opt := DialectOption(func(d DialectGenerator) {
			called = true
		})

		m := &mockDialectGenerator{}
		opt(m)

		assert.True(t, called)
	})
}

// TestCapabilityDetection verifies type assertion for optional capabilities.
func TestCapabilityDetection(t *testing.T) {
	t.Run("MinimalDialect can be detected", func(t *testing.T) {
		var d interface{} = &mockMinimalDialect{}

		_, ok := d.(MinimalDialect)
		assert.True(t, ok)

		_, ok = d.(FeatureGenerator)
		assert.False(t, ok)
	})

	t.Run("DialectGenerator supports all capabilities", func(t *testing.T) {
		var d interface{} = &mockDialectGenerator{}

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
