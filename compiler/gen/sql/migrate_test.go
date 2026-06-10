package sql

import (
	"fmt"
	"go/parser"
	"go/token"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"
)

// TestGenMigrateSchema_OnDeleteAnnotationCompilesAndRendersConstName guards the
// FULL migrate generation path (not just the isolated deleteAction): an M2O
// edge carrying an OnDelete annotation must produce a ForeignKey whose OnDelete
// is the Go constant (schema.Cascade) and the whole generated file must be
// valid Go. This catches both failure modes the prior bug had — an undefined
// identifier (schema.CASCADE, syntactically valid so caught by the string
// assertion) and invalid syntax (schema.SET NULL, caught by the parser). The
// path was unguarded because no test schema used an explicit OnDelete.
func TestGenMigrateSchema_OnDeleteAnnotationCompilesAndRendersConstName(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	postType.Edges = []*gen.Edge{{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
		Annotations: gen.Annotations{
			sqlschema.AnnotationName: sqlschema.Annotation{OnDelete: sqlschema.Cascade},
		},
	}}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	code := genMigrateSchema(helper).GoString()
	assert.Contains(t, code, "schema.Cascade",
		"FK OnDelete must render the Go constant schema.Cascade")
	assert.NotContains(t, code, "schema.CASCADE",
		"FK OnDelete must NOT emit the SQL literal schema.CASCADE (undefined identifier)")
	_, err := parser.ParseFile(token.NewFileSet(), "schema.go", code, parser.AllErrors)
	require.NoError(t, err, "generated migrate schema must be valid Go syntax")
}

// TestGenMigrateSchema_OnDeleteOnAssocEdgeRendersOnM2OSide guards the Ent-style
// placement of the OnDelete annotation. velox emits the FK from the M2O /
// FK-owning edge, but Ent (and users porting an Ent schema) declare the
// referential action on the assoc edge (the parent's edge.To). The annotation
// then lives on the M2O edge's paired edge (e.Ref), NOT on the M2O edge itself.
// Without honoring e.Ref the cascade is silently dropped — the CASCADE-class
// data-integrity divergence the parity migration-DDL guard surfaced. This pins
// the full generator path: assoc-side annotation must still render schema.Cascade
// on the M2O foreign key.
func TestGenMigrateSchema_OnDeleteOnAssocEdgeRendersOnM2OSide(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	postType.Edges = []*gen.Edge{{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
		// No annotation on the M2O edge itself — it lives on the paired assoc
		// edge (the parent's edge.To), mirroring Ent's placement convention.
		Ref: &gen.Edge{
			Name: "posts",
			Type: postType,
			Rel:  gen.Relation{Type: gen.O2M},
			Annotations: gen.Annotations{
				sqlschema.AnnotationName: sqlschema.Annotation{OnDelete: sqlschema.Cascade},
			},
		},
	}}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	code := genMigrateSchema(helper).GoString()
	assert.Contains(t, code, "schema.Cascade",
		"assoc-side (e.Ref) OnDelete annotation must render schema.Cascade on the M2O FK")
	_, err := parser.ParseFile(token.NewFileSet(), "schema.go", code, parser.AllErrors)
	require.NoError(t, err, "generated migrate schema must be valid Go syntax")
}

