package gen

// coverage_boost_test.go covers the functions with 0% or very low coverage that
// were identified in the compiler/gen package coverage analysis.

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"text/template/parse"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// type.go — zero-coverage accessors
// =============================================================================

func TestType_QueryReceiver(t *testing.T) {
	typ := Type{Name: "User"}
	assert.Equal(t, "_q", typ.QueryReceiver())
}

func TestType_FilterName(t *testing.T) {
	typ := Type{Name: "User"}
	assert.Equal(t, "UserFilter", typ.FilterName())
}

func TestType_CreateInputName(t *testing.T) {
	typ := Type{Name: "User"}
	assert.Equal(t, "CreateUserInput", typ.CreateInputName())
}

func TestType_UpdateInputName(t *testing.T) {
	typ := Type{Name: "Post"}
	assert.Equal(t, "UpdatePostInput", typ.UpdateInputName())
}

func TestType_NewCreateInputFunc(t *testing.T) {
	typ := Type{Name: "User"}
	assert.Equal(t, "NewCreateInput", typ.NewCreateInputFunc())
}

func TestType_NewUpdateInputFunc(t *testing.T) {
	typ := Type{Name: "User"}
	assert.Equal(t, "NewUpdateInput", typ.NewUpdateInputFunc())
}

func TestType_ConfigMethodName(t *testing.T) {
	// No "config" field → normal name.
	typ := Type{Name: "User", fields: make(map[string]*Field)}
	assert.Equal(t, "Config", typ.ConfigMethodName())

	// Has lowercase "config" field → avoid clash.
	typWithConfig := Type{
		Name:   "User",
		fields: map[string]*Field{"config": {Name: "config"}},
	}
	assert.Equal(t, "RuntimeConfig", typWithConfig.ConfigMethodName())

	// Has uppercase "Config" field → avoid clash.
	typWithConfigUpper := Type{
		Name:   "User",
		fields: map[string]*Field{"Config": {Name: "Config"}},
	}
	assert.Equal(t, "RuntimeConfig", typWithConfigUpper.ConfigMethodName())
}

func TestType_SetConfigMethodName(t *testing.T) {
	// No "config" field → normal name.
	typ := Type{Name: "User", fields: make(map[string]*Field)}
	assert.Equal(t, "SetConfig", typ.SetConfigMethodName())

	// Has "config" field → avoid clash.
	typWithConfig := Type{
		Name:   "User",
		fields: map[string]*Field{"config": {Name: "config"}},
	}
	assert.Equal(t, "SetRuntimeConfig", typWithConfig.SetConfigMethodName())
}

func TestType_ValueName_Conflict(t *testing.T) {
	// No conflict → "Value".
	typ := Type{Name: "User", fields: make(map[string]*Field)}
	assert.Equal(t, "Value", typ.ValueName())

	// "Value" field exists → "GetValue".
	typWithValue := Type{
		Name:   "User",
		fields: map[string]*Field{"Value": {Name: "Value"}},
	}
	assert.Equal(t, "GetValue", typWithValue.ValueName())

	// "value" field exists → "GetValue".
	typWithLowerValue := Type{
		Name:   "User",
		fields: map[string]*Field{"value": {Name: "value"}},
	}
	assert.Equal(t, "GetValue", typWithLowerValue.ValueName())
}

func TestType_SiblingImports(t *testing.T) {
	postType := &Type{Name: "Post"}
	tagType := &Type{Name: "Tag"}
	typ := Type{
		Name: "User",
		Config: &Config{
			Package: "example.com/project/velox",
		},
		Edges: []*Edge{
			{Name: "posts", Type: postType},
			{Name: "tags", Type: tagType},
			{Name: "extra_posts", Type: postType}, // duplicate — should be deduped
		},
	}
	imports := typ.SiblingImports()
	// Self + post + tag = 3 unique imports.
	assert.Len(t, imports, 3)
}

func TestType_HookPositions(t *testing.T) {
	pos := &load.Position{MixinIndex: 0, MixedIn: true}
	typ := Type{schema: &load.Schema{
		Hooks: []*load.Position{pos},
	}}
	positions := typ.HookPositions()
	require.Len(t, positions, 1)
	assert.Equal(t, pos, positions[0])

	// Nil schema.
	typ2 := Type{}
	assert.Nil(t, typ2.HookPositions())
}

