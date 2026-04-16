package graphql

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// graphqlTypeName Tests
// =============================================================================

func TestGenerator_GraphqlTypeName(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		gen := &Generator{}
		typ := &entgen.Type{Name: "User", Annotations: map[string]any{}}
		assert.Equal(t, "User", gen.graphqlTypeName(typ))
	})

	t.Run("CustomType", func(t *testing.T) {
		gen := &Generator{}
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{Type: "Member"},
			},
		}
		assert.Equal(t, "Member", gen.graphqlTypeName(typ))
	})

	t.Run("NilType", func(t *testing.T) {
		gen := &Generator{}
		assert.Equal(t, "", gen.graphqlTypeName(nil))
	})
}

// =============================================================================
// graphqlFieldName Tests
// =============================================================================

func TestGenerator_GraphqlFieldName(t *testing.T) {
	gen := &Generator{}

	t.Run("Default", func(t *testing.T) {
		f := &entgen.Field{Name: "user_name"}
		assert.Equal(t, "userName", gen.graphqlFieldName(f))
	})

	t.Run("CustomFieldName", func(t *testing.T) {
		f := &entgen.Field{
			Name: "user_name",
			Annotations: map[string]any{
				AnnotationName: &Annotation{FieldName: "name"},
			},
		}
		assert.Equal(t, "name", gen.graphqlFieldName(f))
	})
}

// =============================================================================
// graphqlFieldType Extended Tests
// =============================================================================

func TestGenerator_GraphqlFieldType_CustomAnnotation(t *testing.T) {
	gen := &Generator{}

	f := &entgen.Field{
		Name: "user_id",
		Type: &field.TypeInfo{Type: field.TypeString},
		Annotations: map[string]any{
			AnnotationName: &Annotation{Type: "ID"},
		},
	}
	assert.Equal(t, "ID", gen.graphqlFieldType(nil, f))
}

func TestGenerator_GraphqlFieldType_MapScalarFunc(t *testing.T) {
	gen := &Generator{
		config: Config{
			MapScalarFunc: func(t *entgen.Type, f *entgen.Field) string {
				if f.Name == "ip_address" {
					return "IPAddress"
				}
				return ""
			},
		},
	}

	t.Run("CustomScalar", func(t *testing.T) {
		f := &entgen.Field{Name: "ip_address", Type: &field.TypeInfo{Type: field.TypeString}}
		assert.Equal(t, "IPAddress", gen.graphqlFieldType(nil, f))
	})

	t.Run("DefaultFallback", func(t *testing.T) {
		f := &entgen.Field{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}
		assert.Equal(t, "String", gen.graphqlFieldType(nil, f))
	})
}

func TestGenerator_GraphqlFieldType_NilTypeInfo(t *testing.T) {
	gen := &Generator{}
	f := &entgen.Field{Name: "unknown"}
	assert.Equal(t, "String", gen.graphqlFieldType(nil, f))
}

func TestGenerator_GraphqlFieldType_Bytes(t *testing.T) {
	gen := &Generator{}
	f := &entgen.Field{Name: "data", Type: &field.TypeInfo{Type: field.TypeBytes}}
	assert.Equal(t, "Bytes", gen.graphqlFieldType(nil, f))
}

func TestGenerator_GraphqlFieldType_Enum(t *testing.T) {
	gen := &Generator{}

	t.Run("WithOwnerType", func(t *testing.T) {
		typ := &entgen.Type{Name: "Todo", Annotations: map[string]any{}}
		f := &entgen.Field{Name: "status", Type: &field.TypeInfo{Type: field.TypeEnum}}
		assert.Equal(t, "TodoStatus", gen.graphqlFieldType(typ, f))
	})

	t.Run("WithoutOwnerType", func(t *testing.T) {
		f := &entgen.Field{Name: "status", Type: &field.TypeInfo{Type: field.TypeEnum}}
		assert.Equal(t, "Status", gen.graphqlFieldType(nil, f))
	})
}

// =============================================================================
// graphqlInputFieldType Tests
// =============================================================================

func TestGenerator_GraphqlInputFieldType(t *testing.T) {
	gen := &Generator{enumNames: map[string]bool{}}

	t.Run("StandardType", func(t *testing.T) {
		f := &entgen.Field{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}
		assert.Equal(t, "String", gen.graphqlInputFieldType(nil, f))
	})

	t.Run("ValidInputCustomType", func(t *testing.T) {
		f := &entgen.Field{
			Name: "data",
			Type: &field.TypeInfo{Type: field.TypeJSON},
			Annotations: map[string]any{
				AnnotationName: &Annotation{Type: "JSON"},
			},
		}
		assert.Equal(t, "JSON", gen.graphqlInputFieldType(nil, f))
	})

	t.Run("ObjectTypeCustomAnnotation_FallsBackToJSON", func(t *testing.T) {
		f := &entgen.Field{
			Name: "address",
			Type: &field.TypeInfo{Type: field.TypeJSON},
			Annotations: map[string]any{
				AnnotationName: &Annotation{Type: "Address"},
			},
		}
		result := gen.graphqlInputFieldType(nil, f)
		assert.Equal(t, "JSON", result, "object types should fall back to JSON in input context")
	})
}

// =============================================================================
// isValidInputType Tests
// =============================================================================

