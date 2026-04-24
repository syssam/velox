package graphql

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

var updateGolden = flag.Bool("update-golden", false, "update golden files")

func checkGoldenFile(t *testing.T, goldenPath, code string) {
	t.Helper()
	// Match the on-disk layout produced by gen.writeFile via FormatJenFile.
	// Non-Go assets (e.g. .graphql) are compared verbatim.
	out := []byte(code)
	if filepath.Ext(goldenPath) == ".go" {
		formatted, err := entgen.FormatGoBytes(goldenPath, out)
		require.NoError(t, err)
		out = formatted
	}

	if *updateGolden {
		err := os.MkdirAll(filepath.Dir(goldenPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(goldenPath, out, 0o644)
		require.NoError(t, err)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if os.IsNotExist(err) {
		t.Fatalf("golden file %s does not exist, run with -update-golden to create", goldenPath)
	}
	require.NoError(t, err)
	assert.Equal(t, string(expected), string(out))
}

// goldenTestType builds a deterministic User type for golden file tests.
// Any changes to this function will require regenerating all golden files.
func goldenTestType() (*Generator, *entgen.Type) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "name",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
			{
				Name: "email",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
			{
				Name:     "age",
				Type:     &field.TypeInfo{Type: field.TypeInt},
				Optional: true,
			},
			{
				Name:     "bio",
				Type:     &field.TypeInfo{Type: field.TypeString},
				Nillable: true,
			},
		},
		Annotations: map[string]any{
			AnnotationName: Annotation{
				RelayConnection: true,
				Mutations:       mutCreate | mutUpdate,
				HasMutationsSet: true,
			},
		},
	}

	postType := &entgen.Type{
		Name: "Post",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "title",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
		},
		Annotations: map[string]any{},
	}

	postsEdge := &entgen.Edge{
		Name: "posts",
		Type: postType,
		Rel:  entgen.Relation{Type: entgen.O2M, Table: "posts", Columns: []string{"user_id"}},
	}
	userType.Edges = []*entgen.Edge{postsEdge}

	gen := newTestGeneratorWithConfig(Config{
		ORMPackage:      "example.com/app/velox",
		Package:         "velox",
		RelayConnection: true,
		WhereInputs:     true,
		Mutations:       true,
		Ordering:        true,
		RelaySpec:       true,
	}, userType, postType)

	return gen, userType
}

func TestGolden_MutationInput(t *testing.T) {
	t.Parallel()
	g, userType := goldenTestType()
	f := g.genEntityMutationInput(userType)
	require.NotNil(t, f)
	checkGoldenFile(t, filepath.Join("testdata", "golden", "mutation_input.go"), f.GoString())
}

func TestGolden_WhereInput(t *testing.T) {
	t.Parallel()
	g, userType := goldenTestType()
	f := g.genEntityWhereInputFile(userType)
	require.NotNil(t, f)
	checkGoldenFile(t, filepath.Join("testdata", "golden", "where_input.go"), f.GoString())
}

// TestWhereInput_FilterDoesNotImportQuery pins the cycle-break invariant
// (Plan 2, Phase C/D): generated filter/{entity}.go must NOT import the
// query/ package. The Filter method's signature was deliberately changed
// from Filter(q *XxxQuery) to Filter() (predicate.X, error) so that the
// filter/ → query/ import edge disappears — closing the
// entity → filter → query → entity import cycle that Plan 2 was written
// to eliminate. Any future generator change that re-introduces a query/
// import in the filter file re-opens the cycle.
func TestWhereInput_FilterDoesNotImportQuery(t *testing.T) {
	t.Parallel()
	g, userType := goldenTestType()
	f := g.genEntityWhereInputFile(userType)
	require.NotNil(t, f)
	src := f.GoString()
	if strings.Contains(src, `"example/ent/query"`) || strings.Contains(src, `example/ent/query.`) {
		t.Errorf("filter/%s.go imports query/ — the cycle-break refactor "+
			"requires filter/ to be a leaf relative to query/ (Plan 2 Phase C/D)",
			strings.ToLower(userType.Name))
	}
	// Also verify the Filter method signature is the post-cycle-break form.
	if !strings.Contains(src, "Filter() (predicate.User, error)") {
		t.Errorf("Filter method must have signature `Filter() (predicate.User, error)` — found other shape")
	}
}

// TestWhereInput_FilterDoesNotImportEntityOrClient pins the positive
// cycle-break invariant (Plan 2, Phase E Task 22): filter/ must not import
// entity/ or client/{entity}/. This is the forward-looking guarantee that
// enables entity/ to safely import filter/ in Plan 3 — the whole motivation
// for Plan 2. Allowed filter/ deps: predicate, leaf {entity}/ packages,
// gqlrelay, stdlib. Anything else re-opens a cycle path.
func TestWhereInput_FilterDoesNotImportEntityOrClient(t *testing.T) {
	t.Parallel()
	g, userType := goldenTestType()
	f := g.genEntityWhereInputFile(userType)
	require.NotNil(t, f)
	src := f.GoString()

	// entity/ is where entity types (User, Post, etc.) live — filter
	// referencing them would be the direct filter → entity edge that
	// makes the inverse entity → filter → ... cycle impossible.
	if strings.Contains(src, `"example/ent/entity"`) || strings.Contains(src, `example/ent/entity.`) {
		t.Errorf("filter/%s.go imports entity/ — re-opens the cycle-break "+
			"(entity cannot import filter if filter already imports entity)",
			strings.ToLower(userType.Name))
	}
	// client/{entity}/ holds mutation/builder types — filter has no
	// business touching them. If it ever does, the filter → client → entity
	// chain would also re-introduce the cycle.
	if strings.Contains(src, `"example/ent/client/`) {
		t.Errorf("filter/%s.go imports client/{entity}/ — filter must stay "+
			"independent of the heavy generator outputs to preserve the cycle break",
			strings.ToLower(userType.Name))
	}
}

func TestGolden_Node(t *testing.T) {
	t.Parallel()
	g, userType := goldenTestType()
	f := g.genEntityNode(userType)
	require.NotNil(t, f)
	checkGoldenFile(t, filepath.Join("testdata", "golden", "node.go"), f.GoString())
}

// SDL golden tests

func TestGolden_SDL_FullSchema(t *testing.T) {
	t.Parallel()
	g, _ := goldenTestType()
	sdl := g.genFullSchema()
	checkGoldenFile(t, filepath.Join("testdata", "golden", "schema.graphql"), sdl)
}

func TestGolden_SDL_CreateInput(t *testing.T) {
	t.Parallel()
	g, userType := goldenTestType()
	sdl := g.genCreateInput(userType)
	checkGoldenFile(t, filepath.Join("testdata", "golden", "create_input.graphql"), sdl)
}
