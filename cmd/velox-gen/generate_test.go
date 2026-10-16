package main

import (
	"os"
	"path/filepath"
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
	if code != 1 || !strings.Contains(errs, "type User") || !strings.HasPrefix(errs, "velox-gen: ") {
		t.Fatalf("%d %q", code, errs)
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

func TestOutNeedsASchemaPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	for _, args := range [][]string{
		{"-out", "gen", "./billing"},
		{"-out", "gen", "./..."},
		{"-out", "gen", "./nothing"},
		{"-out", "gen"},
	} {
		code, out, errs := velox(t, dir, args...)
		if code != 1 || out != "" || !strings.Contains(errs, "-out needs a schema package") {
			t.Errorf("%v: %d %q %q", args, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "billing", "invoice_velox.go")); !os.IsNotExist(err) {
		t.Error("the old mode ran")
	}
}

func TestOldModeStillRunsBesideASchemaPackage(t *testing.T) {
	dir := genModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	// ./... names several packages, so the old scanner runs; it skips the
	// schema package, which has no struct with a Schema method.
	code, out, errs := velox(t, dir, "./...")
	if code != 0 || out != "billing/invoice_velox.go\n" {
		t.Fatalf("%d %q %q", code, out, errs)
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
	dir := tempModule(t, map[string]string{
		"schema/user.go": `package schema

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
`})
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

func TestVerifyOutNeedsASchemaPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	if code, _, errs := velox(t, dir, "verify", "-out", "gen", "./billing"); code != 1 || !strings.Contains(errs, "-out needs a schema package") {
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

func TestOrderedAndNumberAreOldModeOnly(t *testing.T) {
	dir := genModule(t, nil)
	for _, flag := range []string{"-ordered=a.B", "-number=a.B"} {
		code, out, errs := velox(t, dir, flag, "./schema")
		if code != 1 || out != "" || errs != "velox-gen: -ordered and -number apply to the old mode only\n" {
			t.Errorf("%s: %d %q %q", flag, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Error("files were written")
	}
}
