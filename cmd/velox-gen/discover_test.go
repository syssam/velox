package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests of schema discovery: which types of the schema package are
// schemas, and what velox-gen refuses (internal/codegen's discover.go).

// generatedFiles lists the files of dir/gen, without directories.
func generatedFiles(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(filepath.Join(dir, "gen"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		if !de.IsDir() {
			names = append(names, de.Name())
		}
	}
	return names
}

// A schema that embeds another exported schema is refused at its line, with
// the way out: an exported Base is a schema itself, as in ent, and both would
// be entities. Nothing is written.
func TestDiscoverRefusesASchemaEmbeddingASchema(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/s.go": `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Base struct{ schema.Schema }

type Release struct{ Base }

func (Release) Fields() []schema.Field { return []schema.Field{field.String("tag")} }
`})
	code, out, errs := runCLI(t, dir, "./schema")
	want := "velox-gen: schema/s.go:10: Release embeds Base, which is a schema itself, so both would be entities: make Base a shared base by leaving it unexported (type base struct{ schema.Schema }), or share fields with a mixin\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("the output directory was created")
	}
}

// An unexported base is never a schema: the schemas that embed it, at any
// depth, are, and it is not generated. A schema may also implement
// schema.Interface without schema.Schema.
func TestDiscoverSharedBase(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/s.go": `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type base struct{ schema.Schema }

func (base) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

type deeper struct{ base }

type Release struct{ base }

func (Release) Fields() []schema.Field { return []schema.Field{field.String("tag")} }

type Build struct{ deeper }

func (Build) Fields() []schema.Field { return []schema.Field{field.Int("number")} }

// Own declares every method of schema.Interface itself.
type Own struct{}

func (Own) Type()                           {}
func (Own) Fields() []schema.Field          { return []schema.Field{field.String("name")} }
func (Own) Edges() []schema.Edge            { return nil }
func (Own) Indexes() []schema.Index         { return nil }
func (Own) Mixin() []schema.Mixin           { return nil }
func (Own) Annotations() []schema.Annotation { return nil }
`})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "build_velox.go own_velox.go release_velox.go velox.go"; got != want {
		t.Fatalf("generated %s, want %s", got, want)
	}
	release, _ := os.ReadFile(filepath.Join(dir, "gen", "release_velox.go"))
	if !strings.Contains(string(release), "CreatedAt") || !strings.Contains(string(release), "Tag ") {
		t.Errorf("Release lacks the base's mixin or its own field:\n%s", release)
	}
	build, _ := os.ReadFile(filepath.Join(dir, "gen", "build_velox.go"))
	if !strings.Contains(string(build), "CreatedAt") || !strings.Contains(string(build), "Number ") {
		t.Errorf("Build lacks the base's mixin or its own field:\n%s", build)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// An unexported struct that embeds schema.Schema and that nothing embeds is
// refused: it is likely a schema with a lower-case name.
func TestDiscoverRefusesALoneUnexportedSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/user.go": genUserSchema, "schema/s.go": `package schema

import "github.com/syssam/velox/schema"

type group struct{ schema.Schema }
`})
	code, out, errs := runCLI(t, dir, "./schema")
	want := "velox-gen: schema/s.go:5: type group embeds schema.Schema and is unexported: a schema must be exported, and a shared base is embedded by one\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
}

// Every method of a schema may have a pointer receiver, Policy and Hooks
// too. The schema's structure is read through a pointer.
func TestDiscoverPointerReceivers(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := withSchemaMethods(t, map[string]string{
		"schema/board.go": `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

type Board struct{ schema.Schema }

func (*Board) Fields() []schema.Field {
	return []schema.Field{field.String("title").Default("untitled"), field.String("slug")}
}

func (*Board) Edges() []schema.Edge { return []schema.Edge{edge.To("cards", Card.Type)} }

func (*Board) Indexes() []schema.Index { return []schema.Index{index.Fields("slug").Unique()} }

func (*Board) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

func (*Board) Annotations() []schema.Annotation { return []schema.Annotation{schema.Comment("boards")} }

type Card struct{ schema.Schema }

func (*Card) Fields() []schema.Field { return []schema.Field{field.String("text")} }

func (*Card) Edges() []schema.Edge {
	return []schema.Edge{edge.From("board", Board.Type).Ref("cards").Unique()}
}
`,
		"schema/board_policy.go": "package schema\n\nimport (\n\t\"github.com/syssam/velox\"\n\n\t\"example.com/app/gen\"\n)\n\n" +
			"func (*Board) Policy() velox.Policy[gen.Board] { return velox.Policy[gen.Board]{} }\n\n" +
			"func (*Card) Hooks() []velox.Hook[gen.Card] { return nil }\n",
	}, tempModule)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	board, _ := os.ReadFile(filepath.Join(dir, "gen", "board_velox.go"))
	for _, want := range []string{"Title ", "Slug ", "CreatedAt", "Cards ", "BindPolicy"} {
		if !strings.Contains(string(board), want) {
			t.Errorf("board_velox.go lacks %q:\n%s", want, board)
		}
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// What looks like a schema and is not one is refused at its line, each
// with its reason, and nothing is written.
func TestDiscoverRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/user.go": genUserSchema, "schema/s.go": `package schema

import "github.com/syssam/velox/schema"

type Ptr struct{ *schema.Schema }

type Generic[T any] struct{ schema.Schema }

type Alias = User

type Wrong struct{ schema.Schema }

func (Wrong) Fields() []string { return nil }

type Hidden struct {
	schema.Schema
	Edges []schema.Edge
}

type Deep struct{ ptrBase }

type ptrBase struct{ *schema.Schema }

type Named int

func (Named) Type()                            {}
func (Named) Fields() []schema.Field           { return nil }
func (Named) Edges() []schema.Edge             { return nil }
func (Named) Indexes() []schema.Index          { return nil }
func (Named) Mixin() []schema.Mixin            { return nil }
func (Named) Annotations() []schema.Annotation { return nil }
`})
	code, out, errs := runCLI(t, dir, "./schema")
	want := []string{
		"velox-gen: schema/s.go:5: Ptr embeds schema.Schema through a pointer: embed it by value, since a nil pointer's methods panic",
		"schema/s.go:7: Generic is generic: a schema has no type parameters, since velox-gen makes one entity of it",
		"schema/s.go:9: Alias is an alias of User: a schema is a type of its own, declared type Alias struct{ ... }",
		"schema/s.go:11: Wrong embeds schema.Schema but does not implement schema.Interface: its method Fields() []string should be Fields() []schema.Field",
		"schema/s.go:15: Hidden embeds schema.Schema but does not implement schema.Interface: it has no method Edges() []schema.Edge; a field or another method of that name hides schema.Schema's",
		"schema/s.go:20: Deep embeds schema.Schema through a pointer: embed it by value, since a nil pointer's methods panic",
		"schema/s.go:24: the type of Named is int: a schema is a struct type",
	}
	if code != 1 || out != "" || errs != strings.Join(want, "\n")+"\n" {
		t.Fatalf("%d %q\n%s\nwant\n%s", code, out, errs, strings.Join(want, "\n"))
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("the output directory was created")
	}
}

// A schema's methods may be in another file than its type, and its edges may
// name a schema declared later or in another file. A _test.go file is not
// part of the package, and its types are not schemas; a file no build
// includes is not read.
func TestDiscoverAcrossFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{
		"schema/a_types.go": "package schema\n\nimport \"github.com/syssam/velox/schema\"\n\ntype Author struct{ schema.Schema }\n",
		"schema/b_methods.go": `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

func (Author) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Author) Edges() []schema.Edge { return []schema.Edge{edge.To("books", Book.Type)} }
`,
		"schema/c_book.go": `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

func (Book) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", Author.Type).Ref("books").Unique()}
}

