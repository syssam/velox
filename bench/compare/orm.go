package main

import (
	"fmt"
	"go/format"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// orm is one of the compared ORMs: where its schemas and generated code go,
// how its DSL is spelled, how its code generator runs, and what a service
// does with the generated code.
type orm struct {
	Name      string
	GenDir    string // the generated code's directory, relative to the app
	SchemaDir string // the schema package's directory, relative to the app
	ModDir    string // the directory of its go.mod, relative to the harness: empty for the harness's
	dsl       dsl
	// Codegen is the generator's argv, run in the app directory. tools is the
	// directory of the generator binaries.
	Codegen func(tools string) []string
	// Service is the source of a service's main.go, which queries and counts
	// each of the entities.
	Service func(entities []int) string
}

// dsl is what differs between the three schema DSLs. The builder calls (field.String,
// edge.To, index.Fields, ...) are spelled the same in all of them.
type dsl struct {
	Imports    string // import paths of the schema package, edge and field, one quoted path per line
	SchemaType string // the type a schema embeds
	FieldType  string // the element type of Fields()
	EdgeType   string // the element type of Edges()
	FK         string // the field constructor of a foreign key
	IndexType  string // the element type of Indexes(); empty: the schema writes no index
	// IndexImport is the import path of the index package; used with IndexType.
	IndexImport string
}

var veloxORM = orm{
	Name: "velox", GenDir: "gen", SchemaDir: "schema",
	dsl: dsl{
		Imports: `"github.com/syssam/velox/schema"
"github.com/syssam/velox/schema/edge"
"github.com/syssam/velox/schema/field"`,
		SchemaType: "schema.Schema", FieldType: "schema.Field", EdgeType: "schema.Edge", FK: "Int64",
	},
	Codegen: func(tools string) []string {
		return []string{filepath.Join(tools, "velox-gen"), "-out", "gen", "./schema"}
	},
	Service: func(es []int) string {
		var b strings.Builder
		b.WriteString("package main\n\nimport (\n\t\"context\"\n\n\t\"example.com/tmp/gen\"\n\t\"github.com/syssam/velox\"\n)\n\n")
		b.WriteString("func main() {\n\tctx := context.Background()\n\tdb := velox.Open(velox.Postgres, nil)\n")
		for _, k := range es {
			fmt.Fprintf(&b, "\tdb.Query[gen.E%[1]d]().Where(gen.E%[1]ds.ID.EQ(1)).Limit(1).All(ctx)\n\tdb.Query[gen.E%[1]d]().Count(ctx)\n", k)
		}
		b.WriteString("}\n")
		return b.String()
	},
}

var veloxeORM = orm{
	Name: "veloxe", GenDir: "velox", SchemaDir: "schema", ModDir: "veloxe",
	dsl: dsl{
		Imports: `"github.com/syssam/velox"
"github.com/syssam/velox/schema/edge"
"github.com/syssam/velox/schema/field"`,
		SchemaType: "velox.Schema", FieldType: "velox.Field", EdgeType: "velox.Edge", FK: "Int",
		IndexType: "velox.Index", IndexImport: "github.com/syssam/velox/schema/index",
	},
	Codegen: func(tools string) []string {
		return []string{filepath.Join(tools, "velox"), "generate", "--target", "./velox", "./schema"}
	},
	Service: func(es []int) string {
		return clientService("velox", "e%[1]d.IDField.EQ(1)", es)
	},
}

var entORM = orm{
	Name: "ent", GenDir: "ent", SchemaDir: "ent/schema",
	dsl: dsl{
		Imports: `"entgo.io/ent"
"entgo.io/ent/schema/edge"
"entgo.io/ent/schema/field"`,
		SchemaType: "ent.Schema", FieldType: "ent.Field", EdgeType: "ent.Edge", FK: "Int",
		IndexType: "ent.Index", IndexImport: "entgo.io/ent/schema/index",
	},
	Codegen: func(tools string) []string {
		return []string{filepath.Join(tools, "ent"), "generate", "./ent/schema"}
	},
	Service: func(es []int) string {
		return clientService("ent", "e%[1]d.ID(1)", es)
	},
}

// clientService is the main.go of a service of an ORM whose generated package
// is example.com/tmp/<pkg> (package name pkg), with one subpackage per entity
// and a NewClient that needs no connection. where is the predicate, with the
// entity as %[1]d. The client is never opened, so the program is linked and
// never run.
func clientService(pkg, where string, es []int) string {
	var imports, calls strings.Builder
	for _, k := range es {
		fmt.Fprintf(&imports, "\t\"example.com/tmp/%s/e%d\"\n", pkg, k)
		fmt.Fprintf(&calls, "\tclient.E%[1]d.Query().Where("+where+").Limit(1).All(ctx)\n\tclient.E%[1]d.Query().Count(ctx)\n", k)
	}
	return fmt.Sprintf("package main\n\nimport (\n\t\"context\"\n\n\t\"example.com/tmp/%[1]s\"\n%[2]s)\n\nfunc main() {\n\tctx := context.Background()\n\tclient := %[1]s.NewClient()\n%[3]s}\n",
		pkg, imports.String(), calls.String())
}

// genSkip is the directory of GenDir that countGenerated leaves out, relative
// to GenDir: ent keeps its schema package inside the generated directory, and
// the others have no such directory.
func (o orm) genSkip() string {
	if rel, err := filepath.Rel(o.GenDir, o.SchemaDir); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return ""
}

// orms are the compared ORMs, velox first.
var orms = []orm{veloxORM, veloxeORM, entORM}

// ormByName returns the ORM called name.
func ormByName(name string) (orm, error) {
	for _, o := range orms {
		if o.Name == name {
			return o, nil
		}
	}
	return orm{}, fmt.Errorf("unknown ORM %q (have velox, veloxe, ent)", name)
}

// runtimeVariants maps each runtime variant to the ORM whose runtime app it
// runs: veloxpgx is velox's app on veloxpgx.Open, measured on PostgreSQL
// only. A variant has no build measurements and is never a loss.
var runtimeVariants = map[string]string{"veloxpgx": "velox"}

// runtimeORMByName returns the ORM whose runtime app name runs: name itself
// for an ORM, the variant's ORM for a variant.
func runtimeORMByName(name string) (string, error) {
	if o, ok := runtimeVariants[name]; ok {
		return o, nil
	}
	if _, err := ormByName(name); err == nil {
		return name, nil
	}
	names := make([]string, 0, len(orms)+len(runtimeVariants))
	for _, o := range orms {
		names = append(names, o.Name)
	}
	names = append(names, slices.Sorted(maps.Keys(runtimeVariants))...)
	return "", fmt.Errorf("unknown ORM or variant %q (have %s)", name, strings.Join(names, ", "))
}

// Schema returns the schema package's files for n entities (path relative to
// the app → source): one file per entity, SchemaDir/e<i>.go.
func (o orm) Schema(n int) map[string]string {
	files := make(map[string]string, n)
	for i := range n {
		files[filepath.Join(o.SchemaDir, fmt.Sprintf("e%d.go", i))] = o.entity(i, n)
	}
	return files
}

// entity is the source of entity i's schema file.
func (o orm) entity(i, n int) string {
	d := o.dsl
	var b strings.Builder
	b.WriteString("package schema\n\nimport (\n")
	for _, imp := range strings.Split(d.Imports, "\n") {
		fmt.Fprintf(&b, "\t%s\n", imp)
	}
	if d.IndexType != "" && i > 0 {
		fmt.Fprintf(&b, "\t%q\n", d.IndexImport)
	}
	fmt.Fprintf(&b, ")\n\n// E%[1]d is a generated schema.\ntype E%[1]d struct{ %[2]s }\n\n", i, d.SchemaType)
	fmt.Fprintf(&b, "func (E%d) Fields() []%s {\n\treturn []%s{\n", i, d.FieldType, d.FieldType)
	for _, f := range fieldLines(i, d.FK) {
		fmt.Fprintf(&b, "\t\t%s,\n", f)
	}
	b.WriteString("\t}\n}\n\n")
	fmt.Fprintf(&b, "func (E%d) Edges() []%s {\n\treturn []%s{\n", i, d.EdgeType, d.EdgeType)
	for _, e := range edgesOf(i, n) {
		fmt.Fprintf(&b, "\t\t%s,\n", edgeLine(e))
	}
	b.WriteString("\t}\n}\n")
	if d.IndexType != "" && i > 0 {
		// The edge key's index, which velox's DDL creates by itself.
		fmt.Fprintf(&b, "\nfunc (E%d) Indexes() []%s {\n\treturn []%s{index.Fields(\"prev_id\")}\n}\n", i, d.IndexType, d.IndexType)
	}
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return b.String() // the parse in the tests names the error
	}
	return string(src)
}
