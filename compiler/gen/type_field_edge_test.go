package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// Field type-check methods
// =============================================================================

func TestField_TypeChecks(t *testing.T) {
	tests := []struct {
		name     string
		typ      field.Type
		isBool   bool
		isBytes  bool
		isTime   bool
		isJSON   bool
		isOther  bool
		isString bool
		isUUID   bool
		isInt    bool
		isInt64  bool
		isEnum   bool
	}{
		{"bool", field.TypeBool, true, false, false, false, false, false, false, false, false, false},
		{"bytes", field.TypeBytes, false, true, false, false, false, false, false, false, false, false},
		{"time", field.TypeTime, false, false, true, false, false, false, false, false, false, false},
		{"json", field.TypeJSON, false, false, false, true, false, false, false, false, false, false},
		{"other", field.TypeOther, false, false, false, false, true, false, false, false, false, false},
		{"string", field.TypeString, false, false, false, false, false, true, false, false, false, false},
		{"uuid", field.TypeUUID, false, false, false, false, false, false, true, false, false, false},
		{"int", field.TypeInt, false, false, false, false, false, false, false, true, false, false},
		{"int64", field.TypeInt64, false, false, false, false, false, false, false, false, true, false},
		{"enum", field.TypeEnum, false, false, false, false, false, false, false, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.typ}}
			assert.Equal(t, tt.isBool, f.IsBool())
			assert.Equal(t, tt.isBytes, f.IsBytes())
			assert.Equal(t, tt.isTime, f.IsTime())
			assert.Equal(t, tt.isJSON, f.IsJSON())
			assert.Equal(t, tt.isOther, f.IsOther())
			assert.Equal(t, tt.isString, f.IsString())
			assert.Equal(t, tt.isUUID, f.IsUUID())
			assert.Equal(t, tt.isInt, f.IsInt())
			assert.Equal(t, tt.isInt64, f.IsInt64())
			assert.Equal(t, tt.isEnum, f.IsEnum())
		})
	}
}

func TestField_TypeChecks_NilType(t *testing.T) {
	f := Field{Type: nil}
	assert.False(t, f.IsBool())
	assert.False(t, f.IsBytes())
	assert.False(t, f.IsTime())
	assert.False(t, f.IsJSON())
	assert.False(t, f.IsOther())
	assert.False(t, f.IsString())
	assert.False(t, f.IsUUID())
	assert.False(t, f.IsInt())
	assert.False(t, f.IsInt64())
	assert.False(t, f.IsEnum())
}

// =============================================================================
// Field naming methods
// =============================================================================

func TestField_UpdateDefaultName(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"active", "UpdateDefaultActive"},
		{"expired_at", "UpdateDefaultExpiredAt"},
		{"group_name", "UpdateDefaultGroupName"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.UpdateDefaultName())
	}
}

func TestField_OrderName(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"email", "ByEmail"},
		{"created_at", "ByCreatedAt"},
		{"user_name", "ByUserName"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.OrderName())
	}
}

func TestField_OrderName_CountSuffix(t *testing.T) {
	// When a field ends with "Count" and there's a matching edge OrderCountName,
	// the field OrderName should get "Field" suffix.
	edgePosts := &Edge{Name: "posts", Rel: Relation{Type: O2M}}
	typ := &Type{
		Name:  "User",
		Edges: []*Edge{edgePosts},
	}
	// "posts_count" => pascal => "PostsCount" => "ByPostsCount"
	// Edge "posts" OrderCountName => "ByPostsCount" (matches)
	f := Field{Name: "posts_count", typ: typ}
	assert.Equal(t, "ByPostsCountField", f.OrderName())

	// A field that ends with "Count" but no matching edge.
	f2 := Field{Name: "login_count", typ: typ}
	assert.Equal(t, "ByLoginCount", f2.OrderName())
}

func TestField_OrderName_NilType(t *testing.T) {
	f := Field{Name: "posts_count", typ: nil}
	assert.Equal(t, "ByPostsCount", f.OrderName())
}

func TestField_StructField(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"email", "Email"},
		{"user_name", "UserName"},
		{"created_at", "CreatedAt"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.StructField())
	}
}

