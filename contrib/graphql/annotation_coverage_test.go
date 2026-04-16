package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema"
)

// =============================================================================
// Functional Constructor Tests (all at 0% coverage)
// =============================================================================

func TestSkip_Constructor(t *testing.T) {
	t.Run("SingleMode", func(t *testing.T) {
		ann := Skip(SkipType)
		assert.Equal(t, SkipType, ann.Skip)
	})

	t.Run("MultipleModes", func(t *testing.T) {
		ann := Skip(SkipType, SkipWhereInput, SkipMutationCreateInput)
		assert.True(t, ann.IsSkipType())
		assert.True(t, ann.IsSkipWhereInput())
		assert.True(t, ann.IsSkipMutationCreateInput())
		assert.False(t, ann.IsSkipOrderField())
	})

	t.Run("SkipAll", func(t *testing.T) {
		ann := Skip(SkipAll)
		assert.True(t, ann.IsSkipType())
		assert.True(t, ann.IsSkipWhereInput())
		assert.True(t, ann.IsSkipOrderField())
		assert.True(t, ann.IsSkipMutationCreateInput())
		assert.True(t, ann.IsSkipMutationUpdateInput())
	})
}

func TestRelayConnection_Constructor(t *testing.T) {
	ann := RelayConnection()
	assert.True(t, ann.RelayConnection)
	assert.True(t, ann.HasRelayConnection())
}

func TestQueryField_Constructor(t *testing.T) {
	ann := QueryField()
	assert.True(t, ann.QueryField)
	assert.True(t, ann.HasQueryField())
}

func TestType_Constructor(t *testing.T) {
	ann := Type("Member")
	assert.Equal(t, "Member", ann.Type)
	assert.Equal(t, "Member", ann.GetType())
	assert.Equal(t, "Member", ann.GetTypeName()) // deprecated alias
}

func TestMutations_Constructor(t *testing.T) {
	t.Run("NoArgs_DefaultsToCreateAndUpdate", func(t *testing.T) {
		ann := Mutations()
		assert.True(t, ann.HasMutationsSet)
		assert.True(t, ann.Mutations.HasCreate())
		assert.True(t, ann.Mutations.HasUpdate())
	})

	t.Run("CreateOnly", func(t *testing.T) {
		ann := Mutations(MutationCreate())
		assert.True(t, ann.HasMutationsSet)
		assert.True(t, ann.Mutations.HasCreate())
		assert.False(t, ann.Mutations.HasUpdate())
	})

	t.Run("CreateAndUpdate", func(t *testing.T) {
		ann := Mutations(MutationCreate(), MutationUpdate())
		assert.True(t, ann.Mutations.HasCreate())
		assert.True(t, ann.Mutations.HasUpdate())
	})
}

func TestMultiOrder_Constructor(t *testing.T) {
	ann := MultiOrder()
	assert.True(t, ann.MultiOrder)
	assert.True(t, ann.HasMultiOrder())
}

func TestDirectives_Constructor(t *testing.T) {
	ann := Directives(
		Directive{Name: "cacheControl", Args: map[string]any{"maxAge": 300}},
		Directive{Name: "deprecated", Args: map[string]any{"reason": "Use NewUser"}},
	)
	assert.Len(t, ann.Directives, 2)
	assert.Equal(t, "cacheControl", ann.GetDirectives()[0].Name)
	assert.Equal(t, "deprecated", ann.GetDirectives()[1].Name)
}

func TestImplements_Constructor(t *testing.T) {
	ann := Implements("Auditable", "Timestamped")
	assert.Equal(t, []string{"Auditable", "Timestamped"}, ann.Implements)
	assert.Equal(t, []string{"Auditable", "Timestamped"}, ann.GetImplements())
}

func TestEnableWhereInputs_Constructor(t *testing.T) {
	t.Run("Enable", func(t *testing.T) {
		ann := EnableWhereInputs(true)
		require.NotNil(t, ann.WithWhereInputs)
		assert.True(t, *ann.WithWhereInputs)
	})

	t.Run("Disable", func(t *testing.T) {
		ann := EnableWhereInputs(false)
		require.NotNil(t, ann.WithWhereInputs)
		assert.False(t, *ann.WithWhereInputs)
	})
}

