package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const genUserSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.String("email").Optional()}
}
`

const genTagSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Tag struct{ schema.Schema }

func (Tag) Fields() []schema.Field { return []schema.Field{field.String("label")} }
`

// genModule is a module whose schema package declares User and Tag.
func genModule(t *testing.T, extra map[string]string) string {
	t.Helper()
	return genModuleWith(t, extra, tempModule)
}

// genModuleSerial is genModule for a test that must not run in parallel.
func genModuleSerial(t *testing.T, extra map[string]string) string {
	t.Helper()
	return genModuleWith(t, extra, tempModuleSerial)
}

func genModuleWith(t *testing.T, extra map[string]string, mk func(*testing.T, map[string]string) string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a module")
	}
	files := map[string]string{"schema/user.go": genUserSchema, "schema/tag.go": genTagSchema}
	maps.Copy(files, extra)
	return withSchemaMethods(t, files, mk)
}

// withSchemaMethods makes the module of files with mk, but writes the schema
// files that import the generated package after it: mk's go mod tidy cannot
// resolve a package that velox-gen has not written yet.
func withSchemaMethods(t *testing.T, files map[string]string, mk func(*testing.T, map[string]string) string) string {
	t.Helper()
	later := map[string]string{}
	for name, src := range files {
		if strings.HasPrefix(name, "schema/") && strings.Contains(src, `"example.com/app/gen`) {
			later[name] = src
			delete(files, name)
		}
	}
	dir := mk(t, files)
	for name, src := range later {
		writeFile(t, filepath.Join(dir, name), src)
	}
	return dir
}

func mtimes(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]time.Time{}
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			t.Fatal(err)
		}
		m[de.Name()] = fi.ModTime()
	}
	return m
}

func TestGenerateWritesThenNothingChanges(t *testing.T) {
	dir := genModule(t, nil)
	code, out, errs := runCLI(t, dir, "./schema")
	if want := "gen/tag_velox.go\ngen/user_velox.go\ngen/velox.go\n"; code != 0 || out != want {
		t.Fatalf("first run: %d %q %q", code, out, errs)
	}
	for _, f := range []string{"tag_velox.go", "user_velox.go", "velox.go"} {
		if !isGenerated(filepath.Join(dir, "gen", f)) {
			t.Errorf("%s lacks the header", f)
		}
	}
	before := mtimes(t, filepath.Join(dir, "gen"))
	time.Sleep(20 * time.Millisecond)
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("second run: %d %q %q", code, out, errs)
	}
	after := mtimes(t, filepath.Join(dir, "gen"))
	for name, mt := range before {
		if !after[name].Equal(mt) {
			t.Errorf("%s was rewritten", name)
		}
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
}