// TestDeleteAction_OnDeleteAnnotationRendersConstName pins that an explicit
// OnDelete annotation renders to the Go CONSTANT name (schema.Cascade), not the
// SQL literal value (schema.CASCADE — undefined; or schema.SET NULL — invalid
// Go). Regression for a build break a downstream consumer hit: migrate.go used
// string(ant.OnDelete) instead of ant.OnDelete.ConstName(). The pre-existing
// deleteAction tests only asserted the result was non-nil, so they never
// rendered the output and missed this.
func TestDeleteAction_OnDeleteAnnotationRendersConstName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action     sqlschema.CascadeAction
		wantConst  string // the Go constant name that must be emitted
		badLiteral string // the SQL literal it must NOT emit
	}{
		{sqlschema.Cascade, "Cascade", "CASCADE"},
		{sqlschema.SetNull, "SetNull", "SET NULL"},
		{sqlschema.Restrict, "Restrict", "RESTRICT"},
		{sqlschema.NoAction, "NoAction", "NO ACTION"},
		{sqlschema.SetDefault, "SetDefault", "SET DEFAULT"},
	}
	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			edge := &gen.Edge{
				Name: "children",
				Annotations: gen.Annotations{
					sqlschema.AnnotationName: sqlschema.Annotation{OnDelete: tc.action},
				},
			}
			rendered := fmt.Sprintf("%#v", deleteAction(edge))
			assert.Contains(t, rendered, "schema."+tc.wantConst,
				"OnDelete %q must render the Go constant schema.%s", tc.action, tc.wantConst)
			assert.NotContains(t, rendered, "schema."+tc.badLiteral,
				"OnDelete %q must NOT emit the SQL literal schema.%s (undefined / invalid Go)", tc.action, tc.badLiteral)
		})
	}
}

// =============================================================================
// genMigrate Tests
// =============================================================================

func TestGenMigrate_ReturnsTwoFiles(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	files := genMigrate(helper)
	assert.NotNil(t, files.Schema)
	assert.NotNil(t, files.Migrate)
}

// =============================================================================
// genMigrateSchema Tests
// =============================================================================

func TestGenMigrateSchema_BasicEntity(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Code generated by velox. DO NOT EDIT.")
	assert.Contains(t, code, "package migrate")
	assert.Contains(t, code, "UserColumns")
	assert.Contains(t, code, "UserTable")
	assert.Contains(t, code, "func init()")
}

func TestGenMigrateSchema_MultipleEntities(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserColumns")
	assert.Contains(t, code, "PostColumns")
	assert.Contains(t, code, "CommentColumns")
	assert.Contains(t, code, "UserTable")
	assert.Contains(t, code, "PostTable")
	assert.Contains(t, code, "CommentTable")
}

func TestGenMigrateSchema_WithEdges(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	postType.Edges = []*gen.Edge{{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
	}}
	userType.Edges = []*gen.Edge{{
		Name:   "posts",
		Type:   postType,
		Unique: false,
		Rel: gen.Relation{
			Type:    gen.O2M,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
	}}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "PostTable")
	assert.Contains(t, code, "ForeignKeys")
	assert.Contains(t, code, "func init()")
}

// TestGenMigrateSchema_GofmtSimplifiedLiterals pins that the migrate schema
// output is already gofmt -s canonical: slice elements use bare composite
// literals ({...}), never the redundant &schema.Column{...} /
// &schema.ForeignKey{...} forms. The repo formats with gofmt -s (regen.sh's
// final pass, the style rule, golangci's gofmt simplify) — un-simplified
// generator output makes the format pass and the next regeneration ping-pong
// the same files forever, defeating the write-if-changed mtime stability.
// Covers both the entity-table path (columns + FK slices) and the M2M
// join-table path (its own literal emission sites).
func TestGenMigrateSchema_GofmtSimplifiedLiterals(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	postType.Edges = []*gen.Edge{{
		Name: "tags", Type: tagType, Unique: false,
		Rel: gen.Relation{Type: gen.M2M, Table: "post_tags", Columns: []string{"post_id", "tag_id"}},
	}}
	tagType.Edges = []*gen.Edge{{
		Name: "posts", Type: postType, Unique: false, Inverse: "tags",
		Rel: gen.Relation{Type: gen.M2M, Table: "post_tags", Columns: []string{"post_id", "tag_id"}},
	}}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)
	code := file.GoString()

	require.Contains(t, code, "PostTagsColumns", "fixture must reach the join-table path")
	assert.NotContains(t, code, "&schema.Column{",
		"column slice elements must be bare {...} literals (gofmt -s form)")
	assert.NotContains(t, code, "&schema.ForeignKey{",
		"foreign-key slice elements must be bare {...} literals (gofmt -s form)")
}

