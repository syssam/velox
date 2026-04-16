package graphql

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

func TestGenCreateInput_Omittable(t *testing.T) {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	memoField := &entgen.Field{
		Name:     "memo",
		Type:     &field.TypeInfo{Type: field.TypeString},
		Optional: true,
		Nillable: true,
		Annotations: map[string]any{
			"graphql": Annotation{Omittable: true},
		},
	}
	typ := &entgen.Type{
		Name:        "Invoice",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{memoField},
		Annotations: mutAnn,
	}
	g := newTestGenerator(typ)
	sdl := g.genCreateInput(typ)
	assert.Contains(t, sdl, `memo: String @goField(omittable: true)`)
}

func TestGenUpdateInput_Omittable(t *testing.T) {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	memoField := &entgen.Field{
		Name:     "memo",
		Type:     &field.TypeInfo{Type: field.TypeString},
		Optional: true,
		Nillable: true,
		Annotations: map[string]any{
			"graphql": Annotation{Omittable: true},
		},
	}
	typ := &entgen.Type{
		Name:        "Invoice",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{memoField},
		Annotations: mutAnn,
	}
	g := newTestGenerator(typ)
	sdl := g.genUpdateInput(typ)
	assert.Contains(t, sdl, `memo: String @goField(omittable: true)`)
}

func TestGenMutationInput_OmittableStructField(t *testing.T) {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	memoField := &entgen.Field{
		Name:     "memo",
		Type:     &field.TypeInfo{Type: field.TypeString},
		Optional: true,
		Nillable: true,
		Annotations: map[string]any{
			"graphql": Annotation{Omittable: true},
		},
	}
	typ := &entgen.Type{
		Name:        "Invoice",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{memoField},
		Annotations: mutAnn,
	}
	g := newTestGenerator(typ)
	f := g.genEntityMutationInput(typ)
	var buf bytes.Buffer
	_ = f.Render(&buf)
	output := buf.String()
	assert.Contains(t, output, "Omittable")
}

func TestGenMutationInput_GlobalNullableInputOmittable(t *testing.T) {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	memoField := &entgen.Field{
		Name:     "memo",
		Type:     &field.TypeInfo{Type: field.TypeString},
		Optional: true,
		Nillable: true,
	}
	typ := &entgen.Type{
		Name:        "Invoice",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{memoField},
		Annotations: mutAnn,
	}
	g := newTestGeneratorWithConfig(Config{
		NullableInputOmittable: true,
		ORMPackage:             "example.com/app/velox",
		Package:                "velox",
	}, typ)
	f := g.genEntityMutationInput(typ)
	var buf bytes.Buffer
	_ = f.Render(&buf)
	output := buf.String()
	assert.Contains(t, output, "Omittable")
}

func TestGenEntityType_FullResolverIntegration(t *testing.T) {
	ann := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
			ResolverMappings: []ResolverMapping{
				Map("glAccount", "PublicGlAccount!"),
				Map("approver", "PublicUser"),
			},
		},
	}
	typ := &entgen.Type{
		Name: "Invoice",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "amount", Type: &field.TypeInfo{Type: field.TypeFloat64}},
			{Name: "internal_ref", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "memo", Type: &field.TypeInfo{Type: field.TypeString}, Optional: true, Nillable: true,
				Annotations: map[string]any{"graphql": Annotation{Omittable: true}}},
		},
		Annotations: ann,
	}
	g := newTestGenerator(typ)

	// Entity type SDL
	sdl := g.genEntityType(typ)
	assert.Contains(t, sdl, `internalRef: String!`)
	assert.Contains(t, sdl, `glAccount: PublicGlAccount! @goField(forceResolver: true)`)
	assert.Contains(t, sdl, `approver: PublicUser @goField(forceResolver: true)`)
	assert.Contains(t, sdl, `amount: Float!`)
	assert.NotContains(t, sdl, `amount: Float! @goField`)

	// Validation passes
	err := g.validateResolverMappings(typ)
	assert.NoError(t, err)
}

