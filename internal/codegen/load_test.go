package codegen

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox2/internal/tempmod"
)

// repoRoot returns the directory of the velox2 module, found by walking up
// from the working directory to the go.mod that declares it.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if f, err := os.Open(filepath.Join(dir, "go.mod")); err == nil {
			sc := bufio.NewScanner(f)
			found := sc.Scan() && strings.TrimSpace(sc.Text()) == "module github.com/syssam/velox2"
			_ = f.Close()
			if found {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod for github.com/syssam/velox2 above the working directory")
		}
		dir = parent
	}
}

const userSchema = `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
`

func module(t *testing.T, files map[string]string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := t.TempDir()
	if err := tempmod.Write(dir, repoRoot(t), files); err != nil {
		t.Fatal(err)
	}
	return dir
}

func offline() []string {
	return append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
}

func TestLoad(t *testing.T) {
	dir := module(t, map[string]string{"schema/user.go": userSchema})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	if m.Package != "example.com/tmp/schema" || len(m.Schemas) != 1 || m.Schemas[0].Fields[0].Name != "name" {
		t.Fatalf("%+v", m)
	}
}

func TestLoadCompileError(t *testing.T) {
	dir := module(t, map[string]string{"schema/user.go": userSchema + "\nfunc broken() int { return \"\" }\n"})
	_, err := Load(dir, "./schema", offline())
	if err == nil || !strings.Contains(err.Error(), "loading example.com/tmp/schema") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadPanicNamesSchema(t *testing.T) {
	src := strings.Replace(userSchema, `return []schema.Field{field.String("name")}`, `_ = field.String; panic("boom")`, 1)
	dir := module(t, map[string]string{"schema/user.go": src})
	_, err := Load(dir, "./schema", offline())
	if err == nil || !strings.Contains(err.Error(), "schema User: Fields panicked: boom") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadNoSchemas(t *testing.T) {
	dir := module(t, map[string]string{"schema/doc.go": "package schema\n"})
	_, err := Load(dir, "./schema", offline())
	if err == nil || !strings.Contains(err.Error(), "no schemas in example.com/tmp/schema") {
		t.Fatalf("got %v", err)
	}
}

func TestInspect(t *testing.T) {
	dir := module(t, map[string]string{
		"schema/user.go":  userSchema,
		"plain/doc.go":    "package plain\n",
		"plain/sub/b.go":  "package sub\n",
		"unexp/schema.go": "package unexp\n\nimport \"github.com/syssam/velox2/schema\"\n\ntype user struct{ schema.Schema }\n",
		"empty/notes.txt": "no Go here\n",
		"mixed/a.go":      "package a\n",
		"mixed/b.go":      "package b\n",
	})
	path, schemas, err := Inspect(dir, "./schema", offline())
	if err != nil || path != "example.com/tmp/schema" || !slices.Equal(schemas, []string{"User"}) {
		t.Errorf("schema: %q %v %v", path, schemas, err)
	}
	path, schemas, err = Inspect(dir, "./plain", offline())
	if err != nil || path != "example.com/tmp/plain" || len(schemas) != 0 {
		t.Errorf("plain: %q %v %v", path, schemas, err)
	}
	// Not a package.
	for _, pattern := range []string{"./nothing", "./empty", "./...", "example.com/tmp/nothing"} {
		if _, _, err := Inspect(dir, pattern, offline()); err == nil {
			t.Errorf("%s: no error", pattern)
		}
	}
	// Another failure of go list is shown as go wrote it.
	if _, _, err := Inspect(dir, "./mixed", offline()); err == nil || !strings.Contains(err.Error(), "found packages a (a.go) and b (b.go)") {
		t.Errorf("mixed: %v", err)
	}
	// A package that cannot be read says why.
	if _, _, err := Inspect(dir, "./unexp", offline()); err == nil || !strings.Contains(err.Error(), "unexported") {
		t.Errorf("unexp: %v", err)
	}
}

func TestLoadLeavesNoDirectory(t *testing.T) {
	dir := module(t, map[string]string{"schema/user.go": userSchema + "\nfunc broken() int { return \"\" }\n"})
	_, _ = Load(dir, "./schema", offline())
	left, _ := filepath.Glob(filepath.Join(dir, ".velox-gen-*"))
	if len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func TestLoadErrorsHaveNoExitStatus(t *testing.T) {
	panics := strings.Replace(userSchema, `return []schema.Field{field.String("name")}`, `_ = field.String; panic("boom")`, 1)
	initPanics := userSchema + "\nvar _ = func() int { panic(\"init boom\") }()\n"
	for name, src := range map[string]string{"Fields panics": panics, "init panics": initPanics} {
		dir := module(t, map[string]string{"schema/user.go": src})
		_, err := Load(dir, "./schema", offline())
		if err == nil || strings.Contains(err.Error(), "exit status") || !strings.Contains(err.Error(), "boom") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestLoadReturnsModelErrors(t *testing.T) {
	src := strings.Replace(userSchema, `field.String("name")}`, "field.String(\"name\"),\n\t\tfield.Int64(\"n\").DefaultFunc(1),\n\t\tfield.Enum(\"e\").NamedValues(\"X\"),\n\t}", 1)
	dir := module(t, map[string]string{"schema/user.go": src})
	_, err := Load(dir, "./schema", offline())
	var errs []*Error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if ge, ok := e.(*Error); ok {
				errs = append(errs, ge)
			}
		}
	}
	if len(errs) != 2 {
		t.Fatalf("got %v", err)
	}
	for i, line := range []int{11, 12} {
		e := errs[i]
		if want := filepath.Join(dir, "schema", "user.go") + ":" + strconv.Itoa(line); !samePos(e.Pos, want) || e.Schema != "User" {
			t.Errorf("error %d: %+v, want pos %s", i, e, want)
		}
	}
	if got := errs[0].Error(); !strings.HasSuffix(got, `user.go:11: schema User: field "n": DefaultFunc must be a func() int64`) {
		t.Errorf("text %q", got)
	}
}

// samePos reports whether two file:line positions name one line of one file,
// whatever symbolic links the paths go through.
func samePos(a, b string) bool {
	resolve := func(p string) string {
		i := strings.LastIndexByte(p, ':')
		if i < 0 {
			return p
		}
		if r, err := filepath.EvalSymlinks(p[:i]); err == nil {
			return r + p[i:]
		}
		return p
	}
	return resolve(a) == resolve(b)
}
