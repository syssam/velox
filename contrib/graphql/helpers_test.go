package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// SkipMode.Is() tests
// =============================================================================

func TestSkipMode_Is_SingleFlag(t *testing.T) {
	assert.True(t, SkipType.Is(SkipType))
	assert.True(t, SkipWhereInput.Is(SkipWhereInput))
	assert.True(t, SkipOrderField.Is(SkipOrderField))
	assert.True(t, SkipMutationCreateInput.Is(SkipMutationCreateInput))
	assert.True(t, SkipMutationUpdateInput.Is(SkipMutationUpdateInput))
}

func TestSkipMode_Is_NoMatch(t *testing.T) {
	assert.False(t, SkipType.Is(SkipWhereInput))
	assert.False(t, SkipOrderField.Is(SkipMutationCreateInput))
	assert.False(t, SkipMode(0).Is(SkipType))
}

func TestSkipMode_Is_CombinedFlags(t *testing.T) {
	combined := SkipType | SkipWhereInput
	assert.True(t, combined.Is(SkipType))
	assert.True(t, combined.Is(SkipWhereInput))
	assert.False(t, combined.Is(SkipOrderField))
	assert.False(t, combined.Is(SkipMutationCreateInput))
}

func TestSkipMode_Is_SkipAll_ContainsAllFlags(t *testing.T) {
	assert.True(t, SkipAll.Is(SkipType))
	assert.True(t, SkipAll.Is(SkipEnumField))
	assert.True(t, SkipAll.Is(SkipOrderField))
	assert.True(t, SkipAll.Is(SkipWhereInput))
	assert.True(t, SkipAll.Is(SkipMutationCreateInput))
	assert.True(t, SkipAll.Is(SkipMutationUpdateInput))
}

func TestSkipMode_Is_ZeroFlag(t *testing.T) {
	// Checking against zero flag always returns false (no bit set).
	assert.False(t, SkipAll.Is(0))
	assert.False(t, SkipMode(0).Is(0))
}

func TestSkipMode_Is_SkipInputs(t *testing.T) {
	assert.True(t, SkipInputs.Is(SkipMutationCreateInput))
	assert.True(t, SkipInputs.Is(SkipMutationUpdateInput))
	assert.False(t, SkipInputs.Is(SkipType))
	assert.False(t, SkipInputs.Is(SkipWhereInput))
}

func TestSkipMode_Is_SkipMutations(t *testing.T) {
	assert.True(t, SkipMutations.Is(SkipMutationCreate))
	assert.True(t, SkipMutations.Is(SkipMutationUpdate))
	assert.False(t, SkipMutations.Is(SkipType))
}

func TestSkipMode_Is_SkipEverything(t *testing.T) {
	// SkipEverything = SkipAll | SkipMutations
	assert.True(t, SkipEverything.Is(SkipType))
	assert.True(t, SkipEverything.Is(SkipMutationCreateInput))
	assert.True(t, SkipEverything.Is(SkipMutationUpdateInput))
}

// =============================================================================
// paginationNames() additional edge cases
// =============================================================================

func TestPaginationNames_MultiWord(t *testing.T) {
	names := paginationNames("BlogPost")
	assert.Equal(t, "BlogPostConnection", names.Connection)
	assert.Equal(t, "BlogPostEdge", names.Edge)
	assert.Equal(t, "BlogPost", names.Node)
	assert.Equal(t, "BlogPostOrder", names.Order)
	assert.Equal(t, "BlogPostOrderField", names.OrderField)
	assert.Equal(t, "BlogPostWhereInput", names.WhereInput)
}

func TestPaginationNames_EmptyString(t *testing.T) {
	names := paginationNames("")
	assert.Equal(t, "Connection", names.Connection)
	assert.Equal(t, "Edge", names.Edge)
	assert.Equal(t, "", names.Node)
	assert.Equal(t, "Order", names.Order)
	assert.Equal(t, "OrderField", names.OrderField)
	assert.Equal(t, "WhereInput", names.WhereInput)
}

// =============================================================================
// filterNodes() tests
// =============================================================================

func TestFilterNodes_NoSkip(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{Name: "User", Annotations: map[string]any{}},
		{Name: "Post", Annotations: map[string]any{}},
	}
	result := g.filterNodes(nodes, SkipType)
	assert.Len(t, result, 2)
}

func TestFilterNodes_SkipByAnnotation(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{Name: "User", Annotations: map[string]any{}},
		{
			Name: "Internal",
			Annotations: map[string]any{
				AnnotationName: Annotation{Skip: SkipType},
			},
		},
	}
	result := g.filterNodes(nodes, SkipType)
	require.Len(t, result, 1)
	assert.Equal(t, "User", result[0].Name)
}

