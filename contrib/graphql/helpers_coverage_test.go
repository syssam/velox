package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// PaginationNames Tests
// =============================================================================

func TestPaginationNames(t *testing.T) {
	names := paginationNames("User")
	assert.Equal(t, "UserConnection", names.Connection)
	assert.Equal(t, "UserEdge", names.Edge)
	assert.Equal(t, "User", names.Node)
	assert.Equal(t, "UserOrder", names.Order)
	assert.Equal(t, "UserOrderField", names.OrderField)
	assert.Equal(t, "UserWhereInput", names.WhereInput)
}

// =============================================================================
// OrderTerm Tests
// =============================================================================

func TestOrderTerm_IsFieldTerm(t *testing.T) {
	// Field term: Field set, no Edge
	term := &OrderTerm{
		Field: &entgen.Field{Name: "name"},
	}
	assert.True(t, term.IsFieldTerm())
	assert.False(t, term.IsEdgeFieldTerm())
	assert.False(t, term.IsEdgeCountTerm())
}

func TestOrderTerm_IsEdgeFieldTerm(t *testing.T) {
	// Edge field term: both Field and Edge set
	term := &OrderTerm{
		Field: &entgen.Field{Name: "name"},
		Edge:  &entgen.Edge{Name: "posts"},
	}
	assert.False(t, term.IsFieldTerm())
	assert.True(t, term.IsEdgeFieldTerm())
	assert.False(t, term.IsEdgeCountTerm())
}

func TestOrderTerm_IsEdgeCountTerm(t *testing.T) {
	// Edge count term: Edge set with Count=true, no Field
	term := &OrderTerm{
		Edge:  &entgen.Edge{Name: "posts"},
		Count: true,
	}
	assert.False(t, term.IsFieldTerm())
	assert.False(t, term.IsEdgeFieldTerm())
	assert.True(t, term.IsEdgeCountTerm())
}

func TestOrderTerm_VarName(t *testing.T) {
	owner := &entgen.Type{Name: "User"}

	t.Run("FieldTerm", func(t *testing.T) {
		term := &OrderTerm{
			Owner: owner,
			Field: &entgen.Field{Name: "name"},
		}
		assert.Equal(t, "UserOrderFieldName", term.VarName())
	})

	t.Run("EdgeFieldTerm", func(t *testing.T) {
		term := &OrderTerm{
			Owner: owner,
			Field: &entgen.Field{Name: "title"},
			Edge:  &entgen.Edge{Name: "posts"},
		}
		assert.Equal(t, "UserOrderFieldPostsTitle", term.VarName())
	})

	t.Run("EdgeCountTerm", func(t *testing.T) {
		term := &OrderTerm{
			Owner: owner,
			Edge:  &entgen.Edge{Name: "posts"},
			Count: true,
		}
		assert.Equal(t, "UserOrderFieldPostsCount", term.VarName())
	})

	t.Run("DefaultTerm", func(t *testing.T) {
		term := &OrderTerm{
			Owner: owner,
		}
		assert.Equal(t, "UserOrderField", term.VarName())
	})
}

func TestOrderTerm_VarField(t *testing.T) {
	t.Run("FieldTerm", func(t *testing.T) {
		term := &OrderTerm{
			Type:  &entgen.Type{Name: "User"},
			Field: &entgen.Field{Name: "name"},
		}
		assert.Equal(t, "user.FieldName", term.VarField())
	})

	t.Run("EdgeFieldTerm", func(t *testing.T) {
		term := &OrderTerm{
			GQL:   "posts_title",
			Type:  &entgen.Type{Name: "Post"},
			Field: &entgen.Field{Name: "title"},
			Edge:  &entgen.Edge{Name: "posts"},
		}
		assert.Equal(t, `"posts_title"`, term.VarField())
	})

	t.Run("EdgeCountTerm", func(t *testing.T) {
		term := &OrderTerm{
			GQL:   "posts_count",
			Type:  &entgen.Type{Name: "Post"},
			Edge:  &entgen.Edge{Name: "posts"},
			Count: true,
		}
		assert.Equal(t, `"posts_count"`, term.VarField())
	})

	t.Run("DefaultTerm", func(t *testing.T) {
		term := &OrderTerm{}
		assert.Equal(t, "", term.VarField())
	})
}

// =============================================================================
// MutationDescriptor Tests
// =============================================================================