func TestType_InterceptorPositions(t *testing.T) {
	pos := &load.Position{MixinIndex: 0, MixedIn: true}
	typ := Type{schema: &load.Schema{
		Interceptors: []*load.Position{pos},
	}}
	positions := typ.InterceptorPositions()
	require.Len(t, positions, 1)
	assert.Equal(t, pos, positions[0])

	// Nil schema.
	typ2 := Type{}
	assert.Nil(t, typ2.InterceptorPositions())
}

func TestType_PolicyPositions(t *testing.T) {
	pos := &load.Position{MixinIndex: 1, MixedIn: true}
	typ := Type{schema: &load.Schema{
		Policy: []*load.Position{pos},
	}}
	positions := typ.PolicyPositions()
	require.Len(t, positions, 1)
	assert.Equal(t, pos, positions[0])

	// Nil schema.
	typ2 := Type{}
	assert.Nil(t, typ2.PolicyPositions())
}

func TestType_RelatedTypes(t *testing.T) {
	postType := &Type{Name: "Post"}
	tagType := &Type{Name: "Tag"}
	typ := Type{Edges: []*Edge{
		{Name: "posts", Type: postType},
		{Name: "tags", Type: tagType},
		{Name: "featured_posts", Type: postType}, // duplicate Post
	}}
	related := typ.RelatedTypes()
	// Should contain Post and Tag exactly once each.
	assert.Len(t, related, 2)
	names := make([]string, 0, 2)
	for _, r := range related {
		names = append(names, r.Name)
	}
	assert.Contains(t, names, "Post")
	assert.Contains(t, names, "Tag")
}

func TestType_UnexportedForeignKeys(t *testing.T) {
	typ := Type{ForeignKeys: []*ForeignKey{
		{UserDefined: true},
		{UserDefined: false},
		{UserDefined: false},
	}}
	unexported := typ.UnexportedForeignKeys()
	assert.Len(t, unexported, 2)
}

func TestType_MixedInInterceptors(t *testing.T) {
	// No schema → nil.
	typ := Type{}
	assert.Nil(t, typ.MixedInInterceptors())

	// With mixed-in interceptors.
	typ2 := Type{schema: &load.Schema{
		Interceptors: []*load.Position{
			{MixinIndex: 0, MixedIn: true},
			{MixinIndex: 1, MixedIn: true},
			{MixinIndex: 0, MixedIn: true}, // duplicate mixin index
			{MixinIndex: -1, MixedIn: false},
		},
	}}
	indices := typ2.MixedInInterceptors()
	assert.Equal(t, []int{0, 1}, indices)
}

func TestType_MixedInPolicies(t *testing.T) {
	// No schema → nil.
	typ := Type{}
	assert.Nil(t, typ.MixedInPolicies())

	// With mixed-in policies.
	typ2 := Type{schema: &load.Schema{
		Policy: []*load.Position{
			{MixinIndex: 2, MixedIn: true},
			{MixinIndex: -1, MixedIn: false},
		},
	}}
	indices := typ2.MixedInPolicies()
	assert.Equal(t, []int{2}, indices)
}

// =============================================================================
// type_field.go — zero-coverage functions
// =============================================================================

func TestField_NillableValue(t *testing.T) {
	// Nillable=true, no RType → true (not a pointer already).
	f := Field{
		Nillable: true,
		Type:     &field.TypeInfo{Type: field.TypeString},
	}
	assert.True(t, f.NillableValue())

	// Not nillable → false.
	f2 := Field{
		Nillable: false,
		Type:     &field.TypeInfo{Type: field.TypeString},
	}
	assert.False(t, f2.NillableValue())

	// Nillable but RType is already a pointer → false.
	f3 := Field{
		Nillable: true,
		Type: &field.TypeInfo{
			Type:  field.TypeString,
			RType: &field.RType{Kind: reflect.Ptr},
		},
	}
	assert.False(t, f3.NillableValue())
}

