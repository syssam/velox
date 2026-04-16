package graphql

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

func TestGenerator_Pluralize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"User", "Users"},
		{"Post", "Posts"},
		{"Category", "Categories"},
		{"Box", "Boxes"},
		{"Bus", "Buses"},
		{"Match", "Matches"},
		{"Dish", "Dishes"},
		{"Day", "Days"},
		// Irregular nouns handled by inflect
		{"Person", "People"},
		{"Child", "Children"},
		{"Mouse", "Mice"},
		{"Goose", "Geese"},
		{"Analysis", "Analyses"},
	}

	for _, tt := range tests {
		result := pluralize(tt.input)
		if result != tt.expected {
			t.Errorf("pluralize(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGenerator_Pascal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "User"},
		{"user_name", "UserName"},
		{"created_at", "CreatedAt"},
		{"ID", "ID"},
	}

	for _, tt := range tests {
		result := pascal(tt.input)
		if result != tt.expected {
			t.Errorf("pascal(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGenerator_Camel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"User", "user"},
		{"UserName", "userName"},
		{"user_name", "userName"},
		{"ID", "id"},
		{"UserID", "userId"},
		{"HTTPServer", "httpServer"},
		{"api_key", "apiKey"},
		{"AvatarURL", "avatarUrl"},
		{"LocationLat", "locationLat"},
		{"IsEmailVerified", "isEmailVerified"},
	}

	for _, tt := range tests {
		result := camel(tt.input)
		if result != tt.expected {
			t.Errorf("camel(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGenerator_ToSnakeCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"User", "user"},
		{"UserName", "user_name"},
		{"createdAt", "created_at"},
	}

	for _, tt := range tests {
		result := toSnakeCase(tt.input)
		if result != tt.expected {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGenerator_TypeDirectives_DeterministicOrder(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{
			"graphql": Annotation{
				Directives: []Directive{
					{
						Name: "cacheControl",
						Args: map[string]any{
							"maxAge": 300,
							"scope":  "PRIVATE",
						},
					},
				},
			},
		},
	}

	g := &entgen.Graph{Nodes: []*entgen.Type{userType}}
	gen := NewGenerator(g, Config{ORMPackage: "example/ent"})

	// Run multiple times to verify deterministic output
	first := gen.typeDirectives(userType)
	for i := 0; i < 20; i++ {
		got := gen.typeDirectives(userType)
		if got != first {
			t.Fatalf("typeDirectives output is non-deterministic:\nfirst: %s\ngot:   %s", first, got)
		}
	}

	// Verify args are sorted alphabetically
	if !strings.Contains(first, "maxAge: 300, scope: ") {
		t.Errorf("args should be sorted: got %s", first)
	}
}

func TestGenerator_TypeDirectives_StringArgQuoted(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{
			"graphql": Annotation{
				Directives: []Directive{
					{
						Name: "deprecated",
						Args: map[string]any{
							"reason": `Use "Member" instead`,
						},
					},
				},
			},
		},
	}

	g := &entgen.Graph{Nodes: []*entgen.Type{userType}}
	gen := NewGenerator(g, Config{ORMPackage: "example/ent"})

	result := gen.typeDirectives(userType)
	// String values should be properly quoted
	if !strings.Contains(result, `reason: "Use \"Member\" instead"`) {
		t.Errorf("string directive arg should be quoted, got: %s", result)
	}
}

// =============================================================================
// extractGraphQLAnnotation JSON fallback test
// =============================================================================

func TestExtractGraphQLAnnotation_JSONFallback(t *testing.T) {
	// Simulate an annotation stored as a map (JSON-like) instead of typed struct
	ann := map[string]any{
		"graphql": map[string]any{
			"Type":            "Member",
			"RelayConnection": true,
			"Skip":            float64(SkipWhereInput), // JSON numbers are float64
		},
	}

	result := extractGraphQLAnnotation(ann)
	if result.Type != "Member" {
		t.Errorf("Type = %q, want %q", result.Type, "Member")
	}
	if !result.RelayConnection {
		t.Error("RelayConnection should be true")
	}
	if result.Skip != SkipWhereInput {
		t.Errorf("Skip = %d, want %d", result.Skip, SkipWhereInput)
	}
}

func TestExtractGraphQLAnnotation_NilMap(t *testing.T) {
	result := extractGraphQLAnnotation(nil)
	if result.Type != "" || result.Skip != 0 || result.RelayConnection {
		t.Error("nil map should return zero Annotation")
	}
}

func TestExtractGraphQLAnnotation_MissingKey(t *testing.T) {
	result := extractGraphQLAnnotation(map[string]any{"other": "value"})
	if result.Type != "" || result.Skip != 0 || result.RelayConnection {
		t.Error("missing key should return zero Annotation")
	}
}

func TestExtractGraphQLAnnotation_PointerType(t *testing.T) {
	ann := &Annotation{Type: "Member", Skip: SkipType}
	result := extractGraphQLAnnotation(map[string]any{"graphql": ann})
	if result.Type != "Member" || result.Skip != SkipType {
		t.Errorf("pointer annotation not extracted correctly: %+v", result)
	}
}

// =============================================================================
// validateWhereInputAnnotations error cases
// =============================================================================

func TestGenerator_ValidateWhereInputAnnotations_InvalidField(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			"graphql": Annotation{
				WhereInputFieldNames: []string{"nonexistent_field"},
			},
		},
	}

	g := &entgen.Graph{Nodes: []*entgen.Type{userType}}
	gen := NewGenerator(g, Config{ORMPackage: "example/ent", WhereInputs: true})

	err := gen.validateWhereInputAnnotations()
	if err == nil {
		t.Fatal("expected error for invalid field name")
	}
	if !strings.Contains(err.Error(), "nonexistent_field") {
		t.Errorf("error should mention field name, got: %v", err)
	}
}

func TestGenerator_ValidateWhereInputAnnotations_InvalidEdge(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{
			"graphql": Annotation{
				WhereInputEdgeNames: []string{"nonexistent_edge"},
			},
		},
	}

	g := &entgen.Graph{Nodes: []*entgen.Type{userType}}
	gen := NewGenerator(g, Config{ORMPackage: "example/ent", WhereInputs: true})

	err := gen.validateWhereInputAnnotations()
	if err == nil {
		t.Fatal("expected error for invalid edge name")
	}
	if !strings.Contains(err.Error(), "nonexistent_edge") {
		t.Errorf("error should mention edge name, got: %v", err)
	}
}

func TestGenerator_ValidateWhereInputAnnotations_ValidFieldByStructName(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			"graphql": Annotation{
				WhereInputFieldNames: []string{"email"},
			},
		},
	}

	g := &entgen.Graph{Nodes: []*entgen.Type{userType}}
	gen := NewGenerator(g, Config{ORMPackage: "example/ent", WhereInputs: true})

	if err := gen.validateWhereInputAnnotations(); err != nil {
		t.Errorf("valid field name should not error: %v", err)
	}
}

// =============================================================================
// genWhereInputGo rendered output test
// =============================================================================

func TestGenerator_GenWhereInputGo_RenderedOutput(t *testing.T) {
	g := mockGraph()
	gen := NewGenerator(g, Config{
		Package:     "ent",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	})

	f := gen.genWhereInputGo()
	if f == nil {
		t.Fatal("genWhereInputGo should return a file")
	}

	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("failed to render WhereInput Go code: %v", err)
	}
	code := buf.String()

	// Check struct generation
	checks := []struct {
		name string
		want string
	}{
		{"UserWhereInput struct", "type UserWhereInput struct"},
		{"PostWhereInput struct", "type PostWhereInput struct"},
		{"ErrFilterDepthExceeded", "ErrFilterDepthExceeded"},
		{"DefaultMaxFilterDepth", "DefaultMaxFilterDepth"},
		{"ErrEmptyUserWhereInput", "ErrEmptyUserWhereInput"},
		{"P method", "func (i *UserWhereInput) P()"},
		{"p depth method", "func (i *UserWhereInput) p(depth int)"},
		{"Filter method", "func (i *UserWhereInput) Filter("},
		{"AddPredicates method", "func (i *UserWhereInput) AddPredicates("},
		{"Not field", "Not "},
		{"Or field", "Or  "},
		{"And field", "And "},
	}

	for _, tc := range checks {
		if !strings.Contains(code, tc.want) {
			t.Errorf("WhereInput Go code missing %s: want %q", tc.name, tc.want)
		}
	}
}

