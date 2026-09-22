package sql

import (
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestItoa(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	f := &gen.Field{Name: "test"}
	result := getBaseType(f)
	assert.NotNil(t, result)
}

func TestGetValidatorType(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	jsonField := createTestField("data", field.TypeJSON)

	result := getValidatorType(helper, jsonField)
	assert.NotNil(t, result)
	// JSON uses h.GoType instead of base type switch
}

// =============================================================================
// genRuntimeDefault / genRuntimeUpdateDefault / genRuntimeValidator Tests
// =============================================================================

const (
	testSchemaPkg = "github.com/test/project/schema"
	testUserPkg   = "github.com/test/project/ent/user"
)

// emptyBlock is what renderGroup returns when a generator appends nothing.
const emptyBlock = "{\n}"

func TestGenRuntimeDefault_StandardType(t *testing.T) {
	t.Parallel()
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}, Default: true, DefaultValue: "unknown"},
	})
	require.NotEmpty(t, userType.Fields)

	code := renderGroup(func(g *jen.Group) {
		genRuntimeDefault(newMockHelper(), g, userType, userType.Fields[0], "userDescName", testUserPkg, "user")
	})
	assert.Contains(t, code, "// user.DefaultName holds the default value on creation for the name field.")
	assert.Contains(t, code, "user.DefaultName = userDescName.Default.(string)\n")
}

func TestGenRuntimeUpdateDefault(t *testing.T) {
	t.Parallel()
	updatedField := createTestField("updated_at", field.TypeTime)
	updatedField.UpdateDefault = true

	code := renderGroup(func(g *jen.Group) {
		genRuntimeUpdateDefault(newMockHelper(), g, createTestType("User"), updatedField, "userDescUpdatedAt", testUserPkg, "user")
	})
	// An update default is always a func, asserted to func() T.
	assert.Contains(t, code, "user.UpdateDefaultUpdatedAt = userDescUpdatedAt.UpdateDefault.(func() time.Time)\n")
}

func TestGenRuntimeValidator(t *testing.T) {
	t.Parallel()
	render := func(n int) string {
		nameField := createTestField("name", field.TypeString)
		nameField.Validators = n
		return renderGroup(func(g *jen.Group) {
			genRuntimeValidator(newMockHelper(), g, createTestType("User"), nameField, "userDescName", testUserPkg, "user")
		})
	}

	t.Run("zero validators emit nothing", func(t *testing.T) {
		assert.Equal(t, emptyBlock, render(0))
	})

	t.Run("one validator is assigned directly", func(t *testing.T) {
		code := render(1)
		assert.Contains(t, code, "user.NameValidator = userDescName.Validators[0].(func(string) error)\n")
		assert.NotContains(t, code, "fns :=")
	})

	t.Run("several validators are chained in order", func(t *testing.T) {
		code := render(3)
		assert.Contains(t, code, "user.NameValidator = func() func(string) error {")
		assert.Contains(t, code, "validators := userDescName.Validators")
		assert.Contains(t, code, "fns := [...]func(string) error{")
		for _, i := range []string{"0", "1", "2"} {
			assert.Contains(t, code, "validators["+i+"].(func(string) error),")
		}
		assert.NotContains(t, code, "validators[3]")
		// The combined validator stops at the first failing one.
		assert.Contains(t, code, "for _, fn := range fns {")
		assert.Contains(t, code, "if err := fn(name); err != nil {")
		assert.Contains(t, code, "}()")
	})
}

// =============================================================================
// genRuntimeHooks / genRuntimePolicies Tests
// =============================================================================

// renderRuntime renders gen for a User type built from s.
func renderRuntime(t *testing.T, s *load.Schema, fn func(h *mockHelper, g *jen.Group, typ *gen.Type)) string {
	t.Helper()
	h := newMockHelper()
	typ := createTestTypeWithSchema(t, "User", s)
	h.graph.Nodes = []*gen.Type{typ}
	return renderGroup(func(g *jen.Group) { fn(h, g, typ) })
}

