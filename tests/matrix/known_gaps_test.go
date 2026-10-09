package matrix

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins schema shapes that are KNOWN TO BE BROKEN. Each case asserts
// the broken outcome as it is today, so that the day someone fixes the
// generator the case flips and this test fails — which is the signal to move
// the shape into the positive matrix (schema/ plus a behaviour test) and
// delete the case here. Do not "fix" a failing case by updating its expected
// outcome to another broken one without reading why it changed.

// outcome is what generating and building a schema results in.
type outcome string

const (
	// generateFails: `go run generate.go` exits non-zero with an error.
	generateFails outcome = "generateFails"
	// generatorPanics: `go run generate.go` dies with a Go panic.
	generatorPanics outcome = "generatorPanics"
	// buildFails: generation succeeds, `go build ./...` does not.
	buildFails outcome = "buildFails"
	// ok: generation and build both succeed.
	ok outcome = "ok"
)

// gapCase is one self-contained schema and its current, broken outcome.
type gapCase struct {
	name string
	// features are gen.Feature expressions passed to gen.WithFeatures.
	features []string
	// schema is the Go source of schema/schema.go, after the common header.
	schema string
	// want and wantSubstr are the outcome observed today: wantSubstr must
	// appear in the combined output of the failing step.
	want       outcome
	wantSubstr string
	// collides marks a case whose result is NOT deterministic: the entity's
	// package and a package the generator emits itself share a directory,
	// two concurrent writers target the same file, and whichever loses is
	// silently discarded. Depending on the winner the project fails to build
	// in one of several ways, or even builds. For such a case want and
	// wantSubstr are ignored; the pin is the collision itself — generation
	// succeeds and this file (relative to the module, written only by the
	// entity's own generator) sits inside the shared directory.
	collides string
}

const gapSchemaHeader = `package schema

import (
	"net/url"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

var (
	_ = url.Parse
	_ schema.Annotation
	_ = edge.To
)
`

// validatedField is an entity with one string field carrying two validators.
func validatedField(name string) string {
	return fmt.Sprintf(`
type Thing struct{ velox.Schema }

func (Thing) Fields() []velox.Field {
	return []velox.Field{field.String(%q).NotEmpty().MaxLen(10)}
}
`, name)
}

// labelEntities declares one trivial entity per name.
func labelEntities(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, `
type %[1]s struct{ velox.Schema }

func (%[1]s) Fields() []velox.Field { return []velox.Field{field.String("label")} }
`, n)
	}
	return b.String()
}

