package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// offlineEnv is the environment of go commands in tests: the module cache
// already holds the library's dependencies (any go build of the library
// fills it), and nothing is downloaded.
func offlineEnv() []string {
	return append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local")
}

// tempModule writes files into a new module, example.com/app, that uses this
// checkout's library, and returns its directory.
func tempModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lib, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(lib, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/app\n\ngo 1.27.0\n\nrequire github.com/syssam/velox2 v0.0.0\n\nreplace github.com/syssam/velox2 => " + lib + "\n"
	files["go.sum"] = string(sum)
	for name, src := range files {
		writeFile(t, filepath.Join(dir, name), src)
	}
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir, cmd.Env = dir, offlineEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	return dir
}

func writeFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// velox runs the command in dir.
func velox(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := run(args, dir, offlineEnv(), &out, &errs)
	return code, out.String(), errs.String()
}

// mustGenerate runs velox-gen over dir to set a test up, and requires it to
// succeed: a test that starts from a failed generation tests nothing.
func mustGenerate(t *testing.T, dir string) {
	t.Helper()
	if code, out, errs := velox(t, dir, "./..."); code != 0 {
		t.Fatalf("generate: %d %q %q", code, out, errs)
	}
}

const invoiceSrc = `package billing

import (
	"time"

	"github.com/syssam/velox2"
)

type Invoice struct {
	ID        int64
	Number    string
	CreatedAt time.Time
}

func (x *Invoice) Schema(s *velox.Schema) { s.Table("invoices") }
`

func TestRunWritesThenNothingChanges(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	code, out, errs := velox(t, dir, "./...")
	if code != 0 || out != "billing/invoice_velox.go\n" {
		t.Fatalf("first run: %d %q %q", code, out, errs)
	}
	if !isGenerated(filepath.Join(dir, "billing/invoice_velox.go")) {
		t.Error("the file lacks velox-gen's header")
	}
	if code, out, errs := velox(t, dir, "./..."); code != 0 || out != "" {
		t.Errorf("second run: %d %q %q", code, out, errs)
	}
	if code, _, errs := velox(t, dir, "-check", "./..."); code != 0 {
		t.Errorf("-check: %d %q", code, errs)
	}
}

func TestCheckFindsAStaleFile(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	mustGenerate(t, dir)
	gen := filepath.Join(dir, "billing/invoice_velox.go")
	before, _ := os.ReadFile(gen)
	writeFile(t, filepath.Join(dir, "billing/invoice.go"), strings.Replace(invoiceSrc, "Number    string", "Number    string\n\tNote      string", 1))
	code, _, errs := velox(t, dir, "-check", "./...")
	if code != 1 || !strings.Contains(errs, "velox-gen: billing/invoice_velox.go is out of date") {
		t.Errorf("-check: %d %q", code, errs)
	}
	if after, _ := os.ReadFile(gen); !bytes.Equal(before, after) {
		t.Error("-check changed the file")
	}
}

// TestRegeneratesAfterARename is Review Focus 1: the old generated file names
// a field that no longer exists and does not compile.
func TestRegeneratesAfterARename(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	mustGenerate(t, dir)
	stale, err := os.ReadFile(filepath.Join(dir, "billing/invoice_velox.go"))
	if err != nil || !strings.Contains(string(stale), "return &x.Number") {
		t.Fatalf("no stale file to regenerate (err %v):\n%s", err, stale)
	}
	writeFile(t, filepath.Join(dir, "billing/invoice.go"), strings.ReplaceAll(invoiceSrc, "Number ", "Code   "))
	if code, out, errs := velox(t, dir, "./..."); code != 0 || out != "billing/invoice_velox.go\n" {
		t.Fatalf("regenerate: %d %q %q", code, out, errs)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "billing/invoice_velox.go"))
	if !strings.Contains(string(src), "return &x.Code") || strings.Contains(string(src), "&x.Number") || strings.Contains(string(src), `"Number"`) {
		t.Errorf("not regenerated:\n%s", src)
	}
}

func TestRemovesTheFileOfAGoneEntity(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	mustGenerate(t, dir)
	writeFile(t, filepath.Join(dir, "billing/invoice.go"), strings.Replace(invoiceSrc, "func (x *Invoice) Schema(s *velox.Schema) { s.Table(\"invoices\") }\n", "var _ = velox.ErrNotFound\n", 1))
	code, out, errs := velox(t, dir, "./...")
	if code != 0 || out != "billing/invoice_velox.go\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "billing/invoice_velox.go")); !os.IsNotExist(err) {
		t.Errorf("the file of a gone entity is still there: %v", err)
	}
}

// TestLeavesTheNewModesFilesAlone: until the old mode is removed (schema plan
// 4), a module can hold both, and a run over ./... must neither report nor
// delete the files the new mode wrote.
func TestLeavesTheNewModesFilesAlone(t *testing.T) {
	newFile := "// Code generated by velox-gen. DO NOT EDIT.\n\npackage gen\n\nimport \"github.com/syssam/velox2\"\n\nconst _ = velox.GeneratedCodeIsVersion3\n"
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc, "gen/user_velox.go": newFile})
	mustGenerate(t, dir)
	if code, _, errs := velox(t, dir, "-check", "./..."); code != 0 {
		t.Errorf("-check: %d %q", code, errs)
	}
	if code, out, errs := velox(t, dir, "./..."); code != 0 || out != "" {
		t.Errorf("run: %d %q %q", code, out, errs)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "gen/user_velox.go")); err != nil || string(got) != newFile {
		t.Errorf("the new mode's file changed: %v\n%s", err, got)
	}
}

func TestProblemsWriteNothing(t *testing.T) {
	dir := tempModule(t, map[string]string{
		"billing/invoice.go": invoiceSrc,
		"billing/bad.go":     "package billing\n\nimport \"github.com/syssam/velox2\"\n\ntype Base struct{ A int64 }\n\ntype Bad struct {\n\t*Base\n\tID int64\n}\n\nfunc (x *Bad) Schema(s *velox.Schema) {}\n",
	})
	code, _, errs := velox(t, dir, "./...")
	if code != 1 || !strings.Contains(errs, "Bad: embedded pointer Base is not supported") {
		t.Errorf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "billing/invoice_velox.go")); !os.IsNotExist(err) {
		t.Error("a file was written although the run had a problem")
	}
}

func TestRefusesToOverwriteAFileItDidNotWrite(t *testing.T) {
	dir := tempModule(t, map[string]string{
		"billing/invoice.go":       invoiceSrc,
		"billing/invoice_velox.go": "package billing\n",
	})
	if code, _, errs := velox(t, dir, "./..."); code != 1 || !strings.Contains(errs, "exists and velox-gen did not write it") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestPatternMatchingNothing(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	if code, _, errs := velox(t, dir, "./nothing"); code != 1 || !strings.HasPrefix(errs, "velox-gen: ") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestDescribeCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"schema/user.go": `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }
`})
	code, out, errs := velox(t, dir, "describe", "-json", "./schema")
	if code != 0 || !strings.Contains(out, `"name": "User"`) || !strings.Contains(out, `"table": "users"`) {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	code, out, errs = velox(t, dir, "describe")
	if code != 0 || !strings.HasPrefix(out, "User (users)\n") || !strings.Contains(out, "name") {
		t.Fatalf("default package: %d %q %q", code, out, errs)
	}
	code, _, errs = velox(t, dir, "describe", "./nothing")
	if code != 1 || !strings.HasPrefix(errs, "velox-gen: ") || strings.Contains(errs, "velox-gen: velox-gen: ") {
		t.Fatalf("%d %q", code, errs)
	}
}