func TestEnableOrderField_Constructor(t *testing.T) {
	t.Run("Enable", func(t *testing.T) {
		ann := EnableOrderField(true)
		require.NotNil(t, ann.WithOrderField)
		assert.True(t, *ann.WithOrderField)
	})

	t.Run("Disable", func(t *testing.T) {
		ann := EnableOrderField(false)
		require.NotNil(t, ann.WithOrderField)
		assert.False(t, *ann.WithOrderField)
	})
}

func TestSkipField_Constructor(t *testing.T) {
	ann := SkipField()
	assert.True(t, ann.SkipField)
	assert.True(t, ann.IsSkipField())
}

func TestFieldName_Constructor(t *testing.T) {
	ann := FieldName("userId")
	assert.Equal(t, "userId", ann.FieldName)
	assert.Equal(t, "userId", ann.GetFieldName())
}

func TestOrderField_Constructor(t *testing.T) {
	ann := OrderField("EMAIL")
	assert.Equal(t, "EMAIL", ann.OrderField)
	assert.Equal(t, "EMAIL", ann.GetOrderField())
}

func TestWhereOps_Constructor(t *testing.T) {
	ann := WhereOps(OpsEquality | OpEqualFold)
	assert.True(t, ann.HasWhereOps)
	assert.Equal(t, OpsEquality|OpEqualFold, ann.WhereOps)
	assert.True(t, ann.HasWhereOpsSet())
	assert.Equal(t, OpsEquality|OpEqualFold, ann.GetWhereOps())
}

func TestFieldMutationOps_Constructor(t *testing.T) {
	t.Run("CreateOnly", func(t *testing.T) {
		ann := FieldMutationOps(IncludeCreate)
		assert.True(t, ann.HasFieldMutationOps)
		assert.Equal(t, IncludeCreate, ann.FieldMutationOps)
		assert.Equal(t, IncludeCreate, ann.GetFieldMutationOps())
		assert.True(t, ann.HasFieldMutationOpsSet())
	})

	t.Run("IncludeNone", func(t *testing.T) {
		ann := FieldMutationOps(IncludeNone)
		assert.True(t, ann.HasFieldMutationOps)
		assert.Equal(t, IncludeNone, ann.FieldMutationOps)
	})

	t.Run("IncludeBoth", func(t *testing.T) {
		ann := FieldMutationOps(IncludeBoth)
		assert.True(t, ann.FieldMutationOps.InCreate())
		assert.True(t, ann.FieldMutationOps.InUpdate())
	})
}

func TestCreateInputValidate_Constructor(t *testing.T) {
	ann := CreateInputValidate("required,email")
	assert.Equal(t, "required,email", ann.CreateInputValidateTag)
	assert.Equal(t, "required,email", ann.GetCreateInputValidateTag())
}

func TestUpdateInputValidate_Constructor(t *testing.T) {
	ann := UpdateInputValidate("omitempty,email")
	assert.Equal(t, "omitempty,email", ann.UpdateInputValidateTag)
	assert.Equal(t, "omitempty,email", ann.GetUpdateInputValidateTag())
}

func TestMutationInputValidate_Constructor(t *testing.T) {
	ann := MutationInputValidate("required,email", "omitempty,email")
	assert.Equal(t, "required,email", ann.GetCreateInputValidateTag())
	assert.Equal(t, "omitempty,email", ann.GetUpdateInputValidateTag())
}

func TestEnumValues_Constructor(t *testing.T) {
	mapping := map[string]string{
		"IN_PROGRESS": "inProgress",
		"COMPLETED":   "completed",
	}
	ann := EnumValues(mapping)
	assert.Equal(t, mapping, ann.EnumValues)
	assert.Equal(t, mapping, ann.GetEnumValues())
}

func TestEnumValue_Constructor(t *testing.T) {
	ann := EnumValue("pending", "PENDING")
	assert.Equal(t, map[string]string{"pending": "PENDING"}, ann.EnumValues)
}

func TestUnbind_Constructor(t *testing.T) {
	ann := Unbind()
	assert.True(t, ann.Unbind)
	assert.True(t, ann.IsUnbound())
}