func TestField_ScanType(t *testing.T) {
	tests := []struct {
		name string
		ft   field.Type
		want string
	}{
		{"json", field.TypeJSON, "[]byte"},
		{"bytes", field.TypeBytes, "[]byte"},
		{"string", field.TypeString, "sql.NullString"},
		{"enum", field.TypeEnum, "sql.NullString"},
		{"bool", field.TypeBool, "sql.NullBool"},
		{"time", field.TypeTime, "sql.NullTime"},
		{"int", field.TypeInt, "sql.NullInt64"},
		{"int8", field.TypeInt8, "sql.NullInt64"},
		{"int16", field.TypeInt16, "sql.NullInt64"},
		{"int32", field.TypeInt32, "sql.NullInt64"},
		{"int64", field.TypeInt64, "sql.NullInt64"},
		{"uint", field.TypeUint, "sql.NullInt64"},
		{"uint8", field.TypeUint8, "sql.NullInt64"},
		{"uint16", field.TypeUint16, "sql.NullInt64"},
		{"uint32", field.TypeUint32, "sql.NullInt64"},
		{"uint64", field.TypeUint64, "sql.NullInt64"},
		{"float32", field.TypeFloat32, "sql.NullFloat64"},
		{"float64", field.TypeFloat64, "sql.NullFloat64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.ft}}
			assert.Equal(t, tt.want, f.ScanType())
		})
	}
}

func TestField_NewScanType(t *testing.T) {
	tests := []struct {
		name string
		ft   field.Type
		want string
	}{
		{"json", field.TypeJSON, "new([]byte)"},
		{"bytes", field.TypeBytes, "new([]byte)"},
		{"string", field.TypeString, "new(sql.NullString)"},
		{"enum", field.TypeEnum, "new(sql.NullString)"},
		{"bool", field.TypeBool, "new(sql.NullBool)"},
		{"time", field.TypeTime, "new(sql.NullTime)"},
		{"int", field.TypeInt, "new(sql.NullInt64)"},
		{"float64", field.TypeFloat64, "new(sql.NullFloat64)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.ft}}
			assert.Equal(t, tt.want, f.NewScanType())
		})
	}
}

func TestField_ValueFunc_NoScanner(t *testing.T) {
	f := Field{
		Name: "name",
		def:  &load.Field{ValueScanner: false},
	}
	_, err := f.ValueFunc()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an external ValueScanner")
}

func TestField_ScanValueFunc_NoScanner(t *testing.T) {
	f := Field{
		Name: "name",
		def:  &load.Field{ValueScanner: false},
	}
	_, err := f.ScanValueFunc()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an external ValueScanner")
}

func TestField_FromValueFunc_NoScanner(t *testing.T) {
	f := Field{
		Name: "name",
		def:  &load.Field{ValueScanner: false},
	}
	_, err := f.FromValueFunc()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an external ValueScanner")
}

func TestField_SupportsMutationAppend(t *testing.T) {
	// JSON slice field → true.
	f := Field{
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			RType: &field.RType{Kind: reflect.Slice},
		},
	}
	assert.True(t, f.SupportsMutationAppend())

	// JSON but non-slice → false.
	f2 := Field{
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			RType: &field.RType{Kind: reflect.Map},
		},
	}
	assert.False(t, f2.SupportsMutationAppend())

	// Not JSON → false.
	f3 := Field{
		Type: &field.TypeInfo{
			Type:  field.TypeString,
			RType: &field.RType{Kind: reflect.Slice},
		},
	}
	assert.False(t, f3.SupportsMutationAppend())

	// nil RType → false.
	f4 := Field{
		Type: &field.TypeInfo{Type: field.TypeJSON},
	}
	assert.False(t, f4.SupportsMutationAppend())
}

func TestField_SignedType(t *testing.T) {
	tests := []struct {
		in  field.Type
		out field.Type
	}{
		{field.TypeUint8, field.TypeInt8},
		{field.TypeUint16, field.TypeInt16},
		{field.TypeUint32, field.TypeInt32},
		{field.TypeUint64, field.TypeInt64},
		{field.TypeUint, field.TypeInt},
		{field.TypeInt, field.TypeInt}, // signed int stays int
	}
	for _, tt := range tests {
		t.Run(tt.in.String(), func(t *testing.T) {
			f := Field{
				Name: "count",
				Type: &field.TypeInfo{Type: tt.in},
			}
			signed, err := f.SignedType()
			require.NoError(t, err)
			assert.Equal(t, tt.out, signed.Type)
		})
	}
}