func TestGenUpdateInput_Omittable_NoClearField(t *testing.T) {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	memoField := &entgen.Field{
		Name:     "memo",
		Type:     &field.TypeInfo{Type: field.TypeString},
		Optional: true,
		Nillable: true,
		Annotations: map[string]any{
			"graphql": Annotation{Omittable: true},
		},
	}
	typ := &entgen.Type{
		Name:        "Invoice",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{memoField},
		Annotations: mutAnn,
	}
	g := newTestGenerator(typ)
	sdl := g.genUpdateInput(typ)
	// Omittable fields should NOT have clearXxx (clearing is via Value() == nil)
	assert.Contains(t, sdl, `memo: String @goField(omittable: true)`)
	assert.NotContains(t, sdl, `clearMemo`)
}

func TestGenUpdateInput_GlobalOmittable_SDL(t *testing.T) {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	memoField := &entgen.Field{
		Name:     "memo",
		Type:     &field.TypeInfo{Type: field.TypeString},
		Optional: true,
		Nillable: true,
	}
	typ := &entgen.Type{
		Name:        "Invoice",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{memoField},
		Annotations: mutAnn,
	}
	g := newTestGeneratorWithConfig(Config{
		NullableInputOmittable: true,
		ORMPackage:             "example.com/app/velox",
		Package:                "velox",
	}, typ)
	sdl := g.genUpdateInput(typ)
	assert.Contains(t, sdl, `memo: String @goField(omittable: true)`)
	assert.NotContains(t, sdl, `clearMemo`)
}

// =============================================================================
// Mutation Input Edge ID Tests
// =============================================================================
//
// These tests verify that all edge types correctly generate edge ID fields
// in both the Go struct (genEntityMutationInput) and GraphQL SDL
// (genCreateInput / genUpdateInput).

// mutationEdgeTestHelper holds types and generator for edge mutation tests.
type mutationEdgeTestHelper struct {
	gen   *Generator
	graph *entgen.Graph
}

// newMutationEdgeTestHelper creates a test helper with the given types.
// All types get mutation annotations by default.
func newMutationEdgeTestHelper(types ...*entgen.Type) *mutationEdgeTestHelper {
	mutAnn := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}
	for _, t := range types {
		if t.Annotations == nil {
			t.Annotations = mutAnn
		}
	}
	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  types,
	}
	gen := NewGenerator(g, Config{
		Package:    "graphql",
		ORMPackage: "example/ent",
		Mutations:  true,
	})
	return &mutationEdgeTestHelper{gen: gen, graph: g}
}

// renderGoStruct renders the Go mutation input code for a type.
func (h *mutationEdgeTestHelper) renderGoStruct(t *testing.T, typ *entgen.Type) string {
	t.Helper()
	f := h.gen.genEntityMutationInput(typ)
	if f == nil {
		t.Fatalf("genEntityMutationInput(%s) returned nil", typ.Name)
	}
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("render %s: %v", typ.Name, err)
	}
	return buf.String()
}

// extractStruct extracts a struct definition section from rendered code.
func extractStruct(code, structName string) string {
	idx := strings.Index(code, "type "+structName+" struct")
	if idx < 0 {
		return ""
	}
	rest := code[idx:]
	// Find next type declaration or end of code
	nextType := strings.Index(rest[1:], "\ntype ")
	if nextType < 0 {
		return rest
	}
	return rest[:nextType+1]
}

