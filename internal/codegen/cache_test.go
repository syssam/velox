package codegen

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFiles writes files, by name, into dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCacheParsesOnce: the passes that read one directory through one Cache
// read and parse each file once. A file changed after the first pass is not
// read again: the later passes see what the first one did.
func TestCacheParsesOnce(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"user.go": `// Package schema is the schema.
package schema

import "github.com/syssam/velox"

type User struct{}

func (User) Policy() velox.Policy { return velox.Policy{} }
`,
	})
	c := NewCache()
	bound, err := c.SchemaMethods(dir, []string{"User"})
	if err != nil || !bound["User"].Policy {
		t.Fatalf("SchemaMethods: %v %v", bound, err)
	}
	// A file that no longer parses, and that has no package comment: the
	// Cache still holds what it read.
	writeFiles(t, dir, map[string]string{"user.go": "package schema\n\nfunc broken( {\n"})
	if err := c.CheckUserFiles(dir, nil); err != nil {
		t.Errorf("CheckUserFiles: %v", err)
	}
	if doc, err := c.HasPackageDoc(dir); err != nil || !doc {
		t.Errorf("HasPackageDoc: %v %v", doc, err)
	}
	if err := c.userFiles(dir, parser.ImportsOnly, func(*token.FileSet, string, *ast.File) error { return nil }); err != nil {
		t.Errorf("ImportsOnly: %v", err)
	}
	f := c.files[filepath.Join(dir, "user.go")]
	if f == nil || !f.parsed || f.fullErr != nil || len(f.narrow) != 0 {
		t.Errorf("the file is not parsed once, whole: %+v", f)
	}
	// A new Cache reads the file as it is now.
	if _, err := NewCache().HasPackageDoc(dir); err != nil {
		t.Errorf("HasPackageDoc of the package clause: %v", err)
	}
	if err := NewCache().CheckUserFiles(dir, nil); err == nil {
		t.Error("CheckUserFiles of a file that does not parse: no error")
	}
}

// TestCacheNarrowModes: a file that parses as far as its imports, but not
// whole, is read by a narrower mode as go/parser reads it in that mode, and
// a mode that parses it whole fails with go/parser's error, whichever order
// the passes ask in.
func TestCacheNarrowModes(t *testing.T) {
	dir := t.TempDir()
	src := "// Package p has a doc.\npackage p\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n\nfunc broken( {\n"
	writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.27\n", "p.go": src})
	_, want := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, "p.go"), src, parser.SkipObjectResolution)
	if want == nil {
		t.Fatal("the source parses")
	}
	for _, order := range [][]string{{"doc", "imports", "whole"}, {"whole", "doc", "imports"}} {
		c := NewCache()
		for _, pass := range order {
			switch pass {
			case "doc":
				if doc, err := c.HasPackageDoc(dir); err != nil || !doc {
					t.Errorf("%v: HasPackageDoc: %v %v", order, doc, err)
				}
			case "imports":
				var imports int
				err := c.userFiles(dir, parser.ImportsOnly, func(_ *token.FileSet, _ string, f *ast.File) error {
					imports = len(f.Imports)
					return nil
				})
				if err != nil || imports != 1 {
					t.Errorf("%v: ImportsOnly: %d %v", order, imports, err)
				}
			case "whole":
				if err := c.CheckUserFiles(dir, nil); err == nil || err.Error() != want.Error() {
					t.Errorf("%v: CheckUserFiles: %v, want %v", order, err, want)
				}
			}
		}
	}
}