func TestMapping_Constructor(t *testing.T) {
	ann := Mapping("authorPosts", "publishedPosts")
	assert.Equal(t, []string{"authorPosts", "publishedPosts"}, ann.Mapping)
	assert.Equal(t, []string{"authorPosts", "publishedPosts"}, ann.GetMapping())
}

func TestCollectedFor_Constructor(t *testing.T) {
	ann := CollectedFor("fullName", "displayName")
	assert.Equal(t, []string{"fullName", "displayName"}, ann.CollectedFor)
	assert.Equal(t, []string{"fullName", "displayName"}, ann.GetCollectedFor())
}

// =============================================================================
// Entity-Level Getter Tests
// =============================================================================

func TestAnnotation_SkipGetters(t *testing.T) {
	ann := Annotation{Skip: SkipOrderField | SkipMutationCreateInput | SkipMutationUpdateInput}

	assert.True(t, ann.IsSkipOrderField())
	assert.True(t, ann.IsSkipMutationCreateInput())
	assert.True(t, ann.IsSkipMutationUpdateInput())
	assert.False(t, ann.IsSkipType())
	assert.False(t, ann.IsSkipWhereInput())

	// Extended skip modes
	ann2 := Annotation{Skip: SkipMutationCreate | SkipMutationUpdate}
	assert.True(t, ann2.IsSkipMutationCreate())
	assert.True(t, ann2.IsSkipMutationUpdate())
}

func TestAnnotation_HasMutations(t *testing.T) {
	t.Run("NotSet", func(t *testing.T) {
		ann := Annotation{}
		assert.False(t, ann.HasMutations())
		assert.Equal(t, MutationType(0), ann.EnabledMutations())
	})

	t.Run("Set", func(t *testing.T) {
		ann := Mutations(MutationCreate())
		assert.True(t, ann.HasMutations())
		assert.True(t, ann.EnabledMutations().HasCreate())
		assert.False(t, ann.EnabledMutations().HasUpdate())
	})
}

func TestAnnotation_WantsWhereInputs(t *testing.T) {
	t.Run("DefaultTrue", func(t *testing.T) {
		ann := Annotation{}
		assert.True(t, ann.WantsWhereInputs())
	})

	t.Run("SkipWhereInput", func(t *testing.T) {
		ann := Annotation{Skip: SkipWhereInput}
		assert.False(t, ann.WantsWhereInputs())
	})

	t.Run("ExplicitEnable_OverridesSkip", func(t *testing.T) {
		enable := true
		ann := Annotation{Skip: SkipWhereInput, WithWhereInputs: &enable}
		assert.True(t, ann.WantsWhereInputs(), "explicit enable should override skip")
	})

	t.Run("ExplicitDisable", func(t *testing.T) {
		disable := false
		ann := Annotation{WithWhereInputs: &disable}
		assert.False(t, ann.WantsWhereInputs())
	})
}

func TestAnnotation_WantsOrderField(t *testing.T) {
	t.Run("DefaultTrue", func(t *testing.T) {
		ann := Annotation{}
		assert.True(t, ann.WantsOrderField())
	})

	t.Run("SkipOrderField", func(t *testing.T) {
		ann := Annotation{Skip: SkipOrderField}
		assert.False(t, ann.WantsOrderField())
	})

	t.Run("ExplicitEnable_OverridesSkip", func(t *testing.T) {
		enable := true
		ann := Annotation{Skip: SkipOrderField, WithOrderField: &enable}
		assert.True(t, ann.WantsOrderField(), "explicit enable should override skip")
	})

	t.Run("ExplicitDisable", func(t *testing.T) {
		disable := false
		ann := Annotation{WithOrderField: &disable}
		assert.False(t, ann.WantsOrderField())
	})
}

