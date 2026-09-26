package graphqlgen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// SkipMode.Is() tests
// =============================================================================

func TestSkipMode_Is_SingleFlag(t *testing.T) {
	assert.True(t, graphql.SkipType.Is(graphql.SkipType))
	assert.True(t, graphql.SkipWhereInput.Is(graphql.SkipWhereInput))
	assert.True(t, graphql.SkipOrderField.Is(graphql.SkipOrderField))
	assert.True(t, graphql.SkipMutationCreateInput.Is(graphql.SkipMutationCreateInput))
	assert.True(t, graphql.SkipMutationUpdateInput.Is(graphql.SkipMutationUpdateInput))
}

func TestSkipMode_Is_NoMatch(t *testing.T) {
	assert.False(t, graphql.SkipType.Is(graphql.SkipWhereInput))
	assert.False(t, graphql.SkipOrderField.Is(graphql.SkipMutationCreateInput))
	assert.False(t, graphql.SkipMode(0).Is(graphql.SkipType))
}

func TestSkipMode_Is_CombinedFlags(t *testing.T) {
	combined := graphql.SkipType | graphql.SkipWhereInput
	assert.True(t, combined.Is(graphql.SkipType))
	assert.True(t, combined.Is(graphql.SkipWhereInput))
	assert.False(t, combined.Is(graphql.SkipOrderField))
	assert.False(t, combined.Is(graphql.SkipMutationCreateInput))
}

func TestSkipMode_Is_SkipAll_ContainsAllFlags(t *testing.T) {
	assert.True(t, graphql.SkipAll.Is(graphql.SkipType))
	assert.True(t, graphql.SkipAll.Is(graphql.SkipEnumField))
	assert.True(t, graphql.SkipAll.Is(graphql.SkipOrderField))
	assert.True(t, graphql.SkipAll.Is(graphql.SkipWhereInput))
	assert.True(t, graphql.SkipAll.Is(graphql.SkipMutationCreateInput))
	assert.True(t, graphql.SkipAll.Is(graphql.SkipMutationUpdateInput))
}

func TestSkipMode_Is_ZeroFlag(t *testing.T) {
	// Checking against zero flag always returns false (no bit set).
	assert.False(t, graphql.SkipAll.Is(0))
	assert.False(t, graphql.SkipMode(0).Is(0))
}

func TestSkipMode_Is_SkipInputs(t *testing.T) {
	assert.True(t, graphql.SkipInputs.Is(graphql.SkipMutationCreateInput))
	assert.True(t, graphql.SkipInputs.Is(graphql.SkipMutationUpdateInput))
	assert.False(t, graphql.SkipInputs.Is(graphql.SkipType))
	assert.False(t, graphql.SkipInputs.Is(graphql.SkipWhereInput))
}

func TestSkipMode_Is_SkipMutations(t *testing.T) {
	assert.True(t, graphql.SkipMutations.Is(graphql.SkipMutationCreate))
	assert.True(t, graphql.SkipMutations.Is(graphql.SkipMutationUpdate))
	assert.False(t, graphql.SkipMutations.Is(graphql.SkipType))
}

func TestSkipMode_Is_SkipEverything(t *testing.T) {
	// SkipEverything = SkipAll | SkipMutations
	assert.True(t, graphql.SkipEverything.Is(graphql.SkipType))
	assert.True(t, graphql.SkipEverything.Is(graphql.SkipMutationCreateInput))
	assert.True(t, graphql.SkipEverything.Is(graphql.SkipMutationUpdateInput))
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
	result := g.filterNodes(nodes, graphql.SkipType)
	assert.Len(t, result, 2)
}

func TestFilterNodes_SkipByAnnotation(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{Name: "User", Annotations: map[string]any{}},
		{
			Name: "Internal",
			Annotations: map[string]any{
				graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipType},
			},
		},
	}
	result := g.filterNodes(nodes, graphql.SkipType)
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
	result := g.filterNodes(nodes, graphql.SkipType)
	require.Len(t, result, 1)
	assert.Equal(t, "User", result[0].Name)
}

func TestFilterNodes_Empty(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	result := g.filterNodes(nil, graphql.SkipType)
	assert.Empty(t, result)
}

func TestFilterNodes_SkipWhereInput_DoesNotFilterType(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{
			Name: "User",
			Annotations: map[string]any{
				graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipWhereInput},
			},
		},
	}
	// Filtering for SkipType should NOT exclude a node with only SkipWhereInput
	result := g.filterNodes(nodes, graphql.SkipType)
	assert.Len(t, result, 1)
}

func TestFilterNodes_AllSkipped(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	nodes := []*gen.Type{
		{
			Name: "A",
			Annotations: map[string]any{
				graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipAll},
			},
		},
		{
			Name: "B",
			Annotations: map[string]any{
				graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipAll},
			},
		},
	}
	result := g.filterNodes(nodes, graphql.SkipType)
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
				graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipType},
			},
		},
	}
	// Even when filtering for SkipWhereInput, a field with SkipType should be excluded
	result := g.filterFields(fields, graphql.SkipWhereInput)
	require.Len(t, result, 1)
	assert.Equal(t, "visible", result[0].Name)
}

func TestFilterFields_EmptySlice(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	result := g.filterFields(nil, graphql.SkipType)
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
			graphql.AnnotationName: graphql.Annotation{Type: "Member"},
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
	result := g.filterEdges(edges)
	require.Len(t, result, 1)
	assert.Equal(t, "posts", result[0].Name)
}

func TestFilterEdges_EmptySlice(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	result := g.filterEdges(nil)
	assert.Empty(t, result)
}

func TestFilterEdges_BothEdgeAndTargetSkip(t *testing.T) {
	g := &Generator{graph: &gen.Graph{}, config: Config{}}
	target := &gen.Type{
		Name: "Hidden",
		Annotations: map[string]any{
			graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipType},
		},
	}
	edges := []*gen.Edge{
		{
			Name: "hidden",
			Type: target,
			Annotations: map[string]any{
				graphql.AnnotationName: graphql.Annotation{Skip: graphql.SkipType},
			},
		},
	}
	result := g.filterEdges(edges)
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
