package gen

// Tests for the small accessors on Type (type.go) and Edge (type_edge.go).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
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
// type_edge.go — HasFieldSetter
// =============================================================================

func TestEdge_HasFieldSetter_NonOwn(t *testing.T) {
	// Inverse edge → not own FK → false.
	e := &Edge{Name: "posts", Rel: Relation{Type: O2M}}
	assert.False(t, e.HasFieldSetter())
}