func TestAnnotation_GetMutationInputs(t *testing.T) {
	t.Run("Explicit", func(t *testing.T) {
		ann := Annotation{MutationInputs: []MutationConfig{{IsCreate: true, Description: "create user"}}}
		inputs := ann.GetMutationInputs()
		require.Len(t, inputs, 1)
		assert.True(t, inputs[0].IsCreate)
		assert.Equal(t, "create user", inputs[0].Description)
	})

	t.Run("DefaultBothInputs", func(t *testing.T) {
		ann := Annotation{}
		inputs := ann.GetMutationInputs()
		require.Len(t, inputs, 2)
		assert.True(t, inputs[0].IsCreate)
		assert.False(t, inputs[1].IsCreate)
	})

	t.Run("SkipCreateInput", func(t *testing.T) {
		ann := Annotation{Skip: SkipMutationCreateInput}
		inputs := ann.GetMutationInputs()
		require.Len(t, inputs, 1)
		assert.False(t, inputs[0].IsCreate)
	})

	t.Run("SkipUpdateInput", func(t *testing.T) {
		ann := Annotation{Skip: SkipMutationUpdateInput}
		inputs := ann.GetMutationInputs()
		require.Len(t, inputs, 1)
		assert.True(t, inputs[0].IsCreate)
	})

	t.Run("SkipBothInputs", func(t *testing.T) {
		ann := Annotation{Skip: SkipMutationCreateInput | SkipMutationUpdateInput}
		inputs := ann.GetMutationInputs()
		assert.Empty(t, inputs)
	})
}

func TestAnnotation_GetGraphQLEnumValue(t *testing.T) {
	t.Run("WithMapping", func(t *testing.T) {
		ann := Annotation{EnumValues: map[string]string{
			"IN_PROGRESS": "inProgress",
			"COMPLETED":   "completed",
		}}
		assert.Equal(t, "inProgress", ann.GetGraphQLEnumValue("IN_PROGRESS"))
		assert.Equal(t, "completed", ann.GetGraphQLEnumValue("COMPLETED"))
	})

	t.Run("NoMapping", func(t *testing.T) {
		ann := Annotation{EnumValues: map[string]string{
			"IN_PROGRESS": "inProgress",
		}}
		assert.Equal(t, "UNKNOWN", ann.GetGraphQLEnumValue("UNKNOWN"))
	})

	t.Run("NilMap", func(t *testing.T) {
		ann := Annotation{}
		assert.Equal(t, "active", ann.GetGraphQLEnumValue("active"))
	})
}

func TestAnnotation_InCreateInput(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		ann := Annotation{}
		assert.True(t, ann.InCreateInput())
	})

	t.Run("IncludeCreate", func(t *testing.T) {
		ann := FieldMutationOps(IncludeCreate)
		assert.True(t, ann.InCreateInput())
	})

	t.Run("ExcludeCreate", func(t *testing.T) {
		ann := FieldMutationOps(IncludeUpdate)
		assert.False(t, ann.InCreateInput())
	})

	t.Run("IncludeNone", func(t *testing.T) {
		ann := FieldMutationOps(IncludeNone)
		assert.False(t, ann.InCreateInput())
	})
}

func TestAnnotation_InUpdateInput(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		ann := Annotation{}
		assert.True(t, ann.InUpdateInput())
	})

	t.Run("IncludeUpdate", func(t *testing.T) {
		ann := FieldMutationOps(IncludeUpdate)
		assert.True(t, ann.InUpdateInput())
	})

	t.Run("ExcludeUpdate", func(t *testing.T) {
		ann := FieldMutationOps(IncludeCreate)
		assert.False(t, ann.InUpdateInput())
	})
}

// =============================================================================
// WhereOp Method Tests
// =============================================================================

func TestWhereOp_Has(t *testing.T) {
	ops := OpsEquality | OpGT
	assert.True(t, ops.Has(OpEQ))
	assert.True(t, ops.Has(OpNEQ))
	assert.True(t, ops.Has(OpGT))
	assert.False(t, ops.Has(OpGTE))
	assert.False(t, ops.Has(OpContains))
}