// =============================================================================
// genEntitySchemaFile test (SchemaSplitPerEntity)
// =============================================================================

func TestGenerator_GenEntitySchemaFile_Content(t *testing.T) {
	g := mockGraph()
	gen := NewGenerator(g, Config{
		Package:         "graphql",
		ORMPackage:      "example/ent",
		WhereInputs:     true,
		Mutations:       true,
		Ordering:        true,
		RelayConnection: true,
	})

	userType := g.Nodes[0] // User
	content := gen.genEntitySchemaFile(userType)

	// Should contain entity type, enums, WhereInput, OrderBy, Connection/Edge, and mutation inputs
	for _, want := range []string{
		"Code generated by velox",
		"type User",
		"enum UserStatus",
		"input UserWhereInput",
		"input UserOrder",
		"enum UserOrderField",
		"type UserConnection",
		"type UserEdge",
		"input CreateUserInput",
		"input UpdateUserInput",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("entity schema file should contain %q", want)
		}
	}

	// Should NOT contain shared types (stays in root)
	for _, notwant := range []string{
		"type Query",
		"scalar Cursor",
		"enum OrderDirection",
		"type PageInfo",
	} {
		if strings.Contains(content, notwant) {
			t.Errorf("entity schema file should NOT contain %q", notwant)
		}
	}
}