func TestGenerator_IsValidInputType(t *testing.T) {
	gen := &Generator{enumNames: map[string]bool{"UserStatus": true}}

	tests := []struct {
		typeName string
		expected bool
	}{
		{"String", true},
		{"Int", true},
		{"Boolean", true},
		{"ID", true},
		{"Time", true},
		{"UUID", true},
		{"JSON", true},
		{"CreateUserInput", true},  // ends with "Input"
		{"ADMIN", true},            // all uppercase (enum)
		{"UserStatus", true},       // known enum
		{"Address", false},         // object type
		{"user", false},            // lowercase non-scalar
		{"[String!]", true},        // list of scalar
		{"[CustomObject!]", false}, // list of object
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			assert.Equal(t, tt.expected, gen.isValidInputType(tt.typeName))
		})
	}
}

// =============================================================================
// extractGraphQLAnnotation Tests
// =============================================================================

func TestExtractGraphQLAnnotation(t *testing.T) {
	t.Run("NilMap", func(t *testing.T) {
		ann := extractGraphQLAnnotation(nil)
		assert.Equal(t, Annotation{}, ann)
	})

	t.Run("NoAnnotation", func(t *testing.T) {
		ann := extractGraphQLAnnotation(map[string]any{})
		assert.Equal(t, Annotation{}, ann)
	})

	t.Run("DirectValue", func(t *testing.T) {
		ann := extractGraphQLAnnotation(map[string]any{
			AnnotationName: Annotation{Type: "Member"},
		})
		assert.Equal(t, "Member", ann.Type)
	})

	t.Run("PointerValue", func(t *testing.T) {
		ann := extractGraphQLAnnotation(map[string]any{
			AnnotationName: &Annotation{RelayConnection: true},
		})
		assert.True(t, ann.RelayConnection)
	})

	t.Run("NilPointer", func(t *testing.T) {
		ann := extractGraphQLAnnotation(map[string]any{
			AnnotationName: (*Annotation)(nil),
		})
		assert.Equal(t, Annotation{}, ann)
	})
}

// =============================================================================
// graphqlEnumValue Tests
// =============================================================================

func TestGenerator_GraphqlEnumValue(t *testing.T) {
	gen := &Generator{}

	t.Run("WithMapping", func(t *testing.T) {
		mapping := map[string]string{"active": "ACTIVE"}
		assert.Equal(t, "ACTIVE", gen.graphqlEnumValue("active", mapping))
	})

	t.Run("WithoutMapping", func(t *testing.T) {
		assert.Equal(t, "ACTIVE", gen.graphqlEnumValue("active", nil))
	})

	t.Run("MappingNotFound", func(t *testing.T) {
		mapping := map[string]string{"active": "ACTIVE"}
		assert.Equal(t, "PENDING", gen.graphqlEnumValue("pending", mapping))
	})
}

// =============================================================================
// genEnumType Tests
// =============================================================================

func TestGenerator_GenEnumType(t *testing.T) {
	gen := &Generator{
		graph:     &entgen.Graph{Nodes: []*entgen.Type{}},
		enumNames: map[string]bool{},
	}

	typ := &entgen.Type{
		Name:        "Todo",
		Annotations: map[string]any{},
	}
	f := &entgen.Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
		Enums: []entgen.Enum{
			{Name: "Active", Value: "active"},
			{Name: "Inactive", Value: "inactive"},
		},
	}

	result := gen.genEnumType(typ, f)

	assert.Contains(t, result, "enum TodoStatus")
	assert.Contains(t, result, "ACTIVE")
	assert.Contains(t, result, "INACTIVE")
}

func TestGenerator_GenEnumType_CustomEnumValues(t *testing.T) {
	gen := &Generator{
		graph:     &entgen.Graph{Nodes: []*entgen.Type{}},
		enumNames: map[string]bool{},
	}

	typ := &entgen.Type{
		Name:        "Todo",
		Annotations: map[string]any{},
	}
	f := &entgen.Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
		Enums: []entgen.Enum{
			{Name: "InProgress", Value: "in_progress"},
			{Name: "Completed", Value: "completed"},
		},
		Annotations: map[string]any{
			AnnotationName: &Annotation{
				EnumValues: map[string]string{
					"in_progress": "inProgress",
					"completed":   "done",
				},
			},
		},
	}

	result := gen.genEnumType(typ, f)

	assert.Contains(t, result, "inProgress")
	assert.Contains(t, result, "done")
	assert.NotContains(t, result, "IN_PROGRESS")
}

// =============================================================================
// genCreateInput / genUpdateInput SDL Tests
// =============================================================================

func TestGenerator_GenCreateInput_SDL(t *testing.T) {
	mutationAnnotation := map[string]any{
		AnnotationName: Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}

	typ := &entgen.Type{
		Name: "Todo",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "done", Type: &field.TypeInfo{Type: field.TypeBool}, Optional: true},
			{Name: "priority", Type: &field.TypeInfo{Type: field.TypeInt}},
		},
		Annotations: mutationAnnotation,
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:    "graphql",
		ORMPackage: "example/ent",
		Mutations:  true,
	})

	createInput := gen.genCreateInput(typ)

	assert.Contains(t, createInput, "input CreateTodoInput")
	assert.Contains(t, createInput, "title: String!")
	assert.Contains(t, createInput, "done: Boolean") // optional, no !
	assert.Contains(t, createInput, "priority: Int!")
}