// The sweep of -out ./gen leaves alone a nested -out ./gen/b, another run's
// output, and a directory that holds a go.mod, another module's: each run
// keeps the other's files, and -check passes for both.
func TestStaleSweepSkipsNestedGenPackage(t *testing.T) {
	const itemSchema = `package schemab

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Item struct{ schema.Schema }

func (Item) Fields() []schema.Field { return []schema.Field{field.String("sku")} }
`
	other := header + "\n\npackage other\n"
	dir := genModule(t, map[string]string{
		"schemab/item.go":    itemSchema,
		"gen/mod/go.mod":     "module example.com/other\n",
		"gen/mod/other.go":   other,
		"gen/mod/x/other.go": other,
	})
	b := []string{"-out", "./gen/b", "-package", "b", "./schemab"}
	if code, out, errs := runCLI(t, dir, b...); code != 0 || out != "gen/b/item_velox.go\ngen/b/velox.go\n" {
		t.Fatalf("gen/b: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 || out != "gen/tag_velox.go\ngen/user_velox.go\ngen/velox.go\n" {
		t.Fatalf("gen: %d %q %q", code, out, errs)
	}
	for _, f := range []string{"b/item_velox.go", "b/velox.go", "mod/other.go", "mod/x/other.go"} {
		if _, err := os.Stat(filepath.Join(dir, "gen", f)); err != nil {
			t.Errorf("gen/%s: %v", f, err)
		}
	}
	if code, out, errs := runCLI(t, dir, append([]string{"-check"}, b...)...); code != 0 {
		t.Errorf("-check gen/b: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 {
		t.Errorf("-check gen: %d %q %q", code, out, errs)
	}
}

func TestGenerateDefaults(t *testing.T) {
	dir := genModule(t, nil)
	// No arguments: the package is ./schema and the output ./gen.
	if code, _, errs := runCLI(t, dir); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	src, err := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go"))
	if err != nil || !strings.Contains(string(src), "package gen\n") {
		t.Fatalf("%v\n%s", err, src)
	}
	// -out and -package: the package name defaults to the base of -out.
	if code, out, errs := runCLI(t, dir, "-out", "model/store", "./schema"); code != 0 || !strings.Contains(out, "model/store/user_velox.go") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	src, _ = os.ReadFile(filepath.Join(dir, "model", "store", "user_velox.go"))
	if !strings.Contains(string(src), "package store\n") {
		t.Errorf("package is not the base of -out:\n%.200s", src)
	}
	if code, _, errs := runCLI(t, dir, "-out", "model/store", "-package", "persist", "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	src, _ = os.ReadFile(filepath.Join(dir, "model", "store", "user_velox.go"))
	if !strings.Contains(string(src), "package persist\n") {
		t.Errorf("-package ignored:\n%.200s", src)
	}
}

func TestCheckAndRewrite(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	userFile := filepath.Join(dir, "gen", "keep.go")
	writeFile(t, userFile, "package gen\n\nconst Keep = 1\n")

	writeFile(t, filepath.Join(dir, "schema", "user.go"), strings.Replace(genUserSchema, `field.String("name"),`, `field.String("name"), field.Int("age"),`, 1))
	before, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go"))
	code, out, errs := runCLI(t, dir, "-check", "./schema")
	if code != 3 || out != "" || !strings.Contains(errs, "velox-gen: gen/user_velox.go is out of date\n") ||
		!strings.Contains(errs, "velox-gen: run go tool velox-gen ./schema\n") || strings.Contains(errs, "tag_velox.go") {
		t.Fatalf("-check: %d %q %q", code, out, errs)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go")); string(after) != string(before) {
		t.Fatal("-check changed the file")
	}

	code, out, errs = runCLI(t, dir, "./schema")
	if code != 0 || !strings.Contains(out, "gen/user_velox.go\n") || strings.Contains(out, "tag_velox.go") {
		t.Fatalf("rewrite: %d %q %q", code, out, errs)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go")); !strings.Contains(string(after), "Age") {
		t.Errorf("not rewritten:\n%s", after)
	}
	if code, _, errs := runCLI(t, dir, "-check", "./schema"); code != 0 {
		t.Fatalf("-check after rewrite: %d %q", code, errs)
	}

	// A removed entity: -check names its file, the next run deletes it.
	if err := os.Remove(filepath.Join(dir, "schema", "tag.go")); err != nil {
		t.Fatal(err)
	}
	code, _, errs = runCLI(t, dir, "-check", "./schema")
	if code != 3 || !strings.Contains(errs, "velox-gen: gen/tag_velox.go is out of date") {
		t.Fatalf("-check after removal: %d %q", code, errs)
	}
	code, out, errs = runCLI(t, dir, "./schema")
	if code != 0 || !strings.Contains(out, "gen/tag_velox.go\n") {
		t.Fatalf("delete: %d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "tag_velox.go")); !os.IsNotExist(err) {
		t.Errorf("the file of the removed entity is still there: %v", err)
	}
	if b, err := os.ReadFile(userFile); err != nil || string(b) != "package gen\n\nconst Keep = 1\n" {
		t.Errorf("the user's file did not survive: %v %q", err, b)
	}
}

// schemaUserPolicy is a Policy for User, declared in the schema package.
const schemaUserPolicy = "package schema\n\nimport (\n\t\"github.com/syssam/velox\"\n\n\t\"example.com/app/gen/entity\"\n)\n\n" +
	"func (User) Policy() velox.Policy[entity.User] { return velox.Policy[entity.User]{} }\n"

// The schema package's Policy splits gen, generated from nothing: the schema
// imports gen/entity, which does not exist yet. gen/entity holds the
// entities, and gen re-exports them and binds the schema in its init.
// Removing the Policy removes gen/entity, and gen holds the entities again.
func TestGenerateBindsSchemaMethods(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/user_policy.go": schemaUserPolicy})
	code, out, errs := runCLI(t, dir, "./schema")
	if want := "gen/entity/entity.go\ngen/entity/runtime_velox.go\ngen/entity/tag_velox.go\ngen/entity/user_velox.go\ngen/tag_velox.go\ngen/user_velox.go\ngen/velox.go\n"; code != 0 || out != want {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
	user, _ := os.ReadFile(filepath.Join(dir, "gen", "entity", "user_velox.go"))
	if !strings.Contains(string(user), `policy := veloxPolicy[User](rt.User, "User")`) || !strings.Contains(string(user), "velox.BindPolicy(policy())") {
		t.Errorf("entity/user_velox.go does not bind the policy:\n%s", user)
	}
	facade, _ := os.ReadFile(filepath.Join(dir, "gen", "velox.go"))
	for _, want := range []string{"User: &sch.User{},", `sch "example.com/app/schema"`, "_ func(*sch.User) velox.Policy[entity.User] = (*sch.User).Policy"} {
		if !strings.Contains(string(facade), want) {
			t.Errorf("velox.go lacks %q:\n%s", want, facade)
		}
	}
	reexp, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go"))
	if !strings.Contains(string(reexp), "type User = entity.User") || !strings.Contains(string(reexp), "var Users = entity.Users") {
		t.Errorf("user_velox.go does not re-export User:\n%s", reexp)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
	if err := os.Remove(filepath.Join(dir, "schema", "user_policy.go")); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("without the policy: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "entity")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gen/entity is still there: %v", err)
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "tag_velox.go user_velox.go velox.go"; got != want {
		t.Errorf("generated %s, want %s", got, want)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify without the policy: %d %q %q", code, out, errs)
	}
}

// The policy test of the generated package itself: velox-gen binds what
// the schema declares.
func TestGenerateBindsUserMethods(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/user_policy.go": schemaUserPolicy})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	user, _ := os.ReadFile(filepath.Join(dir, "gen", "entity", "user_velox.go"))
	if !strings.Contains(string(user), "velox.BindPolicy(policy())") {
		t.Errorf("user_velox.go does not bind the policy:\n%s", user)
	}
	if strings.Contains(string(user), "BindHooks") {
		t.Errorf("user_velox.go binds hooks the user did not declare")
	}
	tag, _ := os.ReadFile(filepath.Join(dir, "gen", "entity", "tag_velox.go"))
	if strings.Contains(string(tag), "BindPolicy") {
		t.Errorf("tag_velox.go binds a policy")
	}
}

// The first Policy, written as before the split with gen.User, is refused at
// the import with what to write instead; written with entity.User, it
// generates. A helper package the schema imports may import gen/entity, and
// not gen, which imports the schema.
func TestGenerateTransitionToSplit(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	old := strings.ReplaceAll(strings.ReplaceAll(schemaUserPolicy, "entity.", "gen."), "/gen/entity", "/gen")
	writeFile(t, filepath.Join(dir, "schema", "user_policy.go"), old)
	code, out, errs := runCLI(t, dir, "./schema")
	want := "velox-gen: schema/user_policy.go:6: the schema package imports \"example.com/app/gen\", the generated package, which imports the schema package to bind its Hooks and Policy: " +
		"import \"example.com/app/gen/entity\", which holds the entities, and write entity.User for gen.User\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
	if code, _, errs := runCLI(t, dir, "-check", "./schema"); code != 1 || errs != want {
		t.Errorf("-check: %d %q, want %q", code, errs, want)
	}
	writeFile(t, filepath.Join(dir, "schema", "user_policy.go"), schemaUserPolicy)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("with entity: %d %q", code, errs)
	}
	// A helper that imports gen is refused with the chain; one that imports
	// gen/entity is fine.
	writeFile(t, filepath.Join(dir, "helper", "h.go"), "package helper\n\nimport \"example.com/app/gen\"\n\n// Users is gen.Users.\nvar Users = gen.Users\n")
	writeFile(t, filepath.Join(dir, "schema", "use.go"), "package schema\n\nimport \"example.com/app/helper\"\n\nvar _ = helper.Users\n")
	code, out, errs = runCLI(t, dir, "./schema")
	want = "velox-gen: example.com/app/schema imports example.com/app/helper imports example.com/app/gen: the generated package imports the schema package to bind its Hooks and Policy, " +
		"so a package the schema imports must import \"example.com/app/gen/entity\", which holds the entities, instead\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("helper: %d %q %q, want %q", code, out, errs, want)
	}
	writeFile(t, filepath.Join(dir, "helper", "h.go"), "package helper\n\nimport \"example.com/app/gen/entity\"\n\n// Users is entity.Users.\nvar Users = entity.Users\n")
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("helper of entity: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// velox.go has the package comment, unless a file of the user's in the output
// directory already has one; -check agrees either way.
func TestGeneratePackageDoc(t *testing.T) {
	const doc = "// Package gen holds the entities of example.com/app/schema, generated by velox-gen.\npackage gen\n"
	for _, c := range []struct {
		name  string
		extra map[string]string
		want  bool
	}{
		{"generated", nil, true},
		{"the user's", map[string]string{"gen/doc.go": "// Package gen is mine.\npackage gen\n"}, false},
	} {
		dir := genModule(t, c.extra)
		if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
			t.Fatalf("%s: %d %q", c.name, code, errs)
		}
		src, _ := os.ReadFile(filepath.Join(dir, "gen", "velox.go"))
		if got := strings.Contains(string(src), doc); got != c.want {
			t.Errorf("%s: velox.go has the package comment: %v, want %v\n%s", c.name, got, c.want, src)
		}
		if !c.want && strings.Contains(string(src), "// Package") {
			t.Errorf("%s: velox.go has a package comment:\n%s", c.name, src)
		}
		if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
			t.Errorf("%s: -check: %d %q %q", c.name, code, out, errs)
		}
	}
}

// A Policy and Hooks added to the schema after generation are not bound:
// -check says the files of package entity are missing, and that gen's,
// which now re-export them, are out of date.
func TestPolicyAddedAfterGenerationFailsCheck(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "schema", "user_policy.go"), schemaUserPolicy+
		"\nfunc (User) Hooks() []velox.Hook[entity.User] { return nil }\n")
	code, out, errs := runCLI(t, dir, "-check", "./schema")
	if code != 3 || out != "" || !strings.Contains(errs, "velox-gen: gen/user_velox.go is out of date\n") ||
		!strings.Contains(errs, "velox-gen: gen/entity/runtime_velox.go is out of date\n") || !strings.Contains(errs, "velox-gen: gen/entity/user_velox.go is out of date\n") || !strings.Contains(errs, "velox-gen: gen/tag_velox.go is out of date\n") {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	// Regenerating binds both, and the check passes.
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("regenerate: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check after regenerating: %d %q %q", code, out, errs)
	}
}

// -check fails for a generated file that is missing, as for one that is stale.
func TestCheckFindsAMissingFile(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "-check", "./schema"); code != 3 || !strings.Contains(errs, "velox-gen: gen/user_velox.go is out of date\n") {
		t.Errorf("-check before any generation: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("-check wrote files")
	}
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if err := os.Remove(filepath.Join(dir, "gen", "tag_velox.go")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "-check", "./schema")
	if code != 3 || out != "" || !strings.Contains(errs, "velox-gen: gen/tag_velox.go is out of date\n") || strings.Contains(errs, "user_velox.go") {
		t.Errorf("-check with a missing file: %d %q %q", code, out, errs)
	}
}

func TestGenerateRefusesAForeignFile(t *testing.T) {
	dir := genModule(t, map[string]string{"gen/user_velox.go": "package gen\n\n// mine\n"})
	code, out, errs := runCLI(t, dir, "./schema")
	if code != 1 || out != "" || !strings.Contains(errs, "gen/user_velox.go was not written by velox-gen; move it") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	des, _ := os.ReadDir(filepath.Join(dir, "gen"))
	if len(des) != 1 {
		t.Errorf("files were written: %v", des)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go")); string(b) != "package gen\n\n// mine\n" {
		t.Errorf("the foreign file changed: %q", b)
	}
	// -check refuses too.
	if code, _, errs := runCLI(t, dir, "-check", "./schema"); code != 1 || !strings.Contains(errs, "was not written by velox-gen") {
		t.Errorf("-check: %d %q", code, errs)
	}
}

func TestGenerateRefusesARedeclaredType(t *testing.T) {
	dir := genModule(t, map[string]string{"gen/mine.go": "package gen\n\ntype User struct{}\n"})
	code, _, errs := runCLI(t, dir, "./schema")
	if want := "velox-gen: gen/mine.go: declares type User, which velox-gen generates\n"; code != 1 || errs != want {
		t.Fatalf("%d %q, want %q", code, errs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "user_velox.go")); !os.IsNotExist(err) {
		t.Error("a file was written")
	}
}

// A Policy or Hooks method in the generated package is an error that says
// where it goes, with a path relative to the working directory, and nothing
// is written. So is one in the schema package of another shape.
func TestGenerateRefusesMisplacedMethods(t *testing.T) {
	for _, c := range []struct{ name, file, src, want string }{
		{"in gen", "gen/user_policy.go", "package gen\n\nimport \"github.com/syssam/velox\"\n\nfunc (*User) Policy() velox.Policy[User] { return velox.Policy[User]{} }\n",
			"velox-gen: gen/user_policy.go:5: declares User.Policy in the generated package; declare it in the schema package, on User: func (User) Policy() velox.Policy[entity.User]\n"},
		{"wrong shape", "schema/user_policy.go", "package schema\n\nimport \"github.com/syssam/velox\"\n\nfunc (User) Policy() *velox.Policy[User] { return nil }\n",
			"velox-gen: schema/user_policy.go:5: User.Policy must take nothing and return velox.Policy[entity.User] (func (User) Policy() velox.Policy[entity.User])\n"},
	} {
		dir := genModule(t, map[string]string{c.file: c.src})
		code, out, errs := runCLI(t, dir, "./schema")
		if code != 1 || out != "" || errs != c.want {
			t.Fatalf("%s: %d %q %q, want %q", c.name, code, out, errs, c.want)
		}
		if _, err := os.Stat(filepath.Join(dir, "gen", "user_velox.go")); !os.IsNotExist(err) {
			t.Errorf("%s: a file was written", c.name)
		}
	}
}

const genFriendsSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (User) Edges() []schema.Edge { return []schema.Edge{edge.To("friends", User.Type)} }
`

// A Policy that sets Write on the join entity of a bidirectional edge is an
// error at its line, relative to the working directory, and nothing is
// written.
func TestGenerateRefusesABidiJoinPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := withSchemaMethods(t, map[string]string{
		"schema/schema.go": genFriendsSchema,
		"schema/user_friend_policy.go": "package schema\n\nimport (\n\t\"context\"\n\n\t\"github.com/syssam/velox\"\n\n\t\"example.com/app/gen/entity\"\n)\n\n" +
			"// UserFriend is the join entity of User's friends, which has no schema.\ntype UserFriend struct{}\n\n" +
			"func (UserFriend) Policy() velox.Policy[entity.UserFriend] {\n\treturn velox.Policy[entity.UserFriend]{\n\t\t" +
			"Write: func(context.Context) (velox.Pred[entity.UserFriend], error) { return velox.Allow[entity.UserFriend](), nil },\n\t}\n}\n",
	}, tempModule)
	code, out, errs := runCLI(t, dir, "./schema")
	want := "velox-gen: schema/user_friend_policy.go:16: UserFriend.Policy sets Read or Write: the join entity of a bidirectional edge allows only Insert, as a condition would show or delete one row of a link\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "user_velox.go")); !os.IsNotExist(err) {
		t.Error("a file was written")
	}
}

func TestGenerateReportsSchemaErrorsAndWritesNothing(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/bad.go": `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Bad struct{ schema.Schema }

func (Bad) Fields() []schema.Field { return []schema.Field{field.String("velox_meta")} }
`})
	code, _, errs := runCLI(t, dir, "./schema")
	if code != 1 || !strings.HasPrefix(errs, "velox-gen: ") || !strings.Contains(errs, "schema Bad") {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("the output directory was created")
	}
}

// plainPkg is a package that declares no schemas.
const plainPkg = "package plain\n"

// A pattern that names no schema package is an error, in every form it can
// take, and nothing is written: velox-gen has one mode.
func TestNoSchemasIsAnError(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"plain/doc.go": plainPkg, "other/doc.go": "package other\n"})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
		{[]string{"-out", "gen", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
		{[]string{"-check", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
		{[]string{"verify", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
		{[]string{"example.com/app/plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
		{[]string{"describe", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
		{[]string{"describe", "-json", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
	} {
		if code, out, errs := runCLI(t, dir, c.args...); code != 1 || out != "" || errs != c.want {
			t.Errorf("%v: %d %q %q, want %q", c.args, code, out, errs, c.want)
		}
	}
	// Patterns that name no package, several, or the old `./...` form.
	for _, args := range [][]string{{"./nothing"}, {"./..."}, {"-check", "./..."}, {"verify", "./..."}} {
		if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: ") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// The flags of the removed mode are unknown.
	for _, flag := range []string{"-ordered=a.B", "-number=a.B"} {
		if code, out, errs := runCLI(t, dir, flag, "./plain"); code != 2 || out != "" || !strings.Contains(errs, "flag provided but not defined") {
			t.Errorf("%s: %d %q %q", flag, code, out, errs)
		}
	}
	for _, d := range []string{"gen", "plain/plain_velox.go"} {
		if _, err := os.Stat(filepath.Join(dir, d)); !os.IsNotExist(err) {
			t.Errorf("%s was written", d)
		}
	}
}

func TestVerifyGenerated(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".velox-verify-*")); len(matches) != 0 {
		t.Errorf("the program was left behind: %v", matches)
	}
}

func TestVerifyGeneratedCountsJoinEntities(t *testing.T) {
	dir := tempModule(t, map[string]string{"schema/user.go": genUserGroupSchema})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 0 || out != "velox-gen verify: 3 entities, 0 with errors\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
}

func TestVerifyGeneratedNeedsTheOutputPackage(t *testing.T) {
	dir := genModule(t, nil)
	code, _, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || errs != "velox-gen verify: gen is not a directory; run velox-gen to generate it first\n" {
		t.Errorf("%d %q", code, errs)
	}
}

// TestRelativeDirAsTheBinaryRuns runs the command as main does, with dir "."
// and the working directory in the module, and a relative -out.
func TestRelativeDirAsTheBinaryRuns(t *testing.T) {
	dir := genModuleSerial(t, nil)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	inCwd := func(args ...string) (int, string, string) {
		var out, errs strings.Builder
		code := run(args, ".", offlineEnv(), &out, &errs)
		return code, out.String(), errs.String()
	}
	code, out, errs := inCwd("-out", "gen", "./schema")
	if want := "gen/tag_velox.go\ngen/user_velox.go\ngen/velox.go\n"; code != 0 || out != want {
		t.Fatalf("generate: %d %q %q", code, out, errs)
	}
	if code, out, errs := inCwd("-check", "-out", "gen", "./schema"); code != 0 || out != "" || errs != "" {
		t.Fatalf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := inCwd("verify", "-out", "gen", "./schema"); code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("verify: %d %q %q", code, out, errs)
	}
	// The defaults too.
	if code, out, errs := inCwd("verify"); code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("verify, defaults: %d %q %q", code, out, errs)
	}
}

// veloxEnv is velox with the environment env.
func veloxEnv(t *testing.T, dir string, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs strings.Builder
	code := run(args, dir, env, &out, &errs)
	return code, out.String(), errs.String()
}

// A go.mod that needs go mod tidy is the error go list gives, not a message
// about the pattern.
func TestGoListErrorsAreShown(t *testing.T) {
	dir := genModule(t, nil)
	// The library needs a newer go than this go.mod says, which -mod=readonly
	// refuses to change.
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), strings.Replace(string(b), "go 1.27.0", "go 1.22", 1))
	env := append(offlineEnv(), "GOFLAGS=-mod=readonly")
	for _, args := range [][]string{{"./schema"}, {}, {"-check", "./schema"}} {
		code, out, errs := veloxEnv(t, dir, env, args...)
		if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: ") || !strings.Contains(errs, "go mod tidy") || strings.Contains(errs, "no packages") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
}

func TestSchemaDirectoryWithoutSchemas(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/doc.go": "package schema\n"})
	const want = "velox-gen: no schemas in example.com/app/schema: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"
	// Named, and by default.
	for _, args := range [][]string{{"./schema"}, {}, {"-check"}, {"verify"}} {
		if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("files were written")
	}
}

// A file of another package in the schema directory is the error go list
// gives for it.
func TestSchemaDirectoryWithAStrayPackage(t *testing.T) {
	dir := genModule(t, nil)
	writeFile(t, filepath.Join(dir, "schema", "stray.go"), "package other\n")
	for _, args := range [][]string{{"./schema"}, {}} {
		code, out, errs := runCLI(t, dir, args...)
		if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: ") || !strings.Contains(errs, "found packages ") || !strings.Contains(errs, "other (stray.go)") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("files were written")
	}
}

const genUserGroupSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (User) Edges() []schema.Edge   { return []schema.Edge{edge.To("groups", Group.Type)} }

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", User.Type).Ref("groups")}
}
`

const genPostUserSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (User) Edges() []schema.Edge   { return []schema.Edge{edge.To("posts", Post.Type)} }

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{field.String("title"), field.Int64("author_id")}
}
func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id")}
}
`

// TestVerifyGeneratedChecksRelations: the generated metadata names no
// relation's target, so CheckEntity cannot resolve a relation; verify calls
// velox.CheckRelation for each, and so catches a stale or hand-edited key
// (which every entity's own metadata accepts) and a relation whose target's
// metadata is broken.
func TestVerifyGeneratedChecksRelations(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/schema.go": genPostUserSchema})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("a good model: %d %q %q", code, out, errs)
	}
	edit := func(file, old, repl string) {
		t.Helper()
		p := filepath.Join(dir, "gen", file)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), old) {
			t.Fatalf("%s has no %q", file, old)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), old, repl)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A key column that does not exist: both entities build, the relation does not.
	edit("user_velox.go", `velox.EdgeTo[Post]("Posts", userColID, postColAuthorID)`, `velox.EdgeTo[Post]("Posts", userColID, 9)`)
	code, out, errs = runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || out != "velox-gen verify: 2 entities, 1 with errors\n" || !strings.Contains(errs, "User.Posts: its key names column 9") {
		t.Errorf("a bad key: %d %q %q", code, out, errs)
	}
	edit("user_velox.go", `velox.EdgeTo[Post]("Posts", userColID, 9)`, `velox.EdgeTo[Post]("Posts", userColID, postColAuthorID)`)
	// A handle that names another entity than the field holds: the target comes
	// from the handle, so verify must compare it with the field.
	edit("user_velox.go", "velox.Rel[User, Post]", "velox.Rel[User, User]")
	code, out, errs = runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "User.Posts: the field is a velox.Many[") || !strings.Contains(errs, "but the Rel handle names") || !strings.Contains(out, "with errors") {
		t.Errorf("a mismatched handle: %d %q %q", code, out, errs)
	}
	edit("user_velox.go", "velox.Rel[User, User]", "velox.Rel[User, Post]")
	// A target whose metadata is broken: the relation names it, and says so.
	edit("post_velox.go", "PrimaryKey: []int{postColID},", "PrimaryKey: []int{9},")
	code, out, errs = runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "User.Posts: its target") || !strings.Contains(errs, "the primary key names column 9") || !strings.Contains(out, "with errors") {
		t.Errorf("a broken target: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
}

// noVerifyDirs fails when verify left its temporary program behind: it removes
// it when the entities report errors too.
func noVerifyDirs(t *testing.T, dir string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(dir, ".velox-verify-*"))
	if len(left) > 0 {
		t.Errorf("verify left %v behind", left)
	}
}

// TestVerifyLeavesNothingBehindWhenItCannotCompile: the temporary program is
// removed when it does not compile (a generated file gone), as well as when
// the entities report errors.
func TestVerifyLeavesNothingBehindWhenItCannotCompile(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if err := os.Remove(filepath.Join(dir, "gen", "user_velox.go")); err != nil { // User is gone: the program cannot compile
		t.Fatal(err)
	}
	if code, _, errs := runCLI(t, dir, "verify"); code != 1 || !strings.Contains(errs, "User") {
		t.Errorf("verify without a generated file: %d %q, want 1 and a compile error naming User", code, errs)
	}
	noVerifyDirs(t, dir)
}

// TestGeneratedCodeRunsNothingAtStart enforces design §3 rule 4: a generated
// file has no init function and no package-level call; its values are
// literals and zero values. The model has relations, a join entity and
// enums, which are the parts that could grow initialisers.
func TestGeneratedCodeRunsNothingAtStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/schema.go": genUserGroupSchema})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "gen", "*_velox.go"))
	if len(files) < 3 {
		t.Fatalf("generated %d files, want one per entity (User, Group, UserGroup)", len(files))
	}
	for _, name := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Name.Name == "init" {
					t.Errorf("%s: an init function", name)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					vs, ok := s.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, v := range vs.Values {
						ast.Inspect(v, func(n ast.Node) bool {
							if _, call := n.(*ast.CallExpr); call {
								t.Errorf("%s: a package-level value calls a function", name)
							}
							_, lit := n.(*ast.FuncLit)
							return !lit // a function's body runs when it is called, not at start
						})
					}
				}
			}
		}
	}
}

// TestVerifyGeneratedChecksThroughRelations: a many-to-many relation's key
// through its join entity is checked too; a column of the join entity that
// does not exist is caught by verify, which names the relation.
func TestVerifyGeneratedChecksThroughRelations(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/schema.go": genUserGroupSchema})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema"); code != 0 || out != "velox-gen verify: 3 entities, 0 with errors\n" {
		t.Fatalf("a good model: %d %q %q", code, out, errs)
	}
	p := filepath.Join(dir, "gen", "user_velox.go")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(velox\.EdgeThrough\[UserGroup, Group\]\("Groups", \w+, \w+, )\w+(, \w+\))`)
	if !re.Match(b) {
		t.Fatalf("no EdgeThrough for Groups in\n%s", b)
	}
	if err := os.WriteFile(p, re.ReplaceAll(b, []byte("${1}9${2}")), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "User.Groups") || !strings.Contains(out, "with errors") {
		t.Errorf("a broken through relation: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
}

const dupFieldSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.String("name"),
		field.Int64("n").DefaultFunc(1),
	}
}
`

// Errors start with the position of the element, relative to the working
// directory when the file is inside it.
func TestRuleErrorHasPos(t *testing.T) {
	dir := tempModule(t, map[string]string{"schema/user.go": dupFieldSchema})
	// A builder misuse is reported first, by itself: the rules run on a
	// model that loaded.
	code, _, errs := runCLI(t, dir, "./schema")
	if want := `velox-gen: schema/user.go:14: schema User: field "n": DefaultFunc must be a func() int64`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("misuse: %d %q, want %q", code, errs, want)
	}
	writeFile(t, filepath.Join(dir, "schema", "user.go"), strings.Replace(dupFieldSchema, "\t\tfield.Int64(\"n\").DefaultFunc(1),\n", "", 1))
	code, _, errs = runCLI(t, dir, "./schema")
	if want := `schema/user.go:13: schema User: field "name": declared twice`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("rule: %d %q, want %q", code, errs, want)
	}
	code, _, errs = runCLI(t, dir, "describe")
	if want := `schema/user.go:13: schema User: field "name": declared twice`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("describe: %d %q, want %q", code, errs, want)
	}
	code, _, errs = runCLI(t, filepath.Join(dir, "schema"), "-check", ".")
	if want := "velox-gen: user.go:13: schema User:"; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("in the schema directory: %d %q, want %q", code, errs, want)
	}
	if strings.Contains(errs, dir) {
		t.Errorf("an absolute path is printed: %q", errs)
	}
}

const posSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.Enum("role").Values("viewer", "admin").Default("viewer"),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", Post.Type), edge.To("friends", User.Type)}
}

func (User) Indexes() []schema.Index { return []schema.Index{index.Fields("name").Unique()} }

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field { return []schema.Field{field.Text("body")} }

func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique()}
}
`

