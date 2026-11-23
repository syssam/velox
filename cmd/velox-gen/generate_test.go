package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/internal/genrun"
)

const genUserSchema = `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.String("email").Optional().Default("")}
}
`

const genTagSchema = `package veloxschema

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
	files := map[string]string{"veloxschema/user.go": genUserSchema, "veloxschema/tag.go": genTagSchema}
	maps.Copy(files, extra)
	return withSchemaMethods(t, files, mk)
}

// withSchemaMethods makes the module of files with mk, but writes the files
// that import the generated package after it: mk's go mod tidy cannot
// resolve a package that velox-gen has not written yet. velox-gen refuses
// such a schema; a test writes one to see the refusal.
func withSchemaMethods(t *testing.T, files map[string]string, mk func(*testing.T, map[string]string) string) string {
	t.Helper()
	later := map[string]string{}
	for name, src := range files {
		if strings.Contains(src, `"example.com/app/veloxgen`) {
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
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if want := "veloxgen/tag.go\nveloxgen/user.go\nveloxgen/velox.go\n"; code != 0 || out != want {
		t.Fatalf("first run: %d %q %q", code, out, errs)
	}
	for _, f := range []string{"tag.go", "user.go", "velox.go"} {
		if !genrun.IsGenerated(filepath.Join(dir, "veloxgen", f)) {
			t.Errorf("%s lacks the header", f)
		}
	}
	before := mtimes(t, filepath.Join(dir, "veloxgen"))
	time.Sleep(20 * time.Millisecond)
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Errorf("second run: %d %q %q", code, out, errs)
	}
	after := mtimes(t, filepath.Join(dir, "veloxgen"))
	for name, mt := range before {
		if !after[name].Equal(mt) {
			t.Errorf("%s was rewritten", name)
		}
	}
	if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
}