func TestGenerator_GenUpdateInput_SDL(t *testing.T) {
	mutationAnnotation := map[string]any{
		AnnotationName: Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}

	typ := &entgen.Type{
		Name: "Todo",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "done", Type: &field.TypeInfo{Type: field.TypeBool}, Optional: true},
			{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}, Immutable: true},
			{Name: "bio", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
		},
		Annotations: mutationAnnotation,
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:    "graphql",
		ORMPackage: "example/ent",
		Mutations:  true,
	})

	updateInput := gen.genUpdateInput(typ)

	assert.Contains(t, updateInput, "input UpdateTodoInput")
	assert.Contains(t, updateInput, "title: String")     // all fields optional in update
	assert.NotContains(t, updateInput, "createdAt")      // immutable fields excluded
	assert.Contains(t, updateInput, "clearBio: Boolean") // nillable fields have clear
}

// =============================================================================
// genWhereInput Extended Tests
// =============================================================================

func TestGenerator_GenWhereInput_NillableField(t *testing.T) {
	typ := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "nickname", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{
			Package:  "example/ent",
			Features: []entgen.Feature{entgen.FeatureWhereInputAll},
		},
		Nodes: []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		WhereInputs: true,
	})

	whereInput := gen.genWhereInput(typ)

	assert.Contains(t, whereInput, "nicknameIsNil: Boolean")
	assert.Contains(t, whereInput, "nicknameNotNil: Boolean")
}

func TestGenerator_GenWhereInput_BoolField(t *testing.T) {
	typ := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "is_active", Type: &field.TypeInfo{Type: field.TypeBool}},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{
			Package:  "example/ent",
			Features: []entgen.Feature{entgen.FeatureWhereInputAll},
		},
		Nodes: []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		WhereInputs: true,
	})

	whereInput := gen.genWhereInput(typ)

	// Bool fields should have EQ/NEQ but not GT/GTE/LT/LTE
	assert.Contains(t, whereInput, "isActive: Boolean")
	assert.Contains(t, whereInput, "isActiveNEQ: Boolean")
	assert.NotContains(t, whereInput, "isActiveGT")
	assert.NotContains(t, whereInput, "isActiveContains")
}

// =============================================================================
// genMutationType Tests
// =============================================================================

func TestGenerator_GenMutationType(t *testing.T) {
	mutationAnnotation := map[string]any{
		AnnotationName: Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}

	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
		Annotations: mutationAnnotation,
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:   "graphql",
		Mutations: true,
		RelaySpec: true,
	})

	mutation := gen.genMutationType()

	assert.Contains(t, mutation, "type Mutation")
	assert.Contains(t, mutation, "createUser")
	assert.Contains(t, mutation, "updateUser")
	assert.NotContains(t, mutation, "deleteUser")
}

func TestGenerator_GenMutationType_CreateOnly(t *testing.T) {
	mutationAnnotation := map[string]any{
		AnnotationName: Annotation{
			Mutations:       mutCreate,
			HasMutationsSet: true,
		},
	}

	typ := &entgen.Type{
		Name:        "Event",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
		Annotations: mutationAnnotation,
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:   "graphql",
		Mutations: true,
		RelaySpec: true,
	})

	mutation := gen.genMutationType()

	assert.Contains(t, mutation, "createEvent")
	assert.NotContains(t, mutation, "updateEvent")
	assert.NotContains(t, mutation, "deleteEvent")
}

// =============================================================================
// validateResolverMappings Tests
// =============================================================================

func TestGenerator_ValidateResolverMappings(t *testing.T) {
	gen := &Generator{}

	t.Run("NoMappings", func(t *testing.T) {
		typ := &entgen.Type{
			Name:        "User",
			Annotations: map[string]any{},
		}
		assert.NoError(t, gen.validateResolverMappings(typ))
	})

	t.Run("ValidMappings", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Fields: []*entgen.Field{
				{Name: "name"},
			},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					ResolverMappings: []ResolverMapping{
						{FieldName: "customField", ReturnType: "String!"},
					},
				},
			},
		}
		assert.NoError(t, gen.validateResolverMappings(typ))
	})

	t.Run("EmptyFieldName", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					ResolverMappings: []ResolverMapping{
						{FieldName: "", ReturnType: "String!"},
					},
				},
			},
		}
		assert.Error(t, gen.validateResolverMappings(typ))
	})

	t.Run("DuplicateFieldName", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					ResolverMappings: []ResolverMapping{
						{FieldName: "field1", ReturnType: "String!"},
						{FieldName: "field1", ReturnType: "Int!"},
					},
				},
			},
		}
		assert.Error(t, gen.validateResolverMappings(typ))
	})
}

// =============================================================================
// genEntityType Tests
// =============================================================================

func TestGenerator_GenEntityType(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}, Optional: true},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package:   "graphql",
		RelaySpec: true,
	})

	result := gen.genEntityType(userType)

	assert.Contains(t, result, "type User implements Node")
	assert.Contains(t, result, "id: ID!")
	assert.Contains(t, result, "email: String!")
	assert.Contains(t, result, "age: Int") // optional, no !
}