// TestMutationInputEdges_O2M_OwnerSide tests that O2M edges on the owner side
// generate AddXxxIDs in create and Add/Remove/Clear in update.
func TestMutationInputEdges_O2M_OwnerSide(t *testing.T) {
	childType := &entgen.Type{
		Name:   "Comment",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	parentType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Edges: []*entgen.Edge{
			{
				Name:   "comments",
				Type:   childType,
				Unique: false,
				Rel:    entgen.Relation{Type: entgen.O2M},
			},
		},
	}

	h := newMutationEdgeTestHelper(parentType, childType)
	goCode := h.renderGoStruct(t, parentType)

	t.Run("CreateInput_Go_HasEdgeIDs", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.NotEmpty(t, s, "CreatePostInput struct not found")
		assert.Contains(t, s, "CommentIDs", "should have CommentIDs field")
		assert.Contains(t, s, "[]int", "should be []int slice")
	})

	t.Run("UpdateInput_Go_HasAddRemoveClear", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		assert.NotEmpty(t, s, "UpdatePostInput struct not found")
		assert.Contains(t, s, "AddCommentIDs", "should have AddCommentIDs")
		assert.Contains(t, s, "RemoveCommentIDs", "should have RemoveCommentIDs")
		assert.Contains(t, s, "ClearComments", "O2M edges should have ClearComments")
	})

	t.Run("CreateInput_SDL_HasEdgeIDs", func(t *testing.T) {
		sdl := h.gen.genCreateInput(parentType)
		assert.Contains(t, sdl, "commentIDs: [ID!]", "SDL should have commentIDs field")
	})

	t.Run("UpdateInput_SDL_HasAddRemove", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(parentType)
		assert.Contains(t, sdl, "addCommentIDs: [ID!]", "SDL should have addCommentIDs")
		assert.Contains(t, sdl, "removeCommentIDs: [ID!]", "SDL should have removeCommentIDs")
		assert.Contains(t, sdl, "clearComments: Boolean", "SDL should have clearComments")
	})

	t.Run("CreateMutate_CallsAddIDs", func(t *testing.T) {
		assert.Contains(t, goCode, "AddCommentIDs(i.CommentIDs...)", "Mutate should call AddCommentIDs")
	})

	t.Run("UpdateMutate_CallsAddAndRemove", func(t *testing.T) {
		assert.Contains(t, goCode, "AddCommentIDs(i.AddCommentIDs...)", "Mutate should call AddCommentIDs")
		assert.Contains(t, goCode, "RemoveCommentIDs(i.RemoveCommentIDs...)", "Mutate should call RemoveCommentIDs")
		assert.Contains(t, goCode, "ClearComments()", "Mutate should call ClearComments")
	})
}

// TestMutationInputEdges_M2O_InverseWithOwnFK tests that M2O edges (inverse
// with OwnFK) generate SetXxxID fields in both create and update.
func TestMutationInputEdges_M2O_InverseWithOwnFK(t *testing.T) {
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Edges: []*entgen.Edge{
			{
				Name:     "author",
				Type:     userType,
				Unique:   true,
				Optional: false, // Required
				Inverse:  "posts",
				Rel:      entgen.Relation{Type: entgen.M2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, userType)
	goCode := h.renderGoStruct(t, postType)

	t.Run("CreateInput_Go_RequiredEdgeID", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.Contains(t, s, "AuthorID", "should have AuthorID")
		// Required edge: should NOT be a pointer
		assert.NotContains(t, s, "AuthorID *", "required edge should not be pointer")
	})

	t.Run("UpdateInput_Go_OptionalEdgeID", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		assert.Contains(t, s, "AuthorID", "should have AuthorID")
		// In update, all fields are optional (pointer)
		idx := strings.Index(s, "AuthorID")
		if idx >= 0 {
			afterField := s[idx+len("AuthorID"):]
			nl := strings.Index(afterField, "\n")
			if nl > 0 {
				afterField = afterField[:nl]
			}
			assert.Contains(t, afterField, "*", "update AuthorID should be pointer")
		}
		// Required edge should NOT have ClearAuthor
		assert.NotContains(t, s, "ClearAuthor", "required edge cannot be cleared")
	})

	t.Run("CreateInput_SDL_RequiredEdgeID", func(t *testing.T) {
		sdl := h.gen.genCreateInput(postType)
		assert.Contains(t, sdl, "authorID: ID!", "required M2O edge should be ID!")
	})

	t.Run("UpdateInput_SDL_OptionalEdgeID", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(postType)
		assert.Contains(t, sdl, "authorID: ID", "update should have authorID")
		// Should NOT have clearAuthor (required edge)
		assert.NotContains(t, sdl, "clearAuthor", "required edge cannot be cleared in SDL")
	})

	t.Run("CreateMutate_CallsSetID", func(t *testing.T) {
		assert.Contains(t, goCode, "SetAuthorID(i.AuthorID)", "Mutate should call SetAuthorID")
	})
}

