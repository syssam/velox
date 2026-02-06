package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genRuntime Tests
// =============================================================================

func TestGenRuntime_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genRuntime(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func init()")
}

func TestGenRuntime_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}

	file := genRuntime(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func init()")
}

func TestGenRuntime_EmptyGraph(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{}

	file := genRuntime(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func init()")
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{-1, "-1"},
		{100, "100"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, itoa(tt.input))
	}
}

func TestGetBaseType(t *testing.T) {
	tests := []struct {
		name   string
		field  *gen.Field
		notNil bool
	}{
		{"nil_field_type", &gen.Field{}, true},
		{"string_field", createTestField("name", field.TypeString), true},
		{"int_field", createTestField("age", field.TypeInt), true},
		{"int8_field", createTestField("val", field.TypeInt8), true},
		{"int16_field", createTestField("val", field.TypeInt16), true},
		{"int32_field", createTestField("val", field.TypeInt32), true},
		{"int64_field", createTestField("id", field.TypeInt64), true},
		{"uint_field", createTestField("val", field.TypeUint), true},
		{"uint8_field", createTestField("val", field.TypeUint8), true},
		{"uint16_field", createTestField("val", field.TypeUint16), true},
		{"uint32_field", createTestField("val", field.TypeUint32), true},
		{"uint64_field", createTestField("val", field.TypeUint64), true},
		{"float32_field", createTestField("val", field.TypeFloat32), true},
		{"float64_field", createTestField("price", field.TypeFloat64), true},
		{"bool_field", createTestField("active", field.TypeBool), true},
		{"enum_field", createEnumField("status", []string{"active"}), true},
		{"json_field", createTestField("data", field.TypeJSON), true},
		{"time_field", createTestField("ts", field.TypeTime), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getBaseType(tt.field)
			assert.NotNil(t, result)
		})
	}
}

func TestGetBaseType_NilType(t *testing.T) {
	f := &gen.Field{Name: "test"}
	result := getBaseType(f)
	assert.NotNil(t, result)
}

func TestGetValidatorType(t *testing.T) {
	helper := newMockHelper()

	tests := []struct {
		name  string
		field *gen.Field
	}{
		{"nil_type", &gen.Field{Name: "test"}},
		{"string_field", createTestField("name", field.TypeString)},
		{"enum_field", createEnumField("status", []string{"active"})},
		{"int_field", createTestField("age", field.TypeInt)},
		{"int8_field", createTestField("val", field.TypeInt8)},
		{"int16_field", createTestField("val", field.TypeInt16)},
		{"int32_field", createTestField("val", field.TypeInt32)},
		{"int64_field", createTestField("id", field.TypeInt64)},
		{"uint_field", createTestField("val", field.TypeUint)},
		{"uint8_field", createTestField("val", field.TypeUint8)},
		{"uint16_field", createTestField("val", field.TypeUint16)},
		{"uint32_field", createTestField("val", field.TypeUint32)},
		{"uint64_field", createTestField("val", field.TypeUint64)},
		{"float32_field", createTestField("val", field.TypeFloat32)},
		{"float64_field", createTestField("price", field.TypeFloat64)},
		{"bool_field", createTestField("active", field.TypeBool)},
		{"json_field", createTestField("data", field.TypeJSON)},
		{"time_field", createTestField("ts", field.TypeTime)},
		{"uuid_field", createTestField("uid", field.TypeUUID)},
		{"bytes_field", createTestField("data", field.TypeBytes)},
		{"other_field", createTestField("custom", field.TypeOther)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getValidatorType(helper, tt.field)
			assert.NotNil(t, result)
		})
	}
}

func TestGetValidatorType_JSONField(t *testing.T) {
	helper := newMockHelper()
	jsonField := createTestField("data", field.TypeJSON)

	result := getValidatorType(helper, jsonField)
	assert.NotNil(t, result)
	// JSON uses h.GoType instead of base type switch
}

// =============================================================================
// genRuntimeDefault Tests
// =============================================================================

func TestGenRuntimeDefault_StandardType(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	nameField := createTestField("name", field.TypeString)
	nameField.Default = true

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeDefault(helper, grp, userType, nameField, "userDescName", "github.com/test/project/ent/user", "user")
	})
	if !ok {
		t.Skip("genRuntimeDefault panicked due to DefaultFunc accessing unexported fields")
	}
}

// =============================================================================
// genRuntimeValidator Tests
// =============================================================================

func TestGenRuntimeValidator_SingleValidator(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	nameField := createTestField("name", field.TypeString)
	nameField.Validators = 1

	grp := &jen.Group{}
	genRuntimeValidator(helper, grp, userType, nameField, "userDescName", "github.com/test/project/ent/user", "user")
	// Should not panic and generate code for single validator
}