// TestCacheGoCommands: go list and go env run once per directory,
// environment and arguments; another go command runs each time.
func TestCacheGoCommands(t *testing.T) {
	dir := t.TempDir()
	c := NewCache()
	env := offline()
	for range 3 {
		if _, _, err := c.run(dir, env, "env", "GOMOD"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := c.run(dir, env, "env", "GOOS"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.run(t.TempDir(), env, "env", "GOMOD"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := c.run(dir, env, "version"); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(c.ran, "|"); got != "env GOMOD|env GOOS|env GOMOD|version|version" {
		t.Errorf("ran %s", got)
	}
	// A command ctx stopped is not kept.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c = NewCache()
	if _, _, err := c.runContext(ctx, dir, env, "env", "GOMOD"); err == nil {
		t.Fatal("a stopped command: no error")
	}
	if _, _, err := c.run(dir, env, "env", "GOMOD"); err != nil {
		t.Errorf("after a stopped command: %v", err)
	}
	if len(c.ran) != 2 {
		t.Errorf("ran %q", c.ran)
	}
}

// TestCacheListOnce: the passes of a run list the schema package once, and
// a module directory comes from that list.
func TestCacheListOnce(t *testing.T) {
	dir := module(t, map[string]string{"schema/user.go": userSchema})
	c := NewCache()
	ctx := context.Background()
	if _, _, _, err := c.InspectContext(ctx, dir, "./schema", offline()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Behaviour(dir, "./schema", "example.com/tmp/gen", offline(), []string{"User"}); err != nil {
		t.Fatal(err)
	}
	lists := 0
	for _, r := range c.ran {
		if strings.HasPrefix(r, "list "+listFields+" ") {
			lists++
		}
	}
	if lists != 1 {
		t.Errorf("listed the schema package %d times: %q", lists, c.ran)
	}
	if mod, ok := c.ModuleDir(dir, "./schema", offline()); !ok || mod == "" {
		t.Errorf("ModuleDir: %q %v", mod, ok)
	}
	if _, ok := c.ModuleDir(dir, "./other", offline()); ok {
		t.Error("ModuleDir of a package not listed")
	}
}

// TestMixinFindBatched: the packages a mixin's file imports, which
// velox-gen looks up to find the one an embedded type names, are found by
// one go list, and only the one named is parsed whole.
func TestMixinFindBatched(t *testing.T) {
	g, dir := mixinModule(t)
	src := strings.Replace(mixinTypes, "\t\"time\"\n", "\t\"context\"\n\t\"fmt\"\n\t\"time\"\n\n\t\"github.com/syssam/velox\"\n", 1) +
		"\nvar _ context.Context\nvar _ = fmt.Sprint\nvar _ velox.Dialect\n" +
		"type D struct{ mixin.Schema }\n\nfunc (D) Policy() velox.Policy { return velox.Policy{} }\n"
	src = strings.Replace(src, "type A struct{ mixin.Schema }", "type A struct{ D }", 1)
	writeFiles(t, filepath.Join(dir, "schema"), map[string]string{"mixins.go": src})
	c := NewCache()
	if _, err := c.MixinBehaviour(dir, "./schema", g, offline()); err != nil {
		t.Fatal(err)
	}
	var finds []string
	for _, r := range c.ran {
		if strings.HasPrefix(r, "list -e -find ") {
			finds = append(finds, r)
		}
	}
	if len(finds) != 1 {
		t.Errorf("finds: %q", finds)
	}
	for path, f := range c.files {
		if strings.Contains(path, string(filepath.Separator)+"fmt"+string(filepath.Separator)) && f.parsed {
			t.Errorf("parsed %s whole", path)
		}
	}
}

// TestFormatAllFirstError: formatAll returns the error of the first index
// that fails, whichever call fails first in time.
func TestFormatAllFirstError(t *testing.T) {
	const n = 64
	for range 20 {
		called := make([]bool, n)
		err := formatAll(n, func(i int) error {
			called[i] = true
			switch i {
			case 5:
				time.Sleep(2 * time.Millisecond)
				return fmt.Errorf("file %d", i)
			case 40, 63:
				return fmt.Errorf("file %d", i)
			}
			return nil
		})
		if err == nil || err.Error() != "file 5" {
			t.Fatalf("got %v, want file 5", err)
		}
		for i, ok := range called {
			if !ok {
				t.Fatalf("index %d not formatted", i)
			}
		}
	}
	if err := formatAll(0, func(int) error { return errors.New("called") }); err != nil {
		t.Errorf("no files: %v", err)
	}
}