// TestMutationInputEdges_M2O_OptionalInverse tests optional M2O edges
// generate pointer IDs and clear operations.
func TestMutationInputEdges_M2O_OptionalInverse(t *testing.T) {
	categoryType := &entgen.Type{
		Name:   "Category",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:     "category",
				Type:     categoryType,
				Unique:   true,
				Optional: true,
				Inverse:  "posts",
				Rel:      entgen.Relation{Type: entgen.M2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, categoryType)
	goCode := h.renderGoStruct(t, postType)

	t.Run("CreateInput_Go_OptionalPointer", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.Contains(t, s, "CategoryID", "should have CategoryID")
		assert.Contains(t, s, "CategoryID *int", "optional edge should be pointer")
	})

	t.Run("UpdateInput_Go_HasClear", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		assert.Contains(t, s, "ClearCategory", "optional edge should have ClearCategory")
		assert.Contains(t, s, "CategoryID", "should have CategoryID")
	})

	t.Run("CreateInput_SDL_OptionalID", func(t *testing.T) {
		sdl := h.gen.genCreateInput(postType)
		assert.Contains(t, sdl, "categoryID: ID\n", "optional M2O should be nullable ID")
		assert.NotContains(t, sdl, "categoryID: ID!", "optional edge should NOT be required")
	})

	t.Run("UpdateInput_SDL_HasClear", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(postType)
		assert.Contains(t, sdl, "clearCategory: Boolean", "optional edge should have clearCategory")
		assert.Contains(t, sdl, "categoryID: ID", "should have categoryID")
	})
}

// TestMutationInputEdges_M2M tests M2M edges generate Add/Remove IDs.
func TestMutationInputEdges_M2M(t *testing.T) {
	tagType := &entgen.Type{
		Name:   "Tag",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:   "tags",
				Type:   tagType,
				Unique: false,
				Rel:    entgen.Relation{Type: entgen.M2M},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, tagType)
	goCode := h.renderGoStruct(t, postType)

	t.Run("CreateInput_Go", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.Contains(t, s, "TagIDs", "should have TagIDs for M2M")
		assert.Contains(t, s, "[]int", "should be slice of int")
	})

	t.Run("UpdateInput_Go", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		assert.Contains(t, s, "AddTagIDs", "should have AddTagIDs")
		assert.Contains(t, s, "RemoveTagIDs", "should have RemoveTagIDs")
		assert.Contains(t, s, "ClearTags", "M2M should have ClearTags")
	})

	t.Run("CreateInput_SDL", func(t *testing.T) {
		sdl := h.gen.genCreateInput(postType)
		assert.Contains(t, sdl, "tagIDs: [ID!]", "SDL should have tagIDs")
	})

	t.Run("UpdateInput_SDL", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(postType)
		assert.Contains(t, sdl, "addTagIDs: [ID!]", "SDL should have addTagIDs")
		assert.Contains(t, sdl, "removeTagIDs: [ID!]", "SDL should have removeTagIDs")
	})
}

// TestMutationInputEdges_O2O tests O2O edges generate SetXxxID fields.
func TestMutationInputEdges_O2O(t *testing.T) {
	profileType := &entgen.Type{
		Name:   "Profile",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:     "profile",
				Type:     profileType,
				Unique:   true,
				Optional: true,
				Rel:      entgen.Relation{Type: entgen.O2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(userType, profileType)
	goCode := h.renderGoStruct(t, userType)

	t.Run("CreateInput_Go", func(t *testing.T) {
		s := extractStruct(goCode, "CreateUserInput")
		assert.Contains(t, s, "ProfileID", "should have ProfileID for O2O")
		assert.Contains(t, s, "ProfileID *int", "optional O2O should be pointer")
	})

	t.Run("UpdateInput_Go", func(t *testing.T) {
		s := extractStruct(goCode, "UpdateUserInput")
		assert.Contains(t, s, "ProfileID", "should have ProfileID")
		assert.Contains(t, s, "ClearProfile", "optional O2O should have ClearProfile")
	})

	t.Run("SDL_Create", func(t *testing.T) {
		sdl := h.gen.genCreateInput(userType)
		assert.Contains(t, sdl, "profileID: ID", "SDL should have profileID")
	})

	t.Run("SDL_Update", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(userType)
		assert.Contains(t, sdl, "profileID: ID", "SDL should have profileID")
		assert.Contains(t, sdl, "clearProfile: Boolean", "SDL should have clearProfile")
	})
}

// TestMutationInputEdges_InverseWithoutOwnFK_Skipped verifies that inverse
// edges that don't own the FK are correctly excluded from mutation inputs.
func TestMutationInputEdges_InverseWithoutOwnFK_Skipped(t *testing.T) {
	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	// User has inverse O2M edge "posts" — FK is on Post table, not User.
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:    "posts",
				Type:    postType,
				Unique:  false,
				Inverse: "author", // inverse O2M — FK not on User
				Rel:     entgen.Relation{Type: entgen.O2M},
			},
		},
	}

	h := newMutationEdgeTestHelper(userType, postType)
	goCode := h.renderGoStruct(t, userType)

	t.Run("CreateInput_Go_NoEdgeIDs", func(t *testing.T) {
		s := extractStruct(goCode, "CreateUserInput")
		assert.NotContains(t, s, "PostIDs", "inverse O2M without OwnFK should be skipped")
		assert.NotContains(t, s, "PostID", "inverse O2M without OwnFK should be skipped")
	})

	t.Run("UpdateInput_Go_NoEdgeIDs", func(t *testing.T) {
		s := extractStruct(goCode, "UpdateUserInput")
		assert.NotContains(t, s, "PostIDs", "inverse O2M without OwnFK should be skipped")
		assert.NotContains(t, s, "AddPostIDs", "inverse O2M without OwnFK should be skipped")
	})

	t.Run("SDL_Create_NoEdgeIDs", func(t *testing.T) {
		sdl := h.gen.genCreateInput(userType)
		assert.NotContains(t, sdl, "postID", "inverse O2M without OwnFK should be skipped in SDL")
	})

	t.Run("SDL_Update_NoEdgeIDs", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(userType)
		assert.NotContains(t, sdl, "postID", "inverse O2M without OwnFK should be skipped in SDL")
		assert.NotContains(t, sdl, "addPostIDs", "inverse O2M without OwnFK should be skipped in SDL")
	})
}

