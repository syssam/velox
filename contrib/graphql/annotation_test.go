package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWhereInput_Constructor(t *testing.T) {
	ann := WhereInput()
	assert.True(t, ann.WhereInputEnabled, "WhereInput() should set WhereInputEnabled")
}

func TestWhereInputFields_Constructor(t *testing.T) {
	ann := WhereInputFields("status", "customer_id", "created_at")
	assert.Equal(t, []string{"status", "customer_id", "created_at"}, ann.WhereInputFieldNames)
}

func TestWhereInputEdges_Constructor(t *testing.T) {
	ann := WhereInputEdges("posts", "comments")
	assert.Equal(t, []string{"posts", "comments"}, ann.WhereInputEdgeNames)
}

func TestMergeAnnotations_WhereInputEnabled(t *testing.T) {
	a := Annotation{}
	b := Annotation{WhereInputEnabled: true}
	result := MergeAnnotations(a, b)
	assert.True(t, result.WhereInputEnabled, "WhereInputEnabled should OR-merge")
}

func TestMergeAnnotations_WhereInputFieldNames(t *testing.T) {
	a := WhereInputFields("status", "email")
	b := WhereInputFields("email", "age") // "email" is duplicate
	result := MergeAnnotations(a, b)
	assert.Equal(t, []string{"status", "email", "age"}, result.WhereInputFieldNames,
		"WhereInputFieldNames should append-deduplicate")
}

func TestMergeAnnotations_WhereInputEdgeNames(t *testing.T) {
	a := WhereInputEdges("posts")
	b := WhereInputEdges("posts", "comments") // "posts" is duplicate
	result := MergeAnnotations(a, b)
	assert.Equal(t, []string{"posts", "comments"}, result.WhereInputEdgeNames,
		"WhereInputEdgeNames should append-deduplicate")
}

func TestMerge_WhereInputEnabled(t *testing.T) {
	a := Annotation{}
	b := Annotation{WhereInputEnabled: true}
	result := a.Merge(b).(Annotation)
	assert.True(t, result.WhereInputEnabled, "Merge should OR-merge WhereInputEnabled")
}

func TestMerge_WhereInputFieldNames(t *testing.T) {
	a := WhereInputFields("status")
	b := WhereInputFields("status", "age")
	result := a.Merge(b).(Annotation)
	assert.Equal(t, []string{"status", "age"}, result.WhereInputFieldNames,
		"Merge should append-deduplicate WhereInputFieldNames")
}

func TestMergeAnnotations_MutationInputs(t *testing.T) {
	a := Annotation{}
	b := Annotation{MutationInputs: []MutationConfig{{IsCreate: true}}}
	result := MergeAnnotations(a, b)
	assert.Equal(t, []MutationConfig{{IsCreate: true}}, result.MutationInputs,
		"MergeAnnotations should carry MutationInputs")

	// Second annotation's MutationInputs should append (like Ent)
	c := Annotation{MutationInputs: []MutationConfig{{IsCreate: false, Description: "update"}}}
	result2 := MergeAnnotations(b, c)
	assert.Equal(t, []MutationConfig{
		{IsCreate: true},
		{IsCreate: false, Description: "update"},
	}, result2.MutationInputs,
		"MergeAnnotations should append MutationInputs (like Ent)")
}

func TestGetWhereOps(t *testing.T) {
	ann := Annotation{WhereOps: OpsEquality, HasWhereOps: true}
	assert.Equal(t, OpsEquality, ann.GetWhereOps())
	assert.True(t, ann.HasWhereOpsSet())

	// Unset returns zero value
	empty := Annotation{}
	assert.Equal(t, WhereOp(0), empty.GetWhereOps())
	assert.False(t, empty.HasWhereOpsSet())
}

func TestNewDirective_Constructor(t *testing.T) {
	d := NewDirective("cacheControl", map[string]any{"maxAge": 300})
	assert.Equal(t, "cacheControl", d.Name)
	assert.Equal(t, 300, d.Args["maxAge"])
}

func TestDeprecated_WithReason(t *testing.T) {
	d := Deprecated("Use Member instead")
	assert.Equal(t, "deprecated", d.Name)
	assert.Equal(t, "Use Member instead", d.Args["reason"])
}

func TestDeprecated_WithoutReason(t *testing.T) {
	d := Deprecated("")
	assert.Equal(t, "deprecated", d.Name)
	assert.Nil(t, d.Args)
}

// --- Task 1: Constructor tests ---

func TestMap_Constructor(t *testing.T) {
	rm := Map("glAccount", "PublicGlAccount!")
	assert.Equal(t, "glAccount", rm.FieldName)
	assert.Equal(t, "PublicGlAccount!", rm.ReturnType)
}

func TestMap_Nullable(t *testing.T) {
	rm := Map("approver", "PublicUser")
	assert.Equal(t, "approver", rm.FieldName)
	assert.Equal(t, "PublicUser", rm.ReturnType)
}

func TestResolvers_Constructor(t *testing.T) {
	ann := Resolvers(
		Map("glAccount", "PublicGlAccount!"),
		Map("approver", "PublicUser"),
	)
	assert.Len(t, ann.ResolverMappings, 2)
	assert.Equal(t, "glAccount", ann.ResolverMappings[0].FieldName)
	assert.Equal(t, "PublicUser", ann.ResolverMappings[1].ReturnType)
}

func TestOmittable_Constructor(t *testing.T) {
	ann := Omittable()
	assert.True(t, ann.Omittable)
}

