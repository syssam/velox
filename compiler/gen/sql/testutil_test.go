package sql

import (
	"testing"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// featureMockHelper extends mockHelper with configurable feature flags and annotations.
type featureMockHelper struct {
	*mockHelper
	enabledFeatures map[string]bool
	annotations     map[string]bool
	rootPkg         string
}

func newFeatureMockHelper() *featureMockHelper {
	return &featureMockHelper{
		mockHelper:      newMockHelper(),
		enabledFeatures: make(map[string]bool),
		annotations:     make(map[string]bool),
	}
}

func (m *featureMockHelper) withFeatures(features ...string) *featureMockHelper {
	for _, f := range features {
		m.enabledFeatures[f] = true
	}
	return m
}

func (m *featureMockHelper) FeatureEnabled(name string) bool   { return m.enabledFeatures[name] }
func (m *featureMockHelper) AnnotationExists(name string) bool { return m.annotations[name] }
func (m *featureMockHelper) RootPkg() string {
	if m.rootPkg != "" {
		return m.rootPkg
	}
	return m.mockHelper.RootPkg()
}

// Ensure featureMockHelper implements gen.GeneratorHelper.
var _ gen.GeneratorHelper = (*featureMockHelper)(nil)

// createTestTypeWithFields creates a Type with custom fields.
func createTestTypeWithFields(name string, fields []*gen.Field) *gen.Type {
	t := &gen.Type{
		Name: name,
		Config: &gen.Config{
			Package: "github.com/test/project/ent",
			Target:  "/tmp/ent",
		},
		ID: &gen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: fields,
	}
	return t
}

// createTestField creates a simple field.
func createTestField(name string, typ field.Type) *gen.Field {
	return &gen.Field{
		Name: name,
		Type: &field.TypeInfo{Type: typ},
	}
}

// createNillableField creates a nillable field.
func createNillableField(name string, typ field.Type) *gen.Field {
	return &gen.Field{
		Name:     name,
		Type:     &field.TypeInfo{Type: typ},
		Nillable: true,
	}
}

// createOptionalField creates an optional field.
func createOptionalField(name string, typ field.Type) *gen.Field {
	return &gen.Field{
		Name:     name,
		Type:     &field.TypeInfo{Type: typ},
		Optional: true,
	}
}

// createEnumField creates an enum field with values.
func createEnumField(name string, values []string) *gen.Field {
	enums := make([]gen.Enum, len(values))
	for i, v := range values {
		enums[i] = gen.Enum{Name: v, Value: v}
	}
	return &gen.Field{
		Name:  name,
		Type:  &field.TypeInfo{Type: field.TypeEnum},
		Enums: enums,
	}
}

// createImmutableField creates an immutable field.
func createImmutableField(name string, typ field.Type) *gen.Field {
	return &gen.Field{
		Name:      name,
		Type:      &field.TypeInfo{Type: typ},
		Immutable: true,
	}
}

// createM2MEdge creates a many-to-many edge with proper relation columns.
func createM2MEdge(name string, target *gen.Type, table string, cols []string) *gen.Edge {
	return &gen.Edge{
		Name:   name,
		Type:   target,
		Unique: false,
		Rel: gen.Relation{
			Type:    gen.M2M,
			Table:   table,
			Columns: cols,
		},
	}
}

// createO2MEdge creates a one-to-many edge.
func createO2MEdge(name string, target *gen.Type, table, col string) *gen.Edge {
	return &gen.Edge{
		Name:   name,
		Type:   target,
		Unique: false,
		Rel: gen.Relation{
			Type:    gen.O2M,
			Table:   table,
			Columns: []string{col},
		},
	}
}

// createM2OEdge creates a many-to-one edge.
func createM2OEdge(name string, target *gen.Type, table, col string) *gen.Edge {
	return &gen.Edge{
		Name:   name,
		Type:   target,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   table,
			Columns: []string{col},
		},
	}
}

// createO2OEdge creates a one-to-one edge.
func createO2OEdge(name string, target *gen.Type, table, col string) *gen.Edge {
	return &gen.Edge{
		Name:   name,
		Type:   target,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.O2O,
			Table:   table,
			Columns: []string{col},
		},
	}
}

// createTestTypeWithSchema creates a Type using gen.NewType with a proper load.Schema.
// This enables testing of functions that depend on t.schema (hooks, interceptors, policies).
func createTestTypeWithSchema(t testing.TB, name string, schema *load.Schema) *gen.Type {
	t.Helper()
	schema.Name = name
	cfg := &gen.Config{
		Package: "github.com/test/project/ent",
		Target:  "/tmp/ent",
	}
	typ, err := gen.NewType(cfg, schema)
	if err != nil {
		t.Fatal("createTestTypeWithSchema: " + err.Error())
		return nil
	}
	return typ
}

// createTypeWithHooks creates a Type that has hook positions set via load.Schema.
func createTypeWithHooks(t testing.TB, name string, hookPositions []*load.Position) *gen.Type {
	t.Helper()
	return createTestTypeWithSchema(t, name, &load.Schema{
		Hooks: hookPositions,
	})
}

// createTypeWithInterceptors creates a Type that has interceptor positions set via load.Schema.
func createTypeWithInterceptors(t testing.TB, name string, interceptorPositions []*load.Position) *gen.Type {
	t.Helper()
	return createTestTypeWithSchema(t, name, &load.Schema{
		Interceptors: interceptorPositions,
	})
}

// createTypeWithPolicies creates a Type that has policy positions set via load.Schema.
func createTypeWithPolicies(t testing.TB, name string, policyPositions []*load.Position) *gen.Type {
	t.Helper()
	return createTestTypeWithSchema(t, name, &load.Schema{
		Policy: policyPositions,
	})
}

// createTypeWithSchemaFields creates a Type using gen.NewType with proper load.Schema fields.
// This allows defaults, validators, and update-defaults to work properly.
func createTypeWithSchemaFields(t testing.TB, name string, fields []*load.Field) *gen.Type {
	t.Helper()
	return createTestTypeWithSchema(t, name, &load.Schema{
		Fields: fields,
	})
}

// newMockHelperWithValidators creates a mockHelper with the FeatureValidator enabled in Config.
func newMockHelperWithValidators() *mockHelper {
	h := newMockHelper()
	h.graph.Features = append(h.graph.Features, gen.FeatureValidator)
	return h
}

// createFieldWithValidators creates a field with validator count > 0.
func createFieldWithValidators(name string, typ field.Type, count int) *gen.Field {
	return &gen.Field{
		Name:       name,
		Type:       &field.TypeInfo{Type: typ},
		Validators: count,
	}
}