// TestMutationInputEdges_ImmutableEdge_SkippedInUpdate tests that immutable
// edges appear in create input but are excluded from update input.
func TestMutationInputEdges_ImmutableEdge_SkippedInUpdate(t *testing.T) {
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:      "author",
				Type:      userType,
				Unique:    true,
				Optional:  false,
				Immutable: true,
				Inverse:   "posts",
				Rel:       entgen.Relation{Type: entgen.M2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, userType)
	goCode := h.renderGoStruct(t, postType)

	t.Run("CreateInput_Go_HasEdge", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.Contains(t, s, "AuthorID", "immutable edge should still appear in create")
	})

	t.Run("UpdateInput_Go_NoEdge", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		assert.NotContains(t, s, "AuthorID", "immutable edge should be excluded from update")
		assert.NotContains(t, s, "ClearAuthor", "immutable edge should not have clear in update")
	})

	t.Run("SDL_Update_NoEdge", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(postType)
		assert.NotContains(t, sdl, "authorID", "immutable edge excluded from update SDL")
	})
}

// TestMutationInputEdges_UUIDIDType tests that edges with UUID ID types
// generate uuid.UUID fields instead of int.
func TestMutationInputEdges_UUIDIDType(t *testing.T) {
	tagType := &entgen.Type{
		Name: "Tag",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{
				Type:    field.TypeUUID,
				PkgPath: "github.com/google/uuid",
				Ident:   "uuid.UUID",
			},
		},
		Fields: []*entgen.Field{},
	}
	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:   "tags",
				Type:   tagType,
				Unique: false,
				Rel:    entgen.Relation{Type: entgen.M2M},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, tagType)
	goCode := h.renderGoStruct(t, postType)

	t.Run("CreateInput_UUID_Type", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.Contains(t, s, "TagIDs", "should have TagIDs")
		assert.Contains(t, s, "uuid.UUID", "TagIDs should use uuid.UUID type")
	})

	t.Run("UpdateInput_UUID_Type", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		assert.Contains(t, s, "AddTagIDs", "should have AddTagIDs")
		assert.Contains(t, s, "uuid.UUID", "edge IDs should use uuid.UUID type")
	})
}

