package sql

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

var updateGolden = flag.Bool("update-golden", false, "update golden files")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// goldenTestType builds a deterministic User type for golden file tests.
// Any changes to this function will require regenerating all golden files.
func goldenTestType() (*featureMockHelper, *gen.Type) {
	h := newFeatureMockHelper()

	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
		createTestField("age", field.TypeInt),
		createOptionalField("bio", field.TypeString),
		createNillableField("nickname", field.TypeString),
	})

	postType := createTestTypeWithFields("Post", []*gen.Field{
		createTestField("title", field.TypeString),
	})

	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	return h, userType
}

func checkGolden(t *testing.T, goldenPath, code string) {
	t.Helper()
	formatted := formatGolden(t, goldenPath, code)

	if *updateGolden {
		err := os.MkdirAll(filepath.Dir(goldenPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(goldenPath, formatted, 0o644)
		require.NoError(t, err)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if os.IsNotExist(err) {
		t.Fatalf("golden file %s does not exist, run with -update-golden to create", goldenPath)
	}
	require.NoError(t, err)
	assert.Equal(t, string(expected), string(formatted))
}

// formatGolden applies the same goimports post-processing that gen.writeFile
// applies to on-disk files. Golden tests operate on f.GoString() which
// bypasses writeFile, so without this the golden layout diverges from what
// users actually see on disk.
func formatGolden(t *testing.T, goldenPath, code string) []byte {
	t.Helper()
	if filepath.Ext(goldenPath) != ".go" {
		return []byte(code)
	}
	formatted, err := gen.FormatGoBytes(goldenPath, []byte(code))
	require.NoError(t, err)
	return formatted
}

func TestGolden_Mutation(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genMutation(h, userType)
	checkGolden(t, filepath.Join("testdata", "golden", "mutation.go"), file.GoString())
}

func TestGolden_EntityClient(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genEntityClient(h, userType)
	checkGolden(t, filepath.Join("testdata", "golden", "entity_client.go"), file.GoString())
}

func TestGolden_Predicate(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genPredicate(h, userType)
	checkGolden(t, filepath.Join("testdata", "golden", "predicate.go"), file.GoString())
}

func TestGolden_Package(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genPackage(h, userType, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "package.go"), file.GoString())
}

func TestGolden_Client(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genClient(h)
	checkGolden(t, filepath.Join("testdata", "golden", "client.go"), file.GoString())
}

func TestGolden_Velox(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genVelox(h)
	checkGolden(t, filepath.Join("testdata", "golden", "velox.go"), file.GoString())
}

func TestGolden_Errors(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genErrors(h)
	checkGolden(t, filepath.Join("testdata", "golden", "errors.go"), file.GoString())
}

func TestGolden_Types(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genTypes(h)
	checkGolden(t, filepath.Join("testdata", "golden", "types.go"), file.GoString())
}

func TestGolden_Tx(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genTx(h)
	checkGolden(t, filepath.Join("testdata", "golden", "tx.go"), file.GoString())
}

func TestGolden_PredicatePackage(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genPredicatePackage(h)
	checkGolden(t, filepath.Join("testdata", "golden", "predicate_package.go"), file.GoString())
}

// goldenTestTypeWithFeatures builds the same deterministic schema as goldenTestType
// but with all feature flags enabled for testing feature generators.
func goldenTestTypeWithFeatures() *featureMockHelper {
	h, _ := goldenTestType()
	h.withFeatures("intercept", "privacy", "snapshot", "globalid", "entql")
	return h
}

func TestGolden_Intercept(t *testing.T) {
	t.Parallel()
	h := goldenTestTypeWithFeatures()
	file := genIntercept(h)
	checkGolden(t, filepath.Join("testdata", "golden", "intercept.go"), file.GoString())
}

func TestGolden_Privacy(t *testing.T) {
	t.Parallel()
	h := goldenTestTypeWithFeatures()
	file := genPrivacy(h)
	checkGolden(t, filepath.Join("testdata", "golden", "privacy.go"), file.GoString())
}

func TestGolden_Snapshot(t *testing.T) {
	t.Parallel()
	h := goldenTestTypeWithFeatures()
	file := genSnapshot(h)
	checkGolden(t, filepath.Join("testdata", "golden", "snapshot.go"), file.GoString())
}

func TestGolden_GlobalID(t *testing.T) {
	t.Parallel()
	h := goldenTestTypeWithFeatures()
	file := genGlobalID(h)
	checkGolden(t, filepath.Join("testdata", "golden", "globalid.go"), file.GoString())
}

func TestGolden_EntQL(t *testing.T) {
	t.Parallel()
	h := goldenTestTypeWithFeatures()
	file := genEntQL(h)
	checkGolden(t, filepath.Join("testdata", "golden", "entql.go"), file.GoString())
}

func TestGolden_Hook(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genHook(h)
	require.NotNil(t, file)
	checkGolden(t, filepath.Join("testdata", "golden", "hook.go"), file.GoString())
}

func TestGolden_Meta(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genRuntimeCombined(h, h.graph.Nodes)
	checkGolden(t, filepath.Join("testdata", "golden", "meta.go"), file.GoString())
}

func TestGolden_EntityRuntime(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genEntityRuntime(h, userType)
	checkGolden(t, filepath.Join("testdata", "golden", "entity_runtime.go"), file.GoString())
}

// =============================================================================
// Golden tests for CRUD generators (previously uncovered)
// =============================================================================

func TestGolden_Create(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	h.withFeatures("upsert")
	file, err := genCreate(h, userType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "create.go"), file.GoString())
}

func TestGolden_Update(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file, err := genUpdate(h, userType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "update.go"), file.GoString())
}