func TestGenRuntimeHooks(t *testing.T) {
	t.Parallel()
	hooks := func(h *mockHelper, g *jen.Group, typ *gen.Type) {
		genRuntimeHooks(h, g, typ, testSchemaPkg, h.LeafPkgPath(typ), "user")
	}

	t.Run("no hooks emit nothing", func(t *testing.T) {
		assert.Equal(t, emptyBlock, renderRuntime(t, &load.Schema{}, hooks))
	})

	t.Run("schema hook", func(t *testing.T) {
		code := renderRuntime(t, &load.Schema{Hooks: []*load.Position{{Index: 0}}}, hooks)
		assert.Contains(t, code, "userHooks := schema.User{}.Hooks()\n")
		assert.Contains(t, code, "user.Hooks[0] = userHooks[0]\n")
		assert.NotContains(t, code, "Mixin")
	})

	t.Run("mixin hook", func(t *testing.T) {
		code := renderRuntime(t, &load.Schema{Hooks: []*load.Position{{Index: 0, MixedIn: true, MixinIndex: 0}}}, hooks)
		assert.Contains(t, code, "userMixinHooks0 := userMixin[0].Hooks()\n")
		assert.Contains(t, code, "user.Hooks[0] = userMixinHooks0[0]\n")
		assert.NotContains(t, code, "schema.User{}.Hooks()")
	})

	t.Run("mixin hooks come before schema hooks", func(t *testing.T) {
		code := renderRuntime(t, &load.Schema{Hooks: []*load.Position{
			{Index: 0, MixedIn: true, MixinIndex: 0},
			{Index: 0},
		}}, hooks)
		assert.Contains(t, code, "user.Hooks[0] = userMixinHooks0[0]\n")
		assert.Contains(t, code, "user.Hooks[1] = userHooks[0]\n")
	})

	// Privacy is an explicit policy field, not a Hooks[0] slot: a policy
	// must not shift the schema's hooks.
	t.Run("policy does not reserve a hook slot", func(t *testing.T) {
		code := renderRuntime(t, &load.Schema{
			Hooks:  []*load.Position{{Index: 0}},
			Policy: []*load.Position{{Index: 0}},
		}, hooks)
		assert.Contains(t, code, "user.Hooks[0] = userHooks[0]\n")
		assert.NotContains(t, code, "Hooks[1]")
		assert.NotContains(t, code, "Policy")
	})
}

func TestGenRuntimePolicies(t *testing.T) {
	t.Parallel()
	policies := func(h *mockHelper, g *jen.Group, typ *gen.Type) {
		genRuntimePolicies(h, g, typ, testSchemaPkg, h.LeafPkgPath(typ), "user")
	}

	t.Run("no policy emits nothing", func(t *testing.T) {
		assert.Equal(t, emptyBlock, renderRuntime(t, &load.Schema{}, policies))
	})

	t.Run("schema policy", func(t *testing.T) {
		code := renderRuntime(t, &load.Schema{Policy: []*load.Position{{Index: 0}}}, policies)
		assert.Contains(t, code, "user.Policy = privacy.NewPolicies(schema.User{})\n")
		// The client reads RuntimePolicy; edge queries look it up by name.
		assert.Contains(t, code, "user.RuntimePolicy = user.Policy\n")
		assert.Contains(t, code, `runtime.RegisterEntityPolicy("User", user.RuntimePolicy)`)
		assert.NotContains(t, code, "Hooks")
	})

	t.Run("mixin policies are composed before the schema policy", func(t *testing.T) {
		code := renderRuntime(t, &load.Schema{Policy: []*load.Position{{Index: 0, MixedIn: true, MixinIndex: 0}}}, policies)
		assert.Contains(t, code, "user.Policy = privacy.NewPolicies(userMixin[0], schema.User{})\n")
		assert.Contains(t, code, "user.RuntimePolicy = user.Policy\n")
	})
}

// =============================================================================
// genRuntimeFields / genRuntimeEntityInit Tests
// =============================================================================

