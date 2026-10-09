package shapes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file pins schema shapes that DO NOT GENERATE, DO NOT COMPILE OR DO NOT VET, and
// the shapes the generator rejects on purpose. Each case is a small schema
// of its own, generated with vxgen and built in a scratch directory inside
// this module, with the outcome it has today. The day the generator changes
// that outcome the case fails — which is the signal to move the shape into
// the positive matrix (./schema plus a behavior test) and delete the case.
// Do not "fix" a failing case by updating its expected outcome to another
// broken one without reading why it changed.
//
// Shapes that compile and misbehave at run time are pinned in
// known_gaps_runtime_test.go.

// outcome is what generating and building a schema results in.
type outcome string

const (
	// loadFails: velox's schema loader or graph validation rejects the
	// schema before the vx generator sees it. Not a vx finding; pinned so
	// that the positive matrix documents why the shape is absent.
	loadFails outcome = "loadFails"
	// generateFails: the vx generator returns an error.
	generateFails outcome = "generateFails"
	// generatorPanics: vxgen dies with a Go panic.
	generatorPanics outcome = "generatorPanics"
	// buildFails: generation succeeds, `go build` of the output does not.
	buildFails outcome = "buildFails"
	// vetFails: generation and build succeed, `go vet` of the output does not.
	vetFails outcome = "vetFails"
	// builds: generation, build and vet all succeed.
	builds outcome = "builds"
)

// gapCase is one self-contained schema and its outcome.
type gapCase struct {
	name string
	// schema is the Go source of schema/schema.go, after gapSchemaHeader.
	schema string
	// want and wantSubstr are the outcome observed today: wantSubstr must
	// appear in the combined output of the failing step.
	want       outcome
	wantSubstr string
	// gap says whether the outcome is a defect (true) or the intended
	// behavior pinned for reference (false). It only changes the message of
	// a failing case.
	gap bool
	// graphql generates the GraphQL layer too (vxgen -graphql).
	graphql bool
	// generated is text that files of the output must contain, by path
	// under db/: for a case that builds either way and is right only when
	// the generator wrote this.
	generated map[string][]string
}

const gapSchemaHeader = `package schema

import (
	"net/url"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"

	"github.com/syssam/velox/vx/tests/shapes/types"
)

var (
	_ = url.Parse
	_ schema.Annotation
	_ = edge.To
	_ types.Label
	_ = graphql.WhereInput
)
`

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

// thing declares an entity Thing with the given fields and edges (Go source
// of the slice elements), and a trivial entity Other for edges to point at.
func thing(fields, edges string) string {
	return fmt.Sprintf(`
type Thing struct{ velox.Schema }

func (Thing) Fields() []velox.Field { return []velox.Field{%s} }

func (Thing) Edges() []velox.Edge { return []velox.Edge{%s} }

type Other struct{ velox.Schema }

func (Other) Fields() []velox.Field { return []velox.Field{field.String("label")} }
`, fields, edges)
}

