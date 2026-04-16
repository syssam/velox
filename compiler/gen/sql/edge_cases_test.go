package sql

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// Edge Cases: JSON Fields
// =============================================================================

// TestMutation_JSONFieldAppend verifies that JSON fields generate AppendXxx
// methods in the mutation (for slice-type JSON fields).
func TestMutation_JSONFieldAppend(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		{
			Name: "tags",
			Type: &field.TypeInfo{Type: field.TypeJSON},
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()

	// JSON fields get AppendXxx
	assert.Contains(t, code, "AppendTags")
	assert.Contains(t, code, `m.appends["tags"] = v`)
}

// TestMutation_JSONFieldNonSliceNoAppend verifies non-JSON fields do NOT
// get AppendXxx methods.
func TestMutation_JSONFieldNonSliceNoAppend(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// Non-JSON fields should NOT get Append
	assert.NotContains(t, code, "AppendName")
	assert.NotContains(t, code, "AppendAge")
}

// =============================================================================
// Edge Cases: Optional AND Nillable Combined
// =============================================================================

// TestMutation_OptionalAndNillableFieldHasClear verifies that fields with
// both Optional AND Nillable generate ClearXxx in mutation (because Nillable
// means NULL is allowed in DB).
func TestMutation_OptionalAndNillableFieldHasClear(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		{
			Name:     "middle_name",
			Type:     &field.TypeInfo{Type: field.TypeString},
			Optional: true,
			Nillable: true,
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// Nillable enables ClearXxx in mutation
	assert.Contains(t, code, "ClearMiddleName")
}

// TestMutation_OptionalAndNillableFieldHasClearAndCleared verifies combined
// Optional+Nillable generates both ClearXxx and XxxCleared in mutation.
func TestMutation_OptionalAndNillableFieldHasClearAndCleared(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	profileType := createTestTypeWithFields("Profile", []*gen.Field{
		{
			Name:     "bio",
			Type:     &field.TypeInfo{Type: field.TypeString},
			Optional: true,
			Nillable: true,
		},
	})
	helper.graph.Nodes = []*gen.Type{profileType}

	file := genMutation(helper, profileType)
	code := file.GoString()

	assert.Contains(t, code, "ClearBio")
	assert.Contains(t, code, "BioCleared")
}

// =============================================================================
// Edge Cases: Named Edges in Entity Mode
// =============================================================================

// TestQuery_NamedEdgesEntityMode verifies WithNamed methods use LoadOption
// in entity mode (per-entity sub-packages).
func TestQuery_NamedEdgesEntityMode(t *testing.T) {
	t.Parallel()
	helper := newFeatureMockHelper()
	helper.withFeatures("namedges")
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	postType := createTestType("Post")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genQueryPkg(helper, userType, helper.graph.Nodes, "github.com/test/project/ent/entity")
	code := file.GoString()

	// Entity mode WithNamed uses LoadOption and WithEdgeLoad
	assert.Contains(t, code, "WithNamedPosts")
	assert.Contains(t, code, "runtime.LoadOption")
}

// =============================================================================
// Edge Cases: Delete with Various Entity Names
// =============================================================================

// TestDelete_EntityNameWithAcronym verifies Pascal case naming for entities
// with acronyms (URL, ID, API).
func TestDelete_EntityNameWithAcronym(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	apiKeyType := createTestType("APIKey")
	helper.graph.Nodes = []*gen.Type{apiKeyType}

	file := testGenDelete(helper, apiKeyType)
	require.NotNil(t, file)
	code := file.GoString()

	assert.Contains(t, code, "APIKeyDelete")
	assert.Contains(t, code, "APIKeyDeleteOne")
}

// =============================================================================
// Edge Cases: Query with Both O2O and O2M Edges
// =============================================================================

// TestQuery_MixedEdgeTypes verifies eager loading with a mix of unique
// and non-unique edges.
func TestQuery_MixedEdgeTypes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	postType := createTestType("Post")

	userType.Edges = []*gen.Edge{
		createO2OEdge("profile", profileType, "profiles", "user_id"),
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, profileType, postType}

	file := genQueryPkg(helper, userType, helper.graph.Nodes, "github.com/test/project/ent/entity")
	code := file.GoString()

	// Both edge types have With* methods
	assert.Contains(t, code, "WithProfile")
	assert.Contains(t, code, "WithPosts")
}

// =============================================================================
// Edge Cases: Mutation with Edge-only Fields (no regular fields)
// =============================================================================

// TestMutation_OnlyEdgesNoFields verifies mutation for entity with edges
// but no regular fields (e.g., a join table entity).
func TestMutation_OnlyEdgesNoFields(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	membershipType := createTestTypeWithFields("Membership", nil)
	userType := createTestType("User")
	groupType := createTestType("Group")

	membershipType.Edges = []*gen.Edge{
		createM2OEdge("user", userType, "memberships", "user_id"),
		createM2OEdge("group", groupType, "memberships", "group_id"),
	}
	helper.graph.Nodes = []*gen.Type{membershipType, userType, groupType}

	file := genMutation(helper, membershipType)
	require.NotNil(t, file)

	code := file.GoString()

	// Edge methods should exist
	assert.Contains(t, code, "SetUserID")
	assert.Contains(t, code, "SetGroupID")
	assert.Contains(t, code, "ClearUser")
	assert.Contains(t, code, "ClearGroup")
}

// =============================================================================
// Edge Cases: Edge Loader
// =============================================================================

// =============================================================================
// Comprehensive Parse Validation for Edge Cases
// =============================================================================

// TestEdgeCases_AllValidGo verifies all edge case generated code is valid Go.
func TestEdgeCases_AllValidGo(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()

	tests := []struct {
		name string
		gen  func() *jen.File
	}{
		{
			name: "json_field_mutation",
			gen: func() *jen.File {
				helper := newMockHelper()
				userType := createTestTypeWithFields("User", []*gen.Field{
					{Name: "tags", Type: &field.TypeInfo{Type: field.TypeJSON}},
					createTestField("name", field.TypeString),
				})
				helper.graph.Nodes = []*gen.Type{userType}
				return genMutation(helper, userType)
			},
		},
		{
			name: "edges_only_mutation",
			gen: func() *jen.File {
				helper := newMockHelper()
				mt := createTestTypeWithFields("Membership", nil)
				ut := createTestType("User")
				mt.Edges = []*gen.Edge{createM2OEdge("user", ut, "memberships", "user_id")}
				helper.graph.Nodes = []*gen.Type{mt, ut}
				return genMutation(helper, mt)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := tt.gen()
			require.NotNil(t, file)

			code := file.GoString()
			_, err := parser.ParseFile(fset, tt.name+".go", code, parser.AllErrors)
			assert.NoError(t, err, "generated code for %s should be valid Go:\n%s", tt.name, code)
		})
	}
}