func TestField_BuilderField_NonEdge(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"active", "active"},
		{"type", "_type"},
		{"Name", "_Name"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.BuilderField())
	}
}

func TestField_Validator(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"email", "EmailValidator"},
		{"user_name", "UserNameValidator"},
		{"status", "StatusValidator"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.Validator())
	}
}

// =============================================================================
// Field mutation methods
// =============================================================================

func TestField_MutationGet(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"email", "Email"},
		{"user_name", "UserName"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.MutationGet())
	}
}

func TestField_MutationGet_ConflictingName(t *testing.T) {
	// "Op" is a method on the Mutation interface, so "op" field should get "Get" prefix.
	f := Field{Name: "op"}
	assert.Equal(t, "GetOp", f.MutationGet())
}

func TestField_MutationGet_SetID_UserDefined(t *testing.T) {
	// When field name is "set_id" => pascal => "SetID", which is in mutMethods.
	// If typ has UserDefined ID, the prefix "Get" is added.
	f := Field{
		Name: "set_id",
		typ: &Type{
			ID: &Field{UserDefined: true},
		},
	}
	assert.Equal(t, "GetSetID", f.MutationGet())
}

func TestField_MutationGetOld(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"email", "OldEmail"},
		{"user_name", "OldUserName"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.name}
		assert.Equal(t, tt.expected, f.MutationGetOld())
	}
}

func TestField_MutationReset(t *testing.T) {
	f := Field{Name: "email"}
	assert.Equal(t, "ResetEmail", f.MutationReset())
}

func TestField_MutationSet(t *testing.T) {
	f := Field{Name: "email"}
	assert.Equal(t, "SetEmail", f.MutationSet())
}

func TestField_MutationClear(t *testing.T) {
	f := Field{Name: "email"}
	assert.Equal(t, "ClearEmail", f.MutationClear())
}

func TestField_MutationCleared(t *testing.T) {
	f := Field{Name: "email"}
	assert.Equal(t, "EmailCleared", f.MutationCleared())
}

func TestField_MutationAdd(t *testing.T) {
	f := Field{Name: "age"}
	assert.Equal(t, "AddAge", f.MutationAdd())
}

func TestField_MutationAdded(t *testing.T) {
	f := Field{Name: "age"}
	assert.Equal(t, "AddedAge", f.MutationAdded())
}

func TestField_MutationAppend(t *testing.T) {
	f := Field{Name: "tags"}
	assert.Equal(t, "AppendTags", f.MutationAppend())
}

func TestField_MutationAppended(t *testing.T) {
	f := Field{Name: "tags"}
	assert.Equal(t, "AppendedTags", f.MutationAppended())
}

// =============================================================================
// Field enum methods
// =============================================================================

func TestField_EnumName_Variants(t *testing.T) {
	tests := []struct {
		fieldName string
		enumVal   string
		expected  string
	}{
		{"status", "active", "StatusActive"},
		{"status", "PENDING", "StatusPENDING"},
		{"type", "GIF", "TypeGIF"},
		{"role", "admin_user", "RoleAdminUser"},
	}
	for _, tt := range tests {
		f := Field{Name: tt.fieldName}
		assert.Equal(t, tt.expected, f.EnumName(tt.enumVal))
	}
}

func TestField_EnumTypeName(t *testing.T) {
	// With a parent type.
	f := Field{Name: "status", typ: &Type{Name: "User"}}
	assert.Equal(t, "UserStatus", f.EnumTypeName())

	// Without a parent type.
	f2 := Field{Name: "status", typ: nil}
	assert.Equal(t, "Status", f2.EnumTypeName())
}

// =============================================================================
// Field SupportsMutationAdd / ConvertedToBasic
// =============================================================================

func TestField_SupportsMutationAdd(t *testing.T) {
	// Numeric field without custom GoType supports add.
	f := Field{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}}
	assert.True(t, f.SupportsMutationAdd())

	// String field does not support add.
	f2 := Field{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}
	assert.False(t, f2.SupportsMutationAdd())

	// Edge field (FK) does not support add even if numeric.
	f3 := Field{
		Name: "owner_id",
		Type: &field.TypeInfo{Type: field.TypeInt},
		fk:   &ForeignKey{},
	}
	assert.False(t, f3.SupportsMutationAdd())
}