type Book struct{ schema.Schema }

func (Book) Fields() []schema.Field { return []schema.Field{field.String("title")} }
`,
		"schema/d_test.go":   "package schema\n\nimport \"github.com/syssam/velox/schema\"\n\ntype Fixture struct{ schema.Schema }\n",
		"schema/e_ignore.go": "//go:build ignore\n\npackage schema\n\nimport \"github.com/syssam/velox/schema\"\n\ntype Ignored struct{ schema.Schema }\n",
	})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "author_velox.go book_velox.go velox.go"; got != want {
		t.Fatalf("generated %s, want %s", got, want)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// A file of the schema package that only some builds include is refused at
// its constraint: the model would depend on the build.
func TestDiscoverRefusesABuildConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	for name, src := range map[string]string{
		"schema/extra.go":       "//go:build pro\n\npackage schema\n\nimport \"github.com/syssam/velox/schema\"\n\ntype Pro struct{ schema.Schema }\n",
		"schema/extra_linux.go": "package schema\n",
	} {
		dir := tempModule(t, map[string]string{"schema/user.go": genUserSchema, name: src})
		code, out, errs := runCLI(t, dir, "./schema")
		want := "velox-gen: " + name + ":1: go builds this file of the schema package only on some systems or with some tags; velox-gen reads the schemas from files that every build includes\n"
		if code != 1 || out != "" || errs != want {
			t.Errorf("%s: %d %q %q, want %q", name, code, out, errs, want)
		}
	}
}

// A mixin from another package may embed another mixin, override its
// methods and declare indexes and annotations; a schema lists it as any
// other.
func TestDiscoverMixinFromAnotherPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{
		"mixins/m.go": `package mixins

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Audit records who changed a row.
type Audit struct{ mixin.Schema }

func (Audit) Fields() []schema.Field { return []schema.Field{field.String("changed_by").Optional()} }

func (Audit) Indexes() []schema.Index { return []schema.Index{index.Fields("changed_by")} }