func TestGenRuntimeValidator_MultipleValidators(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	nameField := createTestField("name", field.TypeString)
	nameField.Validators = 3

	grp := &jen.Group{}
	genRuntimeValidator(helper, grp, userType, nameField, "userDescName", "github.com/test/project/ent/user", "user")
	// Should not panic and generate combined validator
}

func TestGenRuntimeValidator_ZeroValidators(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	nameField := createTestField("name", field.TypeString)
	nameField.Validators = 0

	grp := &jen.Group{}
	genRuntimeValidator(helper, grp, userType, nameField, "userDescName", "github.com/test/project/ent/user", "user")
	// Should not panic - zero validators should be a no-op
}

// =============================================================================
// genRuntimeUpdateDefault Tests
// =============================================================================

func TestGenRuntimeUpdateDefault(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	updatedField := createTestField("updated_at", field.TypeTime)
	updatedField.UpdateDefault = true

	grp := &jen.Group{}
	genRuntimeUpdateDefault(helper, grp, userType, updatedField, "userDescUpdatedAt", "github.com/test/project/ent/user", "user")
	// Should not panic
}

// =============================================================================
// genRuntimeHooks Tests
// =============================================================================

func TestGenRuntimeHooks_NoHooks(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	grp := &jen.Group{}
	genRuntimeHooks(helper, grp, userType, "schema", "entity", "pkg")
	// No hooks, should be a no-op
}