func TestField_ConvertedToBasic(t *testing.T) {
	// Field without custom GoType is always convertible.
	f := Field{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}}
	assert.True(t, f.ConvertedToBasic())

	// Nil type info.
	f2 := Field{Name: "age", Type: nil}
	assert.True(t, f2.ConvertedToBasic())
}

// =============================================================================
// Edge type-check methods (Rel-dependent)
// =============================================================================

func TestEdge_RelationTypes(t *testing.T) {
	tests := []struct {
		name string
		rel  Rel
		m2m  bool
		m2o  bool
		o2m  bool
		o2o  bool
	}{
		{"M2M", M2M, true, false, false, false},
		{"M2O", M2O, false, true, false, false},
		{"O2M", O2M, false, false, true, false},
		{"O2O", O2O, false, false, false, true},
		{"Unk", Unk, false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Edge{Rel: Relation{Type: tt.rel}}
			assert.Equal(t, tt.m2m, e.M2M())
			assert.Equal(t, tt.m2o, e.M2O())
			assert.Equal(t, tt.o2m, e.O2M())
			assert.Equal(t, tt.o2o, e.O2O())
		})
	}
}

// =============================================================================
// Edge naming/constant methods
// =============================================================================

func TestEdge_Label(t *testing.T) {
	owner := &Type{Name: "User"}
	// Assoc edge (not inverse): label = owner_edgename.
	e := Edge{Name: "posts", Owner: owner}
	assert.Equal(t, "user_posts", e.Label())

	// Inverse edge: label = owner_inverse.
	e2 := Edge{Name: "author", Inverse: "posts", Owner: owner}
	assert.Equal(t, "user_posts", e2.Label())
}

func TestEdge_Constant(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"posts", "EdgePosts"},
		{"owner", "EdgeOwner"},
		{"user_groups", "EdgeUserGroups"},
	}
	for _, tt := range tests {
		e := Edge{Name: tt.name}
		assert.Equal(t, tt.expected, e.Constant())
	}
}

func TestEdge_LabelConstant(t *testing.T) {
	// Non-inverse edge.
	e := Edge{Name: "posts"}
	assert.Equal(t, "PostsLabel", e.LabelConstant())

	// Inverse edge uses the inverse name.
	e2 := Edge{Name: "author", Inverse: "posts"}
	assert.Equal(t, "PostsLabel", e2.LabelConstant())
}

func TestEdge_InverseLabelConstant(t *testing.T) {
	e := Edge{Name: "users"}
	assert.Equal(t, "UsersInverseLabel", e.InverseLabelConstant())
}

func TestEdge_TableConstant(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "PostsTable", e.TableConstant())
}

func TestEdge_InverseTableConstant(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "PostsInverseTable", e.InverseTableConstant())
}

func TestEdge_ColumnConstant(t *testing.T) {
	e := Edge{Name: "owner"}
	assert.Equal(t, "OwnerColumn", e.ColumnConstant())
}

func TestEdge_PKConstant(t *testing.T) {
	e := Edge{Name: "groups"}
	assert.Equal(t, "GroupsPrimaryKey", e.PKConstant())
}

// =============================================================================
// Edge struct field methods
// =============================================================================

func TestEdge_BuilderField(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"posts", "posts"},
		{"type", "_type"},
		{"Owner", "_Owner"},
	}
	for _, tt := range tests {
		e := Edge{Name: tt.name}
		assert.Equal(t, tt.expected, e.BuilderField())
	}
}

func TestEdge_EagerLoadField(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "withPosts", e.EagerLoadField())

	e2 := Edge{Name: "user_groups"}
	assert.Equal(t, "withUserGroups", e2.EagerLoadField())
}

func TestEdge_EagerLoadNamedField(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "withNamedPosts", e.EagerLoadNamedField())
}

func TestEdge_StructField(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"posts", "Posts"},
		{"owner", "Owner"},
		{"user_groups", "UserGroups"},
	}
	for _, tt := range tests {
		e := Edge{Name: tt.name}
		assert.Equal(t, tt.expected, e.StructField())
	}
}