func TestGenerator_GenEntityType_WithDirectives(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			AnnotationName: &Annotation{
				Directives: []Directive{
					{Name: "deprecated", Args: map[string]any{"reason": "Use Member"}},
				},
			},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package:   "graphql",
		RelaySpec: true,
	})

	result := gen.genEntityType(userType)
	assert.Contains(t, result, "@deprecated")
}

// =============================================================================
// splitPascal Tests
// =============================================================================

func TestSplitPascal(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"User", []string{"User"}},
		{"UserName", []string{"User", "Name"}},
		{"HTTPServer", []string{"HTTP", "Server"}},
		{"myField", []string{"my", "Field"}},
		{"ID", []string{"ID"}},
		{"userID", []string{"user", "ID"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := splitPascal(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// genScalarsSchema Tests
// =============================================================================

func TestGenerator_GenScalarsSchema(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package: "graphql",
	})

	scalars := gen.genScalarsSchema()
	assert.Contains(t, scalars, "scalar Time")
}

// =============================================================================
// getOrderFieldName Tests
// =============================================================================

func TestGenerator_GetOrderFieldName(t *testing.T) {
	gen := &Generator{}

	t.Run("DefaultUppercase", func(t *testing.T) {
		f := &entgen.Field{Name: "created_at"}
		assert.Equal(t, "CREATED_AT", gen.getOrderFieldName(f))
	})

	t.Run("CustomAnnotation", func(t *testing.T) {
		f := &entgen.Field{
			Name: "email",
			Annotations: map[string]any{
				AnnotationName: &Annotation{OrderField: "EMAIL_ADDRESS"},
			},
		}
		assert.Equal(t, "EMAIL_ADDRESS", gen.getOrderFieldName(f))
	})
}

// =============================================================================
// genOrderBy Extended Tests
// =============================================================================

func TestGenerator_GenOrderBy_MultiOrder(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			AnnotationName: &Annotation{MultiOrder: true},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package:  "graphql",
		Ordering: true,
	})

	orderBy := gen.genOrderBy(userType)
	assert.Contains(t, orderBy, "input UserOrder")
}

// =============================================================================
// fieldInCreateInput / fieldInUpdateInput Tests
// =============================================================================

func TestGenerator_FieldInCreateInput(t *testing.T) {
	gen := &Generator{}

	t.Run("NormalField", func(t *testing.T) {
		f := &entgen.Field{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}}
		assert.True(t, gen.fieldInCreateInput(f))
	})

	t.Run("SystemManagedTimeField", func(t *testing.T) {
		f := &entgen.Field{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}, Default: true}
		assert.False(t, gen.fieldInCreateInput(f))
	})

	t.Run("SystemManagedByUpdateDefault", func(t *testing.T) {
		f := &entgen.Field{Name: "updated_at", Type: &field.TypeInfo{Type: field.TypeTime}, UpdateDefault: true}
		assert.False(t, gen.fieldInCreateInput(f))
	})

	t.Run("ExplicitAnnotation_Overrides", func(t *testing.T) {
		f := &entgen.Field{
			Name:    "created_at",
			Type:    &field.TypeInfo{Type: field.TypeTime},
			Default: true,
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					FieldMutationOps:    IncludeCreate,
					HasFieldMutationOps: true,
				},
			},
		}
		assert.True(t, gen.fieldInCreateInput(f), "explicit annotation should override system field detection")
	})
}

func TestGenerator_FieldInUpdateInput(t *testing.T) {
	gen := &Generator{}

	t.Run("NormalField", func(t *testing.T) {
		f := &entgen.Field{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}}
		assert.True(t, gen.fieldInUpdateInput(f))
	})

	t.Run("UpdateDefaultField", func(t *testing.T) {
		f := &entgen.Field{Name: "modified_at", Type: &field.TypeInfo{Type: field.TypeTime}, UpdateDefault: true}
		assert.False(t, gen.fieldInUpdateInput(f))
	})

	t.Run("SystemManagedField", func(t *testing.T) {
		f := &entgen.Field{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}, Default: true}
		assert.False(t, gen.fieldInUpdateInput(f))
	})
}

// =============================================================================
// isSystemManagedField Tests
// =============================================================================

func TestGenerator_IsSystemManagedField(t *testing.T) {
	gen := &Generator{}

	tests := []struct {
		name     string
		field    *entgen.Field
		expected bool
	}{
		{"time with default (created_at pattern)", &entgen.Field{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}, Default: true}, true},
		{"time without default", &entgen.Field{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}}, false},
		{"UpdateDefault field", &entgen.Field{Name: "updated_at", Type: &field.TypeInfo{Type: field.TypeTime}, UpdateDefault: true}, true},
		{"non-time with UpdateDefault", &entgen.Field{Name: "version", Type: &field.TypeInfo{Type: field.TypeInt}, UpdateDefault: true}, true},
		{"time with default (any name)", &entgen.Field{Name: "published_at", Type: &field.TypeInfo{Type: field.TypeTime}, Default: true}, true},
		{"time without default (any name)", &entgen.Field{Name: "published_at", Type: &field.TypeInfo{Type: field.TypeTime}}, false},
		{"normal string field", &entgen.Field{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}}, false},
		{"string named created_at (not time)", &entgen.Field{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeString}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, gen.isSystemManagedField(tt.field))
		})
	}
}