var gapCases = []gapCase{
	{
		// Correct: the generated code imports the ValueScanner's package and
		// the project builds; link round-trips as a *url.URL.
		name: "external ValueScanner on a GoType field",
		schema: `
type Page struct{ velox.Schema }

func (Page) Fields() []velox.Field {
	return []velox.Field{
		field.String("link").GoType(&url.URL{}).ValueScanner(field.BinaryValueScanner[*url.URL]{}),
	}
}
`,
		want:       buildFails,
		wantSubstr: "undefined: page",
	},
	{
		// Correct: either a composite-key join entity is generated (Ent
		// supports it), or generation fails with an error naming the
		// annotation — never a nil-pointer panic.
		name: "edge schema with a composite field.ID annotation",
		schema: `
type User struct{ velox.Schema }

func (User) Fields() []velox.Field { return []velox.Field{field.String("name")} }

func (User) Edges() []velox.Edge {
	return []velox.Edge{edge.To("groups", Group.Type).Through("memberships", Membership.Type)}
}

type Group struct{ velox.Schema }

func (Group) Fields() []velox.Field { return []velox.Field{field.String("name")} }

func (Group) Edges() []velox.Edge {
	return []velox.Edge{edge.From("users", User.Type).Ref("groups").Through("memberships", Membership.Type)}
}

type Membership struct{ velox.Schema }

func (Membership) Annotations() []schema.Annotation {
	return []schema.Annotation{field.ID("user_id", "group_id")}
}

func (Membership) Fields() []velox.Field {
	return []velox.Field{field.Int("user_id"), field.Int("group_id")}
}

func (Membership) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id"),
		edge.To("group", Group.Type).Unique().Required().Field("group_id"),
	}
}
`,
		want:       generatorPanics,
		wantSubstr: "nil pointer dereference",
	},
	// A field whose name the generated validator wiring also uses as a local.
	// Correct: the generated code builds whatever a field is called.
	// (A field named err with two validators was reported broken too; it
	// builds and behaves, and now lives in the positive matrix as ErrField.)
	{name: "field named fn with two validators", schema: validatedField("fn"),
		want: buildFails, wantSubstr: "cannot use fn"},
	{name: "field named fns with two validators", schema: validatedField("fns"),
		want: buildFails, wantSubstr: "declared and not used: fns"},

	// Entity names that are Go initialisms. Correct: query/helpers.go calls
	// the constructor by the name the query package declares it with — the
	// acronym casing must agree on both sides — and the project builds.
	{name: "entity named Sql", schema: labelEntities("Sql"),
		want: buildFails, wantSubstr: "undefined: NewSQLQuery"},
	{name: "entity named Uuid", schema: labelEntities("Uuid"),
		want: buildFails, wantSubstr: "undefined: NewUUIDQuery"},
	{name: "entity named Json", schema: labelEntities("Json"),
		want: buildFails, wantSubstr: "undefined: NewJSONQuery"},
	{name: "entity named Http", schema: labelEntities("Http"),
		want: buildFails, wantSubstr: "undefined: NewHTTPQuery"},
	{name: "entity named Url", schema: labelEntities("Url"),
		want: buildFails, wantSubstr: "undefined: NewURLQuery"},
	{name: "entity named Api", schema: labelEntities("Api"),
		want: buildFails, wantSubstr: "undefined: NewAPIQuery"},
	// Correct: the entity's package name `id` must not be shadowed by the
	// `id` locals and parameters of the generated client; the project builds.
	{name: "entity named Id", schema: labelEntities("Id"),
		want: buildFails, wantSubstr: "id.FieldID undefined"},

	// Entity names whose package or client field collides with something
	// the generator emits itself. Correct, for every one of these: the
	// project builds, or — like "Schema", "Client", "Hook" — the name is
	// rejected by Graph validation with an error that names it.
	// Observed: mostly "import cycle not allowed", occasionally (about one
	// run in six) a clean build of a project missing one of the two files.
	{name: "entity named Privacy, privacy feature on",
		features: []string{"gen.FeaturePrivacy"}, schema: labelEntities("Privacy", "Other"),
		collides: "velox/privacy/where.go"},
	{name: "entity named Privacy, privacy feature off", schema: labelEntities("Privacy"),
		want: buildFails, wantSubstr: "no required module provides package example.com/gap/velox/privacy"},
	// Observed: "import cycle not allowed" or "field and method with the
	// same name Intercept", about evenly.
	{name: "entity named Intercept, intercept feature on",
		features: []string{"gen.FeatureIntercept"}, schema: labelEntities("Intercept"),
		collides: "velox/intercept/where.go"},
	{name: "entity named Intercept, intercept feature off", schema: labelEntities("Intercept"),
		want: buildFails, wantSubstr: "no required module provides package example.com/gap/velox/intercept"},
	{name: "entity named Predicate", schema: labelEntities("Predicate"),
		want: buildFails, wantSubstr: "import cycle not allowed"},
	{name: "entity named Internal", schema: labelEntities("Internal"),
		want: buildFails, wantSubstr: "use of internal package"},
	// Observed: "undefined: entity.Entity", "undefined: entity.EntityQuerier"
	// or "undefined: FieldID".
	{name: "entity named Entity", schema: labelEntities("Entity"),
		collides: "velox/entity/where.go"},
	// Observed: "undefined: migrate.Schema" or "undefined: FieldID".
	{name: "entity named Migrate", schema: labelEntities("Migrate"),
		collides: "velox/migrate/where.go"},
	{name: "entity named Tx", schema: labelEntities("Tx"),
		want: buildFails, wantSubstr: "field and method with the same name Tx"},
	{name: "entity named Context", schema: labelEntities("Context"),
		want: buildFails, wantSubstr: "field and method with the same name Context"},

	{
		// Correct: the generated SchemaReady package variable must not share
		// a name with the enum type a field named schema_ready generates.
		name: "enum field named schema_ready on an entity with runtime code",
		schema: `
type Job struct{ velox.Schema }

func (Job) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").Default("job"),
		field.Enum("schema_ready").Values("yes", "no").Default("no"),
	}
}
`,
		want:       buildFails,
		wantSubstr: "SchemaReady redeclared",
	},

	// Field names whose Go name collides with a member of the generated
	// entity struct. Correct, for every one: the project builds, or the
	// field name is rejected at generation time with an error naming it.
	{name: "field named edges", schema: validatedField("edges"),
		want: buildFails, wantSubstr: "Edges redeclared"},
	{name: "field named string", schema: validatedField("string"),
		want: buildFails, wantSubstr: "field and method with the same name String"},
	{name: "field named unwrap", schema: validatedField("unwrap"),
		want: buildFails, wantSubstr: "field and method with the same name Unwrap"},
	{name: "field named set_config", schema: validatedField("set_config"),
		want: buildFails, wantSubstr: "field and method with the same name SetConfig"},
	{name: "field named scan_values", schema: validatedField("scan_values"),
		want: buildFails, wantSubstr: "field and method with the same name ScanValues"},
	{name: "field named assign_values", schema: validatedField("assign_values"),
		want: buildFails, wantSubstr: "field and method with the same name AssignValues"},
	{name: "field named is_node", schema: validatedField("is_node"),
		want: buildFails, wantSubstr: "field and method with the same name IsNode"},
	// ...and with a method of the generated mutation. Same correct outcome.
	{name: "field named op", schema: validatedField("op"),
		want: buildFails, wantSubstr: "ThingMutation.SetOp already declared"},
	{name: "field named where", schema: validatedField("where"),
		want: buildFails, wantSubstr: "ThingMutation.Where already declared"},
	{name: "field named fields", schema: validatedField("fields"),
		want: buildFails, wantSubstr: "ThingMutation.Fields already declared"},
	{name: "field named client", schema: validatedField("client"),
		want: buildFails, wantSubstr: "c.mutation.GetClient undefined"},
}