func TestGenPerEntityRootSchema_NoEntityTypes(t *testing.T) {
	g := newTestGenerator(
		&entgen.Type{
			Name: "User",
			ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
			Fields: []*entgen.Field{
				{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
			},
		},
	)
	g.config.SchemaSplitMode = SchemaSplitPerEntity
	g.config.RelaySpec = true
	sdl := g.genPerEntityRootSchema()
	// Root should NOT contain entity types or enums
	assert.NotContains(t, sdl, "type User")
	// Root should still contain shared types
	assert.Contains(t, sdl, "interface Node")
	assert.Contains(t, sdl, "directive @goField")
}

func TestGenEntitySchemaFile_ContainsEntityType(t *testing.T) {
	typ := &entgen.Type{
		Name: "Invoice",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "amount", Type: &field.TypeInfo{Type: field.TypeFloat64}},
			{Name: "status", Type: &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{
					{Name: "Draft", Value: "draft"},
					{Name: "Sent", Value: "sent"},
				}},
		},
	}
	g := newTestGenerator(typ)
	sdl := g.genEntitySchemaFile(typ)
	// Entity type should be in per-entity file
	assert.Contains(t, sdl, "type Invoice")
	assert.Contains(t, sdl, "amount: Float!")
	// Enum should be in per-entity file
	assert.Contains(t, sdl, "enum InvoiceStatus")
	assert.Contains(t, sdl, "DRAFT")
	assert.Contains(t, sdl, "SENT")
}

// =============================================================================
// shouldSkipEdgeFKField test
// =============================================================================

func TestGenerator_ShouldSkipEdgeFKField(t *testing.T) {
	tests := []struct {
		name       string
		annotation Annotation
		wantSkip   bool
	}{
		{
			name:       "plain FK field is skipped",
			annotation: Annotation{},
			wantSkip:   true,
		},
		{
			name:       "FK field with explicit Type is not skipped",
			annotation: Annotation{Type: "ID"},
			wantSkip:   false,
		},
		{
			name:       "FK field with explicit FieldName is not skipped",
			annotation: Annotation{FieldName: "authorID"},
			wantSkip:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &entgen.Field{
				Name:        "author_id",
				Type:        &field.TypeInfo{Type: field.TypeInt64},
				Annotations: map[string]any{"graphql": tc.annotation},
			}

			gen := NewGenerator(&entgen.Graph{Nodes: []*entgen.Type{}}, Config{})
			got := gen.shouldSkipEdgeFKField(nil, f)
			if got != tc.wantSkip {
				t.Errorf("shouldSkipEdgeFKField() = %v, want %v", got, tc.wantSkip)
			}
		})
	}
}

// =============================================================================
// writeFile mtime preservation test
// =============================================================================

func TestGenerator_WriteFile_SkipsUnchanged(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "writeFile-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	g := &entgen.Graph{
		Nodes: []*entgen.Type{},
		Config: &entgen.Config{
			Package: "example/ent",
			Target:  tmpDir,
		},
	}
	gen := NewGenerator(g, Config{
		OutDir:     tmpDir,
		Package:    "ent",
		ORMPackage: "example/ent",
		RelaySpec:  true,
	})

	// First write
	f := gen.genNodeShared()
	if err := gen.writeFile(context.Background(), f, "test_node.go"); err != nil {
		t.Fatalf("first writeFile failed: %v", err)
	}

	// Record mtime
	info1, _ := os.Stat(filepath.Join(tmpDir, "test_node.go"))
	mtime1 := info1.ModTime()

	// Wait a bit to ensure filesystem mtime granularity
	time.Sleep(50 * time.Millisecond)

	// Second write with same content
	f2 := gen.genNodeShared()
	if err := gen.writeFile(context.Background(), f2, "test_node.go"); err != nil {
		t.Fatalf("second writeFile failed: %v", err)
	}

	// mtime should be unchanged
	info2, _ := os.Stat(filepath.Join(tmpDir, "test_node.go"))
	mtime2 := info2.ModTime()

	if !mtime1.Equal(mtime2) {
		t.Errorf("writeFile should skip unchanged content: mtime changed from %v to %v", mtime1, mtime2)
	}
}