// =============================================================================
// typeDirectives Tests
// =============================================================================

func TestGenerator_TypeDirectives(t *testing.T) {
	gen := &Generator{}

	t.Run("NoDirectives", func(t *testing.T) {
		typ := &entgen.Type{Name: "User", Annotations: map[string]any{}}
		assert.Equal(t, "", gen.typeDirectives(typ))
	})

	t.Run("SimpleDirective", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					Directives: []Directive{{Name: "deprecated"}},
				},
			},
		}
		result := gen.typeDirectives(typ)
		assert.Contains(t, result, "@deprecated")
	})

	t.Run("DirectiveWithArgs", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					Directives: []Directive{
						{Name: "cacheControl", Args: map[string]any{"maxAge": 300}},
					},
				},
			},
		}
		result := gen.typeDirectives(typ)
		assert.Contains(t, result, "@cacheControl")
		assert.Contains(t, result, "maxAge")
	})
}

// =============================================================================
// hasRelayConnection Tests
// =============================================================================

func TestGenerator_HasRelayConnection(t *testing.T) {
	t.Run("GloballyEnabled", func(t *testing.T) {
		typ := &entgen.Type{Name: "User", Annotations: map[string]any{}}
		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{typ},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelayConnection: true})
		assert.True(t, gen.hasRelayConnection(typ))
	})

	t.Run("GloballyDisabled", func(t *testing.T) {
		typ := &entgen.Type{Name: "User", Annotations: map[string]any{}}
		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{typ},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelayConnection: false})
		assert.False(t, gen.hasRelayConnection(typ))
	})

	t.Run("PerEntityAnnotation_WithGlobalEnabled", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{RelayConnection: true},
			},
		}
		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{typ},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelayConnection: true})
		assert.True(t, gen.hasRelayConnection(typ))
	})

	t.Run("QueryFieldOverridesRelayDefault", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{QueryField: true}, // QueryField without RelayConnection
			},
		}
		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{typ},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelayConnection: true})
		assert.False(t, gen.hasRelayConnection(typ), "QueryField without RelayConnection should use simple list")
	})
}

func TestGenerator_EntityPkgName(t *testing.T) {
	gen := &Generator{}
	typ := &entgen.Type{Name: "User"}
	assert.Equal(t, "user", gen.entityPkgName(typ))
}

func TestGenerator_EntityPkgPath(t *testing.T) {
	gen := &Generator{config: Config{ORMPackage: "example.com/ent"}}
	typ := &entgen.Type{Name: "User"}
	assert.Equal(t, "example.com/ent/user", gen.entityPkgPath(typ))
}

// =============================================================================
// genField Tests - edge and resolver fields
// =============================================================================

func TestGenerator_GenEdgeField(t *testing.T) {
	postType := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{postType},
	}

	gen := NewGenerator(g, Config{
		Package:   "graphql",
		RelaySpec: true,
	})

	t.Run("UniqueEdge", func(t *testing.T) {
		edge := &entgen.Edge{Name: "author", Type: postType, Unique: true}
		result := gen.genEdgeField(nil, edge)
		assert.Contains(t, result, "author: Post")
	})

	t.Run("NonUniqueEdge", func(t *testing.T) {
		edge := &entgen.Edge{Name: "posts", Type: postType, Unique: false}
		result := gen.genEdgeField(nil, edge)
		assert.Contains(t, result, "posts: [Post!]")
	})
}

// =============================================================================
// genConnectionsSchema Extended Tests
// =============================================================================

func TestGenerator_WriteConnectionEdgeTypes(t *testing.T) {
	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:         "graphql",
		RelayConnection: true,
	})

	var buf bytes.Buffer
	gen.writeConnectionEdgeTypes(&buf, "User")
	result := buf.String()

	assert.Contains(t, result, "type UserConnection")
	assert.Contains(t, result, "type UserEdge")
	assert.Contains(t, result, "totalCount: Int!")
	assert.Contains(t, result, "cursor: Cursor!")
}

// =============================================================================
// @goModel Path Tests
// =============================================================================

func TestGenerator_GoModelPaths_PointToActualPackages(t *testing.T) {
	g := mockGraph()
	gen := NewGenerator(g, Config{
		Package:         "graphql",
		RelayConnection: true,
		WhereInputs:     true,
		Mutations:       true,
		Ordering:        true,
		RelaySpec:       true,
	})

	schema := gen.genFullSchema()

	// Entity types → entity/ sub-package (where structs live)
	assert.Contains(t, schema, `@goModel(model: "example/ent/entity.User")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/entity.Post")`)

	// Connection/Edge → entity/ sub-package
	assert.Contains(t, schema, `@goModel(model: "example/ent/entity.UserConnection")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/entity.UserEdge")`)

	// Order/OrderField → entity/ sub-package
	assert.Contains(t, schema, `@goModel(model: "example/ent/entity.UserOrder")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/entity.UserOrderField")`)

	// WhereInput → gqlfilter/ sub-package
	assert.Contains(t, schema, `@goModel(model: "example/ent/gqlfilter.UserWhereInput")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/gqlfilter.PostWhereInput")`)

	// CreateInput/UpdateInput → entity sub-package (uses t.Name, not graphqlTypeName)
	assert.Contains(t, schema, `@goModel(model: "example/ent/user.CreateUserInput")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/user.UpdateUserInput")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/post.CreatePostInput")`)
	assert.Contains(t, schema, `@goModel(model: "example/ent/post.UpdatePostInput")`)

	// Cursor/PageInfo → gqlrelay library package
	assert.Contains(t, schema, `@goModel(model: "github.com/syssam/velox/contrib/graphql/gqlrelay.Cursor")`)
	assert.Contains(t, schema, `@goModel(model: "github.com/syssam/velox/contrib/graphql/gqlrelay.PageInfo")`)

	// Noder → root ORM package (defined in gql_node.go)
	assert.Contains(t, schema, `@goModel(model: "example/ent.Noder")`)

	// WhereInput → gqlfilter/, CreateInput → entity sub-package (NOT root)
	assert.NotContains(t, schema, `@goModel(model: "example/ent.UserWhereInput")`)
	assert.NotContains(t, schema, `@goModel(model: "example/ent.CreateUserInput")`)
	// PageInfo → gqlrelay library (NOT root)
	assert.NotContains(t, schema, `@goModel(model: "example/ent.PageInfo")`)
}