func TestGenRuntimeEntityInit(t *testing.T) {
	t.Parallel()
	stringField := func(f load.Field) *load.Field {
		f.Info = &field.TypeInfo{Type: field.TypeString}
		return &f
	}
	timeField := func(f load.Field) *load.Field {
		f.Info = &field.TypeInfo{Type: field.TypeTime}
		return &f
	}

	tests := []struct {
		name    string
		schema  *load.Schema
		want    []string
		notWant []string
	}{
		{
			name:   "nothing to wire",
			schema: &load.Schema{Fields: []*load.Field{stringField(load.Field{Name: "name"})}},
		},
		{
			name:   "default",
			schema: &load.Schema{Fields: []*load.Field{stringField(load.Field{Name: "name", Default: true})}},
			want: []string{
				"userFields := schema.User{}.Fields()\n",
				"_ = userFields\n",
				"userDescName := userFields[0].Descriptor()\n",
				"user.DefaultName = userDescName.Default.(string)\n",
			},
			notWant: []string{"Mixin", "Validator"},
		},
		{
			// Validators are generated unconditionally; FeatureValidator is a no-op.
			name:   "validator",
			schema: &load.Schema{Fields: []*load.Field{stringField(load.Field{Name: "name", Validators: 1})}},
			want: []string{
				"userDescName := userFields[0].Descriptor()\n",
				"user.NameValidator = userDescName.Validators[0].(func(string) error)\n",
			},
			notWant: []string{"Default"},
		},
		{
			name:   "update default",
			schema: &load.Schema{Fields: []*load.Field{timeField(load.Field{Name: "updated_at", UpdateDefault: true})}},
			want: []string{
				"userDescUpdatedAt := userFields[0].Descriptor()\n",
				"user.UpdateDefaultUpdatedAt = userDescUpdatedAt.UpdateDefault.(func() time.Time)\n",
			},
		},
		{
			name: "descriptor index follows the schema position",
			schema: &load.Schema{Fields: []*load.Field{
				stringField(load.Field{Name: "a"}),
				stringField(load.Field{Name: "b", Default: true, Position: &load.Position{Index: 1}}),
			}},
			want:    []string{"userDescB := userFields[1].Descriptor()\n"},
			notWant: []string{"userDescA"},
		},
		{
			name: "mixin field reads the mixin's descriptor",
			schema: &load.Schema{Fields: []*load.Field{
				timeField(load.Field{Name: "created_at", Default: true, Position: &load.Position{MixedIn: true, MixinIndex: 0, Index: 0}}),
			}},
			want: []string{
				"userMixin := schema.User{}.Mixin()\n",
				"userMixinFields0 := userMixin[0].Fields()\n",
				"userDescCreatedAt := userMixinFields0[0].Descriptor()\n",
				"user.DefaultCreatedAt = userDescCreatedAt.Default.(time.Time)\n",
			},
			notWant: []string{"userFields[0].Descriptor()"},
		},
		{
			name: "policy, hooks and fields are all wired",
			schema: &load.Schema{
				Fields: []*load.Field{stringField(load.Field{Name: "name", Default: true})},
				Hooks:  []*load.Position{{Index: 0}},
				Policy: []*load.Position{{Index: 0}},
			},
			want: []string{
				"user.RuntimePolicy = user.Policy\n",
				"user.Hooks[0] = userHooks[0]\n",
				"user.DefaultName = userDescName.Default.(string)\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newMockHelper()
			typ := createTestTypeWithSchema(t, "User", tt.schema)
			h.graph.Nodes = []*gen.Type{typ}
			code := renderGroup(func(g *jen.Group) { genRuntimeEntityInit(h, g, typ, testSchemaPkg) })
			if len(tt.want) == 0 {
				assert.Equal(t, emptyBlock, code)
				return
			}
			for _, w := range tt.want {
				assert.Contains(t, code, w)
			}
			for _, nw := range tt.notWant {
				assert.NotContains(t, code, nw)
			}
		})
	}
}

// TestGenRuntimeEntityInit_Order pins Ent's init order: mixin, policies,
// hooks, then fields. The policy must be installed before anything that
// could run a mutation.
func TestGenRuntimeEntityInit_Order(t *testing.T) {
	t.Parallel()
	h := newMockHelper()
	typ := createTestTypeWithSchema(t, "User", &load.Schema{
		Fields: []*load.Field{{Name: "created_at", Info: &field.TypeInfo{Type: field.TypeTime}, Default: true, Position: &load.Position{MixedIn: true, MixinIndex: 0}}},
		Hooks:  []*load.Position{{Index: 0}},
		Policy: []*load.Position{{Index: 0}},
	})
	h.graph.Nodes = []*gen.Type{typ}
	code := renderGroup(func(g *jen.Group) { genRuntimeEntityInit(h, g, typ, testSchemaPkg) })

	order := []string{"userMixin := ", "user.Policy = ", "userHooks := ", "userMixinFields0 := ", "user.DefaultCreatedAt = "}
	last := -1
	for _, s := range order {
		i := strings.Index(code, s)
		require.GreaterOrEqual(t, i, 0, "missing %q in:\n%s", s, code)
		assert.Greater(t, i, last, "%q out of order in:\n%s", s, code)
		last = i
	}
}

// =============================================================================
// genPredicatePackage Tests
// =============================================================================

func TestGenPredicatePackage_MultipleEntities(t *testing.T) {
	t.Parallel()
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