// =============================================================================
// isValidInputType with cached enum names test
// =============================================================================

func TestGenerator_IsValidInputType_CachedEnums(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "status",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "Active", Value: "active"}},
			},
		},
	}

	g := &entgen.Graph{Nodes: []*entgen.Type{userType}}
	gen := NewGenerator(g, Config{ORMPackage: "example/ent"})

	// Generated enum name should be recognized
	if !gen.isValidInputType("UserStatus") {
		t.Error("UserStatus should be valid input type (cached enum)")
	}

	// Known scalars
	for _, scalar := range []string{"ID", "String", "Int", "Float", "Boolean", "Time", "UUID"} {
		if !gen.isValidInputType(scalar) {
			t.Errorf("%s should be valid input type", scalar)
		}
	}

	// Unknown object type
	if gen.isValidInputType("SomeObjectType") {
		t.Error("SomeObjectType should not be valid input type")
	}

	// Input suffix
	if !gen.isValidInputType("CreateUserInput") {
		t.Error("CreateUserInput should be valid input type")
	}
}

// =============================================================================
// MergeAnnotations delegates to mergeAnnotations test
// =============================================================================

func TestMergeAnnotations_DelegatesToMerge(t *testing.T) {
	a := Annotation{
		RelayConnection: true,
		Type:            "Member",
		Skip:            SkipWhereInput,
	}
	b := Annotation{
		QueryField: true,
		Skip:       SkipOrderField,
		FieldName:  "userName",
	}

	// MergeAnnotations (standalone) should produce the same result as sequential Merge
	merged := MergeAnnotations(a, b)
	sequential := mergeAnnotations(mergeAnnotations(Annotation{}, a), b)

	if merged.Skip != sequential.Skip {
		t.Errorf("Skip: MergeAnnotations=%d, sequential=%d", merged.Skip, sequential.Skip)
	}
	if merged.RelayConnection != sequential.RelayConnection {
		t.Error("RelayConnection mismatch")
	}
	if merged.QueryField != sequential.QueryField {
		t.Error("QueryField mismatch")
	}
	if merged.Type != sequential.Type {
		t.Errorf("Type: MergeAnnotations=%q, sequential=%q", merged.Type, sequential.Type)
	}
	if merged.FieldName != sequential.FieldName {
		t.Errorf("FieldName: MergeAnnotations=%q, sequential=%q", merged.FieldName, sequential.FieldName)
	}
}

// =============================================================================
// formatDirectiveArg test
// =============================================================================