func TestGenerator_GoModelPaths_CustomGraphQLTypeName(t *testing.T) {
	// When graphql.Type("Member") is used on User schema, the SDL uses "Member"
	// but the Go struct is still "User" in the model/ package (for gqlgen autobind).
	typ := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			"graphql": Annotation{
				Type:            "Member",
				Mutations:       mutCreate | mutUpdate,
				HasMutationsSet: true,
			},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:         "graphql",
		RelayConnection: true,
		Mutations:       true,
		Ordering:        true,
	})

	// Entity type: SDL says "Member" but goModel points to entity sub-package (Go struct name)
	typesSchema := gen.genTypesSchema()
	assert.Contains(t, typesSchema, `type Member`)
	assert.Contains(t, typesSchema, `@goModel(model: "example/ent/entity.User")`)

	// CreateInput: SDL says "CreateMemberInput" but goModel points to user.CreateUserInput (Go struct name)
	createInput := gen.genCreateInput(typ)
	assert.Contains(t, createInput, `input CreateMemberInput @goModel(model: "example/ent/user.CreateUserInput")`)

	// UpdateInput: same pattern
	updateInput := gen.genUpdateInput(typ)
	assert.Contains(t, updateInput, `input UpdateMemberInput @goModel(model: "example/ent/user.UpdateUserInput")`)

	// Connection types use graphqlTypeName since Go structs are also named with it
	var buf bytes.Buffer
	gen.writeConnectionEdgeTypes(&buf, "Member")
	connSchema := buf.String()
	assert.Contains(t, connSchema, `@goModel(model: "example/ent/entity.MemberConnection")`)
	assert.Contains(t, connSchema, `@goModel(model: "example/ent/entity.MemberEdge")`)
}

// =============================================================================
// WithFilter Generation Tests
// =============================================================================

func TestGenerator_GenModelPagination_WithFilter(t *testing.T) {
	typ := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:         "graphql",
		RelayConnection: true,
		Ordering:        true,
	})

	f := gen.genModelPaginationTypes([]*entgen.Type{typ})
	var buf bytes.Buffer
	err := f.Render(&buf)
	assert.NoError(t, err)
	code := buf.String()

	// PagerConfig should have Filter field
	assert.Contains(t, code, "Filter any")

	// WithUserFilter function should be generated
	assert.Contains(t, code, "func WithUserFilter(filter any) UserPaginateOption")
	assert.Contains(t, code, "cfg.Filter = filter")
}

func TestGenerator_GenEntityPagination_FilterApplication(t *testing.T) {
	typ := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{typ},
	}

	gen := NewGenerator(g, Config{
		Package:         "graphql",
		RelayConnection: true,
		Ordering:        true,
	})

	f := gen.genEntityPagination(typ)
	var buf bytes.Buffer
	err := f.Render(&buf)
	assert.NoError(t, err)
	code := buf.String()

	// Filter should be applied with type assertion
	assert.Contains(t, code, "cfg.Filter != nil")
	assert.Contains(t, code, "cfg.Filter.(func(*UserQuery) (*UserQuery, error))")

	// Wrong filter type should return error (not silently ignored)
	assert.Contains(t, code, "invalid filter type")
	assert.Contains(t, code, "fmt.Errorf")
}

// =============================================================================
// Mutation Input JSON Tag Tests
// =============================================================================

func TestGenerator_MutationInput_JSONTags(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}, Optional: true, Nillable: true},
		},
		Annotations: map[string]any{
			"graphql": Annotation{
				Mutations:       mutCreate | mutUpdate,
				HasMutationsSet: true,
			},
		},
	}
	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{},
	}
	userType.Edges = []*entgen.Edge{
		{Name: "posts", Type: postType, Unique: false},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType},
	}

	gen := NewGenerator(g, Config{
		Package:   "graphql",
		Mutations: true,
	})

	f := gen.genEntityMutationInput(userType)
	var buf bytes.Buffer
	err := f.Render(&buf)
	assert.NoError(t, err)
	code := buf.String()

	// CreateInput: required fields have json tag without omitempty
	assert.Contains(t, code, "`json:\"name\"`")
	// CreateInput: optional fields have json tag with omitempty
	assert.Contains(t, code, "`json:\"age,omitempty\"`")
	// CreateInput: edge slice fields have json tag with omitempty
	assert.Contains(t, code, "`json:\"postIDs,omitempty\"`")

	// UpdateInput: all fields have json tag with omitempty
	assert.Contains(t, code, "`json:\"name,omitempty\"`")
	assert.Contains(t, code, "`json:\"clearAge,omitempty\"`")
	assert.Contains(t, code, "`json:\"addPostIDs,omitempty\"`")
	assert.Contains(t, code, "`json:\"removePostIDs,omitempty\"`")
}