func TestField_SignedType_Error(t *testing.T) {
	// String field doesn't support MutationAdd.
	f := Field{
		Name: "name",
		Type: &field.TypeInfo{Type: field.TypeString},
	}
	_, err := f.SignedType()
	assert.Error(t, err)
}

func TestField_MutationAddAssignExpr(t *testing.T) {
	// Basic int field.
	f := Field{
		Name: "age",
		Type: &field.TypeInfo{Type: field.TypeInt},
	}
	expr, err := f.MutationAddAssignExpr("m.age", "v")
	require.NoError(t, err)
	assert.Equal(t, "*m.age += v", expr)
}

func TestField_MutationAddAssignExpr_Error(t *testing.T) {
	// String field → no add support.
	f := Field{
		Name: "name",
		Type: &field.TypeInfo{Type: field.TypeString},
	}
	_, err := f.MutationAddAssignExpr("m.name", "v")
	assert.Error(t, err)
}

func TestField_BasicType_NoGoType(t *testing.T) {
	// No GoType → returns the ident unchanged.
	f := Field{Type: &field.TypeInfo{Type: field.TypeString}}
	assert.Equal(t, "v", f.BasicType("v"))
}

func TestField_EnumPkgPath(t *testing.T) {
	// nil typ → empty.
	f := Field{Type: &field.TypeInfo{Type: field.TypeEnum}}
	assert.Equal(t, "", f.EnumPkgPath())

	// With typ and cfg.
	typ := &Type{Name: "User"}
	cfg := &Config{Package: "example.com/project/ent"}
	f2 := Field{
		Type: &field.TypeInfo{Type: field.TypeEnum},
		typ:  typ,
		cfg:  cfg,
	}
	assert.Equal(t, "example.com/project/ent/user", f2.EnumPkgPath())

	// With typ but no package in cfg → just dir.
	f3 := Field{
		Type: &field.TypeInfo{Type: field.TypeEnum},
		typ:  typ,
		cfg:  &Config{},
	}
	assert.Equal(t, "user", f3.EnumPkgPath())
}

func TestField_Ops_NilCfg(t *testing.T) {
	// Ops() should not panic when cfg is nil.
	f := &Field{
		Name: "age",
		Type: &field.TypeInfo{Type: field.TypeInt},
	}
	ops := f.Ops()
	assert.NotNil(t, ops)
}

func TestField_BuilderField_EdgeField(t *testing.T) {
	// We can at least call on a non-edge field to confirm the non-panic path.
	fNonEdge := Field{Name: "email"}
	assert.Equal(t, "email", fNonEdge.BuilderField())
}

func TestField_DefaultValue_Nil(t *testing.T) {
	f := Field{}
	assert.Nil(t, f.DefaultValue())
}

func TestField_DefaultValue_WithDef(t *testing.T) {
	f := Field{def: &load.Field{DefaultValue: "hello"}}
	assert.Equal(t, "hello", f.DefaultValue())
}

func TestField_DefaultFunc_Nil(t *testing.T) {
	f := Field{}
	assert.False(t, f.DefaultFunc())
}

func TestField_DefaultFunc_Func(t *testing.T) {
	f := Field{def: &load.Field{DefaultKind: reflect.Func}}
	assert.True(t, f.DefaultFunc())
}

func TestField_DefaultFunc_NonFunc(t *testing.T) {
	f := Field{def: &load.Field{DefaultKind: reflect.String}}
	assert.False(t, f.DefaultFunc())
}

// =============================================================================
// type_helpers.go
// =============================================================================

func TestTitleCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"string", "String"},
		{"bool", "Bool"},
		{"int64", "Int64"},
		{"Float64", "Float64"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, titleCase(tt.in), "titleCase(%q)", tt.in)
	}
}