func TestFormatDirectiveArg(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"string", "hello", `"hello"`},
		{"string with quotes", `say "hi"`, `"say \"hi\""`},
		{"int", 42, "42"},
		{"int64", int64(100), "100"},
		{"uint", uint(7), "7"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"float64", 3.14, "3.14"},
		{"float32", float32(2.5), "2.5"},
		// Unsupported types are quoted as strings (safe fallback)
		{"map", map[string]any{"key": "val"}, `"map[key:val]"`},
		{"slice", []string{"a", "b"}, `"[a b]"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDirectiveArg(tc.val)
			if got != tc.want {
				t.Errorf("formatDirectiveArg(%v) = %q, want %q", tc.val, got, tc.want)
			}
		})
	}
}

// =============================================================================
// Enum Name Collision Detection Tests
// =============================================================================

func TestValidateEnumNames_DifferentValues_ReturnsError(t *testing.T) {
	// Asset.depreciation_method and AssetDepreciation.method both produce
	// "AssetDepreciationMethod" but with different enum values.
	assetType := &entgen.Type{
		Name: "Asset",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "depreciation_method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}, {Name: "DecliningBalance", Value: "declining_balance"}},
			},
		},
	}
	assetDepreciationType := &entgen.Type{
		Name: "AssetDepreciation",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "Monthly", Value: "monthly"}, {Name: "Yearly", Value: "yearly"}},
			},
		},
	}

	gen := newTestGenerator(assetType, assetDepreciationType)
	err := gen.validateEnumNames()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AssetDepreciationMethod")
	assert.Contains(t, err.Error(), "Asset.depreciation_method")
	assert.Contains(t, err.Error(), "AssetDepreciation.method")
	assert.Contains(t, err.Error(), "graphql.Type")
}

func TestValidateEnumNames_SameValues_SharedEnum(t *testing.T) {
	// Both entities produce "AssetDepreciationMethod" with the same values.
	// This should be allowed (shared enum).
	assetType := &entgen.Type{
		Name: "Asset",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "depreciation_method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}, {Name: "DecliningBalance", Value: "declining_balance"}},
			},
		},
	}
	assetDepreciationType := &entgen.Type{
		Name: "AssetDepreciation",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "DecliningBalance", Value: "declining_balance"}, {Name: "StraightLine", Value: "straight_line"}},
			},
		},
	}

	gen := newTestGenerator(assetType, assetDepreciationType)
	err := gen.validateEnumNames()
	assert.NoError(t, err)
	// Should be marked as shared
	assert.Contains(t, gen.sharedEnums, "AssetDepreciationMethod")
}

func TestValidateEnumNames_NoCollision(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "status",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "Active", Value: "active"}, {Name: "Inactive", Value: "inactive"}},
			},
		},
	}
	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "status",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "Draft", Value: "draft"}, {Name: "Published", Value: "published"}},
			},
		},
	}

	gen := newTestGenerator(userType, postType)
	err := gen.validateEnumNames()
	assert.NoError(t, err)
	assert.Empty(t, gen.sharedEnums)
}

func TestValidateEnumNames_CustomTypeAnnotation_NoCollision(t *testing.T) {
	// graphql.Type() annotation resolves the collision.
	assetType := &entgen.Type{
		Name: "Asset",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "depreciation_method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}},
			},
		},
	}
	assetDepreciationType := &entgen.Type{
		Name: "AssetDepreciation",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "Monthly", Value: "monthly"}},
				Annotations: map[string]any{
					"graphql": Annotation{Type: "DepreciationEntryMethod"},
				},
			},
		},
	}

	gen := newTestGenerator(assetType, assetDepreciationType)
	err := gen.validateEnumNames()
	assert.NoError(t, err)
	assert.Empty(t, gen.sharedEnums)
}

func TestGenEntityEnumTypes_SharedEnum_EmittedOnce(t *testing.T) {
	// Two entities share an enum. Only the first entity should emit it.
	assetType := &entgen.Type{
		Name: "Asset",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "depreciation_method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}, {Name: "DecliningBalance", Value: "declining_balance"}},
			},
		},
	}
	assetDepreciationType := &entgen.Type{
		Name: "AssetDepreciation",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}, {Name: "DecliningBalance", Value: "declining_balance"}},
			},
		},
	}

	gen := newTestGenerator(assetType, assetDepreciationType)

	// Neither entity should emit the shared enum — it goes in the root file.
	output1 := gen.genEntityEnumTypes(assetType)
	assert.NotContains(t, output1, "enum AssetDepreciationMethod")

	output2 := gen.genEntityEnumTypes(assetDepreciationType)
	assert.NotContains(t, output2, "enum AssetDepreciationMethod")
}

func TestGenPerEntityRootSchema_SharedEnums(t *testing.T) {
	// Shared enums should appear in the root schema file.
	assetType := &entgen.Type{
		Name: "Asset",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "depreciation_method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}, {Name: "DecliningBalance", Value: "declining_balance"}},
			},
		},
	}
	assetDepreciationType := &entgen.Type{
		Name: "AssetDepreciation",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name:  "method",
				Type:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{{Name: "StraightLine", Value: "straight_line"}, {Name: "DecliningBalance", Value: "declining_balance"}},
			},
		},
	}

	gen := newTestGeneratorWithConfig(Config{
		ORMPackage:      "example.com/app/velox",
		Package:         "velox",
		SchemaGenerator: true,
	}, assetType, assetDepreciationType)

	rootSchema := gen.genPerEntityRootSchema()
	assert.Contains(t, rootSchema, "enum AssetDepreciationMethod")
	// Should appear exactly once
	assert.Equal(t, 1, strings.Count(rootSchema, "enum AssetDepreciationMethod"))
}