// repoRoot is the velox repository root, two levels above this module.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "compiler", "gen"))
	require.NoError(t, err, "tests must run from tests/matrix")
	return root
}

// writeGapModule writes c into dir as a module of its own that replaces
// velox with this checkout. go.mod and go.sum come from this module, so the
// dependency versions match and nothing is downloaded.
func writeGapModule(t *testing.T, dir, root string, c gapCase) {
	t.Helper()
	gomod, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	mod := strings.Replace(string(gomod), "module example.com/matrix", "module example.com/gap", 1)
	require.Contains(t, mod, "=> ../..", "go.mod lost its replace directive")
	mod = strings.Replace(mod, "=> ../..", "=> "+root, 1)
	gosum, err := os.ReadFile("go.sum")
	require.NoError(t, err)

	generate := fmt.Sprintf(`//go:build ignore

package main

import (
	"fmt"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func main() {
	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		gen.WithPackage("example.com/gap/velox"),
		gen.WithFeatures(%s),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if err := compiler.Generate("./schema", cfg); err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
}
`, strings.Join(c.features, ", "))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "schema"), 0o755))
	for name, content := range map[string]string{
		"go.mod":           mod,
		"go.sum":           string(gosum),
		"generate.go":      generate,
		"schema/schema.go": gapSchemaHeader + c.schema,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
}