// TestMutationInputEdges_MultipleEdges tests that a type with multiple edge
// types generates all edge ID fields correctly.
func TestMutationInputEdges_MultipleEdges(t *testing.T) {
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	commentType := &entgen.Type{
		Name:   "Comment",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	tagType := &entgen.Type{
		Name:   "Tag",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	categoryType := &entgen.Type{
		Name:   "Category",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}

	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Edges: []*entgen.Edge{
			// M2O required
			{Name: "author", Type: userType, Unique: true, Optional: false, Inverse: "posts", Rel: entgen.Relation{Type: entgen.M2O}},
			// O2M
			{Name: "comments", Type: commentType, Unique: false, Rel: entgen.Relation{Type: entgen.O2M}},
			// M2M
			{Name: "tags", Type: tagType, Unique: false, Rel: entgen.Relation{Type: entgen.M2M}},
			// M2O optional
			{Name: "category", Type: categoryType, Unique: true, Optional: true, Inverse: "posts", Rel: entgen.Relation{Type: entgen.M2O}},
		},
	}

	h := newMutationEdgeTestHelper(postType, userType, commentType, tagType, categoryType)
	goCode := h.renderGoStruct(t, postType)

	t.Run("CreateInput_AllEdges", func(t *testing.T) {
		s := extractStruct(goCode, "CreatePostInput")
		assert.Contains(t, s, "AuthorID", "M2O required: AuthorID")
		assert.Contains(t, s, "CommentIDs", "O2M: CommentIDs")
		assert.Contains(t, s, "TagIDs", "M2M: TagIDs")
		assert.Contains(t, s, "CategoryID", "M2O optional: CategoryID")
	})

	t.Run("UpdateInput_AllEdges", func(t *testing.T) {
		s := extractStruct(goCode, "UpdatePostInput")
		// M2O required — ID present, no clear
		assert.Contains(t, s, "AuthorID", "M2O required: AuthorID in update")
		assert.NotContains(t, s, "ClearAuthor", "required edge has no clear")
		// O2M — add/remove/clear
		assert.Contains(t, s, "AddCommentIDs", "O2M: AddCommentIDs")
		assert.Contains(t, s, "RemoveCommentIDs", "O2M: RemoveCommentIDs")
		assert.Contains(t, s, "ClearComments", "O2M: ClearComments")
		// M2M — add/remove/clear
		assert.Contains(t, s, "AddTagIDs", "M2M: AddTagIDs")
		assert.Contains(t, s, "RemoveTagIDs", "M2M: RemoveTagIDs")
		assert.Contains(t, s, "ClearTags", "M2M: ClearTags")
		// M2O optional — ID + clear
		assert.Contains(t, s, "CategoryID", "M2O optional: CategoryID")
		assert.Contains(t, s, "ClearCategory", "optional edge has clear")
	})

	t.Run("SDL_Create_AllEdges", func(t *testing.T) {
		sdl := h.gen.genCreateInput(postType)
		assert.Contains(t, sdl, "authorID: ID!", "M2O required in SDL")
		assert.Contains(t, sdl, "commentIDs: [ID!]", "O2M in SDL")
		assert.Contains(t, sdl, "tagIDs: [ID!]", "M2M in SDL")
		assert.Contains(t, sdl, "categoryID: ID\n", "M2O optional in SDL")
	})

	t.Run("SDL_Update_AllEdges", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(postType)
		assert.Contains(t, sdl, "authorID: ID", "M2O in update SDL")
		assert.NotContains(t, sdl, "clearAuthor", "required edge no clear in SDL")
		assert.Contains(t, sdl, "addCommentIDs: [ID!]", "O2M add in SDL")
		assert.Contains(t, sdl, "removeCommentIDs: [ID!]", "O2M remove in SDL")
		assert.Contains(t, sdl, "clearComments: Boolean", "O2M clear in SDL")
		assert.Contains(t, sdl, "addTagIDs: [ID!]", "M2M add in SDL")
		assert.Contains(t, sdl, "removeTagIDs: [ID!]", "M2M remove in SDL")
		assert.Contains(t, sdl, "clearCategory: Boolean", "optional edge clear in SDL")
	})
}

// TestMutationInputEdges_NoMutationAnnotation tests that entities without
// mutation annotations produce no mutation input code.
func TestMutationInputEdges_NoMutationAnnotation(t *testing.T) {
	tagType := &entgen.Type{
		Name:        "Tag",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields:      []*entgen.Field{},
		Annotations: map[string]any{}, // No mutations
	}
	postType := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields:      []*entgen.Field{},
		Annotations: map[string]any{}, // No mutations
		Edges: []*entgen.Edge{
			{Name: "tags", Type: tagType, Unique: false, Rel: entgen.Relation{Type: entgen.M2M}},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{postType, tagType},
	}
	gen := NewGenerator(g, Config{
		Package:    "graphql",
		ORMPackage: "example/ent",
		Mutations:  true,
	})

	f := gen.genEntityMutationInput(postType)
	var buf bytes.Buffer
	_ = f.Render(&buf)
	code := buf.String()

	assert.NotContains(t, code, "CreatePostInput", "no mutation annotation = no create input")
	assert.NotContains(t, code, "UpdatePostInput", "no mutation annotation = no update input")
}