// Review Focus 1: one schema package in two modules at different depths gives
// byte-identical output, describe -json included.
func TestOutputIgnoresPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	parallel(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(base, "a"), filepath.Join(base, "deeper", "x", "y", "b")}
	var gens []map[string]string
	var described []string
	for _, dir := range dirs {
		tempModuleAt(t, dir, map[string]string{"schema/user.go": posSchema})
		if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
			t.Fatalf("%s: %d %q", dir, code, errs)
		}
		files := map[string]string{}
		des, err := os.ReadDir(filepath.Join(dir, "gen"))
		if err != nil {
			t.Fatal(err)
		}
		for _, de := range des {
			b, err := os.ReadFile(filepath.Join(dir, "gen", de.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), base) || strings.Contains(string(b), "user.go:") {
				t.Errorf("%s holds a path", de.Name())
			}
			files[de.Name()] = string(b)
		}
		gens = append(gens, files)
		code, out, errs := runCLI(t, dir, "describe", "-json")
		if code != 0 || strings.Contains(out, base) {
			t.Fatalf("describe: %d %q", code, errs)
		}
		described = append(described, out)
	}
	if _, ok := gens[0]["velox.go"]; !ok || len(gens[0]) != len(gens[1]) {
		t.Fatalf("files %d and %d", len(gens[0]), len(gens[1]))
	}
	for name, a := range gens[0] {
		if b, ok := gens[1][name]; !ok || a != b {
			t.Errorf("%s differs between the two checkouts", name)
		}
	}
	if described[0] != described[1] {
		t.Error("describe -json differs between the two checkouts")
	}
}