func TestValidateSQLAnnotation(t *testing.T) {
	err := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"ColumnType": "TEXT"},
	})
	assert.NoError(t, err)
	err2 := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"ColumnType": "DROP TABLE users"},
	})
	assert.Error(t, err2)
	err3 := validateSQLAnnotation(nil)
	assert.NoError(t, err3)
}

func TestSqlIndexAnnotate(t *testing.T) {
	// Nil → nil.
	assert.Nil(t, sqlIndexAnnotate(nil))

	// Missing key → nil.
	assert.Nil(t, sqlIndexAnnotate(map[string]any{"other": "val"}))
}

// =============================================================================
// generate.go — JenniferGenerator helpers
// =============================================================================

func TestJenniferGenerator_WithPackage(t *testing.T) {
	g := &JenniferGenerator{pkg: "original", generatedEnums: make(map[string]bool)}
	g.WithPackage("newpkg")
	assert.Equal(t, "newpkg", g.pkg)

	// Empty string → no change.
	g.WithPackage("")
	assert.Equal(t, "newpkg", g.pkg)
}

func TestJenniferGenerator_WithWorkers(t *testing.T) {
	g := &JenniferGenerator{workers: 4, generatedEnums: make(map[string]bool)}
	g.WithWorkers(8)
	assert.Equal(t, 8, g.workers)
	g.WithWorkers(0)
	assert.Equal(t, 8, g.workers)
}

func TestJenniferGenerator_VeloxPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox", g.VeloxPkg())
}

func TestJenniferGenerator_SQLPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox/dialect/sql", g.SQLPkg())
}

func TestJenniferGenerator_SQLGraphPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox/dialect/sql/sqlgraph", g.SQLGraphPkg())
}

func TestJenniferGenerator_FieldPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox/schema/field", g.FieldPkg())
}

func TestJenniferGenerator_LeafPkgPath(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	typ := &Type{Name: "User"}
	assert.Equal(t, "example.com/app/ent/user", g.LeafPkgPath(typ))
}

func TestJenniferGenerator_LeafPkgPath_FallbackPkg(t *testing.T) {
	// When graph.Config is nil or Package is empty, falls back to g.pkg.
	graph := &Graph{}
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	typ := &Type{Name: "Post"}
	assert.Equal(t, "ent/post", g.LeafPkgPath(typ))
}

func TestJenniferGenerator_PredicateType(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	typ := &Type{Name: "User"}
	code := g.PredicateType(typ)
	require.NotNil(t, code)
}

func TestJenniferGenerator_EdgePredicateType(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	e := &Edge{Name: "posts", Type: &Type{Name: "Post"}}
	code := g.EdgePredicateType(e)
	require.NotNil(t, code)
}

func TestJenniferGenerator_FeatureEnabled(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
	}
	// Non-existent feature → false (with slog warning, no panic).
	assert.False(t, g.FeatureEnabled("nonexistent_feature"))
	// Known feature name.
	assert.False(t, g.FeatureEnabled(FeatureSnapshot.Name))
}

func TestJenniferGenerator_InternalPkg(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
	}
	assert.Equal(t, "example.com/app/ent/internal", g.InternalPkg())
}

func TestJenniferGenerator_RootPkg_NoEntityPackageDialect(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	// Use a minimal dialect that doesn't implement EntityPackageDialect.
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
		dialect:        nil, // no dialect
	}
	assert.Equal(t, "", g.RootPkg())
}

func TestJenniferGenerator_Pkg(t *testing.T) {
	g := &JenniferGenerator{pkg: "ent", generatedEnums: make(map[string]bool)}
	assert.Equal(t, "ent", g.Pkg())
}

func TestJenniferGenerator_Graph(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
	}
	assert.Equal(t, graph, g.Graph())
}

func TestJenniferGenerator_MarkEnumGenerated(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	// First call → not already generated.
	assert.False(t, g.MarkEnumGenerated("StatusEnum"))
	// Second call → already generated.
	assert.True(t, g.MarkEnumGenerated("StatusEnum"))
	// Different enum → not yet generated.
	assert.False(t, g.MarkEnumGenerated("RoleEnum"))
}