func TestMutationDescriptor_Input(t *testing.T) {
	g := &Generator{}
	typ := &entgen.Type{Name: "User", Annotations: map[string]any{}}

	t.Run("Create", func(t *testing.T) {
		md := &MutationDescriptor{Type: typ, IsCreate: true}
		assert.Equal(t, "CreateUserInput", md.Input(g))
	})

	t.Run("Update", func(t *testing.T) {
		md := &MutationDescriptor{Type: typ, IsCreate: false}
		assert.Equal(t, "UpdateUserInput", md.Input(g))
	})
}

func TestMutationDescriptor_Builders(t *testing.T) {
	typ := &entgen.Type{Name: "User", Annotations: map[string]any{}}

	t.Run("Create", func(t *testing.T) {
		md := &MutationDescriptor{Type: typ, IsCreate: true}
		assert.Equal(t, []string{"UserCreate"}, md.Builders())
	})

	t.Run("Update", func(t *testing.T) {
		md := &MutationDescriptor{Type: typ, IsCreate: false}
		assert.Equal(t, []string{"UserUpdate", "UserUpdateOne"}, md.Builders())
	})
}

// =============================================================================
// InputFieldDescriptor Tests
// =============================================================================

func TestInputFieldDescriptor_IsPointer(t *testing.T) {
	t.Run("NullableNonNillable", func(t *testing.T) {
		fd := &InputFieldDescriptor{
			Field:    &entgen.Field{Type: &field.TypeInfo{Type: field.TypeString}},
			Nullable: true,
		}
		assert.True(t, fd.IsPointer())
	})

	t.Run("NillableNotPointer", func(t *testing.T) {
		fd := &InputFieldDescriptor{
			Field:    &entgen.Field{Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
			Nullable: true,
		}
		assert.False(t, fd.IsPointer(), "Nillable fields are already pointers")
	})

	t.Run("NotNullable", func(t *testing.T) {
		fd := &InputFieldDescriptor{
			Field:    &entgen.Field{Type: &field.TypeInfo{Type: field.TypeString}},
			Nullable: false,
		}
		assert.False(t, fd.IsPointer())
	})
}

// =============================================================================
// MutationDescriptor InputFields/InputEdges Tests
// =============================================================================

func TestMutationDescriptor_InputFields(t *testing.T) {
	g := &Generator{}

	t.Run("UpdateInput_SkipsImmutableFields", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Fields: []*entgen.Field{
				{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "slug", Type: &field.TypeInfo{Type: field.TypeString}, Immutable: true},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: false}
		fields := md.InputFields(g)
		assert.Len(t, fields, 1)
		assert.Equal(t, "title", fields[0].Name)
		// Update fields should be nullable (optional)
		assert.True(t, fields[0].Nullable)
	})

	t.Run("CreateInput_NillableFieldHasClearOp", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Fields: []*entgen.Field{
				{Name: "bio", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
			},
			Annotations: map[string]any{},
		}
		// In update, nillable fields should have ClearOp
		md := &MutationDescriptor{Type: typ, IsCreate: false}
		fields := md.InputFields(g)
		assert.Len(t, fields, 1)
		assert.True(t, fields[0].ClearOp)
	})

	t.Run("CreateInput_OptionalFieldNullable", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Fields: []*entgen.Field{
				{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}, Optional: true},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: true}
		fields := md.InputFields(g)
		assert.True(t, fields[0].Nullable, "optional field should be nullable in create")
	})

	t.Run("CreateInput_DefaultFieldNullable", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Fields: []*entgen.Field{
				{Name: "status", Type: &field.TypeInfo{Type: field.TypeString}, Default: true},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: true}
		fields := md.InputFields(g)
		assert.True(t, fields[0].Nullable, "field with default should be nullable in create")
	})

	t.Run("SkipAnnotations", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Fields: []*entgen.Field{
				{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
				{
					Name: "hidden",
					Type: &field.TypeInfo{Type: field.TypeString},
					Annotations: map[string]any{
						AnnotationName: &Annotation{Skip: SkipMutationCreateInput},
					},
				},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: true}
		fields := md.InputFields(g)
		assert.Len(t, fields, 1)
		assert.Equal(t, "title", fields[0].Name)
	})
}

