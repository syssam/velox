package gen

import (
	"testing"

	"github.com/syssam/velox/schema/field"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraph_Validate_NoErrors(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name:   "User",
		ID:     &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	assert.NoError(t, err)
}

func TestGraph_Validate_EdgeReferencesUnknownType(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Edges: []*Edge{
			{Name: "posts", Type: nil},
			{Name: "comments", Type: nil},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "posts")
	assert.Contains(t, err.Error(), "comments")

	// Should be a joined error with multiple sub-errors.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		assert.Len(t, joined.Unwrap(), 2)
	}
}

func TestGraph_Validate_FieldEdgeNameCollision(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	postType := &Type{Name: "Post", ID: &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}}}
	g.nodes["Post"] = postType
	g.Nodes = append(g.Nodes, postType)

	userType := &Type{
		Name:   "User",
		ID:     &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{{Name: "posts", Type: &field.TypeInfo{Type: field.TypeString}}},
		Edges:  []*Edge{{Name: "posts", Type: postType}},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with field")
}

func TestGraph_Validate_DuplicateEdgeName(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	postType := &Type{Name: "Post", ID: &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}}}
	g.nodes["Post"] = postType
	g.Nodes = append(g.Nodes, postType)

	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Edges: []*Edge{
			{Name: "posts", Type: postType},
			{Name: "posts", Type: postType},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate edge name")
}

func TestGraph_Validate_IndexReferencesUnknownField(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name:   "User",
		ID:     &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
		Indexes: []*Index{
			{Name: "user_missing", Columns: []string{"missing_field"}},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing_field")
}

func TestGraph_Validate_EmptyGraph(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}

	err := g.Validate()
	assert.NoError(t, err)
}

func TestGraph_Validate_MultipleTypesWithErrors(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}

	// Two types, each with an unresolved edge
	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Edges: []*Edge{
			{Name: "posts", Type: nil},
		},
	}
	categoryType := &Type{
		Name: "Category",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Edges: []*Edge{
			{Name: "items", Type: nil},
		},
	}
	g.Nodes = append(g.Nodes, userType, categoryType)
	g.nodes["User"] = userType
	g.nodes["Category"] = categoryType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "posts")
	assert.Contains(t, err.Error(), "items")

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		assert.Len(t, joined.Unwrap(), 2)
	}
}

func TestGraph_Validate_DuplicateFieldNames(t *testing.T) {
	// Duplicate field names are not validated by Validate() since they'd
	// be caught earlier by the schema loader, but edges with same name as
	// fields are caught.
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	postType := &Type{Name: "Post", ID: &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}}}
	g.nodes["Post"] = postType
	g.Nodes = append(g.Nodes, postType)

	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Edges: []*Edge{
			{Name: "name", Type: postType},  // conflicts with "name" field
			{Name: "email", Type: postType}, // conflicts with "email" field
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)

	// Should report both field-edge name collisions
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		assert.Len(t, joined.Unwrap(), 2)
	}
}

func TestGraph_Validate_IndexMultipleUnknownFields(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name:   "User",
		ID:     &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
		Indexes: []*Index{
			{Name: "user_idx", Columns: []string{"missing1", "missing2"}},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing1")
	assert.Contains(t, err.Error(), "missing2")

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		assert.Len(t, joined.Unwrap(), 2)
	}
}

func TestGraph_Validate_ValidEdgesPass(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	postType := &Type{
		Name: "Post",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
	}
	userType := &Type{
		Name:   "User",
		ID:     &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
		Edges:  []*Edge{{Name: "posts", Type: postType}},
	}
	g.Nodes = append(g.Nodes, userType, postType)
	g.nodes["User"] = userType
	g.nodes["Post"] = postType

	err := g.Validate()
	assert.NoError(t, err)
}

func TestValidateOptionalEnumWithoutDefault(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{
			{
				Name:     "status",
				Type:     &field.TypeInfo{Type: field.TypeEnum},
				Optional: true,
				// No Default, no Nillable — should error.
			},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
	assert.Contains(t, err.Error(), "Optional()")
	assert.Contains(t, err.Error(), "Default()")
	assert.Contains(t, err.Error(), "Nillable()")
}

func TestValidateOptionalStringWithoutDefault_OK(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{
			{
				Name:     "bio",
				Type:     &field.TypeInfo{Type: field.TypeString},
				Optional: true,
				// Standard type — string zero value "" is acceptable.
			},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	assert.NoError(t, err)
}

func TestValidateOptionalEnumWithDefault_OK(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{
			{
				Name:     "status",
				Type:     &field.TypeInfo{Type: field.TypeEnum},
				Optional: true,
				Default:  true,
			},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	assert.NoError(t, err)
}

func TestValidateOptionalEnumWithNillable_OK(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	userType := &Type{
		Name: "User",
		ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{
			{
				Name:     "status",
				Type:     &field.TypeInfo{Type: field.TypeEnum},
				Optional: true,
				Nillable: true,
			},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	assert.NoError(t, err)
}

func TestGraph_Validate_BatchesMultipleErrorTypes(t *testing.T) {
	g := &Graph{
		Config: &Config{Package: "example.com/app/velox"},
		Nodes:  make([]*Type, 0),
		nodes:  make(map[string]*Type),
	}
	// Type with: unresolved edge targets + duplicate edge + field-edge collision.
	userType := &Type{
		Name:   "User",
		ID:     &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{{Name: "posts", Type: &field.TypeInfo{Type: field.TypeString}}},
		Edges: []*Edge{
			{Name: "posts", Type: nil},
			{Name: "posts", Type: nil},
		},
	}
	g.Nodes = append(g.Nodes, userType)
	g.nodes["User"] = userType

	err := g.Validate()
	require.Error(t, err)

	// Should contain errors from multiple validation categories.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		// 2 unknown types + 1 field-edge collision + 1 duplicate edge = 4 errors
		assert.GreaterOrEqual(t, len(joined.Unwrap()), 3)
	}
}