func TestWhereOp_IndividualChecks(t *testing.T) {
	all := OpsEquality | OpsComparison | OpsSubstring | OpsCaseFold | OpsNullable | OpsJSONArray

	assert.True(t, all.HasEQ())
	assert.True(t, all.HasNEQ())
	assert.True(t, all.HasIn())
	assert.True(t, all.HasNotIn())
	assert.True(t, all.HasGT())
	assert.True(t, all.HasGTE())
	assert.True(t, all.HasLT())
	assert.True(t, all.HasLTE())
	assert.True(t, all.HasContains())
	assert.True(t, all.HasHasPrefix())
	assert.True(t, all.HasHasSuffix())
	assert.True(t, all.HasEqualFold())
	assert.True(t, all.HasContainsFold())
	assert.True(t, all.HasIsNil())
	assert.True(t, all.HasNotNil())
	assert.True(t, all.HasHas())
	assert.True(t, all.HasHasSome())
	assert.True(t, all.HasHasEvery())
	assert.True(t, all.HasIsEmpty())

	none := OpsNone
	assert.False(t, none.HasEQ())
	assert.False(t, none.HasGT())
	assert.False(t, none.HasContains())
	assert.False(t, none.HasIsNil())
	assert.False(t, none.HasHas())
}

// =============================================================================
// MutationOp Method Tests
// =============================================================================

func TestMutationOp_Methods(t *testing.T) {
	assert.True(t, IncludeCreate.InCreate())
	assert.False(t, IncludeCreate.InUpdate())

	assert.False(t, IncludeUpdate.InCreate())
	assert.True(t, IncludeUpdate.InUpdate())

	assert.True(t, IncludeBoth.InCreate())
	assert.True(t, IncludeBoth.InUpdate())

	assert.False(t, IncludeNone.InCreate())
	assert.False(t, IncludeNone.InUpdate())
}

// =============================================================================
// MutationType Method Tests
// =============================================================================

func TestMutationType_HasMethods(t *testing.T) {
	m := mutCreate | mutUpdate

	assert.True(t, m.HasCreate())
	assert.True(t, m.HasUpdate())

	empty := MutationType(0)
	assert.False(t, empty.HasCreate())
	assert.False(t, empty.HasUpdate())
}

// =============================================================================
// SkipMode Tests
// =============================================================================

func TestSkipMode_Is(t *testing.T) {
	mode := SkipType | SkipWhereInput
	assert.True(t, mode.Is(SkipType))
	assert.True(t, mode.Is(SkipWhereInput))
	assert.False(t, mode.Is(SkipOrderField))
}

// =============================================================================
// Annotation Name and Merge Tests
// =============================================================================

func TestAnnotation_Name(t *testing.T) {
	ann := Annotation{}
	assert.Equal(t, AnnotationName, ann.Name())
	assert.Equal(t, "graphql", ann.Name())
}

func TestAnnotation_Merge_TypeMismatch(t *testing.T) {
	ann := Annotation{Type: "User"}
	// Merge with non-Annotation should return original
	result := ann.Merge(&extensionAnnotation{})
	assert.Equal(t, ann, result)
}