// --- Task 2: Merge and getter tests ---

func TestMergeAnnotations_ResolverMappings(t *testing.T) {
	a := Resolvers(Map("glAccount", "PublicGlAccount!"))
	b := Resolvers(Map("approver", "PublicUser"))
	result := MergeAnnotations(a, b)
	assert.Len(t, result.ResolverMappings, 2)
	assert.Equal(t, "glAccount", result.ResolverMappings[0].FieldName)
	assert.Equal(t, "approver", result.ResolverMappings[1].FieldName)
}

func TestMergeAnnotations_ResolverMappings_DedupByFieldName(t *testing.T) {
	a := Resolvers(Map("glAccount", "OldType"))
	b := Resolvers(Map("glAccount", "NewType"))
	result := MergeAnnotations(a, b)
	assert.Len(t, result.ResolverMappings, 1)
	assert.Equal(t, "NewType", result.ResolverMappings[0].ReturnType,
		"last wins on duplicate FieldName")
}

func TestMergeAnnotations_Omittable(t *testing.T) {
	a := Annotation{}
	b := Annotation{Omittable: true}
	result := MergeAnnotations(a, b)
	assert.True(t, result.Omittable, "Omittable should OR-merge")
}

func TestMerge_ResolverMappings(t *testing.T) {
	a := Resolvers(Map("glAccount", "PublicGlAccount!"))
	b := Resolvers(Map("approver", "PublicUser"))
	result := a.Merge(b).(Annotation)
	assert.Len(t, result.ResolverMappings, 2)
}

func TestAnnotation_GetResolverMappings(t *testing.T) {
	ann := Resolvers(Map("glAccount", "PublicGlAccount!"), Map("memo", "String"))
	mappings := ann.GetResolverMappings()
	assert.Len(t, mappings, 2)
}

func TestAnnotation_IsOmittable(t *testing.T) {
	ann := Omittable()
	assert.True(t, ann.IsOmittable())
}

// --- Ent-compat constructors ---

func TestMapsTo(t *testing.T) {
	ann := MapsTo("subTasks", "assignedTasks")
	assert.Equal(t, []string{"subTasks", "assignedTasks"}, ann.Mapping)
	assert.True(t, ann.Unbind, "MapsTo should auto-set Unbind=true")
}

func TestSkip_NoArgs_DefaultsToSkipAll(t *testing.T) {
	ann := Skip()
	assert.Equal(t, SkipAll, ann.Skip, "Skip() with no args should default to SkipAll")
}

func TestQueryField_WithName(t *testing.T) {
	ann := QueryField("allUsers")
	assert.True(t, ann.QueryField)
	assert.Equal(t, "allUsers", ann.QueryFieldConfig.Name)
}

func TestQueryField_Description_Chaining(t *testing.T) {
	ann := QueryField("users").Description("List all users")
	assert.Equal(t, "List all users", ann.QueryFieldConfig.Description)
}

func TestQueryField_Directives_Chaining(t *testing.T) {
	ann := QueryField().Directives(Deprecated("use members"))
	assert.Len(t, ann.QueryFieldConfig.Directives, 1)
	assert.Equal(t, "deprecated", ann.QueryFieldConfig.Directives[0].Name)
}

func TestMutationCreate_Description(t *testing.T) {
	opt := MutationCreate().Description("Fields for creating a user")
	assert.True(t, opt.IsCreate())
	assert.Equal(t, "Fields for creating a user", opt.GetDescription())
}

func TestMutationUpdate_Description(t *testing.T) {
	opt := MutationUpdate().Description("Fields for updating a user")
	assert.False(t, opt.IsCreate())
	assert.Equal(t, "Fields for updating a user", opt.GetDescription())
}

func TestMutations_PopulatesMutationInputs(t *testing.T) {
	ann := Mutations(
		MutationCreate().Description("Create desc"),
		MutationUpdate().Description("Update desc"),
	)
	assert.Len(t, ann.MutationInputs, 2)
	assert.True(t, ann.MutationInputs[0].IsCreate)
	assert.Equal(t, "Create desc", ann.MutationInputs[0].Description)
	assert.False(t, ann.MutationInputs[1].IsCreate)
	assert.Equal(t, "Update desc", ann.MutationInputs[1].Description)
}

// --- Merge pointer cases ---

func TestMerge_AnnotationPointer(t *testing.T) {
	a := Annotation{Type: "User"}
	b := &Annotation{Type: "Member"}
	result := a.Merge(b).(Annotation)
	assert.Equal(t, "Member", result.Type)
}

func TestMerge_AnnotationNilPointer(t *testing.T) {
	a := Annotation{Type: "User"}
	var b *Annotation
	result := a.Merge(b).(Annotation)
	assert.Equal(t, "User", result.Type, "nil pointer should be no-op")
}

func TestMerge_QueryFieldAnnotationPointer(t *testing.T) {
	a := Annotation{}
	b := QueryField("users")
	result := a.Merge(&b).(Annotation)
	assert.True(t, result.QueryField)
	assert.Equal(t, "users", result.QueryFieldConfig.Name)
}

func TestMerge_QueryFieldAnnotationNilPointer(t *testing.T) {
	a := Annotation{Type: "User"}
	var b *QueryFieldAnnotation
	result := a.Merge(b).(Annotation)
	assert.Equal(t, "User", result.Type, "nil pointer should be no-op")
}
