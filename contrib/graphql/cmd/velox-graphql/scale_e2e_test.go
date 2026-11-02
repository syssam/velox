package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scaleInner is the application's test: it serves the generated schema through
// gqlgen's handler on SQLite and runs queries against it.
//
//go:embed testdata/scale_inner_test.txt
var scaleInner string

// scaleEntities is the number of entities TestLargeSchemaServed generates:
// VELOX_GQL_SCALE_N, a multiple of 10, 100 by default. gqlgen's generate and
// the compile of its output grow with it, so 500 (what bench/scale builds)
// takes minutes.
func scaleEntities(t *testing.T) int {
	t.Helper()
	n := 100
	if v := os.Getenv("VELOX_GQL_SCALE_N"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil {
			t.Fatalf("VELOX_GQL_SCALE_N=%q: %v", v, err)
		}
	}
	if n < 10 || n%10 != 0 {
		t.Fatalf("VELOX_GQL_SCALE_N=%d: want a multiple of 10, at least 10", n)
	}
	return n
}

// scaleSchemas are n schemas in chains of ten: entity i holds prev_id, a
// Required edge prev to entity i-1, and the edge next to entity i+1, except at
// the ends of a chain. Every entity has a root query field (e<i>s), an order
// and where inputs on name, and a time and a float to give the type width.
func scaleSchemas(n int) map[string]string {
	files := map[string]string{}
	for i := range n {
		var fields, edges strings.Builder
		if i%10 != 0 {
			fields.WriteString("\t\tfield.Int64(\"prev_id\"),\n")
			fmt.Fprintf(&edges, "\t\tedge.From(\"prev\", E%d.Type).Ref(\"next\").Unique().Required().Field(\"prev_id\"),\n", i-1)
		}
		if i%10 != 9 {
			fmt.Fprintf(&edges, "\t\tedge.To(\"next\", E%d.Type),\n", i+1)
		}
		files[fmt.Sprintf("schema/e%d.go", i)] = fmt.Sprintf(`package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type E%[1]d struct{ schema.Schema }

func (E%[1]d) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Float("amount"),
		field.Time("created_at"),
%[2]s	}
}

func (E%[1]d) Edges() []schema.Edge {
	return []schema.Edge{
%[3]s	}
}

func (E%[1]d) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("e%[1]ds")}
}
`, i, fields.String(), edges.String())
	}
	return files
}

var (
	// connFunc matches an entity's root connection function in the Go the
	// plugin writes: its parameters, and the connection it returns.
	connFunc = regexp.MustCompile(`(?m)^func (E\d+)sConnection\(ctx context\.Context, ([^)]*)\) \(\*(E\d+Connection), error\) \{`)
	// tableOf matches the table a generated entity reads.
	tableOf = regexp.MustCompile(`Table:\s+"([a-z0-9_]+)"`)
	// genType is a type of the generated package in a parameter list.
	genType = regexp.MustCompile(`(\*|\[\]\*)(E\d+[A-Za-z]*)`)
)

// scaleResolvers is graph/schema.resolvers.go: Node, Nodes and the root
// connection of each of the n entities. The parameters of a connection
// function (an orderBy that is a slice or a pointer) are read from the Go the
// plugin wrote, so the test follows the plugin and does not restate it.
func scaleResolvers(t *testing.T, dir string, n int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gen.Node(ctx, id)
}

// Nodes is the resolver for the nodes field.
func (r *queryResolver) Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gen.Nodes(ctx, ids)
}
`)
	for i := range n {
		src, err := os.ReadFile(filepath.Join(dir, "gen", fmt.Sprintf("e%d_gql.go", i)))
		if err != nil {
			t.Fatal(err)
		}
		m := connFunc.FindStringSubmatch(string(src))
		if m == nil {
			t.Fatalf("no E%dsConnection in gen/e%d_gql.go", i, i)
		}
		var args []string
		for p := range strings.SplitSeq(m[2], ", ") {
			args = append(args, strings.Fields(p)[0])
		}
		fmt.Fprintf(&b, "\n// E%[1]ds is the resolver for the e%[1]ds field.\nfunc (r *queryResolver) E%[1]ds(ctx context.Context, %[2]s) (*gen.%[3]s, error) {\n\treturn gen.E%[1]dsConnection(ctx, %[4]s)\n}\n",
			i, genType.ReplaceAllString(m[2], "${1}gen.${2}"), m[3], strings.Join(args, ", "))
	}
	b.WriteString(`
// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`)
	return b.String()
}

// scaleDDL is the application's tables, as a Go file: one for each entity,
// named as the generated code names it.
func scaleDDL(t *testing.T, dir string, n int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("package app_test\n\n// ddl creates the tables.\nvar ddl = []string{\n")
	for i := range n {
		src, err := os.ReadFile(filepath.Join(dir, "gen", fmt.Sprintf("e%d_velox.go", i)))
		if err != nil {
			t.Fatal(err)
		}
		m := tableOf.FindStringSubmatch(string(src))
		if m == nil {
			t.Fatalf("no Table in gen/e%d_velox.go", i)
		}
		prev := ""
		if i%10 != 0 {
			prev = ", prev_id INTEGER NOT NULL"
		}
		fmt.Fprintf(&b, "\t\"CREATE TABLE %s (id INTEGER PRIMARY KEY, name TEXT NOT NULL, amount REAL NOT NULL, created_at DATETIME NOT NULL%s)\",\n", m[1], prev)
		// Three rows in each table; row k of an entity follows row k of the one before it.
		for k := 1; k <= 3; k++ {
			cols, vals := "", ""
			if i%10 != 0 {
				cols, vals = ", prev_id", fmt.Sprintf(", %d", k)
			}
			fmt.Fprintf(&b, "\t\"INSERT INTO %s (id, name, amount, created_at%s) VALUES (%d, 'e%d-r%d', %d.5, '2026-01-01T00:00:00Z'%s)\",\n", m[1], cols, k, i, k, k, vals)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// TestLargeSchemaServed generates n entities' application with velox-gen and
// the plugin, then gqlgen over it, and serves it through gqlgen's handler on
// SQLite. Nothing else puts hundreds of entities through gqlgen: the others
// render the SDL of that many (veloxgql's cost_meta_test.go) or build a few.
// It logs each step's time, and the application's test logs the handler's.
func TestLargeSchemaServed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module of hundreds of entities")
	}
	n := scaleEntities(t)
	files := scaleSchemas(n)
	files["gqlgen.yml"] = e2eCollectGqlgen
	dir, run := app(t, files, true)

	step := func(name string, args ...string) {
		t.Helper()
		start := time.Now()
		out, err := run(args...)
		t.Logf("%-14s %6.1fs", name, time.Since(start).Seconds())
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	step("velox-gen", "tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema")
	for name, src := range map[string]string{
		"graph/schema.resolvers.go": scaleResolvers(t, dir, n),
		"ddl_test.go":               scaleDDL(t, dir, n),
		"scale_test.go":             strings.ReplaceAll(scaleInner, "__N__", strconv.Itoa(n)),
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	step("gqlgen", "tool", "gqlgen", "generate")
	step("tidy", "mod", "tidy", "-e")
	step("build", "build", "./...")

	start := time.Now()
	out, err := run("test", "-count=1", "-v", ".")
	t.Logf("%-14s %6.1fs\n%s", "serve", time.Since(start).Seconds(), out)
	if err != nil {
		t.Fatalf("go test: %v", err)
	}
	for _, name := range []string{"TestLargeSchemaTypes", "TestLargeSchemaIntrospection", "TestLargeSchemaQueries", "TestLargeSchemaCostLimits"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s", name)
		}
	}
}