// Full is Audit and the times.
type Full struct{ Audit }

func (f Full) Fields() []schema.Field {
	return append(f.Audit.Fields(), field.String("reason").Optional())
}

func (Full) Annotations() []schema.Annotation { return []schema.Annotation{schema.Comment("audited")} }
`,
		"schema/doc.go": `package schema

import (
	"example.com/app/mixins"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Doc struct{ schema.Schema }

func (Doc) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}, mixins.Full{}} }

func (Doc) Fields() []schema.Field { return []schema.Field{field.String("body")} }
`,
	})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	doc, _ := os.ReadFile(filepath.Join(dir, "gen", "doc_velox.go"))
	for _, want := range []string{"CreatedAt", "ChangedBy ", "Reason ", "Body "} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("doc_velox.go lacks %q:\n%s", want, doc)
		}
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "doc_velox.go velox.go"; got != want {
		t.Errorf("generated %s, want %s", got, want)
	}
}

// A schema named like a many-to-many join entity collides with it; the
// join entity's empty struct, which carries its Policy, is not a schema.
func TestDiscoverJoinEntityName(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{
		"schema/schema.go": genFriendsSchema,
		"schema/join.go":   "package schema\n\nimport \"github.com/syssam/velox/schema\"\n\ntype UserFriend struct{ schema.Schema }\n",
	})
	code, out, errs := runCLI(t, dir, "./schema")
	if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: ") || !strings.Contains(errs, "UserFriend") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(dir, "schema", "join.go"), "package schema\n\n// UserFriend is the join entity of User's friends, which has no schema.\ntype UserFriend struct{}\n")
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("with the empty struct: %d %q", code, errs)
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "user_friend_velox.go user_velox.go velox.go"; got != want {
		t.Errorf("generated %s, want %s", got, want)
	}
}

// Regeneration after a rename, a deletion and a new edge to an entity not
// generated yet leaves only the files of the model: a stale entity's file is
// removed, and -check lists it until then.
func TestDiscoverIncrementalRegeneration(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	// Tag becomes Label, User gains an edge to Note, which is new.
	writeFile(t, filepath.Join(dir, "schema", "tag.go"), strings.ReplaceAll(genTagSchema, "Tag", "Label"))
	if err := os.Rename(filepath.Join(dir, "schema", "tag.go"), filepath.Join(dir, "schema", "label.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "schema", "note.go"), `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Note struct{ schema.Schema }

func (Note) Fields() []schema.Field { return []schema.Field{field.String("text")} }

func (User) Edges() []schema.Edge { return []schema.Edge{edge.To("notes", Note.Type)} }
`)
	code, out, errs := runCLI(t, dir, "-check", "./schema")
	for _, want := range []string{"gen/tag_velox.go is out of date", "gen/label_velox.go is out of date", "gen/note_velox.go is out of date", "gen/user_velox.go is out of date"} {
		if !strings.Contains(errs, want) {
			t.Errorf("-check lacks %q: %d %q %q", want, code, out, errs)
		}
	}
	if code != 3 {
		t.Errorf("-check: %d", code)
	}
	code, out, errs = runCLI(t, dir, "./schema")
	if want := "gen/label_velox.go\ngen/note_velox.go\ngen/tag_velox.go\ngen/user_velox.go\n"; code != 0 || out != want {
		t.Fatalf("regenerate: %d %q %q, want %q", code, out, errs, want)
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "label_velox.go note_velox.go user_velox.go velox.go"; got != want {
		t.Fatalf("generated %s, want %s", got, want)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	// Deleting Note removes its file, and User's edge with it.
	if err := os.Remove(filepath.Join(dir, "schema", "note.go")); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 || out != "gen/note_velox.go\ngen/user_velox.go\n" {
		t.Fatalf("after deleting Note: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
}

// A package that the schema's structure reaches and that does not compile,
// such as a helper that uses a generated package not written yet, is
// reported with the chain of imports and what to do; the Hooks and Policy
// methods are pruned only from the schema package itself.
func TestDiscoverHelperThatDoesNotCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{
		"helper/h.go": "package helper\n\n// Name is a field's name.\nconst Name = \"name\"\n",
		"schema/user.go": `package schema

import (
	"example.com/app/helper"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String(helper.Name)} }
`,
	})
	writeFile(t, filepath.Join(dir, "helper", "h.go"), "package helper\n\n// Name is a field's name.\nconst Name = 1 + \"name\"\n")
	code, out, errs := runCLI(t, dir, "./schema")
	for _, want := range []string{"example.com/app/schema imports example.com/app/helper, which does not compile", "Move what needs the generated package out of what Fields, Edges, Indexes, Mixin and Annotations reach"} {
		if code != 1 || out != "" || !strings.Contains(errs, want) {
			t.Errorf("%d %q %q, want %q", code, out, errs, want)
		}
	}
}