func TestGenMigrateSchema_WithM2OEdgeAndFK(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// M2O edge on post referencing user, with FK column
	m2oEdge := &gen.Edge{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
	}
	postType.Edges = []*gen.Edge{m2oEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Check that FK columns and init() references are generated
	assert.Contains(t, code, "PostColumns")
	assert.Contains(t, code, "PostTable")
	assert.Contains(t, code, "ForeignKeys")
	assert.Contains(t, code, "func init()")
	assert.Contains(t, code, "RefTable")
}

func TestGenMigrateSchema_WithO2OEdge(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")

	// O2O edge on profile referencing user, profile owns FK
	o2oEdge := &gen.Edge{
		Name:   "owner",
		Type:   userType,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.O2O,
			Table:   "profiles",
			Columns: []string{"owner_id"},
		},
	}
	profileType.Edges = []*gen.Edge{o2oEdge}
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "ProfileColumns")
	assert.Contains(t, code, "ProfileTable")
}

func TestGenMigrateSchema_WithFields(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
		createTestField("age", field.TypeInt),
		createNillableField("bio", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserColumns")
	assert.Contains(t, code, "Tables")
}

func TestGenMigrateSchema_WithSchemaFields(t *testing.T) {
	t.Parallel()
	// Use createTestTypeWithSchema to get proper field.typ set,
	// so Column() doesn't panic on field.sqlComment()
	helper := newMockHelper()
	userType := createTestTypeWithSchema(t, "User", &load.Schema{
		Fields: []*load.Field{
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			{Name: "email", Info: &field.TypeInfo{Type: field.TypeString}},
			{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}},
		},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserColumns")
	assert.Contains(t, code, "UserTable")
	assert.Contains(t, code, "Tables")
}

func TestGenMigrateSchema_WithM2OEdgeAndFKInit(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithSchema(t, "User", &load.Schema{})
	postType := createTestTypeWithSchema(t, "Post", &load.Schema{})

	// M2O edge with Rel.Columns set so edgeFKColumn returns non-nil
	// and the init() body generates FK ref table assignments
	m2oEdge := &gen.Edge{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
	}
	postType.Edges = []*gen.Edge{m2oEdge}
	// Also add the O2M reverse edge on user
	o2mEdge := &gen.Edge{
		Name:   "posts",
		Type:   postType,
		Unique: false,
		Rel: gen.Relation{
			Type:    gen.O2M,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
	}
	userType.Edges = []*gen.Edge{o2mEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// The init() should contain FK ref table assignment
	assert.Contains(t, code, "RefTable")
	assert.Contains(t, code, "PostTable")
	assert.Contains(t, code, "UserTable")
	assert.Contains(t, code, "ForeignKeys")
}

func TestGenMigrateSchema_MultipleM2OEdges(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithSchema(t, "User", &load.Schema{})
	categoryType := createTestTypeWithSchema(t, "Category", &load.Schema{})
	postType := createTestTypeWithSchema(t, "Post", &load.Schema{})

	// Post has two M2O edges: author (User) and category (Category)
	postType.Edges = []*gen.Edge{
		{
			Name:   "author",
			Type:   userType,
			Unique: true,
			Rel: gen.Relation{
				Type:    gen.M2O,
				Table:   "posts",
				Columns: []string{"author_id"},
			},
		},
		{
			Name:   "category",
			Type:   categoryType,
			Unique: true,
			Rel: gen.Relation{
				Type:    gen.M2O,
				Table:   "posts",
				Columns: []string{"category_id"},
			},
		},
	}
	helper.graph.Nodes = []*gen.Type{userType, categoryType, postType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "PostColumns")
	assert.Contains(t, code, "PostTable")
	assert.Contains(t, code, "RefTable")
}

func TestGenMigrateSchema_O2OEdgeInverseOwnFK(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithSchema(t, "User", &load.Schema{})
	profileType := createTestTypeWithSchema(t, "Profile", &load.Schema{})

	// O2O edge where profile owns FK (inverse side holds the FK in Velox convention)
	profileType.Edges = []*gen.Edge{
		{
			Name:    "owner",
			Inverse: "profile",
			Type:    userType,
			Unique:  true,
			Rel: gen.Relation{
				Type:    gen.O2O,
				Table:   "profiles",
				Columns: []string{"owner_id"},
			},
		},
	}
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "ProfileTable")
	assert.Contains(t, code, "ForeignKeys")
	assert.Contains(t, code, "RefTable")
}

func TestGenMigrateSchema_O2OBidiOwnFK(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithSchema(t, "User", &load.Schema{})

	// O2O bidi edge (self-referential, like User.spouse)
	userType.Edges = []*gen.Edge{
		{
			Name:   "spouse",
			Type:   userType,
			Unique: true,
			Bidi:   true,
			Rel: gen.Relation{
				Type:    gen.O2O,
				Table:   "users",
				Columns: []string{"spouse_id"},
			},
		},
	}
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserTable")
	assert.Contains(t, code, "spouse_id")
}

func TestGenMigrateSchema_WithIndexes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Indexes = []*gen.Index{
		{Name: "user_email_idx", Unique: true, Columns: []string{"email"}},
	}
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Indexes")
}

func TestGenMigrateSchema_PartialIndexWhereClause(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	// Simulate index.Fields("email").Unique().Annotations(sqlschema.IndexWhere("deleted_at IS NULL"))
	// Annotations are stored as map[string]any keyed by annotation.Name() ("sqlindex").
	userType.Indexes = []*gen.Index{{
		Name:    "user_email_unique_active",
		Unique:  true,
		Columns: []string{"email"},
		Annotations: gen.Annotations{
			"sqlindex": map[string]any{"Where": "deleted_at IS NULL"},
		},
	}}
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMigrateSchema(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Annotation")
	assert.Contains(t, code, "IndexAnnotation")
	assert.Contains(t, code, "deleted_at IS NULL")
}

func TestGenIndexAnnotationDict_AllScalarFields(t *testing.T) {
	t.Parallel()
	const sqlschemaPkg = "github.com/syssam/velox/dialect/sqlschema"
	ant := &sqlschema.IndexAnnotation{
		Where:         "status = 'active'",
		Type:          "GIN",
		StorageParams: "fillfactor=90",
		Desc:          true,
		OpClass:       "gin_trgm_ops",
		Prefix:        10,
	}
	d := genIndexAnnotationDict(ant)
	require.NotEmpty(t, d)

	f := jen.NewFile("test")
	f.Var().Id("x").Op("=").Op("&").Qual(sqlschemaPkg, "IndexAnnotation").Values(d)
	code := f.GoString()
	assert.Contains(t, code, "status = 'active'")
	assert.Contains(t, code, "GIN")
	assert.Contains(t, code, "fillfactor=90")
	assert.Contains(t, code, "true")
	assert.Contains(t, code, "gin_trgm_ops")
	assert.Contains(t, code, "Prefix")
}

func TestGenIndexAnnotationDict_MapFields(t *testing.T) {
	t.Parallel()
	const sqlschemaPkg = "github.com/syssam/velox/dialect/sqlschema"
	ant := &sqlschema.IndexAnnotation{
		Types:          map[string]string{"postgres": "GIN"},
		DescColumns:    map[string]bool{"created_at": true},
		OpClassColumns: map[string]string{"name": "gin_trgm_ops"},
		PrefixColumns:  map[string]uint{"title": 5},
		IncludeColumns: []string{"id", "name"},
	}
	d := genIndexAnnotationDict(ant)
	require.NotEmpty(t, d)

	f := jen.NewFile("test")
	f.Var().Id("x").Op("=").Op("&").Qual(sqlschemaPkg, "IndexAnnotation").Values(d)
	code := f.GoString()
	assert.Contains(t, code, "postgres")
	assert.Contains(t, code, "created_at")
	assert.Contains(t, code, "gin_trgm_ops")
	assert.Contains(t, code, "title")
	assert.Contains(t, code, "id")
	assert.Contains(t, code, "name")
}

func TestGenIndexAnnotationDict_EmptyAnnotation(t *testing.T) {
	t.Parallel()
	d := genIndexAnnotationDict(&sqlschema.IndexAnnotation{})
	assert.Empty(t, d)
}

// =============================================================================
// genMigrateMigrate Tests
// =============================================================================

func TestGenMigrateMigrate_BasicEntity(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genMigrateMigrate(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Code generated by velox. DO NOT EDIT.")
	assert.Contains(t, code, "package migrate")
	assert.Contains(t, code, "type Schema struct")
	assert.Contains(t, code, "drv dialect.Driver")
	assert.Contains(t, code, "func NewSchema(")
	assert.Contains(t, code, "return &Schema{")
	assert.Contains(t, code, "func (s *Schema) Create(")
	assert.Contains(t, code, "ctx context.Context")
	assert.Contains(t, code, "opts ...schema.MigrateOption")
	assert.Contains(t, code, "return Create(ctx, s, Tables, opts...)")
	assert.Contains(t, code, "func Create(")
	assert.Contains(t, code, "tables []*schema.Table")
	assert.Contains(t, code, "func (s *Schema) WriteTo(")
	assert.Contains(t, code, "w io.Writer")
	assert.Contains(t, code, "schema.WriteDriver")
}

func TestGenMigrateMigrate_SchemaOptions(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genMigrateMigrate(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "WithGlobalUniqueID")
	assert.Contains(t, code, "WithDropColumn")
	assert.Contains(t, code, "WithDropIndex")
	assert.Contains(t, code, "WithForeignKeys")
}

func TestGenMigrateMigrate_ErrorHandling(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genMigrateMigrate(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "if err != nil")
	assert.Contains(t, code, `fmt.Errorf("velox/migrate: %w", err)`)
	assert.Contains(t, code, "migrate.Create(ctx, tables...)")
}

func TestGenMigrateMigrate_WriteDriverUsage(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genMigrateMigrate(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "schema.WriteDriver{")
	assert.Contains(t, code, "Writer: w")
	assert.Contains(t, code, "Driver: s.drv")
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestPascal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "User"},
		{"post", "Post"},
		{"comment", "Comment"},
		{"", ""},
		{"a", "A"},
		{"User", "User"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, pascal(tt.input))
		})
	}
}