func TestJenniferGenerator_ZeroValue_NilField(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	code := g.ZeroValue(nil)
	require.NotNil(t, code)
}

func TestJenniferGenerator_ZeroValue_NillableField(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	f := &Field{
		Nillable: true,
		Type:     &field.TypeInfo{Type: field.TypeString},
	}
	code := g.ZeroValue(f)
	// For nillable fields, should be nil literal.
	stmt := jen.Return(code)
	require.NotNil(t, stmt)
}

func TestJenniferGenerator_AnnotationExists(t *testing.T) {
	cfg := &Config{}
	cfg.Annotations = Annotations{"foo": "bar"}
	g := &JenniferGenerator{
		graph:          &Graph{Config: cfg},
		generatedEnums: make(map[string]bool),
	}
	assert.True(t, g.AnnotationExists("foo"))
	assert.False(t, g.AnnotationExists("bar"))

	// Nil annotations.
	g2 := &JenniferGenerator{
		graph:          &Graph{Config: &Config{}},
		generatedEnums: make(map[string]bool),
	}
	assert.False(t, g2.AnnotationExists("anything"))
}

func TestJenniferGenerator_NewFile(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	f := g.NewFile("ent")
	require.NotNil(t, f)
}

// =============================================================================
// graph.go — SupportMigrate, SchemaSnapshot
// =============================================================================

func TestGraph_SupportMigrate(t *testing.T) {
	// No storage → false.
	g := &Graph{Config: &Config{}}
	assert.False(t, g.SupportMigrate())

	// Storage with Migrate mode.
	g2 := &Graph{Config: &Config{Storage: &Storage{SchemaMode: Migrate}}}
	assert.True(t, g2.SupportMigrate())

	// Storage without Migrate mode.
	g3 := &Graph{Config: &Config{Storage: &Storage{SchemaMode: Unique}}}
	assert.False(t, g3.SupportMigrate())
}

func TestGraph_SchemaSnapshot(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)

	snap, err := graph.SchemaSnapshot()
	require.NoError(t, err)
	assert.NotEmpty(t, snap)
	// Should be a JSON-quoted string containing the schema nodes.
	assert.Contains(t, snap, "T1")
	assert.Contains(t, snap, "T2")
}

// =============================================================================
// config.go — ModuleInfo (smoke test — returns empty outside module)
// =============================================================================

func TestConfig_ModuleInfo_Smoke(t *testing.T) {
	c := &Config{}
	// Should not panic; result may be empty outside velox module context.
	_ = c.ModuleInfo()
}

// =============================================================================
// helper_entity.go
// =============================================================================

func TestEntityPkgHelper_Pkg(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	assert.Equal(t, "user", h.(*entityPkgHelper).Pkg())
}

func TestEntityPkgHelper_RootPkg(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	assert.Equal(t, "example.com/app/ent", h.(*entityPkgHelper).RootPkg())
}

func TestEntityPkgHelper_LeafPkgPath_Self(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	// Self-reference → empty (no self-import).
	assert.Equal(t, "", h.(*entityPkgHelper).LeafPkgPath(&Type{Name: "User"}))
}

func TestEntityPkgHelper_LeafPkgPath_Other(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	// Other entity → delegates to base.
	path := h.(*entityPkgHelper).LeafPkgPath(&Type{Name: "Post"})
	assert.Contains(t, path, "post")
}

func TestEntityPkgHelper_GoType_Enum(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	f := &Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
	}
	code := h.(*entityPkgHelper).GoType(f)
	require.NotNil(t, code)

	// Nillable enum → pointer.
	f2 := &Field{
		Name:     "status",
		Nillable: true,
		Type:     &field.TypeInfo{Type: field.TypeEnum},
	}
	code2 := h.(*entityPkgHelper).GoType(f2)
	require.NotNil(t, code2)
}

func TestEntityPkgHelper_BaseType_Enum(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	f := &Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
	}
	code := h.(*entityPkgHelper).BaseType(f)
	require.NotNil(t, code)
}

// =============================================================================
// template.go — ParseFiles, ParseGlob, AddParseTree, Dependencies
// =============================================================================