func TestFilterNodes_CompositeID_Skipped(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	composite := &gen.Type{Name: "UserGroup", Annotations: map[string]any{}}
	composite.EdgeSchema.To = &gen.Edge{Name: "to"}
	composite.EdgeSchema.ID = []*gen.Field{{Name: "user_id"}, {Name: "group_id"}}

	nodes := []*gen.Type{
		{Name: "User", Annotations: map[string]any{}},
		composite,
	}
	result := g.filterNodes(nodes, SkipType)
	require.Len(t, result, 1)
	assert.Equal(t, "User", result[0].Name)
}

func TestFilterNodes_Empty(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	result := g.filterNodes(nil, SkipType)
	assert.Empty(t, result)
}

func TestFilterNodes_SkipWhereInput_DoesNotFilterType(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: Annotation{Skip: SkipWhereInput},
			},
		},
	}
	// Filtering for SkipType should NOT exclude a node with only SkipWhereInput
	result := g.filterNodes(nodes, SkipType)
	assert.Len(t, result, 1)
}

func TestFilterNodes_AllSkipped(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{
			Name: "A",
			Annotations: map[string]any{
				AnnotationName: Annotation{Skip: SkipAll},
			},
		},
		{
			Name: "B",
			Annotations: map[string]any{
				AnnotationName: Annotation{Skip: SkipAll},
			},
		},
	}
	result := g.filterNodes(nodes, SkipType)
	assert.Empty(t, result)
}

// =============================================================================
// filterFields() -- SkipType implies exclusion from all surfaces
// =============================================================================

func TestFilterFields_SkipTypeExcludesFromAllSurfaces(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	fields := []*gen.Field{
		{Name: "visible", Type: &field.TypeInfo{Type: field.TypeString}},
		{
			Name: "hidden",
			Type: &field.TypeInfo{Type: field.TypeString},
			Annotations: map[string]any{
				AnnotationName: Annotation{Skip: SkipType},
			},
		},
	}
	// Even when filtering for SkipWhereInput, a field with SkipType should be excluded
	result := g.filterFields(fields, SkipWhereInput)
	require.Len(t, result, 1)
	assert.Equal(t, "visible", result[0].Name)
}

func TestFilterFields_EmptySlice(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	result := g.filterFields(nil, SkipType)
	assert.Empty(t, result)
}

// =============================================================================
// MutationDescriptor.Input() with custom Type annotation
// =============================================================================

func TestMutationDescriptor_Input_CustomTypeName(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	typ := &gen.Type{
		Name: "User",
		Annotations: map[string]any{
			AnnotationName: Annotation{Type: "Member"},
		},
	}

	md := &MutationDescriptor{Type: typ, IsCreate: true}
	assert.Equal(t, "CreateMemberInput", md.Input(g))

	md2 := &MutationDescriptor{Type: typ, IsCreate: false}
	assert.Equal(t, "UpdateMemberInput", md2.Input(g))
}

// =============================================================================
// filterEdges() -- composite ID edge target
// =============================================================================

func TestFilterEdges_CompositeIDTarget_Skipped(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	compositeTarget := &gen.Type{Name: "UserGroup", Annotations: map[string]any{}}
	compositeTarget.EdgeSchema.To = &gen.Edge{Name: "to"}
	compositeTarget.EdgeSchema.ID = []*gen.Field{{Name: "user_id"}, {Name: "group_id"}}

	normalTarget := &gen.Type{Name: "Post", Annotations: map[string]any{}}

	edges := []*gen.Edge{
		{Name: "posts", Type: normalTarget},
		{Name: "user_groups", Type: compositeTarget},
	}
	result := g.filterEdges(edges, SkipType)
	require.Len(t, result, 1)
	assert.Equal(t, "posts", result[0].Name)
}

func TestFilterEdges_EmptySlice(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	result := g.filterEdges(nil, SkipType)
	assert.Empty(t, result)
}

func TestFilterEdges_BothEdgeAndTargetSkip(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	target := &gen.Type{
		Name: "Hidden",
		Annotations: map[string]any{
			AnnotationName: Annotation{Skip: SkipType},
		},
	}
	edges := []*gen.Edge{
		{
			Name: "hidden",
			Type: target,
			Annotations: map[string]any{
				AnnotationName: Annotation{Skip: SkipType},
			},
		},
	}
	result := g.filterEdges(edges, SkipType)
	assert.Empty(t, result)
}

// =============================================================================
// OrderTerm.VarName() additional case
// =============================================================================

func TestOrderTerm_VarName_SnakeCaseField(t *testing.T) {
	ot := &OrderTerm{
		Owner: &gen.Type{Name: "User"},
		Field: &gen.Field{Name: "created_at"},
	}
	assert.Equal(t, "UserOrderFieldCreatedAt", ot.VarName())
}

// =============================================================================
// OrderTerm -- none-of-above case
// =============================================================================

func TestOrderTerm_NoneOfAbove(t *testing.T) {
	ot := &OrderTerm{}
	assert.False(t, ot.IsFieldTerm())
	assert.False(t, ot.IsEdgeFieldTerm())
	assert.False(t, ot.IsEdgeCountTerm())
}