func TestFieldTypeCode(t *testing.T) {
	t.Parallel()
	fieldPkg := "github.com/syssam/velox/schema/field"

	tests := []field.Type{
		field.TypeBool, field.TypeTime, field.TypeJSON, field.TypeUUID,
		field.TypeBytes, field.TypeEnum, field.TypeString, field.TypeInt,
		field.TypeInt64, field.TypeFloat64, field.TypeOther,
		field.TypeInt8, field.TypeInt16, field.TypeInt32,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64,
		field.TypeFloat32,
	}

	for _, typ := range tests {
		t.Run(typ.String(), func(t *testing.T) {
			code := fieldTypeCode(typ, fieldPkg)
			assert.NotNil(t, code)
		})
	}
}

func TestDeleteAction(t *testing.T) {
	t.Parallel()
	t.Run("optional_edge_defaults_to_SetNull", func(t *testing.T) {
		// A nullable (optional) FK with no explicit annotation defaults to SET NULL,
		// matching Ent. Assert the RENDERED action — the prior non-nil check could
		// not tell SetNull from any other action, leaving this default unpinned.
		edge := &gen.Edge{Name: "profile", Optional: true}
		rendered := fmt.Sprintf("%#v", deleteAction(edge))
		assert.Contains(t, rendered, "schema.SetNull",
			"optional/nullable FK defaults to SetNull (matches Ent)")
	})

	t.Run("required_edge_defaults_to_NoAction", func(t *testing.T) {
		// A required (non-nullable) FK with no annotation defaults to NO ACTION,
		// matching Ent — NOT Cascade. The prior subtest was misnamed
		// ("..._to_Cascade") and only checked non-nil, so it asserted nothing and
		// the wrong name went unnoticed.
		edge := &gen.Edge{Name: "author", Optional: false}
		rendered := fmt.Sprintf("%#v", deleteAction(edge))
		assert.Contains(t, rendered, "schema.NoAction",
			"required/non-nullable FK defaults to NoAction (matches Ent)")
		assert.NotContains(t, rendered, "schema.Cascade",
			"required FK must NOT default to Cascade")
	})
}