// The sweep of -out ./veloxgen leaves alone a nested -out ./veloxgen/b, another run's
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
	other := genrun.Header + "\n\npackage other\n"
	dir := genModule(t, map[string]string{
		"schemab/item.go":         itemSchema,
		"veloxgen/mod/go.mod":     "module example.com/other\n",
		"veloxgen/mod/other.go":   other,
		"veloxgen/mod/x/other.go": other,
	})
	b := []string{"-out", "./veloxgen/b", "-package", "b", "./schemab"}
	if code, out, errs := runCLI(t, dir, b...); code != 0 || out != "veloxgen/b/item.go\nveloxgen/b/velox.go\n" {
		t.Fatalf("veloxgen/b: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 || out != "veloxgen/tag.go\nveloxgen/user.go\nveloxgen/velox.go\n" {
		t.Fatalf("gen: %d %q %q", code, out, errs)
	}
	for _, f := range []string{"b/item.go", "b/velox.go", "mod/other.go", "mod/x/other.go"} {
		if _, err := os.Stat(filepath.Join(dir, "veloxgen", f)); err != nil {
			t.Errorf("veloxgen/%s: %v", f, err)
		}
	}
	if code, out, errs := runCLI(t, dir, append([]string{"-check"}, b...)...); code != 0 {
		t.Errorf("-check veloxgen/b: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 {
		t.Errorf("-check gen: %d %q %q", code, out, errs)
	}
}

func TestGenerateDefaults(t *testing.T) {
	dir := genModule(t, nil)
	// No arguments: the package is ./veloxschema and the output ./veloxgen.
	if code, _, errs := runCLI(t, dir); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	src, err := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go"))
	if err != nil || !strings.Contains(string(src), "package veloxgen\n") {
		t.Fatalf("%v\n%s", err, src)
	}
	// -out and -package: the package name defaults to the base of -out.
	if code, out, errs := runCLI(t, dir, "-out", "model/store", "./veloxschema"); code != 0 || !strings.Contains(out, "model/store/user.go") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	src, _ = os.ReadFile(filepath.Join(dir, "model", "store", "user.go"))
	if !strings.Contains(string(src), "package store\n") {
		t.Errorf("package is not the base of -out:\n%.200s", src)
	}
	if code, _, errs := runCLI(t, dir, "-out", "model/store", "-package", "persist", "./veloxschema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	src, _ = os.ReadFile(filepath.Join(dir, "model", "store", "user.go"))
	if !strings.Contains(string(src), "package persist\n") {
		t.Errorf("-package ignored:\n%.200s", src)
	}
}

func TestCheckAndRewrite(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	userFile := filepath.Join(dir, "veloxgen", "keep.go")
	writeFile(t, userFile, "package veloxgen\n\nconst Keep = 1\n")

	writeFile(t, filepath.Join(dir, "veloxschema", "user.go"), strings.Replace(genUserSchema, `field.String("name"),`, `field.String("name"), field.Int("age"),`, 1))
	before, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go"))
	code, out, errs := runCLI(t, dir, "-check", "./veloxschema")
	if code != 3 || out != "" || !strings.Contains(errs, "velox-gen: veloxgen/user.go is out of date\n") ||
		!strings.Contains(errs, "velox-gen: run go tool velox-gen ./veloxschema\n") || strings.Contains(errs, "tag.go") {
		t.Fatalf("-check: %d %q %q", code, out, errs)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go")); string(after) != string(before) {
		t.Fatal("-check changed the file")
	}

	code, out, errs = runCLI(t, dir, "./veloxschema")
	if code != 0 || !strings.Contains(out, "veloxgen/user.go\n") || strings.Contains(out, "tag.go") {
		t.Fatalf("rewrite: %d %q %q", code, out, errs)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go")); !strings.Contains(string(after), "Age") {
		t.Errorf("not rewritten:\n%s", after)
	}
	if code, _, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 {
		t.Fatalf("-check after rewrite: %d %q", code, errs)
	}

	// A removed entity: -check names its file, the next run deletes it.
	if err := os.Remove(filepath.Join(dir, "veloxschema", "tag.go")); err != nil {
		t.Fatal(err)
	}
	code, _, errs = runCLI(t, dir, "-check", "./veloxschema")
	if code != 3 || !strings.Contains(errs, "velox-gen: veloxgen/tag.go is no longer generated; a run removes it\n") {
		t.Fatalf("-check after removal: %d %q", code, errs)
	}
	code, out, errs = runCLI(t, dir, "./veloxschema")
	if code != 0 || !strings.Contains(out, "veloxgen/tag.go\n") {
		t.Fatalf("delete: %d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "tag.go")); !os.IsNotExist(err) {
		t.Errorf("the file of the removed entity is still there: %v", err)
	}
	if b, err := os.ReadFile(userFile); err != nil || string(b) != "package veloxgen\n\nconst Keep = 1\n" {
		t.Errorf("the user's file did not survive: %v %q", err, b)
	}
}

// schemaUserPolicy is a Policy for User, declared in the schema package,
// which does not import gen.
const schemaUserPolicy = "package veloxschema\n\nimport \"github.com/syssam/velox\"\n\n" +
	"func (User) Policy() velox.Policy { return velox.Policy{} }\n"

// The schema package's Policy, which names no type of gen, is read from the
// entity's descriptor: gen imports the schema, by its name, and names the
// schema's value. No veloxgen/runtime is written. Removing the Policy rewrites the
// entity's file alone.
func TestGenerateWithBehaviourWithoutGen(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxschema/user_policy.go": schemaUserPolicy})
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if want := "veloxgen/tag.go\nveloxgen/user.go\nveloxgen/velox.go\n"; code != 0 || out != want || errs != "" {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
	user, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go"))
	for _, want := range []string{`"example.com/app/veloxschema"`, "Schema: veloxschema.User{},"} {
		if !strings.Contains(squeeze(string(user)), want) {
			t.Errorf("user.go lacks %q:\n%s", want, user)
		}
	}
	if strings.Contains(string(user), "Hooks:") {
		t.Errorf("user.go binds hooks the user did not declare")
	}
	tag, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "tag.go"))
	if strings.Contains(string(tag), "example.com/app/veloxschema") || strings.Contains(string(tag), "Policy:") {
		t.Errorf("tag.go imports the schema, or binds a policy:\n%s", tag)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "runtime")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("veloxgen/runtime was written: %v", err)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
	if err := os.Remove(filepath.Join(dir, "veloxschema", "user_policy.go")); err != nil {
		t.Fatal(err)
	}
	// The policy was the schema's only behaviour: the package's entity table
	// goes with it, from velox.go and from every descriptor.
	code, out, errs = runCLI(t, dir, "./veloxschema")
	if want := "veloxgen/tag.go\nveloxgen/user.go\nveloxgen/velox.go\n"; code != 0 || out != want || errs != "" {
		t.Fatalf("without the policy: %d %q %q, want %q", code, out, errs, want)
	}
	if got, want := strings.Join(generatedFiles(t, dir), " "), "tag.go user.go velox.go"; got != want {
		t.Errorf("generated %s, want %s", got, want)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify without the policy: %d %q %q", code, out, errs)
	}
}

// A field that is not Nillable whose database default is a non-zero literal
// generates without a warning: a setter stores its zero (ADR 0136).
func TestGenerateNonZeroDefaultNoWarning(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxschema/flag.go": `package veloxschema

import (
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Flag struct{ schema.Schema }

func (Flag) Fields() []schema.Field {
	return []schema.Field{field.Bool("on").Annotations(sqlschema.DefaultExprs(map[string]string{dialect.Postgres: "true", dialect.MySQL: "true", dialect.SQLite: "true"}))}
}
`})
	code, _, errs := runCLI(t, dir, "./veloxschema")
	if code != 0 || strings.Contains(errs, "warning") {
		t.Errorf("%d %q", code, errs)
	}
}

// A closure default binds in the entity's file, which imports the schema.
// A schema package that imports gen is refused, with or without behaviour:
// directly, at the import, and through a helper, with the chain. A first
// Policy over that gen binds beside the default.
func TestGenerateRefusesSchemaImportingGen(t *testing.T) {
	user := strings.Replace(strings.Replace(genUserSchema, `field.String("name")`, `field.String("name"), field.Time("created_at").Default(func() time.Time { return time.Now() })`, 1),
		"import (\n", "import (\n\t\"time\"\n\n", 1)
	dir := genModule(t, map[string]string{"veloxschema/user.go": user})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 || errs != "" {
		t.Fatalf("generate: %d %q", code, errs)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go"))
	if !strings.Contains(string(src), `"github.com/syssam/velox/schema"`) || !strings.Contains(string(src), `schema.FieldDescriptors(&veloxschema.User{}, "created_at"`) {
		t.Fatalf("user.go does not read the closure from the schema:\n%s", src)
	}
	writeFile(t, filepath.Join(dir, "veloxschema", "user_policy.go"), schemaUserPolicy)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 || errs != "" {
		t.Fatalf("with the Policy: %d %q", code, errs)
	}
	if src, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go")); !strings.Contains(squeeze(string(src)), "Schema: veloxschema.User{},") {
		t.Errorf("user.go does not bind the policy:\n%s", src)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)

	const reason = "the schema declares behaviour by column name and does not use veloxgen, which imports the schema; move the code that needs veloxgen out of the schema package\n"
	writeFile(t, filepath.Join(dir, "veloxschema", "use.go"), "package veloxschema\n\nimport \"example.com/app/veloxgen\"\n\n// Users is veloxgen.Users.\nvar Users = veloxgen.Users\n")
	want := "velox-gen: veloxschema/use.go:3: the schema package imports example.com/app/veloxgen: " + reason
	for _, args := range [][]string{{"./veloxschema"}, {"-check", "./veloxschema"}} {
		if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
			t.Errorf("%v: %d %q %q, want %q", args, code, out, errs, want)
		}
	}
	if err := os.Remove(filepath.Join(dir, "veloxschema", "use.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "helper", "h.go"), "package helper\n\nimport \"example.com/app/veloxgen\"\n\n// Users is veloxgen.Users.\nvar Users = veloxgen.Users\n")
	writeFile(t, filepath.Join(dir, "veloxschema", "use.go"), "package veloxschema\n\nimport _ \"example.com/app/helper\"\n")
	want = "velox-gen: example.com/app/veloxschema imports example.com/app/helper imports example.com/app/veloxgen: " + reason
	for _, args := range [][]string{{"./veloxschema"}, {"-check", "./veloxschema"}} {
		if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
			t.Errorf("helper %v: %d %q %q, want %q", args, code, out, errs, want)
		}
	}
}

// velox.go has the package comment, unless a file of the user's in the output
// directory already has one; -check agrees either way.
func TestGeneratePackageDoc(t *testing.T) {
	const doc = "// Package veloxgen holds the entities of example.com/app/veloxschema, generated by velox-gen.\npackage veloxgen\n"
	for _, c := range []struct {
		name  string
		extra map[string]string
		want  bool
	}{
		{"generated", nil, true},
		{"the user's", map[string]string{"veloxgen/doc.go": "// Package veloxgen is mine.\npackage veloxgen\n"}, false},
	} {
		dir := genModule(t, c.extra)
		if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
			t.Fatalf("%s: %d %q", c.name, code, errs)
		}
		src, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "velox.go"))
		if got := strings.Contains(string(src), doc); got != c.want {
			t.Errorf("%s: velox.go has the package comment: %v, want %v\n%s", c.name, got, c.want, src)
		}
		if !c.want && strings.Contains(string(src), "// Package") {
			t.Errorf("%s: velox.go has a package comment:\n%s", c.name, src)
		}
		if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 || out != "" || errs != "" {
			t.Errorf("%s: -check: %d %q %q", c.name, code, out, errs)
		}
	}
}

// A Policy and Hooks added to the schema after generation are not bound:
// -check says the entity's file is out of date, and, as they are the schema's
// first behaviour, velox.go, which then declares the package's entity table,
// and tag.go, whose descriptor names it.
func TestPolicyAddedAfterGenerationFailsCheck(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "veloxschema", "user_policy.go"), schemaUserPolicy+
		"\nfunc (User) Hooks() []velox.Hook { return nil }\n")
	code, out, errs := runCLI(t, dir, "-check", "./veloxschema")
	want := "velox-gen: veloxgen/tag.go is out of date\nvelox-gen: veloxgen/user.go is out of date\nvelox-gen: veloxgen/velox.go is out of date\n"
	if code != 3 || out != "" || !strings.HasPrefix(errs, want) {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	// Regenerating binds both, and the check passes.
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("regenerate: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check after regenerating: %d %q %q", code, out, errs)
	}
}

// -check fails for a generated file that is missing, as for one that is
// stale, and says it is missing.
func TestCheckFindsAMissingFile(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 3 || !strings.Contains(errs, "velox-gen: veloxgen/user.go is missing\n") {
		t.Errorf("-check before any generation: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen")); !os.IsNotExist(err) {
		t.Error("-check wrote files")
	}
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if err := os.Remove(filepath.Join(dir, "veloxgen", "tag.go")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "-check", "./veloxschema")
	if code != 3 || out != "" || !strings.Contains(errs, "velox-gen: veloxgen/tag.go is missing\n") || strings.Contains(errs, "user.go") {
		t.Errorf("-check with a missing file: %d %q %q", code, out, errs)
	}
}

func TestGenerateRefusesAForeignFile(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxgen/user.go": "package veloxgen\n\n// mine\n"})
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if code != 1 || out != "" || !strings.Contains(errs, "veloxgen/user.go was not written by velox-gen; move it") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	des, _ := os.ReadDir(filepath.Join(dir, "veloxgen"))
	if len(des) != 1 {
		t.Errorf("files were written: %v", des)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go")); string(b) != "package veloxgen\n\n// mine\n" {
		t.Errorf("the foreign file changed: %q", b)
	}
	// -check refuses too.
	if code, _, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 1 || !strings.Contains(errs, "was not written by velox-gen") {
		t.Errorf("-check: %d %q", code, errs)
	}
}

func TestGenerateRefusesARedeclaredType(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxgen/mine.go": "package veloxgen\n\ntype User struct{}\n"})
	code, _, errs := runCLI(t, dir, "./veloxschema")
	if want := "velox-gen: veloxgen/mine.go: declares type User, which velox-gen generates\n"; code != 1 || errs != want {
		t.Fatalf("%d %q, want %q", code, errs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "user.go")); !os.IsNotExist(err) {
		t.Error("a file was written")
	}
}

// A Policy or Hooks method in the generated package is an error that says
// where it goes, with a path relative to the working directory, and nothing
// is written. So is one in the schema package of another shape.
func TestGenerateRefusesMisplacedMethods(t *testing.T) {
	for _, c := range []struct{ name, file, src, want string }{
		{"in gen", "veloxgen/user_policy.go", "package veloxgen\n\nimport \"github.com/syssam/velox\"\n\nfunc (*User) Policy() velox.Policy { return velox.Policy{} }\n",
			"velox-gen: veloxgen/user_policy.go:5: declares User.Policy in the generated package; declare it in the schema package, on User: func (User) Policy() velox.Policy\n"},
		{"wrong shape", "veloxschema/user_policy.go", "package veloxschema\n\nimport \"github.com/syssam/velox\"\n\nfunc (User) Policy() *velox.Policy { return nil }\n",
			"velox-gen: veloxschema/user_policy.go:5: User.Policy must take nothing and return velox.Policy: func (User) Policy() velox.Policy\n"},
		{"typed", "veloxschema/user_policy.go", "package veloxschema\n\nimport \"github.com/syssam/velox\"\n\nfunc (User) Policy() velox.Policy[int] { return velox.Policy[int]{} }\n",
			"velox-gen: veloxschema/user_policy.go:5: User.Policy must take nothing and return velox.Policy: func (User) Policy() velox.Policy\n"},
	} {
		dir := genModule(t, map[string]string{c.file: c.src})
		code, out, errs := runCLI(t, dir, "./veloxschema")
		if code != 1 || out != "" || errs != c.want {
			t.Fatalf("%s: %d %q %q, want %q", c.name, code, out, errs, c.want)
		}
		if _, err := os.Stat(filepath.Join(dir, "veloxgen", "user.go")); !os.IsNotExist(err) {
			t.Errorf("%s: a file was written", c.name)
		}
	}
}

const genFriendsSchema = `package veloxschema

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
		"veloxschema/schema.go": genFriendsSchema,
		"veloxschema/user_friend_policy.go": "package veloxschema\n\nimport (\n\t\"context\"\n\n\t\"github.com/syssam/velox\"\n)\n\n" +
			"// UserFriend is the join entity of User's friends, which has no schema.\ntype UserFriend struct{}\n\n" +
			"func (UserFriend) Policy() velox.Policy {\n\treturn velox.Policy{\n\t\t" +
			"Write: func(context.Context) (velox.Predicate, error) { return velox.Allow(), nil },\n\t}\n}\n",
	}, tempModule)
	code, out, errs := runCLI(t, dir, "./veloxschema")
	want := "velox-gen: veloxschema/user_friend_policy.go:14: UserFriend.Policy sets Read, Write, Query or Mutation: the join entity of a bidirectional edge allows only Insert, as a condition would show or delete one row of a link\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("%d %q %q, want %q", code, out, errs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "user.go")); !os.IsNotExist(err) {
		t.Error("a file was written")
	}
}

func TestGenerateReportsSchemaErrorsAndWritesNothing(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxschema/bad.go": `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Bad struct{ schema.Schema }

func (Bad) Fields() []schema.Field { return []schema.Field{field.String("velox_meta")} }
`})
	code, _, errs := runCLI(t, dir, "./veloxschema")
	if code != 1 || !strings.HasPrefix(errs, "velox-gen: ") || !strings.Contains(errs, "schema Bad") {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen")); !os.IsNotExist(err) {
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
		{[]string{"-out", "veloxgen", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"},
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
	for _, d := range []string{"veloxgen", "plain/plain.go"} {
		if _, err := os.Stat(filepath.Join(dir, d)); !os.IsNotExist(err) {
			t.Errorf("%s was written", d)
		}
	}
}

func TestVerifyGenerated(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".velox-verify-*")); len(matches) != 0 {
		t.Errorf("the program was left behind: %v", matches)
	}
}

func TestVerifyGeneratedCountsJoinEntities(t *testing.T) {
	dir := tempModule(t, map[string]string{"veloxschema/user.go": genUserGroupSchema})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 0 || out != "velox-gen verify: 3 entities, 0 with errors\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
}

func TestVerifyGeneratedNeedsTheOutputPackage(t *testing.T) {
	dir := genModule(t, nil)
	code, _, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 1 || errs != "velox-gen verify: veloxgen is not a directory; run velox-gen to generate it first\n" {
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
	code, out, errs := inCwd("-out", "veloxgen", "./veloxschema")
	if want := "veloxgen/tag.go\nveloxgen/user.go\nveloxgen/velox.go\n"; code != 0 || out != want {
		t.Fatalf("generate: %d %q %q", code, out, errs)
	}
	if code, out, errs := inCwd("-check", "-out", "veloxgen", "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Fatalf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := inCwd("verify", "-out", "veloxgen", "./veloxschema"); code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
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
	for _, args := range [][]string{{"./veloxschema"}, {}, {"-check", "./veloxschema"}} {
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
	dir := tempModule(t, map[string]string{"veloxschema/doc.go": "package veloxschema\n"})
	const want = "velox-gen: no schemas in example.com/app/veloxschema: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does\n"
	// Named, and by default.
	for _, args := range [][]string{{"./veloxschema"}, {}, {"-check"}, {"verify"}} {
		if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen")); !os.IsNotExist(err) {
		t.Error("files were written")
	}
}

// A file of another package in the schema directory is the error go list
// gives for it.
func TestSchemaDirectoryWithAStrayPackage(t *testing.T) {
	dir := genModule(t, nil)
	writeFile(t, filepath.Join(dir, "veloxschema", "stray.go"), "package other\n")
	for _, args := range [][]string{{"./veloxschema"}, {}} {
		code, out, errs := runCLI(t, dir, args...)
		if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: ") || !strings.Contains(errs, "found packages ") || !strings.Contains(errs, "other (stray.go)") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen")); !os.IsNotExist(err) {
		t.Error("files were written")
	}
}

const genUserGroupSchema = `package veloxschema

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

const genPostUserSchema = `package veloxschema

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
	dir := tempModule(t, map[string]string{"veloxschema/schema.go": genPostUserSchema})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("a good model: %d %q %q", code, out, errs)
	}
	edit := func(file, old, repl string) {
		t.Helper()
		p := filepath.Join(dir, "veloxgen", file)
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
	edit("user.go", `velox.EdgeTo[Post]("posts", userColID, postColAuthorID)`, `velox.EdgeTo[Post]("posts", userColID, 9)`)
	code, out, errs = runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 1 || out != "velox-gen verify: 2 entities, 1 with errors\n" || !strings.Contains(errs, "User.posts: its key names column 9") {
		t.Errorf("a bad key: %d %q %q", code, out, errs)
	}
	edit("user.go", `velox.EdgeTo[Post]("posts", userColID, 9)`, `velox.EdgeTo[Post]("posts", userColID, postColAuthorID)`)
	// A handle that names another entity than the field holds: the target comes
	// from the handle, so verify must compare it with the field.
	edit("user.go", "velox.Rel[User, Post]", "velox.Rel[User, User]")
	code, out, errs = runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 1 || !strings.Contains(errs, "User.posts: the field is a velox.Many[") || !strings.Contains(errs, "but the Rel handle names") || !strings.Contains(out, "with errors") {
		t.Errorf("a mismatched handle: %d %q %q", code, out, errs)
	}
	edit("user.go", "velox.Rel[User, User]", "velox.Rel[User, Post]")
	// A target whose metadata is broken: the relation names it, and says so.
	edit("post.go", "PrimaryKey: []int{postColID},", "PrimaryKey: []int{9},")
	code, out, errs = runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 1 || !strings.Contains(errs, "User.posts: its target") || !strings.Contains(errs, "the primary key names column 9") || !strings.Contains(out, "with errors") {
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
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if err := os.Remove(filepath.Join(dir, "veloxgen", "user.go")); err != nil { // User is gone: the program cannot compile
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
	dir := tempModule(t, map[string]string{"veloxschema/schema.go": genUserGroupSchema})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "veloxgen", "*.go"))
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
	dir := tempModule(t, map[string]string{"veloxschema/schema.go": genUserGroupSchema})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema"); code != 0 || out != "velox-gen verify: 3 entities, 0 with errors\n" {
		t.Fatalf("a good model: %d %q %q", code, out, errs)
	}
	p := filepath.Join(dir, "veloxgen", "user.go")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(velox\.EdgeThrough\[UserGroup, Group\]\("groups", \w+, \w+, )\w+(, \w+\))`)
	if !re.Match(b) {
		t.Fatalf("no EdgeThrough for groups in\n%s", b)
	}
	if err := os.WriteFile(p, re.ReplaceAll(b, []byte("${1}9${2}")), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema")
	if code != 1 || !strings.Contains(errs, "User.groups") || !strings.Contains(out, "with errors") {
		t.Errorf("a broken through relation: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
}

const dupFieldSchema = `package veloxschema

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
	dir := tempModule(t, map[string]string{"veloxschema/user.go": dupFieldSchema})
	// A builder misuse is reported first, by itself: the rules run on a
	// model that loaded.
	code, _, errs := runCLI(t, dir, "./veloxschema")
	if want := `velox-gen: veloxschema/user.go:14: schema User: field "n": DefaultFunc must be a func() int64`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("misuse: %d %q, want %q", code, errs, want)
	}
	writeFile(t, filepath.Join(dir, "veloxschema", "user.go"), strings.Replace(dupFieldSchema, "\t\tfield.Int64(\"n\").DefaultFunc(1),\n", "", 1))
	code, _, errs = runCLI(t, dir, "./veloxschema")
	if want := `veloxschema/user.go:13: schema User: field "name": declared twice`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("rule: %d %q, want %q", code, errs, want)
	}
	code, _, errs = runCLI(t, dir, "describe")
	if want := `veloxschema/user.go:13: schema User: field "name": declared twice`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("describe: %d %q, want %q", code, errs, want)
	}
	code, _, errs = runCLI(t, filepath.Join(dir, "veloxschema"), "-check", ".")
	if want := "velox-gen: user.go:13: schema User:"; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("in the schema directory: %d %q, want %q", code, errs, want)
	}
	if strings.Contains(errs, dir) {
		t.Errorf("an absolute path is printed: %q", errs)
	}
}

const posSchema = `package veloxschema

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
		tempModuleAt(t, dir, map[string]string{"veloxschema/user.go": posSchema})
		if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
			t.Fatalf("%s: %d %q", dir, code, errs)
		}
		files := map[string]string{}
		des, err := os.ReadDir(filepath.Join(dir, "veloxgen"))
		if err != nil {
			t.Fatal(err)
		}
		for _, de := range des {
			b, err := os.ReadFile(filepath.Join(dir, "veloxgen", de.Name()))
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
	const item = `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Item struct{ schema.Schema }

func (Item) Fields() []schema.Field { return []schema.Field{field.String("sku")} }
`
	dir := genModuleSerial(t, map[string]string{"veloxschema/item.go": item})
	gen := filepath.Join(dir, "veloxgen")
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	// Without Tag, and with another field of Item and of User:
	// item.go and user.go change, and tag.go goes.
	if err := os.Remove(filepath.Join(dir, "veloxschema", "tag.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "veloxschema", "item.go"), strings.Replace(item, `field.String("sku")`, `field.String("sku"), field.Int("count")`, 1))
	writeFile(t, filepath.Join(dir, "veloxschema", "user.go"), strings.Replace(genUserSchema, `field.String("name"),`, `field.String("name"), field.Int("age"),`, 1))
	before := treeFiles(t, gen)
	injected := errors.New("injected failure")
	genrun.RenameFile = func(from, to string) error {
		if filepath.Base(to) == "user.go" && strings.Contains(from, ".tmp-") {
			return injected
		}
		return os.Rename(from, to)
	}
	code, out, errs := runCLI(t, dir, "./veloxschema")
	genrun.RenameFile = os.Rename
	if code != 1 || out != "" || !strings.Contains(errs, "injected failure") {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if after := treeFiles(t, gen); !reflect.DeepEqual(after, before) {
		t.Errorf("the output changed:\n%v", slices.Sorted(maps.Keys(after)))
	}
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 || out != "veloxgen/item.go\nveloxgen/tag.go\nveloxgen/user.go\n" {
		t.Errorf("the next run: %d %q %q", code, out, errs)
	}
}

// TestGenerateSweepsLeftovers: generate first rids -out of what a killed
// write left, and says so; -check leaves it.
func TestGenerateSweepsLeftovers(t *testing.T) {
	dir := genModule(t, nil)
	gen := filepath.Join(dir, "veloxgen")
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(gen, ".user.go.old-velox-1"), "old")
	writeFile(t, filepath.Join(gen, ".velox.go.tmp-velox-2"), "new")
	if err := os.Rename(filepath.Join(gen, "tag.go"), filepath.Join(gen, ".tag.go.gone-velox-3")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-4", "x.go"), genrun.Header+"\n\npackage pb\n")
	writeFile(t, filepath.Join(gen, ".notes.old-velox"), "mine")
	before := treeFiles(t, gen)
	if code, _, _ := runCLI(t, dir, "-check", "./veloxschema"); code != 3 {
		t.Errorf("-check: %d", code)
	}
	if after := treeFiles(t, gen); !reflect.DeepEqual(after, before) {
		t.Errorf("-check changed gen:\n%v", slices.Sorted(maps.Keys(after)))
	}
	code, out, errs := runCLI(t, dir, "./veloxschema")
	wantErrs := "velox-gen: restored veloxgen/pb from veloxgen/.pb.gone-velox-4, which an interrupted run left\n" +
		"velox-gen: restored veloxgen/tag.go from veloxgen/.tag.go.gone-velox-3, which an interrupted run left\n" +
		"velox-gen: removed veloxgen/.user.go.old-velox-1, which an interrupted run left\n" +
		"velox-gen: removed veloxgen/.velox.go.tmp-velox-2, which an interrupted run left\n"
	if code != 0 || out != "veloxgen/pb/x.go\n" || errs != wantErrs {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if got := slices.Sorted(maps.Keys(treeFiles(t, gen))); !slices.Equal(got, []string{".notes.old-velox", "tag.go", "user.go", "velox.go"}) {
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
	gen := filepath.Join(dir, "veloxgen")
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(gen, ".user.go.old-velox-1"), "old")
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-2", "x.go"), genrun.Header+"\n\npackage pb\n")
	writeFile(t, filepath.Join(gen, ".notes.old-velox"), "mine")
	code, out, errs := runCLI(t, dir, "-check", "./veloxschema")
	want := "velox-gen: veloxgen/.pb.gone-velox-2 is left over from an interrupted run\n" +
		"velox-gen: veloxgen/.user.go.old-velox-1 is left over from an interrupted run\n" +
		"velox-gen: run go tool velox-gen ./veloxschema\n"
	if code != 3 || out != "" || errs != want {
		t.Errorf("%d %q %q", code, out, errs)
	}
}

// TestGenerateKeepsYoursInALeftover: a directory set aside whose name is
// taken again is removed entry by entry: a file of yours in it stays, with
// the directory, and generate names it.
func TestGenerateKeepsYoursInALeftover(t *testing.T) {
	dir := genModule(t, nil)
	gen := filepath.Join(dir, "veloxgen")
	if code, out, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	writeFile(t, filepath.Join(gen, "pb", "keep.txt"), "mine")
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-1", "x.go"), genrun.Header+"\n\npackage pb\n")
	writeFile(t, filepath.Join(gen, ".pb.gone-velox-1", "mine.txt"), "mine")
	code, out, errs := runCLI(t, dir, "./veloxschema")
	want := "velox-gen: kept veloxgen/.pb.gone-velox-1/mine.txt, which is not velox-gen's, in veloxgen/.pb.gone-velox-1, which an interrupted run left; move it\n"
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
	dir := tempModule(t, map[string]string{"veloxschema/s.go": `package veloxschema

import "github.com/syssam/velox/schema"

type base struct{ schema.Schema }

type Shop struct{ base }

type Order struct{ base }
`})
	code, out, errs := runCLI(t, dir, "describe", "./veloxschema")
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

const genInOrgUserSchema = `package veloxschema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

var OrgKey = scope.NewKey[int64]("org")

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

func (InOrg) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("org_id", org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error { return OrgKey.Match(ctx, r.Value("org_id")) },
	}
}

func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if org, ok := OrgKey.From(ctx); ok {
			return m.Fill("org_id", org)
		}
		return nil
	}, velox.OpInsert)}
}

type User struct{ schema.Schema }

func (User) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
`

// A tenancy mixin over package scope is bound as any mixin is: the entity's
// descriptor lists its Policy before the user's, and its Hooks, and names no
// scope; no scope_velox.go is written, and -check and verify, which builds
// the generated package, agree.
func TestGenerateTenancyMixin(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxschema/user.go": genInOrgUserSchema, "veloxschema/user_policy.go": schemaUserPolicy})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "scope_velox.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("scope_velox.go is written: %v", err)
	}
	user, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go"))
	for _, want := range []string{"Schema: veloxschema.User{},"} {
		if !strings.Contains(squeeze(string(user)), want) {
			t.Errorf("user.go lacks %s:\n%s", want, user)
		}
	}
	if strings.Contains(string(user), "Scope") {
		t.Errorf("user.go names a scope:\n%s", user)
	}
	if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// The scope_velox.go an older velox-gen wrote for schema.Scoped is velox-gen's
// file, so a run removes it, and the package builds without it.
func TestSweepRemovesScopeVelox(t *testing.T) {
	dir := genModule(t, nil)
	writeFile(t, filepath.Join(dir, "veloxgen", "scope_velox.go"), "// Code generated by velox-gen. DO NOT EDIT.\n\npackage veloxgen\n\nvar OrgKey = 1\n")
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if code != 0 || !strings.Contains(out, "veloxgen/scope_velox.go") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "scope_velox.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("scope_velox.go is still there: %v", err)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// A method of the user's on an entity, in a file of gen, works with or
// without a Policy: the entity is gen's own type either way.
func TestGenerateKeepsAUserMethodOnAnEntity(t *testing.T) {
	dir := genModule(t, nil)
	writeFile(t, filepath.Join(dir, "veloxgen", "extra.go"), "package veloxgen\n\n// Hello greets.\nfunc (u User) Hello() string { return \"hi \" + u.Name }\n")
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("plain: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "veloxschema", "user_policy.go"), schemaUserPolicy)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("with a Policy: %d %q", code, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// A helper that imports gen and that the schema's structure reaches
// (Fields calls it, or a blank import keeps it) makes an import cycle, since
// gen imports the schema. velox-gen names the chain and the way out, rather
// than go list's bare "import cycle not allowed".
func TestGenerateNamesTheChainOfACycle(t *testing.T) {
	for _, c := range []struct{ name, use string }{
		{"fields", "package veloxschema\n\nimport (\n\t\"github.com/syssam/velox/schema\"\n\t\"github.com/syssam/velox/schema/field\"\n\n\t\"example.com/app/helper\"\n)\n\n" +
			"// Note is a schema whose field is named by the helper.\ntype Note struct{ schema.Schema }\n\n" +
			"func (Note) Fields() []schema.Field { return []schema.Field{field.String(helper.Name())} }\n"},
		{"blank", "package veloxschema\n\nimport _ \"example.com/app/helper\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := genModule(t, map[string]string{"veloxschema/user_policy.go": schemaUserPolicy})
			if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
				t.Fatalf("generate: %d %q", code, errs)
			}
			writeFile(t, filepath.Join(dir, "helper", "h.go"), "package helper\n\nimport _ \"example.com/app/veloxgen\"\n\n// Name is a field's name.\nfunc Name() string { return \"title\" }\n")
			writeFile(t, filepath.Join(dir, "veloxschema", "use.go"), c.use)
			want := "velox-gen: example.com/app/veloxschema imports example.com/app/helper imports example.com/app/veloxgen: " +
				"the schema declares behaviour by column name and does not use veloxgen, which imports the schema; move the code that needs veloxgen out of the schema package\n"
			for _, args := range [][]string{{"./veloxschema"}, {"-check", "./veloxschema"}} {
				if code, out, errs := runCLI(t, dir, args...); code != 1 || out != "" || errs != want {
					t.Errorf("%v: %d %q %q, want %q", args, code, out, errs, want)
				}
			}
		})
	}
}

// A compile error in a schema file that declares a Policy is reported at
// the user's file and line, not at the loader's temporary program.
func TestGenerateCompileErrorInASchemaFile(t *testing.T) {
	dir := genModule(t, nil)
	writeFile(t, filepath.Join(dir, "veloxschema", "user_policy.go"), schemaUserPolicy+"\nfunc init() { _ = undefinedName }\n")
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if code != 1 || out != "" || !strings.Contains(errs, "veloxschema/user_policy.go:7:") || !strings.Contains(errs, "undefinedName") || strings.Contains(errs, ".velox-gen") || strings.Contains(errs, "overlay") {
		t.Errorf("%d %q %q, want the error at veloxschema/user_policy.go:7", code, out, errs)
	}
}

// genInOrg is a mixin, InOrg, whose Policy is policy, and which User lists.
func genInOrg(policy string) map[string]string {
	return map[string]string{
		"veloxschema/inorg.go": `package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id")} }

` + policy,
		"veloxschema/user.go": strings.Replace(genUserSchema, "type User struct{ schema.Schema }\n",
			"type User struct{ schema.Schema }\n\nfunc (User) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }\n", 1),
	}
}

// A mixin's Policy makes the entity's descriptor name its schema, whose
// Mixin method velox reads; a mixin method of the wrong shape, or generic, is refused
// at its line.
func TestGenerateMixinBehaviour(t *testing.T) {
	const policy = "func (InOrg) Policy() velox.Policy {\n\t_ = velox.FieldEQ(\"org_id\", 1)\n\treturn velox.Policy{}\n}\n"
	dir := genModule(t, genInOrg(policy))
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 || errs != "" {
		t.Fatalf("mixin: %d %q", code, errs)
	}
	if src, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go")); !strings.Contains(squeeze(string(src)), "Schema: veloxschema.User{},") {
		t.Errorf("gen does not bind the mixin:\n%s", src)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)

	writeFile(t, filepath.Join(dir, "veloxschema", "user_policy.go"), schemaUserPolicy)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 || errs != "" {
		t.Fatalf("with the schema's: %d %q", code, errs)
	}
	if src, _ := os.ReadFile(filepath.Join(dir, "veloxgen", "user.go")); !strings.Contains(squeeze(string(src)), "Schema: veloxschema.User{},") {
		t.Errorf("gen does not bind the mixin's before the schema's:\n%s", src)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)

	writeFile(t, filepath.Join(dir, "veloxschema", "inorg.go"), genInOrg("func (InOrg) Policy() *velox.Policy { return nil }\n")["veloxschema/inorg.go"])
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code == 0 || !strings.Contains(errs, "veloxschema/inorg.go:14: InOrg.Policy must take nothing and return velox.Policy;") {
		t.Errorf("a wrong shape: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "veloxschema", "inorg.go"), genInOrg("func (InOrg) Policy[E any]() velox.Policy { return velox.Policy{} }\n")["veloxschema/inorg.go"])
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code == 0 || !strings.Contains(errs, "veloxschema/inorg.go:14: InOrg.Policy: behaviour methods are not generic: func (InOrg) Policy() velox.Policy") {
		t.Errorf("generic: %d %q", code, errs)
	}
}

// squeeze is s with each run of spaces one space, so that a test does not
// depend on gofmt's alignment.
func squeeze(s string) string { return regexp.MustCompile(` +`).ReplaceAllString(s, " ") }

// velox-gen refuses the columns and relations that testdata/wrongcolumn's
// behaviour names wrongly, each at its line, with the entity and the
// method, warns of a column it cannot read, and writes nothing. Its output
// is testdata/wrongcolumn/output.golden.
func TestGenerateRefusesWrongColumns(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	files := map[string]string{}
	for _, name := range []string{"schema.go", "behaviour.go"} {
		_, src, _ := strings.Cut(readFile(t, filepath.Join("testdata", "wrongcolumn", name)), "\n")
		files["veloxschema/"+name] = strings.TrimPrefix(src, "\n")
	}
	dir := tempModule(t, files)
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if code == 0 || out != "" {
		t.Fatalf("%d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gen was written: %v", err)
	}
	compareGolden(t, filepath.Join("testdata", "wrongcolumn", "output.golden"), errs)
}

// The <entity>_velox.go an older velox-gen wrote holds what <entity>.go now
// does: a run writes user.go and removes user_velox.go, so the package does
// not declare User twice, and it builds (ADR 0042).
func TestSweepRemovesOldEntityFile(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	gen := filepath.Join(dir, "veloxgen")
	if err := os.Rename(filepath.Join(gen, "user.go"), filepath.Join(gen, "user_velox.go")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "./veloxschema")
	if code != 0 || out != "veloxgen/user.go\nveloxgen/user_velox.go\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(gen, "user_velox.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("user_velox.go is still there: %v", err)
	}
	if code, out, errs := runCLI(t, dir, "verify", "-out", "veloxgen", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
}

// schemaPrivacyPolicies are Policies written for veloxe, with package
// privacy's rule lists: User's returns velox.Policy, as nextapp's do, and
// Tag's returns privacy.Policy.
const schemaPrivacyPolicies = `package veloxschema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
)

func (User) Policy() velox.Policy {
	return privacy.Policy{Query: privacy.QueryPolicy{
		privacy.FilterFunc(func(_ context.Context, f privacy.Filter) error {
			f.Where(velox.FieldEQ("name", "ann"))
			return privacy.Skip
		}),
	}}
}

func (Tag) Policy() privacy.Policy {
	return privacy.Policy{Mutation: privacy.MutationPolicy{privacy.DenyMutationOperationRule(velox.OpDelete)}}
}
`

// A veloxe-shaped Policy is bound as any Policy: velox-gen accepts both
// result spellings, names the schema's value in each descriptor, and the
// generated package builds (verify).
func TestGeneratePrivacyPolicy(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxschema/policies.go": schemaPrivacyPolicies})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	for file, want := range map[string]string{"user.go": "Schema: veloxschema.User{},", "tag.go": "Schema: veloxschema.Tag{},"} {
		src, _ := os.ReadFile(filepath.Join(dir, "veloxgen", file))
		if !strings.Contains(squeeze(string(src)), want) {
			t.Errorf("%s lacks %q:\n%s", file, want, src)
		}
	}
	if code, out, errs := runCLI(t, dir, "-check", "./veloxschema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	if code, out, errs := runCLI(t, dir, "verify", "./veloxschema"); code != 0 {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}

	// veloxe's applications imported the generated package's privacy:
	// velox-gen writes it, and code that imports it builds.
	src, err := os.ReadFile(filepath.Join(dir, "veloxgen", "privacy", "privacy.go"))
	if err != nil || !strings.HasPrefix(string(src), genrun.Header) || !strings.Contains(string(src), "DecisionContext = privacy.DecisionContext") {
		t.Fatalf("veloxgen/privacy/privacy.go: %v\n%s", err, src)
	}
	writeFile(t, filepath.Join(dir, "service", "service.go"), `package service

import (
	"context"

	"example.com/app/veloxgen/privacy"
)

func Allowed(ctx context.Context) context.Context { return privacy.DecisionContext(ctx, privacy.Allow) }

var _ privacy.QueryRule = privacy.FilterFunc(nil)
`)
	cmd := exec.Command("go", "vet", "./service")
	cmd.Dir, cmd.Env = dir, offlineEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go vet ./service: %v\n%s", err, out)
	}
}

// A schema whose Policy is velox's own gets no privacy package: only an
// application written for veloxe imported one.
func TestGenerateNoPrivacyPackage(t *testing.T) {
	dir := genModule(t, map[string]string{"veloxschema/policies.go": `package veloxschema

import (
	"context"

	"github.com/syssam/velox"
)

func (User) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("name", "ann"), nil }}
}
`})
	if code, _, errs := runCLI(t, dir, "./veloxschema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "veloxgen", "privacy")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("veloxgen/privacy: %v, want none", err)
	}
}
