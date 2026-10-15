package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedCodeRunsNothingAtStart enforces design §3 rule 4: a generated
// file has no init function and no package-level call; its values are
// literals and zero values.
func TestGeneratedCodeRunsNothingAtStart(t *testing.T) {
	files, _ := filepath.Glob("testdata/testmodel/*_velox.go")
	if len(files) == 0 {
		t.Fatal("no generated files")
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

// TestStaleFieldTypeDoesNotCompile is Review Focus 2: changing a field's type
// without regenerating fails the build, naming the field.
func TestStaleFieldTypeDoesNotCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a package")
	}
	// The whole testmodel package, its generated files as committed, with
	// Item.Qty changed from int64 to float64 and nothing regenerated.
	paths, err := filepath.Glob("testdata/testmodel/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files["m/"+filepath.Base(p)] = string(src)
	}
	changed := strings.Replace(files["m/item.go"], "Qty       int64", "Qty       float64", 1)
	if changed == files["m/item.go"] {
		t.Fatal("item.go no longer declares Qty as int64; update the test")
	}
	files["m/item.go"] = changed
	dir := t.TempDir()
	if err := tempmodWrite(dir, files); err != nil {
		t.Fatal(err)
	}
	out, err := goIn(dir, "build", "./m")
	if err == nil || !strings.Contains(out, "x.Qty") {
		t.Errorf("a stale Qty type built (err %v):\n%s", err, out)
	}
}

// tempmodWrite creates a module example.com/tmp in dir that requires velox2
// from this repository (by replace), with the given files (path to content),
// and resolves its requirements from the module cache only. It mirrors the
// root module's internal/tempmod, which velox-gen, a separate module, cannot
// import.
func tempmodWrite(dir string, files map[string]string) error {
	repo, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		return err
	}
	all := map[string]string{
		"go.mod": fmt.Sprintf("module example.com/tmp\n\ngo 1.27.0\n\nrequire github.com/syssam/velox2 v0.0.0\n\nreplace github.com/syssam/velox2 => %s\n", repo),
		"go.sum": string(sum),
	}
	for name, content := range files {
		all[name] = content
	}
	for name, content := range all {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	_, err = goIn(dir, "mod", "tidy")
	return err
}

// goIn runs the go command in dir, offline, and returns its combined output.
// It runs "go" from PATH: go test puts its own GOROOT/bin first on a child's
// PATH, so this is the toolchain that runs the test.
func goIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("go %v: %w\n%s", args, err, out.String())
	}
	return out.String(), err
}