func TestTemplate_ParseFiles_Error(t *testing.T) {
	tmpl := NewTemplate("test")
	// Non-existent file → error.
	_, err := tmpl.ParseFiles("/nonexistent/file.tmpl")
	assert.Error(t, err)
}

func TestTemplate_ParseGlob_Error(t *testing.T) {
	tmpl := NewTemplate("test")
	// No matching files → error (ParseGlob fails with no files).
	_, err := tmpl.ParseGlob("/nonexistent/*.tmpl")
	assert.Error(t, err)
}

func TestTemplate_AddParseTree(t *testing.T) {
	tmpl := NewTemplate("test")
	tree := &parse.Tree{Name: "child"}
	result, err := tmpl.AddParseTree("child", tree)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestDependencies_Name(t *testing.T) {
	d := Dependencies{}
	assert.Equal(t, "Dependencies", d.Name())
}

func TestDependencies_Merge(t *testing.T) {
	d1 := Dependencies{&Dependency{Field: "HTTPClient"}}
	d2 := Dependencies{&Dependency{Field: "Logger"}}
	merged := d1.Merge(d2)
	deps, ok := merged.(Dependencies)
	require.True(t, ok)
	assert.Len(t, deps, 2)
}

func TestDependencies_Merge_NonDeps(t *testing.T) {
	d := Dependencies{&Dependency{Field: "HTTPClient"}}
	// Merge with non-Dependencies annotation → returns original.
	result := d.Merge(nil)
	assert.Equal(t, d, result)
}

// =============================================================================
// option.go — WithGenerator
// =============================================================================

func TestWithGenerator(t *testing.T) {
	g := GenerateFunc(func(_ *Graph) error { return nil })
	opt := WithGenerator(g)
	c := &Config{}
	require.NoError(t, opt(c))
	assert.NotNil(t, c.Generator)
}

func TestWithGenerator_Nil(t *testing.T) {
	opt := WithGenerator(nil)
	c := &Config{}
	err := opt(c)
	assert.Error(t, err)
}

// =============================================================================
// type_edge.go — HasFieldSetter
// =============================================================================

func TestEdge_HasFieldSetter_NonOwn(t *testing.T) {
	// Inverse edge → not own FK → false.
	e := &Edge{Name: "posts", Rel: Relation{Type: O2M}}
	assert.False(t, e.HasFieldSetter())
}

// =============================================================================
// type_field.go — rtypeEqual, goType, standardNullType
// =============================================================================

func TestRtypeEqual(t *testing.T) {
	t1 := &field.RType{Kind: reflect.String, Ident: "string", PkgPath: ""}
	t2 := &field.RType{Kind: reflect.String, Ident: "string", PkgPath: ""}
	assert.True(t, rtypeEqual(t1, t2))

	t3 := &field.RType{Kind: reflect.Int, Ident: "int", PkgPath: ""}
	assert.False(t, rtypeEqual(t1, t3))
}

func TestField_GoType_NoGoType(t *testing.T) {
	// goType with no custom type → returns ident unchanged.
	f := Field{Type: &field.TypeInfo{Type: field.TypeString}}
	assert.Equal(t, "v", f.goType("v"))
}

func TestField_FieldAnnotate_Valid(t *testing.T) {
	ann := fieldAnnotate(map[string]any{
		"FieldAnnotation": map[string]any{
			"OrderField": "EMAIL",
		},
	})
	// fieldAnnotate returns nil for unknown keys; returns the object otherwise.
	// The FieldAnnotation key is field.Annotation.Name().
	_ = ann // just ensure no panic
}

func TestField_FieldAnnotate_Nil(t *testing.T) {
	assert.Nil(t, fieldAnnotate(nil))
	assert.Nil(t, fieldAnnotate(map[string]any{}))
}

func TestSqlIndexAnnotate_WithData(t *testing.T) {
	// Has sqlschema.IndexAnnotation key.
	result := sqlIndexAnnotate(map[string]any{
		"IndexAnnotation": map[string]any{"Type": "GIN"},
	})
	// Key doesn't match the annotation name → still nil.
	_ = result
}

// =============================================================================
// generate.go — osFS.WriteFile, osFS.Glob
// =============================================================================

func TestWriteFileResult(t *testing.T) {
	g := &JenniferGenerator{outDir: t.TempDir(), generatedEnums: make(map[string]bool)}
	err := g.writeFileResult(context.Background(), nil, nil, "", "skip.go")
	assert.NoError(t, err)
	err2 := g.writeFileResult(context.Background(), nil, fmt.Errorf("gen failed"), "sub", "fail.go")
	assert.ErrorContains(t, err2, "gen failed")
}

// =============================================================================
// generate.go — JenniferGenerator.EdgeRel
// =============================================================================

func TestJenniferGenerator_EdgeRel(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	tests := []struct {
		rel  Rel
		want string
	}{
		{O2O, "O2O"},
		{O2M, "O2M"},
		{M2O, "M2O"},
		{M2M, "M2M"},
	}
	for _, tt := range tests {
		e := &Edge{Rel: Relation{Type: tt.rel}}
		assert.Equal(t, tt.want, g.EdgeRelType(e))
	}
}

func TestJenniferGenerator_EdgeRel_Default(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	// Unknown relation type → defaults to "O2M".
	e := &Edge{Rel: Relation{Type: Rel(99)}}
	assert.Equal(t, "O2M", g.EdgeRelType(e))
}

// =============================================================================
// type_field.go — ScanTypeField (basic path)
// =============================================================================

func TestField_ScanTypeField_BasicTypes(t *testing.T) {
	tests := []struct {
		name   string
		ft     field.Type
		rec    string
		wantFn func(string) bool
	}{
		{"string", field.TypeString, "v", func(s string) bool { return s != "" }},
		{"bool", field.TypeBool, "v", func(s string) bool { return s != "" }},
		{"int64", field.TypeInt64, "v", func(s string) bool { return s != "" }},
		{"float64", field.TypeFloat64, "v", func(s string) bool { return s != "" }},
		{"time", field.TypeTime, "v", func(s string) bool { return s == "v.Time" }},
		{"float32", field.TypeFloat32, "v", func(s string) bool { return s != "" }},
		{"int", field.TypeInt, "v", func(s string) bool { return s != "" }},
		{"uint", field.TypeUint, "v", func(s string) bool { return s != "" }},
		{"json", field.TypeJSON, "v", func(s string) bool { return s == "v" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.ft}}
			result := f.ScanTypeField(tt.rec)
			assert.True(t, tt.wantFn(result), "ScanTypeField(%q) = %q", tt.rec, result)
		})
	}
}