// =============================================================================
// Edge OwnFK / HasConstraint / IsInverse
// =============================================================================

func TestEdge_OwnFK(t *testing.T) {
	tests := []struct {
		name     string
		edge     Edge
		expected bool
	}{
		{"M2O owns FK", Edge{Rel: Relation{Type: M2O}}, true},
		{"O2O inverse owns FK", Edge{Inverse: "ref", Rel: Relation{Type: O2O}}, true},
		{"O2O bidi owns FK", Edge{Bidi: true, Rel: Relation{Type: O2O}}, true},
		{"O2O assoc does not own FK", Edge{Rel: Relation{Type: O2O}}, false},
		{"O2M does not own FK", Edge{Rel: Relation{Type: O2M}}, false},
		{"M2M does not own FK", Edge{Rel: Relation{Type: M2M}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.edge.OwnFK())
		})
	}
}

func TestEdge_HasConstraint(t *testing.T) {
	tests := []struct {
		name     string
		rel      Rel
		expected bool
	}{
		{"O2O has constraint", O2O, true},
		{"O2M has constraint", O2M, true},
		{"M2O no constraint", M2O, false},
		{"M2M no constraint", M2M, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Edge{Rel: Relation{Type: tt.rel}}
			assert.Equal(t, tt.expected, e.HasConstraint())
		})
	}
}

func TestEdge_IsInverse(t *testing.T) {
	assert.True(t, Edge{Inverse: "posts"}.IsInverse())
	assert.False(t, Edge{Inverse: ""}.IsInverse())
}

// =============================================================================
// Edge mutation methods
// =============================================================================

func TestEdge_MutationSet(t *testing.T) {
	e := Edge{Name: "owner"}
	assert.Equal(t, "SetOwnerID", e.MutationSet())
}

func TestEdge_MutationAdd(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "AddPostIDs", e.MutationAdd())

	// Singular edge name.
	e2 := Edge{Name: "child"}
	assert.Equal(t, "AddChildIDs", e2.MutationAdd())
}

func TestEdge_MutationReset(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "ResetPosts", e.MutationReset())
}

func TestEdge_MutationClear(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "ClearPosts", e.MutationClear())
}

func TestEdge_MutationRemove(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "RemovePostIDs", e.MutationRemove())

	e2 := Edge{Name: "child"}
	assert.Equal(t, "RemoveChildIDs", e2.MutationRemove())
}

func TestEdge_MutationCleared(t *testing.T) {
	e := Edge{Name: "posts"}
	assert.Equal(t, "PostsCleared", e.MutationCleared())
}

// =============================================================================
// Edge Comment / DefiningType
// =============================================================================

func TestEdge_Comment(t *testing.T) {
	// With def and comment.
	e := Edge{def: &load.Edge{Comment: "edge comment"}}
	assert.Equal(t, "edge comment", e.Comment())

	// Without def.
	e2 := Edge{def: nil}
	assert.Equal(t, "", e2.Comment())

	// With def but empty comment.
	e3 := Edge{def: &load.Edge{}}
	assert.Equal(t, "", e3.Comment())
}

func TestEdge_DefiningType(t *testing.T) {
	userType := &Type{Name: "User"}
	postType := &Type{Name: "Post"}

	// Assoc edge: defining type is the owner.
	e := Edge{Name: "posts", Owner: userType}
	assert.Equal(t, userType, e.DefiningType())

	// Inverse edge with Ref: defining type is Ref.Type.
	refEdge := &Edge{Name: "posts", Type: postType}
	e2 := Edge{Name: "author", Inverse: "posts", Owner: postType, Ref: refEdge}
	refEdge.Type = postType
	assert.Equal(t, postType, e2.DefiningType())
}

// =============================================================================
// Edge OrderCountName / OrderTermsName / OrderFieldName
// =============================================================================

func TestEdge_OrderCountName(t *testing.T) {
	// Non-unique edge returns a valid order count name.
	e := Edge{Name: "posts", Unique: false}
	name, err := e.OrderCountName()
	require.NoError(t, err)
	assert.Equal(t, "ByPostsCount", name)

	// Unique edge returns an error.
	e2 := Edge{Name: "owner", Unique: true}
	_, err = e2.OrderCountName()
	require.Error(t, err)
}