// runGapCase generates and builds c and classifies the result. The child
// processes inherit this process's environment, so they share its GOFLAGS,
// GOCACHE and module cache.
func runGapCase(t *testing.T, root string, c gapCase) (got outcome, output, dir string) {
	t.Helper()
	dir = t.TempDir()
	writeGapModule(t, dir, root, c)
	goBin, err := exec.LookPath("go")
	require.NoError(t, err)

	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, goBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run("run", "generate.go"); err != nil {
		if strings.Contains(out, "panic:") && strings.Contains(out, "goroutine ") {
			return generatorPanics, out, dir
		}
		return generateFails, out, dir
	}
	if out, err := run("build", "./..."); err != nil {
		return buildFails, out, dir
	}
	return ok, "", dir
}

// TestKnownGaps generates and builds each broken shape and asserts it is
// still broken in the same way. Slow (each case runs the generator and a
// build), so the cases run in parallel and are skipped under -short.
func TestKnownGaps(t *testing.T) {
	if testing.Short() {
		t.Skip("known-gap cases spawn go run + go build; skipped under -short")
	}
	root := repoRoot(t)
	for _, c := range gapCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, out, dir := runGapCase(t, root, c)
			if c.collides != "" {
				if got != buildFails && got != ok {
					t.Fatalf("known gap %q changed: generation used to succeed and write two packages "+
						"into one directory; now %s.\nIf the name is now rejected with a clear error, "+
						"pin that as generateFails; if the generator panics, that is a new bug.\noutput:\n%s",
						c.name, got, out)
				}
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c.collides))); err != nil {
					t.Fatalf("known gap %q is FIXED: %s is no longer generated into the shared directory "+
						"(build: %s).\nMove this shape into the positive matrix — add it to schema/, "+
						"assert its runtime behaviour in matrix_test.go — and delete this case.",
						c.name, c.collides, got)
				}
				return
			}
			if got == c.want && strings.Contains(out, c.wantSubstr) {
				return
			}
			if got == ok {
				t.Fatalf("known gap %q is FIXED: it now generates and builds (was %s).\n"+
					"Move this shape into the positive matrix — add it to schema/, assert its "+
					"runtime behaviour in matrix_test.go — and delete this case.", c.name, c.want)
			}
			t.Fatalf("known gap %q changed: want %s with output containing %q, got %s.\n"+
				"If the generator was fixed far enough that the shape works, move it into the "+
				"positive matrix (schema/ + a behaviour test) and delete this case; if it now "+
				"breaks differently, update the expected outcome and say why.\noutput:\n%s",
				c.name, c.want, c.wantSubstr, got, out)
		})
	}
}

// The two gaps below compile fine and misbehave at run time, so they are
// pinned against the positive module's own generated code rather than a
// scratch module. Same rule: when one starts failing, the bug is fixed —
// tighten the positive test named in the message and delete the gap test.

// Correct: a NULL in a Bytes().Optional().Nillable() column reads back as a
// nil *[]byte, like every other nillable type. Today the generated
// AssignValues keeps the scan target (`e.Nilb = value`), so NULL reads as a
// non-nil pointer to a nil slice and `if row.Nilb != nil` cannot tell NULL
// from a value.
func TestKnownGap_NillableBytesNULLScansAsPointerToNilSlice(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	created, err := client.Blob.Create().SetSeq(1).SetData([]byte("d")).SetOpt([]byte("o")).Save(ctx)
	require.NoError(t, err)
	require.Nil(t, created.Nilb, "the entity Create returns has a nil pointer")

	got := client.Blob.GetX(ctx, created.ID)
	if got.Nilb == nil {
		t.Fatal("known gap is FIXED: a NULL nillable bytes column now reads back as a nil pointer.\n" +
			"Replace nilBytes() in matrix_data_test.go with assert.Nil and delete this test.")
	}
	assert.Nil(t, *got.Nilb, "the pointer a NULL reads back as points at a nil slice")
	assert.Nil(t, got.Nils, "a nillable string in the same row is a nil pointer")
}