func TestMergeAnnotations_AllBranches(t *testing.T) {
	t.Run("Type", func(t *testing.T) {
		a := Annotation{Type: "Old"}
		b := Annotation{Type: "New"}
		result := MergeAnnotations(a, b)
		assert.Equal(t, "New", result.Type)
	})

	t.Run("SkipField", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{SkipField: true}
		result := MergeAnnotations(a, b)
		assert.True(t, result.SkipField)
	})

	t.Run("FieldName", func(t *testing.T) {
		a := Annotation{FieldName: "old"}
		b := Annotation{FieldName: "new"}
		result := MergeAnnotations(a, b)
		assert.Equal(t, "new", result.FieldName)
	})

	t.Run("OrderField", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{OrderField: "EMAIL"}
		result := MergeAnnotations(a, b)
		assert.Equal(t, "EMAIL", result.OrderField)
	})

	t.Run("WhereOps", func(t *testing.T) {
		a := Annotation{WhereOps: OpsEquality, HasWhereOps: true}
		b := Annotation{WhereOps: OpGT | OpLT, HasWhereOps: true}
		result := MergeAnnotations(a, b)
		assert.True(t, result.WhereOps.Has(OpEQ))
		assert.True(t, result.WhereOps.Has(OpGT))
		assert.True(t, result.WhereOps.Has(OpLT))
	})

	t.Run("FieldMutationOps", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{FieldMutationOps: IncludeCreate, HasFieldMutationOps: true}
		result := MergeAnnotations(a, b)
		assert.True(t, result.HasFieldMutationOps)
		assert.True(t, result.FieldMutationOps.InCreate())
	})

	t.Run("ValidateTags", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{CreateInputValidateTag: "required,email", UpdateInputValidateTag: "omitempty,email"}
		result := MergeAnnotations(a, b)
		assert.Equal(t, "required,email", result.CreateInputValidateTag)
		assert.Equal(t, "omitempty,email", result.UpdateInputValidateTag)
	})

	t.Run("EnumValues", func(t *testing.T) {
		a := Annotation{EnumValues: map[string]string{"a": "A"}}
		b := Annotation{EnumValues: map[string]string{"b": "B"}}
		result := MergeAnnotations(a, b)
		assert.Equal(t, "A", result.EnumValues["a"])
		assert.Equal(t, "B", result.EnumValues["b"])
	})

	t.Run("EnumValues_NilBase", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{EnumValues: map[string]string{"x": "X"}}
		result := MergeAnnotations(a, b)
		assert.Equal(t, "X", result.EnumValues["x"])
	})

	t.Run("Unbind", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{Unbind: true}
		result := MergeAnnotations(a, b)
		assert.True(t, result.Unbind)
	})

	t.Run("Mapping", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{Mapping: []string{"field1", "field2"}}
		result := MergeAnnotations(a, b)
		assert.Equal(t, []string{"field1", "field2"}, result.Mapping)
	})

	t.Run("CollectedFor", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{CollectedFor: []string{"fullName"}}
		result := MergeAnnotations(a, b)
		assert.Equal(t, []string{"fullName"}, result.CollectedFor)
	})

	t.Run("MultiOrder", func(t *testing.T) {
		a := Annotation{}
		b := Annotation{MultiOrder: true}
		result := MergeAnnotations(a, b)
		assert.True(t, result.MultiOrder)
	})

	t.Run("Directives_Append", func(t *testing.T) {
		a := Annotation{Directives: []Directive{{Name: "a"}}}
		b := Annotation{Directives: []Directive{{Name: "b"}}}
		result := MergeAnnotations(a, b)
		assert.Len(t, result.Directives, 2)
	})

	t.Run("Implements_Append", func(t *testing.T) {
		a := Annotation{Implements: []string{"A"}}
		b := Annotation{Implements: []string{"B"}}
		result := MergeAnnotations(a, b)
		assert.Equal(t, []string{"A", "B"}, result.Implements)
	})

	t.Run("WithWhereInputs", func(t *testing.T) {
		enable := true
		a := Annotation{}
		b := Annotation{WithWhereInputs: &enable}
		result := MergeAnnotations(a, b)
		require.NotNil(t, result.WithWhereInputs)
		assert.True(t, *result.WithWhereInputs)
	})

	t.Run("WithOrderField", func(t *testing.T) {
		disable := false
		a := Annotation{}
		b := Annotation{WithOrderField: &disable}
		result := MergeAnnotations(a, b)
		require.NotNil(t, result.WithOrderField)
		assert.False(t, *result.WithOrderField)
	})

	t.Run("Mutations_ORMerge", func(t *testing.T) {
		a := Mutations(MutationCreate())
		b := Mutations(MutationUpdate())
		result := MergeAnnotations(a, b)
		assert.True(t, result.HasMutationsSet)
		assert.True(t, result.Mutations.HasCreate())
		assert.True(t, result.Mutations.HasUpdate())
	})
}

// =============================================================================
// Annotation implements schema.Annotation
// =============================================================================

func TestAnnotation_ImplementsSchemaAnnotation(t *testing.T) {
	var _ schema.Annotation = Annotation{}
	var _ schema.Annotation = (*Annotation)(nil)
}

// =============================================================================
// ResolverMapping Tests
// =============================================================================

func TestResolverMapping_WithComment(t *testing.T) {
	rm := Map("glAccount", "PublicGlAccount!").WithComment("The GL account")
	assert.Equal(t, "glAccount", rm.FieldName)
	assert.Equal(t, "PublicGlAccount!", rm.ReturnType)
	assert.Equal(t, "The GL account", rm.Comment)
}

func TestResolverBaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"glAccount", "glAccount"},
		{"priceListItem(priceListId: ID!)", "priceListItem"},
		{"search(query: String!, limit: Int)", "search"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolverBaseName(tt.input))
		})
	}
}