func TestEdge_OrderTermsName(t *testing.T) {
	e := Edge{Name: "posts", Unique: false}
	name, err := e.OrderTermsName()
	require.NoError(t, err)
	assert.Equal(t, "ByPosts", name)

	e2 := Edge{Name: "owner", Unique: true}
	_, err = e2.OrderTermsName()
	require.Error(t, err)
}

func TestEdge_OrderFieldName(t *testing.T) {
	e := Edge{Name: "owner", Unique: true}
	name, err := e.OrderFieldName()
	require.NoError(t, err)
	assert.Equal(t, "ByOwnerField", name)

	e2 := Edge{Name: "posts", Unique: false}
	_, err = e2.OrderFieldName()
	require.Error(t, err)
}

// =============================================================================
// Rel String method
// =============================================================================

func TestRel_String(t *testing.T) {
	tests := []struct {
		rel      Rel
		expected string
	}{
		{O2O, "O2O"},
		{O2M, "O2M"},
		{M2O, "M2O"},
		{M2M, "M2M"},
		{Unk, "Unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.rel.String())
	}
}

// =============================================================================
// Relation.Column
// =============================================================================

func TestRelation_Column(t *testing.T) {
	r := Relation{Columns: []string{"user_id", "group_id"}}
	assert.Equal(t, "user_id", r.Column())
}

func TestRelation_Column_EmptyColumns(t *testing.T) {
	r := Relation{Table: "users"}
	assert.Equal(t, "", r.Column())
}

// =============================================================================
// Field StorageKey
// =============================================================================

func TestField_StorageKey(t *testing.T) {
	// With explicit storage key.
	f := Field{Name: "userName", def: &load.Field{StorageKey: "user_name_col"}}
	assert.Equal(t, "user_name_col", f.StorageKey())

	// Without explicit storage key, uses snake_case of name.
	f2 := Field{Name: "userName"}
	assert.Equal(t, "user_name", f2.StorageKey())

	// Nil def falls back to snake_case.
	f3 := Field{Name: "createdAt", def: nil}
	assert.Equal(t, "created_at", f3.StorageKey())

	// def with empty StorageKey falls back to snake_case.
	f4 := Field{Name: "createdAt", def: &load.Field{}}
	assert.Equal(t, "created_at", f4.StorageKey())
}

// =============================================================================
// Field HasGoType
// =============================================================================

func TestField_HasGoType(t *testing.T) {
	// Has custom GoType.
	f := Field{Type: &field.TypeInfo{
		Type:  field.TypeString,
		RType: &field.RType{Kind: 24}, // reflect.String
	}}
	assert.True(t, f.HasGoType())

	// No RType.
	f2 := Field{Type: &field.TypeInfo{Type: field.TypeString}}
	assert.False(t, f2.HasGoType())

	// Nil Type.
	f3 := Field{Type: nil}
	assert.False(t, f3.HasGoType())
}

// =============================================================================
// Field IsEdgeField
// =============================================================================

func TestField_IsEdgeField(t *testing.T) {
	f := Field{fk: &ForeignKey{}}
	assert.True(t, f.IsEdgeField())

	f2 := Field{fk: nil}
	assert.False(t, f2.IsEdgeField())
}

// =============================================================================
// Field IsDeprecated / DeprecationReason / Sensitive / Comment
// =============================================================================

func TestField_IsDeprecated(t *testing.T) {
	f := Field{def: &load.Field{Deprecated: true}}
	assert.True(t, f.IsDeprecated())

	f2 := Field{def: &load.Field{Deprecated: false}}
	assert.False(t, f2.IsDeprecated())

	f3 := Field{def: nil}
	assert.False(t, f3.IsDeprecated())
}

func TestField_DeprecationReason(t *testing.T) {
	f := Field{def: &load.Field{DeprecatedReason: "use email_v2 instead"}}
	assert.Equal(t, "use email_v2 instead", f.DeprecationReason())

	f2 := Field{def: nil}
	assert.Equal(t, "", f2.DeprecationReason())
}

func TestField_Sensitive(t *testing.T) {
	f := Field{def: &load.Field{Sensitive: true}}
	assert.True(t, f.Sensitive())

	f2 := Field{def: nil}
	assert.False(t, f2.Sensitive())
}

func TestField_Comment(t *testing.T) {
	f := Field{def: &load.Field{Comment: "field comment"}}
	assert.Equal(t, "field comment", f.Comment())

	f2 := Field{def: nil}
	assert.Equal(t, "", f2.Comment())
}

// =============================================================================
// Field DefaultValue / DefaultFunc
// =============================================================================

func TestField_DefaultValue(t *testing.T) {
	f := Field{def: &load.Field{DefaultValue: "hello"}}
	assert.Equal(t, "hello", f.DefaultValue())

	// Empty default.
	f2 := Field{def: &load.Field{}}
	assert.Nil(t, f2.DefaultValue())
}

func TestField_DefaultFunc(t *testing.T) {
	// Func default kind.
	f := Field{def: &load.Field{DefaultKind: 19}} // reflect.Func = 19
	assert.True(t, f.DefaultFunc())

	// Non-func default kind.
	f2 := Field{def: &load.Field{DefaultKind: 24}} // reflect.String = 24
	assert.False(t, f2.DefaultFunc())
}

// =============================================================================
// Field EnumNames / EnumValues
// =============================================================================

func TestField_EnumNames(t *testing.T) {
	f := Field{Enums: []Enum{
		{Name: "StatusActive", Value: "active"},
		{Name: "StatusInactive", Value: "inactive"},
	}}
	assert.Equal(t, []string{"StatusActive", "StatusInactive"}, f.EnumNames())
}

func TestField_EnumValues(t *testing.T) {
	f := Field{Enums: []Enum{
		{Name: "StatusActive", Value: "active"},
		{Name: "StatusInactive", Value: "inactive"},
	}}
	assert.Equal(t, []string{"active", "inactive"}, f.EnumValues())
}

func TestField_EnumNames_Empty(t *testing.T) {
	f := Field{Enums: nil}
	assert.Empty(t, f.EnumNames())
	assert.Empty(t, f.EnumValues())
}

// =============================================================================
// Edge ForeignKey
// =============================================================================

func TestEdge_ForeignKey(t *testing.T) {
	fk := &ForeignKey{
		Field: &Field{Name: "owner_id"},
	}
	e := Edge{Name: "owner", Rel: Relation{Type: M2O, fk: fk}}
	result, err := e.ForeignKey()
	require.NoError(t, err)
	assert.Equal(t, fk, result)

	// No FK set.
	e2 := Edge{Name: "owner", Rel: Relation{Type: M2O}}
	_, err = e2.ForeignKey()
	require.Error(t, err)
}

// =============================================================================
// Edge Index
// =============================================================================

func TestEdge_Index(t *testing.T) {
	userType := &Type{Name: "User"}
	postsEdge := &Edge{Name: "posts", Owner: userType}
	friendsEdge := &Edge{Name: "friends", Owner: userType}
	userType.Edges = []*Edge{postsEdge, friendsEdge}

	idx, err := postsEdge.Index()
	require.NoError(t, err)
	assert.Equal(t, 0, idx)

	idx, err = friendsEdge.Index()
	require.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestEdge_Index_NotFound(t *testing.T) {
	userType := &Type{Name: "User"}
	userType.Edges = []*Edge{}
	orphanEdge := &Edge{Name: "orphan", Owner: userType}
	_, err := orphanEdge.Index()
	require.Error(t, err)
}

// =============================================================================
// ForeignKey StructField
// =============================================================================

func TestForeignKey_StructField(t *testing.T) {
	// User-defined FK.
	fk := ForeignKey{
		UserDefined: true,
		Field:       &Field{Name: "owner_id"},
	}
	assert.Equal(t, "OwnerID", fk.StructField())

	// Auto-generated FK.
	fk2 := ForeignKey{
		UserDefined: false,
		Field:       &Field{Name: "user_post"},
	}
	assert.Equal(t, "user_post", fk2.StructField())
}