// TestMutationInputEdges_PluralSingularization tests that edge names are
// correctly singularized for ID field names (e.g., "children" -> "ChildIDs").
func TestMutationInputEdges_PluralSingularization(t *testing.T) {
	childType := &entgen.Type{
		Name:   "Child",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	parentType := &entgen.Type{
		Name:   "Parent",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:   "children",
				Type:   childType,
				Unique: false,
				Rel:    entgen.Relation{Type: entgen.O2M},
			},
		},
	}

	h := newMutationEdgeTestHelper(parentType, childType)
	goCode := h.renderGoStruct(t, parentType)

	t.Run("Create_Singularized", func(t *testing.T) {
		s := extractStruct(goCode, "CreateParentInput")
		// "children" -> singularize -> "child" -> "ChildIDs"
		assert.Contains(t, s, "ChildIDs", "plural edge 'children' should singularize to ChildIDs")
		assert.NotContains(t, s, "ChildrenIDs", "should NOT use plural form ChildrenIDs")
	})

	t.Run("Update_Singularized", func(t *testing.T) {
		s := extractStruct(goCode, "UpdateParentInput")
		assert.Contains(t, s, "AddChildIDs", "should use singularized AddChildIDs")
		assert.Contains(t, s, "RemoveChildIDs", "should use singularized RemoveChildIDs")
	})

	t.Run("SDL_Singularized", func(t *testing.T) {
		sdl := h.gen.genCreateInput(parentType)
		assert.Contains(t, sdl, "childIDs: [ID!]", "SDL should use singularized childIDs")
	})
}

// TestMutationInputEdges_Int64IDType tests edges with int64 ID types.
func TestMutationInputEdges_Int64IDType(t *testing.T) {
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{},
	}
	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:     "author",
				Type:     userType,
				Unique:   true,
				Optional: false,
				Inverse:  "posts",
				Rel:      entgen.Relation{Type: entgen.M2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, userType)
	goCode := h.renderGoStruct(t, postType)

	s := extractStruct(goCode, "CreatePostInput")
	assert.Contains(t, s, "AuthorID", "should have AuthorID")
	assert.Contains(t, s, "int64", "should use int64 for target type's ID")
}

// TestMutationInputEdges_StringIDType tests edges with string ID types.
func TestMutationInputEdges_StringIDType(t *testing.T) {
	orgType := &entgen.Type{
		Name:   "Org",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeString}},
		Fields: []*entgen.Field{},
	}
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				Name:     "org",
				Type:     orgType,
				Unique:   true,
				Optional: false,
				Inverse:  "users",
				Rel:      entgen.Relation{Type: entgen.M2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(userType, orgType)
	goCode := h.renderGoStruct(t, userType)

	s := extractStruct(goCode, "CreateUserInput")
	assert.Contains(t, s, "OrgID", "should have OrgID")
	assert.Contains(t, s, "string", "should use string for target type's ID")
}

