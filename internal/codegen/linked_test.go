package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// A relation that behaviour names with velox.HasEdge or HasEdgeWith,
// through a helper of the schema package and a named constant, is linked:
// its descriptor wraps it in velox.Linked. A HasEdge in HasEdgeWith's
// conditions names a relation of the edge's target. A relation no
// behaviour names is not linked.
func TestEdgeLinkedWhenBehaviourNamesIt(t *testing.T) {
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
	linked, _, err := CheckBehaviour(dir, "./schema", g, bound, nil, offline())
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"Comment": {"doc"}, "Doc": {"owner"}}; !reflect.DeepEqual(linked, want) {
		t.Fatalf("got %v, want %v", linked, want)
	}
	files, err := Generate(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Bound: bound, Linked: linked})
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"comment.go": `velox.Linked[Doc](velox.EdgeTo[Doc]("doc", `,
		"doc.go":     `velox.Linked[User](velox.EdgeTo[User]("owner", `,
	} {
		if src := fileText(t, files, file); !strings.Contains(src, want) {
			t.Errorf("%s lacks %s:\n%s", file, want, src)
		}
	}
	for _, file := range []string{"user.go", "doc.go"} {
		if src := fileText(t, files, file); strings.Contains(src, `Linked[Comment]`) || strings.Contains(src, `Linked[Doc]`) {
			t.Errorf("%s links a relation no behaviour names:\n%s", file, src)
		}
	}
	// Without behaviour, nothing is linked, and the schema is not read.
	if linked, _, err := CheckBehaviour(dir, "./schema", g, nil, nil, offline()); err != nil || linked != nil {
		t.Errorf("no behaviour: %v, %v", linked, err)
	}
}

// A relation named with a value velox-gen cannot read is refused at its
// line: velox-gen could not link its target.
func TestEdgeNameMustBeConstant(t *testing.T) {
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
	_, _, err := CheckBehaviour(dir, "./schema", g, bound, nil, offline())
	var fe *FileError
	if !errors.As(err, &fe) || filepath.Base(fe.Path) != "behaviour.go" || fe.Line != 13 ||
		!strings.Contains(fe.Reason, "velox.HasEdge(edgeName) names its relation with a value velox-gen cannot read") {
		t.Fatalf("got %v", err)
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
		var fe *FileError
		if !errors.As(err, &fe) || filepath.Base(fe.Path) != "uses.go" || fe.Line != 3 ||
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
		var fe *FileError
		if !errors.As(err, &fe) || filepath.Base(fe.Path) != "uses.go" || fe.Line != 3 {
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
