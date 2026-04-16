package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

func TestGenEntityType_Map(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("glAccount", "PublicGlAccount!"),
				Map("approver", "PublicUser"),
			},
		},
	}
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: ann,
	}
	g := newTestGenerator(userType)
	sdl := g.genEntityType(userType)
	assert.Contains(t, sdl, `glAccount: PublicGlAccount! @goField(forceResolver: true)`)
	assert.Contains(t, sdl, `approver: PublicUser @goField(forceResolver: true)`)
}

func TestValidateResolverMappings_EmptyFieldName(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{Map("", "PublicGlAccount")},
		},
	}
	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty FieldName")
}

func TestValidateResolverMappings_EmptyReturnType(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{Map("glAccount", "")},
		},
	}
	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty ReturnType")
}

func TestValidateResolverMappings_InvalidFieldName(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{Map("GlAccount", "PublicGlAccount")},
		},
	}
	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid GraphQL identifier")
}

func TestValidateResolverMappings_DuplicateFieldName(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("glAccount", "TypeA"),
				Map("glAccount", "TypeB"),
			},
		},
	}
	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestValidateResolverMappings_ConflictWithExistingField(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{Map("email", "CustomEmail")},
		},
	}
	typ := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with existing field")
}

func TestValidateResolverMappings_Valid(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("glAccount", "PublicGlAccount!"),
			},
		},
	}
	typ := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.NoError(t, err)
}

func TestGenEntityType_MapWithComment(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("glAccount", "PublicGlAccount!").WithComment("The GL account associated with this entity."),
				Map("approver", "PublicUser").WithComment("The user who approved this entity."),
			},
		},
	}
	userType := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: ann,
	}
	g := newTestGenerator(userType)
	sdl := g.genEntityType(userType)
	assert.Contains(t, sdl, "\"\"\"\n  The GL account associated with this entity.\n  \"\"\"\n  glAccount: PublicGlAccount! @goField(forceResolver: true)")
	assert.Contains(t, sdl, "\"\"\"\n  The user who approved this entity.\n  \"\"\"\n  approver: PublicUser @goField(forceResolver: true)")
}

func TestGenEntityType_MapWithoutComment(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("glAccount", "PublicGlAccount!"),
			},
		},
	}
	userType := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(userType)
	sdl := g.genEntityType(userType)
	assert.NotContains(t, sdl, `"""`)
	assert.Contains(t, sdl, `glAccount: PublicGlAccount! @goField(forceResolver: true)`)
}

func TestGenEntityType_MapWithInlineArgs(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("priceListItem(priceListId: ID!)", "PriceListItem!"),
			},
		},
	}
	productType := &entgen.Type{
		Name: "Product",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: ann,
	}
	g := newTestGenerator(productType)
	sdl := g.genEntityType(productType)
	assert.Contains(t, sdl, `priceListItem(priceListId: ID!): PriceListItem! @goField(forceResolver: true)`)
}

func TestGenEntityType_MapWithInlineArgsAndComment(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("priceListItem(priceListId: ID!, currency: String)", "PriceListItem!").
					WithComment("Get price for a specific price list and currency."),
			},
		},
	}
	productType := &entgen.Type{
		Name:        "Product",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(productType)
	sdl := g.genEntityType(productType)
	assert.Contains(t, sdl, "Get price for a specific price list and currency.")
	assert.Contains(t, sdl, `priceListItem(priceListId: ID!, currency: String): PriceListItem! @goField(forceResolver: true)`)
}

func TestValidateResolverMappings_InlineArgsValid(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("priceListItem(priceListId: ID!)", "PriceListItem!"),
			},
		},
	}
	typ := &entgen.Type{
		Name:        "Product",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.NoError(t, err)
}

func TestValidateResolverMappings_DuplicateInlineArgs(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			ResolverMappings: []ResolverMapping{
				Map("item(id: ID!)", "Item"),
				Map("item(code: String!)", "Item"),
			},
		},
	}
	typ := &entgen.Type{
		Name:        "Product",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: ann,
	}
	g := newTestGenerator(typ)
	err := g.validateResolverMappings(typ)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}