// TestMutationInputEdges_SkipAnnotation tests that edge-level Skip annotations
// (SkipMutationCreateInput / SkipMutationUpdateInput) are respected.
func TestMutationInputEdges_SkipAnnotation(t *testing.T) {
	tagType := &entgen.Type{
		Name:   "Tag",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	commentType := &entgen.Type{
		Name:   "Comment",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}
	userType := &entgen.Type{
		Name:   "User",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
	}

	postType := &entgen.Type{
		Name:   "Post",
		ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{},
		Edges: []*entgen.Edge{
			{
				// Skip from create input only
				Name:   "tags",
				Type:   tagType,
				Unique: false,
				Rel:    entgen.Relation{Type: entgen.M2M},
				Annotations: map[string]any{
					"graphql": Annotation{Skip: SkipMutationCreateInput},
				},
			},
			{
				// Skip from update input only
				Name:   "comments",
				Type:   commentType,
				Unique: false,
				Rel:    entgen.Relation{Type: entgen.O2M},
				Annotations: map[string]any{
					"graphql": Annotation{Skip: SkipMutationUpdateInput},
				},
			},
			{
				// No skip — should appear in both
				Name:     "author",
				Type:     userType,
				Unique:   true,
				Optional: false,
				Inverse:  "posts",
				Rel:      entgen.Relation{Type: entgen.M2O},
			},
		},
	}

	h := newMutationEdgeTestHelper(postType, tagType, commentType, userType)

	t.Run("Go_CreateInput_SkipsTagIDs", func(t *testing.T) {
		goCode := h.renderGoStruct(t, postType)
		s := extractStruct(goCode, "CreatePostInput")
		assert.NotContains(t, s, "TagIDs", "tags edge has SkipMutationCreateInput — should be excluded from create")
		assert.Contains(t, s, "CommentIDs", "comments edge has NO create skip — should appear in create")
		assert.Contains(t, s, "AuthorID", "author has no skip — should appear")
	})

	t.Run("Go_UpdateInput_SkipsComments", func(t *testing.T) {
		goCode := h.renderGoStruct(t, postType)
		s := extractStruct(goCode, "UpdatePostInput")
		assert.Contains(t, s, "AddTagIDs", "tags edge has NO update skip — should appear in update")
		assert.NotContains(t, s, "AddCommentIDs", "comments edge has SkipMutationUpdateInput — should be excluded from update")
		assert.NotContains(t, s, "RemoveCommentIDs", "comments edge has SkipMutationUpdateInput — should be excluded")
		assert.NotContains(t, s, "ClearComments", "comments edge has SkipMutationUpdateInput — clear should be excluded")
		assert.Contains(t, s, "AuthorID", "author has no skip — should appear")
	})

	t.Run("SDL_CreateInput_SkipsTagIDs", func(t *testing.T) {
		sdl := h.gen.genCreateInput(postType)
		assert.NotContains(t, sdl, "tagIDs", "tags edge has SkipMutationCreateInput — excluded from SDL")
		assert.Contains(t, sdl, "commentIDs", "comments has no create skip — in SDL")
		assert.Contains(t, sdl, "authorID", "author has no skip — in SDL")
	})

	t.Run("SDL_UpdateInput_SkipsComments", func(t *testing.T) {
		sdl := h.gen.genUpdateInput(postType)
		assert.Contains(t, sdl, "addTagIDs", "tags has no update skip — in SDL")
		assert.NotContains(t, sdl, "addCommentIDs", "comments has SkipMutationUpdateInput — excluded from SDL")
		assert.NotContains(t, sdl, "removeCommentIDs", "comments has SkipMutationUpdateInput — excluded from SDL")
		assert.Contains(t, sdl, "authorID", "author has no skip — in SDL")
	})

	t.Run("Go_CreateMutate_SkipsTagIDs", func(t *testing.T) {
		goCode := h.renderGoStruct(t, postType)
		// Extract just the create Mutate function body (up to UpdatePostInput struct)
		createMutateIdx := strings.Index(goCode, "func (i *CreatePostInput) Mutate")
		if createMutateIdx < 0 {
			t.Fatal("CreatePostInput.Mutate not found")
		}
		// Find UpdatePostInput struct that follows the create section
		updateStructIdx := strings.Index(goCode[createMutateIdx:], "UpdatePostInput")
		if updateStructIdx < 0 {
			t.Fatal("UpdatePostInput not found after CreatePostInput.Mutate")
		}
		createMutate := goCode[createMutateIdx : createMutateIdx+updateStructIdx]
		assert.NotContains(t, createMutate, "TagIDs", "create Mutate should not reference TagIDs")
		assert.Contains(t, createMutate, "AddCommentIDs", "create Mutate should call AddCommentIDs")
	})

	t.Run("Go_UpdateMutate_SkipsComments", func(t *testing.T) {
		goCode := h.renderGoStruct(t, postType)
		updateMutateIdx := strings.Index(goCode, "func (i *UpdatePostInput) Mutate")
		if updateMutateIdx < 0 {
			t.Fatal("UpdatePostInput.Mutate not found")
		}
		// The update Mutate is the last method — use the remainder of the file
		updateMutate := goCode[updateMutateIdx:]
		assert.Contains(t, updateMutate, "AddTagIDs", "update Mutate should call AddTagIDs")
		assert.NotContains(t, updateMutate, "CommentIDs", "update Mutate should not reference CommentIDs")
	})
}