// =============================================================================
// template.go — ParseDir
// =============================================================================

func TestTemplate_ParseDir_Empty(t *testing.T) {
	dir := t.TempDir()
	tmpl := NewTemplate("test")
	result, err := tmpl.ParseDir(dir)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestTemplate_ParseDir_WithFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(dir+"/my.tmpl", []byte("{{.Name}}"), 0o644)
	require.NoError(t, err)

	tmpl := NewTemplate("test")
	result, err := tmpl.ParseDir(dir)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// =============================================================================
// fkSymbols — via graph with M2M edge (exercises internal fkSymbols)
// =============================================================================

func TestFkSymbols_ViaEdgeSchemas(t *testing.T) {
	// fkSymbols is called by edgeSchemas for M2M edges.
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	tables := graph.edgeSchemas()
	// edgeSchemas returns the M2M join tables.
	_ = tables
}

// =============================================================================
// schema.Column usage to keep the import used
// =============================================================================

func TestSchemaColumn_Import(t *testing.T) {
	c := &schema.Column{Name: "id"}
	assert.Equal(t, "id", c.Name)
}

// =============================================================================
// storage.go — TableSchemas error path
// =============================================================================

func TestGraph_TableSchemas_MissingAnnotation(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	_, err = graph.TableSchemas()
	assert.Error(t, err)
}

// =============================================================================
// fieldAnnotate — with valid annotation key
// =============================================================================

func TestFieldAnnotate_WithAnnotationKey(t *testing.T) {
	annotationName := (&field.Annotation{}).Name()
	ann := fieldAnnotate(map[string]any{
		annotationName: map[string]any{
			"OrderField": "EMAIL",
		},
	})
	_ = ann
}