var gapCases = []gapCase{
	// --- Intended: pinned so that the positive matrix documents why the
	// shape is absent.

	// A Go type that names a package the schema does not record the import
	// path of: the key of a map. An error with the way out, not generated
	// code that does not build.
	{name: "JSON map keyed by a type from another package",
		schema: thing(`field.JSON("m", map[types.Label]int{})`, ``),
		want:   generateFails, wantSubstr: "names package \\\"types\\\", whose import path the schema does not record"},
	// A field written and read through an external ValueScanner: not
	// supported, and said so, instead of a field no row can be created with.
	{name: "field with an external ValueScanner",
		schema: thing(`field.String("link").GoType(&url.URL{}).ValueScanner(field.BinaryValueScanner[*url.URL]{})`, ``),
		want:   generateFails, wantSubstr: "an external ValueScanner is not supported yet"},
	// An entity named like a generated type of another entity.
	{name: "entities User and UserQuery", schema: labelEntities("User", "UserQuery"),
		want: generateFails, wantSubstr: "UserQuery"},
	{name: "entities User and UserEdges", schema: labelEntities("User", "UserEdges"),
		want: generateFails, wantSubstr: "UserEdges"},
	{name: "entities User and UserMeta", schema: labelEntities("User", "UserMeta"),
		want: generateFails, wantSubstr: "UserMeta"},
	// Two schema fields that velox stores in one column ("user_name").
	// velox's loader accepts the schema; the vx generator names both fields.
	{name: "fields user_name and userName", schema: thing(`field.String("user_name"), field.String("userName")`, ``),
		want: generateFails, wantSubstr: "are stored in the same column"},
	// Composite ids are not in milestone 1: an error, not a panic.
	{name: "edge schema with a composite id", schema: compositeSchema,
		want: generateFails, wantSubstr: "Membership: entities without a single-column id are not supported yet"},
	// Code of the schema that asserts a mutation to an interface of typed
	// accessors, the way Ent's documentation writes a hook shared by many
	// entities. No mutation has such methods here, so the hook would do
	// nothing for every entity, silently: generation fails and says where.
	{name: "hook asserts the mutation to typed setters",
		schema: labelEntities("Thing") + `
func stamp(m velox.Mutation) {
	if s, ok := m.(interface{ SetLabel(string) }); ok {
		s.SetLabel("x")
	}
}

var _ = stamp
`,
		want: generateFails, wantSubstr: "a velox.Mutation is asserted to an interface with SetLabel"},
	{name: "hook switches on the mutation's typed getters",
		schema: labelEntities("Thing") + `
type withLabel interface{ Label() (string, bool) }

func read(m velox.Mutation) string {
	switch m := m.(type) {
	case withLabel:
		v, _ := m.Label()
		return v
	default:
		return ""
	}
}

var _ = read
`,
		want: generateFails, wantSubstr: "a velox.Mutation is asserted to an interface with Label"},
	// The control: what a mutation does have, by name, passes.
	{name: "hook asserts the mutation to what it has",
		schema: labelEntities("Thing") + `
func stamp(m velox.Mutation) {
	if c, ok := m.(interface{ HasColumn(string) bool }); ok && c.HasColumn("label") {
		_ = m.SetField("label", "x")
	}
	switch m.(type) {
	case interface{ Field(string) (velox.Value, bool) }:
	case nil:
	}
}

var _ = stamp
`,
		want: builds},
	// The same, where an independent review found the check looking away:
	// under another name, through any, and through a generic helper.
	{name: "hook asserts through an interface that embeds the mutation",
		schema: labelEntities("Thing") + `
type mut interface{ velox.Mutation }

func stamp(m velox.Mutation) {
	var w mut = m
	if s, ok := w.(interface{ SetLabel(string) }); ok {
		s.SetLabel("x")
	}
}

var _ = stamp
`,
		want: generateFails, wantSubstr: "a velox.Mutation is asserted to an interface with SetLabel"},
	{name: "hook asserts through any",
		schema: labelEntities("Thing") + `
func stamp(m velox.Mutation) {
	if s, ok := any(m).(interface{ SetLabel(string) }); ok {
		s.SetLabel("x")
	}
}

var _ = stamp
`,
		want: generateFails, wantSubstr: "a velox.Mutation is asserted to an interface with SetLabel"},
	{name: "hook asserts through a generic helper",
		schema: labelEntities("Thing") + `
func as[T any](m velox.Mutation) (T, bool) {
	t, ok := m.(T)
	return t, ok
}

func stamp(m velox.Mutation) {
	if s, ok := as[interface{ SetLabel(string) }](m); ok {
		s.SetLabel("x")
	}
}

var _ = stamp
`,
		want: generateFails, wantSubstr: "through as, a velox.Mutation is asserted to an interface with SetLabel"},
	// And where it cried wolf: the typed way first and the way by name
	// after it is correct under both generators, once its author says so;
	// a case of several types holds when one of them does.
	{name: "hook tries the typed setter and goes by name, and says so",
		schema: labelEntities("Thing") + `
func stamp(m velox.Mutation) {
	//vx:nocheck velox's mutations have the setter; here the name is used
	if s, ok := m.(interface{ SetLabel(string) }); ok {
		s.SetLabel("x")
		return
	}
	_ = m.SetField("label", "x")
}

func kind(m velox.Mutation) int {
	switch m.(type) {
	case interface{ SetLabel(string) }, interface{ SetField(string, velox.Value) error }:
		return 1
	}
	return 0
}

var _, _ = stamp, kind
`,
		want: builds},
	// An entity whose package is named like a variable of the generated
	// methods (conn, in Paginate): its import must not capture the variable
	// in the other entities' files.
	{name: "entities Conn and Where", schema: labelEntities("Conn", "Where", "Pool") + `
func (Conn) Edges() []velox.Edge { return []velox.Edge{edge.To("pools", Pool.Type), edge.To("wheres", Where.Type)} }
`,
		want: builds},
	// --- The GraphQL layer, as an independent review probed it.

	// A where input's field is what the schema generator says it is, not
	// what its name can be read as: logIn is log with In although there is
	// a field login, titleContains is title with Contains although there is
	// a field title_contains, hasOthers is the edge although there is a
	// boolean field has_others.
	{name: "where input fields whose names read two ways", graphql: true,
		schema: `
type Thing struct{ velox.Schema }

func (Thing) Fields() []velox.Field {
	return []velox.Field{
		field.String("log").Annotations(graphql.WhereInput()),
		field.String("login"),
		field.String("title").Annotations(graphql.WhereInput()),
		field.String("title_contains"),
		field.Bool("has_others"),
	}
}

func (Thing) Edges() []velox.Edge {
	return []velox.Edge{edge.To("others", Other.Type).Annotations(graphql.WhereInput())}
}

func (Thing) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }

type Other struct{ velox.Schema }

func (Other) Fields() []velox.Field {
	return []velox.Field{field.String("label").Annotations(graphql.WhereInput())}
}

func (Other) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: builds,
		generated: map[string][]string{"filter/thing.go": {
			`{Col: "log", Op: orm.OpIn}, // logIn`,
			`{Col: "title", Op: orm.OpContains}, // titleContains`,
			`{Op: orm.OpHasEdge, Edge: thingEdges[0]}, // hasOthers`,
			`thingWhere[`, // the neighbor's plan, for the depth limit across the edge
		}}},
	// An entity whose package is named like the package of a field's type:
	// in package filter the type's package keeps the name.
	{name: "entity Types with a field of package types", graphql: true,
		schema: `
type Types struct{ velox.Schema }

func (Types) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("kind").Values("a", "b").Default("a").Annotations(graphql.WhereInput()),
		field.String("label").GoType(types.Label("")).Annotations(graphql.WhereInput()),
	}
}

func (Types) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: builds},
	// An entity with the name of something the schema always has: the
	// schema would define it twice, which used to pass with one of the two
	// silently dropped.
	{name: "entity Time, the name of a scalar", graphql: true,
		schema: `
type Time struct{ velox.Schema }

func (Time) Fields() []velox.Field { return []velox.Field{field.Time("at")} }

func (Time) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "Time is defined twice"},
	// What the schema would name and this generator does not write is said
	// at generation, not found by an engine's build.
	{name: "the schema binds an accessor that is not generated", graphql: true,
		schema: `
type Thing struct{ velox.Schema }

func (Thing) Fields() []velox.Field {
	return []velox.Field{field.String("label"), field.Bytes("blob").Optional().Nillable()}
}

func (Thing) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "which this generator"},
	{name: "an enum field called order_field", graphql: true,
		schema: `
type Thing struct{ velox.Schema }

func (Thing) Fields() []velox.Field {
	return []velox.Field{field.String("label"), field.Enum("order_field").Values("a", "b").Default("a")}
}

func (Thing) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "ThingOrderField is defined twice"},
	{name: "a node with a field is_node", graphql: true,
		schema: `
type Thing struct{ velox.Schema }

func (Thing) Fields() []velox.Field { return []velox.Field{field.String("label"), field.Bool("is_node")} }

func (Thing) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "a method IsNode"},
	{name: "entity Node, the name of the interface", graphql: true,
		schema: `
type Node struct{ velox.Schema }

func (Node) Fields() []velox.Field { return []velox.Field{field.String("label")} }

func (Node) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "Node is defined twice"},
	{name: "entity NodeType in a schema with nodes", graphql: true,
		schema: `
type NodeType struct{ velox.Schema }

func (NodeType) Fields() []velox.Field { return []velox.Field{field.String("label")} }

func (NodeType) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "has the name of what the root package declares"},
	// An entity nothing can be ordered by: velox's schema generator writes
	// an order enum without values, and refuses its own schema. Said at
	// generation; the entity needs a field to order by, or to be left out
	// of the schema.
	{name: "an entity with no field to order by, in a GraphQL schema", graphql: true,
		schema: `
type Bare struct{ velox.Schema }

func (Bare) Fields() []velox.Field { return []velox.Field{field.Bytes("blob")} }

func (Bare) Annotations() []schema.Annotation { return []schema.Annotation{graphql.QueryField(), graphql.RelayConnection()} }
`,
		want: generateFails, wantSubstr: "is invalid", gap: true},
	// Names velox's schema loader rejects before the vx generator runs.
	{name: "entity named Query", schema: labelEntities("Query"),
		want: loadFails, wantSubstr: `schema name conflicts with ent predeclared identifier \"Query\"`},
	{name: "entity named Delete", schema: labelEntities("Delete"),
		want: loadFails, wantSubstr: `conflicts with Go predeclared identifier \"delete\"`},
	{name: "entity named Type", schema: labelEntities("Type"),
		want: loadFails, wantSubstr: `conflicts with Go keyword \"type\"`},
}

