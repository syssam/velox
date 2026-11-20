package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkedSchema is a schema of three entities: a comment is on a doc, and a
// doc has an owner.
const linkedSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (User) Edges() []schema.Edge { return []schema.Edge{edge.To("docs", Doc.Type)} }

type Doc struct{ schema.Schema }

func (Doc) Fields() []schema.Field { return []schema.Field{field.Int64("owner_id")} }

func (Doc) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("owner", User.Type).Ref("docs").Unique().Required().Field("owner_id"),
		edge.To("comments", Comment.Type),
	}
}

type Comment struct{ schema.Schema }

func (Comment) Fields() []schema.Field { return []schema.Field{field.Int64("doc_id")} }

func (Comment) Edges() []schema.Edge {
	return []schema.Edge{edge.From("doc", Doc.Type).Ref("comments").Unique().Required().Field("doc_id")}
}
`

// linkedModule writes linkedSchema and behaviour, as schema/behaviour.go,
// and returns the module's directory, its graph and the entities' bound
// methods.
func linkedModule(t *testing.T, behaviour string) (string, *Graph, map[string]Bound) {
	t.Helper()
	dir := module(t, map[string]string{"schema/schema.go": linkedSchema, "schema/behaviour.go": behaviour})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := SchemaMethods(filepath.Join(dir, "schema"), []string{"User", "Doc", "Comment"})
	if err != nil {
		t.Fatal(err)
	}
	return dir, g, bound
}

// A package whose schema declares behaviour gets one velox.EntityTable in
// velox.go, of every entity by its name and its table, and every descriptor
// names it, so that a relation or an entity named in a helper of the schema
// package, or in another package, is found when the statement runs. A package
// without behaviour has none, and its schema is not read.
func TestEntityTableWhenBehaviour(t *testing.T) {
	dir, g, bound := linkedModule(t, `package schema

import (
	"context"

	"github.com/syssam/velox"
)

const docEdge = "doc"

func (Comment) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) { return visible(), nil }}
}

func visible() velox.Predicate { return velox.HasEdgeWith(docEdge, velox.HasEdge("owner")) }
`)
	if _, err := CheckBehaviour(dir, "./schema", g, bound, nil, offline()); err != nil {
		t.Fatal(err)
	}
	files, err := Generate(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Bound: bound})
	if err != nil {
		t.Fatal(err)
	}
	table := fileText(t, files, "velox.go")
	for _, want := range []string{
		"var veloxEntities velox.EntityTable",
		"func veloxEntityTable() *velox.EntityTable {",
		`velox.LinkEntity[Comment]("Comment", "comments"),`,
		`velox.LinkEntity[Doc]("Doc", "docs"),`,
		`velox.LinkEntity[User]("User", "users"),`,
	} {
		if !strings.Contains(table, want) {
			t.Errorf("velox.go lacks %s:\n%s", want, table)
		}
	}
	for _, file := range []string{"comment.go", "doc.go", "user.go"} {
		src := fileText(t, files, file)
		if !strings.Contains(src, "Entities: veloxEntityTable,") || strings.Contains(src, "Linked[") {
			t.Errorf("%s does not name the entity table, or links a relation:\n%s", file, src)
		}
	}
	// Without behaviour, there is no table, and the schema is not read.
	if warnings, err := CheckBehaviour(dir, "./schema", g, nil, nil, offline()); err != nil || warnings != nil {
		t.Errorf("no behaviour: %v, %v", warnings, err)
	}
	files, err = Generate(g, Config{Package: "gen", GenPath: "example.com/tmp/gen"})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"velox.go", "comment.go"} {
		if src := fileText(t, files, file); strings.Contains(src, "EntityTable") || strings.Contains(src, "Entities:") {
			t.Errorf("%s without behaviour names an entity table:\n%s", file, src)
		}
	}
}

// A relation named with a value velox-gen cannot read is a warning at its
// line, not a refusal: velox finds its target in the entity table.
func TestEdgeNameNotConstantWarns(t *testing.T) {
	dir, g, bound := linkedModule(t, `package schema

import (
	"context"

	"github.com/syssam/velox"
)

var edgeName = "doc"

func (Comment) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
		return velox.HasEdge(edgeName), nil
	}}
}
`)
	warnings, err := CheckBehaviour(dir, "./schema", g, bound, nil, offline())
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "behaviour.go:13: velox.HasEdge(edgeName) names its relation with a value velox-gen cannot read") {
		t.Fatalf("got %q, %v", warnings, err)
	}
}

// A schema package that imports the generated package is refused: by a
// *FileError at the import when a file of it does, and by an error that
// names the chain when another package between them does.
func TestBehaviourRefusesSchemaImportingGen(t *testing.T) {
	const genSrc = "package gen\n\nconst X = 1\n"
	t.Run("direct", func(t *testing.T) {
		dir := module(t, map[string]string{
			"schema/schema.go": linkedSchema,
			"gen/gen.go":       genSrc,
			"schema/uses.go":   "package schema\n\nimport \"example.com/tmp/gen\"\n\nvar _ = gen.X\n",
		})
		_, err := Behaviour(dir, "./schema", "example.com/tmp/gen", offline(), []string{"User"})
		if fe, ok := errors.AsType[*FileError](err); !ok || filepath.Base(fe.Path) != "uses.go" || fe.Line != 3 ||
			fe.Reason != "the schema package imports example.com/tmp/gen: "+genImportReason {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("chain", func(t *testing.T) {
		dir := module(t, map[string]string{
			"schema/schema.go": linkedSchema,
			"gen/gen.go":       genSrc,
			"helper/helper.go": "package helper\n\nimport \"example.com/tmp/gen\"\n\nconst Y = gen.X\n",
			"schema/uses.go":   "package schema\n\nimport \"example.com/tmp/helper\"\n\nvar _ = helper.Y\n",
		})
		_, err := Behaviour(dir, "./schema", "example.com/tmp/gen", offline(), []string{"User"})
		want := "example.com/tmp/schema imports example.com/tmp/helper imports example.com/tmp/gen: " + genImportReason
		if err == nil || err.Error() != want {
			t.Fatalf("got %v, want %s", err, want)
		}
		if err := CheckGenImports(dir, "./schema", "example.com/tmp/gen", offline()); err == nil || err.Error() != want {
			t.Fatalf("CheckGenImports: got %v", err)
		}
	})
	// gen, generated earlier, imports the schema: go list refuses the
	// cycle, and the refusal names the import still.
	t.Run("cycle", func(t *testing.T) {
		dir := module(t, map[string]string{"schema/schema.go": linkedSchema})
		files := map[string]string{
			"gen/gen.go":     "package gen\n\nimport _ \"example.com/tmp/schema\"\n\nconst X = 1\n",
			"schema/uses.go": "package schema\n\nimport \"example.com/tmp/gen\"\n\nvar _ = gen.X\n",
		}
		for name, src := range files {
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		err := CheckGenImports(dir, "./schema", "example.com/tmp/gen", offline())
		if fe, ok := errors.AsType[*FileError](err); !ok || filepath.Base(fe.Path) != "uses.go" || fe.Line != 3 {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("none", func(t *testing.T) {
		dir := module(t, map[string]string{"schema/schema.go": linkedSchema})
		if err := os.WriteFile(filepath.Join(dir, "schema", "p.go"), []byte("package schema\n\nimport \"github.com/syssam/velox\"\n\nfunc (User) Policy() velox.Policy { return velox.Policy{} }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Behaviour(dir, "./schema", "example.com/tmp/gen", offline(), []string{"User"})
		if err != nil || !got["User"].Policy {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}