func TestGolden_Delete(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file, err := genDelete(h, userType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "delete.go"), file.GoString())
}

func TestGolden_QueryPkg(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genQueryPkg(h, userType, h.graph.Nodes, h.SharedEntityPkg())
	checkGolden(t, filepath.Join("testdata", "golden", "query_pkg.go"), file.GoString())
}

// =============================================================================
// Golden tests with diverse fixtures (M9: fixture variety)
// =============================================================================

// goldenTestTypeEnum builds a type with enum fields for golden file tests.
func goldenTestTypeEnum() (*featureMockHelper, *gen.Type) {
	h := newFeatureMockHelper()
	t := createTestTypeWithFields("Task", []*gen.Field{
		createTestField("title", field.TypeString),
		createEnumField("status", []string{"pending", "active", "done"}),
		createEnumField("priority", []string{"low", "medium", "high", "critical"}),
	})
	h.graph.Nodes = []*gen.Type{t}
	return h, t
}

func TestGolden_Enum_Mutation(t *testing.T) {
	t.Parallel()
	h, taskType := goldenTestTypeEnum()
	file := genMutation(h, taskType)
	checkGolden(t, filepath.Join("testdata", "golden", "enum_mutation.go"), file.GoString())
}

func TestGolden_Enum_Package(t *testing.T) {
	t.Parallel()
	h, taskType := goldenTestTypeEnum()
	file := genPackage(h, taskType, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "enum_package.go"), file.GoString())
}

func TestGolden_Enum_Predicate(t *testing.T) {
	t.Parallel()
	h, taskType := goldenTestTypeEnum()
	file := genPredicate(h, taskType)
	checkGolden(t, filepath.Join("testdata", "golden", "enum_predicate.go"), file.GoString())
}

// goldenTestTypeSelfRef builds a type with self-referential edges.
func goldenTestTypeSelfRef() (*featureMockHelper, *gen.Type) {
	h := newFeatureMockHelper()

	employeeType := createTestTypeWithFields("Employee", []*gen.Field{
		createTestField("name", field.TypeString),
	})

	// Employee -> Employee (manager, O2M/M2O self-ref)
	subordinates := createO2MEdge("subordinates", employeeType, "employees", "manager_id")
	manager := createM2OEdge("manager", employeeType, "employees", "manager_id")
	manager.Inverse = "subordinates"

	employeeType.Edges = []*gen.Edge{subordinates, manager}
	h.graph.Nodes = []*gen.Type{employeeType}

	return h, employeeType
}