func TestEdgeFKColumn(t *testing.T) {
	t.Parallel()
	t.Run("M2O_edge_generates_FK_column", func(t *testing.T) {
		userType := createTestType("User")
		postType := createTestType("Post")

		edge := &gen.Edge{
			Name: "author", Type: userType, Unique: true,
			Rel: gen.Relation{Type: gen.M2O, Columns: []string{"author_id"}},
		}
		col := edgeFKColumn(edge, postType)
		require.NotNil(t, col)
		assert.Equal(t, "author_id", col.Name)
		assert.Equal(t, field.TypeInt64, col.Type)
		assert.False(t, col.Nullable)
	})

	t.Run("O2M_edge_returns_nil", func(t *testing.T) {
		postType := createTestType("Post")
		edge := &gen.Edge{
			Name: "posts", Type: postType, Unique: false,
			Rel: gen.Relation{Type: gen.O2M},
		}
		col := edgeFKColumn(edge, createTestType("User"))
		assert.Nil(t, col)
	})

	t.Run("optional_edge_nullable_FK", func(t *testing.T) {
		userType := createTestType("User")
		edge := &gen.Edge{
			Name: "author", Type: userType, Unique: true, Optional: true,
			Rel: gen.Relation{Type: gen.M2O, Columns: []string{"author_id"}},
		}
		col := edgeFKColumn(edge, createTestType("Post"))
		require.NotNil(t, col)
		assert.True(t, col.Nullable)
	})

	t.Run("M2M_edge_returns_nil", func(t *testing.T) {
		tagType := createTestType("Tag")
		edge := &gen.Edge{
			Name: "tags", Type: tagType, Unique: false,
			Rel: gen.Relation{Type: gen.M2M},
		}
		col := edgeFKColumn(edge, createTestType("Post"))
		assert.Nil(t, col)
	})
}

