package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const genUserSchema = `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.String("email").Optional()}
}
`

const genTagSchema = `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type Tag struct{ schema.Schema }

func (Tag) Fields() []schema.Field { return []schema.Field{field.String("label")} }
`

// genModule is a module whose schema package declares User and Tag.
func genModule(t *testing.T, extra map[string]string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a module")
	}
	files := map[string]string{"schema/user.go": genUserSchema, "schema/tag.go": genTagSchema}
	for k, v := range extra {
		files[k] = v
	}
	return tempModule(t, files)
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
	code, out, errs := velox(t, dir, "./schema")
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
	if code, out, errs := velox(t, dir, "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("second run: %d %q %q", code, out, errs)
	}
	after := mtimes(t, filepath.Join(dir, "gen"))
	for name, mt := range before {
		if !after[name].Equal(mt) {
			t.Errorf("%s was rewritten", name)
		}
	}
	if code, out, errs := velox(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
}

func TestGenerateDefaults(t *testing.T) {
	dir := genModule(t, nil)
	// No arguments: the package is ./schema and the output ./gen.
	if code, _, errs := velox(t, dir); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	src, err := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go"))
	if err != nil || !strings.Contains(string(src), "package gen\n") {
		t.Fatalf("%v\n%s", err, src)
	}
	// -out and -package: the package name defaults to the base of -out.
	if code, out, errs := velox(t, dir, "-out", "model/store", "./schema"); code != 0 || !strings.Contains(out, "model/store/user_velox.go") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	src, _ = os.ReadFile(filepath.Join(dir, "model", "store", "user_velox.go"))
	if !strings.Contains(string(src), "package store\n") {
		t.Errorf("package is not the base of -out:\n%.200s", src)
	}
	if code, _, errs := velox(t, dir, "-out", "model/store", "-package", "persist", "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	src, _ = os.ReadFile(filepath.Join(dir, "model", "store", "user_velox.go"))
	if !strings.Contains(string(src), "package persist\n") {
		t.Errorf("-package ignored:\n%.200s", src)
	}
}

func TestCheckAndRewrite(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	userFile := filepath.Join(dir, "gen", "keep.go")
	writeFile(t, userFile, "package gen\n\nconst Keep = 1\n")

	writeFile(t, filepath.Join(dir, "schema", "user.go"), strings.Replace(genUserSchema, `field.String("name"),`, `field.String("name"), field.Int("age"),`, 1))
	before, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go"))
	code, out, errs := velox(t, dir, "-check", "./schema")
	if code != 1 || out != "" || !strings.Contains(errs, "velox-gen: gen/user_velox.go is out of date\n") ||
		!strings.Contains(errs, "velox-gen: run go tool velox-gen ./schema\n") || strings.Contains(errs, "tag_velox.go") {
		t.Fatalf("-check: %d %q %q", code, out, errs)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go")); string(after) != string(before) {
		t.Fatal("-check changed the file")
	}

	code, out, errs = velox(t, dir, "./schema")
	if code != 0 || !strings.Contains(out, "gen/user_velox.go\n") || strings.Contains(out, "tag_velox.go") {
		t.Fatalf("rewrite: %d %q %q", code, out, errs)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go")); !strings.Contains(string(after), "Age") {
		t.Errorf("not rewritten:\n%s", after)
	}
	if code, _, errs := velox(t, dir, "-check", "./schema"); code != 0 {
		t.Fatalf("-check after rewrite: %d %q", code, errs)
	}

	// A removed entity: -check names its file, the next run deletes it.
	if err := os.Remove(filepath.Join(dir, "schema", "tag.go")); err != nil {
		t.Fatal(err)
	}
	code, _, errs = velox(t, dir, "-check", "./schema")
	if code != 1 || !strings.Contains(errs, "velox-gen: gen/tag_velox.go is out of date") {
		t.Fatalf("-check after removal: %d %q", code, errs)
	}
	code, out, errs = velox(t, dir, "./schema")
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

func TestGenerateBindsUserMethods(t *testing.T) {
	dir := genModule(t, map[string]string{
		"gen/user_policy.go": "package gen\n\nimport \"github.com/syssam/velox2\"\n\nfunc (*User) Policy() velox.Policy[User] { return velox.Policy[User]{} }\n",
	})
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	user, _ := os.ReadFile(filepath.Join(dir, "gen", "user_velox.go"))
	if !strings.Contains(string(user), "velox.BindPolicy((*User)(nil).Policy())") {
		t.Errorf("user_velox.go does not bind the policy:\n%s", user)
	}
	if strings.Contains(string(user), "BindHooks") {
		t.Errorf("user_velox.go binds hooks the user did not declare")
	}
	tag, _ := os.ReadFile(filepath.Join(dir, "gen", "tag_velox.go"))
	if strings.Contains(string(tag), "BindPolicy") {
		t.Errorf("tag_velox.go binds a policy")
	}
}

// A Policy or Hooks method added after generation is not bound: -check says
// the entity's file is out of date, and verify reports the method the
// metadata leaves out.
func TestPolicyAddedAfterGenerationFailsCheckAndVerify(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	writeFile(t, filepath.Join(dir, "gen", "user_policy.go"), "package gen\n\nimport \"github.com/syssam/velox2\"\n\nfunc (*User) Policy() velox.Policy[User] { return velox.Policy[User]{} }\n"+
		"func (*User) Hooks() []velox.Hook[User] { return nil }\n")
	code, out, errs := velox(t, dir, "-check", "./schema")
	if code != 1 || out != "" || !strings.Contains(errs, "velox-gen: gen/user_velox.go is out of date\n") || strings.Contains(errs, "tag_velox.go") {
		t.Errorf("-check: %d %q %q", code, out, errs)
	}
	code, out, errs = velox(t, dir, "verify", "./schema")
	if code != 1 || !strings.Contains(out, "2 entities, 1 with errors") ||
		!strings.Contains(errs, "velox: gen.User: has a Policy method, but the generated metadata does not bind it; run go tool velox-gen") {
		t.Errorf("verify: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
	// Regenerating binds both, and the check passes.
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("regenerate: %d %q", code, errs)
	}
	if code, out, errs := velox(t, dir, "-check", "./schema"); code != 0 || out != "" || errs != "" {
		t.Errorf("-check after regenerating: %d %q %q", code, out, errs)
	}
}

// -check fails for a generated file that is missing, as for one that is stale.
func TestCheckFindsAMissingFile(t *testing.T) {
	dir := genModule(t, nil)
	if code, _, errs := velox(t, dir, "-check", "./schema"); code != 1 || !strings.Contains(errs, "velox-gen: gen/user_velox.go is out of date\n") {
		t.Errorf("-check before any generation: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("-check wrote files")
	}
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if err := os.Remove(filepath.Join(dir, "gen", "tag_velox.go")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := velox(t, dir, "-check", "./schema")
	if code != 1 || out != "" || !strings.Contains(errs, "velox-gen: gen/tag_velox.go is out of date\n") || strings.Contains(errs, "user_velox.go") {
		t.Errorf("-check with a missing file: %d %q %q", code, out, errs)
	}
}

func TestGenerateRefusesAForeignFile(t *testing.T) {
	dir := genModule(t, map[string]string{"gen/user_velox.go": "package gen\n\n// mine\n"})
	code, out, errs := velox(t, dir, "./schema")
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
	if code, _, errs := velox(t, dir, "-check", "./schema"); code != 1 || !strings.Contains(errs, "was not written by velox-gen") {
		t.Errorf("-check: %d %q", code, errs)
	}
}

func TestGenerateRefusesARedeclaredType(t *testing.T) {
	dir := genModule(t, map[string]string{"gen/mine.go": "package gen\n\ntype User struct{}\n"})
	code, _, errs := velox(t, dir, "./schema")
	if want := "velox-gen: gen/mine.go: declares type User, which velox-gen generates\n"; code != 1 || errs != want {
		t.Fatalf("%d %q, want %q", code, errs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "user_velox.go")); !os.IsNotExist(err) {
		t.Error("a file was written")
	}
}

// A Policy or Hooks method with a value receiver is an error, with a path
// relative to the working directory, and nothing is written.
func TestGenerateRefusesAValueReceiver(t *testing.T) {
	dir := genModule(t, map[string]string{
		"gen/user_policy.go": "package gen\n\nimport \"github.com/syssam/velox2\"\n\nfunc (User) Policy() velox.Policy[User] { return velox.Policy[User]{} }\n",
	})
	code, out, errs := velox(t, dir, "./schema")
	want := "velox-gen: gen/user_policy.go: User.Policy must have a pointer receiver (func (*User) Policy())\n"
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
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type Bad struct{ schema.Schema }

func (Bad) Fields() []schema.Field { return []schema.Field{field.String("velox_meta")} }
`})
	code, _, errs := velox(t, dir, "./schema")
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
		{[]string{"./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is a struct that embeds schema.Schema\n"},
		{[]string{"-out", "gen", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is a struct that embeds schema.Schema\n"},
		{[]string{"-check", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is a struct that embeds schema.Schema\n"},
		{[]string{"verify", "./plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is a struct that embeds schema.Schema\n"},
		{[]string{"example.com/app/plain"}, "velox-gen: no schemas in example.com/app/plain: a schema is a struct that embeds schema.Schema\n"},
	} {
		if code, out, errs := velox(t, dir, c.args...); code != 1 || out != "" || errs != c.want {
			t.Errorf("%v: %d %q %q, want %q", c.args, code, out, errs, c.want)
		}
	}
	// Patterns that name no package, several, or the old `./...` form.
	for _, args := range [][]string{{"./nothing"}, {"./..."}, {"-check", "./..."}, {"verify", "./..."}, {"./plain", "./other"}} {
		if code, out, errs := velox(t, dir, args...); code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: ") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	// The flags of the removed mode are unknown.
	for _, flag := range []string{"-ordered=a.B", "-number=a.B"} {
		if code, out, errs := velox(t, dir, flag, "./plain"); code != 2 || out != "" || !strings.Contains(errs, "flag provided but not defined") {
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
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 0 || out != "velox-gen verify: 2 entities, 0 with errors\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".velox-verify-*")); len(matches) != 0 {
		t.Errorf("the program was left behind: %v", matches)
	}
}

func TestVerifyGeneratedCountsJoinEntities(t *testing.T) {
	dir := tempModule(t, map[string]string{"schema/user.go": genUserGroupSchema})
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 0 || out != "velox-gen verify: 3 entities, 0 with errors\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
}

func TestVerifyGeneratedNeedsTheOutputPackage(t *testing.T) {
	dir := genModule(t, nil)
	code, _, errs := velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || errs != "velox-gen verify: gen is not a directory; run velox-gen to generate it first\n" {
		t.Errorf("%d %q", code, errs)
	}
}

// TestRelativeDirAsTheBinaryRuns runs the command as main does, with dir "."
// and the working directory in the module, and a relative -out.
func TestRelativeDirAsTheBinaryRuns(t *testing.T) {
	dir := genModule(t, nil)
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
	const want = "velox-gen: no schemas in example.com/app/schema: a schema is a struct that embeds schema.Schema\n"
	// Named, and by default.
	for _, args := range [][]string{{"./schema"}, {}, {"-check"}, {"verify"}} {
		if code, out, errs := velox(t, dir, args...); code != 1 || out != "" || errs != want {
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
		code, out, errs := velox(t, dir, args...)
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
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
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
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
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
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	code, out, errs := velox(t, dir, "verify", "-out", "gen", "./schema")
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
	edit("user_velox.go", `velox.EdgeTo[Post]("Posts", 0, 2)`, `velox.EdgeTo[Post]("Posts", 0, 9)`)
	code, out, errs = velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || out != "velox-gen verify: 2 entities, 1 with errors\n" || !strings.Contains(errs, "User.Posts: its key names column 9") {
		t.Errorf("a bad key: %d %q %q", code, out, errs)
	}
	edit("user_velox.go", `velox.EdgeTo[Post]("Posts", 0, 9)`, `velox.EdgeTo[Post]("Posts", 0, 2)`)
	// A handle that names another entity than the field holds: the target comes
	// from the handle, so verify must compare it with the field.
	edit("user_velox.go", "velox.Rel[User, Post]", "velox.Rel[User, User]")
	code, out, errs = velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "User.Posts: the field is a velox.Many[") || !strings.Contains(errs, "but the Rel handle names") || !strings.Contains(out, "with errors") {
		t.Errorf("a mismatched handle: %d %q %q", code, out, errs)
	}
	edit("user_velox.go", "velox.Rel[User, User]", "velox.Rel[User, Post]")
	// A target whose metadata is broken: the relation names it, and says so.
	edit("post_velox.go", "Tenant:     -1,", "Tenant:     9,")
	code, out, errs = velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "User.Posts: its target") || !strings.Contains(errs, "the tenant names column 9") || !strings.Contains(out, "with errors") {
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
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if err := os.Remove(filepath.Join(dir, "gen", "user_velox.go")); err != nil { // User is gone: the program cannot compile
		t.Fatal(err)
	}
	if code, _, errs := velox(t, dir, "verify"); code != 1 || !strings.Contains(errs, "User") {
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
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
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
	if code, _, errs := velox(t, dir, "./schema"); code != 0 {
		t.Fatalf("generate: %d %q", code, errs)
	}
	if code, out, errs := velox(t, dir, "verify", "-out", "gen", "./schema"); code != 0 || out != "velox-gen verify: 3 entities, 0 with errors\n" {
		t.Fatalf("a good model: %d %q %q", code, out, errs)
	}
	p := filepath.Join(dir, "gen", "user_velox.go")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(velox\.EdgeThrough\[UserGroup, Group\]\("Groups", \d+, \d+, )\d+(, \d+\))`)
	if !re.Match(b) {
		t.Fatalf("no EdgeThrough for Groups in\n%s", b)
	}
	if err := os.WriteFile(p, re.ReplaceAll(b, []byte("${1}9${2}")), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := velox(t, dir, "verify", "-out", "gen", "./schema")
	if code != 1 || !strings.Contains(errs, "User.Groups") || !strings.Contains(out, "with errors") {
		t.Errorf("a broken through relation: %d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
}

const dupFieldSchema = `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
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
	code, _, errs := velox(t, dir, "./schema")
	if want := `velox-gen: schema/user.go:14: schema User: field "n": DefaultFunc must be a func() int64`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("misuse: %d %q, want %q", code, errs, want)
	}
	writeFile(t, filepath.Join(dir, "schema", "user.go"), strings.Replace(dupFieldSchema, "\t\tfield.Int64(\"n\").DefaultFunc(1),\n", "", 1))
	code, _, errs = velox(t, dir, "./schema")
	if want := `schema/user.go:13: schema User: field "name": declared twice`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("rule: %d %q, want %q", code, errs, want)
	}
	code, _, errs = velox(t, dir, "describe")
	if want := `schema/user.go:13: schema User: field "name": declared twice`; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("describe: %d %q, want %q", code, errs, want)
	}
	code, _, errs = velox(t, filepath.Join(dir, "schema"), "-check", ".")
	if want := "velox-gen: user.go:13: schema User:"; code != 1 || !strings.Contains(errs, want) {
		t.Fatalf("in the schema directory: %d %q, want %q", code, errs, want)
	}
	if strings.Contains(errs, dir) {
		t.Errorf("an absolute path is printed: %q", errs)
	}
}

const posSchema = `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
	"github.com/syssam/velox2/schema/mixin"
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
// byte-identical output, velox.go's model hash and describe -json included.
func TestOutputIgnoresPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(base, "a"), filepath.Join(base, "deeper", "x", "y", "b")}
	var gens []map[string]string
	var described []string
	for _, dir := range dirs {
		tempModuleAt(t, dir, map[string]string{"schema/user.go": posSchema})
		if code, _, errs := velox(t, dir, "./schema"); code != 0 {
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
		code, out, errs := velox(t, dir, "describe", "-json")
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