func TestGolden_SelfRef_Mutation(t *testing.T) {
	t.Parallel()
	h, empType := goldenTestTypeSelfRef()
	file := genMutation(h, empType)
	checkGolden(t, filepath.Join("testdata", "golden", "selfref_mutation.go"), file.GoString())
}

// goldenTestTypeUUID builds a type with UUID ID for golden file tests.
func goldenTestTypeUUID() (*featureMockHelper, *gen.Type) {
	h := newFeatureMockHelper()

	t := &gen.Type{
		Name: "Device",
		Config: &gen.Config{
			Package: "github.com/test/project/ent",
			Target:  "/tmp/ent",
		},
		ID: &gen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeUUID},
		},
		Fields: []*gen.Field{
			createTestField("serial", field.TypeString),
			createImmutableField("model", field.TypeString),
		},
	}
	h.graph.Nodes = []*gen.Type{t}
	return h, t
}

func TestGolden_UUID_Predicate(t *testing.T) {
	t.Parallel()
	h, devType := goldenTestTypeUUID()
	file := genPredicate(h, devType)
	checkGolden(t, filepath.Join("testdata", "golden", "uuid_predicate.go"), file.GoString())
}

// =============================================================================
// Golden tests for entity_pkg and filter generators
// =============================================================================

func TestGolden_EntityPkg(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	file := genEntityPkgFileWithRegistry(h, userType, h.graph.Nodes, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "entity_pkg.go"), file.GoString())
}

func TestGolden_EntityPkg_Post(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	postType := h.graph.Nodes[1] // Post type
	file := genEntityPkgFileWithRegistry(h, postType, h.graph.Nodes, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "entity_pkg_post.go"), file.GoString())
}

func TestGolden_Filter(t *testing.T) {
	t.Parallel()
	h, userType := goldenTestType()
	h.withFeatures("privacy")
	file := genFilter(h, userType)
	checkGolden(t, filepath.Join("testdata", "golden", "filter.go"), file.GoString())
}

func TestGolden_EntityHooks(t *testing.T) {
	t.Parallel()
	h, _ := goldenTestType()
	file := genEntityHooks(h)
	checkGolden(t, filepath.Join("testdata", "golden", "entity_hooks.go"), file.GoString())
}

// =============================================================================
// Golden tests with bidirectional edges (O2M + M2O back-ref)
// =============================================================================

// goldenTestTypeBidi builds User+Post with bidirectional edges:
//   - User.posts (O2M) → Post
//   - Post.author (M2O) → User (inverse of posts)
func goldenTestTypeBidi() (*featureMockHelper, *gen.Type, *gen.Type) {
	h := newFeatureMockHelper()

	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
	})
	postType := createTestTypeWithFields("Post", []*gen.Field{
		createTestField("title", field.TypeString),
		createTestField("content", field.TypeString),
	})

	// User → Post (O2M, forward)
	postsEdge := createO2MEdge("posts", postType, "posts", "user_posts")
	userType.Edges = []*gen.Edge{postsEdge}

	// Post → User (M2O, inverse of "posts")
	authorEdge := createM2OEdge("author", userType, "posts", "user_posts")
	authorEdge.Inverse = "posts"
	postType.Edges = []*gen.Edge{authorEdge}

	h.graph.Nodes = []*gen.Type{userType, postType}
	return h, userType, postType
}

func TestGolden_Bidi_Create(t *testing.T) {
	t.Parallel()
	h, _, postType := goldenTestTypeBidi()
	h.withFeatures("upsert")
	file, err := genCreate(h, postType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "bidi_create.go"), file.GoString())
}

func TestGolden_Bidi_Update(t *testing.T) {
	t.Parallel()
	h, _, postType := goldenTestTypeBidi()
	file, err := genUpdate(h, postType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "bidi_update.go"), file.GoString())
}

func TestGolden_Bidi_Mutation(t *testing.T) {
	t.Parallel()
	h, _, postType := goldenTestTypeBidi()
	file := genMutation(h, postType)
	checkGolden(t, filepath.Join("testdata", "golden", "bidi_mutation.go"), file.GoString())
}