func TestGenColumnDict(t *testing.T) {
	t.Parallel()
	fieldPkg := "github.com/syssam/velox/schema/field"

	t.Run("basic_column", func(t *testing.T) {
		col := &schema.Column{Name: "name", Type: field.TypeString}
		dict := genColumnDict(col, fieldPkg)
		require.NotNil(t, dict)
		assert.Len(t, dict, 2)
	})

	t.Run("unique_column", func(t *testing.T) {
		col := &schema.Column{Name: "email", Type: field.TypeString, Unique: true}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("nullable_column", func(t *testing.T) {
		col := &schema.Column{Name: "bio", Type: field.TypeString, Nullable: true}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("enum_column", func(t *testing.T) {
		col := &schema.Column{Name: "status", Type: field.TypeEnum, Enums: []string{"active", "inactive"}}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_size", func(t *testing.T) {
		col := &schema.Column{Name: "code", Type: field.TypeString, Size: 10}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_increment", func(t *testing.T) {
		col := &schema.Column{Name: "id", Type: field.TypeInt64, Increment: true}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_default_string", func(t *testing.T) {
		col := &schema.Column{Name: "role", Type: field.TypeString, Default: "user"}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_default_int", func(t *testing.T) {
		col := &schema.Column{Name: "age", Type: field.TypeInt, Default: 0}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_default_bool", func(t *testing.T) {
		col := &schema.Column{Name: "active", Type: field.TypeBool, Default: true}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_collation", func(t *testing.T) {
		col := &schema.Column{Name: "name", Type: field.TypeString, Collation: "utf8mb4_unicode_ci"}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_comment", func(t *testing.T) {
		col := &schema.Column{Name: "name", Type: field.TypeString, Comment: "user name"}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_schema_type", func(t *testing.T) {
		col := &schema.Column{
			Name:       "amount",
			Type:       field.TypeOther,
			SchemaType: map[string]string{"postgres": "DECIMAL(10,2)"},
		}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})

	t.Run("column_with_default_expr", func(t *testing.T) {
		col := &schema.Column{
			Name:    "created_at",
			Type:    field.TypeTime,
			Default: schema.Expr("CURRENT_TIMESTAMP"),
		}
		dict := genColumnDict(col, fieldPkg)
		assert.Greater(t, len(dict), 2)
	})
}

func TestFindColumnIndex(t *testing.T) {
	t.Parallel()
	typ := createTestType("User")

	t.Run("id_column", func(t *testing.T) {
		assert.Equal(t, 0, findColumnIndex(typ, "id"))
	})

	t.Run("first_field", func(t *testing.T) {
		assert.Equal(t, 1, findColumnIndex(typ, "name"))
	})

	t.Run("second_field", func(t *testing.T) {
		assert.Equal(t, 2, findColumnIndex(typ, "email"))
	})

	t.Run("third_field", func(t *testing.T) {
		assert.Equal(t, 3, findColumnIndex(typ, "age"))
	})

	t.Run("not_found_returns_negative_one", func(t *testing.T) {
		assert.Equal(t, -1, findColumnIndex(typ, "nonexistent"))
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestGenMigrate_FullWorkflow(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	postType.Edges = []*gen.Edge{{
		Name: "author", Type: userType, Unique: true,
		Rel: gen.Relation{
			Type:    gen.M2O,
			Table:   "posts",
			Columns: []string{"author_id"},
		},
	}}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	files := genMigrate(helper)
	require.NotNil(t, files.Schema)
	require.NotNil(t, files.Migrate)

	migrateCode := files.Migrate.GoString()
	assert.Contains(t, migrateCode, "type Schema struct")
	assert.Contains(t, migrateCode, "func NewSchema")
	assert.Contains(t, migrateCode, "func (s *Schema) Create")
	assert.Contains(t, migrateCode, "func (s *Schema) WriteTo")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenMigrateMigrate(b *testing.B) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}
	for b.Loop() {
		_ = genMigrateMigrate(helper)
	}
}

func BenchmarkPascal(b *testing.B) {
	inputs := []string{"user", "post", "comment", "profile", "tag"}
	for b.Loop() {
		for _, input := range inputs {
			_ = pascal(input)
		}
	}
}

// =============================================================================
// genPrimaryKey Tests
// =============================================================================

func TestGenPrimaryKey(t *testing.T) {
	t.Parallel()
	userType := createTestType("User")

	code := genPrimaryKey(userType, "UserColumns", migrateSchemaPkg)
	assert.NotNil(t, code)

	// Render the code to verify structure
	f := jen.NewFile("test")
	f.Var().Id("pk").Op("=").Add(code)
	output := f.GoString()
	assert.Contains(t, output, "UserColumns")
	assert.Contains(t, output, "schema")
}

// =============================================================================
// genForeignKeysSchema Tests
// =============================================================================

func TestGenForeignKeysSchema_NoEdges(t *testing.T) {
	t.Parallel()
	userType := createTestType("User")
	userType.Edges = nil

	code := genForeignKeysSchema(userType)
	assert.NotNil(t, code)

	f := jen.NewFile("test")
	f.Var().Id("fks").Op("=").Add(code)
	output := f.GoString()
	assert.Contains(t, output, "nil")
}

func TestGenForeignKeysSchema_WithM2OEdge(t *testing.T) {
	t.Parallel()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createM2OEdge("author", userType, "posts", "author_id")
	postType.Edges = []*gen.Edge{edge}

	code := genForeignKeysSchema(postType)
	assert.NotNil(t, code)

	f := jen.NewFile("test")
	f.Var().Id("fks").Op("=").Add(code)
	output := f.GoString()
	// Symbol format: {ownerTable}_{refTable}_{assocEdgeName} (matches graph_tables.go
	// fkSymbol and Ent). This M2O edge is standalone (no inverse), so the assoc name
	// is the edge's own name "author". A bidirectional M2O (inverse side) instead
	// uses e.Inverse — see TestGenForeignKeysSchema_BidiM2OUsesAssocName.
	assert.Contains(t, output, "Symbol")
	assert.Contains(t, output, "posts_users_author")
}

// TestGenForeignKeysSchema_BidiM2OUsesAssocName pins the assoc-name rule for the
// bidirectional case — the one the standalone WithM2OEdge test never exercised, so
// the M2O FK constraint symbol silently drifted from graph_tables.go and Ent. For a
// bidirectional O2M/M2O pair the FK is emitted from the M2O (inverse) side, but the
// symbol must use the assoc edge name (e.Inverse), not the M2O edge's own name.
func TestGenForeignKeysSchema_BidiM2OUsesAssocName(t *testing.T) {
	t.Parallel()
	postType := createTestType("Post")
	commentType := createTestType("Comment")
	// Comment.post: the M2O / FK-owning side of Post --O2M--> Comment. It is an
	// inverse edge whose assoc-edge name (on Post) is "comments".
	edge := createM2OEdge("post", postType, "comments", "post_comments")
	edge.Inverse = "comments"
	commentType.Edges = []*gen.Edge{edge}

	f := jen.NewFile("test")
	f.Var().Id("fks").Op("=").Add(genForeignKeysSchema(commentType))
	code := f.GoString()
	assert.Contains(t, code, "comments_posts_comments",
		"bidirectional M2O FK symbol must use the assoc edge name (matches graph_tables.go + Ent)")
	assert.NotContains(t, code, "comments_posts_post",
		"must NOT use the M2O edge name — that was the silent drift from the reference builder and Ent")
}

func TestGenForeignKeysSchema_SkipsO2M(t *testing.T) {
	t.Parallel()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{edge}

	code := genForeignKeysSchema(userType)
	assert.NotNil(t, code)

	f := jen.NewFile("test")
	f.Var().Id("fks").Op("=").Add(code)
	output := f.GoString()
	// O2M edges don't own FK, so should be nil
	assert.Contains(t, output, "nil")
}

// =============================================================================
// genIndexesSchema Tests
// =============================================================================

func TestGenIndexesSchema_NoIndexes(t *testing.T) {
	t.Parallel()
	userType := createTestType("User")
	userType.Indexes = nil

	code := genIndexesSchema(userType, migrateSchemaPkg)
	assert.NotNil(t, code)

	f := jen.NewFile("test")
	f.Var().Id("idxs").Op("=").Add(code)
	output := f.GoString()
	assert.Contains(t, output, "nil")
}

func TestGenIndexesSchema_WithIndexes(t *testing.T) {
	t.Parallel()
	userType := createTestType("User")
	userType.Indexes = []*gen.Index{
		{Name: "user_email", Unique: true, Columns: []string{"email"}},
		{Name: "user_status_name", Unique: false, Columns: []string{"status", "name"}},
	}

	code := genIndexesSchema(userType, migrateSchemaPkg)
	assert.NotNil(t, code)

	f := jen.NewFile("test")
	f.Var().Id("idxs").Op("=").Add(code)
	output := f.GoString()
	assert.Contains(t, output, "user_email")
	assert.Contains(t, output, "user_status_name")
}

func BenchmarkFieldTypeCode(b *testing.B) {
	fieldPkg := "github.com/syssam/velox/schema/field"
	types := []field.Type{field.TypeString, field.TypeInt, field.TypeInt64, field.TypeBool, field.TypeTime}
	for b.Loop() {
		for _, typ := range types {
			_ = fieldTypeCode(typ, fieldPkg)
		}
	}
}