func TestMutationDescriptor_InputEdges(t *testing.T) {
	g := &Generator{}

	t.Run("CreateInput_AllEdges", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Edges: []*entgen.Edge{
				{Name: "tags", Type: &entgen.Type{Name: "Tag"}},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: true}
		edges := md.InputEdges(g)
		assert.Len(t, edges, 1)
	})

	t.Run("UpdateInput_SkipsImmutableEdges", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Edges: []*entgen.Edge{
				{Name: "tags", Type: &entgen.Type{Name: "Tag"}},
				{Name: "author", Type: &entgen.Type{Name: "User"}, Immutable: true},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: false}
		edges := md.InputEdges(g)
		assert.Len(t, edges, 1)
		assert.Equal(t, "tags", edges[0].Name)
	})

	t.Run("SkipAnnotations", func(t *testing.T) {
		typ := &entgen.Type{
			Name: "Post",
			Edges: []*entgen.Edge{
				{Name: "tags", Type: &entgen.Type{Name: "Tag"}},
				{
					Name: "internal",
					Type: &entgen.Type{Name: "Internal"},
					Annotations: map[string]any{
						AnnotationName: &Annotation{Skip: SkipMutationUpdateInput},
					},
				},
			},
			Annotations: map[string]any{},
		}
		md := &MutationDescriptor{Type: typ, IsCreate: false}
		edges := md.InputEdges(g)
		assert.Len(t, edges, 1)
		assert.Equal(t, "tags", edges[0].Name)
	})
}

// =============================================================================
// IDType Tests
// =============================================================================

func TestIDType(t *testing.T) {
	id := IDType{Type: "int64", Mixed: false}
	assert.Equal(t, "int64", id.Type)
	assert.False(t, id.Mixed)

	mixed := IDType{Mixed: true}
	assert.True(t, mixed.Mixed)
}

// =============================================================================
// appendUnique Tests
// =============================================================================

func TestAppendUnique(t *testing.T) {
	t.Run("NoDuplicates", func(t *testing.T) {
		result := appendUnique([]string{"a", "b"}, "c", "d")
		assert.Equal(t, []string{"a", "b", "c", "d"}, result)
	})

	t.Run("WithDuplicates", func(t *testing.T) {
		result := appendUnique([]string{"a", "b"}, "b", "c")
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("EmptyBase", func(t *testing.T) {
		result := appendUnique(nil, "a", "b")
		assert.Equal(t, []string{"a", "b"}, result)
	})

	t.Run("EmptyItems", func(t *testing.T) {
		result := appendUnique([]string{"a"})
		assert.Equal(t, []string{"a"}, result)
	})
}

// =============================================================================
// Generator Filter Function Tests
// =============================================================================

func TestGenerator_FilterEdges(t *testing.T) {
	userType := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}
	postType := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType},
	}
	gen := NewGenerator(g, Config{Package: "graphql"})

	t.Run("NilTypeEdgeSkipped", func(t *testing.T) {
		edges := []*entgen.Edge{
			{Name: "posts", Type: postType},
			{Name: "broken", Type: nil}, // nil type
		}
		result := gen.filterEdges(edges, SkipType)
		assert.Len(t, result, 1)
		assert.Equal(t, "posts", result[0].Name)
	})

	t.Run("SkippedEdgeAnnotation", func(t *testing.T) {
		edges := []*entgen.Edge{
			{Name: "posts", Type: postType},
			{
				Name: "hidden",
				Type: userType,
				Annotations: map[string]any{
					AnnotationName: &Annotation{Skip: SkipType},
				},
			},
		}
		result := gen.filterEdges(edges, SkipType)
		assert.Len(t, result, 1)
		assert.Equal(t, "posts", result[0].Name)
	})

	t.Run("SkippedTargetType", func(t *testing.T) {
		hiddenType := &entgen.Type{
			Name: "Hidden",
			ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
			Annotations: map[string]any{
				AnnotationName: &Annotation{Skip: SkipType},
			},
		}
		edges := []*entgen.Edge{
			{Name: "posts", Type: postType},
			{Name: "hidden", Type: hiddenType},
		}
		result := gen.filterEdges(edges, SkipType)
		assert.Len(t, result, 1)
	})
}

func TestGenerator_FilterFields(t *testing.T) {
	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{},
	}
	gen := NewGenerator(g, Config{Package: "graphql"})

	fields := []*entgen.Field{
		{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		{
			Name: "internal",
			Type: &field.TypeInfo{Type: field.TypeString},
			Annotations: map[string]any{
				AnnotationName: &Annotation{Skip: SkipType},
			},
		},
	}

	result := gen.filterFields(fields, SkipType)
	assert.Len(t, result, 1)
	assert.Equal(t, "name", result[0].Name)
}