func TestGolden_Bidi_QueryPkg(t *testing.T) {
	t.Parallel()
	h, _, postType := goldenTestTypeBidi()
	file := genQueryPkg(h, postType, h.graph.Nodes, h.SharedEntityPkg())
	checkGolden(t, filepath.Join("testdata", "golden", "bidi_query_pkg.go"), file.GoString())
}

func TestGolden_Bidi_EntityPkg(t *testing.T) {
	t.Parallel()
	h, _, postType := goldenTestTypeBidi()
	file := genEntityPkgFileWithRegistry(h, postType, h.graph.Nodes, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "bidi_entity_pkg.go"), file.GoString())
}

// =============================================================================
// Golden tests with rich fixture (defaults, JSON, required edges)
// =============================================================================

// goldenTestTypeRich builds a schema with features commonly used in production:
//   - Default values and update-defaults (simulating mixin.Time)
//   - JSON slice field
//   - Optional + nillable field
//   - Required M2O edge (author)
//   - Enum field
func goldenTestTypeRich() (*featureMockHelper, *gen.Type, *gen.Type) {
	h := newFeatureMockHelper()

	authorType := createTestTypeWithFields("Author", []*gen.Field{
		createTestField("name", field.TypeString),
	})

	articleType := createTestTypeWithFields("Article", []*gen.Field{
		createTestField("title", field.TypeString),
		{
			Name:     "content",
			Type:     &field.TypeInfo{Type: field.TypeString},
			Optional: true,
			Nillable: true,
		},
		{
			Name: "created_at",
			Type: &field.TypeInfo{Type: field.TypeTime},
			// Simulates mixin.Time: has creation default
			Default: true,
		},
		{
			Name: "updated_at",
			Type: &field.TypeInfo{Type: field.TypeTime},
			// Simulates mixin.Time: has both creation and update default
			Default:       true,
			UpdateDefault: true,
		},
		{
			Name:     "tags",
			Type:     &field.TypeInfo{Type: field.TypeJSON},
			Optional: true,
		},
		createEnumField("status", []string{"draft", "review", "published"}),
	})

	// M2O edge: article has an author (not optional)
	authorEdge := createM2OEdge("author", authorType, "articles", "author_id")
	articleType.Edges = []*gen.Edge{authorEdge}

	// Author has O2M back to articles
	articlesEdge := createO2MEdge("articles", articleType, "articles", "author_id")
	authorType.Edges = []*gen.Edge{articlesEdge}

	h.graph.Nodes = []*gen.Type{authorType, articleType}
	return h, authorType, articleType
}

func TestGolden_Rich_Create(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	h.withFeatures("upsert")
	file, err := genCreate(h, articleType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "rich_create.go"), file.GoString())
}

func TestGolden_Rich_Update(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	file, err := genUpdate(h, articleType)
	require.NoError(t, err)
	checkGolden(t, filepath.Join("testdata", "golden", "rich_update.go"), file.GoString())
}

func TestGolden_Rich_Mutation(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	file := genMutation(h, articleType)
	checkGolden(t, filepath.Join("testdata", "golden", "rich_mutation.go"), file.GoString())
}

func TestGolden_Rich_Package(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	file := genPackage(h, articleType, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "rich_package.go"), file.GoString())
}

func TestGolden_Rich_EntityPkg(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	file := genEntityPkgFileWithRegistry(h, articleType, h.graph.Nodes, buildEntityPkgEnumRegistry(h.graph.Nodes))
	checkGolden(t, filepath.Join("testdata", "golden", "rich_entity_pkg.go"), file.GoString())
}

func TestGolden_Rich_QueryPkg(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	file := genQueryPkg(h, articleType, h.graph.Nodes, h.SharedEntityPkg())
	checkGolden(t, filepath.Join("testdata", "golden", "rich_query_pkg.go"), file.GoString())
}

func TestGolden_Rich_Predicate(t *testing.T) {
	t.Parallel()
	h, _, articleType := goldenTestTypeRich()
	file := genPredicate(h, articleType)
	checkGolden(t, filepath.Join("testdata", "golden", "rich_predicate.go"), file.GoString())
}