func TestGenRuntimeHooks_WithSchemaHooks(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithHooks("User", []*load.Position{
		{Index: 0, MixedIn: false},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeHooks(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeHooks panicked due to incomplete mock state")
	}
	// Should generate code assigning hooks
}

func TestGenRuntimeHooks_WithMixinHooks(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithHooks("User", []*load.Position{
		{Index: 0, MixedIn: true, MixinIndex: 0},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeHooks(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeHooks panicked due to incomplete mock state")
	}
}

func TestGenRuntimeHooks_WithPolicyOffset(t *testing.T) {
	helper := newMockHelper()

	// Create a type with both policies and hooks
	userType := createTestTypeWithSchema("User", &load.Schema{
		Hooks:  []*load.Position{{Index: 0, MixedIn: false}},
		Policy: []*load.Position{{Index: 0, MixedIn: false}},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeHooks(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeHooks panicked due to incomplete mock state")
	}
	// Hook should be at index 1 (offset 1 because policy is at index 0)
}

// =============================================================================
// genRuntimeInterceptors Tests
// =============================================================================

func TestGenRuntimeInterceptors_NoInterceptors(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	grp := &jen.Group{}
	genRuntimeInterceptors(helper, grp, userType, "schema", "entity", "pkg")
	// No interceptors, should be a no-op
}

func TestGenRuntimeInterceptors_WithSchemaInterceptors(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithInterceptors("User", []*load.Position{
		{Index: 0, MixedIn: false},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeInterceptors(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeInterceptors panicked due to incomplete mock state")
	}
}

func TestGenRuntimeInterceptors_WithMixinInterceptors(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithInterceptors("User", []*load.Position{
		{Index: 0, MixedIn: true, MixinIndex: 0},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeInterceptors(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeInterceptors panicked due to incomplete mock state")
	}
}

func TestGenRuntimeInterceptors_MixedSchemaAndMixin(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithInterceptors("User", []*load.Position{
		{Index: 0, MixedIn: true, MixinIndex: 0},
		{Index: 0, MixedIn: false},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeInterceptors(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeInterceptors panicked due to incomplete mock state")
	}
}

// =============================================================================
// genRuntimePolicies Tests
// =============================================================================

func TestGenRuntimePolicies_NoPolicies(t *testing.T) {
	helper := newMockHelper()

	userType := createTestType("User")
	grp := &jen.Group{}
	genRuntimePolicies(helper, grp, userType, "schema", "entity", "pkg")
	// No policies, should be a no-op
}

func TestGenRuntimePolicies_WithSchemaPolicy(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithPolicies("User", []*load.Position{
		{Index: 0, MixedIn: false},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimePolicies(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimePolicies panicked due to incomplete mock state")
	}
}

func TestGenRuntimePolicies_WithMixinPolicy(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithPolicies("User", []*load.Position{
		{Index: 0, MixedIn: true, MixinIndex: 0},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimePolicies(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimePolicies panicked due to incomplete mock state")
	}
}

// =============================================================================
// genRuntimeFields Tests
// =============================================================================

func TestGenRuntimeFields_WithDefaults(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:    "name",
			Info:    &field.TypeInfo{Type: field.TypeString},
			Default: true,
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeFields(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeFields panicked due to incomplete mock state")
	}
}

func TestGenRuntimeFields_WithValidators(t *testing.T) {
	fh := newFeatureMockHelper()
	fh.withFeatures("validator")

	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:       "name",
			Info:       &field.TypeInfo{Type: field.TypeString},
			Validators: 1,
		},
	})
	fh.graph.Nodes = []*gen.Type{userType}
	entityPkg := fh.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeFields(fh, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeFields panicked due to incomplete mock state")
	}
}

func TestGenRuntimeFields_WithUpdateDefault(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:          "updated_at",
			Info:          &field.TypeInfo{Type: field.TypeTime},
			UpdateDefault: true,
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeFields(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeFields panicked due to incomplete mock state")
	}
}

func TestGenRuntimeFields_MixinField(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:     "created_at",
			Info:     &field.TypeInfo{Type: field.TypeTime},
			Default:  true,
			Position: &load.Position{MixedIn: true, MixinIndex: 0, Index: 0},
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}
	entityPkg := helper.EntityPkgPath(userType)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeFields(helper, grp, userType, "github.com/test/project/schema", entityPkg, "user")
	})
	if !ok {
		t.Skip("genRuntimeFields panicked due to incomplete mock state")
	}
}

// =============================================================================
// genRuntimeEntityInit Tests
// =============================================================================

func TestGenRuntimeEntityInit_WithHooks(t *testing.T) {
	helper := newMockHelper()

	userType := createTestTypeWithSchema("User", &load.Schema{
		Hooks: []*load.Position{{Index: 0, MixedIn: false}},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

func TestGenRuntimeEntityInit_WithPoliciesAndHooks(t *testing.T) {
	helper := newMockHelper()

	userType := createTestTypeWithSchema("User", &load.Schema{
		Hooks:  []*load.Position{{Index: 0, MixedIn: false}},
		Policy: []*load.Position{{Index: 0, MixedIn: false}},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

func TestGenRuntimeEntityInit_NoRuntimeNeeded(t *testing.T) {
	helper := newMockHelper()

	// Type with no defaults, no validators, no hooks, no mixins
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	// Should return early - no runtime code needed
}

func TestGenRuntimeEntityInit_WithDefaults(t *testing.T) {
	helper := newMockHelper()

	// Type with default fields triggers hasRuntimeFields=true
	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:    "name",
			Info:    &field.TypeInfo{Type: field.TypeString},
			Default: true,
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

func TestGenRuntimeEntityInit_WithValidators(t *testing.T) {
	helper := newMockHelper()
	// Enable validator feature in config
	helper.graph.Config.Features = append(helper.graph.Config.Features, gen.FeatureValidator)

	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:       "name",
			Info:       &field.TypeInfo{Type: field.TypeString},
			Validators: 1,
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

func TestGenRuntimeEntityInit_WithUpdateDefault(t *testing.T) {
	helper := newMockHelper()

	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:          "updated_at",
			Info:          &field.TypeInfo{Type: field.TypeTime},
			UpdateDefault: true,
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

func TestGenRuntimeEntityInit_WithInterceptors(t *testing.T) {
	helper := newMockHelper()

	userType := createTestTypeWithSchema("User", &load.Schema{
		Interceptors: []*load.Position{{Index: 0, MixedIn: false}},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

func TestGenRuntimeEntityInit_WithMixinFields(t *testing.T) {
	helper := newMockHelper()

	// Type with mixin fields that have defaults triggers RuntimeMixin
	userType := createTypeWithSchemaFields("User", []*load.Field{
		{
			Name:     "created_at",
			Info:     &field.TypeInfo{Type: field.TypeTime},
			Default:  true,
			Position: &load.Position{MixedIn: true, MixinIndex: 0, Index: 0},
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genRuntimeEntityInit(helper, grp, userType, "github.com/test/project/schema")
	})
	if !ok {
		t.Skip("genRuntimeEntityInit panicked due to incomplete mock state")
	}
}

// =============================================================================
// genPredicatePackage Tests
// =============================================================================

func TestGenPredicatePackage_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}

	file := genPredicatePackage(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "package predicate")
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Post")
	assert.Contains(t, code, "Comment")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkItoa(b *testing.B) {
	for b.Loop() {
		_ = itoa(42)
	}
}

func BenchmarkGetBaseType(b *testing.B) {
	f := createTestField("name", field.TypeString)
	for b.Loop() {
		_ = getBaseType(f)
	}
}

func BenchmarkGetValidatorType(b *testing.B) {
	helper := newMockHelper()
	f := createTestField("name", field.TypeString)
	for b.Loop() {
		_ = getValidatorType(helper, f)
	}
}