// =============================================================================
// Validate Tag Emission Tests (Fix #2)
// =============================================================================

func TestGenerator_ValidateTagEmission(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name: "email",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{
						CreateInputValidateTag: "required,email",
						UpdateInputValidateTag: "omitempty,email",
					},
				},
			},
			{
				Name: "name",
				Type: &field.TypeInfo{Type: field.TypeString},
				// No validate tag
			},
		},
		Annotations: map[string]any{
			AnnotationName: Mutations(MutationCreate(), MutationUpdate()),
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}
	gen := NewGenerator(g, Config{
		Package:    "graphql",
		ORMPackage: "example/ent",
		Mutations:  true,
	})

	f := gen.genEntityMutationInput(userType)
	var buf bytes.Buffer
	err := f.Render(&buf)
	assert.NoError(t, err)
	code := buf.String()

	// Validate tag should be emitted for email in CreateInput
	assert.Contains(t, code, `validate:"required,email"`, "CreateInput should have validate tag for email")

	// Validate tag should be emitted for email in UpdateInput
	assert.Contains(t, code, `validate:"omitempty,email"`, "UpdateInput should have validate tag for email")

	// Name field should NOT have a validate tag
	assert.NotContains(t, code, `validate:""`, "Fields without validate annotation should not have empty validate tag")
}

// =============================================================================
// genNodeInterface with empty ORMPackage (Fix #3)
// =============================================================================

func TestGenerator_GenNodeInterface_EmptyORMPackage(t *testing.T) {
	g := &entgen.Graph{
		Config: &entgen.Config{Package: ""},
		Nodes:  []*entgen.Type{},
	}
	gen := NewGenerator(g, Config{Package: "graphql", ORMPackage: ""})

	result := gen.genNodeInterface()
	assert.Contains(t, result, "interface Node {", "empty ORMPackage should emit Node without @goModel")
	assert.NotContains(t, result, `@goModel(model: ".Noder")`, "should not emit invalid .Noder model path")
}

func TestGenerator_GenNodeInterface_WithORMPackage(t *testing.T) {
	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{},
	}
	gen := NewGenerator(g, Config{Package: "graphql", ORMPackage: "example/ent"})

	result := gen.genNodeInterface()
	assert.Contains(t, result, `@goModel(model: "example/ent.Noder")`)
}

// =============================================================================
// orderBy skipped with SkipOrderField (Fix #4)
// =============================================================================

func TestGenerator_GenEdgeField_SkipOrderField(t *testing.T) {
	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{
			AnnotationName: &Annotation{Skip: SkipOrderField},
		},
	}
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Edges: []*entgen.Edge{
			{Name: "posts", Type: postType},
		},
		Annotations: map[string]any{
			AnnotationName: &Annotation{RelayConnection: true},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType},
	}
	gen := NewGenerator(g, Config{
		Package:         "graphql",
		ORMPackage:      "example/ent",
		RelayConnection: true,
		Ordering:        true,
	})

	result := gen.genEdgeField(userType, userType.Edges[0])
	assert.NotContains(t, result, "orderBy", "edge to entity with SkipOrderField should not include orderBy arg")
}

func TestGenerator_GenEdgeField_WithOrderField(t *testing.T) {
	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{
			AnnotationName: &Annotation{RelayConnection: true},
		},
	}
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Edges: []*entgen.Edge{
			{Name: "posts", Type: postType},
		},
		Annotations: map[string]any{
			AnnotationName: &Annotation{RelayConnection: true},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType},
	}
	gen := NewGenerator(g, Config{
		Package:         "graphql",
		ORMPackage:      "example/ent",
		RelayConnection: true,
		Ordering:        true,
	})

	result := gen.genEdgeField(userType, userType.Edges[0])
	assert.Contains(t, result, "orderBy", "edge to entity without SkipOrderField should include orderBy arg")
}

// =============================================================================
// splitPascal UTF-8 safety (Fix #8)
// =============================================================================

func TestSplitPascal_UTF8(t *testing.T) {
	// Basic ASCII cases (regression)
	assert.Equal(t, []string{"User", "Name"}, splitPascal("UserName"))
	assert.Equal(t, []string{"HTTP", "Server"}, splitPascal("HTTPServer"))
	assert.Equal(t, []string{"ID"}, splitPascal("ID"))

	// UTF-8 runes should not cause panics or index-out-of-range errors
	assert.Equal(t, []string{"Ünder"}, splitPascal("Ünder"))
	// Non-ASCII uppercase is not treated as a word boundary (Go identifiers are ASCII)
	assert.Equal(t, []string{"FieldÑame"}, splitPascal("FieldÑame"))
}