// TestGenerateIsAllOrNothing: a run whose write fails part-way leaves the
// output as it was, the file it would remove included.
func TestGenerateIsAllOrNothing(t *testing.T) {
	const item = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Item struct{ schema.Schema }

func (Item) Fields() []schema.Field { return []schema.Field{field.String("sku")} }
`
	dir := genModuleSerial(t, map[string]string{"schema/item.go": item})
	gen := filepath.Join(dir, "gen")
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	// Without Tag, and with another field of Item and of User:
	// item_velox.go and user_velox.go change, and tag_velox.go goes.
	if err := os.Remove(filepath.Join(dir, "schema", "tag.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "schema", "item.go"), strings.Replace(item, `field.String("sku")`, `field.String("sku"), field.Int("count")`, 1))
	writeFile(t, filepath.Join(dir, "schema", "user.go"), strings.Replace(genUserSchema, `field.String("name"),`, `field.String("name"), field.Int("age"),`, 1))
	before := treeFiles(t, gen)
	injected := errors.New("injected failure")
	renameFile = func(from, to string) error {
		if filepath.Base(to) == "user_velox.go" && strings.Contains(from, ".tmp-") {
			return injected
		}
		return os.Rename(from, to)
	}
	code, out, errs := runCLI(t, dir, "./schema")
	renameFile = os.Rename
	if code != 1 || out != "" || !strings.Contains(errs, "injected failure") {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if after := treeFiles(t, gen); !reflect.DeepEqual(after, before) {
		t.Errorf("the output changed:\n%v", slices.Sorted(maps.Keys(after)))
	}
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 || out != "gen/item_velox.go\ngen/tag_velox.go\ngen/user_velox.go\n" {
		t.Errorf("the next run: %d %q %q", code, out, errs)
	}
}

// TestGenerateSweepsLeftovers: generate first rids -out of what a killed
// write left, and says so; -check leaves it.
func TestGenerateSweepsLeftovers(t *testing.T) {
	dir := genModule(t, nil)
	gen := filepath.Join(dir, "gen")
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(gen, ".user_velox.go.old-velox-1"), "old")
	writeFile(t, filepath.Join(gen, ".velox.go.tmp-velox-2"), "new")
	if err := os.Rename(filepath.Join(gen, "tag_velox.go"), filepath.Join(gen, ".tag_velox.go.gone-velox-3")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-4", "x.go"), header+"\n\npackage pb\n")
	writeFile(t, filepath.Join(gen, ".notes.old-velox"), "mine")
	before := treeFiles(t, gen)
	if code, _, _ := runCLI(t, dir, "-check", "./schema"); code != 3 {
		t.Errorf("-check: %d", code)
	}
	if after := treeFiles(t, gen); !reflect.DeepEqual(after, before) {
		t.Errorf("-check changed gen:\n%v", slices.Sorted(maps.Keys(after)))
	}
	code, out, errs := runCLI(t, dir, "./schema")
	wantErrs := "velox-gen: restored gen/pb from gen/.pb.gone-velox-4, which an interrupted run left\n" +
		"velox-gen: restored gen/tag_velox.go from gen/.tag_velox.go.gone-velox-3, which an interrupted run left\n" +
		"velox-gen: removed gen/.user_velox.go.old-velox-1, which an interrupted run left\n" +
		"velox-gen: removed gen/.velox.go.tmp-velox-2, which an interrupted run left\n"
	if code != 0 || out != "gen/pb/x.go\n" || errs != wantErrs {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if got := slices.Sorted(maps.Keys(treeFiles(t, gen))); !slices.Equal(got, []string{".notes.old-velox", "tag_velox.go", "user_velox.go", "velox.go"}) {
		t.Errorf("gen holds %v", got)
	}
	if exists(filepath.Join(gen, "pb")) {
		t.Error("pb is left")
	}
}

// TestGenerateCheckReportsLeftovers: -check reports, one line each, what a
// killed write left, though every file is up to date, and exits 3.
func TestGenerateCheckReportsLeftovers(t *testing.T) {
	dir := genModule(t, nil)
	gen := filepath.Join(dir, "gen")
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(gen, ".user_velox.go.old-velox-1"), "old")
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-2", "x.go"), header+"\n\npackage pb\n")
	writeFile(t, filepath.Join(gen, ".notes.old-velox"), "mine")
	code, out, errs := runCLI(t, dir, "-check", "./schema")
	want := "velox-gen: gen/.pb.gone-velox-2 is left over from an interrupted run\n" +
		"velox-gen: gen/.user_velox.go.old-velox-1 is left over from an interrupted run\n" +
		"velox-gen: run go tool velox-gen ./schema\n"
	if code != 3 || out != "" || errs != want {
		t.Errorf("%d %q %q", code, out, errs)
	}
}

// TestGenerateKeepsYoursInALeftover: a directory set aside whose name is
// taken again is removed entry by entry: a file of yours in it stays, with
// the directory, and generate names it.
func TestGenerateKeepsYoursInALeftover(t *testing.T) {
	dir := genModule(t, nil)
	gen := filepath.Join(dir, "gen")
	if code, out, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(gen, "pb", "keep.txt"), "mine")
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-1", "x.go"), header+"\n\npackage pb\n")
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-1", "mine.txt"), "mine")
	code, out, errs := runCLI(t, dir, "./schema")
	want := "velox-gen: kept gen/.pb.gone-velox-1/mine.txt, which is not velox-gen's, in gen/.pb.gone-velox-1, which an interrupted run left; move it\n"
	if code != 0 || out != "" || errs != want {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if got := treeFiles(t, filepath.Join(gen, ".pb.gone-velox-1")); !reflect.DeepEqual(got, map[string]string{"mine.txt": "mine"}) {
		t.Errorf("the leftover holds %v", got)
	}
}

// A struct that embeds a struct that embeds schema.Schema is not a schema:
// describe skips it without a message.
// describe lists the schemas that embed an unexported base, and not the base.
func TestDescribeListsSchemasOfABase(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/s.go": `package schema

import "github.com/syssam/velox/schema"

type base struct{ schema.Schema }

type Shop struct{ base }

type Order struct{ base }
`})
	code, out, errs := runCLI(t, dir, "describe", "./schema")
	if code != 0 || errs != "" {
		t.Fatalf("describe: %d %q %q", code, out, errs)
	}
	for _, name := range []string{"Order", "Shop"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s is not listed: %q", name, out)
		}
	}
	if strings.Contains(out, "base") {
		t.Errorf("base is listed: %q", out)
	}
}

const genScopedUserSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable().Annotations(schema.Scoped("org")),
		field.String("name"),
	}
}
`

// A scoped entity's descriptor wraps the user's Policy in the scope, which the
// user's file does not call; scope_velox.go holds the key and the scope; and
// -check and verify, which builds the generated package, agree.
func TestGenerateScoped(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/user.go": genScopedUserSchema, "schema/user_policy.go": schemaUserPolicy})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	scope, err := os.ReadFile(filepath.Join(dir, "gen", "entity", "scope_velox.go"))
	if err != nil || !strings.Contains(string(scope), `var OrgKey = scope.NewKey[int64]("org")`) {
		t.Fatalf("entity/scope_velox.go: %v\n%s", err, scope)
	}
	if reexp, _ := os.ReadFile(filepath.Join(dir, "gen", "scope_velox.go")); !strings.Contains(string(reexp), "var OrgKey = entity.OrgKey") {
		t.Errorf("scope_velox.go does not re-export OrgKey:\n%s", reexp)
	}
	user, _ := os.ReadFile(filepath.Join(dir, "gen", "entity", "user_velox.go"))
	if !strings.Contains(string(user), "velox.BindPolicy(userScope.Wrap(policy()))") ||
		!strings.Contains(string(user), "velox.BindHooks([]velox.Hook[User]{userScope.Fill()})") {
		t.Errorf("user_velox.go does not bind the scope around the policy:\n%s", user)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// Dropping Scoped from the last scoped entity removes scope_velox.go and the
// wrapping, and the package still builds.
func TestGenerateScopedRemoved(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/user.go": genScopedUserSchema, "schema/user_policy.go": schemaUserPolicy})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema", "user.go"), []byte(genUserSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("second run: %d %q", code, errs)
	}
	for _, name := range []string{"scope_velox.go", filepath.Join("entity", "scope_velox.go")} {
		if _, err := os.Stat(filepath.Join(dir, "gen", name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", name, err)
		}
	}
	user, _ := os.ReadFile(filepath.Join(dir, "gen", "entity", "user_velox.go"))
	if strings.Contains(string(user), "Scope") || !strings.Contains(string(user), "velox.BindPolicy(policy())") {
		t.Errorf("user_velox.go still wraps the scope:\n%s", user)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "gen", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// A file of the user's that declares a name velox-gen writes for a scope is
// refused, naming the file.
func TestGenerateRefusesAScopeName(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/user.go": genScopedUserSchema, "gen/mine.go": "package gen\n\nvar OrgKey = 1\n"})
	code, _, errs := runCLI(t, dir, "./schema")
	if code == 0 || !strings.Contains(errs, "mine.go") || !strings.Contains(errs, "OrgKey, a name velox-gen generates") {
		t.Errorf("%d %q", code, errs)
	}
}

// A method of the user's on an entity in gen works in plain mode. Once a
// Policy splits gen, gen.User is an alias of entity.User, which takes no
// methods there: velox-gen refuses the method at its line and says where it
// goes, instead of writing a gen that does not build. Moved to gen/entity,
// it generates.
func TestGenerateRefusesAMethodOnAnAlias(t *testing.T) {
	dir := genModule(t, nil)
	extra := "package gen\n\n// Hello greets.\nfunc (u User) Hello() string { return \"hi \" + u.Name }\n"
	writeFile(t, filepath.Join(dir, "gen", "extra.go"), extra)
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("plain: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "schema", "user_policy.go"), schemaUserPolicy)
	want := "velox-gen: gen/extra.go:4: declares method User.Hello in the package that re-exports package entity, where User is an alias of entity.User and takes no methods: move it to gen/entity, package entity\n"
	for _, args := range [][]string{{"./schema"}, {"-check", "./schema"}} {
		if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
			t.Errorf("%v: %d %q %q, want %q", args, code, out, errs, want)
		}
	}
	if err := os.Remove(filepath.Join(dir, "gen", "extra.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "gen", "entity", "extra.go"), strings.Replace(extra, "package gen", "package entity", 1))
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("moved to entity: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./schema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// A helper that imports gen and that the schema's structure reaches (Fields
// calls it, or a blank import keeps it) makes an import cycle once gen
// imports the schema. velox-gen names the chain and the way out, rather
// than go list's bare "import cycle not allowed".
func TestGenerateNamesTheChainOfACycle(t *testing.T) {
	for _, c := range []struct{ name, use string }{
		{"fields", "package schema\n\nimport (\n\t\"github.com/syssam/velox/schema\"\n\t\"github.com/syssam/velox/schema/field\"\n\n\t\"example.com/app/helper\"\n)\n\n" +
			"// Note is a schema whose field is named by the helper.\ntype Note struct{ schema.Schema }\n\n" +
			"func (Note) Fields() []schema.Field { return []schema.Field{field.String(helper.Name())} }\n"},
		{"blank", "package schema\n\nimport _ \"example.com/app/helper\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := genModule(t, map[string]string{"schema/user_policy.go": schemaUserPolicy})
			if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
				t.Fatalf("generate: %d %q", code, errs)
			}
			writeFile(t, filepath.Join(dir, "helper", "h.go"), "package helper\n\nimport \"example.com/app/gen\"\n\n// Users is gen.Users.\nvar Users = gen.Users\n\n// Name is a field's name.\nfunc Name() string { return \"title\" }\n")
			writeFile(t, filepath.Join(dir, "schema", "use.go"), c.use)
			want := "velox-gen: example.com/app/schema imports example.com/app/helper imports example.com/app/gen: the generated package imports the schema package to bind its Hooks and Policy, " +
				"so a package the schema imports must import \"example.com/app/gen/entity\", which holds the entities, instead\n"
			for _, args := range [][]string{{"./schema"}, {"-check", "./schema"}} {
				if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
					t.Errorf("%v: %d %q %q, want %q", args, code, out, errs, want)
				}
			}
		})
	}
}

// A compile error in a schema file the loader rewrote, one that declares a
// Policy, is reported at the user's file and line, not at the loader's
// temporary copy.
func TestGenerateCompileErrorInARewrittenFile(t *testing.T) {
	dir := genModule(t, nil)
	writeFile(t, filepath.Join(dir, "schema", "user_policy.go"), schemaUserPolicy+"\nfunc init() { _ = undefinedName }\n")
	code, out, errs := runCLI(t, dir, "./schema")
	if code != 1 || out != "" || !strings.Contains(errs, "schema/user_policy.go:11:") || !strings.Contains(errs, "undefinedName") || strings.Contains(errs, ".velox-gen") || strings.Contains(errs, "overlay") {
		t.Errorf("%d %q %q, want the error at schema/user_policy.go:11", code, out, errs)
	}
}

// When the last Policy goes, gen is one package again and nothing imports
// gen/entity. A file of the user's there stays, as it is the user's, and
// velox-gen warns, naming it, since it would no longer be built with gen.
func TestGenerateWarnsOfUserFilesLeftInEntity(t *testing.T) {
	dir := genModule(t, map[string]string{"schema/user_policy.go": schemaUserPolicy})
	if code, _, errs := runCLI(t, dir, "./schema"); code != 0 {
		t.Fatalf("split: %d %q", code, errs)
	}
	extra := filepath.Join(dir, "gen", "entity", "extra.go")
	writeFile(t, extra, "package entity\n\n// Hello greets.\nfunc (u User) Hello() string { return \"hi \" + u.Name }\n")
	if err := os.Remove(filepath.Join(dir, "schema", "user_policy.go")); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runCLI(t, dir, "./schema")
	want := "velox-gen: warning: gen/entity/extra.go is yours, and stays; gen is one package now, which does not build gen/entity: move the file to gen, as package gen, or delete it\n"
	if code != 0 || errs != want {
		t.Errorf("plain: %d %q, want %q", code, errs, want)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Errorf("the user's file is gone: %v", err)
	}
}