const compositeSchema = `
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
`

// vxRoot is the vx module root, two levels above this package.
func vxRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "cmd", "vxgen"))
	require.NoError(t, err, "tests must run from vx/tests/shapes")
	return root
}

var (
	vxgenOnce sync.Once
	vxgenPath string
	vxgenErr  error
)

// vxgenBinary builds cmd/vxgen once per test run.
func vxgenBinary(t *testing.T) string {
	t.Helper()
	root := vxRoot(t)
	vxgenOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vxgen")
		if err != nil {
			vxgenErr = err
			return
		}
		vxgenPath = filepath.Join(dir, "vxgen")
		cmd := exec.Command("go", "build", "-o", vxgenPath, "./cmd/vxgen")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			vxgenErr = fmt.Errorf("building vxgen: %w\n%s", err, out)
		}
	})
	require.NoError(t, vxgenErr)
	return vxgenPath
}

// scratch returns a new directory inside this package's _work directory and
// its import path. A directory whose name starts with an underscore is
// invisible to ./... patterns, so a leftover of a killed run does not break
// the module; it is still a package of the module when named explicitly.
func scratch(t *testing.T, name string) (dir, importPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll("_work", 0o755))
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return '_'
	}, name)
	dir, err := os.MkdirTemp("_work", clean+"_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	return abs, "github.com/syssam/velox/vx/tests/shapes/_work/" + filepath.Base(dir)
}