// =============================================================================
// buildFieldTags Tests (Fix #2 helper)
// =============================================================================

func TestBuildFieldTags(t *testing.T) {
	t.Run("WithValidateTag", func(t *testing.T) {
		tags := buildFieldTags("name", "required")
		assert.Equal(t, "name", tags["json"])
		assert.Equal(t, "required", tags["validate"])
	})

	t.Run("WithoutValidateTag", func(t *testing.T) {
		tags := buildFieldTags("name,omitempty", "")
		assert.Equal(t, "name,omitempty", tags["json"])
		_, hasValidate := tags["validate"]
		assert.False(t, hasValidate, "empty validate tag should not be present")
	})
}

// =============================================================================
// samePackage detection tests
// =============================================================================

func TestNewGenerator_SamePackageDetection(t *testing.T) {
	tests := []struct {
		name        string
		ormPackage  string
		pkg         string
		outDir      string
		target      string
		wantSamePkg bool
	}{
		{
			name:        "full path match",
			ormPackage:  "example.com/app/velox",
			pkg:         "example.com/app/velox",
			wantSamePkg: true,
		},
		{
			name:        "full path mismatch",
			ormPackage:  "example.com/app/velox",
			pkg:         "example.com/other/velox",
			wantSamePkg: false,
		},
		{
			name:        "short name same outdir",
			ormPackage:  "example.com/app/velox",
			pkg:         "velox",
			outDir:      "./velox",
			target:      "./velox",
			wantSamePkg: true,
		},
		{
			name:        "short name different outdir",
			ormPackage:  "example.com/app/velox",
			pkg:         "velox",
			outDir:      "./graphql",
			target:      "./velox",
			wantSamePkg: false,
		},
		{
			name:        "short name empty outdir auto-inferred",
			ormPackage:  "example.com/app/velox",
			pkg:         "velox",
			outDir:      "",
			wantSamePkg: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGenerator(
				&entgen.Graph{Config: &entgen.Config{Target: tt.target}},
				Config{ORMPackage: tt.ormPackage, Package: tt.pkg, OutDir: tt.outDir},
			)
			assert.Equal(t, tt.wantSamePkg, g.samePackage)
		})
	}
}

// =============================================================================
// NodeDescriptor generation tests
// =============================================================================

func TestGenEntityNode_WithNodeDescriptor(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{},
	}

	// Without NodeDescriptor — no Node(ctx) method
	gen := newTestGeneratorWithConfig(Config{
		ORMPackage: "example.com/app/velox",
		Package:    "velox",
		RelaySpec:  true,
	}, userType)
	f := gen.genEntityNode(userType)
	code := f.GoString()
	assert.NotContains(t, code, "func (e *User) Node(")

	// With NodeDescriptor — generates Node(ctx) method
	gen2 := newTestGeneratorWithConfig(Config{
		ORMPackage:     "example.com/app/velox",
		Package:        "velox",
		RelaySpec:      true,
		NodeDescriptor: true,
	}, userType)
	f2 := gen2.genEntityNode(userType)
	code2 := f2.GoString()
	assert.Contains(t, code2, "func (e *User) Node(")
	assert.Contains(t, code2, "NodeDescriptor")
	assert.Contains(t, code2, `"name"`)
	assert.Contains(t, code2, `"email"`)
}

func TestGenNodeShared_WithNodeDescriptor(t *testing.T) {
	userType := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	// Without NodeDescriptor — no Client.Node method
	gen := newTestGeneratorWithConfig(Config{
		ORMPackage: "example.com/app/velox",
		Package:    "velox",
		RelaySpec:  true,
	}, userType)
	f := gen.genNodeShared()
	code := f.GoString()
	assert.NotContains(t, code, "func (c *Client) Node(")

	// With NodeDescriptor — generates Client.Node method
	gen2 := newTestGeneratorWithConfig(Config{
		ORMPackage:     "example.com/app/velox",
		Package:        "velox",
		RelaySpec:      true,
		NodeDescriptor: true,
	}, userType)
	f2 := gen2.genNodeShared()
	code2 := f2.GoString()
	assert.Contains(t, code2, "func (c *Client) Node(")
	assert.Contains(t, code2, "NodeDescriptor")
}

// TestNoderInjectsConfigIntoCtx pins that generated Noder/Noders call
// runtime.WithConfigContext(ctx, c.RuntimeConfig()) before iterating
// NodeResolvers — without this, every resolver closure cannot fetch
// entities and Noder always returns ErrNodeNotFound.
func TestNoderInjectsConfigIntoCtx(t *testing.T) {
	userType := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}
	gen := newTestGeneratorWithConfig(Config{
		ORMPackage: "example.com/app/velox",
		Package:    "velox",
		RelaySpec:  true,
	}, userType)

	code := gen.genNodeShared().GoString()

	want := "runtime.WithConfigContext(ctx, c.RuntimeConfig())"
	if !strings.Contains(code, want) {
		t.Errorf("genNodeShared output missing %q — Noder/Noders must inject Config into ctx", want)
	}
	// Both Noder and Noders must inject — count occurrences.
	if strings.Count(code, want) < 2 {
		t.Errorf("expected ctx-injection in BOTH Noder and Noders, found %d occurrences", strings.Count(code, want))
	}
}
