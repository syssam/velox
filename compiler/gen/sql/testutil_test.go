package sql

import (
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// featureMockHelper extends mockHelper with configurable feature flags and annotations.
type featureMockHelper struct {
	*mockHelper
	enabledFeatures map[string]bool
	annotations     map[string]bool
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

// createTestTypeWithID creates a Type with a custom ID type.
func createTestTypeWithID(name string, idType field.Type) *gen.Type {
	t := createTestType(name)
	t.ID = &gen.Field{
		Name: "id",
		Type: &field.TypeInfo{Type: idType},
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

// createFieldWithDefault creates a field with Default set to true.
func createFieldWithDefault(name string, typ field.Type) *gen.Field {
	return &gen.Field{
		Name:    name,
		Type:    &field.TypeInfo{Type: typ},
		Default: true,
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

// createInverseEdge creates an inverse (back-reference) edge.
func createInverseEdge(name string, target *gen.Type, rel gen.Rel, inverse string) *gen.Edge {
	return &gen.Edge{
		Name:    name,
		Type:    target,
		Unique:  rel == gen.O2O || rel == gen.M2O,
		Inverse: inverse,
		Rel:     gen.Relation{Type: rel},
	}
}

// createTestTypeWithSchema creates a Type using gen.NewType with a proper load.Schema.
// This enables testing of functions that depend on t.schema (hooks, interceptors, policies).
func createTestTypeWithSchema(name string, schema *load.Schema) *gen.Type {
	schema.Name = name
	cfg := &gen.Config{
		Package: "github.com/test/project/ent",
		Target:  "/tmp/ent",
	}
	t, err := gen.NewType(cfg, schema)
	if err != nil {
		panic("createTestTypeWithSchema: " + err.Error())
	}
	return t
}

// createTypeWithHooks creates a Type that has hook positions set via load.Schema.
func createTypeWithHooks(name string, hookPositions []*load.Position) *gen.Type {
	return createTestTypeWithSchema(name, &load.Schema{
		Hooks: hookPositions,
	})
}

// createTypeWithInterceptors creates a Type that has interceptor positions set via load.Schema.
func createTypeWithInterceptors(name string, interceptorPositions []*load.Position) *gen.Type {
	return createTestTypeWithSchema(name, &load.Schema{
		Interceptors: interceptorPositions,
	})
}

// createTypeWithPolicies creates a Type that has policy positions set via load.Schema.
func createTypeWithPolicies(name string, policyPositions []*load.Position) *gen.Type {
	return createTestTypeWithSchema(name, &load.Schema{
		Policy: policyPositions,
	})
}

// createTypeWithSchemaFields creates a Type using gen.NewType with proper load.Schema fields.
// This allows defaults, validators, and update-defaults to work properly.
func createTypeWithSchemaFields(name string, fields []*load.Field) *gen.Type {
	return createTestTypeWithSchema(name, &load.Schema{
		Fields: fields,
	})
}

// newMockHelperWithValidators creates a mockHelper with the FeatureValidator enabled in Config.
func newMockHelperWithValidators() *mockHelper {
	h := newMockHelper()
	h.graph.Config.Features = append(h.graph.Config.Features, gen.FeatureValidator)
	return h
}

// createFieldWithUpdateDefault creates a field with UpdateDefault set to true.
func createFieldWithUpdateDefault(name string, typ field.Type) *gen.Field {
	return &gen.Field{
		Name:          name,
		Type:          &field.TypeInfo{Type: typ},
		UpdateDefault: true,
	}
}

// createFieldWithValidators creates a field with validator count > 0.
func createFieldWithValidators(name string, typ field.Type, count int) *gen.Field {
	return &gen.Field{
		Name:       name,
		Type:       &field.TypeInfo{Type: typ},
		Validators: count,
	}
}