// run runs a command in dir with a generous timeout and returns its
// combined output.
func run(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runGapCase generates and builds c and classifies the result.
func runGapCase(t *testing.T, c gapCase) (got outcome, output string) {
	t.Helper()
	bin := vxgenBinary(t)
	root := vxRoot(t)
	dir, importPath := scratch(t, c.name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "schema"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(gapSchemaHeader+c.schema), 0o644))

	// vxgen runs from the scratch directory: the schema loader keeps its
	// cache in the working directory.
	gen := []string{"-schema", "./schema", "-target", "./db", "-package", importPath + "/db"}
	if c.graphql {
		gen = append(gen, "-graphql", "./gql/velox.graphql")
	}
	out, err := run(t, dir, bin, gen...)
	if err != nil {
		switch {
		case strings.Contains(out, "panic:") && strings.Contains(out, "goroutine "):
			return generatorPanics, out
		case strings.Contains(out, "vxgen: loading the schema"):
			return loadFails, out
		default:
			return generateFails, out
		}
	}
	for file, texts := range c.generated {
		src, err := os.ReadFile(filepath.Join(dir, "db", file))
		require.NoError(t, err)
		// gofmt aligns the comments of a table: compare without the layout.
		flat := strings.Join(strings.Fields(string(src)), " ")
		for _, text := range texts {
			if !strings.Contains(flat, strings.Join(strings.Fields(text), " ")) {
				return buildFails, fmt.Sprintf("db/%s does not contain %q:\n%s", file, text, src)
			}
		}
	}
	// Every generated package, named one by one (./... skips _work).
	args := []string{"build"}
	base := filepath.Join(dir, "db")
	require.NoError(t, filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == base {
			return err
		}
		if files, _ := filepath.Glob(filepath.Join(path, "*.go")); len(files) > 0 {
			rel, _ := filepath.Rel(base, path)
			args = append(args, importPath+"/db/"+filepath.ToSlash(rel))
		}
		return nil
	}))
	if out, err := run(t, root, "go", args...); err != nil {
		return buildFails, out
	}
	args[0] = "vet"
	if out, err := run(t, root, "go", args...); err != nil {
		return vetFails, out
	}
	return builds, ""
}

// TestKnownGaps generates and builds each pinned shape and asserts its
// outcome is what it was. Slow (each case runs the schema loader, the
// generator and a build), so the cases run in parallel and are skipped under
// -short. SHAPES_DISCOVER=1 logs the outcomes instead of asserting them.
func TestKnownGaps(t *testing.T) {
	if testing.Short() {
		t.Skip("known-gap cases run the generator and a build; skipped under -short")
	}
	discover := os.Getenv("SHAPES_DISCOVER") != ""
	vxgenBinary(t)
	for _, c := range gapCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, out := runGapCase(t, c)
			if discover {
				t.Logf("DISCOVER %q: %s\n%s", c.name, got, firstLines(out, 12))
				return
			}
			if got == c.want && strings.Contains(out, c.wantSubstr) {
				return
			}
			if c.gap && got == builds {
				t.Fatalf("known gap %q is FIXED: it now generates and builds (was %s).\n"+
					"Move this shape into the positive matrix — add it to schema/, regenerate, assert its "+
					"runtime behavior — and delete this case.", c.name, c.want)
			}
			t.Fatalf("pinned shape %q changed: want %s with output containing %q, got %s.\n"+
				"If the generator now handles the shape, move it into the positive matrix (schema/ + a "+
				"behavior test) and delete this case; if it breaks differently, update the expected "+
				"outcome and say why.\noutput:\n%s", c.name, c.want, c.wantSubstr, got, firstLines(out, 60))
		})
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, "\n")
}