func TestGenerator_NodeImplementors(t *testing.T) {
	t.Run("RelaySpecEnabled", func(t *testing.T) {
		userType := &entgen.Type{
			Name:        "User",
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: true})

		ifaces := gen.nodeImplementors(userType)
		assert.Contains(t, ifaces, "Node")
	})

	t.Run("RelaySpecDisabled", func(t *testing.T) {
		userType := &entgen.Type{
			Name:        "User",
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: false})

		ifaces := gen.nodeImplementors(userType)
		assert.NotContains(t, ifaces, "Node")
	})

	t.Run("WithCustomInterfaces", func(t *testing.T) {
		userType := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{Implements: []string{"Auditable"}},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: true})

		ifaces := gen.nodeImplementors(userType)
		assert.Contains(t, ifaces, "Node")
		assert.Contains(t, ifaces, "Auditable")
	})

	t.Run("SkipType_NoNode", func(t *testing.T) {
		userType := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{Skip: SkipType},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: true})

		ifaces := gen.nodeImplementors(userType)
		assert.NotContains(t, ifaces, "Node")
	})

	t.Run("ExplicitNodeInterface_NoDuplicate", func(t *testing.T) {
		userType := &entgen.Type{
			Name: "User",
			Annotations: map[string]any{
				AnnotationName: &Annotation{Implements: []string{"Node"}},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}
		gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: true})

		ifaces := gen.nodeImplementors(userType)
		// Should have exactly one "Node", not duplicated
		nodeCount := 0
		for _, i := range ifaces {
			if i == "Node" {
				nodeCount++
			}
		}
		assert.Equal(t, 1, nodeCount, "Node should not be duplicated")
	})
}

func TestGenerator_HasWhereInput(t *testing.T) {
	postType := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}}},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{
			Package:  "example/ent",
			Features: []entgen.Feature{entgen.FeatureWhereInputAll},
		},
		Nodes: []*entgen.Type{postType},
	}
	gen := NewGenerator(g, Config{Package: "graphql", WhereInputs: true})

	t.Run("NormalEdge", func(t *testing.T) {
		edge := &entgen.Edge{
			Name:        "posts",
			Type:        postType,
			Annotations: map[string]any{},
		}
		assert.True(t, gen.hasWhereInput(edge))
	})

	t.Run("SkippedEdge", func(t *testing.T) {
		edge := &entgen.Edge{
			Name: "posts",
			Type: postType,
			Annotations: map[string]any{
				AnnotationName: &Annotation{Skip: SkipWhereInput},
			},
		}
		assert.False(t, gen.hasWhereInput(edge))
	})

	t.Run("SkippedTargetType", func(t *testing.T) {
		skippedType := &entgen.Type{
			Name: "Hidden",
			ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
			Annotations: map[string]any{
				AnnotationName: &Annotation{Skip: SkipWhereInput},
			},
		}
		edge := &entgen.Edge{
			Name:        "hidden",
			Type:        skippedType,
			Annotations: map[string]any{},
		}
		assert.False(t, gen.hasWhereInput(edge))
	})
}

func TestGenerator_HasOrderField(t *testing.T) {
	postType := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{postType},
	}
	gen := NewGenerator(g, Config{Package: "graphql"})

	t.Run("Normal", func(t *testing.T) {
		edge := &entgen.Edge{
			Name:        "posts",
			Type:        postType,
			Annotations: map[string]any{},
		}
		assert.True(t, gen.hasOrderField(edge))
	})

	t.Run("Skipped", func(t *testing.T) {
		edge := &entgen.Edge{
			Name: "posts",
			Type: postType,
			Annotations: map[string]any{
				AnnotationName: &Annotation{Skip: SkipOrderField},
			},
		}
		assert.False(t, gen.hasOrderField(edge))
	})
}

// =============================================================================
// fieldDirectives Tests
// =============================================================================

func TestFieldDirectives_RendersDeprecated(t *testing.T) {
	gen := &Generator{}
	f := &entgen.Field{
		Name: "old_name",
		Annotations: map[string]any{
			AnnotationName: &Annotation{
				Directives: []Directive{Deprecated("use new_name")},
			},
		},
	}
	result := gen.fieldDirectives(f)
	assert.Contains(t, result, "@deprecated")
	assert.Contains(t, result, "use new_name")
}

func TestFieldDirectives_NoDirectives(t *testing.T) {
	gen := &Generator{}
	f := &entgen.Field{
		Name:        "name",
		Annotations: map[string]any{},
	}
	result := gen.fieldDirectives(f)
	assert.Equal(t, "", result)
}

func TestRenderDirectives_Empty(t *testing.T) {
	assert.Equal(t, "", renderDirectives(nil))
	assert.Equal(t, "", renderDirectives([]Directive{}))
}

func TestRenderDirectives_Multiple(t *testing.T) {
	dirs := []Directive{
		{Name: "deprecated", Args: map[string]any{"reason": "old"}},
		{Name: "auth"},
	}
	result := renderDirectives(dirs)
	assert.Contains(t, result, `@deprecated(reason: "old")`)
	assert.Contains(t, result, "@auth")
}
